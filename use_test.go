package playsql_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/martin3zra/playsql"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, no CGO)
)

// rawUsersDB opens a *sql.DB directly (bypassing playsql.Open) and seeds the
// users table, mimicking a handle a test harness would hand to playsql.Use.
func rawUsersDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1) // :memory: is per-connection; pin to one
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, name, age) VALUES (1,'Jane',30),(2,'Bob',25),(3,'Ann',30)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// TestUseWrapsExistingDB proves playsql can ride a *sql.DB it did not open: the
// core prerequisite for go-txdb single-connection test isolation.
func TestUseWrapsExistingDB(t *testing.T) {
	raw := rawUsersDB(t)

	db, err := playsql.Use(raw, "sqlite")
	if err != nil {
		t.Fatalf("Use: %v", err)
	}

	var users []User
	if err := db.Model(&User{}).WhereEq("age", int64(30)).Get(context.Background(), &users); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2 users aged 30, got %d: %+v", len(users), users)
	}
}

// TestUseUnknownDialect proves the dialect string — not the real driver —
// selects the grammar, and that an unknown name is rejected up front rather than
// nil-panicking on first query.
func TestUseUnknownDialect(t *testing.T) {
	raw := rawUsersDB(t)

	if _, err := playsql.Use(raw, "txdb"); err == nil {
		t.Fatal("want error for unknown dialect, got nil")
	}
}

// TestUseTxWrapsExistingTx proves the same for an in-progress *sql.Tx.
func TestUseTxWrapsExistingTx(t *testing.T) {
	raw := rawUsersDB(t)
	ctx := context.Background()

	sqlTx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer sqlTx.Rollback()

	tx, err := playsql.UseTx(sqlTx, "sqlite")
	if err != nil {
		t.Fatalf("UseTx: %v", err)
	}

	var users []User
	if err := tx.Model(&User{}).Get(ctx, &users); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("want 3 users, got %d", len(users))
	}

	if _, err := playsql.UseTx(sqlTx, "nope"); err == nil {
		t.Fatal("want error for unknown dialect, got nil")
	}
}
