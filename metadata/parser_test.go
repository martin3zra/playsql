package metadata

import "testing"

func TestPluralize(t *testing.T) {
	cases := map[string]string{
		"user":     "users",
		"city":     "cities",
		"box":      "boxes",
		"class":    "classes",
		"bus":      "buses",
		"dish":     "dishes",
		"match":    "matches",
		"day":      "days", // vowel before y -> just add s
		"category": "categories",
	}
	for in, want := range cases {
		if got := pluralize(in); got != want {
			t.Errorf("pluralize(%q) = %q, want %q", in, got, want)
		}
	}
}

// widget has no TableName method, so the table is inferred.
type widget struct {
	ID int64 `db:"id" play:"pk,incrementing"`
}

func TestTableNameInference(t *testing.T) {
	m := For(&widget{})
	if m.Table != "widgets" {
		t.Errorf("inferred table = %q, want %q", m.Table, "widgets")
	}
}

// explicitTabler overrides inference.
type explicitTabler struct {
	ID int64 `db:"id" play:"pk"`
}

func (explicitTabler) TableName() string { return "custom_table" }

func TestTableNameExplicitWins(t *testing.T) {
	if got := For(&explicitTabler{}).Table; got != "custom_table" {
		t.Errorf("explicit TableName ignored: got %q", got)
	}
}
