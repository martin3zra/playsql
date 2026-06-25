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

	// Build the sorted union of fillable columns.
	set := map[string]bool{}
	for _, row := range rows {
		for k := range row {
			if b.meta.CanFill(k) {
				set[k] = true
			}
		}
	}
	now := time.Now()
	stamp := map[string]bool{}
	for _, c := range []string{b.meta.CreatedAtColumn, b.meta.UpdatedAtColumn} {
		if c != "" {
			set[c] = true
			stamp[c] = true
		}
	}
	if len(set) == 0 {
		return 0, fmt.Errorf("playsql: InsertMany: no fillable columns in data")
	}

	cols := make([]string, 0, len(set))
	for c := range set {
		cols = append(cols, c)
	}
	sort.Strings(cols)

	// Row-major values.
	var vals []any
	for _, row := range rows {
		for _, c := range cols {
			switch {
			case stamp[c]:
				vals = append(vals, now)
			default:
				vals = append(vals, row[c]) // missing key -> nil -> NULL
			}
		}
	}

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
