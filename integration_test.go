//go:build integration

// Package-level integration suite exercising the same operations against live
// Postgres, MySQL, and SQL Server. Run with: make db-up && make test-int.
// A driver whose DSN is unreachable is skipped. Override DSNs via env:
//
//	PLAYSQL_POSTGRES_DSN, PLAYSQL_MYSQL_DSN, PLAYSQL_MSSQL_DSN
package playsql_test

import (
	"context"
	"fmt"
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
	ID         int64     `db:"id" play:"pk,incrementing"`
	Name       string    `db:"name"`
	Books      []*itBook `play:"hasMany,foreignKey=author_id"`
	BooksCount int64     `db:"books_count" play:"readonly"` // LoadCount target
	Notes      []*itNote `play:"morphMany,morph=notable"`
	NotesCount int64     `db:"notes_count" play:"readonly"` // WithCount target
}

type itNote struct {
	ID         int64  `db:"id" play:"pk,incrementing"`
	NotableID  int64  `db:"notable_id" play:"fillable"`
	NotableTyp string `db:"notable_type" play:"fillable"`
	Body       string `db:"body" play:"fillable"`
}

func (itNote) TableName() string { return "it_notes" }

// itLog holds a morphTo: each log belongs to an author or a widget.
type itLog struct {
	playsql.Model
	ID           int64  `db:"id" play:"pk,incrementing"`
	LoggableID   int64  `db:"loggable_id" play:"fillable"`
	LoggableType string `db:"loggable_type" play:"fillable"`
	Msg          string `db:"msg" play:"fillable"`
	Loggable     any    `play:"morphTo,morph=loggable"`
}

func (itLog) TableName() string { return "it_logs" }

func (itLog) MorphOwners() map[string]any {
	return map[string]any{"it_authors": &itAuthor{}, "it_widgets": &itWidget{}}
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

type itWidget struct {
	playsql.Model
	ID     int64      `db:"id" play:"pk,incrementing"`
	Name   string     `db:"name" play:"fillable"`
	Price  int64      `db:"price" play:"fillable"`
	Cheap  bool       `db:"cheap" play:"fillable"`
	Tags   []*itTag   `play:"belongsToMany,pivot=it_widget_tag"`
	Labels []*itLabel `play:"morphToMany,morph=labelable,pivot=it_labelables"`

	TagsCount int64 `db:"tags_count" play:"readonly"` // WithCount target (Strategy A)
}

type itLabel struct {
	playsql.Model
	ID   int64  `db:"id" play:"pk,incrementing"`
	Name string `db:"name" play:"fillable"`
}

func (itLabel) TableName() string { return "it_labels" }

func (itWidget) TableName() string { return "it_widgets" }

type itTag struct {
	ID   int64  `db:"id" play:"pk,incrementing"`
	Name string `db:"name" play:"fillable"`
}

func (itTag) TableName() string { return "it_tags" }

// Through chain: it_regions <- it_writers (it_region_id) <- it_articles (it_writer_id).
type itRegion struct {
	ID       int64        `db:"id" play:"pk,incrementing"`
	Name     string       `db:"name" play:"fillable"`
	Articles []*itArticle `play:"hasManyThrough,through=it_writers"`

	ArticlesCount int64 `db:"articles_count" play:"readonly"` // WithCount target (Strategy A)
}

func (itRegion) TableName() string { return "it_regions" }

type itWriter struct {
	ID       int64  `db:"id" play:"pk,incrementing"`
	RegionID int64  `db:"it_region_id" play:"fillable"`
	Name     string `db:"name" play:"fillable"`
}

func (itWriter) TableName() string { return "it_writers" }

type itArticle struct {
	ID       int64  `db:"id" play:"pk,incrementing"`
	WriterID int64  `db:"it_writer_id" play:"fillable"`
	Title    string `db:"title" play:"fillable"`
}

func (itArticle) TableName() string { return "it_articles" }

// Subquery select/order: destinations + flights.
type itDestination struct {
	playsql.Model
	ID         int64  `db:"id" play:"pk,incrementing"`
	Name       string `db:"name" play:"fillable"`
	LastFlight string `db:"last_flight" play:"readonly"` // AddSelect target
}

func (itDestination) TableName() string { return "it_destinations" }

type itFlight struct {
	playsql.Model
	ID            int64  `db:"id" play:"pk,incrementing"`
	DestinationID int64  `db:"destination_id" play:"fillable"`
	Name          string `db:"name" play:"fillable"`
	ArrivedAt     int64  `db:"arrived_at" play:"fillable"`
}

func (itFlight) TableName() string { return "it_flights" }

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
		`DROP TABLE IF EXISTS it_widgets`,
		`CREATE TABLE it_widgets (id BIGSERIAL PRIMARY KEY, name TEXT, price BIGINT, cheap BOOLEAN DEFAULT FALSE)`,
		`DROP TABLE IF EXISTS it_widget_tag`,
		`DROP TABLE IF EXISTS it_tags`,
		`CREATE TABLE it_tags (id BIGSERIAL PRIMARY KEY, name TEXT)`,
		`CREATE TABLE it_widget_tag (it_widget_id BIGINT, it_tag_id BIGINT)`,
		`DROP TABLE IF EXISTS it_labelables`,
		`DROP TABLE IF EXISTS it_labels`,
		`CREATE TABLE it_labels (id BIGSERIAL PRIMARY KEY, name TEXT)`,
		`CREATE TABLE it_labelables (it_label_id BIGINT, labelable_id BIGINT, labelable_type TEXT)`,
		`DROP TABLE IF EXISTS it_articles`,
		`DROP TABLE IF EXISTS it_writers`,
		`DROP TABLE IF EXISTS it_regions`,
		`CREATE TABLE it_regions (id BIGSERIAL PRIMARY KEY, name TEXT)`,
		`CREATE TABLE it_writers (id BIGSERIAL PRIMARY KEY, it_region_id BIGINT, name TEXT)`,
		`CREATE TABLE it_articles (id BIGSERIAL PRIMARY KEY, it_writer_id BIGINT, title TEXT)`,
		`DROP TABLE IF EXISTS it_flights`,
		`DROP TABLE IF EXISTS it_destinations`,
		`CREATE TABLE it_destinations (id BIGSERIAL PRIMARY KEY, name TEXT)`,
		`CREATE TABLE it_flights (id BIGSERIAL PRIMARY KEY, destination_id BIGINT, name TEXT, arrived_at BIGINT)`,
		`DROP TABLE IF EXISTS it_notes`,
		`CREATE TABLE it_notes (id BIGSERIAL PRIMARY KEY, notable_id BIGINT, notable_type TEXT, body TEXT)`,
		`DROP TABLE IF EXISTS it_logs`,
		`CREATE TABLE it_logs (id BIGSERIAL PRIMARY KEY, loggable_id BIGINT, loggable_type TEXT, msg TEXT)`,
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
		`DROP TABLE IF EXISTS it_widgets`,
		`CREATE TABLE it_widgets (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(191), price BIGINT, cheap BOOLEAN DEFAULT 0)`,
		`DROP TABLE IF EXISTS it_widget_tag`,
		`DROP TABLE IF EXISTS it_tags`,
		`CREATE TABLE it_tags (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(191))`,
		`CREATE TABLE it_widget_tag (it_widget_id BIGINT, it_tag_id BIGINT)`,
		`DROP TABLE IF EXISTS it_labelables`,
		`DROP TABLE IF EXISTS it_labels`,
		`CREATE TABLE it_labels (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(191))`,
		`CREATE TABLE it_labelables (it_label_id BIGINT, labelable_id BIGINT, labelable_type VARCHAR(191))`,
		`DROP TABLE IF EXISTS it_articles`,
		`DROP TABLE IF EXISTS it_writers`,
		`DROP TABLE IF EXISTS it_regions`,
		`CREATE TABLE it_regions (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(191))`,
		`CREATE TABLE it_writers (id BIGINT AUTO_INCREMENT PRIMARY KEY, it_region_id BIGINT, name VARCHAR(191))`,
		`CREATE TABLE it_articles (id BIGINT AUTO_INCREMENT PRIMARY KEY, it_writer_id BIGINT, title VARCHAR(191))`,
		`DROP TABLE IF EXISTS it_flights`,
		`DROP TABLE IF EXISTS it_destinations`,
		`CREATE TABLE it_destinations (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(191))`,
		`CREATE TABLE it_flights (id BIGINT AUTO_INCREMENT PRIMARY KEY, destination_id BIGINT, name VARCHAR(191), arrived_at BIGINT)`,
		`DROP TABLE IF EXISTS it_notes`,
		`CREATE TABLE it_notes (id BIGINT AUTO_INCREMENT PRIMARY KEY, notable_id BIGINT, notable_type VARCHAR(191), body VARCHAR(191))`,
		`DROP TABLE IF EXISTS it_logs`,
		`CREATE TABLE it_logs (id BIGINT AUTO_INCREMENT PRIMARY KEY, loggable_id BIGINT, loggable_type VARCHAR(191), msg VARCHAR(191))`,
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
		`DROP TABLE IF EXISTS it_widgets`,
		`CREATE TABLE it_widgets (id BIGINT IDENTITY(1,1) PRIMARY KEY, name NVARCHAR(191), price BIGINT, cheap BIT DEFAULT 0)`,
		`DROP TABLE IF EXISTS it_widget_tag`,
		`DROP TABLE IF EXISTS it_tags`,
		`CREATE TABLE it_tags (id BIGINT IDENTITY(1,1) PRIMARY KEY, name NVARCHAR(191))`,
		`CREATE TABLE it_widget_tag (it_widget_id BIGINT, it_tag_id BIGINT)`,
		`DROP TABLE IF EXISTS it_labelables`,
		`DROP TABLE IF EXISTS it_labels`,
		`CREATE TABLE it_labels (id BIGINT IDENTITY(1,1) PRIMARY KEY, name NVARCHAR(191))`,
		`CREATE TABLE it_labelables (it_label_id BIGINT, labelable_id BIGINT, labelable_type NVARCHAR(191))`,
		`DROP TABLE IF EXISTS it_articles`,
		`DROP TABLE IF EXISTS it_writers`,
		`DROP TABLE IF EXISTS it_regions`,
		`CREATE TABLE it_regions (id BIGINT IDENTITY(1,1) PRIMARY KEY, name NVARCHAR(191))`,
		`CREATE TABLE it_writers (id BIGINT IDENTITY(1,1) PRIMARY KEY, it_region_id BIGINT, name NVARCHAR(191))`,
		`CREATE TABLE it_articles (id BIGINT IDENTITY(1,1) PRIMARY KEY, it_writer_id BIGINT, title NVARCHAR(191))`,
		`DROP TABLE IF EXISTS it_flights`,
		`DROP TABLE IF EXISTS it_destinations`,
		`CREATE TABLE it_destinations (id BIGINT IDENTITY(1,1) PRIMARY KEY, name NVARCHAR(191))`,
		`CREATE TABLE it_flights (id BIGINT IDENTITY(1,1) PRIMARY KEY, destination_id BIGINT, name NVARCHAR(191), arrived_at BIGINT)`,
		`DROP TABLE IF EXISTS it_notes`,
		`CREATE TABLE it_notes (id BIGINT IDENTITY(1,1) PRIMARY KEY, notable_id BIGINT, notable_type NVARCHAR(191), body NVARCHAR(191))`,
		`DROP TABLE IF EXISTS it_logs`,
		`CREATE TABLE it_logs (id BIGINT IDENTITY(1,1) PRIMARY KEY, loggable_id BIGINT, loggable_type NVARCHAR(191), msg NVARCHAR(191))`,
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
			runRawReturningSuite(t, db, drv.dialect)
			runExistenceSuite(t, db)
			runSubquerySuite(t, db)
			runMorphSuite(t, db)
		})
	}
}

// runMorphSuite exercises morphMany (eager + existence + aggregate) against a
// live database. Author "A" gets two polymorphic notes (notable_type =
// "it_authors", the default alias).
func runMorphSuite(t *testing.T, db *playsql.DB) {
	ctx := context.Background()

	var a itAuthor
	if err := db.Model(&itAuthor{}).WhereEq("name", "A").First(ctx, &a); err != nil {
		t.Fatalf("find author A: %v", err)
	}
	for _, body := range []string{"n1", "n2"} {
		if err := db.Insert(ctx, &itNote{NotableID: a.ID, NotableTyp: "it_authors", Body: body}); err != nil {
			t.Fatalf("insert note: %v", err)
		}
	}
	// A decoy with a non-matching type must be ignored.
	if err := db.Insert(ctx, &itNote{NotableID: a.ID, NotableTyp: "other", Body: "x"}); err != nil {
		t.Fatalf("insert decoy: %v", err)
	}

	// Eager load.
	var authors []itAuthor
	if err := db.Model(&itAuthor{}).WhereEq("name", "A").With("Notes").Get(ctx, &authors); err != nil {
		t.Fatalf("eager notes: %v", err)
	}
	if len(authors) != 1 || len(authors[0].Notes) != 2 {
		t.Fatalf("Notes = %d, want 2 (decoy excluded)", len(authors[0].Notes))
	}

	// Existence.
	has, err := playsql.Query[itAuthor](db).Has("Notes").Get(ctx)
	if err != nil {
		t.Fatalf("has notes: %v", err)
	}
	if len(has) != 1 || has[0].Name != "A" {
		t.Fatalf("Has(Notes) = %+v, want [A]", has)
	}

	// Aggregate.
	counted, err := playsql.Query[itAuthor](db).WithCount("Notes").Get(ctx)
	if err != nil {
		t.Fatalf("withcount notes: %v", err)
	}
	for _, au := range counted {
		want := int64(0)
		if au.Name == "A" {
			want = 2
		}
		if au.NotesCount != want {
			t.Fatalf("author %q notes_count = %d, want %d", au.Name, au.NotesCount, want)
		}
	}

	// morphTo: logs that belong to an author or a widget, resolved via MorphOwners.
	var wa itWidget
	if err := db.Model(&itWidget{}).WhereEq("name", "a").First(ctx, &wa); err != nil {
		t.Fatalf("find widget a: %v", err)
	}
	if err := db.Insert(ctx, &itLog{LoggableID: a.ID, LoggableType: "it_authors", Msg: "m1"}); err != nil {
		t.Fatalf("insert log: %v", err)
	}
	if err := db.Insert(ctx, &itLog{LoggableID: wa.ID, LoggableType: "it_widgets", Msg: "m2"}); err != nil {
		t.Fatalf("insert log: %v", err)
	}
	logs, err := playsql.Query[itLog](db).With("Loggable").OrderBy("id", playsql.Asc).Get(ctx)
	if err != nil {
		t.Fatalf("morphto eager: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("want 2 logs, got %d", len(logs))
	}
	if au, ok := logs[0].Loggable.(*itAuthor); !ok || au.Name != "A" {
		t.Fatalf("log0 owner = %#v, want *itAuthor{A}", logs[0].Loggable)
	}
	if w, ok := logs[1].Loggable.(*itWidget); !ok || w.Name != "a" {
		t.Fatalf("log1 owner = %#v, want *itWidget{a}", logs[1].Loggable)
	}

	// morphToMany: label "hot" attached to widget "a" (and a decoy region row
	// that must be ignored by the labelable_type filter).
	if err := db.Insert(ctx, &itLabel{Name: "hot"}); err != nil {
		t.Fatalf("insert label: %v", err)
	}
	var hot itLabel
	if err := db.Model(&itLabel{}).WhereEq("name", "hot").First(ctx, &hot); err != nil {
		t.Fatalf("find label: %v", err)
	}
	if _, err := db.Exec(ctx, fmt.Sprintf(
		"INSERT INTO it_labelables (it_label_id, labelable_id, labelable_type) VALUES (%d, %d, 'it_widgets'), (%d, 999, 'it_regions')",
		hot.ID, wa.ID, hot.ID)); err != nil {
		t.Fatalf("link labelable: %v", err)
	}
	labeled, err := playsql.Query[itWidget](db).With("Labels").Get(ctx)
	if err != nil {
		t.Fatalf("morphtomany eager: %v", err)
	}
	for _, w := range labeled {
		want := 0
		if w.Name == "a" {
			want = 1
		}
		if len(w.Labels) != want {
			t.Fatalf("widget %q labels = %d, want %d", w.Name, len(w.Labels), want)
		}
	}
}

// runSubquerySuite exercises AddSelect and OrderBySubquery (correlated subquery
// select and ordering) against a live database.
func runSubquerySuite(t *testing.T, db *playsql.DB) {
	ctx := context.Background()

	nyc := &itDestination{Name: "NYC"}
	lax := &itDestination{Name: "LAX"}
	for _, d := range []*itDestination{nyc, lax} {
		if err := db.Insert(ctx, d); err != nil {
			t.Fatalf("insert destination: %v", err)
		}
	}
	for _, f := range []*itFlight{
		{DestinationID: nyc.ID, Name: "DL100", ArrivedAt: 10},
		{DestinationID: nyc.ID, Name: "DL200", ArrivedAt: 30}, // latest for NYC
		{DestinationID: lax.ID, Name: "AA1", ArrivedAt: 20},
	} {
		if err := db.Insert(ctx, f); err != nil {
			t.Fatalf("insert flight: %v", err)
		}
	}

	sub := func(col string) *playsql.Builder {
		return db.Model(&itFlight{}).Select(col).
			WhereColumn("destination_id", "=", "it_destinations.id").
			OrderBy("arrived_at", playsql.Desc).Limit(1)
	}

	// AddSelect: each destination's most recent flight name.
	withLast, err := playsql.Query[itDestination](db).
		AddSelect("last_flight", sub("name")).
		OrderBy("id", playsql.Asc).Get(ctx)
	if err != nil {
		t.Fatalf("addselect: %v", err)
	}
	if len(withLast) != 2 || withLast[0].LastFlight != "DL200" || withLast[1].LastFlight != "AA1" {
		t.Fatalf("AddSelect(last_flight) wrong: %+v", withLast)
	}

	// OrderBySubquery: sort by latest arrival desc -> NYC (30) before LAX (20).
	ordered, err := playsql.Query[itDestination](db).
		OrderBySubquery(sub("arrived_at"), playsql.Desc).Get(ctx)
	if err != nil {
		t.Fatalf("orderbysubquery: %v", err)
	}
	if len(ordered) != 2 || ordered[0].Name != "NYC" || ordered[1].Name != "LAX" {
		t.Fatalf("OrderBySubquery wrong: %+v", ordered)
	}
}

// runExistenceSuite exercises Has/DoesntHave/WhereHas/HasCount (correlated
// EXISTS) against a live database. It builds on runSuite's state: author "A"
// with two books, and no other author has books.
func runExistenceSuite(t *testing.T, db *playsql.DB) {
	ctx := context.Background()

	if err := db.Insert(ctx, &itAuthor{Name: "Childless"}); err != nil {
		t.Fatalf("insert childless author: %v", err)
	}

	withBooks, err := playsql.Query[itAuthor](db).Has("Books").Get(ctx)
	if err != nil {
		t.Fatalf("has books: %v", err)
	}
	if len(withBooks) != 1 || withBooks[0].Name != "A" {
		t.Fatalf("Has(Books) wrong: %+v", withBooks)
	}

	none, err := playsql.Query[itAuthor](db).DoesntHave("Books").Get(ctx)
	if err != nil {
		t.Fatalf("doesnthave books: %v", err)
	}
	if len(none) != 1 || none[0].Name != "Childless" {
		t.Fatalf("DoesntHave(Books) wrong: %+v", none)
	}

	two, err := playsql.Query[itAuthor](db).HasCount("Books", ">=", 2).Get(ctx)
	if err != nil {
		t.Fatalf("hascount books: %v", err)
	}
	if len(two) != 1 || two[0].Name != "A" {
		t.Fatalf("HasCount(Books,>=,2) wrong: %+v", two)
	}

	whereHas, err := playsql.Query[itAuthor](db).
		WhereHas("Books", func(q *playsql.Builder) { q.Where("title", "like", "b%") }).
		Get(ctx)
	if err != nil {
		t.Fatalf("wherehas books: %v", err)
	}
	if len(whereHas) != 1 || whereHas[0].Name != "A" {
		t.Fatalf("WhereHas(Books) wrong: %+v", whereHas)
	}

	// belongsToMany existence: tag exactly one widget, via the it_widget_tag pivot.
	if err := db.Insert(ctx, &itTag{Name: "featured"}); err != nil {
		t.Fatalf("insert tag: %v", err)
	}
	var wa itWidget
	if err := db.Model(&itWidget{}).WhereEq("name", "a").First(ctx, &wa); err != nil {
		t.Fatalf("find widget a: %v", err)
	}
	var tag itTag
	if err := db.Model(&itTag{}).WhereEq("name", "featured").First(ctx, &tag); err != nil {
		t.Fatalf("find tag: %v", err)
	}
	// ids are trusted integers — inline to stay dialect-placeholder-agnostic.
	if _, err := db.Exec(ctx, fmt.Sprintf(
		"INSERT INTO it_widget_tag (it_widget_id, it_tag_id) VALUES (%d, %d)", wa.ID, tag.ID)); err != nil {
		t.Fatalf("link pivot: %v", err)
	}

	tagged, err := playsql.Query[itWidget](db).Has("Tags").Get(ctx)
	if err != nil {
		t.Fatalf("has tags: %v", err)
	}
	if len(tagged) != 1 || tagged[0].Name != "a" {
		t.Fatalf("Has(Tags) wrong: %+v", tagged)
	}

	untagged, err := playsql.Query[itWidget](db).DoesntHave("Tags").Get(ctx)
	if err != nil {
		t.Fatalf("doesnthave tags: %v", err)
	}
	if len(untagged) != 2 {
		t.Fatalf("DoesntHave(Tags) want 2, got %d", len(untagged))
	}

	whereHasTag, err := playsql.Query[itWidget](db).
		WhereHas("Tags", func(q *playsql.Builder) { q.WhereEq("name", "featured") }).
		Get(ctx)
	if err != nil {
		t.Fatalf("wherehas tags: %v", err)
	}
	if len(whereHasTag) != 1 || whereHasTag[0].Name != "a" {
		t.Fatalf("WhereHas(Tags) wrong: %+v", whereHasTag)
	}

	// has*Through existence: North has 3 articles (writers A+B), South 1 (C),
	// East none.
	north := &itRegion{Name: "North"}
	south := &itRegion{Name: "South"}
	east := &itRegion{Name: "East"}
	for _, r := range []*itRegion{north, south, east} {
		if err := db.Insert(ctx, r); err != nil {
			t.Fatalf("insert region: %v", err)
		}
	}
	wA := &itWriter{RegionID: north.ID, Name: "A"}
	wB := &itWriter{RegionID: north.ID, Name: "B"}
	wC := &itWriter{RegionID: south.ID, Name: "C"}
	for _, w := range []*itWriter{wA, wB, wC} {
		if err := db.Insert(ctx, w); err != nil {
			t.Fatalf("insert writer: %v", err)
		}
	}
	for _, a := range []*itArticle{
		{WriterID: wA.ID, Title: "a1"}, {WriterID: wA.ID, Title: "a2"},
		{WriterID: wB.ID, Title: "b1"}, {WriterID: wC.ID, Title: "c1"},
	} {
		if err := db.Insert(ctx, a); err != nil {
			t.Fatalf("insert article: %v", err)
		}
	}

	hasArticles, err := playsql.Query[itRegion](db).Has("Articles").Get(ctx)
	if err != nil {
		t.Fatalf("has articles: %v", err)
	}
	if len(hasArticles) != 2 {
		t.Fatalf("Has(Articles) want 2 (North,South), got %d", len(hasArticles))
	}

	noArticles, err := playsql.Query[itRegion](db).DoesntHave("Articles").Get(ctx)
	if err != nil {
		t.Fatalf("doesnthave articles: %v", err)
	}
	if len(noArticles) != 1 || noArticles[0].Name != "East" {
		t.Fatalf("DoesntHave(Articles) wrong: %+v", noArticles)
	}

	// Exact far-row count: only North has >= 3 articles (2 writers notwithstanding).
	three, err := playsql.Query[itRegion](db).HasCount("Articles", ">=", 3).Get(ctx)
	if err != nil {
		t.Fatalf("hascount articles: %v", err)
	}
	if len(three) != 1 || three[0].Name != "North" {
		t.Fatalf("HasCount(Articles,>=,3) wrong: %+v", three)
	}

	whereHasArticle, err := playsql.Query[itRegion](db).
		WhereHas("Articles", func(q *playsql.Builder) { q.Where("title", "like", "a%") }).
		Get(ctx)
	if err != nil {
		t.Fatalf("wherehas articles: %v", err)
	}
	if len(whereHasArticle) != 1 || whereHasArticle[0].Name != "North" {
		t.Fatalf("WhereHas(Articles like a%%) wrong: %+v", whereHasArticle)
	}

	// Aggregate columns (WithCount) — Strategy A fields populated in one query.
	// belongsToMany via pivot: widget "a" has 1 tag, others 0.
	wc, err := playsql.Query[itWidget](db).WithCount("Tags").Get(ctx)
	if err != nil {
		t.Fatalf("withcount tags: %v", err)
	}
	for _, w := range wc {
		want := int64(0)
		if w.Name == "a" {
			want = 1
		}
		if w.TagsCount != want {
			t.Fatalf("widget %q TagsCount = %d, want %d", w.Name, w.TagsCount, want)
		}
	}

	// has*Through: North 3 articles, South 1, East 0.
	ac, err := playsql.Query[itRegion](db).WithCount("Articles").Get(ctx)
	if err != nil {
		t.Fatalf("withcount articles: %v", err)
	}
	wantArticles := map[string]int64{"North": 3, "South": 1, "East": 0}
	for _, r := range ac {
		if w, ok := wantArticles[r.Name]; ok && r.ArticlesCount != w {
			t.Fatalf("region %q ArticlesCount = %d, want %d", r.Name, r.ArticlesCount, w)
		}
	}

	// Deferred aggregate loading (post-fetch) on hasMany: author "A" has 2 books,
	// "Childless" has 0.
	var authors []itAuthor
	if err := db.Model(&itAuthor{}).Get(ctx, &authors); err != nil {
		t.Fatalf("get authors: %v", err)
	}
	if err := db.LoadCount(ctx, &authors, "Books"); err != nil {
		t.Fatalf("loadcount books: %v", err)
	}
	wantBooks := map[string]int64{"A": 2, "Childless": 0}
	for _, a := range authors {
		if w, ok := wantBooks[a.Name]; ok && a.BooksCount != w {
			t.Fatalf("author %q BooksCount = %d, want %d", a.Name, a.BooksCount, w)
		}
	}
}

// runRawReturningSuite exercises Raw/RawQuery, UpdateReturning (RETURNING /
// OUTPUT), and CTE updates (WithCTE + WhereRaw) against a live database.
func runRawReturningSuite(t *testing.T, db *playsql.DB, dialect string) {
	ctx := context.Background()

	seed := []map[string]any{
		{"name": "a", "price": int64(5)},
		{"name": "b", "price": int64(50)},
		{"name": "c", "price": int64(500)},
	}
	if _, err := db.Model(&itWidget{}).InsertMany(ctx, seed); err != nil {
		t.Fatalf("seed widgets: %v", err)
	}

	// Raw read into a slice.
	var pricey []itWidget
	if err := db.Raw(ctx, &pricey, `SELECT id, name, price FROM it_widgets WHERE price >= 50 ORDER BY price`); err != nil {
		t.Fatalf("raw: %v", err)
	}
	if len(pricey) != 2 || pricey[0].Name != "b" || pricey[1].Name != "c" {
		t.Fatalf("raw rows wrong: %+v", pricey)
	}

	// Generic RawQuery.
	all, err := playsql.RawQuery[itWidget](db, ctx, `SELECT * FROM it_widgets ORDER BY id`)
	if err != nil {
		t.Fatalf("rawquery: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("rawquery count: %d", len(all))
	}

	// RawScalar: single value.
	count, err := playsql.RawScalar[int64](db, ctx, `SELECT COUNT(*) FROM it_widgets`)
	if err != nil {
		t.Fatalf("rawscalar: %v", err)
	}
	if count != 3 {
		t.Fatalf("rawscalar count = %d, want 3", count)
	}

	// RawRows: manual scan over the raw result.
	rrows, err := db.RawRows(ctx, `SELECT name FROM it_widgets ORDER BY price`)
	if err != nil {
		t.Fatalf("rawrows: %v", err)
	}
	var names []string
	for rrows.Next() {
		var name string
		if err := rrows.Scan(&name); err != nil {
			rrows.Close()
			t.Fatalf("rawrows scan: %v", err)
		}
		names = append(names, name)
	}
	rrows.Close()
	if len(names) != 3 || names[0] != "a" {
		t.Fatalf("rawrows wrong: %v", names)
	}

	// UpdateReturning: RETURNING (pg/sqlite) or OUTPUT (mssql). MySQL has none.
	if dialect == "mysql" {
		var dest []itWidget
		err := db.Model(&itWidget{}).WhereEq("name", "a").
			Returning("id").UpdateReturning(ctx, map[string]any{"price": int64(7)}, &dest)
		if err == nil {
			t.Fatal("mysql UpdateReturning should error (no RETURNING)")
		}
	} else {
		var updated []itWidget
		err := db.Model(&itWidget{}).Where("price", ">", int64(40)).
			Returning("id", "name", "price").
			UpdateReturning(ctx, map[string]any{"cheap": true}, &updated)
		if err != nil {
			t.Fatalf("update returning: %v", err)
		}
		if len(updated) != 2 {
			t.Fatalf("want 2 returned, got %d: %+v", len(updated), updated)
		}
		for _, w := range updated {
			if w.Name == "" || w.Price <= 40 {
				t.Fatalf("returned row not hydrated: %+v", w)
			}
		}
	}

	// CTE update: mark rows below the average price as cheap.
	if _, err := db.Model(&itWidget{}).Update(ctx, map[string]any{"cheap": false}); err != nil {
		t.Fatalf("reset cheap: %v", err)
	}
	n, err := db.Model(&itWidget{}).
		WithCTE("avg_price", "SELECT AVG(price) AS value FROM it_widgets").
		WhereRaw("price < (SELECT value FROM avg_price)").
		Update(ctx, map[string]any{"cheap": true})
	if err != nil {
		t.Fatalf("cte update: %v", err)
	}
	if n != 2 { // avg(5,50,500)=185; 5 and 50 are below
		t.Fatalf("cte update affected %d, want 2", n)
	}

	// Bound CTE (WithCTEQuery): the subquery carries a placeholder that must be
	// renumbered ahead of the SET/WHERE binds. cheap_ids = price < 600 (all);
	// update those also priced > 40 -> the 50 and 500 widgets.
	cheapIDs := db.Model(&itWidget{}).Select("id").Where("price", "<", int64(600))
	bn, err := db.Model(&itWidget{}).
		WithCTEQuery("cheap_ids", cheapIDs).
		WhereRaw("id IN (SELECT id FROM cheap_ids)").
		Where("price", ">", int64(40)).
		Update(ctx, map[string]any{"cheap": false})
	if err != nil {
		t.Fatalf("bound cte update: %v", err)
	}
	if bn != 2 {
		t.Fatalf("bound cte update affected %d, want 2", bn)
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
