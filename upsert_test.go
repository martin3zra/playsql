package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

// Setting has a unique key column for upsert conflicts.
type Setting struct {
	ID    int64  `db:"id" play:"pk,incrementing"`
	Key   string `db:"key" play:"fillable"`
	Value string `db:"value" play:"fillable"`
}

func (Setting) TableName() string { return "settings" }

func setupSettings(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(context.Background(),
		`CREATE TABLE settings (id INTEGER PRIMARY KEY, key TEXT UNIQUE, value TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	return db
}

func TestUpsert_InsertThenUpdate(t *testing.T) {
	db := setupSettings(t)
	ctx := context.Background()

	// First upsert inserts.
	if _, err := db.Model(&Setting{}).Upsert(ctx,
		[]map[string]any{{"key": "theme", "value": "dark"}},
		[]string{"key"}, []string{"value"}); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}

	// Second upsert on same key updates value.
	if _, err := db.Model(&Setting{}).Upsert(ctx,
		[]map[string]any{{"key": "theme", "value": "light"}},
		[]string{"key"}, []string{"value"}); err != nil {
		t.Fatalf("upsert update: %v", err)
	}

	var got Setting
	if err := db.Model(&Setting{}).WhereEq("key", "theme").First(ctx, &got); err != nil {
		t.Fatalf("first: %v", err)
	}
	if got.Value != "light" {
		t.Fatalf("upsert did not update: %+v", got)
	}
	n, _ := db.Model(&Setting{}).Count(ctx)
	if n != 1 {
		t.Fatalf("want 1 row (no duplicate), got %d", n)
	}
}

func TestUpsert_DefaultUpdateColumns(t *testing.T) {
	db := setupSettings(t)
	ctx := context.Background()

	db.Model(&Setting{}).Upsert(ctx,
		[]map[string]any{{"key": "k", "value": "v1"}}, []string{"key"}, nil)
	// nil updateColumns -> defaults to all non-conflict columns (value).
	db.Model(&Setting{}).Upsert(ctx,
		[]map[string]any{{"key": "k", "value": "v2"}}, []string{"key"}, nil)

	var got Setting
	if err := db.Model(&Setting{}).WhereEq("key", "k").First(ctx, &got); err != nil {
		t.Fatalf("first: %v", err)
	}
	if got.Value != "v2" {
		t.Fatalf("default update columns failed: %+v", got)
	}
}

func TestUpsert_Bulk(t *testing.T) {
	db := setupSettings(t)
	ctx := context.Background()

	_, err := db.Model(&Setting{}).Upsert(ctx, []map[string]any{
		{"key": "a", "value": "1"},
		{"key": "b", "value": "2"},
	}, []string{"key"}, []string{"value"})
	if err != nil {
		t.Fatalf("bulk upsert: %v", err)
	}

	n, _ := db.Model(&Setting{}).Count(ctx)
	if n != 2 {
		t.Fatalf("want 2 rows, got %d", n)
	}
}

func TestUpsert_RequiresConflictColumn(t *testing.T) {
	db := setupSettings(t)
	_, err := db.Model(&Setting{}).Upsert(context.Background(),
		[]map[string]any{{"key": "x"}}, nil, []string{"value"})
	if err == nil {
		t.Fatal("expected error without conflict columns")
	}
}
