package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

type Destination struct {
	playsql.Model
	ID         int64  `db:"id" play:"pk,incrementing"`
	Name       string `db:"name" play:"fillable"`
	LastFlight string `db:"last_flight" play:"readonly"` // AddSelect target (Strategy A)
}

func (Destination) TableName() string { return "destinations" }

type Flight struct {
	playsql.Model
	ID            int64  `db:"id" play:"pk,incrementing"`
	DestinationID int64  `db:"destination_id" play:"fillable"`
	Name          string `db:"name" play:"fillable"`
	ArrivedAt     int64  `db:"arrived_at" play:"fillable"`
}

func (Flight) TableName() string { return "flights" }

// setupFlights: NYC has flights DL100@10, DL200@30 (latest); LAX has AA1@20.
func setupFlights(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, s := range []string{
		`CREATE TABLE destinations (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE flights (id INTEGER PRIMARY KEY, destination_id INTEGER, name TEXT, arrived_at INTEGER)`,
	} {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	for _, d := range []Destination{{Name: "NYC"}, {Name: "LAX"}} {
		if err := db.Insert(ctx, &d); err != nil {
			t.Fatalf("insert dest: %v", err)
		}
	}
	for _, f := range []Flight{
		{DestinationID: 1, Name: "DL100", ArrivedAt: 10},
		{DestinationID: 1, Name: "DL200", ArrivedAt: 30},
		{DestinationID: 2, Name: "AA1", ArrivedAt: 20},
	} {
		if err := db.Insert(ctx, &f); err != nil {
			t.Fatalf("insert flight: %v", err)
		}
	}
	return db
}

func lastFlightSub(db *playsql.DB, col string) *playsql.Builder {
	return db.Model(&Flight{}).Select(col).
		WhereColumn("destination_id", "=", "destinations.id").
		OrderBy("arrived_at", playsql.Desc).Limit(1)
}

func TestAddSelect_StrategyA(t *testing.T) {
	db := setupFlights(t)
	got, err := playsql.Query[Destination](db).
		AddSelect("last_flight", lastFlightSub(db, "name")).
		OrderBy("id", playsql.Asc).
		Get(context.Background())
	if err != nil {
		t.Fatalf("addselect: %v", err)
	}
	if len(got) != 2 || got[0].LastFlight != "DL200" || got[1].LastFlight != "AA1" {
		t.Fatalf("last_flight wrong: %+v", got)
	}
}

func TestAddSelect_StrategyB(t *testing.T) {
	db := setupFlights(t)
	// alias with no matching field -> aggregate bag.
	got, err := playsql.Query[Destination](db).
		AddSelect("recent_flight", lastFlightSub(db, "name")).
		OrderBy("id", playsql.Asc).
		Get(context.Background())
	if err != nil {
		t.Fatalf("addselect B: %v", err)
	}
	if v, _ := got[0].Aggregate("recent_flight"); v != "DL200" {
		t.Fatalf("NYC recent_flight = %v, want DL200", v)
	}
}

func TestOrderBySubquery(t *testing.T) {
	db := setupFlights(t)
	// Sort destinations by their latest flight arrival, descending: NYC (30) then
	// LAX (20).
	got, err := playsql.Query[Destination](db).
		OrderBySubquery(lastFlightSub(db, "arrived_at"), playsql.Desc).
		Get(context.Background())
	if err != nil {
		t.Fatalf("orderbysubquery: %v", err)
	}
	if len(got) != 2 || got[0].Name != "NYC" || got[1].Name != "LAX" {
		t.Fatalf("order wrong: %+v", got)
	}
}

func TestWhereColumn_Public(t *testing.T) {
	db := setupFlights(t)
	// flights whose name is not tied to arrival ordering — just exercise the
	// public WhereColumn on a self-comparison that's always true.
	var flights []Flight
	err := db.Model(&Flight{}).WhereColumn("destination_id", "=", "destination_id").Get(context.Background(), &flights)
	if err != nil {
		t.Fatalf("wherecolumn: %v", err)
	}
	if len(flights) != 3 {
		t.Fatalf("want 3 flights, got %d", len(flights))
	}
}

func TestAddSelect_PlainQueryStillWorks(t *testing.T) {
	// readonly last_flight must not break a plain select.
	db := setupFlights(t)
	var dests []Destination
	if err := db.Model(&Destination{}).Get(context.Background(), &dests); err != nil {
		t.Fatalf("plain get: %v", err)
	}
	if len(dests) != 2 || dests[0].LastFlight != "" {
		t.Fatalf("unexpected: %+v", dests)
	}
}
