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

func TestResolvePivotConventions(t *testing.T) {
	user := &ModelMeta{StructName: "User", PrimaryKey: "id"}
	role := &ModelMeta{StructName: "Role", PrimaryKey: "id"}
	rel := RelationMeta{Kind: BelongsToMany}

	pivot, fpk, rpk, pk, rk := ResolvePivot(user, rel, role)
	if pivot != "role_user" { // alphabetical
		t.Errorf("pivot table = %q, want role_user", pivot)
	}
	if fpk != "user_id" || rpk != "role_id" {
		t.Errorf("pivot keys = %q/%q, want user_id/role_id", fpk, rpk)
	}
	if pk != "id" || rk != "id" {
		t.Errorf("join keys = %q/%q, want id/id", pk, rk)
	}
}

func TestResolvePivotOverrides(t *testing.T) {
	user := &ModelMeta{StructName: "User", PrimaryKey: "id"}
	role := &ModelMeta{StructName: "Role", PrimaryKey: "id"}
	rel := RelationMeta{
		Kind: BelongsToMany, PivotTable: "memberships",
		ForeignPivotKey: "member_id", RelatedPivotKey: "grp_id",
	}
	pivot, fpk, rpk, _, _ := ResolvePivot(user, rel, role)
	if pivot != "memberships" || fpk != "member_id" || rpk != "grp_id" {
		t.Errorf("overrides ignored: %q %q %q", pivot, fpk, rpk)
	}
}
