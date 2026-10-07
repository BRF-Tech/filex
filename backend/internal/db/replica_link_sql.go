package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ReplicaLinkSQL implements the replica_links half of Store (migration 00095,
// internal/replica folder.go, #186): the folder each storage writes into on
// its replication target. Written ONCE for every engine and embedded in each
// driver's Store, like ReplicaInitialCopySQL.
type ReplicaLinkSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (p *ReplicaLinkSQL) q(query string) string { return rebind(p.Placeholders, query) }

const replicaLinkCols = `storage_id, target_id, folder, folder_key, created_unix`

func scanReplicaLink(r rowScanner) (*model.ReplicaLink, error) {
	var l model.ReplicaLink
	if err := r.Scan(&l.StorageID, &l.TargetID, &l.Folder, &l.FolderKey, &l.CreatedUnix); err != nil {
		return nil, err
	}
	return &l, nil
}

// GetReplicaLink returns the storage's row, or (nil, nil) when it has none.
func (p *ReplicaLinkSQL) GetReplicaLink(ctx context.Context, storageID int64) (*model.ReplicaLink, error) {
	return queryRow(ctx, p.Pool, scanReplicaLink, "get replica link",
		p.q(`SELECT `+replicaLinkCols+` FROM replica_links WHERE storage_id=?`), storageID)
}

// ListReplicaLinks returns every row, by storage.
func (p *ReplicaLinkSQL) ListReplicaLinks(ctx context.Context) ([]*model.ReplicaLink, error) {
	return queryRows(ctx, p.Pool, scanReplicaLink, "list replica links",
		p.q(`SELECT `+replicaLinkCols+` FROM replica_links ORDER BY storage_id`))
}

// PutReplicaLink creates or replaces the storage's row.
//
// ⚠ DELETE-then-INSERT in one transaction, not an upsert: the upsert
// spellings differ per engine and this file is written once, and an UPDATE
// that changes nothing reads as "no row" on MySQL. The unique key on
// (target_id, folder_key) refuses a folder another storage on the target
// holds; the transaction then leaves the old row in place.
func (p *ReplicaLinkSQL) PutReplicaLink(ctx context.Context, l *model.ReplicaLink) error {
	if l == nil || l.StorageID == 0 || l.Folder == "" {
		return errors.New("replica link: storage and folder required")
	}
	return RunInTx(ctx, p.Pool, func(ctx context.Context) error {
		if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
			`DELETE FROM replica_links WHERE storage_id=?`), l.StorageID); err != nil {
			return fmt.Errorf("put replica link: %w", err)
		}
		if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
			`INSERT INTO replica_links (`+replicaLinkCols+`) VALUES (?,?,?,?,?)`),
			l.StorageID, l.TargetID, l.Folder, l.FolderKey, l.CreatedUnix); err != nil {
			return fmt.Errorf("put replica link: %w", err)
		}
		return nil
	})
}

// DeleteReplicaLink removes the storage's row (it was unlinked, relinked
// elsewhere, or deleted).
func (p *ReplicaLinkSQL) DeleteReplicaLink(ctx context.Context, storageID int64) error {
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM replica_links WHERE storage_id=?`), storageID); err != nil {
		return fmt.Errorf("delete replica link: %w", err)
	}
	return nil
}
