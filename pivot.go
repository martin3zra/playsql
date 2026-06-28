package playsql

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"github.com/martin3zra/playsql/grammar"
	"github.com/martin3zra/playsql/metadata"
)

// Pivot management for many-to-many relations (belongsToMany, morphToMany,
// morphedByMany): attach/detach rows on the join table, sync to an exact set,
// toggle membership, or update pivot columns — without raw SQL. The morph kinds
// write/filter the polymorphic *_type column automatically.
//
// Sync and Toggle issue multiple statements and are not wrapped in a transaction;
// wrap the call in db.Tx for atomicity.

// SyncResult reports the changes a Sync applied.
type SyncResult struct {
	Attached []any
	Detached []any
}

// pivotInfo is the resolved join-table shape for a parent instance + relation.
type pivotInfo struct {
	table     string
	fpk       string // parent's key column in the pivot
	rpk       string // related's key column in the pivot
	typeCol   string // polymorphic type column ("" for plain belongsToMany)
	typeVal   string // value for typeCol
	parentVal any    // the parent row's key value
}

func (s *session) pivotInfo(model any, relation string) (*pivotInfo, error) {
	meta, elem, err := structValue(model)
	if err != nil {
		return nil, err
	}
	rel, ok := meta.Relations[relation]
	if !ok {
		return nil, fmt.Errorf("playsql: unknown relation %q on %s", relation, meta.StructName)
	}
	relatedMeta := metadata.For(reflect.New(rel.RelatedType).Interface())

	pi := &pivotInfo{}
	var parentKey string
	switch rel.Kind {
	case metadata.BelongsToMany:
		pi.table, pi.fpk, pi.rpk, parentKey, _ = metadata.ResolvePivot(meta, rel, relatedMeta)
	case metadata.MorphToMany, metadata.MorphedByMany:
		pi.table, pi.fpk, pi.rpk, parentKey, _, pi.typeCol, pi.typeVal = metadata.ResolveMorphPivot(meta, rel, relatedMeta)
	default:
		return nil, fmt.Errorf("playsql: relation %q is not an attachable pivot relation (%s)", relation, rel.Kind)
	}

	idx, ok := meta.FieldIndexByColumn(parentKey)
	if !ok {
		return nil, fmt.Errorf("playsql: relation %q: parent key column %q not found on %s", relation, parentKey, meta.StructName)
	}
	pi.parentVal = elem.Field(idx).Interface()
	return pi, nil
}

// Attach inserts pivot rows linking the parent to each relatedID.
func (s *session) Attach(ctx context.Context, model any, relation string, relatedIDs ...any) error {
	pi, err := s.pivotInfo(model, relation)
	if err != nil {
		return err
	}
	return s.attachIDs(ctx, pi, relatedIDs, nil)
}

// AttachWith inserts a single pivot row carrying extra pivot column values.
func (s *session) AttachWith(ctx context.Context, model any, relation string, relatedID any, pivot map[string]any) error {
	pi, err := s.pivotInfo(model, relation)
	if err != nil {
		return err
	}
	return s.attachIDs(ctx, pi, []any{relatedID}, pivot)
}

func (s *session) attachIDs(ctx context.Context, pi *pivotInfo, ids []any, pivot map[string]any) error {
	if len(ids) == 0 {
		return nil
	}
	cols := []string{pi.fpk, pi.rpk}
	if pi.typeCol != "" {
		cols = append(cols, pi.typeCol)
	}
	dataKeys := sortedKeys(pivot)
	cols = append(cols, dataKeys...)

	vals := make([]any, 0, len(ids)*len(cols))
	for _, id := range ids {
		vals = append(vals, pi.parentVal, id)
		if pi.typeCol != "" {
			vals = append(vals, pi.typeVal)
		}
		for _, k := range dataKeys {
			vals = append(vals, pivot[k])
		}
	}

	sqlStr, _ := s.grammar.CompileInsert(grammar.InsertStmt{Table: pi.table, Columns: cols, Rows: len(ids)})
	_, err := s.run.ExecContext(ctx, sqlStr, vals...)
	return err
}

// Detach removes pivot rows for the given relatedIDs; with none, it removes all
// of the parent's rows (within its type for a morph relation).
func (s *session) Detach(ctx context.Context, model any, relation string, relatedIDs ...any) error {
	pi, err := s.pivotInfo(model, relation)
	if err != nil {
		return err
	}
	return s.detachIDs(ctx, pi, relatedIDs)
}

func (s *session) detachIDs(ctx context.Context, pi *pivotInfo, ids []any) error {
	wheres := s.pivotScope(pi)
	if len(ids) > 0 {
		wheres = append(wheres, grammar.WhereClause{Kind: grammar.WhereIn, Boolean: "AND", Column: pi.rpk, Values: ids})
	}
	sqlStr := s.grammar.CompileDelete(grammar.DeleteStmt{Table: pi.table, Wheres: wheres})
	_, err := s.run.ExecContext(ctx, sqlStr, whereArgs(wheres)...)
	return err
}

// Sync makes the parent's pivot rows exactly relatedIDs: attaching the missing
// ones and detaching the extra. Returns what changed.
func (s *session) Sync(ctx context.Context, model any, relation string, relatedIDs []any) (SyncResult, error) {
	pi, err := s.pivotInfo(model, relation)
	if err != nil {
		return SyncResult{}, err
	}
	current, err := s.pivotCurrentIDs(ctx, pi)
	if err != nil {
		return SyncResult{}, err
	}

	want := keySet(relatedIDs)
	have := keySet(current)

	var toAttach, toDetach []any
	for _, id := range relatedIDs {
		if !have[keyStr(id)] {
			toAttach = append(toAttach, id)
		}
	}
	for _, id := range current {
		if !want[keyStr(id)] {
			toDetach = append(toDetach, id)
		}
	}

	if len(toDetach) > 0 {
		if err := s.detachIDs(ctx, pi, toDetach); err != nil {
			return SyncResult{}, err
		}
	}
	if len(toAttach) > 0 {
		if err := s.attachIDs(ctx, pi, toAttach, nil); err != nil {
			return SyncResult{}, err
		}
	}
	return SyncResult{Attached: toAttach, Detached: toDetach}, nil
}

// SyncWithoutDetaching attaches any missing relatedIDs but never detaches.
func (s *session) SyncWithoutDetaching(ctx context.Context, model any, relation string, relatedIDs []any) error {
	pi, err := s.pivotInfo(model, relation)
	if err != nil {
		return err
	}
	have, err := s.pivotCurrentIDs(ctx, pi)
	if err != nil {
		return err
	}
	set := keySet(have)
	var toAttach []any
	for _, id := range relatedIDs {
		if !set[keyStr(id)] {
			toAttach = append(toAttach, id)
		}
	}
	return s.attachIDs(ctx, pi, toAttach, nil)
}

// Toggle attaches relatedIDs that are absent and detaches those present.
func (s *session) Toggle(ctx context.Context, model any, relation string, relatedIDs []any) error {
	pi, err := s.pivotInfo(model, relation)
	if err != nil {
		return err
	}
	have, err := s.pivotCurrentIDs(ctx, pi)
	if err != nil {
		return err
	}
	set := keySet(have)
	var toAttach, toDetach []any
	for _, id := range relatedIDs {
		if set[keyStr(id)] {
			toDetach = append(toDetach, id)
		} else {
			toAttach = append(toAttach, id)
		}
	}
	if len(toDetach) > 0 {
		if err := s.detachIDs(ctx, pi, toDetach); err != nil {
			return err
		}
	}
	return s.attachIDs(ctx, pi, toAttach, nil)
}

// UpdatePivot updates pivot column values for one existing link.
func (s *session) UpdatePivot(ctx context.Context, model any, relation string, relatedID any, pivot map[string]any) error {
	pi, err := s.pivotInfo(model, relation)
	if err != nil {
		return err
	}
	cols := sortedKeys(pivot)
	if len(cols) == 0 {
		return nil
	}
	vals := make([]any, len(cols))
	for i, c := range cols {
		vals[i] = pivot[c]
	}

	wheres := s.pivotScope(pi)
	wheres = append(wheres, grammar.WhereClause{Kind: grammar.WhereBasic, Boolean: "AND", Column: pi.rpk, Op: "=", Value: relatedID})

	sqlStr, _ := s.grammar.CompileUpdate(grammar.UpdateStmt{Table: pi.table, Columns: cols, Wheres: wheres})
	args := append(vals, whereArgs(wheres)...)
	_, err = s.run.ExecContext(ctx, sqlStr, args...)
	return err
}

// pivotScope is the parent (and type, for morph) predicate shared by detach and
// update.
func (s *session) pivotScope(pi *pivotInfo) []grammar.WhereClause {
	wheres := []grammar.WhereClause{{Kind: grammar.WhereBasic, Column: pi.fpk, Op: "=", Value: pi.parentVal}}
	if pi.typeCol != "" {
		wheres = append(wheres, grammar.WhereClause{Kind: grammar.WhereBasic, Boolean: "AND", Column: pi.typeCol, Op: "=", Value: pi.typeVal})
	}
	return wheres
}

// pivotCurrentIDs reads the related key values currently linked to the parent.
func (s *session) pivotCurrentIDs(ctx context.Context, pi *pivotInfo) ([]any, error) {
	wheres := s.pivotScope(pi)
	sqlStr, args := s.grammar.CompileSelect(grammar.CompiledQuery{
		Table:   pi.table,
		Columns: []string{pi.rpk},
		Wheres:  wheres,
	})
	rows, err := s.run.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []any
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func sortedKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func keySet(ids []any) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[keyStr(id)] = true
	}
	return set
}
