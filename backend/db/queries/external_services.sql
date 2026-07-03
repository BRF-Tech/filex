-- name: UpsertExternalService :exec
INSERT INTO external_services (name, enabled, url, secret_enc, options_json, last_check, last_state)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
    enabled    = excluded.enabled,
    url        = excluded.url,
    secret_enc = excluded.secret_enc,
    options_json = excluded.options_json,
    last_check = excluded.last_check,
    last_state = excluded.last_state;

-- name: GetExternalService :one
SELECT * FROM external_services WHERE name = ?;

-- name: ListExternalServices :many
SELECT * FROM external_services ORDER BY name;

-- name: UpdateExternalServiceState :exec
UPDATE external_services SET last_check = ?, last_state = ? WHERE name = ?;
