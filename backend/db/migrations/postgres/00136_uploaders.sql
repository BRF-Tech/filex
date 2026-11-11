-- +goose Up
-- UPLOADERS (filex 0.60): a person's upload destinations, each with its own
-- key, folder and link rule, and a deletion link per upload. See the sqlite
-- variant of this migration for the reasoning - in short, the key is looked
-- up by ONE door (POST /api/uploads) and is never an API token, so a key that
-- leaks can add files to one folder and do nothing else; the folder is held
-- by storage id plus the tenant stamp; times are unix seconds.
CREATE TABLE IF NOT EXISTS uploaders (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    storage_id   BIGINT NOT NULL,
    folder       TEXT NOT NULL DEFAULT '',
    visibility   TEXT NOT NULL DEFAULT 'public',
    link_days    INTEGER NOT NULL DEFAULT 0,
    tenant_id    BIGINT NOT NULL DEFAULT 0,
    key_hash     TEXT NOT NULL UNIQUE,
    created_at   BIGINT NOT NULL,
    key_set_at   BIGINT NOT NULL,
    last_used_at BIGINT
);
CREATE INDEX IF NOT EXISTS uploaders_user_idx ON uploaders(user_id);

CREATE TABLE IF NOT EXISTS upload_deletions (
    id          BIGSERIAL PRIMARY KEY,
    token_hash  TEXT NOT NULL UNIQUE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    uploader_id BIGINT NOT NULL DEFAULT 0,
    storage_id  BIGINT NOT NULL,
    node_id     BIGINT NOT NULL,
    share_id    BIGINT NOT NULL DEFAULT 0,
    tenant_id   BIGINT NOT NULL DEFAULT 0,
    name        TEXT NOT NULL DEFAULT '',
    created_at  BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS upload_deletions_user_idx ON upload_deletions(user_id);

-- +goose Down
DROP INDEX IF EXISTS upload_deletions_user_idx;
DROP TABLE IF EXISTS upload_deletions;
DROP INDEX IF EXISTS uploaders_user_idx;
DROP TABLE IF EXISTS uploaders;
