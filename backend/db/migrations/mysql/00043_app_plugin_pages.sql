-- +goose Up
-- App-plugin public pages — see sqlite/00043_app_plugin_pages.sql. Not
-- wrapped in a statement block; indexes declared inside CREATE TABLE.
CREATE TABLE IF NOT EXISTS app_plugin_pages (
    token_hash   VARCHAR(64) NOT NULL PRIMARY KEY,
    plugin_id    BIGINT NOT NULL,
    plugin_name  VARCHAR(64) NOT NULL DEFAULT '',
    page_id      VARCHAR(64) NOT NULL DEFAULT '',
    subject      VARCHAR(190) NOT NULL DEFAULT '',
    storage_id   BIGINT NOT NULL DEFAULT 0,
    rel          TEXT NOT NULL DEFAULT (''),
    state_json   MEDIUMTEXT NOT NULL DEFAULT ('{}'),
    files_json   TEXT NOT NULL DEFAULT ('[]'),
    pin_hash     VARCHAR(100) NOT NULL DEFAULT '',
    pin_fails    INT NOT NULL DEFAULT 0,
    locked_until DATETIME,
    expires_at   DATETIME,
    max_visits   INT NOT NULL DEFAULT 0,
    visits       INT NOT NULL DEFAULT 0,
    created_by   BIGINT,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at   DATETIME,
    INDEX idx_app_plugin_pages_plugin (plugin_id, created_at),
    INDEX idx_app_plugin_pages_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS app_plugin_pages;
