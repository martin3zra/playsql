package grammar

import (
	"reflect"
	"testing"
)

func TestOrderBy(t *testing.T) {
	sql, _ := SQLite{}.CompileSelect(CompiledQuery{
		Table: "users",
		Orders: []OrderClause{
			{Column: "age", Direction: "DESC"},
			{Column: "name", Direction: "ASC"},
		},
		Limit: 10,
	})
	want := `SELECT * FROM "users" ORDER BY "age" DESC, "name" ASC LIMIT 10`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileDelete(t *testing.T) {
	sql := Postgres{}.CompileDelete(DeleteStmt{
		Table: "users",
		Wheres: []WhereClause{
			{Kind: WhereBasic, Column: "age", Op: "<", Value: 18},
			{Kind: WhereBasic, Boolean: "AND", Column: "active", Op: "=", Value: false},
		},
	})
	want := `DELETE FROM "users" WHERE "age" < $1 AND "active" = $2`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileDelete_NoWhere(t *testing.T) {
	sql := SQLite{}.CompileDelete(DeleteStmt{Table: "logs"})
	if sql != `DELETE FROM "logs"` {
		t.Errorf("unexpected: %s", sql)
	}
}

func TestOrderByBeforeLimitAcrossDialects(t *testing.T) {
	// ORDER BY appears before LIMIT/OFFSET for both dialects.
	q := CompiledQuery{Table: "t", Orders: []OrderClause{{Column: "id", Direction: "ASC"}}, Limit: 1, Offset: 2}
	pg, _ := Postgres{}.CompileSelect(q)
	lite, _ := SQLite{}.CompileSelect(q)
	if !reflect.DeepEqual(pg, `SELECT * FROM "t" ORDER BY "id" ASC LIMIT 1 OFFSET 2`) {
		t.Errorf("pg: %s", pg)
	}
	if lite != pg {
		t.Errorf("dialects diverged: %s vs %s", lite, pg)
	}
}
