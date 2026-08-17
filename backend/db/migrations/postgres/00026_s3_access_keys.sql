-- +goose Up
-- S3 access keys: the credential an S3 client signs with. See the sqlite
-- variant of this migration for the reasoning — in short, SigV4 sends no
-- secret, only an HMAC chain derived from one, so unlike api_tokens the value
-- must be recoverable (sealed by internal/secretbox) rather than hashed; and a
-- key minted from an API token inherits that token's permissions rather than
-- creating new ones.
CREATE TABLE IF NOT EXISTS s3_access_keys (
    id            BIGSERIAL PRIMARY KEY,
    access_key_id TEXT NOT NULL UNIQUE,
    secret_enc    TEXT NOT NULL,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_token_id  BIGINT REFERENCES api_tokens(id) ON DELETE CASCADE,
    label         TEXT NOT NULL DEFAULT '',
    bucket        TEXT NOT NULL DEFAULT '',
    prefix        TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ,
    disabled_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS s3_access_keys_user_idx ON s3_access_keys(user_id);
CREATE INDEX IF NOT EXISTS s3_access_keys_token_idx ON s3_access_keys(api_token_id);

-- +goose Down
DROP INDEX IF EXISTS s3_access_keys_token_idx;
DROP INDEX IF EXISTS s3_access_keys_user_idx;
DROP TABLE IF EXISTS s3_access_keys;
