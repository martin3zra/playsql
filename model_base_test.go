package playsql_test

import (
	"context"
	"testing"
	"time"

	"github.com/martin3zra/playsql"
)

// Account embeds playsql.Model (exists + dirty tracking), has timestamp columns,
// and no TableName (so the table is inferred as "accounts").
type Account struct {
	playsql.Model
	ID        int64     `db:"id" play:"pk,incrementing"`
	Name      string    `db:"name"`
	Balance   int64     `db:"balance"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func setupAccounts(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// Table name inferred as "accounts".
	if _, err := db.Exec(context.Background(),
		`CREATE TABLE accounts (id INTEGER PRIMARY KEY, name TEXT, balance INTEGER, created_at DATETIME, updated_at DATETIME)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	return db
}

func TestTimestamps_OnInsert(t *testing.T) {
	db := setupAccounts(t)

	a := &Account{Name: "Jane"}
	if err := db.Insert(context.Background(), a); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if a.CreatedAt.IsZero() || a.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not stamped on insert: %+v", a)
	}
	if a.ID == 0 {
		t.Fatal("id not set")
	}
}

func TestSave_UsesExistsFlag(t *testing.T) {
	db := setupAccounts(t)
	ctx := context.Background()

	// Fresh struct: exists=false -> Save inserts.
	a := &Account{Name: "Eve", Balance: 100}
	if err := db.Save(ctx, a); err != nil {
		t.Fatalf("save insert: %v", err)
	}
	if a.ID == 0 {
		t.Fatal("save should have inserted")
	}

	// Same struct now exists -> Save updates (no duplicate row).
	a.Balance = 250
	if err := db.Save(ctx, a); err != nil {
		t.Fatalf("save update: %v", err)
	}
	n, _ := db.Model(&Account{}).Count(ctx)
	if n != 1 {
		t.Fatalf("save should not have inserted twice, count=%d", n)
	}

	var got Account
	if err := db.Model(&Account{}).Find(ctx, &got, a.ID); err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Balance != 250 {
		t.Fatalf("update not applied: %+v", got)
	}
}

func TestUpdate_BumpsUpdatedAtButNotCreatedAt(t *testing.T) {
	db := setupAccounts(t)
	ctx := context.Background()

	a := &Account{Name: "Jane"}
	if err := db.Insert(ctx, a); err != nil {
		t.Fatalf("insert: %v", err)
	}
	created := a.CreatedAt

	time.Sleep(5 * time.Millisecond)

	a.Name = "Janet"
	if err := db.Update(ctx, a); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !a.CreatedAt.Equal(created) {
		t.Errorf("created_at must not change on update: was %v now %v", created, a.CreatedAt)
	}
	if !a.UpdatedAt.After(created) {
		t.Errorf("updated_at should advance: created %v updated %v", created, a.UpdatedAt)
	}
}

func TestDirtyTracking_NoOpWhenUnchanged(t *testing.T) {
	db := setupAccounts(t)
	ctx := context.Background()

	a := &Account{Name: "Jane", Balance: 10}
	if err := db.Insert(ctx, a); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Load a fresh copy (baseline captured on scan).
	var loaded Account
	if err := db.Model(&Account{}).Find(ctx, &loaded, a.ID); err != nil {
		t.Fatalf("find: %v", err)
	}
	before := loaded.UpdatedAt

	// No fields changed -> Update is a no-op and must not bump updated_at.
	if err := db.Update(ctx, &loaded); err != nil {
		t.Fatalf("update no-op: %v", err)
	}
	if !loaded.UpdatedAt.Equal(before) {
		t.Errorf("no-op update should not stamp updated_at: before %v after %v", before, loaded.UpdatedAt)
	}
}
