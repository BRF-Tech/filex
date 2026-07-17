# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

(Nothing yet — see v0.4.1 below.)

## [0.4.1] - 2026-07-17

### Added

- **App-store packaging**: ready-to-submit manifests for Umbrel, CasaOS,
  Runtipi, Unraid (Community Applications) and Portainer templates under
  `deploy/`, plus a refreshed Helm chart (appVersion now tracks a real
  image tag).
- **Documentation site**: a VitePress site over `docs/` (see `docs-site/`)
  with local search, feature landing page and dark mode — published at
  https://docs.filex.sh.

### Fixed

- External-service capability probes now hit real health endpoints
  (OnlyOffice `/healthcheck`, converter `/healthz`) and failures are only
  cached for 2 minutes instead of an hour — a transient outage no longer
  pins a "configured but unreachable" banner for the rest of the hour.
- Returning to the root from a dead deep link now clears the stale hash,
  so a reload lands on the root instead of the 404 screen again.

## [0.4.0] - 2026-07-17

### Added

- **Inspector panel**: press `i` (or the toolbar toggle) for a details
  sidebar on the selected item — metadata, copyable path/etag, **version
  history** (list, restore with optional pre-restore snapshot, take a
  version on demand), effective permission with a jump to the permissions
  dialog, and the item's share links. Full-screen overlay in narrow mode.
- **Optional antivirus scanning**: with `clamscan`/`clamdscan` on the host
  (`FILEX_CLAMAV_BIN` or PATH), uploads are scanned asynchronously;
  infected files are quarantined to the trash and a new **`file.infected`**
  event fires through webhooks and in-app notifications. Capability
  endpoint reports `antivirus`. See `docs/PROTECTION.md`.
- **Protection settings**: `GET/PATCH /api/admin/protection` + a new admin
  "Protection" page — trash retention days, version retention (`keep N`),
  antivirus status at a glance.
- **Version retention cron**: with `versions.keep_n` set, a daily sweep
  trims old versions per node.
- **Admin quota UI**: user edit screen gains a storage-quota card with a
  usage bar, limit editor and recompute action; new
  `GET /api/admin/quota/{user_id}` endpoint backs it.
- Shares admin view: sortable download counts and a copy-link row action.
- `POST /api/files/versions/snapshot` records an on-demand version (the
  service existed; the endpoint was never wired).
- File listings now include `etag` when the backend knows it.

### Fixed

- **Admin quota endpoints always returned 400** since introduction — the
  routes bound `{user_id}` while the handlers read `{id}`.
- **Daily trash purge never actually ran** — the retention loop existed
  but was never started; it now runs daily. NOTE: after upgrading,
  soft-deleted files older than the retention window (default 30 days)
  will be permanently purged on the first sweep.

## [0.3.0] - 2026-07-17

### Added

- **WebDAV server**: mount your filex as a network drive. `/dav/<storage>/...`
  speaks class-2 WebDAV (Windows map-drive, macOS Finder, rclone, davfs2) with
  HTTP Basic auth (account password or API token), full RBAC enforcement,
  read-only storage protection and best-effort DB/search-index sync — files
  written over WebDAV show up in the UI and in content search. Kill-switch:
  `FILEX_DAV=0`. See `docs/WEBDAV.md`.
- **`filex client` CLI**: `login`, `ls`, `upload`, `download`, `mkdir`, `rm`,
  `mv`, `search` (content-aware) and `share` subcommands against any remote
  filex — flags/env/`~/.filex/cli.yaml` (0600) config, streaming uploads,
  `--json` output. See `docs/CLI.md`.
- **Webhook targets**: multiple webhook endpoints with per-target event
  filters and HMAC signing (`X-Filex-Signature: sha256=…`, plus
  `X-Filex-Event` / `X-Filex-Delivery` headers). New file events fire on
  uploads, moves, deletes, trash, share creation and file-drop receipts.
  Admin UI for target CRUD + test deliveries; the legacy global webhook keeps
  working unchanged.
- **Narrow / embed mini mode**: below 560px the explorer collapses its
  toolbar behind a "⋯" menu, search expands from an icon, touch devices get a
  bottom-sheet context menu and a floating upload button — wide layouts are
  pixel-for-pixel unchanged.

### Fixed

- WebDAV extension verbs (PROPFIND & friends) are registered with the router
  so they survive `chi` method filtering.

## [0.2.0] - 2026-07-17

### Added

- **Content search**: filex now indexes what's *inside* your files, not just
  their names. Plain text, Markdown, source code, CSV/JSON/YAML, PDF text
  layers and Office documents (docx/xlsx/pptx) are extracted asynchronously
  (never blocking writes) into the embedded Bleve index. Search hits carry a
  highlighted `snippet` and a `matched` field (`name`/`content`/`both`), and
  the search endpoints accept `scope=name|content|all`. Rebuild with content
  via `POST /api/admin/search/rebuild?content=1`. Tunables:
  `FILEX_SEARCH_CONTENT` (kill-switch) and `FILEX_SEARCH_CONTENT_MAX`.
- **Optional OCR**: when a `tesseract` binary is available
  (`FILEX_TESSERACT_BIN` or PATH), image files (png/jpg/webp/tiff) are OCR'd
  into the content index; without the binary the extractor stays silent.
  Capability endpoint now reports `ocr`.
- **Duplicate report**: `GET /api/admin/duplicates` groups files by
  (size, etag) and reports wasted bytes; new read-only admin view lists the
  groups with per-group totals.
- **Search UX**: the command palette gains an "Everywhere" section (global
  search with content-match badges and safe highlighted snippets), list view
  shows snippets under content matches, searches can be saved and re-run from
  the palette, and the admin SearchTest view grows a scope selector.
- **MCP**: `file_search` accepts a `content` flag (default on) and returns
  snippets, so agents can find files by what they contain.

## [0.1.84] - 2026-07-17

### Added

- **Command palette** (`Ctrl/Cmd+K`): fuzzy-jump to files and folders in the
  current listing, run common actions (new folder, upload, toggle view, trash,
  refresh, go up) and jump to a typed path — all from the keyboard.
- **Keyboard shortcuts help**: press `?` to see every shortcut, grouped and
  sourced from a single registry so the sheet never drifts from reality.
- **Date grouping & sorting**: list columns (name / size / date) are now
  sortable; sorting by date segments rows under Today / Yesterday / This week /
  This month / month-year headers.
- **Density toggle**: compact ⇄ comfortable list & grid density, persisted per
  browser.
- **Undo snackbar**: rename, move and trash operations offer a one-click
  "Undo" for 8 seconds.
- **Connection badge**: when the live (WebSocket) channel is unavailable the
  explorer quietly falls back to polling and now says so with a small amber
  pill instead of staying silent.

### Changed

- **File-type icons**: hand-drawn SVG icon set (12 families with per-family
  accent colors, light + dark) replaces the emoji icons in grid and list views;
  thumbnails keep priority.
- **Empty / not-found / error states**: illustrated, actionable screens (drag &
  drop hint + upload button, retry on load failure, distinct empty-trash and
  empty-search states) replace bare text.
- **Skeleton loading**: initial listing shows ghost rows/cards instead of a
  spinner (motion-reduced friendly).
- **Public share pages** (PIN, download, ZIP-preparing, file-drop) redesigned
  with a shared card language, dark-mode support, accessible focus states and
  a subtle "Shared with filex" footer; login page got the same visual pass with
  SSO-first hierarchy.

## [0.1.83] - 2026-07-16

### Fixed

- **Embedded explorers could not scroll in height-constrained hosts** (small
  screens, mobile touch): three compounding layout issues fixed. The
  web-component wrapper no longer forwards the host element's `style`/`class`
  onto the inner `.fe` root (`inheritAttrs: false`) — an embedder's inline
  `display:block` used to override `.fe{display:flex}` and collapse the
  column layout. `.fe__body` gains `min-height: 0` so the flex body shrinks
  to the remaining space and its `overflow:auto` actually engages. The
  stylesheet now ships a `filex-explorer{display:block;height:100%}` default,
  so embedders no longer need inline styles on the host element.

## [0.1.82] - 2026-07-10

### Fixed

- **i18n:** viewer strings (save chip, Edit / Close / Download buttons,
  read-only and no-preview labels) and the presence-bar toggle tooltip now
  follow the UI locale instead of leaking Turkish into English sessions (#2).
- **Thumbnails for AI-surface writes:** files written through `/api/ai/upload`,
  the `file_write` MCP tool, unzip and ShareX captures now dispatch thumbnail
  generation exactly like manager uploads — agent-uploaded images no longer
  show the broken-image placeholder in grid view (#3).
- **Demo landing:** the stale "Open source (soon)" card now reads
  "Open source · MIT" and links to the public GitHub repository (#4).

## [0.1.81] - 2026-07-09

### Added

- **Per-token identities** (`X-Filex-Token-User`): an API token can define a
  list of usernames (first = default); the audit log, shares (`created_via`)
  and presence are attributed per integration. Unknown username → 403.
- `PATCH /api/admin/ai-tokens/{id}` and `PATCH /api/tokens/{id}` — token
  editing (label / usernames) which previously did not exist.
- `/api/ai/*` and `/api/sharex` writes are now audit-logged.

## [0.1.80] - 2026-07-09

### Fixed

- **Embedded grid thumbnails**: thumbs are now fetched with the same auth chain
  as API calls (bearer/proxy) and rendered from blob object-URLs, so embedded
  web-component and PWA contexts show real previews instead of broken images.

### Added

- Presence bar expand/collapse toggle — full-name chips with horizontal scroll,
  preference persisted per browser.

## [0.1.79] - 2026-07-09

### Fixed

- **Presence shows real user identities** in embedded contexts: host proxies
  stamp `X-Filex-Presence-Name` (RFC 2047) + `X-Filex-Presence-Key`, spoofing
  headers are stripped, rosters exclude self, and renames follow focus.
- `realtimeRoom` no longer subscribes to a mis-qualified room in single-storage
  embeds (the root cause of live updates never arriving there).

## [0.1.74] – [0.1.78] - 2026-07-08/09

### Added

- **Real-time collaboration**: `/api/ws` WebSocket presence + live folder
  updates in the core component (native UI *and* embedded contexts via
  short-lived tickets from `POST /api/files/ws-ticket`), API-polling fallback.
- **Folder-share ZIP cache** keyed by content signature, a 5-minute warmer and
  a "preparing %" page for cold hits.
- **ShareX endpoint** (`POST /api/sharex/upload`) returning a ready public link.

### Fixed

- Confined (embedded) WebSocket contexts: optional-auth route, ticket-bound
  RBAC user, relative→absolute room mapping, per-client frame paths.

## [0.1.69] – [0.1.73] - 2026-07-07

### Added

- **Deep links**: the address bar tracks the open folder (`#storage/dir`);
  pasting a link opens that folder, login preserves the hash.
- Web Share (`navigator.share`) button in the share modal.

### Fixed

- Ghost folders now 404 (S3 empty-prefix verification); unauthorized folders
  render the same "not found" screen (no RBAC information leak).
- `GET /api/files/share` list route existed in the UI but not the backend —
  "existing links" no longer always empty.

## [0.1.68] - 2026-07-06

### Fixed

- **OIDC login no longer loops behind a CDN that strips Set-Cookie from
  redirects.** Measured live (nginx `$upstream_http_set_cookie` vs the
  browser through Cloudflare): the origin emits the session `Set-Cookie` on
  the callback's 302, but the CDN strips a **Domain-scoped** Set-Cookie from a
  **3xx** response while passing it on a 200 (host-only cookies survive either
  way) — so the just-minted session cookie vanished and the SPA looped on
  `/api/auth/me` 401. The successful OIDC callback now writes the session
  cookie (unchanged Domain/Secure/SameSite logic) and forwards the browser
  with a minimal **200 `text/html` bounce** (`<meta refresh>` +
  `location.replace` + a `<noscript>` link) to a fixed relative `/admin/`, so
  the cookie rides a 200 the CDN passes through. The bounce target is a
  constant relative path (stays on the tenant host from v0.1.66, zero
  open-redirect surface; html/template-escaped). Error/maintenance branches
  stay 302 (they set no cookie). No config or DB change; works with or without
  a CDN, single- and multi-tenant. The OIDC *state* cookie is host-only and
  already survives the start redirect. (`handlers.OIDCCallback`,
  `writeOIDCBounce`.)

## [0.1.67] - 2026-07-06

### Fixed

- **Session (and OIDC state) cookies are now marked `Secure` on HTTPS.**
  Previously the session cookie never set `Secure`, and the OIDC state cookie
  only did so when `r.TLS != nil` — which is never true behind a
  TLS-terminating reverse proxy (nginx/Caddy), where filex is reached over
  plain HTTP with `X-Forwarded-Proto: https`. Both cookies now derive `Secure`
  from `r.TLS` **or** `X-Forwarded-Proto=https`. On a **`Domain`-scoped**
  cookie this is what Chrome's schemeful-same-site rules require to keep the
  cookie through the OIDC redirect chain — the observed difference from a
  working Roundcube cookie behind the very same proxy. Plain-HTTP installs
  (no `X-Forwarded-Proto`) still get a non-Secure cookie, so TLS-less setups
  keep working. This is both correct hardening and the most likely fix for
  cookie-domain SSO login loops behind a proxy. (`handlers.requestIsHTTPS`,
  `oidc.StartFlow`.)

## [0.1.66] - 2026-07-06

### Fixed

- **Multi-tenant: OIDC callback now redirects to the TENANT's host.** After a
  successful (or failed) IdP round-trip the callback bounced the user to
  `FILEX_PUBLIC_URL` — the operator/supertenant host — instead of the tenant
  host the login started on, stranding them without a session. All three
  callback redirects (success `/admin/`, error `?error=oidc`, maintenance
  `?maintenance=1`) now derive their base from the request host, but only
  when it resolves to an enabled provider row (the same trusted-host model
  as tenant resolution); unknown hosts fall back to `PublicURL`, and
  single-tenant installs are untouched. Scheme honors
  `X-Forwarded-Proto: http` for TLS-less setups, defaulting to https.

### Added

- **Multi-tenant: per-tenant session-cookie `Domain`.** The global
  `FILEX_COOKIE_DOMAIN` (0.1.63) cannot serve tenants on different apex
  domains. In multi-tenant mode the `filex_session` Domain now resolves per
  request: the provider's new optional **`cookie_domain`** column (settable
  via `/api/admin/providers`, migration `00015`) wins; else it is derived
  from the provider host by dropping the first label (`files.example.com` →
  `.example.com`); else the global value. Set and clear stay symmetric.
  Single-tenant behaviour is unchanged. ⚠ Tenants served on a bare apex or
  whose derived value would be a public suffix (`.com.tr`) must set
  `cookie_domain` explicitly — see docs/MULTI-TENANCY.md.

## [0.1.65] - 2026-07-06

### Added

- **Multi-arch Docker images: linux/amd64 + linux/arm64.** Release binaries
  were already cross-built for arm64 (goreleaser), but the container images
  only shipped amd64. The Dockerfiles now pin the Node/Go build stages to
  `$BUILDPLATFORM` and cross-compile the Go binary via `TARGETOS`/`TARGETARCH`
  (CGO=0 makes it free), so only the runtime stage's package installs run
  per-arch; the release workflow builds and pushes both platforms as a
  single manifest (QEMU + buildx). All runtime packages verified present in
  Alpine 3.20 aarch64. A plain single-arch `docker build` keeps working
  unchanged.

### Fixed

- **Full image version stamp.** `Dockerfile.full` still used the `-X
  main.version` ldflags form — a silent no-op — so `:full` images always
  reported "0.1.0-dev (unknown, unknown)". Now uses the fully-qualified
  version package path like the default image.
- **CI: 30-minute hard timeout on the e2e jobs** (source-repo CI) — a hung
  browser e2e run sat 14 hours on a single-slot runner and starved every
  queued pipeline.

## [0.1.64] - 2026-07-06

### Fixed

- **CI lint pass restored.** staticcheck flagged an unused
  `adminIDFiltersIn` type and two files had drifted from gofmt, which kept
  the source repository's tag-triggered release automation from running.
  Dead type removed, files formatted. (No runtime changes — see 0.1.63
  for the feature content.)

## [0.1.63] - 2026-07-05

### Added

- **SSO-first login** (`FILEX_OIDC_AUTO_REDIRECT`, default `false`). With the
  flag on and `oidc` among the auth drivers, the login page starts the OIDC
  flow immediately (redirect to the IdP) instead of rendering the password
  form. Local login stays available for break-glass/`admin@local` behind a
  "Sign in with password" link (`/admin/login?local=1`). Loop guards: the
  auto-redirect is suppressed on `?local=1`, after a failed IdP round-trip
  (`?error=oidc`) and on `?maintenance=1`; demo mode is unaffected. A failed
  OIDC callback now redirects back to the login page with `?error=oidc` and a
  friendly message instead of dead-ending on a raw JSON 401 (the error is
  logged server-side). Exposed to the SPA as `oidc_auto_redirect` in
  `/api/capabilities`. Multi-tenant installs keep their per-host realm
  dispatch — the redirect simply enters the existing `/api/auth/oidc/start`
  flow. **Off by default — existing installs behave exactly as before.**
- **`FILEX_COOKIE_DOMAIN`** (default empty). Sets the `Domain` attribute on
  the `filex_session` cookie (e.g. `.example.com`) so subdomains share the
  session. Applied on **both** login set and logout clear — clearing with a
  different scope would leave a stale cookie behind. `Secure`/`SameSite`/
  `HttpOnly` unchanged. Empty = host-only cookie, the historical behavior.

### Fixed

- **Login-page query params no longer vanish on cold load.** During the
  SPA's initial navigation the axios 401 interceptor (fired by the router
  guard's session probe) pushed a bare `/login`, racing the pending
  navigation and stripping its query (`?redirect=…`, and now `?local=1` /
  `?error=oidc`). The interceptor now stays quiet until the first route has
  settled — the router guard already owns cold-load routing. (`client.ts`.)

## [0.1.62] - 2026-07-05

### Fixed

- **Empty files and empty folders now show a size and a date in the explorer.**
  A real zero renders as `0 B` instead of `—`, and rows without a backend
  mtime (e.g. an empty folder on a synthetic-dir store, which has no
  descendants to aggregate a date from) fall back to when filex first indexed
  them. (`useLocale.formatSize`, `manager.go` index serialization.)
- **The Trash row shows its real size and date.** The explorer's virtual
  `.trash` entry now hydrates from the trash listing — total bytes of trashed
  items + the newest deletion time — instead of a bare `— / —`. It also gets a
  proper 🗑 icon (it rendered as a plain folder). (`FileExplorer.vue`,
  `ListView`/`GridView`.)

## [0.1.61] - 2026-07-05

### Added

- **Native multi-tenancy** (`FILEX_MULTI_TENANT`). One install serves N
  tenants: a *provider* = an auth realm (OIDC or local) bound to a host and
  linked to its storages. A realm's users sign in on their own domain and see
  only their own storages — even admins — and users of other realms are
  invisible (including on the permission/grant picker, search, shares/audit/
  grants lists). Per-tenant OIDC realms (host → provider → cached driver) with
  provider-scoped JIT and an immutable tenant tag; a supertenant provider is
  platform-scoped (at most one, moved only by transfer, undeletable); tenant
  lifecycle API under `/api/admin/providers` (provision / suspend / delete
  with user cascade); maintenance mode (flag off + tenants present ⇒
  supertenant-only login, fully reversible). **Off by default — a
  single-tenant install behaves exactly as before** (migration 00014 is
  additive and inert). Design + status: `docs/MULTI-TENANCY.md`; deploy
  examples: `deploy/compose/docker-compose.multi-tenant.yml` + Helm
  `ingress.extraHosts`.

## [0.1.60] - 2026-07-05

### Fixed

- **Folder dates now appear for existing files after an upgrade.** The folder
  "last activity" date added in 0.1.59 is derived from descendant file mtimes;
  files first indexed by an older version (before mtime was recorded on insert)
  carried no stored mtime, so their folders showed no date until the file's
  content next changed. The sync now backfills a missing mtime from the storage
  on its next pass — one cheap write per file, only while the value is missing.
  (`backend/internal/sync/poll.go`.)

## [0.1.59] - 2026-07-05

### Added

- **Folders now show a date in the explorer.** Alongside the recursive size
  added in 0.1.58, each folder's row reports a "last activity" date — the
  modification time of its newest descendant. It is computed in the same
  end-of-sync aggregation pass (one post-order tree walk, no extra queries) and
  cached in `nodes.backend_mtime`, so the explorer serves it straight from the
  index with no per-folder backend scan. This matters most for object stores
  whose directories are synthetic and carry no native mtime (e.g. S3 prefixes),
  which previously showed no date at all. (`backend/internal/sync/aggregate.go`,
  sqlite/postgres drivers, `db.Store.SetNodeMtime`.)

### Changed

- **The trash sidebar icon reflects its contents** — a full bin when the trash
  holds items, the empty bin otherwise. Refreshed on navigation (e.g. after
  emptying or restoring). Falls back to the empty bin if the count can't be
  fetched. (`web/src/components/Sidebar.vue`, `icons/TrashFull.vue`.)

## [0.1.58] - 2026-07-04

### Added

- **Folder sizes in the explorer.** Each folder's row shows its recursive total
  size (the sum of its descendant files). Sizes are computed once at the end of
  every storage sync and cached in the node index (`nodes.size`) — served from
  the index, never re-scanned per folder (no N+1). (`backend/internal/sync/`,
  `db.Store.AggNodes` / `SetNodeSize`.)
- **`FILEX_DEFAULT_LOCALE`** pins the UI's default language independent of the
  browser (e.g. a public demo can default to English while a user may still
  switch to another supported locale — their choice persists in
  `localStorage`). Exposed via the capabilities endpoint. (`config`,
  `capability`, `web/src/i18n`.)

## [0.1.56] - 2026-07-03

### Added

- **Optional Sentry-wire error reporting** (self-hosted GlitchTip). Set
  `FILEX_SENTRY_DSN` (+ `FILEX_SENTRY_ENVIRONMENT`) and the backend tees
  WARN+ERROR slog records to the DSN, so operational failures already logged —
  the ops worker's "ops: step failed", storage errors, recovered panics —
  surface centrally without scattering capture calls. WARN is only forwarded
  when it carries an `err` attribute (filters benign warnings); ERROR always.
  No DSN → no reporting (default build unchanged). Errors-only (no perf
  tracing). (`backend/internal/observability/`, `config`, `cmd/filex`.)

## [0.1.55] - 2026-07-03

### Fixed

- **S3 CopyObject on special-character keys 404'd** (`NoSuchKey`). The
  `CopySource` header was not URL-encoded, so any file whose name contained a
  space or non-ASCII character (e.g. Turkish `ÜYE BİLGİ … (1).doc`) failed to
  move, rename or delete-to-trash. `CopySource` is now URL-encoded per path
  segment. (`backend/internal/storage/drivers/s3/s3.go`.)
- **Delete/move now tolerate an already-missing source.** A stale index row
  (S3 object deleted out-of-band, or old test artifacts) made
  `Copy`/`Move` 404 and aborted the *entire* batch — so one phantom item broke
  a multi-select delete. The S3 `Copy` now returns `storage.ErrNotFound` for a
  missing source, and the delete/move paths (sync `vfDelete`, async ops) treat
  that as "already gone": they drop the stale cache row and carry on instead of
  failing. (`s3.go`, `ops/service.go`, `manager_mutate.go`.)

## [0.1.54] - 2026-07-03

### Fixed

- **S3 folder delete/move/copy was broken** (empty *and* non-empty folders).
  On an object store a folder is only a key prefix, but the S3 driver's
  `Move`/`Copy`/`Delete` issued a single `CopyObject`/`DeleteObject` on the bare
  folder key — which 404s (`NoSuchKey`) because no object lives at that exact
  key. Every folder delete therefore failed with
  `trash: … S3: CopyObject 404`, and the trash/restore path inherited it. The
  S3 driver is now **directory-aware**: `Move`/`Copy`/`Delete` detect a prefix
  and recurse over every object under it (preserving the relative subtree),
  so folders trash, restore, move and copy correctly. Local/SFTP were already
  dir-native (`os.Rename`/`RemoveAll`); this was S3-specific.
  (`backend/internal/storage/drivers/s3/s3.go`.)

### Changed

- Empty folders on S3 are now marked with a hidden `.empty` keep-object (was a
  bare `<path>/` marker), created by `Mkdir` and filtered from every listing —
  so an empty folder persists and shows as a directory without any visible
  child. Recursive delete/move carries the marker along.

## [0.1.53] - 2026-07-03

### Changed

- **File-drop UX polish** (follow-up to v0.1.52):
  - The public upload page now sets the native file picker's `accept` filter
    to the link's allowed extensions when configured, so pickers only offer
    valid files.
  - The upload-link invite email now spells out the configured limits
    (max files, MB per file, allowed types) and its subject names the target
    folder ("«Folder» — you've been asked to add files"). Limits are read back
    from the drop link's own settings, so the email always matches.
  - Copy buttons added next to the generated PIN in the Share and Request-files
    tabs.
  - "Request files" (Dosya İste) is no longer a separate context action — it
    lives as a tab inside the unified "Share / Permissions" popup, so folders
    expose share, per-user permissions and file-drop from one button.

## [0.1.52] - 2026-07-03

### Added

- **Public file-drop (upload link)** — the inverse of the share/download
  link. "Dosya İste" (Request files), a new folder-only action, mints a
  public `/d/{token}` link that lets anyone UPLOAD one or more files INTO a
  folder without an account. Critically it is a **blind drop**: the uploader
  never sees, lists or downloads the folder's existing contents — the target
  is resolved server-side from the token and confined; the anonymous client
  cannot influence the destination path. Each submission lands in its own
  `<date_time>_<name|anon>` subfolder (no collisions, clear provenance), with
  an optional uploader name + note (`NOT.txt`). Options: PIN, expiry, and an
  "Advanced" panel (max files, MB/file, allowed extensions, ask-name).
  Per-IP rate limiting guards the anonymous write surface. The owner is
  notified on each drop (in-app + email). Backend reuses the manager's ingest
  path (`IngestFile`/`EnsureDir`) so dropped files get identical mime
  detection, node caching and thumbnails. Server-rendered upload page (same
  dependency-free template style as the share PIN/error pages).
  (`shares.kind='drop'` + `max_uploads`/`upload_count`/`drop_settings`
  columns, migration `00013_share_drop`; `internal/api/handlers/drop.go`.)
- **Multiple recipients for share-mail** — both the download share link and
  the new upload link can be emailed to one *or many* addresses at once
  (comma/space/semicolon separated). (`POST /permissions/share-mail` now
  accepts `emails[]` + a `mode:"drop"` upload-worded body.)

## [0.1.2] - 2026-05-09

Patch closing the two follow-up bugs that surfaced after v0.1.1 went out
the door (sweep-2026-05-09 #21 fully + #25). Both are runtime-only
fixes; no schema changes, no breaking API.

### Fixed

- **Copy with collision now auto-suffixes** (sweep bug 25). The async
  copy worker used to ship `joinIntoDir(dest, src)` straight to the
  storage driver; when the user picked "Kopyasını Oluştur" / "Make a
  copy" the destination resolved to the **same key** as the source and
  S3 rejected the request as `InvalidRequest: trying to copy an object
  to itself ...`. The worker now probes the destination with `Stat`
  and falls back to `<base>-copy<ext>`, `<base>-copy-2<ext>`, … (up to
  100) until it finds a free slot, mirroring Finder/Nautilus/Explorer
  behaviour. Also handles the cross-directory paste-into-occupied
  variant (no silent overwrite). (`backend/internal/ops/service.go`)
- **3D viewer host now fully renders real GLB files** (bug 21
  follow-up). The v0.1.1 inline-style fix gave the `<model-viewer>`
  host a layout box (1048×685 instead of 0×0) — but the e2e fixture
  shipped at 104 bytes was a header-only glTF placeholder with no
  mesh data, so model-viewer's poster canvas stayed at 0×0 and emitted
  WebGL framebuffer warnings. Replaced the fixture with the Khronos
  Box.glb sample (~1.6 KB, real cube mesh) — re-verify on
  `https://fm.brf.sh/admin/files/edit?path=s3-test%3A%2F%2Fexample%2Fcube.glb&type=glb`
  now shows zero console warnings and a properly rendered cube. The
  v0.1.1 inline-style code change is what made the fixture-fix possible
  — both layers were needed.

## [0.1.1] - 2026-05-09

Patch release closing six bugs surfaced by the post-v0.1.0 production
sweep against `https://fm.brf.sh` (see `sweep-2026-05-09/sweep-report.md`
for the full matrix). No breaking changes; existing storages continue to
work, three previously dead-end UI features are now usable.

### Fixed

- **Frontend `apiBase: ''` (empty string) was silently dropped**
  (sweep bugs 22, 24). `useFileApi.resolveEndpoints` treated falsy
  `apiBase` as "no apiBase, legacy mode" — boolean-coerced empty strings
  collapsed to `null` for every derived endpoint. The relative-root
  variant is now treated as a valid prefix, so admin SPA mounts that pass
  `apiBase: ''` get a fully wired endpoint map (share, copy, move,
  restore, archive, ops). The error message that exposed this — "*XYZ*
  endpoint not configured" — should no longer surface for a legitimate
  relative-root config. (`packages/core/src/composables/useFileApi.ts`)
- **3D viewer JSON-parse crash on unsupported formats** (bugs 19, 20).
  `Viewer3D.vue` previously fed STL/OBJ/FBX/3DS files to
  `<model-viewer>`, which only understands glTF JSON; ASCII STL files
  starting with `solid <name>` triggered `JSON.parse(<solid …>)` →
  uncaught `SyntaxError`. The viewer now guards on extension, mounts
  `<model-viewer>` only for `glb` / `gltf` / `usdz`, and renders a
  download-fallback message (locale-aware
  `viewer.format_unsupported_3d`) for other 3D formats.
- **`<model-viewer>` host element collapsed to 0×0** (bug 21). The
  ancestor flexbox wasn't always granting a height to the viewer, so
  WebGL initialised with a zero-size framebuffer and emitted
  `GL_INVALID_FRAMEBUFFER_OPERATION: Attachment has zero size`. Pinned
  explicit `width: 100%; height: 100%; min-height: 480px; display:
  block` inline on the `<model-viewer>` host so the layout is stable
  regardless of parent context.
- **S3 driver default `path_style` for custom endpoints** (bug 23, part
  1). Hetzner Object Storage / MinIO / Backblaze B2 / Cloudflare R2 all
  serve path-style URLs; AWS S3 itself never sets a custom endpoint.
  When the operator does not explicitly set `path_style` and `endpoint`
  is non-empty, default to `path_style: true`. Existing storages that
  explicitly set `path_style: false` are unchanged.
- **Configurable `disable_presign` for S3 driver** (bug 23, part 2).
  Hetzner Ceph RGW emits `SignatureDoesNotMatch` for AWS SDK v2
  SigV4-presigned URLs (the canonical-string drift is non-trivial to
  unwind on the SDK side). New storage config flag `disable_presign:
  true` makes the driver advertise no-presign capability so the share
  download handler streams the bytes through the backend instead of
  redirecting to a presigned URL the bucket would reject.
- **Share handler honors `Capabilities().Presign` runtime flag.** The
  type-assertion `drv.(storage.Presigner)` always succeeds for drivers
  that implement the interface, even when the operator wants presign
  off. The handler now also checks `drv.Capabilities().Presign` and
  falls through to backend-stream when it's false.

### Notes

- The live `s3-test` storage on `fm.brf.sh` was retrofitted with
  `path_style: true` + `disable_presign: true` in addition to this
  release. Operators on Hetzner Object Storage should set both flags
  on existing storages (no migration provided since storage configs
  are operator-edited JSON; `path_style` will only auto-flip to true
  on newly-created storages).
- A seventh bug (#25 — duplicate-in-place sends `source == destination`
  to the S3 backend, which 400s as illegal self-copy) was discovered
  during v0.1.1 verification but is out of scope for this patch. See
  `sweep-2026-05-09/bugs.md`.

## [0.1.0] - 2026-05-06

First public release. The skeleton from earlier dev cycles plus the Round B
+ Round C delta work that turns filex into a complete self-hosted file
manager with replication, persistent queue and notifications.

### Added — core (skeleton)

- Standalone Go binary + monorepo (Vue / Web Component / React adapters).
- Storage driver interface with reference implementations: `local`, `s3`
  (Hetzner-tested), `sftp`, `webdav`, `ftp` (jlaffaye/ftp).
- Auth driver interface with reference implementations: `local` (bcrypt),
  `oidc` (Keycloak-tested), `ldap`, `proxy-header` (trusted CIDR enforced).
- DB driver interface with reference implementations: `sqlite` (default,
  modernc.org/sqlite), `mysql`, `postgres`.
- Sync worker with ETag-based diff and tombstone-false-positive guard.
- Bleve full-text search (embedded).
- Thumbnail pipeline (image GD, video ffmpeg, PDF ghostscript, Office
  libreoffice; capability-aware).
- Vue 3 admin UI (embedded into Go binary via `go:embed`).
- `@brftech/filex-core` — Vue 3 SFC source of truth.
- `@brftech/filex` — Web Component wrapper (`<filex-explorer>`).
- `@brftech/filex-react` — React adapter via `@lit/react`.
- First-run console banner with admin credentials + embed instructions.
- Multi-platform release matrix (Linux / macOS / Windows × amd64 / arm64).
- Docker images: `brftech/filex:slim` (~40 MB) and `brftech/filex:full`
  (~250 MB w/ thumbnail tools).
- GitLab CI pipeline (lint + test + build + npm publish + Docker push +
  release matrix).
- Plug & play external services: OnlyOffice, Drawio (URL-configured,
  capability-discovered).
- Monaco eager-load with highlight.js fallback for code preview/edit.

### Added — Round A (storage + auth deltas)

- **FTP driver** (`internal/storage/drivers/ftp`) — full Driver +
  Writer/Mover/Copier/Deleter/Mkdirer; FTPS (explicit AUTH TLS) and
  passive-mode toggles.
- **Storage root path guard** — `ValidateNonRootPath` rejects empty or
  `"/"` storage prefixes (s3.prefix, local.path, ftp/sftp/webdav.root)
  so filex never silently mounts at the bucket root and shadows
  pre-existing files. Wired into Storage create + update API handlers.
- **Proxy-header auth driver** (`internal/auth/drivers/proxyheader`) —
  reads `X-Auth-User`/`X-Auth-Email`/`X-Auth-Roles` from a trusted
  upstream proxy. `trusted_proxies` (CIDR list) is required; missing or
  empty list blocks `Init`. Auto-provisions users on first sight.

### Added — Round B (queue + notify + replica)

- **Persistent op queue** (`internal/queue`) — driver-based
  (`sqlite` default | `redis` | `postgres`). `ops_queue` table with
  status / priority / attempts / max_attempts / last_error /
  enqueued_at / started_at / finished_at / not_before. Worker pool
  with N goroutines, type-filtered Dequeue, exponential backoff,
  graceful Stop. Admin endpoints: `GET /admin/queue/{stats, list,
  {id}}`, `POST /admin/queue/{id}/retry`, `DELETE /admin/queue/{id}`.
- **Notifications subsystem** (`internal/notify`) — single
  `Service.Send` call fans out to (a) the in-app history table and
  (b) a configurable webhook with 3× exponential backoff retry. Per-
  user mute matrix; admin global view + smoke test trigger; webhook
  URL/token can be changed at runtime via the admin UI. Endpoints
  under `/api/notifications/...` (user) and `/admin/notifications/...`
  (admin).
- **Replica storage layer** (`internal/storage/replicated.go` +
  `internal/replica`) — `ReplicatedDriver` wraps a primary Driver and
  fans writes/moves/copies/deletes asynchronously to a replica.
  - **Read fallback**: primary errors → replica retry → emits
    `primary_read_fail` event.
  - **Path-glob rules** (`replica_rules` table) with priority asc;
    modes mirror | append_only | skip. Default-on rule mirrors when
    no rule matches (configurable via `replica_settings.default_mode`).
  - **Failure recorder** (`replica_failures` table, UNIQUE(path, op))
    tracks every fan-out failure; `Resolve` clears on success.
  - **Reconciliation** — admin "Fix all" enqueues `replica_retry` ops;
    queue handler reads from primary and writes to replica, then
    resolves the failure row.
  - **Cron status report** (`replica_status_reports` singleton) —
    user-supplied cron spec generates a snapshot on schedule (full
    payload to webhook, summary in DB + bell). Robfig/cron/v3 parses
    the spec; Reload primitive lets the admin UI change it without
    a restart.

### Added — Round C (admin UI delta pages)

- **Replica.vue** — 4-tab page (Rules / Failures / Report / Settings)
  with per-row Fix, "Fix all", Run-now, cron preset dropdown +
  advanced raw cron input.
- **Notifications.vue** — admin global feed with severity + webhook
  badges, "Send test" CTA, webhook config card (URL + bearer token).
- **Queue.vue** — 5 stat cards + paginated op table + per-row
  Retry/Cancel.
- **NotificationBell.vue** — top-nav bell with unread badge, 15s
  polling, dropdown listing the latest 15 notifications, mark-read
  on click, "View all" deep link.
- Sidebar entries (Replica / Queue / Notifications) and i18n keys
  for both `tr` and `en`.

### Changed

- `db.Store` interface gained 21 new methods (notifications + replica
  CRUD + counts + report singleton + settings).
- Server bootstrap registers `replica_retry`, `replica_report`,
  `reconcile` queue handlers; CronScheduler starts after the queue
  pool and reloads from `replica_settings` on boot.

### Fixed

- `internal/testutil` was importing `auth/drivers/local`,
  `capability` and `share` directly, so each of those packages'
  test files (which used `testutil`) failed with `import cycle not
  allowed in test`. Split into `internal/testutil/dbtest` (minimal,
  db + model + bcrypt only) — three problem suites now reference
  `dbtest` instead and `go test ./...` is green.

### Demo URL

- `files.brf.sh` → `demo-fm.brf.sh` rename across `deploy/`,
  `docs/DEPLOY_BRF.md`, `docs/MIGRATION_FISHAPP.md`,
  `deploy/keycloak-client-filex.json`, `deploy/.env.example`,
  `deploy/README.md`. Deploy host moved from main to brkip Caddy
  (DR-site, internal CA TLS).

### Known Gaps for v0.2

- Full B-plan brf-mono backend swap (filex Go binary as the sole
  files backend; legacy `Modules/FishApp/Services/*` removed). v0.1
  ships with the frontend-swap A-plan (filex UI + brf-mono PHP
  backend continues to handle storage). See
  `plan/07-integration-and-release.md` §1.
- `replicated_driver` is wired by ad-hoc admin SQL today
  (storages.role + replica_of_id). v0.2 will auto-discover replica
  pairs from the `storages` table.
- E2E Playwright suite only covers the original flows; new admin UI
  pages (Replica, Queue, Notifications) ship with manual smoke
  testing only.
- Sentry SDK integration deferred to v0.2.
