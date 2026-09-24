-- +goose Up
-- An app plugin's public page is a share — see
-- sqlite/00046_share_app_pages.sql for the model, for what each column
-- carries and for why the old app_plugin_pages rows cannot be converted.
--
-- ⚠ Not wrapped in a goose statement block: a wrapped block of several
-- statements reaches the server as ONE multi-statement query, which the driver
-- refuses unless the DSN opts into multiStatements — the fault that broke five
-- migrations on the first boot of every MySQL install (issue #19).
--
-- ⚠ Every column and the index in ONE ALTER. MySQL has neither ADD COLUMN IF
-- NOT EXISTS nor CREATE INDEX IF NOT EXISTS and its DDL is not transactional,
-- so a split statement can leave a half-applied migration that fails
-- differently on the retry.
--
-- ⚠ No partial index. MySQL has no WHERE on an index, so unlike SQLite and
-- PostgreSQL this one also carries the plugin_id = 0 rows. A size difference,
-- not a behaviour one.
--
-- ⚠ state_json / files_json are nullable with no default: MySQL cannot give a
-- TEXT column an ordinary DEFAULT, and the parity gate compares nullability.
ALTER TABLE shares ADD COLUMN plugin_id BIGINT NOT NULL DEFAULT 0, ADD COLUMN page_id VARCHAR(64) NOT NULL DEFAULT '', ADD COLUMN subject VARCHAR(190) NOT NULL DEFAULT '', ADD COLUMN state_json MEDIUMTEXT NULL, ADD COLUMN files_json TEXT NULL, ADD COLUMN pin_fails INT NOT NULL DEFAULT 0, ADD COLUMN locked_until DATETIME NULL, ADD INDEX idx_shares_plugin (plugin_id, created_at);

DROP TABLE IF EXISTS app_plugin_pages;

-- +goose Down
-- ⚠ app_plugin_pages is NOT recreated; see the SQLite file.
ALTER TABLE shares DROP INDEX idx_shares_plugin, DROP COLUMN locked_until, DROP COLUMN pin_fails, DROP COLUMN files_json, DROP COLUMN state_json, DROP COLUMN subject, DROP COLUMN page_id, DROP COLUMN plugin_id;
