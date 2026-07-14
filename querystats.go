package playsql

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// QueryStats is a snapshot of the query time accumulated by one tracked scope —
// or, from DB.TotalQueryDuration and DB.QueryCount, by the connection over its
// whole life.
type QueryStats struct {
	Total   time.Duration // cumulative time across every statement in the scope
	Count   int           // statements executed in the scope
	Slowest QueryEvent    // slowest single statement so far
}

// --- ctx-scoped accumulation ---

type scopeKey struct{}

// queryScope accumulates the query time of every statement run under one ctx.
// A request may fan its queries out across goroutines, so the counters take a
// lock rather than being atomics: Total, Count and Slowest have to advance
// together for a snapshot to be coherent.
type queryScope struct {
	mu      sync.Mutex
	total   time.Duration
	count   int
	slowest QueryEvent
	fired   map[int]bool // handler index -> already fired in this scope
}

// TrackQueryTime returns a child ctx carrying a fresh query-time accumulator.
// Every statement run under that ctx adds to it, and the handlers registered
// with DB.WhenQueryingForLongerThan fire when its cumulative time crosses their
// threshold.
//
// Seed one scope per unit of work whose database time you want bounded — a
// request in HTTP middleware, an iteration of a worker loop, a queued job:
//
//	ctx := playsql.TrackQueryTime(r.Context())
//	next.ServeHTTP(w, r.WithContext(ctx))
//
// This is what a PHP framework gets for free from its process-per-request
// model. A Go *DB is long-lived and shared by every goroutine in the process,
// so the scope has to be marked explicitly. Statements run under a ctx that was
// never tracked accumulate nothing and fire no handler.
//
// Scopes do not nest: calling TrackQueryTime on an already-tracked ctx starts a
// fresh, independent accumulator, and statements under the child no longer
// count toward the parent.
func TrackQueryTime(ctx context.Context) context.Context {
	return context.WithValue(ctx, scopeKey{}, &queryScope{})
}

// Stats returns the query time accumulated so far by ctx's scope. It returns the
// zero QueryStats for a ctx that TrackQueryTime never touched.
func Stats(ctx context.Context) QueryStats {
	sc := scopeFrom(ctx)
	if sc == nil {
		return QueryStats{}
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.snapshot()
}

func scopeFrom(ctx context.Context) *queryScope {
	if ctx == nil {
		return nil
	}
	sc, _ := ctx.Value(scopeKey{}).(*queryScope)
	return sc
}

// snapshot copies the counters. Callers hold sc.mu.
func (sc *queryScope) snapshot() QueryStats {
	return QueryStats{Total: sc.total, Count: sc.count, Slowest: sc.slowest}
}

// record folds e into the scope and returns the resulting snapshot along with
// the handlers this statement just tripped — those whose threshold the scope has
// now crossed and which have not fired for this scope before. They are returned
// rather than called so they run outside the lock.
func (sc *queryScope) record(e QueryEvent, handlers []durationHandler) (QueryStats, []durationHandler) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.total += e.Duration
	sc.count++
	if e.Duration > sc.slowest.Duration {
		sc.slowest = e
	}

	var tripped []durationHandler
	for i, h := range handlers {
		if sc.total < h.threshold || sc.fired[i] {
			continue
		}
		if sc.fired == nil {
			sc.fired = make(map[int]bool, len(handlers))
		}
		sc.fired[i] = true
		tripped = append(tripped, h)
	}
	return sc.snapshot(), tripped
}

// --- threshold handlers ---

type durationHandler struct {
	threshold time.Duration
	fn        func(QueryEvent, QueryStats)
}

// WhenQueryingForLongerThan registers fn to run when the statements of a single
// tracked scope have cumulatively spent longer than threshold in the database.
// It is the alarm for the request that dies of a thousand cheap queries, which
// no per-statement slow-query log would catch.
//
// fn receives the statement that tipped the scope over and the scope's stats at
// that moment (total time, statement count, slowest statement). It fires at most
// once per scope: a fresh scope re-arms it, so there is nothing to reset. Like
// DB.Listen it runs synchronously, panics are contained, and it must be
// registered during setup, before the DB serves queries.
//
// It only sees statements run under a ctx from TrackQueryTime. Registering a
// handler and then querying with an untracked ctx logs a one-time warning,
// because the alternative is silence.
func (db *DB) WhenQueryingForLongerThan(threshold time.Duration, fn func(QueryEvent, QueryStats)) {
	db.handlers = append(db.handlers, durationHandler{threshold: threshold, fn: fn})
}

// --- lifetime counters ---

// dbStats counts every statement a connection runs, across all scopes and
// transactions. Read at any time, from any goroutine.
type dbStats struct {
	nanos atomic.Int64
	count atomic.Int64
}

// TotalQueryDuration is the time this connection has spent executing statements
// since it was opened, or since the last ResetQueryStats. It spans every
// goroutine and transaction, so it measures the whole process's database load —
// for the database time of one request, use Stats with a tracked ctx.
func (db *DB) TotalQueryDuration() time.Duration {
	return time.Duration(db.stats.nanos.Load())
}

// QueryCount is the number of statements this connection has run since it was
// opened, or since the last ResetQueryStats.
func (db *DB) QueryCount() int64 { return db.stats.count.Load() }

// ResetQueryStats zeroes the lifetime counters. Useful to measure one benchmark
// or one test in isolation.
func (db *DB) ResetQueryStats() {
	db.stats.nanos.Store(0)
	db.stats.count.Store(0)
}

// --- recording ---

// record accumulates one statement into the lifetime counters and into the ctx's
// scope, then fires any handler the scope just tripped. Called by emit for every
// statement, before the DB.Listen listeners run.
func (s *session) record(e QueryEvent) {
	s.stats.nanos.Add(int64(e.Duration))
	s.stats.count.Add(1)

	sc := scopeFrom(e.Ctx)
	if sc == nil {
		if len(s.handlers) > 0 {
			warnUntracked()
		}
		return
	}

	snap, tripped := sc.record(e, s.handlers)
	for _, h := range tripped {
		func() {
			defer func() { _ = recover() }()
			h.fn(e, snap)
		}()
	}
}

var untrackedOnce sync.Once

// warnUntracked fires once per process, the first time a statement runs under an
// untracked ctx while duration handlers are registered — the forgotten-middleware
// case, where handlers would otherwise never fire and never say why.
func warnUntracked() {
	untrackedOnce.Do(func() {
		defaultLogger{}.Printf("[playsql] WhenQueryingForLongerThan is registered, " +
			"but a query ran under a context that TrackQueryTime never wrapped: " +
			"its time is not accumulated and no handler will fire for it. " +
			"Wrap the request context, e.g. ctx := playsql.TrackQueryTime(r.Context()).")
	})
}
