-- name: CreateNode :one
INSERT INTO nodes (storage_id, parent_id, name, path, path_hash, storage_key, type, size, mime, etag, backend_mtime, sync_state)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = ?;

-- name: GetNodeByPath :one
SELECT * FROM nodes WHERE storage_id = ? AND path_hash = ? AND deleted_at IS NULL;

-- name: ListNodesByParent :many
SELECT * FROM nodes
WHERE storage_id = ? AND parent_id = ? AND deleted_at IS NULL
ORDER BY type DESC, name;

-- name: ListRootNodes :many
SELECT * FROM nodes
WHERE storage_id = ? AND parent_id IS NULL AND deleted_at IS NULL
ORDER BY type DESC, name;

-- name: UpdateNodeMeta :exec
UPDATE nodes
SET size = ?, mime = ?, etag = ?, backend_mtime = ?, seen_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: TouchNodeSeen :exec
UPDATE nodes SET seen_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: SoftDeleteNode :exec
UPDATE nodes SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: HardDeleteNode :exec
DELETE FROM nodes WHERE id = ?;

-- name: MoveNode :exec
UPDATE nodes SET parent_id = ?, name = ?, path = ?, path_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: ListStaleNodes :many
SELECT * FROM nodes WHERE storage_id = ? AND seen_at < ? AND deleted_at IS NULL;

-- name: CountNodesByStorage :one
SELECT COUNT(*) FROM nodes WHERE storage_id = ? AND deleted_at IS NULL;

-- name: SearchNodes :many
SELECT * FROM nodes
WHERE storage_id = ? AND name LIKE ? AND deleted_at IS NULL
ORDER BY name
LIMIT ?;
