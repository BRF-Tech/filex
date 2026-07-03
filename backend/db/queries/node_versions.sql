-- name: CreateNodeVersion :one
INSERT INTO node_versions (node_id, version_n, storage_key, size, etag)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: ListNodeVersions :many
SELECT * FROM node_versions WHERE node_id = ? ORDER BY version_n DESC;

-- name: GetNodeVersion :one
SELECT * FROM node_versions WHERE node_id = ? AND version_n = ?;

-- name: DeleteOldNodeVersions :exec
DELETE FROM node_versions WHERE node_id = ? AND version_n < ?;

-- name: GetThumbnail :one
SELECT * FROM thumbnails WHERE node_id = ?;

-- name: UpsertThumbnail :exec
INSERT INTO thumbnails (node_id, state, storage_key, width, height, error, generated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(node_id) DO UPDATE SET
    state = excluded.state,
    storage_key = excluded.storage_key,
    width = excluded.width,
    height = excluded.height,
    error = excluded.error,
    generated_at = excluded.generated_at;

-- name: SetThumbnailState :exec
UPDATE thumbnails SET state = ?, error = ? WHERE node_id = ?;

-- name: SetNodeMeta :exec
INSERT INTO node_meta (node_id, key, value)
VALUES (?, ?, ?)
ON CONFLICT(node_id, key) DO UPDATE SET value = excluded.value;

-- name: GetNodeMeta :many
SELECT * FROM node_meta WHERE node_id = ?;

-- name: DeleteNodeMeta :exec
DELETE FROM node_meta WHERE node_id = ? AND key = ?;
