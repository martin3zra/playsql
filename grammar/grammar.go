// Package grammar owns SQL dialect concerns: identifier quoting, placeholder
// style, and statement assembly. The builder hands it a neutral CompiledQuery;
// the grammar turns it into a driver-specific SQL string + args.
package grammar

import "strings"

// WhereClause is one compiled predicate. Value is bound, never interpolated.
type WhereClause struct {
	Column string
	Op     string
	Value  any
}

// CompiledQuery is the dialect-neutral description of a SELECT the builder
// produced. It carries no SQL text and no placeholders — those are the
// grammar's job.
type CompiledQuery struct {
	Table   string
	Columns []string // empty => SELECT *
	Wheres  []WhereClause
}

// Grammar generates SQL for a specific driver.
type Grammar interface {
	CompileSelect(q CompiledQuery) (sql string, args []any)
	Wrap(identifier string) string
	Placeholder(n int) string // n is 1-based bind position
}

// For returns the grammar for a driver name, or nil if unsupported.
func For(driver string) Grammar {
	switch driver {
	case "sqlite", "sqlite3":
		return SQLite{}
	case "postgres", "pgx":
		return Postgres{}
	default:
		return nil
	}
}

// compileSelect is the dialect-neutral SELECT assembler. Dialects differ only by
// Wrap (quoting) and Placeholder (bind style), so both are taken from g.
func compileSelect(g Grammar, q CompiledQuery) (string, []any) {
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
			sb.WriteByte(' ')
			sb.WriteString(w.Op)
			sb.WriteByte(' ')
			sb.WriteString(g.Placeholder(i + 1))
			args = append(args, w.Value)
		}
	}

	return sb.String(), args
}

// wrapQualified wraps a possibly dotted identifier (table.column) part by part,
// using the supplied open/close quote characters.
func wrapQualified(id, open, close string) string {
	if id == "*" {
		return id
	}
	parts := strings.Split(id, ".")
	for i, p := range parts {
		if p == "*" {
			continue
		}
		parts[i] = open + p + close
	}
	return strings.Join(parts, ".")
}
