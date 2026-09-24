-- +goose Up
-- App-plugin file locks — see sqlite/00045_app_plugin_locks.sql.
CREATE TABLE IF NOT EXISTS app_plugin_locks (
    storage_id  BIGINT NOT NULL,
    path_hash   TEXT NOT NULL,
    rel         TEXT NOT NULL DEFAULT '',
    plugin_id   BIGINT NOT NULL,
    plugin_name TEXT NOT NULL DEFAULT '',
    reason      TEXT NOT NULL DEFAULT '',
    until       TIMESTAMPTZ,
    created_by  BIGINT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (storage_id, path_hash)
);

-- +goose Down
DROP TABLE IF EXISTS app_plugin_locks;
