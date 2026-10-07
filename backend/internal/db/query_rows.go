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
