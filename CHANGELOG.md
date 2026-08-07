# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

(Nothing yet — see v0.13.1 below.)

## [0.13.1] - 2026-08-07

### Fixed

- **The explorer's onboarding tour sat on top of the desktop app's Settings
  panel.** The tour is appended to `<body>`, not to the explorer, so hiding the
  explorer left it exactly where it was — in the middle of Settings.

  ⚠ The v0.13.0 test for this passed while the bug was on screen: it asserted
  that the element *we* hide was hidden, which was true and beside the point.
  It now asks the browser what is actually topmost
  (`document.elementFromPoint`) — which fails on the old code and passes on the
  new, both measured.


## [0.13.0] - 2026-08-07

### Fixed

- **The desktop app opened the admin console.** Signing in landed you on the
  server's dashboard — users, storages, server settings — because the shell
  embedded the whole admin SPA. It now shows the **file explorer and nothing
  else**: the same `<filex-explorer>` component this project ships to
  embedders, pointed at your server with the token the browser sign-in handed
  back.

  Around it: an **account rail** down the left (click to switch, Slack-style),
  and a gear for **this app's** settings — accounts, synced folders, background
  running, start-at-login. Your server's admin panel is a link that opens in
  your browser, where a web console belongs.

- ⭐ **Starred files, recently-opened, starring and tags were broken in every
  cross-origin embed** — silently. Four calls in the core hardcoded
  `credentials: 'include'` while every other request used the configured mode.
  A credentialed cross-origin request may not be answered with
  `Access-Control-Allow-Origin: *`, which is what filex sends, so the browser
  rejected those four before the response was read: empty lists, no error. Two
  more defaulted to `'include'` when the host passed no prop. All six now take
  the mode from `useFileApi`.

  This affected **any** deployment serving the UI from a different origin to
  the API, not only the desktop app.

- **The explorer's multi-storage root mirrors a storage list the host provides**
  — it does not discover storages by itself. Undocumented, and easy to get
  wrong: the desktop window sat on an empty `/` and never issued a listing
  request at all. `docs/INTEGRATION.md` now says so.

### Changed

- **Desktop packages are no longer versioned in the filename**
  (`filex-desktop-x64.exe`, `filex-desktop-amd64.deb`,
  `filex-desktop-x86_64.AppImage`). `releases/latest/download/<name>` only
  resolves for a fixed filename, so the web app can now link straight at the
  right file instead of dropping people on a release page with ten assets and
  no indication of which is an installer and which is portable. The app reports
  its version in Settings.

- **The download offer says what each file does** — installer vs portable, and
  roughly how large. macOS says plainly that no build exists yet rather than
  offering a button that 404s.

### Added

- **[docs/DESKTOP.md](docs/DESKTOP.md)** — install per platform, how sign-in
  works and what to do when the browser cannot come back, where tokens and sync
  state live, and the unsigned-package warning stated rather than buried.
- `filex sync trash --json`.


## [0.12.0] - 2026-08-07

### Added

- **Selective folder sync.** A folder on your computer and a folder on a filex
  server are kept in step in both directions, in the background — the desktop
  app's "Sync folders" panel now actually transfers files rather than only
  recording pairings.

  The engine ships as `filex sync` in the CLI, and the desktop app runs that
  same binary. One implementation, two front ends: a terminal and the app read
  and write the same `~/.filex/sync/pairs.json`, so they can never disagree
  about what is paired.

  ```
  filex sync add ~/Documents/work docs://work
  filex sync run --watch 30s
  filex sync trash              # what sync removed from this machine
  ```

  How it decides, in short:

  - **The first sync of a pair deletes nothing.** With no record of a previous
    run there is no way to tell "you deleted this" from "you have not
    downloaded it yet", and guessing wrong empties a folder. Both sides are
    merged instead.
  - **A delete never beats an edit.** If a file was removed on one side but
    changed on the other since the last sync, the change wins and the file is
    restored.
  - **Changed in both places keeps both.** Your file keeps its name; the
    server's copy lands beside it as `report (server copy 2026-08-07 14-05).xlsx`.
  - **Anything sync removes from your machine is kept for 30 days**
    (`filex sync trash --restore <path>`). The engine never calls delete on
    local content directly.
  - Local and server timestamps are never compared to each other — only
    against what that side looked like at the end of the last run — so clock
    skew and the new mtime an upload gets cannot make files look permanently
    conflicted.
  - Paths in a server listing are validated before anything is written, so a
    listing containing `..` cannot write outside the sync folder.
  - Interrupted downloads are written to a temporary file and renamed, so a
    half-transferred file is never mistaken for a complete one.

- **`filex sync run --dry-run`** prints exactly what would happen before
  anything is touched, and `--account` limits a run to one signed-in server
  (one token cannot speak for two).

### Fixed

- **Pairing a folder to a server path that did not exist yet failed on every
  run.** The listing cannot walk a missing directory, so nothing ever synced
  and the folder had to be created by hand in the web UI first. Found against a
  live server, not in a unit test.


## [0.11.0] - 2026-08-07

### Added

- **Desktop app (Windows, Linux).** An Electron shell that runs the same web UI
  this repo already ships, so there is one file explorer, not two. Sign-in
  happens **in the user's browser**: the app opens the server's own login page
  and waits, so installs that authenticate through an identity provider (OIDC,
  SSO, passkeys, MFA) work — a native username/password form in the app would
  have locked all of those out. The browser hands back a **one-time code** over
  a `filex://` deep link and the app exchanges it for a token using a PKCE
  verifier that never leaves the process; only the code ever travels in a URL.

  When the deep link cannot work — no browser registered the scheme, a
  locked-down machine, or the user finished signing in on their phone — the
  waiting screen shows a copyable sign-in URL and accepts the code by hand.

  Multiple accounts on multiple servers, a background tray (closing the window
  keeps it running), optional start-at-login, and tokens held in the OS
  keychain (`safeStorage`). The app refuses to store a token in plaintext if
  the keychain is unavailable rather than silently downgrading.

- **`POST /api/auth/desktop/complete` and `/api/auth/desktop/exchange`.** The
  server half of that flow. `complete` requires an authenticated session and
  mints a scoped API token; `exchange` is public but one-time, expires in ten
  minutes, and constant-time compares both the code and the PKCE challenge.
  Nothing else about the auth surface changed.

- **Install prompts for both shapes of client.** On a phone or tablet the web
  app offers to install itself as a PWA; on a PC it offers the desktop
  download for the running platform (`.exe`, `.AppImage`, `.deb`) instead of a
  PWA it does not want.

### Fixed

- **The web app could not be embedded cross-origin.** It sent
  `X-Requested-With` on every request, which is not a CORS-safelisted header,
  so browsers preflighted — and go-chi/cors fails the *entire* preflight when
  one requested header is outside its allow-list. Any deployment serving the UI
  from a different origin to the API (the desktop app is one, but so is any
  reverse-proxy split) got a network error on every call. The header carried
  nothing the backend read.

- **Runtime API base URL, bearer token and credential mode are now
  configurable** at load time instead of being fixed at build time, which is
  what lets one bundle serve both the hosted site and the packaged app.

- **The service worker tried to precache ~19 MB**, including the Monaco editor
  chunks, and silently exceeded workbox's per-asset limit. Precache is now 224
  entries / 6.6 MB; the editor loads on demand as before.


## [0.10.2] - 2026-08-06

### Fixed

- **The v0.10.1 guard did not cover every write.** A sweep of the codebase —
  rather than the endpoints the bug report named — found four more places that
  could still write a file onto a folder: browser archive extraction and
  archive creation, the OnlyOffice save-back, and version restore. Replication
  is now guarded too, on both its write and copy paths: the replica is where
  the collision does its real damage, so a refusal there is recorded as a
  replication failure instead of quietly corrupting the mirror.

  v0.10.1 remains a genuine fix for the paths the report named; this closes the
  rest of the class.

## [0.10.1] - 2026-08-06

### Fixed

- **A file could be written onto a folder, corrupting the storage.** Passing a
  folder as the upload target wrote a single object at that exact key, leaving
  `X` (a file) and `X/…` (a folder) side by side. A real filesystem refuses
  this — the OS does it for us — so it never appeared locally; an object store
  has no such rule and accepted it silently.

  The damage showed up far from the cause. On a directory-backed mirror the
  colliding prefix can never settle, so `mc mirror` re-copied it on every run:
  2760 syncs in 24 hours, 1016 versions of a single PNG, a 43 MiB folder
  occupying 45 GB, and a disk at 96%. Quieter and worse, the colliding object
  made everything underneath it unlistable — 314 objects had no backup at all
  and nothing reported it.

  Every write surface now refuses the collision with `409`: the AI/MCP upload,
  the browser upload, the public file-drop link, text save, WebDAV `PUT`, and
  archive extraction. The reverse is refused too — a folder created on top of
  an existing file. Overwriting a file with a file is unchanged; it was never
  the problem. The check fails **open** if the backend cannot answer, because
  one flaky listing must not become an upload outage.

- **Uploads through the public file-drop link still failed on strict S3
  providers.** v0.9.0 fixed this for the browser upload but missed the shared
  ingest path behind the file-drop link, which kept wrapping the body and so
  kept sending it chunked with no `Content-Length` — the exact shape DT Cloud
  S3 answers `411` to.

### Added

- **`filex storage scan-collisions`** reports names that already exist as both
  a file and a folder, for damage that predates the guards above. It only
  reports: choosing which of the two to keep is a judgement about the data, not
  something a command should decide.

## [0.10.0] - 2026-08-06

Two interface fixes reported by olivov, whose tenants mount WebDAV from macOS.

Released as a minor rather than a patch on purpose: both change what the
explorer does on screen, and the update engine may apply a *patch* by itself
when the policy allows it. A release that changes the interface has to be one
the operator opts into.

### Added

- **Hidden (dot-prefixed) files can be shown or hidden; hidden by default.**
  They were listed like any other file, and most of them are not the user's
  files at all: a Mac mounting `/dav` leaves a `.DS_Store` in every folder it
  opens and an AppleDouble `._name` beside every file carrying extended
  attributes. Finder hides its own litter locally, so watching it reappear in
  the web UI reads as corruption — upload `4.jpeg`, find `4.jpeg` and
  `._4.jpeg` next to it.

  A toggle rather than a silent filter, because these *are* real files:
  hiding them outright would leave the ones already uploaded both invisible
  and undeletable, since the UI is the only way most people reach them.
  Right-click empty space, or `Ctrl+Shift+.`; the choice is remembered. It
  stays available to read-only viewers — what you can see is a view
  preference, not a change to anything.

### Changed

- **With exactly one storage visible, that storage now opens directly.** The
  storage list was a one-row page that carried no information and had to be
  clicked through on every visit; "up" is no longer offered where it would
  only lead back to that single row. Installs with more than one visible
  storage — and single-storage mode — are unaffected.

## [0.9.0] - 2026-08-06

Closes the ten items the olivov deployment (multi-tenant, 10 providers, DT
Cloud S3) filed against v0.8.0, plus one follow-up found while reviewing them.

### Security

- **A tenant admin could reach every other tenant over WebDAV.** `/dav`
  performs its own Basic authentication, so it is mounted outside the chain
  that runs the tenant resolver — no scope ever reached the scoped store, and
  the WebDAV root listed every tenant's storages. A second hole ran in
  parallel: storage lookup by name goes through `GetStorageByName`, which the
  scoped store does not wrap. The result was cross-tenant **read, write and
  delete** — and `DELETE` over WebDAV is permanent, it does not go to trash.
  Foreign storages now answer `404`, which keeps a storage that exists
  indistinguishable from one that does not. Supertenant operators stay
  confine-exempt. **Only multi-tenant installs (`FILEX_MULTI_TENANT`) were
  affected**; a single-tenant install has no second tenant to reach.

- **A tenant admin could read, modify and delete another tenant's users.**
  Only the user *list* was tenant-confined; `GET`, `PATCH` and `DELETE` on
  `/api/admin/users/{id}` acted on any id in the install — so a foreign
  account could be read, re-passworded (account takeover), disabled or
  deleted. All three now refuse out-of-tenant ids with `404`.

- **New users silently became platform operators.** `POST /api/admin/users`
  ignored `provider_id`, so every account fell to the store default of
  provider 1 — and provider 1 (`default`) is the *supertenant*. Accounts meant
  for one tenant came out confine-exempt and saw every tenant's storages.
  Absent, a new user now lands in the caller's own tenant; a tenant admin
  naming another gets `403`; an unknown id gets `400`. `PATCH` can re-home a
  user — supertenant only — which is the repair path for accounts already
  stranded in provider 1.

- **An unqualified grant path wrote the grant to an arbitrary storage.** It
  fell back to `storages[0]`. Elsewhere that fallback is harmless because a
  wrong guess shows up in the next listing; a grant is durable authorization
  state written where nobody looks again. It is now `400`, and the same
  tenant gate applies to the lookup.

### Fixed

- **Uploads failed on strict S3 providers (DT Cloud, and any provider that
  requires a length).** The S3 driver accepted a `size` argument and dropped
  it; with a non-seekable body the SDK framed the request chunked with no
  `Content-Length`. AWS and MinIO accept that — the S3 specification leaves it
  to the provider — and DT Cloud S3 answers `411 MissingContentLength`. WebDAV,
  MCP and the empty-folder marker kept working throughout, because all three
  hand the driver a seekable body. The upload handler now rewinds the
  multipart part after mime-sniffing rather than wrapping it, which is what
  destroyed seekability in the first place.

- **Upload failures were invisible.** The browser reported a failed upload
  only through an `error` event, which a standalone deployment has nothing
  listening for; the progress bar still ran to 100%, because the bytes really
  are sent and the server rejects them afterwards. Failures now raise a toast
  and carry the message into the upload row.

- **WebDAV could not create or browse subfolders on S3.** `Stat` only issued
  `HeadObject`, but on an object store a folder is a prefix, so every folder
  looked missing and the pre-`PUT` parent check failed with `409`. `Stat` now
  resolves a prefix as a directory, the way listing already did.

- **`GET /api/admin/quota/{id}` answered `500` for an unknown user**, and set
  and recompute were worse — both ran an `UPDATE` matching nothing and
  answered `200`. Unknown users are now `404`.

- **Creating a user could leave a supertenant account behind.** If homing the
  new user in its tenant failed, the handler returned `500` and left the row
  in provider 1. The half-created account is now removed; if that cleanup also
  fails, the error names the id that needs fixing by hand.

### Added

- **Per-user enable/disable switch** (`users.enabled`, migration 00022,
  defaults to enabled so every existing account keeps working). Refusing the
  next login is not enough — a session minted before the switch and every API
  token the user ever created both outlive it, so all three paths refuse a
  disabled account. Files, quota and grants are untouched: this is an access
  switch, not a soft delete. Disabling the last admin is refused.

- **Per-user quota is served where it was already documented**
  (`/api/admin/users/{id}/quota`); the original flat path keeps working. Usage
  and quota now ride on the user object, so an admin table costs one call
  instead of one request per row.

## [0.8.0] - 2026-07-29

### Added

- **Updates: filex now knows which releases exist, and can install them.**
  Behaviour is decided by which part of `x.y.z` moved — `z` applies itself
  (when the policy allows), `y` is announced and applied with one click, `x` is
  announced with upgrade instructions. Full documentation:
  [docs/UPDATES.md](docs/UPDATES.md).

  - **Nothing moves until you opt in.** The default is `policy: manual` —
    check and announce only. `AUTO_UPGRADE=true` selects the patch policy;
    `FILEX_UPDATE_CHECK=0` stops every outbound request.
  - **Admin → Updates page**: running version, what is available, why filex is
    or is not taking it, the releases being skipped over, and — when it cannot
    act itself — the exact commands for this install shape.
  - **`filex self-update`** (`--check`, `--to <version>`) for binary/systemd
    installs.
  - **Container installs never self-apply, by design.** An image layer is
    immutable: a binary replaced inside a running container disappears at the
    next `docker compose up` and the version silently reverts. filex refuses
    and prints the image-upgrade steps instead. It does not ask for
    `/var/run/docker.sock`.
  - **Release manifest** (`FILEX_UPDATE_MANIFEST_URL`, default
    `https://filex.sh/updates/stable.json`) carries two things a git tag
    cannot: `auto_ok` — a kill switch that pulls a bad release out of automatic
    distribution without deleting it — and `migrations`, which makes "patches
    carry no schema changes" checkable rather than a promise. A patch that
    declares a migration is never applied automatically.
  - **Safety sequence on apply**: SHA-256 verification against the manifest →
    unpack beside the current binary (atomic rename, same filesystem) →
    **smoke-test the new binary** (`filex --version`) → **database snapshot**
    (`VACUUM INTO` for sqlite, `FILEX_UPDATE_PRE_COMMAND` for external
    engines; a failure aborts the upgrade) → keep the old binary as
    `filex.bak-<version>` → swap → restart via systemd if present, otherwise
    report "restart required". Everything before the swap is undone by doing
    nothing.
  - `0.x` guard: while filex is pre-1.0, minor releases are never automatic
    even under `policy: minor` — semver gives them no compatibility promise.
  - New notification events `update_available` (once per version) and
    `update_applied`.
  - `scripts/gen-update-manifest.py` builds the manifest from the published
    GitHub releases, taking digests from goreleaser's `checksums.txt`.

### Changed

- The update check sends exactly one identifying header,
  `User-Agent: filex-updater/<version>` — no hostname, license, instance id or
  usage data. It is an update check, not telemetry.

## [0.7.6] - 2026-07-29

### Fixed

- **AI surface: denials now answer `403`, not `500`.** A confined token
  (`root:<adapter>://<path>` scope) writing or listing outside its root, and a
  bound user without the required grant level, both returned **500**:
  `resolveStorage` surfaced them as plain `fmt.Errorf` values, so `aiStatus`
  fell through to `mapDriverErr`'s server-error default. `errAIForbidden` was
  unmapped as well, so mutating permission denials answered 500 too. Both
  refusals are now wrapped so `errors.Is` matches `confine.ErrOutOfRoot` /
  `errAIForbidden`, and `aiStatus` maps them to `403`. The caller-facing hint
  ("… is outside your confined root … call file_root to see your root") is
  unchanged.

  Why it matters: a `5xx` reads as "server glitch, retry" — scripts, agents and
  HTTP clients retry a request that can never succeed, while the real cause
  (wrong path, missing grant) hides behind a generic server error.

## [0.7.5] - 2026-07-19

### Changed

- **Internal refactor (no behavior change): the storage-scoped path hash is now
  a single `internal/pathkey.Hash()`**. The `md5(cleaned path) + NUL +
  little-endian storage id` body had been copy-pasted across nine call sites
  (handlers, e2e, sync, DAV, and both DB drivers) — the same file could map to
  different rows if any copy drifted. The body is moved verbatim (byte-identical
  output, existing `path_hash` values unaffected), and a hash-equivalence test
  pins it against the previous implementation.

## [0.7.4] - 2026-07-18

### Fixed

- **Split view: the trash bin now renders in the secondary pane too**. The
  virtual "Trash" row was injected only by the main panel, so in split view
  the right pane started one row higher and the two panes' rows were
  visually offset. The trash-row synthesis and the internal-entry filter
  are now a single shared source (`lib/listing.ts`) used by both panes, so
  they always list identical rows. Opening the trash row in the secondary
  pane opens the trash view (with restore actions) in the main panel.
- **Standalone file manager: tall listings scroll inside the pane, not the
  whole page**. The standalone Explore page wrapper was `min-h-screen`
  (min-height), which lets the page grow past the viewport when the listing
  is taller than the screen (e.g. grid view / split view) — so the root
  `.fe` grew with it and its internal `overflow: auto` never engaged,
  scrolling the entire page. It is now `h-screen` (height), which caps the
  shell to the viewport so each pane's listing scrolls internally.

## [0.7.3] - 2026-07-18

### Fixed

- **Split view context menu now matches the main panel exactly**: the
  secondary pane's right-click menu was a separate, shorter list (missing
  rename, delete, share, convert, tags…). Both panels now render the single
  `selectionActionList` source, and pane actions (including rename / delete /
  new folder) are routed to the pane. Right-click and the keyboard shortcuts
  for delete/rename follow the focused pane.
- **Dropping a file onto its own folder no longer errors**: dragging an item
  onto the folder it already lives in (its own breadcrumb or the same
  listing) used to fire a move that the backend rejected with an S3
  "copy object to itself" 400. It is now a silent no-op.
- **Split view breadcrumb alignment**: the secondary pane's breadcrumb used a
  slightly different padding, font size and height than the main one, so the
  two panels were subtly misaligned. They now share identical metrics.

## [0.7.2] - 2026-07-18

### Fixed

- **Split view breadcrumb**: in split view the main (left) panel's
  breadcrumb spanned the whole width instead of just its own half — it now
  sits in the left half, mirroring the secondary pane's breadcrumb. The
  breadcrumb, presence and lock strips moved into a `.fe__primary` wrapper
  that occupies the left half when split (and the active-panel accent moved
  with them).
- **Split view context menu**: right-clicking a row in the secondary pane
  now opens a menu (Open / Open in new tab / Download / Copy / Cut / Paste)
  — previously it only selected the row. All actions target the secondary
  pane. Right-clicking empty space in the pane shows a Paste-only menu, so
  you can paste into an empty folder there.

## [0.7.1] - 2026-07-18

### Fixed

- **The explorer no longer overflows its host by 2px** (`.fe` was
  content-box, so its borders pushed past `height:100%`) — the outer page
  scrollbar that this produced in embeds is gone, and toasts stay visible
  at the bottom of the explorer instead of drifting out of view.
- The tab strip no longer shows a scrollbar under the tabs when many tabs
  are open — overflow still scrolls (wheel/drag), just without the strip.
- **Split view**: the secondary pane now renders with the same view
  components as the main panel (list / grid / gallery) instead of its own
  flat list; the pane keeps its own view mode (inherited from the main
  panel when you split) and the toolbar's view switcher applies to
  whichever pane is focused.
- **Split view**: pasting from the context menu now targets the focused
  pane — copying in the left pane and right-click → Paste in the right
  pane pastes into the right pane's folder (keyboard paste already did).
- Long action rows in the toolbar no longer wrap the whole toolbar onto a
  second line: the row is single-line and actions that do not fit fold
  into a "⋯" menu.
- Global shortcuts stay quiet while a context menu is open — pressing
  Delete with a menu open used to open the confirm dialog underneath the
  menu backdrop, wedging the UI.
- Default accent color darkened (`#3b82f6` → `#2f6fe0`) so white text on
  primary buttons meets WCAG AA (4.7:1); the unknown-file icon color in
  the light theme now meets the 3:1 UI-contrast bar too.

## [0.7.0] - 2026-07-18

### Added

- **Branding**: settings-driven identity for the public share/PIN/drop and
  folder-browse pages plus the admin login — display name, logo, accent
  color and footer text, with per-tenant overrides in multi-tenant mode
  (a new admin "Branding" page with live preview, and a public
  `GET /api/branding`). The "shared with filex" footer stays by default
  and can be turned off.
- **End-to-end encrypted folders (MVP)**: create a password-protected
  folder whose contents are encrypted in the browser (WebCrypto,
  PBKDF2 + AES-256-GCM) — the server never sees the password, a key or
  plaintext. Transparent upload/download/preview once unlocked, lock
  badge + unlock screen, and server-side blind spots closed (no
  thumbnails, no content indexing, no convert/OnlyOffice on encrypted
  blobs). There is NO recovery — a lost password means lost data; see
  docs/E2E-ENCRYPTION.md for the threat model and trade-offs.
- **Cloud readiness (preparation only)**: a `FILEX_CLOUD` master flag
  (default OFF — zero behavior change while off) gating a self-signup
  skeleton wired to multi-tenant provisioning, config-driven plans,
  Stripe stubs that answer 503 until configured, and provider
  plan/limits columns (migration 00021). See docs/CLOUD.md.

## [0.6.0] - 2026-07-17

### Added

- **Tabs**: open multiple locations as tabs (Ctrl+T / middle-click a folder /
  right-click → "Open in new tab"), switch with Ctrl(+Shift)+Tab, reorder by
  dragging, close with Ctrl+W or middle-click. The tab strip only appears
  with two or more tabs, and tabs (including the active one and per-tab
  split state) persist per browser. All tab shortcuts are remappable.
- **Split view**: split the active tab into two panes — the secondary pane
  navigates independently, and dragging files between panes moves them
  (same storage) or copies them (across storages). Keyboard actions follow
  the focused pane; shared clipboard works across panes.
- **Gallery view**: a third view mode with large media thumbnails alongside
  list and grid.
- **Browsable folder shares**: public folder share links now open a
  navigable page (subfolders, per-file open/download) instead of jumping
  straight to the ZIP flow — "Download all" keeps the ZIP path; folders
  that are mostly images/videos render as a gallery.
- **File comments**: per-file comment threads (visible to anyone who can
  see the file; authors and admins can delete) shown in the details panel
  with a count badge, plus a `comment.added` webhook event.

## [0.5.0] - 2026-07-17

### Added

- **Theme gallery**: 8 built-in themes (default, night blue, forest, amber,
  lilac, high contrast, soft gray, terminal green), each with its own light
  and dark variant — theme choice is independent from light/dark mode.
  Applied as `--fe-*` token overrides on the explorer root (embeds keep the
  host page untouched), persisted per browser, synced across tabs.
- **Customizable keyboard shortcuts**: a settings modal (toolbar menu, the
  "?" card or the command palette) lists every action grouped by category;
  press-to-capture rebinding with conflict detection ("unbind the old one"
  flow), per-row and global reset. Only deviations from the defaults are
  stored.
- **Quick look**: Space peeks the selected file in a lightweight overlay —
  arrow keys move the selection with the preview following, Enter promotes
  to the full open flow.
- **Operations center**: uploads and background file operations now live in
  one corner badge with an expandable panel — overall progress ring, per-item
  progress, session history, sticky error rows with retry (failed uploads
  retry into their original target folder).
- **Onboarding tour**: a first-run coach-mark tour (6 steps) highlights the
  core surfaces; steps whose targets are absent are skipped, and the tour
  can be restarted from the toolbar menu or the command palette.
- Command palette: new "Theme", "Shortcuts" and "Restart tour" commands.

### Improved

- Accessibility: grid/list views expose proper `grid`/`listbox` roles with
  keyboard focus rings, the context menu is fully keyboard-navigable
  (arrows/Home/End/Esc with focus restore) and flips at screen edges,
  modals trap focus and restore it on close, icon-only buttons carry
  localized labels, and animations respect `prefers-reduced-motion`.
- Drag & drop: valid drop targets highlight while dragging and the drag
  ghost shows the item name with a count badge for multi-selections.
- Error states: friendlier error cards with a collapsible technical-details
  section alongside retry.

## [0.4.2] - 2026-07-17

### Fixed

- **Trash no longer wedges storage sync** (#5): moving a folder to trash now
  rewrites every descendant's path along with it (restore stays symmetric),
  and the `(storage, parent, name)` unique index ignores soft-deleted rows
  (partial index on SQLite/Postgres, generated `is_live` column on MySQL) —
  a trashed folder's leftovers can no longer block sync from re-creating
  the same names forever.
- **`versions.keep_n` above 20 now works**: the snapshot path trimmed
  version history to a hardcoded 20 on every write, silently overriding
  larger configured limits.
- WebDAV `DELETE` now moves files and folders to the filex trash (matching
  the web UI) instead of deleting permanently; drivers without rename
  support keep the old behavior.

### Added

- **One post-write gate for antivirus + webhooks**: uploads, moves and
  deletes through the AI/token surface, ShareX, WebDAV and the ops worker
  now enqueue antivirus scans and emit `file.*` webhook events just like
  manager uploads (payloads carry an `origin` field: `manager`, `ai`,
  `sharex`, `dav`, `ops`).
- **Webhook delivery status**: webhook targets persist their last delivery
  result (`last_http_status`, `last_error`, `last_delivery_at`) and the
  admin Webhooks page shows a green/red "last delivery" badge.
- **Recursive CLI upload**: `filex client upload -r <dir> <storage://path>`
  mirrors a whole folder tree (empty folders included, symlinks skipped,
  non-zero exit on partial failure).
- Search results in grid view now show the same highlighted content
  snippet as list view.

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
