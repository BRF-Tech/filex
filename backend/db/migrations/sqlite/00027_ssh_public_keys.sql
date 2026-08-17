-- +goose Up
-- Registered SSH public keys: how an account logs into the SFTP endpoint
-- without sending a password.
--
-- # Why a table rather than a column on users
--
-- People have more than one machine, and revoking the laptop must not lock out
-- the backup server. One row per key also gives each one its own name, its own
-- last-used stamp and its own off switch — which is the whole reason to prefer
-- keys over a shared password in the first place.
--
-- # What the fingerprint is
--
-- The SHA256 fingerprint OpenSSH prints, base64, WITHOUT the `SHA256:` prefix.
-- It is unique across the install and it is what an incoming login is looked up
-- by: the client proves possession of the private key (x/crypto/ssh verifies
-- the signature before filex is asked anything), so all this table decides is
-- WHICH ACCOUNT that key belongs to.
--
-- ⚠ Globally unique, not unique-per-user. Two accounts sharing one key would
-- make the login ambiguous, and resolving it by taking the first row is how a
-- key ends up authenticating as somebody else.
--
-- ⚠ `ssh-copy-id` cannot work against filex: it needs a shell to append to
-- ~/.ssh/authorized_keys and there is none. Keys are registered through the
-- app, which is why the settings screen is a prerequisite for shipping SFTP
-- rather than a follow-up.
CREATE TABLE IF NOT EXISTS ssh_public_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    fingerprint  TEXT NOT NULL UNIQUE,
    -- The normalised wire form (`<type> <base64>`), so the key can be shown
    -- back and exported. The comment a user pasted is kept in `name`.
    public_key   TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME,
    disabled_at  DATETIME
);

CREATE INDEX IF NOT EXISTS ssh_public_keys_user_idx ON ssh_public_keys(user_id);

-- +goose Down
DROP INDEX IF EXISTS ssh_public_keys_user_idx;
DROP TABLE IF EXISTS ssh_public_keys;
