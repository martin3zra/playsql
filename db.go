// Package playsql is an Eloquent-style ORM for Go. See DESIGN.md.
//
// This is the v2 walking skeleton: it proves the end-to-end pipeline
// (metadata -> builder -> grammar -> execution -> scanner) for a single query
// path, with no global state. Breadth (more wheres, relations, persistence)
// builds on this spine.
package playsql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/martin3zra/playsql/grammar"
)

// runner is the only thing that differs between a pooled connection and a
// transaction. Both *sql.DB and *sql.Tx satisfy it.
type runner interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// session carries everything connection-level except the executor. Query entry
// lives here, defined once and inherited identically by DB and Tx.
type session struct {
	run       runner
	grammar   grammar.Grammar
	inTx      bool               // true when run is a *sql.Tx; gates pessimistic locking
	listeners []func(QueryEvent) // DB.Listen callbacks; see events.go
	handlers  []durationHandler  // WhenQueryingForLongerThan callbacks; see querystats.go
	stats     *dbStats           // lifetime counters, shared with transaction sessions
}

// newSession wraps run in a listenRunner so every statement the session issues
// emits a QueryEvent. All connection constructors go through it.
func newSession(run runner, g grammar.Grammar, inTx bool) *session {
	s := &session{grammar: g, inTx: inTx, stats: &dbStats{}}
	s.run = listenRunner{next: run, sess: s}
	return s
}

// child builds the session a transaction runs on: a different executor, but the
// same grammar, listeners, duration handlers and lifetime counters as the
// connection that opened it.
func (s *session) child(run runner) *session {
	c := &session{
		grammar:   s.grammar,
		inTx:      true,
		listeners: s.listeners,
		handlers:  s.handlers,
		stats:     s.stats,
	}
	c.run = listenRunner{next: run, sess: c}
	return c
}

// Model starts a query builder for the given model value. The value is used for
// its type only (metadata); its field values are ignored.
func (s *session) Model(model any) *Builder {
	return newBuilder(s, model)
}

// rebind rewrites "?" bind placeholders to the session dialect's form
// (Postgres "$1", MSSQL "@p1", ...). The query builder already emits
// dialect-correct placeholders, so this only runs on the Raw* / Exec entry
// points, where callers write portable "?" SQL. Dialects that use "?"
// natively (SQLite, MySQL) short-circuit the query unchanged.
//
// A "?" inside a single-quoted string literal is left alone. A literal "?"
// outside a string (e.g. a Postgres jsonb key-exists operator) isn't
// supported in Raw SQL — use the builder for that.
func (s *session) rebind(query string) string {
	if s.grammar.Placeholder(1) == "?" {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n, inString := 0, false
	for i := 0; i < len(query); i++ {
		switch c := query[i]; {
		case c == '\'':
			inString = !inString
			b.WriteByte(c)
		case c == '?' && !inString:
			n++
			b.WriteString(s.grammar.Placeholder(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Exec runs a raw statement on the session's runner (connection or tx).
func (s *session) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.run.ExecContext(ctx, s.rebind(query), args...)
}

// Raw runs an arbitrary query and scans the result into dest, a pointer to a
// slice of model structs or struct pointers (*[]T or *[]*T). Column-to-field
// mapping uses the element type's metadata; unmapped columns are discarded. Use
// it for queries the builder cannot express; see RawQuery for a generic form.
func (s *session) Raw(ctx context.Context, dest any, query string, args ...any) error {
	meta, err := sliceElemMeta(dest)
	if err != nil {
		return err
	}
	rows, err := s.run.QueryContext(ctx, s.rebind(query), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if err := scanRows(rows, dest, meta); err != nil {
		return err
	}
	return rows.Err()
}

// RawRows runs a query and returns the raw *sql.Rows for manual scanning — an
// escape hatch for result shapes Raw cannot map (multiple result sets, columns
// scanned into locals, etc.). The caller owns the rows and must Close them.
func (s *session) RawRows(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.run.QueryContext(ctx, s.rebind(query), args...)
}

// rawRow runs a query and returns the raw *sql.Row; it backs the generic
// RawScalar. Unexported so only *DB/*Tx (same package) satisfy that interface.
func (s *session) rawRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.run.QueryRowContext(ctx, s.rebind(query), args...)
}

// DB is a pooled connection. It can begin transactions and be closed.
//
// A DB from Dynamic has no pool of its own: sql is nil and resolve is set, so
// Close and Tx go through the resolver instead. See dynamic.go.
type DB struct {
	*session
	sql     *sql.DB
	resolve func(context.Context) (*sql.DB, error)
}

// Tx is an in-progress transaction. It cannot be closed or re-opened.
type Tx struct {
	*session
}

// Open connects using the given Config, applying pool settings and selecting
// the grammar for the driver. It returns an error rather than calling log.Fatal.
func Open(cfg Config) (*DB, error) {
	dsn, err := cfg.DSN()
	if err != nil {
		return nil, err
	}

	db, err := OpenDSN(string(cfg.Driver), dsn)
	if err != nil {
		return nil, err
	}

	if cfg.MaxOpenConns > 0 {
		db.sql.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.sql.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		db.sql.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}

	return db, nil
}

// Use wraps an already-open *sql.DB, selecting the grammar by dialect name
// rather than by the real driver. It bypasses sql.Open, so the handle's pool
// and connection lifecycle are owned by the caller — e.g. a go-txdb
// single-connection test transaction opened under the "txdb" driver but spoken
// to with the "postgres" grammar. Because the caller owns the handle, prefer not
// to call the returned DB's Close (it would close the passed *sql.DB).
func Use(existing *sql.DB, dialect string) (*DB, error) {
	g := grammar.For(dialect)
	if g == nil {
		return nil, fmt.Errorf("playsql: unsupported dialect %q", dialect)
	}
	return &DB{sql: existing, session: newSession(existing, g, false)}, nil
}

// UseTx wraps an in-progress *sql.Tx with a grammar selected by dialect name,
// like Use. The returned *Tx cannot be closed or begin nested transactions,
// matching the *Tx handed to DB.Tx.
func UseTx(tx *sql.Tx, dialect string) (*Tx, error) {
	g := grammar.For(dialect)
	if g == nil {
		return nil, fmt.Errorf("playsql: unsupported dialect %q", dialect)
	}
	return &Tx{session: newSession(tx, g, true)}, nil
}

// OpenDSN is the low-level constructor: a driver name and a ready-made DSN. Open
// builds on it. Useful for tests and custom DSNs.
func OpenDSN(driver, dsn string) (*DB, error) {
	g := grammar.For(driver)
	if g == nil {
		return nil, fmt.Errorf("playsql: unsupported driver %q", driver)
	}

	// "mssql" and "sqlserver" select the same grammar, but the SQL Server driver
	// forks register under different names (denisenkom: both; microsoft:
	// "sqlserver" only). Resolve to whichever name is actually registered so the
	// config spelling does not have to match the imported driver.
	conn, err := sql.Open(resolveDriverName(driver), dsn)
	if err != nil {
		return nil, fmt.Errorf("playsql: open: %w", err)
	}

	// An in-memory SQLite database is private to each connection, so a pool of
	// them would see different (empty) databases. Pin to a single connection.
	if isSQLite(driver) && strings.Contains(dsn, ":memory:") {
		conn.SetMaxOpenConns(1)
	}

	if err := conn.PingContext(context.Background()); err != nil {
		return nil, fmt.Errorf("playsql: ping: %w", err)
	}

	return &DB{sql: conn, session: newSession(conn, g, false)}, nil
}

func isSQLite(driver string) bool {
	return driver == "sqlite" || driver == "sqlite3"
}

// resolveDriverName maps a SQL Server config name to a registered driver that
// uses @pN placeholders, which the MSSQL grammar emits. go-mssqldb registers
// both "sqlserver" (native @pN) and "mssql" (legacy, expects ? placeholders), so
// we must always pick "sqlserver" — even when the config says "mssql" — or the
// @pN statements fail to bind. Falls back to "mssql" only if that is the only
// one registered. Non-SQL-Server drivers pass through unchanged.
func resolveDriverName(driver string) string {
	if driver != "mssql" && driver != "sqlserver" {
		return driver
	}
	registered := map[string]bool{}
	for _, d := range sql.Drivers() {
		registered[d] = true
	}
	for _, prefer := range []string{"sqlserver", "mssql"} {
		if registered[prefer] {
			return prefer
		}
	}
	return driver // neither registered; let sql.Open report it
}

// Close releases the underlying connection pool. On a DB from Dynamic there is
// no such pool — the resolver hands back handles the caller owns — so Close
// closes nothing and says so.
func (db *DB) Close() error {
	if db.resolve != nil {
		return errors.New("playsql: Close on a Dynamic DB: the caller owns the resolved pools")
	}
	return db.sql.Close()
}

// Tx runs fn inside a transaction. The closure receives a *Tx — so transaction
// code physically cannot reach Close, the pool, or a non-tx runner. Commit on
// success, rollback on error or panic.
// A Dynamic DB resolves the connection first, then begins on it: the whole
// transaction runs on the one handle ctx selected at Tx time, and a resolver
// that changes its mind mid-transaction cannot move it.
func (db *DB) Tx(ctx context.Context, fn func(*Tx) error) (err error) {
	conn := db.sql
	if db.resolve != nil {
		if conn, err = db.resolve(ctx); err != nil {
			return err
		}
	}

	sqlTx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("playsql: begin: %w", err)
	}

	tx := &Tx{session: db.child(sqlTx)}

	defer func() {
		if p := recover(); p != nil {
			_ = sqlTx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = sqlTx.Rollback()
			return
		}
		err = sqlTx.Commit()
	}()

	return fn(tx)
}
