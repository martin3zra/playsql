package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

type exPost struct {
	playsql.Model
	ID       int64        `db:"id" play:"pk,incrementing"`
	Title    string       `db:"title" play:"fillable"`
	Comments []*exComment `play:"hasMany,foreignKey=post_id"`
}

func (exPost) TableName() string { return "ex_posts" }

type exComment struct {
	playsql.Model
	ID       int64      `db:"id" play:"pk,incrementing"`
	PostID   int64      `db:"post_id" play:"fillable"`
	Content  string     `db:"content" play:"fillable"`
	Approved bool       `db:"approved" play:"fillable"`
	Images   []*exImage `play:"hasMany,foreignKey=comment_id"`
	Post     *exPost    `play:"belongsTo,foreignKey=post_id"`
}

func (exComment) TableName() string { return "ex_comments" }

type exImage struct {
	playsql.Model
	ID        int64  `db:"id" play:"pk,incrementing"`
	CommentID int64  `db:"comment_id" play:"fillable"`
	URL       string `db:"url" play:"fillable"`
}

func (exImage) TableName() string { return "ex_images" }

// setupBlog builds posts/comments/images:
//   - p1 "hello": 2 comments ("code review" approved [has image], "nice" not)
//   - p2 "empty": no comments
//   - p3 "spam":  1 comment ("buy now" not approved, no image)
func setupBlog(t *testing.T) (*playsql.DB, map[string]int64) {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TABLE ex_posts (id INTEGER PRIMARY KEY, title TEXT)`,
		`CREATE TABLE ex_comments (id INTEGER PRIMARY KEY, post_id INTEGER, content TEXT, approved BOOLEAN)`,
		`CREATE TABLE ex_images (id INTEGER PRIMARY KEY, comment_id INTEGER, url TEXT)`,
	} {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}

	ids := map[string]int64{}
	for _, title := range []string{"hello", "empty", "spam"} {
		p := &exPost{Title: title}
		if err := db.Insert(ctx, p); err != nil {
			t.Fatalf("insert post: %v", err)
		}
		ids[title] = p.ID
	}
	mk := func(postID int64, content string, approved bool) int64 {
		c := &exComment{PostID: postID, Content: content, Approved: approved}
		if err := db.Insert(ctx, c); err != nil {
			t.Fatalf("insert comment: %v", err)
		}
		return c.ID
	}
	codeID := mk(ids["hello"], "code review", true)
	mk(ids["hello"], "nice", false)
	mk(ids["spam"], "buy now", false)
	if err := db.Insert(ctx, &exImage{CommentID: codeID, URL: "x.png"}); err != nil {
		t.Fatalf("insert image: %v", err)
	}
	return db, ids
}

func postTitles(posts []exPost) []string {
	out := make([]string, len(posts))
	for i, p := range posts {
		out[i] = p.Title
	}
	return out
}

func eqPosts(t *testing.T, got []exPost, want ...string) {
	t.Helper()
	g := postTitles(got)
	if len(g) != len(want) {
		t.Fatalf("got %v, want %v", g, want)
	}
	set := map[string]bool{}
	for _, w := range want {
		set[w] = true
	}
	for _, x := range g {
		if !set[x] {
			t.Fatalf("got %v, want %v", g, want)
		}
	}
}

func TestHas(t *testing.T) {
	db, _ := setupBlog(t)
	got, err := playsql.Query[exPost](db).Has("Comments").Get(context.Background())
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	eqPosts(t, got, "hello", "spam")
}

func TestDoesntHave(t *testing.T) {
	db, _ := setupBlog(t)
	got, err := playsql.Query[exPost](db).DoesntHave("Comments").Get(context.Background())
	if err != nil {
		t.Fatalf("doesnthave: %v", err)
	}
	eqPosts(t, got, "empty")
}

func TestWhereHas(t *testing.T) {
	db, _ := setupBlog(t)
	got, err := playsql.Query[exPost](db).
		WhereHas("Comments", func(q *playsql.Builder) {
			q.Where("content", "like", "code%")
		}).Get(context.Background())
	if err != nil {
		t.Fatalf("wherehas: %v", err)
	}
	eqPosts(t, got, "hello")
}

func TestHasCount(t *testing.T) {
	db, _ := setupBlog(t)
	got, err := playsql.Query[exPost](db).HasCount("Comments", ">=", 2).Get(context.Background())
	if err != nil {
		t.Fatalf("hascount: %v", err)
	}
	eqPosts(t, got, "hello")
}

func TestWhereRelation(t *testing.T) {
	db, _ := setupBlog(t)
	got, err := playsql.Query[exPost](db).
		WhereRelation("Comments", "approved", "=", true).Get(context.Background())
	if err != nil {
		t.Fatalf("whererelation: %v", err)
	}
	eqPosts(t, got, "hello")
}

func TestWhereDoesntHave(t *testing.T) {
	db, _ := setupBlog(t)
	// Posts without an approved comment: empty (none) + spam (only unapproved).
	got, err := playsql.Query[exPost](db).
		WhereDoesntHave("Comments", func(q *playsql.Builder) {
			q.WhereEq("approved", true)
		}).Get(context.Background())
	if err != nil {
		t.Fatalf("wheredoesnthave: %v", err)
	}
	eqPosts(t, got, "empty", "spam")
}

func TestNestedHas(t *testing.T) {
	db, _ := setupBlog(t)
	// Posts with a comment that has an image: only "hello".
	got, err := playsql.Query[exPost](db).Has("Comments.Images").Get(context.Background())
	if err != nil {
		t.Fatalf("nested has: %v", err)
	}
	eqPosts(t, got, "hello")
}

func TestHasBelongsTo(t *testing.T) {
	db, ids := setupBlog(t)
	// Comments whose parent post exists (all of them, here 3).
	got, err := playsql.Query[exComment](db).Has("Post").Get(context.Background())
	if err != nil {
		t.Fatalf("has belongsTo: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 comments with a post, got %d", len(got))
	}
	_ = ids
}

func TestHas_UnknownRelation(t *testing.T) {
	db, _ := setupBlog(t)
	_, err := playsql.Query[exPost](db).Has("Nope").Get(context.Background())
	if err == nil {
		t.Fatal("expected error for unknown relation")
	}
}

func TestHasCount_NestedRejected(t *testing.T) {
	db, _ := setupBlog(t)
	_, err := playsql.Query[exPost](db).HasCount("Comments.Images", ">=", 1).Get(context.Background())
	if err == nil {
		t.Fatal("expected error: count form on nested relation")
	}
}

// --- belongsToMany existence (Author/Book + author_book pivot from setupAuthorsBooks) ---

func authorNames(a []Author) map[string]bool {
	m := map[string]bool{}
	for _, x := range a {
		m[x.Name] = true
	}
	return m
}

func TestHas_BelongsToMany(t *testing.T) {
	db := setupAuthorsBooks(t)
	// Ann(Go,SQL), Bob(SQL,ORM), Cy(none).
	got, err := playsql.Query[Author](db).Has("Books").Get(context.Background())
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	names := authorNames(got)
	if len(names) != 2 || !names["Ann"] || !names["Bob"] {
		t.Fatalf("Has(Books) = %v, want Ann+Bob", names)
	}
}

func TestDoesntHave_BelongsToMany(t *testing.T) {
	db := setupAuthorsBooks(t)
	got, err := playsql.Query[Author](db).DoesntHave("Books").Get(context.Background())
	if err != nil {
		t.Fatalf("doesnthave: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Cy" {
		t.Fatalf("DoesntHave(Books) = %v, want [Cy]", authorNames(got))
	}
}

func TestWhereHas_BelongsToMany(t *testing.T) {
	db := setupAuthorsBooks(t)
	// Authors having the book "Go": only Ann.
	got, err := playsql.Query[Author](db).
		WhereHas("Books", func(q *playsql.Builder) { q.WhereEq("title", "Go") }).
		Get(context.Background())
	if err != nil {
		t.Fatalf("wherehas: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Ann" {
		t.Fatalf("WhereHas(Books title=Go) = %v, want [Ann]", authorNames(got))
	}
}

func TestHasCount_BelongsToMany(t *testing.T) {
	db := setupAuthorsBooks(t)
	// Authors with >= 2 books: Ann and Bob.
	got, err := playsql.Query[Author](db).HasCount("Books", ">=", 2).Get(context.Background())
	if err != nil {
		t.Fatalf("hascount: %v", err)
	}
	names := authorNames(got)
	if len(names) != 2 || !names["Ann"] || !names["Bob"] {
		t.Fatalf("HasCount(Books,>=,2) = %v, want Ann+Bob", names)
	}
}

func TestWhereRelation_BelongsToMany(t *testing.T) {
	db := setupAuthorsBooks(t)
	got, err := playsql.Query[Author](db).
		WhereRelation("Books", "title", "=", "ORM").Get(context.Background())
	if err != nil {
		t.Fatalf("whererelation: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Bob" {
		t.Fatalf("WhereRelation(Books title=ORM) = %v, want [Bob]", authorNames(got))
	}
}
