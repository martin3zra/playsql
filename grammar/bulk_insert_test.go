package grammar

import "testing"

func TestCompileInsert_Bulk(t *testing.T) {
	sql, returnsID := Postgres{}.CompileInsert(InsertStmt{
		Table: "users", Columns: []string{"name", "age"}, Rows: 2,
		PrimaryKey: "id", Incrementing: true,
	})
	want := `INSERT INTO "users" ("name", "age") VALUES ($1, $2), ($3, $4)`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if returnsID {
		t.Error("bulk insert must not RETURNING a single id")
	}
}

func TestCompileInsert_BulkSQLite(t *testing.T) {
	sql, _ := SQLite{}.CompileInsert(InsertStmt{
		Table: "t", Columns: []string{"a"}, Rows: 3,
	})
	if sql != `INSERT INTO "t" ("a") VALUES (?), (?), (?)` {
		t.Errorf("unexpected: %s", sql)
	}
}
