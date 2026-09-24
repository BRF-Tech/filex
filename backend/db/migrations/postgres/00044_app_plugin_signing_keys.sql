-- +goose Up
-- App-plugin signing keys — see sqlite/00044_app_plugin_signing_keys.sql.
CREATE TABLE IF NOT EXISTS app_plugin_signing_keys (
    id           TEXT PRIMARY KEY,
    tenant_id    BIGINT NOT NULL DEFAULT 0,
    plugin_id    BIGINT NOT NULL DEFAULT 0,
    purpose      TEXT NOT NULL DEFAULT 'leaf',
    subject      TEXT NOT NULL DEFAULT '',
    cert_pem     TEXT NOT NULL DEFAULT '',
    key_sealed   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ,
    destroyed_at TIMESTAMPTZ,
    retired_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_app_plugin_signing_keys_ca ON app_plugin_signing_keys(tenant_id, purpose, retired_at);

-- +goose Down
DROP TABLE IF EXISTS app_plugin_signing_keys;
