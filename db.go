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
	run     runner
	grammar grammar.Grammar
}

// Model starts a query builder for the given model value. The value is used for
// its type only (metadata); its field values are ignored.
func (s *session) Model(model any) *Builder {
	return newBuilder(s, model)
}

// Exec runs a raw statement on the session's runner (connection or tx).
func (s *session) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.run.ExecContext(ctx, query, args...)
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
	rows, err := s.run.QueryContext(ctx, query, args...)
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
	return s.run.QueryContext(ctx, query, args...)
}

// rawRow runs a query and returns the raw *sql.Row; it backs the generic
// RawScalar. Unexported so only *DB/*Tx (same package) satisfy that interface.
func (s *session) rawRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.run.QueryRowContext(ctx, query, args...)
}

// DB is a pooled connection. It can begin transactions and be closed.
type DB struct {
	*session
	sql *sql.DB
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

	return &DB{sql: conn, session: &session{run: conn, grammar: g}}, nil
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

// Close releases the underlying connection pool.
func (db *DB) Close() error { return db.sql.Close() }

// Tx runs fn inside a transaction. The closure receives a *Tx — so transaction
// code physically cannot reach Close, the pool, or a non-tx runner. Commit on
// success, rollback on error or panic.
func (db *DB) Tx(ctx context.Context, fn func(*Tx) error) (err error) {
	sqlTx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("playsql: begin: %w", err)
	}

	tx := &Tx{session: &session{run: sqlTx, grammar: db.grammar}}

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
