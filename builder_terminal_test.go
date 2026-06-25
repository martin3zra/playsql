package playsql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/martin3zra/playsql"
)

func TestFirst(t *testing.T) {
	db := setup(t)

	var u User
	if err := db.Model(&User{}).WhereEq("age", int64(30)).First(context.Background(), &u); err != nil {
		t.Fatalf("first: %v", err)
	}
	if u.ID == 0 || u.Age != 30 {
		t.Fatalf("unexpected row: %+v", u)
	}
}

func TestFirst_NotFound(t *testing.T) {
	db := setup(t)

	var u User
	err := db.Model(&User{}).WhereEq("age", int64(999)).First(context.Background(), &u)
	if !errors.Is(err, playsql.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestFind_IntKey(t *testing.T) {
	db := setup(t)

	var u User
	if err := db.Model(&User{}).Find(context.Background(), &u, int64(2)); err != nil {
		t.Fatalf("find: %v", err)
	}
	if u.Name != "Bob" {
		t.Fatalf("want Bob, got %+v", u)
	}
}

// TestFind_NonInt64Key proves the v1 panic is gone: Find binds the id as-is, so
// a plain int (not int64) no longer triggers value[0].(int64).
func TestFind_NonInt64Key(t *testing.T) {
	db := setup(t)

	var u User
	if err := db.Model(&User{}).Find(context.Background(), &u, 1); err != nil {
		t.Fatalf("find with int: %v", err)
	}
	if u.Name != "Jane" {
		t.Fatalf("want Jane, got %+v", u)
	}
}

func TestCount(t *testing.T) {
	db := setup(t)

	n, err := db.Model(&User{}).Count(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Fatalf("want 3, got %d", n)
	}

	n, err = db.Model(&User{}).WhereEq("age", int64(30)).Count(context.Background())
	if err != nil {
		t.Fatalf("count filtered: %v", err)
	}
	if n != 2 {
		t.Fatalf("want 2 aged 30, got %d", n)
	}
}

// Product has a string primary key — exercises Find on a non-incrementing,
// non-integer key.
type Product struct {
	SKU  string `db:"sku" play:"pk"`
	Name string `db:"name"`
}

func (Product) TableName() string { return "products" }

func TestFind_StringKey(t *testing.T) {
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE products (sku TEXT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO products (sku, name) VALUES ('ABC-1','Widget')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var p Product
	if err := db.Model(&Product{}).Find(ctx, &p, "ABC-1"); err != nil {
		t.Fatalf("find string key: %v", err)
	}
	if p.Name != "Widget" {
		t.Fatalf("want Widget, got %+v", p)
	}
}
