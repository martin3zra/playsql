package grammar

import "testing"

func TestCompileUpdate_PostgresReturning(t *testing.T) {
	sql, returns := Postgres{}.CompileUpdate(UpdateStmt{
		Table:     "users",
		Columns:   []string{"name"},
		Wheres:    []WhereClause{{Kind: WhereBasic, Column: "id", Op: "=", Value: 7}},
		Returning: []string{"id", "name"},
	})
	want := `UPDATE "users" SET "name" = $1 WHERE "id" = $2 RETURNING "id", "name"`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if !returns {
		t.Error("Postgres with Returning should report returnsRows=true")
	}
}

func TestCompileUpdate_SQLiteReturning(t *testing.T) {
	sql, returns := SQLite{}.CompileUpdate(UpdateStmt{
		Table:     "users",
		Columns:   []string{"active"},
		Returning: []string{"id"},
	})
	want := `UPDATE "users" SET "active" = ? RETURNING "id"`
	if sql != want || !returns {
		t.Errorf("got %q returns=%v", sql, returns)
	}
}

func TestCompileUpdate_MySQLNoReturning(t *testing.T) {
	// MySQL has no RETURNING: the clause is dropped and returnsRows is false.
	sql, returns := MySQL{}.CompileUpdate(UpdateStmt{
		Table:     "users",
		Columns:   []string{"name"},
		Returning: []string{"id"},
	})
	want := "UPDATE `users` SET `name` = ?"
	if sql != want {
		t.Errorf("got %q want %q", sql, want)
	}
	if returns {
		t.Error("MySQL must not report returnsRows")
	}
}

func TestCompileUpdate_MSSQLOutput(t *testing.T) {
	// OUTPUT INSERTED goes between SET and WHERE.
	sql, returns := MSSQL{}.CompileUpdate(UpdateStmt{
		Table:     "users",
		Columns:   []string{"name"},
		Wheres:    []WhereClause{{Kind: WhereBasic, Column: "id", Op: "=", Value: 7}},
		Returning: []string{"id", "name"},
	})
	want := `UPDATE [users] SET [name] = @p1 OUTPUT INSERTED.[id], INSERTED.[name] WHERE [id] = @p2`
	if sql != want || !returns {
		t.Errorf("got %q returns=%v\nwant %q", sql, returns, want)
	}
}

func TestCompileUpdate_StructuredCTEBinds(t *testing.T) {
	// A bound CTE subquery ($1) leads, then SET ($2), then WHERE ($3).
	cte := CompiledQuery{
		Table:   "orders",
		Columns: []string{"id"},
		Wheres:  []WhereClause{{Kind: WhereBasic, Column: "region", Op: "=", Value: "eu"}},
	}
	sql, _ := Postgres{}.CompileUpdate(UpdateStmt{
		Table:   "products",
		Columns: []string{"on_sale"},
		CTEs:    []CTE{{Name: "cheap", Query: &cte}},
		Wheres:  []WhereClause{{Kind: WhereBasic, Column: "active", Op: "=", Value: true}},
	})
	want := `WITH "cheap" AS (SELECT "id" FROM "orders" WHERE "region" = $1) ` +
		`UPDATE "products" SET "on_sale" = $2 WHERE "active" = $3`
	if sql != want {
		t.Errorf("sql:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileUpdate_CTEAndWhereRaw(t *testing.T) {
	// WITH prefix + a verbatim predicate referencing it; SET binds stay at $1.
	sql, _ := Postgres{}.CompileUpdate(UpdateStmt{
		Table:   "products",
		Columns: []string{"cheap"},
		CTEs:    []CTE{{Name: "avg_price", SQL: "SELECT AVG(price) AS value FROM products"}},
		Wheres:  []WhereClause{{Kind: WhereRaw, Raw: "price < (SELECT value FROM avg_price)"}},
	})
	want := `WITH "avg_price" AS (SELECT AVG(price) AS value FROM products) ` +
		`UPDATE "products" SET "cheap" = $1 WHERE price < (SELECT value FROM avg_price)`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileUpdate_WhereRawKeepsPlaceholderOrder(t *testing.T) {
	// A raw predicate consumes no placeholder; the bound one after it is $2.
	sql, _ := Postgres{}.CompileUpdate(UpdateStmt{
		Table:   "t",
		Columns: []string{"a"},
		Wheres: []WhereClause{
			{Kind: WhereRaw, Raw: "x IS NOT NULL"},
			{Kind: WhereBasic, Boolean: "AND", Column: "b", Op: "=", Value: 1},
		},
	})
	want := `UPDATE "t" SET "a" = $1 WHERE x IS NOT NULL AND "b" = $2`
	if sql != want {
		t.Errorf("got %q want %q", sql, want)
	}
}
