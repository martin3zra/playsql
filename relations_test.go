package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

// Blog has many Comments and one Owner; Comment belongs to Blog.
type Blog struct {
	ID       int64      `db:"id" play:"pk,incrementing"`
	Title    string     `db:"title"`
	Comments []*Comment `play:"hasMany"`
	Cover    *Cover     `play:"hasOne"`
}

func (Blog) TableName() string { return "blogs" }

type Comment struct {
	ID     int64  `db:"id" play:"pk,incrementing"`
	BlogID int64  `db:"blog_id"`
	Body   string `db:"body"`
	Blog   *Blog  `play:"belongsTo"`
}

func (Comment) TableName() string { return "comments" }

type Cover struct {
	ID     int64  `db:"id" play:"pk,incrementing"`
	BlogID int64  `db:"blog_id"`
	URL    string `db:"url"`
}

func (Cover) TableName() string { return "covers" }

func setupBlogs(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	stmts := []string{
		`CREATE TABLE blogs (id INTEGER PRIMARY KEY, title TEXT)`,
		`CREATE TABLE comments (id INTEGER PRIMARY KEY, blog_id INTEGER, body TEXT)`,
		`CREATE TABLE covers (id INTEGER PRIMARY KEY, blog_id INTEGER, url TEXT)`,
		`INSERT INTO blogs (id, title) VALUES (1,'First'),(2,'Second')`,
		`INSERT INTO comments (id, blog_id, body) VALUES (1,1,'a'),(2,1,'b'),(3,2,'c')`,
		`INSERT INTO covers (id, blog_id, url) VALUES (1,1,'cover1.png')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	return db
}

func TestHasMany_Eager(t *testing.T) {
	db := setupBlogs(t)

	var blogs []Blog
	if err := db.Model(&Blog{}).With("Comments").OrderBy("id", playsql.Asc).Get(context.Background(), &blogs); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(blogs) != 2 {
		t.Fatalf("want 2 blogs, got %d", len(blogs))
	}
	if len(blogs[0].Comments) != 2 {
		t.Fatalf("blog 1 want 2 comments, got %d", len(blogs[0].Comments))
	}
	if len(blogs[1].Comments) != 1 {
		t.Fatalf("blog 2 want 1 comment, got %d", len(blogs[1].Comments))
	}
	if blogs[0].Comments[0].Body == "" {
		t.Fatal("comment not hydrated")
	}
}

func TestHasOne_Eager(t *testing.T) {
	db := setupBlogs(t)

	var blogs []Blog
	if err := db.Model(&Blog{}).With("Cover").OrderBy("id", playsql.Asc).Get(context.Background(), &blogs); err != nil {
		t.Fatalf("get: %v", err)
	}
	if blogs[0].Cover == nil || blogs[0].Cover.URL != "cover1.png" {
		t.Fatalf("blog 1 cover not loaded: %+v", blogs[0].Cover)
	}
	if blogs[1].Cover != nil {
		t.Fatalf("blog 2 has no cover, got %+v", blogs[1].Cover)
	}
}

func TestBelongsTo_Eager(t *testing.T) {
	db := setupBlogs(t)

	var comments []Comment
	if err := db.Model(&Comment{}).With("Blog").OrderBy("id", playsql.Asc).Get(context.Background(), &comments); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(comments) != 3 {
		t.Fatalf("want 3 comments, got %d", len(comments))
	}
	if comments[0].Blog == nil || comments[0].Blog.Title != "First" {
		t.Fatalf("comment 1 blog not loaded: %+v", comments[0].Blog)
	}
	if comments[2].Blog == nil || comments[2].Blog.Title != "Second" {
		t.Fatalf("comment 3 blog not loaded: %+v", comments[2].Blog)
	}
}

func TestBelongsTo_OnFirst(t *testing.T) {
	db := setupBlogs(t)

	var c Comment
	if err := db.Model(&Comment{}).With("Blog").Find(context.Background(), &c, int64(3)); err != nil {
		t.Fatalf("find: %v", err)
	}
	if c.Blog == nil || c.Blog.Title != "Second" {
		t.Fatalf("blog not eager-loaded on Find: %+v", c.Blog)
	}
}

func TestWith_UnknownRelation(t *testing.T) {
	db := setupBlogs(t)
	var blogs []Blog
	err := db.Model(&Blog{}).With("Nope").Get(context.Background(), &blogs)
	if err == nil {
		t.Fatal("expected error for unknown relation")
	}
}
