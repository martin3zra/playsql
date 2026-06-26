package playsql_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/martin3zra/playsql"
)

type pricedItem struct {
	playsql.Model
	ID    int64  `db:"id" play:"pk,incrementing"`
	Name  string `db:"name" play:"fillable"`
	Price int64  `db:"price" play:"fillable"`
	Cheap bool   `db:"cheap" play:"fillable"`
}

func (pricedItem) TableName() string { return "priced_items" }

func setupPriced(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(context.Background(),
		`CREATE TABLE priced_items (id INTEGER PRIMARY KEY, name TEXT, price INTEGER, cheap BOOLEAN DEFAULT 0)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	return db
}

func seedPriced(t *testing.T, db *playsql.DB) {
	t.Helper()
	ctx := context.Background()
	for _, w := range []pricedItem{{Name: "a", Price: 5}, {Name: "b", Price: 50}, {Name: "c", Price: 500}} {
		if err := db.Insert(ctx, &w); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestRaw_ScansIntoSlice(t *testing.T) {
	db := setupPriced(t)
	seedPriced(t, db)
	ctx := context.Background()

	var got []pricedItem
	if err := db.Raw(ctx, &got, `SELECT id, name, price FROM priced_items WHERE price >= ? ORDER BY price`, 50); err != nil {
		t.Fatalf("raw: %v", err)
	}
	if len(got) != 2 || got[0].Name != "b" || got[1].Name != "c" {
		t.Fatalf("unexpected rows: %+v", got)
	}
}

func TestRawQuery_Generic(t *testing.T) {
	db := setupPriced(t)
	seedPriced(t, db)
	ctx := context.Background()

	got, err := playsql.RawQuery[pricedItem](db, ctx, `SELECT * FROM priced_items ORDER BY id`)
	if err != nil {
		t.Fatalf("rawquery: %v", err)
	}
	if len(got) != 3 || got[0].Name != "a" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestRaw_RejectsNonSlice(t *testing.T) {
	db := setupPriced(t)
	var one pricedItem
	if err := db.Raw(context.Background(), &one, `SELECT * FROM priced_items`); err == nil {
		t.Fatal("expected error for non-slice dest")
	}
}

func TestRawScalar(t *testing.T) {
	db := setupPriced(t)
	seedPriced(t, db)
	ctx := context.Background()

	n, err := playsql.RawScalar[int64](db, ctx, `SELECT COUNT(*) FROM priced_items`)
	if err != nil {
		t.Fatalf("scalar count: %v", err)
	}
	if n != 3 {
		t.Fatalf("count = %d, want 3", n)
	}

	sum, err := playsql.RawScalar[int64](db, ctx, `SELECT SUM(price) FROM priced_items WHERE price >= ?`, 50)
	if err != nil {
		t.Fatalf("scalar sum: %v", err)
	}
	if sum != 550 {
		t.Fatalf("sum = %d, want 550", sum)
	}

	name, err := playsql.RawScalar[string](db, ctx, `SELECT name FROM priced_items ORDER BY price LIMIT 1`)
	if err != nil || name != "a" {
		t.Fatalf("scalar name = %q err = %v", name, err)
	}
}

func TestRawScalar_NoRows(t *testing.T) {
	db := setupPriced(t)
	_, err := playsql.RawScalar[int64](db, context.Background(), `SELECT price FROM priced_items WHERE id = 999`)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want sql.ErrNoRows, got %v", err)
	}
}

func TestRawRows_ManualScan(t *testing.T) {
	db := setupPriced(t)
	seedPriced(t, db)
	ctx := context.Background()

	rows, err := db.RawRows(ctx, `SELECT name, price FROM priced_items ORDER BY price`)
	if err != nil {
		t.Fatalf("rawrows: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		var price int64
		if err := rows.Scan(&name, &price); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if len(names) != 3 || names[0] != "a" {
		t.Fatalf("unexpected: %v", names)
	}
}

func TestUpdateReturning_SQLite(t *testing.T) {
	db := setupPriced(t)
	seedPriced(t, db)
	ctx := context.Background()

	var updated []pricedItem
	err := db.Model(&pricedItem{}).
		Where("price", ">", int64(40)).
		Returning("id", "name", "price").
		UpdateReturning(ctx, map[string]any{"cheap": false}, &updated)
	if err != nil {
		t.Fatalf("update returning: %v", err)
	}
	if len(updated) != 2 {
		t.Fatalf("want 2 returned rows, got %d: %+v", len(updated), updated)
	}
	for _, w := range updated {
		if w.Name == "" || w.Price <= 40 {
			t.Fatalf("row not hydrated: %+v", w)
		}
	}
}

func TestUpdateReturning_RequiresColumns(t *testing.T) {
	db := setupPriced(t)
	var dest []pricedItem
	err := db.Model(&pricedItem{}).UpdateReturning(context.Background(), map[string]any{"cheap": true}, &dest)
	if err == nil {
		t.Fatal("expected error when Returning columns are absent")
	}
}

func TestUpdateReturning_Typed(t *testing.T) {
	db := setupPriced(t)
	seedPriced(t, db)
	ctx := context.Background()

	rows, err := playsql.Query[pricedItem](db).
		WhereEq("name", "a").
		Returning("id", "name").
		UpdateReturning(ctx, map[string]any{"price": int64(7)})
	if err != nil {
		t.Fatalf("typed update returning: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "a" {
		t.Fatalf("unexpected: %+v", rows)
	}
}

func TestWithCTE_Update_SQLite(t *testing.T) {
	db := setupPriced(t)
	seedPriced(t, db)
	ctx := context.Background()

	// Mark rows below the average price as cheap, computing the average in a CTE.
	n, err := db.Model(&pricedItem{}).
		WithCTE("avg_price", "SELECT AVG(price) AS value FROM priced_items").
		WhereRaw("price < (SELECT value FROM avg_price)").
		Update(ctx, map[string]any{"cheap": true})
	if err != nil {
		t.Fatalf("cte update: %v", err)
	}
	// avg of 5,50,500 = 185; only 5 and 50 are below it.
	if n != 2 {
		t.Fatalf("want 2 cheap rows, got %d", n)
	}

	cheap, err := playsql.RawQuery[pricedItem](db, ctx, `SELECT * FROM priced_items WHERE cheap = 1 ORDER BY price`)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(cheap) != 2 || cheap[0].Name != "a" || cheap[1].Name != "b" {
		t.Fatalf("wrong rows marked cheap: %+v", cheap)
	}
}
