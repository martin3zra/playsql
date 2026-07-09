//go:build integration

package playsql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/martin3zra/playsql"
)

// runLockSuite proves the pessimistic-lock SQL is accepted by each live server.
// Unit tests only assert the strings we expect to emit; this asserts the server
// agrees — in particular that MySQL takes LOCK IN SHARE MODE *after* LIMIT, and
// that SQL Server's FROM-clause hint parses where we place it.
func runLockSuite(t *testing.T, db *playsql.DB, dialect string) {
	ctx := context.Background()

	seed := &itPerson{Name: "LockSubject", Age: 44}
	if err := db.Insert(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	defer db.Model(&itPerson{}).WhereEq("id", seed.ID).Delete(ctx)

	t.Run("lock_for_update", func(t *testing.T) {
		err := db.Tx(ctx, func(tx *playsql.Tx) error {
			var people []itPerson
			return tx.Model(&itPerson{}).WhereEq("id", seed.ID).
				LockForUpdate().Get(ctx, &people)
		})
		if err != nil {
			t.Fatalf("FOR UPDATE rejected by %s: %v", dialect, err)
		}
	})

	t.Run("shared_lock", func(t *testing.T) {
		err := db.Tx(ctx, func(tx *playsql.Tx) error {
			var people []itPerson
			return tx.Model(&itPerson{}).WhereEq("id", seed.ID).
				SharedLock().Get(ctx, &people)
		})
		if err != nil {
			t.Fatalf("shared lock rejected by %s: %v", dialect, err)
		}
	})

	// The clause ordering that unit tests cannot validate: the lock keyword must
	// trail LIMIT/OFFSET on MySQL and Postgres, and coexist with SQL Server's
	// OFFSET/FETCH paging.
	t.Run("lock_with_limit_offset", func(t *testing.T) {
		err := db.Tx(ctx, func(tx *playsql.Tx) error {
			var people []itPerson
			return tx.Model(&itPerson{}).OrderBy("id", playsql.Asc).
				Limit(2).Offset(0).LockForUpdate().Get(ctx, &people)
		})
		if err != nil {
			t.Fatalf("FOR UPDATE with LIMIT/OFFSET rejected by %s: %v", dialect, err)
		}
	})

	t.Run("shared_lock_with_limit", func(t *testing.T) {
		err := db.Tx(ctx, func(tx *playsql.Tx) error {
			var people []itPerson
			return tx.Model(&itPerson{}).OrderBy("id", playsql.Asc).
				Limit(2).SharedLock().Get(ctx, &people)
		})
		if err != nil {
			t.Fatalf("shared lock with LIMIT rejected by %s: %v", dialect, err)
		}
	})

	// First() sets Limit(1) internally, then appends the lock.
	t.Run("lock_with_first", func(t *testing.T) {
		err := db.Tx(ctx, func(tx *playsql.Tx) error {
			var p itPerson
			return tx.Model(&itPerson{}).WhereEq("id", seed.ID).
				LockForUpdate().First(ctx, &p)
		})
		if err != nil {
			t.Fatalf("FOR UPDATE with First rejected by %s: %v", dialect, err)
		}
	})

	// Postgres rejects FOR UPDATE alongside an aggregate; Count must strip it.
	t.Run("count_strips_lock", func(t *testing.T) {
		err := db.Tx(ctx, func(tx *playsql.Tx) error {
			n, err := tx.Model(&itPerson{}).LockForUpdate().Count(ctx)
			if err != nil {
				return err
			}
			if n < 1 {
				t.Errorf("want >=1 rows, got %d", n)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("locked Count rejected by %s: %v", dialect, err)
		}
	})

	t.Run("outside_tx_is_refused", func(t *testing.T) {
		var people []itPerson
		err := db.Model(&itPerson{}).LockForUpdate().Get(ctx, &people)
		if !errors.Is(err, playsql.ErrLockOutsideTx) {
			t.Fatalf("want ErrLockOutsideTx, got %v", err)
		}
	})
}
