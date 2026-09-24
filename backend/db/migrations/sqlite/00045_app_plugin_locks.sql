-- +goose Up
-- App-plugin file locks: a plugin (a signature request, say) may lock one
-- file so that NOBODY — the owner and administrators included — can write,
-- rename, move or delete it until the lock ends: everybody's effective level
-- on that path is capped at viewer (internal/acl). The plugin's own jobs
-- write through the ops worker, which does not consult the ACL, so the
-- signing itself still lands. `until` is the expiry; a NULL never expires
-- until the plugin unlocks. One lock per file; the plugin that holds it is
-- the only one that may lift it.
CREATE TABLE IF NOT EXISTS app_plugin_locks (
    storage_id  INTEGER NOT NULL,
    path_hash   TEXT NOT NULL,
    rel         TEXT NOT NULL DEFAULT '',
    plugin_id   INTEGER NOT NULL,
    plugin_name TEXT NOT NULL DEFAULT '',
    reason      TEXT NOT NULL DEFAULT '',
    until       DATETIME,
    created_by  INTEGER,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (storage_id, path_hash)
);

-- +goose Down
DROP TABLE IF EXISTS app_plugin_locks;
