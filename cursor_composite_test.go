package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

// Event has a non-unique sort column (bucket) plus a unique id — the classic
// case where a single-column cursor skips tied rows.
type Event struct {
	ID     int64 `db:"id" play:"pk,incrementing"`
	Bucket int64 `db:"bucket"`
}

func (Event) TableName() string { return "events" }

func setupEvents(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE events (id INTEGER PRIMARY KEY, bucket INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	// buckets with ties: id1..2 share bucket 10, id3..4 share bucket 20, id5 bucket 30.
	if _, err := db.Exec(ctx, `INSERT INTO events (id, bucket) VALUES (1,10),(2,10),(3,20),(4,20),(5,30)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// TestCursor_SingleColumnSkipsTies documents the failure the composite cursor
// fixes: paging by the non-unique bucket alone drops tied rows.
func TestCursor_SingleColumnSkipsTies(t *testing.T) {
	db := setupEvents(t)

	seen := walkCursor(t, db, func(after any) playsql.Cursor {
		return playsql.Cursor{Column: "bucket", After: after, Limit: 1}
	})
	// With limit 1 and only the bucket key, the second row of each tied bucket
	// is skipped (bucket > 10 jumps past id 2). So ids 2 and 4 are lost.
	if len(seen) == 5 {
		t.Fatalf("single-column cursor unexpectedly kept all rows: %v", seen)
	}
}

// TestCursor_CompositeKeepsTies is the fix: bucket + id total order, no skips.
func TestCursor_CompositeKeepsTies(t *testing.T) {
	db := setupEvents(t)
	ctx := context.Background()

	seen := walkCursorComposite(t, db, ctx)
	if len(seen) != 5 {
		t.Fatalf("composite cursor should see all 5 rows, got %d: %v", len(seen), seen)
	}
	for i, id := range seen {
		if id != int64(i+1) {
			t.Fatalf("rows out of order: %v", seen)
		}
	}
}

func walkCursor(t *testing.T, db *playsql.DB, mk func(after any) playsql.Cursor) []int64 {
	t.Helper()
	ctx := context.Background()
	var seen []int64
	var after any
	for {
		var page []Event
		res, err := db.Model(&Event{}).CursorPaginate(ctx, &page, mk(after))
		if err != nil {
			t.Fatalf("cursor: %v", err)
		}
		for _, e := range page {
			seen = append(seen, e.ID)
		}
		if !res.HasMore {
			break
		}
		after = res.NextCursor
	}
	return seen
}

func walkCursorComposite(t *testing.T, db *playsql.DB, ctx context.Context) []int64 {
	t.Helper()
	var seen []int64
	var after any
	for {
		var page []Event
		res, err := db.Model(&Event{}).CursorPaginate(ctx, &page, playsql.Cursor{
			Keys:  []playsql.CursorKey{{Column: "bucket"}, {Column: "id"}},
			After: after,
			Limit: 1,
		})
		if err != nil {
			t.Fatalf("composite cursor: %v", err)
		}
		for _, e := range page {
			seen = append(seen, e.ID)
		}
		if !res.HasMore {
			if res.NextCursor != nil {
				t.Fatalf("last page NextCursor should be nil, got %v", res.NextCursor)
			}
			break
		}
		// NextCursor is a []any aligned to Keys.
		cur, ok := res.NextCursor.([]any)
		if !ok || len(cur) != 2 {
			t.Fatalf("composite NextCursor should be []any{bucket,id}, got %#v", res.NextCursor)
		}
		after = cur
	}
	return seen
}

func TestCursor_CompositeFirstPage(t *testing.T) {
	db := setupEvents(t)

	var page []Event
	res, err := db.Model(&Event{}).CursorPaginate(context.Background(), &page, playsql.Cursor{
		Keys:  []playsql.CursorKey{{Column: "bucket"}, {Column: "id"}},
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if len(page) != 2 || page[0].ID != 1 || page[1].ID != 2 {
		t.Fatalf("first page wrong: %+v", page)
	}
	cur, ok := res.NextCursor.([]any)
	if !ok || cur[0] != int64(10) || cur[1] != int64(2) {
		t.Fatalf("composite cursor value wrong: %#v", res.NextCursor)
	}
}

func TestCursor_CompositeAfterLengthValidated(t *testing.T) {
	db := setupEvents(t)
	var page []Event
	_, err := db.Model(&Event{}).CursorPaginate(context.Background(), &page, playsql.Cursor{
		Keys:  []playsql.CursorKey{{Column: "bucket"}, {Column: "id"}},
		After: []any{int64(10)}, // too few
		Limit: 2,
	})
	if err == nil {
		t.Fatal("expected error for mismatched After length")
	}
}
