-- +goose Up
-- UPLOADERS (filex 0.60): a person's upload destinations, each with its own
-- key, folder and link rule, and a deletion link per upload. See the sqlite
-- variant of this migration for the reasoning - in short, the key is looked
-- up by ONE door (POST /api/uploads) and is never an API token, so a key that
-- leaks can add files to one folder and do nothing else; the folder is held
-- by storage id plus the tenant stamp; times are unix seconds.
--
-- ⚠ VARCHAR, not TEXT, on key_hash and token_hash: MySQL cannot build a
-- UNIQUE index over a TEXT column without a prefix length, and both are what
-- a request is looked up by (a sha256 in hex is 64 characters).
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS uploaders (
    id           BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    user_id      BIGINT NOT NULL,
    name         VARCHAR(190) NOT NULL DEFAULT '',
    storage_id   BIGINT NOT NULL,
    folder       VARCHAR(1024) NOT NULL DEFAULT '',
    visibility   VARCHAR(16) NOT NULL DEFAULT 'public',
    link_days    INT NOT NULL DEFAULT 0,
    tenant_id    BIGINT NOT NULL DEFAULT 0,
    key_hash     VARCHAR(64) NOT NULL UNIQUE,
    created_at   BIGINT NOT NULL,
    key_set_at   BIGINT NOT NULL,
    last_used_at BIGINT NULL,
    CONSTRAINT uploaders_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX uploaders_user_idx ON uploaders(user_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS upload_deletions (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    token_hash  VARCHAR(64) NOT NULL UNIQUE,
    user_id     BIGINT NOT NULL,
    uploader_id BIGINT NOT NULL DEFAULT 0,
    storage_id  BIGINT NOT NULL,
    node_id     BIGINT NOT NULL,
    share_id    BIGINT NOT NULL DEFAULT 0,
    tenant_id   BIGINT NOT NULL DEFAULT 0,
    name        VARCHAR(1024) NOT NULL DEFAULT '',
    created_at  BIGINT NOT NULL,
    CONSTRAINT upload_deletions_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX upload_deletions_user_idx ON upload_deletions(user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS upload_deletions;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS uploaders;
-- +goose StatementEnd
