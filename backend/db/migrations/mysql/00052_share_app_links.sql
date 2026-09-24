-- +goose Up
-- An app's links, counted and named for what they are — see
-- sqlite/00052_share_app_links.sql for the model (visits apart from
-- downloads, with the backfill; a link's own purpose).
--
-- ⚠ Not wrapped in a goose statement block: a wrapped block of several
-- statements reaches the server as ONE multi-statement query, which the driver
-- refuses unless the DSN opts into multiStatements (issue #19).
ALTER TABLE shares ADD COLUMN visit_count INTEGER NOT NULL DEFAULT 0;
UPDATE shares SET visit_count = download_count, download_count = 0
 WHERE plugin_id > 0 AND page_id <> '';
ALTER TABLE shares ADD COLUMN purpose_json TEXT NULL;

-- +goose Down
ALTER TABLE shares DROP COLUMN purpose_json;
UPDATE shares SET download_count = download_count + visit_count
 WHERE plugin_id > 0 AND page_id <> '';
ALTER TABLE shares DROP COLUMN visit_count;
