-- +goose Up
-- LDAP PEOPLE BY PERMANENT ID, AND SWITCHED OFF BY THE DIRECTORY (docs/LDAP.md).
--
--   users.directory_id  the person's permanent id in the directory that made
--                       the account: its name, ":", then entryUUID (OpenLDAP,
--                       lldap, 389-ds) or objectGUID (Active Directory, hex).
--                       A sign-in or sync finds the account by it first, so
--                       a person whose e-mail changes there keeps their
--                       account (and its e-mail follows), and an address the
--                       directory gives to someone else does not hand them
--                       the previous owner's account. '' for any other account,
--                       and for a directory with no permanent ids (e-mail
--                       alone then, as before).
--   users.disabled_reason = 'directory' (column from 00079): directory sync
--                       switched the account off — disabled there, or no longer
--                       listed with sync_disable_missing — and switches it
--                       back on when the directory does. Switching an account
--                       on or off by hand clears it, so the administrator's
--                       choice stands. No column of its own.

ALTER TABLE users ADD COLUMN IF NOT EXISTS directory_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_users_directory_id ON users (directory_id);


-- +goose Down
DROP INDEX IF EXISTS idx_users_directory_id;
ALTER TABLE users DROP COLUMN IF EXISTS directory_id;
