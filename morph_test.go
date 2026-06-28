package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

// mPost uses the default morph alias (its table name, "m_posts").
type mPost struct {
	playsql.Model
	ID     int64     `db:"id" play:"pk,incrementing"`
	Title  string    `db:"title" play:"fillable"`
	Images []*mImage `play:"morphMany,morph=imageable"`
}

func (mPost) TableName() string { return "m_posts" }

// mVideo overrides the alias via MorphType -> "video".
type mVideo struct {
	playsql.Model
	ID     int64     `db:"id" play:"pk,incrementing"`
	Name   string    `db:"name" play:"fillable"`
	Images []*mImage `play:"morphMany,morph=imageable"`
}

func (mVideo) TableName() string { return "m_videos" }
func (mVideo) MorphType() string { return "video" }

type mUser struct {
	playsql.Model
	ID     int64   `db:"id" play:"pk,incrementing"`
	Name   string  `db:"name" play:"fillable"`
	Avatar *mImage `play:"morphOne,morph=imageable"`
}

func (mUser) TableName() string { return "m_users" }

type mImage struct {
	playsql.Model
	ID            int64  `db:"id" play:"pk,incrementing"`
	ImageableID   int64  `db:"imageable_id" play:"fillable"`
	ImageableType string `db:"imageable_type" play:"fillable"`
	URL           string `db:"url" play:"fillable"`
}

func (mImage) TableName() string { return "m_images" }

func setupMorph(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, s := range []string{
		`CREATE TABLE m_posts (id INTEGER PRIMARY KEY, title TEXT)`,
		`CREATE TABLE m_videos (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE m_users (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE m_images (id INTEGER PRIMARY KEY, imageable_id INTEGER, imageable_type TEXT, url TEXT)`,
	} {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	mustInsert(t, db, &mPost{Title: "hello"})
	mustInsert(t, db, &mVideo{Name: "clip"})
	mustInsert(t, db, &mUser{Name: "al"})
	for _, img := range []mImage{
		{ImageableID: 1, ImageableType: "m_posts", URL: "p1"}, // post 1
		{ImageableID: 1, ImageableType: "m_posts", URL: "p2"}, // post 1
		{ImageableID: 1, ImageableType: "video", URL: "v1"},   // video 1 (custom alias)
		{ImageableID: 1, ImageableType: "m_users", URL: "av"}, // user 1 avatar
		{ImageableID: 1, ImageableType: "m_videos", URL: "x"}, // decoy: matches nothing
	} {
		mustInsert(t, db, &img)
	}
	return db
}

func mustInsert(t *testing.T, db *playsql.DB, m any) {
	t.Helper()
	if err := db.Insert(context.Background(), m); err != nil {
		t.Fatalf("insert %T: %v", m, err)
	}
}

func TestMorphMany_Eager(t *testing.T) {
	db := setupMorph(t)
	got, err := playsql.Query[mPost](db).With("Images").Get(context.Background())
	if err != nil {
		t.Fatalf("eager: %v", err)
	}
	if len(got) != 1 || len(got[0].Images) != 2 {
		t.Fatalf("post images = %d, want 2 (decoy excluded): %+v", len(got[0].Images), got)
	}
}

func TestMorphMany_CustomAlias(t *testing.T) {
	db := setupMorph(t)
	got, err := playsql.Query[mVideo](db).With("Images").Get(context.Background())
	if err != nil {
		t.Fatalf("eager video: %v", err)
	}
	if len(got) != 1 || len(got[0].Images) != 1 || got[0].Images[0].URL != "v1" {
		t.Fatalf("video images wrong (custom alias): %+v", got)
	}
}

func TestMorphOne_Eager(t *testing.T) {
	db := setupMorph(t)
	got, err := playsql.Query[mUser](db).With("Avatar").Get(context.Background())
	if err != nil {
		t.Fatalf("eager avatar: %v", err)
	}
	if len(got) != 1 || got[0].Avatar == nil || got[0].Avatar.URL != "av" {
		t.Fatalf("avatar wrong: %+v", got)
	}
}

func TestMorph_Has(t *testing.T) {
	db := setupMorph(t)
	// Add a post with no images; it must be excluded by Has.
	mustInsert(t, db, &mPost{Title: "lonely"})
	got, err := playsql.Query[mPost](db).Has("Images").Get(context.Background())
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if len(got) != 1 || got[0].Title != "hello" {
		t.Fatalf("Has(Images) = %+v, want [hello]", got)
	}
}

func TestMorph_WithCount(t *testing.T) {
	db := setupMorph(t)
	got, err := playsql.Query[mPost](db).WithCount("Images").Get(context.Background())
	if err != nil {
		t.Fatalf("withcount: %v", err)
	}
	if got[0].CountOf("Images") != 2 {
		t.Fatalf("post images_count = %d, want 2", got[0].CountOf("Images"))
	}
}

func TestMorph_LoadCount(t *testing.T) {
	db := setupMorph(t)
	var posts []mPost
	if err := db.Model(&mPost{}).Get(context.Background(), &posts); err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := db.LoadCount(context.Background(), &posts, "Images"); err != nil {
		t.Fatalf("loadcount: %v", err)
	}
	if posts[0].CountOf("Images") != 2 {
		t.Fatalf("loaded count = %d, want 2", posts[0].CountOf("Images"))
	}
}
