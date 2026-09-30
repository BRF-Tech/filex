-- +goose Up
-- +goose StatementBegin

-- GROUPS (internal/group, docs/GROUPS.md).
--
-- A group is a named set of people in one tenant. It can be given folder
-- grants (group_file_grants — every member reaches what the group was
-- granted) and a custom role (role_id — the role of every member who has none
-- of their own). Purely additive: no existing row changes, so an upgrade
-- changes nobody's access until an administrator makes a group.
--
--   user_groups         provider_id NULL = install-wide; set = one tenant
--                       only, like permission_rules. tenant_key is
--                       provider_id or 0, so UNIQUE(tenant_key, name) holds
--                       for install-wide groups too (a NULL never collides in
--                       a UNIQUE index). priority orders the groups whose role
--                       a member gets: highest first, then lowest id.
--                       links_json names the groups of an outside
--                       directory whose people are members too:
--                       [{"kind":"sso","value":"finance"}]. A deleted role
--                       leaves its groups with none (SET NULL) — the delete handler moves them
--                       first, as it moves the people who hold it.
--   user_group_members  one row per person per group. source says who put
--                       them there: "manual" (an administrator) or the kind
--                       of the link that did ("sso"). A sign-in replaces only
--                       its own kind's rows, so a person added by hand stays.
--   group_file_grants   file_grants for a group: same columns, same levels,
--                       group_id where file_grants has user_id. Its own table
--                       (and id space) so no reader of file_grants ever meets
--                       a row without a user.
--   user_group_levels   the built-in level (user/viewer) an account had before
--                       a group's role moved it, restored when no group role
--                       applies any more.
--
-- 00072: the highest number in every filex worktree on disk was 00071.
CREATE TABLE IF NOT EXISTS user_groups (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    provider_id INTEGER REFERENCES providers(id) ON DELETE CASCADE,
    role_id     INTEGER REFERENCES permission_rules(id) ON DELETE SET NULL,
    priority    INTEGER NOT NULL DEFAULT 0,
    tenant_key  INTEGER NOT NULL DEFAULT 0,
    links_json  TEXT    NOT NULL DEFAULT '[]',
    created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_user_groups_provider ON user_groups (provider_id);
CREATE INDEX IF NOT EXISTS idx_user_groups_role ON user_groups (role_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_groups_name ON user_groups (tenant_key, name);

CREATE TABLE IF NOT EXISTS user_group_members (
    group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source   TEXT    NOT NULL DEFAULT 'manual',
    added_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_user_group_members_user ON user_group_members (user_id);

CREATE TABLE IF NOT EXISTS group_file_grants (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    storage_id  INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path_prefix TEXT    NOT NULL DEFAULT '',
    is_dir      INTEGER NOT NULL DEFAULT 1,
    group_id    INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    level       TEXT    NOT NULL,
    created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_file_grants_uniq ON group_file_grants (storage_id, path_prefix, group_id);
CREATE INDEX IF NOT EXISTS idx_group_file_grants_group ON group_file_grants (group_id);

CREATE TABLE IF NOT EXISTS user_group_levels (
    user_id      INTEGER NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    level_before TEXT    NOT NULL
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_group_levels;
DROP INDEX IF EXISTS idx_group_file_grants_group;
DROP INDEX IF EXISTS idx_group_file_grants_uniq;
DROP TABLE IF EXISTS group_file_grants;
DROP INDEX IF EXISTS idx_user_group_members_user;
DROP TABLE IF EXISTS user_group_members;
DROP INDEX IF EXISTS idx_user_groups_name;
DROP INDEX IF EXISTS idx_user_groups_role;
DROP INDEX IF EXISTS idx_user_groups_provider;
DROP TABLE IF EXISTS user_groups;
-- +goose StatementEnd
