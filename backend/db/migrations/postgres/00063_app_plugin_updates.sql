-- +goose Up
-- APPS UPDATE THEMSELVES (filex 0.47.0, docs/APP-PLUGINS.md → Updates).
--
-- An installed app — a language pack above all — follows the source it was
-- installed from: once a day filex asks that source for a newer version the
-- running filex can run, and applies it by itself when it asks for nothing
-- the administrator has not already approved.
--
-- auto_update  the per-app switch, ON for every row (existing ones included:
--              that is the point of the release — a pack installed last month
--              moves to the new strings by itself).
-- manifest_url where a URL install read filex-app.json. source_url is the
--              MODULE's address for an app with one, so without this column
--              a URL-installed app could not be asked for a newer manifest.
-- update_json  what the last check found (wasmplugin.UpdateInfo): a newer
--              version, one that needs approval, one this filex cannot run, a
--              failure, the last automatic update. '' = never checked.
--
-- 00063: the highest number in every filex worktree on disk was 00062.
ALTER TABLE app_plugins ADD COLUMN IF NOT EXISTS auto_update BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE app_plugins ADD COLUMN IF NOT EXISTS manifest_url TEXT NOT NULL DEFAULT '';
ALTER TABLE app_plugins ADD COLUMN IF NOT EXISTS update_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE app_plugins DROP COLUMN IF EXISTS update_json;
ALTER TABLE app_plugins DROP COLUMN IF EXISTS manifest_url;
ALTER TABLE app_plugins DROP COLUMN IF EXISTS auto_update;
