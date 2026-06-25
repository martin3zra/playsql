package playsql

import "context"

// Lifecycle hooks. A model may implement any subset; each is detected by an
// interface assertion and called at the matching point of a struct-based
// Insert/Update/Save/Delete. A Before* hook returning an error aborts the
// operation. Map-based and bulk writes (Builder.Insert/Update/InsertMany)
// bypass hooks, matching Eloquent's query-builder semantics.
//
// Ordering mirrors Eloquent: BeforeSave then BeforeCreate/BeforeUpdate around
// the write; AfterCreate/AfterUpdate then AfterSave after it.
type (
	BeforeSaveHook   interface{ BeforeSave(context.Context) error }
	AfterSaveHook    interface{ AfterSave(context.Context) error }
	BeforeCreateHook interface{ BeforeCreate(context.Context) error }
	AfterCreateHook  interface{ AfterCreate(context.Context) error }
	BeforeUpdateHook interface{ BeforeUpdate(context.Context) error }
	AfterUpdateHook  interface{ AfterUpdate(context.Context) error }
	BeforeDeleteHook interface{ BeforeDelete(context.Context) error }
	AfterDeleteHook  interface{ AfterDelete(context.Context) error }
)

func fireBeforeSave(ctx context.Context, m any) error {
	if h, ok := m.(BeforeSaveHook); ok {
		return h.BeforeSave(ctx)
	}
	return nil
}

func fireAfterSave(ctx context.Context, m any) error {
	if h, ok := m.(AfterSaveHook); ok {
		return h.AfterSave(ctx)
	}
	return nil
}

func fireBeforeCreate(ctx context.Context, m any) error {
	if h, ok := m.(BeforeCreateHook); ok {
		return h.BeforeCreate(ctx)
	}
	return nil
}

func fireAfterCreate(ctx context.Context, m any) error {
	if h, ok := m.(AfterCreateHook); ok {
		return h.AfterCreate(ctx)
	}
	return nil
}

func fireBeforeUpdate(ctx context.Context, m any) error {
	if h, ok := m.(BeforeUpdateHook); ok {
		return h.BeforeUpdate(ctx)
	}
	return nil
}

func fireAfterUpdate(ctx context.Context, m any) error {
	if h, ok := m.(AfterUpdateHook); ok {
		return h.AfterUpdate(ctx)
	}
	return nil
}

func fireBeforeDelete(ctx context.Context, m any) error {
	if h, ok := m.(BeforeDeleteHook); ok {
		return h.BeforeDelete(ctx)
	}
	return nil
}

func fireAfterDelete(ctx context.Context, m any) error {
	if h, ok := m.(AfterDeleteHook); ok {
		return h.AfterDelete(ctx)
	}
	return nil
}
