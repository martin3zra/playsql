package playsql

import (
	"context"

	"github.com/martin3zra/playsql/grammar"
	"github.com/martin3zra/playsql/metadata"
)

// Builder is the only query entry point. It is created fresh per query (never
// reused) and is agnostic to whether it runs on a connection or a transaction.
type Builder struct {
	sess    *session
	meta    *metadata.ModelMeta
	columns []string
	wheres  []grammar.WhereClause
}

func newBuilder(s *session, model any) *Builder {
	return &Builder{sess: s, meta: metadata.For(model)}
}

// Select restricts the columns retrieved. Empty => all columns.
func (b *Builder) Select(columns ...string) *Builder {
	b.columns = columns
	return b
}

// Where adds a bound predicate. The value is parameterized, never interpolated.
func (b *Builder) Where(column, op string, value any) *Builder {
	b.wheres = append(b.wheres, grammar.WhereClause{Column: column, Op: op, Value: value})
	return b
}

// WhereEq is shorthand for Where(column, "=", value).
func (b *Builder) WhereEq(column string, value any) *Builder {
	return b.Where(column, "=", value)
}

// Get executes the query and scans all rows into dest (a *[]T or *[]*T).
func (b *Builder) Get(ctx context.Context, dest any) error {
	cols := b.columns
	if len(cols) == 0 {
		cols = b.meta.ColumnNames()
	}

	sqlStr, args := b.sess.grammar.CompileSelect(grammar.CompiledQuery{
		Table:   b.meta.Table,
		Columns: cols,
		Wheres:  b.wheres,
	})

	rows, err := b.sess.run.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	if err := scanRows(rows, dest, b.meta); err != nil {
		return err
	}
	return rows.Err()
}
