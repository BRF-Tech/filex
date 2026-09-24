-- +goose Up
-- App-plugin public pages: a flow an OUTSIDE participant opens at /p/<token>
-- (an e-signature request sent to somebody without a filex account, a
-- review link). Created by a plugin through the public_page_create host
-- function while running an action for a signed-in user; answered by the
-- plugin's page_event export for anonymous visitors.
--
-- The visitor never touches a storage driver: the files a page may show are
-- COPIES the plugin exposed at creation, kept under
-- <data-dir>/app-plugins/public/<token_hash>/ and listed in files_json.
-- state_json is the plugin's own durable record for the page (≤ 64 KiB);
-- storage_id + rel name the file the flow is about, so per-file state
-- (state_get/state_set) and a follow-up job (Surface.job from a page event,
-- run as created_by) have their anchor.
--
-- The token itself is never stored — only its sha256 — so a database read
-- does not hand out live links. pin_hash is bcrypt; pin_fails/locked_until
-- implement the 5-strikes lock.
CREATE TABLE IF NOT EXISTS app_plugin_pages (
    token_hash   TEXT PRIMARY KEY,
    plugin_id    INTEGER NOT NULL,
    plugin_name  TEXT NOT NULL DEFAULT '',
    page_id      TEXT NOT NULL DEFAULT '',
    subject      TEXT NOT NULL DEFAULT '',
    storage_id   INTEGER NOT NULL DEFAULT 0,
    rel          TEXT NOT NULL DEFAULT '',
    state_json   TEXT NOT NULL DEFAULT '{}',
    files_json   TEXT NOT NULL DEFAULT '[]',
    pin_hash     TEXT NOT NULL DEFAULT '',
    pin_fails    INTEGER NOT NULL DEFAULT 0,
    locked_until DATETIME,
    expires_at   DATETIME,
    max_visits   INTEGER NOT NULL DEFAULT 0,
    visits       INTEGER NOT NULL DEFAULT 0,
    created_by   INTEGER,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at   DATETIME
);
CREATE INDEX IF NOT EXISTS idx_app_plugin_pages_plugin ON app_plugin_pages(plugin_id, created_at);
CREATE INDEX IF NOT EXISTS idx_app_plugin_pages_expires ON app_plugin_pages(expires_at);

-- +goose Down
DROP TABLE IF EXISTS app_plugin_pages;
