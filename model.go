package playsql

import (
	"strconv"

	"github.com/martin3zra/playsql/metadata"
)

// Model is the optional embeddable base. Embed it to get persistence-state
// tracking — whether the row exists yet (insert vs update) and which columns are
// dirty (so Update writes only what changed):
//
//	type User struct {
//		playsql.Model
//		ID   int64  `db:"id" play:"pk,incrementing"`
//		Name string `db:"name"`
//	}
//
// Plain structs without the base still work; they fall back to a zero-key
// heuristic for Save and update every column.
type Model struct {
	exists   bool
	original map[string]any
	extras   map[string]any // aggregate columns with no matching struct field
}

// baseAccessor is satisfied by any type embedding Model (methods are promoted
// through the embedded value). It lets the package read/write persistence state
// without reflecting over unexported fields.
type baseAccessor interface {
	playExists() bool
	playMarkPersisted(original map[string]any)
	playOriginal() map[string]any
	playSetExtra(name string, value any)
}

func (m *Model) playExists() bool { return m.exists }

func (m *Model) playMarkPersisted(original map[string]any) {
	m.exists = true
	m.original = original
}

func (m *Model) playOriginal() map[string]any { return m.original }

func (m *Model) playSetExtra(name string, value any) {
	if m.extras == nil {
		m.extras = make(map[string]any)
	}
	m.extras[name] = value
}

// Aggregate returns the raw value of an aggregate column (e.g. "comments_count")
// loaded by WithCount/WithSum/… that had no matching struct field, and whether
// it was present.
func (m *Model) Aggregate(alias string) (any, bool) {
	v, ok := m.extras[alias]
	return v, ok
}

// CountOf returns the WithCount result for a relation (the <relation>_count
// aggregate), or 0 if absent.
func (m *Model) CountOf(relation string) int64 {
	v, _ := m.Aggregate(metadata.Snake(relation) + "_count")
	return toInt64(v)
}

// SumOf returns the WithSum result for a relation/column (<relation>_sum_<column>),
// or 0 if absent.
func (m *Model) SumOf(relation, column string) int64 {
	v, _ := m.Aggregate(metadata.Snake(relation) + "_sum_" + column)
	return toInt64(v)
}

// toInt64 coerces the common driver scalar representations to int64.
func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	case []byte:
		return parseNumeric(string(n))
	case string:
		return parseNumeric(n)
	default:
		return 0
	}
}

// parseNumeric parses a driver text scalar (which may be a decimal like
// "12.00") into int64; returns 0 on failure.
func parseNumeric(s string) int64 {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}

// baseOf returns the embedded Model accessor if the value has one.
func baseOf(model any) (baseAccessor, bool) {
	acc, ok := model.(baseAccessor)
	return acc, ok
}
