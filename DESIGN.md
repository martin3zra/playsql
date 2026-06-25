# playsql v2 — target package design

Plan to extract `playsql` as a standalone, concurrency-safe, Eloquent-style ORM.
No code changed yet — this is the target.

## Module layout

```
playsql/
  go.mod                      // own module, own deps
  db.go                       // DB (connection manager) — replaces all globals
  config.go                   // Config struct (injected, no env reads)
  builder.go                  // query Builder — owns columns/wheres/joins/values
  model.go                    // Model — identity, attributes, persistence, hooks
  scope.go                    // Scope contract (scopes declared in metadata, no registry)
  metadata/
    cache.go                  // type-keyed immutable metadata cache (sync.Map)
    parser.go                 // parse struct tags + interface -> ModelMeta (once per type)
  grammar/
    grammar.go                // Grammar interface + shared compile
    mysql.go postgres.go sqlite.go sqlserver.go
  scan.go                     // typed row scanning (no JSON round-trip)
  relations/
    relation.go has_one.go has_many.go belongs_to.go belongs_to_many.go
  errors.go expression.go     // mostly as-is
```

## Core: kill the globals — DB / Tx split over a shared session

`context.go` today holds `instance`, `Connection`, `dbConfig`, `txInstance`,
`isPretending`, `queryLog`, plus `placeholder`/`toReplaceFromPlaceholder` in
`driver.go`. All package globals → one connection per process, not concurrency
safe, one transaction at a time for the whole process.

`DB` and `Tx` are **separate types** (a `Tx` is not closable/poolable; a `DB`
isn't commit/rollback-able) sharing a `session` that carries everything
connection-level *except* the executor. The executor is the only thing that
differs, behind a `runner` interface. `Builder` depends on `*session`, never on
`*DB` — so it can't tell whether it runs in a tx, which is correct.

```go
// runner = the only thing that differs between a pooled conn and a tx
type runner interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// session = shared config + whichever runner. Query entry lives here, once.
type session struct {
	run     runner          // *sql.DB or *sql.Tx
	grammar grammar.Grammar
	logger  QueryLogger      // nil = no logging; per-session, not global queryLog
	dbName  string
}

type DB struct {
	*session
	sql *sql.DB // DB-only: BeginTx / Close / pool config
}

type Tx struct {
	*session // run is the *sql.Tx; no Close, no Open
}

func Open(cfg Config) (*DB, error) {        // returns error, never log.Fatal
	conn, err := sql.Open(cfg.Driver.dsn(), cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("playsql: open: %w", err)
	}
	conn.SetMaxOpenConns(cfg.MaxOpenConns)
	conn.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	if err := conn.PingContext(context.Background()); err != nil {
		return nil, fmt.Errorf("playsql: ping: %w", err)
	}
	g := grammarFor(cfg.Driver)
	return &DB{sql: conn, session: &session{run: conn, grammar: g, dbName: cfg.Database}}, nil
}
```

Query entry is defined **once** on `*session`, inherited identically by `DB` and
`Tx` — no duplicated query API:

```go
func (s *session) Model(self any) *Builder { return newBuilder(s, self) }

func (db *DB) Tx(ctx context.Context, fn func(*Tx) error) (err error) {
	sqlTx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	tx := &Tx{session: &session{run: sqlTx, grammar: db.grammar, logger: db.logger, dbName: db.dbName}}
	defer func() {
		if p := recover(); p != nil {
			sqlTx.Rollback()
			panic(p)
		}
		if err != nil {
			sqlTx.Rollback()
		} else {
			err = sqlTx.Commit()
		}
	}()
	return fn(tx)
}
```

The closure receives `*Tx`, not `*DB` — so transaction code physically cannot
reach `Close`/pool ops or a non-tx runner. Buys concurrency, multi-DB, and
tx-safety by construction. Replaces global `Transaction`/`Begin`/`Rollback`.

Savepoints (Eloquent's nested transactions) are reserved for later: a future
`(*Tx).Tx(ctx, fn)` issues `SAVEPOINT`/`ROLLBACK TO` rather than `BEGIN`. Not
Phase 1 — but the `Tx` type exists so it has somewhere to live.

## Models are session-rooted (decision) — no DB reference on the model

A `Model` holds **zero** connection reference. Every query and persistence op is
rooted in a `DB` or `Tx`:

```go
var users []User
err := db.Model(&User{}).WhereEq("active", true).Get(ctx, &users)

u := &User{Name: "Jane"}
err = db.Save(ctx, u)

db.Tx(ctx, func(tx *playsql.Tx) error {
	return tx.Save(ctx, u) // *Tx is the only handle in scope
})
```

Why session-rooted over an injected-session-on-model:
- **Tx-safety is unrepresentable-by-construction.** A model carrying a `*DB`,
  saved inside a `Tx` closure, silently writes on the *non-tx* connection — the
  classic Eloquent footgun. With no `u.Save`, there is no connection to get
  wrong. A safety rule a human must remember (re-bind before save) is a bug that
  ships; the compiler should enforce it instead.
- Model becomes pure data + identity + dirty state — genuinely "identity only."
- The generic façade needs it anyway: `Query[User](db)` has no instance to hang a
  session on. Session-rooting is where Phase 5 forces you regardless.
- Eager-loaded children get the session passed down the load tree — clean —
  instead of injecting a `*DB` into every hydrated child.

Cost: `db.Save(ctx, u)` / `db.Model(&User{})` instead of `u.Save(ctx)`. More
verbose, less magic. For a library whose v2 thesis is *kill hidden state*, that
verbosity is the feature.

## Split Model from Builder

Today `Model` holds query state (`columns`, `items`, `values`) → leaks between
queries on a reused instance; `wipeInstance` runs only on some paths. Eloquent
splits Model from Query\Builder. Mirror it:

```go
// builder.go — fresh per query, never reused
type Builder struct {
	sess    *session          // not *DB: agnostic to conn-vs-tx
	meta    *metadata.ModelMeta // resolved once, drives table/keys/scopes/casts
	columns []string
	wheres  []where
	joins   []join
	orders  []order
	limit, offset int
	withs   []withClause      // supports nesting + constraint closures
	trashed trashedMode       // none | with | only — checked by soft-delete compile
}

// ctx is passed to terminal ops, not stored on the builder
func (b *Builder) Where(col string, op Expression, v any) *Builder { /* ... */ return b }
func (b *Builder) WhereEq(col string, v any) *Builder { return b.Where(col, Equals, v) }
func (b *Builder) OrderBy(col string, dir Direction) *Builder { /* ... */ return b }
func (b *Builder) WithTrashed() *Builder { b.trashed = trashedWith; return b }
func (b *Builder) OnlyTrashed() *Builder { b.trashed = trashedOnly; return b }

// terminal ops take ctx + a typed destination
func (b *Builder) Get(ctx context.Context, dest any) error          { /* ... */ }
func (b *Builder) First(ctx context.Context, dest any) error         { /* ... */ }
func (b *Builder) Find(ctx context.Context, dest, id any) error      { /* ... */ }
func (b *Builder) Count(ctx context.Context) (int64, error)          { /* ... */ }  // no panic
func (b *Builder) Paginate(ctx context.Context, dest any, perPage, page int) (*Paginator, error) { /* ... */ }
```

`Model` keeps identity/config/persistence only:

```go
// model.go — identity + dirty state only. No DB, no config (config is in metadata).
type Model struct {
	exists   bool           // drives Save = insert vs update
	original map[string]any // dirty tracking
}
```

Config (table, primary key, timestamps, guarded, soft-delete, scopes) does **not**
live here — it's static, resolved into `metadata.ModelMeta` (see below). Query
and persistence are session-rooted, not methods on the model:

```go
db.Model(&User{}).WhereEq("active", true).OrderBy("id", Asc).Get(ctx, &users)
db.Save(ctx, &user)
```

## Grammar interface — add identifier quoting

Fixes the SQL-injection surface. Today `OrderBy`, `Select` columns, table names,
and join constraints are string-interpolated with no quoting/escaping. Every
identifier flows through `Wrap`:

```go
// grammar/grammar.go
type Grammar interface {
	CompileSelect(*Query) (string, []any)   // Query = neutral compiled struct, not *Model
	CompileInsert(*Query) (string, []any)
	CompileUpdate(*Query) (string, []any)
	CompileDelete(*Query) (string, []any)
	Wrap(identifier string) string          // `col` / "col" / [col]
	Placeholder(n int) string               // ?  |  $1  |  @p1
	CompileLimit(limit, offset int) string
}
```

`Placeholder(n)` generates numbered binds during compile — kills the `$-`/`@p-`
count-and-replace hack in `computedPlaceholderIfNeeded` and the broken MySQL
debug binding. The MySQL LIMIT bug (limit-only branch emits `%d` without the
`LIMIT` keyword) disappears in one correct `CompileLimit`.

## Typed scanning — drop the JSON round-trip

Replace `scanner.go`'s `decode` (map -> JSON -> struct). It is slow, loses types,
and can't handle `sql.NullX`, `driver.Valuer`/`sql.Scanner`, or `time.Time`
precision cleanly. Build a column->field-index map once via reflection, scan
directly into addressable fields:

```go
// scan.go
func scanRows(rows *sql.Rows, dest any) error {
	// dest = *[]T or *[]*T
	// 1. reflect element type, build tag->fieldIndex once
	// 2. for each row: make dest pointers per column, rows.Scan into them
	// 3. handle Scanner/NullX natively
}
```

Or adopt `github.com/georgysavva/scany/v2/sqlscan` and delete `scanner.go` +
`fulfillment` + `B2S` entirely. Recommend scany — battle-tested, less reflection
to own.

## Relations — finish + nest

```go
// relations/relation.go — relations are STATELESS/immutable
type Relation interface {
	Name() string                                // the struct-field name
	AddEagerConstraints(parents []*Model)
	Match(parents []*Model, results any) error   // map children back to parents
	Build() *Builder
}
```

- Implement the three stubs that currently return nil in
  `getRelationInstanceBaseOnType` (`belongsToMany`, `hasOneThrough`,
  `hasManyThrough`) → no more nil deref.
- `Name()` comes from the **struct field** (already available in
  `compileRelationships`), not the `system.GetCaller()` backtrace.
- `withClause` carries a constraint closure → `With("posts", func(b *Builder){
  b.WhereEq("published", true) })` and dotted nesting `With("posts.comments")`.

**No `IsLoaded()` on the relation.** "Already loaded?" is a property of a parent
instance's field, and cycle/duplicate prevention is a property of the **eager-load
plan**, not the relation. The loader walks the requested dotted paths against a
`visited map[string]bool` and refuses to re-descend a path — this kills duplicate
loads and N+1 recursion while keeping relations immutable. Putting mutable
`IsLoaded` state on a shared relation would break that immutability.

## Config — injected, not env

`driver.go`'s `env.Get`/`config` reads move out. Caller supplies:

```go
// config.go
type Config struct {
	Driver             Driver
	Host, Database     string
	Username, Password string
	Port               int
	MaxOpenConns       int
	ConnMaxLifetime    time.Duration
}
func (c Config) DSN() string { /* per-driver */ }
```

Host app maps its env -> Config. Package has zero knowledge of `elysium/env` or
`elysium/config`.

## Dependency cuts (for extraction)

playsql currently imports 11 sibling packages. Target:

| current dep                                          | action                                            |
| ---------------------------------------------------- | ------------------------------------------------- |
| `is`, `str`, `array`, `ensure`, `class`, `system`, `output` | inline the few funcs used -> drop          |
| `env`, `config`                                      | replace with injected `Config`                    |
| `to`                                                 | replaced by typed scan / `encoding/json` only     |
| `collection`, `pagination`                           | keep — move in as `paginate.go`, or own small mods |

Result: stdlib + driver packages (`go-sql-driver/mysql`, `lib/pq`,
`mattn/go-sqlite3`, `denisenkom/go-mssqldb`) only.

## Hooks — context-aware interface

```go
type Hooks interface {
	BeforeSave(ctx context.Context) error   // return error to abort
	AfterSave(ctx context.Context) error
	BeforeDelete(ctx context.Context) error
	AfterDelete(ctx context.Context) error
}
```

Errors abort the op (today hooks return nothing). Fire on **update and delete
too** (today only create fires), inside the same tx. Add soft-delete support on
top of the existing `deletedAt` scaffolding.

## Tests

Add a `sqlite :memory:` path (un-panic `forSQLite` in `driver.go`) → CI runs with
no external DB. Keep MySQL/PG integration tests behind a build tag
(`//go:build integration`).

## Known bugs to fix in passing

- `DisableQueryLog` sets `loggingQueries = true` (copy-paste of Enable).
- MySQL `CompileLimit` limit-only branch returns `%d` without `LIMIT`.
- `RawQuery` defers `rows.Close()` before the `err != nil` check → nil panic.
- `composeFindConstraint` hard-asserts `value[0].(int64)` → panic on int/string.
- `First` hardcodes `table.id` instead of `GetKeyName()`.
- Postgres insert hardcodes `RETURNING ID` instead of the primary key.
- `readModel` field-skip condition looks inverted (`!IsZero() && !CanInterface()`).
- `Save` reflection assumes exact `ID`/`CreatedAt` field names → panic otherwise.
- `compileQueryItems` OR-group values path is dead code.
- `max` redefined despite Go 1.21+ builtin (module on go1.24).
- `ensureMethodExists` uses a value receiver (copies the struct).
- `DoesntExists` -> `DoesntExist`; `Exists`/`DoesntExists` return always-nil error.

## Metadata layer — the foundation

The single most important subsystem. Today reflection runs **per query**
(`readModel`, `fulfillment`, `getRelationship`, `eagerLoadRelations` all re-walk
struct fields every call). Parse once per type, cache forever; every other
subsystem consumes metadata instead of re-reflecting.

```go
// metadata/cache.go
type ModelMeta struct {
	Table        string
	PrimaryKey   string
	Incrementing bool
	Timestamps   bool
	SoftDeletes  bool
	Columns      []ColumnMeta            // db name, field index, cast, nullable
	Relations    map[string]RelationMeta // parsed from the `play` tag
	fieldByCol   map[string]int          // scanner hot path
	colByField   map[int]*ColumnMeta
}

var cache sync.Map // reflect.Type -> *ModelMeta

func For(t reflect.Type) *ModelMeta { /* parse-once, lazy */ }
```

Source of truth for: scanning, relations, eager loading, scopes, casts, dirty
tracking, persistence, generics. The `play`-tag relationship parsing currently
in `compilers.go:getRelationship` **moves into `metadata/parser.go`** and runs
once — it is the single place tags become `RelationMeta`. Phase 1 deliverable.

Global `sync.Map` keyed by `reflect.Type` is safe **because metadata is
immutable and type-derived** — unlike connection state (which is per-`DB`) and
scope registries (which are per-`DB`, see below). This distinction only holds if
model config is static (next section).

## Model config is static (decision)

Config — guarded/fillable, timestamps, primary key, incrementing, soft-deletes —
is a property of the **type**, read from struct tags + a marker interface. The
imperative setters (`SetGuarded`, `SetUseTimestamps`, `SetKeyName`,
`SetIncrementing`, `SetFillable`) are **removed** from the public API.

```go
type User struct {
	playsql.Model
	ID    int64    `play:"pk,incrementing"`
	Name  string   `play:"fillable"`
	Roles []*Role  `play:"belongsToMany,pivot=role_user"`
}
func (User) Timestamps() bool  { return true }
func (User) Guarded() []string { return []string{"id"} }
```

Why static over imperative:
- A type-keyed metadata cache is only sound if config can't differ per instance.
  Imperative setters make two instances of one type configurable differently —
  first-to-parse wins, later config silently ignored. Static config removes the
  contradiction.
- Eloquent-faithful (config lives on the class, not the object).
- Required for generics: `Query[User](db)` has no instance to call setters on.
- Shrinks the oversized `ModelContract` interface.

**Breaking change**: every model constructor in the host app changes
(`gest/`, `foundation/auth/user.go`, ~10 importers drop their `Set*` calls).
Deliberate and acceptable for a v2/standalone extraction — stated up front, not
discovered in Phase 5.

## Global scopes — declared in metadata, no registry

Multi-tenancy, active/inactive, partitioning. **Superseded the earlier
`db.RegisterGlobalScope` idea** — a mutable registry reintroduces global state and
the scope-ordering race (risk 3.3). Instead scopes are declared on the type via
the marker interface and parsed into immutable metadata:

```go
type Scope interface {
	Apply(ctx context.Context, b *Builder) error // error lets a scope reject bad context
}

func (User) Scopes() []playsql.Scope { return []playsql.Scope{TenantScope{}} }
// parser -> ModelMeta.Scopes []Scope  (immutable, deterministic order)
```

Static declaration, runtime value: the `TenantScope` object is a constant in
metadata; it reads the current tenant from `ctx` at `Apply` time. `Apply` returns
`error` so a tenancy scope with no tenant in `ctx` **aborts** the query rather
than silently producing an unscoped, data-leaking global query.

Trade-off (consistent with static config): you cannot attach a scope to a
third-party model you don't own. Acceptable — already true once setters are gone.

## Soft deletes — metadata-driven, not a scope

Driven by `ModelMeta.SoftDeletes`, not the scope mechanism — avoids
scope-ordering bugs and per-query overhead. When enabled, the soft-delete
compile step injects `deleted_at IS NULL` automatically. API:

- **Builder** (query modifiers): `WithTrashed()`, `OnlyTrashed()`, and a mass
  `db.Model(&X{}).Where(...).ForceDelete(ctx)`.
- **Session** (loaded-row ops, session-rooted): `db.Restore(ctx, &u)`,
  `db.ForceDelete(ctx, &u)`.

**Compile ordering rule:** the soft-delete filter is applied *first* and is gated
by the builder's `trashed` flag — `WithTrashed` skips the injection, `OnlyTrashed`
inverts it to `deleted_at IS NOT NULL`. Global scopes run *after* and must never
re-inject or override the soft-delete predicate. One deterministic order, owned by
the builder's compile step, not by scope execution.

## belongsToMany before through-relations, with pivot from day one

Many-to-many (Users↔Roles, Posts↔Tags, Products↔Categories) is everyday;
`hasOneThrough`/`hasManyThrough` are rare. Implement `belongsToMany` first, and
design pivot handling into it from the start, not as a later add-on:

```go
WithPivot("granted_at", "granted_by")  // extra pivot columns
WithTimestamps()                        // pivot created_at/updated_at
PivotModel(UserRole{})                  // typed pivot (later)
```

Pivot table name + keys come from `RelationMeta` (parsed from the
`pivot=role_user` tag).

## Generics — designed early, shipped as a deferred façade

Evaluate the generic surface now; keep the core reflection-based; add the typed
façade in the final phase over the stable foundation:

```go
users, err := playsql.Query[User](db).WhereEq("active", true).Get(ctx)
```

Generics give compile-time element typing and drop the `dest any` slice
reflection, but do **not** remove field-level reflection (column→field mapping
still needs metadata, or codegen in a future v3). Root queries go generic;
eager-loaded relations stay reflection-based (related type differs per relation).
A generic core rewrite — or codegen à la sqlc/ent to truly minimize reflection —
is a v3 conversation, not v2.

## Revised phase plan

### Phase 1 — Foundation
- DB struct; remove globals; per-call transactions
- `context.Context` on all runner methods
- Error handling (no `log.Fatal`/`panic`/`fmt.Println`)
- Metadata cache + parser (absorbs `play`-tag parsing)
- Static model config (tags + interface); drop `Set*` setters
- Query logger interface; connection-pool config

### Phase 2 — Core ORM
- Builder/Model separation
- Typed scanning (with a cast hook point; no JSON round-trip)
- Dirty tracking; Save = insert-or-update
- Soft deletes (metadata-driven)
- Global scopes (metadata-declared, ctx-aware)
- UUID / non-incrementing primary keys (fixes the `value[0].(int64)` panic) — must
  not slip later: it's a panic bug wired into `Save`/`Find`/relation keys

### Phase 3 — Relationships
- `belongsToMany` + pivot support
- Nested eager loading (`With("posts.comments")`)
- Relation constraint closures

### Phase 4 — Eloquent features
- Attribute casting (JSON, enum, encrypted, custom `Caster`)
- Hooks/observers (context-aware, error-aborting, on create+update+delete)
- JSON column queries
- Upsert
- `hasOneThrough` / `hasManyThrough`

### Phase 5 — Modern API layer
- Generic query façade (`Query[T](db)`)
- Reflection minimization
- Performance optimizations

Phases 1–2 deliver ~80% of the safety/correctness wins. The metadata layer in
Phase 1 is the keystone — relationships, eager loading, casts, scopes, dirty
tracking, soft deletes, and generics all consume it instead of reimplementing
reflection independently.

## Enforced invariants

These are not features — they are rules the architecture must not violate. Worth a
lint check / review checklist:

1. **No global mutable state.** No package-level connection, tx, query log, scope
   registry, or placeholder vars. Everything lives on a `session`.
2. **Metadata is immutable after parse, and the single source of truth.** Table,
   keys, columns, relations, casts, timestamps, soft-delete, scopes all resolve
   into `ModelMeta` from tags + interface. No runtime override path. No "dual
   behavior" where an instance disagrees with its cached type metadata.
3. **Reflection happens exactly once per type, only inside `metadata/`.** The
   `builder`, `model`, `scanner`, and grammar packages import `metadata` and must
   **never** parse struct tags or walk fields themselves. Reflection leakage
   outside `metadata/` is a defect.
4. **The pipeline order is fixed:** `ModelMeta → Builder → Grammar → Execution →
   Scanner → Hydration`. Any step that bypasses metadata reintroduces the
   per-call reflection and divergence bugs v2 exists to remove.
5. **Builder is the only query entry point. Model is identity + dirty state only.**
   SQL generation is grammar-owned. Connection selection is session-owned.
