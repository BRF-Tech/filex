-- +goose Up
-- An app plugin's public page is a share — see
-- sqlite/00046_share_app_pages.sql for the model, for what each column
-- carries and for why the old app_plugin_pages rows cannot be converted.
ALTER TABLE shares ADD COLUMN IF NOT EXISTS plugin_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN IF NOT EXISTS page_id TEXT NOT NULL DEFAULT '';
ALTER TABLE shares ADD COLUMN IF NOT EXISTS subject TEXT NOT NULL DEFAULT '';
ALTER TABLE shares ADD COLUMN IF NOT EXISTS state_json TEXT;
ALTER TABLE shares ADD COLUMN IF NOT EXISTS files_json TEXT;
ALTER TABLE shares ADD COLUMN IF NOT EXISTS pin_fails INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_shares_plugin ON shares(plugin_id, created_at) WHERE plugin_id > 0;

DROP TABLE IF EXISTS app_plugin_pages;

-- +goose Down
-- ⚠ app_plugin_pages is NOT recreated; see the SQLite file.
DROP INDEX IF EXISTS idx_shares_plugin;
ALTER TABLE shares DROP COLUMN IF EXISTS locked_until;
ALTER TABLE shares DROP COLUMN IF EXISTS pin_fails;
ALTER TABLE shares DROP COLUMN IF EXISTS files_json;
ALTER TABLE shares DROP COLUMN IF EXISTS state_json;
ALTER TABLE shares DROP COLUMN IF EXISTS subject;
ALTER TABLE shares DROP COLUMN IF EXISTS page_id;
ALTER TABLE shares DROP COLUMN IF EXISTS plugin_id;
