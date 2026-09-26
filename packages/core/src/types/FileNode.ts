/**
 * FileNode — node shape returned by the manager endpoint.
 *
 * Backend `?q=index` (or `GET /api/files/manager?action=index`) returns:
 *   { adapter, storages, dirname, files: FileNode[] }
 */
import type { AppLock } from './Plugins';

export interface FileNode {
  /** DB node ID — needed by the per-user meta routes (starred, tags,
   *  recently-opened). Backend's projectFileNodes() emits it for every
   *  row; only client-synthesized rows (e.g. multi-storage virtual
   *  folders) lack one. */
  id?: number;
  /** Full adapter-qualified path: `local://receipts/2024/invoice.pdf` */
  path: string;
  /** Relative basename: `invoice.pdf` */
  basename: string;
  /** Adapter-stripped relative path: `receipts/2024/invoice.pdf` */
  relativePath?: string;
  /** `file` or `dir` */
  type: 'file' | 'dir';
  /** Extension (lowercased, no dot). Empty string = no extension. */
  extension?: string;
  /** Bytes. 0 = directory. */
  size?: number;
  /**
   * The folder's size leaves something out: the catalog does not cover all of
   * it yet (a lazily cataloged folder, or any folder while its storage's first
   * sync runs). `size` is then a lower bound, or unknown when 0. Draw it with
   * useLocale `formatNodeSize`, never `formatSize` (docs/LAZY-CATALOGUE.md).
   */
  size_partial?: boolean;
  /** Unix ms (backend "last_modified"). */
  last_modified?: number;
  /** MIME. */
  mime_type?: string;
  /** Optional thumbnail URL (backend may inline). */
  thumb_url?: string | null;
  /** Visibility: private | public. */
  visibility?: 'private' | 'public';
  /** File count for directories. */
  count?: number;
  /** Client-side tag (localStorage or backend). */
  starred?: boolean;
  /** Hex color tag. */
  color?: string | null;
  /** Server-side trash marker. */
  trashed?: boolean;
  /** RBAC effective level for the current user on this entry (backend
   *  projectFileNodes emits it when a storage has RBAC on). '' / undefined =
   *  ACL not enforced. Used to gate edit/manage affordances client-side. */
  perm?: 'none' | 'viewer' | 'editor' | 'owner';
  /** The row sits on a read-only storage. Carried by the rows that come from
   *  OUTSIDE a folder listing — Recent, Starred, a tag view, the Home cards,
   *  the Recently-opened tray (`handlers/meta.go`) — because there the
   *  listing-level `read_only` has no folder to describe. A folder listing's
   *  rows leave it unset; `dirReadOnly` answers for the whole folder. Folded
   *  into the write gate with `perm`, so the context menu is the same in
   *  every view. */
  read_only?: boolean;
  /* wiring:e2 — dir rows: true when the folder is E2E-encrypted (carries a
   * `.filex-e2e.json` marker). Drives the 🔒 badge in the listings. */
  e2e?: boolean;
  /** An app plugin holds this file read-only (docs/APP-PLUGINS-API.md →
   *  "File locks"). `perm` already arrives capped at `viewer` for everyone,
   *  administrators included — the flag is what the badge and the details
   *  panel say out loud, and what turns the server's 423 into words. */
  locked?: boolean;
  lock?: AppLock;
  /** The state keys apps keep on this file, `<plugin>:<key>`. Read by the
   *  menu's state-aware rows (`applies.state` / `no_state`). */
  app_state?: string[];
  /** Issue #34 — this row is a symlink the SERVER WILL NOT FOLLOW. A link
   *  whose target is inside the storage root IS followed and arrives as that
   *  target (a linked directory is a `dir` and opens normally), so this flag
   *  always means "cannot be opened", never merely "is a link".
   *
   *  ⚠ Additive, and `type` stays the closed `'file' | 'dir'` union it has
   *  always been: widening it would be a breaking change for every embedder
   *  of `@brftech/filex`, so the backend reports such a row as a file and
   *  flags it here (`handlers/manager.go`). A client that does not know the
   *  flag renders exactly the row it rendered before — which is precisely the
   *  0-byte-file-that-will-not-open the issue was filed about, and why
   *  `lib/symlink` exists. */
  symlink?: boolean;
  /** Why it will not open — `outside_root` | `broken` | `unresolved`.
   *
   *  ⚠⚠ Absent far more often than present: only the cold-cache driver
   *  listing carries it. The normal DB-backed listing sends `symlink: true`
   *  alone, because `model.Node` has no column for a fact that belongs to the
   *  link as it is RIGHT NOW rather than as it was at scan time. Read it
   *  through `lib/symlink.linkStateOf`, which answers `'unknown'` for that
   *  case — and for a state a newer server invents — instead of dropping the
   *  row back into silence. */
  link_state?: string;
  /** Generic — any additional fields the backend wants to inline. */
  [k: string]: unknown;
}

export interface ShareInfo {
  uuid: string;
  url: string;
  password_pin?: string | null;
  expires_at?: string | null;
  /** True when the server shortened (or set) the expiry to honour its
   *  max-TTL setting — the UI then shows the real date, not the request. */
  expiry_clamped?: boolean;
  max_downloads?: number | null;
  downloads?: number;
  created_at?: string;
}

export interface UploadLimits {
  max_upload_mb: number;
}

export type ExternalServiceState = 'ok' | 'error' | 'disabled' | 'unknown';

export interface ExternalServiceStatus {
  enabled: boolean;
  state: ExternalServiceState;
  url?: string;
  last_check?: string;
  detail?: string;
}

/**
 * One document type the SERVER can create, from `capabilities.newdoc_types`.
 *
 * ⚠ `requires` is the whole point of shipping this list instead of hardcoding
 * one in the client. The server knows it holds template bytes for `.docx`; it
 * does NOT know whether this deployment has a document server that can open
 * one. So it names the dependency and the client — which already resolves
 * OnlyOffice/drawio, config override included — answers it. A client that
 * re-derived "docx needs OnlyOffice" from a list of its own would be a second
 * source of truth, and the one that rots first.
 */
export interface NewDocType {
  /** Extension without the dot, lowercase. Also the key the create call sends. */
  ext: string;
  /** Coarse family, used for the picker's section headings. */
  group: 'document' | 'text' | 'diagram';
  mime: string;
  /** External service the editor for this type needs; absent = built-in. */
  requires?: 'onlyoffice' | 'drawio';
  /**
   * Must the file carry this extension? (#56) `true` for the containers — an
   * office document or a diagram, which its editor finds by extension; `false`
   * for text, which may be named anything (`LICENSE`, `test.conf`). ABSENT on
   * a server from before #56, which appends the extension to every type —
   * lib/newDocName `extLocked` reads that as `true`.
   */
  ext_required?: boolean;
}

export type ArchiveCreateFormat = 'zip' | '7z' | 'tar' | 'tar.gz' | 'tar.bz2' | 'tar.xz';

export interface Capabilities {
  /** Document types this build can create. Absent on a server older than the
   *  "New document" feature — hosts must treat that as "offer nothing". */
  newdoc_types?: NewDocType[];
  ffmpeg?: boolean;
  ghostscript?: boolean;
  libreoffice?: boolean;
  onlyoffice_url?: string | null;
  drawio_url?: string | null;
  convert_url?: string | null;
  max_chunk_mb?: number;
  upload_limit_mb?: number;
  /** Longest life a new share link may be given, in days (0 = no ceiling).
   *  Read by the share dialogs so they offer only expiries the server keeps. */
  share_max_ttl_days?: number;
  /** Archive creation policy. Absent on servers older than archive providers.
   *  `allowed_formats` is what this server can actually make (every format
   *  but a plain ZIP needs 7-Zip there); empty means it can make none, and
   *  `encryption` says whether a password can be set. */
  archive?: {
    enabled: boolean;
    default_format: ArchiveCreateFormat | '';
    allowed_formats: ArchiveCreateFormat[];
    encryption?: boolean;
  };
  /** Is the caller a person (`'user'` — a session OR their own API token) or an
   *  integration (`'app'` — a host proxy, a bot, an MCP client)? The explorer
   *  reads it to decide whether to draw the identity-bearing surfaces; see
   *  ExplorerConfig.callerKind. Absent on a server older than the app/user
   *  token split, which is why every reader treats "missing" as a person. */
  caller_kind?: 'user' | 'app';
  /** Could this caller set up a missing optional service (ONLYOFFICE,
   *  draw.io, the converter…)? An administrator who can reach the instance
   *  settings — not a tenant admin, not an API token. Decides between "greyed
   *  with where to fix it" and "not offered" (lib/serviceGate). Absent on an
   *  older server, which reads as "no". */
  caller_admin?: boolean;
  /** App plugins (docs/APP-PLUGINS-API.md). Absent or `enabled: false` → the
   *  explorer makes no plugin request at all. */
  app_plugins?: { enabled: boolean };
  /** The address this deployment is reached at — only when it is real (the
   *  operator configured it, or the request came in on a tenant's host).
   *  Read by the connection guides; see `connectionsOrigin`. */
  public_url?: string;
  external?: {
    onlyoffice?: ExternalServiceStatus;
    drawio?: ExternalServiceStatus;
    mermaid?: ExternalServiceStatus;
    /** The file converter. ⚠ `convert_url` is filled whenever the service is
     *  ENABLED, healthy or not — the menu reads this for health. */
    convert?: ExternalServiceStatus;
  };
  /** Can outgoing mail be sent right now (SMTP configured AND verified)?
   *  Absent on an older server or one with no mailer wired — read as "yes",
   *  i.e. keep offering mail the way it always was. */
  mail?: { ready: boolean };
  /* wiring:e2 */
  /** Whether this installation holds an escrow key for E2E-encrypted folders,
   *  and the public half the browser wraps new folders' master keys to.
   *
   *  Published on purpose. Escrow means the operator can open the folders you
   *  create here without your password, and someone about to create one is
   *  entitled to know that BEFORE they create it. Fixed at install time
   *  (FILEX_INSTALLATION_E2E_ESCROW_KEY), so this answer never changes for a
   *  running installation. */
  e2e_escrow?: {
    enabled: boolean;
    /** Short id of the escrow key (SHA-256(SPKI)[:8], hex). */
    kid?: string;
    alg?: string;
    /** Base64 SPKI. Public material — it can only seal, never open. */
    public_key?: string;
  };
}

/** Single source of truth for "is the IdP/editor/diagram service ready?".
 *  A capability is usable only when both flags say so — `enabled=true` but
 *  `state='error'` means an operator turned it on but a probe just failed,
 *  and we'd rather hide the entry than offer a button that 500s on click.
 */
export function isExternalUsable(s: ExternalServiceStatus | undefined): boolean {
  return !!s && s.enabled && s.state === 'ok';
}

export interface UploadInitResponse {
  uploadId: string;
  parts: Array<{ partNumber: number; presignedUrl: string }>;
  expiresAt: string;
  s3Key?: string;
}

export interface UploadFinalizeResponse {
  s3Key: string;
  url?: string;
}

export interface ArchiveEntry {
  name: string;
  size: number;
  isDir: boolean;
  lastModified?: number;
}

export type ViewMode = 'list' | 'grid' | 'gallery'; /* wiring:d2 — the third view: gallery */

export interface ClipboardState {
  mode: 'cut' | 'copy' | null;
  items: FileNode[];
  sourcePath: string | null;
}

/** A soft-deleted node as returned by the filex trash listing endpoint. */
export interface TrashEntry {
  id: number;
  storage_id: number;
  storage_name?: string;
  path: string;
  name: string;
  size: number;
  mime?: string;
  deleted_at: string;
  ttl_days?: number | null;
}
