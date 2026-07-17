/**
 * FileNode — node shape returned by the manager endpoint.
 *
 * Backend `?q=index` (or `GET /api/files/manager?action=index`) returns:
 *   { adapter, storages, dirname, files: FileNode[] }
 */
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
  /* wiring:e2 — dir rows: true when the folder is E2E-encrypted (carries a
   * `.filex-e2e.json` marker). Drives the 🔒 badge in the listings. */
  e2e?: boolean;
  /** Generic — any additional fields the backend wants to inline. */
  [k: string]: unknown;
}

export interface ShareInfo {
  uuid: string;
  url: string;
  password_pin?: string | null;
  expires_at?: string | null;
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

export interface Capabilities {
  ffmpeg?: boolean;
  ghostscript?: boolean;
  libreoffice?: boolean;
  onlyoffice_url?: string | null;
  drawio_url?: string | null;
  convert_url?: string | null;
  max_chunk_mb?: number;
  upload_limit_mb?: number;
  external?: {
    onlyoffice?: ExternalServiceStatus;
    drawio?: ExternalServiceStatus;
    mermaid?: ExternalServiceStatus;
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

export type ViewMode = 'list' | 'grid' | 'gallery'; /* wiring:d2 — üçüncü görünüm: galeri */

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
