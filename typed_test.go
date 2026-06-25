package playsql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/martin3zra/playsql"
)

func TestQuery_Get(t *testing.T) {
	db := setup(t) // users_tests-equivalent: User{ID,FirstName,Active,...}
	ctx := context.Background()

	// typed: returns []User directly, no dest pointer.
	users, err := playsql.Query[User](db).
		WhereEq("age", int64(30)).
		OrderBy("id", playsql.Asc).
		Get(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2, got %d", len(users))
	}
	if users[0].Name == "" {
		t.Fatal("not hydrated")
	}
}

func TestQuery_First(t *testing.T) {
	db := setup(t)

	u, err := playsql.Query[User](db).OrderBy("age", playsql.Asc).First(context.Background())
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if u.Name != "Bob" { // youngest
		t.Fatalf("want Bob, got %+v", u)
	}
}

func TestQuery_FirstNotFound(t *testing.T) {
	db := setup(t)
	_, err := playsql.Query[User](db).WhereEq("age", int64(999)).First(context.Background())
	if !errors.Is(err, playsql.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestQuery_Find(t *testing.T) {
	db := setup(t)
	u, err := playsql.Query[User](db).Find(context.Background(), int64(2))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if u.Name != "Bob" {
		t.Fatalf("want Bob, got %+v", u)
	}
}

func TestQuery_Count(t *testing.T) {
	db := setup(t)
	n, err := playsql.Query[User](db).WhereEq("age", int64(30)).Count(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("want 2, got %d", n)
	}
}

func TestQuery_WithRelations(t *testing.T) {
	db := setupBlogs(t)
	blogs, err := playsql.Query[Blog](db).With("Comments").OrderBy("id", playsql.Asc).Get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(blogs) != 2 || len(blogs[0].Comments) != 2 {
		t.Fatalf("typed eager load failed: %+v", blogs)
	}
}

func TestQuery_InTransaction(t *testing.T) {
	db := setup(t)
	ctx := context.Background()

	err := db.Tx(ctx, func(tx *playsql.Tx) error {
		// Query works against *Tx too (both DB and Tx satisfy Model(any) *Builder).
		users, err := playsql.Query[User](tx).Get(ctx)
		if err != nil {
			return err
		}
		if len(users) != 3 {
			t.Fatalf("want 3 in tx, got %d", len(users))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
}
