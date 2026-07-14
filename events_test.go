package playsql_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/martin3zra/playsql"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, no CGO)
)

// recorder collects the events a DB emits. Queries may run from several
// goroutines, so appends are guarded.
type recorder struct {
	mu     sync.Mutex
	events []playsql.QueryEvent
}

func (r *recorder) listen(e playsql.QueryEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) all() []playsql.QueryEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]playsql.QueryEvent(nil), r.events...)
}

func (r *recorder) len() int { return len(r.all()) }

// matching returns the events whose SQL contains substr, case-insensitively.
func (r *recorder) matching(substr string) []playsql.QueryEvent {
	var out []playsql.QueryEvent
	for _, e := range r.all() {
		if strings.Contains(strings.ToUpper(e.SQL), strings.ToUpper(substr)) {
			out = append(out, e)
		}
	}
	return out
}

// listenTo attaches a recorder to db and returns it.
func listenTo(db *playsql.DB) *recorder {
	rec := &recorder{}
	db.Listen(rec.listen)
	return rec
}

// TestListen_FiresForEveryStatementKind proves the listener sits at the one seam
// every statement crosses: builder reads and writes, struct persistence, and raw
// queries all emit, without a single call site knowing about events.
func TestListen_FiresForEveryStatementKind(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	rec := listenTo(db)

	var users []User
	if err := db.Model(&User{}).Get(ctx, &users); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := db.Model(&User{}).WhereEq("id", int64(1)).Update(ctx, map[string]any{"age": int64(31)}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := db.Model(&User{}).WhereEq("id", int64(2)).Delete(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := db.Insert(ctx, &User{Name: "Zoe", Age: 41}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var raw []User
	if err := db.Raw(ctx, &raw, `SELECT id, name, age FROM users`); err != nil {
		t.Fatalf("raw: %v", err)
	}

	for _, want := range []string{"SELECT", "UPDATE", "DELETE", "INSERT"} {
		if len(rec.matching(want)) == 0 {
			t.Errorf("no event emitted for a %s statement", want)
		}
	}
	if got := len(rec.matching("SELECT")); got != 2 {
		t.Errorf("want 2 SELECT events (builder + raw), got %d", got)
	}
}

// TestListen_EventPayload checks the fields a listener actually reads.
func TestListen_EventPayload(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	rec := listenTo(db)

	var users []User
	if err := db.Model(&User{}).WhereEq("age", int64(30)).Get(ctx, &users); err != nil {
		t.Fatalf("get: %v", err)
	}

	if rec.len() != 1 {
		t.Fatalf("want 1 event, got %d", rec.len())
	}
	e := rec.all()[0]

	if e.Op != playsql.OpQuery {
		t.Errorf("Op = %q, want %q", e.Op, playsql.OpQuery)
	}
	if !strings.Contains(e.SQL, "users") {
		t.Errorf("SQL does not name the table: %q", e.SQL)
	}
	if len(e.Args) != 1 || e.Args[0] != int64(30) {
		t.Errorf("Args = %v, want [30]", e.Args)
	}
	if e.Err != nil {
		t.Errorf("Err = %v, want nil on a query that succeeded", e.Err)
	}
	if e.InTx {
		t.Error("InTx is true for a statement run on the connection")
	}
	if e.Ctx == nil {
		t.Error("Ctx not carried on the event")
	}
	if e.Duration <= 0 {
		t.Errorf("Duration = %v, want a positive measurement", e.Duration)
	}
}

// TestListen_CarriesError is the reason QueryEvent has an Err field at all:
// Go has no exception to observe, so a failed statement would otherwise be
// indistinguishable from a fast one.
func TestListen_CarriesError(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	rec := listenTo(db)

	var users []User
	if err := db.Raw(ctx, &users, `SELECT * FROM no_such_table`); err == nil {
		t.Fatal("want an error from a query against a missing table")
	}

	if rec.len() != 1 {
		t.Fatalf("want 1 event, got %d", rec.len())
	}
	if e := rec.all()[0]; e.Err == nil {
		t.Error("Err is nil on an event for a statement that failed")
	}
}

// TestListen_InsideTransaction proves a Tx session inherits the connection's
// listeners and marks its events InTx.
func TestListen_InsideTransaction(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	rec := listenTo(db)

	err := db.Tx(ctx, func(tx *playsql.Tx) error {
		_, err := tx.Model(&User{}).WhereEq("id", int64(1)).Update(ctx, map[string]any{"age": int64(99)})
		return err
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}

	updates := rec.matching("UPDATE")
	if len(updates) != 1 {
		t.Fatalf("want 1 UPDATE event from inside the tx, got %d", len(updates))
	}
	if !updates[0].InTx {
		t.Error("InTx is false for a statement run inside DB.Tx")
	}
}

// TestListen_PanickingListenerDoesNotKillTheQuery: a listener is diagnostics, and
// a broken one must not take down the request it was watching.
func TestListen_PanickingListenerDoesNotKillTheQuery(t *testing.T) {
	db := setup(t)
	ctx := context.Background()

	db.Listen(func(playsql.QueryEvent) { panic("listener is broken") })
	rec := listenTo(db) // registered after the panicking one; must still run

	var users []User
	if err := db.Model(&User{}).Get(ctx, &users); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) == 0 {
		t.Fatal("query returned no rows; the panicking listener interfered")
	}
	if rec.len() != 1 {
		t.Errorf("want the surviving listener to see 1 event, got %d", rec.len())
	}
}

// TestListen_DDEmitsNothing: DD halts before the statement reaches the database,
// and QueryEvent describes statements that ran.
func TestListen_DDEmitsNothing(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	rec := listenTo(db)

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected DumpAndDie panic")
			} else if _, ok := r.(playsql.DumpAndDie); !ok {
				t.Fatalf("want DumpAndDie, got %T", r)
			}
		}()
		var users []User
		_ = db.Model(&User{}).DD().Get(ctx, &users)
	}()

	if rec.len() != 0 {
		t.Errorf("DD emitted %d events; it must emit none", rec.len())
	}
}

// TestListen_DebugStillEmits: Debug stacks its runner on top of the listenRunner
// rather than replacing it, so a debugged query is still observed.
func TestListen_DebugStillEmits(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	rec := listenTo(db)

	var users []User
	if err := db.Model(&User{}).Debug().Get(ctx, &users); err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.len() != 1 {
		t.Errorf("want 1 event from a Debug()'d query, got %d", rec.len())
	}
}
