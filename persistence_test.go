package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

func TestInsert_SetsGeneratedID(t *testing.T) {
	db := setup(t)
	ctx := context.Background()

	u := &User{Name: "Zoe", Age: 41}
	if err := db.Insert(ctx, u); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("generated id was not written back onto the struct")
	}

	var got User
	if err := db.Model(&User{}).Find(ctx, &got, u.ID); err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Name != "Zoe" || got.Age != 41 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestUpdate(t *testing.T) {
	db := setup(t)
	ctx := context.Background()

	u := &User{ID: 2, Name: "Bobby", Age: 26}
	if err := db.Update(ctx, u); err != nil {
		t.Fatalf("update: %v", err)
	}

	var got User
	if err := db.Model(&User{}).Find(ctx, &got, int64(2)); err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Name != "Bobby" || got.Age != 26 {
		t.Fatalf("update not applied: %+v", got)
	}
}

func TestUpdate_RequiresPrimaryKey(t *testing.T) {
	db := setup(t)

	err := db.Update(context.Background(), &User{Name: "nope"})
	if err == nil {
		t.Fatal("expected error updating with zero primary key")
	}
}

func TestSave_InsertThenUpdate(t *testing.T) {
	db := setup(t)
	ctx := context.Background()

	// No id -> Save inserts.
	u := &User{Name: "Eve", Age: 22}
	if err := db.Save(ctx, u); err != nil {
		t.Fatalf("save insert: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("save should have inserted and set id")
	}

	// Has id -> Save updates.
	u.Age = 23
	if err := db.Save(ctx, u); err != nil {
		t.Fatalf("save update: %v", err)
	}

	var got User
	if err := db.Model(&User{}).Find(ctx, &got, u.ID); err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Age != 23 {
		t.Fatalf("save-update not applied: %+v", got)
	}

	n, _ := db.Model(&User{}).Count(ctx)
	if n != 4 { // 3 seeded + 1 inserted (update must not have inserted again)
		t.Fatalf("want 4 rows, got %d", n)
	}
}

func TestSave_NonIncrementingKeyInserts(t *testing.T) {
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	if _, err := db.Exec(ctx, `CREATE TABLE products (sku TEXT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	p := &Product{SKU: "ABC-1", Name: "Widget"}
	if err := db.Insert(ctx, p); err != nil {
		t.Fatalf("insert string-key row: %v", err)
	}

	var got Product
	if err := db.Model(&Product{}).Find(ctx, &got, "ABC-1"); err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Name != "Widget" {
		t.Fatalf("want Widget, got %+v", got)
	}
}
