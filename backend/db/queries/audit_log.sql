-- name: InsertAuditEntry :exec
INSERT INTO audit_log (user_id, action, target_type, target_id, metadata_json, ip)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListAuditByUser :many
SELECT * FROM audit_log WHERE user_id = ? ORDER BY created_at DESC LIMIT ?;

-- name: ListAuditRecent :many
SELECT * FROM audit_log ORDER BY created_at DESC LIMIT ?;

-- name: ListAuditByTarget :many
SELECT * FROM audit_log WHERE target_type = ? AND target_id = ? ORDER BY created_at DESC LIMIT ?;
