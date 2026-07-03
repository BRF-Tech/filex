-- +goose Up
-- API tokens — long-lived bearer credentials for non-interactive callers
-- (AI agents, work.example.com FilexClient, the MCP server). Unlike `sessions`
-- these never expire on their own (optional expires_at) and are not tied to
-- a browser cookie. The plaintext token is shown ONCE at create time; only
-- a sha256 hash is stored here.
--
-- A token is bound to a user (FK) so every REST/MCP call inherits that
-- user's role for the existing auth.Middleware/RequireAdmin checks. `scopes`
-- is a comma-separated allow-list (e.g. "read,write,delete"); empty == all.
CREATE TABLE IF NOT EXISTS api_tokens (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label       TEXT    NOT NULL DEFAULT '',
    token_hash  TEXT    NOT NULL UNIQUE,
    scopes      TEXT    NOT NULL DEFAULT '',
    last_used_at DATETIME,
    expires_at  DATETIME,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id);

-- +goose Down
DROP TABLE IF EXISTS api_tokens;
