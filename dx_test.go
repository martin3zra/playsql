package playsql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/martin3zra/playsql"
)

func TestTap_ExecutesCallbackOnce(t *testing.T) {
	db := setup(t)
	calls := 0
	playsql.Query[User](db).Tap(func(b *playsql.TypedBuilder[User]) {
		calls++
		b.WhereEq("age", int64(30))
	})
	if calls != 1 {
		t.Fatalf("Tap should invoke callback once, got %d", calls)
	}
}

func TestScope_AppliesScopesInOrder(t *testing.T) {
	db := setup(t)

	var order []string
	young := func(q *playsql.TypedBuilder[User]) {
		order = append(order, "age")
		q.WhereEq("age", int64(30))
	}
	named := func(q *playsql.TypedBuilder[User]) {
		order = append(order, "name")
		q.WhereEq("name", "Jane")
	}

	q := playsql.Query[User](db).Scope(young, named)
	if len(order) != 2 || order[0] != "age" || order[1] != "name" {
		t.Fatalf("scopes should apply in order [age name], got %v", order)
	}

	sql := q.SQL()
	if !strings.Contains(sql, "age") || !strings.Contains(sql, "name") {
		t.Fatalf("both scope predicates should be in SQL: %s", sql)
	}

	users, err := q.Get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 1 || users[0].Name != "Jane" {
		t.Fatalf("scoped query want [Jane], got %+v", users)
	}
}

func TestSQL_ReturnsSQLWithoutExecuting(t *testing.T) {
	db := setup(t)
	q := playsql.Query[User](db).WhereEq("age", int64(30))

	sql := q.SQL()
	if !strings.Contains(sql, "SELECT") || !strings.Contains(sql, "WHERE") {
		t.Fatalf("SQL() should render a SELECT ... WHERE, got %q", sql)
	}

	// SQL() must not consume or run the builder: it stays executable.
	users, err := q.Get(context.Background())
	if err != nil {
		t.Fatalf("get after SQL(): %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2 rows aged 30, got %d", len(users))
	}
}

func TestArgs_ReturnsBindings(t *testing.T) {
	db := setup(t)
	args := playsql.Query[User](db).
		WhereEq("age", int64(30)).
		WhereEq("name", "Jane").
		Args()

	if len(args) != 2 {
		t.Fatalf("want 2 bindings, got %d: %v", len(args), args)
	}
	if args[0] != int64(30) || args[1] != "Jane" {
		t.Fatalf("bindings out of order: %v", args)
	}
}

func TestDD_PanicsWithDumpAndDie(t *testing.T) {
	db := setup(t)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("DD() should panic")
		}
		dd, ok := r.(playsql.DumpAndDie)
		if !ok {
			t.Fatalf("want DumpAndDie panic, got %T: %v", r, r)
		}
		// DD must dump SQL, bindings, model and table before dying.
		for _, want := range []string{"SELECT", "7", "User", "users"} {
			if !strings.Contains(dd.Dump, want) {
				t.Fatalf("dump missing %q:\n%s", want, dd.Dump)
			}
		}
	}()

	playsql.Query[User](db).WhereEq("age", int64(7)).DD().Get(context.Background())
	t.Fatal("unreachable: DD() should have panicked before Get ran")
}

func TestDD_DoesNotExecuteQuery(t *testing.T) {
	db := setup(t)
	// A malformed column would error at the DB — DD must dump-and-die before the
	// statement is ever sent, so no DB error surfaces, only the panic.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected DumpAndDie panic")
			} else if _, ok := r.(playsql.DumpAndDie); !ok {
				t.Fatalf("query reached the DB (got %v) instead of dumping", r)
			}
		}()
		playsql.Query[User](db).WhereRaw("this_is_not_valid_sql").DD().Get(context.Background())
	}()
}
