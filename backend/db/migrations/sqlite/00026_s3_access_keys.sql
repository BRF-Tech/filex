-- +goose Up
-- S3 access keys: the credential an S3 client signs with.
--
-- # Why this cannot reuse api_tokens
--
-- An API token is stored as a sha256 hash, which is enough because a bearer
-- protocol SENDS the secret and the server only compares. SigV4 sends no
-- secret at all — it sends an HMAC chain derived from one — so the server must
-- hold the secret in recoverable form. That is a different storage contract,
-- not a different label on the same row, so it gets its own table with its own
-- rules. `secret_enc` is sealed by internal/secretbox (AES-GCM, key from the
-- environment); the owner chose that over plaintext on 2026-08-16.
--
-- # Why it points at BOTH a user and (optionally) a token
--
-- The requirement is that every credential the product already issues can
-- connect, at exactly its own permission. A key minted from an API token must
-- therefore inherit that token's scopes, confinement, username and expiry —
-- it is a projection of the token into a protocol that cannot carry it, not a
-- new grant. api_token_id NULL means the key was minted straight from the
-- account and carries the account's own permissions.
--
-- ⚠ The effective permission is always the INTERSECTION of everything upstream
-- (token scopes ∩ ACL grants ∩ confinement ∩ tenant ∩ storage read_only ∩ role
-- ceiling). A key may narrow; it may never widen. Enforcement lives in
-- internal/protocolauth, not here — this table only records what was issued.
--
-- ON DELETE CASCADE on both parents is the revocation path that cannot be
-- forgotten: deleting the account or the token it came from takes the key with
-- it, so a credential can never outlive the thing it was derived from.
CREATE TABLE IF NOT EXISTS s3_access_keys (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    access_key_id TEXT NOT NULL UNIQUE,
    secret_enc    TEXT NOT NULL,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_token_id  INTEGER REFERENCES api_tokens(id) ON DELETE CASCADE,
    label         TEXT NOT NULL DEFAULT '',
    -- Optional (bucket, prefix) confinement, the shape an S3 client already
    -- understands. Empty bucket = the whole account's visible storages.
    bucket        TEXT NOT NULL DEFAULT '',
    prefix        TEXT NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at  DATETIME,
    expires_at    DATETIME,
    disabled_at   DATETIME
);

CREATE INDEX IF NOT EXISTS s3_access_keys_user_idx ON s3_access_keys(user_id);
CREATE INDEX IF NOT EXISTS s3_access_keys_token_idx ON s3_access_keys(api_token_id);

-- +goose Down
DROP INDEX IF EXISTS s3_access_keys_token_idx;
DROP INDEX IF EXISTS s3_access_keys_user_idx;
DROP TABLE IF EXISTS s3_access_keys;
