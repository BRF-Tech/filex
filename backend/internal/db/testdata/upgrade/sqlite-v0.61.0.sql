-- The schema filex v0.61.0 leaves in a new SQLite database on its first start,
-- and its goose version rows. Written by make-fixture.sh from the release's
-- published image; do not edit by hand.
--
-- filex-fixture-version: 0.61.0
-- filex-fixture-goose: 139

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
, role           TEXT    NOT NULL DEFAULT 'primary', replica_of_id  INTEGER, replica_mode   TEXT    NOT NULL DEFAULT 'async', replica_target_id INTEGER, rbac_enabled INTEGER NOT NULL DEFAULT 0, uid TEXT, sort_order INTEGER);
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
, owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL, transfer_state TEXT NOT NULL DEFAULT 'stored', last_actor_id INTEGER REFERENCES users(id) ON DELETE SET NULL, external_upload INTEGER NOT NULL DEFAULT 0, deleted_by INTEGER REFERENCES users(id) ON DELETE SET NULL, unavailable_reason TEXT, unavailable_at DATETIME, link_state TEXT);
CREATE TABLE node_meta (
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    meta_key TEXT NOT NULL,
    value TEXT,
    PRIMARY KEY (node_id, meta_key)
);
CREATE TABLE thumbnails (
    node_id INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'pending',
    storage_key TEXT,
    width INTEGER,
    height INTEGER,
    error TEXT,
    generated_at DATETIME
, source_sig TEXT NOT NULL DEFAULT '', attempted_at DATETIME, generator TEXT NOT NULL DEFAULT '', attempts TEXT NOT NULL DEFAULT '');
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
, kind TEXT NOT NULL DEFAULT 'download', max_uploads INTEGER, upload_count INTEGER NOT NULL DEFAULT 0, drop_settings TEXT, created_via TEXT NOT NULL DEFAULT '', plugin_id INTEGER NOT NULL DEFAULT 0, page_id TEXT NOT NULL DEFAULT '', subject TEXT NOT NULL DEFAULT '', state_json TEXT, files_json TEXT, pin_fails INTEGER NOT NULL DEFAULT 0, locked_until DATETIME, pin_enc TEXT, visit_count INTEGER NOT NULL DEFAULT 0, purpose_json TEXT, revoked_at DATETIME, app_root TEXT, tenant_id INTEGER);
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
, peak_mem_bytes INTEGER NOT NULL DEFAULT 0);
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
, totp_pending_secret TEXT, totp_enabled INTEGER NOT NULL DEFAULT 0, totp_recovery_codes_json TEXT NOT NULL DEFAULT '[]', quota_bytes BIGINT NOT NULL DEFAULT 0, usage_bytes BIGINT NOT NULL DEFAULT 0, display_name TEXT NOT NULL DEFAULT '', provider_id INTEGER, oidc_subject TEXT, enabled BOOLEAN NOT NULL DEFAULT 1, avatar_url TEXT, username TEXT, oidc_issuer TEXT, disabled_reason TEXT, auth_source TEXT NOT NULL DEFAULT 'local', auth_directory TEXT NOT NULL DEFAULT '', directory_id TEXT NOT NULL DEFAULT '', admin_by_group INTEGER NOT NULL DEFAULT 0);
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token CHAR(64) NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL,
    ip TEXT,
    user_agent TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
, id_token TEXT);
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
    setting_key TEXT PRIMARY KEY,
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
    meta_key TEXT NOT NULL,
    value TEXT,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, node_id, meta_key)
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
, dedup_key TEXT);
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
, urgent_overrides TEXT);
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
, usernames TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT 'app', source TEXT NOT NULL DEFAULT '');
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
, cookie_domain TEXT, plan TEXT, limits_json TEXT, billing_ref TEXT, realm TEXT, allow_insecure_auth INTEGER NOT NULL DEFAULT 0, oidc_trust_email INTEGER NOT NULL DEFAULT 0, e2e_allowed INTEGER NOT NULL DEFAULT 1, e2e_policy TEXT NOT NULL DEFAULT 'permitted');
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
, last_status INTEGER, last_error TEXT, last_delivery_at DATETIME, lang TEXT NOT NULL DEFAULT '');
CREATE TABLE node_comments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id     INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body        TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at  DATETIME
);
CREATE TABLE staged_uploads (
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
CREATE TABLE s3_access_keys (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    access_key_id TEXT NOT NULL UNIQUE,
    secret_enc    TEXT NOT NULL,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_token_id  INTEGER REFERENCES api_tokens(id) ON DELETE CASCADE,
    label         TEXT NOT NULL DEFAULT '',
    -- Optional (bucket, prefix) confinement, the shape an S3 client already
    -- understands. Empty bucket = the whole account's visible storages.
    bucket        TEXT NOT NULL DEFAULT '',
    prefix        TEXT NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at  DATETIME,
    expires_at    DATETIME,
    disabled_at   DATETIME
);
CREATE TABLE ssh_public_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    fingerprint  TEXT NOT NULL UNIQUE,
    -- The normalised wire form (`<type> <base64>`), so the key can be shown
    -- back and exported. The comment a user pasted is kept in `name`.
    public_key   TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME,
    disabled_at  DATETIME
);
CREATE TABLE nfs_exports (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- The API token this export inherits from, or NULL when it was minted from
    -- the account. The FK cascades: revoking the token revokes the mount.
    api_token_id INTEGER REFERENCES api_tokens(id) ON DELETE CASCADE,
    label        TEXT NOT NULL DEFAULT '',
    -- sha256 of the path secret. UNIQUE because it is what a mount is looked
    -- up by.
    token_hash   TEXT NOT NULL UNIQUE,
    -- Optional confinement, the same (storage, prefix) shape the S3 keys use.
    storage_name TEXT NOT NULL DEFAULT '',
    prefix       TEXT NOT NULL DEFAULT '',
    -- ⚠ Read-only is a per-export choice here, unlike the other protocols. An
    -- NFS mount is used by a MACHINE — a media player, a build box, a backup
    -- reader — and "this one may only read" is the commonest thing an operator
    -- wants to say about one.
    read_only    INTEGER NOT NULL DEFAULT 0,
    -- Optional CIDR allow-list, comma-separated. Empty means any address the
    -- listener itself accepts.
    allow_cidrs  TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME,
    expires_at   DATETIME,
    disabled_at  DATETIME
);
CREATE TABLE plugins (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,
    kind         TEXT NOT NULL DEFAULT 'binary',
    binary_path       TEXT NOT NULL DEFAULT '',
    sha256       TEXT NOT NULL DEFAULT '',
    address      TEXT NOT NULL DEFAULT '',
    token_sealed TEXT NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1,
    version      TEXT NOT NULL DEFAULT '',
    driver       TEXT NOT NULL DEFAULT '',
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
, source TEXT NOT NULL DEFAULT '', update_json TEXT NOT NULL DEFAULT '');
CREATE TABLE pending_ops (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    kind            TEXT NOT NULL,
    storage_id      INTEGER NOT NULL,
    sources_json    TEXT NOT NULL,
    dest            TEXT,
    total           INTEGER NOT NULL DEFAULT 0,
    done            INTEGER NOT NULL DEFAULT 0,
    failed          INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending',
    error           TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at      DATETIME,
    finished_at     DATETIME,
    dest_storage_id INTEGER NOT NULL DEFAULT 0
, actor_id INTEGER, skipped INTEGER NOT NULL DEFAULT 0);
CREATE TABLE user_view_prefs (
    user_id INTEGER NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    prefs_json TEXT,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE app_plugins (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT NOT NULL UNIQUE,
    version          TEXT NOT NULL DEFAULT '',
    label_json       TEXT NOT NULL DEFAULT '{}',
    manifest_json    TEXT NOT NULL DEFAULT '{}',
    wasm_path        TEXT NOT NULL DEFAULT '',
    sha256           TEXT NOT NULL DEFAULT '',
    source           TEXT NOT NULL DEFAULT 'upload',
    source_url       TEXT NOT NULL DEFAULT '',
    signed           INTEGER NOT NULL DEFAULT 0,
    permissions_json TEXT NOT NULL DEFAULT '[]',
    enabled          INTEGER NOT NULL DEFAULT 1,
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
, auto_update INTEGER NOT NULL DEFAULT 1, manifest_url TEXT NOT NULL DEFAULT '', update_json TEXT NOT NULL DEFAULT '', ui_sha256 TEXT NOT NULL DEFAULT '', signature TEXT NOT NULL DEFAULT '');
CREATE TABLE app_plugin_settings (
    plugin_id  INTEGER NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (plugin_id, key)
);
CREATE TABLE app_plugin_overrides (
    plugin_id    INTEGER NOT NULL,
    action_id    TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1,
    admin_only   INTEGER NOT NULL DEFAULT 0,
    applies_json TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (plugin_id, action_id)
);
CREATE TABLE app_plugin_state (
    plugin_id  INTEGER NOT NULL,
    storage_id INTEGER NOT NULL,
    path_hash  TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, rel TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (plugin_id, storage_id, path_hash, key)
);
CREATE TABLE app_plugin_jobs (
    id           TEXT PRIMARY KEY,
    op_id        INTEGER,
    plugin_id    INTEGER NOT NULL,
    plugin_name  TEXT NOT NULL DEFAULT '',
    action_id    TEXT NOT NULL DEFAULT '',
    storage_id   INTEGER NOT NULL,
    paths_json   TEXT NOT NULL DEFAULT '[]',
    params_json  TEXT NOT NULL DEFAULT '{}',
    actor_id     INTEGER,
    locale       TEXT NOT NULL DEFAULT '',
    label        TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending',
    message      TEXT NOT NULL DEFAULT '',
    outputs_json TEXT NOT NULL DEFAULT '[]',
    error        TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at  DATETIME
);
CREATE TABLE app_plugin_signing_keys (
    id           TEXT PRIMARY KEY,
    tenant_id    INTEGER NOT NULL DEFAULT 0,
    plugin_id    INTEGER NOT NULL DEFAULT 0,
    purpose      TEXT NOT NULL DEFAULT 'leaf',
    subject      TEXT NOT NULL DEFAULT '',
    cert_pem     TEXT NOT NULL DEFAULT '',
    key_sealed   TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at   DATETIME,
    destroyed_at DATETIME,
    retired_at   DATETIME
);
CREATE TABLE app_plugin_locks (
    storage_id  INTEGER NOT NULL,
    path_hash   TEXT NOT NULL,
    rel         TEXT NOT NULL DEFAULT '',
    plugin_id   INTEGER NOT NULL,
    plugin_name TEXT NOT NULL DEFAULT '',
    reason      TEXT NOT NULL DEFAULT '',
    until       DATETIME,
    created_by  INTEGER,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (storage_id, path_hash)
);
CREATE TABLE user_prefs (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    surface TEXT NOT NULL,
    doc TEXT,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, surface)
);
CREATE TABLE app_plugin_schedule (
    plugin_id   INTEGER NOT NULL,
    key         TEXT NOT NULL,
    due_at      DATETIME NOT NULL,
    action_id   TEXT NOT NULL DEFAULT '',
    storage_id  INTEGER NOT NULL DEFAULT 0,
    paths_json  TEXT NOT NULL DEFAULT '[]',
    params_json TEXT NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'due',
    attempts    INTEGER NOT NULL DEFAULT 0,
    claimed_by  TEXT NOT NULL DEFAULT '',
    claimed_at  DATETIME,
    job_id      TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plugin_id, key)
);
CREATE TABLE custom_themes (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    theme_key    TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    tokens_light TEXT NOT NULL,
    tokens_dark  TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE tags (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,
    tenant_id  INTEGER,
    owner_id   INTEGER REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    name_key   TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE node_tags (
    tag_id     INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    node_id    INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tag_id, node_id)
);
CREATE TABLE notification_read_through (
    user_id    INTEGER NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    through_id INTEGER NOT NULL,
    read_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE notification_reads (
    notification_id INTEGER NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (notification_id, user_id)
);
CREATE TABLE catalogue_folders (
    storage_id        INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path_hash         TEXT NOT NULL,
    path              TEXT NOT NULL,
    depth             INTEGER NOT NULL DEFAULT 0,
    state             TEXT NOT NULL DEFAULT 'uncatalogued',
    reconciled_at     DATETIME,
    visited_at        DATETIME,
    watched_at        DATETIME,
    reconcile_on_open INTEGER NOT NULL DEFAULT 0,
    entries           INTEGER NOT NULL DEFAULT 0,
    held_back         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (storage_id, path_hash)
);
CREATE TABLE drafts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    draft_key   TEXT NOT NULL,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    storage_id  INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    node_id     INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    target_dir  TEXT NOT NULL DEFAULT '',
    target_name TEXT NOT NULL,
    doc_type    TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE app_plugin_versions (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    plugin_id        INTEGER NOT NULL REFERENCES app_plugins(id) ON DELETE CASCADE,
    version          TEXT NOT NULL,
    manifest_json    TEXT NOT NULL,
    wasm_path        TEXT NOT NULL DEFAULT '',
    sha256           TEXT NOT NULL,
    ui_sha256        TEXT NOT NULL DEFAULT '',
    permissions_json TEXT NOT NULL DEFAULT '[]',
    source           TEXT NOT NULL DEFAULT '',
    source_url       TEXT NOT NULL DEFAULT '',
    manifest_url     TEXT NOT NULL DEFAULT '',
    signed           INTEGER NOT NULL DEFAULT 0,
    signature        TEXT NOT NULL DEFAULT '',
    dir              TEXT NOT NULL,
    replaced_by      INTEGER NULL,
    replaced_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE user_permissions (
    user_id        INTEGER NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    overrides_json TEXT    NOT NULL DEFAULT '{}',
    updated_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE permission_rules (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT    NOT NULL,
    description      TEXT    NOT NULL DEFAULT '',
    enabled          INTEGER NOT NULL DEFAULT 1,
    permissions_json TEXT    NOT NULL DEFAULT '[]',
    provider_id      INTEGER REFERENCES providers(id) ON DELETE CASCADE,
    targets_json     TEXT    NOT NULL DEFAULT '[]',
    effects_json     TEXT    NOT NULL DEFAULT '{}',
    settings_json    TEXT    NOT NULL DEFAULT '{}',
    conditions_json  TEXT    NOT NULL DEFAULT '{}',
    created_by       INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
, names_json TEXT NOT NULL DEFAULT '{}', descriptions_json TEXT NOT NULL DEFAULT '{}');
CREATE TABLE user_custom_roles (
    user_id INTEGER NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    role_id INTEGER NOT NULL REFERENCES permission_rules(id) ON DELETE CASCADE
);
CREATE TABLE user_sso_groups (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_name TEXT    NOT NULL,
    PRIMARY KEY (user_id, group_name)
);
CREATE TABLE plugin_requests (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    request_key      TEXT NOT NULL,
    kind             TEXT NOT NULL,
    op               TEXT NOT NULL,
    name             TEXT NOT NULL DEFAULT '',
    plugin_id        INTEGER,
    source_kind      TEXT NOT NULL,
    source_json      TEXT NOT NULL DEFAULT '{}',
    source_key       TEXT NOT NULL,
    version          TEXT NOT NULL DEFAULT '',
    from_version     TEXT NOT NULL DEFAULT '',
    manifest_json    TEXT NOT NULL DEFAULT '',
    review_json      TEXT NOT NULL DEFAULT '',
    sha256           TEXT NOT NULL DEFAULT '',
    manifest_sha256  TEXT NOT NULL DEFAULT '',
    permissions_json TEXT NOT NULL DEFAULT '[]',
    requested_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    requester        TEXT NOT NULL DEFAULT '',
    token_id         INTEGER,
    token_label      TEXT NOT NULL DEFAULT '',
    reason           TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'pending',
    decided_by       INTEGER REFERENCES users(id) ON DELETE SET NULL,
    decider          TEXT NOT NULL DEFAULT '',
    decided_at       DATETIME,
    decision_note    TEXT NOT NULL DEFAULT '',
    result_json      TEXT NOT NULL DEFAULT '',
    expires_at       DATETIME NOT NULL,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE login_throttle (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    scope         TEXT NOT NULL,
    subject       TEXT NOT NULL,
    fails         INTEGER NOT NULL DEFAULT 0,
    lock_level    INTEGER NOT NULL DEFAULT 0,
    window_start  DATETIME NOT NULL,
    locked_until  DATETIME,
    last_fail_at  DATETIME NOT NULL,
    last_ip       TEXT NOT NULL DEFAULT '',
    last_protocol TEXT NOT NULL DEFAULT '',
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE user_groups (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    provider_id INTEGER REFERENCES providers(id) ON DELETE CASCADE,
    role_id     INTEGER REFERENCES permission_rules(id) ON DELETE SET NULL,
    priority    INTEGER NOT NULL DEFAULT 0,
    tenant_key  INTEGER NOT NULL DEFAULT 0,
    links_json  TEXT    NOT NULL DEFAULT '[]',
    created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
, directory_id    TEXT NOT NULL DEFAULT '', directory_name  TEXT NOT NULL DEFAULT '', directory_state TEXT NOT NULL DEFAULT '', gives_admin INTEGER NOT NULL DEFAULT 0);
CREATE TABLE user_group_members (
    group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source   TEXT    NOT NULL DEFAULT 'manual',
    added_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (group_id, user_id)
);
CREATE TABLE group_file_grants (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    storage_id  INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path_prefix TEXT    NOT NULL DEFAULT '',
    is_dir      INTEGER NOT NULL DEFAULT 1,
    group_id    INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    level       TEXT    NOT NULL,
    created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE user_group_levels (
    user_id      INTEGER NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    level_before TEXT    NOT NULL
);
CREATE TABLE auth_instances (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    slug              TEXT    NOT NULL,
    driver            TEXT    NOT NULL,
    label             TEXT    NOT NULL DEFAULT '',
    origin            TEXT    NOT NULL DEFAULT 'page',
    owner_provider_id INTEGER REFERENCES providers(id) ON DELETE CASCADE,
    enabled           INTEGER NOT NULL DEFAULT 0,
    legacy            INTEGER NOT NULL DEFAULT 0,
    config_json       TEXT    NOT NULL DEFAULT '{}',
    promoted_json     TEXT    NOT NULL DEFAULT '[]',
    created_by        INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE provider_auth_instances (
    provider_id INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    instance_id INTEGER NOT NULL REFERENCES auth_instances(id) ON DELETE CASCADE,
    source      TEXT    NOT NULL DEFAULT 'explicit',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (provider_id, instance_id)
);
CREATE TABLE provider_domains (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id    INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    domain         TEXT    NOT NULL,
    status         TEXT    NOT NULL DEFAULT 'pending',
    last_error     TEXT    NOT NULL DEFAULT '',
    last_error_code   TEXT NOT NULL DEFAULT '',
    last_error_params TEXT NOT NULL DEFAULT '{}',
    checked_at     DATETIME,
    active_since   DATETIME,
    tls_cert_pem   TEXT    NOT NULL DEFAULT '',
    tls_key_sealed TEXT    NOT NULL DEFAULT '',
    tls_not_after  DATETIME,
    created_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE file_associations (
    capability    TEXT NOT NULL,
    ext           TEXT NOT NULL,
    handlers_json TEXT NOT NULL DEFAULT '[]',
    off_json      TEXT NOT NULL DEFAULT '[]',
    updated_by    INTEGER,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (capability, ext)
);
CREATE TABLE app_thumb_limits (
    plugin_id    INTEGER PRIMARY KEY,
    max_input_mb INTEGER NOT NULL DEFAULT 0,
    timeout_s    INTEGER NOT NULL DEFAULT 0,
    memory_mb    INTEGER NOT NULL DEFAULT 0,
    concurrency  INTEGER NOT NULL DEFAULT 0,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE e2e_requests (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    request_key   TEXT NOT NULL,
    provider_id   INTEGER REFERENCES providers(id) ON DELETE CASCADE,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requester     TEXT NOT NULL DEFAULT '',
    storage_id    INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path          TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL,
    reason        TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'pending',
    decided_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    decider       TEXT NOT NULL DEFAULT '',
    decided_at    DATETIME,
    decision_note TEXT NOT NULL DEFAULT '',
    expires_at    DATETIME NOT NULL,
    used_at       DATETIME,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE app_store_state (
    state_key   TEXT NOT NULL PRIMARY KEY,
    state_value TEXT NOT NULL DEFAULT '',
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE user_ldap_groups (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_name TEXT    NOT NULL,
    PRIMARY KEY (user_id, group_name)
);
CREATE TABLE notify_digest_state (
    user_id     INTEGER NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    through_id  INTEGER NOT NULL DEFAULT 0,
    due_at      DATETIME,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE notify_digest_policy (
    scope_id        INTEGER NOT NULL PRIMARY KEY,
    window_minutes  INTEGER NOT NULL DEFAULT 1,
    urgent_events   TEXT,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE recent_searches (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    surface     TEXT NOT NULL DEFAULT 'admin',
    query_text  TEXT NOT NULL,
    searched_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE office_sessions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    doc_key         TEXT NOT NULL,
    node_id         INTEGER NOT NULL,
    file_size       INTEGER NOT NULL DEFAULT 0,
    file_mtime_ns   INTEGER NOT NULL DEFAULT 0,
    file_etag       TEXT NOT NULL DEFAULT '',
    version_unknown INTEGER NOT NULL DEFAULT 0,
    dropped         INTEGER NOT NULL DEFAULT 0,
    expires_unix    INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE "replica_failures" (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    storage_id      INTEGER NOT NULL DEFAULT 0,
    path            TEXT NOT NULL,
    op              TEXT NOT NULL,
    error_code      TEXT NOT NULL DEFAULT '',
    error_msg       TEXT NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 1,
    last_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at     DATETIME,
    UNIQUE(storage_id, path, op)
);
CREATE TABLE replica_initial_copies (
    storage_id      INTEGER PRIMARY KEY,
    target_id       INTEGER NOT NULL DEFAULT 0,
    phase           TEXT NOT NULL DEFAULT 'pending',
    walk_cursor     TEXT NOT NULL DEFAULT '',
    counted         INTEGER NOT NULL DEFAULT 0,
    total_files     INTEGER NOT NULL DEFAULT 0,
    copied_files    INTEGER NOT NULL DEFAULT 0,
    present_files   INTEGER NOT NULL DEFAULT 0,
    excluded_files  INTEGER NOT NULL DEFAULT 0,
    failed_files    INTEGER NOT NULL DEFAULT 0,
    copied_bytes    INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    started_unix    INTEGER NOT NULL DEFAULT 0,
    updated_unix    INTEGER NOT NULL DEFAULT 0,
    finished_unix   INTEGER NOT NULL DEFAULT 0,
    lease_owner     TEXT NOT NULL DEFAULT '',
    lease_until     INTEGER NOT NULL DEFAULT 0,
    revision        INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE replica_links (
    storage_id    INTEGER PRIMARY KEY,
    target_id     INTEGER NOT NULL DEFAULT 0,
    folder        TEXT NOT NULL DEFAULT '',
    folder_key    TEXT NOT NULL DEFAULT '',
    created_unix  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE vault_locks (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id         INTEGER NOT NULL DEFAULT 0,
    vault_id          TEXT NOT NULL,
    rev               INTEGER NOT NULL DEFAULT 0,
    token_hash        TEXT NOT NULL DEFAULT '',
    holder_user_id    INTEGER NOT NULL DEFAULT 0,
    holder_name       TEXT NOT NULL DEFAULT '',
    holder_client     TEXT NOT NULL DEFAULT '',
    holder_label      TEXT NOT NULL DEFAULT '',
    storage_id        INTEGER NOT NULL DEFAULT 0,
    path              TEXT NOT NULL DEFAULT '',
    taken_ms          INTEGER NOT NULL DEFAULT 0,
    lease_ms          INTEGER NOT NULL DEFAULT 0,
    active_ms         INTEGER NOT NULL DEFAULT 0,
    idle_seconds      INTEGER NOT NULL DEFAULT 0,
    index_started_ms  INTEGER NOT NULL DEFAULT 0,
    first_gen         INTEGER NOT NULL DEFAULT 0,
    last_gen          INTEGER NOT NULL DEFAULT 0,
    ended_token_hash  TEXT NOT NULL DEFAULT '',
    ended_reason      TEXT NOT NULL DEFAULT '',
    ended_by          TEXT NOT NULL DEFAULT '',
    ended_ms          INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE vault_prefs (
    user_id      INTEGER PRIMARY KEY,
    idle_minutes INTEGER NOT NULL DEFAULT 3,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE push_subscriptions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint      TEXT NOT NULL,
    endpoint_hash TEXT NOT NULL,
    p256dh        TEXT NOT NULL,
    auth_secret   TEXT NOT NULL,
    label         TEXT NOT NULL DEFAULT '',
    through_id    INTEGER NOT NULL DEFAULT 0,
    failures      INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_ok_at    DATETIME
);
CREATE TABLE push_vapid_keys (
    id          INTEGER NOT NULL PRIMARY KEY,
    public_key  TEXT NOT NULL,
    private_key TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE coedit_sessions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    sid             TEXT NOT NULL,
    doc_key         TEXT NOT NULL,
    storage_id      INTEGER NOT NULL DEFAULT 0,
    path            TEXT NOT NULL DEFAULT '',
    app             TEXT NOT NULL DEFAULT '',
    build           TEXT NOT NULL DEFAULT '',
    base_sig        TEXT NOT NULL DEFAULT '',
    saved_sig       TEXT NOT NULL DEFAULT '',
    key_mode        TEXT NOT NULL DEFAULT '',
    sealed_key      TEXT NOT NULL DEFAULT '',
    user_salt       TEXT NOT NULL DEFAULT '',
    rev             INTEGER NOT NULL DEFAULT 0,
    head            INTEGER NOT NULL DEFAULT 0,
    changes_head    INTEGER NOT NULL DEFAULT 0,
    saved_through   INTEGER NOT NULL DEFAULT 0,
    log_bytes       INTEGER NOT NULL DEFAULT 0,
    blob_bytes      INTEGER NOT NULL DEFAULT 0,
    has_base        INTEGER NOT NULL DEFAULT 0,
    members         INTEGER NOT NULL DEFAULT 0,
    next_client     INTEGER NOT NULL DEFAULT 0,
    next_index      INTEGER NOT NULL DEFAULT 0,
    lease_holder    TEXT NOT NULL DEFAULT '',
    lease_until_ms  INTEGER NOT NULL DEFAULT 0,
    created_ms      INTEGER NOT NULL DEFAULT 0,
    active_ms       INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
, saved_to TEXT NOT NULL DEFAULT '');
CREATE TABLE coedit_members (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    sid         TEXT NOT NULL,
    client      TEXT NOT NULL,
    token_hash  TEXT NOT NULL DEFAULT '',
    user_id     INTEGER NOT NULL DEFAULT 0,
    user_ref    TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL DEFAULT '',
    index_user  INTEGER NOT NULL DEFAULT 0,
    can_edit    INTEGER NOT NULL DEFAULT 0,
    join_seq    INTEGER NOT NULL DEFAULT 0,
    last_ctr    INTEGER NOT NULL DEFAULT 0,
    seen_ms     INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE coedit_entries (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    sid         TEXT NOT NULL,
    seq         INTEGER NOT NULL,
    kind        TEXT NOT NULL DEFAULT '',
    client      TEXT NOT NULL DEFAULT '',
    at_ms       INTEGER NOT NULL DEFAULT 0,
    ctr         INTEGER NOT NULL DEFAULT 0,
    ct          BLOB,
    member      TEXT NOT NULL DEFAULT '',
    through     INTEGER NOT NULL DEFAULT 0,
    sig         TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE coedit_blobs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    sid         TEXT NOT NULL,
    name        TEXT NOT NULL,
    size        INTEGER NOT NULL DEFAULT 0,
    sha256      TEXT NOT NULL DEFAULT '',
    created_ms  INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE coedit_cursors (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    sid         TEXT NOT NULL,
    client      TEXT NOT NULL,
    ctr         INTEGER NOT NULL DEFAULT 0,
    ct          BLOB,
    at_ms       INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE assistant_conversations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conv_key    TEXT NOT NULL UNIQUE,
    title       TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT '',
    rev         INTEGER NOT NULL DEFAULT 1,
    created_ms  INTEGER NOT NULL DEFAULT 0,
    updated_ms  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE captcha_marks (
    mark_key   TEXT    NOT NULL PRIMARY KEY,
    expires_at INTEGER NOT NULL
);
CREATE TABLE uploaders (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    storage_id   INTEGER NOT NULL,
    folder       TEXT NOT NULL DEFAULT '',
    visibility   TEXT NOT NULL DEFAULT 'public',
    link_days    INTEGER NOT NULL DEFAULT 0,
    tenant_id    INTEGER NOT NULL DEFAULT 0,
    key_hash     TEXT NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL,
    key_set_at   INTEGER NOT NULL,
    last_used_at INTEGER
, short_link INTEGER NOT NULL DEFAULT 0);
CREATE TABLE upload_deletions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash  TEXT NOT NULL UNIQUE,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    uploader_id INTEGER NOT NULL DEFAULT 0,
    storage_id  INTEGER NOT NULL,
    node_id     INTEGER NOT NULL,
    share_id    INTEGER NOT NULL DEFAULT 0,
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    name        TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE TABLE templates (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id     INTEGER NOT NULL DEFAULT 0,
    tkey          TEXT    NOT NULL,
    subject       TEXT    NOT NULL DEFAULT '',
    body          TEXT    NOT NULL DEFAULT '',
    text_body     TEXT    NOT NULL DEFAULT '',
    revision      INTEGER NOT NULL DEFAULT 1,
    updated_by    INTEGER,
    updated_at    INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT    NOT NULL DEFAULT '',
    last_error_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE app_plugin_setup (
    app         TEXT    NOT NULL PRIMARY KEY,
    record_json TEXT    NOT NULL DEFAULT '{}',
    updated_at  INTEGER NOT NULL DEFAULT 0
);
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
CREATE INDEX idx_user_node_meta_userkey ON user_node_meta(user_id, meta_key, updated_at);
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
CREATE INDEX idx_staged_uploads_state ON staged_uploads(state, updated_at);
CREATE INDEX idx_staged_uploads_user ON staged_uploads(user_id, state);
CREATE INDEX idx_staged_uploads_node ON staged_uploads(node_id);
CREATE UNIQUE INDEX users_username_uidx ON users(username);
CREATE INDEX s3_access_keys_user_idx ON s3_access_keys(user_id);
CREATE INDEX s3_access_keys_token_idx ON s3_access_keys(api_token_id);
CREATE INDEX ssh_public_keys_user_idx ON ssh_public_keys(user_id);
CREATE INDEX nfs_exports_user_idx ON nfs_exports(user_id);
CREATE INDEX nfs_exports_token_idx ON nfs_exports(api_token_id);
CREATE UNIQUE INDEX idx_ops_queue_dedup_pending
    ON ops_queue (dedup_key)
    WHERE dedup_key IS NOT NULL AND status = 'pending';
CREATE UNIQUE INDEX idx_nodes_storage_pathhash
    ON nodes(storage_id, path_hash) WHERE deleted_at IS NULL;
CREATE INDEX idx_nodes_storage_pathhash_all
    ON nodes(storage_id, path_hash);
CREATE INDEX idx_pending_ops_status ON pending_ops(status, created_at);
CREATE UNIQUE INDEX idx_storages_uid ON storages(uid);
CREATE INDEX idx_nodes_last_actor ON nodes(last_actor_id) WHERE last_actor_id IS NOT NULL;
CREATE INDEX idx_app_plugin_jobs_op ON app_plugin_jobs(op_id);
CREATE INDEX idx_app_plugin_jobs_plugin ON app_plugin_jobs(plugin_id, created_at);
CREATE INDEX idx_app_plugin_signing_keys_ca ON app_plugin_signing_keys(tenant_id, purpose, retired_at);
CREATE INDEX idx_shares_plugin ON shares(plugin_id, created_at) WHERE plugin_id > 0;
CREATE INDEX idx_app_plugin_schedule_due ON app_plugin_schedule(status, due_at);
CREATE UNIQUE INDEX uq_tags_owner_key ON tags(owner_id, name_key);
CREATE UNIQUE INDEX uq_tags_tenant_key ON tags(tenant_id, name_key);
CREATE INDEX idx_node_tags_node ON node_tags(node_id);
CREATE INDEX idx_notification_reads_user ON notification_reads (user_id);
CREATE INDEX idx_notifications_unread_since ON notifications (user_id, read_at, id);
CREATE INDEX idx_nodes_parent_id ON nodes(parent_id);
CREATE INDEX idx_catalogue_folders_state ON catalogue_folders (storage_id, state, depth);
CREATE INDEX idx_catalogue_folders_path ON catalogue_folders (storage_id, path);
CREATE INDEX idx_nodes_deleted_by ON nodes(deleted_by) WHERE deleted_by IS NOT NULL;
CREATE INDEX idx_pending_ops_actor ON pending_ops(actor_id, id);
CREATE UNIQUE INDEX idx_drafts_key ON drafts (draft_key);
CREATE UNIQUE INDEX idx_drafts_node ON drafts (node_id);
CREATE INDEX idx_drafts_user ON drafts (user_id);
CREATE INDEX idx_app_plugin_versions_plugin ON app_plugin_versions (plugin_id, id);
CREATE INDEX idx_permission_rules_provider ON permission_rules (provider_id);
CREATE INDEX idx_user_custom_roles_role ON user_custom_roles (role_id);
CREATE INDEX idx_user_sso_groups_group ON user_sso_groups (group_name);
CREATE UNIQUE INDEX idx_plugin_requests_key ON plugin_requests (request_key);
CREATE INDEX idx_plugin_requests_status ON plugin_requests (status);
CREATE INDEX idx_plugin_requests_source ON plugin_requests (source_key, status);
CREATE UNIQUE INDEX idx_login_throttle_subject ON login_throttle (scope, subject);
CREATE INDEX idx_login_throttle_locked ON login_throttle (locked_until);
CREATE UNIQUE INDEX idx_providers_realm ON providers (realm);
CREATE INDEX idx_user_groups_provider ON user_groups (provider_id);
CREATE INDEX idx_user_groups_role ON user_groups (role_id);
CREATE UNIQUE INDEX idx_user_groups_name ON user_groups (tenant_key, name);
CREATE INDEX idx_user_group_members_user ON user_group_members (user_id);
CREATE UNIQUE INDEX idx_group_file_grants_uniq ON group_file_grants (storage_id, path_prefix, group_id);
CREATE INDEX idx_group_file_grants_group ON group_file_grants (group_id);
CREATE UNIQUE INDEX idx_auth_instances_slug ON auth_instances (slug);
CREATE INDEX idx_auth_instances_owner ON auth_instances (owner_provider_id);
CREATE INDEX idx_provider_auth_instances_instance ON provider_auth_instances (instance_id);
CREATE UNIQUE INDEX idx_provider_domains_domain ON provider_domains (domain);
CREATE INDEX idx_provider_domains_provider ON provider_domains (provider_id);
CREATE UNIQUE INDEX idx_users_oidc_identity ON users (provider_id, oidc_issuer, oidc_subject);
CREATE UNIQUE INDEX idx_e2e_requests_key ON e2e_requests (request_key);
CREATE INDEX idx_e2e_requests_status ON e2e_requests (status);
CREATE INDEX idx_e2e_requests_provider ON e2e_requests (provider_id, status);
CREATE INDEX idx_e2e_requests_target ON e2e_requests (user_id, storage_id, path, status);
CREATE INDEX idx_nodes_storage_path ON nodes(storage_id, path, deleted_at) WHERE path IS NOT NULL;
CREATE INDEX idx_user_groups_directory ON user_groups (directory_id);
CREATE INDEX idx_users_directory_id ON users (directory_id);
CREATE INDEX idx_notify_digest_state_due ON notify_digest_state (due_at);
CREATE INDEX idx_recent_searches_user ON recent_searches (user_id, surface);
CREATE UNIQUE INDEX idx_office_sessions_key ON office_sessions (doc_key);
CREATE INDEX idx_office_sessions_expires ON office_sessions (expires_unix);
CREATE INDEX idx_replica_failures_unresolved
    ON replica_failures (resolved_at, last_attempt_at DESC);
CREATE UNIQUE INDEX idx_replica_links_target_folder ON replica_links (target_id, folder_key);
CREATE UNIQUE INDEX idx_vault_locks_key ON vault_locks (tenant_id, vault_id);
CREATE UNIQUE INDEX idx_push_subscriptions_endpoint ON push_subscriptions (endpoint_hash);
CREATE INDEX idx_push_subscriptions_user ON push_subscriptions (user_id);
CREATE UNIQUE INDEX idx_coedit_sessions_sid ON coedit_sessions (sid);
CREATE UNIQUE INDEX idx_coedit_sessions_doc ON coedit_sessions (doc_key);
CREATE INDEX idx_coedit_sessions_active ON coedit_sessions (members, active_ms);
CREATE UNIQUE INDEX idx_coedit_members_client ON coedit_members (sid, client);
CREATE INDEX idx_coedit_members_seen ON coedit_members (seen_ms);
CREATE UNIQUE INDEX idx_coedit_entries_seq ON coedit_entries (sid, seq);
CREATE UNIQUE INDEX idx_coedit_blobs_name ON coedit_blobs (sid, name);
CREATE UNIQUE INDEX idx_coedit_cursors_client ON coedit_cursors (sid, client);
CREATE INDEX idx_coedit_cursors_at ON coedit_cursors (sid, at_ms);
CREATE INDEX idx_assistant_conversations_user ON assistant_conversations (user_id, updated_ms);
CREATE INDEX idx_assistant_conversations_updated ON assistant_conversations (updated_ms);
CREATE INDEX idx_captcha_marks_expires ON captcha_marks (expires_at);
CREATE INDEX uploaders_user_idx ON uploaders(user_id);
CREATE INDEX upload_deletions_user_idx ON upload_deletions(user_id);
CREATE UNIQUE INDEX ux_templates_scope_key ON templates (tenant_id, tkey);

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
INSERT INTO goose_db_version (version_id, is_applied) VALUES (23, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (24, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (25, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (26, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (27, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (28, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (29, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (30, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (31, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (32, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (33, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (34, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (35, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (36, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (37, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (38, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (39, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (40, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (41, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (42, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (43, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (44, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (45, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (46, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (47, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (48, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (49, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (50, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (51, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (52, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (53, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (54, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (55, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (56, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (57, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (58, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (59, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (60, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (61, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (62, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (63, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (64, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (65, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (66, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (67, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (68, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (69, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (70, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (71, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (72, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (73, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (74, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (75, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (76, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (77, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (78, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (79, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (80, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (81, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (82, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (83, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (84, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (85, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (86, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (87, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (90, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (91, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (92, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (94, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (95, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (96, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (97, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (98, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (105, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (107, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (109, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (126, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (133, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (134, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (135, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (136, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (137, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (138, 1);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (139, 1);
