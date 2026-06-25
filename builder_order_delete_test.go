package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

func TestOrderBy_Integration(t *testing.T) {
	db := setup(t)
	ctx := context.Background()

	var users []User
	// age DESC, name ASC -> Ann(30), Jane(30), Bob(25)
	err := db.Model(&User{}).
		OrderBy("age", playsql.Desc).
		OrderBy("name", playsql.Asc).
		Get(ctx, &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("want 3, got %d", len(users))
	}
	if users[0].Name != "Ann" || users[1].Name != "Jane" || users[2].Name != "Bob" {
		t.Fatalf("wrong order: %s, %s, %s", users[0].Name, users[1].Name, users[2].Name)
	}
}

func TestFirst_WithOrderBy(t *testing.T) {
	db := setup(t)

	var u User
	if err := db.Model(&User{}).OrderBy("age", playsql.Asc).First(context.Background(), &u); err != nil {
		t.Fatalf("first: %v", err)
	}
	if u.Name != "Bob" { // youngest
		t.Fatalf("want Bob (age 25), got %+v", u)
	}
}

func TestMassDelete(t *testing.T) {
	db := setup(t)
	ctx := context.Background()

	n, err := db.Model(&User{}).WhereEq("age", int64(30)).Delete(ctx)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 2 {
		t.Fatalf("want 2 deleted, got %d", n)
	}

	remaining, _ := db.Model(&User{}).Count(ctx)
	if remaining != 1 {
		t.Fatalf("want 1 remaining, got %d", remaining)
	}
}

func TestDeleteByModel(t *testing.T) {
	db := setup(t)
	ctx := context.Background()

	if err := db.Delete(ctx, &User{ID: 2}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var u User
	err := db.Model(&User{}).Find(ctx, &u, int64(2))
	if err == nil {
		t.Fatal("row should be gone")
	}

	n, _ := db.Model(&User{}).Count(ctx)
	if n != 2 {
		t.Fatalf("want 2 remaining, got %d", n)
	}
}

func TestDeleteByModel_RequiresPrimaryKey(t *testing.T) {
	db := setup(t)
	if err := db.Delete(context.Background(), &User{}); err == nil {
		t.Fatal("expected error deleting with zero primary key")
	}
}
