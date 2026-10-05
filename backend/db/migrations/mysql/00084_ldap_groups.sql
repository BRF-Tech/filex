-- +goose Up
-- LDAP GROUPS AND WHERE AN ACCOUNT COMES FROM (docs/GROUPS.md, docs/LDAP.md).
--
--   users.auth_source  how the account came to exist, so the Users page can
--                      tell a directory account from one made here: 'local'
--                      (the Users page, an invitation, first run), 'sso' (an
--                      OpenID Connect sign-in made it), 'ldap' (an LDAP /
--                      Active Directory sign-in made it) or 'proxy' (a trusted
--                      proxy's headers made it). A label: it gates nothing.
--                      Accounts that predate it: one with an OIDC subject is
--                      'sso'; one with a password here is 'local'; one with
--                      neither is '' - not known (a directory, a header proxy
--                      or an administrator made it with no password) - and
--                      takes the label, and for LDAP the directory, of its
--                      next sign-in. ⚠ Not 'local': only the main directory
--                      signs in a 'local' account, so a second directory's
--                      people, or a tenant's own directory's, would lose
--                      their accounts at the upgrade.
--   users.auth_directory which LDAP directory made the account, by its
--                      provider name ('ldap', 'ldap-partner' …; '' for any
--                      other account): only that directory signs it in, and
--                      only that directory's sync counts it as no longer
--                      listed. An LDAP account from before it is the main
--                      directory's ('ldap').
--   user_ldap_groups   the groups of a person's latest LDAP sign-in, in
--                      group.LDAPValue's form (lower case; a group's DN and
--                      its common name) — what a changed LDAP link on a group
--                      re-applies at once, as user_sso_groups does for SSO.
--   user_groups.directory_*   a group directory sync brought in from a
--                      directory group: directory_id is that group's
--                      permanent id ("ldap:" + entryUUID / objectGUID, or its
--                      DN when the directory has none) — so a group renamed
--                      there is renamed here, keeping its folders and role —
--                      directory_name the name it last had there, and
--                      directory_state '' while the directory has it,
--                      'removed' once it does not (the group stays, flagged,
--                      until an administrator deletes it or keeps it as a
--                      filex group). '' / '' / '' for every other group.
--                      The directory decides which groups exist: one deleted
--                      here while the directory has it is made again by the
--                      next sync (leave it out with sync_group_filter).
--
-- Additive: no one's access changes until a group names an LDAP group.
ALTER TABLE users ADD COLUMN auth_source VARCHAR(16) NOT NULL DEFAULT 'local';
UPDATE users SET auth_source = 'sso' WHERE oidc_subject IS NOT NULL AND oidc_subject <> '';
UPDATE users SET auth_source = '' WHERE (oidc_subject IS NULL OR oidc_subject = '') AND (password_hash IS NULL OR password_hash = '');
ALTER TABLE users ADD COLUMN auth_directory VARCHAR(64) NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS user_ldap_groups (
    user_id    BIGINT       NOT NULL,
    group_name VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL,
    PRIMARY KEY (user_id, group_name),
    CONSTRAINT fk_user_ldap_groups_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE user_groups ADD COLUMN directory_id    VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL DEFAULT '';
ALTER TABLE user_groups ADD COLUMN directory_name  VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE user_groups ADD COLUMN directory_state VARCHAR(16)  NOT NULL DEFAULT '';
CREATE INDEX idx_user_groups_directory ON user_groups (directory_id);


-- +goose Down
ALTER TABLE users DROP COLUMN auth_directory;
DROP INDEX idx_user_groups_directory ON user_groups;
ALTER TABLE user_groups DROP COLUMN directory_state;
ALTER TABLE user_groups DROP COLUMN directory_name;
ALTER TABLE user_groups DROP COLUMN directory_id;
DROP TABLE IF EXISTS user_ldap_groups;
ALTER TABLE users DROP COLUMN auth_source;
