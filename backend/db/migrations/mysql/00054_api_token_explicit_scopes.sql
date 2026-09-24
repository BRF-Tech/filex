-- +goose Up
-- Empty token scope lists become the explicit full list, admin included —
-- see sqlite/00054_api_token_explicit_scopes.sql for why, and why admin is
-- kept (the token keeps exactly the access it had).
--
-- ⚠ One statement, not a goose statement block (issue #19: a block reaches
-- the server as one multi-statement query).
UPDATE api_tokens SET scopes = 'read,write,delete,mcp,admin' WHERE TRIM(COALESCE(scopes, '')) = '';

-- +goose Down
-- No-op: see the sqlite file.
SELECT 1;
