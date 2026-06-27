package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

func TestLoadCount_Single_StrategyA(t *testing.T) {
	db, ids := setupAgg(t)
	ctx := context.Background()

	var post aPost
	if err := db.Model(&aPost{}).Find(ctx, &post, ids["hello"]); err != nil {
		t.Fatalf("find: %v", err)
	}
	if post.CommentsCount != 0 {
		t.Fatalf("precondition: count should be 0 before load, got %d", post.CommentsCount)
	}
	if err := db.LoadCount(ctx, &post, "Comments"); err != nil {
		t.Fatalf("loadcount: %v", err)
	}
	if post.CommentsCount != 2 {
		t.Fatalf("CommentsCount = %d, want 2", post.CommentsCount)
	}
}

func TestLoadSum_Slice_StrategyA(t *testing.T) {
	db, _ := setupAgg(t)
	ctx := context.Background()

	var posts []aPost
	if err := db.Model(&aPost{}).OrderBy("id", playsql.Asc).Get(ctx, &posts); err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := db.LoadSum(ctx, &posts, "Comments", "votes"); err != nil {
		t.Fatalf("loadsum: %v", err)
	}
	if posts[0].VotesSum != 5 {
		t.Fatalf("hello sum = %d, want 5", posts[0].VotesSum)
	}
	if posts[1].VotesSum != 0 {
		t.Fatalf("empty sum = %d, want 0", posts[1].VotesSum)
	}
}

func TestLoadCount_StrategyB(t *testing.T) {
	db, _ := setupBlog(t)
	ctx := context.Background()

	var posts []exPost
	if err := db.Model(&exPost{}).Get(ctx, &posts); err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := db.LoadCount(ctx, &posts, "Comments"); err != nil {
		t.Fatalf("loadcount: %v", err)
	}
	for _, p := range posts {
		switch p.Title {
		case "hello":
			if p.CountOf("Comments") != 2 {
				t.Fatalf("hello = %d, want 2", p.CountOf("Comments"))
			}
		case "empty":
			if p.CountOf("Comments") != 0 {
				t.Fatalf("empty = %d, want 0", p.CountOf("Comments"))
			}
		}
	}
}

func TestLoadExists_And_MinMax(t *testing.T) {
	db, _ := setupAgg(t)
	ctx := context.Background()

	var posts []aPost
	if err := db.Model(&aPost{}).OrderBy("id", playsql.Asc).Get(ctx, &posts); err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := db.LoadExists(ctx, &posts, "Comments"); err != nil {
		t.Fatalf("loadexists: %v", err)
	}
	if err := db.LoadMin(ctx, &posts, "Comments", "votes"); err != nil {
		t.Fatalf("loadmin: %v", err)
	}
	if err := db.LoadMax(ctx, &posts, "Comments", "votes"); err != nil {
		t.Fatalf("loadmax: %v", err)
	}
	hello, empty := posts[0], posts[1]
	if v, _ := hello.Aggregate("comments_exists"); toI(v) != 1 {
		t.Fatalf("hello exists = %v, want 1", v)
	}
	if v, _ := empty.Aggregate("comments_exists"); toI(v) != 0 {
		t.Fatalf("empty exists = %v, want 0", v)
	}
	if v, _ := hello.Aggregate("comments_min_votes"); toI(v) != 2 {
		t.Fatalf("hello min = %v, want 2", v)
	}
	if v, _ := hello.Aggregate("comments_max_votes"); toI(v) != 3 {
		t.Fatalf("hello max = %v, want 3", v)
	}
}

func TestLoadCount_AliasConstrain(t *testing.T) {
	db, _ := setupAgg(t)
	ctx := context.Background()

	var posts []aPost
	if err := db.Model(&aPost{}).OrderBy("id", playsql.Asc).Get(ctx, &posts); err != nil {
		t.Fatalf("get: %v", err)
	}
	err := db.LoadCount(ctx, &posts, "Comments", playsql.As("approved_count"),
		playsql.Constrain(func(q *playsql.Builder) { q.WhereEq("approved", true) }))
	if err != nil {
		t.Fatalf("loadcount constrained: %v", err)
	}
	if v, _ := posts[0].Aggregate("approved_count"); toI(v) != 1 {
		t.Fatalf("hello approved_count = %v, want 1", v)
	}
}

func TestLoadCount_BelongsTo(t *testing.T) {
	db, _ := setupBlog(t)
	ctx := context.Background()

	var comments []exComment
	if err := db.Model(&exComment{}).Get(ctx, &comments); err != nil {
		t.Fatalf("get comments: %v", err)
	}
	if err := db.LoadCount(ctx, &comments, "Post"); err != nil {
		t.Fatalf("loadcount belongsTo: %v", err)
	}
	for _, c := range comments {
		if c.CountOf("Post") != 1 { // each comment belongs to one existing post
			t.Fatalf("comment %d post count = %d, want 1", c.ID, c.CountOf("Post"))
		}
	}
}

func TestLoadCount_BelongsToManyRejected(t *testing.T) {
	db := setupAuthorsBooks(t)
	ctx := context.Background()

	var authors []Author
	if err := db.Model(&Author{}).Get(ctx, &authors); err != nil {
		t.Fatalf("get authors: %v", err)
	}
	if err := db.LoadCount(ctx, &authors, "Books"); err == nil {
		t.Fatal("expected error: deferred aggregate on belongsToMany")
	}
}

func TestLoadCount_UnknownRelation(t *testing.T) {
	db, _ := setupAgg(t)
	var posts []aPost
	_ = db.Model(&aPost{}).Get(context.Background(), &posts)
	if err := db.LoadCount(context.Background(), &posts, "Nope"); err == nil {
		t.Fatal("expected error for unknown relation")
	}
}
