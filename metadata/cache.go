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
}

// ModelMeta is the immutable, parsed description of a model type.
type ModelMeta struct {
	Table           string
	PrimaryKey      string
	Incrementing    bool
	SoftDeletes     bool
	DeletedAtColumn string // set when SoftDeletes is true
	CreatedAtColumn string // set when a created_at column is present
	UpdatedAtColumn string // set when an updated_at column is present
	Fillable        []string
	Guarded         []string
	Columns         []ColumnMeta

	fieldByCol map[string]int // db column -> struct field index (scanner hot path)
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

// ColumnNames returns the database column names in declaration order.
func (m *ModelMeta) ColumnNames() []string {
	names := make([]string, len(m.Columns))
	for i, c := range m.Columns {
		names[i] = c.DBName
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
