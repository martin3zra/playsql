package grammar

import "testing"

func TestCompileUpsert_Postgres(t *testing.T) {
	sql := Postgres{}.CompileUpsert(UpsertStmt{
		Table:           "users",
		Columns:         []string{"email", "name"},
		Rows:            1,
		ConflictColumns: []string{"email"},
		UpdateColumns:   []string{"name"},
	})
	want := `INSERT INTO "users" ("email", "name") VALUES ($1, $2) ` +
		`ON CONFLICT ("email") DO UPDATE SET "name" = EXCLUDED."name"`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileUpsert_BulkSQLite(t *testing.T) {
	sql := SQLite{}.CompileUpsert(UpsertStmt{
		Table:           "kv",
		Columns:         []string{"k", "v"},
		Rows:            2,
		ConflictColumns: []string{"k"},
		UpdateColumns:   []string{"v"},
	})
	want := `INSERT INTO "kv" ("k", "v") VALUES (?, ?), (?, ?) ` +
		`ON CONFLICT ("k") DO UPDATE SET "v" = EXCLUDED."v"`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileUpsert_DoNothing(t *testing.T) {
	sql := Postgres{}.CompileUpsert(UpsertStmt{
		Table:           "users",
		Columns:         []string{"email"},
		Rows:            1,
		ConflictColumns: []string{"email"},
		UpdateColumns:   nil,
	})
	want := `INSERT INTO "users" ("email") VALUES ($1) ON CONFLICT ("email") DO NOTHING`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
}
