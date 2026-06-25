package grammar

// MySQL grammar. Identifiers wrapped in backticks, "?" placeholders, no
// RETURNING (uses LastInsertId), ON DUPLICATE KEY UPDATE for upserts.
type MySQL struct{}

func (MySQL) Wrap(id string) string { return wrapQualified(id, "`", "`") }

func (MySQL) Placeholder(int) string { return "?" }

func (g MySQL) CompileSelect(q CompiledQuery) (string, []any) {
	return compileSelect(g, q)
}

func (g MySQL) CompileInsert(s InsertStmt) (string, bool) {
	return compileInsert(g, s, false) // LastInsertId, no RETURNING
}

func (g MySQL) CompileUpdate(s UpdateStmt) string {
	return compileUpdate(g, s)
}

func (g MySQL) CompileDelete(s DeleteStmt) string {
	return compileDelete(g, s)
}

func (g MySQL) CompileUpsert(s UpsertStmt) string {
	return compileUpsertMySQL(g, s)
}

func (g MySQL) JSONExtract(column, path string) string {
	// col ->> '$.a.b' returns the value at the path as unquoted text.
	return g.Wrap(column) + " ->> '" + jsonPath(path) + "'"
}
