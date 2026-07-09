package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

func TestWhen_TrueAppliesPredicate(t *testing.T) {
	db := setup(t)

	// condition true -> WhereEq("age", 30) applied -> Jane, Ann
	var users []User
	err := db.Model(&User{}).
		When(true, func(q *playsql.Builder) { q.WhereEq("age", int64(30)) }).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(users), users)
	}
}

func TestWhen_FalseSkipsPredicate(t *testing.T) {
	db := setup(t)

	// condition false, no otherwise -> no filter -> all 3 rows
	var users []User
	err := db.Model(&User{}).
		When(false, func(q *playsql.Builder) { q.WhereEq("age", int64(30)) }).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("want 3, got %d: %+v", len(users), users)
	}
}

func TestUnless_FalseAppliesPredicate(t *testing.T) {
	db := setup(t)

	// showAll false -> predicate applied -> Jane, Ann
	var users []User
	err := db.Model(&User{}).
		Unless(false, func(q *playsql.Builder) { q.WhereEq("age", int64(30)) }).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(users), users)
	}
}

func TestUnless_TrueSkipsPredicate(t *testing.T) {
	db := setup(t)

	// showAll true -> predicate skipped -> all 3 rows
	var users []User
	err := db.Model(&User{}).
		Unless(true, func(q *playsql.Builder) { q.WhereEq("age", int64(30)) }).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("want 3, got %d: %+v", len(users), users)
	}
}

func TestUnless_TrueRunsOtherwise(t *testing.T) {
	db := setup(t)

	// showAll true -> otherwise runs -> Bob only
	var users []User
	err := db.Model(&User{}).
		Unless(true,
			func(q *playsql.Builder) { q.WhereEq("age", int64(30)) },
			func(q *playsql.Builder) { q.WhereEq("name", "Bob") }).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 1 || users[0].Name != "Bob" {
		t.Fatalf("want only Bob, got %+v", users)
	}
}

func TestWhen_FalseRunsOtherwise(t *testing.T) {
	db := setup(t)

	// condition false -> otherwise runs -> WhereEq("name","Bob") -> Bob only
	var users []User
	err := db.Model(&User{}).
		When(false,
			func(q *playsql.Builder) { q.WhereEq("age", int64(30)) },
			func(q *playsql.Builder) { q.WhereEq("name", "Bob") }).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 1 || users[0].Name != "Bob" {
		t.Fatalf("want only Bob, got %+v", users)
	}
}
