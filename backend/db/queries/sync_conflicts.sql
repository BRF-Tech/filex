-- name: CreateSyncConflict :one
INSERT INTO sync_conflicts (node_id, storage_id, storage_key, db_etag, backend_etag, db_mtime, backend_mtime)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListUnresolvedConflicts :many
SELECT * FROM sync_conflicts WHERE resolved_at IS NULL ORDER BY detected_at DESC;

-- name: ResolveConflict :exec
UPDATE sync_conflicts
SET resolved_at = CURRENT_TIMESTAMP, resolution = ?
WHERE id = ?;
