package playsql

import (
	"testing"

	"github.com/martin3zra/playsql/grammar"
)

func TestSessionRebind(t *testing.T) {
	pg := &session{grammar: grammar.Postgres{}}
	sqlite := &session{grammar: grammar.SQLite{}}
	mssql := &session{grammar: grammar.MSSQL{}}

	cases := []struct {
		in, pg, mssql string
	}{
		{`SELECT 1`, `SELECT 1`, `SELECT 1`},
		{`WHERE a = ? AND b = ?`, `WHERE a = $1 AND b = $2`, `WHERE a = @p1 AND b = @p2`},
		{`IN (?, ?, ?)`, `IN ($1, $2, $3)`, `IN (@p1, @p2, @p3)`},
		{`WHERE note = 'is it ok?' AND id = ?`, `WHERE note = 'is it ok?' AND id = $1`, `WHERE note = 'is it ok?' AND id = @p1`},
	}

	for _, c := range cases {
		if got := pg.rebind(c.in); got != c.pg {
			t.Errorf("postgres rebind(%q) = %q, want %q", c.in, got, c.pg)
		}
		if got := mssql.rebind(c.in); got != c.mssql {
			t.Errorf("mssql rebind(%q) = %q, want %q", c.in, got, c.mssql)
		}
		if got := sqlite.rebind(c.in); got != c.in {
			t.Errorf("sqlite rebind(%q) = %q, want it unchanged", c.in, got)
		}
	}
}
