-- +goose Up
-- RBAC + per-file/per-folder ACL (see internal/acl). See the sqlite migration
-- for the full rationale. Backwards compatible: rbac_enabled defaults 0.
-- path_prefix is VARCHAR(512) (not TEXT) so the composite UNIQUE index stays
-- under InnoDB's 3072-byte key limit at utf8mb4 (512*4 + 2*8 = 2064 bytes).
ALTER TABLE storages ADD COLUMN rbac_enabled TINYINT(1) NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS file_grants (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    storage_id  BIGINT NOT NULL,
    path_prefix VARCHAR(512) NOT NULL DEFAULT '',
    is_dir      TINYINT(1) NOT NULL DEFAULT 1,
    user_id     BIGINT NOT NULL,
    level       VARCHAR(16) NOT NULL,
    created_by  BIGINT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY idx_file_grants_uniq (storage_id, path_prefix, user_id),
    KEY idx_file_grants_storage_user (storage_id, user_id),
    KEY idx_file_grants_user (user_id),
    CONSTRAINT fk_file_grants_storage FOREIGN KEY (storage_id) REFERENCES storages(id) ON DELETE CASCADE,
    CONSTRAINT fk_file_grants_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_file_grants_creator FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO roles (name, permissions_json) VALUES ('viewer', '["files.read"]');

-- +goose Down
DROP TABLE IF EXISTS file_grants;
ALTER TABLE storages DROP COLUMN rbac_enabled;
DELETE FROM roles WHERE name='viewer';
