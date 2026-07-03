-- +goose Up
-- +goose StatementBegin

-- 1. storages
CREATE TABLE IF NOT EXISTS storages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    driver TEXT NOT NULL,
    mount_path TEXT NOT NULL,
    config_json TEXT NOT NULL DEFAULT '{}',
    sync_mode TEXT NOT NULL DEFAULT 'poll',
    sync_interval_s INTEGER NOT NULL DEFAULT 900,
    last_sync_at DATETIME,
    last_sync_token TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    read_only INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 2. nodes
CREATE TABLE IF NOT EXISTS nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    storage_id INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    parent_id INTEGER REFERENCES nodes(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    path_hash CHAR(32) NOT NULL,
    storage_key TEXT,
    type TEXT NOT NULL,
    size INTEGER NOT NULL DEFAULT 0,
    mime TEXT,
    etag TEXT,
    backend_mtime DATETIME,
    db_mtime DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    sync_state TEXT NOT NULL DEFAULT 'synced',
    seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_storage_pathhash ON nodes(storage_id, path_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_storage_parent_name ON nodes(storage_id, parent_id, name);
CREATE INDEX IF NOT EXISTS idx_nodes_storage_seen ON nodes(storage_id, seen_at);
CREATE INDEX IF NOT EXISTS idx_nodes_deleted ON nodes(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_nodes_storage_etag ON nodes(storage_id, etag);

-- 3. node_meta
CREATE TABLE IF NOT EXISTS node_meta (
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT,
    PRIMARY KEY (node_id, key)
);

-- 4. thumbnails
CREATE TABLE IF NOT EXISTS thumbnails (
    node_id INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'pending',
    storage_key TEXT,
    width INTEGER,
    height INTEGER,
    error TEXT,
    generated_at DATETIME
);

-- 5. node_versions
CREATE TABLE IF NOT EXISTS node_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    version_n INTEGER NOT NULL,
    storage_key TEXT,
    size INTEGER NOT NULL DEFAULT 0,
    etag TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (node_id, version_n)
);

-- 6. shares
CREATE TABLE IF NOT EXISTS shares (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    token CHAR(32) NOT NULL UNIQUE,
    pin_hash TEXT,
    expires_at DATETIME,
    max_downloads INTEGER,
    download_count INTEGER NOT NULL DEFAULT 0,
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_shares_node ON shares(node_id);
CREATE INDEX IF NOT EXISTS idx_shares_expires ON shares(expires_at) WHERE expires_at IS NOT NULL;

-- 7. sync_runs
CREATE TABLE IF NOT EXISTS sync_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    storage_id INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME,
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
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id INTEGER REFERENCES nodes(id) ON DELETE CASCADE,
    storage_id INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    storage_key TEXT,
    db_etag TEXT,
    backend_etag TEXT,
    db_mtime DATETIME,
    backend_mtime DATETIME,
    detected_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at DATETIME,
    resolution TEXT
);

-- 9. chunked_uploads
CREATE TABLE IF NOT EXISTS chunked_uploads (
    id CHAR(32) PRIMARY KEY,
    storage_id INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL,
    upload_id TEXT NOT NULL,
    total_size INTEGER NOT NULL,
    parts_json TEXT NOT NULL DEFAULT '[]',
    expires_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chunked_uploads_expires ON chunked_uploads(expires_at);

-- 10. users
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT,
    role TEXT NOT NULL DEFAULT 'user',
    totp_secret TEXT,
    locale TEXT NOT NULL DEFAULT 'en',
    timezone TEXT NOT NULL DEFAULT 'UTC',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login_at DATETIME
);

-- 11. sessions
CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token CHAR(64) NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL,
    ip TEXT,
    user_agent TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

-- 12. roles
CREATE TABLE IF NOT EXISTS roles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    permissions_json TEXT NOT NULL DEFAULT '[]'
);
INSERT OR IGNORE INTO roles (name, permissions_json) VALUES
    ('admin', '["*"]'),
    ('user',  '["files.read","files.write","files.delete","files.share"]');

-- 13. audit_log
CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    target_type TEXT,
    target_id TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    ip TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_audit_log_user ON audit_log(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_target ON audit_log(target_type, target_id);

-- 14. settings
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 15. external_services
CREATE TABLE IF NOT EXISTS external_services (
    name TEXT PRIMARY KEY,
    enabled INTEGER NOT NULL DEFAULT 0,
    url TEXT,
    secret_enc TEXT,
    options_json TEXT NOT NULL DEFAULT '{}',
    last_check DATETIME,
    last_state TEXT
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS external_services;
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS chunked_uploads;
DROP TABLE IF EXISTS sync_conflicts;
DROP TABLE IF EXISTS sync_runs;
DROP TABLE IF EXISTS shares;
DROP TABLE IF EXISTS node_versions;
DROP TABLE IF EXISTS thumbnails;
DROP TABLE IF EXISTS node_meta;
DROP TABLE IF EXISTS nodes;
DROP TABLE IF EXISTS storages;
-- +goose StatementEnd
