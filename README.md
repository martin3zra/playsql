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
// offset pagination — typed: TypedPage[User]{ Items, Total, Page, PerPage, LastPage }
res, err := playsql.Query[User](db).OrderBy("id", playsql.Asc).Paginate(ctx, page, 20)

// offset — untyped: fills dest, returns metadata
var users []User
p, err := db.Model(&User{}).Paginate(ctx, &users, page, 20)

// cursor (keyset) pagination — seek-based, fast at any depth
res, err := playsql.Query[User](db).CursorPaginate(ctx, playsql.Cursor{
    Column: "id", After: lastID, Limit: 20, // After nil starts at the beginning
})
// res.Items, res.HasMore, res.NextCursor (pass as the next After)

// composite cursor — a non-unique sort key plus a unique tiebreaker
res, err := playsql.Query[User](db).CursorPaginate(ctx, playsql.Cursor{
    Keys:  []playsql.CursorKey{{Column: "created_at"}, {Column: "id"}},
    After: lastCursor, // []any{lastCreatedAt, lastID}; NextCursor returns the same shape
    Limit: 20,
})
```

Cursor pagination orders by the key(s) and seeks past `After` — no `OFFSET`, so
it stays fast for deep pages. A single `Column` must be unique and monotonic
(e.g. the primary key); paging by a non-unique column alone **skips rows on
ties**. Add a unique tiebreaker via composite `Keys` (e.g. `created_at` + `id`)
to page non-unique columns safely.

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

`UpdateReturning` updates and returns the affected rows, via `RETURNING`
(PostgreSQL, SQLite) or `OUTPUT INSERTED` (SQL Server). MySQL has no equivalent
and returns an error.

```go
var updated []User
db.Model(&User{}).Where("active", "=", false).
    Returning("id", "name").
    UpdateReturning(ctx, map[string]any{"active": true}, &updated)

// Generic form returns []T directly:
rows, _ := playsql.Query[User](db).
    WhereEq("active", false).
    Returning("id", "name").
    UpdateReturning(ctx, map[string]any{"active": true})
```

A `WITH` clause (CTE) can prefix an update — compute an aggregate once, then
update against it. `WithCTE` adds the CTE; `WhereRaw` references it. Both render
verbatim and must carry **no** bind parameters; never interpolate untrusted input.

```go
// Mark every product priced below the average as cheap.
db.Model(&Product{}).
    WithCTE("avg_price", "SELECT AVG(price) AS value FROM products").
    WhereRaw("price < (SELECT value FROM avg_price)").
    Update(ctx, map[string]any{"cheap": true})
```

## Raw queries

For statements the builder cannot express. `Exec` runs a raw write; `Raw` scans
a query into a slice of models (column→field mapping from metadata); `RawQuery`
is its generic form.

```go
db.Exec(ctx, `UPDATE users SET seen_at = now() WHERE id = $1`, id)

var users []User
db.Raw(ctx, &users, `SELECT * FROM users WHERE age > ?`, 18)

users, err := playsql.RawQuery[User](db, ctx, `SELECT * FROM users WHERE age > ?`, 18)
```

`Raw`/`RawQuery` scan into any `db`-tagged struct — it need not be a registered
model, so aggregates and joins map cleanly:

```go
type Report struct {
    Bucket string `db:"bucket"`
    Total  int64  `db:"total"`
}
var rows []Report
db.Raw(ctx, &rows, `SELECT region AS bucket, SUM(amount) AS total FROM sales GROUP BY region`)
```

A single value comes back via `RawScalar`; for shapes none of these fit, drop to
`RawRows` and scan the `*sql.Rows` yourself (you own them — `Close` when done).

```go
n, _ := playsql.RawScalar[int64](db, ctx, `SELECT COUNT(*) FROM users`)

rows, _ := db.RawRows(ctx, `SELECT name, age FROM users`)
defer rows.Close()
for rows.Next() { /* rows.Scan(&name, &age) */ }
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

### Filtering by relationship existence

Limit parents by whether a related row exists — compiled as a correlated
`EXISTS` subquery, no JOIN. `Has`/`DoesntHave` check presence/absence; `WhereHas`
adds constraints to the related query; `HasCount` compares the related count.
Relations are named by field and may be dotted for nesting.

```go
// posts that have at least one comment
playsql.Query[Post](db).Has("Comments").Get(ctx)

// posts with three or more comments
playsql.Query[Post](db).HasCount("Comments", ">=", 3).Get(ctx)

// posts with no comments
playsql.Query[Post](db).DoesntHave("Comments").Get(ctx)

// posts having a comment whose content matches
playsql.Query[Post](db).WhereHas("Comments", func(q *playsql.Builder) {
    q.Where("content", "like", "code%")
}).Get(ctx)

// shorthand for a single related condition
playsql.Query[Post](db).WhereRelation("Comments", "approved", "=", false).Get(ctx)

// nested: posts having a comment that has an image
playsql.Query[Post](db).Has("Comments.Images").Get(ctx)
```

Works the same across every relation kind:

```go
// belongsTo — comments that belong to an existing post
playsql.Query[Comment](db).Has("Post").Get(ctx)

// belongsToMany — users that have any role (through the pivot)
playsql.Query[User](db).Has("Roles").Get(ctx)
// ...users with three or more roles (counts pivot rows)
playsql.Query[User](db).HasCount("Roles", ">=", 3).Get(ctx)
// ...users having the "admin" role
playsql.Query[User](db).WhereRelation("Roles", "name", "=", "admin").Get(ctx)

// hasManyThrough — countries that have posts (through users)
playsql.Query[Country](db).Has("Posts").Get(ctx)
// ...countries with ten or more posts (counts far rows exactly)
playsql.Query[Country](db).HasCount("Posts", ">=", 10).Get(ctx)
// ...countries with no posts
playsql.Query[Country](db).DoesntHave("Posts").Get(ctx)
```

Also: `OrHas`, `OrDoesntHave`, `OrWhereHas`, `OrWhereDoesntHave`,
`WhereHasCount`, `OrWhereRelation`. Soft-deleted related rows are excluded by
default. All relation kinds are supported — `hasMany`, `hasOne`, `belongsTo`,
`belongsToMany`, `hasManyThrough`, `hasOneThrough` — compiled as nested `EXISTS`
through the pivot/intermediate table (no JOIN). The count form is not allowed on
nested (dotted) paths. `belongsToMany` counts pivot rows (equal to the related
count for a duplicate-free pivot); `has*Through` counts the far rows exactly via
an `IN` subquery.

### Aggregating related models

Pull a per-parent aggregate over a relation as an extra column, without loading
the related rows — `WithCount`, `WithSum`, `WithAvg`, `WithMin`, `WithMax`,
`WithExists` (each a correlated subquery, no JOIN). The default column name is
`{relation}_{func}[_{column}]` (`comments_count`, `comments_sum_votes`,
`comments_exists`); override with `As`, constrain with `Constrain`.

```go
playsql.Query[Post](db).
    WithCount("comments").
    WithSum("comments", "votes").
    WithExists("comments").
    WithCount("comments", playsql.As("pending_count"),
        playsql.Constrain(func(q *playsql.Builder) { q.WhereEq("approved", false) })).
    Get(ctx)
```

The result has two possible homes, and they coexist:

- **Typed field** — declare a `db`-tagged field matching the column name and tag
  it `play:"readonly"` (scanned, never written, excluded from default selects):

  ```go
  type Post struct {
      playsql.Model
      ID            int64 `db:"id" play:"pk,incrementing"`
      CommentsCount int64 `db:"comments_count" play:"readonly"`
  }
  // post.CommentsCount
  ```

- **Dynamic bag** — if the model embeds `playsql.Model` and has no matching
  field, the value lands in an aggregate bag:

  ```go
  n, ok := post.Aggregate("comments_count") // raw value
  post.CountOf("comments")                  // -> comments_count as int64
  post.SumOf("comments", "votes")           // -> comments_sum_votes as int64
  ```

Works across all relation kinds (many-to-many and through aggregate over the
related/far table via an `IN` subquery). Deferred post-fetch loading
(`loadCount`-style) is not yet available.

## Soft deletes

Add a `deleted_at` field tagged `play:"softdelete"`. Queries then exclude
trashed rows automatically; `Delete` stamps `deleted_at`.

```go
.WithTrashed()    // include trashed
.OnlyTrashed()    // only trashed
db.Restore(ctx, &u)
db.ForceDelete(ctx, &u)
```

## Global scopes

A model can declare scopes that are applied to every query automatically — for
multi-tenancy, active-only filters, etc. A scope reads request-scoped values
from `ctx` and may reject the query (so a missing tenant fails instead of leaking
rows).

```go
func (Post) Scopes() []playsql.Scope { return []playsql.Scope{TenantScope{}} }

type TenantScope struct{}
func (TenantScope) Apply(ctx context.Context, b *playsql.Builder) error {
    tid, ok := ctx.Value(tenantKey{}).(int64)
    if !ok { return errors.New("no tenant in context") }
    b.WhereEq("tenant_id", tid)
    return nil
}

db.Model(&Post{}).Get(ctx, &posts)              // scoped to the ctx tenant
db.Model(&Post{}).WithoutGlobalScopes().Get(...) // bypass
```

Scopes apply to reads, mass updates, and deletes; user predicates are grouped so
a scope can't be escaped by an `OR`.

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
