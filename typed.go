package playsql

import (
	"context"
	"database/sql"
)

// Query starts a type-parameterized query for model T against a DB or Tx. It is
// a thin, allocation-light wrapper over *Builder: the terminal methods return
// typed results ([]T, T) instead of taking a destination pointer.
//
//	users, err := playsql.Query[User](db).
//		WhereEq("active", true).
//		OrderBy("id", playsql.Asc).
//		Get(ctx)
func Query[T any](src interface{ Model(any) *Builder }) *TypedBuilder[T] {
	var zero T
	return &TypedBuilder[T]{b: src.Model(&zero)}
}

// RawQuery runs an arbitrary query and scans the rows into a []T, mapping
// columns to fields via T's metadata. It is the generic counterpart to
// (*session).Raw — src is a *DB or *Tx.
//
//	users, err := playsql.RawQuery[User](db, ctx,
//		"SELECT * FROM users WHERE age > ?", 18)
func RawQuery[T any](src interface {
	Raw(ctx context.Context, dest any, query string, args ...any) error
}, ctx context.Context, query string, args ...any) ([]T, error) {
	var out []T
	if err := src.Raw(ctx, &out, query, args...); err != nil {
		return nil, err
	}
	return out, nil
}

// RawScalar runs a query expected to yield a single row with a single column and
// returns it as T. It wraps QueryRow().Scan, so it returns sql.ErrNoRows when no
// row matches. src is a *DB or *Tx.
//
//	n, err := playsql.RawScalar[int64](db, ctx, `SELECT COUNT(*) FROM users`)
func RawScalar[T any](src interface {
	rawRow(ctx context.Context, query string, args ...any) *sql.Row
}, ctx context.Context, query string, args ...any) (T, error) {
	var v T
	err := src.rawRow(ctx, query, args...).Scan(&v)
	return v, err
}

// TypedBuilder is a generic view over *Builder. Builder methods are mirrored so
// chains stay typed; Builder itself remains available via Unwrap for anything
// not surfaced here.
type TypedBuilder[T any] struct {
	b *Builder
}

// Unwrap returns the underlying untyped builder.
func (t *TypedBuilder[T]) Unwrap() *Builder { return t.b }

// --- predicates (each returns t for chaining) ---

func (t *TypedBuilder[T]) Where(column, op string, value any) *TypedBuilder[T] {
	t.b.Where(column, op, value)
	return t
}

func (t *TypedBuilder[T]) WhereEq(column string, value any) *TypedBuilder[T] {
	t.b.WhereEq(column, value)
	return t
}

// When runs fn only when condition is true (Laravel's when helper). The
// optional otherwise closure runs when condition is false.
func (t *TypedBuilder[T]) When(condition bool, fn func(*TypedBuilder[T]), otherwise ...func(*TypedBuilder[T])) *TypedBuilder[T] {
	if condition {
		if fn != nil {
			fn(t)
		}
	} else if len(otherwise) > 0 && otherwise[0] != nil {
		otherwise[0](t)
	}
	return t
}

// Unless is When with the condition inverted: fn runs when condition is false.
// The optional otherwise closure runs when condition is true.
func (t *TypedBuilder[T]) Unless(condition bool, fn func(*TypedBuilder[T]), otherwise ...func(*TypedBuilder[T])) *TypedBuilder[T] {
	return t.When(!condition, fn, otherwise...)
}

func (t *TypedBuilder[T]) OrWhere(column, op string, value any) *TypedBuilder[T] {
	t.b.OrWhere(column, op, value)
	return t
}

func (t *TypedBuilder[T]) WhereRaw(sql string) *TypedBuilder[T] {
	t.b.WhereRaw(sql)
	return t
}

func (t *TypedBuilder[T]) WhereColumn(column, op, other string) *TypedBuilder[T] {
	t.b.WhereColumn(column, op, other)
	return t
}

func (t *TypedBuilder[T]) OrWhereColumn(column, op, other string) *TypedBuilder[T] {
	t.b.OrWhereColumn(column, op, other)
	return t
}

// AddSelect adds a correlated subquery column (see (*Builder).AddSelect).
func (t *TypedBuilder[T]) AddSelect(alias string, sub *Builder) *TypedBuilder[T] {
	t.b.AddSelect(alias, sub)
	return t
}

// OrderBySubquery orders by a correlated subquery (see (*Builder).OrderBySubquery).
func (t *TypedBuilder[T]) OrderBySubquery(sub *Builder, dir Direction) *TypedBuilder[T] {
	t.b.OrderBySubquery(sub, dir)
	return t
}

// --- relationship aggregates (mirrors *Builder) ---

func (t *TypedBuilder[T]) WithCount(relation string, opts ...AggOption) *TypedBuilder[T] {
	t.b.WithCount(relation, opts...)
	return t
}

func (t *TypedBuilder[T]) WithSum(relation, column string, opts ...AggOption) *TypedBuilder[T] {
	t.b.WithSum(relation, column, opts...)
	return t
}

func (t *TypedBuilder[T]) WithAvg(relation, column string, opts ...AggOption) *TypedBuilder[T] {
	t.b.WithAvg(relation, column, opts...)
	return t
}

func (t *TypedBuilder[T]) WithMin(relation, column string, opts ...AggOption) *TypedBuilder[T] {
	t.b.WithMin(relation, column, opts...)
	return t
}

func (t *TypedBuilder[T]) WithMax(relation, column string, opts ...AggOption) *TypedBuilder[T] {
	t.b.WithMax(relation, column, opts...)
	return t
}

func (t *TypedBuilder[T]) WithExists(relation string, opts ...AggOption) *TypedBuilder[T] {
	t.b.WithExists(relation, opts...)
	return t
}

// --- relationship existence (mirrors *Builder) ---

func (t *TypedBuilder[T]) Has(relation string) *TypedBuilder[T] {
	t.b.Has(relation)
	return t
}

func (t *TypedBuilder[T]) OrHas(relation string) *TypedBuilder[T] {
	t.b.OrHas(relation)
	return t
}

func (t *TypedBuilder[T]) HasCount(relation, op string, count int) *TypedBuilder[T] {
	t.b.HasCount(relation, op, count)
	return t
}

func (t *TypedBuilder[T]) DoesntHave(relation string) *TypedBuilder[T] {
	t.b.DoesntHave(relation)
	return t
}

func (t *TypedBuilder[T]) OrDoesntHave(relation string) *TypedBuilder[T] {
	t.b.OrDoesntHave(relation)
	return t
}

func (t *TypedBuilder[T]) WhereHas(relation string, fn func(*Builder)) *TypedBuilder[T] {
	t.b.WhereHas(relation, fn)
	return t
}

func (t *TypedBuilder[T]) OrWhereHas(relation string, fn func(*Builder)) *TypedBuilder[T] {
	t.b.OrWhereHas(relation, fn)
	return t
}

func (t *TypedBuilder[T]) WhereHasCount(relation string, fn func(*Builder), op string, count int) *TypedBuilder[T] {
	t.b.WhereHasCount(relation, fn, op, count)
	return t
}

func (t *TypedBuilder[T]) WhereDoesntHave(relation string, fn func(*Builder)) *TypedBuilder[T] {
	t.b.WhereDoesntHave(relation, fn)
	return t
}

func (t *TypedBuilder[T]) OrWhereDoesntHave(relation string, fn func(*Builder)) *TypedBuilder[T] {
	t.b.OrWhereDoesntHave(relation, fn)
	return t
}

func (t *TypedBuilder[T]) WhereRelation(relation, column, op string, value any) *TypedBuilder[T] {
	t.b.WhereRelation(relation, column, op, value)
	return t
}

func (t *TypedBuilder[T]) OrWhereRelation(relation, column, op string, value any) *TypedBuilder[T] {
	t.b.OrWhereRelation(relation, column, op, value)
	return t
}

func (t *TypedBuilder[T]) WhereIn(column string, values ...any) *TypedBuilder[T] {
	t.b.WhereIn(column, values...)
	return t
}

func (t *TypedBuilder[T]) WhereNotIn(column string, values ...any) *TypedBuilder[T] {
	t.b.WhereNotIn(column, values...)
	return t
}

func (t *TypedBuilder[T]) WhereNull(column string) *TypedBuilder[T] {
	t.b.WhereNull(column)
	return t
}

func (t *TypedBuilder[T]) WhereNotNull(column string) *TypedBuilder[T] {
	t.b.WhereNotNull(column)
	return t
}

func (t *TypedBuilder[T]) WhereBetween(column string, low, high any) *TypedBuilder[T] {
	t.b.WhereBetween(column, low, high)
	return t
}

func (t *TypedBuilder[T]) WhereJSON(column, path, op string, value any) *TypedBuilder[T] {
	t.b.WhereJSON(column, path, op, value)
	return t
}

func (t *TypedBuilder[T]) WhereGroup(fn func(*Builder)) *TypedBuilder[T] {
	t.b.WhereGroup(fn)
	return t
}

// --- shaping ---

func (t *TypedBuilder[T]) Select(columns ...string) *TypedBuilder[T] {
	t.b.Select(columns...)
	return t
}

func (t *TypedBuilder[T]) OrderBy(column string, dir Direction) *TypedBuilder[T] {
	t.b.OrderBy(column, dir)
	return t
}

func (t *TypedBuilder[T]) Limit(n int) *TypedBuilder[T]  { t.b.Limit(n); return t }
func (t *TypedBuilder[T]) Offset(n int) *TypedBuilder[T] { t.b.Offset(n); return t }
func (t *TypedBuilder[T]) Take(n int) *TypedBuilder[T]   { t.b.Take(n); return t }
func (t *TypedBuilder[T]) Skip(n int) *TypedBuilder[T]   { t.b.Skip(n); return t }

// --- relations / soft deletes ---

func (t *TypedBuilder[T]) With(relations ...string) *TypedBuilder[T] {
	t.b.With(relations...)
	return t
}

func (t *TypedBuilder[T]) WithConstraint(name string, fn func(*Builder)) *TypedBuilder[T] {
	t.b.WithConstraint(name, fn)
	return t
}

func (t *TypedBuilder[T]) WithTrashed() *TypedBuilder[T] { t.b.WithTrashed(); return t }
func (t *TypedBuilder[T]) OnlyTrashed() *TypedBuilder[T] { t.b.OnlyTrashed(); return t }

// --- terminals (typed results) ---

// Get returns all matching rows as a slice of T.
func (t *TypedBuilder[T]) Get(ctx context.Context) ([]T, error) {
	var out []T
	err := t.b.Get(ctx, &out)
	return out, err
}

// First returns the first matching row, or the zero T and ErrNotFound.
func (t *TypedBuilder[T]) First(ctx context.Context) (T, error) {
	var out T
	err := t.b.First(ctx, &out)
	return out, err
}

// Find returns the row with the given primary key, or zero T and ErrNotFound.
func (t *TypedBuilder[T]) Find(ctx context.Context, id any) (T, error) {
	var out T
	err := t.b.Find(ctx, &out, id)
	return out, err
}

// Count returns the number of matching rows.
func (t *TypedBuilder[T]) Count(ctx context.Context) (int64, error) {
	return t.b.Count(ctx)
}

// GetPtr returns all matching rows as a slice of *T.
func (t *TypedBuilder[T]) GetPtr(ctx context.Context) ([]*T, error) {
	var out []*T
	err := t.b.Get(ctx, &out)
	return out, err
}

// --- writes (map-based, mass-assignment filtered) ---

// Insert mass-assigns data as a new row and returns the generated id.
func (t *TypedBuilder[T]) Insert(ctx context.Context, data map[string]any) (int64, error) {
	return t.b.Insert(ctx, data)
}

// Create inserts data and returns the new row hydrated as a T.
func (t *TypedBuilder[T]) Create(ctx context.Context, data map[string]any) (T, error) {
	var out T
	err := t.b.Create(ctx, &out, data)
	return out, err
}

// Update mass-assigns data to all matching rows and returns the rows affected.
func (t *TypedBuilder[T]) Update(ctx context.Context, data map[string]any) (int64, error) {
	return t.b.Update(ctx, data)
}

// Returning names the columns UpdateReturning returns from the affected rows.
func (t *TypedBuilder[T]) Returning(columns ...string) *TypedBuilder[T] {
	t.b.Returning(columns...)
	return t
}

// WithCTE prepends a CTE (WITH name AS (rawSQL)) to an Update/UpdateReturning.
func (t *TypedBuilder[T]) WithCTE(name, rawSQL string) *TypedBuilder[T] {
	t.b.WithCTE(name, rawSQL)
	return t
}

// WithCTEQuery prepends a CTE built from a sub-query (may carry binds).
func (t *TypedBuilder[T]) WithCTEQuery(name string, sub *Builder) *TypedBuilder[T] {
	t.b.WithCTEQuery(name, sub)
	return t
}

// UpdateReturning mass-assigns data and returns the affected rows as a []T.
// Requires Returning(...) columns and a dialect with RETURNING/OUTPUT.
func (t *TypedBuilder[T]) UpdateReturning(ctx context.Context, data map[string]any) ([]T, error) {
	var out []T
	if err := t.b.UpdateReturning(ctx, data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// InsertMany bulk-inserts rows and returns the number inserted.
func (t *TypedBuilder[T]) InsertMany(ctx context.Context, rows []map[string]any) (int64, error) {
	return t.b.InsertMany(ctx, rows)
}

// Upsert inserts rows, updating updateColumns on a conflict over conflictColumns.
func (t *TypedBuilder[T]) Upsert(ctx context.Context, rows []map[string]any, conflictColumns, updateColumns []string) (int64, error) {
	return t.b.Upsert(ctx, rows, conflictColumns, updateColumns)
}

// Delete removes all matching rows (soft-deletes a soft-deletable model).
func (t *TypedBuilder[T]) Delete(ctx context.Context) (int64, error) {
	return t.b.Delete(ctx)
}
