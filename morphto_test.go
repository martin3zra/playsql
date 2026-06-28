package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

type tPost struct {
	playsql.Model
	ID    int64  `db:"id" play:"pk,incrementing"`
	Title string `db:"title" play:"fillable"`
}

func (tPost) TableName() string { return "t_posts" }

type tVideo struct {
	playsql.Model
	ID   int64  `db:"id" play:"pk,incrementing"`
	Name string `db:"name" play:"fillable"`
}

func (tVideo) TableName() string { return "t_videos" }

type tComment struct {
	playsql.Model
	ID              int64  `db:"id" play:"pk,incrementing"`
	CommentableID   int64  `db:"commentable_id" play:"fillable"`
	CommentableType string `db:"commentable_type" play:"fillable"`
	Body            string `db:"body" play:"fillable"`
	Commentable     any    `play:"morphTo,morph=commentable"`
}

func (tComment) TableName() string { return "t_comments" }

func (tComment) MorphOwners() map[string]any {
	return map[string]any{"t_posts": &tPost{}, "video": &tVideo{}}
}

func setupMorphTo(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, s := range []string{
		`CREATE TABLE t_posts (id INTEGER PRIMARY KEY, title TEXT)`,
		`CREATE TABLE t_videos (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE t_comments (id INTEGER PRIMARY KEY, commentable_id INTEGER, commentable_type TEXT, body TEXT)`,
	} {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	mustInsert(t, db, &tPost{Title: "hello"})
	mustInsert(t, db, &tVideo{Name: "clip"})
	for _, c := range []tComment{
		{CommentableID: 1, CommentableType: "t_posts", Body: "on post"},
		{CommentableID: 1, CommentableType: "video", Body: "on video"},
		{CommentableID: 1, CommentableType: "unknown", Body: "orphan"},
	} {
		mustInsert(t, db, &c)
	}
	return db
}

func TestMorphTo_Eager(t *testing.T) {
	db := setupMorphTo(t)
	var comments []tComment
	if err := db.Model(&tComment{}).OrderBy("id", playsql.Asc).Get(context.Background(), &comments); err != nil {
		t.Fatalf("get: %v", err)
	}
	// not loaded yet
	if comments[0].Commentable != nil {
		t.Fatalf("Commentable should be nil before load")
	}

	if err := db.Model(&tComment{}).With("Commentable").OrderBy("id", playsql.Asc).Get(context.Background(), &comments); err != nil {
		t.Fatalf("eager: %v", err)
	}

	post, ok := comments[0].Commentable.(*tPost)
	if !ok || post.Title != "hello" {
		t.Fatalf("comment 0 owner = %#v, want *tPost{hello}", comments[0].Commentable)
	}
	video, ok := comments[1].Commentable.(*tVideo)
	if !ok || video.Name != "clip" {
		t.Fatalf("comment 1 owner = %#v, want *tVideo{clip}", comments[1].Commentable)
	}
	if comments[2].Commentable != nil { // unmapped type
		t.Fatalf("comment 2 owner should be nil (unknown type), got %#v", comments[2].Commentable)
	}
}

func TestMorphTo_Typed(t *testing.T) {
	db := setupMorphTo(t)
	got, err := playsql.Query[tComment](db).With("Commentable").OrderBy("id", playsql.Asc).Get(context.Background())
	if err != nil {
		t.Fatalf("typed eager: %v", err)
	}
	if p, ok := got[0].Commentable.(*tPost); !ok || p.Title != "hello" {
		t.Fatalf("typed owner wrong: %#v", got[0].Commentable)
	}
}

type tBad struct {
	playsql.Model
	ID            int64  `db:"id" play:"pk,incrementing"`
	CommentableID int64  `db:"commentable_id"`
	Commentable   any    `play:"morphTo,morph=commentable"`
	CType         string `db:"commentable_type"`
}

func (tBad) TableName() string { return "t_comments" }

func TestMorphTo_MissingMorphOwners(t *testing.T) {
	db := setupMorphTo(t)
	var rows []tBad
	err := db.Model(&tBad{}).With("Commentable").Get(context.Background(), &rows)
	if err == nil {
		t.Fatal("expected error: model lacks MorphOwners")
	}
}

func TestMorphTo_NestedRejected(t *testing.T) {
	db := setupMorphTo(t)
	var comments []tComment
	err := db.Model(&tComment{}).With("Commentable.Title").Get(context.Background(), &comments)
	if err == nil {
		t.Fatal("expected error: morphTo cannot be a nested segment")
	}
}
