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

	for _, wc := range b.withs {
		if err := b.loadPath(ctx, parents, b.meta, wc.segments, wc.constraint); err != nil {
			return err
		}
	}
	return nil
}

// loadPath loads one (possibly dotted) relation path. It loads the first
// segment onto parents, then recurses into the loaded children for the rest.
// The constraint applies only to the deepest segment's query.
func (b *Builder) loadPath(ctx context.Context, parents []reflect.Value, parentMeta *metadata.ModelMeta, segments []string, constraint func(*Builder)) error {
	name := segments[0]
	rel, ok := parentMeta.Relations[name]
	if !ok {
		return fmt.Errorf("playsql: unknown relation %q on %s", name, parentMeta.StructName)
	}

	last := len(segments) == 1
	var cons func(*Builder)
	if last {
		cons = constraint
	}
	if err := b.loadRelation(ctx, parents, parentMeta, rel, cons); err != nil {
		return err
	}
	if last {
		return nil
	}
	// morphTo has no single related type, so it cannot be an intermediate segment.
	if rel.Kind == metadata.MorphTo {
		return fmt.Errorf("playsql: morphTo relation %q cannot have nested segments %v", name, segments[1:])
	}

	children := collectChildren(parents, rel.FieldIndex)
	if len(children) == 0 {
		return nil
	}
	childMeta := metadata.For(reflect.New(rel.RelatedType).Interface())
	return b.loadPath(ctx, children, childMeta, segments[1:], constraint)
}

func (b *Builder) loadRelation(ctx context.Context, parents []reflect.Value, parentMeta *metadata.ModelMeta, rel metadata.RelationMeta, constraint func(*Builder)) error {
	// morphTo has no single related type (RelatedType is nil) — resolve it first.
	if rel.Kind == metadata.MorphTo {
		return b.loadMorphTo(ctx, parents, parentMeta, rel, constraint)
	}

	relatedMeta := metadata.For(reflect.New(rel.RelatedType).Interface())
	foreignKey, otherKey := metadata.ResolveRelationKeys(parentMeta, rel, relatedMeta)

	switch rel.Kind {
	case metadata.HasMany, metadata.HasOne:
		// parent[otherKey] (local key) === child[foreignKey]
		parentKeyIdx, ok := parentMeta.FieldIndexByColumn(otherKey)
		if !ok {
			return fmt.Errorf("playsql: relation %q: local key column %q not found", rel.Name, otherKey)
		}
		childFKIdx, ok := relatedMeta.FieldIndexByColumn(foreignKey)
		if !ok {
			return fmt.Errorf("playsql: relation %q: foreign key column %q not found on %s", rel.Name, foreignKey, relatedMeta.StructName)
		}

		children, err := b.queryRelated(ctx, rel.RelatedType, foreignKey, distinctKeys(parents, parentKeyIdx), constraint)
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
		childFKIdx, ok := parentMeta.FieldIndexByColumn(foreignKey)
		if !ok {
			return fmt.Errorf("playsql: relation %q: foreign key column %q not found on %s", rel.Name, foreignKey, parentMeta.StructName)
		}
		ownerKeyIdx, ok := relatedMeta.FieldIndexByColumn(otherKey)
		if !ok {
			return fmt.Errorf("playsql: relation %q: owner key column %q not found on %s", rel.Name, otherKey, relatedMeta.StructName)
		}

		owners, err := b.queryRelated(ctx, rel.RelatedType, otherKey, distinctKeys(parents, childFKIdx), constraint)
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

	case metadata.MorphOne, metadata.MorphMany:
		// parent[localKey] === child[morphId] AND child[morphType] === parent alias
		idCol, typeCol, localKey, typeVal := metadata.ResolveMorphKeys(parentMeta, rel)
		parentKeyIdx, ok := parentMeta.FieldIndexByColumn(localKey)
		if !ok {
			return fmt.Errorf("playsql: relation %q: local key column %q not found", rel.Name, localKey)
		}
		childIDIdx, ok := relatedMeta.FieldIndexByColumn(idCol)
		if !ok {
			return fmt.Errorf("playsql: relation %q: morph id column %q not found on %s", rel.Name, idCol, relatedMeta.StructName)
		}

		children, err := b.queryRelated(ctx, rel.RelatedType, idCol, distinctKeys(parents, parentKeyIdx), func(rb *Builder) {
			rb.WhereEq(typeCol, typeVal)
			if constraint != nil {
				constraint(rb)
			}
		})
		if err != nil {
			return err
		}

		groups := map[any][]reflect.Value{}
		for j := 0; j < children.Len(); j++ {
			c := children.Index(j)
			k := c.Field(childIDIdx).Interface()
			groups[k] = append(groups[k], c)
		}

		kind := metadata.HasMany
		if rel.Kind == metadata.MorphOne {
			kind = metadata.HasOne
		}
		for _, p := range parents {
			matches := groups[p.Field(parentKeyIdx).Interface()]
			assignChildren(p.Field(rel.FieldIndex), matches, kind)
		}

	case metadata.BelongsToMany:
		return b.loadBelongsToMany(ctx, parents, parentMeta, rel, relatedMeta, constraint)

	case metadata.MorphToMany, metadata.MorphedByMany:
		return b.loadMorphToMany(ctx, parents, parentMeta, rel, relatedMeta, constraint)

	case metadata.HasManyThrough, metadata.HasOneThrough:
		return b.loadThrough(ctx, parents, parentMeta, rel, relatedMeta, constraint)
	}

	return nil
}

// MorphOwners maps each polymorphic type value to a model instance of that owner
// type. A model holding a morphTo field must implement it so the loader can
// resolve a child's *_type string to a concrete model — a per-model alternative
// to a global morph registry.
//
//	func (Image) MorphOwners() map[string]any {
//	    return map[string]any{"posts": &Post{}, "videos": &Video{}}
//	}
type MorphOwners interface {
	MorphOwners() map[string]any
}

// loadMorphTo resolves a morphTo relation: it groups the holders by their
// *_type value, and for each type queries the corresponding owner table (looked
// up via MorphOwners) by the *_id values, assigning a *Owner into the holder's
// interface field. One query per distinct type, no N+1.
func (b *Builder) loadMorphTo(ctx context.Context, holders []reflect.Value, holderMeta *metadata.ModelMeta, rel metadata.RelationMeta, constraint func(*Builder)) error {
	idCol := rel.MorphID
	if idCol == "" {
		idCol = rel.MorphName + "_id"
	}
	typeCol := rel.MorphType
	if typeCol == "" {
		typeCol = rel.MorphName + "_type"
	}
	idIdx, ok := holderMeta.FieldIndexByColumn(idCol)
	if !ok {
		return fmt.Errorf("playsql: relation %q: morph id column %q not found on %s", rel.Name, idCol, holderMeta.StructName)
	}
	typeIdx, ok := holderMeta.FieldIndexByColumn(typeCol)
	if !ok {
		return fmt.Errorf("playsql: relation %q: morph type column %q not found on %s", rel.Name, typeCol, holderMeta.StructName)
	}

	owner, ok := reflect.New(holders[0].Type()).Interface().(MorphOwners)
	if !ok {
		return fmt.Errorf("playsql: relation %q: %s must implement MorphOwners for morphTo", rel.Name, holderMeta.StructName)
	}
	ownersMap := owner.MorphOwners()

	// Bucket holders by the polymorphic type value.
	byType := map[string][]reflect.Value{}
	for _, h := range holders {
		byType[fmt.Sprint(h.Field(typeIdx).Interface())] = append(byType[fmt.Sprint(h.Field(typeIdx).Interface())], h)
	}

	for typeVal, group := range byType {
		model, ok := ownersMap[typeVal]
		if !ok {
			continue // unmapped type -> leave the field nil
		}
		ownerMeta := metadata.For(model)
		ownerType := reflect.TypeOf(model)
		for ownerType.Kind() == reflect.Ptr {
			ownerType = ownerType.Elem()
		}
		pkIdx, ok := ownerMeta.FieldIndexByColumn(ownerMeta.PrimaryKey)
		if !ok {
			return fmt.Errorf("playsql: morphTo owner %s has no primary key column %q", ownerMeta.StructName, ownerMeta.PrimaryKey)
		}

		owners, err := b.queryRelated(ctx, ownerType, ownerMeta.PrimaryKey, distinctKeys(group, idIdx), constraint)
		if err != nil {
			return err
		}
		index := map[any]reflect.Value{}
		for j := 0; j < owners.Len(); j++ {
			o := owners.Index(j)
			index[o.Field(pkIdx).Interface()] = o
		}
		for _, h := range group {
			if o, ok := index[h.Field(idIdx).Interface()]; ok {
				field := h.Field(rel.FieldIndex)
				if field.CanSet() {
					field.Set(o.Addr()) // assign *Owner into the interface field
				}
			}
		}
	}
	return nil
}

// loadThrough resolves a has*Through relation in two batched queries (no JOIN):
// parent local keys -> through rows -> far rows, mapping each far row back to a
// parent via the through table.
func (b *Builder) loadThrough(ctx context.Context, parents []reflect.Value, parentMeta *metadata.ModelMeta, rel metadata.RelationMeta, farMeta *metadata.ModelMeta, constraint func(*Builder)) error {
	if rel.ThroughTable == "" {
		return fmt.Errorf("playsql: relation %q: %s requires a through= table", rel.Name, rel.Kind)
	}
	throughTable, firstKey, secondKey, throughKey, localKey := metadata.ResolveThrough(parentMeta, rel)

	parentLocalIdx, ok := parentMeta.FieldIndexByColumn(localKey)
	if !ok {
		return fmt.Errorf("playsql: relation %q: local key column %q not found", rel.Name, localKey)
	}
	farSecondIdx, ok := farMeta.FieldIndexByColumn(secondKey)
	if !ok {
		return fmt.Errorf("playsql: relation %q: far key column %q not found on %s", rel.Name, secondKey, farMeta.StructName)
	}

	// Step 1: through rows (firstKey, throughKey) for the parents' local keys.
	pairs, err := b.queryPivot(ctx, throughTable, firstKey, throughKey, nil, distinctKeys(parents, parentLocalIdx), "", "")
	if err != nil {
		return err
	}

	throughMap := map[any]any{} // through PK value -> parent local key value
	throughKeys := make([]any, 0, len(pairs))
	seen := map[any]bool{}
	for _, pr := range pairs {
		throughMap[pr.related] = pr.parent
		if !seen[pr.related] {
			seen[pr.related] = true
			throughKeys = append(throughKeys, pr.related)
		}
	}

	// Step 2: far rows whose second key matches a through PK.
	far, err := b.queryRelated(ctx, rel.RelatedType, secondKey, throughKeys, constraint)
	if err != nil {
		return err
	}

	groups := map[any][]reflect.Value{}
	for j := 0; j < far.Len(); j++ {
		f := far.Index(j)
		if pkey, ok := throughMap[f.Field(farSecondIdx).Interface()]; ok {
			groups[pkey] = append(groups[pkey], f)
		}
	}

	kind := metadata.HasMany
	if rel.Kind == metadata.HasOneThrough {
		kind = metadata.HasOne
	}
	for _, p := range parents {
		matches := groups[p.Field(parentLocalIdx).Interface()]
		assignChildren(p.Field(rel.FieldIndex), matches, kind)
	}

	return nil
}

// loadBelongsToMany resolves a many-to-many relation in three batched queries:
// parents (already loaded) -> pivot rows -> related rows.
func (b *Builder) loadBelongsToMany(ctx context.Context, parents []reflect.Value, parentMeta *metadata.ModelMeta, rel metadata.RelationMeta, relatedMeta *metadata.ModelMeta, constraint func(*Builder)) error {
	pivotTable, fpk, rpk, parentKey, relatedKey := metadata.ResolvePivot(parentMeta, rel, relatedMeta)
	return b.loadPivotRelation(ctx, parents, parentMeta, rel, relatedMeta, constraint, pivotTable, fpk, rpk, parentKey, relatedKey, "", "")
}

// loadMorphToMany handles morphToMany / morphedByMany: belongsToMany through a
// polymorphic pivot, filtered by the morphable side's type value.
func (b *Builder) loadMorphToMany(ctx context.Context, parents []reflect.Value, parentMeta *metadata.ModelMeta, rel metadata.RelationMeta, relatedMeta *metadata.ModelMeta, constraint func(*Builder)) error {
	pivotTable, fpk, rpk, parentKey, relatedKey, typeCol, typeVal := metadata.ResolveMorphPivot(parentMeta, rel, relatedMeta)
	return b.loadPivotRelation(ctx, parents, parentMeta, rel, relatedMeta, constraint, pivotTable, fpk, rpk, parentKey, relatedKey, typeCol, typeVal)
}

// loadPivotRelation is the shared pivot loader for belongsToMany and the morph
// many-to-many kinds; typeCol/typeVal (when set) filter a polymorphic pivot.
func (b *Builder) loadPivotRelation(ctx context.Context, parents []reflect.Value, parentMeta *metadata.ModelMeta, rel metadata.RelationMeta, relatedMeta *metadata.ModelMeta, constraint func(*Builder), pivotTable, fpk, rpk, parentKey, relatedKey, typeCol, typeVal string) error {
	parentKeyIdx, ok := parentMeta.FieldIndexByColumn(parentKey)
	if !ok {
		return fmt.Errorf("playsql: relation %q: parent key column %q not found", rel.Name, parentKey)
	}
	relatedKeyIdx, ok := relatedMeta.FieldIndexByColumn(relatedKey)
	if !ok {
		return fmt.Errorf("playsql: relation %q: related key column %q not found", rel.Name, relatedKey)
	}

	// 1. pivot rows for these parents (incl. any withPivot columns).
	pairs, err := b.queryPivot(ctx, pivotTable, fpk, rpk, rel.PivotColumns, distinctKeys(parents, parentKeyIdx), typeCol, typeVal)
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
	related, err := b.queryRelated(ctx, rel.RelatedType, relatedKey, relatedKeys, constraint)
	if err != nil {
		return err
	}
	relIndex := map[any]reflect.Value{}
	for j := 0; j < related.Len(); j++ {
		r := related.Index(j)
		relIndex[r.Field(relatedKeyIdx).Interface()] = r
	}

	withPivot := len(rel.PivotColumns) > 0 && relatedMeta.PivotFieldIndex >= 0

	// 3. group related by parent key via the pivot, then assign.
	groups := map[any][]reflect.Value{}
	for _, pr := range pairs {
		r, ok := relIndex[pr.related]
		if !ok {
			continue
		}
		if withPivot {
			// Copy so each parent's instance carries its own pivot row.
			cp := reflect.New(r.Type()).Elem()
			cp.Set(r)
			cp.Field(relatedMeta.PivotFieldIndex).Set(reflect.ValueOf(pr.data))
			r = cp
		}
		groups[pr.parent] = append(groups[pr.parent], r)
	}
	for _, p := range parents {
		matches := groups[p.Field(parentKeyIdx).Interface()]
		assignChildren(p.Field(rel.FieldIndex), matches, metadata.HasMany)
	}

	return nil
}

type pivotPair struct {
	parent, related any
	data            map[string]any // withPivot column values
}

// queryPivot reads (foreignPivotKey, relatedPivotKey [, pivotColumns...]) rows
// from the pivot table for the given parent keys. When typeCol is non-empty the
// pivot is additionally filtered by typeCol = typeVal (polymorphic pivots).
func (b *Builder) queryPivot(ctx context.Context, table, fpk, rpk string, pivotCols []string, parentKeys []any, typeCol, typeVal string) ([]pivotPair, error) {
	cols := append([]string{fpk, rpk}, pivotCols...)
	wheres := []grammar.WhereClause{{Kind: grammar.WhereIn, Column: fpk, Values: parentKeys}}
	if typeCol != "" {
		wheres = append(wheres, grammar.WhereClause{Kind: grammar.WhereBasic, Boolean: "AND", Column: typeCol, Op: "=", Value: typeVal})
	}
	sqlStr, args := b.sess.grammar.CompileSelect(grammar.CompiledQuery{
		Table:   table,
		Columns: cols,
		Wheres:  wheres,
	})

	rows, err := b.sess.run.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pairs []pivotPair
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		pair := pivotPair{parent: cells[0], related: cells[1]}
		if len(pivotCols) > 0 {
			pair.data = make(map[string]any, len(pivotCols))
			for i, c := range pivotCols {
				pair.data[c] = normalizePivot(cells[2+i])
			}
		}
		pairs = append(pairs, pair)
	}
	return pairs, rows.Err()
}

// normalizePivot turns driver []byte values into strings for friendlier maps.
func normalizePivot(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

// queryRelated fetches related rows where whereCol IN keys, applying an optional
// constraint, and returns the result slice (a reflect []relatedType value).
func (b *Builder) queryRelated(ctx context.Context, relatedType reflect.Type, whereCol string, keys []any, constraint func(*Builder)) (reflect.Value, error) {
	destPtr := reflect.New(reflect.SliceOf(relatedType)) // *[]Related
	rb := newBuilder(b.sess, reflect.New(relatedType).Interface())
	rb.WhereIn(whereCol, keys...)
	if constraint != nil {
		constraint(rb)
	}
	if err := rb.Get(ctx, destPtr.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return destPtr.Elem(), nil
}

// collectChildren gathers the loaded related struct values from a relation field
// across parents, for recursing into nested eager loads.
func collectChildren(parents []reflect.Value, fieldIdx int) []reflect.Value {
	var out []reflect.Value
	for _, p := range parents {
		f := p.Field(fieldIdx)
		switch f.Kind() {
		case reflect.Slice:
			for j := 0; j < f.Len(); j++ {
				e := f.Index(j)
				if e.Kind() == reflect.Ptr {
					if e.IsNil() {
						continue
					}
					e = e.Elem()
				}
				out = append(out, e)
			}
		case reflect.Ptr:
			if !f.IsNil() {
				out = append(out, f.Elem())
			}
		case reflect.Struct:
			out = append(out, f)
		}
	}
	return out
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
