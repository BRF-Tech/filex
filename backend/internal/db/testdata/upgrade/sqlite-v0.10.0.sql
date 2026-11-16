-- The schema filex v0.10.0 leaves in a new SQLite database on its first start,
-- and its goose version rows. Written by make-fixture.sh from the release's
-- published image; do not edit by hand.
--
-- filex-fixture-version: 0.10.0
-- filex-fixture-goose: 22

CREATE TABLE goose_db_version (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		version_id INTEGER NOT NULL,
		is_applied INTEGER NOT NULL,
		tstamp TIMESTAMP DEFAULT (datetime('now'))
	);
CREATE TABLE storages (
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
, role           TEXT    NOT NULL DEFAULT 'primary', replica_of_id  INTEGER, replica_mode   TEXT    NOT NULL DEFAULT 'async', replica_target_id INTEGER, rbac_enabled INTEGER NOT NULL DEFAULT 0);
CREATE TABLE nodes (
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
, owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL);
CREATE TABLE node_meta (
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT,
    PRIMARY KEY (node_id, key)
);
CREATE TABLE thumbnails (
    node_id INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'pending',
    storage_key TEXT,
    width INTEGER,
    height INTEGER,
    error TEXT,
    generated_at DATETIME
);
CREATE TABLE node_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    version_n INTEGER NOT NULL,
    storage_key TEXT,
    size INTEGER NOT NULL DEFAULT 0,
    etag TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (node_id, version_n)
);
CREATE TABLE shares (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    token CHAR(32) NOT NULL UNIQUE,
    pin_hash TEXT,
    expires_at DATETIME,
    max_downloads INTEGER,
    download_count INTEGER NOT NULL DEFAULT 0,
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
, kind TEXT NOT NULL DEFAULT 'download', max_uploads INTEGER, upload_count INTEGER NOT NULL DEFAULT 0, drop_settings TEXT, created_via TEXT NOT NULL DEFAULT '');
CREATE TABLE sync_runs (
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
CREATE TABLE sync_conflicts (
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
CREATE TABLE chunked_uploads (
    id CHAR(32) PRIMARY KEY,
    storage_id INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL,
    upload_id TEXT NOT NULL,
    total_size INTEGER NOT NULL,
    parts_json TEXT NOT NULL DEFAULT '[]',
    expires_at DATETIME NOT NULL
);
CREATE TABLE users (
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
, totp_pending_secret TEXT, totp_enabled INTEGER NOT NULL DEFAULT 0, totp_recovery_codes_json TEXT NOT NULL DEFAULT '[]', quota_bytes BIGINT NOT NULL DEFAULT 0, usage_bytes BIGINT NOT NULL DEFAULT 0, display_name TEXT NOT NULL DEFAULT '', provider_id INTEGER, oidc_subject TEXT, enabled BOOLEAN NOT NULL DEFAULT 1);
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token CHAR(64) NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL,
    ip TEXT,
    user_agent TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE roles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    permissions_json TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    target_type TEXT,
    target_id TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    ip TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE external_services (
    name TEXT PRIMARY KEY,
    enabled INTEGER NOT NULL DEFAULT 0,
    url TEXT,
    secret_enc TEXT,
    options_json TEXT NOT NULL DEFAULT '{}',
    last_check DATETIME,
    last_state TEXT
);
CREATE TABLE user_node_meta (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, node_id, key)
);
CREATE TABLE ops_queue (
    id            TEXT PRIMARY KEY,
    type          TEXT NOT NULL,
    payload       TEXT NOT NULL DEFAULT '{}',
    status        TEXT NOT NULL DEFAULT 'pending',
    priority      INTEGER NOT NULL DEFAULT 0,
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 3,
    last_error    TEXT,
    enqueued_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at    DATETIME,
    finished_at   DATETIME,
    not_before    DATETIME
);
CREATE TABLE notifications (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event           TEXT NOT NULL,
    severity        TEXT NOT NULL,
    title           TEXT NOT NULL,
    body            TEXT NOT NULL,
    meta_json       TEXT NOT NULL DEFAULT '{}',
    user_id         INTEGER REFERENCES users(id) ON DELETE CASCADE,
    read_at         DATETIME,
    webhook_status  TEXT NOT NULL DEFAULT 'pending',
    webhook_error   TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE notification_settings (
    user_id         INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    in_app_enabled  INTEGER NOT NULL DEFAULT 1,
    muted_events    TEXT NOT NULL DEFAULT '[]',
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE replica_rules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    path_pattern    TEXT NOT NULL,
    mode            TEXT NOT NULL,            -- mirror | append_only | skip
    priority        INTEGER NOT NULL DEFAULT 100,
    enabled         INTEGER NOT NULL DEFAULT 1,
    description     TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE replica_failures (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    path            TEXT NOT NULL,
    op              TEXT NOT NULL,            -- write | delete | move | copy
    error_code      TEXT NOT NULL DEFAULT '',
    error_msg       TEXT NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 1,
    last_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at     DATETIME,
    UNIQUE(path, op)
);
CREATE TABLE replica_status_reports (
    id              INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    generated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    total_files     INTEGER NOT NULL DEFAULT 0,
    failed_count    INTEGER NOT NULL DEFAULT 0,
    repaired_count  INTEGER NOT NULL DEFAULT 0,
    summary_json    TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE replica_settings (
    id              INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    report_cron     TEXT NOT NULL DEFAULT '',
    report_enabled  INTEGER NOT NULL DEFAULT 0,
    default_mode    TEXT NOT NULL DEFAULT 'mirror',
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE replication_targets (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL UNIQUE,
    driver      TEXT    NOT NULL,
    config_json TEXT    NOT NULL DEFAULT '{}',
    mode        TEXT    NOT NULL DEFAULT 'async',
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE api_tokens (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label       TEXT    NOT NULL DEFAULT '',
    token_hash  TEXT    NOT NULL UNIQUE,
    scopes      TEXT    NOT NULL DEFAULT '',
    last_used_at DATETIME,
    expires_at  DATETIME,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
, usernames TEXT NOT NULL DEFAULT '');
CREATE TABLE file_grants (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    storage_id  INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path_prefix TEXT    NOT NULL DEFAULT '',
    is_dir      INTEGER NOT NULL DEFAULT 1,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    level       TEXT    NOT NULL,
    created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE providers (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    slug               TEXT NOT NULL UNIQUE,
    name               TEXT NOT NULL DEFAULT '',
    host               TEXT,
    auth_type          TEXT NOT NULL DEFAULT 'oidc',
    oidc_issuer        TEXT,
    oidc_client_id     TEXT,
    oidc_client_secret TEXT,
    oidc_redirect_url  TEXT,
    role_claim         TEXT,
    admin_group        TEXT,
    is_supertenant     INTEGER NOT NULL DEFAULT 0,
    enabled            INTEGER NOT NULL DEFAULT 1,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
, cookie_domain TEXT, plan TEXT, limits_json TEXT, billing_ref TEXT);
CREATE TABLE provider_storages (
    provider_id INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    storage_id  INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    PRIMARY KEY (provider_id, storage_id)
);
CREATE TABLE webhook_targets (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    url         TEXT NOT NULL,
    secret      TEXT NOT NULL DEFAULT '',
    events      TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
, last_status INTEGER, last_error TEXT, last_delivery_at DATETIME);
CREATE TABLE node_comments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id     INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body        TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at  DATETIME
);
CREATE TABLE pending_ops (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT NOT NULL,
			storage_id INTEGER NOT NULL,
			sources_json TEXT NOT NULL,
			dest TEXT,
			total INTEGER NOT NULL DEFAULT 0,
			done INTEGER NOT NULL DEFAULT 0,
			failed INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',
			error TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			finished_at DATETIME
		);
CREATE UNIQUE INDEX idx_nodes_storage_pathhash ON nodes(storage_id, path_hash);
CREATE INDEX idx_nodes_storage_seen ON nodes(storage_id, seen_at);
CREATE INDEX idx_nodes_deleted ON nodes(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_nodes_storage_etag ON nodes(storage_id, etag);
CREATE INDEX idx_shares_node ON shares(node_id);
CREATE INDEX idx_shares_expires ON shares(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX idx_sync_runs_storage ON sync_runs(storage_id, started_at);
CREATE INDEX idx_chunked_uploads_expires ON chunked_uploads(expires_at);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
CREATE INDEX idx_audit_log_user ON audit_log(user_id, created_at);
CREATE INDEX idx_audit_log_target ON audit_log(target_type, target_id);
CREATE INDEX idx_nodes_owner ON nodes(owner_id) WHERE owner_id IS NOT NULL;
CREATE INDEX idx_user_node_meta_userkey ON user_node_meta(user_id, key, updated_at);
CREATE INDEX idx_user_node_meta_node ON user_node_meta(node_id);
CREATE INDEX idx_ops_queue_status_pri_at
    ON ops_queue (status, priority DESC, enqueued_at);
CREATE INDEX idx_ops_queue_type_status
    ON ops_queue (type, status);
CREATE INDEX idx_ops_queue_finished_at
    ON ops_queue (finished_at);
CREATE INDEX idx_notifications_user_read_created
    ON notifications (user_id, read_at, created_at DESC);
CREATE INDEX idx_notifications_event_created
    ON notifications (event, created_at DESC);
CREATE INDEX idx_notifications_webhook_status
    ON notifications (webhook_status, created_at DESC);
CREATE INDEX idx_replica_rules_priority
    ON replica_rules (priority, enabled);
CREATE INDEX idx_replica_failures_unresolved
    ON replica_failures (resolved_at, last_attempt_at DESC);
CREATE INDEX idx_api_tokens_user ON api_tokens(user_id);
CREATE UNIQUE INDEX idx_file_grants_uniq ON file_grants(storage_id, path_prefix, user_id);
CREATE INDEX idx_file_grants_storage_user ON file_grants(storage_id, user_id);
CREATE INDEX idx_file_grants_user ON file_grants(user_id);
CREATE INDEX idx_providers_host ON providers(host);
CREATE INDEX idx_users_provider ON users(provider_id);
CREATE UNIQUE INDEX idx_nodes_storage_parent_name
    ON nodes(storage_id, parent_id, name) WHERE deleted_at IS NULL;
CREATE INDEX idx_node_comments_node
    ON node_comments (node_id, deleted_at);
CREATE INDEX idx_pending_ops_status ON pending_ops(status, created_at);

INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (1, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (2, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (3, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (4, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (5, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (6, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (7, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (8, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (9, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (10, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (11, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (12, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (13, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (14, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (15, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (16, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (17, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (18, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (19, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (20, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (21, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (22, 1);
