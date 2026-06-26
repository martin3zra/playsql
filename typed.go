package playsql

import "context"

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

func (t *TypedBuilder[T]) OrWhere(column, op string, value any) *TypedBuilder[T] {
	t.b.OrWhere(column, op, value)
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
