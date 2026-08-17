-- +goose Up
-- +goose StatementBegin
-- Registered SSH public keys for the SFTP endpoint. See the sqlite variant for
-- the reasoning — in short: one row per machine so revoking the laptop does not
-- lock out the backup server, and the fingerprint is globally unique because a
-- key shared between two accounts would make the login ambiguous.
--
-- ⚠ VARCHAR, not TEXT, on fingerprint: MySQL cannot build a UNIQUE index over a
-- TEXT column without a prefix length, and this column must be unique because
-- it is what an incoming login is looked up by. A SHA256 fingerprint in base64
-- is 43 characters; 128 leaves room for a longer hash without a migration.
CREATE TABLE IF NOT EXISTS ssh_public_keys (
    id           BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    user_id      BIGINT NOT NULL,
    name         VARCHAR(190) NOT NULL DEFAULT '',
    fingerprint  VARCHAR(128) NOT NULL UNIQUE,
    public_key   TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME NULL,
    disabled_at  DATETIME NULL,
    CONSTRAINT ssh_public_keys_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX ssh_public_keys_user_idx ON ssh_public_keys(user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ssh_public_keys;
-- +goose StatementEnd
