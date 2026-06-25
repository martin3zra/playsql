package grammar

import (
	"reflect"
	"testing"
)

func TestWhereKinds_Postgres(t *testing.T) {
	q := CompiledQuery{
		Table: "users",
		Wheres: []WhereClause{
			{Kind: WhereIn, Column: "id", Values: []any{1, 2, 3}},
			{Kind: WhereNull, Boolean: "AND", Column: "deleted_at"},
			{Kind: WhereBetween, Boolean: "AND", Column: "age", Values: []any{18, 65}},
			{Kind: WhereBasic, Boolean: "OR", Column: "name", Op: "=", Value: "Jane"},
		},
	}
	sql, args := Postgres{}.CompileSelect(q)

	// Bind counter must stay contiguous across IN-expansion and BETWEEN.
	want := `SELECT * FROM "users" WHERE "id" IN ($1, $2, $3)` +
		` AND "deleted_at" IS NULL` +
		` AND "age" BETWEEN $4 AND $5` +
		` OR "name" = $6`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if !reflect.DeepEqual(args, []any{1, 2, 3, 18, 65, "Jane"}) {
		t.Errorf("args mismatch: %#v", args)
	}
}

func TestNestedGroup_Postgres(t *testing.T) {
	// name = $1 AND ( age > $2 OR vip = $3 )
	q := CompiledQuery{
		Table: "users",
		Wheres: []WhereClause{
			{Kind: WhereBasic, Column: "name", Op: "=", Value: "Jane"},
			{Kind: WhereNested, Boolean: "AND", Group: []WhereClause{
				{Kind: WhereBasic, Column: "age", Op: ">", Value: 30},
				{Kind: WhereBasic, Boolean: "OR", Column: "vip", Op: "=", Value: true},
			}},
		},
	}
	sql, args := Postgres{}.CompileSelect(q)

	want := `SELECT * FROM "users" WHERE "name" = $1 AND ("age" > $2 OR "vip" = $3)`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if !reflect.DeepEqual(args, []any{"Jane", 30, true}) {
		t.Errorf("args mismatch: %#v", args)
	}
}

func TestEmptyIn(t *testing.T) {
	in, _ := SQLite{}.CompileSelect(CompiledQuery{
		Table:  "t",
		Wheres: []WhereClause{{Kind: WhereIn, Column: "id"}},
	})
	if in != `SELECT * FROM "t" WHERE 1 = 0` {
		t.Errorf("empty IN should be always-false, got: %s", in)
	}

	notIn, _ := SQLite{}.CompileSelect(CompiledQuery{
		Table:  "t",
		Wheres: []WhereClause{{Kind: WhereNotIn, Column: "id"}},
	})
	if notIn != `SELECT * FROM "t" WHERE 1 = 1` {
		t.Errorf("empty NOT IN should be always-true, got: %s", notIn)
	}
}
