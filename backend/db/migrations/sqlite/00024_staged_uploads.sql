-- +goose Up
-- Staged uploads: filex takes the bytes into its own staging area first,
-- acknowledges, and transfers them to the backend in the background
-- (docs/UPLOADS.md, chunk 4 of docs/handovers/2026-08-14-write-path-and-slow-storage.md).
--
-- A NEW TABLE, not an extension of `chunked_uploads`. The two protocols are
-- not the same object wearing different clothes:
--   * chunked_uploads.upload_id is the BACKEND's multipart id and is NOT NULL —
--     it exists from the first call because the browser PUTs the parts straight
--     to S3. A staged upload has no backend id at all until commit time, and on
--     a driver without multipart it never gets one.
--   * chunked_uploads.parts_json holds {part_number, etag} — S3's completion
--     tokens. Staging parts carry {n, size, md5}: the sizes are what the resume
--     offset is computed from, and the md5s are what a future S3-compatible
--     UploadPart API needs to compute a composite ETag without re-reading data.
--   * The lifecycles differ: a chunked_uploads row dies with the S3 session; a
--     staged row survives a failed transfer on purpose, so the bytes can be
--     retried.
-- Folding both into one table would mean a mode flag on every query and dummy
-- values in NOT NULL columns, and would put the old (working, S3-only) fast
-- path at risk for no gain.
CREATE TABLE IF NOT EXISTS staged_uploads (
    id TEXT PRIMARY KEY,
    storage_id INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    -- Storage-relative destination key, already sanitised at begin.
    storage_key TEXT NOT NULL,
    -- Owner. An upload id is not a capability: every later call re-checks that
    -- the caller is this user AND still has write permission on storage_key.
    user_id INTEGER,
    total_size INTEGER NOT NULL,
    chunk_size INTEGER NOT NULL,
    mime TEXT NOT NULL DEFAULT '',
    -- Optional client-declared content hash, "<algo>:<hex>" (sha256/md5).
    -- Verified at commit before a single byte is handed to the driver.
    hash TEXT NOT NULL DEFAULT '',
    -- Mirror of the on-disk manifest's contiguous offset. The manifest is
    -- authoritative (it is consistent with the files by construction); this
    -- column exists so listings/GC do not have to open every manifest.
    received_bytes INTEGER NOT NULL DEFAULT 0,
    -- staging → committing → failed. Success deletes the row.
    state TEXT NOT NULL DEFAULT 'staging',
    error TEXT,
    node_id INTEGER,
    op_id INTEGER,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_staged_uploads_state ON staged_uploads(state, updated_at);
CREATE INDEX IF NOT EXISTS idx_staged_uploads_user ON staged_uploads(user_id, state);
CREATE INDEX IF NOT EXISTS idx_staged_uploads_node ON staged_uploads(node_id);

-- transfer_state on the node: 'staged' = the row (and its listing entry) exists
-- but the bytes still live in staging, 'stored' = they are on the driver.
-- Everything written before this migration is, by definition, stored.
--
-- SEAM FOR CHUNK 5 (read-during-transfer): every read path (vfStream, /s/, /f/,
-- thumbnails, WebDAV GET) checks this and, when it is not 'stored', serves the
-- assembled staging bytes instead — staged_uploads.node_id is the reverse index
-- that finds the staging directory from a node.
ALTER TABLE nodes ADD COLUMN transfer_state TEXT NOT NULL DEFAULT 'stored';

-- +goose Down
DROP TABLE IF EXISTS staged_uploads;
ALTER TABLE nodes DROP COLUMN transfer_state;
