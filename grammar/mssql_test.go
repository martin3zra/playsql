package grammar

import (
	"reflect"
	"testing"
)

func TestMSSQL_SelectOffsetFetch(t *testing.T) {
	sql, args := MSSQL{}.CompileSelect(CompiledQuery{
		Table:   "users",
		Columns: []string{"id", "name"},
		Wheres:  []WhereClause{{Kind: WhereBasic, Column: "age", Op: ">", Value: 18}},
		Orders:  []OrderClause{{Column: "id", Direction: "ASC"}},
		Limit:   5,
		Offset:  10,
	})
	want := "SELECT [id], [name] FROM [users] WHERE [age] > @p1 " +
		"ORDER BY [id] ASC OFFSET 10 ROWS FETCH NEXT 5 ROWS ONLY"
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if !reflect.DeepEqual(args, []any{18}) {
		t.Errorf("args mismatch: %#v", args)
	}
}

func TestMSSQL_PagingAddsFallbackOrder(t *testing.T) {
	// Limit with no ORDER BY -> SQL Server requires one.
	sql, _ := MSSQL{}.CompileSelect(CompiledQuery{Table: "t", Limit: 3})
	want := "SELECT * FROM [t] ORDER BY (SELECT NULL) OFFSET 0 ROWS FETCH NEXT 3 ROWS ONLY"
	if sql != want {
		t.Errorf("unexpected: %s", sql)
	}
}

func TestMSSQL_NoPagingNoOffset(t *testing.T) {
	sql, _ := MSSQL{}.CompileSelect(CompiledQuery{Table: "t"})
	if sql != "SELECT * FROM [t]" {
		t.Errorf("unexpected: %s", sql)
	}
}

func TestMSSQL_InsertOutputInserted(t *testing.T) {
	sql, returnsID := MSSQL{}.CompileInsert(InsertStmt{
		Table: "users", Columns: []string{"name", "age"},
		PrimaryKey: "id", Incrementing: true,
	})
	want := "INSERT INTO [users] ([name], [age]) OUTPUT INSERTED.[id] VALUES (@p1, @p2)"
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if !returnsID {
		t.Error("incrementing insert should return id via OUTPUT")
	}
}

func TestMSSQL_UpsertMerge(t *testing.T) {
	sql := MSSQL{}.CompileUpsert(UpsertStmt{
		Table:           "settings",
		Columns:         []string{"k", "v"},
		Rows:            1,
		ConflictColumns: []string{"k"},
		UpdateColumns:   []string{"v"},
	})
	want := "MERGE INTO [settings] AS tgt USING (VALUES (@p1, @p2)) AS src ([k], [v]) " +
		"ON tgt.[k] = src.[k] " +
		"WHEN MATCHED THEN UPDATE SET tgt.[v] = src.[v] " +
		"WHEN NOT MATCHED THEN INSERT ([k], [v]) VALUES (src.[k], src.[v]);"
	if sql != want {
		t.Errorf("sql mismatch:\n got:  %s\nwant: %s", sql, want)
	}
}

func TestMSSQL_RegisteredInFor(t *testing.T) {
	if _, ok := For("sqlserver").(MSSQL); !ok {
		t.Error("For(\"sqlserver\") should return MSSQL grammar")
	}
}
