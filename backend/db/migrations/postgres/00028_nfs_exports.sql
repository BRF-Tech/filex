-- +goose Up
-- NFS exports — see the sqlite variant for the reasoning. In short: NFSv3 has
-- no usable authentication (real identity means Kerberos, and AUTH_SYS is the
-- client asserting its own uid), so the identity is bound to the EXPORT PATH
-- instead: 32 bytes of entropy in the path, hashed here, and the mount is
-- pinned to one principal for its lifetime.
CREATE TABLE IF NOT EXISTS nfs_exports (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_token_id BIGINT REFERENCES api_tokens(id) ON DELETE CASCADE,
    label        TEXT NOT NULL DEFAULT '',
    token_hash   TEXT NOT NULL UNIQUE,
    storage_name TEXT NOT NULL DEFAULT '',
    prefix       TEXT NOT NULL DEFAULT '',
    read_only    BOOLEAN NOT NULL DEFAULT FALSE,
    allow_cidrs  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    disabled_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS nfs_exports_user_idx ON nfs_exports(user_id);
CREATE INDEX IF NOT EXISTS nfs_exports_token_idx ON nfs_exports(api_token_id);

-- +goose Down
DROP INDEX IF EXISTS nfs_exports_token_idx;
DROP INDEX IF EXISTS nfs_exports_user_idx;
DROP TABLE IF EXISTS nfs_exports;
