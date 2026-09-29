-- +goose Up
-- Roles and per-user permissions (internal/perm, docs/PERMISSIONS.md).
--
--   user_permissions   one account's own exceptions (Allow/Deny per permission)
--   permission_rules   the custom roles: each its own list of permissions
--                      (permissions_json), limits (settings_json), an optional
--                      "different in some folders" part (effects_json where
--                      conditions_json matches), and the SSO groups that make
--                      it a new account's starting role (targets_json)
--   user_custom_roles  the one custom role a person holds (at most one each)
--   user_sso_groups    the groups claim of a person's latest SSO sign-in
--   api_tokens.source  what minted a token (the desktop app, or a person), so
--                      access.desktop and access.api can tell them apart
--
-- Everything here is new: an upgrade moves nobody. Every account keeps its
-- built-in role, and the built-in roles start from what they could always do.

CREATE TABLE IF NOT EXISTS user_permissions (
    user_id        BIGINT NOT NULL,
    overrides_json JSON   NOT NULL DEFAULT ('{}'),
    updated_by     BIGINT NULL,
    updated_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (user_id),
    CONSTRAINT fk_user_permissions_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_user_permissions_updated_by FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS permission_rules (
    id               BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name             VARCHAR(255)  NOT NULL,
    description      VARCHAR(1000) NOT NULL DEFAULT '',
    enabled          TINYINT(1)    NOT NULL DEFAULT 1,
    permissions_json JSON NOT NULL DEFAULT ('[]'),
    provider_id      BIGINT NULL,
    targets_json     JSON NOT NULL DEFAULT ('[]'),
    effects_json     JSON NOT NULL DEFAULT ('{}'),
    settings_json    JSON NOT NULL DEFAULT ('{}'),
    conditions_json  JSON NOT NULL DEFAULT ('{}'),
    created_by       BIGINT NULL,
    created_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    INDEX idx_permission_rules_provider (provider_id),
    CONSTRAINT fk_permission_rules_provider FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE,
    CONSTRAINT fk_permission_rules_created_by FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_custom_roles (
    user_id BIGINT NOT NULL,
    role_id BIGINT NOT NULL,
    PRIMARY KEY (user_id),
    KEY idx_user_custom_roles_role (role_id),
    CONSTRAINT fk_user_custom_roles_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_user_custom_roles_role FOREIGN KEY (role_id) REFERENCES permission_rules(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_sso_groups (
    user_id    BIGINT       NOT NULL,
    group_name VARCHAR(190) NOT NULL,
    PRIMARY KEY (user_id, group_name),
    INDEX idx_user_sso_groups_group (group_name),
    CONSTRAINT fk_user_sso_groups_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE api_tokens ADD COLUMN source VARCHAR(16) NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE api_tokens DROP COLUMN source;
DROP TABLE IF EXISTS user_sso_groups;
DROP TABLE IF EXISTS user_custom_roles;
DROP TABLE IF EXISTS permission_rules;
DROP TABLE IF EXISTS user_permissions;
