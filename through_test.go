package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

// Region has many Articles through Writers:
// regions <- writers (writers.region_id) <- articles (articles.writer_id).
type Region struct {
	ID       int64      `db:"id" play:"pk,incrementing"`
	Name     string     `db:"name"`
	Articles []*Article `play:"hasManyThrough,through=writers"`
	Lead     *Article   `play:"hasOneThrough,through=writers"`
}

func (Region) TableName() string { return "regions" }

type Article struct {
	ID       int64  `db:"id" play:"pk,incrementing"`
	WriterID int64  `db:"writer_id"`
	Title    string `db:"title"`
}

func (Article) TableName() string { return "articles" }

func setupThrough(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	stmts := []string{
		`CREATE TABLE regions (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE writers (id INTEGER PRIMARY KEY, region_id INTEGER, name TEXT)`,
		`CREATE TABLE articles (id INTEGER PRIMARY KEY, writer_id INTEGER, title TEXT)`,
		`INSERT INTO regions (id, name) VALUES (1,'North'),(2,'South')`,
		// North has writers 1,2 ; South has writer 3
		`INSERT INTO writers (id, region_id, name) VALUES (1,1,'A'),(2,1,'B'),(3,2,'C')`,
		// writer 1: 2 articles, writer 2: 1, writer 3: 1
		`INSERT INTO articles (id, writer_id, title) VALUES (1,1,'a1'),(2,1,'a2'),(3,2,'b1'),(4,3,'c1')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	return db
}

func TestHasManyThrough(t *testing.T) {
	db := setupThrough(t)

	var regions []Region
	if err := db.Model(&Region{}).With("Articles").OrderBy("id", playsql.Asc).Get(context.Background(), &regions); err != nil {
		t.Fatalf("get: %v", err)
	}
	// North: writers 1+2 -> 3 articles; South: writer 3 -> 1 article.
	if len(regions[0].Articles) != 3 {
		t.Fatalf("North want 3 articles, got %d", len(regions[0].Articles))
	}
	if len(regions[1].Articles) != 1 || regions[1].Articles[0].Title != "c1" {
		t.Fatalf("South want [c1], got %+v", regions[1].Articles)
	}
}

func TestHasOneThrough(t *testing.T) {
	db := setupThrough(t)

	var regions []Region
	if err := db.Model(&Region{}).With("Lead").OrderBy("id", playsql.Asc).Get(context.Background(), &regions); err != nil {
		t.Fatalf("get: %v", err)
	}
	if regions[0].Lead == nil {
		t.Fatal("North should have a lead article")
	}
	if regions[1].Lead == nil || regions[1].Lead.Title != "c1" {
		t.Fatalf("South lead should be c1, got %+v", regions[1].Lead)
	}
}

// --- has*Through existence ---

func regionNames(r []Region) map[string]bool {
	m := map[string]bool{}
	for _, x := range r {
		m[x.Name] = true
	}
	return m
}

func TestHas_Through(t *testing.T) {
	db := setupThrough(t)
	ctx := context.Background()
	// A region with no writers (hence no articles).
	if _, err := db.Exec(ctx, `INSERT INTO regions (id, name) VALUES (3,'East')`); err != nil {
		t.Fatalf("insert region: %v", err)
	}

	got, err := playsql.Query[Region](db).Has("Articles").Get(ctx)
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	names := regionNames(got)
	if len(names) != 2 || !names["North"] || !names["South"] {
		t.Fatalf("Has(Articles) = %v, want North+South", names)
	}
}

func TestDoesntHave_Through(t *testing.T) {
	db := setupThrough(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO regions (id, name) VALUES (3,'East')`); err != nil {
		t.Fatalf("insert region: %v", err)
	}

	got, err := playsql.Query[Region](db).DoesntHave("Articles").Get(ctx)
	if err != nil {
		t.Fatalf("doesnthave: %v", err)
	}
	if len(got) != 1 || got[0].Name != "East" {
		t.Fatalf("DoesntHave(Articles) = %v, want [East]", regionNames(got))
	}
}

func TestWhereHas_Through(t *testing.T) {
	db := setupThrough(t)
	// Regions having an article titled like "a%": only North (a1,a2).
	got, err := playsql.Query[Region](db).
		WhereHas("Articles", func(q *playsql.Builder) { q.Where("title", "like", "a%") }).
		Get(context.Background())
	if err != nil {
		t.Fatalf("wherehas: %v", err)
	}
	if len(got) != 1 || got[0].Name != "North" {
		t.Fatalf("WhereHas(Articles like a%%) = %v, want [North]", regionNames(got))
	}
}

func TestHasCount_Through_ExactFarCount(t *testing.T) {
	db := setupThrough(t)
	// North: 3 articles (writers 1+2). South: 1 (writer 3).
	// >= 3 must count FAR rows, not intermediate writers (North has 2 writers),
	// so only North qualifies.
	got, err := playsql.Query[Region](db).HasCount("Articles", ">=", 3).Get(context.Background())
	if err != nil {
		t.Fatalf("hascount: %v", err)
	}
	if len(got) != 1 || got[0].Name != "North" {
		t.Fatalf("HasCount(Articles,>=,3) = %v, want [North]", regionNames(got))
	}

	// >= 1 includes both.
	atLeastOne, err := playsql.Query[Region](db).HasCount("Articles", ">=", 1).Get(context.Background())
	if err != nil {
		t.Fatalf("hascount >=1: %v", err)
	}
	if len(atLeastOne) != 2 {
		t.Fatalf("HasCount(Articles,>=,1) want 2, got %d", len(atLeastOne))
	}
}

func TestHas_OneThrough(t *testing.T) {
	db := setupThrough(t)
	// hasOneThrough behaves identically for existence.
	got, err := playsql.Query[Region](db).Has("Lead").Get(context.Background())
	if err != nil {
		t.Fatalf("has lead: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Has(Lead) want 2, got %d", len(got))
	}
}
