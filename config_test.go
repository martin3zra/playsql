package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

func TestConfig_SourceVerbatim(t *testing.T) {
	cases := map[playsql.Driver]string{
		playsql.SQLite:    "acme.sqlite",
		playsql.Postgres:  "postgres://postgres:secret@localhost:5433/camel?sslmode=disable",
		playsql.MySQL:     "root:secret@tcp(localhost:3306)/camel?parseTime=true",
		playsql.MSSQL:     "sqlserver://sa:Camel_Test_123@localhost:1433?database=camel_test&encrypt=disable&TrustServerCertificate=true",
		playsql.SQLServer: "sqlserver://sa:pass@localhost:1433?database=db",
	}
	for driver, src := range cases {
		cfg := playsql.Config{Driver: driver, Source: src}
		got, err := cfg.DSN()
		if err != nil {
			t.Fatalf("%s: %v", driver, err)
		}
		if got != src {
			t.Errorf("%s: DSN = %q, want verbatim source %q", driver, got, src)
		}
	}
}

func TestOpen_WithSource(t *testing.T) {
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Source: ":memory:"})
	if err != nil {
		t.Fatalf("open with source: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO t (id) VALUES (1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
}
