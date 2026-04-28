-- name: CreateSyncRun :one
INSERT INTO sync_runs (storage_id, cursor_before, status)
VALUES (?, ?, 'running')
RETURNING *;

-- name: FinishSyncRun :exec
UPDATE sync_runs
SET finished_at = CURRENT_TIMESTAMP,
    cursor_after = ?,
    seen_count = ?,
    added = ?,
    updated = ?,
    deleted = ?,
    status = ?,
    error = ?
WHERE id = ?;

-- name: GetLastSyncRun :one
SELECT * FROM sync_runs WHERE storage_id = ? ORDER BY started_at DESC LIMIT 1;

-- name: ListSyncRuns :many
SELECT * FROM sync_runs WHERE storage_id = ? ORDER BY started_at DESC LIMIT ?;
