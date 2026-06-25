package metadata

import (
	"reflect"
	"strings"
	"time"
)

var timeType = reflect.TypeOf(time.Time{})

// TableNamer lets a model declare its table name statically.
type TableNamer interface {
	TableName() string
}

// parse builds a ModelMeta from a struct type via reflection. Config is static:
// table from the TableNamer interface (else snake(type name)), columns from the
// `db` tag (else `json`, else snake(field name)), primary key from `play:"pk"`
// (else "id"). This is the only reflection path in the library.
func parse(t reflect.Type) *ModelMeta {
	if t.Kind() != reflect.Struct {
		panic("playsql/metadata: model must be a struct, got " + t.Kind().String())
	}

	m := &ModelMeta{
		Table:        tableName(t),
		PrimaryKey:   "id",
		Incrementing: true,
		fieldByCol:   make(map[string]int),
	}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)

		// Skip embedded/anonymous and unexported fields.
		if f.Anonymous || f.PkgPath != "" {
			continue
		}

		// Skip relation fields for the skeleton (slices, pointers to struct).
		if isRelationField(f.Type) {
			continue
		}

		col := columnName(f)
		isPK := false
		if play := f.Tag.Get("play"); play != "" {
			for _, opt := range strings.Split(play, ",") {
				switch strings.TrimSpace(opt) {
				case "pk":
					isPK = true
					m.PrimaryKey = col
				case "incrementing":
					m.Incrementing = true
				}
			}
		}

		m.Columns = append(m.Columns, ColumnMeta{DBName: col, FieldIndex: i, PrimaryKey: isPK})
		m.fieldByCol[col] = i
	}

	return m
}

func tableName(t reflect.Type) string {
	if tn, ok := reflect.New(t).Interface().(TableNamer); ok {
		return tn.TableName()
	}
	return snake(t.Name())
}

func columnName(f reflect.StructField) string {
	if db := f.Tag.Get("db"); db != "" {
		return db
	}
	if j := f.Tag.Get("json"); j != "" {
		if name := strings.Split(j, ",")[0]; name != "" && name != "-" {
			return name
		}
	}
	return snake(f.Name)
}

func isRelationField(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Slice:
		e := t.Elem()
		if e.Kind() == reflect.Ptr {
			e = e.Elem()
		}
		return e.Kind() == reflect.Struct && e != timeType
	case reflect.Ptr:
		return t.Elem().Kind() == reflect.Struct && t.Elem() != timeType
	}
	return false
}

// snake converts CamelCase / PascalCase to snake_case.
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
