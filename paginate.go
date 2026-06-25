package playsql

import (
	"context"
	"fmt"
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
