-- +goose Up
-- +goose StatementBegin
-- S3 access keys: the credential an S3 client signs with. See the sqlite
-- variant of this migration for the reasoning — in short, SigV4 sends no
-- secret, only an HMAC chain derived from one, so unlike api_tokens the value
-- must be recoverable (sealed by internal/secretbox) rather than hashed; and a
-- key minted from an API token inherits that token's permissions rather than
-- creating new ones.
--
-- ⚠ VARCHAR, not TEXT, on access_key_id: MySQL cannot build a UNIQUE index
-- over a TEXT column without a prefix length, and this one must be unique
-- because it is what an incoming signature is looked up by.
CREATE TABLE IF NOT EXISTS s3_access_keys (
    id            BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    access_key_id VARCHAR(128) NOT NULL UNIQUE,
    secret_enc    TEXT NOT NULL,
    user_id       BIGINT NOT NULL,
    api_token_id  BIGINT NULL,
    label         VARCHAR(190) NOT NULL DEFAULT '',
    bucket        VARCHAR(190) NOT NULL DEFAULT '',
    prefix        VARCHAR(1024) NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at  DATETIME NULL,
    expires_at    DATETIME NULL,
    disabled_at   DATETIME NULL,
    CONSTRAINT s3_access_keys_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT s3_access_keys_token_fk FOREIGN KEY (api_token_id) REFERENCES api_tokens(id) ON DELETE CASCADE
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX s3_access_keys_user_idx ON s3_access_keys(user_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX s3_access_keys_token_idx ON s3_access_keys(api_token_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS s3_access_keys;
-- +goose StatementEnd
