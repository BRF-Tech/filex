// Shared API DTOs. Mirrors the planned Go structs; treat as authoritative for
// the admin UI. Backend Go handlers should marshal these names exactly.

export type UserRole = 'admin' | 'editor' | 'viewer';

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
  created_at: string;
  updated_at: string;
  // Cached stats (filled by backend, may be null right after creation)
  file_count?: number;
  total_bytes?: number;
  last_sync_at?: string | null;
  last_sync_state?: 'ok' | 'error' | 'running' | 'pending';
  last_sync_error?: string | null;
}

export interface StorageCreateRequest {
  name: string;
  driver: StorageDriver;
  config: Record<string, unknown>;
  read_only?: boolean;
}

export interface StorageUpdateRequest {
  name?: string;
  config?: Record<string, unknown>;
  enabled?: boolean;
  read_only?: boolean;
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
  storage_id: number;
  storage_name: string;
  path: string;
  pin_set: boolean;
  expires_at: string | null;
  max_downloads: number | null;
  download_count: number;
  created_by: string;
  created_at: string;
  revoked: boolean;
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
