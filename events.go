package playsql

import (
	"context"
	"database/sql"
	"time"
)

// QueryOp identifies which runner method executed a statement.
type QueryOp string

const (
	OpQuery    QueryOp = "query"    // QueryContext
	OpQueryRow QueryOp = "queryRow" // QueryRowContext
	OpExec     QueryOp = "exec"     // ExecContext
)

// QueryEvent describes one statement sent to the database. Listeners registered
// with DB.Listen receive one per statement — builder terminals, raw queries,
// eager loads, pivot and persistence writes alike, since all of them run through
// the session's runner.
//
// Duration is measured around the driver call, so it covers network and server
// time but not the row scanning playsql does afterwards. SQL carries
// placeholders, not interpolated values.
type QueryEvent struct {
	Ctx      context.Context // the ctx the statement ran under (trace/tenant IDs)
	SQL      string          // as sent to the driver
	Args     []any           // bind arguments
	Duration time.Duration
	Err      error   // non-nil when the driver call failed
	Op       QueryOp // which runner method ran it
	InTx     bool    // ran on a *Tx runner
}

// Listen registers fn to run after every statement this connection executes,
// including statements run inside DB.Tx. Listeners observe; they cannot alter,
// abort, or retry a statement. A listener runs synchronously in the goroutine
// that issued the query, so a slow listener slows the query. A panicking
// listener is contained: the panic is swallowed and the query still returns.
//
// Call Listen during setup, before the DB serves queries. Registering
// concurrently with in-flight queries is a data race, the same way
// http.Handle after ListenAndServe is.
//
// Statements halted by Builder.DD never reach the database, so they emit no
// event. Statements under Builder.Debug emit one as usual, in addition to the
// debug log line.
func (db *DB) Listen(fn func(QueryEvent)) {
	db.listeners = append(db.listeners, fn)
}

// emit accumulates e into the lifetime and ctx-scoped counters, then delivers it
// to every listener, isolating panics so one bad listener cannot take down an
// unrelated query.
func (s *session) emit(e QueryEvent) {
	s.record(e)
	for _, fn := range s.listeners {
		func() {
			defer func() { _ = recover() }()
			fn(e)
		}()
	}
}

// listenRunner times every statement and emits a QueryEvent, then delegates to
// the real runner. Every session wraps its runner in one — including the
// sessions DB.Tx builds around a *sql.Tx — so no call site has to know it
// exists. With no listeners registered, emit walks an empty slice.
type listenRunner struct {
	next runner
	sess *session
}

func (l listenRunner) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := l.next.QueryContext(ctx, query, args...)
	l.sess.emit(l.event(ctx, OpQuery, query, args, start, err))
	return rows, err
}

// QueryRowContext cannot report a duration or an error worth having:
// *sql.Row defers the driver work to Scan, which happens after this returns.
// The event fires with a near-zero Duration and a nil Err. Builder.Count and
// the Postgres insert-returning-id path go through here.
func (l listenRunner) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	start := time.Now()
	row := l.next.QueryRowContext(ctx, query, args...)
	l.sess.emit(l.event(ctx, OpQueryRow, query, args, start, nil))
	return row
}

func (l listenRunner) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	res, err := l.next.ExecContext(ctx, query, args...)
	l.sess.emit(l.event(ctx, OpExec, query, args, start, err))
	return res, err
}

func (l listenRunner) event(ctx context.Context, op QueryOp, query string, args []any, start time.Time, err error) QueryEvent {
	return QueryEvent{
		Ctx:      ctx,
		SQL:      query,
		Args:     args,
		Duration: time.Since(start),
		Err:      err,
		Op:       op,
		InTx:     l.sess.inTx,
	}
}
