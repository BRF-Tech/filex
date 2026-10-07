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
  /** Short login name. Sign-in accepts this OR the e-mail, on every surface —
   *  the browser, the desktop app, WebDAV, and the connection protocols, whose
   *  clients cannot carry an `@` in a login without escaping it. */
  username?: string;
  display_name: string;
  role: UserRole;
  locale?: string;
  timezone?: string;
  /** Profile picture — a small data:image/… URI or a URL. Also the face the
   *  explorer's collaboration strip draws for this account, on every client of
   *  it (browser session, desktop app, any API key minted under it). */
  avatar_url?: string;
  totp_enabled?: boolean;
  created_at: string;
  updated_at: string;
  last_login_at?: string | null;
  /** Switched on: the account may sign in (absent from older servers = on). */
  enabled?: boolean;
  /** Why the SERVER switched it off: `pending_approval` for an account an SSO
   *  sign-in opened whose identity provider did not confirm the address
   *  (docs/SSO.md); `directory` for one LDAP directory sync switched off,
   *  which sync switches back on when the directory does (docs/LDAP.md). An
   *  administrator switching it on or off clears it. */
  disabled_reason?: string;
  /** Bound to the SSO identity it first signed in with (docs/SSO.md). */
  sso_linked?: boolean;
  /** Where the account comes from (migration 00084): made here, or by an SSO,
   *  LDAP or reverse-proxy sign-in. A label only. */
  auth_source?: 'local' | 'sso' | 'ldap' | 'proxy' | string;
  /** The LDAP provider that made it ("ldap", "ldap-2"…), if one did. */
  auth_directory?: string;
  /** An administrator because a group gives its members Administrator (00086). */
  admin_by_group?: boolean;
}

export interface MeResponse {
  user: User;
  /** The account's effective permissions, account-wide (backend internal/perm). */
  permissions?: string[];
  /** What the role allows only in some folders; the file browser offers
   *  these and the server decides per file. */
  permissions_in_folders?: string[];
  /** Every permission whose answer differs from folder to folder; the file
   *  browser asks the server about the selected files before offering these. */
  permissions_by_folder?: string[];
  /** Limits the permission rules set for this account. */
  permission_settings?: import('./roles').PermRuleSettings;
  /** A rule requires 2FA and the account has not enrolled: every API call
   *  but enrolment answers 403 "2fa_required" until it does. */
  two_factor_required?: boolean;
}

export interface LoginRequest {
  email: string;
  password: string;
  remember?: boolean;
  totp?: string;
  /** The tenant realm (multi-tenant installs only; empty = the platform's own
   *  tenant, or the one the address names). */
  realm?: string;
}

/** A sign-in typed on the platform's address for a tenant that has an address
 *  of its own ends there: the browser carries `code` (one use, a minute) to
 *  that address's sign-in page, which redeems it (`AuthApi.handoff`). */
export interface LoginHandoff {
  origin: string;
  code: string;
}

export interface LoginResponse {
  /** Absent when the answer is a handoff. */
  user?: User;
  token?: string; // optional bearer if cookie auth disabled
  handoff?: LoginHandoff;
}

/** Driver names the backend registers. Not a closed set in practice —
 *  every surface picks drivers up from GET /admin/storage-drivers, so a
 *  driver added on the backend appears without a frontend release. The
 *  union lists the built-ins for autocomplete; `(string & {})` keeps a
 *  new one assignable. */
export type StorageDriver = 'local' | 's3' | 'sftp' | 'ftp' | 'webdav' | (string & {});

/**
 * The driver-descriptor wire types come from @brftech/filex-core, they are not
 * re-declared here.
 *
 * They describe `backend/internal/storage/descriptor.go`, which both packages
 * render — the admin form and the explorer's connections panel. Declared once
 * per package they were free to disagree with each other and with the Go
 * struct, and they did: neither copy had `range`, which the server has always
 * sent in `Capabilities`. web already depends on the package that owns them.
 */
export type {
  StorageDriverCapabilities,
  StorageField,
  StorageFieldOption,
  StorageFieldType,
} from '@brftech/filex-core';

import type { StorageDriverCapabilities, StorageField } from '@brftech/filex-core';

export interface StorageDriverDescriptor {
  /** Narrower than core's `string`: the built-ins get autocomplete here. */
  driver: StorageDriver;
  label: string;
  i18n_key: string;
  fields: StorageField[];
  capabilities: StorageDriverCapabilities;
  /**
   * Settings a STORAGE on this driver has for the scan that catalogues it
   * (issue #44 — `scan_exclude`), kept in the same `config` map. Drawn where a
   * storage is created or edited, never in the replication-target dialog: a
   * target is not scanned. Absent from a server older than v0.43.0.
   */
  scan_fields?: StorageField[];
  /**
   * The settings of sync_mode `lazy` (docs/LAZY-CATALOGUE.md), present only
   * for a driver a storage may be cataloged lazily on (local). The form offers
   * the mode only where they are. Kept in the same `config` map.
   */
  lazy_fields?: StorageField[];
}

/**
 * How much of a storage its catalog covers (sync.CatalogueCoverage). The
 * explorer's own copy of the shape — one type for both packages.
 */
export type { CatalogCoverage } from '@brftech/filex-core';
import type { CatalogCoverage } from '@brftech/filex-core';

export interface StorageRef {
  id: number;
  name: string;
  /** The address that does not move. `name` is the first path segment on
   *  WebDAV, SFTP, NFS and the S3 API, so renaming a storage re-addresses it
   *  and every existing mount 404s; the protocols accept this in its place and
   *  it is assigned once, at creation. Absent on a row written before the
   *  column existed that the migration did not reach. */
  uid?: string;
  /** #57 — the administrator's position (1 = first; `PUT
   *  /admin/storages/order`). Null = not placed: listed after the placed
   *  ones, in creation order. Everybody's navigation panel follows it unless
   *  they have arranged their own. */
  sort_order?: number | null;
  driver: StorageDriver;
  enabled: boolean;
  config: Record<string, unknown>;
  read_only: boolean;
  /** Poll cadence in seconds (`poll` mode). 0 = the server default (15 min,
   *  or FILEX_SYNC_INTERVAL); anything under 5 s is treated as unset. */
  sync_interval_s?: number;
  /** Per-storage RBAC toggle. When true, non-admins see only paths granted
   *  to them (via the permissions panel); when false the storage is open to
   *  every authenticated user (capability by account role). Default false. */
  rbac_enabled?: boolean;
  created_at: string;
  updated_at: string;
  /** `push` is LEGACY: the server refuses it on write (nothing implements a
   *  push receiver) but still returns it for rows written before that check.
   *  It stays in the union so such a row types cleanly and renders a label. */
  sync_mode?: 'poll' | 'fsnotify' | 'ondemand' | 'lazy' | 'push';
  /** Set while `stats` count only part of the storage: its catalog does not
   *  cover all of it yet (a first sync, a lazily cataloged storage). */
  coverage?: CatalogCoverage | null;
  /** A lazily cataloged storage's engine: folders cataloged / waiting /
   *  watched, the watch budget, the background pass. Storage page only. */
  catalogue?: CatalogCoverage | null;
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
  /** Raw `sync_runs.status` of the last run: the backend writes 'ok',
   *  'running', 'failed' or 'aborted' (a run the server stopped in the middle
   *  of, closed when it next started). ('error' is the sync-runs list's
   *  translated spelling — accepted here too so both round-trip.) */
  last_sync_state?: 'ok' | 'failed' | 'error' | 'running' | 'aborted' | 'pending';
  last_sync_error?: string | null;
  /** A scan is walking the storage right now (the worker's own word, not the
   *  runs table): what a page following a "Sync now" waits on. */
  running?: boolean;
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

/** Extra storage_name field — brings the storage name out of the backend
 *  ShareWithMeta envelope and into the UI (the Storage column of the Shares
 *  table). v0.1.19+ */
/** Replication target — backup-only sink (NOT a regular storage).
 *  Lives in its own table; managed from the Replication page. */
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
  /** The saved storage a form edits: "Test connection" fills the credentials
   *  it sends back masked ("***") from it. */
  id?: number;
  read_only?: boolean;
  rbac_enabled?: boolean;
  sync_interval_s?: number;
  sync_mode?: SyncMode;
}

/** A sync mode an operator may choose (model.SyncModes). */
export type SyncMode = 'poll' | 'fsnotify' | 'ondemand' | 'lazy';

/** One folder under a probed root, with the root a storage on it would carry. */
export interface DiscoveredFolder {
  name: string;
  root: string;
}

export interface StorageDiscoverResponse {
  ok: boolean;
  error?: string;
  /** Which config key the root lives in for this driver (`prefix`, `path`, `root`…). */
  root_key?: string;
  folders?: DiscoveredFolder[];
}

export interface StorageUpdateRequest {
  name?: string;
  config?: Record<string, unknown>;
  /** A legacy `push` row round-trips as it is (the Replica page sends the
   *  whole row back); the storage form only ever sends a SyncMode. */
  sync_mode?: SyncMode | 'push';
  enabled?: boolean;
  read_only?: boolean;
  rbac_enabled?: boolean;
  sync_interval_s?: number;
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
  /** That ImageMagick decodes a HEIC photo: measured by the server with a
   *  sample (0.50), since libheif can be there without its HEVC decoder.
   *  Absent on an older server. */
  heic?: boolean;
  ghostscript: boolean;
  /** What each kind of thumbnail has (the server's nested snapshot). `office`
   *  is OnlyOffice being configured (0.50): office documents get their
   *  thumbnails from it and from nothing else. Absent on an older server. */
  thumbs?: { office?: boolean };
  onlyoffice_url?: string | null;
  drawio_url?: string | null;
  monaco: boolean;
  storage_drivers: string[];
  auth_drivers: string[];
  db_driver: string;
  search_enabled: boolean;
  /** SSO-first installs: login page starts the OIDC flow immediately;
   *  the password form stays reachable via ?local=1. */
  oidc_auto_redirect?: boolean;
  /** Password sign-in is off, but the bootstrap administrator may still use it (recovery). */
  auth_recovery_login?: boolean;
  /** Multi-tenant mode IN FORCE on this server (0.53, Admin → Multi-tenant
   *  mode). The one answer every screen reads (composables/useTenancy):
   *  false, and nothing about tenants or realms is drawn. Absent on an older
   *  server, where `realm` alone said it. */
  multi_tenant?: boolean;
  /** The sign-in form's Realm field. Present on a multi-tenant install only
   *  (absent: no field). On a tenant's own address `locked_realm` is that
   *  tenant's realm and the field is filled in and read-only; on the
   *  platform's address it is null and the field is free (empty = the
   *  platform's own tenant). */
  realm?: { enabled: boolean; locked_realm: string | null };
  /** The sign-in page's SSO buttons for this address (the tenant it names,
   *  else the platform's own): `id` goes back as `?instance=`. Absent on an
   *  older server, where `auth_drivers` holding `oidc` is one button. */
  auth_sso?: LoginSSO[];
  demo_mode?: boolean;
  demo_user?: string;
  /** Demo password (FILEX_DEMO_PASS). Sent by the server only when
   *  demo_mode is on — the demo landing publishes these credentials. */
  demo_pass?: string;
  default_locale?: string | null;
  /** The changes this server runs as jobs of its operations queue when asked
   *  with `queued=1` (`rename`, `restore`, `purge`). Absent on an older server,
   *  which makes them inside the request. */
  queued?: string[];
  /** The origin the server builds absolute links on — present only when the
   *  operator chose one (FILEX_PUBLIC_URL) or the request came in on a
   *  tenant's own host. */
  public_url?: string;
  /** False when FILEX_PUBLIC_URL was never set: every share link and every
   *  mailed link then carries the built-in guess (http://localhost:5212). The
   *  admin layout puts up a sign for exactly this (#32). */
  public_url_configured?: boolean;
  /** Whether the host can run app plugins at all (WASM runtime present and
   *  not switched off). The same block the explorer reads off
   *  `/api/files/capabilities`; absent on a server too old to say, which
   *  reads as off — and off means the panel makes no plugin call at all. */
  app_plugins?: { enabled: boolean };
  /** Async upload scanning is configured (ClamAV). Configured, not probed. */
  antivirus?: boolean;
  /** Whether this installation holds an escrow key for encrypted folders. */
  e2e_escrow?: { enabled: boolean };
  /** Who may start encrypting here, the caller's own tenant's row:
   *  `available` is the platform operator's switch, `policy` the tenant's
   *  choice (`off` | `admins` | `permitted` | `approval`). Absent for an
   *  anonymous caller and on a server from before the policy. The settings
   *  dialog and the Webhooks screen read it to say when the two encryption
   *  request events can happen (core lib/webhookEvents). */
  e2e_policy?: { available: boolean; policy: string };
  /** The caller could configure the instance (the server's answer — the
   *  same checks the admin routes apply, supertenant included). */
  caller_admin?: boolean;
}

/** One SSO button of the sign-in page (docs/TENANT-ADMIN.md). */
export interface LoginSSO {
  /** The instance to start: a provider's slug, or "tenant" for a tenant's own OIDC. */
  id: string;
  /** What the button says; "" = the page's own words. */
  label: string;
}

/** How a sign-in for a realm may go (GET /api/auth/methods). */
export interface LoginMethods {
  password: boolean;
  recovery: boolean;
  sso: LoginSSO[];
}

export interface SettingsMap {
  // free-form, but a few well-known keys
  site_name?: string;
  // ⚠ `public_url`, `sync_interval_seconds`, `log_level`, `default_locale`
  // and `default_timezone` were declared here and offered on the Settings
  // page. Nothing on the server ever read those rows — the live values come
  // from FILEX_PUBLIC_URL / FILEX_SYNC_INTERVAL / FILEX_LOG_LEVEL /
  // FILEX_DEFAULT_LOCALE (and there is no timezone knob at all). Do not
  // re-add a key here without a reader.
  [k: string]: unknown;
}

/**
 * One thing the server can say about a configuration that no probe from the
 * filex process can settle — a Document Server URL a browser cannot resolve, a
 * FILEX_PUBLIC_URL the document server cannot post back to. `severity:
 * 'warning'` withholds the "configuration complete" badge; `'note'` never
 * does. Rendered by `code` (translated EN + TR); `message` is the English
 * fallback for anything without a translation.
 */
export interface ExternalAdvisory {
  code: string;
  field: 'url' | 'public_url' | string;
  severity: 'warning' | 'note';
  detail?: string;
  message: string;
}

export interface ExternalService {
  id: 'onlyoffice' | 'drawio';
  url: string | null;
  jwt_secret_set: boolean;
  enabled: boolean;
  last_checked_at: string | null;
  last_state: 'healthy' | 'configured-unreachable' | 'disabled' | 'unconfigured';
  last_error: string | null;
  // True when env/YAML pins this service. Its row is re-asserted from the
  // environment at every boot, so an edit made here applies live but does not
  // survive a restart — the card says so rather than letting the operator find
  // out later.
  env_managed?: boolean;
  /**
   * ⚠ Present on the LIST response, not only after a Test. The whole defect
   * behind issue #17's second round was a badge that looked settled without
   * anyone pressing anything, so a browser-unreachable address has to be
   * visible the moment the page paints.
   */
  advisories?: ExternalAdvisory[];
  /**
   * The address the SERVICE uses to reach filex. Empty means "filex's public
   * URL", which is right wherever one address serves both the browser and the
   * container. Only OnlyOffice calls back, so only it shows the field.
   */
  callback_url?: string;
}

export interface AuthProvider {
  /**
   * The provider's slug (docs/TENANT-ADMIN.md): the driver's name for its
   * first instance (`ldap`, `oidc`…), so a driver's first provider is still
   * addressed by it; any other instance's own name (`ldap-2`, `acme-sso`).
   */
  id: string;
  /** What it is: oidc, ldap, pam, windows, proxy-header, local, api-token. */
  driver: 'local' | 'oidc' | 'ldap' | 'proxy-header' | 'windows' | 'pam' | 'api-token' | string;
  /** Its row (0 for `local` and API tokens, which are not instances). */
  instance_id?: number;
  /** What the sign-in page calls it ("" = the page's own words). */
  label?: string;
  /** The tenant a tenant's own provider belongs to. */
  owner_provider_id?: number | null;
  /** The tenants that sign in through it (multi-tenant installs only). */
  tenants?: number[];
  enabled: boolean;
  config: Record<string, unknown>;
  config_redacted?: Record<string, unknown>;
  status: 'ok' | 'misconfigured' | 'disabled';
  last_error?: string | null;
  /** "Test now" can check this provider for real (the driver is an auth.Prober). */
  testable?: boolean;
  /** The page may change it (not defined by the environment). */
  managed?: boolean;
  /** Where it is configured: the environment, this page, a tenant's administrator, or built in. */
  origin?: 'environment' | 'page' | 'tenant' | 'builtin';
  /** Where an environment provider is defined (FILEX_AUTH_DRIVERS, a config file…). */
  from?: string;
  state?: 'running' | 'failed' | 'off';
  /** Saved on this page before v0.43.0, never applied; imported switched off. */
  legacy?: boolean;
  /** The page holds a configuration under this name, but the environment's is used. */
  shadowed?: boolean;
  /** Secret fields holding a value — never the value itself. */
  secrets_set?: Record<string, boolean>;
  /** The fields the page may set (server-side schema). */
  fields?: AuthProviderField[];
  /**
   * The test signs a real account in (an operating-system provider): the test
   * and a save that switches it on carry `test_account`, and a failing test
   * can never be confirmed away.
   */
  test_account_required?: boolean;
  /** Fields whose value the upgrade to 0.50 chose and nobody saved since
   *  (`trust_email` of an OIDC that existed before it): the form says so. */
  set_by_upgrade?: string[];
}

/**
 * The account an operating-system provider's test signs in with. ⚠ Sent on
 * the one request that tests or saves, never kept: the page clears the
 * password as soon as that request is answered.
 */
export interface AuthProviderTestAccount {
  username: string;
  password: string;
}

/** One field of a provider the page manages (authsetup.Field). */
export interface AuthProviderField {
  key: string;
  /** `multiline` keeps its line breaks (a pasted PEM certificate). */
  kind: 'text' | 'secret' | 'bool' | 'multiline';
  required?: boolean;
  default?: string;
}

/** A tenant as the provider bindings list it. */
export interface AuthProviderTenant {
  id: number;
  name: string;
  /** "" for the platform's own tenant. */
  realm: string;
  is_supertenant: boolean;
}

/** `GET /api/admin/auth-providers`, read whole. */
export interface AuthProvidersOverview {
  providers: AuthProvider[];
  /** FILEX_MULTI_TENANT: providers are bound to tenants. */
  multiTenant: boolean;
  /** The upgrade bound every provider to every tenant; the operator has not reviewed it yet. */
  reviewPending: boolean;
  /** Every tenant, for the bindings (multi-tenant installs only). */
  tenants: AuthProviderTenant[];
  /** The password form answers (password sign-in or the recovery sign-in). */
  passwordSignIn: boolean;
  /** The installation administrator's recovery sign-in is on. */
  recoveryLogin: boolean;
  /** FILEX_SECRET_KEY is set, so secrets can be stored (sealed). */
  secretKey: boolean;
}

/** One step of a provider test (auth.ProbeCheck). */
export interface AuthProviderCheck {
  id: string;
  status: 'ok' | 'fail' | 'unchecked';
  params?: Record<string, string>;
}

/** `POST /api/admin/auth-providers/{name}/test`. */
export interface AuthProviderTestResult {
  testable: boolean;
  ok: boolean;
  checks: AuthProviderCheck[];
}

export interface AuditEntry {
  id: number;
  at: string;
  user_id: number | null;
  user_email: string | null;
  /** The person as every screen names them (server model.PersonLabel). */
  user_name?: string | null;
  action: string; // e.g. "user.create", "storage.delete", "share.access"
  target_type: string | null;
  target_id: string | null;
  ip: string | null;
  user_agent: string | null;
  details: Record<string, unknown> | null;
  /** Backend metadata_json — token-authenticated writes carry token_id + token_username. */
  metadata?: Record<string, unknown> | null;
  /** WHICH thing the row is about, in words — a user's e-mail, a storage's
   *  name, a file's path (backend handlers/audit_targets.go). */
  target_name?: string | null;
}

export interface Share {
  id: number;
  token: string;
  /** Canonical public link (`<public origin>/s/<token>`), sent by the server. */
  url?: string;
  node_id?: number;
  storage_id?: number;
  storage_name?: string;
  path?: string;
  /** Legacy boolean — older callers still set this. */
  pin_set?: boolean;
  /** Current backend field. */
  has_pin?: boolean;
  /**
   * Can this link's PIN still be SHOWN to its owner or to an admin?
   *
   * Since migration 00049 the PIN is also sealed (internal/secretbox, under
   * FILEX_SECRET_KEY) so those two principals can be told it again. False for
   * a link with no PIN, for one minted before that migration, and on an
   * instance with no secret key — the three cases a row must say plainly
   * rather than offer a copy button with nothing behind it. The PIN itself is
   * never in a listing; it comes from GET /api/shares/{id}/pin, one row at a
   * time and audited.
   */
  pin_recoverable?: boolean;
  expires_at?: string | null;
  max_downloads?: number | null;
  download_count?: number;
  created_by?: string | number;
  /** Token username the creating API call acted under ("work", "fishapp"…). */
  created_via?: string;
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
  /** What the job is about, in words — a file's path with its storage, or a
   *  storage's name (handlers/queue.go subjectOf). */
  subject?: string;
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

/**
 * Where a click on a notification goes. Filled by the backend
 * (`model.NotificationTarget`); absent on rows that have nothing to open and
 * on every row written before the field existed — both read as "none".
 *
 * ⚠ Resolve it with `lib/notificationTarget.ts`, never by reading `meta`. The
 * whole point of the field is that one rule decides where every surface lands.
 */
export interface NotificationTargetRef {
  /** `trash` — the Trash view, the item deleted from `path` selected. */
  kind: 'file' | 'dir' | 'share' | 'trash' | 'none';
  /** Storage NAME, not id — the explorer addresses storages by name. */
  storage?: string;
  /** Path inside that storage, relative, no `<storage>://` prefix. */
  path?: string;
  /** Share token, for `kind: 'share'`. */
  id?: string;
}

export interface NotificationItem {
  id: number;
  event: string;
  severity: Severity;
  title: string;
  body: string;
  meta: Record<string, unknown>;
  target?: NotificationTargetRef;
  user_id?: number | null;
  /** Display name of the row's person — admin list only (server fills it). */
  user_name?: string;
  /** A broadcast only administrators' bells show — admin list only. */
  admins_only?: boolean;
  /**
   * Who a BROADCAST reaches — admin list only, the bells' own rule
   * (notify.BroadcastAudience): everybody, administrators and the members who
   * can see the file it names, administrators only, or no bell at all (routine
   * file activity recorded without the person who did it).
   */
  audience?: 'everyone' | 'viewers' | 'admins' | 'nobody';
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
  /** The digest's urgent choices (kind -> told at once?); see core
   *  SettingsNotificationPrefs. */
  urgent_overrides?: Record<string, boolean>;
  /** The digest as the settings pane draws it; null when it is off. */
  digest?: {
    window_minutes: number;
    urgent_events: string[];
    default_urgent: string[];
    events: string[];
    admin_events: string[];
  } | null;
}

/** The save of the person's own notification settings. ⚠ `urgent_overrides`
 *  left out keeps the stored ones (backend handlers/notifications.go). */
export interface NotificationSettingsPatch {
  in_app_enabled: boolean;
  muted_events: string[];
  urgent_overrides?: Record<string, boolean>;
}

/** GET/PATCH /api/admin/notifications/digest - the digest's defaults for the
 *  tenant (or the instance) the administrator runs. */
export interface DigestPolicy {
  window_minutes: number;
  urgent_events: string[];
  /** An administrator chose these; otherwise they are the built-in ones. */
  saved: boolean;
  scope: 'instance' | 'tenant';
  tenant: { id: number; name: string } | null;
  defaults: { window_minutes: number; urgent_events: string[] };
  events: string[];
  admin_events: string[];
  window_min: number;
  window_max: number;
}

export interface WebhookConfig {
  url: string;
  token_set: boolean;
}

// ─── Webhook v2 targets (bag:b3) ─────────────────────────────

export interface WebhookTargetLastStatus {
  status: 'sent' | 'failed';
  error?: string;
  at: string;
}

export interface WebhookTarget {
  id: number;
  name: string;
  url: string;
  secret_set: boolean;
  events: string[];
  enabled: boolean;
  created_at: string;
  /** Legacy in-memory last delivery (process lifetime only). */
  last_status?: WebhookTargetLastStatus | null;
  /** Persisted last delivery (migration 00019) — survives restarts.
   *  HTTP status code of the final attempt; 0 = no response at all. */
  last_http_status?: number | null;
  /** Aggregated error message; absent after a successful delivery. */
  last_error?: string | null;
  /** Timestamp of the newest delivery attempt (RFC3339, UTC). */
  last_delivery_at?: string | null;
}

export interface WebhookTargetCreatePayload {
  name: string;
  url: string;
  secret?: string;
  events?: string[];
  enabled?: boolean;
}

export interface WebhookTargetPatchPayload {
  name?: string;
  url?: string;
  secret?: string; // absent = keep, '' = clear, value = replace
  events?: string[];
  enabled?: boolean;
}

export interface WebhookTargetTestResult {
  ok: boolean;
  result: WebhookTargetLastStatus;
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
  /** The storage the path belongs to (0: a failure from before 0.53). */
  storage_id?: number;
  path: string;
  op: string;
  error_code: string;
  error_msg: string;
  attempts: number;
  last_attempt_at: string;
  resolved_at?: string | null;
}

/** A storage's initial copy to its replication target (#186): what the
 *  storage already held when it was linked, copied in the background. */
export type ReplicaCopyPhase = 'pending' | 'counting' | 'copying' | 'waiting' | 'done';

export interface ReplicaInitialCopy {
  storage_id: number;
  target_id: number;
  storage_name: string;
  target_name: string;
  phase: ReplicaCopyPhase;
  /** total is final once counted is true. */
  counted: boolean;
  total: number;
  /** copied + present + excluded + failed. */
  done: number;
  copied: number;
  /** Already on the target (same size and time). */
  present: number;
  /** Left out by a `skip` rule. */
  excluded: number;
  failed: number;
  copied_bytes: number;
  last_error?: string;
  started_unix: number;
  updated_unix: number;
  finished_unix: number;
}

/** The folder a storage writes into on its replication target: chosen once
 *  from its name, kept through a rename (#186). */
export interface ReplicaLink {
  storage_id: number;
  target_id: number;
  folder: string;
  storage_name: string;
  target_name: string;
  created_unix: number;
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
