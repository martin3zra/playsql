package playsql

import (
	"context"
	"fmt"
	"reflect"

	"github.com/martin3zra/playsql/grammar"
	"github.com/martin3zra/playsql/metadata"
)

// loadRelations eager-loads each requested relation onto the just-scanned dest.
// dest is the same value passed to Get/First (*[]T, *[]*T, or *T).
func (b *Builder) loadRelations(ctx context.Context, dest any) error {
	if len(b.withs) == 0 {
		return nil
	}

	parents, err := collectParents(dest)
	if err != nil {
		return err
	}
	if len(parents) == 0 {
		return nil
	}

	for _, name := range b.withs {
		rel, ok := b.meta.Relations[name]
		if !ok {
			return fmt.Errorf("playsql: unknown relation %q on %s", name, b.meta.StructName)
		}
		if err := b.loadRelation(ctx, parents, rel); err != nil {
			return err
		}
	}
	return nil
}

func (b *Builder) loadRelation(ctx context.Context, parents []reflect.Value, rel metadata.RelationMeta) error {
	relatedMeta := metadata.For(reflect.New(rel.RelatedType).Interface())
	foreignKey, otherKey := metadata.ResolveRelationKeys(b.meta, rel, relatedMeta)

	switch rel.Kind {
	case metadata.HasMany, metadata.HasOne:
		// parent[otherKey] (local key) === child[foreignKey]
		parentKeyIdx, ok := b.meta.FieldIndexByColumn(otherKey)
		if !ok {
			return fmt.Errorf("playsql: relation %q: local key column %q not found", rel.Name, otherKey)
		}
		childFKIdx, ok := relatedMeta.FieldIndexByColumn(foreignKey)
		if !ok {
			return fmt.Errorf("playsql: relation %q: foreign key column %q not found on %s", rel.Name, foreignKey, relatedMeta.StructName)
		}

		children, err := b.queryRelated(ctx, rel.RelatedType, foreignKey, distinctKeys(parents, parentKeyIdx))
		if err != nil {
			return err
		}

		groups := map[any][]reflect.Value{}
		for j := 0; j < children.Len(); j++ {
			c := children.Index(j)
			k := c.Field(childFKIdx).Interface()
			groups[k] = append(groups[k], c)
		}

		for _, p := range parents {
			matches := groups[p.Field(parentKeyIdx).Interface()]
			assignChildren(p.Field(rel.FieldIndex), matches, rel.Kind)
		}

	case metadata.BelongsTo:
		// parent[foreignKey] === related[otherKey] (owner key)
		childFKIdx, ok := b.meta.FieldIndexByColumn(foreignKey)
		if !ok {
			return fmt.Errorf("playsql: relation %q: foreign key column %q not found on %s", rel.Name, foreignKey, b.meta.StructName)
		}
		ownerKeyIdx, ok := relatedMeta.FieldIndexByColumn(otherKey)
		if !ok {
			return fmt.Errorf("playsql: relation %q: owner key column %q not found on %s", rel.Name, otherKey, relatedMeta.StructName)
		}

		owners, err := b.queryRelated(ctx, rel.RelatedType, otherKey, distinctKeys(parents, childFKIdx))
		if err != nil {
			return err
		}

		index := map[any]reflect.Value{}
		for j := 0; j < owners.Len(); j++ {
			o := owners.Index(j)
			index[o.Field(ownerKeyIdx).Interface()] = o
		}

		for _, p := range parents {
			if o, ok := index[p.Field(childFKIdx).Interface()]; ok {
				assignOne(p.Field(rel.FieldIndex), o)
			}
		}

	case metadata.BelongsToMany:
		return b.loadBelongsToMany(ctx, parents, rel, relatedMeta)
	}

	return nil
}

// loadBelongsToMany resolves a many-to-many relation in three batched queries:
// parents (already loaded) -> pivot rows -> related rows.
func (b *Builder) loadBelongsToMany(ctx context.Context, parents []reflect.Value, rel metadata.RelationMeta, relatedMeta *metadata.ModelMeta) error {
	pivotTable, fpk, rpk, parentKey, relatedKey := metadata.ResolvePivot(b.meta, rel, relatedMeta)

	parentKeyIdx, ok := b.meta.FieldIndexByColumn(parentKey)
	if !ok {
		return fmt.Errorf("playsql: relation %q: parent key column %q not found", rel.Name, parentKey)
	}
	relatedKeyIdx, ok := relatedMeta.FieldIndexByColumn(relatedKey)
	if !ok {
		return fmt.Errorf("playsql: relation %q: related key column %q not found", rel.Name, relatedKey)
	}

	// 1. pivot rows for these parents.
	pairs, err := b.queryPivot(ctx, pivotTable, fpk, rpk, distinctKeys(parents, parentKeyIdx))
	if err != nil {
		return err
	}

	// 2. related rows for the pivot's related keys.
	relatedKeys := make([]any, 0, len(pairs))
	seen := map[any]bool{}
	for _, pr := range pairs {
		if !seen[pr.related] {
			seen[pr.related] = true
			relatedKeys = append(relatedKeys, pr.related)
		}
	}
	related, err := b.queryRelated(ctx, rel.RelatedType, relatedKey, relatedKeys)
	if err != nil {
		return err
	}
	relIndex := map[any]reflect.Value{}
	for j := 0; j < related.Len(); j++ {
		r := related.Index(j)
		relIndex[r.Field(relatedKeyIdx).Interface()] = r
	}

	// 3. group related by parent key via the pivot, then assign.
	groups := map[any][]reflect.Value{}
	for _, pr := range pairs {
		if r, ok := relIndex[pr.related]; ok {
			groups[pr.parent] = append(groups[pr.parent], r)
		}
	}
	for _, p := range parents {
		matches := groups[p.Field(parentKeyIdx).Interface()]
		assignChildren(p.Field(rel.FieldIndex), matches, metadata.HasMany)
	}

	return nil
}

type pivotPair struct{ parent, related any }

// queryPivot reads (foreignPivotKey, relatedPivotKey) pairs from the pivot table
// for the given parent keys.
func (b *Builder) queryPivot(ctx context.Context, table, fpk, rpk string, parentKeys []any) ([]pivotPair, error) {
	sqlStr, args := b.sess.grammar.CompileSelect(grammar.CompiledQuery{
		Table:   table,
		Columns: []string{fpk, rpk},
		Wheres:  []grammar.WhereClause{{Kind: grammar.WhereIn, Column: fpk, Values: parentKeys}},
	})

	rows, err := b.sess.run.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pairs []pivotPair
	for rows.Next() {
		var f, r any
		if err := rows.Scan(&f, &r); err != nil {
			return nil, err
		}
		pairs = append(pairs, pivotPair{parent: f, related: r})
	}
	return pairs, rows.Err()
}

// queryRelated fetches related rows where whereCol IN keys, returning the result
// slice (a reflect []relatedType value).
func (b *Builder) queryRelated(ctx context.Context, relatedType reflect.Type, whereCol string, keys []any) (reflect.Value, error) {
	destPtr := reflect.New(reflect.SliceOf(relatedType)) // *[]Related
	rb := newBuilder(b.sess, reflect.New(relatedType).Interface())
	rb.WhereIn(whereCol, keys...)
	if err := rb.Get(ctx, destPtr.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return destPtr.Elem(), nil
}

// collectParents normalizes dest into a slice of settable struct values.
func collectParents(dest any) ([]reflect.Value, error) {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("playsql: dest must be a pointer, got %T", dest)
	}
	rv = rv.Elem()

	switch rv.Kind() {
	case reflect.Slice:
		out := make([]reflect.Value, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			e := rv.Index(i)
			if e.Kind() == reflect.Ptr {
				if e.IsNil() {
					continue
				}
				e = e.Elem()
			}
			out = append(out, e)
		}
		return out, nil
	case reflect.Struct:
		return []reflect.Value{rv}, nil
	default:
		return nil, fmt.Errorf("playsql: dest must point to a struct or slice, got %s", rv.Kind())
	}
}

// distinctKeys collects the unique values of a column across parents.
func distinctKeys(parents []reflect.Value, fieldIdx int) []any {
	seen := map[any]bool{}
	out := make([]any, 0, len(parents))
	for _, p := range parents {
		k := p.Field(fieldIdx).Interface()
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// assignChildren sets a has-one (single) or has-many (slice) relation field.
func assignChildren(field reflect.Value, matches []reflect.Value, kind metadata.RelationKind) {
	if kind == metadata.HasOne {
		if len(matches) > 0 {
			assignOne(field, matches[0])
		}
		return
	}

	elemType := field.Type().Elem()
	slice := reflect.MakeSlice(field.Type(), 0, len(matches))
	for _, m := range matches {
		if elemType.Kind() == reflect.Ptr {
			slice = reflect.Append(slice, m.Addr())
		} else {
			slice = reflect.Append(slice, m)
		}
	}
	field.Set(slice)
}

// assignOne sets a pointer-or-value relation field from a struct value.
func assignOne(field, val reflect.Value) {
	if field.Kind() == reflect.Ptr {
		field.Set(val.Addr())
	} else {
		field.Set(val)
	}
}
