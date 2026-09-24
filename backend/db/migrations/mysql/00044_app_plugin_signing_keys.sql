-- +goose Up
-- App-plugin signing keys — see sqlite/00044_app_plugin_signing_keys.sql.
-- Not wrapped in a statement block; index declared inside CREATE TABLE.
CREATE TABLE IF NOT EXISTS app_plugin_signing_keys (
    id           VARCHAR(64) NOT NULL PRIMARY KEY,
    tenant_id    BIGINT NOT NULL DEFAULT 0,
    plugin_id    BIGINT NOT NULL DEFAULT 0,
    purpose      VARCHAR(16) NOT NULL DEFAULT 'leaf',
    subject      VARCHAR(190) NOT NULL DEFAULT '',
    cert_pem     TEXT NOT NULL DEFAULT (''),
    key_sealed   TEXT NOT NULL DEFAULT (''),
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at   DATETIME,
    destroyed_at DATETIME,
    retired_at   DATETIME,
    INDEX idx_app_plugin_signing_keys_ca (tenant_id, purpose, retired_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS app_plugin_signing_keys;
