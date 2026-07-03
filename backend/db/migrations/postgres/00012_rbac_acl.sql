-- +goose Up
-- RBAC + per-file/per-folder ACL (see internal/acl). See the sqlite migration
-- for the full rationale. Backwards compatible: rbac_enabled defaults FALSE.
ALTER TABLE storages ADD COLUMN IF NOT EXISTS rbac_enabled BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS file_grants (
    id          BIGSERIAL PRIMARY KEY,
    storage_id  BIGINT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path_prefix TEXT NOT NULL DEFAULT '',
    is_dir      BOOLEAN NOT NULL DEFAULT TRUE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    level       TEXT NOT NULL,
    created_by  BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_file_grants_uniq ON file_grants(storage_id, path_prefix, user_id);
CREATE INDEX IF NOT EXISTS idx_file_grants_storage_user ON file_grants(storage_id, user_id);
CREATE INDEX IF NOT EXISTS idx_file_grants_user ON file_grants(user_id);

INSERT INTO roles (name, permissions_json) VALUES ('viewer', '["files.read"]') ON CONFLICT (name) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS file_grants;
ALTER TABLE storages DROP COLUMN IF EXISTS rbac_enabled;
DELETE FROM roles WHERE name='viewer';
