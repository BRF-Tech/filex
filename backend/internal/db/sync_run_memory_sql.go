package db

import (
	"context"
	"database/sql"

	"github.com/brf-tech/filex/backend/internal/model"
)

// SyncRunMemorySQL implements Store.SetSyncRunPeakMemory, Store.SyncRunPeaks
// and Store.LastMeasuredSyncRun (migration 00139, task #353), written ONCE
// for every engine. Each driver embeds a *SyncRunMemorySQL in its Store, so
// the methods are promoted and there is no per-driver copy to drift (the
// arrangement of NodeLinkStateSQL).
//
// A full scan samples the process's memory while it walks (sync.memPeak) and
// records the highest figure on its run. The storage form reads the last
// finished scan - its duration, what it saw and that peak - to say what a scan
// interval would cost (internal/syncadvice).
//
// ⚠ The column is read only here; it is NOT in the drivers' common sync_runs
// column list. Upgrade tests write runs at an older schema with the ordinary
// store methods, and a column every run read names would make each of them
// fail on a database that does not have it yet.
type SyncRunMemorySQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (o *SyncRunMemorySQL) q(query string) string { return rebind(o.Placeholders, query) }

// SetSyncRunPeakMemory records bytes as the most memory the process held while
// run id walked its storage.
func (o *SyncRunMemorySQL) SetSyncRunPeakMemory(ctx context.Context, id, bytes int64) error {
	if bytes < 0 {
		bytes = 0
	}
	_, err := Conn(ctx, o.Pool).ExecContext(ctx, o.q(`UPDATE sync_runs SET peak_mem_bytes=? WHERE id=?`), bytes, id)
	return err
}

// SyncRunPeaksMax is the most runs SyncRunPeaks answers for in one call: one
// query, under every engine's bind parameter limit (SQLite's oldest is 999).
// The run lists that ask (GET /api/admin/storages/{id}/sync-runs) show at most
// 500.
const SyncRunPeaksMax = 500

// SyncRunPeaks returns the recorded peak of every run in ids that has one. A
// run with none (from before migration 00139, or still running) is absent, and
// so is every id past the first SyncRunPeaksMax.
func (o *SyncRunMemorySQL) SyncRunPeaks(ctx context.Context, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(ids) > SyncRunPeaksMax {
		ids = ids[:SyncRunPeaksMax]
	}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := Conn(ctx, o.Pool).QueryContext(ctx, o.q(
		`SELECT id, peak_mem_bytes FROM sync_runs WHERE peak_mem_bytes > 0 AND id IN (`+placeholders(len(ids))+`)`), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, peak int64
		if err := rows.Scan(&id, &peak); err != nil {
			return nil, err
		}
		out[id] = peak
	}
	return out, rows.Err()
}

// LastMeasuredSyncRun is the most recent full scan of a storage that finished
// "ok": when it started and ended, what it saw and its peak memory
// (sql.ErrNoRows when there is none). A scan that failed or was cut short
// says nothing about what a whole one costs.
func (o *SyncRunMemorySQL) LastMeasuredSyncRun(ctx context.Context, storageID int64) (*model.SyncRun, error) {
	// id breaks a same-second tie: CURRENT_TIMESTAMP has no fraction on SQLite.
	row := Conn(ctx, o.Pool).QueryRowContext(ctx, o.q(`
		SELECT id, storage_id, started_at, finished_at, seen_count, added, updated, deleted, status, peak_mem_bytes
		  FROM sync_runs
		 WHERE storage_id=? AND status='ok' AND finished_at IS NOT NULL
		 ORDER BY started_at DESC, id DESC LIMIT 1`), storageID)
	r := &model.SyncRun{}
	if err := row.Scan(&r.ID, &r.StorageID, &r.StartedAt, &r.FinishedAt, &r.SeenCount,
		&r.Added, &r.Updated, &r.Deleted, &r.Status, &r.PeakMemBytes); err != nil {
		return nil, err
	}
	return r, nil
}
