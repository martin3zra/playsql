package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

func TestCursorPaginate_WalksAllPages(t *testing.T) {
	db := setupManyUsers(t, 5) // ids 1..5
	ctx := context.Background()

	var after any
	var seen []int64
	for {
		var page []User
		res, err := db.Model(&User{}).CursorPaginate(ctx, &page, playsql.Cursor{
			Column: "id", After: after, Limit: 2,
		})
		if err != nil {
			t.Fatalf("cursor: %v", err)
		}
		if len(page) > 2 {
			t.Fatalf("page exceeded limit: %d", len(page))
		}
		for _, u := range page {
			seen = append(seen, u.ID)
		}
		if !res.HasMore {
			if res.NextCursor != nil {
				t.Fatalf("last page should have nil NextCursor, got %v", res.NextCursor)
			}
			break
		}
		after = res.NextCursor
	}

	if len(seen) != 5 {
		t.Fatalf("want 5 rows across pages, got %d: %v", len(seen), seen)
	}
	for i, id := range seen {
		if id != int64(i+1) {
			t.Fatalf("rows out of order: %v", seen)
		}
	}
}

func TestCursorPaginate_FirstPageCursor(t *testing.T) {
	db := setupManyUsers(t, 5)

	var page []User
	res, err := db.Model(&User{}).CursorPaginate(context.Background(), &page, playsql.Cursor{
		Column: "id", Limit: 2,
	})
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if len(page) != 2 || page[0].ID != 1 || page[1].ID != 2 {
		t.Fatalf("first page wrong: %+v", page)
	}
	if !res.HasMore || res.NextCursor != int64(2) {
		t.Fatalf("cursor metadata wrong: %+v", res)
	}
}

func TestCursorPaginate_Desc(t *testing.T) {
	db := setupManyUsers(t, 5)

	var page []User
	res, err := db.Model(&User{}).CursorPaginate(context.Background(), &page, playsql.Cursor{
		Column: "id", Limit: 2, Desc: true,
	})
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if len(page) != 2 || page[0].ID != 5 || page[1].ID != 4 {
		t.Fatalf("desc first page wrong: %+v", page)
	}
	if res.NextCursor != int64(4) {
		t.Fatalf("desc cursor wrong: %+v", res)
	}
}

func TestCursorPaginate_RespectsWhere(t *testing.T) {
	db := setupManyUsers(t, 5)

	var page []User
	res, err := db.Model(&User{}).Where("age", ">", int64(2)).CursorPaginate(
		context.Background(), &page, playsql.Cursor{Column: "id", Limit: 2})
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	// ages = ids here; age>2 -> ids 3,4,5. First page 3,4 with more.
	if len(page) != 2 || page[0].ID != 3 || !res.HasMore {
		t.Fatalf("where+cursor wrong: %+v / %+v", page, res)
	}
}

func TestQuery_CursorPaginate(t *testing.T) {
	db := setupManyUsers(t, 3)

	res, err := playsql.Query[User](db).CursorPaginate(context.Background(), playsql.Cursor{
		Column: "id", Limit: 2,
	})
	if err != nil {
		t.Fatalf("typed cursor: %v", err)
	}
	if len(res.Items) != 2 || !res.HasMore || res.NextCursor != int64(2) {
		t.Fatalf("typed cursor page wrong: %+v", res)
	}
}

func TestCursorPaginate_LimitRequired(t *testing.T) {
	db := setupManyUsers(t, 1)
	var page []User
	if _, err := db.Model(&User{}).CursorPaginate(context.Background(), &page, playsql.Cursor{Column: "id"}); err == nil {
		t.Fatal("expected error for Limit < 1")
	}
}
