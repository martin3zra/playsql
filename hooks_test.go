package playsql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/martin3zra/playsql"
)

// Widget records which hooks fired, and can be made to abort.
type Widget struct {
	ID    int64  `db:"id" play:"pk,incrementing"`
	Name  string `db:"name"`
	fired *[]string
	fail  string // hook name that should return an error
}

func (w *Widget) note(name string) error {
	if w.fired != nil {
		*w.fired = append(*w.fired, name)
	}
	if w.fail == name {
		return errors.New("aborted by " + name)
	}
	return nil
}

func (w *Widget) BeforeSave(context.Context) error   { return w.note("BeforeSave") }
func (w *Widget) AfterSave(context.Context) error    { return w.note("AfterSave") }
func (w *Widget) BeforeCreate(context.Context) error { return w.note("BeforeCreate") }
func (w *Widget) AfterCreate(context.Context) error  { return w.note("AfterCreate") }
func (w *Widget) BeforeUpdate(context.Context) error { return w.note("BeforeUpdate") }
func (w *Widget) AfterUpdate(context.Context) error  { return w.note("AfterUpdate") }
func (w *Widget) BeforeDelete(context.Context) error { return w.note("BeforeDelete") }
func (w *Widget) AfterDelete(context.Context) error  { return w.note("AfterDelete") }
func (Widget) TableName() string                     { return "widgets" }

func setupWidgets(t *testing.T) *playsql.DB {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(context.Background(), `CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	return db
}

func eqSeq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestHooks_CreateOrder(t *testing.T) {
	db := setupWidgets(t)
	var fired []string

	w := &Widget{Name: "x", fired: &fired}
	if err := db.Insert(context.Background(), w); err != nil {
		t.Fatalf("insert: %v", err)
	}
	want := []string{"BeforeSave", "BeforeCreate", "AfterCreate", "AfterSave"}
	if !eqSeq(fired, want) {
		t.Fatalf("create hook order = %v, want %v", fired, want)
	}
}

func TestHooks_UpdateOrder(t *testing.T) {
	db := setupWidgets(t)
	ctx := context.Background()

	w := &Widget{Name: "x"}
	if err := db.Insert(ctx, w); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var fired []string
	w.fired = &fired
	w.Name = "y"
	if err := db.Update(ctx, w); err != nil {
		t.Fatalf("update: %v", err)
	}
	want := []string{"BeforeSave", "BeforeUpdate", "AfterUpdate", "AfterSave"}
	if !eqSeq(fired, want) {
		t.Fatalf("update hook order = %v, want %v", fired, want)
	}
}

func TestHooks_DeleteOrder(t *testing.T) {
	db := setupWidgets(t)
	ctx := context.Background()

	w := &Widget{Name: "x"}
	if err := db.Insert(ctx, w); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var fired []string
	w.fired = &fired
	if err := db.Delete(ctx, w); err != nil {
		t.Fatalf("delete: %v", err)
	}
	want := []string{"BeforeDelete", "AfterDelete"}
	if !eqSeq(fired, want) {
		t.Fatalf("delete hook order = %v, want %v", fired, want)
	}
}

func TestHooks_BeforeCreateAborts(t *testing.T) {
	db := setupWidgets(t)
	ctx := context.Background()

	w := &Widget{Name: "x", fail: "BeforeCreate"}
	err := db.Insert(ctx, w)
	if err == nil {
		t.Fatal("BeforeCreate error should abort insert")
	}
	// No row should have been written.
	n, _ := db.Model(&Widget{}).Count(ctx)
	if n != 0 {
		t.Fatalf("insert should have been aborted, count=%d", n)
	}
}

func TestHooks_SaveRoutesToCreateThenUpdate(t *testing.T) {
	db := setupWidgets(t)
	ctx := context.Background()

	var fired []string
	w := &Widget{Name: "x", fired: &fired}

	// First save -> create (no embedded Model, so zero-key heuristic inserts).
	if err := db.Save(ctx, w); err != nil {
		t.Fatalf("save create: %v", err)
	}
	if !eqSeq(fired, []string{"BeforeSave", "BeforeCreate", "AfterCreate", "AfterSave"}) {
		t.Fatalf("save-create fired %v", fired)
	}

	fired = nil
	w.Name = "y"
	if err := db.Save(ctx, w); err != nil {
		t.Fatalf("save update: %v", err)
	}
	if !eqSeq(fired, []string{"BeforeSave", "BeforeUpdate", "AfterUpdate", "AfterSave"}) {
		t.Fatalf("save-update fired %v", fired)
	}
}
