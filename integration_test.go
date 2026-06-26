//go:build integration

// Package-level integration suite exercising the same operations against live
// Postgres, MySQL, and SQL Server. Run with: make db-up && make test-int.
// A driver whose DSN is unreachable is skipped. Override DSNs via env:
//
//	PLAYSQL_POSTGRES_DSN, PLAYSQL_MYSQL_DSN, PLAYSQL_MSSQL_DSN
package playsql_test

import (
	"context"
	"os"
	"testing"

	"github.com/martin3zra/playsql"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"
)

type itPerson struct {
	playsql.Model
	ID   int64  `db:"id" play:"pk,incrementing"`
	Name string `db:"name" play:"fillable"`
	Age  int64  `db:"age" play:"fillable"`
}

func (itPerson) TableName() string { return "it_people" }

type itAuthor struct {
	ID    int64     `db:"id" play:"pk,incrementing"`
	Name  string    `db:"name"`
	Books []*itBook `play:"hasMany,foreignKey=author_id"`
}

func (itAuthor) TableName() string { return "it_authors" }

type itBook struct {
	ID       int64     `db:"id" play:"pk,incrementing"`
	AuthorID int64     `db:"author_id"`
	Title    string    `db:"title"`
	Author   *itAuthor `play:"belongsTo,foreignKey=author_id"`
}

func (itBook) TableName() string { return "it_books" }

type itPrefs struct {
	Theme string `json:"theme"`
}

type itProfile struct {
	ID    int64   `db:"id" play:"pk,incrementing"`
	Name  string  `db:"name"`
	Prefs itPrefs `db:"prefs" play:"cast=json"`
}

func (itProfile) TableName() string { return "it_profiles" }

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type itDriver struct {
	name    string
	driver  playsql.Driver
	dsn     string
	dialect string
}

func itDrivers() []itDriver {
	// Defaults target the already-running camel containers; override via env.
	return []itDriver{
		{"postgres", playsql.Postgres, env("PLAYSQL_POSTGRES_DSN", "postgres://postgres:secret@localhost:5433/camel?sslmode=disable"), "postgres"},
		{"mysql", playsql.MySQL, env("PLAYSQL_MYSQL_DSN", "root:secret@tcp(localhost:3306)/camel?parseTime=true"), "mysql"},
		{"mssql", playsql.MSSQL, env("PLAYSQL_MSSQL_DSN", "sqlserver://sa:Camel_Test_123@localhost:1433?database=camel_test&encrypt=disable&TrustServerCertificate=true"), "mssql"},
	}
}

// ddl holds idempotent schema per dialect (drop + create).
var ddl = map[string][]string{
	"postgres": {
		`DROP TABLE IF EXISTS it_books`,
		`DROP TABLE IF EXISTS it_authors`,
		`DROP TABLE IF EXISTS it_people`,
		`CREATE TABLE it_people (id BIGSERIAL PRIMARY KEY, name TEXT UNIQUE, age BIGINT)`,
		`CREATE TABLE it_authors (id BIGSERIAL PRIMARY KEY, name TEXT)`,
		`CREATE TABLE it_books (id BIGSERIAL PRIMARY KEY, author_id BIGINT, title TEXT)`,
		`DROP TABLE IF EXISTS it_profiles`,
		`CREATE TABLE it_profiles (id BIGSERIAL PRIMARY KEY, name TEXT, prefs JSONB)`,
	},
	"mysql": {
		`DROP TABLE IF EXISTS it_books`,
		`DROP TABLE IF EXISTS it_authors`,
		`DROP TABLE IF EXISTS it_people`,
		`CREATE TABLE it_people (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(191) UNIQUE, age BIGINT)`,
		`CREATE TABLE it_authors (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(191))`,
		`CREATE TABLE it_books (id BIGINT AUTO_INCREMENT PRIMARY KEY, author_id BIGINT, title VARCHAR(191))`,
		`DROP TABLE IF EXISTS it_profiles`,
		`CREATE TABLE it_profiles (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(191), prefs JSON)`,
	},
	"mssql": {
		`DROP TABLE IF EXISTS it_books`,
		`DROP TABLE IF EXISTS it_authors`,
		`DROP TABLE IF EXISTS it_people`,
		`CREATE TABLE it_people (id BIGINT IDENTITY(1,1) PRIMARY KEY, name NVARCHAR(191) UNIQUE, age BIGINT)`,
		`CREATE TABLE it_authors (id BIGINT IDENTITY(1,1) PRIMARY KEY, name NVARCHAR(191))`,
		`CREATE TABLE it_books (id BIGINT IDENTITY(1,1) PRIMARY KEY, author_id BIGINT, title NVARCHAR(191))`,
		`DROP TABLE IF EXISTS it_profiles`,
		`CREATE TABLE it_profiles (id BIGINT IDENTITY(1,1) PRIMARY KEY, name NVARCHAR(191), prefs NVARCHAR(MAX))`,
	},
}

func TestIntegration(t *testing.T) {
	for _, drv := range itDrivers() {
		drv := drv
		t.Run(drv.name, func(t *testing.T) {
			db, err := playsql.Open(playsql.Config{Driver: drv.driver, Source: drv.dsn})
			if err != nil {
				t.Skipf("%s unreachable (%v) — start it with `make db-up`", drv.name, err)
			}
			t.Cleanup(func() { db.Close() })

			ctx := context.Background()
			for _, stmt := range ddl[drv.dialect] {
				if _, err := db.Exec(ctx, stmt); err != nil {
					t.Fatalf("schema: %v\n  %s", err, stmt)
				}
			}
			runSuite(t, db)
		})
	}
}

func runSuite(t *testing.T, db *playsql.DB) {
	ctx := context.Background()

	// Insert + generated id + Save(update).
	p := &itPerson{Name: "Jane", Age: 30}
	if err := db.Insert(ctx, p); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("generated id not set")
	}
	p.Age = 31
	if err := db.Save(ctx, p); err != nil {
		t.Fatalf("save: %v", err)
	}

	var got itPerson
	if err := db.Model(&itPerson{}).Find(ctx, &got, p.ID); err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Age != 31 {
		t.Fatalf("update not applied: %+v", got)
	}

	// where + count + order.
	if err := db.Insert(ctx, &itPerson{Name: "Bob", Age: 25}); err != nil {
		t.Fatalf("insert2: %v", err)
	}
	n, err := db.Model(&itPerson{}).Where("age", ">", int64(26)).Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 over 26, got %d", n)
	}
	var all []itPerson
	if err := db.Model(&itPerson{}).OrderBy("age", playsql.Asc).Get(ctx, &all); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(all) != 2 || all[0].Name != "Bob" {
		t.Fatalf("order/get wrong: %+v", all)
	}

	// upsert (conflict on unique name) — no duplicate, value updated.
	if _, err := db.Model(&itPerson{}).Upsert(ctx,
		[]map[string]any{{"name": "Jane", "age": int64(40)}},
		[]string{"name"}, []string{"age"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var jane itPerson
	if err := db.Model(&itPerson{}).WhereEq("name", "Jane").First(ctx, &jane); err != nil {
		t.Fatalf("first jane: %v", err)
	}
	if jane.Age != 40 {
		t.Fatalf("upsert did not update: %+v", jane)
	}
	if c, _ := db.Model(&itPerson{}).Count(ctx); c != 2 {
		t.Fatalf("upsert duplicated rows: count=%d", c)
	}

	// delete.
	if err := db.Delete(ctx, &jane); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if c, _ := db.Model(&itPerson{}).Count(ctx); c != 1 {
		t.Fatalf("delete failed: count=%d", c)
	}

	// relations: hasMany + belongsTo, eager loaded.
	a := &itAuthor{Name: "A"}
	if err := db.Insert(ctx, a); err != nil {
		t.Fatalf("insert author: %v", err)
	}
	for _, title := range []string{"b1", "b2"} {
		if err := db.Insert(ctx, &itBook{AuthorID: a.ID, Title: title}); err != nil {
			t.Fatalf("insert book: %v", err)
		}
	}

	var authors []itAuthor
	if err := db.Model(&itAuthor{}).With("Books").Get(ctx, &authors); err != nil {
		t.Fatalf("with books: %v", err)
	}
	if len(authors) != 1 || len(authors[0].Books) != 2 {
		t.Fatalf("hasMany eager load wrong: %+v", authors)
	}

	var books []itBook
	if err := db.Model(&itBook{}).With("Author").OrderBy("id", playsql.Asc).Get(ctx, &books); err != nil {
		t.Fatalf("with author: %v", err)
	}
	if len(books) != 2 || books[0].Author == nil || books[0].Author.Name != "A" {
		t.Fatalf("belongsTo eager load wrong: %+v", books)
	}

	// JSON cast round-trip + WhereJSON (dialect-specific extract syntax).
	if err := db.Insert(ctx, &itProfile{Name: "x", Prefs: itPrefs{Theme: "dark"}}); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if err := db.Insert(ctx, &itProfile{Name: "y", Prefs: itPrefs{Theme: "light"}}); err != nil {
		t.Fatalf("insert profile2: %v", err)
	}
	var dark []itProfile
	if err := db.Model(&itProfile{}).WhereJSON("prefs", "theme", "=", "dark").Get(ctx, &dark); err != nil {
		t.Fatalf("where json: %v", err)
	}
	if len(dark) != 1 || dark[0].Name != "x" || dark[0].Prefs.Theme != "dark" {
		t.Fatalf("WhereJSON/json cast wrong: %+v", dark)
	}
}
