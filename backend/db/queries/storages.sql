-- name: CreateStorage :one
INSERT INTO storages (name, driver, mount_path, config_json, sync_mode, sync_interval_s, enabled, read_only)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetStorage :one
SELECT * FROM storages WHERE id = ?;

-- name: GetStorageByName :one
SELECT * FROM storages WHERE name = ?;

-- name: ListStorages :many
SELECT * FROM storages ORDER BY id;

-- name: ListEnabledStorages :many
SELECT * FROM storages WHERE enabled = 1 ORDER BY id;

-- name: UpdateStorage :exec
UPDATE storages
SET name = ?, driver = ?, mount_path = ?, config_json = ?, sync_mode = ?, sync_interval_s = ?, enabled = ?, read_only = ?
WHERE id = ?;

-- name: UpdateStorageSyncCursor :exec
UPDATE storages SET last_sync_at = ?, last_sync_token = ? WHERE id = ?;

-- name: DeleteStorage :exec
DELETE FROM storages WHERE id = ?;
