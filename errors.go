package playsql

import "errors"

// ErrNotFound is returned by First and Find when no row matches.
var ErrNotFound = errors.New("playsql: no rows found")
