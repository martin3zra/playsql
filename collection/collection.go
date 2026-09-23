// Package collection provides a small, generic, Laravel-inspired collection
// type built on top of the standard slices, maps and iter packages.
//
// Design rules:
//   - Operations that keep the element type are chainable methods.
//   - Operations that change the element type (Map, GroupBy, Sum, ...) are
//     package functions, because Go methods cannot declare type parameters.
//   - Nothing mutates the receiver; every transformation returns a new value.
//   - Lookups return (value, ok) instead of a nil that can't be told apart
//     from a real value.
//
// Requires Go 1.23+ (range-over-func iterators).
package collection

import (
	"encoding/json"
	"iter"
	"slices"
)

// Collection is a slice with helper methods. Because it is a named slice,
// range, len, indexing and append all work on it directly.
type Collection[T any] []T

// Collect wraps an existing slice without copying it.
func Collect[T any](items []T) Collection[T] {
	return Collection[T](items)
}

// Of builds a collection from literal values: collection.Of(1, 2, 3).
func Of[T any](items ...T) Collection[T] {
	return Collection[T](items)
}

// Times calls fn n times with 1..n and collects the results.
func Times[T any](n int, fn func(number int) T) Collection[T] {
	if n < 1 {
		return Collection[T]{}
	}
	out := make(Collection[T], n)
	for i := range n {
		out[i] = fn(i + 1)
	}
	return out
}

// Range returns the integers from start to end, inclusive.
func Range(start, end int) Collection[int] {
	if end < start {
		return Collection[int]{}
	}
	out := make(Collection[int], 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, i)
	}
	return out
}

// ---------------------------------------------------------------------------
// Inspection
// ---------------------------------------------------------------------------

// All returns the underlying slice.
func (c Collection[T]) All() []T { return []T(c) }

func (c Collection[T]) Count() int       { return len(c) }
func (c Collection[T]) IsEmpty() bool    { return len(c) == 0 }
func (c Collection[T]) IsNotEmpty() bool { return len(c) > 0 }

// Get returns the item at index i. Negative indexes count from the end.
func (c Collection[T]) Get(i int) (T, bool) {
	if i < 0 {
		i += len(c)
	}
	if i < 0 || i >= len(c) {
		var zero T
		return zero, false
	}
	return c[i], true
}

func (c Collection[T]) First() (T, bool) { return c.Get(0) }
func (c Collection[T]) Last() (T, bool)  { return c.Get(-1) }

// FirstWhere returns the first item for which fn is true.
func (c Collection[T]) FirstWhere(fn func(T) bool) (T, bool) {
	if i := slices.IndexFunc(c, fn); i >= 0 {
		return c[i], true
	}
	var zero T
	return zero, false
}

// Contains reports whether any item satisfies fn.
func (c Collection[T]) Contains(fn func(T) bool) bool {
	return slices.ContainsFunc(c, fn)
}

// Every reports whether all items satisfy fn (true for an empty collection).
func (c Collection[T]) Every(fn func(T) bool) bool {
	for _, v := range c {
		if !fn(v) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Iteration
// ---------------------------------------------------------------------------

// Each calls fn for every item with its index.
func (c Collection[T]) Each(fn func(index int, item T)) {
	for i, v := range c {
		fn(i, v)
	}
}

// Values returns a lazy iterator over the items, for use with the *Seq
// helpers or a plain for-range loop.
func (c Collection[T]) Values() iter.Seq[T] { return slices.Values(c) }

// ---------------------------------------------------------------------------
// Same-type transformations (chainable)
// ---------------------------------------------------------------------------

// Filter keeps the items for which fn is true.
func (c Collection[T]) Filter(fn func(T) bool) Collection[T] {
	out := make(Collection[T], 0, len(c))
	for _, v := range c {
		if fn(v) {
			out = append(out, v)
		}
	}
	return out
}

// Reject drops the items for which fn is true.
func (c Collection[T]) Reject(fn func(T) bool) Collection[T] {
	return c.Filter(func(v T) bool { return !fn(v) })
}

// Take returns the first n items, or the last -n items when n is negative.
func (c Collection[T]) Take(n int) Collection[T] {
	if n < 0 {
		n = max(len(c)+n, 0)
		return slices.Clone(c[n:])
	}
	return slices.Clone(c[:min(n, len(c))])
}

// Skip drops the first n items.
func (c Collection[T]) Skip(n int) Collection[T] {
	return slices.Clone(c[min(max(n, 0), len(c)):])
}

// SortBy returns a stably sorted copy. cmp follows the slices.SortFunc
// convention: negative when a < b, zero when equal, positive when a > b.
// Use cmp.Compare for simple fields: func(a, b E) int { return cmp.Compare(a.Name, b.Name) }.
func (c Collection[T]) SortBy(cmp func(a, b T) int) Collection[T] {
	out := slices.Clone(c)
	slices.SortStableFunc(out, cmp)
	return out
}

// Reverse returns a reversed copy.
func (c Collection[T]) Reverse() Collection[T] {
	out := slices.Clone(c)
	slices.Reverse(out)
	return out
}

// Chunk splits the collection into groups of at most size items.
// It returns []Collection[T] rather than Collection[Collection[T]], because
// the latter is an instantiation cycle, which the compiler rejects.
func (c Collection[T]) Chunk(size int) []Collection[T] {
	if size < 1 || len(c) == 0 {
		return []Collection[T]{}
	}
	return slices.Collect(slices.Chunk(slices.Clone(c), size))
}

// MarshalJSON encodes an empty or nil collection as [] instead of null.
func (c Collection[T]) MarshalJSON() ([]byte, error) {
	if c == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]T(c))
}

// ---------------------------------------------------------------------------
// Type-changing transformations (package functions)
// ---------------------------------------------------------------------------

// Number is any built-in numeric type, including named types based on them.
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// Map transforms every item. Also covers Laravel's pluck:
// Map(employees, func(e Employee) string { return e.Name }).
func Map[T, U any](c Collection[T], fn func(T) U) Collection[U] {
	out := make(Collection[U], len(c))
	for i, v := range c {
		out[i] = fn(v)
	}
	return out
}

// Reduce folds the collection into a single value.
func Reduce[T, A any](c Collection[T], fn func(acc A, item T) A, initial A) A {
	acc := initial
	for _, v := range c {
		acc = fn(acc, v)
	}
	return acc
}

// Sum adds up the value returned by fn for each item. For money, sum integer
// cents or use Reduce with a decimal type; avoid float64.
func Sum[T any, N Number](c Collection[T], fn func(T) N) N {
	var total N
	for _, v := range c {
		total += fn(v)
	}
	return total
}

// GroupBy groups items by the key returned by fn, preserving order in groups.
func GroupBy[T any, K comparable](c Collection[T], fn func(T) K) map[K]Collection[T] {
	out := make(map[K]Collection[T])
	for _, v := range c {
		k := fn(v)
		out[k] = append(out[k], v)
	}
	return out
}

// KeyBy indexes items by the key returned by fn. Later items win on duplicates.
func KeyBy[T any, K comparable](c Collection[T], fn func(T) K) map[K]T {
	out := make(map[K]T, len(c))
	for _, v := range c {
		out[fn(v)] = v
	}
	return out
}

// UniqueBy keeps the first item for each key returned by fn.
func UniqueBy[T any, K comparable](c Collection[T], fn func(T) K) Collection[T] {
	seen := make(map[K]struct{}, len(c))
	out := make(Collection[T], 0, len(c))
	for _, v := range c {
		k := fn(v)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, v)
	}
	return out
}

// ---------------------------------------------------------------------------
// Lazy helpers (Laravel's LazyCollection equivalent)
// ---------------------------------------------------------------------------

// FilterSeq lazily keeps the items for which fn is true.
func FilterSeq[T any](seq iter.Seq[T], fn func(T) bool) iter.Seq[T] {
	return func(yield func(T) bool) {
		for v := range seq {
			if fn(v) && !yield(v) {
				return
			}
		}
	}
}

// MapSeq lazily transforms every item.
func MapSeq[T, U any](seq iter.Seq[T], fn func(T) U) iter.Seq[U] {
	return func(yield func(U) bool) {
		for v := range seq {
			if !yield(fn(v)) {
				return
			}
		}
	}
}

// TakeSeq lazily yields at most n items, then stops the source.
func TakeSeq[T any](seq iter.Seq[T], n int) iter.Seq[T] {
	return func(yield func(T) bool) {
		if n <= 0 {
			return
		}
		i := 0
		for v := range seq {
			if !yield(v) {
				return
			}
			if i++; i >= n {
				return
			}
		}
	}
}

// FromSeq materialises an iterator into a collection.
func FromSeq[T any](seq iter.Seq[T]) Collection[T] {
	return Collection[T](slices.Collect(seq))
}
