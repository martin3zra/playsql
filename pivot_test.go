package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

type pUser struct {
	playsql.Model
	ID    int64    `db:"id" play:"pk,incrementing"`
	Name  string   `db:"name" play:"fillable"`
	Roles []*pRole `play:"belongsToMany,pivot=role_user,foreignPivotKey=user_id,relatedPivotKey=role_id"`
	Pets  []*pRole `play:"hasMany,foreignKey=user_id"` // non-pivot, for the error path
}

func (pUser) TableName() string { return "p_users" }

type pRole struct {
	playsql.Model
	ID   int64  `db:"id" play:"pk,incrementing"`
	Name string `db:"name" play:"fillable"`
}

func (pRole) TableName() string { return "p_roles" }

func setupRoles(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, s := range []string{
		`CREATE TABLE p_users (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE p_roles (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE role_user (user_id INTEGER, role_id INTEGER, assigned_by TEXT)`,
		`INSERT INTO p_users (id,name) VALUES (1,'al')`,
		`INSERT INTO p_roles (id,name) VALUES (1,'admin'),(2,'editor'),(3,'viewer')`,
	} {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	return db
}

func roleIDs(t *testing.T, db *playsql.DB) []int64 {
	t.Helper()
	var u pUser
	if err := db.Model(&pUser{}).With("Roles").Find(context.Background(), &u, int64(1)); err != nil {
		t.Fatalf("load roles: %v", err)
	}
	out := make([]int64, len(u.Roles))
	for i, r := range u.Roles {
		out[i] = r.ID
	}
	return out
}

func has(ids []int64, want ...int64) bool {
	set := map[int64]bool{}
	for _, id := range ids {
		set[id] = true
	}
	if len(ids) != len(want) {
		return false
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func TestAttachDetach(t *testing.T) {
	db := setupRoles(t)
	ctx := context.Background()
	u := &pUser{ID: 1}

	if err := db.Attach(ctx, u, "Roles", int64(1), int64(2)); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if got := roleIDs(t, db); !has(got, 1, 2) {
		t.Fatalf("after attach: %v, want [1 2]", got)
	}
	if err := db.Detach(ctx, u, "Roles", int64(1)); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if got := roleIDs(t, db); !has(got, 2) {
		t.Fatalf("after detach: %v, want [2]", got)
	}
	if err := db.Detach(ctx, u, "Roles"); err != nil { // detach all
		t.Fatalf("detach all: %v", err)
	}
	if got := roleIDs(t, db); len(got) != 0 {
		t.Fatalf("after detach all: %v, want empty", got)
	}
}

func TestSync(t *testing.T) {
	db := setupRoles(t)
	ctx := context.Background()
	u := &pUser{ID: 1}

	db.Attach(ctx, u, "Roles", int64(1), int64(2))
	res, err := db.Sync(ctx, u, "Roles", []any{int64(2), int64(3)})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := roleIDs(t, db); !has(got, 2, 3) {
		t.Fatalf("after sync: %v, want [2 3]", got)
	}
	if len(res.Attached) != 1 || res.Attached[0] != int64(3) {
		t.Fatalf("attached = %v, want [3]", res.Attached)
	}
	if len(res.Detached) != 1 {
		t.Fatalf("detached = %v, want one (role 1)", res.Detached)
	}
}

func TestToggle(t *testing.T) {
	db := setupRoles(t)
	ctx := context.Background()
	u := &pUser{ID: 1}

	db.Attach(ctx, u, "Roles", int64(2))
	// 2 is present (toggle off), 3 is absent (toggle on).
	if err := db.Toggle(ctx, u, "Roles", []any{int64(2), int64(3)}); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if got := roleIDs(t, db); !has(got, 3) {
		t.Fatalf("after toggle: %v, want [3]", got)
	}
}

func TestAttachWith_And_UpdatePivot(t *testing.T) {
	db := setupRoles(t)
	ctx := context.Background()
	u := &pUser{ID: 1}

	if err := db.AttachWith(ctx, u, "Roles", int64(1), map[string]any{"assigned_by": "admin"}); err != nil {
		t.Fatalf("attachwith: %v", err)
	}
	by, err := playsql.RawScalar[string](db, ctx, `SELECT assigned_by FROM role_user WHERE user_id=1 AND role_id=1`)
	if err != nil || by != "admin" {
		t.Fatalf("assigned_by = %q err %v, want admin", by, err)
	}

	if err := db.UpdatePivot(ctx, u, "Roles", int64(1), map[string]any{"assigned_by": "system"}); err != nil {
		t.Fatalf("updatepivot: %v", err)
	}
	by, _ = playsql.RawScalar[string](db, ctx, `SELECT assigned_by FROM role_user WHERE user_id=1 AND role_id=1`)
	if by != "system" {
		t.Fatalf("after update assigned_by = %q, want system", by)
	}
}

func TestAttach_NonPivotRelation(t *testing.T) {
	db := setupRoles(t)
	if err := db.Attach(context.Background(), &pUser{ID: 1}, "Pets", int64(1)); err == nil {
		t.Fatal("expected error attaching to a non-pivot relation")
	}
}

func TestAttach_MorphToMany(t *testing.T) {
	db := setupMorphToMany(t)
	ctx := context.Background()
	// Attach tag 2 (sql) to post 2 (world) — must write taggable_type.
	if err := db.Attach(ctx, &mmPost{ID: 2}, "Tags", int64(2)); err != nil {
		t.Fatalf("morph attach: %v", err)
	}
	posts, err := playsql.Query[mmPost](db).With("Tags").OrderBy("id", playsql.Asc).Get(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if n := tagNames(posts[1].Tags); len(n) != 2 || !n["go"] || !n["sql"] {
		t.Fatalf("post2 tags = %v, want go+sql", n)
	}
}
