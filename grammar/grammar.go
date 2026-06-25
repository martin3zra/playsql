// Package grammar owns SQL dialect concerns: identifier quoting, placeholder
// style, and statement assembly. The builder hands it a neutral CompiledQuery;
// the grammar turns it into a driver-specific SQL string + args.
package grammar

import (
	"fmt"
	"strings"
)

// WhereKind classifies a predicate so the grammar can emit the right SQL and
// the right number of placeholders.
type WhereKind int

const (
	WhereBasic      WhereKind = iota // Column Op ?
	WhereIn                          // Column IN (?, ?, ...)
	WhereNotIn                       // Column NOT IN (?, ?, ...)
	WhereNull                        // Column IS NULL
	WhereNotNull                     // Column IS NOT NULL
	WhereBetween                     // Column BETWEEN ? AND ?
	WhereNotBetween                  // Column NOT BETWEEN ? AND ?
	WhereNested                      // ( <Group> )
)

// WhereClause is one compiled predicate. All values are bound, never
// interpolated. Boolean is the connector to the previous clause ("AND"/"OR").
type WhereClause struct {
	Kind    WhereKind
	Boolean string // "AND" (default) | "OR"
	Column  string
	Op      string
	Value   any           // WhereBasic
	Values  []any         // WhereIn / WhereNotIn / WhereBetween
	Group   []WhereClause // WhereNested
}

// CompiledQuery is the dialect-neutral description of a SELECT the builder
// produced. It carries no SQL text and no placeholders — those are the
// grammar's job.
type CompiledQuery struct {
	Table     string
	Columns   []string // empty => SELECT *
	Aggregate string   // e.g. "COUNT(*)"; overrides Columns, emitted verbatim
	Wheres    []WhereClause
	Limit     int // 0 => no LIMIT
	Offset    int // 0 => no OFFSET
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
	switch {
	case q.Aggregate != "":
		cols = q.Aggregate // verbatim, e.g. COUNT(*)
	case len(q.Columns) > 0:
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
		n := 0
		clause, wargs := compileWheres(g, q.Wheres, &n)
		sb.WriteString(" WHERE ")
		sb.WriteString(clause)
		args = append(args, wargs...)
	}

	if q.Limit > 0 {
		fmt.Fprintf(&sb, " LIMIT %d", q.Limit)
	}
	if q.Offset > 0 {
		fmt.Fprintf(&sb, " OFFSET %d", q.Offset)
	}

	return sb.String(), args
}

// compileWheres assembles a list of predicates, threading a running 1-based
// bind counter n through every placeholder (so IN-expansion and nested groups
// keep $1,$2,... contiguous for dialects that number binds). It recurses for
// nested groups.
func compileWheres(g Grammar, clauses []WhereClause, n *int) (string, []any) {
	var sb strings.Builder
	var args []any

	for i, w := range clauses {
		if i > 0 {
			boolean := w.Boolean
			if boolean == "" {
				boolean = "AND"
			}
			sb.WriteString(" ")
			sb.WriteString(boolean)
			sb.WriteString(" ")
		}

		switch w.Kind {
		case WhereBasic:
			*n++
			sb.WriteString(g.Wrap(w.Column))
			sb.WriteByte(' ')
			sb.WriteString(w.Op)
			sb.WriteByte(' ')
			sb.WriteString(g.Placeholder(*n))
			args = append(args, w.Value)

		case WhereIn, WhereNotIn:
			// Empty list: emit a constant rather than invalid "IN ()".
			if len(w.Values) == 0 {
				if w.Kind == WhereIn {
					sb.WriteString("1 = 0") // IN nothing -> always false
				} else {
					sb.WriteString("1 = 1") // NOT IN nothing -> always true
				}
				break
			}
			op := "IN"
			if w.Kind == WhereNotIn {
				op = "NOT IN"
			}
			sb.WriteString(g.Wrap(w.Column))
			sb.WriteByte(' ')
			sb.WriteString(op)
			sb.WriteString(" (")
			for j, v := range w.Values {
				if j > 0 {
					sb.WriteString(", ")
				}
				*n++
				sb.WriteString(g.Placeholder(*n))
				args = append(args, v)
			}
			sb.WriteByte(')')

		case WhereNull, WhereNotNull:
			sb.WriteString(g.Wrap(w.Column))
			if w.Kind == WhereNull {
				sb.WriteString(" IS NULL")
			} else {
				sb.WriteString(" IS NOT NULL")
			}

		case WhereBetween, WhereNotBetween:
			op := "BETWEEN"
			if w.Kind == WhereNotBetween {
				op = "NOT BETWEEN"
			}
			sb.WriteString(g.Wrap(w.Column))
			sb.WriteByte(' ')
			sb.WriteString(op)
			sb.WriteByte(' ')
			*n++
			sb.WriteString(g.Placeholder(*n))
			sb.WriteString(" AND ")
			*n++
			sb.WriteString(g.Placeholder(*n))
			args = append(args, w.Values[0], w.Values[1])

		case WhereNested:
			sub, subArgs := compileWheres(g, w.Group, n)
			sb.WriteByte('(')
			sb.WriteString(sub)
			sb.WriteByte(')')
			args = append(args, subArgs...)
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
