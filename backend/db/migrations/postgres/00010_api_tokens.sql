-- +goose Up
-- API tokens — long-lived bearer credentials for non-interactive callers
-- (AI agents, work.example.com FilexClient, the MCP server). See the sqlite
-- migration for the full rationale. Plaintext is shown once; only the
-- sha256 hash is stored.
CREATE TABLE IF NOT EXISTS api_tokens (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label        TEXT NOT NULL DEFAULT '',
    token_hash   TEXT NOT NULL UNIQUE,
    scopes       TEXT NOT NULL DEFAULT '',
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id);

-- +goose Down
DROP TABLE IF EXISTS api_tokens;
