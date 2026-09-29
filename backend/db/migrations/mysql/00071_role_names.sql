-- +goose Up
-- A CUSTOM ROLE IN EVERY LANGUAGE (filex 0.49.0, docs/PERMISSIONS.md → Roles).
--
-- permission_rules.names_json         interface language → the role's name
--                                     in it, e.g. {"tr": "Muhasebe"}
-- permission_rules.descriptions_json  interface language → its description
--
-- Both are optional and start empty. The role's own name and description
-- stay what they were: the answer for every language without an entry, and
-- the name the audit log records. JSON, like the table's other documents
-- (00069).
--
-- 00071: the highest number in every filex worktree on disk was 00070.
ALTER TABLE permission_rules ADD COLUMN names_json JSON NOT NULL DEFAULT ('{}');
ALTER TABLE permission_rules ADD COLUMN descriptions_json JSON NOT NULL DEFAULT ('{}');

-- +goose Down
ALTER TABLE permission_rules DROP COLUMN descriptions_json;
ALTER TABLE permission_rules DROP COLUMN names_json;
