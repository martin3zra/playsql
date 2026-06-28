package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

type cteProduct struct {
	playsql.Model
	ID     int64 `db:"id" play:"pk,incrementing"`
	Price  int64 `db:"price" play:"fillable"`
	OnSale bool  `db:"on_sale" play:"fillable"`
}

func (cteProduct) TableName() string { return "cte_products" }

func TestWithCTEQuery_BoundUpdate(t *testing.T) {
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE cte_products (id INTEGER PRIMARY KEY, price INTEGER, on_sale BOOLEAN DEFAULT 0)`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for _, p := range []cteProduct{{Price: 5}, {Price: 50}, {Price: 500}} {
		if err := db.Insert(ctx, &p); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// cheap = products under 100 (bound $? = 100). Then update on_sale for the
	// cheap ones also priced above 10 (another bound). Only price 50 qualifies:
	// 5 is excluded by price>10, 500 is not in cheap.
	cheap := db.Model(&cteProduct{}).Select("id").Where("price", "<", int64(100))
	n, err := db.Model(&cteProduct{}).
		WithCTEQuery("cheap", cheap).
		WhereRaw("id IN (SELECT id FROM cheap)").
		Where("price", ">", int64(10)).
		Update(ctx, map[string]any{"on_sale": true})
	if err != nil {
		t.Fatalf("cte update: %v", err)
	}
	if n != 1 {
		t.Fatalf("rows affected = %d, want 1", n)
	}

	onSale, err := playsql.RawQuery[cteProduct](db, ctx, `SELECT * FROM cte_products WHERE on_sale = 1`)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(onSale) != 1 || onSale[0].Price != 50 {
		t.Fatalf("on_sale rows wrong: %+v", onSale)
	}
}
