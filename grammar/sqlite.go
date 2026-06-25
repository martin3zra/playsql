package grammar

// SQLite grammar. Identifiers wrapped in double quotes, "?" placeholders.
type SQLite struct{}

func (SQLite) Wrap(id string) string { return wrapQualified(id, `"`, `"`) }

func (SQLite) Placeholder(int) string { return "?" }

func (g SQLite) CompileSelect(q CompiledQuery) (string, []any) {
	return compileSelect(g, q)
}
