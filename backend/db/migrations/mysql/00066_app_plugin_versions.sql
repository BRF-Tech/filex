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
    id               BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    plugin_id        BIGINT NOT NULL,
    version          VARCHAR(128) NOT NULL,
    manifest_json    MEDIUMTEXT NOT NULL,
    wasm_path        VARCHAR(255) NOT NULL DEFAULT '',
    sha256           VARCHAR(64) NOT NULL,
    ui_sha256        VARCHAR(64) NOT NULL DEFAULT '',
    permissions_json TEXT NOT NULL DEFAULT ('[]'),
    source           VARCHAR(32) NOT NULL DEFAULT '',
    source_url       TEXT NOT NULL DEFAULT (''),
    manifest_url     TEXT NOT NULL DEFAULT (''),
    signed           TINYINT(1) NOT NULL DEFAULT 0,
    signature        TEXT NOT NULL DEFAULT (''),
    dir              VARCHAR(255) NOT NULL,
    replaced_by      BIGINT NULL,
    replaced_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    INDEX idx_app_plugin_versions_plugin (plugin_id, id),
    CONSTRAINT fk_app_plugin_versions_plugin FOREIGN KEY (plugin_id) REFERENCES app_plugins(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
ALTER TABLE app_plugins ADD COLUMN signature TEXT NOT NULL DEFAULT ('');


-- +goose Down
DROP TABLE IF EXISTS app_plugin_versions;
ALTER TABLE app_plugins DROP COLUMN signature;
