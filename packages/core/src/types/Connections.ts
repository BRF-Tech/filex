/**
 * Connections — the shapes the storage-connection surface works with.
 *
 * Every one of these mirrors something the BACKEND declares, on purpose.
 * `StorageField` / `StorageDriverDescriptor` are the wire form of
 * `backend/internal/storage/descriptor.go`, served by
 * `GET /api/admin/storage-drivers`: a driver states its own config keys,
 * their types, which one is the storage root and which hold credentials,
 * and every surface renders that one declaration.
 *
 * The alternative — a form per surface — is exactly what shipped broken:
 * three of the four drivers the admin UI offered could not be created
 * through it because the form collected keys the backend never read. A
 * second hand-written form in the desktop app would have been a fourth
 * copy of the same mistake.
 */

export type StorageFieldType = 'string' | 'int' | 'bool' | 'password' | 'select';

export interface StorageFieldOption {
  value: string;
  /** English fallback; `i18n_key` wins when the catalogue has it. */
  label: string;
  i18n_key?: string;
}

/** One config key of a storage driver, as declared by the driver itself. */
export interface StorageField {
  key: string;
  type: StorageFieldType;
  /** English fallback label — used only when `i18n_key` is missing from
   *  the catalogue, so a driver released after this build still renders
   *  readable labels instead of raw keys. */
  label: string;
  help?: string;
  i18n_key: string;
  help_i18n_key?: string;
  required: boolean;
  /** Credential material: render masked, never log, never put in a URL. */
  secret: boolean;
  default?: unknown;
  placeholder?: string;
  options?: StorageFieldOption[];
  min?: number;
  max?: number;
  monospace?: boolean;
  multiline?: boolean;
  advanced?: boolean;
  /** THE field that scopes the storage inside the backend (s3 prefix,
   *  local path, sftp/ftp/webdav root). The backend rejects an empty or
   *  "/" value with ROOT_PATH_FORBIDDEN. */
  root?: boolean;
  /** Legacy spellings the driver still reads for this field. */
  aliases?: string[];
}

export interface StorageDriverCapabilities {
  read?: boolean;
  write?: boolean;
  move?: boolean;
  copy?: boolean;
  delete?: boolean;
  mkdir?: boolean;
  presign?: boolean;
  watch?: boolean;
}

export interface StorageDriverDescriptor {
  driver: string;
  label: string;
  i18n_key: string;
  fields: StorageField[];
  capabilities: StorageDriverCapabilities;
}

/** A configured storage, as `GET /api/admin/storages` returns it. */
export interface StorageRow {
  id: number;
  name: string;
  driver: string;
  enabled: boolean;
  read_only: boolean;
  config: Record<string, unknown>;
  rbac_enabled?: boolean;
  sync_mode?: string;
  created_at?: string;
  updated_at?: string;
  stats?: { file_count?: number; total_size_bytes?: number };
}

/** POST/PATCH body for a storage. */
export interface StorageWrite {
  name: string;
  driver: string;
  config: Record<string, unknown>;
  read_only?: boolean;
  enabled?: boolean;
}

export interface StorageTestResult {
  ok: boolean;
  error?: string;
  object_count?: number;
  sample_listing?: unknown[];
}

/** The signed-in account, from `GET /api/auth/me`. */
export interface ConnectionsUser {
  id: number;
  email: string;
  display_name?: string;
  role?: string;
}

/**
 * Why the caller cannot manage storages, when they cannot.
 *
 * `none` is the honest answer for a signed-in non-admin: the surface then
 * shows the connection guides (which every user needs) and a plain "ask
 * your administrator" line instead of a form that would 403 on submit.
 */
export type ManageDenial = 'none' | 'anonymous' | 'unreachable';
