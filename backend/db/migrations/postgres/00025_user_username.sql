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
-- The unique index treats NULLs as distinct (SQLite, Postgres and MySQL all
-- agree here), so the column can be added to a live table before the backfill
-- has assigned anything.
ALTER TABLE users ADD COLUMN IF NOT EXISTS username TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS users_username_uidx ON users(username);

-- +goose Down
DROP INDEX IF EXISTS users_username_uidx;
ALTER TABLE users DROP COLUMN IF EXISTS username;
