package playsql

import (
	"context"
	"fmt"
	"reflect"

	"github.com/martin3zra/playsql/grammar"
	"github.com/martin3zra/playsql/metadata"
)

// Deferred aggregate loading: compute a relation aggregate for models that have
// already been retrieved, in a single batched GROUP BY query (no N+1). The
// result lands in a matching db-tagged field (Strategy A) or the model's
// aggregate bag (Strategy B), exactly like the query-time With* methods.
//
// dest is the same shape passed to Get/First: *T, *[]T, or *[]*T. Supported
// relation kinds are hasMany, hasOne, and belongsTo; for belongsToMany and
// has*Through use the query-time WithCount/WithSum/… instead.

// LoadCount loads {relation}_count onto already-retrieved models.
func (s *session) LoadCount(ctx context.Context, dest any, relation string, opts ...AggOption) error {
	return s.loadAggregate(ctx, dest, "COUNT", relation, "", opts)
}

// LoadSum loads {relation}_sum_{column}.
func (s *session) LoadSum(ctx context.Context, dest any, relation, column string, opts ...AggOption) error {
	return s.loadAggregate(ctx, dest, "SUM", relation, column, opts)
}

// LoadAvg loads {relation}_avg_{column}.
func (s *session) LoadAvg(ctx context.Context, dest any, relation, column string, opts ...AggOption) error {
	return s.loadAggregate(ctx, dest, "AVG", relation, column, opts)
}

// LoadMin loads {relation}_min_{column}.
func (s *session) LoadMin(ctx context.Context, dest any, relation, column string, opts ...AggOption) error {
	return s.loadAggregate(ctx, dest, "MIN", relation, column, opts)
}

// LoadMax loads {relation}_max_{column}.
func (s *session) LoadMax(ctx context.Context, dest any, relation, column string, opts ...AggOption) error {
	return s.loadAggregate(ctx, dest, "MAX", relation, column, opts)
}

// LoadExists loads {relation}_exists (1/0) onto already-retrieved models.
func (s *session) LoadExists(ctx context.Context, dest any, relation string, opts ...AggOption) error {
	return s.loadAggregate(ctx, dest, "EXISTS", relation, "", opts)
}

func (s *session) loadAggregate(ctx context.Context, dest any, fn, relation, column string, opts []AggOption) error {
	parents, err := collectParents(dest)
	if err != nil {
		return err
	}
	if len(parents) == 0 {
		return nil
	}
	parentMeta := metadata.For(parents[0].Addr().Interface())

	rel, ok := parentMeta.Relations[relation]
	if !ok {
		return fmt.Errorf("playsql: unknown relation %q on %s", relation, parentMeta.StructName)
	}
	relatedMeta := metadata.For(reflect.New(rel.RelatedType).Interface())

	spec := aggSpec{relation: relation, fn: fn, column: column}
	for _, o := range opts {
		o(&spec)
	}
	alias := spec.alias
	if alias == "" {
		alias = defaultAggAlias(spec)
	}

	// groupCol is the related-side key the query groups by; parentKeyCol is the
	// parent-side column whose values match it.
	var groupCol, parentKeyCol string
	switch rel.Kind {
	case metadata.HasMany, metadata.HasOne:
		foreignKey, otherKey := metadata.ResolveRelationKeys(parentMeta, rel, relatedMeta)
		groupCol, parentKeyCol = foreignKey, otherKey
	case metadata.BelongsTo:
		foreignKey, otherKey := metadata.ResolveRelationKeys(parentMeta, rel, relatedMeta)
		groupCol, parentKeyCol = otherKey, foreignKey
	case metadata.MorphOne, metadata.MorphMany:
		idCol, _, localKey, _ := metadata.ResolveMorphKeys(parentMeta, rel)
		groupCol, parentKeyCol = idCol, localKey
	default:
		return fmt.Errorf("playsql: deferred aggregate not supported for %s relations (use the query-time With* methods)", rel.Kind)
	}

	pkIdx, ok := parentMeta.FieldIndexByColumn(parentKeyCol)
	if !ok {
		return fmt.Errorf("playsql: relation %q: key column %q not found on %s", relation, parentKeyCol, parentMeta.StructName)
	}
	keys := distinctKeys(parents, pkIdx)
	if len(keys) == 0 {
		return nil
	}

	wheres := []grammar.WhereClause{{Kind: grammar.WhereIn, Column: groupCol, Values: keys}}
	// Morph relations also filter the related rows by the parent's type alias.
	if rel.Kind == metadata.MorphOne || rel.Kind == metadata.MorphMany {
		_, typeCol, _, typeVal := metadata.ResolveMorphKeys(parentMeta, rel)
		wheres = append(wheres, grammar.WhereClause{
			Kind: grammar.WhereBasic, Boolean: "AND", Column: typeCol, Op: "=", Value: typeVal,
		})
	}
	if relatedMeta.SoftDeletes {
		wheres = append(wheres, grammar.WhereClause{
			Kind: grammar.WhereNull, Boolean: "AND", Column: relatedMeta.DeletedAtColumn,
		})
	}
	if spec.where != nil {
		sub := &Builder{sess: s, meta: relatedMeta}
		spec.where(sub)
		if sub.err != nil {
			return sub.err
		}
		wheres = append(wheres, sub.wheres...)
	}

	queryFn := fn
	aggCol := column
	if fn == "COUNT" || fn == "EXISTS" {
		queryFn = "COUNT"
		aggCol = ""
	}

	sqlStr, args := grammar.CompileGroupedAggregate(s.grammar, grammar.GroupedAggregate{
		Table:     relatedMeta.Table,
		KeyColumn: groupCol,
		Func:      queryFn,
		Column:    aggCol,
		Wheres:    wheres,
	})

	rows, err := s.run.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return err
	}
	byKey := map[string]any{}
	for rows.Next() {
		var k, agg any
		if err := rows.Scan(&k, &agg); err != nil {
			rows.Close()
			return err
		}
		byKey[keyStr(k)] = agg
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, p := range parents {
		raw, present := byKey[keyStr(p.Field(pkIdx).Interface())]
		var val any
		switch {
		case fn == "EXISTS":
			if present && toInt64(raw) > 0 {
				val = int64(1)
			} else {
				val = int64(0)
			}
		case fn == "COUNT":
			if present {
				val = toInt64(raw)
			} else {
				val = int64(0) // no related rows
			}
		case present:
			val = raw
		default:
			continue // sum/avg/min/max with no rows: leave the zero value
		}
		setAggregate(p, parentMeta, alias, val)
	}
	return nil
}

// setAggregate writes an aggregate result onto a parent: into a matching
// db-tagged field if present (Strategy A), otherwise the model's bag (B).
func setAggregate(parent reflect.Value, meta *metadata.ModelMeta, alias string, val any) {
	if idx, ok := meta.FieldIndexByColumn(alias); ok {
		setNumericField(parent.Field(idx), val)
		return
	}
	if acc, ok := baseOf(parent.Addr().Interface()); ok {
		acc.playSetExtra(alias, val)
	}
}

// setNumericField coerces an aggregate value into a numeric/bool struct field.
func setNumericField(field reflect.Value, val any) {
	if !field.CanSet() {
		return
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		field.SetInt(toInt64(val))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		field.SetUint(uint64(toInt64(val)))
	case reflect.Float32, reflect.Float64:
		field.SetFloat(toFloat64(val))
	case reflect.Bool:
		field.SetBool(toInt64(val) != 0)
	default:
		if v := reflect.ValueOf(val); v.IsValid() && v.Type().AssignableTo(field.Type()) {
			field.Set(v)
		}
	}
}

// keyStr normalizes a key value for cross-driver map lookup ([]byte text columns
// become their string form; everything else uses its default formatting).
func keyStr(v any) string {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(v)
}
