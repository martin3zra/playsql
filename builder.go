package playsql

import (
	"context"
	"reflect"

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
	limit   int
	offset  int
	err     error // first construction error; surfaced by terminal ops
}

func newBuilder(s *session, model any) *Builder {
	return &Builder{sess: s, meta: metadata.For(model)}
}

// fail records the first construction error. Builder methods stay chainable; the
// error surfaces at the next terminal op (Get/First/...).
func (b *Builder) fail(err error) *Builder {
	if b.err == nil {
		b.err = err
	}
	return b
}

// Select restricts the columns retrieved. Empty => all columns.
func (b *Builder) Select(columns ...string) *Builder {
	b.columns = columns
	return b
}

// Where adds a bound predicate (AND). The value is parameterized.
func (b *Builder) Where(column, op string, value any) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereBasic, Boolean: "AND", Column: column, Op: op, Value: value})
}

// OrWhere adds a bound predicate joined with OR.
func (b *Builder) OrWhere(column, op string, value any) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereBasic, Boolean: "OR", Column: column, Op: op, Value: value})
}

// WhereEq is shorthand for Where(column, "=", value).
func (b *Builder) WhereEq(column string, value any) *Builder {
	return b.Where(column, "=", value)
}

// WhereIn matches column against a set. Pass values variadically or as a single
// slice: WhereIn("id", 1, 2, 3) or WhereIn("id", []int64{1,2,3}).
func (b *Builder) WhereIn(column string, values ...any) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereIn, Boolean: "AND", Column: column, Values: expandValues(values)})
}

// WhereNotIn is the negation of WhereIn.
func (b *Builder) WhereNotIn(column string, values ...any) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereNotIn, Boolean: "AND", Column: column, Values: expandValues(values)})
}

// WhereNull matches rows where column IS NULL.
func (b *Builder) WhereNull(column string) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereNull, Boolean: "AND", Column: column})
}

// WhereNotNull matches rows where column IS NOT NULL.
func (b *Builder) WhereNotNull(column string) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereNotNull, Boolean: "AND", Column: column})
}

// WhereBetween matches column within [low, high] inclusive.
func (b *Builder) WhereBetween(column string, low, high any) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereBetween, Boolean: "AND", Column: column, Values: []any{low, high}})
}

// WhereNotBetween matches column outside [low, high].
func (b *Builder) WhereNotBetween(column string, low, high any) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereNotBetween, Boolean: "AND", Column: column, Values: []any{low, high}})
}

// WhereGroup adds a parenthesized group of predicates joined with AND to the
// outer query. Inside the closure, use the sub-builder's Where/OrWhere to shape
// the group: WhereGroup(func(q) { q.WhereEq("a", 1).OrWhere("b", ">", 2) }).
func (b *Builder) WhereGroup(fn func(*Builder)) *Builder {
	return b.group("AND", fn)
}

// OrWhereGroup is WhereGroup joined with OR.
func (b *Builder) OrWhereGroup(fn func(*Builder)) *Builder {
	return b.group("OR", fn)
}

func (b *Builder) group(boolean string, fn func(*Builder)) *Builder {
	sub := &Builder{sess: b.sess, meta: b.meta}
	fn(sub)
	if sub.err != nil {
		return b.fail(sub.err)
	}
	return b.add(grammar.WhereClause{Kind: grammar.WhereNested, Boolean: boolean, Group: sub.wheres})
}

func (b *Builder) add(w grammar.WhereClause) *Builder {
	b.wheres = append(b.wheres, w)
	return b
}

// expandValues lets WhereIn accept either variadic values or a single slice.
func expandValues(values []any) []any {
	if len(values) != 1 {
		return values
	}
	v := reflect.ValueOf(values[0])
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return values
	}
	out := make([]any, v.Len())
	for i := range out {
		out[i] = v.Index(i).Interface()
	}
	return out
}

// Limit caps the number of rows returned (0 = no limit).
func (b *Builder) Limit(n int) *Builder { b.limit = n; return b }

// Offset skips n rows.
func (b *Builder) Offset(n int) *Builder { b.offset = n; return b }

// Take is an alias for Limit (Eloquent-style).
func (b *Builder) Take(n int) *Builder { return b.Limit(n) }

// Skip is an alias for Offset (Eloquent-style).
func (b *Builder) Skip(n int) *Builder { return b.Offset(n) }

// compiled assembles the dialect-neutral query from the builder's state.
func (b *Builder) compiled() grammar.CompiledQuery {
	cols := b.columns
	if len(cols) == 0 {
		cols = b.meta.ColumnNames()
	}
	return grammar.CompiledQuery{
		Table:   b.meta.Table,
		Columns: cols,
		Wheres:  b.wheres,
		Limit:   b.limit,
		Offset:  b.offset,
	}
}

// Get executes the query and scans all rows into dest (a *[]T or *[]*T).
func (b *Builder) Get(ctx context.Context, dest any) error {
	if b.err != nil {
		return b.err
	}

	sqlStr, args := b.sess.grammar.CompileSelect(b.compiled())

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

// First scans the first matching row into dest (a *T). Returns ErrNotFound when
// nothing matches.
func (b *Builder) First(ctx context.Context, dest any) error {
	if b.err != nil {
		return b.err
	}

	b.limit = 1
	sqlStr, args := b.sess.grammar.CompileSelect(b.compiled())

	rows, err := b.sess.run.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	return scanOne(rows, dest, b.meta)
}

// Find scans the row whose primary key equals id into dest (a *T). The id is
// bound as a parameter as-is — no type assertion — so int, int64, string, and
// UUID keys all work. Returns ErrNotFound when nothing matches.
func (b *Builder) Find(ctx context.Context, dest, id any) error {
	return b.WhereEq(b.meta.PrimaryKey, id).First(ctx, dest)
}

// Count returns the number of matching rows, ignoring any limit/offset.
func (b *Builder) Count(ctx context.Context) (int64, error) {
	if b.err != nil {
		return 0, b.err
	}

	q := b.compiled()
	q.Aggregate = "COUNT(*)"
	q.Limit, q.Offset = 0, 0
	sqlStr, args := b.sess.grammar.CompileSelect(q)

	var count int64
	if err := b.sess.run.QueryRowContext(ctx, sqlStr, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
