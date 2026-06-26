package playsql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/martin3zra/playsql"
)

type tenantKey struct{}

func withTenant(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, tenantKey{}, id)
}

// tenantScope filters every query to the tenant in context; with no tenant it
// rejects rather than returning all rows.
type tenantScope struct{}

func (tenantScope) Apply(ctx context.Context, b *playsql.Builder) error {
	tid, ok := ctx.Value(tenantKey{}).(int64)
	if !ok {
		return errors.New("no tenant in context")
	}
	b.WhereEq("tenant_id", tid)
	return nil
}

type ScopedPost struct {
	ID       int64  `db:"id" play:"pk,incrementing"`
	TenantID int64  `db:"tenant_id"`
	Title    string `db:"title"`
}

func (ScopedPost) TableName() string       { return "scoped_posts" }
func (ScopedPost) Scopes() []playsql.Scope { return []playsql.Scope{tenantScope{}} }

func setupScoped(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE scoped_posts (id INTEGER PRIMARY KEY, tenant_id INTEGER, title TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	rows := `INSERT INTO scoped_posts (id, tenant_id, title) VALUES
		(1,1,'a'),(2,1,'b'),(3,1,'c'),(4,2,'a')`
	if _, err := db.Exec(ctx, rows); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

func TestScope_FiltersByTenant(t *testing.T) {
	db := setupScoped(t)

	var t1 []ScopedPost
	if err := db.Model(&ScopedPost{}).Get(withTenant(context.Background(), 1), &t1); err != nil {
		t.Fatalf("get t1: %v", err)
	}
	if len(t1) != 3 {
		t.Fatalf("tenant 1 should see 3 posts, got %d", len(t1))
	}

	var t2 []ScopedPost
	if err := db.Model(&ScopedPost{}).Get(withTenant(context.Background(), 2), &t2); err != nil {
		t.Fatalf("get t2: %v", err)
	}
	if len(t2) != 1 || t2[0].TenantID != 2 {
		t.Fatalf("tenant 2 should see 1 post, got %+v", t2)
	}
}

func TestScope_CountRespectsScope(t *testing.T) {
	db := setupScoped(t)
	n, err := db.Model(&ScopedPost{}).Count(withTenant(context.Background(), 1))
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Fatalf("scoped count want 3, got %d", n)
	}
}

func TestScope_RejectsWithoutTenant(t *testing.T) {
	db := setupScoped(t)
	var posts []ScopedPost
	err := db.Model(&ScopedPost{}).Get(context.Background(), &posts)
	if err == nil {
		t.Fatal("query with no tenant should be rejected by the scope")
	}
}

func TestScope_WithoutGlobalScopes(t *testing.T) {
	db := setupScoped(t)
	var all []ScopedPost
	// Bypassing scopes: no tenant needed, sees everything.
	if err := db.Model(&ScopedPost{}).WithoutGlobalScopes().Get(context.Background(), &all); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("WithoutGlobalScopes should see all 4, got %d", len(all))
	}
}

func TestScope_PrecedenceWithUserOr(t *testing.T) {
	db := setupScoped(t)
	ctx := withTenant(context.Background(), 1)

	// scope (tenant_id=1) AND (title='a' OR title='b') -> only tenant 1's a,b.
	// Tenant 2 also has title 'a' but must be excluded.
	var posts []ScopedPost
	err := db.Model(&ScopedPost{}).
		WhereGroup(func(q *playsql.Builder) {
			q.WhereEq("title", "a").OrWhere("title", "=", "b")
		}).
		Get(ctx, &posts)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("want 2 (tenant1 a,b), got %d: %+v", len(posts), posts)
	}
	for _, p := range posts {
		if p.TenantID != 1 {
			t.Fatalf("scope leaked tenant %d", p.TenantID)
		}
	}
}

func TestScope_DeleteRespectsScope(t *testing.T) {
	db := setupScoped(t)
	ctx := withTenant(context.Background(), 1)

	// Delete with tenant 1 must not touch tenant 2's row.
	n, err := db.Model(&ScopedPost{}).Delete(ctx)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 3 {
		t.Fatalf("want 3 deleted (tenant 1 only), got %d", n)
	}
	remaining, _ := db.Model(&ScopedPost{}).WithoutGlobalScopes().Count(context.Background())
	if remaining != 1 {
		t.Fatalf("tenant 2 row should survive, remaining=%d", remaining)
	}
}

func TestScope_Typed(t *testing.T) {
	db := setupScoped(t)
	posts, err := playsql.Query[ScopedPost](db).Get(withTenant(context.Background(), 2))
	if err != nil {
		t.Fatalf("typed scoped get: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("typed scope want 1, got %d", len(posts))
	}
}
