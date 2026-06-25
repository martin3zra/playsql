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
// is written back onto the struct.
func (s *session) Insert(ctx context.Context, model any) error {
	meta, elem, err := structValue(model)
	if err != nil {
		return err
	}

	pkIdx, _ := meta.PrimaryKeyFieldIndex()

	var columns []string
	var values []any
	for _, c := range meta.Columns {
		// Skip an auto-incrementing primary key when unset; let the DB assign it.
		if c.PrimaryKey && meta.Incrementing && elem.Field(c.FieldIndex).IsZero() {
			continue
		}
		columns = append(columns, c.DBName)
		values = append(values, elem.Field(c.FieldIndex).Interface())
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
		return nil
	}

	res, err := s.run.ExecContext(ctx, sqlStr, values...)
	if err != nil {
		return err
	}
	if meta.Incrementing {
		if id, err := res.LastInsertId(); err == nil {
			setPK(elem, pkIdx, id)
		}
	}
	return nil
}

// Update writes the model's non-primary-key columns to the row matching its
// primary key. The key field must be set.
func (s *session) Update(ctx context.Context, model any) error {
	meta, elem, err := structValue(model)
	if err != nil {
		return err
	}

	pkIdx, ok := meta.PrimaryKeyFieldIndex()
	if !ok {
		return fmt.Errorf("playsql: model %q has no primary key", meta.Table)
	}
	pkVal := elem.Field(pkIdx).Interface()
	if elem.Field(pkIdx).IsZero() {
		return fmt.Errorf("playsql: Update requires a non-zero primary key")
	}

	var columns []string
	var values []any
	for _, c := range meta.Columns {
		if c.FieldIndex == pkIdx {
			continue
		}
		columns = append(columns, c.DBName)
		values = append(values, elem.Field(c.FieldIndex).Interface())
	}
	values = append(values, pkVal) // bound after the SET values

	sqlStr := s.grammar.CompileUpdate(grammar.UpdateStmt{
		Table:   meta.Table,
		Columns: columns,
		Wheres: []grammar.WhereClause{
			{Kind: grammar.WhereBasic, Column: meta.PrimaryKey, Op: "=", Value: pkVal},
		},
	})

	_, err = s.run.ExecContext(ctx, sqlStr, values...)
	return err
}

// Save inserts when the model has no identity yet, otherwise updates. For an
// incrementing key, "no identity" means the key field is zero.
func (s *session) Save(ctx context.Context, model any) error {
	meta, elem, err := structValue(model)
	if err != nil {
		return err
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
