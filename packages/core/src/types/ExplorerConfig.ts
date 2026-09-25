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

import type { UiProfile } from '../lib/uiProfile';
import type { GlobalSearchHit, GlobalSearchScope } from '../composables/useFileApi';

export type { UiProfile };

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
/**
 * A language code. `tr` and `en` are the two this package ships a catalogue
 * for; ANY other tag may arrive too — a language pack (an app whose manifest
 * carries `ui_locales`) adds languages at run time, see `lib/uiLocales`.
 * `(string & {})` keeps the two named ones in autocompletion without
 * pretending they are the only ones.
 */
export type LocaleCode = 'tr' | 'en' | (string & {});

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
  archiveCreate: string | null;
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
  /* App plugins (docs/APP-PLUGINS-API.md). Templates carry `{plugin}`,
   * `{action}`, `{view}` and `{id}` placeholders, filled at call time. */
  pluginActions: string | null;
  pluginActionRun: string | null;
  pluginView: string | null;
  pluginViewEvent: string | null;
  /** `?plugin=<name>&q=` — the people-picker's user lookup (M2). */
  pluginUsers: string | null;
  /** `POST` cancel of a queued/running ops row — `{id}` placeholder. */
  opsCancel: string | null;
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
  archiveCreate?: string;
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

  /** Cancel a queued/running op — `{id}` placeholder (plugin jobs). */
  opsCancel?: string;

  /** App plugins: the actions list, the run template (`{plugin}`/`{action}`),
   *  the view template (`{plugin}`/`{view}`) and its `/event` sibling. */
  pluginActions?: string;
  pluginActionRun?: string;
  pluginView?: string;
  pluginViewEvent?: string;
  pluginUsers?: string;

  /**
   * App plugins (file-menu rows drawn from WebAssembly plugins).
   *
   * Absent: follow the server — `capabilities.app_plugins.enabled`. `false`
   * switches the feature off for this instance whatever the server says, and
   * the explorer then makes no plugin request at all. `true` asks even when
   * the capabilities answer is missing (a host that knows its backend).
   */
  plugins?: boolean;

  /**
   * Where this host serves the SPA, for an app plugin's `page` view
   * (`${pluginPageBase}apps/{plugin}/{view}?path=…`, lib/pluginPage).
   *
   * ⚠ A mount BASE, not the site root: the same bundle is served from
   * `/admin/` and `/drive/`, and only those prefixes fall back to index.html
   * for an `apps/…` address, so a bare `/apps/…` is a 404 on the server. The
   * host passes the prefix it was itself served from.
   *
   * Absent: a `page` action falls back to the modal — the same conversation
   * in a dialog. A degraded frame, never a missing feature.
   */
  pluginPageBase?: string;

  /**
   * How this host opens a plugin page. Return `true` when the host handled
   * it; anything else lets the explorer open a browser tab itself.
   *
   * ⚠ The hook exists for shells that have no tabs to open: the desktop app
   * wants its own window (and a `window.open` there lands in the system
   * browser with no session), an embed inside another product may want a
   * panel. The RULE — which actions open a page, and what the address is —
   * stays in the package for all of them (lib/pluginPage).
   */
  openPluginPage?: (target: {
    plugin: string;
    view: string;
    path?: string;
    /** The address the explorer would open, already built from `pluginPageBase`. */
    url: string;
  }) => boolean | void;

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

  /**
   * Public share base URL.
   *
   * @deprecated IGNORED — declared, never read. Share URLs are used exactly as
   * the server returns them (`share.url`), which is what makes a link work
   * behind a reverse proxy; a client-side base could only disagree with it.
   * Set `FILEX_PUBLIC_URL` on the server instead.
   */
  shareBase?: string;

  /** Auth strategy (see AuthConfig). */
  auth?: AuthConfig;

  /** Show the virtual `.trash/` entry in the root listing. */
  trashVisible?: boolean;

  /**
   * paylas:m1 — show the navigation panel's **My shares** row: the public
   * links THIS person handed out, directly under its mirror "Shared with me".
   *
   * Default: OFF, and it is the only row in that group whose default is off
   * for a reason that has nothing to do with taste. Every other entry either
   * opens a listing this component owns (Home, Recent, Starred, Trash, a tag,
   * a storage) or a panel it draws itself ("How to connect", "API keys") —
   * this one opens a page of the HOST. The explorer can announce the intent
   * (`open-my-shares`) and nothing more; a host that does not listen is left
   * with a row that draws, takes a click and does nothing at all. Off by
   * default is therefore the honest default: `<filex-explorer>` on
   * work.example.com, in the fishapp and on fm.example.com has no such page, and a dead
   * row is worse than a missing one.
   *
   * ⚠ Opting in is a promise, not a preference: set it to `true` only if you
   * handle `@open-my-shares`. The SPA does (`web/src/views/Explore.vue` pushes
   * its `my-shares` route), which is why it is the one host that asks for it.
   * Only the Vue component emits it: `<filex-explorer>` and the React
   * `<FileManager>` do not forward `open-my-shares`, so there it cannot be
   * handled and the flag must stay off.
   *
   * ⚠ ANDed with `callerKind`, never ORed: an app token is not a person and
   * has shared nothing, so the row goes with its mirror even for a host that
   * asked for it. See `callerKind`.
   */
  mySharesVisible?: boolean;

  /**
   * The host draws an app plugin's `home` view as a page of ITS OWN, in the
   * same tab (the SPA's `app-home` route), and handles `@open-app-home`.
   *
   * ⚠⚠ The owner, 2026-09-21: the Signatures screen opened as a dialog over
   * the file list, and it should be "its own page" — the way My shares is,
   * with its sections in a menu, the Back button working, and a
   * notification landing on the right section. The explorer cannot give a
   * host a page; it can only say which one was asked for, so the navigation
   * panel's "Apps" row emits `open-app-home` when this is on.
   *
   * Default: OFF, for the reason `mySharesVisible` is off — only the Vue
   * component emits the event, and a host that does not listen would be
   * left with a row that does nothing. Off, an "Apps" row keeps opening the
   * view in a dialog, which is a smaller frame but a working one.
   */
  appHomePage?: boolean;

  /** UI dil kodu */
  locale?: LocaleCode;

  /**
   * The clock this embed's visitors read dates on by default — an IANA id,
   * `'Asia/Tokyo'`, `'Europe/Istanbul'`, `'UTC'`.
   *
   * Only which clock an instant is READ on changes; the instant itself does
   * not. Unset, a visitor sees the zone of the account behind the credential
   * when that credential is a person's, and otherwise their browser's own.
   *
   * ⚠ A default, not a lock. A visitor who picks a zone in the explorer's own
   * "⋯" → Time zone keeps their pick, in their browser. The whole order, and
   * why it is that order, is `TIME_ZONE_TIERS` in `lib/timezone`. An id the
   * browser does not accept is ignored rather than guessed at.
   */
  timeZone?: string;

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

  /**
   * Initial path (storage-prefix included: `local://`). Default: root.
   *
   * gorunum:v3-shell — a VIEW is addressable here too, by its sentinel:
   * `'.home'` opens the overview (storages · recent · starred), and
   * `'.recent'` / `'.starred'` / `'.shared'` / `'.trash'` / `'.tag~invoices'`
   * open theirs. That is how our own `/home` route opens Home without a second
   * mechanism — the sentinels are the same strings the address-bar hash, the
   * tab strip and a restored session already speak (lib/listing
   * VIRTUAL_SEGMENTS), so there is one answer to "where does this explorer
   * start" rather than a path answer and a view answer that can disagree.
   *
   * ⚠ A sentinel this build does not know keeps going to the backend as a
   * folder name, so a host cannot invent views by passing strings.
   */
  initialPath?: string;

  /**
   * Confine the explorer to this folder (qualified: `main://projeler/acme`).
   * The UI opens here, hides the multi-storage drives root, and blocks
   * navigation above it. SECURITY IS NOT THIS — enforce it server-side with an
   * X-Filex-Root header / root-scoped API token; this is the clean-embed UX.
   */
  rootPath?: string;

  /**
   * gorunum:v3-shell — the product mark at the far left of the top bar: the
   * wordmark beside it, and an image for the mark itself.
   *
   * ⚠⚠ This exists because a `<slot>` IS NOT REACHABLE from a host that mounts
   * `<filex-explorer>`, and that is not a bug anybody can fix in a line.
   * Measured, 2026-09-13, in a real browser with Vue's own
   * `defineCustomElement`: a `<span slot="brand">` placed inside the element
   * leaves `Object.keys(slots)` EMPTY in the element's `setup` — with the
   * wrapper forwarding slots, without it, when the node is added after mount,
   * and with `shadowRoot: true` as well. Vue projects light DOM into a custom
   * element only through a NATIVE `<slot>` element inside a shadow root, which
   * this package cannot have: its entire look is one global stylesheet
   * (`styles/base.css`), and a shadow root would leave every embed unstyled.
   *
   * So the slot is for hosts that mount the Vue SFC (our admin app does, and
   * its `#brand` still wins when it is filled); this is for everybody else —
   * and "everybody else" includes OUR OWN DESKTOP APP, which mounts the web
   * component. A logo the web app has and the desktop app silently lacks is
   * exactly the one-surface split this package exists to prevent.
   *
   * ⚠ An `<img src>`, not markup. The mark is drawn from a URL — a file, or a
   * `data:image/svg+xml;base64,…` URI for an inline logo — so a host never
   * hands this package a string to inject. There is no `v-html` on the path.
   *
   * ⚠ Both halves are optional and independent: a name with no mark renders
   * the wordmark alone, a mark with no name renders the glyph alone, and
   * neither renders nothing at all (the collapse control still sits there).
   */
  brand?: {
    /** Wordmark text, e.g. `'filex'`. Printed verbatim, never translated. */
    name?: string;
    /** URL of the mark: a path, an absolute URL, or a `data:` URI. */
    markUrl?: string;
  };

  /**
   * Whether the info-panel (inspector) toggle is shown in the toolbar.
   * Default true. The inspector itself stays reachable from the context menu.
   */
  showInfoPanel?: boolean;

  /**
   * How much of the explorer to put on screen. A REDUCTION, and only that.
   *
   *   'standard' (default) — everything: the tab strip, the split pane and all
   *                          three view modes, on top of the shell below.
   *   'simple'             — one pane, one folder, list and grid only. The tab
   *                          strip and the split pane are off, the gallery view
   *                          mode is hidden, and the "How to connect" /
   *                          "API keys" entries default to off.
   *
   * There is no third value and no alias. ⚠ Anything else that arrives here —
   * a typo, or the `'drive'` profile that was REMOVED after v0.40.0 —
   * resolves to `'standard'` and logs one console line naming it
   * (`lib/uiProfile.resolveUiProfile`, which explains why that direction and
   * not the other). **If you passed `'drive'`, pass `'simple'`.**
   *
   * ⚠⚠ IT DOES NOT DECIDE THE LOOK, and that is the whole of this option.
   * The header with its one wide search field, the "+ New" menu, the filter
   * row, the Folders/Files sections, the Details/Activity tabs and the storage
   * line USED to be gated behind a profile. They are what filex is now — the
   * admin panel, the desktop app and every `<filex-explorer>` embed draw them,
   * with no string passed. Owner's decision, 2026-09-12, verbatim (translated
   * from Turkish): "their app and our app will be one to one. The admin gets
   * one extra button, nothing else. The things we have over them — tabs, split
   * pane, theme choice, icon choice — stay."
   *
   * Why the reduction exists at all (GitHub #14): the reporter's users are not
   * in IT and read split panes, tabs and mount instructions as a file manager
   * they would have to relearn. The answer was NOT a second UI — one explorer,
   * configured, so a fix lands in one place for every surface that mounts this
   * package.
   *
   * ⚠ It does not gate the navigation panel. The panel ships in every profile,
   * for administrators too; only a viewer's own collapse choice moves it, and
   * that is a per-viewer preference, not a policy.
   */
  uiProfile?: UiProfile;

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


  /**
   * How a mouse opens an item.
   *
   *   'double' (default) — a single click SELECTS, a double click OPENS
   *                        (classic desktop file-manager). This is a
   *                        per-viewer preference the host may expose as a
   *                        setting.
   *   'single'           — a single click OPENS (the item is opened on the
   *                        first click; the checkbox is still the one click
   *                        that selects).
   *
   * ⚠ TOUCH is not governed by this: a finger tap always opens (the mobile
   * convention, and there is no hover-to-select on a touchscreen). The
   * checkbox always selects, on every device and in either mode. See
   * FilePane.onViewClick + composables/useRowTouch.
   */
  openTrigger?: 'single' | 'double';

  /**
   * Hand the OPEN of a file to the host instead of opening the in-page
   * preview/editor overlay. When true, opening a file emits `file-opened`
   * (path + basename) and the explorer does NOT mount its own PreviewModal —
   * the host decides what to do (the desktop app opens the file in its own
   * window per document). Directories still navigate inline, and Space
   * quick-look still peeks in-page. Default false (web/embeds preview inline).
   *
   * ⚠ Ignored inside an unlocked E2E-encrypted folder: the host window would
   * fetch raw server bytes (ciphertext), so those keep the in-page decrypted
   * preview.
   */
  openInHost?: boolean;

  /** Default view. */
  viewMode?: 'list' | 'grid';

  /**
   * Max upload size in MB.
   *
   * @deprecated IGNORED — declared, never read. There is no client-side size
   * check in the explorer at all (and the `/limits` fallback this comment used
   * to promise is not called either), so an embedder who set it watched
   * oversized uploads start anyway and fail at the server, which is the only
   * place the limit is enforced (FILEX_UPLOAD_* / the storage's own limits).
   * Left in place rather than removed so existing embeds keep compiling; do
   * not add a reader without also adding the pre-flight rejection the name
   * implies.
   */
  maxFileSizeMb?: number;

  /**
   * Accept patterns (MIME or extension).
   *
   * @deprecated IGNORED — declared, never read. No `accept` attribute is
   * rendered on any file input in the package and nothing filters a drop, so
   * setting it restricts nothing. Same note as `maxFileSizeMb`.
   */
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
  /**
   * tablo:t1 — remember how each folder was last viewed (view mode + sort),
   * Windows Explorer style. Default ON.
   *
   * ⚠ The opt-out exists for embeds. A product mounting filex in a two-inch
   * panel has one shape it wants and no room to argue with a gallery view
   * arriving from the person's main window; setting this false makes every
   * folder open in the host's chosen default and writes nothing. The columns
   * are NOT covered by this flag — their widths are a global preference about
   * the reader's screen, and a panel that narrow sheds them for want of room
   * before any preference is consulted.
   */
  rememberFolderView?: boolean;
  storages?: Array<{
    name: string;
    /**
     * tablo:t1 — the storage's immutable uid, when the host knows it.
     *
     * ⚠ A storage's NAME is editable — that is the point of a name — so it is
     * not a stable address, which is why every protocol also accepts the uid
     * as a path's first segment (`backend/internal/storageref`, filex issue
     * #21). Anything keyed on a storage for the long term should prefer this:
     * per-folder view memory does (`lib/viewPrefs.folderKey`), and folders
     * under a renamed storage keep their remembered view the moment a host
     * starts filling it in. Absent = the name is used and a rename loses the
     * memory, which is a mild, self-healing loss.
     */
    uid?: string;
    label?: string;
    driver?: string;
    readOnly?: boolean;
    /**
     * gorunum:v3-shell — bytes this storage holds, when the host knows. Drawn
     * as the caption on the Home view's storage card; absent means the card
     * names the kind of thing instead. For a person without a quota the
     * storage line under the navigation sums these (lib/storageLine), and a
     * storage left without one is measured by the explorer itself.
     *
     * ⚠ It must be the SAME quantity for every caller who gets it (in our own
     * app: `/api/admin/storages` for an operator, the RBAC-filtered
     * `/api/files/quota/storages` for everybody else). The per-USER quota is a
     * sum across every storage, so passing that here would print a number
     * about the person under a label about the drive.
     */
    usedBytes?: number;
    /**
     * `usedBytes` counts only part of the storage: its catalog does not cover
     * all of it yet (the server's `coverage` beside the figure —
     * lib/catalogCoverage). The card then draws it as a lower bound. Absent:
     * the explorer uses what the last listing said about the storage.
     */
    usedPartial?: boolean;
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

  /**
   * #47 — the host holds several accounts at once (the desktop app's rail),
   * and ⌘K's "Everywhere" group should search all of them.
   *
   * The explorer never talks to another account's server itself: it has no
   * credential for one, and it must not be handed any. Every call that reaches
   * another account goes through this hook, and the host answers it with the
   * credential it already keeps for that account. Hits come back tagged with
   * the account they belong to (`GlobalSearchHit.account`) and the palette
   * draws one group per account under a badge, this mount's own first.
   *
   * Absent — the web admin, every embed, a desktop with one account — and the
   * palette is exactly what it was: this account's hits, no badges.
   */
  accountSearch?: AccountSearchHook;
}

/** One signed-in account, as the palette's group badge draws it. */
export interface SearchAccount {
  /** The host's own id for the account; passed back on every hook call. */
  id: string;
  /** Short name on the badge — the server's brand name or its host. */
  label: string;
  /** Second line, quieter — typically the email signed in there. */
  detail?: string;
  /** The account's colour on the host (the rail avatar's), for the badge dot. */
  color?: string;
}

/** See `ExplorerConfig.accountSearch`. */
export interface AccountSearchHook {
  /** The account THIS explorer is mounted for — heads its own group. */
  self: SearchAccount;
  /** Every OTHER account signed in right now. Read on each query, so an
   *  account added or signed out while the explorer is mounted counts. */
  others: () => SearchAccount[] | Promise<SearchAccount[]>;
  /** `/api/files/search` on that account's server, with its credential. */
  search: (
    accountId: string,
    query: string,
    opts: { limit: number; scope: GlobalSearchScope },
  ) => Promise<GlobalSearchHit[]>;
  /**
   * Open another account's hit — the host switches to that account. The hit
   * arrives ADDRESSED (`name://rel`, as a listing row carries it): the host
   * never re-derives where a file is.
   */
  open: (accountId: string, item: SearchHitItem) => void | Promise<void>;
  /** Download another account's hit. Absent: its rows offer no Download. */
  download?: (accountId: string, item: SearchHitItem) => void | Promise<void>;
  /** Drag another account's hit out to the OS. Absent: its rows do not drag. */
  dragStart?: (accountId: string, items: SearchHitItem[]) => void | Promise<unknown>;
}

/** A search hit, addressed — the `{path, basename, type}` a listing row hands
 *  the drag-out hook, with `path` = `name://rel`. */
export interface SearchHitItem {
  path: string;
  basename: string;
  type: 'file' | 'dir';
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
