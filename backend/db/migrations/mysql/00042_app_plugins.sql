-- +goose Up
-- App plugins (in-process WebAssembly) — see sqlite/00042_app_plugins.sql for
-- what each table is. Column names must stay identical across the three
-- dialects (schema_parity_test).
--
-- ⚠ Not wrapped in a goose statement block (a wrapped block reaches the server
-- as one multi-statement query, which the driver refuses — see 00036).
-- ⚠ Indexes are declared inside CREATE TABLE: MySQL has no CREATE INDEX IF
-- NOT EXISTS. `key` is a reserved word on MySQL and is quoted.
CREATE TABLE IF NOT EXISTS app_plugins (
    id               BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name             VARCHAR(64) NOT NULL UNIQUE,
    version          VARCHAR(64) NOT NULL DEFAULT '',
    label_json       TEXT NOT NULL DEFAULT ('{}'),
    manifest_json    MEDIUMTEXT NOT NULL DEFAULT ('{}'),
    wasm_path        VARCHAR(255) NOT NULL DEFAULT '',
    sha256           VARCHAR(64) NOT NULL DEFAULT '',
    source           VARCHAR(16) NOT NULL DEFAULT 'upload',
    source_url       TEXT NOT NULL DEFAULT (''),
    signed           TINYINT(1) NOT NULL DEFAULT 0,
    permissions_json TEXT NOT NULL DEFAULT ('[]'),
    enabled          TINYINT(1) NOT NULL DEFAULT 1,
    last_error       TEXT NOT NULL DEFAULT (''),
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS app_plugin_settings (
    plugin_id  BIGINT NOT NULL,
    `key`      VARCHAR(64) NOT NULL,
    value      TEXT NOT NULL DEFAULT (''),
    PRIMARY KEY (plugin_id, `key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS app_plugin_overrides (
    plugin_id    BIGINT NOT NULL,
    action_id    VARCHAR(64) NOT NULL,
    enabled      TINYINT(1) NOT NULL DEFAULT 1,
    admin_only   TINYINT(1) NOT NULL DEFAULT 0,
    applies_json TEXT NOT NULL DEFAULT (''),
    PRIMARY KEY (plugin_id, action_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS app_plugin_state (
    plugin_id  BIGINT NOT NULL,
    storage_id BIGINT NOT NULL,
    path_hash  VARCHAR(64) NOT NULL,
    `key`      VARCHAR(64) NOT NULL,
    value      MEDIUMTEXT NOT NULL DEFAULT (''),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plugin_id, storage_id, path_hash, `key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS app_plugin_jobs (
    id           VARCHAR(64) NOT NULL PRIMARY KEY,
    op_id        BIGINT,
    plugin_id    BIGINT NOT NULL,
    plugin_name  VARCHAR(64) NOT NULL DEFAULT '',
    action_id    VARCHAR(64) NOT NULL DEFAULT '',
    storage_id   BIGINT NOT NULL,
    paths_json   TEXT NOT NULL DEFAULT ('[]'),
    params_json  TEXT NOT NULL DEFAULT ('{}'),
    actor_id     BIGINT,
    locale       VARCHAR(16) NOT NULL DEFAULT '',
    label        VARCHAR(190) NOT NULL DEFAULT '',
    status       VARCHAR(32) NOT NULL DEFAULT 'pending',
    message      TEXT NOT NULL DEFAULT (''),
    outputs_json TEXT NOT NULL DEFAULT ('[]'),
    error        TEXT NOT NULL DEFAULT (''),
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at  DATETIME,
    INDEX idx_app_plugin_jobs_op (op_id),
    INDEX idx_app_plugin_jobs_plugin (plugin_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS app_plugin_jobs;
DROP TABLE IF EXISTS app_plugin_state;
DROP TABLE IF EXISTS app_plugin_overrides;
DROP TABLE IF EXISTS app_plugin_settings;
DROP TABLE IF EXISTS app_plugins;
