-- +goose Up
-- The desktop app's own token comments, like its owner's browser: the
-- pairings made before this version get `comments:rw` - see
-- sqlite/00091_desktop_comments.sql for why, and why only these tokens.
--
-- ⚠ CONCAT, not `||`: in MySQL `||` is a logical OR unless the server runs
-- with PIPES_AS_CONCAT. One statement, not a goose statement block (issue
-- #19). `scopes` is VARCHAR(255) here, and a desktop pairing's list is
-- `read,write,delete` at most, so the entry always fits.
UPDATE api_tokens SET scopes = CONCAT(scopes, ',comments:rw')
WHERE source = 'desktop'
  AND TRIM(COALESCE(scopes, '')) <> ''
  AND CONCAT(',', scopes, ',') NOT LIKE '%,comments:%';

-- +goose Down
-- No-op: see the sqlite file.
SELECT 1;
