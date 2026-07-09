package playsql

import "errors"

// ErrNotFound is returned by First and Find when no row matches.
var ErrNotFound = errors.New("playsql: no rows found")

// ErrNotSoftDeletable is returned by Restore on a model without a soft-delete column.
var ErrNotSoftDeletable = errors.New("playsql: model is not soft-deletable")

// ErrLockOutsideTx is returned when LockForUpdate or SharedLock is called on a
// query that is not running inside a transaction. Under autocommit the lock is
// released before the rows are scanned, so the call would silently do nothing.
var ErrLockOutsideTx = errors.New("playsql: pessimistic lock requires a transaction")
