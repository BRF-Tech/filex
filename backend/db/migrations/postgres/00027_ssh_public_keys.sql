-- +goose Up
-- Registered SSH public keys for the SFTP endpoint. See the sqlite variant for
-- the reasoning — in short: one row per machine so revoking the laptop does not
-- lock out the backup server, and the fingerprint is globally unique because a
-- key shared between two accounts would make the login ambiguous.
CREATE TABLE IF NOT EXISTS ssh_public_keys (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    fingerprint  TEXT NOT NULL UNIQUE,
    public_key   TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    disabled_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS ssh_public_keys_user_idx ON ssh_public_keys(user_id);

-- +goose Down
DROP INDEX IF EXISTS ssh_public_keys_user_idx;
DROP TABLE IF EXISTS ssh_public_keys;
