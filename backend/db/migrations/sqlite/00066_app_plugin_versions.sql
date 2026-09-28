-- +goose Up

-- EVERY UPDATE WAITS FOR AN ADMINISTRATOR, AND CAN BE UNDONE (filex 0.48.0,
-- docs/APP-PLUGINS.md → Updates).
--
-- No app, language pack or storage plugin installs a newer version by itself
-- any more: the daily check says one is there, an administrator approves it,
-- everybody uses the same version. An approved version replaces the previous
-- one — and the previous one is KEPT, so "Back to <version>" puts it back
-- without asking again (its grant was approved before).
--
-- app_plugin_versions  the version an upgrade replaced: its manifest, hashes,
--                      grant and source as they were, and the folder under
--                      <data-dir>/app-plugins/_versions/<name>/ its files were
--                      moved into (module, manifest, interface bundle and its
--                      mirrored files). replaced_by is the administrator who
--                      approved the version that replaced it. One is kept per
--                      app.
-- app_plugins.signature
--                      the detached signature the current version was
--                      installed with, so a roll-back to it on an instance
--                      that only runs signed apps can show it again.
--
-- 00066: the highest number in every filex worktree on disk was 00065.
CREATE TABLE IF NOT EXISTS app_plugin_versions (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    plugin_id        INTEGER NOT NULL REFERENCES app_plugins(id) ON DELETE CASCADE,
    version          TEXT NOT NULL,
    manifest_json    TEXT NOT NULL,
    wasm_path        TEXT NOT NULL DEFAULT '',
    sha256           TEXT NOT NULL,
    ui_sha256        TEXT NOT NULL DEFAULT '',
    permissions_json TEXT NOT NULL DEFAULT '[]',
    source           TEXT NOT NULL DEFAULT '',
    source_url       TEXT NOT NULL DEFAULT '',
    manifest_url     TEXT NOT NULL DEFAULT '',
    signed           INTEGER NOT NULL DEFAULT 0,
    signature        TEXT NOT NULL DEFAULT '',
    dir              TEXT NOT NULL,
    replaced_by      INTEGER NULL,
    replaced_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_app_plugin_versions_plugin ON app_plugin_versions (plugin_id, id);
ALTER TABLE app_plugins ADD COLUMN signature TEXT NOT NULL DEFAULT '';


-- +goose Down
DROP TABLE IF EXISTS app_plugin_versions;
ALTER TABLE app_plugins DROP COLUMN signature;
