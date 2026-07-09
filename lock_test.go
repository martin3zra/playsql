package playsql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/martin3zra/playsql"
)

func TestLockForUpdate_OutsideTxFails(t *testing.T) {
	db := setup(t)

	var users []User
	err := db.Model(&User{}).LockForUpdate().Get(context.Background(), &users)
	if !errors.Is(err, playsql.ErrLockOutsideTx) {
		t.Fatalf("want ErrLockOutsideTx, got %v", err)
	}
}

func TestSharedLock_OutsideTxFails(t *testing.T) {
	db := setup(t)

	var users []User
	err := db.Model(&User{}).SharedLock().Get(context.Background(), &users)
	if !errors.Is(err, playsql.ErrLockOutsideTx) {
		t.Fatalf("want ErrLockOutsideTx, got %v", err)
	}
}

// The error surfaces at the terminal op, not at the call, so the chain stays
// usable and a later Where does not panic on a failed builder.
func TestLockForUpdate_ErrorSurfacesAtTerminalOp(t *testing.T) {
	db := setup(t)

	q := db.Model(&User{}).LockForUpdate().WhereEq("age", int64(30)).Limit(1)

	var users []User
	if err := q.Get(context.Background(), &users); !errors.Is(err, playsql.ErrLockOutsideTx) {
		t.Fatalf("want ErrLockOutsideTx, got %v", err)
	}
}

// Inside a transaction the call is accepted. SQLite drops the clause, so this
// asserts the query still executes and scans correctly.
func TestLockForUpdate_InsideTxRuns(t *testing.T) {
	db := setup(t)

	err := db.Tx(context.Background(), func(tx *playsql.Tx) error {
		var users []User
		if err := tx.Model(&User{}).WhereEq("age", int64(30)).
			LockForUpdate().Get(context.Background(), &users); err != nil {
			return err
		}
		if len(users) != 2 {
			t.Errorf("want 2 locked rows, got %d: %+v", len(users), users)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
}

func TestSharedLock_InsideTxRuns(t *testing.T) {
	db := setup(t)

	err := db.Tx(context.Background(), func(tx *playsql.Tx) error {
		var users []User
		return tx.Model(&User{}).SharedLock().Get(context.Background(), &users)
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
}

// Count reuses the builder's compiled query; the lock must be stripped, since
// Postgres rejects FOR UPDATE alongside an aggregate.
func TestLockForUpdate_CountStripsLock(t *testing.T) {
	db := setup(t)

	err := db.Tx(context.Background(), func(tx *playsql.Tx) error {
		n, err := tx.Model(&User{}).LockForUpdate().Count(context.Background())
		if err != nil {
			return err
		}
		if n != 3 {
			t.Errorf("want 3, got %d", n)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
}
