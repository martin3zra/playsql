package playsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

	"github.com/martin3zra/playsql/grammar"
)

// Dynamic returns a DB whose executor is resolved from ctx on every call. The
// grammar is fixed by dialect; only the connection varies.
//
//	db, err := playsql.Dynamic("postgres", func(ctx context.Context) (*sql.DB, error) {
//	    return poolFor(tenantFrom(ctx))
//	})
//	users, err := playsql.Query[User](db).Get(ctx) // runs on the ctx's pool
//
// This is the hook multi-tenancy, read replicas and sharding are built on: the
// connection is chosen at call time, not at construction time. resolve runs once
// per statement, so it should be cheap — a map lookup, not a sql.Open.
//
// The caller owns every pool resolve hands back: Dynamic never opens one and
// Close does not close them (it returns an error, since there is no single pool
// to close). DB.Tx resolves first, then begins on the resolved handle, so a
// transaction stays pinned to one connection for its whole life.
//
// A resolve that returns an error fails the statement with that error; it is
// never swallowed and never falls back to another connection.
func Dynamic(dialect string, resolve func(context.Context) (*sql.DB, error)) (*DB, error) {
	g := grammar.For(dialect)
	if g == nil {
		return nil, fmt.Errorf("playsql: unsupported dialect %q", dialect)
	}
	if resolve == nil {
		return nil, errors.New("playsql: Dynamic requires a resolve func")
	}
	return &DB{
		session: newSession(ctxRunner{resolve: resolve}, g, false),
		resolve: resolve,
	}, nil
}

// ctxRunner is the runner a Dynamic DB executes on: it resolves the real
// executor from ctx per statement and delegates. It sits under the session's
// listenRunner, so events still fire for every statement, resolved or not.
type ctxRunner struct {
	resolve func(context.Context) (*sql.DB, error)
}

func (r ctxRunner) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return db.QueryContext(ctx, query, args...)
}

func (r ctxRunner) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	db, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return db.ExecContext(ctx, query, args...)
}

func (r ctxRunner) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db, err := r.resolve(ctx)
	if err != nil {
		return errRow(ctx, err)
	}
	return db.QueryRowContext(ctx, query, args...)
}

// errRow returns a *sql.Row whose Scan reports err. database/sql exports no way
// to build one — Row's error field is unexported and set only by a failed query
// — so we run a throwaway query against a connector that fails with exactly err.
// Nothing is retried (retries need driver.ErrBadConn) and nothing is held open:
// a Row carrying an error holds no rows and no connection.
func errRow(ctx context.Context, err error) *sql.Row {
	db := sql.OpenDB(errConnector{err: err})
	defer db.Close()
	return db.QueryRowContext(ctx, "")
}

type errConnector struct{ err error }

func (c errConnector) Connect(context.Context) (driver.Conn, error) { return nil, c.err }
func (c errConnector) Driver() driver.Driver                        { return errDriver{err: c.err} }

type errDriver struct{ err error }

func (d errDriver) Open(string) (driver.Conn, error) { return nil, d.err }
