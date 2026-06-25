package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

type Prefs struct {
	Theme string `json:"theme"`
	Beta  bool   `json:"beta"`
}

// Profile has JSON-cast columns: a struct, a slice, and a map.
type Profile struct {
	ID    int64          `db:"id" play:"pk,incrementing"`
	Name  string         `db:"name"`
	Prefs Prefs          `db:"prefs" play:"cast=json"`
	Tags  []string       `db:"tags" play:"cast=json"`
	Extra map[string]int `db:"extra" play:"cast=json"`
}

func (Profile) TableName() string { return "profiles" }

func setupProfiles(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(context.Background(),
		`CREATE TABLE profiles (id INTEGER PRIMARY KEY, name TEXT, prefs TEXT, tags TEXT, extra TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	return db
}

func TestJSONCast_RoundTrip(t *testing.T) {
	db := setupProfiles(t)
	ctx := context.Background()

	p := &Profile{
		Name:  "Jane",
		Prefs: Prefs{Theme: "dark", Beta: true},
		Tags:  []string{"a", "b"},
		Extra: map[string]int{"x": 1},
	}
	if err := db.Insert(ctx, p); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got Profile
	if err := db.Model(&Profile{}).Find(ctx, &got, p.ID); err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Prefs.Theme != "dark" || !got.Prefs.Beta {
		t.Fatalf("struct cast round-trip failed: %+v", got.Prefs)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "a" {
		t.Fatalf("slice cast round-trip failed: %+v", got.Tags)
	}
	if got.Extra["x"] != 1 {
		t.Fatalf("map cast round-trip failed: %+v", got.Extra)
	}
}

// profileRaw reads the prefs column as a plain string (no cast) to confirm the
// JSON encoding actually stored in the database.
type profileRaw struct {
	ID    int64  `db:"id" play:"pk,incrementing"`
	Prefs string `db:"prefs"`
}

func (profileRaw) TableName() string { return "profiles" }

func TestJSONCast_StoredAsText(t *testing.T) {
	db := setupProfiles(t)
	ctx := context.Background()

	p := &Profile{Name: "Jane", Prefs: Prefs{Theme: "light"}}
	if err := db.Insert(ctx, p); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var raw profileRaw
	if err := db.Model(&profileRaw{}).Find(ctx, &raw, p.ID); err != nil {
		t.Fatalf("find raw: %v", err)
	}
	if raw.Prefs != `{"theme":"light","beta":false}` {
		t.Fatalf("prefs not stored as expected JSON: %q", raw.Prefs)
	}
}

func TestJSONCast_NullLeavesZero(t *testing.T) {
	db := setupProfiles(t)
	ctx := context.Background()

	// Insert a row with NULL json columns via raw SQL.
	if _, err := db.Exec(ctx, `INSERT INTO profiles (id, name, prefs, tags, extra) VALUES (1,'Bob',NULL,NULL,NULL)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var got Profile
	if err := db.Model(&Profile{}).Find(ctx, &got, int64(1)); err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Prefs.Theme != "" || got.Tags != nil || got.Extra != nil {
		t.Fatalf("NULL json should leave zero values: %+v", got)
	}
}

func TestJSONCast_Update(t *testing.T) {
	db := setupProfiles(t)
	ctx := context.Background()

	p := &Profile{Name: "Jane", Tags: []string{"a"}}
	if err := db.Insert(ctx, p); err != nil {
		t.Fatalf("insert: %v", err)
	}

	p.Tags = []string{"a", "b", "c"}
	if err := db.Update(ctx, p); err != nil {
		t.Fatalf("update: %v", err)
	}

	var got Profile
	if err := db.Model(&Profile{}).Find(ctx, &got, p.ID); err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got.Tags) != 3 {
		t.Fatalf("json update failed: %+v", got.Tags)
	}
}
