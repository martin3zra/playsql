package grammar

import (
	"strings"
	"testing"
)

func lockQuery(m LockMode) CompiledQuery {
	return CompiledQuery{
		Table:   "users",
		Columns: []string{"id"},
		Wheres:  []WhereClause{{Column: "id", Op: "=", Value: 1}},
		Lock:    m,
	}
}

func TestCompileSelect_LockPerDialect(t *testing.T) {
	tests := []struct {
		name string
		g    Grammar
		mode LockMode
		want string
	}{
		{"postgres for update", Postgres{}, LockUpdate,
			`SELECT "id" FROM "users" WHERE "id" = $1 FOR UPDATE`},
		{"postgres shared", Postgres{}, LockShared,
			`SELECT "id" FROM "users" WHERE "id" = $1 FOR SHARE`},
		{"mysql for update", MySQL{}, LockUpdate,
			"SELECT `id` FROM `users` WHERE `id` = ? FOR UPDATE"},
		{"mysql shared", MySQL{}, LockShared,
			"SELECT `id` FROM `users` WHERE `id` = ? LOCK IN SHARE MODE"},
		// SQLite has no row-level locks: the clause is dropped entirely.
		{"sqlite for update", SQLite{}, LockUpdate,
			`SELECT "id" FROM "users" WHERE "id" = ?`},
		{"sqlite shared", SQLite{}, LockShared,
			`SELECT "id" FROM "users" WHERE "id" = ?`},
		// SQL Server carries the lock as a FROM hint, not a trailing keyword.
		{"mssql for update", MSSQL{}, LockUpdate,
			`SELECT [id] FROM [users] WITH (ROWLOCK, UPDLOCK, HOLDLOCK) WHERE [id] = @p1`},
		{"mssql shared", MSSQL{}, LockShared,
			`SELECT [id] FROM [users] WITH (ROWLOCK, HOLDLOCK) WHERE [id] = @p1`},
		{"no lock", Postgres{}, LockNone,
			`SELECT "id" FROM "users" WHERE "id" = $1`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sql, _ := tc.g.CompileSelect(lockQuery(tc.mode))
			if sql != tc.want {
				t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, tc.want)
			}
		})
	}
}

// The lock keyword must follow LIMIT/OFFSET, not precede it.
func TestCompileSelect_LockFollowsLimit(t *testing.T) {
	q := lockQuery(LockUpdate)
	q.Limit, q.Offset = 10, 5

	sql, _ := Postgres{}.CompileSelect(q)
	want := `SELECT "id" FROM "users" WHERE "id" = $1 LIMIT 10 OFFSET 5 FOR UPDATE`
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
}

// SQL Server pages with OFFSET/FETCH; the hint still sits on the FROM clause.
func TestCompileSelect_LockMSSQLWithPaging(t *testing.T) {
	q := lockQuery(LockUpdate)
	q.Limit = 10

	sql, _ := MSSQL{}.CompileSelect(q)
	if !strings.Contains(sql, `FROM [users] WITH (ROWLOCK, UPDLOCK, HOLDLOCK) WHERE`) {
		t.Errorf("hint not on FROM clause: %s", sql)
	}
	if !strings.HasSuffix(sql, "FETCH NEXT 10 ROWS ONLY") {
		t.Errorf("paging should close the statement: %s", sql)
	}
}

// A lock set on a subquery is meaningless and must never reach the SQL, in
// either the trailing-keyword or the FROM-hint position.
func TestCompileSelect_LockNeverLeaksIntoSubquery(t *testing.T) {
	sub := lockQuery(LockUpdate)
	sub.Table = "orders"

	outer := CompiledQuery{
		Table:      "users",
		Columns:    []string{"id"},
		SubSelects: []SubSelectColumn{{Query: sub, Alias: "recent"}},
	}

	for _, g := range []Grammar{Postgres{}, MySQL{}, MSSQL{}} {
		sql, _ := g.CompileSelect(outer)
		for _, bad := range []string{"FOR UPDATE", "LOCK IN SHARE MODE", "UPDLOCK"} {
			if strings.Contains(sql, bad) {
				t.Errorf("%T: subquery lock leaked (%q): %s", g, bad, sql)
			}
		}
	}
}
