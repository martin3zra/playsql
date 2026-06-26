package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

func TestQuery_Create(t *testing.T) {
	db := setupMembers(t) // members: id, name, email, role(guarded), created_at, updated_at
	ctx := context.Background()

	m, err := playsql.Query[Member](db).Create(ctx, map[string]any{
		"name":  "Jane",
		"email": "jane@x.com",
		"role":  "admin", // guarded -> dropped
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.ID == 0 || m.Name != "Jane" {
		t.Fatalf("not hydrated: %+v", m)
	}
	if m.Role != "" {
		t.Fatalf("guarded role mass-assigned: %q", m.Role)
	}
	if m.CreatedAt.IsZero() {
		t.Fatal("timestamp not set")
	}
}

func TestQuery_InsertUpdateDelete(t *testing.T) {
	db := setupMembers(t)
	ctx := context.Background()

	id, err := playsql.Query[Member](db).Insert(ctx, map[string]any{"name": "Bob", "email": "b@x.com"})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == 0 {
		t.Fatal("no id")
	}

	n, err := playsql.Query[Member](db).WhereEq("id", id).Update(ctx, map[string]any{"name": "Bobby"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 updated, got %d", n)
	}

	got, err := playsql.Query[Member](db).Find(ctx, id)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Name != "Bobby" {
		t.Fatalf("update not applied: %+v", got)
	}

	d, err := playsql.Query[Member](db).WhereEq("id", id).Delete(ctx)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if d != 1 {
		t.Fatalf("want 1 deleted, got %d", d)
	}
}

func TestQuery_GetPtr(t *testing.T) {
	db := setupMembers(t)
	ctx := context.Background()

	playsql.Query[Member](db).Insert(ctx, map[string]any{"name": "A", "email": "a@x.com"})
	playsql.Query[Member](db).Insert(ctx, map[string]any{"name": "B", "email": "b@x.com"})

	ptrs, err := playsql.Query[Member](db).OrderBy("id", playsql.Asc).GetPtr(ctx)
	if err != nil {
		t.Fatalf("getptr: %v", err)
	}
	if len(ptrs) != 2 || ptrs[0].Name != "A" {
		t.Fatalf("getptr wrong: %+v", ptrs)
	}
}
