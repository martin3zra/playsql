package metadata

import (
	"reflect"
	"strings"
	"time"
)

var timeType = reflect.TypeOf(time.Time{})

const (
	createdAtColumn = "created_at"
	updatedAtColumn = "updated_at"
)

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

	var (
		pkKind            = reflect.Invalid
		explicitIncrement bool
		sawPK             bool
	)

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
					sawPK = true
					m.PrimaryKey = col
					pkKind = f.Type.Kind()
				case "incrementing":
					explicitIncrement = true
				case "softdelete":
					m.SoftDeletes = true
					m.DeletedAtColumn = col
				case "fillable":
					m.Fillable = append(m.Fillable, col)
				case "guarded":
					m.Guarded = append(m.Guarded, col)
				}
			}
		}

		switch col {
		case createdAtColumn:
			m.CreatedAtColumn = col
		case updatedAtColumn:
			m.UpdatedAtColumn = col
		}

		m.Columns = append(m.Columns, ColumnMeta{DBName: col, FieldIndex: i, PrimaryKey: isPK})
		m.fieldByCol[col] = i
	}

	// Incrementing defaults to whether the key is an integer; an explicit
	// `incrementing` option forces it on regardless of type.
	if sawPK && !explicitIncrement {
		m.Incrementing = isIntegerKind(pkKind)
	}

	return m
}

func isIntegerKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

func tableName(t reflect.Type) string {
	if tn, ok := reflect.New(t).Interface().(TableNamer); ok {
		return tn.TableName()
	}
	// Eloquent convention: snake_case the type name and pluralize.
	return pluralize(snake(t.Name()))
}

// pluralize applies simple English pluralization rules. For irregular nouns,
// define TableName() explicitly.
func pluralize(s string) string {
	if s == "" {
		return s
	}
	switch {
	case hasSuffix(s, "s"), hasSuffix(s, "x"), hasSuffix(s, "z"),
		hasSuffix(s, "ch"), hasSuffix(s, "sh"):
		return s + "es"
	case hasSuffix(s, "y") && !isVowel(s[len(s)-2]):
		return s[:len(s)-1] + "ies" // city -> cities
	default:
		return s + "s"
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
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
