-- +goose Up
-- Staged uploads: filex takes the bytes into its own staging area first,
-- acknowledges, and transfers them to the backend in the background
-- (docs/UPLOADS.md, chunk 4 of docs/handovers/2026-08-14-write-path-and-slow-storage.md).
--
-- A NEW TABLE, not an extension of `chunked_uploads` — see the sqlite variant
-- of this migration for the reasoning (different id lifecycle, different part
-- payload, different failure semantics).
CREATE TABLE IF NOT EXISTS staged_uploads (
    id TEXT PRIMARY KEY,
    storage_id BIGINT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL,
    user_id BIGINT,
    total_size BIGINT NOT NULL,
    chunk_size BIGINT NOT NULL,
    mime TEXT NOT NULL DEFAULT '',
    hash TEXT NOT NULL DEFAULT '',
    received_bytes BIGINT NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'staging',
    error TEXT,
    node_id BIGINT,
    op_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_staged_uploads_state ON staged_uploads(state, updated_at);
CREATE INDEX IF NOT EXISTS idx_staged_uploads_user ON staged_uploads(user_id, state);
CREATE INDEX IF NOT EXISTS idx_staged_uploads_node ON staged_uploads(node_id);

-- transfer_state on the node — the seam chunk 5 (read-during-transfer) reads.
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS transfer_state TEXT NOT NULL DEFAULT 'stored';

-- +goose Down
DROP TABLE IF EXISTS staged_uploads;
ALTER TABLE nodes DROP COLUMN IF EXISTS transfer_state;
