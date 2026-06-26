package grammar

import "testing"

func TestCompileInsert_SQLite(t *testing.T) {
	sql, returnsID := SQLite{}.CompileInsert(InsertStmt{
		Table: "users", Columns: []string{"name", "age"},
		PrimaryKey: "id", Incrementing: true,
	})
	if sql != `INSERT INTO "users" ("name", "age") VALUES (?, ?)` {
		t.Errorf("unexpected: %s", sql)
	}
	if returnsID {
		t.Error("sqlite should use LastInsertId, not RETURNING")
	}
}

func TestCompileInsert_PostgresReturning(t *testing.T) {
	sql, returnsID := Postgres{}.CompileInsert(InsertStmt{
		Table: "users", Columns: []string{"name", "age"},
		PrimaryKey: "id", Incrementing: true,
	})
	if sql != `INSERT INTO "users" ("name", "age") VALUES ($1, $2) RETURNING "id"` {
		t.Errorf("unexpected: %s", sql)
	}
	if !returnsID {
		t.Error("postgres incrementing insert should return id")
	}
}

func TestCompileInsert_PostgresNonIncrementing(t *testing.T) {
	sql, returnsID := Postgres{}.CompileInsert(InsertStmt{
		Table: "products", Columns: []string{"sku", "name"},
		PrimaryKey: "sku", Incrementing: false,
	})
	if returnsID {
		t.Error("non-incrementing key should not RETURNING")
	}
	if sql != `INSERT INTO "products" ("sku", "name") VALUES ($1, $2)` {
		t.Errorf("unexpected: %s", sql)
	}
}

func TestCompileUpdate_ContiguousPlaceholders(t *testing.T) {
	// SET uses $1,$2; WHERE must continue at $3.
	sql, _ := Postgres{}.CompileUpdate(UpdateStmt{
		Table:   "users",
		Columns: []string{"name", "age"},
		Wheres:  []WhereClause{{Kind: WhereBasic, Column: "id", Op: "=", Value: 7}},
	})
	want := `UPDATE "users" SET "name" = $1, "age" = $2 WHERE "id" = $3`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
}
