package grammar

import "testing"

// lastFlight builds the canonical correlated subquery: latest flight name/col
// for the current destination.
func lastFlight(col string) CompiledQuery {
	return CompiledQuery{
		Table:   "flights",
		Columns: []string{col},
		Wheres:  []WhereClause{{Kind: WhereColumn, Column: "destination_id", Op: "=", Second: "destinations.id"}},
		Orders:  []OrderClause{{Column: "arrived_at", Direction: "DESC"}},
		Limit:   1,
	}
}

func TestCompileSelect_AddSelectSubquery(t *testing.T) {
	q := CompiledQuery{
		Table:      "destinations",
		SubSelects: []SubSelectColumn{{Alias: "last_flight", Query: lastFlight("name")}},
	}
	sql, args := Postgres{}.CompileSelect(q)
	want := `SELECT *, (SELECT "name" FROM "flights" WHERE "destination_id" = "destinations"."id" ` +
		`ORDER BY "arrived_at" DESC LIMIT 1) AS "last_flight" FROM "destinations"`
	if sql != want {
		t.Errorf("sql:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 0 {
		t.Errorf("no binds expected, got %v", args)
	}
}

func TestCompileSelect_OrderBySubquery(t *testing.T) {
	sub := lastFlight("arrived_at")
	q := CompiledQuery{
		Table:  "destinations",
		Orders: []OrderClause{{Direction: "DESC", Sub: &sub}},
	}
	sql, _ := Postgres{}.CompileSelect(q)
	want := `SELECT * FROM "destinations" ORDER BY (SELECT "arrived_at" FROM "flights" ` +
		`WHERE "destination_id" = "destinations"."id" ORDER BY "arrived_at" DESC LIMIT 1) DESC`
	if sql != want {
		t.Errorf("sql:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileSelect_SubqueryMSSQLLimit(t *testing.T) {
	q := CompiledQuery{
		Table:      "destinations",
		SubSelects: []SubSelectColumn{{Alias: "last_flight", Query: lastFlight("name")}},
	}
	sql, _ := MSSQL{}.CompileSelect(q)
	want := `SELECT *, (SELECT [name] FROM [flights] WHERE [destination_id] = [destinations].[id] ` +
		`ORDER BY [arrived_at] DESC OFFSET 0 ROWS FETCH NEXT 1 ROWS ONLY) AS [last_flight] FROM [destinations]`
	if sql != want {
		t.Errorf("sql:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileSelect_SubqueryBindOrdering(t *testing.T) {
	// Binds must number in SQL emission order: select-subquery ($1) -> WHERE ($2)
	// -> order-subquery ($3).
	selSub := CompiledQuery{
		Table:   "flights",
		Columns: []string{"name"},
		Wheres: []WhereClause{
			{Kind: WhereColumn, Column: "destination_id", Op: "=", Second: "destinations.id"},
			{Kind: WhereBasic, Boolean: "AND", Column: "active", Op: "=", Value: true},
		},
		Limit: 1,
	}
	ordSub := CompiledQuery{
		Table:   "flights",
		Columns: []string{"arrived_at"},
		Wheres: []WhereClause{
			{Kind: WhereColumn, Column: "destination_id", Op: "=", Second: "destinations.id"},
			{Kind: WhereBasic, Boolean: "AND", Column: "ok", Op: "=", Value: 1},
		},
		Limit: 1,
	}
	q := CompiledQuery{
		Table:      "destinations",
		Columns:    []string{"id"},
		SubSelects: []SubSelectColumn{{Alias: "recent", Query: selSub}},
		Wheres:     []WhereClause{{Kind: WhereBasic, Column: "region", Op: "=", Value: "x"}},
		Orders:     []OrderClause{{Direction: "DESC", Sub: &ordSub}},
	}
	sql, args := Postgres{}.CompileSelect(q)
	want := `SELECT "id", (SELECT "name" FROM "flights" WHERE "destination_id" = "destinations"."id" ` +
		`AND "active" = $1 LIMIT 1) AS "recent" FROM "destinations" WHERE "region" = $2 ` +
		`ORDER BY (SELECT "arrived_at" FROM "flights" WHERE "destination_id" = "destinations"."id" ` +
		`AND "ok" = $3 LIMIT 1) DESC`
	if sql != want {
		t.Errorf("sql:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 3 || args[0] != true || args[1] != "x" || args[2] != 1 {
		t.Errorf("args = %v, want [true x 1]", args)
	}
}
