package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ReplicaInitialCopySQL implements the replica_initial_copies half of Store
// (migration 00094, internal/replica initial.go, #186), written ONCE for every
// engine and embedded in each driver's Store - the arrangement of
// OfficeSessionSQL.
//
// Every column is an integer or text: the times are Unix seconds, so nothing
// here depends on how an engine stores or compares a timestamp.
type ReplicaInitialCopySQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (p *ReplicaInitialCopySQL) q(query string) string { return rebind(p.Placeholders, query) }

const replicaCopyCols = `storage_id, target_id, phase, walk_cursor, counted, total_files, copied_files,
	present_files, excluded_files, failed_files, copied_bytes, last_error, started_unix, updated_unix,
	finished_unix, lease_owner, lease_until, revision`

func scanReplicaInitialCopy(r rowScanner) (*model.ReplicaInitialCopy, error) {
	var (
		c       model.ReplicaInitialCopy
		counted int64
	)
	if err := r.Scan(&c.StorageID, &c.TargetID, &c.Phase, &c.Cursor, &counted, &c.Total, &c.Copied,
		&c.Present, &c.Excluded, &c.Failed, &c.CopiedBytes, &c.LastError, &c.StartedUnix, &c.UpdatedUnix,
		&c.FinishedUnix, &c.LeaseOwner, &c.LeaseUntil, &c.Revision); err != nil {
		return nil, err
	}
	c.Counted = counted != 0
	return &c, nil
}

func copyFlag(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// GetReplicaInitialCopy returns the storage's row, or (nil, nil) when it has
// none.
func (p *ReplicaInitialCopySQL) GetReplicaInitialCopy(ctx context.Context, storageID int64) (*model.ReplicaInitialCopy, error) {
	return queryRow(ctx, p.Pool, scanReplicaInitialCopy, "get replica initial copy",
		p.q(`SELECT `+replicaCopyCols+` FROM replica_initial_copies WHERE storage_id=?`), storageID)
}

// ListReplicaInitialCopies returns every row, by storage.
func (p *ReplicaInitialCopySQL) ListReplicaInitialCopies(ctx context.Context) ([]*model.ReplicaInitialCopy, error) {
	return queryRows(ctx, p.Pool, scanReplicaInitialCopy, "list replica initial copies",
		p.q(`SELECT `+replicaCopyCols+` FROM replica_initial_copies ORDER BY storage_id`))
}

// StartReplicaInitialCopy makes the storage's row a fresh copy to targetID,
// unless a row for that target is already there and restart is false.
//
// ⚠ UPDATE-then-INSERT and not an upsert, because the upsert spellings differ
// per engine and this file is written once. Two callers racing on a storage
// with no row: the loser's INSERT meets the primary key, and the row the
// winner wrote is the one returned.
func (p *ReplicaInitialCopySQL) StartReplicaInitialCopy(ctx context.Context, storageID, targetID, now int64, restart bool) (*model.ReplicaInitialCopy, bool, error) {
	existing, err := p.GetReplicaInitialCopy(ctx, storageID)
	if err != nil {
		return nil, false, err
	}
	if existing != nil && existing.TargetID == targetID && !restart {
		return existing, false, nil
	}
	fresh := &model.ReplicaInitialCopy{
		StorageID:   storageID,
		TargetID:    targetID,
		Phase:       model.ReplicaCopyPending,
		StartedUnix: now,
		UpdatedUnix: now,
	}
	if existing != nil {
		// revision+1 keeps the UPDATE a change even when every value is the
		// same (MySQL counts changed rows): a slice still holding the old
		// lease loses it here, which is the point of a restart.
		if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
			`UPDATE replica_initial_copies SET target_id=?, phase=?, walk_cursor='', counted=0, total_files=0,
			        copied_files=0, present_files=0, excluded_files=0, failed_files=0, copied_bytes=0, last_error='',
			        started_unix=?, updated_unix=?, finished_unix=0, lease_owner='', lease_until=0, revision=revision+1
			  WHERE storage_id=?`),
			targetID, fresh.Phase, now, now, storageID); err != nil {
			return nil, false, fmt.Errorf("restart replica initial copy: %w", err)
		}
		fresh.Revision = existing.Revision + 1
		return fresh, true, nil
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO replica_initial_copies (storage_id, target_id, phase, started_unix, updated_unix)
		 VALUES (?,?,?,?,?)`),
		storageID, targetID, fresh.Phase, now, now); err != nil {
		if again, gerr := p.GetReplicaInitialCopy(ctx, storageID); gerr == nil && again != nil {
			return again, false, nil
		}
		return nil, false, fmt.Errorf("insert replica initial copy: %w", err)
	}
	return fresh, true, nil
}

// ClaimReplicaInitialCopy takes the row for one slice of work.
func (p *ReplicaInitialCopySQL) ClaimReplicaInitialCopy(ctx context.Context, storageID, targetID int64, owner string, now, until int64) (bool, error) {
	if owner == "" {
		return false, errors.New("replica initial copy: claim without an owner")
	}
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE replica_initial_copies SET lease_owner=?, lease_until=?, revision=revision+1
		  WHERE storage_id=? AND target_id=? AND phase<>? AND (lease_owner='' OR lease_until<?)`),
		owner, until, storageID, targetID, model.ReplicaCopyDone, now)
	if err != nil {
		return false, fmt.Errorf("claim replica initial copy: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// SaveReplicaInitialCopy writes c's state while `owner` holds the row.
func (p *ReplicaInitialCopySQL) SaveReplicaInitialCopy(ctx context.Context, c *model.ReplicaInitialCopy, owner string) (bool, error) {
	if c == nil {
		return false, errors.New("replica initial copy: nothing to save")
	}
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE replica_initial_copies SET phase=?, walk_cursor=?, counted=?, total_files=?, copied_files=?,
		        present_files=?, excluded_files=?, failed_files=?, copied_bytes=?, last_error=?, updated_unix=?,
		        finished_unix=?, lease_owner=?, lease_until=?, revision=revision+1
		  WHERE storage_id=? AND target_id=? AND lease_owner=?`),
		c.Phase, c.Cursor, copyFlag(c.Counted), c.Total, c.Copied,
		c.Present, c.Excluded, c.Failed, c.CopiedBytes, c.LastError, c.UpdatedUnix,
		c.FinishedUnix, c.LeaseOwner, c.LeaseUntil,
		c.StorageID, c.TargetID, owner)
	if err != nil {
		return false, fmt.Errorf("save replica initial copy: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// DeleteReplicaInitialCopy removes the storage's row (it was unlinked, or the
// storage is gone).
func (p *ReplicaInitialCopySQL) DeleteReplicaInitialCopy(ctx context.Context, storageID int64) error {
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM replica_initial_copies WHERE storage_id=?`), storageID); err != nil {
		return fmt.Errorf("delete replica initial copy: %w", err)
	}
	return nil
}
