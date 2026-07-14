package playsql_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/martin3zra/playsql"
)

type dynKey struct{}

// twoDBs opens two independent in-memory SQLite databases, each holding one row
// naming itself, plus a resolver that picks between them by ctx.
func twoDBs(t *testing.T) (a, b *sql.DB, resolve func(context.Context) (*sql.DB, error)) {
	t.Helper()

	open := func(name string) *sql.DB {
		db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		t.Cleanup(func() { db.Close() })
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`CREATE TABLE owners (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO owners (id, name) VALUES (1, ?)`, name); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		return db
	}

	a, b = open("alpha"), open("beta")

	resolve = func(ctx context.Context) (*sql.DB, error) {
		switch ctx.Value(dynKey{}) {
		case "alpha":
			return a, nil
		case "beta":
			return b, nil
		default:
			return nil, errors.New("no database in ctx")
		}
	}
	return a, b, resolve
}

type Owner struct {
	ID   int    `db:"id"`
	Name string `db:"name"`
}

func (Owner) Table() string { return "owners" }

func TestDynamicRoutesByContext(t *testing.T) {
	_, _, resolve := twoDBs(t)

	db, err := playsql.Dynamic("sqlite", resolve)
	if err != nil {
		t.Fatalf("Dynamic: %v", err)
	}

	for _, want := range []string{"alpha", "beta", "alpha"} {
		ctx := context.WithValue(context.Background(), dynKey{}, want)

		owner, err := playsql.Query[Owner](db).First(ctx)
		if err != nil {
			t.Fatalf("First(%s): %v", want, err)
		}
		if owner.Name != want {
			t.Errorf("ctx said %q, query hit %q", want, owner.Name)
		}
	}
}

// The resolve error must surface as the query's error, not be swallowed and not
// fall back to some other connection.
func TestDynamicResolveErrorFailsTheQuery(t *testing.T) {
	_, _, resolve := twoDBs(t)

	db, err := playsql.Dynamic("sqlite", resolve)
	if err != nil {
		t.Fatalf("Dynamic: %v", err)
	}

	ctx := context.Background() // no tenant

	if _, err := playsql.Query[Owner](db).Get(ctx); err == nil {
		t.Error("Get with an unresolvable ctx returned no error")
	}

	// Count goes through QueryRowContext, whose *sql.Row cannot carry an error
	// unless errRow builds one. This is the path that regresses silently.
	if _, err := playsql.Query[Owner](db).Count(ctx); err == nil {
		t.Error("Count with an unresolvable ctx returned no error")
	} else if err.Error() != "no database in ctx" {
		t.Errorf("Count lost the resolver's error: got %v", err)
	}
}

// A transaction resolves once and stays on that handle, even if the resolver
// would answer differently mid-flight.
func TestDynamicTxPinsOneConnection(t *testing.T) {
	a, _, _ := twoDBs(t)

	calls := 0
	db, err := playsql.Dynamic("sqlite", func(context.Context) (*sql.DB, error) {
		calls++
		return a, nil
	})
	if err != nil {
		t.Fatalf("Dynamic: %v", err)
	}

	err = db.Tx(context.Background(), func(tx *playsql.Tx) error {
		if _, err := tx.Exec(context.Background(), `INSERT INTO owners (id, name) VALUES (2, 'in-tx')`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}

	if calls != 1 {
		t.Errorf("resolve called %d times inside one Tx, want 1 (the tx must not re-resolve per statement)", calls)
	}

	var n int
	if err := a.QueryRow(`SELECT count(*) FROM owners WHERE name = 'in-tx'`).Scan(&n); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if n != 1 {
		t.Errorf("committed row not found in the resolved database")
	}
}

func TestDynamicCloseDoesNotClosePools(t *testing.T) {
	a, _, resolve := twoDBs(t)

	db, err := playsql.Dynamic("sqlite", resolve)
	if err != nil {
		t.Fatalf("Dynamic: %v", err)
	}

	if err := db.Close(); err == nil {
		t.Error("Close on a Dynamic DB should report that it owns no pool")
	}

	// The caller's pool must still be usable: Close closed nothing.
	if err := a.Ping(); err != nil {
		t.Errorf("Close took the caller's pool with it: %v", err)
	}
}

func TestDynamicRejectsBadWiring(t *testing.T) {
	if _, err := playsql.Dynamic("oracle", func(context.Context) (*sql.DB, error) { return nil, nil }); err == nil {
		t.Error("Dynamic accepted an unsupported dialect")
	}
	if _, err := playsql.Dynamic("sqlite", nil); err == nil {
		t.Error("Dynamic accepted a nil resolve func")
	}
}
