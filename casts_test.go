package playsql_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/martin3zra/playsql"
)

// csvCaster stores a []string as a comma-joined string. Demonstrates a custom,
// non-JSON cast registered by the user.
type csvCaster struct{}

func (csvCaster) Decode(raw any, dest reflect.Value) error {
	var s string
	switch v := raw.(type) {
	case []byte:
		s = string(v)
	case string:
		s = v
	default:
		return fmt.Errorf("csv: cannot decode %T", raw)
	}
	if s == "" {
		return nil
	}
	dest.Set(reflect.ValueOf(strings.Split(s, ",")))
	return nil
}

func (csvCaster) Encode(field any) (any, error) {
	parts, ok := field.([]string)
	if !ok {
		return nil, fmt.Errorf("csv: expected []string, got %T", field)
	}
	return strings.Join(parts, ","), nil
}

func init() {
	playsql.RegisterCaster("csv", csvCaster{})
}

// Doc uses both the built-in json cast and the custom csv cast.
type Doc struct {
	ID   int64    `db:"id" play:"pk,incrementing"`
	Meta DocMeta  `db:"meta" play:"cast=json"`
	Tags []string `db:"tags" play:"cast=csv"`
}

type DocMeta struct {
	Author string `json:"author"`
}

func (Doc) TableName() string { return "docs" }

// docRaw probes the raw tags column (no cast) to confirm storage format.
type docRaw struct {
	ID   int64  `db:"id" play:"pk,incrementing"`
	Tags string `db:"tags"`
}

func (docRaw) TableName() string { return "docs" }

func TestCustomCaster_CSV(t *testing.T) {
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	if _, err := db.Exec(ctx, `CREATE TABLE docs (id INTEGER PRIMARY KEY, meta TEXT, tags TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	d := &Doc{Meta: DocMeta{Author: "Ann"}, Tags: []string{"go", "sql", "orm"}}
	if err := db.Insert(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// json cast and custom csv cast coexist; both round-trip.
	var got Doc
	if err := db.Model(&Doc{}).Find(ctx, &got, d.ID); err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Meta.Author != "Ann" {
		t.Fatalf("json cast still works: %+v", got.Meta)
	}
	if len(got.Tags) != 3 || got.Tags[0] != "go" || got.Tags[2] != "orm" {
		t.Fatalf("csv cast round-trip failed: %+v", got.Tags)
	}

	// The csv column is stored as plain comma-joined text.
	var probe docRaw
	if err := db.Model(&docRaw{}).Find(ctx, &probe, d.ID); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if probe.Tags != "go,sql,orm" {
		t.Fatalf("csv not stored as comma text: %q", probe.Tags)
	}
}
