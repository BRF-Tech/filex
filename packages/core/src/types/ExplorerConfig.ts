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
  /**
   * Staged upload — the chunked, resumable, driver-agnostic path every client
   * speaks (docs/UPLOADS.md). The per-upload routes (`PUT/GET/DELETE {id}`,
   * `POST {id}/commit`) are derived from it by stripping `/begin`, so one
   * override moves the whole protocol.
   */
  uploadBegin: string | null;
  /** Legacy S3-presigned chunked upload. Still served by the backend for
   *  older embedders; nothing in this package calls it. */
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
  /* wiring:e2 */
  e2eEscrowChallenge: string | null;
  e2eEscrowUsed: string | null;
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
  /** Staged upload entry point; the `{id}` routes hang off it. */
  uploadBegin?: string;
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
   * Standalone editor page base — when set, "Open" on an office file
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

  /* wiring:e2 */
  /** E2E escrow proof-of-possession — `POST { path } → { id, challenge }`. */
  e2eEscrowChallenge?: string;
  /** E2E escrow use report — `POST { path, id, nonce }`. */
  e2eEscrowUsed?: string;

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

  /**
   * Upload chunk size (bytes). Default 8 MB — the server's default too, so
   * "large enough to chunk" means the same thing on both ends. The value the
   * server returns from `begin` is binding; this is what the client asks for
   * and the threshold above which a file goes on the staged path at all.
   */
  chunkSize?: number;

  /**
   * @deprecated Ignored since the move to the staged protocol. Chunks are sent
   * sequentially because `offset` — the resume point — is the contiguous run
   * from part 1; parallel parts would leave holes that a resumed upload has to
   * re-send anyway.
   */
  parallelChunks?: number;

  /** Theme. */
  theme?: ThemeMode;

  /**
   * When the tab strip is on screen. Default `'always'`.
   *
   * ⚠⚠ The default is the SAME on every surface on purpose. This package is
   * the reason the desktop app, the web explorer and the embeds are one
   * product; shipping a strip that appears in one of them and not the others
   * turns it back into three. 0.16.0 defaulted this to `'auto'` and opted the
   * desktop app in — so tabs were permanent in the app and came and went on
   * the web, which is exactly the split the shared package exists to prevent.
   *
   * `'auto'` remains as a deliberate opt-OUT for an embed too short to spend
   * a row on: it shows the strip only once a second tab exists.
   */
  tabStrip?: 'auto' | 'always';

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

  /**
   * Which set of chrome the explorer presents. A PRESET, not a feature switch:
   * nothing is removed from the code, and every capability stays reachable —
   * `'simple'` only changes what is on screen by default.
   *
   *   'standard' (default) — everything: tab strip, split pane, all three view
   *                          modes. The tool the explorer has always been.
   *   'simple'             — one pane, one folder, list/grid only. The
   *                          navigation panel starts expanded, the tab strip
   *                          and split pane are off, the gallery view mode and
   *                          the host's "How to connect" surface are hidden.
   *   'drive'              — everything `simple` does, plus the shell an end
   *                          user already knows: one primary "New" menu, one
   *                          search field in the header (with its ⌘K/Ctrl+K
   *                          escalation into the command palette), a filter row
   *                          under the breadcrumb, Folders and Files as
   *                          labelled sections in grid view, the details panel
   *                          split into Details / Activity, and a storage line
   *                          under the navigation.
   *
   * ⚠ `drive` is a SUPERSET of `simple`, not a sibling: everything `simple`
   * turns off stays off, and the code asks `simpleUi` for those questions so a
   * later change to `simple` cannot silently miss `drive`. It is a third value
   * rather than a second boolean because "which chrome" is ONE question with
   * three answers — a `driveShell: true` next to `uiProfile: 'standard'` would
   * be a combination nobody can describe, and keeping that question single is
   * why `uiProfile` was a preset to begin with.
   *
   * Why it exists (GitHub #14): the reporter's users are not in IT and read
   * split panes, tabs and mount instructions as a file manager they would have
   * to relearn. The answer was NOT a second UI — one explorer, configured, so
   * a fix lands in one place for every surface that mounts this package.
   *
   * ⚠ It does not gate the navigation panel. The panel ships in both profiles,
   * for administrators too; only its default expanded/collapsed state and the
   * rest of the chrome differ. A viewer's own collapse choice, once made,
   * outranks the profile — it is a per-viewer preference, not a policy.
   */
  uiProfile?: 'standard' | 'simple' | 'drive';

  /**
   * Render the navigation panel (Upload · Recent / Starred / Shared with me /
   * Trash · the storage list). Collapsible to an icon rail by the viewer, whose
   * choice is remembered per browser.
   *
   * Default: ON — everywhere, including the desktop app and every embed. This
   * package is the reason those are one product; a panel that appeared in the
   * web app and nowhere else would turn it back into three (0.16.0 did exactly
   * that with `tabStrip` and it had to be undone).
   *
   * ⚠ ONE exception, and it is about `rootPath`, not about who is embedding: a
   * confined embed has no storage list to show, and its views would list files
   * from OUTSIDE the folder the embed was confined to. So `rootPath` flips the
   * default to off. Setting `sideNav: true` alongside `rootPath` still wins —
   * the panel is then yours, along with what its views will show.
   */
  sideNav?: boolean;

  /**
   * Show the navigation panel's **Connections** entries — "How to connect"
   * (the storage list and the per-protocol guides: WebDAV · SFTP · FTPS · S3 ·
   * NFS · `filex mount`) and **API keys** (mint and revoke the tokens three of
   * those protocols sign in with).
   *
   * Default: on, EXCEPT under `uiProfile: 'simple'`, where it is off. Those two
   * defaults answer two different people: #14's reporter called mount
   * instructions power-user noise in front of users who are not in IT, and an
   * embedder's own tenant may still legitimately need an S3 key. Whichever one
   * you are, say so explicitly and the default stops mattering.
   *
   * ⚠ Not gated on role, ever. The backend decides what a caller may see —
   * ConnectionsPanel renders what the API returns, and `/api/tokens` caps every
   * scope against the caller's own role and grants. A UI-side role check here
   * would only hide the surface from the accounts that need it most; that is
   * the bug this became: for a year the sole place to mint the token the FTPS
   * guide told you to use was the admin panel.
   *
   * ⚠ Role is not the same question as `callerKind`. "API keys" IS hidden for
   * an app token — not because of what that caller may do, but because there is
   * no single person behind it whose keys they would be. See `callerKind`.
   *
   * ⚠ The entries live in the panel, so `sideNav: false` takes them with it.
   * Hosts that want the surface without the panel mount `<filex-connections>`
   * (or `ConnectionsPanel`) on a page of their own.
   */
  connections?: boolean;

  /**
   * Who is behind this explorer — a person, or an integration?
   *
   * `'user'` (a signed-in human, or their own API token) draws everything.
   * `'app'` suppresses the surfaces that only mean something for ONE person:
   * **API keys**, **Recent**, **Starred** and **Shared with me**. Upload, the
   * storage list, Trash and "How to connect" stay — an embed's users still
   * upload files and still need mount instructions.
   *
   * Why it exists: a filex API token authenticates AS its owner, and the embeds
   * we run authenticate every visitor with ONE shared token injected by the
   * host's proxy. v0.30.0 put "API keys" in the panel, so under that token an
   * embed visitor could list and revoke the credential the embed itself runs
   * on — and "your Recent" meant the token owner's history shown to a stranger.
   *
   * Default: read from `GET /api/files/capabilities` (`caller_kind`), which is
   * authoritative because only the server knows the token's kind (migration
   * 00030). Set this only to answer BEFORE that request lands — a host that
   * already knows it proxies with an app token spares its users the flash of a
   * Starred row that then disappears. A value here wins over the server's.
   */
  callerKind?: 'user' | 'app';


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

  /**
   * Desktop-shell hook — dragging rows OUT of the window onto the OS.
   *
   * Present only in the filex desktop app. A web page cannot hand the OS a
   * list of files: Chromium carries one `DownloadURL` per drag, so the browser
   * gets a single-file drag-out for free (the explorer sets it itself) and
   * folders/multi-selections need real local paths — which is what the shell
   * provides here.
   *
   * The bytes have to exist BEFORE the drag starts — the OS copies from a path
   * at drop time — and the shell has two ways to satisfy that, which is why
   * this is more than one call:
   *
   *   `prepare` — fetch local copies up front. The explorer calls it for small
   *     selections as soon as they are selected, so the common drag hands over
   *     real, complete files (correct even when the drop target is an
   *     application that reads the file immediately).
   *   `start` — begin the OS drag, whatever the size. The shell may hand the
   *     OS empty placeholders and download into wherever they land afterwards,
   *     so this is never gated on `prepare` having finished.
   *   `cancel` — the drag ended INSIDE the explorer (an internal move). The
   *     shell stops waiting for a drop it will never see.
   *
   * `onProgress` drives the explorer's toast; `error: 'drop_not_found'` means
   * the drop went somewhere the shell cannot write to (an application rather
   * than a folder) and nothing was transferred.
   */
  dragOut?: {
    prepare: (
      items: Array<{ path: string; basename: string; type: 'file' | 'dir' }>,
    ) => Promise<{ ready: boolean; error?: string }>;
    start: (
      items: Array<{ path: string; basename: string; type: 'file' | 'dir' }>,
    ) => void | Promise<void>;
    cancel?: () => void | Promise<void>;
    onProgress?: (
      cb: (p: {
        done: number;
        total: number;
        name?: string;
        dropped?: string;
        finished?: boolean;
        error?: string;
      }) => void,
    ) => void;
  };

  /**
   * Desktop-shell hook — selective sync ("keep on this computer").
   *
   * Present only when the explorer runs inside the filex desktop app; the
   * shell passes functions that talk to its sync engine, and the explorer
   * grows "Keep on this computer" / "Online only" entries on folder menus.
   * Absent (web admin, embeds): nothing about it renders.
   *
   * Folders only, by design: the sync engine pairs directories, and a file
   * rides along with the folder that holds it.
   */
  desktopSync?: {
    /** Kept folders for the mounted account, adapter-qualified remotes. */
    kept: () => Promise<Array<{ remote: string; local: string }>>;
    /** Start keeping a folder — or, with kind 'file', a single file.
     *  Resolves once the pair is registered (or the user cancelled the
     *  shell's root-folder prompt — re-read `kept`). */
    keep: (remote: string, kind: 'dir' | 'file') => Promise<void>;
    /** Stop keeping. The SHELL owns the "what happens to the local copy"
     *  question — it asks natively and may cancel; re-read `kept` after. */
    unkeep: (remote: string) => Promise<void>;
    /** Open the folder's local mirror in the OS file manager. */
    reveal: (remote: string) => Promise<void>;
    /** Live engine state, for the row badges and the bottom progress strip.
     *  `active` is the folder being worked on right now — the engine walks
     *  its pairs one at a time — or null between runs. */
    status?: () => Promise<{
      running: boolean;
      lastError?: string | null;
      active: {
        remote: string;
        phase: 'inventory' | 'plan' | 'transfer' | 'settling';
        done: number;
        total: number;
      } | null;
    }>;
    /** Subscribe to "something about sync changed" pokes from the shell —
     *  the explorer re-reads `kept` and `status` when poked. The shell keeps
     *  ONE subscriber (the mounted explorer) and overwrites it on remount. */
    onChange?: (cb: () => void) => void;
  };
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
