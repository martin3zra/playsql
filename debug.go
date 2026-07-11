package playsql

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

// Logger is the sink Debug() and DD() write to. Implement it to route playsql's
// diagnostic output into an existing logging stack; when a builder has no logger
// the package falls back to defaultLogger (the standard library log package).
type Logger interface {
	Printf(format string, args ...any)
}

type defaultLogger struct{}

func (defaultLogger) Printf(format string, args ...any) { log.Printf(format, args...) }

// log returns the builder's logger, or the package default.
func (b *Builder) log() Logger {
	if b.logger != nil {
		return b.logger
	}
	return defaultLogger{}
}

// DumpAndDie is the panic value raised by DD(). It halts execution before the
// query is sent and carries the rendered dump so a recovering caller (a test,
// an HTTP recover middleware) can inspect exactly what would have run.
type DumpAndDie struct {
	Dump string
}

func (DumpAndDie) Error() string { return "playsql: dump and die" }

// --- render helpers (never execute) ---

// render compiles the current builder state to SQL and bind args without
// running anything. Global scopes are only reflected once a terminal has
// applied them; SQL()/Args() therefore show the query as composed so far.
func (b *Builder) render() (string, []any) {
	return b.sess.grammar.CompileSelect(b.compiled())
}

// SQL returns the SELECT the builder would run, without executing it.
func (b *Builder) SQL() string { s, _ := b.render(); return s }

// Args returns the bind arguments SQL() would carry, without executing.
func (b *Builder) Args() []any { _, a := b.render(); return a }

// Tap invokes fn with the builder and returns the same builder, so a side
// effect (logging, assertion, conditional shaping) can sit inline in a chain
// without breaking it. Tap mutates nothing itself; fn may.
func (b *Builder) Tap(fn func(*Builder)) *Builder {
	if fn != nil {
		fn(b)
	}
	return b
}

// Debug enables SQL logging for this builder only. Each statement the builder
// executes is timed and printed (SQL, args, duration). It swaps in a private
// session wrapping the runner, so global state and sibling builders are
// untouched.
func (b *Builder) Debug() *Builder {
	b.useRunner(debugRunner{next: b.sess.run, logger: b.log()})
	return b
}

// DD dumps the pending query and stops execution by panicking with DumpAndDie,
// before the statement reaches the database. Like Debug it works by wrapping the
// runner, so any terminal (Get/First/Count/…) triggers the dump uniformly.
func (b *Builder) DD() *Builder {
	b.useRunner(dumpRunner{b: b, logger: b.log()})
	return b
}

// useRunner replaces the builder's session with a clone whose runner is r. Only
// this builder is affected; the grammar and transaction flag are preserved.
func (b *Builder) useRunner(r runner) {
	b.sess = &session{run: r, grammar: b.sess.grammar, inTx: b.sess.inTx}
}

// --- Debug: timing/logging runner ---

// debugRunner times and logs every statement, then delegates to the real runner.
type debugRunner struct {
	next   runner
	logger Logger
}

func (d debugRunner) emit(query string, args []any, start time.Time) {
	d.logger.Printf("[playsql]\n\nSQL:\n%s\n\nArgs:\n%v\n\nDuration:\n%s\n",
		query, args, time.Since(start))
}

func (d debugRunner) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := d.next.QueryContext(ctx, query, args...)
	d.emit(query, args, start)
	return rows, err
}

func (d debugRunner) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	start := time.Now()
	row := d.next.QueryRowContext(ctx, query, args...)
	d.emit(query, args, start)
	return row
}

func (d debugRunner) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	res, err := d.next.ExecContext(ctx, query, args...)
	d.emit(query, args, start)
	return res, err
}

// --- DD: dump-and-die runner ---

// dumpRunner renders a rich dump of the builder plus the compiled statement and
// panics with DumpAndDie instead of touching the database.
type dumpRunner struct {
	b      *Builder
	logger Logger
}

func (d dumpRunner) dumpAndDie(query string, args []any) {
	dump := d.b.renderDump(query, args)
	d.logger.Printf("%s", dump)
	panic(DumpAndDie{Dump: dump})
}

func (d dumpRunner) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	d.dumpAndDie(query, args)
	return nil, nil // unreachable
}

func (d dumpRunner) QueryRowContext(_ context.Context, query string, args ...any) *sql.Row {
	d.dumpAndDie(query, args)
	return nil // unreachable
}

func (d dumpRunner) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	d.dumpAndDie(query, args)
	return nil, nil // unreachable
}

// renderDump builds the DD() report: the compiled SQL and bindings plus the
// builder's model, table, selected columns, eager loads and global scopes.
func (b *Builder) renderDump(query string, args []any) string {
	var sb strings.Builder
	sb.WriteString("[playsql] dump and die\n\n")

	sb.WriteString("Model:\n")
	sb.WriteString(b.meta.StructName)
	sb.WriteString("\n\nTable:\n")
	sb.WriteString(b.meta.Table)

	cols := b.columns
	if len(cols) == 0 {
		cols = b.meta.ColumnNames()
	}
	sb.WriteString("\n\nColumns:\n")
	sb.WriteString(strings.Join(cols, ", "))

	sb.WriteString("\n\nSQL:\n")
	sb.WriteString(query)

	sb.WriteString("\n\nBindings:\n")
	fmt.Fprintf(&sb, "%v", args)

	if len(b.withs) > 0 {
		eager := make([]string, len(b.withs))
		for i, w := range b.withs {
			eager[i] = strings.Join(w.segments, ".")
		}
		sb.WriteString("\n\nEager loads:\n")
		sb.WriteString(strings.Join(eager, ", "))
	}

	if !b.skipScopes && len(b.scopes) > 0 {
		names := make([]string, len(b.scopes))
		for i, s := range b.scopes {
			names[i] = fmt.Sprintf("%T", s)
		}
		sb.WriteString("\n\nScopes:\n")
		sb.WriteString(strings.Join(names, ", "))
	}

	sb.WriteString("\n")
	return sb.String()
}
