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

// MorphTyper lets a model declare the value stored in a child's polymorphic
// *_type column. Defaults to the table name when not implemented.
type MorphTyper interface {
	MorphType() string
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
		StructName:      t.Name(),
		Table:           tableName(t),
		MorphAlias:      morphAlias(t),
		PrimaryKey:      "id",
		Incrementing:    true,
		Relations:       make(map[string]RelationMeta),
		PivotFieldIndex: -1,
		fieldByCol:      make(map[string]int),
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

		col := columnName(f)
		isPK := false
		cast := ""
		isPivotField := false
		readOnly := false
		if play := f.Tag.Get("play"); play != "" {
			for _, opt := range strings.Split(play, ",") {
				opt = strings.TrimSpace(opt)
				if kv := strings.SplitN(opt, "=", 2); len(kv) == 2 {
					if kv[0] == "cast" {
						cast = kv[1]
					}
					continue
				}
				switch opt {
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
				case "pivot":
					isPivotField = true
				case "readonly":
					readOnly = true
				}
			}
		}

		// A map field tagged `play:"pivot"` receives belongsToMany pivot columns;
		// it is neither a relation nor a regular column.
		if isPivotField {
			m.PivotFieldIndex = i
			continue
		}

		// A struct/slice/pointer field is a relation UNLESS it has a cast, which
		// reclassifies it as a (JSON-encoded) column. A morphTo field is an
		// interface, recognized by its tag rather than its type.
		if cast == "" && (isRelationField(f.Type) || isMorphToTag(f)) {
			if rel, ok := parseRelation(f, i); ok {
				m.Relations[f.Name] = rel
			}
			continue
		}

		switch col {
		case createdAtColumn:
			m.CreatedAtColumn = col
		case updatedAtColumn:
			m.UpdatedAtColumn = col
		}

		m.Columns = append(m.Columns, ColumnMeta{DBName: col, FieldIndex: i, PrimaryKey: isPK, Cast: cast, ReadOnly: readOnly})
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

// morphAlias returns the value a model writes into children's *_type columns:
// MorphType() when implemented, otherwise the table name.
func morphAlias(t reflect.Type) string {
	if mt, ok := reflect.New(t).Interface().(MorphTyper); ok {
		return mt.MorphType()
	}
	return tableName(t)
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

// singular reverses the common English plural rules (loose inverse of pluralize).
func singular(s string) string {
	switch {
	case hasSuffix(s, "ies"):
		return s[:len(s)-3] + "y" // cities -> city
	case hasSuffix(s, "ses"), hasSuffix(s, "xes"), hasSuffix(s, "zes"),
		hasSuffix(s, "ches"), hasSuffix(s, "shes"):
		return s[:len(s)-2] // buses -> bus
	case hasSuffix(s, "s"):
		return s[:len(s)-1] // users -> user
	default:
		return s
	}
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

// parseRelation builds a RelationMeta from a relation field's `play` tag. The
// first option is the kind; foreignKey=/localKey=/ownerKey= override conventions.
// Returns ok=false when the tag isn't a recognized relationship.
func parseRelation(f reflect.StructField, index int) (RelationMeta, bool) {
	play := f.Tag.Get("play")
	if play == "" {
		return RelationMeta{}, false
	}
	opts := strings.Split(play, ",")
	kind := RelationKind(strings.TrimSpace(opts[0]))
	switch kind {
	case HasMany, HasOne, BelongsTo, BelongsToMany, HasManyThrough, HasOneThrough,
		MorphOne, MorphMany, MorphTo, MorphToMany, MorphedByMany:
	default:
		return RelationMeta{}, false
	}

	rel := RelationMeta{
		Name:       f.Name,
		Kind:       kind,
		FieldIndex: index,
	}
	// morphTo is polymorphic: the related type is resolved at load time from the
	// holder's MorphOwners map, not from the (interface) field type.
	if kind != MorphTo {
		rel.RelatedType = relatedType(f.Type)
	}
	for _, opt := range opts[1:] {
		kv := strings.SplitN(strings.TrimSpace(opt), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "foreignKey", "fk":
			rel.ForeignKey = kv[1]
		case "localKey", "ownerKey":
			rel.LocalKey = kv[1]
		case "pivot":
			rel.PivotTable = kv[1]
		case "foreignPivotKey":
			rel.ForeignPivotKey = kv[1]
		case "relatedPivotKey":
			rel.RelatedPivotKey = kv[1]
		case "withPivot":
			rel.PivotColumns = strings.Split(kv[1], "|")
		case "through":
			rel.ThroughTable = kv[1]
		case "firstKey":
			rel.FirstKey = kv[1]
		case "secondKey":
			rel.SecondKey = kv[1]
		case "throughKey":
			rel.ThroughKey = kv[1]
		case "morph":
			rel.MorphName = kv[1]
		case "morphId":
			rel.MorphID = kv[1]
		case "morphType":
			rel.MorphType = kv[1]
		}
	}
	if kind == MorphTo && rel.MorphName == "" {
		rel.MorphName = snake(f.Name) // field "Imageable" -> "imageable"
	}
	return rel, true
}

// isMorphToTag reports whether a field's play tag declares a morphTo relation
// (its Go type is an interface, so type-based detection does not apply).
func isMorphToTag(f reflect.StructField) bool {
	play := f.Tag.Get("play")
	if play == "" {
		return false
	}
	return RelationKind(strings.TrimSpace(strings.SplitN(play, ",", 2)[0])) == MorphTo
}

// relatedType unwraps a relation field type to the related struct type:
// []Post, []*Post, *Post, Post all -> Post.
func relatedType(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
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
// Snake exposes the package's snake_case conversion for default aggregate alias
// naming (e.g. relation "BlogPosts" -> "blog_posts_count").
func Snake(s string) string { return snake(s) }

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
