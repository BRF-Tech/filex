-- +goose Up
-- Durable OAuth state. The singleton lock serializes mutations and bounds
-- record count across application instances and database isolation levels.
CREATE TABLE mcp_oauth_records (
    kind VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    record_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    payload TEXT NOT NULL,
    expires_at BIGINT NOT NULL,
    PRIMARY KEY (kind, record_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE INDEX idx_mcp_oauth_records_expires ON mcp_oauth_records (expires_at);
CREATE TABLE mcp_oauth_record_lock (
    lock_id INTEGER NOT NULL PRIMARY KEY,
    record_count INTEGER NOT NULL,
    client_count INTEGER NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO mcp_oauth_record_lock (lock_id, record_count, client_count) VALUES (1, 0, 0);

-- +goose Down
DROP TABLE mcp_oauth_record_lock;
DROP TABLE mcp_oauth_records;
