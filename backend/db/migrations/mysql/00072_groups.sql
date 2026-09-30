-- +goose Up

-- GROUPS — see the SQLite migration of the same number for the design.
--
-- ⚠ group_file_grants.path_prefix and user_groups.name are utf8mb4_0900_bin,
-- as file_grants.path_prefix is since 00041: the table default ignores case
-- and accents, so a grant on `docs` would find and overwrite the one on
-- `Docs`, and `Finance` / `finance` would be one name here and two on the
-- other engines. 0900_bin is NO PAD, so a trailing space differs too.
-- 00072: the highest number in every filex worktree on disk was 00071.
CREATE TABLE IF NOT EXISTS user_groups (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name        VARCHAR(190)  CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL,
    description VARCHAR(1000) NOT NULL DEFAULT '',
    provider_id BIGINT NULL,
    role_id     BIGINT NULL,
    priority    INT NOT NULL DEFAULT 0,
    tenant_key  BIGINT NOT NULL DEFAULT 0,
    links_json  JSON NOT NULL DEFAULT ('[]'),
    created_by  BIGINT NULL,
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    INDEX idx_user_groups_provider (provider_id),
    INDEX idx_user_groups_role (role_id),
    UNIQUE KEY idx_user_groups_name (tenant_key, name),
    CONSTRAINT fk_user_groups_provider FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE,
    CONSTRAINT fk_user_groups_role FOREIGN KEY (role_id) REFERENCES permission_rules(id) ON DELETE SET NULL,
    CONSTRAINT fk_user_groups_created_by FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_group_members (
    group_id BIGINT NOT NULL,
    user_id  BIGINT NOT NULL,
    source   VARCHAR(16) NOT NULL DEFAULT 'manual',
    added_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (group_id, user_id),
    INDEX idx_user_group_members_user (user_id),
    CONSTRAINT fk_user_group_members_group FOREIGN KEY (group_id) REFERENCES user_groups(id) ON DELETE CASCADE,
    CONSTRAINT fk_user_group_members_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS group_file_grants (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    storage_id  BIGINT NOT NULL,
    path_prefix VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL DEFAULT '',
    is_dir      TINYINT(1) NOT NULL DEFAULT 1,
    group_id    BIGINT NOT NULL,
    level       VARCHAR(16) NOT NULL,
    created_by  BIGINT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY idx_group_file_grants_uniq (storage_id, path_prefix, group_id),
    KEY idx_group_file_grants_group (group_id),
    CONSTRAINT fk_group_file_grants_storage FOREIGN KEY (storage_id) REFERENCES storages(id) ON DELETE CASCADE,
    CONSTRAINT fk_group_file_grants_group FOREIGN KEY (group_id) REFERENCES user_groups(id) ON DELETE CASCADE,
    CONSTRAINT fk_group_file_grants_creator FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_group_levels (
    user_id      BIGINT NOT NULL,
    level_before VARCHAR(16) NOT NULL,
    PRIMARY KEY (user_id),
    CONSTRAINT fk_user_group_levels_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS user_group_levels;
DROP TABLE IF EXISTS group_file_grants;
DROP TABLE IF EXISTS user_group_members;
DROP TABLE IF EXISTS user_groups;
