package grammar

import (
	"strconv"
	"strings"
)

// MSSQL is the SQL Server grammar: [bracket] quoting, @pN placeholders, OFFSET/
// FETCH paging (no LIMIT), OUTPUT INSERTED for generated ids, and MERGE for
// upserts.
type MSSQL struct{}

func (MSSQL) Wrap(id string) string { return wrapQualified(id, "[", "]") }

func (MSSQL) Placeholder(n int) string { return "@p" + strconv.Itoa(n) }

func (g MSSQL) CompileSelect(q CompiledQuery) (string, []any) {
	return compileSelectOffsetFetch(g, q)
}

func (g MSSQL) CompileInsert(s InsertStmt) (string, bool) {
	single := s.Rows <= 1
	returnsID := single && s.Incrementing && s.PrimaryKey != ""

	var sb strings.Builder
	sb.WriteString("INSERT INTO ")
	sb.WriteString(g.Wrap(s.Table))
	sb.WriteString(" (")
	sb.WriteString(wrapList(g, s.Columns))
	sb.WriteByte(')')

	// OUTPUT goes between the column list and VALUES in T-SQL.
	if returnsID {
		sb.WriteString(" OUTPUT INSERTED.")
		sb.WriteString(g.Wrap(s.PrimaryKey))
	}

	sb.WriteString(" VALUES ")
	sb.WriteString(valueTuples(g, len(s.Columns), s.Rows, 0))
	return sb.String(), returnsID
}

// CompileUpdate emits OUTPUT INSERTED.<col> between SET and WHERE — T-SQL's
// equivalent of RETURNING. With no Returning columns it falls back to the shared
// helper (no return clause).
func (g MSSQL) CompileUpdate(s UpdateStmt) (string, bool) {
	if len(s.Returning) == 0 {
		return compileUpdate(g, s, "")
	}

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

	sb.WriteString(" OUTPUT ")
	for i, c := range s.Returning {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("INSERTED.")
		sb.WriteString(g.Wrap(c))
	}

	if len(s.Wheres) > 0 {
		clause, _ := compileWheres(g, s.Wheres, &n)
		sb.WriteString(" WHERE ")
		sb.WriteString(clause)
	}

	return sb.String(), true
}

func (g MSSQL) CompileDelete(s DeleteStmt) string { return compileDelete(g, s) }

// CompileUpsert builds a MERGE statement. SQL Server has no ON CONFLICT; MERGE
// matches on the conflict columns, updates on match, inserts otherwise.
func (g MSSQL) CompileUpsert(s UpsertStmt) string {
	var sb strings.Builder
	sb.WriteString("MERGE INTO ")
	sb.WriteString(g.Wrap(s.Table))
	sb.WriteString(" AS tgt USING (VALUES ")
	sb.WriteString(valueTuples(g, len(s.Columns), s.Rows, 0))
	sb.WriteString(") AS src (")
	sb.WriteString(wrapList(g, s.Columns))
	sb.WriteString(") ON ")
	for i, c := range s.ConflictColumns {
		if i > 0 {
			sb.WriteString(" AND ")
		}
		w := g.Wrap(c)
		sb.WriteString("tgt.")
		sb.WriteString(w)
		sb.WriteString(" = src.")
		sb.WriteString(w)
	}

	if len(s.UpdateColumns) > 0 {
		sb.WriteString(" WHEN MATCHED THEN UPDATE SET ")
		for i, c := range s.UpdateColumns {
			if i > 0 {
				sb.WriteString(", ")
			}
			w := g.Wrap(c)
			sb.WriteString("tgt.")
			sb.WriteString(w)
			sb.WriteString(" = src.")
			sb.WriteString(w)
		}
	}

	sb.WriteString(" WHEN NOT MATCHED THEN INSERT (")
	sb.WriteString(wrapList(g, s.Columns))
	sb.WriteString(") VALUES (")
	for i, c := range s.Columns {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("src.")
		sb.WriteString(g.Wrap(c))
	}
	sb.WriteString(");")
	return sb.String()
}

func (g MSSQL) JSONExtract(column, path string) string {
	return "JSON_VALUE(" + g.Wrap(column) + ", '" + jsonPath(path) + "')"
}

// wrapList joins wrapped column names with ", ".
func wrapList(g Grammar, cols []string) string {
	wrapped := make([]string, len(cols))
	for i, c := range cols {
		wrapped[i] = g.Wrap(c)
	}
	return strings.Join(wrapped, ", ")
}

// valueTuples renders "(p1, p2), (p3, p4), ..." with placeholders numbered from
// startN+1 across rows rows of cols columns each.
func valueTuples(g Grammar, cols, rows, startN int) string {
	if rows < 1 {
		rows = 1
	}
	var sb strings.Builder
	n := startN
	for r := 0; r < rows; r++ {
		if r > 0 {
			sb.WriteString(", ")
		}
		sb.WriteByte('(')
		for c := 0; c < cols; c++ {
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
