package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

// Country has many Cities; City has many Landmarks. Used for nested eager load.
type Country struct {
	ID     int64   `db:"id" play:"pk,incrementing"`
	Name   string  `db:"name"`
	Cities []*City `play:"hasMany"`
}

func (Country) TableName() string { return "countries" }

type City struct {
	ID        int64       `db:"id" play:"pk,incrementing"`
	CountryID int64       `db:"country_id"`
	Name      string      `db:"name"`
	Landmarks []*Landmark `play:"hasMany"`
}

func (City) TableName() string { return "cities" }

type Landmark struct {
	ID     int64  `db:"id" play:"pk,incrementing"`
	CityID int64  `db:"city_id"`
	Name   string `db:"name"`
	Famous bool   `db:"famous"`
}

func (Landmark) TableName() string { return "landmarks" }

func setupGeo(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	stmts := []string{
		`CREATE TABLE countries (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE cities (id INTEGER PRIMARY KEY, country_id INTEGER, name TEXT)`,
		`CREATE TABLE landmarks (id INTEGER PRIMARY KEY, city_id INTEGER, name TEXT, famous INTEGER)`,
		`INSERT INTO countries (id, name) VALUES (1,'France'),(2,'Japan')`,
		`INSERT INTO cities (id, country_id, name) VALUES (1,1,'Paris'),(2,2,'Tokyo')`,
		`INSERT INTO landmarks (id, city_id, name, famous) VALUES
			(1,1,'Eiffel Tower',1),(2,1,'Side Alley',0),
			(3,2,'Tokyo Tower',1),(4,2,'Random Corner',0)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	return db
}

func TestNestedEagerLoad(t *testing.T) {
	db := setupGeo(t)

	var countries []Country
	err := db.Model(&Country{}).
		With("Cities.Landmarks").
		OrderBy("id", playsql.Asc).
		Get(context.Background(), &countries)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if len(countries) != 2 {
		t.Fatalf("want 2 countries, got %d", len(countries))
	}
	france := countries[0]
	if len(france.Cities) != 1 || france.Cities[0].Name != "Paris" {
		t.Fatalf("France cities wrong: %+v", france.Cities)
	}
	// Nested: Paris landmarks loaded two levels deep.
	if len(france.Cities[0].Landmarks) != 2 {
		t.Fatalf("Paris want 2 landmarks, got %d", len(france.Cities[0].Landmarks))
	}
}

func TestConstraintClosure(t *testing.T) {
	db := setupGeo(t)

	var cities []City
	// Only famous landmarks.
	err := db.Model(&City{}).
		WithConstraint("Landmarks", func(q *playsql.Builder) {
			q.WhereEq("famous", true)
		}).
		OrderBy("id", playsql.Asc).
		Get(context.Background(), &cities)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	for _, c := range cities {
		if len(c.Landmarks) != 1 {
			t.Fatalf("%s: want 1 famous landmark, got %d", c.Name, len(c.Landmarks))
		}
		if !c.Landmarks[0].Famous {
			t.Fatalf("%s: non-famous landmark leaked: %+v", c.Name, c.Landmarks[0])
		}
	}
}

func TestNestedWithConstraint(t *testing.T) {
	db := setupGeo(t)

	var countries []Country
	// Cities.Landmarks but only famous landmarks (constraint on deepest segment).
	err := db.Model(&Country{}).
		WithConstraint("Cities.Landmarks", func(q *playsql.Builder) {
			q.WhereEq("famous", true)
		}).
		OrderBy("id", playsql.Asc).
		Get(context.Background(), &countries)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	paris := countries[0].Cities[0]
	if len(paris.Landmarks) != 1 || !paris.Landmarks[0].Famous {
		t.Fatalf("nested constraint failed: %+v", paris.Landmarks)
	}
}
