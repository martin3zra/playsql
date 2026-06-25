package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

func TestWhereIn_Slice(t *testing.T) {
	db := setup(t)

	var users []User
	// single-slice form
	err := db.Model(&User{}).WhereIn("id", []int64{1, 3}).Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2, got %d", len(users))
	}
}

func TestWhereIn_Variadic(t *testing.T) {
	db := setup(t)

	var users []User
	err := db.Model(&User{}).WhereIn("id", 2).Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 1 || users[0].Name != "Bob" {
		t.Fatalf("want only Bob, got %+v", users)
	}
}

func TestWhereBetween(t *testing.T) {
	db := setup(t)

	var users []User
	err := db.Model(&User{}).WhereBetween("age", 26, 31).Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// Jane(30) and Ann(30) are in [26,31]; Bob(25) is not.
	if len(users) != 2 {
		t.Fatalf("want 2 in [26,31], got %d: %+v", len(users), users)
	}
}

func TestNestedGroup_Integration(t *testing.T) {
	db := setup(t)

	// age = 30 AND ( name = 'Jane' OR name = 'Ann' ) -> Jane, Ann
	var users []User
	err := db.Model(&User{}).
		WhereEq("age", int64(30)).
		WhereGroup(func(q *playsql.Builder) {
			q.WhereEq("name", "Jane").OrWhere("name", "=", "Ann")
		}).
		Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(users), users)
	}
}

func TestEmptyIn_Integration(t *testing.T) {
	db := setup(t)

	var users []User
	err := db.Model(&User{}).WhereIn("id").Get(context.Background(), &users)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("empty IN should match nothing, got %d", len(users))
	}
}
