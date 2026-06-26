package grammar

import (
	"strconv"
	"strings"
)

// Postgres grammar. Identifiers wrapped in double quotes, numbered "$n"
// placeholders.
type Postgres struct{}

func (Postgres) Wrap(id string) string { return wrapQualified(id, `"`, `"`) }

func (Postgres) Placeholder(n int) string { return "$" + strconv.Itoa(n) }

func (g Postgres) CompileSelect(q CompiledQuery) (string, []any) {
	return compileSelect(g, q)
}

func (g Postgres) CompileInsert(s InsertStmt) (string, bool) {
	return compileInsert(g, s, true) // RETURNING <pk>
}

func (g Postgres) CompileUpdate(s UpdateStmt) (string, bool) {
	return compileUpdate(g, s, "RETURNING")
}

func (g Postgres) CompileDelete(s DeleteStmt) string {
	return compileDelete(g, s)
}

func (g Postgres) CompileUpsert(s UpsertStmt) string {
	return compileUpsert(g, s)
}

func (g Postgres) JSONExtract(column, path string) string {
	// col #>> '{a,b}' returns the value at the path as text.
	return g.Wrap(column) + " #>> '{" + strings.ReplaceAll(path, ".", ",") + "}'"
}
