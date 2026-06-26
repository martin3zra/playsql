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

// Cursor describes a keyset (cursor) page: order by Column and return up to
// Limit rows after the After value. For stable paging Column should be unique
// and monotonic (e.g. the primary key).
type Cursor struct {
	Column string
	After  any // exclusive lower (or upper, when Desc) bound; nil starts from the beginning
	Limit  int
	Desc   bool // descending order
}

// CursorResult is the metadata for a keyset page.
type CursorResult struct {
	HasMore    bool
	NextCursor any // pass as the next Cursor.After; nil when HasMore is false
}

// CursorPaginate fetches a keyset page into dest (a *[]T or *[]*T) and returns
// the cursor metadata. It is seek-based (WHERE Column > After ... LIMIT n), so
// it stays fast at any depth — unlike offset pagination.
func (b *Builder) CursorPaginate(ctx context.Context, dest any, c Cursor) (CursorResult, error) {
	if b.err != nil {
		return CursorResult{}, b.err
	}
	if c.Limit < 1 {
		return CursorResult{}, fmt.Errorf("playsql: CursorPaginate requires Limit >= 1")
	}

	op, dir := ">", Asc
	if c.Desc {
		op, dir = "<", Desc
	}
	if c.After != nil {
		b.Where(c.Column, op, c.After)
	}
	b.OrderBy(c.Column, dir).Limit(c.Limit + 1) // one extra row to detect more

	if err := b.Get(ctx, dest); err != nil {
		return CursorResult{}, err
	}

	slice := reflect.ValueOf(dest).Elem()
	n := slice.Len()
	res := CursorResult{HasMore: n > c.Limit}
	if res.HasMore {
		slice.Set(slice.Slice(0, c.Limit)) // drop the probe row
		last := slice.Index(c.Limit - 1)
		if last.Kind() == reflect.Ptr {
			last = last.Elem()
		}
		if idx, ok := b.meta.FieldIndexByColumn(c.Column); ok {
			res.NextCursor = last.Field(idx).Interface()
		}
	}
	return res, nil
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
