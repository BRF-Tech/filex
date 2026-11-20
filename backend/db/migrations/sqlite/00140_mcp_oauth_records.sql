-- +goose Up
-- Durable OAuth state. The singleton lock serializes mutations and bounds
-- record count across application instances and database isolation levels.
CREATE TABLE mcp_oauth_records (
    kind VARCHAR(32) NOT NULL,
    record_key VARCHAR(128) NOT NULL,
    payload TEXT NOT NULL,
    expires_at BIGINT NOT NULL,
    PRIMARY KEY (kind, record_key)
);
CREATE INDEX idx_mcp_oauth_records_expires ON mcp_oauth_records (expires_at);
CREATE TABLE mcp_oauth_record_lock (
    lock_id INTEGER NOT NULL PRIMARY KEY,
    record_count INTEGER NOT NULL,
    client_count INTEGER NOT NULL
);
INSERT INTO mcp_oauth_record_lock (lock_id, record_count, client_count) VALUES (1, 0, 0);

-- +goose Down
DROP TABLE mcp_oauth_record_lock;
DROP TABLE mcp_oauth_records;
