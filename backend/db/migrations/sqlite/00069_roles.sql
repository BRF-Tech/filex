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
    user_id        INTEGER NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    overrides_json TEXT    NOT NULL DEFAULT '{}',
    updated_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS permission_rules (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT    NOT NULL,
    description      TEXT    NOT NULL DEFAULT '',
    enabled          INTEGER NOT NULL DEFAULT 1,
    permissions_json TEXT    NOT NULL DEFAULT '[]',
    provider_id      INTEGER REFERENCES providers(id) ON DELETE CASCADE,
    targets_json     TEXT    NOT NULL DEFAULT '[]',
    effects_json     TEXT    NOT NULL DEFAULT '{}',
    settings_json    TEXT    NOT NULL DEFAULT '{}',
    conditions_json  TEXT    NOT NULL DEFAULT '{}',
    created_by       INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_permission_rules_provider ON permission_rules (provider_id);

CREATE TABLE IF NOT EXISTS user_custom_roles (
    user_id INTEGER NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    role_id INTEGER NOT NULL REFERENCES permission_rules(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_user_custom_roles_role ON user_custom_roles (role_id);

CREATE TABLE IF NOT EXISTS user_sso_groups (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_name TEXT    NOT NULL,
    PRIMARY KEY (user_id, group_name)
);
CREATE INDEX IF NOT EXISTS idx_user_sso_groups_group ON user_sso_groups (group_name);

ALTER TABLE api_tokens ADD COLUMN source TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE api_tokens DROP COLUMN source;
DROP INDEX IF EXISTS idx_user_sso_groups_group;
DROP TABLE IF EXISTS user_sso_groups;
DROP INDEX IF EXISTS idx_user_custom_roles_role;
DROP TABLE IF EXISTS user_custom_roles;
DROP INDEX IF EXISTS idx_permission_rules_provider;
DROP TABLE IF EXISTS permission_rules;
DROP TABLE IF EXISTS user_permissions;
