-- +goose Up
-- A GROUP CAN MAKE ITS MEMBERS ADMINISTRATORS (docs/GROUPS.md → Administrators).
--
--   user_groups.gives_admin  the group gives its members the built-in
--                       Administrator role (full access) instead of a role
--                       from the Roles page — typically a group linked to an
--                       LDAP or SSO group such as "IT Admins", so who
--                       administers filex is managed in the directory.
--   users.admin_by_group  the account is an administrator because a group
--                       made it one: when no group gives it any more it goes
--                       back to the level it had before (user_group_levels).
--                       An administrator made by hand has 0, and no group
--                       ever demotes them. Any other change of role clears it.

ALTER TABLE user_groups ADD COLUMN gives_admin TINYINT(1) NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN admin_by_group TINYINT(1) NOT NULL DEFAULT 0;


-- +goose Down
ALTER TABLE users DROP COLUMN admin_by_group;
ALTER TABLE user_groups DROP COLUMN gives_admin;
