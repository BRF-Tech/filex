/**
 * ExplorerConfig — props passed to the FileExplorer component.
 *
 * Two equivalent ways to wire it up:
 *
 *   1. New clean API (preferred — RESTful, server-agnostic):
 *      { apiBase: 'https://files.example.com', auth: { kind: 'bearer', token } }
 *      → URLs like `${apiBase}/api/files/manager`, `${apiBase}/api/files/upload/init`.
 *
 *   2. Legacy API (Vuefinder-compat — for embedders that mounted the
 *      old `@brftech/file-explorer` package against their own routes):
 *      { endpoint: '/api/files/manager', uploadInit: '/api/files/upload/init', … }
 *      → caller fully controls every URL.
 *
 * If both `apiBase` AND any explicit `endpoint`/`uploadInit`/etc. are
 * present, the explicit field wins (lets you override one route while
 * keeping the auto-derived rest).
 */

/**
 * Auth strategy. Discriminated union so request building is type-safe.
 *
 * - `bearer`  — `Authorization: Bearer <token>`. Token may be a string
 *               OR a sync/async function (auto-refresh).
 * - `csrf`    — `X-CSRF-TOKEN` + `credentials: include` (Laravel/Filament).
 * - `basic`   — `Authorization: Basic <base64(user:pass)>`.
 * - `none`    — no auth (development / public sandbox).
 */
export type AuthConfig =
  | { kind: 'bearer'; token: string | (() => string | Promise<string>) }
  | { kind: 'csrf'; csrf: string }
  | { kind: 'basic'; user: string; pass: string }
  | { kind: 'none' }
  /**
   * Legacy `type` field (back-compat with @brftech/file-explorer 0.1.0).
   * Internally normalized to the `kind`-tagged shape.
   */
  | { type: 'bearer'; token: string }
  | { type: 'csrf'; csrf: string };

export type ThemeMode = 'light' | 'dark' | 'auto';
export type LocaleCode = 'tr' | 'en';

/**
 * Resolved endpoint map. `useFileApi` derives this once on construction
 * — components never need to think about config/apiBase precedence.
 */
export interface EndpointMap {
  manager: string;
  uploadInit: string | null;
  uploadFinalize: string | null;
  uploadAbort: string | null;
  shareCreate: string | null;
  shareList: string | null;
  shareDelete: string | null;
  limits: string | null;
  capabilities: string | null;
  archiveList: string | null;
  archiveExtract: string | null;
  archiveAdd: string | null;
  copy: string | null;
  moveAsync: string | null;
  deleteAsync: string | null;
  opsList: string | null;
  opsShow: string | null;
  onlyOfficeConfig: string | null;
  saveText: string | null;
  restore: string | null;
  trashList: string | null;
  trashRestore: string | null;
}

export interface ExplorerConfig {
  /**
   * Modern shorthand: URL prefix for the standard /api/files/* layout.
   * Example: `https://files.example.com` → `${apiBase}/api/files/manager`,
   * `${apiBase}/api/files/upload/init`, etc. Any explicit endpoint*
   * field below overrides the derived URL.
   */
  apiBase?: string;

  /** Legacy main endpoint (Vuefinder-compat: GET/POST `?q=…`). */
  endpoint?: string;

  // ——— Per-route overrides (optional; auto-derived from apiBase if absent) ———
  uploadInit?: string;
  uploadFinalize?: string;
  uploadAbort?: string;

  shareCreate?: string;
  shareList?: string;
  /** DELETE template; `{uuid}` placeholder is replaced at call time. */
  shareDelete?: string;

  limits?: string;
  capabilities?: string;

  archiveList?: string;
  archiveExtract?: string;
  archiveAdd?: string;

  /** Recursive S3-side copy with "-copy" collision suffix (async). */
  copy?: string;

  /** Async move endpoint — returns {op}, client polls /opsList. */
  moveAsync?: string;

  /** Async delete endpoint — returns {op}, client polls /opsList. */
  deleteAsync?: string;

  /** Pending ops list endpoint (poll target). */
  opsList?: string;

  /** Single op show endpoint — `{id}` placeholder. */
  opsShow?: string;

  /**
   * OnlyOffice config endpoint. Backend POST returns
   * `{ config, documentServerUrl }` where `config` is a JWT-signed
   * DocEditor config. PreviewModal mounts the editor against this
   * when the user opens an office file with mode=edit.
   */
  onlyOfficeConfig?: string;

  /**
   * Standalone editor page base — when set, "Aç" on an office file
   * opens `${openPageBase}?path=...&mode=edit` in a new tab instead
   * of the modal preview.
   */
  openPageBase?: string;

  /**
   * Plain-text save endpoint — POST `{path, content}`. When set, code
   * preview gains an editable mode + save button. Falsy = read-only.
   */
  saveText?: string;

  /**
   * Trash restore endpoint — `POST {source: string[]}`. When set, a
   * "Geri Getir" action shows up in `.trash/` listings.
   */
  restore?: string;

  /** filex trash listing endpoint — `GET → { entries: TrashEntry[] }`. */
  trashList?: string;
  /** filex trash restore endpoint — `POST { node_id }`. */
  trashRestore?: string;

  /** Public share base URL — `${shareBase}/${uuid}` */
  shareBase?: string;

  /** Auth strategy (see AuthConfig). */
  auth?: AuthConfig;

  /** Show the virtual `.trash/` entry in the root listing. */
  trashVisible?: boolean;

  /** UI dil kodu */
  locale?: LocaleCode;

  /** OnlyOffice iframe base (e.g. `https://docs.example.com`). */
  onlyOfficeBase?: string;

  /** Drawio iframe base. */
  drawioBase?: string;

  /** Universal converter (p2r3/convert fork) iframe base, e.g. `https://fm.example.com/convert`. */
  convertBase?: string;

  /**
   * Drawio embed URL (full URL to the embed endpoint). Defaults to
   * `https://embed.diagrams.net`. The DrawioViewer iframes this with
   * `?embed=1&proto=json` postMessage handshake to load + save XML.
   */
  drawioUrl?: string;

  /**
   * Optional URL where serialized PDF annotations / form data is
   * persisted. POST `{ path, base64 }` — the rich PdfViewer uses
   * `pdf.saveDocument()` and forwards the saved bytes here when the
   * user clicks the save annotation button.
   */
  pdfSaveUrl?: string;

  /**
   * Override the pdf.js worker URL. Defaults to a CDN copy matching
   * the version pdfjs-dist resolves to at runtime — hosts can pin
   * their own self-hosted worker for CSP-strict environments.
   */
  pdfWorkerUrl?: string;

  /**
   * Standalone full-screen viewer route. The PreviewModal toolbar's
   * "Open in new tab" button navigates to
   * `${viewerBaseUrl}?path=…&storage=…&type=…`. The consumer wires
   * that route to mount the same viewer fullscreen (admin UI / fishapp
   * embed). Defaults to `/files/viewer`.
   */
  viewerBaseUrl?: string;

  /** Upload chunk size (bytes). Default 5 MB. */
  chunkSize?: number;

  /** Parallel chunks. Default 4. */
  parallelChunks?: number;

  /** Theme. */
  theme?: ThemeMode;

  /** Initial path (storage-prefix included: `local://`). Default: root. */
  initialPath?: string;

  /**
   * Confine the explorer to this folder (qualified: `main://projeler/acme`).
   * The UI opens here, hides the multi-storage drives root, and blocks
   * navigation above it. SECURITY IS NOT THIS — enforce it server-side with an
   * X-Filex-Root header / root-scoped API token; this is the clean-embed UX.
   */
  rootPath?: string;

  /** Whether the info panel toggle is visible. */
  showInfoPanel?: boolean;

  /** Default view. */
  viewMode?: 'list' | 'grid';

  /** Override max upload size (MB) — falls back to /limits otherwise. */
  maxFileSizeMb?: number;

  /** Accept patterns (MIME or extension). Empty = unrestricted. */
  acceptTypes?: string[];

  /** Storage adapter to default to (avoids the initial flash). */
  defaultAdapter?: string;

  /**
   * Where to persist the current path so reload lands the user back
   * in the same folder.
   *   'hash'              — URL hash. Default — works on plain web pages.
   *   'localStorage'      — `brf-file-explorer:path` key. Best for SPAs that
   *                         already own the URL (Ionic / Vue Router / Next).
   *   'hash+localStorage' — both: the address bar always mirrors the
   *                         current folder (copy-paste deep links), and
   *                         localStorage remembers it for hash-less visits.
   *                         Read priority: hash → `initialPath` → localStorage.
   *   'none'              — don't persist. Embedder controls path externally.
   */
  pathPersist?: 'hash' | 'localStorage' | 'hash+localStorage' | 'none';

  /**
   * Multi-storage root mode. When true, the explorer's "/" virtual
   * folder lists every entry in `storages` as a clickable directory.
   * Clicking one drills into that storage's root; the breadcrumb
   * walks `/ › <storage> › <sub> › …`.
   *
   * When false (default) the SFC still works against a single
   * storage — `defaultAdapter` / `initialPath` decide which one.
   *
   * Pair with `storages` so the SFC has labels + driver hints to
   * render even before the first API call.
   */
  multiStorageRoot?: boolean;

  /**
   * Storage list for `multiStorageRoot` mode. Provide name + driver
   * + (optional) display label / read-only flag. The SFC mirrors
   * each entry as a virtual `dir` row at "/".
   */
  storages?: Array<{
    name: string;
    label?: string;
    driver?: string;
    readOnly?: boolean;
  }>;
}

/** Component emits — the parent listens for these events. */
export interface ExplorerEmits {
  'share-created': (payload: { path: string; url: string; pin: string | null }) => void;
  'file-opened': (file: { path: string; basename: string }) => void;
  error: (err: { message: string; context?: unknown }) => void;
  'upload-progress': (p: { uploadId: string; percent: number; done: boolean }) => void;
  'selection-change': (
    items: Array<{ path: string; basename: string; type: 'file' | 'dir' }>,
  ) => void;
}
