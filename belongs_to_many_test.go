package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

// Account2/Role with a many-to-many via the conventional pivot "account2_role"
// ... but to keep the pivot name predictable we use explicit names.

type Author struct {
	ID    int64   `db:"id" play:"pk,incrementing"`
	Name  string  `db:"name"`
	Books []*Book `play:"belongsToMany,pivot=author_book"`
}

func (Author) TableName() string { return "authors" }

type Book struct {
	ID    int64  `db:"id" play:"pk,incrementing"`
	Title string `db:"title"`
}

func (Book) TableName() string { return "books" }

func setupAuthorsBooks(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	stmts := []string{
		`CREATE TABLE authors (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE books (id INTEGER PRIMARY KEY, title TEXT)`,
		`CREATE TABLE author_book (author_id INTEGER, book_id INTEGER)`,
		`INSERT INTO authors (id, name) VALUES (1,'Ann'),(2,'Bob'),(3,'Cy')`,
		`INSERT INTO books (id, title) VALUES (1,'Go'),(2,'SQL'),(3,'ORM')`,
		// Ann: Go, SQL ; Bob: SQL, ORM ; Cy: none
		`INSERT INTO author_book (author_id, book_id) VALUES (1,1),(1,2),(2,2),(2,3)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	return db
}

func TestBelongsToMany_Eager(t *testing.T) {
	db := setupAuthorsBooks(t)

	var authors []Author
	if err := db.Model(&Author{}).With("Books").OrderBy("id", playsql.Asc).Get(context.Background(), &authors); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(authors) != 3 {
		t.Fatalf("want 3 authors, got %d", len(authors))
	}

	if got := titles(authors[0].Books); !eq(got, []string{"Go", "SQL"}) {
		t.Fatalf("Ann books = %v, want [Go SQL]", got)
	}
	if got := titles(authors[1].Books); !eq(got, []string{"SQL", "ORM"}) {
		t.Fatalf("Bob books = %v, want [SQL ORM]", got)
	}
	if len(authors[2].Books) != 0 {
		t.Fatalf("Cy should have no books, got %d", len(authors[2].Books))
	}
}

func TestBelongsToMany_OnFind(t *testing.T) {
	db := setupAuthorsBooks(t)

	var a Author
	if err := db.Model(&Author{}).With("Books").Find(context.Background(), &a, int64(2)); err != nil {
		t.Fatalf("find: %v", err)
	}
	if got := titles(a.Books); !eq(got, []string{"SQL", "ORM"}) {
		t.Fatalf("Bob books = %v, want [SQL ORM]", got)
	}
}

func titles(books []*Book) []string {
	out := make([]string, len(books))
	for i, b := range books {
		out[i] = b.Title
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	// order-independent compare
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
	}
	for _, v := range seen {
		if v != 0 {
			return false
		}
	}
	return true
}
