package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

// Account3 stores a JSON-cast settings column we then query into.
type Account3 struct {
	ID       int64    `db:"id" play:"pk,incrementing"`
	Name     string   `db:"name"`
	Settings Settings `db:"settings" play:"cast=json"`
}

type Settings struct {
	Theme  string `json:"theme"`
	Region string `json:"region"`
}

func (Account3) TableName() string { return "account3s" }

func TestWhereJSON_Integration(t *testing.T) {
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	if _, err := db.Exec(ctx, `CREATE TABLE account3s (id INTEGER PRIMARY KEY, name TEXT, settings TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	seed := []*Account3{
		{Name: "Ann", Settings: Settings{Theme: "dark", Region: "us"}},
		{Name: "Bob", Settings: Settings{Theme: "light", Region: "eu"}},
		{Name: "Cy", Settings: Settings{Theme: "dark", Region: "eu"}},
	}
	for _, a := range seed {
		if err := db.Insert(ctx, a); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	var dark []Account3
	if err := db.Model(&Account3{}).
		WhereJSON("settings", "theme", "=", "dark").
		OrderBy("id", playsql.Asc).
		Get(ctx, &dark); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(dark) != 2 {
		t.Fatalf("want 2 dark accounts, got %d", len(dark))
	}
	if dark[0].Name != "Ann" || dark[1].Name != "Cy" {
		t.Fatalf("wrong rows: %s, %s", dark[0].Name, dark[1].Name)
	}

	// Combined JSON predicates.
	var darkEU []Account3
	if err := db.Model(&Account3{}).
		WhereJSON("settings", "theme", "=", "dark").
		WhereJSON("settings", "region", "=", "eu").
		Get(ctx, &darkEU); err != nil {
		t.Fatalf("get2: %v", err)
	}
	if len(darkEU) != 1 || darkEU[0].Name != "Cy" {
		t.Fatalf("want only Cy, got %+v", darkEU)
	}
}
