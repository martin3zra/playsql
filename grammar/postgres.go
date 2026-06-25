package grammar

import "strconv"

// Postgres grammar. Identifiers wrapped in double quotes, numbered "$n"
// placeholders.
type Postgres struct{}

func (Postgres) Wrap(id string) string { return wrapQualified(id, `"`, `"`) }

func (Postgres) Placeholder(n int) string { return "$" + strconv.Itoa(n) }

func (g Postgres) CompileSelect(q CompiledQuery) (string, []any) {
	return compileSelect(g, q)
}
