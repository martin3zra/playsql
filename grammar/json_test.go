package grammar

import "testing"

func TestJSONExtract_AllDialects(t *testing.T) {
	cases := []struct {
		name string
		g    Grammar
		want string
	}{
		{"sqlite", SQLite{}, "json_extract(\"prefs\", '$.theme')"},
		{"postgres", Postgres{}, `"prefs" #>> '{theme}'`},
		{"mysql", MySQL{}, "`prefs` ->> '$.theme'"},
		{"mssql", MSSQL{}, "JSON_VALUE([prefs], '$.theme')"},
	}
	for _, c := range cases {
		if got := c.g.JSONExtract("prefs", "theme"); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestJSONExtract_NestedPath(t *testing.T) {
	if got := (Postgres{}).JSONExtract("addr", "geo.lat"); got != `"addr" #>> '{geo,lat}'` {
		t.Errorf("postgres nested: %s", got)
	}
	if got := (MySQL{}).JSONExtract("addr", "geo.lat"); got != "`addr` ->> '$.geo.lat'" {
		t.Errorf("mysql nested: %s", got)
	}
}

func TestWhereJSON_InSelect(t *testing.T) {
	sql, args := Postgres{}.CompileSelect(CompiledQuery{
		Table: "profiles",
		Wheres: []WhereClause{
			{Kind: WhereJSON, Column: "prefs", Path: "theme", Op: "=", Value: "dark"},
			{Kind: WhereBasic, Boolean: "AND", Column: "active", Op: "=", Value: true},
		},
	})
	want := `SELECT * FROM "profiles" WHERE "prefs" #>> '{theme}' = $1 AND "active" = $2`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 2 || args[0] != "dark" || args[1] != true {
		t.Errorf("args mismatch: %#v", args)
	}
}
