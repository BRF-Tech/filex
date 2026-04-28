-- +goose Up
-- +goose StatementBegin

-- 1. storages
CREATE TABLE IF NOT EXISTS storages (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    driver TEXT NOT NULL,
    mount_path TEXT NOT NULL,
    config_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    sync_mode TEXT NOT NULL DEFAULT 'poll',
    sync_interval_s INTEGER NOT NULL DEFAULT 900,
    last_sync_at TIMESTAMPTZ,
    last_sync_token TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    read_only BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 10. users (created early so 'shares' FK can reference it)
CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT,
    role TEXT NOT NULL DEFAULT 'user',
    totp_secret TEXT,
    locale TEXT NOT NULL DEFAULT 'en',
    timezone TEXT NOT NULL DEFAULT 'UTC',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ
);

-- 2. nodes
CREATE TABLE IF NOT EXISTS nodes (
    id BIGSERIAL PRIMARY KEY,
    storage_id BIGINT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    parent_id BIGINT REFERENCES nodes(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    path_hash CHAR(32) NOT NULL,
    storage_key TEXT,
    type TEXT NOT NULL,
    size BIGINT NOT NULL DEFAULT 0,
    mime TEXT,
    etag TEXT,
    backend_mtime TIMESTAMPTZ,
    db_mtime TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sync_state TEXT NOT NULL DEFAULT 'synced',
    seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_storage_pathhash ON nodes(storage_id, path_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_storage_parent_name ON nodes(storage_id, parent_id, name);
CREATE INDEX IF NOT EXISTS idx_nodes_storage_seen ON nodes(storage_id, seen_at);
CREATE INDEX IF NOT EXISTS idx_nodes_deleted ON nodes(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_nodes_storage_etag ON nodes(storage_id, etag);

-- 3. node_meta
CREATE TABLE IF NOT EXISTS node_meta (
    node_id BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT,
    PRIMARY KEY (node_id, key)
);

-- 4. thumbnails
CREATE TABLE IF NOT EXISTS thumbnails (
    node_id BIGINT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'pending',
    storage_key TEXT,
    width INTEGER,
    height INTEGER,
    error TEXT,
    generated_at TIMESTAMPTZ
);

-- 5. node_versions
CREATE TABLE IF NOT EXISTS node_versions (
    id BIGSERIAL PRIMARY KEY,
    node_id BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    version_n INTEGER NOT NULL,
    storage_key TEXT,
    size BIGINT NOT NULL DEFAULT 0,
    etag TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (node_id, version_n)
);

-- 6. shares
CREATE TABLE IF NOT EXISTS shares (
    id BIGSERIAL PRIMARY KEY,
    node_id BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    token CHAR(32) NOT NULL UNIQUE,
    pin_hash TEXT,
    expires_at TIMESTAMPTZ,
    max_downloads INTEGER,
    download_count INTEGER NOT NULL DEFAULT 0,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_shares_node ON shares(node_id);
CREATE INDEX IF NOT EXISTS idx_shares_expires ON shares(expires_at) WHERE expires_at IS NOT NULL;

-- 7. sync_runs
CREATE TABLE IF NOT EXISTS sync_runs (
    id BIGSERIAL PRIMARY KEY,
    storage_id BIGINT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    cursor_before TEXT,
    cursor_after TEXT,
    seen_count INTEGER NOT NULL DEFAULT 0,
    added INTEGER NOT NULL DEFAULT 0,
    updated INTEGER NOT NULL DEFAULT 0,
    deleted INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'running',
    error TEXT
);
CREATE INDEX IF NOT EXISTS idx_sync_runs_storage ON sync_runs(storage_id, started_at);

-- 8. sync_conflicts
CREATE TABLE IF NOT EXISTS sync_conflicts (
    id BIGSERIAL PRIMARY KEY,
    node_id BIGINT REFERENCES nodes(id) ON DELETE CASCADE,
    storage_id BIGINT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    storage_key TEXT,
    db_etag TEXT,
    backend_etag TEXT,
    db_mtime TIMESTAMPTZ,
    backend_mtime TIMESTAMPTZ,
    detected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ,
    resolution TEXT
);

-- 9. chunked_uploads
CREATE TABLE IF NOT EXISTS chunked_uploads (
    id CHAR(32) PRIMARY KEY,
    storage_id BIGINT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL,
    upload_id TEXT NOT NULL,
    total_size BIGINT NOT NULL,
    parts_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chunked_uploads_expires ON chunked_uploads(expires_at);

-- 11. sessions
CREATE TABLE IF NOT EXISTS sessions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token CHAR(64) NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    ip TEXT,
    user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

-- 12. roles
CREATE TABLE IF NOT EXISTS roles (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    permissions_json JSONB NOT NULL DEFAULT '[]'::jsonb
);
INSERT INTO roles (name, permissions_json) VALUES
    ('admin', '["*"]'::jsonb),
    ('user',  '["files.read","files.write","files.delete","files.share"]'::jsonb)
ON CONFLICT (name) DO NOTHING;

-- 13. audit_log
CREATE TABLE IF NOT EXISTS audit_log (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    target_type TEXT,
    target_id TEXT,
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    ip TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_log_user ON audit_log(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_target ON audit_log(target_type, target_id);

-- 14. settings
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 15. external_services
CREATE TABLE IF NOT EXISTS external_services (
    name TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    url TEXT,
    secret_enc TEXT,
    options_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_check TIMESTAMPTZ,
    last_state TEXT
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS external_services CASCADE;
DROP TABLE IF EXISTS settings CASCADE;
DROP TABLE IF EXISTS audit_log CASCADE;
DROP TABLE IF EXISTS roles CASCADE;
DROP TABLE IF EXISTS sessions CASCADE;
DROP TABLE IF EXISTS chunked_uploads CASCADE;
DROP TABLE IF EXISTS sync_conflicts CASCADE;
DROP TABLE IF EXISTS sync_runs CASCADE;
DROP TABLE IF EXISTS shares CASCADE;
DROP TABLE IF EXISTS node_versions CASCADE;
DROP TABLE IF EXISTS thumbnails CASCADE;
DROP TABLE IF EXISTS node_meta CASCADE;
DROP TABLE IF EXISTS nodes CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS storages CASCADE;
-- +goose StatementEnd
