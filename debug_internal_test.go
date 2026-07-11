package playsql

import (
	"context"
	"fmt"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

type dxUser struct {
	ID   int64  `db:"id" play:"pk,incrementing"`
	Name string `db:"name"`
}

func (dxUser) TableName() string { return "dx_users" }

// capLogger records what Debug/DD would print.
type capLogger struct{ msgs []string }

func (c *capLogger) Printf(format string, args ...any) {
	c.msgs = append(c.msgs, fmt.Sprintf(format, args...))
}

func (c *capLogger) all() string { return strings.Join(c.msgs, "") }

func openDX(t *testing.T) *DB {
	t.Helper()
	db, err := Open(Config{Driver: SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE dx_users (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO dx_users (id, name) VALUES (1,'Jane'),(2,'Bob')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

func TestDebug_OnlyAffectsOneBuilder(t *testing.T) {
	db := openDX(t)
	orig := db.session

	b1 := db.Model(&dxUser{})
	b2 := db.Model(&dxUser{})

	b1.Debug()

	// b1 got a private session wrapping a debugRunner; b2 and the DB are untouched.
	if b1.sess == orig {
		t.Fatal("Debug() should give the builder its own session")
	}
	if _, ok := b1.sess.run.(debugRunner); !ok {
		t.Fatalf("b1 runner should be debugRunner, got %T", b1.sess.run)
	}
	if b2.sess != orig {
		t.Fatal("sibling builder's session must be unchanged")
	}
	if _, ok := orig.run.(debugRunner); ok {
		t.Fatal("DB session runner must not be wrapped")
	}
}

func TestDebug_LogsSQLArgsAndDuration(t *testing.T) {
	db := openDX(t)
	log := &capLogger{}

	b := db.Model(&dxUser{})
	b.logger = log // inject before Debug so the debugRunner captures it
	b.Debug()

	var out []dxUser
	if err := b.WhereEq("id", int64(1)).Get(context.Background(), &out); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 row, got %d", len(out))
	}

	got := log.all()
	for _, want := range []string{"[playsql]", "SQL:", "Args:", "Duration:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("debug output missing %q:\n%s", want, got)
		}
	}
}
