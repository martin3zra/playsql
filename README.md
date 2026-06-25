# playsql

An Eloquent-style ORM for Go — concurrency-safe, no global state, metadata-driven.

> **Status: walking skeleton.** A single query path runs end-to-end
> (metadata → builder → grammar → execution → scanner). Breadth — more `WHERE`
> kinds, persistence, relationships, scopes, casts — builds on this spine. See
> [DESIGN.md](DESIGN.md) for the full architecture and phase plan.

## Example

```go
db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
if err != nil {
	log.Fatal(err)
}
defer db.Close()

type User struct {
	ID   int64  `db:"id" play:"pk,incrementing"`
	Name string `db:"name"`
	Age  int64  `db:"age"`
}
func (User) TableName() string { return "users" }

var users []User
err = db.Model(&User{}).WhereEq("age", int64(30)).Get(ctx, &users)
```

## Design principles (enforced invariants)

1. No global mutable state — everything lives on a `session` (`DB` / `Tx`).
2. Metadata is immutable after parse and the single source of truth.
3. Reflection happens once per type, only inside `metadata/`.
4. Fixed pipeline: `ModelMeta → Builder → Grammar → Execution → Scanner → Hydration`.
5. Builder is the only query entry point; Model is identity + dirty state only.

## Layout

```
db.go         // session / DB / Tx over a runner interface (no globals)
builder.go    // query builder — the only query entry point
scan.go       // typed row scanning via metadata (no JSON round-trip)
metadata/     // parse-once, cache-forever model metadata (the only reflection site)
grammar/      // SQL dialect: identifier quoting, placeholders, statement assembly
```

## License

TBD.
