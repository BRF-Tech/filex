-- +goose Up
-- +goose StatementBegin
-- Staged uploads: filex takes the bytes into its own staging area first,
-- acknowledges, and transfers them to the backend in the background
-- (docs/UPLOADS.md, chunk 4 of docs/handovers/2026-08-14-write-path-and-slow-storage.md).
--
-- A NEW TABLE, not an extension of `chunked_uploads` — see the sqlite variant
-- of this migration for the reasoning (different id lifecycle, different part
-- payload, different failure semantics).
CREATE TABLE IF NOT EXISTS staged_uploads (
    id VARCHAR(64) NOT NULL PRIMARY KEY,
    storage_id BIGINT NOT NULL,
    storage_key VARCHAR(2048) NOT NULL,
    user_id BIGINT,
    total_size BIGINT NOT NULL,
    chunk_size BIGINT NOT NULL,
    mime VARCHAR(190) NOT NULL DEFAULT '',
    hash VARCHAR(190) NOT NULL DEFAULT '',
    received_bytes BIGINT NOT NULL DEFAULT 0,
    state VARCHAR(32) NOT NULL DEFAULT 'staging',
    error TEXT,
    node_id BIGINT,
    op_id BIGINT,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    expires_at DATETIME(6) NOT NULL,
    KEY idx_staged_uploads_state (state, updated_at),
    KEY idx_staged_uploads_user (user_id, state),
    KEY idx_staged_uploads_node (node_id),
    CONSTRAINT fk_staged_uploads_storage FOREIGN KEY (storage_id) REFERENCES storages(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
-- +goose StatementEnd

-- +goose StatementBegin
-- transfer_state on the node — the seam chunk 5 (read-during-transfer) reads.
-- VARCHAR, not TEXT: MySQL refuses a DEFAULT on a TEXT column.
ALTER TABLE nodes ADD COLUMN transfer_state VARCHAR(16) NOT NULL DEFAULT 'stored';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS staged_uploads;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE nodes DROP COLUMN transfer_state;
-- +goose StatementEnd
