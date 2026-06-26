package playsql

import "context"

// Scope is an auto-applied query constraint (multi-tenancy, active-only, …).
// Apply adds predicates to b; returning an error aborts the query — e.g. a
// tenancy scope with no tenant in ctx should fail rather than leak all rows.
type Scope interface {
	Apply(ctx context.Context, b *Builder) error
}

// Scoper is implemented by a model to declare its global scopes. The scopes are
// static (a property of the type); each reads request-scoped values from ctx at
// Apply time.
//
//	func (Post) Scopes() []playsql.Scope { return []playsql.Scope{TenantScope{}} }
type Scoper interface {
	Scopes() []Scope
}

// ScopeFunc adapts a plain function to a Scope.
type ScopeFunc func(ctx context.Context, b *Builder) error

func (f ScopeFunc) Apply(ctx context.Context, b *Builder) error { return f(ctx, b) }
