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

// OpenDSN is the low-level constructor: a registered database/sql driver name
// and a ready-made DSN. Open builds on it. Useful for tests and custom DSNs.
func OpenDSN(driver, dsn string) (*DB, error) {
	g := grammar.For(driver)
	if g == nil {
		return nil, fmt.Errorf("playsql: unsupported driver %q", driver)
	}

	conn, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("playsql: open: %w", err)
	}
	if err := conn.PingContext(context.Background()); err != nil {
		return nil, fmt.Errorf("playsql: ping: %w", err)
	}

	return &DB{sql: conn, session: &session{run: conn, grammar: g}}, nil
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
