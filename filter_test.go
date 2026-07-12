package playsql_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/martin3zra/playsql"
)

// userFilters exercises every FilterValue path against the users table
// (id, name, age) seeded by setup: (1,Jane,30) (2,Bob,25) (3,Ann,30).
type userFilters struct{}

func (userFilters) Filters() playsql.FilterMap {
	return playsql.FilterMap{
		"name": func(b *playsql.Builder, v playsql.FilterValue) {
			b.Where("name", "LIKE", "%"+v.String()+"%")
		},
		"starts": func(b *playsql.Builder, v playsql.FilterValue) { // begins-with
			b.Where("name", "LIKE", v.String()+"%")
		},
		"q": func(b *playsql.Builder, v playsql.FilterValue) { // alias: q -> name column
			b.WhereEq("name", v.String())
		},
		"names": func(b *playsql.Builder, v playsql.FilterValue) {
			b.WhereIn("name", v.CSVStrings()...)
		},
		"id": func(b *playsql.Builder, v playsql.FilterValue) {
			b.WhereIn("id", v.CSVInts()...)
		},
		"age": func(b *playsql.Builder, v playsql.FilterValue) {
			op, n := v.OperatorInt()
			b.Where("age", op, n)
		},
	}
}

func TestApplyFilters_Exact(t *testing.T) {
	db := setup(t)
	var users []User
	err := db.Model(&User{}).
		ApplyFilters(url.Values{"name": {"Bob"}}, userFilters{}).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 1 || users[0].Name != "Bob" {
		t.Fatalf("want [Bob], got %+v", users)
	}
}

func TestApplyFilters_CSVIn(t *testing.T) {
	db := setup(t)
	var users []User
	err := db.Model(&User{}).
		ApplyFilters(url.Values{"id": {"1,3"}}, userFilters{}).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2 rows for id IN (1,3), got %d: %+v", len(users), users)
	}
	for _, u := range users {
		if u.ID != 1 && u.ID != 3 {
			t.Errorf("unexpected id %d", u.ID)
		}
	}
}

func TestApplyFilters_DynamicOperator(t *testing.T) {
	db := setup(t)
	cases := []struct {
		raw  string
		want int
	}{
		{">=30", 2}, // Jane, Ann
		{">25", 2},  // Jane, Ann
		{"25", 1},   // Bob (default "=")
		{"<30", 1},  // Bob
	}
	for _, c := range cases {
		var users []User
		err := db.Model(&User{}).
			ApplyFilters(url.Values{"age": {c.raw}}, userFilters{}).
			Get(context.Background(), &users)
		if err != nil {
			t.Fatalf("age=%q: %v", c.raw, err)
		}
		if len(users) != c.want {
			t.Errorf("age=%q: want %d rows, got %d", c.raw, c.want, len(users))
		}
	}
}

func TestApplyFilters_CombineAnd(t *testing.T) {
	db := setup(t)
	// name LIKE %a% (Jane, Ann) AND age >= 30 (Jane, Ann) -> both.
	var users []User
	err := db.Model(&User{}).
		ApplyFilters(url.Values{"name": {"a"}, "age": {">=30"}}, userFilters{}).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2 (Jane, Ann), got %d: %+v", len(users), users)
	}
}

func TestApplyFilters_BeginsWith(t *testing.T) {
	db := setup(t)
	var users []User
	err := db.Model(&User{}).
		ApplyFilters(url.Values{"starts": {"A"}}, userFilters{}).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 1 || users[0].Name != "Ann" {
		t.Fatalf("want [Ann] for name LIKE 'A%%', got %+v", users)
	}
}

func TestApplyFilters_AliasedKey(t *testing.T) {
	db := setup(t)
	// request key "q" maps to the name column, not exposing the column name.
	var users []User
	err := db.Model(&User{}).
		ApplyFilters(url.Values{"q": {"Jane"}}, userFilters{}).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 1 || users[0].Name != "Jane" {
		t.Fatalf("want [Jane], got %+v", users)
	}
}

func TestApplyFilters_CSVStrings(t *testing.T) {
	db := setup(t)
	var users []User
	err := db.Model(&User{}).
		ApplyFilters(url.Values{"names": {"Jane,Bob"}}, userFilters{}).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2 for name IN (Jane,Bob), got %d: %+v", len(users), users)
	}
}

func TestApplyFilters_UnknownKeyIgnored(t *testing.T) {
	db := setup(t)
	var users []User
	err := db.Model(&User{}).
		ApplyFilters(url.Values{"bogus": {"x"}}, userFilters{}).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("unknown filter should be ignored, want all 3, got %d", len(users))
	}
}

func TestApplyFilters_AbsentDeclaredKeyIgnored(t *testing.T) {
	db := setup(t)
	var users []User
	err := db.Model(&User{}).
		ApplyFilters(url.Values{}, userFilters{}).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("no params should apply no filters, want 3, got %d", len(users))
	}
}

func TestApplyFilters_MalformedCoercionErrors(t *testing.T) {
	db := setup(t)
	var users []User
	err := db.Model(&User{}).
		ApplyFilters(url.Values{"age": {"abc"}}, userFilters{}).
		Get(context.Background(), &users)
	if err == nil {
		t.Fatal("malformed numeric filter should surface an error at the terminal")
	}
}

func TestApplyFilters_Typed(t *testing.T) {
	db := setup(t)
	users, err := playsql.Query[User](db).
		ApplyFilters(url.Values{"id": {"2"}}, userFilters{}).
		Get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 1 || users[0].ID != 2 {
		t.Fatalf("want [id=2], got %+v", users)
	}
}

// --- FilterValue unit tests (no DB) ---

func TestFilterValue_Operator(t *testing.T) {
	db := setup(t)
	cases := []struct {
		raw, op, rest string
	}{
		{">=30", ">=", "30"},
		{"<=5", "<=", "5"},
		{"<>x", "<>", "x"},
		{"!=y", "!=", "y"},
		{">1", ">", "1"},
		{"<2", "<", "2"},
		{"=3", "=", "3"},
		{"plain", "=", "plain"},
	}
	for _, c := range cases {
		var gotOp, gotRest string
		probe := probeFilter{fn: func(v playsql.FilterValue) { gotOp, gotRest = v.Operator() }}
		_ = db.Model(&User{}).ApplyFilters(url.Values{"probe": {c.raw}}, probe)
		if gotOp != c.op || gotRest != c.rest {
			t.Errorf("Operator(%q) = (%q,%q), want (%q,%q)", c.raw, gotOp, gotRest, c.op, c.rest)
		}
	}
}

func TestFilterValue_CSVAndBool(t *testing.T) {
	db := setup(t)

	var csv []string
	_ = db.Model(&User{}).ApplyFilters(
		url.Values{"probe": {"a, b ,,c"}},
		probeFilter{fn: func(v playsql.FilterValue) { csv = v.CSV() }},
	)
	if len(csv) != 3 || csv[0] != "a" || csv[1] != "b" || csv[2] != "c" {
		t.Errorf("CSV should trim and drop empties, got %#v", csv)
	}

	var b bool
	_ = db.Model(&User{}).ApplyFilters(
		url.Values{"probe": {"true"}},
		probeFilter{fn: func(v playsql.FilterValue) { b = v.Bool() }},
	)
	if !b {
		t.Error("Bool(\"true\") should be true")
	}

	var all []string
	_ = db.Model(&User{}).ApplyFilters(
		url.Values{"probe": {"a", "b"}}, // repeated key ?probe=a&probe=b
		probeFilter{fn: func(v playsql.FilterValue) { all = v.All() }},
	)
	if len(all) != 2 || all[0] != "a" || all[1] != "b" {
		t.Errorf("All() should return every repeated value, got %#v", all)
	}
}

// probeFilter exposes a single "probe" key whose handler forwards the
// FilterValue to a test closure, so accessors can be asserted directly.
type probeFilter struct {
	fn func(playsql.FilterValue)
}

func (p probeFilter) Filters() playsql.FilterMap {
	return playsql.FilterMap{
		"probe": func(_ *playsql.Builder, v playsql.FilterValue) { p.fn(v) },
	}
}
