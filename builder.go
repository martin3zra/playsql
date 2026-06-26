package playsql

import (
	"context"
	"reflect"
	"strings"
	"time"

	"github.com/martin3zra/playsql/grammar"
	"github.com/martin3zra/playsql/metadata"
)

// Direction is a sort order. Use Asc or Desc.
type Direction string

const (
	Asc  Direction = "ASC"
	Desc Direction = "DESC"
)

// trashedMode controls how soft-deleted rows are treated in a query.
type trashedMode int

const (
	trashedExclude trashedMode = iota // default: hide soft-deleted rows
	trashedInclude                    // WithTrashed: include them
	trashedOnly                       // OnlyTrashed: only soft-deleted rows
)

// Builder is the only query entry point. It is created fresh per query (never
// reused) and is agnostic to whether it runs on a connection or a transaction.
type Builder struct {
	sess        *session
	meta        *metadata.ModelMeta
	columns     []string
	wheres      []grammar.WhereClause
	scopeWheres []grammar.WhereClause // predicates added by global scopes
	orders      []grammar.OrderClause
	withs       []withClause
	limit       int
	offset      int
	trashed     trashedMode
	scopes      []Scope
	inScope     bool  // routes add() into scopeWheres while a scope applies
	scopesDone  bool  // scopes applied (or skipped) once already
	skipScopes  bool  // WithoutGlobalScopes
	err         error // first construction error; surfaced by terminal ops
}

func newBuilder(s *session, model any) *Builder {
	b := &Builder{sess: s, meta: metadata.For(model)}
	if sc, ok := model.(Scoper); ok {
		b.scopes = sc.Scopes()
	}
	return b
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

// WhereJSON filters on a value inside a JSON column at a dotted path, e.g.
// WhereJSON("prefs", "theme", "=", "dark") or WhereJSON("addr", "city", "=", x).
// The extracted value is compared as text.
func (b *Builder) WhereJSON(column, path, op string, value any) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereJSON, Boolean: "AND", Column: column, Path: path, Op: op, Value: value})
}

// OrWhereJSON is WhereJSON joined with OR.
func (b *Builder) OrWhereJSON(column, path, op string, value any) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereJSON, Boolean: "OR", Column: column, Path: path, Op: op, Value: value})
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
	if b.inScope {
		b.scopeWheres = append(b.scopeWheres, w)
	} else {
		b.wheres = append(b.wheres, w)
	}
	return b
}

// WithoutGlobalScopes skips the model's global scopes for this query.
func (b *Builder) WithoutGlobalScopes() *Builder { b.skipScopes = true; return b }

// applyScopes runs the model's global scopes once, routing the predicates they
// add into scopeWheres. A scope returning an error aborts the query.
func (b *Builder) applyScopes(ctx context.Context) error {
	if b.scopesDone || b.skipScopes || len(b.scopes) == 0 {
		b.scopesDone = true
		return nil
	}
	b.scopesDone = true
	b.inScope = true
	defer func() { b.inScope = false }()
	for _, s := range b.scopes {
		if err := s.Apply(ctx, b); err != nil {
			return err
		}
	}
	return nil
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

// withClause is one requested eager load: a dotted path of relation field names
// and an optional constraint applied to the deepest relation's query.
type withClause struct {
	segments   []string
	constraint func(*Builder)
}

// With eager-loads the named relationships after the query runs, batching each
// with a single WhereIn to avoid N+1. Names may be dotted for nesting, e.g.
// With("Comments.Author") loads each blog's comments and each comment's author.
func (b *Builder) With(relations ...string) *Builder {
	for _, r := range relations {
		b.withs = append(b.withs, withClause{segments: strings.Split(r, ".")})
	}
	return b
}

// WithConstraint eager-loads a single relationship (dotted path allowed),
// applying fn to the related query — e.g. to filter or order the loaded rows:
//
//	WithConstraint("Comments", func(q *playsql.Builder) {
//		q.WhereEq("approved", true).OrderBy("id", playsql.Desc)
//	})
func (b *Builder) WithConstraint(name string, fn func(*Builder)) *Builder {
	b.withs = append(b.withs, withClause{segments: strings.Split(name, "."), constraint: fn})
	return b
}

// WithTrashed includes soft-deleted rows in the query.
func (b *Builder) WithTrashed() *Builder { b.trashed = trashedInclude; return b }

// OnlyTrashed restricts the query to soft-deleted rows.
func (b *Builder) OnlyTrashed() *Builder { b.trashed = trashedOnly; return b }

// softDeletePredicate returns the deleted_at filter for the current trashed mode,
// or nil when none applies (not soft-deletable, or WithTrashed).
func (b *Builder) softDeletePredicate(mode trashedMode) *grammar.WhereClause {
	if !b.meta.SoftDeletes {
		return nil
	}
	switch mode {
	case trashedExclude:
		return &grammar.WhereClause{Kind: grammar.WhereNull, Column: b.meta.DeletedAtColumn}
	case trashedOnly:
		return &grammar.WhereClause{Kind: grammar.WhereNotNull, Column: b.meta.DeletedAtColumn}
	default: // trashedInclude
		return nil
	}
}

// effectiveWheres composes the final predicate list as
//
//	<scope predicates> AND <deleted_at filter> AND ( <user predicates> )
//
// Scope and soft-delete predicates are leading ANDs; the user predicates are
// wrapped in a group (when any leading predicate exists) so a user OR can't
// escape them. With no leading predicate the user wheres pass through unwrapped,
// preserving their own boolean structure.
func (b *Builder) effectiveWheres(mode trashedMode) []grammar.WhereClause {
	var lead []grammar.WhereClause
	lead = append(lead, b.scopeWheres...)
	if pred := b.softDeletePredicate(mode); pred != nil {
		lead = append(lead, *pred)
	}

	if len(lead) == 0 {
		return b.wheres
	}
	if len(b.wheres) == 0 {
		return lead
	}
	return append(lead, grammar.WhereClause{Kind: grammar.WhereNested, Boolean: "AND", Group: b.wheres})
}

// OrderBy adds an ORDER BY term. Call repeatedly for multiple sort keys.
func (b *Builder) OrderBy(column string, dir Direction) *Builder {
	b.orders = append(b.orders, grammar.OrderClause{Column: column, Direction: string(dir)})
	return b
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
		Wheres:  b.effectiveWheres(b.trashed),
		Orders:  b.orders,
		Limit:   b.limit,
		Offset:  b.offset,
	}
}

// Get executes the query and scans all rows into dest (a *[]T or *[]*T).
func (b *Builder) Get(ctx context.Context, dest any) error {
	if b.err != nil {
		return b.err
	}
	if err := b.applyScopes(ctx); err != nil {
		return err
	}

	sqlStr, args := b.sess.grammar.CompileSelect(b.compiled())

	rows, err := b.sess.run.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return err
	}

	err = scanRows(rows, dest, b.meta)
	if err == nil {
		err = rows.Err()
	}
	// Release the connection before eager-loading so relation queries can run
	// even on a single-connection pool (e.g. in-memory SQLite).
	rows.Close()
	if err != nil {
		return err
	}
	return b.loadRelations(ctx, dest)
}

// First scans the first matching row into dest (a *T). Returns ErrNotFound when
// nothing matches.
func (b *Builder) First(ctx context.Context, dest any) error {
	if b.err != nil {
		return b.err
	}
	if err := b.applyScopes(ctx); err != nil {
		return err
	}

	b.limit = 1
	sqlStr, args := b.sess.grammar.CompileSelect(b.compiled())

	rows, err := b.sess.run.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return err
	}

	err = scanOne(rows, dest, b.meta)
	// Release the connection before eager-loading (see Get).
	rows.Close()
	if err != nil {
		return err
	}
	return b.loadRelations(ctx, dest)
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
	if err := b.applyScopes(ctx); err != nil {
		return 0, err
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

// Delete removes all matching rows and returns the number affected. For a
// soft-deletable model this sets deleted_at instead of removing the row; use
// ForceDelete for a hard delete. With no WHERE constraints this affects every
// (non-trashed) row.
func (b *Builder) Delete(ctx context.Context) (int64, error) {
	if b.err != nil {
		return 0, b.err
	}
	if err := b.applyScopes(ctx); err != nil {
		return 0, err
	}

	if b.meta.SoftDeletes {
		// Soft delete: stamp deleted_at on rows not already trashed.
		wheres := b.effectiveWheres(trashedExclude)
		sqlStr := b.sess.grammar.CompileUpdate(grammar.UpdateStmt{
			Table:   b.meta.Table,
			Columns: []string{b.meta.DeletedAtColumn},
			Wheres:  wheres,
		})
		args := append([]any{time.Now()}, whereArgs(wheres)...)
		res, err := b.sess.run.ExecContext(ctx, sqlStr, args...)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}

	return b.hardDelete(ctx)
}

// ForceDelete permanently removes all matching rows, ignoring soft-delete.
func (b *Builder) ForceDelete(ctx context.Context) (int64, error) {
	if b.err != nil {
		return 0, b.err
	}
	if err := b.applyScopes(ctx); err != nil {
		return 0, err
	}
	return b.hardDelete(ctx)
}

func (b *Builder) hardDelete(ctx context.Context) (int64, error) {
	// trashedInclude: no soft-delete filter, but scope predicates still apply.
	wheres := b.effectiveWheres(trashedInclude)
	sqlStr := b.sess.grammar.CompileDelete(grammar.DeleteStmt{
		Table:  b.meta.Table,
		Wheres: wheres,
	})
	res, err := b.sess.run.ExecContext(ctx, sqlStr, whereArgs(wheres)...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Restore clears deleted_at on matching soft-deleted rows.
func (b *Builder) Restore(ctx context.Context) (int64, error) {
	if b.err != nil {
		return 0, b.err
	}
	if !b.meta.SoftDeletes {
		return 0, ErrNotSoftDeletable
	}
	if err := b.applyScopes(ctx); err != nil {
		return 0, err
	}

	// Operate on trashed rows regardless of the builder's trashed mode.
	wheres := b.effectiveWheres(trashedOnly)
	sqlStr := b.sess.grammar.CompileUpdate(grammar.UpdateStmt{
		Table:   b.meta.Table,
		Columns: []string{b.meta.DeletedAtColumn},
		Wheres:  wheres,
	})
	args := append([]any{nil}, whereArgs(wheres)...)
	res, err := b.sess.run.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// whereArgs flattens the bound values of a where list in placeholder order.
func whereArgs(wheres []grammar.WhereClause) []any {
	var args []any
	for _, w := range wheres {
		switch w.Kind {
		case grammar.WhereBasic, grammar.WhereJSON:
			args = append(args, w.Value)
		case grammar.WhereIn, grammar.WhereNotIn, grammar.WhereBetween, grammar.WhereNotBetween:
			args = append(args, w.Values...)
		case grammar.WhereNested:
			args = append(args, whereArgs(w.Group)...)
		}
	}
	return args
}
