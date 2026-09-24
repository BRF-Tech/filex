-- +goose Up
-- An app's links, counted and named for what they are — see
-- sqlite/00052_share_app_links.sql for the model (visits apart from
-- downloads, with the backfill; a link's own purpose).
ALTER TABLE shares ADD COLUMN IF NOT EXISTS visit_count INTEGER NOT NULL DEFAULT 0;
UPDATE shares SET visit_count = download_count, download_count = 0
 WHERE plugin_id > 0 AND page_id <> '';
ALTER TABLE shares ADD COLUMN IF NOT EXISTS purpose_json TEXT;

-- +goose Down
ALTER TABLE shares DROP COLUMN IF EXISTS purpose_json;
UPDATE shares SET download_count = download_count + visit_count
 WHERE plugin_id > 0 AND page_id <> '';
ALTER TABLE shares DROP COLUMN IF EXISTS visit_count;
