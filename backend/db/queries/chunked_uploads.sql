-- name: CreateChunkedUpload :one
INSERT INTO chunked_uploads (id, storage_id, storage_key, upload_id, total_size, parts_json, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetChunkedUpload :one
SELECT * FROM chunked_uploads WHERE id = ?;

-- name: UpdateChunkedUploadParts :exec
UPDATE chunked_uploads SET parts_json = ? WHERE id = ?;

-- name: DeleteChunkedUpload :exec
DELETE FROM chunked_uploads WHERE id = ?;

-- name: DeleteExpiredChunkedUploads :exec
DELETE FROM chunked_uploads WHERE expires_at < CURRENT_TIMESTAMP;
