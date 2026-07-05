package playsql

import (
	"database/sql"
	"testing"

	"github.com/martin3zra/playsql/grammar"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, no CGO)
)

// TestUseGrammarByName proves the grammar comes from the dialect argument, not
// from the underlying driver: a "sqlite"-backed handle wrapped with "postgres"
// gets the Postgres grammar. This is why acme can run the postgres grammar over
// the txdb driver.
func TestUseGrammarByName(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer raw.Close()

	db, err := Use(raw, "postgres")
	if err != nil {
		t.Fatalf("Use: %v", err)
	}
	if _, ok := db.grammar.(grammar.Postgres); !ok {
		t.Fatalf("want grammar.Postgres, got %T", db.grammar)
	}
	if db.run != raw {
		t.Fatal("runner is not the passed *sql.DB")
	}
}
