-- +goose Up
-- A short, protocol-friendly name for the account, alongside the e-mail.
--
-- Why a second identifier at all: the connection protocols filex is growing
-- (SFTP, FTPS, and the S3/NFS credential pages) put the identity in places an
-- e-mail address does not survive. `sftp://ada@example.com@host` needs escaping,
-- rclone and WinSCP config files split on `@`, and an FTP USER command with an
-- `@` in it confuses proxying clients. Every product that serves these
-- protocols has a login name for exactly this reason.
--
-- Nullable here, and filled by identitystore/backfill rather than by SQL: the
-- rules (lowercase, [a-z0-9._-], 3-32, no leading digit, a reserved-name list,
-- and a deterministic suffix on collision) cannot be expressed sanely in three
-- dialects, and getting them subtly different per database is exactly the kind
-- of drift that shows up years later as "login works on one deployment".
--
-- VARCHAR(32), not TEXT, because MySQL cannot build a unique index over a TEXT
-- column without a prefix length — and 32 is the maximum the validator allows,
-- so the column is exactly as wide as the rule. NULLs are distinct in a MySQL
-- unique index too, so the column can be added before the backfill runs.
ALTER TABLE users ADD COLUMN username VARCHAR(32);
CREATE UNIQUE INDEX users_username_uidx ON users(username);

-- +goose Down
DROP INDEX users_username_uidx ON users;
ALTER TABLE users DROP COLUMN username;
