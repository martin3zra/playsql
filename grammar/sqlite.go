package grammar

import "strings"

// SQLite grammar. Identifiers wrapped in double quotes, "?" placeholders.
type SQLite struct{}

func (SQLite) Wrap(id string) string { return wrapQualified(id, `"`, `"`) }

func (SQLite) Placeholder(int) string { return "?" }

func (g SQLite) CompileSelect(q CompiledQuery) (string, []any) {
	cols := "*"
	if len(q.Columns) > 0 {
		wrapped := make([]string, len(q.Columns))
		for i, c := range q.Columns {
			wrapped[i] = g.Wrap(c)
		}
		cols = strings.Join(wrapped, ", ")
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(cols)
	sb.WriteString(" FROM ")
	sb.WriteString(g.Wrap(q.Table))

	var args []any
	if len(q.Wheres) > 0 {
		sb.WriteString(" WHERE ")
		for i, w := range q.Wheres {
			if i > 0 {
				sb.WriteString(" AND ")
			}
			sb.WriteString(g.Wrap(w.Column))
			sb.WriteString(" ")
			sb.WriteString(w.Op)
			sb.WriteString(" ")
			sb.WriteString(g.Placeholder(i + 1))
			args = append(args, w.Value)
		}
	}

	return sb.String(), args
}
