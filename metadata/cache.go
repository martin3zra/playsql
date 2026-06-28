// Package metadata is the single source of truth for model shape and config.
//
// Reflection happens here and nowhere else: builder, scanner, and grammar
// consume *ModelMeta instead of walking struct fields themselves. Metadata is
// parsed once per reflect.Type and cached immutably for the process lifetime.
package metadata

import (
	"reflect"
	"sync"
)

// ColumnMeta maps a database column to a struct field.
type ColumnMeta struct {
	DBName     string // database column name
	FieldIndex int    // index into the struct's fields
	PrimaryKey bool
	Cast       string // e.g. "json"; empty for a plain scalar column
	ReadOnly   bool   // scanned but never written (e.g. aggregate result columns)
}

// RelationKind enumerates the supported relationship types.
type RelationKind string

const (
	HasMany        RelationKind = "hasMany"
	HasOne         RelationKind = "hasOne"
	BelongsTo      RelationKind = "belongsTo"
	BelongsToMany  RelationKind = "belongsToMany"
	HasManyThrough RelationKind = "hasManyThrough"
	HasOneThrough  RelationKind = "hasOneThrough"
	MorphOne       RelationKind = "morphOne"
	MorphMany      RelationKind = "morphMany"
	MorphTo        RelationKind = "morphTo"
	MorphToMany    RelationKind = "morphToMany"
	MorphedByMany  RelationKind = "morphedByMany"
)

// RelationMeta describes one relationship field on a model. Keys may be empty
// (resolved by convention via ResolveRelationKeys/ResolvePivot at load time).
type RelationMeta struct {
	Name        string // struct field name (the With() key)
	Kind        RelationKind
	FieldIndex  int          // index of the relation field on the parent struct
	RelatedType reflect.Type // the related struct type (slice/pointer unwrapped)
	ForeignKey  string       // explicit override
	LocalKey    string       // explicit override (local key / owner key)

	// belongsToMany only:
	PivotTable      string
	ForeignPivotKey string   // parent's key column in the pivot
	RelatedPivotKey string   // related's key column in the pivot
	PivotColumns    []string // extra pivot columns to load (withPivot=)

	// has*Through only:
	ThroughTable string // intermediate table name (required)
	FirstKey     string // parent's foreign key on the through table
	SecondKey    string // through's foreign key on the far (related) table
	ThroughKey   string // through table's primary key

	// morphOne/morphMany only:
	MorphName string // the polymorphic name, e.g. "commentable"
	MorphID   string // override; default MorphName + "_id"
	MorphType string // override; default MorphName + "_type"
}

// ModelMeta is the immutable, parsed description of a model type.
type ModelMeta struct {
	StructName      string // Go type name (for convention-based key naming)
	Table           string
	MorphAlias      string // value stored in a child's *_type column (default: Table)
	PrimaryKey      string
	Incrementing    bool
	SoftDeletes     bool
	DeletedAtColumn string // set when SoftDeletes is true
	CreatedAtColumn string // set when a created_at column is present
	UpdatedAtColumn string // set when an updated_at column is present
	Fillable        []string
	Guarded         []string
	Columns         []ColumnMeta
	Relations       map[string]RelationMeta // keyed by struct field name
	PivotFieldIndex int                     // map[string]any field receiving pivot data; -1 if none

	fieldByCol map[string]int // db column -> struct field index (scanner hot path)
}

// ResolveRelationKeys returns the foreign key (on the "child" side) and the
// other key (local key on the parent for has-*, owner key on the related for
// belongsTo), filling in Eloquent conventions where the relation left them blank.
func ResolveRelationKeys(parent *ModelMeta, rel RelationMeta, related *ModelMeta) (foreignKey, otherKey string) {
	switch rel.Kind {
	case HasMany, HasOne:
		foreignKey = rel.ForeignKey
		if foreignKey == "" {
			foreignKey = snake(parent.StructName) + "_id"
		}
		otherKey = rel.LocalKey
		if otherKey == "" {
			otherKey = parent.PrimaryKey
		}
	case BelongsTo:
		foreignKey = rel.ForeignKey
		if foreignKey == "" {
			foreignKey = snake(related.StructName) + "_id"
		}
		otherKey = rel.LocalKey
		if otherKey == "" {
			otherKey = related.PrimaryKey
		}
	}
	return foreignKey, otherKey
}

// ResolvePivot fills in belongsToMany conventions. The pivot table defaults to
// the two singular model names joined alphabetically (role_user); the pivot key
// columns default to <model>_id; the join keys default to each model's PK.
func ResolvePivot(parent *ModelMeta, rel RelationMeta, related *ModelMeta) (pivotTable, foreignPivotKey, relatedPivotKey, parentKey, relatedKey string) {
	pSnake := snake(parent.StructName)
	rSnake := snake(related.StructName)

	pivotTable = rel.PivotTable
	if pivotTable == "" {
		if pSnake < rSnake {
			pivotTable = pSnake + "_" + rSnake
		} else {
			pivotTable = rSnake + "_" + pSnake
		}
	}
	foreignPivotKey = rel.ForeignPivotKey
	if foreignPivotKey == "" {
		foreignPivotKey = pSnake + "_id"
	}
	relatedPivotKey = rel.RelatedPivotKey
	if relatedPivotKey == "" {
		relatedPivotKey = rSnake + "_id"
	}
	parentKey = parent.PrimaryKey
	relatedKey = related.PrimaryKey
	return
}

// ResolveMorphPivot fills in morphToMany/morphedByMany conventions: the pivot
// table, the parent/related pivot key columns, each model's local key, and the
// polymorphic type column plus the value to match. The morphable side (the one
// the *_type column describes) differs by direction: for morphToMany the parent
// is morphable; for morphedByMany the related is.
func ResolveMorphPivot(parent *ModelMeta, rel RelationMeta, related *ModelMeta) (pivotTable, fpk, rpk, parentKey, relatedKey, typeCol, typeValue string) {
	morph := rel.MorphName
	pivotTable = rel.PivotTable
	if pivotTable == "" {
		pivotTable = morph + "s" // "taggable" -> "taggables"
	}
	typeCol = rel.MorphType
	if typeCol == "" {
		typeCol = morph + "_type"
	}
	morphIDCol := rel.MorphID
	if morphIDCol == "" {
		morphIDCol = morph + "_id" // "taggable_id"
	}
	parentKey = parent.PrimaryKey
	relatedKey = related.PrimaryKey

	if rel.Kind == MorphToMany {
		// Parent is the morphable: pivot.<morph>_id = parent, related's key column.
		fpk = morphIDCol
		rpk = rel.RelatedPivotKey
		if rpk == "" {
			rpk = snake(related.StructName) + "_id"
		}
		typeValue = parent.MorphAlias
	} else { // MorphedByMany: related is the morphable.
		fpk = rel.ForeignPivotKey
		if fpk == "" {
			fpk = snake(parent.StructName) + "_id"
		}
		rpk = morphIDCol
		typeValue = related.MorphAlias
	}
	return pivotTable, fpk, rpk, parentKey, relatedKey, typeCol, typeValue
}

// ResolveMorphKeys fills in morphOne/morphMany conventions: the related row's
// id and type columns, the parent's local key, and the type value to match
// (parent.MorphAlias).
func ResolveMorphKeys(parent *ModelMeta, rel RelationMeta) (idCol, typeCol, localKey, typeValue string) {
	idCol = rel.MorphID
	if idCol == "" {
		idCol = rel.MorphName + "_id"
	}
	typeCol = rel.MorphType
	if typeCol == "" {
		typeCol = rel.MorphName + "_type"
	}
	localKey = rel.LocalKey
	if localKey == "" {
		localKey = parent.PrimaryKey
	}
	typeValue = parent.MorphAlias
	return idCol, typeCol, localKey, typeValue
}

// ResolveThrough fills in has*Through conventions. ThroughTable is required;
// the keys default to Eloquent conventions. Returns:
//   - throughTable: the intermediate table
//   - firstKey:   parent's FK on the through table   (default snake(parent)_id)
//   - secondKey:  through's FK on the far table       (default singular(through)_id)
//   - throughKey: through table PK                     (default "id")
//   - localKey:   parent's local key                   (default parent PK)
func ResolveThrough(parent *ModelMeta, rel RelationMeta) (throughTable, firstKey, secondKey, throughKey, localKey string) {
	throughTable = rel.ThroughTable
	firstKey = rel.FirstKey
	if firstKey == "" {
		firstKey = snake(parent.StructName) + "_id"
	}
	secondKey = rel.SecondKey
	if secondKey == "" {
		secondKey = singular(throughTable) + "_id"
	}
	throughKey = rel.ThroughKey
	if throughKey == "" {
		throughKey = "id"
	}
	localKey = rel.LocalKey
	if localKey == "" {
		localKey = parent.PrimaryKey
	}
	return
}

// CanFill reports whether a column may be mass-assigned from a map. Fillable is
// a whitelist; if empty, Guarded acts as a blacklist; if both are empty, all
// columns are assignable.
func (m *ModelMeta) CanFill(column string) bool {
	if len(m.Fillable) > 0 {
		return containsStr(m.Fillable, column)
	}
	if len(m.Guarded) > 0 {
		return !containsStr(m.Guarded, column)
	}
	return true
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// FieldIndexByColumn returns the struct field index for a database column.
func (m *ModelMeta) FieldIndexByColumn(col string) (int, bool) {
	i, ok := m.fieldByCol[col]
	return i, ok
}

// Column returns the metadata for a database column.
func (m *ModelMeta) Column(col string) (ColumnMeta, bool) {
	if i, ok := m.fieldByCol[col]; ok {
		for _, c := range m.Columns {
			if c.FieldIndex == i {
				return c, true
			}
		}
	}
	return ColumnMeta{}, false
}

// ColumnNames returns the database column names in declaration order.
// ColumnNames returns the real table columns for the default SELECT projection.
// ReadOnly columns (computed/aggregate results with no backing column) are
// excluded — they only appear when explicitly added as aggregate subqueries.
func (m *ModelMeta) ColumnNames() []string {
	names := make([]string, 0, len(m.Columns))
	for _, c := range m.Columns {
		if c.ReadOnly {
			continue
		}
		names = append(names, c.DBName)
	}
	return names
}

// PrimaryKeyFieldIndex returns the struct field index of the primary key column.
func (m *ModelMeta) PrimaryKeyFieldIndex() (int, bool) {
	for _, c := range m.Columns {
		if c.PrimaryKey {
			return c.FieldIndex, true
		}
	}
	i, ok := m.fieldByCol[m.PrimaryKey]
	return i, ok
}

var cache sync.Map // reflect.Type -> *ModelMeta

// For returns the metadata for a model value, parsing once and caching by type.
// value may be a pointer; it is dereferenced to the struct type.
func For(value any) *ModelMeta {
	t := reflect.TypeOf(value)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if cached, ok := cache.Load(t); ok {
		return cached.(*ModelMeta)
	}

	meta := parse(t)
	actual, _ := cache.LoadOrStore(t, meta)
	return actual.(*ModelMeta)
}
