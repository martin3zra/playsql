package grammar

import (
	"reflect"
	"testing"
)

// query used across dialect assertions.
func sampleQuery() CompiledQuery {
	return CompiledQuery{
		Table:   "users",
		Columns: []string{"id", "name", "age"},
		Wheres: []WhereClause{
			{Column: "age", Op: "=", Value: 30},
			{Column: "active", Op: "=", Value: true},
		},
	}
}

func TestSQLiteCompileSelect(t *testing.T) {
	sql, args := SQLite{}.CompileSelect(sampleQuery())

	want := `SELECT "id", "name", "age" FROM "users" WHERE "age" = ? AND "active" = ?`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if !reflect.DeepEqual(args, []any{30, true}) {
		t.Errorf("args mismatch: %#v", args)
	}
}

func TestPostgresCompileSelect(t *testing.T) {
	sql, args := Postgres{}.CompileSelect(sampleQuery())

	// Same neutral query, different dialect: numbered $n placeholders.
	want := `SELECT "id", "name", "age" FROM "users" WHERE "age" = $1 AND "active" = $2`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if !reflect.DeepEqual(args, []any{30, true}) {
		t.Errorf("args mismatch: %#v", args)
	}
}

func TestSelectStarWhenNoColumns(t *testing.T) {
	sql, _ := SQLite{}.CompileSelect(CompiledQuery{Table: "users"})
	if sql != `SELECT * FROM "users"` {
		t.Errorf("unexpected: %s", sql)
	}
}

func TestForUnknownDriver(t *testing.T) {
	if For("oracle") != nil {
		t.Error("expected nil grammar for unsupported driver")
	}
	if For("postgres") == nil || For("sqlite") == nil {
		t.Error("expected non-nil grammar for supported drivers")
	}
}
