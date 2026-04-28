-- name: CreateUser :one
INSERT INTO users (email, password_hash, role, locale, timezone)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY id;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: UpdateUserLocale :exec
UPDATE users SET locale = ?, timezone = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: UpdateUserRole :exec
UPDATE users SET role = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: TouchLastLogin :exec
UPDATE users SET last_login_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;

-- name: CreateSession :one
INSERT INTO sessions (user_id, token, expires_at, ip, user_agent)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetSessionByToken :one
SELECT * FROM sessions WHERE token = ? AND expires_at > CURRENT_TIMESTAMP;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= CURRENT_TIMESTAMP;
