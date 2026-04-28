-- name: CreateShare :one
INSERT INTO shares (node_id, token, pin_hash, expires_at, max_downloads, created_by)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetShareByToken :one
SELECT * FROM shares WHERE token = ?;

-- name: ListSharesByNode :many
SELECT * FROM shares WHERE node_id = ? ORDER BY created_at DESC;

-- name: ListSharesByUser :many
SELECT * FROM shares WHERE created_by = ? ORDER BY created_at DESC;

-- name: IncrementShareDownload :exec
UPDATE shares SET download_count = download_count + 1 WHERE id = ?;

-- name: DeleteShare :exec
DELETE FROM shares WHERE id = ?;

-- name: DeleteExpiredShares :exec
DELETE FROM shares WHERE expires_at IS NOT NULL AND expires_at < CURRENT_TIMESTAMP;
