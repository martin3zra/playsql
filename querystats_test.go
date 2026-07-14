package playsql_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/martin3zra/playsql"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, no CGO)
)

// getAll runs one SELECT, the unit of work these tests accumulate.
func getAll(t *testing.T, db *playsql.DB, ctx context.Context) {
	t.Helper()
	var users []User
	if err := db.Model(&User{}).Get(ctx, &users); err != nil {
		t.Fatalf("get: %v", err)
	}
}

// trip is a threshold every real query crosses; noTrip is one none of them will.
const (
	trip   = time.Nanosecond
	noTrip = time.Hour
)

// TestStats_AccumulatesAcrossScope: the point of the feature — no single query is
// slow, but the request's total is. Stats sees the sum, not the last statement.
func TestStats_AccumulatesAcrossScope(t *testing.T) {
	db := setup(t)
	ctx := playsql.TrackQueryTime(context.Background())

	for range 3 {
		getAll(t, db, ctx)
	}

	s := playsql.Stats(ctx)
	if s.Count != 3 {
		t.Errorf("Count = %d, want 3", s.Count)
	}
	if s.Total <= 0 {
		t.Errorf("Total = %v, want the sum of three queries", s.Total)
	}
	if s.Slowest.SQL == "" {
		t.Error("Slowest carries no event")
	}
	if s.Slowest.Duration > s.Total {
		t.Errorf("Slowest (%v) exceeds Total (%v)", s.Slowest.Duration, s.Total)
	}
}

// TestStats_UntrackedCtxAccumulatesNothing documents the sharp edge: a ctx that
// never went through TrackQueryTime is invisible to the scope machinery.
func TestStats_UntrackedCtxAccumulatesNothing(t *testing.T) {
	db := setup(t)
	ctx := context.Background()

	getAll(t, db, ctx)

	s := playsql.Stats(ctx)
	if s.Count != 0 || s.Total != 0 || s.Slowest.SQL != "" {
		t.Errorf("untracked ctx accumulated %+v, want the zero QueryStats", s)
	}
}

// TestWhenQueryingForLongerThan_FiresOncePerScope is Laravel's latch, keyed to
// the ctx instead of the connection: one alarm per request, however many queries
// go past the line.
func TestWhenQueryingForLongerThan_FiresOncePerScope(t *testing.T) {
	db := setup(t)

	var (
		mu    sync.Mutex
		fired []playsql.QueryStats
	)
	db.WhenQueryingForLongerThan(trip, func(_ playsql.QueryEvent, s playsql.QueryStats) {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, s)
	})

	ctx := playsql.TrackQueryTime(context.Background())
	for range 3 {
		getAll(t, db, ctx)
	}

	if len(fired) != 1 {
		t.Fatalf("handler fired %d times in one scope, want exactly 1", len(fired))
	}
	if fired[0].Count != 1 {
		t.Errorf("handler saw Count = %d, want 1: it should fire on the query that crossed", fired[0].Count)
	}

	// A fresh scope re-arms it. This is why there is no re-arm API.
	getAll(t, db, playsql.TrackQueryTime(context.Background()))
	if len(fired) != 2 {
		t.Errorf("handler fired %d times across two scopes, want 2", len(fired))
	}
}

// TestWhenQueryingForLongerThan_BelowThresholdStaysQuiet.
func TestWhenQueryingForLongerThan_BelowThresholdStaysQuiet(t *testing.T) {
	db := setup(t)

	fired := 0
	db.WhenQueryingForLongerThan(noTrip, func(playsql.QueryEvent, playsql.QueryStats) { fired++ })

	ctx := playsql.TrackQueryTime(context.Background())
	for range 3 {
		getAll(t, db, ctx)
	}

	if fired != 0 {
		t.Errorf("handler fired %d times without the scope reaching its threshold", fired)
	}
}

// TestWhenQueryingForLongerThan_UntrackedCtxDoesNotFire: the counterpart to the
// warning playsql logs once in this situation.
func TestWhenQueryingForLongerThan_UntrackedCtxDoesNotFire(t *testing.T) {
	db := setup(t)

	fired := 0
	db.WhenQueryingForLongerThan(trip, func(playsql.QueryEvent, playsql.QueryStats) { fired++ })

	getAll(t, db, context.Background())

	if fired != 0 {
		t.Errorf("handler fired %d times for a query outside any tracked scope", fired)
	}
}

// TestWhenQueryingForLongerThan_ThresholdsLatchIndependently: handlers do not
// mask each other, including two that share a threshold.
func TestWhenQueryingForLongerThan_ThresholdsLatchIndependently(t *testing.T) {
	db := setup(t)

	var low, alsoLow, high int
	db.WhenQueryingForLongerThan(trip, func(playsql.QueryEvent, playsql.QueryStats) { low++ })
	db.WhenQueryingForLongerThan(trip, func(playsql.QueryEvent, playsql.QueryStats) { alsoLow++ })
	db.WhenQueryingForLongerThan(noTrip, func(playsql.QueryEvent, playsql.QueryStats) { high++ })

	ctx := playsql.TrackQueryTime(context.Background())
	for range 2 {
		getAll(t, db, ctx)
	}

	if low != 1 || alsoLow != 1 {
		t.Errorf("handlers sharing a threshold fired %d and %d times, want 1 each", low, alsoLow)
	}
	if high != 0 {
		t.Errorf("the unreached handler fired %d times", high)
	}
}

// TestWhenQueryingForLongerThan_PanickingHandlerDoesNotKillTheQuery.
func TestWhenQueryingForLongerThan_PanickingHandlerDoesNotKillTheQuery(t *testing.T) {
	db := setup(t)

	db.WhenQueryingForLongerThan(trip, func(playsql.QueryEvent, playsql.QueryStats) {
		panic("handler is broken")
	})

	var users []User
	ctx := playsql.TrackQueryTime(context.Background())
	if err := db.Model(&User{}).Get(ctx, &users); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) == 0 {
		t.Fatal("query returned no rows; the panicking handler interfered")
	}
}

// TestStats_ScopeIsRaceClean: a request may fan its queries out across
// goroutines. Run under -race.
func TestStats_ScopeIsRaceClean(t *testing.T) {
	db := setup(t)
	ctx := playsql.TrackQueryTime(context.Background())

	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			var users []User
			if err := db.Model(&User{}).Get(ctx, &users); err != nil {
				t.Errorf("get: %v", err)
			}
		}()
	}
	wg.Wait()

	if s := playsql.Stats(ctx); s.Count != n {
		t.Errorf("Count = %d, want %d: concurrent queries in one scope lost updates", s.Count, n)
	}
}

// TestStats_TransactionCountsTowardTheEnclosingScope: a Tx runs on its own
// session, which must still inherit the scope machinery.
func TestStats_TransactionCountsTowardTheEnclosingScope(t *testing.T) {
	db := setup(t)
	ctx := playsql.TrackQueryTime(context.Background())

	err := db.Tx(ctx, func(tx *playsql.Tx) error {
		_, err := tx.Model(&User{}).WhereEq("id", int64(1)).Update(ctx, map[string]any{"age": int64(50)})
		return err
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}

	if s := playsql.Stats(ctx); s.Count != 1 {
		t.Errorf("Count = %d, want 1: the statement inside DB.Tx did not reach the scope", s.Count)
	}
}

// TestLifetimeCounters: the process-wide view, which needs no tracked ctx.
func TestLifetimeCounters(t *testing.T) {
	db := setup(t) // setup runs a CREATE and an INSERT of its own
	db.ResetQueryStats()

	ctx := context.Background()
	for range 3 {
		getAll(t, db, ctx)
	}

	if got := db.QueryCount(); got != 3 {
		t.Errorf("QueryCount = %d, want 3", got)
	}
	if got := db.TotalQueryDuration(); got <= 0 {
		t.Errorf("TotalQueryDuration = %v, want a positive total", got)
	}

	db.ResetQueryStats()
	if got := db.QueryCount(); got != 0 {
		t.Errorf("QueryCount = %d after reset, want 0", got)
	}
	if got := db.TotalQueryDuration(); got != 0 {
		t.Errorf("TotalQueryDuration = %v after reset, want 0", got)
	}
}
