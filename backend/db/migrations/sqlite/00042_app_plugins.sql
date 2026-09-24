-- +goose Up
-- App plugins: WebAssembly modules filex runs IN PROCESS (wazero) that add
-- rows to the file menu and run as ops jobs. Not to be confused with
-- `plugins` (00029), which are out-of-process STORAGE drivers.
--
-- # app_plugins — the admin's intent
--
-- One row per installed module: where it came from, its manifest as
-- installed (the grant the admin approved is the manifest's permission list,
-- copied into permissions_json so a later manifest cannot widen it silently),
-- the sha256 the file must still match at every load, and on/off. Runtime
-- state (compiled, refused, failed) is derived by loading the module and is
-- never written here; last_error keeps the last load failure so a stopped
-- plugin still shows something useful.
--
-- # app_plugin_settings — the plugin's configuration
--
-- Key/value per plugin, drawn from the manifest's settings[] fields. A field
-- marked secret is sealed with the instance key (secretbox, `enc:v1:`) and
-- only opened inside the settings_get host function.
--
-- # app_plugin_overrides — the admin's per-action changes
--
-- enabled / admin_only / applies_json ('' = the manifest's own rule). A
-- missing row means "as the manifest says".
--
-- # app_plugin_state — per-file state a plugin keeps (state_get/state_set)
--
-- Keyed by (plugin, storage, path_hash, key) so a move can carry it along
-- and a delete can drop it. Values are capped at 64 KiB by the host.
--
-- # app_plugin_jobs — one row per action run
--
-- The ops queue row (pending_ops) carries the sources and the progress; this
-- row carries what the queue has no column for: which plugin and action,
-- the parameters, who asked, and the outputs once committed. op_id links the
-- two; the ops worker finds the job through pending_ops.dest = jobs.id.
CREATE TABLE IF NOT EXISTS app_plugins (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT NOT NULL UNIQUE,
    version          TEXT NOT NULL DEFAULT '',
    label_json       TEXT NOT NULL DEFAULT '{}',
    manifest_json    TEXT NOT NULL DEFAULT '{}',
    wasm_path        TEXT NOT NULL DEFAULT '',
    sha256           TEXT NOT NULL DEFAULT '',
    source           TEXT NOT NULL DEFAULT 'upload',
    source_url       TEXT NOT NULL DEFAULT '',
    signed           INTEGER NOT NULL DEFAULT 0,
    permissions_json TEXT NOT NULL DEFAULT '[]',
    enabled          INTEGER NOT NULL DEFAULT 1,
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS app_plugin_settings (
    plugin_id  INTEGER NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (plugin_id, key)
);

CREATE TABLE IF NOT EXISTS app_plugin_overrides (
    plugin_id    INTEGER NOT NULL,
    action_id    TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1,
    admin_only   INTEGER NOT NULL DEFAULT 0,
    applies_json TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (plugin_id, action_id)
);

CREATE TABLE IF NOT EXISTS app_plugin_state (
    plugin_id  INTEGER NOT NULL,
    storage_id INTEGER NOT NULL,
    path_hash  TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plugin_id, storage_id, path_hash, key)
);

CREATE TABLE IF NOT EXISTS app_plugin_jobs (
    id           TEXT PRIMARY KEY,
    op_id        INTEGER,
    plugin_id    INTEGER NOT NULL,
    plugin_name  TEXT NOT NULL DEFAULT '',
    action_id    TEXT NOT NULL DEFAULT '',
    storage_id   INTEGER NOT NULL,
    paths_json   TEXT NOT NULL DEFAULT '[]',
    params_json  TEXT NOT NULL DEFAULT '{}',
    actor_id     INTEGER,
    locale       TEXT NOT NULL DEFAULT '',
    label        TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending',
    message      TEXT NOT NULL DEFAULT '',
    outputs_json TEXT NOT NULL DEFAULT '[]',
    error        TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at  DATETIME
);
CREATE INDEX IF NOT EXISTS idx_app_plugin_jobs_op ON app_plugin_jobs(op_id);
CREATE INDEX IF NOT EXISTS idx_app_plugin_jobs_plugin ON app_plugin_jobs(plugin_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS app_plugin_jobs;
DROP TABLE IF EXISTS app_plugin_state;
DROP TABLE IF EXISTS app_plugin_overrides;
DROP TABLE IF EXISTS app_plugin_settings;
DROP TABLE IF EXISTS app_plugins;
