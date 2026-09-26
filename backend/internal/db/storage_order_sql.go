package db

import (
	"context"
	"database/sql"
)

// StorageOrderSQL implements Store.SetStorageOrder (migration 00060, issue
// #57), written ONCE for every engine. Each driver embeds a *StorageOrderSQL
// in its Store, so the method is promoted and there is no per-driver copy to
// drift (the same arrangement as CatalogueFolderSQL, and for the same reason:
// the duplication gate in web/tests/quality).
type StorageOrderSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (o *StorageOrderSQL) q(query string) string {
	if o.Placeholders != nil {
		return o.Placeholders(query)
	}
	return query
}

// SetStorageOrder gives ordered[i] position i+1 and clears every id in
// cleared, in one transaction (or in the caller's, when ctx carries one on
// this database), so no reader ever sees half an order. An id that names no
// row changes nothing; the caller decides which ids are valid.
//
// ⚠ Clearing runs first, so an id in both lists ends up placed.
func (o *StorageOrderSQL) SetStorageOrder(ctx context.Context, ordered []int64, cleared []int64) error {
	clearQ := o.q(`UPDATE storages SET sort_order=NULL WHERE id=?`)
	placeQ := o.q(`UPDATE storages SET sort_order=? WHERE id=?`)
	return RunInTx(ctx, o.Pool, func(ctx context.Context) error {
		c := Conn(ctx, o.Pool)
		for _, id := range cleared {
			if _, err := c.ExecContext(ctx, clearQ, id); err != nil {
				return err
			}
		}
		for i, id := range ordered {
			if _, err := c.ExecContext(ctx, placeQ, int64(i+1), id); err != nil {
				return err
			}
		}
		return nil
	})
}
