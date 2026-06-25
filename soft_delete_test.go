package playsql_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/martin3zra/playsql"
)

// Post is a soft-deletable model.
type Post struct {
	ID        int64      `db:"id" play:"pk,incrementing"`
	Title     string     `db:"title"`
	DeletedAt *time.Time `db:"deleted_at" play:"softdelete"`
}

func (Post) TableName() string { return "posts" }

func setupPosts(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT, deleted_at DATETIME)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, title := range []string{"A", "B", "C"} {
		if err := db.Insert(ctx, &Post{Title: title}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return db
}

func count(t *testing.T, b *playsql.Builder) int64 {
	t.Helper()
	n, err := b.Count(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestSoftDelete_HidesRows(t *testing.T) {
	db := setupPosts(t)
	ctx := context.Background()

	if err := db.Delete(ctx, &Post{ID: 2}); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	// Default queries hide the trashed row.
	if got := count(t, db.Model(&Post{})); got != 2 {
		t.Fatalf("want 2 visible, got %d", got)
	}
	// WithTrashed sees all.
	if got := count(t, db.Model(&Post{}).WithTrashed()); got != 3 {
		t.Fatalf("want 3 with trashed, got %d", got)
	}
	// OnlyTrashed sees just the deleted one.
	if got := count(t, db.Model(&Post{}).OnlyTrashed()); got != 1 {
		t.Fatalf("want 1 only-trashed, got %d", got)
	}

	// Row still physically present.
	var all []Post
	if err := db.Model(&Post{}).WithTrashed().Get(ctx, &all); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("row should not be physically removed, got %d", len(all))
	}
}

func TestSoftDelete_SetsStructField(t *testing.T) {
	db := setupPosts(t)
	p := &Post{ID: 1}
	if err := db.Delete(context.Background(), p); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if p.DeletedAt == nil {
		t.Fatal("deleted_at should be set on the struct after soft delete")
	}
}

func TestRestore(t *testing.T) {
	db := setupPosts(t)
	ctx := context.Background()

	p := &Post{ID: 2}
	if err := db.Delete(ctx, p); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := count(t, db.Model(&Post{})); got != 2 {
		t.Fatalf("want 2 after delete, got %d", got)
	}

	if err := db.Restore(ctx, p); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if p.DeletedAt != nil {
		t.Fatal("deleted_at should be cleared on the struct after restore")
	}
	if got := count(t, db.Model(&Post{})); got != 3 {
		t.Fatalf("want 3 after restore, got %d", got)
	}
}

func TestForceDelete_HardRemoves(t *testing.T) {
	db := setupPosts(t)
	ctx := context.Background()

	if err := db.ForceDelete(ctx, &Post{ID: 3}); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	if got := count(t, db.Model(&Post{}).WithTrashed()); got != 2 {
		t.Fatalf("force delete should physically remove, got %d", got)
	}
}

func TestMassSoftDeleteAndRestore(t *testing.T) {
	db := setupPosts(t)
	ctx := context.Background()

	// Soft-delete A and B via the builder.
	n, err := db.Model(&Post{}).WhereIn("id", []int64{1, 2}).Delete(ctx)
	if err != nil {
		t.Fatalf("mass delete: %v", err)
	}
	if n != 2 {
		t.Fatalf("want 2 soft-deleted, got %d", n)
	}
	if got := count(t, db.Model(&Post{})); got != 1 {
		t.Fatalf("want 1 visible, got %d", got)
	}

	// Restore them via the builder.
	r, err := db.Model(&Post{}).WhereIn("id", []int64{1, 2}).Restore(ctx)
	if err != nil {
		t.Fatalf("mass restore: %v", err)
	}
	if r != 2 {
		t.Fatalf("want 2 restored, got %d", r)
	}
	if got := count(t, db.Model(&Post{})); got != 3 {
		t.Fatalf("want 3 after restore, got %d", got)
	}
}

func TestRestore_NotSoftDeletable(t *testing.T) {
	db := setup(t)
	err := db.Restore(context.Background(), &User{ID: 1})
	if !errors.Is(err, playsql.ErrNotSoftDeletable) {
		t.Fatalf("want ErrNotSoftDeletable, got %v", err)
	}
}
