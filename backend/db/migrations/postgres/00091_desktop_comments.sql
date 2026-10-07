-- +goose Up
-- The desktop app's own token comments, like its owner's browser: the
-- pairings made before this version get `comments:rw` - see
-- sqlite/00091_desktop_comments.sql for why, and why only these tokens.
UPDATE api_tokens SET scopes = scopes || ',comments:rw'
WHERE source = 'desktop'
  AND TRIM(COALESCE(scopes, '')) <> ''
  AND (',' || scopes || ',') NOT LIKE '%,comments:%';

-- +goose Down
-- No-op: see the sqlite file.
SELECT 1;
