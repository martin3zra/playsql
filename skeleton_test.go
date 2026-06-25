package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, no CGO)
)

// User exercises the static-config model: table via TableName(), columns via
// the `db` tag, primary key via play:"pk".
type User struct {
	ID   int64  `db:"id" play:"pk,incrementing"`
	Name string `db:"name"`
	Age  int64  `db:"age"`
}

func (User) TableName() string { return "users" }

func setup(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	seed := `INSERT INTO users (id, name, age) VALUES (1,'Jane',30),(2,'Bob',25),(3,'Ann',30)`
	if _, err := db.Exec(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// TestWalkingSkeleton proves the full pipeline end-to-end:
// metadata -> builder -> grammar -> execution -> scanner.
func TestWalkingSkeleton(t *testing.T) {
	db := setup(t)
	ctx := context.Background()

	var users []User
	if err := db.Model(&User{}).WhereEq("age", int64(30)).Get(ctx, &users); err != nil {
		t.Fatalf("get: %v", err)
	}

	if len(users) != 2 {
		t.Fatalf("want 2 users aged 30, got %d: %+v", len(users), users)
	}
	for _, u := range users {
		if u.Age != 30 {
			t.Errorf("scanned wrong age: %+v", u)
		}
		if u.Name == "" || u.ID == 0 {
			t.Errorf("fields not hydrated: %+v", u)
		}
	}
}

// TestGetAll covers the no-where path (SELECT all columns).
func TestGetAll(t *testing.T) {
	db := setup(t)

	var users []*User
	if err := db.Model(&User{}).Get(context.Background(), &users); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("want 3 users, got %d", len(users))
	}
}
