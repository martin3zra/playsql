package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

type mmPost struct {
	playsql.Model
	ID    int64    `db:"id" play:"pk,incrementing"`
	Title string   `db:"title" play:"fillable"`
	Tags  []*mmTag `play:"morphToMany,morph=taggable,pivot=taggables"`
}

func (mmPost) TableName() string { return "mm_posts" }

type mmVideo struct {
	playsql.Model
	ID   int64    `db:"id" play:"pk,incrementing"`
	Name string   `db:"name" play:"fillable"`
	Tags []*mmTag `play:"morphToMany,morph=taggable,pivot=taggables"`
}

func (mmVideo) TableName() string { return "mm_videos" }

type mmTag struct {
	playsql.Model
	ID    int64     `db:"id" play:"pk,incrementing"`
	Name  string    `db:"name" play:"fillable"`
	Posts []*mmPost `play:"morphedByMany,morph=taggable,pivot=taggables"`
}

func (mmTag) TableName() string { return "mm_tags" }

func setupMorphToMany(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, s := range []string{
		`CREATE TABLE mm_posts (id INTEGER PRIMARY KEY, title TEXT)`,
		`CREATE TABLE mm_videos (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE mm_tags (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE taggables (mm_tag_id INTEGER, taggable_id INTEGER, taggable_type TEXT)`,
		`INSERT INTO mm_posts (id,title) VALUES (1,'hello'),(2,'world')`,
		`INSERT INTO mm_videos (id,name) VALUES (1,'clip')`,
		`INSERT INTO mm_tags (id,name) VALUES (1,'go'),(2,'sql')`,
		// tag1<->post1, tag2<->post1, tag1<->video1, tag1<->post2
		`INSERT INTO taggables (mm_tag_id,taggable_id,taggable_type) VALUES
			(1,1,'mm_posts'),(2,1,'mm_posts'),(1,1,'mm_videos'),(1,2,'mm_posts')`,
	} {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	return db
}

func tagNames(tags []*mmTag) map[string]bool {
	m := map[string]bool{}
	for _, x := range tags {
		m[x.Name] = true
	}
	return m
}

func TestMorphToMany_Owning(t *testing.T) {
	db := setupMorphToMany(t)
	posts, err := playsql.Query[mmPost](db).With("Tags").OrderBy("id", playsql.Asc).Get(context.Background())
	if err != nil {
		t.Fatalf("eager: %v", err)
	}
	// post1: go + sql; post2: go.
	if n := tagNames(posts[0].Tags); len(n) != 2 || !n["go"] || !n["sql"] {
		t.Fatalf("post1 tags = %v, want go+sql", n)
	}
	if n := tagNames(posts[1].Tags); len(n) != 1 || !n["go"] {
		t.Fatalf("post2 tags = %v, want go", n)
	}
}

func TestMorphToMany_TypeDiscrimination(t *testing.T) {
	db := setupMorphToMany(t)
	// video1 is tagged "go" only; the post pivots must not leak in.
	videos, err := playsql.Query[mmVideo](db).With("Tags").Get(context.Background())
	if err != nil {
		t.Fatalf("eager video: %v", err)
	}
	if n := tagNames(videos[0].Tags); len(n) != 1 || !n["go"] {
		t.Fatalf("video tags = %v, want [go]", n)
	}
}

func TestMorphedByMany_Inverse(t *testing.T) {
	db := setupMorphToMany(t)
	var tags []mmTag
	if err := db.Model(&mmTag{}).With("Posts").OrderBy("id", playsql.Asc).Get(context.Background(), &tags); err != nil {
		t.Fatalf("eager inverse: %v", err)
	}
	// tag1 (go): post1 + post2; tag2 (sql): post1. Videos excluded by type.
	titles := func(ps []*mmPost) map[string]bool {
		m := map[string]bool{}
		for _, p := range ps {
			m[p.Title] = true
		}
		return m
	}
	if n := titles(tags[0].Posts); len(n) != 2 || !n["hello"] || !n["world"] {
		t.Fatalf("tag1 posts = %v, want hello+world", n)
	}
	if n := titles(tags[1].Posts); len(n) != 1 || !n["hello"] {
		t.Fatalf("tag2 posts = %v, want hello", n)
	}
}
