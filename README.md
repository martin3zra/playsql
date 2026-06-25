# playsql

[![CI](https://github.com/martin3zra/playsql/actions/workflows/ci.yml/badge.svg)](https://github.com/martin3zra/playsql/actions/workflows/ci.yml)

An Eloquent-style ORM for Go — concurrency-safe, no global state, metadata-driven.
Four dialects (SQLite, PostgreSQL, MySQL, SQL Server), eager-loaded relationships
with no N+1, and an optional generic query API.

```go
users, err := playsql.Query[User](db).
    WhereEq("active", true).
    With("Posts").
    OrderBy("created_at", playsql.Desc).
    Get(ctx)
```

## Install

```bash
go get github.com/martin3zra/playsql
```

playsql imports **no** SQL drivers — add the one you use:

```go
import (
    _ "modernc.org/sqlite"               // sqlite   (pure Go, no CGO)
    _ "github.com/lib/pq"                // postgres
    _ "github.com/go-sql-driver/mysql"   // mysql
    _ "github.com/microsoft/go-mssqldb"  // sqlserver
)
```

## Connect

Provide a full DSN via `Source`, or the individual fields:

```go
db, err := playsql.Open(playsql.Config{
    Driver: playsql.Postgres,
    Source: "postgres://user:pass@localhost:5432/app?sslmode=disable",
})
defer db.Close()
```

| Driver | `Config.Driver` | example `Source` |
|---|---|---|
| SQLite | `playsql.SQLite` | `app.db` or `:memory:` |
| PostgreSQL | `playsql.Postgres` | `postgres://user:pass@host:5432/db?sslmode=disable` |
| MySQL | `playsql.MySQL` | `user:pass@tcp(host:3306)/db?parseTime=true` |
| SQL Server | `playsql.SQLServer` / `playsql.MSSQL` | `sqlserver://user:pass@host:1433?database=db` |

Pool settings: `MaxOpenConns`, `MaxIdleConns`, `ConnMaxLifetime`.

## Models

A model is a plain struct. Columns come from the `db` tag (else `json`, else
snake_case of the field); the table is `TableName()` if defined, otherwise the
snake-cased, pluralized type name.

```go
type User struct {
    playsql.Model                          // optional: exists + dirty tracking
    ID        int64     `db:"id" play:"pk,incrementing"`
    Name      string    `db:"name" play:"fillable"`
    Email     string    `db:"email" play:"fillable"`
    Role      string    `db:"role" play:"guarded"`
    CreatedAt time.Time `db:"created_at"`
    UpdatedAt time.Time `db:"updated_at"`
}
```

`play` tag options: `pk`, `incrementing`, `fillable`, `guarded`, `softdelete`,
`cast=<name>`, `pivot`, and the relation kinds below. `created_at`/`updated_at`
columns are managed automatically.

Embed `playsql.Model` to opt into `exists` tracking (so `Save` knows insert vs
update) and dirty tracking (so `Update` writes only changed columns).

## Query

Two equivalent styles — typed (`Query[T]`) or untyped (`db.Model`):

```go
// typed: returns []User
users, err := playsql.Query[User](db).WhereEq("active", true).Get(ctx)

// untyped: scans into a destination
var users []User
err := db.Model(&User{}).WhereEq("active", true).Get(ctx, &users)
```

Predicates (all bound, never interpolated):

```go
.Where("age", ">", 18)
.WhereEq("status", "active").OrWhere("status", "=", "trial")
.WhereIn("id", 1, 2, 3)            // or WhereIn("id", []int64{1,2,3})
.WhereNull("deleted_at").WhereNotNull("verified_at")
.WhereBetween("age", 18, 65)
.WhereGroup(func(q *playsql.Builder) { q.WhereEq("a", 1).OrWhere("b", "=", 2) })
.WhereJSON("prefs", "theme", "=", "dark")   // JSON column path
```

Shaping + terminals: `Select`, `OrderBy`, `Limit`/`Offset` (`Take`/`Skip`),
`Get`, `First`, `Find(id)`, `Count`, `Paginate`.

```go
// typed: TypedPage[User]{ Items, Total, Page, PerPage, LastPage }
res, err := playsql.Query[User](db).OrderBy("id", playsql.Asc).Paginate(ctx, page, 20)

// untyped: fills dest, returns metadata
var users []User
p, err := db.Model(&User{}).Paginate(ctx, &users, page, 20)
```

## Write

Struct-based (full model lifecycle, fires hooks):

```go
u := &User{Name: "Jane", Email: "jane@x.com"}
db.Insert(ctx, u)        // sets generated id + timestamps
u.Name = "Janet"
db.Save(ctx, u)          // insert or update; updates only changed columns
db.Delete(ctx, u)        // soft-deletes if the model is soft-deletable
```

Map-based (mass assignment from a request/form; respects fillable/guarded):

```go
db.Model(&User{}).Insert(ctx, data)              // -> id
db.Model(&User{}).Create(ctx, &u, data)          // insert + hydrate
db.Model(&User{}).Where(...).Update(ctx, data)   // -> rows affected
db.Model(&User{}).InsertMany(ctx, []data)        // bulk
db.Model(&User{}).Upsert(ctx, rows, conflictCols, updateCols)
```

## Relationships

Declared with `play` tags; eager-loaded with `With`, each as one batched query
(no N+1):

```go
type Blog struct {
    ID       int64      `db:"id" play:"pk,incrementing"`
    Comments []*Comment `play:"hasMany"`
    Author   *User      `play:"belongsTo"`
    Tags     []*Tag     `play:"belongsToMany,pivot=blog_tag,withPivot=added_at"`
}

db.Model(&Blog{}).With("Comments", "Author").Get(ctx, &blogs)
db.Model(&Blog{}).With("Comments.Author").Get(ctx, &blogs)          // nested
db.Model(&Blog{}).WithConstraint("Comments", func(q *playsql.Builder) {
    q.WhereEq("approved", true)
}).Get(ctx, &blogs)                                                  // constrained
```

Kinds: `hasOne`, `hasMany`, `belongsTo`, `belongsToMany` (with `withPivot`),
`hasOneThrough`, `hasManyThrough`. Keys follow Eloquent conventions and are
overridable (`foreignKey=`, `localKey=`, `through=`, …).

## Soft deletes

Add a `deleted_at` field tagged `play:"softdelete"`. Queries then exclude
trashed rows automatically; `Delete` stamps `deleted_at`.

```go
.WithTrashed()    // include trashed
.OnlyTrashed()    // only trashed
db.Restore(ctx, &u)
db.ForceDelete(ctx, &u)
```

## Casts

`play:"cast=json"` (de)serializes struct/slice/map columns. Register custom
casts:

```go
playsql.RegisterCaster("csv", csvCaster{}) // implements Decode/Encode
// type Settings struct{ Tags []string `db:"tags" play:"cast=csv"` }
```

## Hooks

Implement any subset; a `Before*` error aborts the operation.

```go
func (u *User) BeforeCreate(ctx context.Context) error { /* validate */ return nil }
func (u *User) AfterDelete(ctx context.Context) error  { /* cleanup  */ return nil }
```

## Transactions

```go
err := db.Tx(ctx, func(tx *playsql.Tx) error {
    if err := tx.Save(ctx, &a); err != nil {
        return err // rolls back
    }
    return tx.Save(ctx, &b)
})
```

The closure receives a `*Tx`, so transaction code cannot accidentally run on a
non-transactional connection.

## Testing

- `make test` — unit + in-memory SQLite, no external databases.
- `make test-int` — live integration against Postgres/MySQL/SQL Server
  (`make db-up` first, or point `PLAYSQL_*_DSN` at running instances).

## Design principles (enforced invariants)

1. No global mutable state — everything lives on a `session` (`DB` / `Tx`).
2. Metadata is immutable after parse and the single source of truth.
3. Reflection happens once per type, only inside `metadata/`.
4. Fixed pipeline: `ModelMeta → Builder → Grammar → Execution → Scanner → Hydration`.
5. Builder is the only query entry point.

See [DESIGN.md](DESIGN.md) for the full architecture.

## License

MIT — see [LICENSE](LICENSE).
