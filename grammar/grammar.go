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
	WhereJSON                        // <json-extract(Column, Path)> Op ?
	WhereRaw                         // <Raw> verbatim, no binds
	WhereColumn                      // Column Op Second (both wrapped), no bind
	WhereExists                      // [NOT] EXISTS (...) or (SELECT COUNT(*) ...) Op ?
	WhereInSub                       // Column IN (SELECT Sub.Column FROM Sub.Table WHERE ...)
)

// WhereClause is one compiled predicate. All values are bound, never
// interpolated. Boolean is the connector to the previous clause ("AND"/"OR").
type WhereClause struct {
	Kind    WhereKind
	Boolean string // "AND" (default) | "OR"
	Column  string
	Op      string
	Path    string          // WhereJSON: dotted path into the JSON column
	Raw     string          // WhereRaw: verbatim SQL fragment
	Second  string          // WhereColumn: the right-hand column (wrapped, not bound)
	Value   any             // WhereBasic / WhereJSON
	Values  []any           // WhereIn / WhereNotIn / WhereBetween
	Group   []WhereClause   // WhereNested
	Exists  *RelationExists // WhereExists
	Sub     *Subselect      // WhereInSub
}

// Subselect is a single-column correlated subquery used by WhereInSub:
// "SELECT Column FROM Table WHERE Wheres". It backs the exact far-row count for
// has*Through existence. Wheres carries the correlation (and is bind-free in that
// use), threaded through the outer placeholder counter.
type Subselect struct {
	Column string // projected (and compared-against) column, qualified
	Table  string
	Wheres []WhereClause
}

// RelationExists describes a correlated subquery predicate (the EXISTS form
// behind Has/WhereHas/DoesntHave). On holds the correlation (and any join)
// predicates — WhereColumn comparisons that carry no binds; Wheres holds the
// closure constraints and may itself contain nested RelationExists. With CountOp
// empty it renders "[NOT] EXISTS (SELECT 1 FROM Table WHERE ...)"; with CountOp
// set it renders "(SELECT COUNT(*) FROM Table WHERE ...) CountOp ?".
type RelationExists struct {
	Not      bool          // NOT EXISTS (doesntHave); ignored when CountOp is set
	Table    string        // the inner (related) table
	On       []WhereClause // correlation predicates (no binds)
	Wheres   []WhereClause // closure constraints + nested existence
	CountOp  string        // "" => plain EXISTS; else ">=", "<", ... => COUNT(*) form
	CountVal any           // bound value for the COUNT comparison
}

// CompiledQuery is the dialect-neutral description of a SELECT the builder
// produced. It carries no SQL text and no placeholders — those are the
// grammar's job.
type CompiledQuery struct {
	Table      string
	Columns    []string          // empty => SELECT *
	Aggregate  string            // e.g. "COUNT(*)"; overrides Columns, emitted verbatim
	Aggregates []AggregateSelect // extra correlated-subquery columns (withCount/withSum/…)
	Wheres     []WhereClause
	Orders     []OrderClause
	Limit      int // 0 => no LIMIT
	Offset     int // 0 => no OFFSET
}

// AggregateSelect is a correlated scalar subquery emitted as an extra select
// column, e.g. "(SELECT COUNT(*) FROM comments WHERE comments.post_id =
// posts.id) AS comments_count". On carries the correlation (WhereColumn, or
// WhereInSub for many-to-many/through); Wheres carries constraint/soft-delete
// predicates. Func EXISTS renders a CASE WHEN EXISTS form (SQL Server forbids a
// bare EXISTS in a select list).
type AggregateSelect struct {
	Func   string // COUNT | SUM | AVG | MIN | MAX | EXISTS
	Column string // aggregated column (qualified); "" => COUNT(*); ignored for EXISTS
	Table  string
	On     []WhereClause
	Wheres []WhereClause
	Alias  string
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

// CTE is a common table expression prepended to a statement as
// WITH <Name> AS (<SQL>). SQL is rendered verbatim and must not carry bind
// parameters — it sits before the statement's own binds, so embedding
// placeholders would break contiguous numbering on $-style dialects.
type CTE struct {
	Name string
	SQL  string
}

// UpdateStmt describes an UPDATE. Set columns first, then Wheres; the grammar
// numbers placeholders contiguously across both. Optional CTEs prepend a WITH
// clause; optional Returning names columns to return from the affected rows
// (RETURNING on Postgres/SQLite, OUTPUT INSERTED on SQL Server; MySQL has none).
type UpdateStmt struct {
	Table     string
	Columns   []string // SET columns
	Wheres    []WhereClause
	CTEs      []CTE    // optional WITH ... prefix
	Returning []string // optional columns to return; empty => none
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
	// CompileUpdate returns the statement and whether it yields the affected
	// rows via a RETURNING/OUTPUT clause (true) — driving Exec vs Query.
	CompileUpdate(s UpdateStmt) (sql string, returnsRows bool)
	CompileDelete(s DeleteStmt) (sql string)
	CompileUpsert(s UpsertStmt) (sql string)
	Wrap(identifier string) string
	Placeholder(n int) string // n is 1-based bind position
	// JSONExtract renders a SQL expression extracting a value (as text) from a
	// JSON column at a dotted path, e.g. "prefs" + "theme" -> the dialect form.
	JSONExtract(column, path string) string
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
	case "sqlserver", "mssql":
		return MSSQL{}
	default:
		return nil
	}
}

// selectCore builds everything up to (and including) ORDER BY, leaving the
// dialect-specific row-limiting clause to the caller. hasOrder reports whether
// an ORDER BY was emitted.
func selectCore(g Grammar, q CompiledQuery) (sql string, args []any, hasOrder bool) {
	var sb strings.Builder
	sb.WriteString("SELECT ")

	// n threads contiguously across aggregate subquery columns (which may carry
	// binds from constraint closures) and then the WHERE clause.
	n := 0
	if q.Aggregate != "" {
		sb.WriteString(q.Aggregate) // verbatim, e.g. COUNT(*)
	} else {
		if len(q.Columns) > 0 {
			for i, c := range q.Columns {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(g.Wrap(c))
			}
		} else {
			sb.WriteByte('*')
		}
		for _, a := range q.Aggregates {
			sb.WriteString(", ")
			args = append(args, compileAggregate(g, &sb, a, &n)...)
		}
	}

	sb.WriteString(" FROM ")
	sb.WriteString(g.Wrap(q.Table))

	if len(q.Wheres) > 0 {
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
		hasOrder = true
	}

	return sb.String(), args, hasOrder
}

// compileSelect is the LIMIT/OFFSET assembler shared by SQLite/Postgres/MySQL.
func compileSelect(g Grammar, q CompiledQuery) (string, []any) {
	sql, args, _ := selectCore(g, q)
	if q.Limit > 0 {
		sql += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	if q.Offset > 0 {
		sql += fmt.Sprintf(" OFFSET %d", q.Offset)
	}
	return sql, args
}

// compileSelectOffsetFetch is the SQL Server form: OFFSET n ROWS FETCH NEXT m
// ROWS ONLY, which requires an ORDER BY (a stable fallback is added when none).
func compileSelectOffsetFetch(g Grammar, q CompiledQuery) (string, []any) {
	sql, args, hasOrder := selectCore(g, q)
	if q.Limit <= 0 && q.Offset <= 0 {
		return sql, args
	}
	if !hasOrder {
		sql += " ORDER BY (SELECT NULL)"
	}
	sql += fmt.Sprintf(" OFFSET %d ROWS", q.Offset)
	if q.Limit > 0 {
		sql += fmt.Sprintf(" FETCH NEXT %d ROWS ONLY", q.Limit)
	}
	return sql, args
}

// jsonPath turns a dotted path ("a.b") into a SQL/JSON path ("$.a.b"). An empty
// path is the document root "$".
func jsonPath(path string) string {
	if path == "" {
		return "$"
	}
	return "$." + path
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
// SET assignments and the WHERE predicates. An optional WITH prefix is emitted
// from s.CTEs. When kw is non-empty (the dialect's return keyword, "RETURNING")
// and columns are requested, a trailing return clause is appended and the second
// result is true. SQL Server differs (OUTPUT mid-statement) and does not use
// this helper for returns.
func compileUpdate(g Grammar, s UpdateStmt, kw string) (string, bool) {
	var sb strings.Builder
	writeCTEs(g, &sb, s.CTEs)

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

	if kw != "" && len(s.Returning) > 0 {
		sb.WriteString(" ")
		sb.WriteString(kw)
		sb.WriteString(" ")
		sb.WriteString(wrapList(g, s.Returning))
		return sb.String(), true
	}
	return sb.String(), false
}

// writeCTEs emits a "WITH name AS (sql), ..." prefix (with a trailing space)
// when ctes is non-empty. The SQL bodies are verbatim; see CTE.
func writeCTEs(g Grammar, sb *strings.Builder, ctes []CTE) {
	if len(ctes) == 0 {
		return
	}
	sb.WriteString("WITH ")
	for i, c := range ctes {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(g.Wrap(c.Name))
		sb.WriteString(" AS (")
		sb.WriteString(c.SQL)
		sb.WriteByte(')')
	}
	sb.WriteByte(' ')
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

		case WhereJSON:
			*n++
			sb.WriteString(g.JSONExtract(w.Column, w.Path))
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

		case WhereRaw:
			// Verbatim fragment; carries no binds and consumes no placeholder.
			sb.WriteString(w.Raw)

		case WhereColumn:
			// Column-to-column comparison; both sides wrapped, no bind.
			sb.WriteString(g.Wrap(w.Column))
			sb.WriteByte(' ')
			sb.WriteString(w.Op)
			sb.WriteByte(' ')
			sb.WriteString(g.Wrap(w.Second))

		case WhereExists:
			args = append(args, compileExists(g, &sb, w.Exists, n)...)

		case WhereInSub:
			sub := w.Sub
			sb.WriteString(g.Wrap(w.Column))
			sb.WriteString(" IN (SELECT ")
			sb.WriteString(g.Wrap(sub.Column))
			sb.WriteString(" FROM ")
			sb.WriteString(g.Wrap(sub.Table))
			if len(sub.Wheres) > 0 {
				clause, subArgs := compileWheres(g, sub.Wheres, n)
				sb.WriteString(" WHERE ")
				sb.WriteString(clause)
				args = append(args, subArgs...)
			}
			sb.WriteByte(')')

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

// compileExists renders a RelationExists into sb, threading the bind counter n
// through the inner predicates (correlation first, then closure constraints) so
// placeholders stay contiguous with the outer query. Returns the bound args in
// emission order. Recurses via compileWheres for nested existence.
func compileExists(g Grammar, sb *strings.Builder, ex *RelationExists, n *int) []any {
	inner := make([]WhereClause, 0, len(ex.On)+len(ex.Wheres))
	inner = append(inner, ex.On...)
	inner = append(inner, ex.Wheres...)

	if ex.CountOp != "" {
		sb.WriteString("(SELECT COUNT(*) FROM ")
		sb.WriteString(g.Wrap(ex.Table))
		args := writeExistsWhere(g, sb, inner, n)
		sb.WriteString(") ")
		sb.WriteString(ex.CountOp)
		sb.WriteByte(' ')
		*n++
		sb.WriteString(g.Placeholder(*n))
		return append(args, ex.CountVal)
	}

	if ex.Not {
		sb.WriteString("NOT ")
	}
	sb.WriteString("EXISTS (SELECT 1 FROM ")
	sb.WriteString(g.Wrap(ex.Table))
	args := writeExistsWhere(g, sb, inner, n)
	sb.WriteByte(')')
	return args
}

// writeExistsWhere appends " WHERE <clauses>" to sb when clauses is non-empty,
// threading n, and returns the bound args.
func writeExistsWhere(g Grammar, sb *strings.Builder, clauses []WhereClause, n *int) []any {
	if len(clauses) == 0 {
		return nil
	}
	clause, args := compileWheres(g, clauses, n)
	sb.WriteString(" WHERE ")
	sb.WriteString(clause)
	return args
}

// GroupedAggregate describes the batched query behind deferred aggregate loading
// (LoadCount/LoadSum/…): one aggregate per key value over a related table.
type GroupedAggregate struct {
	Table     string
	KeyColumn string // grouped/selected key (the join column on the related side)
	Func      string // COUNT | SUM | AVG | MIN | MAX
	Column    string // aggregated column; "" => FN(*)
	Wheres    []WhereClause
}

// CompileGroupedAggregate builds
// "SELECT <key>, <fn>(<col|*>) AS agg FROM <table> WHERE <wheres> GROUP BY <key>".
// Wheres typically carries the parent-key IN list plus constraints/soft-delete.
func CompileGroupedAggregate(g Grammar, q GroupedAggregate) (string, []any) {
	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(g.Wrap(q.KeyColumn))
	sb.WriteString(", ")
	sb.WriteString(q.Func)
	sb.WriteByte('(')
	if q.Column == "" {
		sb.WriteByte('*')
	} else {
		sb.WriteString(g.Wrap(q.Column))
	}
	sb.WriteString(") AS ")
	sb.WriteString(g.Wrap("agg"))
	sb.WriteString(" FROM ")
	sb.WriteString(g.Wrap(q.Table))

	var args []any
	if len(q.Wheres) > 0 {
		n := 0
		clause, wargs := compileWheres(g, q.Wheres, &n)
		sb.WriteString(" WHERE ")
		sb.WriteString(clause)
		args = wargs
	}

	sb.WriteString(" GROUP BY ")
	sb.WriteString(g.Wrap(q.KeyColumn))
	return sb.String(), args
}

// compileAggregate renders one correlated aggregate subquery as a select column,
// threading n through its correlation/constraint binds. EXISTS uses a portable
// CASE WHEN EXISTS form; the rest are scalar (SELECT fn(col) FROM ...) subqueries.
func compileAggregate(g Grammar, sb *strings.Builder, a AggregateSelect, n *int) []any {
	var args []any
	if a.Func == "EXISTS" {
		sb.WriteString("CASE WHEN EXISTS (SELECT 1 FROM ")
		sb.WriteString(g.Wrap(a.Table))
		args = writeAggWhere(g, sb, a.On, a.Wheres, n)
		sb.WriteString(") THEN 1 ELSE 0 END")
	} else {
		sb.WriteString("(SELECT ")
		sb.WriteString(a.Func)
		sb.WriteByte('(')
		if a.Column == "" {
			sb.WriteByte('*')
		} else {
			sb.WriteString(g.Wrap(a.Column))
		}
		sb.WriteByte(')')
		sb.WriteString(" FROM ")
		sb.WriteString(g.Wrap(a.Table))
		args = writeAggWhere(g, sb, a.On, a.Wheres, n)
		sb.WriteByte(')')
	}
	sb.WriteString(" AS ")
	sb.WriteString(g.Wrap(a.Alias))
	return args
}

// writeAggWhere appends " WHERE <on AND wheres>" to sb when present, threading n.
func writeAggWhere(g Grammar, sb *strings.Builder, on, wheres []WhereClause, n *int) []any {
	combined := make([]WhereClause, 0, len(on)+len(wheres))
	combined = append(combined, on...)
	combined = append(combined, wheres...)
	if len(combined) == 0 {
		return nil
	}
	clause, args := compileWheres(g, combined, n)
	sb.WriteString(" WHERE ")
	sb.WriteString(clause)
	return args
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
