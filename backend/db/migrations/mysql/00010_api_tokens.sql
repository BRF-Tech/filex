-- +goose Up
-- API tokens — long-lived bearer credentials for non-interactive callers
-- (AI agents, work.example.com FilexClient, the MCP server). See the sqlite
-- migration for the full rationale. Plaintext is shown once; only the
-- sha256 hash is stored.
CREATE TABLE IF NOT EXISTS api_tokens (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id      BIGINT NOT NULL,
    label        VARCHAR(255) NOT NULL DEFAULT '',
    token_hash   VARCHAR(255) NOT NULL UNIQUE,
    scopes       VARCHAR(255) NOT NULL DEFAULT '',
    last_used_at DATETIME NULL,
    expires_at   DATETIME NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_api_tokens_user (user_id),
    CONSTRAINT fk_api_tokens_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS api_tokens;
