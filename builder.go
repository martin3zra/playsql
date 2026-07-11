package playsql

import (
	"context"
	"fmt"
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
	ctes        []grammar.CTE             // WITH prefixes for Update/UpdateReturning
	cteArgs     []any                     // bound args from structured CTE subqueries
	returning   []string                  // columns for UpdateReturning
	aggSelects  []grammar.AggregateSelect // withCount/withSum/… extra columns
	subSelects  []grammar.SubSelectColumn // AddSelect correlated subquery columns
	limit       int
	offset      int
	lock        grammar.LockMode
	trashed     trashedMode
	scopes      []Scope
	inScope     bool   // routes add() into scopeWheres while a scope applies
	scopesDone  bool   // scopes applied (or skipped) once already
	skipScopes  bool   // WithoutGlobalScopes
	logger      Logger // Debug/DD sink; nil => defaultLogger
	err         error  // first construction error; surfaced by terminal ops
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

// WhereColumn compares two columns (no bound value), e.g.
// WhereColumn("destination_id", "=", "destinations.id"). Both sides are wrapped;
// use a dotted name to reference another table (for correlated subqueries).
func (b *Builder) WhereColumn(column, op, other string) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereColumn, Boolean: "AND", Column: column, Op: op, Second: other})
}

// OrWhereColumn is WhereColumn joined with OR.
func (b *Builder) OrWhereColumn(column, op, other string) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereColumn, Boolean: "OR", Column: column, Op: op, Second: other})
}

// WhereRaw adds a verbatim SQL predicate (AND). The fragment is emitted as-is
// and must carry no bind parameters — it is an escape hatch for expressions the
// builder cannot model, e.g. referencing a CTE added via WithCTE. Never
// interpolate untrusted input into it.
func (b *Builder) WhereRaw(sql string) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereRaw, Boolean: "AND", Raw: sql})
}

// OrWhereRaw is WhereRaw joined with OR.
func (b *Builder) OrWhereRaw(sql string) *Builder {
	return b.add(grammar.WhereClause{Kind: grammar.WhereRaw, Boolean: "OR", Raw: sql})
}

// --- relationship existence (correlated EXISTS subqueries) ---
//
// Has/DoesntHave filter the parent by whether a related row exists; WhereHas adds
// closure constraints to that related query. Relations are looked up by field
// name and may be dotted for nesting ("comments.images"). Phase 1 supports
// hasMany, hasOne, and belongsTo; other kinds return an error at the terminal op.

// Has keeps rows that have at least one related record (EXISTS).
func (b *Builder) Has(relation string) *Builder {
	return b.addExists(relation, "AND", false, "", 0, nil)
}

// OrHas is Has joined with OR.
func (b *Builder) OrHas(relation string) *Builder {
	return b.addExists(relation, "OR", false, "", 0, nil)
}

// HasCount keeps rows whose related count satisfies op/count, e.g.
// HasCount("comments", ">=", 3). Not supported on nested (dotted) relations.
func (b *Builder) HasCount(relation, op string, count int) *Builder {
	return b.addExists(relation, "AND", false, op, count, nil)
}

// DoesntHave keeps rows with no matching related record (NOT EXISTS).
func (b *Builder) DoesntHave(relation string) *Builder {
	return b.addExists(relation, "AND", true, "", 0, nil)
}

// OrDoesntHave is DoesntHave joined with OR.
func (b *Builder) OrDoesntHave(relation string) *Builder {
	return b.addExists(relation, "OR", true, "", 0, nil)
}

// WhereHas keeps rows that have a related record matching the closure.
func (b *Builder) WhereHas(relation string, fn func(*Builder)) *Builder {
	return b.addExists(relation, "AND", false, "", 0, fn)
}

// OrWhereHas is WhereHas joined with OR.
func (b *Builder) OrWhereHas(relation string, fn func(*Builder)) *Builder {
	return b.addExists(relation, "OR", false, "", 0, fn)
}

// WhereHasCount combines WhereHas with a count comparison. Not supported on
// nested (dotted) relations.
func (b *Builder) WhereHasCount(relation string, fn func(*Builder), op string, count int) *Builder {
	return b.addExists(relation, "AND", false, op, count, fn)
}

// WhereDoesntHave keeps rows with no related record matching the closure.
func (b *Builder) WhereDoesntHave(relation string, fn func(*Builder)) *Builder {
	return b.addExists(relation, "AND", true, "", 0, fn)
}

// OrWhereDoesntHave is WhereDoesntHave joined with OR.
func (b *Builder) OrWhereDoesntHave(relation string, fn func(*Builder)) *Builder {
	return b.addExists(relation, "OR", true, "", 0, fn)
}

// WhereRelation is shorthand for WhereHas with a single equality/comparison on
// the related model: WhereRelation("comments", "approved", "=", false).
func (b *Builder) WhereRelation(relation, column, op string, value any) *Builder {
	return b.WhereHas(relation, func(q *Builder) { q.Where(column, op, value) })
}

// OrWhereRelation is WhereRelation joined with OR.
func (b *Builder) OrWhereRelation(relation, column, op string, value any) *Builder {
	return b.OrWhereHas(relation, func(q *Builder) { q.Where(column, op, value) })
}

// addExists builds the EXISTS clause for a (possibly dotted) relation path and
// appends it. not/op apply to the outermost level; the closure applies to the
// innermost. The count form is rejected on nested paths (ambiguous semantics).
func (b *Builder) addExists(relation, boolean string, not bool, op string, count int, fn func(*Builder)) *Builder {
	segments := strings.Split(relation, ".")
	if op != "" && len(segments) > 1 {
		return b.fail(fmt.Errorf("playsql: count form not supported on nested relation %q", relation))
	}

	// The count form for has*Through counts far rows exactly via an IN-subquery,
	// not the nested-EXISTS structure used for existence. (Single segment is
	// guaranteed here by the check above.)
	if op != "" {
		if rel, ok := b.meta.Relations[segments[0]]; ok &&
			(rel.Kind == metadata.HasManyThrough || rel.Kind == metadata.HasOneThrough) {
			ex, err := b.throughCountExists(b.meta, rel, op, count, fn)
			if err != nil {
				return b.fail(err)
			}
			return b.add(grammar.WhereClause{Kind: grammar.WhereExists, Boolean: boolean, Exists: ex})
		}
	}

	ex, err := b.relationExists(b.meta, segments, fn)
	if err != nil {
		return b.fail(err)
	}
	ex.Not = not
	if op != "" {
		ex.CountOp = op
		ex.CountVal = count
		ex.Not = false // the operator encodes presence/absence
	}
	return b.add(grammar.WhereClause{Kind: grammar.WhereExists, Boolean: boolean, Exists: ex})
}

// relationExists builds a (possibly nested) correlated EXISTS for segments[0...]
// against parentMeta. fn (when non-nil) constrains the innermost related query.
// The related model's soft-delete filter is applied by default.
func (b *Builder) relationExists(parentMeta *metadata.ModelMeta, segments []string, fn func(*Builder)) (*grammar.RelationExists, error) {
	name := segments[0]
	rel, ok := parentMeta.Relations[name]
	if !ok {
		return nil, fmt.Errorf("playsql: unknown relation %q on %s", name, parentMeta.StructName)
	}
	relatedMeta := metadata.For(reflect.New(rel.RelatedType).Interface())

	// outer is the clause to return/decorate; related is the level whose table is
	// the related model — where the closure and any nested segment attach. They
	// differ for belongsToMany (the outer wraps the pivot).
	outer, related, err := buildExistsForKind(parentMeta, rel, relatedMeta)
	if err != nil {
		return nil, err
	}

	// Exclude soft-deleted related rows by default (matching default queries).
	if relatedMeta.SoftDeletes {
		related.Wheres = append(related.Wheres, grammar.WhereClause{
			Kind:    grammar.WhereNull,
			Boolean: "AND",
			Column:  relatedMeta.Table + "." + relatedMeta.DeletedAtColumn,
		})
	}

	if len(segments) == 1 {
		if fn != nil {
			sub := &Builder{sess: b.sess, meta: relatedMeta}
			fn(sub)
			if sub.err != nil {
				return nil, sub.err
			}
			related.Wheres = append(related.Wheres, sub.wheres...)
		}
		return outer, nil
	}

	nested, err := b.relationExists(relatedMeta, segments[1:], fn)
	if err != nil {
		return nil, err
	}
	related.Wheres = append(related.Wheres, grammar.WhereClause{
		Kind: grammar.WhereExists, Boolean: "AND", Exists: nested,
	})
	return outer, nil
}

// buildExistsForKind constructs the EXISTS clause(s) tying a related subquery to
// its parent, per relation kind. It returns the outermost clause and the level
// whose table is the related model (the attach point for closures/nesting); for
// most kinds these are the same clause, but belongsToMany wraps the related
// EXISTS in an outer EXISTS over the pivot table. Supported: hasMany, hasOne,
// belongsTo, belongsToMany.
func buildExistsForKind(parentMeta *metadata.ModelMeta, rel metadata.RelationMeta, relatedMeta *metadata.ModelMeta) (outer, related *grammar.RelationExists, err error) {
	switch rel.Kind {
	case metadata.HasMany, metadata.HasOne:
		foreignKey, otherKey := metadata.ResolveRelationKeys(parentMeta, rel, relatedMeta)
		// related.foreignKey = parent.localKey
		ex := &grammar.RelationExists{Table: relatedMeta.Table, On: []grammar.WhereClause{{
			Kind:   grammar.WhereColumn,
			Column: relatedMeta.Table + "." + foreignKey,
			Op:     "=",
			Second: parentMeta.Table + "." + otherKey,
		}}}
		return ex, ex, nil

	case metadata.BelongsTo:
		foreignKey, otherKey := metadata.ResolveRelationKeys(parentMeta, rel, relatedMeta)
		// parent.foreignKey = related.ownerKey
		ex := &grammar.RelationExists{Table: relatedMeta.Table, On: []grammar.WhereClause{{
			Kind:   grammar.WhereColumn,
			Column: parentMeta.Table + "." + foreignKey,
			Op:     "=",
			Second: relatedMeta.Table + "." + otherKey,
		}}}
		return ex, ex, nil

	case metadata.BelongsToMany:
		pivotTable, fpk, rpk, parentKey, relatedKey := metadata.ResolvePivot(parentMeta, rel, relatedMeta)
		// inner: related.relatedKey = pivot.relatedPivotKey
		inner := &grammar.RelationExists{Table: relatedMeta.Table, On: []grammar.WhereClause{{
			Kind:   grammar.WhereColumn,
			Column: relatedMeta.Table + "." + relatedKey,
			Op:     "=",
			Second: pivotTable + "." + rpk,
		}}}
		// outer: pivot.foreignPivotKey = parent.parentKey, wrapping the inner EXISTS.
		out := &grammar.RelationExists{
			Table: pivotTable,
			On: []grammar.WhereClause{{
				Kind:   grammar.WhereColumn,
				Column: pivotTable + "." + fpk,
				Op:     "=",
				Second: parentMeta.Table + "." + parentKey,
			}},
			Wheres: []grammar.WhereClause{{Kind: grammar.WhereExists, Boolean: "AND", Exists: inner}},
		}
		return out, inner, nil

	case metadata.HasManyThrough, metadata.HasOneThrough:
		if rel.ThroughTable == "" {
			return nil, nil, fmt.Errorf("playsql: relation %q: %s requires a through= table", rel.Name, rel.Kind)
		}
		throughTable, firstKey, secondKey, throughKey, localKey := metadata.ResolveThrough(parentMeta, rel)
		// inner: far.secondKey = through.throughKey
		inner := &grammar.RelationExists{Table: relatedMeta.Table, On: []grammar.WhereClause{{
			Kind:   grammar.WhereColumn,
			Column: relatedMeta.Table + "." + secondKey,
			Op:     "=",
			Second: throughTable + "." + throughKey,
		}}}
		// outer: through.firstKey = parent.localKey, wrapping the inner EXISTS.
		out := &grammar.RelationExists{
			Table: throughTable,
			On: []grammar.WhereClause{{
				Kind:   grammar.WhereColumn,
				Column: throughTable + "." + firstKey,
				Op:     "=",
				Second: parentMeta.Table + "." + localKey,
			}},
			Wheres: []grammar.WhereClause{{Kind: grammar.WhereExists, Boolean: "AND", Exists: inner}},
		}
		return out, inner, nil

	case metadata.MorphOne, metadata.MorphMany:
		idCol, typeCol, localKey, typeVal := metadata.ResolveMorphKeys(parentMeta, rel)
		ex := &grammar.RelationExists{Table: relatedMeta.Table, On: []grammar.WhereClause{
			{Kind: grammar.WhereColumn, Column: relatedMeta.Table + "." + idCol, Op: "=", Second: parentMeta.Table + "." + localKey},
			{Kind: grammar.WhereBasic, Boolean: "AND", Column: relatedMeta.Table + "." + typeCol, Op: "=", Value: typeVal},
		}}
		return ex, ex, nil

	default:
		return nil, nil, fmt.Errorf("playsql: relation existence not supported for %s relations", rel.Kind)
	}
}

// throughCountExists builds the COUNT form for has*Through, which counts the far
// rows exactly (a nested EXISTS would count intermediate rows). It emits
// (SELECT COUNT(*) FROM far WHERE far.secondKey IN (SELECT through.throughKey
// FROM through WHERE through.firstKey = parent.localKey) [AND closure]) op ?.
func (b *Builder) throughCountExists(parentMeta *metadata.ModelMeta, rel metadata.RelationMeta, op string, count int, fn func(*Builder)) (*grammar.RelationExists, error) {
	if rel.ThroughTable == "" {
		return nil, fmt.Errorf("playsql: relation %q: %s requires a through= table", rel.Name, rel.Kind)
	}
	farMeta := metadata.For(reflect.New(rel.RelatedType).Interface())
	throughTable, firstKey, secondKey, throughKey, localKey := metadata.ResolveThrough(parentMeta, rel)

	ex := &grammar.RelationExists{
		Table: farMeta.Table,
		On: []grammar.WhereClause{{
			Kind:   grammar.WhereInSub,
			Column: farMeta.Table + "." + secondKey,
			Sub: &grammar.Subselect{
				Column: throughTable + "." + throughKey,
				Table:  throughTable,
				Wheres: []grammar.WhereClause{{
					Kind:   grammar.WhereColumn,
					Column: throughTable + "." + firstKey,
					Op:     "=",
					Second: parentMeta.Table + "." + localKey,
				}},
			},
		}},
		CountOp:  op,
		CountVal: count,
	}
	if farMeta.SoftDeletes {
		ex.Wheres = append(ex.Wheres, grammar.WhereClause{
			Kind:    grammar.WhereNull,
			Boolean: "AND",
			Column:  farMeta.Table + "." + farMeta.DeletedAtColumn,
		})
	}
	if fn != nil {
		sub := &Builder{sess: b.sess, meta: farMeta}
		fn(sub)
		if sub.err != nil {
			return nil, sub.err
		}
		ex.Wheres = append(ex.Wheres, sub.wheres...)
	}
	return ex, nil
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

// When runs fn only when condition is true, folding a conditional predicate
// into a chain instead of breaking out into an if. The optional otherwise
// closure runs when condition is false (Laravel's $default argument).
//
//	q.When(customerType != "all", func(q *Builder) {
//	    q.WhereEq("customer_type", string(customerType))
//	})
func (b *Builder) When(condition bool, fn func(*Builder), otherwise ...func(*Builder)) *Builder {
	if condition {
		if fn != nil {
			fn(b)
		}
	} else if len(otherwise) > 0 && otherwise[0] != nil {
		otherwise[0](b)
	}
	return b
}

// Unless is When with the condition inverted: fn runs when condition is false.
// The optional otherwise closure runs when condition is true.
//
//	q.Unless(showAll, func(q *Builder) { q.WhereEq("active", true) })
func (b *Builder) Unless(condition bool, fn func(*Builder), otherwise ...func(*Builder)) *Builder {
	return b.When(!condition, fn, otherwise...)
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

// --- pessimistic locking ---
//
// Both methods require a transaction: under autocommit the lock is released
// before the rows are scanned, so the query would appear to succeed while
// protecting nothing. Calling them outside one fails with ErrLockOutsideTx at
// the next terminal op. Note that SQLite has no row-level locks and drops the
// clause; a transaction there still serializes writers.

// LockForUpdate takes a FOR UPDATE lock on the selected rows, blocking other
// writers and shared locks until the transaction ends.
func (b *Builder) LockForUpdate() *Builder { return b.setLock(grammar.LockUpdate) }

// SharedLock takes a shared lock on the selected rows, blocking writers while
// still allowing other readers to take a shared lock.
func (b *Builder) SharedLock() *Builder { return b.setLock(grammar.LockShared) }

func (b *Builder) setLock(m grammar.LockMode) *Builder {
	if !b.sess.inTx {
		return b.fail(ErrLockOutsideTx)
	}
	b.lock = m
	return b
}

// Returning names the columns UpdateReturning should return from the affected
// rows (emitted as RETURNING, or OUTPUT INSERTED on SQL Server).
func (b *Builder) Returning(columns ...string) *Builder {
	b.returning = columns
	return b
}

// WithCTE prepends a common table expression (WITH name AS (rawSQL)) to an
// Update/UpdateReturning. rawSQL is rendered verbatim and must carry no bind
// parameters (see grammar.CTE). Pair it with WhereRaw to reference the CTE.
//
//	db.Model(Product{}).
//		WithCTE("avg_price", "SELECT AVG(price) AS value FROM products").
//		WhereRaw("price < (SELECT value FROM avg_price)").
//		Update(ctx, map[string]any{"cheap": true})
func (b *Builder) WithCTE(name, rawSQL string) *Builder {
	b.ctes = append(b.ctes, grammar.CTE{Name: name, SQL: rawSQL})
	return b
}

// WithCTEQuery prepends a CTE built from a sub-query (a builder for any table),
// unlike WithCTE it may carry bound parameters — the grammar renumbers the
// subquery's placeholders to lead the statement.
//
//	cheap := db.Model(&Product{}).Select("id").Where("price", "<", threshold)
//	db.Model(&Product{}).
//		WithCTEQuery("cheap", cheap).
//		WhereRaw("id IN (SELECT id FROM cheap)").
//		Update(ctx, map[string]any{"on_sale": true})
func (b *Builder) WithCTEQuery(name string, sub *Builder) *Builder {
	if b.err != nil {
		return b
	}
	if sub == nil || sub.err != nil {
		return b.fail(fmt.Errorf("playsql: WithCTEQuery(%q): invalid subquery", name))
	}
	cq := sub.compiled()
	_, args := b.sess.grammar.CompileSelect(cq) // args in placeholder order
	b.ctes = append(b.ctes, grammar.CTE{Name: name, Query: &cq})
	b.cteArgs = append(b.cteArgs, args...)
	return b
}

// --- relationship aggregates (withCount/withSum/…) ---
//
// Each adds a correlated-subquery column to the SELECT, exposing the result as
// {relation}_{func}[_{column}] (override with As). The value scans into a
// matching db-tagged field if one exists, otherwise into the model's aggregate
// bag (reachable via Aggregate/CountOf/SumOf) when it embeds playsql.Model.

// aggSpec is the pre-resolution description of one requested aggregate.
type aggSpec struct {
	relation string
	fn       string // COUNT | SUM | AVG | MIN | MAX | EXISTS
	column   string // related column for SUM/AVG/MIN/MAX
	alias    string // explicit alias; "" => default naming
	where    func(*Builder)
}

// AggOption customizes a With* aggregate (alias, constraint).
type AggOption func(*aggSpec)

// As sets the output column alias for an aggregate.
func As(alias string) AggOption { return func(s *aggSpec) { s.alias = alias } }

// Constrain adds predicates to an aggregate's related subquery.
func Constrain(fn func(*Builder)) AggOption { return func(s *aggSpec) { s.where = fn } }

// WithCount adds a count of the relation as {relation}_count.
func (b *Builder) WithCount(relation string, opts ...AggOption) *Builder {
	return b.addAggregate("COUNT", relation, "", opts)
}

// WithSum adds a sum of column over the relation as {relation}_sum_{column}.
func (b *Builder) WithSum(relation, column string, opts ...AggOption) *Builder {
	return b.addAggregate("SUM", relation, column, opts)
}

// WithAvg adds an average of column over the relation.
func (b *Builder) WithAvg(relation, column string, opts ...AggOption) *Builder {
	return b.addAggregate("AVG", relation, column, opts)
}

// WithMin adds the minimum of column over the relation.
func (b *Builder) WithMin(relation, column string, opts ...AggOption) *Builder {
	return b.addAggregate("MIN", relation, column, opts)
}

// WithMax adds the maximum of column over the relation.
func (b *Builder) WithMax(relation, column string, opts ...AggOption) *Builder {
	return b.addAggregate("MAX", relation, column, opts)
}

// WithExists adds a boolean (0/1) column reporting whether the relation exists,
// as {relation}_exists.
func (b *Builder) WithExists(relation string, opts ...AggOption) *Builder {
	return b.addAggregate("EXISTS", relation, "", opts)
}

func (b *Builder) addAggregate(fn, relation, column string, opts []AggOption) *Builder {
	if b.err != nil {
		return b
	}
	s := aggSpec{relation: relation, fn: fn, column: column}
	for _, o := range opts {
		o(&s)
	}
	sel, err := b.buildAggregate(s)
	if err != nil {
		return b.fail(err)
	}
	b.aggSelects = append(b.aggSelects, sel)
	return b
}

// buildAggregate resolves an aggSpec into a grammar.AggregateSelect, including
// correlation, default alias, the aggregated column, related soft-delete, and
// any constraint closure.
func (b *Builder) buildAggregate(s aggSpec) (grammar.AggregateSelect, error) {
	rel, ok := b.meta.Relations[s.relation]
	if !ok {
		return grammar.AggregateSelect{}, fmt.Errorf("playsql: unknown relation %q on %s", s.relation, b.meta.StructName)
	}
	relatedMeta := metadata.For(reflect.New(rel.RelatedType).Interface())

	table, on, err := aggregateCorrelation(b.meta, rel, relatedMeta)
	if err != nil {
		return grammar.AggregateSelect{}, err
	}

	sel := grammar.AggregateSelect{Func: s.fn, Table: table, On: on, Alias: s.alias}
	if sel.Alias == "" {
		sel.Alias = defaultAggAlias(s)
	}
	if s.fn != "COUNT" && s.fn != "EXISTS" {
		sel.Column = relatedMeta.Table + "." + s.column
	}
	if relatedMeta.SoftDeletes {
		sel.Wheres = append(sel.Wheres, grammar.WhereClause{
			Kind:    grammar.WhereNull,
			Boolean: "AND",
			Column:  relatedMeta.Table + "." + relatedMeta.DeletedAtColumn,
		})
	}
	if s.where != nil {
		sub := &Builder{sess: b.sess, meta: relatedMeta}
		s.where(sub)
		if sub.err != nil {
			return grammar.AggregateSelect{}, sub.err
		}
		sel.Wheres = append(sel.Wheres, sub.wheres...)
	}
	return sel, nil
}

// aggregateCorrelation returns the subquery FROM table and the correlation
// predicate(s) tying an aggregate over a relation to its parent. Simple kinds
// correlate directly; belongsToMany and has*Through correlate through the
// junction via an IN-subquery (so the aggregate runs over the related/far table).
func aggregateCorrelation(parentMeta *metadata.ModelMeta, rel metadata.RelationMeta, relatedMeta *metadata.ModelMeta) (string, []grammar.WhereClause, error) {
	switch rel.Kind {
	case metadata.HasMany, metadata.HasOne:
		foreignKey, otherKey := metadata.ResolveRelationKeys(parentMeta, rel, relatedMeta)
		return relatedMeta.Table, []grammar.WhereClause{{
			Kind:   grammar.WhereColumn,
			Column: relatedMeta.Table + "." + foreignKey,
			Op:     "=",
			Second: parentMeta.Table + "." + otherKey,
		}}, nil

	case metadata.BelongsTo:
		foreignKey, otherKey := metadata.ResolveRelationKeys(parentMeta, rel, relatedMeta)
		return relatedMeta.Table, []grammar.WhereClause{{
			Kind:   grammar.WhereColumn,
			Column: parentMeta.Table + "." + foreignKey,
			Op:     "=",
			Second: relatedMeta.Table + "." + otherKey,
		}}, nil

	case metadata.BelongsToMany:
		pivotTable, fpk, rpk, parentKey, relatedKey := metadata.ResolvePivot(parentMeta, rel, relatedMeta)
		return relatedMeta.Table, []grammar.WhereClause{{
			Kind:   grammar.WhereInSub,
			Column: relatedMeta.Table + "." + relatedKey,
			Sub: &grammar.Subselect{
				Column: pivotTable + "." + rpk,
				Table:  pivotTable,
				Wheres: []grammar.WhereClause{{
					Kind: grammar.WhereColumn, Column: pivotTable + "." + fpk, Op: "=", Second: parentMeta.Table + "." + parentKey,
				}},
			},
		}}, nil

	case metadata.HasManyThrough, metadata.HasOneThrough:
		if rel.ThroughTable == "" {
			return "", nil, fmt.Errorf("playsql: relation %q: %s requires a through= table", rel.Name, rel.Kind)
		}
		throughTable, firstKey, secondKey, throughKey, localKey := metadata.ResolveThrough(parentMeta, rel)
		return relatedMeta.Table, []grammar.WhereClause{{
			Kind:   grammar.WhereInSub,
			Column: relatedMeta.Table + "." + secondKey,
			Sub: &grammar.Subselect{
				Column: throughTable + "." + throughKey,
				Table:  throughTable,
				Wheres: []grammar.WhereClause{{
					Kind: grammar.WhereColumn, Column: throughTable + "." + firstKey, Op: "=", Second: parentMeta.Table + "." + localKey,
				}},
			},
		}}, nil

	case metadata.MorphOne, metadata.MorphMany:
		idCol, typeCol, localKey, typeVal := metadata.ResolveMorphKeys(parentMeta, rel)
		return relatedMeta.Table, []grammar.WhereClause{
			{Kind: grammar.WhereColumn, Column: relatedMeta.Table + "." + idCol, Op: "=", Second: parentMeta.Table + "." + localKey},
			{Kind: grammar.WhereBasic, Boolean: "AND", Column: relatedMeta.Table + "." + typeCol, Op: "=", Value: typeVal},
		}, nil

	default:
		return "", nil, fmt.Errorf("playsql: aggregates not supported for %s relations", rel.Kind)
	}
}

// defaultAggAlias builds the Eloquent-style column name for an aggregate.
func defaultAggAlias(s aggSpec) string {
	base := metadata.Snake(s.relation)
	switch s.fn {
	case "COUNT":
		return base + "_count"
	case "EXISTS":
		return base + "_exists"
	default:
		return base + "_" + strings.ToLower(s.fn) + "_" + s.column
	}
}

// AddSelect adds a correlated subquery as an extra column named alias. sub is a
// builder for the related table, typically correlated with WhereColumn:
//
//	sub := db.Model(&Flight{}).Select("name").
//		WhereColumn("destination_id", "=", "destinations.id").
//		OrderBy("arrived_at", playsql.Desc).Limit(1)
//	playsql.Query[Destination](db).AddSelect("last_flight", sub).Get(ctx)
//
// The result scans into a matching db-tagged field (tag it play:"readonly") or
// the model's aggregate bag, like the With* aggregates.
func (b *Builder) AddSelect(alias string, sub *Builder) *Builder {
	if b.err != nil {
		return b
	}
	if sub == nil || sub.err != nil {
		return b.fail(fmt.Errorf("playsql: AddSelect(%q): invalid subquery", alias))
	}
	b.subSelects = append(b.subSelects, grammar.SubSelectColumn{Query: sub.compiled(), Alias: alias})
	return b
}

// OrderBySubquery orders the parent query by a correlated subquery (a single
// scalar per row), e.g. sort destinations by their latest flight arrival.
func (b *Builder) OrderBySubquery(sub *Builder, dir Direction) *Builder {
	if b.err != nil {
		return b
	}
	if sub == nil || sub.err != nil {
		return b.fail(fmt.Errorf("playsql: OrderBySubquery: invalid subquery"))
	}
	sq := sub.compiled()
	b.orders = append(b.orders, grammar.OrderClause{Direction: string(dir), Sub: &sq})
	return b
}

// compiled assembles the dialect-neutral query from the builder's state.
func (b *Builder) compiled() grammar.CompiledQuery {
	cols := b.columns
	if len(cols) == 0 {
		cols = b.meta.ColumnNames()
	}
	return grammar.CompiledQuery{
		Table:      b.meta.Table,
		Columns:    cols,
		Aggregates: b.aggSelects,
		SubSelects: b.subSelects,
		Wheres:     b.effectiveWheres(b.trashed),
		Orders:     b.orders,
		Limit:      b.limit,
		Offset:     b.offset,
		Lock:       b.lock,
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
	q.Lock = grammar.LockNone // Postgres rejects FOR UPDATE with an aggregate
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
		sqlStr, _ := b.sess.grammar.CompileUpdate(grammar.UpdateStmt{
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
	sqlStr, _ := b.sess.grammar.CompileUpdate(grammar.UpdateStmt{
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
		case grammar.WhereExists:
			// Order mirrors compileExists: correlation (no binds), inner wheres,
			// then the COUNT comparison value when present.
			ex := w.Exists
			args = append(args, whereArgs(ex.On)...)
			args = append(args, whereArgs(ex.Wheres)...)
			if ex.CountOp != "" {
				args = append(args, ex.CountVal)
			}
		case grammar.WhereInSub:
			args = append(args, whereArgs(w.Sub.Wheres)...)
		}
	}
	return args
}
