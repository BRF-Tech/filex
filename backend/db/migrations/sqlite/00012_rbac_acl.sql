-- +goose Up
-- RBAC + per-file/per-folder ACL (see internal/acl).
--
-- Backwards compatible: storages.rbac_enabled defaults to 0, so every
-- existing storage behaves exactly as before (visible to all authenticated
-- users; capability governed purely by account role). Grants are only
-- consulted when a storage flips rbac_enabled=1.
ALTER TABLE storages ADD COLUMN rbac_enabled INTEGER NOT NULL DEFAULT 0;

-- One row grants a single user a level on a path within one storage.
-- path_prefix is confine-form (cleaned, no leading/trailing slash; '' == the
-- storage root). is_dir=1 → the level cascades to every descendant; is_dir=0
-- → applies to that exact file path only. level ∈ (viewer|editor|owner).
CREATE TABLE IF NOT EXISTS file_grants (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    storage_id  INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path_prefix TEXT    NOT NULL DEFAULT '',
    is_dir      INTEGER NOT NULL DEFAULT 1,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    level       TEXT    NOT NULL,
    created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_file_grants_uniq ON file_grants(storage_id, path_prefix, user_id);
CREATE INDEX IF NOT EXISTS idx_file_grants_storage_user ON file_grants(storage_id, user_id);
CREATE INDEX IF NOT EXISTS idx_file_grants_user ON file_grants(user_id);

INSERT OR IGNORE INTO roles (name, permissions_json) VALUES ('viewer', '["files.read"]');

-- +goose Down
DROP TABLE IF EXISTS file_grants;
ALTER TABLE storages DROP COLUMN rbac_enabled;
DELETE FROM roles WHERE name='viewer';
