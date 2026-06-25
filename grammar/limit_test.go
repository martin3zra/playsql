package grammar

import "testing"

func TestLimitOffset(t *testing.T) {
	sql, _ := SQLite{}.CompileSelect(CompiledQuery{Table: "users", Limit: 5, Offset: 10})
	if sql != `SELECT * FROM "users" LIMIT 5 OFFSET 10` {
		t.Errorf("unexpected: %s", sql)
	}

	sql, _ = SQLite{}.CompileSelect(CompiledQuery{Table: "users", Limit: 1})
	if sql != `SELECT * FROM "users" LIMIT 1` {
		t.Errorf("offset omitted when zero, got: %s", sql)
	}
}

func TestAggregate(t *testing.T) {
	sql, args := Postgres{}.CompileSelect(CompiledQuery{
		Table:     "users",
		Aggregate: "COUNT(*)",
		Wheres:    []WhereClause{{Kind: WhereBasic, Column: "age", Op: ">", Value: 18}},
	})
	if sql != `SELECT COUNT(*) FROM "users" WHERE "age" > $1` {
		t.Errorf("unexpected: %s", sql)
	}
	if len(args) != 1 || args[0] != 18 {
		t.Errorf("args mismatch: %#v", args)
	}
}
