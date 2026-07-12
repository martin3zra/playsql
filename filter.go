package playsql

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// FilterFunc applies a single request-driven filter to the builder. It receives
// the builder and the raw request value(s) wrapped in a FilterValue.
type FilterFunc func(b *Builder, v FilterValue)

// FilterMap declares the filters a model exposes, keyed by query-param name.
type FilterMap map[string]FilterFunc

// Filterable is implemented by a per-model filter type that returns its FilterMap.
//
//	type LessonFilters struct{}
//
//	func (LessonFilters) Filters() playsql.FilterMap {
//	    return playsql.FilterMap{
//	        "difficulty": func(b *playsql.Builder, v playsql.FilterValue) {
//	            b.WhereEq("difficulty", v.String())
//	        },
//	    }
//	}
type Filterable interface {
	Filters() FilterMap
}

// ApplyFilters runs every declared filter whose key is present in values. Keys
// absent from the request, or request keys with no matching filter, are ignored.
// values is typically r.URL.Query() or a framework equivalent.
func (b *Builder) ApplyFilters(values url.Values, f Filterable) *Builder {
	for key, fn := range f.Filters() {
		if fn != nil && values.Has(key) {
			fn(b, FilterValue{b: b, key: key, raw: values.Get(key), all: values[key]})
		}
	}
	return b
}

// FilterValue wraps the raw request string(s) for one filter key and exposes
// typed accessors. HTTP query params are always strings; coercion happens here
// so handlers can bind correctly typed values (binding a string to a numeric
// column errors on some drivers, e.g. Postgres). Coercion failures are recorded
// via the builder and surface at the next terminal op.
type FilterValue struct {
	b   *Builder
	key string
	raw string   // first value
	all []string // every value for repeated keys
}

// String returns the raw first value.
func (v FilterValue) String() string { return v.raw }

// Int coerces the value to an int, recording an error on failure.
func (v FilterValue) Int() int {
	n, err := strconv.Atoi(strings.TrimSpace(v.raw))
	if err != nil {
		v.fail("int")
	}
	return n
}

// Int64 coerces the value to an int64, recording an error on failure.
func (v FilterValue) Int64() int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(v.raw), 10, 64)
	if err != nil {
		v.fail("int64")
	}
	return n
}

// Float coerces the value to a float64, recording an error on failure.
func (v FilterValue) Float() float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v.raw), 64)
	if err != nil {
		v.fail("float")
	}
	return f
}

// Bool coerces the value to a bool (1/0, t/f, true/false), recording an error on
// failure.
func (v FilterValue) Bool() bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v.raw))
	if err != nil {
		v.fail("bool")
	}
	return b
}

// All returns every value for a repeated key (?tag=a&tag=b).
func (v FilterValue) All() []string { return v.all }

// CSV splits the raw value on commas, trimming whitespace and dropping empties.
func (v FilterValue) CSV() []string {
	parts := strings.Split(v.raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// CSVStrings is CSV as []any, sized for the variadic WhereIn.
//
//	b.WhereIn("slug", v.CSVStrings()...)
func (v FilterValue) CSVStrings() []any {
	parts := v.CSV()
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = p
	}
	return out
}

// CSVInts is CSV coerced to ints as []any, sized for the variadic WhereIn. A
// non-numeric element records an error.
//
//	b.WhereIn("id", v.CSVInts()...)
func (v FilterValue) CSVInts() []any {
	parts := v.CSV()
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			v.fail("int")
			continue
		}
		out = append(out, n)
	}
	return out
}

// operators recognized by Operator, longest first so ">=" beats ">".
var filterOperators = []string{">=", "<=", "<>", "!=", ">", "<", "="}

// Operator peels a leading comparison operator off the raw value, returning the
// operator and the remaining value. With no leading operator it defaults to "=".
// Operators pass through to the SQL grammar unchanged.
//
//	// ?age=>=30  ->  ">=", "30"
//	op, rest := v.Operator()
//	b.Where("age", op, rest)
func (v FilterValue) Operator() (op, rest string) {
	s := strings.TrimSpace(v.raw)
	for _, o := range filterOperators {
		if strings.HasPrefix(s, o) {
			return o, strings.TrimSpace(s[len(o):])
		}
	}
	return "=", s
}

// OperatorInt is Operator with the remaining value coerced to an int, for numeric
// columns where binding a string is unsafe. A non-numeric remainder records an
// error.
func (v FilterValue) OperatorInt() (op string, n int) {
	op, rest := v.Operator()
	parsed, err := strconv.Atoi(rest)
	if err != nil {
		v.fail("int")
	}
	return op, parsed
}

// OperatorFloat is Operator with the remaining value coerced to a float64.
func (v FilterValue) OperatorFloat() (op string, f float64) {
	op, rest := v.Operator()
	parsed, err := strconv.ParseFloat(rest, 64)
	if err != nil {
		v.fail("float")
	}
	return op, parsed
}

func (v FilterValue) fail(kind string) {
	v.b.fail(fmt.Errorf("filter %q: cannot parse %q as %s", v.key, v.raw, kind))
}
