-- +goose Up
-- App-plugin file locks — see sqlite/00045_app_plugin_locks.sql. `until`
-- is not reserved on MySQL; kept unquoted like the other dialects.
CREATE TABLE IF NOT EXISTS app_plugin_locks (
    storage_id  BIGINT NOT NULL,
    path_hash   VARCHAR(64) NOT NULL,
    rel         TEXT NOT NULL DEFAULT (''),
    plugin_id   BIGINT NOT NULL,
    plugin_name VARCHAR(64) NOT NULL DEFAULT '',
    reason      VARCHAR(190) NOT NULL DEFAULT '',
    until       DATETIME,
    created_by  BIGINT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (storage_id, path_hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS app_plugin_locks;
