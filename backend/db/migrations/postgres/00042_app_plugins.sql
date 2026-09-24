-- +goose Up
-- App plugins (in-process WebAssembly) — see sqlite/00042_app_plugins.sql for
-- what each table is. Column names must stay identical across the three
-- dialects (schema_parity_test).
CREATE TABLE IF NOT EXISTS app_plugins (
    id               BIGSERIAL PRIMARY KEY,
    name             TEXT NOT NULL UNIQUE,
    version          TEXT NOT NULL DEFAULT '',
    label_json       TEXT NOT NULL DEFAULT '{}',
    manifest_json    TEXT NOT NULL DEFAULT '{}',
    wasm_path        TEXT NOT NULL DEFAULT '',
    sha256           TEXT NOT NULL DEFAULT '',
    source           TEXT NOT NULL DEFAULT 'upload',
    source_url       TEXT NOT NULL DEFAULT '',
    signed           BOOLEAN NOT NULL DEFAULT FALSE,
    permissions_json TEXT NOT NULL DEFAULT '[]',
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS app_plugin_settings (
    plugin_id  BIGINT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (plugin_id, key)
);

CREATE TABLE IF NOT EXISTS app_plugin_overrides (
    plugin_id    BIGINT NOT NULL,
    action_id    TEXT NOT NULL,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    admin_only   BOOLEAN NOT NULL DEFAULT FALSE,
    applies_json TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (plugin_id, action_id)
);

CREATE TABLE IF NOT EXISTS app_plugin_state (
    plugin_id  BIGINT NOT NULL,
    storage_id BIGINT NOT NULL,
    path_hash  TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plugin_id, storage_id, path_hash, key)
);

CREATE TABLE IF NOT EXISTS app_plugin_jobs (
    id           TEXT PRIMARY KEY,
    op_id        BIGINT,
    plugin_id    BIGINT NOT NULL,
    plugin_name  TEXT NOT NULL DEFAULT '',
    action_id    TEXT NOT NULL DEFAULT '',
    storage_id   BIGINT NOT NULL,
    paths_json   TEXT NOT NULL DEFAULT '[]',
    params_json  TEXT NOT NULL DEFAULT '{}',
    actor_id     BIGINT,
    locale       TEXT NOT NULL DEFAULT '',
    label        TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending',
    message      TEXT NOT NULL DEFAULT '',
    outputs_json TEXT NOT NULL DEFAULT '[]',
    error        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_app_plugin_jobs_op ON app_plugin_jobs(op_id);
CREATE INDEX IF NOT EXISTS idx_app_plugin_jobs_plugin ON app_plugin_jobs(plugin_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS app_plugin_jobs;
DROP TABLE IF EXISTS app_plugin_state;
DROP TABLE IF EXISTS app_plugin_overrides;
DROP TABLE IF EXISTS app_plugin_settings;
DROP TABLE IF EXISTS app_plugins;
