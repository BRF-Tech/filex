-- +goose Up
-- +goose StatementBegin

-- 1. storages
CREATE TABLE IF NOT EXISTS storages (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(190) NOT NULL UNIQUE,
    driver VARCHAR(64) NOT NULL,
    mount_path VARCHAR(500) NOT NULL,
    config_json JSON NOT NULL,
    sync_mode VARCHAR(32) NOT NULL DEFAULT 'poll',
    sync_interval_s INT NOT NULL DEFAULT 900,
    last_sync_at DATETIME(6),
    last_sync_token TEXT,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    read_only TINYINT(1) NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 10. users (early for FK)
CREATE TABLE IF NOT EXISTS users (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    email VARCHAR(190) NOT NULL UNIQUE,
    password_hash VARCHAR(255),
    role VARCHAR(32) NOT NULL DEFAULT 'user',
    totp_secret VARCHAR(255),
    locale VARCHAR(8) NOT NULL DEFAULT 'en',
    timezone VARCHAR(64) NOT NULL DEFAULT 'UTC',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    last_login_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 2. nodes
CREATE TABLE IF NOT EXISTS nodes (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    storage_id BIGINT NOT NULL,
    parent_id BIGINT,
    name VARCHAR(255) NOT NULL,
    path VARCHAR(2048) NOT NULL,
    path_hash CHAR(32) NOT NULL,
    storage_key VARCHAR(2048),
    type VARCHAR(16) NOT NULL,
    size BIGINT NOT NULL DEFAULT 0,
    mime VARCHAR(190),
    etag VARCHAR(190),
    backend_mtime DATETIME(6),
    db_mtime DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    sync_state VARCHAR(32) NOT NULL DEFAULT 'synced',
    seen_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY idx_nodes_storage_pathhash (storage_id, path_hash),
    UNIQUE KEY idx_nodes_storage_parent_name (storage_id, parent_id, name),
    KEY idx_nodes_storage_seen (storage_id, seen_at),
    KEY idx_nodes_deleted (deleted_at),
    KEY idx_nodes_storage_etag (storage_id, etag),
    CONSTRAINT fk_nodes_storage FOREIGN KEY (storage_id) REFERENCES storages(id) ON DELETE CASCADE,
    CONSTRAINT fk_nodes_parent  FOREIGN KEY (parent_id)  REFERENCES nodes(id)    ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 3. node_meta
CREATE TABLE IF NOT EXISTS node_meta (
    node_id BIGINT NOT NULL,
    `key` VARCHAR(190) NOT NULL,
    value TEXT,
    PRIMARY KEY (node_id, `key`),
    CONSTRAINT fk_meta_node FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 4. thumbnails
CREATE TABLE IF NOT EXISTS thumbnails (
    node_id BIGINT NOT NULL PRIMARY KEY,
    state VARCHAR(32) NOT NULL DEFAULT 'pending',
    storage_key VARCHAR(2048),
    width INT,
    height INT,
    error TEXT,
    generated_at DATETIME(6),
    CONSTRAINT fk_thumb_node FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 5. node_versions
CREATE TABLE IF NOT EXISTS node_versions (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    node_id BIGINT NOT NULL,
    version_n INT NOT NULL,
    storage_key VARCHAR(2048),
    size BIGINT NOT NULL DEFAULT 0,
    etag VARCHAR(190),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY idx_node_version (node_id, version_n),
    CONSTRAINT fk_ver_node FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 6. shares
CREATE TABLE IF NOT EXISTS shares (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    node_id BIGINT NOT NULL,
    token CHAR(32) NOT NULL UNIQUE,
    pin_hash VARCHAR(255),
    expires_at DATETIME(6),
    max_downloads INT,
    download_count INT NOT NULL DEFAULT 0,
    created_by BIGINT,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    KEY idx_shares_node (node_id),
    KEY idx_shares_expires (expires_at),
    CONSTRAINT fk_share_node FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE,
    CONSTRAINT fk_share_user FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 7. sync_runs
CREATE TABLE IF NOT EXISTS sync_runs (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    storage_id BIGINT NOT NULL,
    started_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    finished_at DATETIME(6),
    cursor_before TEXT,
    cursor_after TEXT,
    seen_count INT NOT NULL DEFAULT 0,
    added INT NOT NULL DEFAULT 0,
    updated INT NOT NULL DEFAULT 0,
    deleted INT NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL DEFAULT 'running',
    error TEXT,
    KEY idx_sync_runs_storage (storage_id, started_at),
    CONSTRAINT fk_run_storage FOREIGN KEY (storage_id) REFERENCES storages(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 8. sync_conflicts
CREATE TABLE IF NOT EXISTS sync_conflicts (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    node_id BIGINT,
    storage_id BIGINT NOT NULL,
    storage_key VARCHAR(2048),
    db_etag VARCHAR(190),
    backend_etag VARCHAR(190),
    db_mtime DATETIME(6),
    backend_mtime DATETIME(6),
    detected_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    resolved_at DATETIME(6),
    resolution VARCHAR(64),
    CONSTRAINT fk_conflict_node FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE,
    CONSTRAINT fk_conflict_storage FOREIGN KEY (storage_id) REFERENCES storages(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 9. chunked_uploads
CREATE TABLE IF NOT EXISTS chunked_uploads (
    id CHAR(32) NOT NULL PRIMARY KEY,
    storage_id BIGINT NOT NULL,
    storage_key VARCHAR(2048) NOT NULL,
    upload_id VARCHAR(255) NOT NULL,
    total_size BIGINT NOT NULL,
    parts_json JSON NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    KEY idx_chunked_uploads_expires (expires_at),
    CONSTRAINT fk_upload_storage FOREIGN KEY (storage_id) REFERENCES storages(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 11. sessions
CREATE TABLE IF NOT EXISTS sessions (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT NOT NULL,
    token CHAR(64) NOT NULL UNIQUE,
    expires_at DATETIME(6) NOT NULL,
    ip VARCHAR(64),
    user_agent VARCHAR(500),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    KEY idx_sessions_user (user_id),
    KEY idx_sessions_expires (expires_at),
    CONSTRAINT fk_session_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 12. roles
CREATE TABLE IF NOT EXISTS roles (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(64) NOT NULL UNIQUE,
    permissions_json JSON NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
INSERT IGNORE INTO roles (name, permissions_json) VALUES
    ('admin', '["*"]'),
    ('user',  '["files.read","files.write","files.delete","files.share"]');

-- 13. audit_log
CREATE TABLE IF NOT EXISTS audit_log (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT,
    action VARCHAR(64) NOT NULL,
    target_type VARCHAR(64),
    target_id VARCHAR(64),
    metadata_json JSON NOT NULL,
    ip VARCHAR(64),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    KEY idx_audit_log_user (user_id, created_at),
    KEY idx_audit_log_target (target_type, target_id),
    CONSTRAINT fk_audit_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 14. settings
CREATE TABLE IF NOT EXISTS settings (
    `key` VARCHAR(190) NOT NULL PRIMARY KEY,
    value TEXT,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 15. external_services
CREATE TABLE IF NOT EXISTS external_services (
    name VARCHAR(64) NOT NULL PRIMARY KEY,
    enabled TINYINT(1) NOT NULL DEFAULT 0,
    url VARCHAR(500),
    secret_enc TEXT,
    options_json JSON NOT NULL,
    last_check DATETIME(6),
    last_state VARCHAR(32)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET FOREIGN_KEY_CHECKS=0;
DROP TABLE IF EXISTS external_services;
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS chunked_uploads;
DROP TABLE IF EXISTS sync_conflicts;
DROP TABLE IF EXISTS sync_runs;
DROP TABLE IF EXISTS shares;
DROP TABLE IF EXISTS node_versions;
DROP TABLE IF EXISTS thumbnails;
DROP TABLE IF EXISTS node_meta;
DROP TABLE IF EXISTS nodes;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS storages;
SET FOREIGN_KEY_CHECKS=1;
-- +goose StatementEnd
