package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

func setupManyUsers(t *testing.T, n int) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 1; i <= n; i++ {
		if err := db.Insert(ctx, &User{Name: "u", Age: int64(i)}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return db
}

func TestPaginate(t *testing.T) {
	db := setupManyUsers(t, 5)
	ctx := context.Background()

	var page []User
	p, err := db.Model(&User{}).OrderBy("id", playsql.Asc).Paginate(ctx, &page, 2, 2)
	if err != nil {
		t.Fatalf("paginate: %v", err)
	}
	if p.Total != 5 || p.LastPage != 3 || p.Page != 2 || p.PerPage != 2 {
		t.Fatalf("metadata wrong: %+v", p)
	}
	if len(page) != 2 {
		t.Fatalf("want 2 items on page 2, got %d", len(page))
	}
	// page 2 of [1..5] ordered by id -> ages 3,4
	if page[0].Age != 3 || page[1].Age != 4 {
		t.Fatalf("wrong page slice: %+v", page)
	}
	if !p.HasMore() {
		t.Fatal("page 2 of 3 should have more")
	}
}

func TestPaginate_LastPagePartial(t *testing.T) {
	db := setupManyUsers(t, 5)
	ctx := context.Background()

	var page []User
	p, err := db.Model(&User{}).OrderBy("id", playsql.Asc).Paginate(ctx, &page, 3, 2)
	if err != nil {
		t.Fatalf("paginate: %v", err)
	}
	if len(page) != 1 || page[0].Age != 5 {
		t.Fatalf("last page should hold the single remainder, got %+v", page)
	}
	if p.HasMore() {
		t.Fatal("last page should not report more")
	}
}

func TestPaginate_RespectsWhere(t *testing.T) {
	db := setupManyUsers(t, 5)
	ctx := context.Background()

	var page []User
	// only ages > 2 -> 3 rows total
	p, err := db.Model(&User{}).Where("age", ">", int64(2)).OrderBy("id", playsql.Asc).Paginate(ctx, &page, 1, 2)
	if err != nil {
		t.Fatalf("paginate: %v", err)
	}
	if p.Total != 3 || p.LastPage != 2 {
		t.Fatalf("total should respect where: %+v", p)
	}
}

func TestPaginate_PerPageRequired(t *testing.T) {
	db := setupManyUsers(t, 1)
	var page []User
	if _, err := db.Model(&User{}).Paginate(context.Background(), &page, 1, 0); err == nil {
		t.Fatal("expected error for perPage < 1")
	}
}

func TestQuery_Paginate(t *testing.T) {
	db := setupManyUsers(t, 5)

	res, err := playsql.Query[User](db).OrderBy("id", playsql.Asc).Paginate(context.Background(), 1, 3)
	if err != nil {
		t.Fatalf("typed paginate: %v", err)
	}
	if res.Total != 5 || res.LastPage != 2 || len(res.Items) != 3 {
		t.Fatalf("typed page wrong: %+v", res)
	}
	if res.Items[0].Age != 1 {
		t.Fatalf("typed page items wrong: %+v", res.Items)
	}
}
