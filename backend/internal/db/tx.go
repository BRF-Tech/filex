package db

import (
	"context"
	"database/sql"
	"errors"
)

// One transaction for many Store calls (issue #70).
//
// The catalogue writes a folder's listing one row at a time: a lookup, then an
// INSERT or an UPDATE, each its own implicit transaction. On SQLite every one
// of those ends in an fsync of the WAL; on PostgreSQL and MySQL in a commit
// round trip. Store.WithTx lets a caller put a whole folder's writes into ONE
// transaction without a second copy of any statement: the transaction travels
// in the context, and every statement every driver runs asks Conn which
// handle to use.
//
// ⚠ The context is the only thing that carries it. Everything that runs a
// statement through a Store while the transaction is open must be handed the
// context WithTx gave it — the wrappers (quotastore, identitystore) and the
// quota service built on the raw store already pass their context through,
// so they join it with no code of their own. Anything that reaches the same
// database through ANOTHER handle does not: on SQLite, where the pool has
// exactly one connection and the transaction is holding it, such a call waits
// for ever. The job queue is the case in point — it shares the application's
// *sql.DB but not this plumbing — so a caller enqueues (the search index's
// content hook, the antivirus) only after WithTx has returned.

// Querier is what one statement needs. *sql.DB and *sql.Tx are both one.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ErrNestedTx refuses a store method that opens a transaction of its own while
// the context already carries one on the same database. On SQLite it would
// wait for the connection the outer transaction holds, for ever.
var ErrNestedTx = errors.New("db: this operation opens its own transaction and cannot run inside another")

type txKey struct{}

type txState struct {
	pool *sql.DB
	tx   *sql.Tx
}

// Conn returns the transaction ctx carries on pool, or pool itself. A
// transaction on another database is not this one's business.
func Conn(ctx context.Context, pool *sql.DB) Querier {
	if t, ok := ctx.Value(txKey{}).(*txState); ok && t.pool == pool {
		return t.tx
	}
	return pool
}

// InTx reports whether ctx carries a transaction on pool.
func InTx(ctx context.Context, pool *sql.DB) bool {
	t, ok := ctx.Value(txKey{}).(*txState)
	return ok && t.pool == pool
}

// RunInTx runs fn in one transaction on pool: every statement fn runs through
// a Store on pool with the context it is handed is part of it. It commits when
// fn returns nil and rolls back otherwise (fn's error is returned). A context
// already carrying a transaction on pool runs fn in that one.
//
// ⚠ Do not keep fn's context: once RunInTx returns, a statement run with it
// fails (sql.ErrTxDone).
func RunInTx(ctx context.Context, pool *sql.DB, fn func(ctx context.Context) error) error {
	if InTx(ctx, pool) {
		return fn(ctx)
	}
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(context.WithValue(ctx, txKey{}, &txState{pool: pool, tx: tx})); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// BeginOwnTx is BeginTx for a store method that commits on its own: refused
// (ErrNestedTx) inside a transaction RunInTx opened on the same database.
func BeginOwnTx(ctx context.Context, pool *sql.DB) (*sql.Tx, error) {
	if InTx(ctx, pool) {
		return nil, ErrNestedTx
	}
	return pool.BeginTx(ctx, nil)
}
