-- +goose Up
-- A STORAGE PLUGIN FOLLOWS A SOURCE — AND WAITS FOR AN ADMINISTRATOR
-- (filex 0.48.0, docs/PLUGINS.md → Updates).
--
-- No plugin updates itself (owner's rule, 2026-09-27). A binary storage
-- plugin may name where its newer versions are published; the daily check
-- reads it and SAYS when one is there, and an administrator's "Review
-- update" installs it through the ordinary upgrade (signature, conformance,
-- roll-back).
--
-- plugins.source       `owner/name` (the filex-storage.json attached to the
--                      repository's latest release) or the https address of
--                      a filex-storage.json feed. '' = none.
-- plugins.update_json  what the last check found (plugin.UpdateInfo).
--
-- 00067: the highest number in every filex worktree on disk was 00066.
ALTER TABLE plugins ADD COLUMN source TEXT NOT NULL DEFAULT '';
ALTER TABLE plugins ADD COLUMN update_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE plugins DROP COLUMN update_json;
ALTER TABLE plugins DROP COLUMN source;
