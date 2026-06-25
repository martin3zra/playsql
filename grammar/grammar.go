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
	default:
		return nil
	}
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
