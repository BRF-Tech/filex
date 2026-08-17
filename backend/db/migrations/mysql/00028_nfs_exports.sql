-- +goose Up
-- +goose StatementBegin
-- NFS exports — see the sqlite variant for the reasoning. In short: NFSv3 has
-- no usable authentication, so the identity is bound to the EXPORT PATH (32
-- bytes of entropy, hashed here) and the mount is pinned to one principal.
--
-- ⚠ VARCHAR, not TEXT, on token_hash: MySQL cannot build a UNIQUE index over a
-- TEXT column without a prefix length, and this is what a mount is looked up by.
CREATE TABLE IF NOT EXISTS nfs_exports (
    id           BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    user_id      BIGINT NOT NULL,
    api_token_id BIGINT NULL,
    label        VARCHAR(190) NOT NULL DEFAULT '',
    token_hash   VARCHAR(128) NOT NULL UNIQUE,
    storage_name VARCHAR(190) NOT NULL DEFAULT '',
    prefix       VARCHAR(1024) NOT NULL DEFAULT '',
    read_only    TINYINT(1) NOT NULL DEFAULT 0,
    allow_cidrs  TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME NULL,
    expires_at   DATETIME NULL,
    disabled_at  DATETIME NULL,
    CONSTRAINT nfs_exports_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT nfs_exports_token_fk FOREIGN KEY (api_token_id) REFERENCES api_tokens(id) ON DELETE CASCADE
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX nfs_exports_user_idx ON nfs_exports(user_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX nfs_exports_token_idx ON nfs_exports(api_token_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS nfs_exports;
-- +goose StatementEnd
