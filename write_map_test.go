package playsql_test

import (
	"context"
	"testing"
	"time"

	"github.com/martin3zra/playsql"
)

// Member uses a fillable whitelist: only name and email are mass-assignable.
type Member struct {
	playsql.Model
	ID        int64     `db:"id" play:"pk,incrementing,guarded"`
	Name      string    `db:"name" play:"fillable"`
	Email     string    `db:"email" play:"fillable"`
	Role      string    `db:"role"` // not fillable -> dropped from map writes
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func setupMembers(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(context.Background(),
		`CREATE TABLE members (id INTEGER PRIMARY KEY, name TEXT, email TEXT, role TEXT, created_at DATETIME, updated_at DATETIME)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	return db
}

func TestMapInsert_FiltersFillable(t *testing.T) {
	db := setupMembers(t)
	ctx := context.Background()

	// role is NOT fillable and must be dropped (over-posting protection).
	id, err := db.Model(&Member{}).Insert(ctx, map[string]any{
		"name":  "Jane",
		"email": "jane@example.com",
		"role":  "admin",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == 0 {
		t.Fatal("expected generated id")
	}

	var m Member
	if err := db.Model(&Member{}).Find(ctx, &m, id); err != nil {
		t.Fatalf("find: %v", err)
	}
	if m.Name != "Jane" || m.Email != "jane@example.com" {
		t.Fatalf("fillable columns not written: %+v", m)
	}
	if m.Role != "" {
		t.Fatalf("guarded column 'role' was mass-assigned: %q", m.Role)
	}
	if m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not stamped: %+v", m)
	}
}

func TestMapCreate_Hydrates(t *testing.T) {
	db := setupMembers(t)

	var m Member
	err := db.Model(&Member{}).Create(context.Background(), &m, map[string]any{
		"name":  "Eve",
		"email": "eve@example.com",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.ID == 0 || m.Name != "Eve" || m.CreatedAt.IsZero() {
		t.Fatalf("dest not hydrated: %+v", m)
	}
}

func TestMapUpdate(t *testing.T) {
	db := setupMembers(t)
	ctx := context.Background()

	id, _ := db.Model(&Member{}).Insert(ctx, map[string]any{"name": "Jane", "email": "j@x.com"})

	// role still dropped on update; name updated.
	n, err := db.Model(&Member{}).WhereEq("id", id).Update(ctx, map[string]any{
		"name": "Janet",
		"role": "admin",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 affected, got %d", n)
	}

	var m Member
	if err := db.Model(&Member{}).Find(ctx, &m, id); err != nil {
		t.Fatalf("find: %v", err)
	}
	if m.Name != "Janet" {
		t.Fatalf("name not updated: %+v", m)
	}
	if m.Role != "" {
		t.Fatalf("guarded role updated via map: %q", m.Role)
	}
}

func TestMapInsertMany(t *testing.T) {
	db := setupMembers(t)
	ctx := context.Background()

	n, err := db.Model(&Member{}).InsertMany(ctx, []map[string]any{
		{"name": "A", "email": "a@x.com"},
		{"name": "B", "email": "b@x.com"},
		{"name": "C", "email": "c@x.com", "role": "admin"}, // role dropped
	})
	if err != nil {
		t.Fatalf("insert many: %v", err)
	}
	if n != 3 {
		t.Fatalf("want 3 inserted, got %d", n)
	}

	total, _ := db.Model(&Member{}).Count(ctx)
	if total != 3 {
		t.Fatalf("want 3 rows, got %d", total)
	}

	// row C's role must not have been written.
	var only []Member
	if err := db.Model(&Member{}).WhereEq("name", "C").Get(ctx, &only); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(only) != 1 || only[0].Role != "" {
		t.Fatalf("guarded role leaked in bulk insert: %+v", only)
	}
}
