// Shared API DTOs. Mirrors the planned Go structs; treat as authoritative for
// the admin UI. Backend Go handlers should marshal these names exactly.

// Account roles (mirror backend model.Role*):
//   admin  — full admin panel + all files (exempt from RBAC/ACL)
//   user   — explorer only; read+write within granted paths (owner-capable)
//   viewer — explorer only; read-only (view+download; no edit/convert/mutate)
export type UserRole = 'admin' | 'user' | 'viewer';

export interface User {
  id: number;
  email: string;
  display_name: string;
  role: UserRole;
  locale?: string;
  timezone?: string;
  oidc_subject?: string | null;
  totp_enabled?: boolean;
  created_at: string;
  updated_at: string;
  last_login_at?: string | null;
}

export interface MeResponse {
  user: User;
  permissions: string[];
}

export interface LoginRequest {
  email: string;
  password: string;
  remember?: boolean;
  totp?: string;
}

export interface LoginResponse {
  user: User;
  token?: string; // optional bearer if cookie auth disabled
}

export type StorageDriver = 'local' | 's3' | 'sftp' | 'webdav';

export interface StorageRef {
  id: number;
  name: string;
  driver: StorageDriver;
  enabled: boolean;
  config: Record<string, unknown>;
  read_only: boolean;
  /** Per-storage RBAC toggle. When true, non-admins see only paths granted
   *  to them (via the permissions panel); when false the storage is open to
   *  every authenticated user (capability by account role). Default false. */
  rbac_enabled?: boolean;
  created_at: string;
  updated_at: string;
  sync_mode?: 'poll' | 'fsnotify' | 'push' | 'ondemand';
  // Cached stats (filled by backend, may be null right after creation)
  file_count?: number;
  total_bytes?: number;
  /** Live aggregate from the backend storages list endpoint (v0.1.10+).
   *  Backend computes COUNT(*) and SUM(size) per storage on every list
   *  call so the admin grid shows real "12 files, 4.2 MB" labels
   *  instead of the static `0` placeholder the SPA started with. */
  stats?: {
    file_count: number;
    total_size_bytes: number;
  };
  last_sync_at?: string | null;
  last_sync_state?: 'ok' | 'error' | 'running' | 'pending';
  last_sync_error?: string | null;
  /** Replica fields. v0.1.18+: the canonical link is
   *  `replica_target_id` — a foreign key into the new
   *  `replication_targets` table. `role` / `replica_of_id` /
   *  `replica_mode` are LEGACY columns retained for backward
   *  compatibility; do not write them from new code. */
  role?: 'primary' | 'replica';
  replica_of_id?: number | null;
  replica_mode?: 'async' | 'sync';
  replica_target_id?: number | null;
}

/** Ek storage_name field — backend ShareWithMeta envelope'unun
 *  storage adını UI'a getirir (Paylaşımlar tablosunda Depo kolonu).
 *  v0.1.19+ */
/** Replication target — backup-only sink (NOT a regular storage).
 *  Lives in its own table; managed from the Replikasyon page. */
export interface ReplicationTarget {
  id: number;
  name: string;
  driver: StorageDriver;
  config: Record<string, unknown>;
  mode: 'async' | 'sync';
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface ReplicationTargetInput {
  name: string;
  driver: StorageDriver;
  config: Record<string, unknown>;
  mode?: 'async' | 'sync';
  enabled?: boolean;
}

export interface StorageCreateRequest {
  name: string;
  driver: StorageDriver;
  config: Record<string, unknown>;
  read_only?: boolean;
  rbac_enabled?: boolean;
}

export interface StorageUpdateRequest {
  name?: string;
  config?: Record<string, unknown>;
  enabled?: boolean;
  read_only?: boolean;
  rbac_enabled?: boolean;
  role?: 'primary' | 'replica';
  replica_of_id?: number | null;
  replica_mode?: 'async' | 'sync';
  replica_target_id?: number | null;
}

export interface SyncRun {
  id: number;
  storage_id: number;
  storage_name: string;
  started_at: string;
  finished_at?: string | null;
  state: 'ok' | 'error' | 'running' | 'aborted';
  added: number;
  updated: number;
  deleted: number;
  scanned: number;
  error?: string | null;
}

export interface DriftReport {
  storage_id: number;
  generated_at: string;
  missing_in_db: number;
  missing_in_storage: number;
  size_mismatch: number;
  hash_mismatch: number;
  details_url?: string;
}

export interface DemoMode {
  enabled: boolean;
  user: string;
}

export interface Capabilities {
  version: string;
  build: string;
  ffmpeg: boolean;
  imagemagick: boolean;
  ghostscript: boolean;
  libreoffice: boolean;
  onlyoffice_url?: string | null;
  drawio_url?: string | null;
  mermaid_url?: string | null;
  monaco: boolean;
  storage_drivers: string[];
  auth_drivers: string[];
  db_driver: string;
  search_enabled: boolean;
  demo_mode?: boolean;
  demo_user?: string;
}

export interface SettingsMap {
  // free-form, but a few well-known keys
  site_name?: string;
  public_url?: string;
  sync_interval_seconds?: number;
  log_level?: 'debug' | 'info' | 'warn' | 'error';
  default_locale?: 'en' | 'tr';
  default_timezone?: string;
  [k: string]: unknown;
}

export interface ExternalService {
  id: 'onlyoffice' | 'drawio' | 'mermaid';
  url: string | null;
  jwt_secret_set: boolean;
  enabled: boolean;
  last_checked_at: string | null;
  last_state: 'healthy' | 'configured-unreachable' | 'disabled' | 'unconfigured';
  last_error: string | null;
}

export interface AuthProvider {
  id: 'local' | 'oidc' | 'ldap' | 'proxy-header';
  enabled: boolean;
  config: Record<string, unknown>;
  config_redacted?: Record<string, unknown>;
  status: 'ok' | 'misconfigured' | 'disabled';
  last_error?: string | null;
}

export interface AuditEntry {
  id: number;
  at: string;
  user_id: number | null;
  user_email: string | null;
  action: string; // e.g. "user.create", "storage.delete", "share.access"
  target_type: string | null;
  target_id: string | null;
  ip: string | null;
  user_agent: string | null;
  details: Record<string, unknown> | null;
}

export interface Share {
  id: number;
  token: string;
  node_id?: number;
  storage_id?: number;
  storage_name?: string;
  path?: string;
  /** Legacy boolean — older callers still set this. */
  pin_set?: boolean;
  /** Current backend field. */
  has_pin?: boolean;
  expires_at?: string | null;
  max_downloads?: number | null;
  download_count?: number;
  created_by?: string | number;
  created_at?: string;
  /** Legacy boolean — older callers. */
  revoked?: boolean;
  /** Current backend timestamp; truthy = revoked. */
  revoked_at?: string | null;
}

export interface DashboardStats {
  storage_count: number;
  user_count: number;
  total_files: number;
  total_bytes: number;
  active_sync_count: number;
  queue_depth: number;
  last_sync_at: string | null;
  recent_audit: AuditEntry[];
  recent_syncs: SyncRun[];
}

export interface SearchHit {
  id: string;
  storage_id: number;
  storage_name: string;
  path: string;
  filename: string;
  size: number;
  mime: string;
  modified_at: string;
  score: number;
  highlights?: Record<string, string[]>;
}

export interface PaginatedResponse<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

// ─── Queue ────────────────────────────────────────────────────

export type QueueOpStatus = 'pending' | 'running' | 'done' | 'failed' | 'cancelled';

export interface QueueOp {
  id: string;
  type: string;
  payload: Record<string, unknown>;
  status: QueueOpStatus;
  priority: number;
  attempts: number;
  max_attempts: number;
  last_error?: string;
  enqueued_at: string;
  started_at?: string | null;
  finished_at?: string | null;
  not_before?: string | null;
}

export interface QueueStats {
  pending: number;
  running: number;
  failed: number;
  done_24h: number;
  cancelled: number;
}

export interface QueueListResponse {
  items: QueueOp[];
  total: number;
  limit: number;
  offset: number;
}

// ─── Notifications ────────────────────────────────────────────

export type Severity = 'info' | 'warning' | 'error' | 'critical';

export type WebhookStatus = 'pending' | 'sent' | 'failed' | 'skipped';

export interface NotificationItem {
  id: number;
  event: string;
  severity: Severity;
  title: string;
  body: string;
  meta: Record<string, unknown>;
  user_id?: number | null;
  read_at?: string | null;
  webhook_status: WebhookStatus;
  webhook_error?: string;
  created_at: string;
}

export interface NotificationListResponse {
  items: NotificationItem[];
  total: number;
  limit: number;
  offset: number;
}

export interface NotificationSettings {
  user_id: number;
  in_app_enabled: boolean;
  muted_events: string[]; // raw JSON array name; backend exposes muted_events (json.RawMessage)
}

export interface WebhookConfig {
  url: string;
  token_set: boolean;
}

// ─── Replica ─────────────────────────────────────────────────

export type ReplicaMode = 'mirror' | 'append_only' | 'skip';

export interface ReplicaRule {
  id: number;
  path_pattern: string;
  mode: ReplicaMode;
  priority: number;
  enabled: boolean;
  description: string;
  created_at: string;
  updated_at: string;
}

export interface ReplicaRuleInput {
  path_pattern: string;
  mode: ReplicaMode;
  priority: number;
  enabled: boolean;
  description: string;
}

export interface ReplicaFailure {
  id: number;
  path: string;
  op: string;
  error_code: string;
  error_msg: string;
  attempts: number;
  last_attempt_at: string;
  resolved_at?: string | null;
}

export interface ReplicaFailureListResponse {
  items: ReplicaFailure[];
  total: number;
  limit: number;
  offset: number;
}

export interface ReplicaStatusReport {
  generated_at: string;
  total_files: number;
  failed_count: number;
  repaired_count: number;
  summary: Record<string, unknown>;
}

export interface ReplicaSettings {
  report_cron: string;
  report_enabled: boolean;
  default_mode: ReplicaMode;
}
