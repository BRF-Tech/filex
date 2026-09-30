-- +goose Up
-- +goose StatementBegin

-- GROUPS — see the SQLite migration of the same number for the design.
-- 00072: the highest number in every filex worktree on disk was 00071.
CREATE TABLE IF NOT EXISTS user_groups (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT   NOT NULL,
    description TEXT   NOT NULL DEFAULT '',
    provider_id BIGINT REFERENCES providers(id) ON DELETE CASCADE,
    role_id     BIGINT REFERENCES permission_rules(id) ON DELETE SET NULL,
    priority    INTEGER NOT NULL DEFAULT 0,
    tenant_key  BIGINT NOT NULL DEFAULT 0,
    links_json  TEXT   NOT NULL DEFAULT '[]',
    created_by  BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_groups_provider ON user_groups (provider_id);
CREATE INDEX IF NOT EXISTS idx_user_groups_role ON user_groups (role_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_groups_name ON user_groups (tenant_key, name);

CREATE TABLE IF NOT EXISTS user_group_members (
    group_id BIGINT NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    user_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source   TEXT   NOT NULL DEFAULT 'manual',
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_user_group_members_user ON user_group_members (user_id);

CREATE TABLE IF NOT EXISTS group_file_grants (
    id          BIGSERIAL PRIMARY KEY,
    storage_id  BIGINT  NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path_prefix TEXT    NOT NULL DEFAULT '',
    is_dir      BOOLEAN NOT NULL DEFAULT TRUE,
    group_id    BIGINT  NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    level       TEXT    NOT NULL,
    created_by  BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_file_grants_uniq ON group_file_grants (storage_id, path_prefix, group_id);
CREATE INDEX IF NOT EXISTS idx_group_file_grants_group ON group_file_grants (group_id);

CREATE TABLE IF NOT EXISTS user_group_levels (
    user_id      BIGINT NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    level_before TEXT   NOT NULL
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_group_levels CASCADE;
DROP TABLE IF EXISTS group_file_grants CASCADE;
DROP TABLE IF EXISTS user_group_members CASCADE;
DROP TABLE IF EXISTS user_groups CASCADE;
-- +goose StatementEnd
