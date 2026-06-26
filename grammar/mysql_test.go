package grammar

import (
	"reflect"
	"testing"
)

func TestMySQL_SelectBackticksAndPlaceholders(t *testing.T) {
	sql, args := MySQL{}.CompileSelect(CompiledQuery{
		Table:   "users",
		Columns: []string{"id", "name"},
		Wheres:  []WhereClause{{Kind: WhereBasic, Column: "age", Op: ">", Value: 18}},
		Limit:   5,
		Offset:  10,
	})
	want := "SELECT `id`, `name` FROM `users` WHERE `age` > ? LIMIT 5 OFFSET 10"
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if !reflect.DeepEqual(args, []any{18}) {
		t.Errorf("args mismatch: %#v", args)
	}
}

func TestMySQL_InsertNoReturning(t *testing.T) {
	sql, returnsID := MySQL{}.CompileInsert(InsertStmt{
		Table: "users", Columns: []string{"name"},
		PrimaryKey: "id", Incrementing: true,
	})
	if sql != "INSERT INTO `users` (`name`) VALUES (?)" {
		t.Errorf("unexpected: %s", sql)
	}
	if returnsID {
		t.Error("MySQL uses LastInsertId, must not RETURNING")
	}
}

func TestMySQL_Upsert(t *testing.T) {
	sql := MySQL{}.CompileUpsert(UpsertStmt{
		Table:           "settings",
		Columns:         []string{"k", "v"},
		Rows:            1,
		ConflictColumns: []string{"k"}, // ignored by MySQL
		UpdateColumns:   []string{"v"},
	})
	want := "INSERT INTO `settings` (`k`, `v`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `v` = VALUES(`v`)"
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
}

func TestMySQL_UpdateContiguous(t *testing.T) {
	sql, _ := MySQL{}.CompileUpdate(UpdateStmt{
		Table:   "users",
		Columns: []string{"name", "age"},
		Wheres:  []WhereClause{{Kind: WhereBasic, Column: "id", Op: "=", Value: 7}},
	})
	want := "UPDATE `users` SET `name` = ?, `age` = ? WHERE `id` = ?"
	if sql != want {
		t.Errorf("unexpected: %s", sql)
	}
}

func TestMySQL_RegisteredInFor(t *testing.T) {
	if _, ok := For("mysql").(MySQL); !ok {
		t.Error("For(\"mysql\") should return MySQL grammar")
	}
}
