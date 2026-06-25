package playsql

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/martin3zra/playsql/grammar"
	"github.com/martin3zra/playsql/metadata"
)

// Insert writes model as a new row. model must be a pointer to a struct. For an
// incrementing primary key, the zero key field is omitted and the generated id
// is written back onto the struct. created_at/updated_at columns are stamped
// automatically when present.
func (s *session) Insert(ctx context.Context, model any) error {
	meta, elem, err := structValue(model)
	if err != nil {
		return err
	}

	pkIdx, _ := meta.PrimaryKeyFieldIndex()
	now := time.Now()

	var columns []string
	var values []any
	for _, c := range meta.Columns {
		// Skip an auto-incrementing primary key when unset; let the DB assign it.
		if c.PrimaryKey && meta.Incrementing && elem.Field(c.FieldIndex).IsZero() {
			continue
		}
		val := elem.Field(c.FieldIndex).Interface()
		if c.DBName == meta.CreatedAtColumn || c.DBName == meta.UpdatedAtColumn {
			val = now
			setTimeField(elem, c.FieldIndex, now)
		}
		columns = append(columns, c.DBName)
		values = append(values, val)
	}

	sqlStr, returnsID := s.grammar.CompileInsert(grammar.InsertStmt{
		Table:        meta.Table,
		Columns:      columns,
		PrimaryKey:   meta.PrimaryKey,
		Incrementing: meta.Incrementing,
	})

	if returnsID {
		var id int64
		if err := s.run.QueryRowContext(ctx, sqlStr, values...).Scan(&id); err != nil {
			return err
		}
		setPK(elem, pkIdx, id)
	} else {
		res, err := s.run.ExecContext(ctx, sqlStr, values...)
		if err != nil {
			return err
		}
		if meta.Incrementing {
			if id, err := res.LastInsertId(); err == nil {
				setPK(elem, pkIdx, id)
			}
		}
	}

	markPersisted(model, meta, elem)
	return nil
}

// Update writes the model's columns to the row matching its primary key. When
// the model embeds playsql.Model and was loaded/persisted, only changed columns
// are written (and the update is a no-op if nothing changed). updated_at is
// stamped automatically when present. The key field must be set.
func (s *session) Update(ctx context.Context, model any) error {
	meta, elem, err := structValue(model)
	if err != nil {
		return err
	}

	pkIdx, ok := meta.PrimaryKeyFieldIndex()
	if !ok {
		return fmt.Errorf("playsql: model %q has no primary key", meta.Table)
	}
	if elem.Field(pkIdx).IsZero() {
		return fmt.Errorf("playsql: Update requires a non-zero primary key")
	}
	pkVal := elem.Field(pkIdx).Interface()

	// Determine which columns to write.
	original := originalOf(model)
	now := time.Now()

	var columns []string
	var values []any
	for _, c := range meta.Columns {
		if c.FieldIndex == pkIdx {
			continue
		}
		if c.DBName == meta.CreatedAtColumn {
			continue // never overwrite created_at on update
		}
		if c.DBName == meta.UpdatedAtColumn {
			continue // handled after the dirty check
		}

		cur := elem.Field(c.FieldIndex).Interface()
		if original != nil && reflect.DeepEqual(cur, original[c.DBName]) {
			continue // unchanged — skip when we have a baseline
		}
		columns = append(columns, c.DBName)
		values = append(values, cur)
	}

	// Nothing changed and we have a baseline -> no-op.
	if len(columns) == 0 && original != nil {
		return nil
	}

	// Stamp updated_at when present (and there is something to write).
	if meta.UpdatedAtColumn != "" {
		if idx, ok := meta.FieldIndexByColumn(meta.UpdatedAtColumn); ok {
			setTimeField(elem, idx, now)
		}
		columns = append(columns, meta.UpdatedAtColumn)
		values = append(values, now)
	}

	values = append(values, pkVal) // bound after the SET values

	sqlStr := s.grammar.CompileUpdate(grammar.UpdateStmt{
		Table:   meta.Table,
		Columns: columns,
		Wheres: []grammar.WhereClause{
			{Kind: grammar.WhereBasic, Column: meta.PrimaryKey, Op: "=", Value: pkVal},
		},
	})

	if _, err := s.run.ExecContext(ctx, sqlStr, values...); err != nil {
		return err
	}

	markPersisted(model, meta, elem)
	return nil
}

// Save inserts when the model has no identity yet, otherwise updates. When the
// model embeds playsql.Model, identity is its exists flag; otherwise an
// incrementing key is "new" when its field is zero.
func (s *session) Save(ctx context.Context, model any) error {
	meta, elem, err := structValue(model)
	if err != nil {
		return err
	}

	if acc, ok := baseOf(model); ok {
		if acc.playExists() {
			return s.Update(ctx, model)
		}
		return s.Insert(ctx, model)
	}

	if meta.Incrementing {
		if pkIdx, ok := meta.PrimaryKeyFieldIndex(); ok && elem.Field(pkIdx).IsZero() {
			return s.Insert(ctx, model)
		}
	}
	return s.Update(ctx, model)
}

// Delete removes the row matching the model's primary key. For a soft-deletable
// model this sets deleted_at; use ForceDelete for a hard delete. The key must
// be set.
func (s *session) Delete(ctx context.Context, model any) error {
	meta, elem, err := structValue(model)
	if err != nil {
		return err
	}
	pkVal, err := requirePK(meta, elem)
	if err != nil {
		return err
	}

	if meta.SoftDeletes {
		now := time.Now()
		sqlStr := s.grammar.CompileUpdate(grammar.UpdateStmt{
			Table:   meta.Table,
			Columns: []string{meta.DeletedAtColumn},
			Wheres:  []grammar.WhereClause{{Kind: grammar.WhereBasic, Column: meta.PrimaryKey, Op: "=", Value: pkVal}},
		})
		if _, err := s.run.ExecContext(ctx, sqlStr, now, pkVal); err != nil {
			return err
		}
		setDeletedAt(elem, meta, &now)
		return nil
	}

	return s.hardDelete(ctx, meta, pkVal)
}

// ForceDelete permanently removes the row, ignoring soft-delete.
func (s *session) ForceDelete(ctx context.Context, model any) error {
	meta, elem, err := structValue(model)
	if err != nil {
		return err
	}
	pkVal, err := requirePK(meta, elem)
	if err != nil {
		return err
	}
	return s.hardDelete(ctx, meta, pkVal)
}

// Restore clears deleted_at on a soft-deleted row.
func (s *session) Restore(ctx context.Context, model any) error {
	meta, elem, err := structValue(model)
	if err != nil {
		return err
	}
	if !meta.SoftDeletes {
		return ErrNotSoftDeletable
	}
	pkVal, err := requirePK(meta, elem)
	if err != nil {
		return err
	}

	sqlStr := s.grammar.CompileUpdate(grammar.UpdateStmt{
		Table:   meta.Table,
		Columns: []string{meta.DeletedAtColumn},
		Wheres:  []grammar.WhereClause{{Kind: grammar.WhereBasic, Column: meta.PrimaryKey, Op: "=", Value: pkVal}},
	})
	if _, err := s.run.ExecContext(ctx, sqlStr, nil, pkVal); err != nil {
		return err
	}
	setDeletedAt(elem, meta, nil)
	return nil
}

func (s *session) hardDelete(ctx context.Context, meta *metadata.ModelMeta, pkVal any) error {
	sqlStr := s.grammar.CompileDelete(grammar.DeleteStmt{
		Table: meta.Table,
		Wheres: []grammar.WhereClause{
			{Kind: grammar.WhereBasic, Column: meta.PrimaryKey, Op: "=", Value: pkVal},
		},
	})
	_, err := s.run.ExecContext(ctx, sqlStr, pkVal)
	return err
}

// requirePK returns the model's primary-key value, erroring when it is unset.
func requirePK(meta *metadata.ModelMeta, elem reflect.Value) (any, error) {
	pkIdx, ok := meta.PrimaryKeyFieldIndex()
	if !ok {
		return nil, fmt.Errorf("playsql: model %q has no primary key", meta.Table)
	}
	if elem.Field(pkIdx).IsZero() {
		return nil, fmt.Errorf("playsql: operation requires a non-zero primary key")
	}
	return elem.Field(pkIdx).Interface(), nil
}

// setDeletedAt writes the deleted_at field back onto the struct when present and
// settable (*time.Time). Best-effort; ignored for other field types.
func setDeletedAt(elem reflect.Value, meta *metadata.ModelMeta, t *time.Time) {
	idx, ok := meta.FieldIndexByColumn(meta.DeletedAtColumn)
	if !ok {
		return
	}
	f := elem.Field(idx)
	if f.Kind() == reflect.Ptr && f.Type().Elem() == reflect.TypeOf(time.Time{}) && f.CanSet() {
		if t == nil {
			f.Set(reflect.Zero(f.Type()))
		} else {
			f.Set(reflect.ValueOf(t))
		}
	}
}

// structValue resolves model to its metadata and addressable struct value.
func structValue(model any) (*metadata.ModelMeta, reflect.Value, error) {
	rv := reflect.ValueOf(model)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		return nil, reflect.Value{}, fmt.Errorf("playsql: model must be a pointer to a struct, got %T", model)
	}
	return metadata.For(model), rv.Elem(), nil
}

// setPK writes a generated integer id back onto an integer key field.
func setPK(elem reflect.Value, pkIdx int, id int64) {
	f := elem.Field(pkIdx)
	if f.CanSet() && f.Kind() >= reflect.Int && f.Kind() <= reflect.Int64 {
		f.SetInt(id)
	}
}

// setTimeField writes t onto a time.Time or *time.Time field.
func setTimeField(elem reflect.Value, idx int, t time.Time) {
	f := elem.Field(idx)
	if !f.CanSet() {
		return
	}
	switch {
	case f.Type() == reflect.TypeOf(time.Time{}):
		f.Set(reflect.ValueOf(t))
	case f.Kind() == reflect.Ptr && f.Type().Elem() == reflect.TypeOf(time.Time{}):
		tc := t
		f.Set(reflect.ValueOf(&tc))
	}
}

// snapshot captures the current column values for dirty tracking.
func snapshot(meta *metadata.ModelMeta, elem reflect.Value) map[string]any {
	m := make(map[string]any, len(meta.Columns))
	for _, c := range meta.Columns {
		m[c.DBName] = elem.Field(c.FieldIndex).Interface()
	}
	return m
}

// markPersisted records exists+baseline on models that embed playsql.Model.
func markPersisted(model any, meta *metadata.ModelMeta, elem reflect.Value) {
	if acc, ok := baseOf(model); ok {
		acc.playMarkPersisted(snapshot(meta, elem))
	}
}

// originalOf returns the dirty-tracking baseline, or nil when unavailable.
func originalOf(model any) map[string]any {
	if acc, ok := baseOf(model); ok {
		return acc.playOriginal()
	}
	return nil
}
