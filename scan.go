package playsql

import (
	"database/sql"
	"fmt"
	"reflect"

	"github.com/martin3zra/playsql/metadata"
)

// scanRows scans all rows into dest, which must be a pointer to a slice of
// structs or pointers to structs (*[]T or *[]*T). Each column is scanned into
// its mapped struct field via database/sql's native conversion; unmapped
// columns are discarded. No reflection over tags happens here — column->field
// mapping comes from metadata.
func scanRows(rows *sql.Rows, dest any, meta *metadata.ModelMeta) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Ptr || dv.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("playsql: dest must be a pointer to a slice, got %T", dest)
	}

	slice := dv.Elem()
	elemType := slice.Type().Elem()
	elemIsPtr := elemType.Kind() == reflect.Ptr
	structType := elemType
	if elemIsPtr {
		structType = elemType.Elem()
	}
	if structType.Kind() != reflect.Struct {
		return fmt.Errorf("playsql: dest element must be a struct, got %s", structType.Kind())
	}

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	for rows.Next() {
		elemPtr := reflect.New(structType) // *T
		structVal := elemPtr.Elem()

		targets, finish := scanInto(structVal, cols, meta)
		if err := rows.Scan(targets...); err != nil {
			return err
		}
		finish()

		// Record exists+baseline before appending (the value path copies).
		markPersisted(elemPtr.Interface(), meta, structVal)

		if elemIsPtr {
			slice.Set(reflect.Append(slice, elemPtr))
		} else {
			slice.Set(reflect.Append(slice, structVal))
		}
	}

	return nil
}

// scanOne scans a single row into dest (a *T where T is a struct). It returns
// ErrNotFound when there is no row.
func scanOne(rows *sql.Rows, dest any, meta *metadata.ModelMeta) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Ptr || dv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("playsql: dest must be a pointer to a struct, got %T", dest)
	}

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return ErrNotFound
	}

	targets, finish := scanInto(dv.Elem(), cols, meta)
	if err := rows.Scan(targets...); err != nil {
		return err
	}
	finish()
	markPersisted(dest, meta, dv.Elem())
	return rows.Err()
}

// scanInto builds the per-column scan destinations for one struct value and a
// finish func that copies them onto the struct. Each mapped column is scanned
// into a fresh pointer-to-field-type (**T) so a SQL NULL is tolerated: it leaves
// the holder nil and the field keeps its zero value, while non-NULL values use
// database/sql's native typed conversion. Unmapped columns are discarded.
func scanInto(structVal reflect.Value, cols []string, meta *metadata.ModelMeta) (targets []any, finish func()) {
	type binding struct {
		holder reflect.Value // the *T inside the **T target
		field  reflect.Value
	}
	type castBinding struct {
		raw    reflect.Value // *any target (holds the driver value)
		field  reflect.Value
		caster Caster
	}

	targets = make([]any, len(cols))
	var binds []binding
	var castBinds []castBinding

	for i, col := range cols {
		cm, ok := meta.Column(col)
		if !ok {
			var discard any
			targets[i] = &discard
			continue
		}
		field := structVal.Field(cm.FieldIndex)

		if cm.Cast != "" {
			if caster, ok := casterFor(cm.Cast); ok {
				raw := reflect.New(anyType) // *any
				targets[i] = raw.Interface()
				castBinds = append(castBinds, castBinding{raw: raw, field: field, caster: caster})
				continue
			}
		}

		holderPtr := reflect.New(reflect.PtrTo(field.Type())) // **T, *T is nil
		targets[i] = holderPtr.Interface()
		binds = append(binds, binding{holder: holderPtr.Elem(), field: field})
	}

	finish = func() {
		for _, b := range binds {
			if !b.holder.IsNil() {
				b.field.Set(b.holder.Elem())
			}
		}
		for _, cb := range castBinds {
			raw := cb.raw.Elem().Interface()
			if raw == nil { // SQL NULL -> leave the zero value
				continue
			}
			_ = cb.caster.Decode(raw, cb.field)
		}
	}
	return targets, finish
}

var anyType = reflect.TypeOf((*any)(nil)).Elem()

// sliceElemMeta derives model metadata from dest, a pointer to a slice of
// structs or struct pointers (*[]T or *[]*T). It is used by raw queries, where
// there is no Builder to carry the metadata.
func sliceElemMeta(dest any) (*metadata.ModelMeta, error) {
	t := reflect.TypeOf(dest)
	if t == nil || t.Kind() != reflect.Ptr || t.Elem().Kind() != reflect.Slice {
		return nil, fmt.Errorf("playsql: dest must be a pointer to a slice, got %T", dest)
	}
	elem := t.Elem().Elem()
	for elem.Kind() == reflect.Ptr {
		elem = elem.Elem()
	}
	if elem.Kind() != reflect.Struct {
		return nil, fmt.Errorf("playsql: dest element must be a struct, got %s", elem.Kind())
	}
	return metadata.For(reflect.New(elem).Interface()), nil
}
