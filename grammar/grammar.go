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
	Orders    []OrderClause
	Limit     int // 0 => no LIMIT
	Offset    int // 0 => no OFFSET
}

// OrderClause is one ORDER BY term. Direction is "ASC" or "DESC".
type OrderClause struct {
	Column    string
	Direction string
}

// DeleteStmt describes a DELETE.
type DeleteStmt struct {
	Table  string
	Wheres []WhereClause
}

// InsertStmt describes an INSERT. Values are supplied by the caller in Columns
// order, repeated Rows times for bulk inserts. The grammar only needs the
// column names, row count, and key info.
type InsertStmt struct {
	Table        string
	Columns      []string
	Rows         int // number of value tuples; 0 or 1 => single row
	PrimaryKey   string
	Incrementing bool
}

// UpdateStmt describes an UPDATE. Set columns first, then Wheres; the grammar
// numbers placeholders contiguously across both.
type UpdateStmt struct {
	Table   string
	Columns []string // SET columns
	Wheres  []WhereClause
}

// UpsertStmt describes an INSERT ... ON CONFLICT DO UPDATE. UpdateColumns empty
// means DO NOTHING on conflict.
type UpsertStmt struct {
	Table           string
	Columns         []string
	Rows            int
	ConflictColumns []string
	UpdateColumns   []string
}

// Grammar generates SQL for a specific driver.
type Grammar interface {
	CompileSelect(q CompiledQuery) (sql string, args []any)
	// CompileInsert returns the statement and whether it yields the new id via a
	// RETURNING clause (true) versus the driver's LastInsertId (false).
	CompileInsert(s InsertStmt) (sql string, returnsID bool)
	CompileUpdate(s UpdateStmt) (sql string)
	CompileDelete(s DeleteStmt) (sql string)
	CompileUpsert(s UpsertStmt) (sql string)
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
	case "mysql":
		return MySQL{}
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

	if len(q.Orders) > 0 {
		sb.WriteString(" ORDER BY ")
		for i, o := range q.Orders {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(g.Wrap(o.Column))
			sb.WriteByte(' ')
			sb.WriteString(o.Direction)
		}
	}

	if q.Limit > 0 {
		fmt.Fprintf(&sb, " LIMIT %d", q.Limit)
	}
	if q.Offset > 0 {
		fmt.Fprintf(&sb, " OFFSET %d", q.Offset)
	}

	return sb.String(), args
}

// compileDelete builds a DELETE with parameterized WHERE predicates.
func compileDelete(g Grammar, s DeleteStmt) string {
	var sb strings.Builder
	sb.WriteString("DELETE FROM ")
	sb.WriteString(g.Wrap(s.Table))
	if len(s.Wheres) > 0 {
		n := 0
		clause, _ := compileWheres(g, s.Wheres, &n)
		sb.WriteString(" WHERE ")
		sb.WriteString(clause)
	}
	return sb.String()
}

// insertInto builds "INSERT INTO <table> (<cols>) VALUES (...)[, (...)]" with
// placeholders numbered across all rows.
func insertInto(g Grammar, table string, cols []string, rows int) string {
	var sb strings.Builder
	sb.WriteString("INSERT INTO ")
	sb.WriteString(g.Wrap(table))
	sb.WriteString(" (")
	for i, c := range cols {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(g.Wrap(c))
	}
	sb.WriteString(") VALUES ")

	if rows < 1 {
		rows = 1
	}
	n := 0
	for r := 0; r < rows; r++ {
		if r > 0 {
			sb.WriteString(", ")
		}
		sb.WriteByte('(')
		for c := range cols {
			if c > 0 {
				sb.WriteString(", ")
			}
			n++
			sb.WriteString(g.Placeholder(n))
		}
		sb.WriteByte(')')
	}
	return sb.String()
}

// compileInsert builds an INSERT. returning appends RETURNING (Postgres) to
// yield the generated id; only for a single-row incrementing-key insert.
func compileInsert(g Grammar, s InsertStmt, returning bool) (string, bool) {
	sql := insertInto(g, s.Table, s.Columns, s.Rows)
	if returning && s.Rows <= 1 && s.Incrementing && s.PrimaryKey != "" {
		return sql + " RETURNING " + g.Wrap(s.PrimaryKey), true
	}
	return sql, false
}

// compileUpsert builds INSERT ... ON CONFLICT (cols) DO UPDATE SET c = EXCLUDED.c
// (the Postgres/SQLite form). Empty UpdateColumns yields DO NOTHING.
func compileUpsert(g Grammar, s UpsertStmt) string {
	var sb strings.Builder
	sb.WriteString(insertInto(g, s.Table, s.Columns, s.Rows))

	sb.WriteString(" ON CONFLICT (")
	for i, c := range s.ConflictColumns {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(g.Wrap(c))
	}
	sb.WriteByte(')')

	if len(s.UpdateColumns) == 0 {
		sb.WriteString(" DO NOTHING")
		return sb.String()
	}

	sb.WriteString(" DO UPDATE SET ")
	for i, c := range s.UpdateColumns {
		if i > 0 {
			sb.WriteString(", ")
		}
		w := g.Wrap(c)
		sb.WriteString(w)
		sb.WriteString(" = EXCLUDED.")
		sb.WriteString(w)
	}
	return sb.String()
}

// compileUpsertMySQL builds INSERT ... ON DUPLICATE KEY UPDATE c = VALUES(c).
// MySQL keys off the table's unique indexes, so ConflictColumns is ignored.
// With no UpdateColumns it self-assigns the first column (an idempotent no-op,
// MySQL has no DO NOTHING).
func compileUpsertMySQL(g Grammar, s UpsertStmt) string {
	var sb strings.Builder
	sb.WriteString(insertInto(g, s.Table, s.Columns, s.Rows))
	sb.WriteString(" ON DUPLICATE KEY UPDATE ")

	cols := s.UpdateColumns
	if len(cols) == 0 && len(s.Columns) > 0 {
		cols = s.Columns[:1] // no-op self-assign
	}
	for i, c := range cols {
		if i > 0 {
			sb.WriteString(", ")
		}
		w := g.Wrap(c)
		sb.WriteString(w)
		sb.WriteString(" = VALUES(")
		sb.WriteString(w)
		sb.WriteByte(')')
	}
	return sb.String()
}

// compileUpdate builds an UPDATE, numbering placeholders contiguously across the
// SET assignments and the WHERE predicates.
func compileUpdate(g Grammar, s UpdateStmt) string {
	var sb strings.Builder
	sb.WriteString("UPDATE ")
	sb.WriteString(g.Wrap(s.Table))
	sb.WriteString(" SET ")

	n := 0
	for i, c := range s.Columns {
		if i > 0 {
			sb.WriteString(", ")
		}
		n++
		sb.WriteString(g.Wrap(c))
		sb.WriteString(" = ")
		sb.WriteString(g.Placeholder(n))
	}

	if len(s.Wheres) > 0 {
		clause, _ := compileWheres(g, s.Wheres, &n)
		sb.WriteString(" WHERE ")
		sb.WriteString(clause)
	}

	return sb.String()
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
