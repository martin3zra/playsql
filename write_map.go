package playsql

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/martin3zra/playsql/grammar"
)

// Insert mass-assigns a map of column => value as a new row and returns the
// generated id. Keys not permitted by the model's fillable/guarded rules are
// dropped. created_at/updated_at are stamped automatically when present.
func (b *Builder) Insert(ctx context.Context, data map[string]any) (int64, error) {
	if b.err != nil {
		return 0, b.err
	}

	cols, vals := b.fillable(data)
	cols, vals = b.stampInsertTimestamps(cols, vals, time.Now())
	if len(cols) == 0 {
		return 0, fmt.Errorf("playsql: Insert: no fillable columns in data")
	}

	return b.execInsert(ctx, cols, vals)
}

// Create inserts the map like Insert, then loads the new row into dest (a *T),
// so dest carries the generated id, timestamps, and any database defaults.
func (b *Builder) Create(ctx context.Context, dest any, data map[string]any) error {
	id, err := b.Insert(ctx, data)
	if err != nil {
		return err
	}
	fresh := &Builder{sess: b.sess, meta: b.meta}
	return fresh.Find(ctx, dest, id)
}

// Update mass-assigns a map of column => value to all matching rows and returns
// the number affected. Keys are filtered by fillable/guarded; updated_at is
// stamped automatically when present.
func (b *Builder) Update(ctx context.Context, data map[string]any) (int64, error) {
	if b.err != nil {
		return 0, b.err
	}

	cols, vals := b.fillable(data)
	if c := b.meta.UpdatedAtColumn; c != "" && !contains(cols, c) {
		cols = append(cols, c)
		vals = append(vals, time.Now())
	}
	if len(cols) == 0 {
		return 0, nil
	}

	wheres := b.effectiveWheres(b.trashed)
	args := append(vals, whereArgs(wheres)...)

	sqlStr := b.sess.grammar.CompileUpdate(grammar.UpdateStmt{
		Table:   b.meta.Table,
		Columns: cols,
		Wheres:  wheres,
	})

	res, err := b.sess.run.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// InsertMany bulk-inserts multiple rows in a single statement and returns the
// number inserted. The column set is the sorted union of fillable keys across
// all rows; a row missing a column inserts NULL for it. Timestamps are stamped.
func (b *Builder) InsertMany(ctx context.Context, rows []map[string]any) (int64, error) {
	if b.err != nil {
		return 0, b.err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	cols, stamp := b.unionColumns(rows)
	if len(cols) == 0 {
		return 0, fmt.Errorf("playsql: InsertMany: no fillable columns in data")
	}
	vals := b.rowValues(rows, cols, stamp, time.Now())

	sqlStr, _ := b.sess.grammar.CompileInsert(grammar.InsertStmt{
		Table:        b.meta.Table,
		Columns:      cols,
		Rows:         len(rows),
		Incrementing: b.meta.Incrementing,
	})

	res, err := b.sess.run.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Upsert inserts rows, and on a conflict over conflictColumns updates
// updateColumns (DO UPDATE SET col = EXCLUDED.col). If updateColumns is nil it
// defaults to every inserted column except the conflict columns and created_at;
// an explicit empty slice means DO NOTHING on conflict. Keys are filtered by
// fillable/guarded; created_at/updated_at are stamped.
func (b *Builder) Upsert(ctx context.Context, rows []map[string]any, conflictColumns, updateColumns []string) (int64, error) {
	if b.err != nil {
		return 0, b.err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if len(conflictColumns) == 0 {
		return 0, fmt.Errorf("playsql: Upsert requires at least one conflict column")
	}

	cols, stamp := b.unionColumns(rows)
	if len(cols) == 0 {
		return 0, fmt.Errorf("playsql: Upsert: no fillable columns in data")
	}
	vals := b.rowValues(rows, cols, stamp, time.Now())

	if updateColumns == nil {
		for _, c := range cols {
			if contains(conflictColumns, c) || c == b.meta.CreatedAtColumn {
				continue
			}
			updateColumns = append(updateColumns, c)
		}
	}

	sqlStr := b.sess.grammar.CompileUpsert(grammar.UpsertStmt{
		Table:           b.meta.Table,
		Columns:         cols,
		Rows:            len(rows),
		ConflictColumns: conflictColumns,
		UpdateColumns:   updateColumns,
	})

	res, err := b.sess.run.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// unionColumns returns the sorted union of fillable columns across rows, plus
// the timestamp columns; stamp marks which are timestamp columns.
func (b *Builder) unionColumns(rows []map[string]any) (cols []string, stamp map[string]bool) {
	set := map[string]bool{}
	for _, row := range rows {
		for k := range row {
			if b.meta.CanFill(k) {
				set[k] = true
			}
		}
	}
	stamp = map[string]bool{}
	for _, c := range []string{b.meta.CreatedAtColumn, b.meta.UpdatedAtColumn} {
		if c != "" {
			set[c] = true
			stamp[c] = true
		}
	}
	for c := range set {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	return cols, stamp
}

// rowValues flattens rows into row-major values for the given column order,
// substituting now for timestamp columns and NULL for missing keys.
func (b *Builder) rowValues(rows []map[string]any, cols []string, stamp map[string]bool, now time.Time) []any {
	vals := make([]any, 0, len(rows)*len(cols))
	for _, row := range rows {
		for _, c := range cols {
			if stamp[c] {
				vals = append(vals, now)
			} else {
				vals = append(vals, row[c])
			}
		}
	}
	return vals
}

// execInsert compiles and runs a single-row insert, returning the new id.
func (b *Builder) execInsert(ctx context.Context, cols []string, vals []any) (int64, error) {
	sqlStr, returnsID := b.sess.grammar.CompileInsert(grammar.InsertStmt{
		Table:        b.meta.Table,
		Columns:      cols,
		Rows:         1,
		PrimaryKey:   b.meta.PrimaryKey,
		Incrementing: b.meta.Incrementing,
	})

	if returnsID {
		var id int64
		err := b.sess.run.QueryRowContext(ctx, sqlStr, vals...).Scan(&id)
		return id, err
	}

	res, err := b.sess.run.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		return 0, err
	}
	if b.meta.Incrementing {
		if id, err := res.LastInsertId(); err == nil {
			return id, nil
		}
	}
	return 0, nil
}

// fillable filters data to assignable columns and returns them sorted (for
// deterministic SQL) with their values.
func (b *Builder) fillable(data map[string]any) ([]string, []any) {
	cols := make([]string, 0, len(data))
	for k := range data {
		if b.meta.CanFill(k) {
			cols = append(cols, k)
		}
	}
	sort.Strings(cols)

	vals := make([]any, len(cols))
	for i, c := range cols {
		vals[i] = data[c]
	}
	return cols, vals
}

// stampInsertTimestamps appends created_at/updated_at = now when those columns
// exist and were not already provided.
func (b *Builder) stampInsertTimestamps(cols []string, vals []any, now time.Time) ([]string, []any) {
	for _, c := range []string{b.meta.CreatedAtColumn, b.meta.UpdatedAtColumn} {
		if c != "" && !contains(cols, c) {
			cols = append(cols, c)
			vals = append(vals, now)
		}
	}
	return cols, vals
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
