package playsql

import (
	"context"
	"fmt"
	"reflect"
)

// Pagination holds the metadata for an offset-paginated result.
type Pagination struct {
	Total    int64 // total rows matching the query (ignoring limit/offset)
	Page     int   // 1-based current page
	PerPage  int   // rows per page
	LastPage int   // total number of pages (>= 1)
}

// HasMore reports whether a page after the current one exists.
func (p Pagination) HasMore() bool { return p.Page < p.LastPage }

// Paginate runs a COUNT for the current constraints, then fetches one page into
// dest (a *[]T or *[]*T) and returns the pagination metadata. page is 1-based.
func (b *Builder) Paginate(ctx context.Context, dest any, page, perPage int) (Pagination, error) {
	if b.err != nil {
		return Pagination{}, b.err
	}
	if perPage < 1 {
		return Pagination{}, fmt.Errorf("playsql: Paginate requires perPage >= 1")
	}
	if page < 1 {
		page = 1
	}

	total, err := b.Count(ctx)
	if err != nil {
		return Pagination{}, err
	}

	lastPage := 1
	if total > 0 {
		lastPage = int((total + int64(perPage) - 1) / int64(perPage))
	}

	if err := b.Limit(perPage).Offset((page-1)*perPage).Get(ctx, dest); err != nil {
		return Pagination{}, err
	}

	return Pagination{Total: total, Page: page, PerPage: perPage, LastPage: lastPage}, nil
}

// TypedPage is a paginated result for the generic API: the page items plus the
// pagination metadata.
type TypedPage[T any] struct {
	Pagination
	Items []T
}

// Paginate fetches one page of T and its metadata.
func (t *TypedBuilder[T]) Paginate(ctx context.Context, page, perPage int) (TypedPage[T], error) {
	var items []T
	p, err := t.b.Paginate(ctx, &items, page, perPage)
	return TypedPage[T]{Pagination: p, Items: items}, err
}

// CursorKey is one ordered key of a (possibly composite) keyset cursor.
type CursorKey struct {
	Column string
	Desc   bool
}

// Cursor describes a keyset (cursor) page. Order by the key columns and return
// up to Limit rows after the cursor position.
//
// Single key (the common case): set Column; After is the scalar key value.
//
//	playsql.Cursor{Column: "id", After: lastID, Limit: 20}
//
// Composite (a non-unique sort key plus a unique tiebreaker, to avoid skipping
// rows on ties): set Keys; After is a []any aligned to Keys.
//
//	playsql.Cursor{
//	    Keys:  []playsql.CursorKey{{Column: "created_at"}, {Column: "id"}},
//	    After: []any{lastCreatedAt, lastID},
//	    Limit: 20,
//	}
type Cursor struct {
	Column string // single-key form
	Desc   bool   // single-key direction

	Keys []CursorKey // composite form (overrides Column when set)

	After any // scalar (single key) or []any aligned to Keys (composite); nil starts at the beginning
	Limit int
}

// keys returns the effective ordered keys.
func (c Cursor) keys() ([]CursorKey, error) {
	if len(c.Keys) > 0 {
		return c.Keys, nil
	}
	if c.Column != "" {
		return []CursorKey{{Column: c.Column, Desc: c.Desc}}, nil
	}
	return nil, fmt.Errorf("playsql: Cursor requires Column or Keys")
}

// afterValues returns the cursor values aligned to keys, or nil for the first page.
func (c Cursor) afterValues(keys []CursorKey) ([]any, error) {
	if c.After == nil {
		return nil, nil
	}
	if len(c.Keys) > 0 {
		vals, ok := c.After.([]any)
		if !ok {
			return nil, fmt.Errorf("playsql: composite Cursor needs After of type []any")
		}
		if len(vals) != len(keys) {
			return nil, fmt.Errorf("playsql: Cursor After has %d values, want %d", len(vals), len(keys))
		}
		return vals, nil
	}
	return []any{c.After}, nil
}

// CursorResult is the metadata for a keyset page.
type CursorResult struct {
	HasMore bool
	// NextCursor is the value to pass as the next Cursor.After: a scalar for a
	// single-key cursor, a []any for a composite one. nil when HasMore is false.
	NextCursor any
}

// CursorPaginate fetches a keyset page into dest (a *[]T or *[]*T) and returns
// the cursor metadata. It is seek-based (WHERE <keys> > <after> ... LIMIT n), so
// it stays fast at any depth — unlike offset pagination. A composite cursor adds
// a tiebreaker key so rows sharing a non-unique sort value are not skipped.
func (b *Builder) CursorPaginate(ctx context.Context, dest any, c Cursor) (CursorResult, error) {
	if b.err != nil {
		return CursorResult{}, b.err
	}
	if c.Limit < 1 {
		return CursorResult{}, fmt.Errorf("playsql: CursorPaginate requires Limit >= 1")
	}
	keys, err := c.keys()
	if err != nil {
		return CursorResult{}, err
	}
	after, err := c.afterValues(keys)
	if err != nil {
		return CursorResult{}, err
	}

	if after != nil {
		b.applyCursorSeek(keys, after)
	}
	for _, k := range keys {
		dir := Asc
		if k.Desc {
			dir = Desc
		}
		b.OrderBy(k.Column, dir)
	}
	b.Limit(c.Limit + 1) // one extra row to detect more

	if err := b.Get(ctx, dest); err != nil {
		return CursorResult{}, err
	}

	slice := reflect.ValueOf(dest).Elem()
	res := CursorResult{HasMore: slice.Len() > c.Limit}
	if res.HasMore {
		slice.Set(slice.Slice(0, c.Limit)) // drop the probe row
		last := slice.Index(c.Limit - 1)
		if last.Kind() == reflect.Ptr {
			last = last.Elem()
		}
		res.NextCursor = b.cursorValues(last, keys, len(c.Keys) > 0)
	}
	return res, nil
}

// applyCursorSeek adds the lexicographic seek predicate for the keys:
//
//	(k0 op0 a0)
//	OR (k0 = a0 AND k1 op1 a1)
//	OR (k0 = a0 AND k1 = a1 AND k2 op2 a2) ...
//
// built from group/where primitives so it is dialect-portable.
func (b *Builder) applyCursorSeek(keys []CursorKey, after []any) {
	op := func(k CursorKey) string {
		if k.Desc {
			return "<"
		}
		return ">"
	}

	if len(keys) == 1 {
		b.Where(keys[0].Column, op(keys[0]), after[0])
		return
	}

	b.WhereGroup(func(q *Builder) {
		for i := range keys {
			level := func(s *Builder) {
				for j := 0; j < i; j++ {
					s.WhereEq(keys[j].Column, after[j]) // equal on the higher keys
				}
				s.Where(keys[i].Column, op(keys[i]), after[i]) // strict on this key
			}
			if i == 0 {
				q.WhereGroup(level)
			} else {
				q.OrWhereGroup(level)
			}
		}
	})
}

// cursorValues reads the key columns from the last row of a page. Returns a
// scalar for a single-key cursor, a []any for a composite one.
func (b *Builder) cursorValues(last reflect.Value, keys []CursorKey, composite bool) any {
	vals := make([]any, len(keys))
	for i, k := range keys {
		if idx, ok := b.meta.FieldIndexByColumn(k.Column); ok {
			vals[i] = last.Field(idx).Interface()
		}
	}
	if composite {
		return vals
	}
	return vals[0]
}

// CursorPage is a typed keyset page: the items plus the cursor metadata.
type CursorPage[T any] struct {
	CursorResult
	Items []T
}

// CursorPaginate fetches a keyset page of T.
func (t *TypedBuilder[T]) CursorPaginate(ctx context.Context, c Cursor) (CursorPage[T], error) {
	var items []T
	res, err := t.b.CursorPaginate(ctx, &items, c)
	return CursorPage[T]{CursorResult: res, Items: items}, err
}
