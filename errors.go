package playsql

import "errors"

// ErrNotFound is returned by First and Find when no row matches.
var ErrNotFound = errors.New("playsql: no rows found")

// ErrNotSoftDeletable is returned by Restore on a model without a soft-delete column.
var ErrNotSoftDeletable = errors.New("playsql: model is not soft-deletable")
