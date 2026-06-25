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

		if err := rows.Scan(scanTargets(structVal, cols, meta)...); err != nil {
			return err
		}

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

	if err := rows.Scan(scanTargets(dv.Elem(), cols, meta)...); err != nil {
		return err
	}
	return rows.Err()
}

// scanTargets builds the per-column scan destinations for one struct value:
// the mapped field's address, or a throwaway for unmapped columns. Column->field
// mapping comes from metadata; no tag reflection happens here.
func scanTargets(structVal reflect.Value, cols []string, meta *metadata.ModelMeta) []any {
	targets := make([]any, len(cols))
	for i, col := range cols {
		if idx, ok := meta.FieldIndexByColumn(col); ok {
			targets[i] = structVal.Field(idx).Addr().Interface()
		} else {
			var discard any
			targets[i] = &discard
		}
	}
	return targets
}
