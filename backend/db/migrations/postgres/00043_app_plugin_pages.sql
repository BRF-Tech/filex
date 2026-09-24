-- +goose Up
-- App-plugin public pages — see sqlite/00043_app_plugin_pages.sql.
CREATE TABLE IF NOT EXISTS app_plugin_pages (
    token_hash   TEXT PRIMARY KEY,
    plugin_id    BIGINT NOT NULL,
    plugin_name  TEXT NOT NULL DEFAULT '',
    page_id      TEXT NOT NULL DEFAULT '',
    subject      TEXT NOT NULL DEFAULT '',
    storage_id   BIGINT NOT NULL DEFAULT 0,
    rel          TEXT NOT NULL DEFAULT '',
    state_json   TEXT NOT NULL DEFAULT '{}',
    files_json   TEXT NOT NULL DEFAULT '[]',
    pin_hash     TEXT NOT NULL DEFAULT '',
    pin_fails    INTEGER NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    max_visits   INTEGER NOT NULL DEFAULT 0,
    visits       INTEGER NOT NULL DEFAULT 0,
    created_by   BIGINT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_app_plugin_pages_plugin ON app_plugin_pages(plugin_id, created_at);
CREATE INDEX IF NOT EXISTS idx_app_plugin_pages_expires ON app_plugin_pages(expires_at);

-- +goose Down
DROP TABLE IF EXISTS app_plugin_pages;
