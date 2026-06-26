package grammar

// SQLite grammar. Identifiers wrapped in double quotes, "?" placeholders.
type SQLite struct{}

func (SQLite) Wrap(id string) string { return wrapQualified(id, `"`, `"`) }

func (SQLite) Placeholder(int) string { return "?" }

func (g SQLite) CompileSelect(q CompiledQuery) (string, []any) {
	return compileSelect(g, q)
}

func (g SQLite) CompileInsert(s InsertStmt) (string, bool) {
	return compileInsert(g, s, false) // uses LastInsertId
}

func (g SQLite) CompileUpdate(s UpdateStmt) (string, bool) {
	return compileUpdate(g, s, "RETURNING") // RETURNING since SQLite 3.35
}

func (g SQLite) CompileDelete(s DeleteStmt) string {
	return compileDelete(g, s)
}

func (g SQLite) CompileUpsert(s UpsertStmt) string {
	return compileUpsert(g, s)
}

func (g SQLite) JSONExtract(column, path string) string {
	return "json_extract(" + g.Wrap(column) + ", '" + jsonPath(path) + "')"
}
