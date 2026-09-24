-- +goose Up
-- Empty token scope lists become the explicit full list, admin included —
-- see sqlite/00054_api_token_explicit_scopes.sql for why, and why admin is
-- kept (the token keeps exactly the access it had).
UPDATE api_tokens SET scopes = 'read,write,delete,mcp,admin' WHERE TRIM(COALESCE(scopes, '')) = '';

-- +goose Down
-- No-op: see the sqlite file.
SELECT 1;
