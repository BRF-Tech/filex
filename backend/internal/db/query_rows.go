package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// rowScanner is a *sql.Row or a *sql.Rows: what a scanX function reads one
// row from. An alias, so a `func(interface{ Scan(dst ...any) error })` scan
// function is this type as it is written.
type rowScanner = interface{ Scan(dst ...any) error }

// queryRow runs a query that answers at most one row and reads it with scan:
// (nil, nil) when there is no row, the error wrapped as `what: …` otherwise.
// The one-row read every *SQL half written once for all engines shares.
func queryRow[T any](ctx context.Context, pool *sql.DB, scan func(rowScanner) (*T, error), what, query string, args ...any) (*T, error) {
	v, err := scan(Conn(ctx, pool).QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return v, nil
}

// queryRows runs a query and reads every row it answers with scan, in order.
// A failed query is wrapped as `what: …`; a row that does not scan is
// returned as scan said it.
func queryRows[T any](ctx context.Context, pool *sql.DB, scan func(rowScanner) (*T, error), what, query string, args ...any) ([]*T, error) {
	rows, err := Conn(ctx, pool).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer rows.Close()
	var out []*T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// insertFirst runs the INSERT of a row that moves by compare-and-set (its
// first revision, 1, is in args) into a table with a unique key, and answers
// whether it made the row - then *rev is 1. false, and no error, when the
// INSERT failed because somebody was first: the engines word that error
// three ways, so taken (a read of the key) recognises it by looking. Any
// other failure is wrapped as `what: …`.
func insertFirst(ctx context.Context, pool *sql.DB, what string, rev *int64, taken func() bool, query string, args ...any) (bool, error) {
	if _, err := Conn(ctx, pool).ExecContext(ctx, query, args...); err != nil {
		if taken() {
			return false, nil
		}
		return false, fmt.Errorf("%s: %w", what, err)
	}
	*rev = 1
	return true, nil
}

// updateAtRev runs a compare-and-set UPDATE - its WHERE names from, the
// revision the caller read, and its SET moves the row to from+1 - and answers
// whether it moved the row: then *rev is from+1. false, and no error, when
// another writer came first. The revision changes on every successful
// update, so a matched row is a changed row (MySQL counts changed rows, and
// the count is the same).
func updateAtRev(ctx context.Context, pool *sql.DB, what string, rev *int64, from int64, query string, args ...any) (bool, error) {
	res, err := Conn(ctx, pool).ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("%s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("%s: %w", what, err)
	}
	if n != 1 {
		return false, nil
	}
	*rev = from + 1
	return true, nil
}
