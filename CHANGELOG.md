# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security

- **Everybody on a storage could read everybody's queued operations, file
  paths included.** `GET /api/files/ops` — the list the explorer's operations
  centre polls — was scoped to the storages a caller's tenant reaches and to
  nothing finer, so every member of a tenant (every user of a single-tenant
  install) was handed every other member's copies, moves, deletes, uploads and
  app jobs: their sources and destinations, inside folders the reader may hold
  no grant for, and a toast when one the tray was following finished.
  `GET /api/files/ops/{id}` answered the full row, every source, for any id in
  reach, and the ids are sequential. Found on a production install, where the
  queue held a colleague's move into a folder of client records and handed it
  to every member of the tenant, whatever their grants. An administrator still
  follows every operation in reach; anybody else
  is shown, can read and can cancel only the operations they queued, and gets
  the same `404` for someone else's as for an id that does not exist. A row
  that names nobody (queued before `actor_id` was recorded, or by a scheduled
  app job) is an administrator's only.

## [0.46.1] - 2026-09-26

### Fixed

- **The version line no longer runs off the menu.** The server reports its
  version as the release followed by the full commit hash and the build time,
  and the line printed all of it: the explorer's avatar menu grew a sideways
  scroll bar, the sign-in page carried the 40-character hash twice, and the
  About page ran it off its card. The account menus, user settings and the
  sign-in page now name the release only (`filex v0.46.1`); the About page
  shows the release with the commit shortened to seven characters and the
  build date under it, and its copy button still copies the whole string.

## [0.46.0] - 2026-09-26

### Added

- **Put the storages in the navigation panel in your own order (#57).** Drag a
  storage row, or use its menu (right click, a long press, or Shift+F10):
  Move up, Move down, Sort by name, Use default order. The order is kept on
  your account, so it follows you to every browser, and Home's storage cards
  follow it too.
- **Administrators set the order everybody starts from (#57).** The admin
  Storages page is a table now: drag a row by its handle, use Move up / Move
  down in its Actions menu or the arrow keys on the handle, and Reset to
  default order. People who have not arranged their own see this order;
  `PUT /api/admin/storages/order` sets it (migration 00060 adds
  `storages.sort_order`).

### Changed

- **New document: name the file anything — `LICENSE`, `Makefile`,
  `test.conf`, `example.custom` (#56).** The dialog drew the type's extension
  as a read-only suffix, so a Plain text document was always `<name>.txt`. The
  name field now holds the whole file name: the type prefills it
  (`Untitled.txt`), focusing it selects the stem like a rename, and a type
  switch keeps what you typed (only swapping the previous type's default
  extension). The type still decides the contents and the editor — `LICENSE`
  made as Plain text opens in the text editor, `README` made as Markdown in
  the markdown editor. Office documents and diagrams keep their extension,
  because their editors find them by it: the dialog says "will be created as
  `report.docx`" instead of locking the field, and an empty `x.docx` made as
  Plain text is refused. Behind it: `POST /api/files/manager?action=newfile`
  takes `exact_name: true` (without it the extension is appended exactly as
  before, so older clients are unchanged), `newdoc_types` rows carry
  `ext_required`, and a refused borrowed extension answers `400
  EXT_NEEDS_TYPE`. Name checks — a leaf, no `..`, reserved names — are as
  strict as they were.
- **A text file whose name has no known extension opens as text and can be
  saved (#56).** `LICENSE`, `NOTICE` or `notes.custom` fell through to the
  viewer's Download fallback, and save-text — which allows by extension —
  refused them with `415`. The viewer now opens such a file as plain text when
  the server's mime for it says text, and save-text saves an existing file the
  catalogue calls text (a file it calls anything else, or a path with no
  catalogue row, is refused as before). The listing draws such a file with the
  text icon and names it "Plain text" instead of "?" and "—".

### Fixed

- **A new document opens once in the desktop app.** It came up twice: in its
  own window and in the viewer over the explorer, because creating a document
  opened the in-page viewer before telling the app. It now takes the same path
  as every other open (the app's window only). Found while building #56.
- **An app screen speaks the language ON SCREEN, not the account's.** An
  embedded explorer (`<filex-explorer>` with `config.locale: 'tr'`) draws the
  language its host page chose, and nobody asked the account — whose language
  defaults to English. The server told every app the ACCOUNT's language
  (Accept-Language ranks below it on purpose), so an app's plain strings came
  out English in a Turkish popup: the signing app's wizard asked "Identity"
  with "One signer per line…" under it. The explorer now names its language on
  every app call (`?lang=` on `…/run`, the view `GET`, every `…/event`), and
  the server puts that explicit choice first — then the account's, then
  Accept-Language, as before. The standalone app is unchanged: its language
  and the account's are the same one.
- **The operations tray says an app's job in the reader's language.** The job
  row froze the action's label and the app's result in ONE language when the
  job was created, so a Turkish embed read "Convert…" and an English result.
  The row now keeps every language the app wrote and the tray's poll
  (`/api/files/ops?lang=`) reads them in its reader's — the label, the result,
  and the reason an app gave for failing. Rows written before read as they
  were; no migration.
- **Every core dialog's close button is named in the explorer's language.**
  The × of every dialog (modals/Modal.vue) took its accessible name from the
  dialog's own `locale` prop and fell back to English, and not one of the
  twenty core dialogs passed it: a screen reader said "Close" over a Turkish
  app popup. A dialog now inherits the language of the explorer it sits in
  (an explicit `locale` still wins).
- **An archive being made no longer looks like a click that did nothing.** An
  archive job read "0%" beside an empty ring for most of its run: reading the
  members off their storage — most of the time on a remote store — was worth
  0–10% of the bar, counted per member, and the built-in ZIP and the write of
  the result reported nothing. Measured in production, 175 files off S3: two
  minutes between 0% and 9%, and the person who started it saw no sign of it.
  Each phase now moves in bytes while it runs — reading 0–45%, compressing
  45–90%, writing 90–99% — and creating or extracting an archive opens the
  operations panel on the job, so its name, progress bar and Cancel are on
  screen when the dialog closes rather than a small badge in the corner.

- **Typing in the code editor no longer triggers the explorer's shortcuts.**
  In Chrome and Edge the editor types through the browser's EditContext, so
  its focused element is not a text field, and the explorer took the
  keystrokes: `I` opened the info panel, `S` starred the file, Space opened
  quick look and Backspace went up a folder — "MIT License" typed into a new
  file arrived as "MLcene". The shortcut guard and quick look's arrow keys now
  treat the editor (and any `role="textbox"`) as typing. Found while building
  #56.

## [0.45.1] - 2026-09-25

0.45.0 as it was meant to ship. **v0.45.0 was tagged but never published**:
one of its unit tests read a file only the maintainers' private repository has,
the release workflow's test gate failed on it in the public tree, and nothing —
no images, binaries, npm packages or desktop apps — went out. Everything
0.45.0 carried is below, in this release, together with the fix and a new
release gate that runs the web tests in the public tree before anything is
tagged.

### Security

- **A token confined to one folder could reach files outside it through the
  app doors and the selection archive.** A `root:`-scoped API token (the kind
  a host application hands an embed, confined to the tenant's folder) could
  run an app on any file of the storage — its result written next to that
  file — and put files from outside its folder into a selection archive
  (`POST /api/files/archive/download`). The path confinement rewrote the body
  keys it knew and passed a `paths` array through untouched, and those doors
  checked the person's permissions but not the token's root. Both layers now
  refuse: the confinement covers `paths`, and every app door (run, view
  events, the chosen output folder) and the archive check each path against
  the token's root. Found while building #71.

### Added

- **Drag a file out of the web admin onto the desktop (#71).** The admin app
  signs its requests with a bearer token, which the browser's drag-out
  download cannot carry, so it offered no drag-out. The explorer now asks the
  server for a one-file link while the pointer rests on a row or a ⌘K result
  (`POST /api/files/archive/download` with `"mode":"file"` → `/z/<ticket>`, on
  the existing archive tickets): it lasts a minute at most and works once, and
  at the drop it is checked again as its owner — account, token, tenant host,
  tenant storage and at least viewer on the file. Every drag-out download is in
  the audit log (`file.download_link`, `file.download_link_refused`); the link
  itself never is. Folders are refused (`409 IS_FOLDER`). A cookie session
  keeps its plain download URL.

- **Desktop: Mount as a drive.** One button in Settings attaches a filex
  server as a drive of the operating system over WebDAV, and one detaches it —
  the one-click counterpart of the connection guide, whose commands now come
  from the same code. The account's own API token is the WebDAV credential and
  reaches the OS on stdin, never on a command line. On Windows it uses
  WNetAddConnection2, starts the WebClient service if it is stopped, and says
  up front what WebClient needs (HTTPS for a password). The macOS and Linux
  paths are there but not yet verified on those systems (#36).
- **⌘K search results you can act on.** A result in the palette's
  *Everywhere* group has a Download button (a folder arrives as one zip) and
  drags out like a row of the file list — through the desktop app's own drag,
  or as the browser's single-file download where the session is a cookie. A
  drag let go on the palette itself no longer starts an upload (#47).
- **Desktop: ⌘K searches every account on the rail.** Results are grouped
  under one badge per account, the one you are looking at first. Each account
  is searched, downloaded and dragged with its own sign-in, and opening another
  account's result switches the rail to it. Embedders get the same through the
  new `accountSearch` config hook
  ([docs/INTEGRATION.md](docs/INTEGRATION.md)) (#47).
- **`pnpm release X.Y.Z` cuts a release in order, and a red gate stops it.**
  The release process in [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) is one
  command now: preflight, the README/screenshot/docs audit, the doc gates, the
  version stamp, the whole pre-tag chain (both images, three database engines,
  TZ=UTC, Cypress, Playwright), the public export and its guards, then the
  signed tags, the push, the workflow's output and the deploy. No option skips
  a gate. The tool never signs, pushes or deploys: at those steps it prints the
  commands, and on `--resume` it checks what was done — each tag's signature
  and target, what both remotes hold, and what the servers, the update feed,
  the desktop feeds (bytes against their sha512) and docs.filex.sh serve.
  `--dry-run` runs every gate and writes nothing (#48).

### Fixed

- **The Storage line says how full the drives are, not how much you uploaded
  (#54, Berk Başarır).** The line at the foot of the explorer's navigation
  (web and desktop) and the storage chip in the admin top bar printed the
  person's own upload counter under a label about the drive: on an S3 drive
  holding 245.3 GB it read 523.5 MB, because files a sync discovered belong to
  nobody. With a quota the line still shows the person's usage against it;
  without one it shows the size of the drives the panel lists, the figure
  Home's cards print, and "at least …" where a drive is not fully counted.
  Refresh reads it again, and a failed poll keeps the last figure.
- **The release checks the public tree the way the release workflow does.**
  `pnpm release` built and tested the web code only in the private tree; it
  now runs the Frontend job (packages, admin build, web and package unit
  tests) in the exported tree as well, and `--resume` gets past its own
  export after a red export gate instead of stopping until the checkout is
  cleared by hand.
- **Desktop app: the sidebar entries are left-aligned again (#53).** Home,
  Trash, each storage and *How to connect* sat centred in their rows on macOS
  and Windows. The explorer renders into the page that hosts it, and the
  desktop app's own rule for its dialog buttons (`justify-content: center`)
  reached the sidebar's buttons, which left that property unsaid. They say it
  now, so a host page with a global button rule no longer moves them.
- **WebDAV: a file saved through a mapped Windows drive keeps its content.**
  Windows' WebDAV client sends a PROPPATCH with the Win32 times after every
  save, and answering it committed an empty upload over the file — so every
  file saved through a mapped drive landed on the server empty, with no error
  anywhere. A write-open that is not an upload now only describes the file.
- **A big local storage is catalogued about three times faster (#70).** A
  folder's catalogue rows were written one statement at a time and every new
  file indexed for search on its own, a disk-bound write each. They now go in
  one database transaction per folder (SQLite, PostgreSQL and MySQL), the
  folder's deletions included, and into the search index in one batch after it
  commits. On a 100,020-file tree the lazy catalogue's background fill went
  from 34 to 95 files/s and a full scan from 46 to 162 files/s; the lazy
  catalogue's deletion-safety rules are unchanged. Content extraction, queued
  per file as before, now trails behind the catalogue
  ([docs/LAZY-CATALOGUE.md](docs/LAZY-CATALOGUE.md)).
- **WebDAV storages no longer cut transfers at 60 seconds (#73).** The driver
  bounded every request, body included, to one minute, so any upload or
  download longer than that failed however well it was moving. Now only
  silence is bounded: connecting, the answer and each next piece of a transfer
  each wait `attempt_timeout_s` (30 s); copies, moves and deletes wait up to 10
  minutes. A dead server is reported as unavailable within seconds, failures
  that can pass are retried within `total_timeout_s`, and an upload is never
  sent twice. The driver speaks HTTP/1.1.
- **An FTP server that stops answering no longer freezes its storage (#73).**
  Only the connect was bounded: the liveness check held the storage's single
  connection forever, so every other operation on it waited too, and so did a
  server that never greeted and a transfer that stopped. Every wait now ends
  after `attempt_timeout_s` (15 s), a fresh connection is tried within the
  budget, a request whose client leaves stops waiting, and moving transfers
  are never cut. FTPS data connections are encrypted by the driver itself.
  WebDAV and FTP storages have the same three time settings as S3 on the
  storage form.
- **S3: a store that is down is reported within seconds, not a minute and a
  half (#44).** A drop into an S3 folder while the object storage was down
  answered `503 storage_unavailable` after 85 s; a refused port took 26 s, a
  host off the network ~145 s, and a store that accepted the connection but
  never answered did not return at all. Every wait that is not a moving
  transfer is now bounded per attempt — connecting (DNS included), the TLS
  handshake, the answer after the request, an answer that stops arriving, an
  upload the store stops taking — and retries are held to a time budget: with
  the defaults a dead store is reported within 15 s as
  `s3 endpoint … is unavailable: <reason> (N attempts in Xs)`. A transfer that
  keeps moving is never cut; copies, renames and multipart completion wait up
  to 10 minutes. A certificate that does not verify is no longer retried, and
  uploads over 2 MB (`Expect: 100-continue`) have an answer limit too. New
  storage settings `attempt_timeout_s` (10), `max_attempts` (6) and
  `total_timeout_s` (15) appear in the storage form; see
  [docs/STORAGE.md](docs/STORAGE.md).
- **Ops → Updates no longer promises what the install cannot do (#72).** On
  an install a package manager owns (Homebrew, winget, Snap, a distribution
  package) or a container, a saved policy of `patch` or `minor` made the badge
  read "Policy: install patches" although filex never replaces such a binary.
  The server works out the effective behaviour in one place, shared with the
  upgrade decision, and the status carries `behavior` and `policy_limit`: the
  badge reads **Announces only**, and the line under it says the saved policy
  has no effect on this install, with the package manager and its command.
  Checking switched off reads **Checking off**; `minor` on 0.x reads
  **Installs patches**. The saved policy is kept as set.
- **The release badges on Ops → Updates have their colours again.** Security,
  schema change and the size of the step all rendered grey: they passed a
  property the badge component does not have.
- **App screens wear the theme everywhere (#57).** On a branded, dark
  instance the wizard's current-step number and the people picker's avatar
  were white on the theme's primary (2.3:1 on a pale one); they now use the
  palette's `--fe-text-on-primary`, like filex's own buttons, and a signing
  box's number takes its ink from the signer's colour. An app's page and a
  public page (share, file request, signing link) now turn with light and dark
  while they are open, instead of keeping the mode they were opened in.
  [docs/APP-PLUGINS-API.md](docs/APP-PLUGINS-API.md) lists the theme tokens
  each surface node reads and what stays fixed on purpose (the PDF page,
  signer colours, the signature pad's paper), and a test fails when a surface
  node carries a fixed colour, corner or font.
- **Apps: a modal screen opened on several files keeps all of them.** An
  action that applies to a selection and opens a view saw every file on its
  first screen, but from the first form edit (or the submit) on it was
  answered about the first file only, and the job it queued ran on that one
  file (#64). The explorer now sends the whole selection with every view
  event; the server re-checks each path for the person asking, re-checks
  `applies` on every file at submit, and takes at most 500 paths, as `run`
  does.
- **Desktop: a copy prepared for one account's drag-out is not handed to
  another account's.** Two accounts with a storage of the same name (the same
  server, or the same drive name) shared the prepared copy of a path, so a drag
  from the second could carry the first account's file. The copies are keyed
  by account as well now (#47).
- **Linux: the desktop app opens on a minimal install.** Electron loads the
  ALSA sound library at start, and the `.deb` and `.rpm` did not depend on it:
  on a system without it the app did not open. They now require
  `libasound2 | libasound2t64` (Debian, Ubuntu) and `(alsa-lib or libasound2)`
  (Fedora, openSUSE).
- **Linux: the desktop app warns when the Snap Store copy is installed too.**
  The snap keeps its own accounts and its own sync history, so a folder paired
  in both copies was synced by two apps that did not know about each other —
  duplicate uploads, conflict copies, a delete reaching the wrong side. A
  `.deb`, `.rpm`, AppImage or AUR copy now says so in Settings, with the
  commands to keep one.
- **`filex self-update` leaves a distribution package alone.** A filex in
  `/usr/bin` (or `/usr/sbin`, `/bin`, `/sbin`) was put there by a package
  manager, and is now treated like a Homebrew, winget or Snap install: no
  self-replacement, upgrade with the package manager. A hand install in
  `/usr/local/bin`, where [docs/CLI.md](docs/CLI.md) puts it, is unchanged.

## [0.45.0] - 2026-09-25

Tagged, but its release workflow failed a unit test in the public tree
before anything was published. Everything it carried shipped as 0.45.1.

## [0.44.2] - 2026-09-25

0.44.0 as it was meant to ship, and **the one to install the desktop app
from**. v0.44.0 and v0.44.1 both published their npm packages, container
images and CLI binaries and then stopped at the winget step, so neither
shipped the desktop packages or reached the stores. Everything 0.44.0
describes below is in 0.44.2.

### Fixed

- **The release reaches the desktop packages and the stores.** GoReleaser
  accepts a publisher's repository token only as a single `.Env` variable
  reference — nothing around it, not even an `index` lookup — and checks it
  only when it publishes. The winget token is written that way now, and the
  template test checks every token against GoReleaser's own rule and that the
  release hands each variable to the GoReleaser step.
- **A desktop page that cannot load now stops the release.** Nothing ran
  the desktop unit tests before, which is how 0.43.x shipped a main window
  whose script never parsed; the release's gate now runs them. Found and
  proposed by Berk Başarır ([#52](https://github.com/BRF-Tech/filex/pull/52)).

## [0.44.1] - 2026-09-25

Tagged, but it stopped at the same winget step as 0.44.0 (see 0.44.2); its
npm packages, container images and CLI binaries were published. It replaced
`envOrDefault` in the release's template, and a test fails on any template
function GoReleaser does not define.

## [0.44.0] - 2026-09-25

Big storages open at once, archives come in every common format, and the
desktop app arrives through the stores. A **lazy catalogue** for local
storages (`sync_mode: lazy`, the idea from Alex / @ahjephson in #45) lists a
folder straight from disk the moment it is opened and catalogues it behind the
listing, with a throttled pass that fills in the rest — or, for a huge
archive, only the folders people visit. **Archives** gain 7z, TAR and its
compressed forms and password-protected ZIP and 7z (RAR too, where the
server's 7-Zip has it), contributed by Alex in #48 and hardened on the way in. The **desktop app and the CLI** ship
through Homebrew, winget and Snap, with a Microsoft Store build and an `.rpm`
beside them.

⚠ **Upgrade the desktop app if it is on 0.43.0 – 0.43.2**: its main window
stayed blank (see *Fixed*). ⚠ On Linux the desktop app's command is now
`filex-app`. ⚠ The desktop app needs macOS 13 or later (Electron 44). ⚠ The
full container image moves to Alpine 3.24 and grows by about 390 MB
unpacked (see *Changed*).

### Added

- **The desktop app and the CLI in package managers.** The desktop app is
  `filex-app` everywhere and the CLI is plain `filex`: `sudo snap install
  filex-app` (also in Ubuntu's App Center), `brew install
  brf-tech/filex/filex-app` and `brew install brf-tech/filex/filex` from the new
  tap [BRF-Tech/homebrew-filex](https://github.com/BRF-Tech/homebrew-filex),
  `winget install BRFTech.filex-app` and `winget install BRFTech.filex`. The
  release job publishes all of them from the release's own files, pinned by
  SHA-256 to the versioned download (goreleaser for the CLI,
  `desktop/scripts/pkg-manifests.mjs` for the app), and says so in the run when
  a store's credentials are missing instead of skipping it quietly. A new
  winget package waits for winget's review for its first release. A copy
  installed by a store never runs its own updater: Settings names the store
  and opens its page.
- **An `.rpm` for Fedora and openSUSE**, next to the `.deb` and the AppImage.
- **A Microsoft Store (MSIX) build of the desktop app** (`pnpm dist:store`,
  checked as an installed Store copy by `pnpm e2e:store`). It is ready for
  Partner Center; the Store listing comes later. The Store version carries a
  mapping the Store requires (0.43.x → 1.0.43xx.0); the app keeps reporting its
  own version.
- **A privacy page, [filex.sh/privacy](https://filex.sh/privacy/)** (English and
  Turkish): what the desktop app, the website and the public demo do and do
  not do with information. The stores link to it.

- **A third install mode, `package`: filex installed by Homebrew, winget or
  Snap leaves its binary to the package manager.** filex recognizes the
  layout around the running binary (links followed): a Homebrew
  `Caskroom`/`Cellar` keg, a winget `WinGet\Packages\<id>_<source>` folder, or
  `SNAP`/`SNAP_NAME` with the binary under `$SNAP`. There `filex self-update`
  refuses and prints the manager's command (`brew upgrade --cask filex`,
  `winget upgrade BRFTech.filex`, `snap refresh filex`), `--check` still
  reports the release, and no update policy ever replaces the binary — the
  package manager would otherwise keep recording the old version and write
  over ours at its next upgrade (a snap is read-only). **Ops → Updates** names
  the manager and shows its command instead of **Upgrade now**.
  `FILEX_INSTALL_MODE` also takes `package`, `homebrew`, `winget` and `snap`
  (a `.deb`/`.rpm`/AUR package can declare itself). See
  [UPDATES.md](docs/UPDATES.md#package-manager-installs).
- **Archives are made, opened and extracted in the explorer, with passwords,
  in the background.** Select files and choose **Create archive…** for a ZIP,
  7z, TAR, TAR.GZ, TAR.BZ2 or TAR.XZ, with a password (ZIP, 7z) and hidden
  file names (7z) if you like; open an archive to browse it like a folder;
  extract all of it or **Extract here**. An encrypted archive asks for its
  password first. Both directions run as operations of the queue, in a lane
  of their own, with progress and cancel in the operations centre, and
  **Settings → Archives** sets the formats, the size, entry and time limits,
  and tests the provider. By Alex ([@ahjephson](https://github.com/ahjephson),
  [#48](https://github.com/BRF-Tech/filex/pull/48)); see `docs/ARCHIVES.md`.
  - filex reads plain ZIP, TAR, TAR.GZ and TAR.BZ2 itself, on every install.
    7z, XZ and password-protected ZIP, and creating anything but a plain ZIP,
    need 7-Zip 25.01 or newer: the full image ships 26.01, and a server
    without it offers ZIP only, with no password fields. RAR extraction needs
    a 7-Zip built with RAR support, which Alpine's package (the full image's)
    is not; `FILEX_ARCHIVE_7Z_BIN` can point at one that is.
  - An archive is refused before anything is written when it holds a link or
    a special file — also when 7-Zip shows one only by its file mode (a
    `zip -y` or `7zz -snl` symlink, a FIFO or device in a TAR, a RAR5 file
    copy) — or a member that does not declare its size. The size limit stops
    a TAR-family archive at the exact byte, a gzip whose trailer lies
    included, because filex reads those itself. The archive type comes from
    the file name, never from its bytes; a password reaches 7-Zip on its
    standard input, never on its command line; and every member lands
    through the same gate as any other write, so it cannot replace a document
    an app has locked or land in filex's own folders.

- **Lazy catalogue for big local storages — `sync_mode: lazy`**
  ([#45](https://github.com/BRF-Tech/filex/issues/45); the idea is Alex's,
  @ahjephson). Every other mode catalogues a storage by walking all of it first;
  on a multi-terabyte NAS that is hours before the first folder is right. Now
  the folder somebody opens is listed straight from disk at once, with what the
  catalogue already knows laid over it, and is catalogued first in the
  background. Two behaviours per storage: **click first, fill in the
  background** (the default — a slow pass catalogues the rest, slowing down
  while people use the storage, honouring scan exclusions and carrying on after
  a restart) and **only on open** (nothing runs in the background; an
  administrator can catalogue everything once). Opened folders are watched for
  outside changes within a budget (`lazy_max_watches`, `lazy_watch_ttl`), and a
  desktop sync pair gets its whole subtree catalogued and kept current. ⚠ A
  folder nobody visited is never treated as deleted: rows are only removed from
  a folder that was just listed in full, each one confirmed gone. See
  [docs/STORAGE.md](docs/STORAGE.md#lazy-catalogue) and the design in
  [docs/LAZY-CATALOGUE.md](docs/LAZY-CATALOGUE.md).
- **The storage form offers the sync mode.** It could only be set through the
  API; the new and edit forms now have **Sync mode**, and the lazy catalogue's
  settings drawn from the driver descriptor (`lazy_fields`).
- **Search, folder sizes and drive usage say when they do not cover a whole
  storage.** One line above the listing and the search results names the
  reason (a first sync still running, a lazy catalogue still filling, a storage
  catalogued only on open); a folder whose size leaves something out reads
  `≥ 1.2 GB`, or `—` when nothing below it is catalogued yet; Home's drive card
  says *at least … used*. The storage page shows the catalogue's progress, the
  background pass and the watch budget.

### Changed

- **Linux: the desktop app's command is now `filex-app`,** and so is its
  package (`.deb`, `.rpm`) and desktop entry — `filex` is the CLI's name, and
  with both installed the command you typed depended on the order of your
  `PATH`. Installing the new package replaces the old `filex` one in a single
  step, and the app moves its *Start when I sign in* entry and the default-app
  choices made under the old name.
- **The container images move from Alpine 3.20 to 3.24, and the full image
  grows by about 390 MB** (its download by 134 MB, 545 → 679 MB). Alpine
  3.20 left support in April 2026, and archives need 7-Zip 25.01 or newer
  (25.00 and 25.01 fixed its ZIP symlink traversal, a RAR5 overflow, a
  compound-document crash and link handling during extraction:
  CVE-2025-11001/11002, CVE-2025-53816/53817, CVE-2025-55188). 3.24 ships
  7-Zip 26.01 and newer ffmpeg (8.1),
  Ghostscript (10.07), ImageMagick, poppler, librsvg and LibreOffice (25.8).
  On Alpine since 3.22, LibreOffice depends on Qt 6, which brings Mesa and
  LLVM with it: the full image's package layer grows from 1.13 GB to 1.52 GB.
  Thumbnails and every conversion engine (ffmpeg, ImageMagick, LibreOffice,
  Ghostscript, poppler, rsvg) were smoke-tested on the new image. The slim
  image moves to 3.24 as well and still ships no 7-Zip.

### Fixed

- **A right-click menu taller than the window scrolls.** With a few apps
  installed the menu outgrew a small window and its last items could not be
  reached; it now fits the window and scrolls inside itself.
- **Desktop app (0.43.0 – 0.43.2): the main window runs again.** An HTML
  comment in the Settings template quoted a function name in backticks inside
  a JavaScript template literal, which ended the literal early; the browser
  rejected the page's whole script, so after signing in the window drew its
  static frame and nothing worked. A test now parses every inline script of
  the desktop pages.
- **Desktop app (0.43.0 – 0.43.2): Settings shows your synced folders again.**
  The folder cards read a value that 0.43.0 had moved into another function,
  so with any folder paired Settings stopped drawing at that point: no folder
  card, no status line, no *Stop* button.
- **Linux: signing in through the browser returns to the app.** No Linux
  package declared the `filex://` link, so the browser had nothing to hand
  the sign-in back to and only pasting the code worked.
- **Linux: without a keyring the app says so before the sign-in.** It used to
  send you through the browser, spend the one-time code and then fail in
  English; it now refuses up front and explains what to install or connect
  (for the snap, the exact `snap connect` command).
- **Desktop app: a failed browser sign-in shows its reason again** (the error
  window did not refresh when it was already open).

- **Two filex on one computer no longer sync the same folder at the same
  time.** Nothing stopped a second engine on a pair that one was already
  syncing — the desktop app and `filex sync run` in a terminal, or an
  installed copy of the app and the Microsoft Store one, which reads the same
  sign-ins and, living in its own virtualised profile, does not see the other
  copy's single-instance lock. Both planned from the same history, so files
  were uploaded twice, conflict copies appeared out of nothing and a delete
  could travel to the side that did not ask for it — silently. Every run now
  holds an operating-system lock on its pair (`~/.filex/sync/locks/`,
  `LockFileEx` on Windows, `flock` elsewhere), released by the OS however the
  process ends. A pair another process holds is left untouched: `filex sync
  run` says `lock: busy`, syncs the rest and exits with status 4; `--watch`
  waits and takes the pair over when the other process stops; the desktop app
  shows *Another filex on this computer is syncing this folder* under it
  instead of an error, and does not restart its engine.

- **During a storage's first sync, a folder shows everything in it.** A
  partly catalogued folder — above all the root of a big storage — used to list
  only the entries the sync had reached so far, for as long as it ran. Until the
  first sync has finished, a folder is listed from the storage with the
  catalogue laid over it (ids, owners, thumbnails and tags kept), on every
  driver.
- **A full sync that met another writer at a folder no longer skips that
  folder's contents.** When the insert of a folder row lost a race, the walk
  gave up on the subtree and the sync finished with a hole in the catalogue;
  it now carries on with the row the other writer created.
- **The desktop's upload precondition is judged against the disk when the
  listing came from the disk.** A file with no catalogue row (or a drifted one)
  is compared with the file that is actually there, and `expect=none` is
  refused when a file already sits at that path.

### Security

- **Desktop: the app runs on Electron 44** (Chromium 152, Node 24), up from
  Electron 31, whose support ended on 2025-01-14 — every Chromium security fix
  since then was missing from the desktop app. Electron 44 is supported until
  2027-03-02. **The macOS build now needs macOS 13 (Ventura) or later**; Windows
  10/11 64-bit and 64-bit Linux are unchanged. An account signed in under the
  old runtime stays signed in (measured: a profile written by Electron 31 opens
  signed in under 44, and the other way round). The Windows installer grows
  from 96 MB to 132 MB: the runtime binary itself grew (181 → 246 MB unpacked;
  ANGLE is linked into it now) and Chromium ships a DirectX shader compiler
  (27 MB).
  Three behaviour changes of the new runtime are handled in the app rather
  than shipped: a failed browser sign-in still says why (the sign-in window
  had stopped redrawing when it was sent to the address it was already on),
  "Copy link" in the share menu waits for the now-asynchronous clipboard, and
  the Settings folder picker opens where the last pick was made instead of in
  Downloads every time.

## [0.43.2] - 2026-09-24

A fix-forward release for 0.43.0, and **the one to deploy. v0.43.0's
container images were never published**, and neither was anything from
v0.43.1: that tag's release gate stopped the run before a single package,
image or page went out. 0.43.2 is 0.43.1 as it was meant to ship, plus the two
fixes that gate asked for (below). Its npm packages and its GitHub
Release went out, but the image build failed on every attempt, so
`ghcr.io/brf-tech/filex` has no `v0.43.0` or `slim-v0.43.0` tag and `latest`
stayed on v0.42.2 until this release. The Helm chart and the CasaOS, Umbrel
and Runtipi manifests at 0.43.0 name that missing image, so installing or
upgrading from them fails to pull; at 0.43.1 they name one that exists.
Everything the 0.43.0 notes below describe is in 0.43.1 — **including its
security fixes, which an install that runs the image gets only now** — and two
more fixes: from Berk Başarır, a big folder no longer re-renders itself for
every thumbnail that arrives, and the desktop app signs in to a server behind a
private CA.

### Security

- **Container, Helm and app-store installs get 0.43.0's security fixes only
  with this release — upgrade promptly.** With no v0.43.0 image, every install
  that runs `ghcr.io/brf-tech/filex` is still on v0.42.2 or older, without
  anything 0.43.0 closed: an unsigned ONLYOFFICE save callback that lets
  anybody who can reach the server overwrite a file, a narrow API token that
  could mint a full-rights one, API answers a caching proxy could hand to
  every visitor, and a notification bell that named files its reader could
  not open. Read 0.43.0's ⚠⚠ notes before upgrading: a Document Server running
  with `JWT_ENABLED` off stops saving, and desktop sync needs the new desktop
  app as well as the server.
- **The public repository no longer carries a storage credential.**
  `scripts/seed-example-fixtures.sh`, which seeds the project's own demo
  server, held an object-storage access key and secret as the fallback values
  of `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`, and it was published with
  every release from the first one through v0.43.0. The key has been revoked
  and replaced. The script now takes both from the machine it runs on and is
  no longer published. No filex install used it — it only ever wrote demo
  files to the project's own bucket.
- **A test fixture no longer names the project's own servers.**
  `e2e/fixtures/file-types/diagram.drawio` labelled two boxes with the public
  addresses of the project's main server and its mirror. They are RFC 5737
  documentation addresses (`192.0.2.10`, `198.51.100.20`) with neutral labels
  now.
- **The export that builds the public tree refuses both.** It now stops on any
  of the project's own server addresses and on any credential written as a
  shell default (`…SECRET…=${VAR:-literal}`), in whatever file it turns up.

### Fixed

- **The container images build again, and a release can no longer publish
  without them.** The frontend stage of `docker/Dockerfile` and
  `docker/Dockerfile.slim` copied the workspace manifests, `packages/` and
  `web/`, but the build reaches outside them: its configs import two files
  from `scripts/` (`vite-fonts-as-files.mjs` and `lib/i18n-catalogue.mjs`,
  whose type declaration the admin build's type-check reads too), and the
  translator's catalogue is built from the server's string tables in
  `backend/internal/srvtext/locales/`. Both images copy all of it now. Nothing had built an image before the tag: the release ran the CI
  workflow as its gate with the image build switched off, then published the
  npm packages and the Release beside the image job rather than after it. The
  gate now builds both images, with no switch to turn that off, before
  anything is published, and the release checklist builds both locally before
  a tag is made. Two tests keep it that way: one follows what the frontend
  build reaches outside `packages/` and `web/` — imports, their type
  declarations and the paths they read — and fails if either image does not
  copy it; the other fails if the release skips the image build or publishes
  before its gate.
- **A big folder no longer re-renders itself once per thumbnail.** Each
  thumbnail that arrived re-rendered the whole grid, gallery or table: a
  folder of 344 files, about 240 of them with thumbnails, ran a Chrome tab on
  a Windows PC out of memory and froze Edge for about 30 seconds. Every
  thumbnail is drawn by a tile of its own now, so an arrival re-renders that
  tile and nothing else. A thumbnail is also fetched only when its tile comes
  near the screen, not fetched again because its signed link was renewed on
  the hour (a new version of the file still is), and past the cache's 500
  pictures, dropping the oldest no longer sets off an endless round of
  re-fetches. Found and fixed by Berk Başarır
  ([#50](https://github.com/BRF-Tech/filex/pull/50)).
- **The docs site's release page no longer fails to build when a release note
  wraps a code span across lines.** v0.43.0's notes wrapped the command
  `filex sync confirm <pair>` so that its second line opened with the
  placeholder, which the site read as an unclosed component, and every hourly
  refresh of the site failed. The span is joined back onto one line now.
- **Desktop: signing in to a server whose certificate comes from a private CA
  no longer fails with "fetch failed".** The request that trades the browser's
  one-time code for the app's token was the only one in the app's main process
  that went through Node's own HTTP client, which ignores the operating
  system's certificate store and proxy settings. A server behind a company or
  home CA — trusted by the browser, so the browser half worked — failed there
  after the server had already spent the code. It goes through Electron's
  network stack now, like every other request the app makes, and a test fails
  if anything in the main process uses the other client again
  ([#36](https://github.com/BRF-Tech/filex/issues/36)).
- **A release page that has to be shortened ends on a line break.** The
  GitHub release body is capped, and when an entry's opening paragraph alone
  ran past the cap the cut fell mid-word; it now falls back to the last line
  break.

### Tests

- **"Empty trash" leaves what is deleted while it runs — now proven on a run
  longer than one batch, and on one that waits its turn.** v0.43.0 already
  bounds an empty by the moment it was asked for: the ops row's `created_at`,
  on the database's clock, which a restart does not lose. Berk Başarır's field
  report — on the pull request's earlier runner, a 1 h 48 min run purged
  61,845 rows of a total of 61,844, the extra one a file a member deleted six
  minutes before the end — came with a test that deletes a file while a run of
  more than one batch is under way. It now runs against the job at both the
  trash and the ops level, beside a new one for a run queued behind another
  tenant's ([#47](https://github.com/BRF-Tech/filex/pull/47)).
- **The viewer audit opens draw.io and ONLYOFFICE when they answer.** e2e 100
  waited for a capability state of `reachable`, which the server has never
  sent (it says `ok`), so both rich viewers were skipped on every run, even
  with the services configured.


## [0.43.1] - 2026-09-24

Tagged, never published. Its release gate failed before anything went out —
no GitHub Release, no npm packages, no images — and every change it carried
is in 0.43.2. The gate caught two things: the published workflows still let
the release skip the image build (the change that removes that switch had not
reached them), and the release-notes step cut a long opening paragraph
mid-word.

## [0.43.0] - 2026-09-24

The release that makes filex extensible. **Apps** are a second kind of
plugin — a sandboxed WebAssembly module that adds things to *do* with
files — and two ship alongside it as public repositories: **e-Signature**
and **Convert**.

A **language pack** is an app with nothing that runs, so filex can be
translated without waiting for a release; the text the server writes — mail,
notifications, the pages behind a link — comes from the same catalogue; and
the interface lays itself out **right to left** for the languages that read
that way.

Alongside them: desktop folder sync is now **live** in both directions, the
explorer's table is the only table left in the product, an operator composes
the instance's **theme**, tags are personal or shared with the team, and
every door that issues a credential refuses to issue one wider than the
caller.

> ⚠⚠ **Security — upgrade promptly.** Every filex up to v0.42.2 with ONLYOFFICE
> configured accepts an unsigned save callback, which lets anybody who can
> reach the server overwrite a file ([Security](#security)). filex has never
> enabled the editor without a JWT secret — an install with no secret has
> editing off and is unaffected — so the one thing to check is the other
> side: a Document Server running with `JWT_ENABLED` off sends unsigned
> callbacks, and after this upgrade its saves fail. Turn JWT on there, with
> the same secret filex holds.
>
> ⚠⚠ **A narrow API token could mint a wide one.** Every door that issues
> a credential — the desktop pairing endpoint, self-service API tokens, S3
> access keys, NFS exports and SSH keys — measured the account behind the
> caller instead of the caller itself, so a token restricted to reading, or
> confined to one folder, could create a full-rights credential for the same
> account and then use it ([Security](#security)). Every one of them now
> refuses with `403 token_ceiling`. The desktop door was found and fixed by
> Berk Başarır ([#35](https://github.com/BRF-Tech/filex/pull/35)).
>
> ⚠⚠ **Desktop sync could replace real files — upgrade the server AND the
> desktop app.** A field report, traced and fixed by Berk Başarır
> ([#35](https://github.com/BRF-Tech/filex/pull/35)), found filex's own sync client writing a server's
> `202 "preparing"` status report to disk as the file and uploading it over
> the original, conflict copies nesting by the thousand, a stale mirror
> re-uploaded over a cleaned-up server, and a rename onto a taken name
> destroying the file that had it ([Fixed](#fixed)). The server fix protects
> every client already installed; the new desktop build carries the rest.
>
> ⚠⚠ **A cache in front of filex could hand one person's answers to
> everybody.** API answers carried no `Cache-Control`, so a CDN rule that
> caches everything kept `GET /api/auth/me` for two hours and served one
> administrator's identity to every visitor. Every `/api` answer is now
> `no-store` unless it is one of the four answers that say who the instance
> is ([Security](#security)). Found and fixed by Berk Başarır
> ([#41](https://github.com/BRF-Tech/filex/pull/41)).
>
> ⚠⚠ **The notification bell named files its reader could not open.** Queued
> copies, moves and deletes were announced to every account, a member of an
> RBAC storage read the names of files in folders they have no grant on, one
> person's "mark all read" read everybody's alerts, and a tenant admin could
> read every tenant's notification history ([Security](#security)). Found and
> fixed by Berk Başarır ([#42](https://github.com/BRF-Tech/filex/pull/42), [#43](https://github.com/BRF-Tech/filex/pull/43)).
>
> ⚠ **Before upgrading, read [Upgrade notes](#upgrade-notes)**: API/MCP tokens
> created with no scopes are rewritten to an explicit list that includes
> `admin` — review and narrow them; identity providers saved on the admin
> page come back switched off; existing tags become team tags, and a client
> that sends no kind now makes personal ones; custom CSS is off until
> switched on (see *Changed*); live desktop sync needs the new desktop build;
> a desktop pairing needs a signed-in browser, and existing pairings keep
> their old token until paired again; a rename onto a taken name answers
> `409`; a program that wants the `202` "preparing" answer must now ask for
> it (`X-Filex-Accept-Prepare: 1`); with SSO, sign-out now ends the identity
> provider's session too, and the provider must allow filex's sign-in pages
> as a post-logout return address; `POST /api/admin/trash/empty` answers
> `202` while a large trash is still being emptied, and `400` for a value it
> cannot read.

### Added

- **Apps (app plugins) — a sandboxed plugin that adds things to *do* with
  files.** A WebAssembly module that adds actions to the file menu, screens
  filex draws for it, and public pages for outside participants. Install from a GitHub repository
  URL, a file upload or a URL, always through a permission review; the grant
  is exactly the manifest's list and an upgrade that asks for more stops at
  the review again. Actions run as ops-queue jobs (progress, cancel, open the
  output), write through the same path as every other write, and are gated
  by ACL, read-only storages and encrypted folders at submit. Host functions
  under one permission each: files, per-file state, settings (secrets sealed),
  the server's engines (ffmpeg, ImageMagick, LibreOffice, Ghostscript, poppler,
  rsvg) with bare-token arguments, directory lookup, notifications (new
  `plugin.notice` event), mail (60/hour), guarded outbound HTTP, public links
  for outside participants (PIN with lock-out, exposed copies, jobs as the
  link's creator) and host-held signing (per-tenant CA, certificates,
  `host_sign`, admin CA download/rotate). Admin: **Plugins → Apps** tab with
  the install wizard, settings, per-action overrides and logs. Explorer:
  menu rows, ops-tray jobs, surface screens (form, steps, list, progress,
  people picker, PIN, file chooser, preview, PDF fields, signature pad),
  details-panel sections and an **Apps** side-bar section. Guest SDK
  `pkg/pluginkit` (stock Go, `GOOS=wasip1`). Docs: `APP-PLUGINS.md`,
  `PLUGIN-KIT.md`. Off in demo mode (`FILEX_APP_PLUGINS_DISABLED`).
- **Language packs — filex can be translated without a release.** An app can
  add a language to filex, and it now does so end to end. A manifest that only carries `ui_locales` is a *language pack*:
  it installs from the manifest alone (upload, GitHub repository or URL — no
  module, no Go, no release), never starts a runtime, and is listed in
  **Plugins → Apps** as a *Language pack* with each language's coverage of the
  running version's catalogue (*Español — 97% translated · the rest shows in
  English*). Its language joins every picker — the settings dialog, the admin
  header, public share pages — and translates the explorer, the admin panel
  and the public pages alike; anything it lacks shows in English. Integrity
  moves to the manifest (the sha256 pin and, with `FILEX_PLUGIN_TRUSTED_KEYS`,
  the signature are over the manifest). A right-to-left language lays the
  interface out right to left (see *Right-to-left layout*). Format, grammar and limits:
  `PLUGIN-KIT.md` → *Writing a language pack*; a template repository,
  `BRF-Tech/filex-lang-template`, walks a translator from export to install.
- **Right-to-left layout.** Arabic, Hebrew, Persian, Urdu and every other
  right-to-left language a language pack adds now lays the whole interface
  out right to left — the explorer, the admin panel, the public share, PIN
  and file-request pages, the signing wizard and the embeddable components.
  Whether a language is right to left is the server's one list
  (`wire.IsRTL`, the `rtl` flag on the offered-language rows); a page's `dir`
  is derived from its `lang`, and an embedded explorer takes its direction
  from its OWN `locale`, not the host page's (menus drawn under `<body>`
  carry it too). The stylesheets are written in logical properties and the
  admin panel in Tailwind's logical utilities (`ms-`, `pe-`, `start-`,
  `text-end`, …), so English and Turkish draw the same pixels as before.
  Gestures turn with the layout: a column's edge grows the way the pointer
  drags it, a dragged column lands on the side of its neighbour the pointer
  is on, the arrow keys move things the way they point, menus open toward the
  line's end and flip at the screen's edge, and the frozen Name and Actions
  columns draw their edges while a right-to-left table scrolls. Icons that
  mean a direction (back/forward, breadcrumb and disclosure chevrons, undo,
  sign-out, send, the panel icons) are mirrored; "open in a new tab",
  refresh, media controls and **document space** — a PDF page's signature
  boxes, images — never are. Commands, paths and URLs stay left to right;
  file names, paths and people are isolated (`<bdi>`), and number pairs,
  `tag:…` tokens and names inside a right-to-left sentence are isolated so
  they keep their order. List fields (recipients, extensions, tags,
  identities) accept the Arabic `،`, ideographic `、` and fullwidth `，`
  commas. A source check (`web/tests/quality/rtlLogical.test.ts`) keeps
  physical `left`/`right` from coming back. Docs: `docs/RTL.md`.
- **Appearance: the instance can wear your colours.** A new admin screen
  (**Appearance**, `/admin/appearance`) where an operator composes
  named themes — twelve colours per light/dark variant, a corner radius and a
  font stack — and picks one as the instance default. Ten more tokens are
  derived server-side and stored with the theme, so the browser does no colour
  maths at paint time, and the text colour on a coloured button is chosen by
  WCAG contrast rather than assumed to be white. Served to the login page and
  to anonymous share visitors as well, because a palette that stops at the
  sign-in screen is not branding. Themes export and import as one JSON
  document — the exported file *is* the upload body (migration 00051).
- **Desktop sync is live: an edit on either side arrives in about a second.**
  A save in the web app's text editor, an ONLYOFFICE save, an upload or a
  delete now reaches the synced folder on the desktop as it happens, and a
  save on the desktop reaches the server just as fast — instead of waiting for
  the engine's next 30-second lap. Measured on one Windows machine against a
  local server, six edits each at random moments: browser text save → file on
  disk 6.2–24.8 s (median 15.2 s) before, 0.19–0.22 s (median 0.20 s) now;
  ONLYOFFICE callback → disk 0.5–25.9 s before, 0.19–0.21 s now; local save →
  server 9.4–29.0 s (median 25.3 s) before, 0.43–0.61 s (median 0.46 s) now.
  `filex sync run --watch` subscribes to the server's change stream (the same
  WebSocket the web explorer uses, authenticated with the engine's own token
  through a ws-ticket) and watches the local folders with file-system events;
  a change reconciles just the folder it happened in (one listing, not a walk
  of the tree), and the `--watch` interval is the safety net — it no longer
  walks every pair, see *Changed*. `--live=false` leaves only the interval. The
  engine prints `live: connected|polling|offline — …` and the desktop app
  shows it as one word under each synced folder (*Live* / *Polling* /
  *Offline*). A newly paired folder starts syncing at once instead of on the
  next lap. See [Folder sync](docs/SYNC.md#how-fast-a-change-arrives).
- **Realtime: recursive, presence-less `watch` subscriptions.**
  `{"type":"watch","paths":[…]}` registers any number of roots per socket,
  authorised exactly like a room subscribe (confinement, RBAC, tenant), always
  acknowledged with `watching` (the capability probe for older servers), and
  delivered as `tree_change` frames with root-relative folders, coalesced like
  rooms and never dropped on a full queue. Folder-size refresh frames are not
  delivered to watches. See [Realtime](docs/REALTIME.md#watching-a-whole-tree-sync-clients).
- **Conditional uploads.** The multipart upload and the staged commit accept
  `expect` (`none`, or the `<size>:<last_modified>` a listing showed) and
  answer `412 PRECONDITION_FAILED` without writing when the file changed in
  between. The sync engine sends it on every upload, so a browser save that
  lands in the same second as a desktop save is kept beside it instead of
  being replaced. See [Uploads](docs/UPLOADS.md#conditional-uploads-expect).
- **A first sync that would re-upload a stale copy holds it and asks.** With no
  history, "new here" and "deleted on the server" look the same, and one client
  put 9,665 cleaned-up files back on a server. A first run that would upload
  more than 100 local-only files into a server folder that already has files
  holds them (and any file that differs) and runs the rest: `filex sync confirm
  <pair>` sends them, `filex sync discard <pair>` moves them to the local sync
  trash so the folder matches the server. `sync list --json` carries `hold_new`
  / `held`; the desktop app shows the count with **Upload them** and **Move to
  local trash**. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Bandwidth limits and a sync window.** `filex sync run --limit-down` /
  `--limit-up <KiB/s>` cap all transfers of a run together (the bodies are
  paced, never the connection), and `--window HH:MM-HH:MM` only syncs in that
  part of the day — outside it nothing talks to the server, not even the change
  stream, and a pass still busy when it closes stops cleanly and continues in
  the next window. The desktop app offers both as presets in Settings. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Transfer progress in bytes, with an estimate:** `transfer: 120/11704 (1.2 GiB
  of 52.6 GiB, about 8h 10m left)`, printed at least every 5 seconds. The desktop
  app shows it on each folder's line, in its own language. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **`GET /api/files/manager?action=changes&path=…&since=<cursor>`** answers "has
  anything under this folder changed since my cursor?" as `{ cursor, changed }`,
  from an in-memory change log fed by every write surface. Every doubt — no
  cursor, a restart, a cursor older than the log — is `changed`, and a change
  counts only if the caller can see what it touched. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Rescan one folder: `POST /api/admin/storages/{id}/sync?path=<folder>`.** The
  same walk over one catalogued folder, synchronously, answering `{path,
  scanned, added, updated, removed, reconciled}` (504 after ten minutes). Only
  rows inside the folder can be removed, a listing that failed part-way removes
  nothing, and no sync-run row is written. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Scan exclusions: tell a storage what not to catalogue.** A new storage
  setting, *Paths to exclude from scanning* (`config.scan_exclude`, on every
  driver), takes glob patterns relative to the storage root, one per line:
  `*` within a name, `**` across folders, and a pattern without a `/` names an
  entry at any depth (`.*` skips every hidden file and folder, `@eaDir` every
  Synology thumbnail folder, `*.tmp` every temp file), while `/build` or
  `downloads/incomplete/**` is anchored at the root. The scan does not go
  **into** a matching folder — a `.git`, a `.snapshots` or a download client's
  `incomplete/` costs nothing — and nothing matching is catalogued, indexed,
  thumbnailed or virus-scanned. One rule decides every walk that catalogues
  (the full scan, the one-pass object-store listing, a folder rescan — which
  refuses an excluded folder with 400 — the catalogue of a copied folder) and
  the `fsnotify` watcher, where a change to an excluded path no longer starts
  a scan. **A cost control, not an access control:** the files stay on the
  storage and are still served by path, over the file protocols and to the AI
  tools. Rows catalogued before a pattern was added stay as they are and are
  never moved to the trash for being unseen; filex's own names (`.filex-open`,
  `.keepdir`, an encrypted folder's marker) are outside the patterns; a pattern
  that would exclude everything, a `!`, a `..` or a broken glob is refused on
  save with `SCAN_EXCLUDE_INVALID` and a sentence naming the pattern in the
  reader's language — as is an empty or `/` storage root (`ROOT_PATH_FORBIDDEN`,
  which used to answer one English sentence in `error`; the code stays there
  and the sentence moved to `message`). Docs: [STORAGE.md → Scan exclusions](docs/STORAGE.md#scan-exclusions).
  ([#44](https://github.com/BRF-Tech/filex/issues/44))
- **Desktop: Pause sync,** in the tray menu and Settings, remembered across
  restarts, reboots and the hidden start at sign-in. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Desktop: the computer stays awake while sync moves files** (the screen still
  locks); an overnight first sync lost 1 h 40 min to idle sleep. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **The access log says who asked:** `user_id`, `token_id` (the row id, never the
  secret) and, in multi-tenant mode, `tenant` on every `msg=http` line, plus
  `action` for `/api/files/manager` (one of its own verbs, or `other`). The
  query string is still never logged. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **A cut search result says so:** both search responses carry `truncated`, and
  the explorer shows "More results than shown — narrow your search". ([#35](https://github.com/BRF-Tech/filex/pull/35))

- **Apps: the platform seal.** `cert_issue {purpose: "platform"}` hands an app
  with `sign` the installation's own seal: one key per tenant and app, kept by
  the host (never destroyed — `key_destroy` refuses it), CN *filex document
  seal*, OU the app's name, issued by the live authority and re-issued by the
  next one after a rotation; it signs through `host_sign` from jobs only. The
  signing app seals every completed request with it (the owner, 2026-09-22:
  "the final bytes are sealed by filex itself"). SDK: `pluginkit.PlatformSeal`,
  `plugintest.Host.PlatformSeal`.
- **Apps: a lock until lifted, and a lock on the job's own output.**
  `file_lock` takes `ttl_days: -1` (`pluginkit.LockUntilLifted`) — no end,
  until the app or an administrator (audited) lifts it — and a `ref` naming one
  of the job's OWN outputs, which is promised and taken when the output is
  committed (never when the job fails). How the signing app keeps a finished
  document locked for good, when the request asks for it.

- **Apps: `asset_fetch` — a pinned file, downloaded once by the host.**
  `{url, sha256, max_bytes}` → `{ref, size, cached}`, read with the ordinary
  file calls (an asset needs no `files:read`). Built for the signing app's
  fonts — "online çekebilirsek 0 MB": a Noto face for Arabic, Devanagari or
  Japanese is fetched the first time such text is used instead of adding tens
  of megabytes to the module. Through the same guarded path as `http_request`
  (`https`, the app's `http:<host>` grant shown at install, private addresses
  refused, redirects inside the grant); the sha256 is REQUIRED and checked
  before the app sees a byte (`integrity`, nothing kept — a font is a
  parser's attack surface); at most 32 MiB per file (the largest pinned font
  is 10.5 MB, its variable release 17.8 MB) and 256 MiB per app, least
  recently used first; kept in `<plugins dir>/assets/<app>/` across upgrades,
  removed with the app. A download outlives the call that asked for it (a
  screen's 30 s would cut a slow one and the next keystroke restart it from
  zero), a failure is not retried for a minute and is logged once per outage.
  The SDK wraps it as `pluginkit.AssetFetch`; `plugintest.Host` has
  `Network`, `Offline` and `Downloads` to test the offline path with.
  Measured against the real network: first Japanese use 0.9–1.4 s (5.3 MB
  downloaded and verified), every later one ~0.3 s with no network; with no
  internet the screen answers in ~0.1 s and says which characters will not
  print.

- **Apps: a home view is a page of its own, with a menu.** The explorer's
  **Apps** rows open an app's `home` view as a page of this app, in the same
  tab (`{base}app/{plugin}/{view}`, laid out like **My shares**), instead of a
  dialog over the file list — the owner's "İmzalar popup açıyor … kendi
  sayfasını açsın". A home surface may declare `sections` (`[{id, label,
  count?}]`) and which one it is; the frame draws them as the product's tab
  strip and keeps the open one in the address (`?section=`), so Back walks
  them and a link lands on one (`GET …/views/{p}/{v}?section=` hands the app
  `data.section`); a page opened with no section writes the one it landed on
  into the address without a step in history. The admin panel's Apps page and the dialog an embed falls
  back to draw the same menu. Hosts opt in with `config.appHomePage` +
  `@open-app-home`.
- **Apps: a notification can open an app's home page**, at a section:
  `notify_send` with no file and a `view` the app places `home` stores a new
  target kind, `app` (`open: {plugin, view, section}`); the web opens the
  page, the desktop app brings its window forward.
- **Apps: `context.actor.ip`** — the address a signed-in person's view event
  came from, read like a public page's `visitor_ip`, so an app that records
  who acted (the signing app's IP line) has it for a signed-in signer too.
- **`pdf-fields`: drawn or typed, lines under a signature, "Filled by".** A
  signature box says how it is signed (`style: "typed"`, and only then is a
  face asked for); the node may offer `stamp_lines` and each signature box
  chooses its own `lines`, previewed in the app's own words; a date / text /
  tick box's owner is labelled **Filled by** (tr **Dolduran**) rather than
  **Signer**.
- **`signature-pad`: `label`, `required`, `font`, `fonts`.** The pad is
  labelled like a form field, with the same `*` when required — a required
  signature was the one required box with no star — and `fonts` narrows the
  faces a typed signature may use (one face: no picker).

- **Apps: an app's links say what they are.** A manifest page may declare a
  `purpose` — a label ("Signing request"), what revoking a link of it does,
  and the section of the app's home page that shows it — and `share_create`
  may carry one for the link it opens, the only way a page-less link (the
  finished document handed to everybody) can say what it is ("Signed copy").
  My shares and the admin's Shares list then carry `app` on those rows
  (`db.AppLink`, `shares.purpose_json`), and My
  shares marks the link, opens the app's page for it, and says the app's own
  words before a revoke. The owner's decision: signing links stay listed,
  but no longer as plain shares of the file whose revoke silently cancelled
  a request.
- **Apps: `applies.engine_ext`.** Extensions an action takes only while an
  engine is there (`{"libreoffice": ["docx", …]}` on top of `ext: ["pdf"]`);
  the host folds them in before the menu or the run check sees the action.
  "Sign…" was offered on a .docx on an installation without LibreOffice and
  the click opened a page saying it could not be done.
- **Apps: a file reference says whether its storage takes writes**
  (`FileRef.read_only`, on views, jobs and pages), so an app whose flow ends
  in a write can refuse at the start — a signing request on a read-only
  storage was "sent", froze the file, notified the signer, and could never
  complete.
- **Apps: manifest settings in every language.** A setting's `label`, `help`
  and `placeholder` (and an option's `label`) may be `{"en": …, "tr": …}`;
  the signing app's settings were English inside the Turkish admin panel. The
  wake-up line an app reports is kept in every language it gave and read in
  the reader's, with the host's tally beside it in the same language.
- **Translator tooling** — `scripts/i18n-export.mjs` writes the complete string
  catalogue in the exact shape of a pack (`filex-catalogue-en.json`) plus
  per-key context (which catalogue, which grammar, the Turkish reference, where
  it is used); `scripts/i18n-validate.mjs` checks a pack against it — key
  shape and byte limits, unknown and missing keys with coverage, placeholders,
  plural forms and the per-catalogue `@` / `|` / `{'…'}` / `%{` rules, using
  vue-i18n's own parser when installed. The catalogue is built into every
  binary (served at `/admin/i18n/filex-catalogue-en.json`) and attached to
  every release.
- **Text the server writes, in a pack's language** — e-mails (share and
  file-request links, invitations, new accounts, drop notices to the owner,
  the SMTP test, filex cloud verification, the footer under an app's mail),
  the notification phrases (bell, browser pop-up), the no-JavaScript pages
  behind a link (PIN gate, file request, folder listing, error pages, the
  sign-in hop) and the install review's permission sentences now come from
  one server catalogue: English and Turkish built in, and any installed pack
  extends it through `server.*` keys in the same `ui_locales` file — 223 keys
  in the catalogue, with where each appears and what each placeholder holds.
  Each flow keeps the reader it always had (the sender's language for a link,
  the recipient's account for a grant, the folder owner's for a drop notice,
  the visitor's browser for a public page) and the catalogue answers in it,
  falling back to English per key. A translation whose placeholders differ
  from the English is not used at run time, so a mail never loses its link
  or PIN. Mails carry `Content-Language`; apps may say which language they
  wrote a mail in (`pluginkit.MailSendIn`) so filex's footer matches it.
- **Plural forms by CLDR category** — the explorer, the admin panel and the
  server pick a form by `Intl.PluralRules` (Go: the same Unicode rules), so a
  pack may write `zero`, `one`, `two`, `few`, `many` and `other` where its
  language has them: `<key>_few` beside the plain key in the explorer and the
  server's text, one `|` form per category in the admin panel. Arabic gets its
  six forms, Russian its four; English and Turkish read exactly as before, and
  packs written with one, two or three forms keep working. A form may say
  the number as a word where its category holds only that number (*يوم
  واحد*). The export lists each language's categories; the validator checks
  forms against them.
- **Apps, platform v2** — file **locks** (`files:lock`: an app freezes a
  document for everyone, administrators included, until it lifts the lock or
  the TTL passes; `423 locked` on rename/move/delete, `locked` badge in
  listings, admin list/force-unlift), **state-aware menu rows**
  (`applies.state` / `no_state` on the app's own state keys, exposed as
  `app_state` on listings), **hidden actions**, **`page` placement** (a
  view opens as a full page in a new tab), **per-job output choice**
  (`job.output`: same file as a new version / new sibling / custom name) and
  **addressed, clickable notifications** (`notify_send` `to_user_id` +
  `target` with the action or view to open — the notification's `target`
  now carries `open`).
- **Apps, a scheduled wake-up.** An app that asks for the new `schedule`
  permission is woken once an hour and asked what it wants done and *when*;
  filex runs each answer at the minute it named, as an ordinary
  `plugin-action` job (same ops row, sandbox, outputs and cancel), with
  `actor_id` null because nobody asked for it. So a signature request closes
  itself at 03:00 and tells both sides, instead of waiting for the next
  person to open the status screen. New guest export `tick`
  (`TickInput`/`TickOutput`, read-only scope: decide in `tick`, act in the
  action it schedules). The schedule is a table (migration 00050), so it
  survives a restart and runs late rather than never; `status` doubles as a
  lease, so two filex processes on one database run a due item exactly once.
  An item's `key` is an idempotency key — naming it again moves it rather
  than adding a second. Bounded throughout: 64 items a wake-up, 16 files an
  item, 16 KiB of params, a 30-second call budget, the window is the hour.
  A wake-up that traps, hangs or fails costs its app one hour and is never
  retried inside it. Off under `FILEX_APP_PLUGINS_DISABLED` and in demo mode.
  An app granted `schedule` whose module has no `tick` export is refused at
  install.
- **Apps, platform v3 — one public surface.** A download share, a file
  request and an app's page are now the same branded shell: the instance's own
  name, logo, colours and footer (`GET /api/public/branding`, unauthenticated,
  and revalidated against an ETag rather than held — see *Fixed*), one PIN gate, one expiry story, one visit counter, the
  "this link is gone" wording and a language picker — whichever kind of link
  a stranger was sent. Whitelabel means whitelabel: a signature request from a
  renamed instance does not say "filex".
  The shell wears the shape the PIN gate had before it: a calm card centred on
  a soft ground, the instance's mark above it, a round accent-tinted badge, a
  full-width field and a full-width accent button — in three widths, 400px for
  a gate or a single document, 520 for a file request, 880 for a folder's
  listing or an app's screen. ⚠ **A locked gate no longer names the link.**
  The file's or folder's name is drawn once the link has opened, not in front
  of the PIN box: telling somebody who cannot get in what is behind the link
  hands the name of a real document to anybody typing at a token. It is the
  same reasoning as the single "this link is not available" sentence for
  expired, used up and withdrawn. The plain server-rendered pages stay
  as the no-JS fallback, and `curl -O` on a share link still downloads the
  file rather than collecting an HTML page. New JSON:
  `GET /api/public/s/{token}` (+ `/pin`, `/event`, `/file/{ref}`) and
  `GET /api/public/d/{token}` (+ `/pin`, `/upload`), every answer `no-store`
  and `noindex`. `expired` and `revoked` are two different words for two
  different things, and a shut PIN gate is neither.
- **Apps, platform v3 — an app's public page IS a share.** `share_create` /
  `share_revoke` / `share_state` open and manage a real share, so an app's
  link is `/s/<token>` and an administrator sees and revokes a signature
  request in **Shares** like any other link: one revoke list, one expiry
  policy, one PIN implementation to get right, one visit counter, one set of
  audit rows. The share row carries `plugin_id`, `page_id`, `subject`,
  `state_json` and `files_json` (migration 00046), and
  `GET /api/admin/app-plugins/shares` lists one app's links.
  `public_page_create` / `_revoke` / `_state` stay bound as the older
  spelling of the same three calls. ⚠ `/p/*` and `/api/p/*` are retired and
  **301** to their share; pages created under the pre-release table cannot be
  carried over, because it stored only the hash of each token.
- **A scheduled app can see its own work again.** `state_list` filtered every
  row through the asking person's ACL, and the hourly `tick` has no person, so
  `acl.CanSee` refused all of them: a woken app was told the world was empty
  while the same rows were on a screen in front of somebody, and with no inputs
  to key `state_get` on there was nothing to fall back to — the wake-up was
  inert on any instance with the ACL wired. A call the HOST itself started now
  lists the app's own rows in full. ⚠ Keyed on that, never on "no actor": a
  public-page call is person-less too and stays told nothing.
- **The plugin SDK knows the `schedule` permission.** `plugintest`'s closed
  permission set was not extended when the wake-up landed, so an app asking for
  it had its manifest refused by its own tests while this server installed it
  without complaint. The set is now checked against the host's in the host's
  own tests, so the two cannot drift again.
- **An app can share the file its own job is writing.** `share_create` now
  accepts a ref naming one of THIS job's outputs. The link is decided and
  handed back at once — token, PIN, expiry, caps — so the app can put the
  address in the mail it is composing, and the share **row is written when
  that output is committed**, pointing at the node the bytes landed on. It is
  an ordinary share from that moment: in **Shares**, revoked and expiring like
  any other, behind the same bcrypt PIN gate, naming the file the recipient
  actually gets — for both output modes, a new file beside the original and a
  new version of it. A job that fails, or that never keeps the output, writes
  no row at all, so the promised token answers nothing: a link to a file that
  does not exist is never created rather than created and cleaned up.
  Without this a signature round finished and the signed document had no link
  to travel by — an output has no catalogue node while the job runs, and a
  share points at one. Sharing a file that already exists is unchanged.
- **The PIN lock-out now guards every public link.** Five wrong answers shut
  the gate for ten minutes, counted on the share row so it survives a restart
  and holds across two instances behind one address, and the correct PIN is
  refused during the lock — a lock the right answer lifts is no lock at all.
  Before this it existed only on an app's page; a PIN on a `/s/` link could be
  walked through at the speed of HTTP.
- **Apps, platform v3 — screens say what they are asking.** A `select`
  renders as a row of choice buttons instead of a dropdown whose options are
  hidden until clicked (`multi` for several); `Field.Advanced` is gone, so
  nothing is folded away behind "advanced"; a step carries at most one primary
  button; and `show_when` / `required_when` let a field depend on another, so
  a form cannot present a contradiction. Both conditions are re-checked by the
  host at submit: a hidden field's value is **dropped before the job runs**.
  `date` is a field type (and the `date` rule on text fields is gone — two
  ways to ask for a date is how you get two formats); a `pdf-fields` node in
  a `page` view fills the screen; and its fields carry a `label`, so a signer
  fills a form of named fields instead of hunting across a document.
- **`Surface.open {path, action|view}`** — a screen can send the person to a
  file and start one of the app's own screens on it, which is what makes a
  home screen a list of *documents* rather than of names. The host checks the
  screen belongs to the app that answered; the server checks the path against
  the **asking** person's permissions and drops the link rather than refusing
  the screen. A public link's surface never carries it.
- **`state_list {key, limit}`** (permission `state`) — an app can find the
  files it keeps state on, which is what a home screen is made of; before this
  an app only ever saw the file it was opened on. A deleted file drops out of
  the answer by itself, an un-indexed one does not (the state row now carries
  the path as well as its hash, migration 00048), and every row is filtered
  through the asking person's permissions.
- **Apps can add a language.** A manifest declares `languages[]`, and the host
  **refuses to install** an app whose own screens are missing one of them — a
  half-translated screen is the author's bug and they should meet it before a
  person does. It may also ship `ui_locales{}`, a language pack for **filex
  itself**: the language joins the interface's picker (public links included)
  while the app is installed and leaves with it.
- **Bring your own signing authority.**
  `POST /api/admin/app-plugins/signing/ca/import` takes a certificate and its
  key in PEM (an encrypted `.p12`/`.pfx` is converted first with
  `openssl pkcs12 -nodes`); `GET …/signing/cas` lists every authority the
  tenant has, live and retired. ⚠ Importing or rotating **retires** the
  current authority and never deletes it — a signature made two authorities
  ago must still verify — and `host_sign_info` now hands the guest
  `ca_certs_pem`, every authority ever used, which is what a verifier's root
  pool should be built from. Signer certificates are issued for ten years
  rather than thirty days: a verifier asks whether the certificate is valid
  *now*, so short ones made every signature read "certificate expired" on its
  31st day, and the private key is destroyed seconds after the signature
  either way.
- **`pluginkit/plugintest`, a test kit shipped with the SDK.** A fake filex in
  memory — files, settings, per-file state, locks, engines, signing,
  notifications, mail, HTTP and shares — answering with the same error codes
  the real host returns and refusing what the manifest never asked for, plus
  assertions for the surface rules, every declared language, the manifest, the
  registered handlers and golden screens. It runs before `plugin.wasm` exists,
  so a build can be refused instead of shipping a broken or half-translated
  screen.
- **Interface preferences are per person, not per browser.**
  `GET|PUT /api/me/prefs?surface=web|desktop` (migration 00047) keeps theme,
  palette, density and language in the database, capped at 64 KiB per
  surface; how each folder was left is the separate view-prefs store it always
  was. `localStorage` is per BROWSER and never per person, so a theme picked in
  one browser was simply not there in the other. The web app reads and writes
  it; the desktop app and an embed still keep these choices on that machine or
  in that browser.
- Ops queue: `POST /api/files/ops/{id}/cancel` and a `cancelled` status.
- **My shares** (`/drive/my-shares`, and `/admin/my-shares` for an
  administrator; in the explorer's navigation panel): a person who is not an
  administrator can see the links they created — **Copy link**, **Copy PIN**,
  **Revoke** — and read back their PINs. `GET /api/shares` lists them;
  `GET /api/shares/{id}/pin` reads one PIN back. Share PINs are sealed with
  AES-256-GCM beside the bcrypt hash that guards the gate (migration 00049),
  and only the creator or an administrator can open one, through an endpoint
  that writes an audit row every time.
- **Symlinked directories are navigable, and `follow_symlinks` decides what
  happens at the storage boundary** (`local`, **off** by default, under
  Advanced settings). A link whose target is **inside** the folder is always
  followed — it opens as the directory it is, reports the target's size, and
  this holds for relative and absolute links alike. A link whose target is
  **outside** is governed by the option: off, it is **listed with a reason**
  and cannot be opened, written through or deleted through; on, filex treats
  the linked content as part of the storage. Out-of-root links are shown
  rather than hidden deliberately — an entry you can see and cannot open is
  confusing, but an entry that silently is not there is worse. Listings carry
  `symlink: true` and a `link_state` of `outside_root`, `broken` or
  `unresolved`; the wire `type` stays the closed `file`/`dir` union, so
  existing clients render exactly what they rendered before. Docs:
  [STORAGE.md → Symlinks](docs/STORAGE.md#symlinks).
- **A link that will not open now says so, in words, everywhere the explorer
  runs.** The flag above was reaching the browser and nothing drew it, so the
  half of issue #34 that was actually reported — a row that looks like an
  ordinary file and mysteriously fails — was still on screen. Such a row now
  carries a badge in the list, the grid and the gallery (*Outside storage*,
  *Broken link*, *Remote link*), with the reason as its tooltip and as its
  screen-reader label, and the same sentence again in the details panel above
  the facts that mislead on their own ("Size: 0 bytes", "Type: file"). A
  **broken** link deliberately reads differently from an out-of-root one:
  only the second can be allowed, with *Follow symlinks that leave this
  folder*, and sending somebody to a settings screen for a target that has
  been deleted helps nobody. Opening one is **refused out loud** rather than
  silently ignored — and refused in the browser, before the request, because
  the driver's containment error reads as a fault rather than as a boundary
  somebody chose. It is in `@brftech/filex-core`, so the admin app, the
  desktop app and every embed get it together; English and Turkish.

- **The signing boxes are defined first and placed after.** A `pdf-fields`
  node gains two modes: `define` draws the boxes as numbered cards — a name,
  whose it is, required, a text box's rule, a date box's layout — with no
  document on screen, and `place` draws the document with the boxes that still
  need a place: choose one, tap the page, or drag to size it. Two questions,
  two screens. A date box says how it is written (`31.12.2000`,
  `12/31/2000`, `2000-12-31`), and that `format` now survives the round trip.
- **The admin panel's navigation has an Apps section**: one row per installed
  app's home screen (e-Signature's is *Signatures*), administrators only, and
  absent when no app has one.
- **The bell carries its count, and everybody can read all of their
  notifications.** The unread count is a badge on the bell icon — exact to 99,
  `99+` above — drawn by one component wherever a count is shown, and the
  desktop app shows it on its dock icon where the system has one, and in its
  tray tooltip. **View all** opens the complete, paged list over the explorer
  for every account, instead of linking to the admin notifications page a
  non-administrator cannot open. A row is clickable exactly when it has
  somewhere to go; the desktop app's notification now opens an app's screen
  the way the web one does.
- **One table, everywhere — the explorer's own.** Every table in filex is now
  the file list's table: the admin screens (Users, Shares, Audit, Queue,
  Webhooks, Trash, Usage, Updates, Replication, Sync, Duplicates, the plugin
  and app pages, …), My shares, notifications, the connection panels (API
  keys, S3 keys, SSH keys, NFS exports), the archive viewer and an app's
  `list` screen. Each one has the list's column resizing, sorting, column
  visibility and order, remembered **per table** on your account, and ends
  every row in **one** pinned *Actions* menu. A table that holds one page of
  a longer list does not pretend to sort it: its headers close and say why.
  An app's `list` node gains optional `width`, `sortable`, `align` and a
  per-row `sort` value (backward compatible; `docs/APP-PLUGINS-API.md`).
- **A default folder view — yours, and the instance's.** Settings → Default
  folder view sets how a folder you have not arranged opens (view, sort,
  columns); an administrator sets the instance's on the admin Settings page
  (same name), which applies to everybody who has not chosen their own. A
  folder you arranged keeps its own arrangement over both; "Reset" hands it
  back to the default.
- **Admin → Identity providers really manages sign-in.** OIDC, LDAP and the
  proxy header configured on the page are built through the same code as the
  environment's, applied the moment they are saved (no restart) and offered on
  the login page at once. Before this version the page wrote settings no
  server read and answered "restart the server" to a restart that changed
  nothing. The page cannot lock the instance out:
  - password sign-in and the installation administrator's recovery sign-in
    are the environment's (`FILEX_AUTH_DRIVERS`, `FILEX_AUTH_RECOVERY_LOGIN`);
    the page has no switch for either;
  - providers from the page are added AFTER the environment's, so `local`
    judges the administrator's password before any directory round trip, and
    one that cannot start is left out with its reason on its card;
  - every save runs the real test; switching a provider on while its test
    fails needs a confirmation that names the failed steps;
  - switching off the last way an administrator can sign in is refused.

  A provider the environment defines is shown read-only with where it is
  defined (`FILEX_AUTH_DRIVERS`, the config file, the built-in default), and
  wins over a configuration the page holds under the same name. Secrets are
  sealed with `FILEX_SECRET_KEY` and never sent back; every change is one
  audit row naming the provider, the switch and the fields that changed —
  never a value. Instance-wide: supertenant administrators only. See
  [docs/SSO.md](docs/SSO.md#managing-providers-on-the-identity-providers-page).

- **Personal and team tags.** A tag is now either **personal** — yours alone,
  like a star — or **team** — shared with everyone in your tenant who can see
  the file; adding or removing a team tag needs edit permission on the file,
  and a viewer sees team tags without being able to change them. A team tag
  never crosses a tenant boundary, not even on a storage two tenants share.
  Every place a tag appears says which kind it is, with a glyph and in words:
  the chips on a file (personal outlined, team filled), the tag picker (which
  asks who sees a new tag — personal by default, team offered only where you
  may edit, and otherwise shown disabled with the reason), the navigation
  panel (two groups, **Personal** and **Team**), the tag view's crumb
  (`#rapor · Personal`; `.mytag~` / `.teamtag~` addresses, the old `.tag~`
  still opening both), the Tagged files page and the advanced search, which
  now offers your tags as one-click picks. Names keep the capitals they were
  typed with ("Müşteri Teklifi" is no longer stored as "müşteri teklifi");
  sameness is case-insensitive and treats the Turkish `I`/`ı`/`İ`/`i` as one
  letter, so "IŞIK" and "ışık" are one tag, and so are "INVOICE" and
  "invoice" (docs/SEARCH.md → "What the same tag means"). The API answers
  `items: [{name, kind}]` beside the old `tags`, takes `items` on write,
  `?kind=` on `tagged`, and says `can_edit_team`; a `tag:` search filter
  covers both kinds you can see. Agents get `GET/POST /api/ai/tags` and the
  MCP `file_tags` tool, which require the kind of every tag written.
- **An app's table can hold dates.** A `list` column may say
  `format: "date"` (`YYYY-MM-DD`) or `"datetime"` (RFC 3339): filex prints
  the value the way the explorer prints dates — the reader's language and
  clock, a calendar day never moved by a time zone — and sorts by the value.
  The e-Signature app's *Due* column uses it ("29 Eyl 2026", not
  "2026-09-29"). An older filex shows the value as sent.
- **Apps: the platform says who an action is for, and where its result may
  go.** Four manifest words, each replacing something filex had to guess.
  `applies.writable` marks a flow that ends in writing the file although the
  action's own output is `none` (a signing request: nothing now, the signed
  document at the end), so it is not offered where nothing can be written.
  `output.elsewhere` (with `mode: "sibling"`) lets the result go somewhere
  else: on a read-only storage *Convert…* is still offered, the wizard asks
  WHERE with filex's own destination picker — starting at `context.home`,
  the person's first writable storage — and the job answers `output:
  {"mode": "folder", "dir": "<storage>://<folder>"}`, which the server
  re-checks before queueing it (the storage enabled and in the caller's
  tenant, not read-only, the folder really there, the caller an editor, and
  locks and internal directories honoured). A personal state key
  (`<key>@<user id>`, matched by `"state": ["todo@me"]`) offers an action to
  ONE person: the e-Signature app marks each signer whose turn it is, so
  *Sign / Fill* is offered to them and not to everybody who can open the
  file. And a manifest's `messages` are texts filex says on the app's
  behalf later — a file lock's reason is kept as a key with arguments and
  read back in each reader's language, on the admin page and in the `423` a
  refused write gets. `plugintest` checks all four, and
  `pkg/pluginkit/humandate` gives an app the explorer's date style in Go so
  its own screens read like filex's. Docs:
  [App plugins](docs/APP-PLUGINS-API.md), [Plugin kit](docs/PLUGIN-KIT.md).
- **`pkg/pluginkit/humandate` — an app writes dates the way filex does.** A
  date inside an app's own sentence (a mail, a notice, "valid until …") is
  written in the explorer's format in English, Turkish, German, Spanish and
  French (`Day`, `DayTime`, `Stamp`), so no app keeps a month table of its
  own. The e-Signature app uses it for every date it shows a person; its
  audit trail keeps ISO 8601.

- **Which filex this is, where a person can find it.** `filex 0.43.0` at
  the foot of the account menu — the admin panel's and the explorer's — and
  in the head of the user settings dialog. It was on the sign-in page and
  the administrators' About page only, so somebody already signed in who is
  not an administrator had no way to say which version they were on. The
  server's own string, as it reports it (a development build says so), and
  nothing to translate: a name and a number.

### Changed

- **Without the index, every word of a search has to be in the file's own
  name.** A word that appears only in a folder name used to answer through the
  path when the longest word of the query happened to be in the file name, so
  whether it did depended on word order (`main code` found `/Code/main.go`,
  `Code main` did not). It is now found with the index only; matching the words
  against the path read every row from the table (1.06 s for an eleven-word
  query on a 169k-file catalogue). By Berk Başarır
  ([#46](https://github.com/BRF-Tech/filex/pull/46)); see `docs/SEARCH.md`.
- **Connections is "how to connect" and nothing else — the Storages tab is
  gone.** Both doors into that screen (the explorer's *How to connect* and the
  admin panel's *Connections*) are the same component, and both opened on a
  **Storages** tab holding a second, poorer storage list and form: no sync
  mode, no RBAC, no sync runs, no drift report, next to an Admin → Storages
  that has all four. Storages are created, edited and deleted there — the
  Connections page links straight to it — and the storages you can browse are
  listed by the explorer's navigation panel; the panel itself now opens
  directly on the protocol guides and the credential each one needs. The
  `<filex-connections>` element loses the `initial-tab` attribute and the
  `changed` event with it: there is no second half to open on, and nothing in
  the panel can change a storage any more.
- **A row's Actions menu opens above the dialog it was opened from.** Inside
  the explorer's pop-up screens — *API keys*, *How to connect* — the menu was
  painted **underneath** the pop-up and looked as if it never opened. It
  teleports to `<body>` so no ancestor can clip it, and that left its
  container's place in the stack behind: menu 80, pop-up 130. It now measures
  the highest layer in the ancestry of whatever opened it and sits one above
  that, so it clears the dialog — and, in an embed, whatever the host page's
  own container is worth — without flattening the orderings the stylesheet
  sets on purpose.
- **One word per thing, in the words the product already uses (translation
  pass).** Translating v0.43.0 into Spanish, German and French read
  the whole catalogue in one sitting and found the places where it does not
  agree with itself. What an API key is allowed to do is a **permission** /
  **izin** everywhere now — the fieldset the boxes sit in, the column that
  lists them afterwards and the server's refusal said *Scopes*, *Permissions*,
  *Can do* and *Yetkisi* between them; `scope` survives only where it is the
  OIDC provider's own word. The four near-identical "at least one scope is
  required, `admin` is never implicit" sentences are three, one per surface
  that genuinely has one, and the admin form's error and hint are ONE sentence
  drawn in red or in grey. Spelling is American English throughout (*Colour
  palette*, *your own colours*, *the colour palette* and macFUSE's *licence*
  sat beside *Accent color* and *License: {license}*). A place filex keeps
  files is a **storage**, never a *drive* or a *sürücü* — the connection
  guides' drive letters are a real Windows drive and stay. Smaller ones: the
  Queue's *About* column is *Subject* (it was the About page's word), the
  About page's tools are *Found* / *Not found* (it was the Apps table's
  install date), the Search test's *Search scope* is *Look in* like the
  explorer's, a notification's *Scope* column is *Recipient*, an install
  refusal no longer quotes a button label that does not exist, the default
  folder view's help points at the person's own Preferences instead of at
  itself, and an S3 key refusal says the server has no encryption key instead
  of calling that key an *access key*. The glossary rows are in
  [CONTRIBUTING](docs/CONTRIBUTING.md) and `web/tests/i18n/vocabulary.test.ts`
  fails a relapse.
- **An environment variable's NAME is never inside a translated sentence.**
  `FILEX_SECRET_KEY`, `FILEX_AUTH_DRIVERS`, `FILEX_AUTH_RECOVERY_LOGIN` and
  ONLYOFFICE's `JWT_SECRET` were letters in the middle of six strings a
  translator retypes; they are a `{env}` slot the page draws as `<code>`, the
  way `login.noProviders` already did.
- **A pack is not asked to translate a string that is only a placeholder.**
  Ten notification bodies were the whole value `{path}`, `{reason}`, `{body}`,
  `{folder}`, `{error}` or `{notice_title}` — nothing to translate, ten
  entries every pack had to reproduce byte for byte. They are no longer
  exported; the renderer falls back to the same template, so nothing on screen
  changes. The words a notification falls back on (`server.notify.word.*`)
  moved into the server catalogue, because two of them — *Someone* and *a
  file* — are also what a MAIL says when nobody typed a name; they were two
  keys with one meaning (`server.mail.drop_received.someone`,
  `server.mail.share.unnamed_file`, both removed).
- **A sentence with a link in it is one message.** The ZIP wait page said
  `zip_hint_a` + `zip_hint_b` (the link's words) + `zip_hint_c` (a full stop):
  a sentence no translator could reorder or punctuate. It is one message with
  `{link}` in it plus the link's own label, the shape
  `access.ui.create_then_send` already took.
- **`events:<name>` is refused at install.** It parsed, was granted, and
  printed *"Is told about … events (not wired yet)"* in the permission list an
  administrator reads before trusting an app with their files — while nothing
  delivers a file event to an app (`on_event` returns 0 and no host code calls
  it). A permission that does nothing is worse than a missing one. The export
  stays reserved in the kit; the permission comes back with the wiring.
- **The catalogue's translator notes say "email", not "e-mail"**, in all 64
  entries that mention it — the screens have said *email* since v0.42.0, and a
  translator who trusts the note over the string writes the hyphen into their
  own language.

- **The admin panel says which thing, not only which kind.** The Panel's
  Recent activity and the Audit log name what a row is about — the user's
  e-mail, the storage's name, the file's path ("Kullanıcı “ayse@…”" instead
  of "Kullanıcı", "Depo “arsiv”" instead of "Depo #2") — and keep the name
  after the thing is deleted. The Audit log's action filter offers resources
  by name; its address column no longer carries the client's port; its
  "Target" box, which filtered nothing, is gone.
- **"File history" finds a file by name.** It asked for a node id; it now
  searches, lists the matching files and opens the one picked, and the
  history page names the file and where it lives.
- **Corporate identity's live preview is the public page itself** — the
  same shell and file card a visitor gets — instead of a hand-drawn card
  that looked like no page filex serves.
- **One set of words for a storage** everywhere storages are listed: the
  driver by name, "Read-only", "Disabled" (the admin list said "RO" and
  `local`, Connections "SALT OKUNUR" and `LOCAL`).
- **An admin's own notification settings live in the user settings dialog
  only**; the admin Notifications page opens it instead of repeating the
  switches. An upgrade notice is scoped "Administrators" there, not
  "Everyone" — no other bell shows it.
- **A notification a service makes impossible** (virus found with scanning
  off, escrow key with none set up, an app's message with apps off) is shown
  to an administrator greyed with the reason, and not offered to anybody
  else; the Webhooks screen keeps it subscribable and says why it will not
  fire yet.

- **The sync watcher's interval asks instead of walking.** A pair is walked only
  when its local tree changed unseen, the server's change log (`action=changes`)
  says something under its folder changed while the change stream was down (a
  reconnect asks once, instead of walking everything), a folder whose last pass
  failed is due a retry — that folder only, with a growing gap — or
  `--full-every` (default 30 min) passed. It used to walk every pair every 30 s:
  one Mac with 7,048 folders sent 100–150 thousand listings an hour, around the
  clock. Against a server without the change log, a quiet pair's walks back off
  to `--watch-max` (default 5 min). ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Deleting many files is several times faster.** A delete job trashes 4 items
  at a time (`FILEX_OPS_DELETE_WORKERS`); jobs still run one after another,
  items listed inside a folder that is also deleted are dropped first (the folder
  lands whole in the trash), and progress is written about once a second. On S3
  a trashed file takes three requests instead of four. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- Default CORS allowed headers gain `Range` and `X-Filex-Accept-Prepare`;
  `Content-Range` and `Retry-After` are exposed.
- ⚠ **A view change belongs to the folder it was made in.** Switching a folder
  to grid, sorting it or resizing its columns no longer changes every other
  folder: an untouched folder opens as your default (else the instance's,
  else filex's own). The "remember per folder" switch is gone — per-folder
  memory is simply how it works. Upgrading keeps every folder you had
  arranged and your old column widths (as your default columns); the old
  "last change anywhere" view is not carried over, because it was never a
  choice anybody made.
- **A service that is not set up is not offered as if it were.** Where
  ONLYOFFICE, draw.io, the converter or mail is missing, an administrator
  sees the entry greyed with where to set it up (*External services*), and
  everybody else does not see it. Previewing an office document or a diagram
  says so in words — never `Config fetch 503`, never an environment
  variable's name.
- **A line between every app's actions** in the file menu, the selection
  bar's "⋯" and the row's ⋮, so two apps' verbs no longer read as one list.
- One name per service, the one its project uses: **draw.io** (not
  "Drawio", "drawio" or "diagrams.net") and **ONLYOFFICE**; a `.drawio` file
  is a "draw.io diagram" in the Type column.
- **One word per thing, one date and size format, one name per person.** The
  key a person creates for a device or an agent is an **API key** everywhere
  (it was also "API token" and "token"); a share's **PIN** is called that on
  the page that asks for it too (it said "Code"). Turkish uses one term per
  concept — parola, ad, depo, oturum aç / oturumu kapat, Ana sayfa, Çöp
  kutusu, Değiştirilme, Sahibi, e-posta; *senkron* for the server's scan of a
  storage and *eşitleme* for the desktop's folder sync — sentence case, and
  the polite "siz" form in every sentence. English writes "email" (not
  "e-mail"), and a replica's write mode is "Synchronous / Asynchronous".
  The glossary is in `docs/CONTRIBUTING.md` → *Words*, and
  `web/tests/i18n/vocabulary.test.ts` keeps it. Every date reads the way the
  explorer writes it ("Sep 21, 2026, 2:50 PM" / "21 Eyl 2026, 14:50") — the
  admin tables, the audit log and the share dialog included; sizes,
  percentages and durations are written the way the interface language writes
  numbers. The share email and the no-JavaScript folder page count sizes in
  1000s like the interface, so a file the listing calls "1.5 MB" no longer
  arrives as "1.4 MB". A person is shown by their display name, else
  username, else email — in the Owner column, the share dialog, the presence
  strip and the admin audit log, Shares, grants and dashboard alike (those
  responses gain `user_name` / `creator_name` beside the email).
- **New installs name the first administrator `admin`.** The account first
  run creates (from `FILEX_ADMIN_EMAIL`, or `admin@local`) gets the username
  `admin`, and signs in with it on the web, SFTP and FTPS. `admin` stays
  reserved for everyone else: a later `admin@…` account still becomes
  `admin2`, and nobody can rename themselves to it. Existing installs keep
  their first administrator's name.
- The sign-in form no longer suggests `admin@local` in its email box, and a
  failed request in the admin panel is never said in axios's English
  ("Request failed with status code 500", "Network Error").
- `node scripts/i18n-export.mjs --help` prints its usage instead of writing
  the catalogue into the current directory.
- The manager's listings carry `storage_info` (`[{name, read_only}]`) beside
  `storages`, filtered like the names.
- **One "Convert".** The legacy iframe converter (`FILEX_CONVERT_URL`,
  External services → Converter) is offered only when the Convert app is not
  available and the service is configured; it never appears beside the
  app's "Convert…". An administrator reads that it is being retired, and
  External services marks it as legacy.
- **Failures are said in words.** Whatever went wrong — a refused request, a
  failed app job, a viewer that could not load, the admin SMTP test, an
  update check — is now a translated sentence that says what happened and
  what to do. An administrator also sees the technical detail as a second
  line; nobody else is shown a status code, a JSON body or an environment
  variable. Ops rows of app jobs carry `error_code` / `error_engine`; the
  S3 keys endpoint answers `no_secret_key` (with `admin_hint` for an
  administrator); the SMTP test answers a `reason`.
- **Language pack limits are bytes, not a count of strings.** The
  2 000-strings-per-language cap made a complete translation impossible
  (filex has 3 593 keys). Now: 1 MiB per language, 4 MiB per manifest, 4 KiB
  per string, 128-byte keys of a fixed shape, and a 16 MiB manifest document
  that is refused as `too_large` instead of being silently truncated.
- **`GET /api/public/branding` lists added languages without their strings**
  (`ui_locales: [{code, source, plugin, rtl}]`); one language's strings are
  `GET /api/public/ui-locales/{code}`, fetched when somebody picks it.
- A language pack's language formats dates and numbers as that language
  (`localeTag`), and a regional pack (`pt-br`, `zh-hant`) keeps its tag.
- ⚠ **Custom CSS is off until you switch it on, even where a sheet already
  exists**, and its editor moved from Settings to the new Appearance screen.
  There is deliberately no grandfathering: the rules changed underneath an
  existing sheet — it no longer applies to the sign-in page, and `url()` no
  longer fetches anything — so continuing to apply it silently would be
  applying something the operator never approved. **Anyone upgrading with a
  custom stylesheet in use must re-enable it.**
- ⚠ `custom_css` is **removed** from `GET /api/branding`, not emptied. It used
  to ride that public payload, so it reached anonymous visitors and the very
  screen that edits it; it is now served from `GET /api/me/custom-css` behind
  authentication. The field is deleted rather than blanked so an old client
  fails loudly instead of quietly rendering nothing.
- The palette a person picks is now recorded explicitly, including the stock
  one. "Has never chosen" is the absence of the setting, which is what an
  instance default paints over — so deliberately choosing filex's own colours
  is an answer that sticks, instead of being overwritten on every page load.
- ⚠ **Upgrading with symlinks already inside a `local` storage: three things
  change on the next scan.** (1) A link pointing **inside** the folder starts
  being catalogued as what it points at, so a linked directory gains rows for
  its contents — files that were always in the storage but were listed as one
  0-byte entry. (2) A link pointing **outside** stops being catalogued as a
  file: it is typed `symlink`, which means it is no longer content-indexed,
  virus-scanned, versioned or counted against quota. If you were relying on
  that content being part of the storage, set `follow_symlinks` on the storage
  and it returns, deliberately this time. (3) Rows created by an **older**
  version for out-of-root links keep their old `file` type — the type is
  written when a row is created and nothing rewrites it — so until such a row
  is re-catalogued its scans will now fail against the driver's refusal and
  terminate after the queue's three attempts. Removing the link, or turning
  `follow_symlinks` on, settles it either way.
- ⚠ **A folder copy now skips symlinks it may not follow** instead of copying
  the bytes behind them — inside a `local` storage and to another storage
  alike. One unfollowable link no longer costs the operator the rest of the
  folder: inside a storage a *directory* link used to kill the copy partway
  through (it tried to read a directory as a file), and to another storage a
  single broken or out-of-root link failed the whole transfer. Inside one
  storage a contained directory link is skipped rather than recursed into; to
  another storage it is carried as the folder it points at, under the cycle
  guard below. ⚠ The cross-storage copy **says what it left out**: the op ends
  `partial`, its message naming each entry and why (*a broken link*, *a link
  pointing outside the storage*, …), and a cross-storage **move keeps its
  source** when anything was left out — it deletes only what it carried. The
  agent surface answers the same with `entry.source_kept` and
  `entry.left_behind` ([MCP.md](docs/MCP.md#links-that-cannot-travel)).
- The recursive walks that follow links — catalogue scan, collision scan,
  transfer size estimate and the cross-storage transfer itself — now carry a
  cycle guard keyed on the link's resolved target, plus a hard depth cap of
  64. This is the prerequisite for the navigability above, not an
  afterthought: a root containing `real/cycle -> root` was
  measured reaching depth 81 (123 listings) on Linux and depth 127 (192) on
  Windows before the operating system stopped it, re-cataloguing the same
  subtree under a fresh path hash every time.
- `sftp` reports a remote symlink as a symlink. It used to call every
  non-directory a file, so a remote **directory** link was offered as a
  downloadable file that could not be downloaded. It is still not resolved to
  its target: filex's boundary on a remote host is the SSH account's own
  permissions, so there is no in-root test to make and following one would
  walk the catalogue off the configured root.

- **One table, one row control, across the admin panel.** Every admin table
  freezes its first column on the left and its actions on the right, the way
  the explorer's list does, and every row ends in one pinned **Actions** menu
  holding everything the row can do — the same menu component the explorer's ⋮
  opens. The loose per-row buttons are gone; anything that automates the admin
  panel by clicking them now opens the row's menu and picks the entry by its
  words.
- A plugin's settings are drawn by the one field mapper core uses everywhere:
  a `select` is a row of buttons, `multi` is honoured, and a boolean field's
  `style` is read (a switch or a two-button choice).
- One language per flow: a job's message, the page around it and the API
  client all follow the language on screen, and switching language mirrors the
  choice onto the account, so server-rendered text agrees with the interface.
- ⚠ **Notifications and webhooks: filex's own directories are no longer
  announced, and a soft delete targets the Trash view.** A new target kind,
  `trash` (`{kind: "trash", storage, path}` with the path the item was
  deleted FROM), replaces `{kind: "file", path: ".filex-trash/<key>"}` on
  `file.trashed` and on a quarantining `file.infected`; a click opens the
  Trash view with the item selected. A receiver that switched on the four old
  kinds sees an unknown one — treat it as "open the trash".
  `meta.trash_path` stays in the webhook body. Writes, moves and deletes whose
  subject is inside `.filex-trash`, `.versions`, `.thumbs` or the desktop
  app's `.filex-open` working area are bookkeeping and produce **no bell row
  and no webhook**. The one exception is a save of an open-with working copy,
  which is a change to the person's own document: it is announced under the
  document's original name, with an empty `node.path`, `target: none` and
  `meta.open_with: true` (the original lives on their computer, so no storage
  path names it). Rows recorded before this release are filtered on read —
  hidden when their body is an internal path, re-addressed to the Trash view
  when they targeted the bin — and the table itself is not rewritten.
- The WebDAV, SFTP, FTPS, NFS and S3 endpoints also refuse `.filex-open`
  (they already refused the other three): all five now judge paths by one
  shared list instead of five copies of a three-name one.
- ⚠ `POST /api/files/manager?action=delete` of a path under `.filex-trash/`
  is refused (403 `RESERVED_NAME`) instead of hard-deleting it. Removing one
  item from the bin for good is `DELETE /api/admin/trash/{id}`, which it
  always was for the web app.
- Live updates (the explorer's change frames) no longer announce anything
  inside filex's own folders or naming them: the desktop creating
  `.filex-open`, saving a working copy, a trash move or a version snapshot
  used to reach every open explorer of that storage as a change.
- **"Share", not "Share / permissions" — and one link per item.** The menu,
  the selection bar, the shortcut list and the tour name the verb for what it
  does for everybody. *People with access* stays inside the dialog for the
  item's **owner** (an editor was sent a request for the grant list, got a
  403, and saw an empty dialog); the details panel's *Manage permissions* is
  an owner's button too. On a storage with RBAC off an administrator sees
  the people form greyed, with where to switch RBAC on — it used to accept
  people under a box saying grants did not apply — and nobody else is
  offered it. With a link already on, the options' button now reads
  *Replace the link with these settings* and replaces it (new address, the
  old one stops working) instead of leaving a second live link beside the
  first; the header's link is no longer listed a second time underneath;
  every *Copy* is one control and every on/off is the switch.
- **The explorer's Trash has the facts of a deleted item.** Its table shows
  **Deleted** (when — sortable and grouped by it), **Deleted from** and
  **Time left** (`ttl_days`), instead of a date column of dashes, no
  location and an Owner column reading *System* on every row. *+ New* is no
  longer drawn above the Trash with every entry greyed.
- **The first-use tour is offered to a person once.** It opened a moment
  after every explorer mount whose browser had not closed it — a second
  tab, another browser, another device — and browser automation met its
  card on each fresh mount. It is now recorded when it is **offered**, on
  the account (`GET/PUT /api/me/prefs`, key `tour`) as well as in the
  browser, and read as one answer; *Restart the tour* still opens it any
  time.
- **A refused file-request upload says why, in words.** Every refusal of
  `POST /d/{token}` and `POST /api/public/d/{token}/upload` carries a
  `message` in the visitor's language next to the `error` code it always
  had.

### Upgrade notes

- **A script that empties the trash through the API: check `running`.**
  `POST /api/admin/trash/empty` waits up to two seconds for the purge it
  starts: an ordinary trash is done by then and the answer is the final count
  (`200`, as before), a large one answers `202` with its progress while it
  carries on as an operation of the queue — follow it on
  `GET /api/admin/trash/empty` (or `GET /api/files/ops/{op_id}`) until
  `running` is false. A second press while the same tenant's run goes on is
  `409 BUSY`, with that run. A day count that is not a whole number `≥ 0`, a
  `storage_id` that is not a number, or an unknown field is `400` and purges
  nothing — they used to be dropped, which meant "everything". "Empty" now
  means *what was in the trash when it was asked for*: a file deleted while
  the purge runs stays in the trash ([#47](https://github.com/BRF-Tech/filex/pull/47)).
- ⚠⚠ **ONLYOFFICE: turn JWT on at the Document Server.** filex now refuses
  a save callback that carries no token whenever a JWT secret is configured
  (see *Security*). A Document Server running with `JWT_ENABLED` off sends
  unsigned callbacks, so **its saves stop working** the moment you upgrade:
  set `JWT_ENABLED=true` and the same secret filex holds before, or
  immediately after, the upgrade. An install with no secret in filex never
  had editing on in the first place and nothing changes for it — except
  that the Panel and **External services** now say so in a red warning
  until a secret is set.
- **A reverse proxy or CDN in front of filex: let it follow filex's cache
  headers.** Every `/api` answer is now `Cache-Control: no-store` unless its
  handler says otherwise ([#41](https://github.com/BRF-Tech/filex/pull/41)) — thumbnails, file content and
  share downloads say `private, …`. The one exception is the four answers
  that say who the instance is: `/api/public/branding`, `/api/branding`,
  `/api/appearance` and `/api/public/ui-locales/{code}`, which were
  `public, max-age=60` and now carry a strong ETag with `public, no-cache`, so
  a reader revalidates and an unchanged answer costs a `304`. Nothing in filex
  needs changing; an edge rule that *ignores* origin headers ("cache
  everything") still caches — scope such a rule to your website's own hosts
  (see [docs/DOCKER.md](docs/DOCKER.md)), or it keeps one person's answers for
  everybody and an installed language pack or a saved theme takes a minute to
  appear.
- ⚠ **A narrow token can no longer mint a wide one — check your
  integrations.** A token, S3 access key, NFS export or SSH key created
  through the API now inherits the ceiling of the credential that asked for
  it: its verbs must be a subset, its `root:` confinement must lie inside
  the caller's and its expiry cannot outlive the caller's. An automation
  that used a read-only or confined token to create wider credentials will
  now get `403 token_ceiling` naming what was too wide — give that
  automation a token with the rights it actually hands out, or have a person
  create the credential in the browser. Credentials that already exist are
  untouched.
- ⚠⚠ **API/MCP tokens created with no scopes keep `admin` — review them.**
  Until this version a token created with no scope ticked received **every**
  scope, `admin` included. On upgrade, migration 00054 rewrites every such
  token to the explicit list `read,write,delete,mcp,admin`: exactly the access
  it had, so no integration breaks — and it now shows the `admin` scope on
  **Admin → API / MCP**. Review those tokens and narrow any that does not need
  the admin panel: create a token with only the scopes it needs and revoke the
  old one (a token's scopes cannot be edited). From now on every token names
  at least one scope, and `admin` is never included unless it is ticked.
- ⚠⚠ **Identity provider settings saved on the page before v0.43.0 were never
  applied — and on upgrade they are imported SWITCHED OFF, not switched on.**
  The Admin → Identity providers page used to save settings that no server
  read. Now the page's settings drive sign-in, so turning old rows on at
  upgrade could switch on a sign-in configuration someone typed long ago and
  forgot. Instead each such provider comes back **off**, marked *"saved before
  v0.43.0, never applied — review and enable"*. **Before or right after
  upgrading, open Admin → Identity providers**, review every field of a marked
  provider, use **Test now**, and switch on what you want. A client secret or
  bind password among those rows is sealed with `FILEX_SECRET_KEY`; without
  the key it is cleared (it was never used — type it again) and the log names
  the provider and field. Providers set in the environment are untouched and
  keep working exactly as before.
- The conversion engines (ffmpeg, ImageMagick, LibreOffice, Ghostscript,
  poppler, rsvg) are looked for once, when filex starts, and every screen
  reads that one answer: install an engine, then restart filex for **About**,
  **Apps** and the converter to see it.
- An app action's customised *applies* rule (**Apps → an app → Menu
  actions**) is converted once, at the first start, from the copy of the rule
  it used to be into the change it made against the manifest — keeping what
  it said (an extension you removed stays removed, one you added stays). See
  *Fixed* for what that changes.
- **Migrations 00042–00058 run on the first start** (SQLite, PostgreSQL and
  MySQL alike) — seventeen of them, because this release adds a whole
  platform: 00042–00046 the apps themselves, their pages, their signing keys,
  their file locks and the share row an app's link is; 00047 interface
  preferences per person; 00048 an app's state row bound to the file it is
  about; 00049 the sealed copy of a share's PIN that **My shares** reads
  back; 00050 the hourly wake-up schedule; 00051 the operator's own themes;
  00052 an app link's own visit counter and purpose (`shares.visit_count`,
  `shares.purpose_json`); 00053 a recorded revoke (`shares.revoked_at`);
  00054 the rewrite of empty-scope tokens (above); 00055 the split of tags
  into personal and team (below); 00056 per-reader read state for
  notifications ([#43](https://github.com/BRF-Tech/filex/pull/43), written as 00043 there); 00057 the
  identity provider's id_token kept beside an SSO session, for sign-out
  ([#40](https://github.com/BRF-Tech/filex/pull/40), written as 00042 there); and 00058 an index on
  `nodes.parent_id`, which a purge's cascading deletes need
  ([#47](https://github.com/BRF-Tech/filex/pull/47), written as 00044 there; a no-op on MySQL, which
  already has one). On a large SQLite database the first start builds that
  index before it serves.
- **Existing tags become team tags** (migration `00055`). They were
  effectively shared with everybody, so nothing anybody could see disappears;
  each lands in the tenant of the file's storage (a storage linked to two
  tenants gives each its own copy; a storage linked to none, as on a
  single-tenant install, keeps them for the whole instance). Names the old
  code lower-cased stay lower-case; new tags keep their capitals.
- **A client that does not send a kind now makes personal tags.** Every
  client before v0.43.0 posts `{node_id, tags:[names]}`; new names in such a
  request become **personal** — a request that does not say who should see a
  label must not publish it. Names it round-trips keep their kind, so an old
  client leaves existing team tags alone; one that drops a team tag from the
  list needs edit permission for that. A script that shares labels with a team
  adds `"kind": "team"` (or sends `items`).
- **What an existing desktop install needs for live sync:** a **new desktop
  build**. The engine that does the work is the `filex` CLI bundled inside the
  app (`resources/bin/filex[.exe]`), so the release must ship new desktop
  packages carrying the new CLI — a server upgrade alone changes nothing on the
  PC; the app code adds the Live / Polling / Offline word and the per-folder
  status (a folder's own error, cleared by its next clean pass; the local
  watching note).
- **Mixed versions keep working**, just not live in both directions:
  new app + older server → local saves still go up at once (file-system
  events), server-side edits arrive with the 30-second full check, and the app
  says *Polling*; older app + new server → exactly the old behaviour (30 s both
  ways) — the `watch` subscription, `tree_change` frames and `expect` are only
  used by a client that asks for them.
- A reverse proxy in front of filex must pass WebSocket upgrades for `/api/ws`
  (the web explorer's live updates already need this). Without it the app shows
  *Offline* and falls back to the 30-second full check.
- The 30-second check no longer walks every pair (see *Changed*): a quiet pair
  costs one local walk and — only while the change stream is down — one
  `action=changes` request per interval, and is walked in full every
  `--full-every` (30 min). An older server without the change log is walked
  on a timer that backs off to `--watch-max` (5 min).
- A program that relied on the `202` "preparing" answer to a download must now
  send `X-Filex-Accept-Prepare: 1`. Browser navigations are unchanged.
- `POST /api/auth/desktop/complete` refuses API tokens (browser sessions only).
  **Existing desktop pairings keep their `app` token** until the app signs in
  again (then revoke the old *filex desktop* token) or an admin sends
  `PATCH /api/admin/ai-tokens/{id} {"kind":"user"}` — see docs/DESKTOP.md.
- A rename onto a taken name now answers `409 NAME_TAKEN` instead of replacing
  it; API clients that relied on the overwrite must handle it (`filex client mv
  a b` onto an existing `b` fails the same way). Moves by an agent (MCP
  `file_move`, `POST /api/ai/move`) keep giving the item a free name instead.
- The first full scan after upgrading drops the catalogue rows an earlier scan
  minted under `.versions/` and `.thumbs/`; on a storage whose version history
  was a large share of its objects the 70% tombstone guard may trip once.
- The images start `/sbin/tini -s --` before the entrypoint; overriding the
  entrypoint removes it (docs/DOCKER.md).
- The search index is rebuilt once, in the background, on the first start
  (document schema 3: names composed and the four i's folded). Search keeps
  answering from the old index until the new one is live.
- Without the index, a PostgreSQL install searches file names through
  `normalize` (PostgreSQL 13, the documented minimum) and `lower`, which follows
  the database's locale: with the `C` locale only `A`–`Z` change case
  (docs/DATABASES.md).
- For out-of-tree `db.Store` implementations: `SearchNodes` takes a
  `model.NameMatch` (every word, the cheap runs, the word to rank by) instead
  of a LIKE pattern, `ListTrashedExpired` takes the storages to read,
  `ListNotifications` and `UnreadNotificationCount` take the hidden-body
  patterns and a `model.BroadcastFilter`, and there are new methods
  (`ListNodesUnder`, `CountLiveNodesUnder`, `ListStaleNodesUnder`,
  `ListUnstoredNodes`, `AbortUnfinishedSyncRuns`, `GetLastSyncRunByStatus`,
  `MarkBroadcastsRead`, `MarkAllBroadcastsRead`, `SetSessionIDToken`,
  `GetSessionIDToken`).

- **The first administrator's username:** new installs name it `admin`;
  existing installs keep theirs (usually `admin2`) — nothing is renamed on
  upgrade.

- ⚠ **SSO: allow filex's sign-in pages as a post-logout return address**, or
  sign-out ends on the identity provider's "invalid redirect URI" page.
  Sign-out now ends the provider's session too ([#40](https://github.com/BRF-Tech/filex/pull/40), see
  *Fixed*): allow `https://<host>/admin/login?signed_out=1` and
  `https://<host>/drive/login?signed_out=1`, or simply `https://<host>/*` — on
  Keycloak the client's *Valid post logout redirect URIs* (left empty it
  allows only the *Valid redirect URIs*, which for filex is the callback
  alone); in multi-tenant mode on every tenant's client
  ([docs/SSO.md](docs/SSO.md#signing-out)). `FILEX_OIDC_LOGOUT=local` keeps
  the old behavior. Sessions signed in before the upgrade kept no id_token
  and sign out of filex only until they expire (12 h); a provider whose
  discovery document has no `end_session_endpoint` is unaffected.
- **Who a notification reaches changed** ([#42](https://github.com/BRF-Tech/filex/pull/42), [#43](https://github.com/BRF-Tech/filex/pull/43), see
  *Security*). Queued work is now addressed to the person who asked; a
  member's bell takes only an antivirus hit, a failed upload, the admin
  page's test and an app's instance-wide notice from the broadcasts, and only
  when the file it names, if any, is one they can see; a drop or share notice
  with no owner reaches administrators only. The rows the queue wrote without
  an actor before the upgrade leave every bell — administrators' too — and
  stay in the admin history, whose Scope column now says who a broadcast
  reaches. Read state is per reader from now on; a broadcast somebody marked
  read before the upgrade stays read for everyone. In multi-tenant mode the
  notification history and the test event are supertenant-only. An
  integration that reads the bell with a folder-confined (`root:`) token now
  sees only the notices about that folder.

### Security

- ⚠⚠ **API answers are no longer stored by shared caches.** Every `/api/`
  answer now carries `Cache-Control: no-store` unless its handler sets a
  policy of its own, and the only `public` ones are the four that say who the
  instance is — branding, themes, the offered languages and a language's
  strings — which are the same for every visitor of a host and revalidate
  (`public, no-cache` + ETag). JSON answers used to carry no `Cache-Control`
  at all, and a CDN rule that caches everything took that as permission:
  measured behind Cloudflare, a zone rule written for the tenant's website,
  with no host condition, kept `GET /api/auth/me` for two hours and served one
  administrator's identity (e-mail, role) to everyone who asked, anonymous
  requests included; signing out and in as somebody else still showed the
  administrator. `no-store` also keeps a signed-out tab's back button from
  bringing the previous person back. A test walks every `GET /api` route as
  four kinds of caller and fails on a fifth `public` answer. Found and fixed
  by Berk Başarır ([#41](https://github.com/BRF-Tech/filex/pull/41)).
- ⚠⚠ **The bell no longer names files its reader cannot open.** Every queued
  copy, move and delete — and the commit of every staged upload — was written
  as a notification addressed to nobody, because the ops worker has no
  request user and the event never looked at the actor the queue row
  carries; the bell handed such a row to every account, so on an RBAC storage
  members read the names of files deleted from folders they have no grant on
  (tens of thousands of rows on one instance). Now the event is addressed to
  the person who queued the work, and who reads a broadcast is one rule, in
  SQL and then per row: a member gets an antivirus hit, a failed upload, the
  admin page's test or an app's instance-wide notice, and only when the file
  it names — if it names one — is one the explorer would list for them (the
  listing's own grant check); a notice that names a file by name alone, as an
  "open with filex" working copy does, reaches no member (the antivirus
  scanner addresses its own to the copy's owner); administrators get every
  broadcast about their tenant; operator alarms and a drop or share notice
  with no owner — it carries the link's bearer token — never reach members.
  The badge and the list's total count exactly what the reader may see.
  A token confined to one folder (`root:`) reads — and marks read — only the
  notices about files inside that folder, its owner's own included; it used to
  read its owner's whole bell. Found and fixed by Berk Başarır
  ([#42](https://github.com/BRF-Tech/filex/pull/42)); the `root:` gap was named
  in the PR and closed while it was integrated.
- ⚠⚠ **"Mark all read" marks YOUR bell read, not everybody's.** A broadcast
  had one `read_at`, and the read endpoints stamped it for whoever asked: one
  member's "mark all read" marked every broadcast on the instance read for
  every reader — another tenant's antivirus alerts, the operator's replica
  reports, alerts about folders the member cannot open — and `read` did the
  same to any id anybody typed, the AI admin tool
  `admin_notifications_mark_read` included. Read state for broadcasts is now
  per reader (migration 00056): `read-all` reads everything up to that moment
  for the caller, in one write, and `read` marks a broadcast for the caller
  only when their bell shows it, answering `204` either way. Found and fixed
  by Berk Başarır ([#43](https://github.com/BRF-Tech/filex/pull/43)).
- ⚠ **Multi-tenant: the notification history and the test event are
  supertenant-only.** `GET /api/admin/notifications` returned every tenant's
  notifications — file paths included — to the admin of any tenant, and
  `POST /api/admin/notifications/test` let a tenant admin fire deliveries at
  the instance's webhook receivers. Both answer `403 supertenant_only` to a
  tenant admin now; a tenant admin reads the tenant's own events in their
  bell, which is scoped. Found and fixed by Berk Başarır ([#42](https://github.com/BRF-Tech/filex/pull/42)).
- ⚠⚠ **Any API token could mint a full personal token through the desktop
  pairing.** `POST /api/auth/desktop/complete` accepted any API token and handed
  back a fresh `read,write,delete` token for its owner with no `root:`
  confinement — a read-only integration token could mint a full one. Found and
  fixed by Berk Başarır ([#35](https://github.com/BRF-Tech/filex/pull/35)): the route now answers
  `403 session_required` to every token and mints nothing; only a signed-in
  browser completes a pairing.
- ⚠⚠ **A credential a token mints is never wider than that token.** The
  desktop hole above was one door of a pattern: a narrow token — read-only,
  or confined to one folder with `root:` — could still create an API token,
  an S3 access key, an NFS export or an SSH key for its owner with the
  owner's full rights, and use it to reach everything the owner can reach.
  Every issuing door now measures the caller first: the verbs of what it
  mints must be a subset of the caller's, its confinement root must lie
  inside the caller's, its expiry cannot outlive the caller's, and a narrow
  caller cannot borrow a wider parent token for a key or an export. A
  refusal says `403 token_ceiling` and names what was too wide. A
  browser session is unaffected — it has no ceiling to exceed.
- ⚠⚠ **An unsigned ONLYOFFICE save callback was accepted — upgrade.** Every
  filex up to v0.42.2 with ONLYOFFICE configured is affected. The callback
  route is public and the JWT was checked only when a token was present, so
  anybody who could reach the server could have filex overwrite a file with
  bytes of their choosing. With a JWT secret configured, a callback without a
  token is now refused (a document server with JWT on always signs its
  callbacks). ⚠ A Document Server running with `JWT_ENABLED` off sends
  unsigned callbacks, so its saves now fail: turn JWT on there with the same
  secret filex holds. filex itself has never enabled ONLYOFFICE without a
  secret — a URL with no secret leaves editing off — and the admin Panel and
  External services now say so in a red warning that stays until a secret is
  set ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#1-run-the-document-server-with-a-jwt-secret)).
- ⚠⚠ **An API/MCP token with no scopes no longer means "every scope".** A
  token created on the admin screen with nothing ticked was granted
  everything, `admin` included — the form said so ("If none are selected, all
  scopes are granted") and such a token read `/api/ai/admin/users` and
  `/api/ai/admin/storages`. Now one rule covers every door that issues a
  token (the admin screen, self-service keys, the desktop sign-in): at least
  one scope is required and an empty list is refused with a `400` in the
  reader's language; `admin` is granted only when it is ticked, and the
  screen says what it grants; and the token driver reads an empty list as
  **nothing**, so a row that is empty anyway fails closed. The self-service
  door, which used to fill a default silently, refuses the same way. Existing
  tokens: see the upgrade note.
- ⚠⚠ **An app's freeze held only in the explorer.** While a signature request
  is open the signing app freezes the document and the screen promises
  nobody can change it — but only the file manager's own verbs checked the
  lock. Measured before the fix, against a frozen document: the document
  editor's save of a copy opened before the freeze overwrote it
  (`{"error":0}`); an agent's delete of the folder holding it answered
  `{"ok":true}`; an archive extracted over it replaced it; a WebDAV `DELETE`
  of its folder answered 204; FTP and SFTP sessions that were already open
  overwrote it and renamed it and its folder; NFS renamed them. (The signing
  app's own hash check caught the change before anyone signed, so no false
  signature came of it — the request died as "the document changed".) Every
  write door now asks one check (`writegate`) for both filex's own names and
  app locks: HTTP answers 423 with the app and its reason (the web app says
  it in the reader's language), WebDAV answers 423, SFTP/FTPS/NFS their
  permission error, S3 AccessDenied, the document server gets `error: 1`.
  The app holding the lock still writes its signed output; another app's
  output landing on the file is refused.
- ⚠⚠ **Anybody who could write could write into filex's own folders.** The
  explorer stopped showing `.filex-trash`, `.versions`, `.thumbs` and the
  desktop's `.filex-open`, but any editor could still create a folder called
  `.filex-trash`, upload into it, rename a document to `.versions`, copy or
  move into or out of these folders, extract an archive whose members sat
  under them, save text over a version snapshot, grant access to them or put
  a public link on `.filex-open` (other people's open documents) — and what
  landed there vanished from every view the moment it was written. The file
  manager's `delete` also **hard-deleted** anything under `.filex-trash/` for
  any editor, bypassing the rule that removing an item from the bin for good
  is an administrator's action. Every person-facing write now refuses these
  names with **403 `RESERVED_NAME`** (manager verbs, chunked and staged
  uploads, the operations queue, archives, text save, shares, grants,
  restores, app output, drop links); an archive member under one of them is
  skipped like a zip-slip entry. The WebDAV, SFTP, FTPS, NFS and S3 endpoints
  already refused them and now have tests that say so. The one exception is
  the desktop app's "open with filex" round trip, exactly as every desktop
  since 0.29.0 sends it: `newfolder` of `.filex-open` at the storage root,
  `upload` and the document editor's save of `.filex-open/<session>-<name>`,
  and `delete` of that copy.
- ⚠ **An OnlyOffice config requested without `mode` was an editing session
  for anybody who could view the file.** The document server treats a missing
  mode as edit, and the viewer downgrade only looked for the literal `edit`;
  a viewer (or a trashed file, or a version) opened that way came back
  editable, with a save callback. A missing mode is now edit before any check
  runs, so the downgrades see what the server will do.
- **Identity provider secrets are never sent back, and are sealed at rest.**
  The providers list used to return stored configuration, and a whole
  configuration saved as one blob went out with its client secret in clear
  (masked on a demo only). The OIDC client secret and the LDAP bind password
  are now stored sealed with `FILEX_SECRET_KEY` and listed only as *set*, on
  every install; a secret is never logged.
- ⚠⚠ **A `local` storage could be escaped on Windows, with no symlink and no
  special privileges.** `?action=index&path=..\other-folder` returned the
  contents of a directory outside the storage root: the driver cleaned wire
  paths with a POSIX cleaner that does not treat `\` as a separator, so the
  `..` survived it and the host's own path join then spent it on the way out.
  The listing endpoint was also the one verb with no traversal guard —
  `download` and `preview` on the identical path answered `400`. Separately,
  the root boundary was a string-prefix test, so a storage rooted at
  `…/storage1` accepted every path under `…/storage10`: with numbered roots
  that is another tenant's file names, sizes and timestamps. **Linux hosts
  were never affected by the separator half; the prefix half affected both.**
  Fixed in the driver (host-aware separator folding, and a real path-boundary
  test) and guarded again at the listing endpoint, which covers every driver
  including third-party plugin backends.
- ⚠⚠ **Symlinks leaving a `local` storage root were followed by everything,
  including recursive delete.** One link inside the folder gave read, ranged
  read, list, stat, write, mkdir, copy, move, set-mtime and `RemoveAll` over
  whatever it pointed at, on Linux and Windows alike. A folder **copy** pulled
  outside bytes *in*: the tree walk sees a link as an ordinary file and copied
  the target's contents into the storage. And the catalogue walk was handing
  those links to the antivirus scanner and the content indexer, which read the
  bytes on the other end — so out-of-root content was being scanned, indexed,
  version-tracked and quota-counted. Planting such a link needs filesystem
  access to the server (it cannot be done through filex — uploads and unzip
  both write through the storage layer), but an administrator who created one
  to share a folder was also handing filex delete rights over it. Containment
  is now enforced on every verb; see **Added → `follow_symlinks`**.
- **A folder copied or moved to another storage no longer reads through links
  its source did not resolve.** The transfer handed every non-folder entry to
  the source driver's `Read` — and on `sftp` and `ftp` that is the server
  opening the path, which follows a link wherever the SSH/FTP account can read,
  outside the configured folder. Measured with a source shaped like those
  drivers: the linked file's bytes arrived at the destination and the copy
  answered `ok`. A link the source driver reports as unresolved, broken or out
  of the root is now never opened, only named (see **Changed**); a folder link
  back into the tree used to nest 41 duplicate folders into the destination
  before the operating system stopped it.
- **The custom stylesheet can no longer be used to watch people or to lock you
  out.** It is scoped with `@scope (:root) to (.fe-css-immune)` and the
  Appearance screen removes the `<style>` element entirely while it is open, so
  the switch that turns a ruinous sheet off is always reachable — measured in a
  browser with the first guard defeated. `@import` is stripped and every
  `url()` that is not a `data:` URI or a fragment is made inert, which closes
  the CSS exfiltration classic (an attribute selector plus a background image
  reporting what a viewer is looking at). An unbalanced sheet is refused
  outright, because one stray `}` would close the scope wrapper early and let
  the rest escape it.
- **Apps: a job asked for from a public link now passes the same submit-time
  gate as one started inside filex.** That door resolved the action out of the
  app's raw manifest and checked only the read-only flag, so an outside
  visitor's press could start an action the administrator had **disabled** or
  reserved to **administrators**, or one belonging to a **stopped** app, with
  no ceiling on the parameters and without re-reading the link creator's
  access to the document — and the job then ran as that creator, under their
  name in the audit log. The action is now resolved through the registry (with
  the **creator's** admin status, never the visitor's: the job spends the
  creator's rights, and a link an administrator minted into a reserved action
  is deliberate), the creator's ACL on the document is re-read at submit
  (viewer, editor when the job writes, higher on `min_role`), the
  encrypted-folder refusal applies, and `params` are capped at the same 64 KiB
  the authenticated door uses. ⚠ **Consequence:** a link does not outlive its
  creator's access — when the person who opened it loses their grant on the
  document, the links they already sent stop working and the visitor is told,
  in one sentence and without any detail about the instance, to ask them for a
  new one.
- **Apps: disabling an account stops the public links it opened.** A public
  page whose creator's account is switched off (or deleted) reports `revoked`
  and answers **410** on every event, so an outside participant meets the
  ordinary dead-link screen instead of filling in a document nothing will
  accept — somebody who has left leaves no open door behind. ⚠ It is a
  **pause, not a demolition**: nothing about the share is rewritten, and
  re-enabling the account brings every outstanding link back. The stop is
  taken at the door rather than at the job, so the app is not called at all
  and a dead link cannot record a signature it will never finalise — and it
  covers the exposed copies and the no-JS page with it, since a closed surface
  beside a file route that still serves the document is not closed. ⚠ A page
  an app's scheduled wake-up opened has no account behind it (a `tick` runs
  with no actor) and is not affected.
- Storage plugins: a plugin installed **from a URL** is compared with the
  required sha256 (and its signature checked) **before** anything is written
  to the row or executed — a mismatched download used to run first and be
  removed afterwards.
- Storage plugins: a launched plugin no longer inherits filex's environment.
  It sees `PATH`, `HOME`, temp/locale variables (and what Windows needs) plus
  the `FILEX_PLUGIN_*` variables filex sets — never `FILEX_SECRET_KEY`, the
  database DSN or any other `FILEX_*` variable.
- Storage plugins: a plugin URL may only point at a public host (private,
  loopback and link-local targets are refused after DNS and on redirects); a
  **remote** plugin address must be `https://` unless it is on the private
  network.
- Storage plugins: the detached signature is stored beside the binary
  (`<binary>.sig`) and verified again at every start while
  `FILEX_PLUGIN_TRUSTED_KEYS` is set; a plugin installed before the keys were
  set is refused with `signature required … reinstall`.
- Storage plugins: the SDK compares the bearer token in constant time.

- **Tags were shared with every account on the server.** A tag was one
  label per file with no owner and no tenant, although the routes introduced
  it as per-user metadata: another user — and, in a multi-tenant install,
  another tenant — saw your tag names in their panel and on the file, and any
  user who could reach the file's id could remove them (reported by a tester;
  reproduced before the fix: `B GET tags/all → ["müşteri teklifi"]`, `B POST
  tags [] → 200`, bravo's panel listing alpha's tag). Tags are now personal
  or team (see Added); a tag is only ever named to someone who can see a file
  carrying it (RBAC included — the tag view and `tags?node_id=` had no ACL
  step at all), and changing a team tag needs edit permission.

### Fixed

- **The admin Notifications page's Webhook cell no longer draws over itself.**
  The "Not sent" badge and the reason beside it were two items of the
  table's flex row, and a table cell lets its items shrink below their
  content; when the reason wrapped, the badge was squeezed narrower than its
  own label and the label ran under the reason. Long reasons (Spanish,
  Arabic) made it certain, and at the default width English did it too —
  measured at 1440px: a 116px cell, the label and the reason overlapping by
  14×16px. The cell is one box now, the badge inline and the reason flowing
  after it. The table gate that should have caught it looked for a second
  line announced by a margin class; this one came from wrapping text and
  announced nothing. It now refuses any `Badge` that shares a cell with
  something beside it, which also found the same shape on the search-test
  page and the updates table.

- **A file at the root of a storage reads `/informe.pdf` in Arabic, not
  `informe.pdf/`.** A notification's body names such a file by a path of one
  segment, and the shared rule that keeps a path left to right inside a
  right-to-left line wanted two — so the leading slash took the line's
  direction and was drawn at the far end, in the bell, the browser toast and
  the admin list alike. One segment is a path now too, when its slash starts
  something (not `and/or`, `km/h`, `TCP/IP`, a date, or a slash before
  Arabic). The admin list's Webhook reason — the receiving server's own
  error text — was the one text cell never routed through that rule, and is
  now.

- **`fsnotify` mode watched the inside of the folders it meant to skip.** The
  watcher left out every folder whose own name began with a dot but still
  descended into it, so every folder inside a `.git` — and inside filex's own
  version history, `.versions/<id>/` — was watched, and changes there started
  scans. It now watches exactly what the scan walks: filex's own trees and the
  storage's scan exclusions are skipped whole, and a hidden folder the storage
  does not exclude is watched like any other. ([#44](https://github.com/BRF-Tech/filex/issues/44))
- **A connection that drops says so once, not once per request.** The page was
  right to report that the server could not be reached, and it reported it
  from every single call that got no answer — so a dropped connection filled
  the corner with copies of one sentence, and the explorer alone fires several
  a second (the listing, a thumbnail per row, the bell's 15 s poll, the
  pending-operations poll). Being offline is one fact about the page, so it is
  said once now: a quiet strip across the top while the condition lasts, which
  takes itself down the moment anything gets an answer — nothing is left to
  dismiss. **A failed write the person started still speaks for itself**: an
  upload, a rename, a save is something they are waiting on, and folding it
  into a generic "offline" would be worse than the storm. Reads are folded,
  writes are not, and the rule is shared (`lib/connection`), so the web app,
  the desktop shell and an embedded explorer behave the same. No polling was
  added and no cadence changed: recovery is noticed by the next call the page
  was going to make anyway. Measured in a browser with the server cut off:
  35 failed calls, 5 toasts on screen before and 0 after, one strip
  throughout (e2e/tests/132).

- **An app's English no longer beats filex's own translation.** An app ships
  its text in the languages its author speaks; filex has its own words for
  some of the same things — a date box's *order*, *separator* and *example*
  captions, a list's empty line, a job's progress line — and the call was
  `labelOf(appText, locale) || t(hostKey)`. `labelOf` falls back to English,
  English is truthy, so for every reader whose language the app does not
  speak the host string was unreachable: an Arabic reader, with a pack that
  translates all three captions, got three English words in the middle of an
  Arabic screen. The order of preference is now the app's text **for this
  reader**, then **filex's own**, and only then whatever other language the
  app has — the last resort, for something filex has never had a word for.

- **A public page's language reaches the document, not only the wrapper.**
  Pressing a language on a share link, a file request or an app's signing
  page turned the card around, because the shell puts `dir` on its own
  wrapper — while `<html lang>` and `<html dir>` kept what the server
  rendered. Everything that does not look at pixels was misled: a screen
  reader announces the language it is told, and hyphenation, quotation marks
  and `:lang()` rules follow the document. The cause was two owners for one
  attribute: the web app restamps `<html lang>` with ITS language whenever
  the offered languages or a pack's strings arrive, and it did so over the
  public page's choice. The public route now holds the document's language
  while it is mounted, and `dir` follows from it by the one rule that owns
  direction.

- **Signing out of an SSO session signs you out.** "Sign out" dropped filex's
  own session and nothing else. The identity provider's session stayed open,
  so with `FILEX_OIDC_AUTO_REDIRECT` the sign-in page went straight back to
  it, it issued a new code without a form, and the same account was signed in
  again about half a second later (measured on Keycloak 26) — nobody could
  switch accounts, and on a shared computer the next person got the previous
  one's files. filex now does OpenID Connect RP-Initiated Logout: the callback
  keeps the id_token with the session (migration 00057, only when the
  provider can end sessions), `POST /api/auth/logout` answers with the
  provider's end-session URL (`logout_url`, with `id_token_hint`, so Keycloak
  does not stop on "Do you want to log out?"), and the web app follows it. The
  provider sends the browser back to the sign-in page of the front door it
  came from (`/admin/login` or `/drive/login`, with `?signed_out=1`), which
  says so and does not start SSO by itself. It follows the provider the
  **Identity providers** page runs — configured or changed without a restart —
  and a session from a provider that has since been replaced signs out of
  filex only, because no provider is handed another's token. Multi-tenant:
  each tenant signs out at its own realm. `FILEX_OIDC_LOGOUT=local` keeps the
  old behavior. Found and fixed by Berk Başarır ([#40](https://github.com/BRF-Tech/filex/pull/40)).

- **A frozen file says which app is holding it, by its name.** The details
  panel's lock banner read "sign locked this file" — `sign` is the app's
  manifest name, the string that ADDRESSES it, and the one thing no other
  screen ever shows a person: the Apps list, the install review and the app's
  own page all say **e-Signature**. The lock payloads (the listing's `lock`
  and a refused write's `423`) now carry `plugin_label` beside `plugin`, the
  app's own label in every language its manifest wrote it in, and every
  surface that turns a lock into a sentence prefers it — falling back to the
  name only where the server cannot resolve one, which is an app that has
  been removed, or a server with no app runtime at all.

- **A table in a narrow pane shows its columns, not just its first one.** No
  table in filex sheds a column for want of room — it stays whole and scrolls
  sideways — but two things decided what a person saw *before* scrolling, and
  both were sized for a desktop. The lead column stopped shrinking at 240px,
  which in a 265px details panel is the entire pane; and the trailing
  `Actions` control is pinned to the end edge with an opaque ground, so at
  104px it painted over what little was left. An app's signer list in the
  inspector therefore showed names, Actions buttons, and a blank gap where
  its *Waiting / Invited / Opened it / Signed / Refused* column should have
  been. The lead now gives way to its own minimum once the other columns have
  given all they can, and a control is frozen only while it leaves the
  columns sliding under it somewhere to be — the same rule the frozen lead
  has always had, for the same reason. Nothing is shed at any width; a table
  that somebody has sized keeps the widths they chose.

- **A table cannot loop on its own scrollbars.** Where a scrollbar takes room
  (Windows, or macOS set to "always show"), a table that lays itself out
  against its pane's width can move its own container: it overflows by a pixel,
  the scrollbars come, the pane shrinks, the table fits, the scrollbars go —
  every frame. A field report blamed that for a 12-file folder in list view
  that froze Edge and Opera and ran a Chrome tab out of memory on a Windows PC.
  `.fe-list`, the scroll box of every table in filex, now reserves the vertical
  scrollbar's room (`scrollbar-gutter: stable`), so a table's width no longer
  depends on its own scrollbar and no width rule can close that loop. The
  shipped table was measured not to loop on v0.42.2 or v0.43.0 — its widths
  round to the pixel the box snaps to — but a table given a width rule with a
  threshold in it loops exactly as reported without the reserve and settles
  with it (e2e 139 measures both, with real scrollbars). The PR's second layer,
  a filter that held any width coming back within 500 ms at the narrower of
  the two, was taken back out: a real resize that came straight back — a panel
  opened and shut — left the table 300 px short of its pane. Found by Berk
  Başarır ([#39](https://github.com/BRF-Tech/filex/pull/39)).

- **The operations list no longer ships every path of every op.** `GET
  /api/files/ops` returns the newest 200 rows, and a row kept every path it
  was given: a bulk delete queued in batches left rows of up to 82 KB, and 200
  of them made an 11.5 MB answer that the explorer downloaded and parsed on
  every mount — and every 2 s while a copy, move or delete ran. A list row now
  carries the first 5 sources (`sources_truncated` when there were more),
  `source_count`, and `source_dir`, the folder a delete came from, which the
  operations center already knew how to show. `GET /api/files/ops/{id}` still
  returns every source. Found and fixed by Berk Başarır
  ([#37](https://github.com/BRF-Tech/filex/pull/37)).

- **Pages cached at full size are scaled down.** Before 0.41.0 PDF and office
  thumbnails were written at page size (794×1123 for A4, 100–400 KB), and the
  fix in 0.41.0 changed only new renders, so an upgraded install kept serving
  every older page at full size — on one install 14,706 of 43,467 thumbnails,
  1.68 GB of a 2.2 GB cache, and ~3.6 MB of decoded image per card in the
  browser for a folder of office documents. Once per boot the thumbnail sweeper
  now rewrites any cached thumbnail wider than 320 px at the size a new render
  gets; a portrait video frame, 320 px wide, is left alone. Nothing is deleted
  or regenerated, it runs in the background, and `FILEX_THUMBS_SWEEP_INTERVAL=0`
  turns it off with the sweep. Found and fixed by Berk Başarır
  ([#37](https://github.com/BRF-Tech/filex/pull/37)).

- ⚠ **A named pipe under a local storage no longer hangs its scan** (#38).
  The local driver took everything that was not a folder or a symlink for a
  file and opened it to read its type — and opening a named pipe (FIFO) nobody
  writes to never returns. One `mkfifo` anywhere under the root, which Docker
  overlay directories are full of, left the storage scan `running` with
  nothing processed, and every scan after it queued behind the hung one. filex
  now serves regular files and folders only: a named pipe, socket or device is
  skipped by the scan and by every copy, never opened by a read, a write or a
  thumbnail, refused as a write target, and reported in the server log once
  per entry. Every open goes through a check that cannot itself block. The
  `sftp` driver skips them the same way, and `filex upload <folder>` reports
  and skips them (`skipped_special` in `--json`).

- **A failed desktop sign-in no longer throws you back to the server address**
  (#36). The waiting screen — the address to copy and the box to paste the
  browser's code into — lived only in the sign-in page, and the page took a
  pending attempt back only on *Reconnect*. A sign-in link that failed (one
  from an earlier attempt, or a code the server refused) re-opened the window,
  and so did clicking the tray or Dock icon or starting the app again: each
  reloaded the page onto the server form, with the attempt still pending behind
  it and no code box to finish it in. The main process now says what the window
  shows, so every reload comes back on the waiting screen of the same attempt
  with the reason written on it; a refused code — which the server has already
  used up — offers **Start again in the browser** for the same server; and
  **Cancel** is the one way back to the server address. Server error bodies are
  shown as their sentence, not their JSON.

- **An installed language pack is offered on the next page load.** The
  offered-language list rides `/api/public/branding`, which was served
  `Cache-Control: public, max-age=60`: an administrator installed a pack,
  reloaded, and the picker still showed two languages — for up to a minute,
  with nothing on screen to say why, which reads as a broken install. That
  answer and its three siblings (`/api/branding`, `/api/appearance`,
  `/api/public/ui-locales/{code}`) now carry a strong **ETag** over the body
  and `no-cache`, so a reader revalidates instead of holding a copy. They keep
  their caching benefit: an unchanged answer is a `304` with no body, which
  matters most for the one that is ~300 KB — a complete language's strings,
  which also stopped being yesterday's after a pack upgrade. The tag is the
  body's hash, so it moves for an app installed, removed or upgraded, a theme
  saved or a logo changed, with no version stamp for anybody to forget; and
  the answers gained the `Vary: Accept-Language` they were missing while they
  were cached.

- **A sentence the SERVER wrote is isolated in Arabic too.** `useLocale().t`
  and the admin panel's post-translation hook isolate a machine run inside a
  right-to-left sentence; neither sees the other half of what a person reads —
  a `server.*` message arriving as the `message` of a refusal, an installed
  app's own words, a notification composed out of a row — and that half is the
  half full of paths, URLs, commands and token syntax. Measured against the
  Arabic pack: the API/MCP page drew `server.token.scope_unknown`, which names
  `root:<storage>://<folder>`, with its closing `>` at the far left of the run
  and mirrored into `<`, so the line read `…<root:<storage>://<folder`. The
  catalogue's own copy of the same sentence was right, which is what made it
  read as a broken pack — and a pack cannot fix it, because bidi controls are
  forbidden in a translation. Every one of those paths now goes through one
  function (`foreignText`): the failures lib/errorWords says (the reader's
  direction rides on the translator), the panel's `extractError`, the
  connection panels' `serverWords`, an app's own error text, and the bell and
  browser notification. An absolute path is isolated as well as a `word:value`
  token — measured in Chromium, its leading slash was drawn at the wrong end of
  the run. See [RTL](docs/RTL.md#mixed-direction-text).

- **The Apps table's Label cell drew itself on top of itself.** The label, the
  "Language pack" badge and the per-language coverage lines were three
  siblings of a `DataTable` cell, and a cell is a flex ROW: `ms-1` and `mt-1`
  on a sibling are margins on flex items, not a second line. The badge covered
  the label and the coverage line covered the badge, in a 180px track, at
  958px and at 1440px, in English as well as under a long German name. The
  label and the badge are one line and what the pack covers is the line under
  it; `web/tests/ui/tablePinnedActions.test.ts` now fails any `#cell-*` slot
  whose second root node carries a top or bottom margin, and
  `e2e/tests/114-language-pack.spec.ts` measures the three real boxes at three
  widths.
- **A counted sentence the server writes says the number.** Ten `server.*`
  singulars and three admin `one | other` choices still typed a literal `1`
  ("This link is valid for 1 day.", "Create 1 storage") — the exact defect the
  rest of the release fixed — so a French pack that copied them read
  *"1 fichier"* for zero and a Russian one *"1 файл"* for 21.
  `web/tests/i18n/pluralFormsCarryTheCount.test.ts` now reads all three
  catalogues, not only the explorer's.
- **A language pack's plural forms are read where the SERVER writes the
  sentence too.** Go gated plural lookup on English having a `_one` form, and
  English writes one only where English itself inflects: the wake-up report's
  "{count} scheduled / refused / beyond this window" reads the same for 1 and
  3, so a pack's Arabic dual and Russian few were shipped, validated and never
  read — while the same pack's forms worked everywhere the browser drew the
  string. The two now ask the same question: did the caller pass a number, and
  did the language write the form for it.
- **Tagged files says which storage each file is in.** The server already
  attached the storage's name to every row; the client blanked it, so the
  Storage column printed an em dash on a page whose whole job is finding one
  file across several storages.
- **Panel "Queue depth" is the job queue's backlog** (pending + running, the
  numbers the Queue page shows). It was the number of storage watchers, so it
  read 2 beside a Queue page saying 0 and 0.
- **The Search page counts files the way the Panel does** (and says how many
  folders the index holds besides); "Last built" shows when the index was
  built instead of "—" on every install.
- **Empty or invalid form fields are refused in the panel's language.** The
  browser's own bubble ("Please fill out this field.", in the browser's
  language) is replaced by a sentence under the box, on every admin form;
  nothing is sent. External services refuse an address that is not an
  http(s) URL, on the page and on the server.
- **No wire values or English on a translated admin panel:** sync states,
  queue job types (and what a job is about, instead of `{"node_id":8}`),
  update policy and reason, SMTP "None", token scope names, a user's role,
  About's program and database names and "search:", durations ("0s"),
  "Done 24h", byte units (the language's own, e.g. "Go" in French), and
  failures with no server message (axios's "Network Error" / "Request failed
  with status code …"). The server's remaining inline Turkish/English
  sentences (account and token refusals, identity-provider refusals, an
  app's wake-up tally) come from the server catalogue, so a language pack's
  language reads them too; a scan now fails on any new inline pair.
- The Panel's tiles no longer let a long value run out of the card, status
  pills no longer wrap, and a "(unknown, unknown)" build stamp is no longer
  printed after a development build's version.
- **ONLYOFFICE and draw.io no longer vanish for an hour after a page is left
  mid-load.** The capabilities snapshot is cached for an hour and read by
  everyone, but it was rebuilt on the context of whichever request asked
  first; a browser navigating away cancelled it, the list of external
  services came back empty, and that empty list was what every client saw
  until the cache expired. The rebuild no longer depends on one caller.

- ⚠⚠ **A download is only ever the file.** Every non-browser download of a big
  file (≥ `FILEX_CACHE_MIN_SIZE`) on a slow storage was answered `202
  {"state":"preparing",…}`, and filex's own sync client took any `2xx` for the
  file: the JSON was written to disk under the file's name and the next run
  uploaded it over the real one — 45 files of 70–290 MB on one deployment,
  outside any version window. Three independent layers now stand in the way:
  the server answers `202` only to a browser navigation or a client that sends
  `X-Filex-Accept-Prepare: 1` (everyone else gets the stream, and no
  preparation is started for them — this protects every client already
  installed); the CLI and the desktop app's sync, drag-out and "open with" ask
  for `Range: bytes=0-`, which no server version answers with `202`, and accept
  only a `200` or a `206` covering the whole object; and the sync engine refuses
  a body whose length is not the size the listing reported. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- ⚠⚠ **Renaming onto a name that is taken no longer destroys the file that had
  it.** Renaming `a.txt` to `b.txt` in a folder holding a `b.txt` answered 200,
  replaced b.txt's bytes and hard-deleted its row — version history, shares and
  comments with it, nothing in the trash; on an object store a folder renamed
  onto another folder's name was merged into it. A rename now answers `409
  NAME_TAKEN` and nothing moves (`503 EXISTS_CHECK_FAILED` when the backend
  cannot tell), and the explorer's dialog says so. A case-only rename still
  works on a case-insensitive disk, and `.` and `..` are refused. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- ⚠ **A rename retried with the same taken name says so again.** The dialog
  drops its error line as soon as the name is edited and re-shows it when the
  answer changes; typing the same taken name a second time produced the
  identical sentence, so nothing changed, nothing re-rendered, and Save read
  as a broken button. (Review of [#35](https://github.com/BRF-Tech/filex/pull/35).)
- ⚠ **A move that finds every candidate name taken refuses instead of
  overwriting.** The de-collision that gives a moved file ` (2)`, ` (3)`… gave
  up after its last try and returned the taken name, which the move then
  wrote over. It now answers `409 NO_FREE_NAME` and touches nothing, on
  every door — the explorer, the AI surface and MCP, cross-storage transfers
  and the app plugins. (Review of [#35](https://github.com/BRF-Tech/filex/pull/35).)
- ⚠⚠ **A file's name, typed as it is shown, finds the file.** A field report
  from an install without the index: two files in the storage, and typing their
  names found nothing. Two defects, and either one emptied the list:
  - **Names a Mac uploaded are stored decomposed.** macOS hands a filename over
    in Unicode form D — `ü` as `u` + U+0308 — and filex keeps the name as
    uploaded, while a search box sends the composed `ü`. Nothing normalised
    either side, so every query word with `ü`, `ö`, `ç`, `ş`, `ğ` or `İ` missed
    every such name — 71 388 of 169 471 on the instance that reported it — and
    the index's separator-blind copy cut such a word in two at its mark
    (`gu rel`). A query a client lower-cased the full Unicode way (`İ` → `i` +
    U+0307) matched nothing either.
  - **Without the index, only the longest word reached the database.** The
    fallback took the first 1,000 rows by name holding that one word and
    checked the others afterwards; when it was a word most files share, the
    file being looked for was past row 1,000 (rows 4 128 and 33 623 in the
    report), so the more of a name you typed, the less you found — the name
    pasted byte for byte found nothing. Every word is a condition in the
    query now, on the toolbar search, `/api/files/search` and the AI/MCP name
    search alike, and the limit counts real answers.

  Found, measured on the reporter's catalogue and fixed by Berk Başarır
  ([#46](https://github.com/BRF-Tech/filex/pull/46)). Joined with the rest of
  this release's search work into **one rule** for when a typed word is in a
  name (`internal/namefold`): composed, the dot a full lower-casing leaves on
  an `i` dropped, and the four Latin i's — `I`, `ı`, `İ`, `i` — one letter,
  which is the rule tags use. It applies to both sides of every comparison:
  the query, the index's normalised fields, the scorer, and the stored name in
  the fallback's database query — a function filex registers on SQLite,
  `normalize` + `lower` on PostgreSQL, the collation over both Unicode forms
  on MySQL. So `şubat` finds `ŞUBAT.pdf` on SQLite (whose `LIKE` folds ASCII
  only), and `kış` finds `KIŞ LİSTESİ.xlsx` and `IŞIK` finds `ışık.txt` —
  which no search path found before, with the index or without it; the
  explorer's name box folds the four i's the same way. The three-engine test
  holds SQLite, PostgreSQL and MySQL to the answer the rule gives in Go. The
  index document schema goes to 3, so an existing index is rebuilt
  automatically, in the background, on the first start.
- **An upload session row that cannot be deleted is handed to the sweeper.**
  After the bytes were stored, a failing row delete left a `committing` row
  with no staging behind it — a state the sweeper skips by design — so the
  quota reservation of a finished upload never ended. The row is demoted to
  `failed` with a line saying the file was stored and only the session could
  not be closed.
- **A folder an S3 storage cannot list right now is an error, not "not
  found".** `Stat` on a prefix swallowed the listing error and answered
  `ErrNotFound`, which reads as "the folder is gone" — a sync pass could act
  on it. The error is now returned as it is. (Review of
  [#35](https://github.com/BRF-Tech/filex/pull/35).)
- **The change log no longer announces filex's own folders.** A write into
  `.versions/`, `.thumbs/` or the trash raised a change frame that told every
  sync client to reconcile a tree it must never see; `changes` is also
  recorded in the access log under its own verb instead of the previous
  request's. (Review of [#35](https://github.com/BRF-Tech/filex/pull/35).)
- **A sync window is read digits-first and an ETA never says "60m".**
  `--window +7:00-09:00` was accepted as 7 o'clock, and a run with 59 m 30 s
  left printed "about 60m left" instead of "about 1h 0m left". (Review of
  [#35](https://github.com/BRF-Tech/filex/pull/35).)
- **The AI surface builds its search pattern like every other door.** It
  concatenated its own `%…%` instead of going through the one escaper, so a
  caller that ever handed it a raw query would have had `_` and `%` read as
  wildcards. (Review of [#35](https://github.com/BRF-Tech/filex/pull/35).)
- ⚠⚠ **"Empty trash" empties a large trash, and says so while it does.** The
  purge ran inside `POST /api/admin/trash/empty`, and a trash of tens of
  thousands of files — 61,844 after one sync incident — could not be emptied
  at all: nginx answered 504 at sixty seconds, the request's context was
  cancelled mid-batch, and the admin page, whose own client had given up at
  thirty, showed nothing; so the admin pressed again, and three purges raced
  over the same rows. The purge is an **operation of the queue** now: the
  endpoint answers within two seconds (the final count, or `202` with its
  progress), the run is in the explorer's operations centre and the admin tray
  with its counts, an administrator can stop it there or on the Trash page,
  it is its tenant's alone, it never holds the queue's worker (copies, moves,
  deletes and uploads keep running beside it), and a restart does not forget
  it — it carries on with what is left. The admin Trash page draws a progress
  strip and picks a running empty up when reopened, the explorer's trash
  banner counts it, and a second press is told about the first instead of
  starting another. A tenant's press while ANOTHER tenant's purge (or the
  nightly retention) runs waits its turn instead of being told the trash "is
  already being emptied". A narrowing the server cannot read —
  `{"storage_id":2,"older_than_days":""}`, which the page sent once its days
  box had been typed in and cleared — no longer widens the purge to every
  storage: it is `400`, and the page no longer sends it. Found and fixed by
  Berk Başarır ([#47](https://github.com/BRF-Tech/filex/pull/47)); the queue integration adds two things the background
  run needed: **nothing deleted after the empty was asked for is purged** (the
  cutoff was "now + 24 h", taken when the purge began — harmless in a request
  of seconds, but a purge of tens of minutes took with it, for good, files
  deleted by mistake while it ran), and a row that has started is finished
  before a cancellation stops the run (stopped between its storage delete and
  its database delete, it stayed in the trash with its bytes gone). New MCP
  tool `admin_trash_empty_status`. And a purge no longer reads the whole nodes
  table for every row it removes: `nodes.parent_id` cascades on delete and
  nothing indexed it on SQLite or PostgreSQL, so each hard delete scanned the
  table for children to cascade to — 300 ms a row on the install that reported
  the trash bug (231,074 nodes). On SQLite, where the store runs one
  connection, a large purge held that connection while every other request
  queued behind it: GET p50 19 ms before, 333 ms during. Migration 00058 adds
  `idx_nodes_parent_id`: 85 ms a row, the purge four times faster (1.8 to 7.1
  rows a second) and GET p50 46 ms during the same purge. MySQL already had
  the index (InnoDB creates one for a foreign key). Found and measured by Berk
  Başarır in production right after deploying this change
  ([#47](https://github.com/BRF-Tech/filex/pull/47)).

- **A cancelled operation ends in the operations centre.** A job somebody
  cancelled read as "Queued" for as long as the operations list still carried
  its row — the explorer had no word for `cancelled` and fell back to
  `pending`. It is shown as cancelled now, and not announced as done.

- **An agent's `tag:` search lists every tagged file, and `-tag:` excludes.**
  On the AI search and MCP `file_search`, a bare `tag:x` was the first 200 rows
  of the storage by name, filtered by the tag afterwards, so a tagged file that
  sorted past row 200 was not found; and `-tag:x` excluded nothing from the name
  results. A bare tag now lists the tagged files, as the web search does, and
  the filter is applied to every row by node. (Review of
  [#46](https://github.com/BRF-Tech/filex/pull/46).)
- ⚠⚠ **Emptying the trash no longer deletes a file that came back under an old
  name.** A trash entry the storage sync writes in place for a file it found gone
  keeps that file's path; purging it deleted whatever stood there again — a new
  file, or a whole folder that had come back. Only entries inside
  `.filex-trash/` delete bytes now. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- ⚠⚠ **A scan no longer catalogues `.versions/`, and cannot put version history
  in the trash.** The walk skipped only `.filex-trash/`, so a full scan minted a
  hidden row per snapshot (and for `.thumbs/`), counted in storage totals and
  indexed for search — and once unseen, a snapshot folder row went to the trash
  where it stood, and purging a trashed folder deletes its prefix: every version
  of every file. The walk now skips filex's own trees (the one list,
  `syspath`), rows an earlier scan minted there are dropped from the catalogue
  before the delete pass (the storage and `node_versions` are never touched),
  and the delete pass refuses anything inside them. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- ⚠ **A sync conflict compares the bytes before keeping two copies.** Most
  "changed in both places" were the same file — a lost baseline, a reinstalled
  client, a touched timestamp — and one 13 KB spreadsheet grew 14,724 nested
  `(server copy …) (server copy …)` copies, one every ~30 s. Identical bytes now
  settle the file. A real conflict's copy goes to the server too (create-only,
  so two machines never write theirs over each other's) and is recorded at once
  (a copy tidied away on the server is removed here instead of coming back as a
  new file), its name never nests, and a taken name gets ` (2)`. A folder on one
  side and a file on the other touches nothing. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- ⚠ **A folder with a non-ASCII name (`Müşteri`, `Çıktılar`) goes to the trash
  with its contents,** and comes back with them. `SUBSTR` was given a byte
  length where SQL counts characters, so the files stayed live under a folder
  that was gone. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- ⚠ **A staged upload is only reported `ok` once its file says `stored`.** The
  two writes that flip the row after the storage write were unchecked, and the
  staging was released regardless: one failed write left a listed, fully stored
  file that answered `503 STAGING_GONE` on every read. The writes are retried
  and must succeed before the staging goes. Files already stuck that way are
  repaired by the next scan — and a boot pass for storages nobody scans — when
  no staging session is left and the object has the committed size and is not
  older than the commit. An object store's listing no longer erases a file's
  mime type on drift. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **A paired desktop app is a person's client again.** New pairings are `user`
  tokens with the scopes their owner could mint at `/api/tokens` (`read` for a
  viewer; never `admin`), so the desktop window no longer answers `403
  app_token` on its own API keys, S3 keys, SSH keys and NFS panels, or hides
  Recent, Starred and Shared with me. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **An overwrite records the new file's etag, size and time, not the ones it
  replaced** — browser upload, text editor, file drop, WebDAV, the S3 gateway,
  SFTP, FTPS, NFS, the agent API and archive extract. The content fingerprint
  never moved, so the old words stayed searchable until the next scan, and
  clients that compare etags (the desktop's "Open with") missed edits. When the
  storage cannot be asked, the etag is left empty for the next scan to fill. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **A search without the index no longer loses its exact match.** A word
  matching more names than the search reads (1,000, or 400 per storage from the
  root) was cut alphabetically before it was ranked; exact and prefix names now
  survive the cut on every engine, and `/api/files/search` ranks its whole
  window before keeping `limit` rows. Escaped `_` and `%` match themselves on
  SQLite, which had no `ESCAPE` clause. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **The trash purge meets every row once, and never loops.** It re-read "the
  oldest 500 expired rows" on every pass and relied on the purge to empty that
  window, so a full batch of rows it skipped (another storage, another tenant)
  or failed to purge stalled it for good: emptying one storage's trash behind
  500 older rows of other storages ran until the request died, and the nightly
  retention worker, which has no deadline, would never have stopped. The
  storage and tenant narrowing is in the SQL now ([#35](https://github.com/BRF-Tech/filex/pull/35)) and the sweep walks
  the trash by id ([#47](https://github.com/BRF-Tech/filex/pull/47)): each row is read once per run, a row that will not
  purge is counted once, and a run that is cut short stops where it is and
  says so instead of failing the rest of its batch on a dead context. One
  sweep runs at a time — an admin empty and the nightly retention take turns —
  and two purges of the same row no longer release its bytes from the owner's
  quota twice. Found by Berk Başarır.
- **A storage scan that is cut short is closed as `aborted`, not left
  `running`.** Rows a stopped server left open are closed when the sync worker
  starts; the tombstone guard compares with the last run that finished `ok` (a
  failed run's ~0 "seen" had been switching the guard off); the dashboard shows
  a failed last scan as an error — it compared against a status that is never
  written. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **The images run filex under `tini`,** which reaps the orphaned helpers
  LibreOffice leaves behind: 19,110 zombies in ten days on one deployment, until
  the healthcheck could not fork and every thumbnail failed. `init: true` is no
  longer needed and harmless if kept. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **A revoked token stops the sync watcher.** `filex sync run --watch` retried a
  401 every round, forever; it now stops at the first 401 with **exit status 3**
  (every other failure exits 1). `sync run` also stops cleanly on SIGINT and
  SIGTERM, with its ledger written. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Desktop: a revoked or expired sign-in stops sync and asks to reconnect.** The
  window showed "Can't reach … / Try again" forever while the bell and the
  watcher retried every 15 and 30 seconds. The account is now marked signed out —
  no watcher, no bell, across restarts — and offers **Reconnect**, which signs in
  again for the same server and keeps the account's synced folders. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Desktop: filex no longer switches itself back on at sign-in.** Every start
  re-registered the login item, on Windows re-enabling an entry disabled in Task
  Manager. Only the Settings switch writes it now, and at startup the switch
  follows what the OS says it will do. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Desktop: signing out stops that account's sync at once, and signing in again
  restarts it with the new token** — the old watcher kept the old (often
  revoked) token in its environment. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Desktop: sync status is read in whole lines** (a line split across two pipe
  reads showed "0/0", and a Turkish file name in an error could arrive garbled),
  and the watcher's last line before it exits is no longer lost. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Desktop: the idle-time auto-update no longer stops an overnight sync;** it
  waits until the engine is between passes. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Sync no longer absorbs changes made while a pass is running.** The
  engine rebuilt its baseline after every pass from a second walk of both
  trees, so whatever changed during the pass was recorded as agreed: a browser
  save that landed mid-pass was never downloaded, a local save made mid-pass
  was never uploaded, and a failed download of a changed file was recorded as
  done (all three reproduced on the previous engine). The baseline is now built
  from what the pass did; anything that changed meanwhile stays a change for
  the next pass. The second walk is gone, so a full check lists the server tree
  once instead of twice. (Found independently in the #35 field report, where a
  nine-hour first run recorded files edited meanwhile as in step, and a later
  edit of the stale copy then overwrote the newer one.)
- **Sync never replaces or trashes a local file that changed after the pass
  looked at it**, and never trashes a folder that gained a file meanwhile; the
  next pass keeps both versions. Two conflicts on one file inside the same
  minute no longer overwrite the first one's side copy.
- **Uploads, file-drop and text-editor saves record the storage's own
  modification time** on the catalogue row (the staged path and the ONLYOFFICE
  callback already did). A row stamped with the handler's clock, or none, was
  rewritten by the next storage scan — `last_modified` changed with no byte
  changing, and a desktop client downloaded its own upload back (measured:
  `…692000` → `…692587` after one scan).
- **The sync engine's upload skips two round-trips per file**: the destination
  probe and the per-file mkdir when the folder is known to exist.
- **A sync error in the desktop app no longer outlives the problem.** It was
  copied into one field per account and never cleared, so a failure from
  minutes ago sat under every synced folder and read as a current one — far
  longer now that passes run every few seconds. An error now belongs to the
  folder it happened in and disappears on that folder's next clean pass; only
  a failure that is not about any one folder (the engine could not start, the
  token was refused) shows under all of them, until any pass completes again.
  The engine names the pair on every line it prints and ends the summary of a
  pass that had errors with `, N failed`; a skipped symlink is a `note:`, not
  an error. The engine's output is also read as whole lines now (a pipe read
  can end mid-line; each half used to be parsed on its own) and as UTF-8 across
  reads. Each folder shows its own last line instead of whatever the account's
  engine printed last.
- **The desktop app says when changes on this computer are not watched.** The
  engine used to print a `live: note` nobody saw; it now reports per folder
  (`pair-1: local: poll-only — too-large|unavailable — …`, and `local: watched`
  on recovery) and the folder's card says so in English and Turkish, next to
  the Live / Polling / Offline word.
- **The window controls paint from the product's palette.** The close button's
  hover was `#e53935` on `#fff` — a near miss of `--fe-danger` that no theme
  could reach — and the main window and the document windows now both use
  `--fe-danger` / `--fe-text-on-primary`, the pair the product's own danger
  button uses. `desktop/test/chrome-tokens.test.ts` had been red since 0.42.0
  because of it.
- **Desktop test runs no longer register the checkout as the `filex://`
  handler.** Under `FILEX_NO_BROWSER=1` (every desktop E2E suite) the app
  registers nothing with the OS; a suite run used to point the user's real
  deep-link handler at `electron.exe <checkout>` until the installed app
  restarted.
- The server no longer dies with `fatal error: concurrent map writes` when a
  storage is attached while thumbnails are being generated (the content
  indexer resolving a storage for the first time and a thumbnail job reading
  the same map). A full end-to-end run hit it half-way through; the map is
  now guarded, and a race test holds it.
- **An install a closed tab could break for good.** The server compiles a
  module before it answers; the admin page gave up at 30 s, and the cancelled
  request aborted the compile (a good module answered `describe_mismatch`) AND
  the cleanup — leaving a row no list showed, and every later install refused
  as `name_taken` until a restart. Measured 2026-09-21 with the 20 MB signing
  module on a busy machine (29 s). An install or upgrade now finishes, or
  undoes itself, whatever the client does, and the admin page waits up to
  180 s for one.
- **Apps: `cache`, `spool`, `public` and `assets` are not app names** — they
  are the host's own directories beside the apps', and uninstalling an app
  named after one would have removed everybody's.

- **Placing and dragging a signing box no longer flickers, and a dragged box
  stays under the pointer.** Every `change` answer re-sent the surface with a
  new `src` object, and a deep watcher reloaded the whole document on each —
  "Loading…", every canvas thrown away — after every placement; a drag in
  progress when it landed kept a detached page element, whose 0×0 rectangle
  read every pointer position as the far corner, and the box jumped to the
  document's edge (measured: box centre at the page's bottom-right while the
  pointer was 200 px away). The document now reloads only for a different
  document, a drag measures the live page and ignores a page that cannot be
  measured, a seed that arrives mid-drag waits for the drag, the place step's
  hint and controls share one strip of constant height (choosing a box
  re-fitted a `page`-layout document from 383 to 349 px wide mid-gesture),
  the fit is held still while a box is dragged, and a page is redrawn into a
  fresh canvas swapped in when ready rather than cleared first. A document
  loaded while the wizard was still on its define step is drawn when the
  place step shows it (that had only ever worked because of the reload).
- **"Anyone" shows as chosen.** A box that belongs to anyone left all three
  owner buttons unpressed: the choice control reads an empty value as
  "nothing picked".
- **The outside signer's page looks like the product's share page.** An app
  page's exposed files are drawn the way a shared file is (name, size, a
  Download button) instead of under a "Documents" heading, and a document in
  an app's screen is fitted to the window inside the card — the approve step
  showed the top 60 % of the page and none of the signer's boxes.
- **The desktop-app chip never stands on something to press — on any page.**
  At 1366×768 it hid the signing page's final "Sign" button completely, and
  only the sign-in page had been guarded. The chip now asks what is under it
  (`lib/keepClear.ts`): nothing → its corner; a bar pinned to the edge → on
  top of that bar; a control in the page's flow, or a phone-wide chip →
  it steps aside until the spot is clear (the offer stays in Settings). The
  sign-in page's full card also keeps off the version line under the form.
- **Opening an app's page is not a download.** An app page's visits have a
  counter of their own (`shares.visit_count`, migration 00052, backfilled
  from the counter they used to share) — what `max_visits` caps and what
  the app reads as `page.visits` — and `download_count` counts only a file
  the page exposed being TAKEN (`…/file/<ref>?download=1`, the Download
  button and the no-JS page's link; the page's own viewer is not counted).
  A signing link that had only been looked at said "İndirme 2" in My shares.
- A language an app added joined the picker and then **spoke English**: the
  server sent its strings as a map, the browser read a list. The two ends now
  agree and the browser tests read the server's own fixtures.
- A chosen pack language was **lost on every reload** — each source of the
  choice (this browser, the account, the instance default) was judged before
  the offered list had arrived. The choice is held until the list lands.
- The language pickers offered only English and Turkish, and the explorer
  (`resolveLocale`) turned any other language into the browser's — so a pack
  translated the panel but not the file browser inside it.
- Strings no language pack could reach are now in the catalogues: the admin
  footer, the queue and sync status values, labels built once when a page
  opened (Appearance, the role and state filters, the theme menu — they kept
  the opening language after a switch), hard-coded template text and
  accessible names, and ~100 inline English/Turkish pairs in the share dialog
  and the explorer, which printed **Turkish** under any third language.
- A share-link mail composed in a pack's language (or any third language)
  arrived **in Turkish** — the `else` of an English/Turkish pair — and so did
  the drop notice to a Spanish owner, the new account an admin invited from a
  Spanish screen (its language forced to Turkish), and every app mail's
  footer arrived in English. An unknown language is now English, never
  Turkish.
- An app was told `en` for every language but Turkish (`normLang`), so a
  Spanish reader's signing wizard spoke English even from an app that ships
  Spanish. An app's call now carries the reader's real language by the one
  rule the screens and the server's text use (a region kept: `pt-br`), and
  `Text.Get` falls back per string to the base language, then English.
- An uploader's name kept only ASCII and Turkish letters: "Lucía" reached the
  folder owner as "Luca" and an Arabic name as nothing ("anon"); a long
  Turkish name could be cut inside a letter. Letters of any script are kept,
  cut by characters.
- Three numbers were printed with a separately translated word after them
  (a storage's file count, a trashed item's days left, the free operations in
  Usage), which no translation could order or inflect ("1 days"). Each is one
  message with the count in it and plural forms.
- A non-ASCII mail subject went out as raw UTF-8 in the header; it is now
  RFC 2047 encoded, and a line break in a subject can no longer add headers.
- The translator's catalogue named the last release (*filex 0.42.2* on the
  v0.43.0 branch): it now names the build — `FILEX_VERSION`, the CI tag, and
  a running server rewrites it to its own version when it serves the file.
- The validator warned when a singular form left the count out, so Arabic
  could not write *يوم واحد* without a digit; a category that holds one
  number may now say it as a word (and one that holds several, like Russian
  `one`, must keep it).
- Turkish: the pending-operations progress line printed `97` instead of `%97`
  (`%{percent}` is vue-i18n's modulo form and swallows the sign); three
  storage help texts differed between the admin panel and the explorer; the
  SMB share hint showed `\nas\media` with one backslash in the admin panel.
- Storage plugins: an upgrade uploaded under a different file name now rolls
  back correctly — the file keeps the name it was installed under, so the
  backup and the rollback always refer to the same path; the previous
  signature is restored with the previous binary.
- Storage plugins: a binary that fails to start ten times in a row is no
  longer restarted forever — it is marked failed with the reason, and
  **Restart** begins a fresh count.
- Storage plugins: success bodies from a plugin are bounded (64 MiB for a
  listing, 1 MiB otherwise); listing entries are normalised (name from the
  path, `.`/`..`/root entries dropped, negative sizes clamped); network
  errors are classified by type rather than by message text; a database row
  whose name or binary would leave the plugins directory is refused rather
  than started or removed.
- **An agent's move no longer destroys a file that is already at the
  destination.** `file_move` (MCP) and `POST /api/ai/move` handed the
  destination straight to the driver — and a driver's move onto an occupied
  path replaces what is there, with no trash copy, so "move this into that
  folder" silently deleted the file already carrying that name. Both arms now
  de-collide like every other move in filex: the item lands on a free name
  beside it (`rapor-copy.txt`), nothing is overwritten, and a move onto an
  item's own path stays a no-op. The answer, the catalogue row and the
  `file.moved` / `file.uploaded` events all name the path the file really
  landed on, never the one that was requested.
- A move's answer now reports what it moved: `entry.type` is `"dir"` for a
  folder instead of the `"file"` it always claimed, so an agent no longer
  reads "file" and calls `file_read` on a directory.
- The app detail drawer showed an installed app as blank — no name, version or
  source, dates as dashes, *unsigned* for a signed app — because the client
  read the endpoint's envelope as a flat object.
- The install wizard's permission review printed every reason as raw
  `{"en": …, "tr": …}`, both languages at once: the server sends an app's
  words as an object of languages and the client typed the field as a plain
  string. Reasons are read in the reader's language now, and so is every other
  localised field of the Apps screens — the detail's `permissions` had the same
  fault and held the review rows where the permission ids belonged. The web
  client's tests read the server's own answers
  (`backend/internal/api/handlers/testdata/wire/`, written and checked by a Go
  test), because both faults hid behind fixtures typed the client's way.
- **Every explorer dialog closes on Escape and on a click outside it, and
  takes the focus.** The converter's dialog — the frame every app screen opens
  in — did none of the three, while the share dialog closed on Escape: a
  dialog created already open never wired its keys or its focus, and no
  dialog closed on an outside click at all (an absent `closeOnBackdrop` was
  read as `false`). Only the dialog in front answers Escape; a text selection
  dragged onto the backdrop does not close anything; the standalone editor
  still never closes on Escape. An app screen that brings its own buttons no
  longer gets a second "Close" beside its "Cancel".
- **About said ImageMagick was there when Apps and the converter said it was
  not.** On Windows `convert` is `C:\Windows\System32\convert.exe`, the disk
  converter. There is one engine probe now, read by every screen: `magick`
  first, nothing under the Windows system directory ever counts, and a
  `convert` elsewhere must say "ImageMagick" in its version banner.
- **An app's details are a page of their own** (`/admin/plugins/apps/<name>`,
  Back works), in sections, instead of a dialog holding settings, three tables
  and a live log. What the app was allowed to do is said in words (the
  sentences the install review showed), not as `engines:libreoffice` chips;
  its hidden actions (the signer's `apply`) are no longer offered as switches
  — a hidden action is not a person's to switch off, and the server neither
  stores nor honours an override for one; the log's times are in the
  product's date format.
- **A customised menu rule no longer freezes the app's rule.** Changing which
  files an app's action is offered on stored a COPY of the whole rule, which
  replaced the manifest's from then on: extensions offered only while an
  engine is on the server (an office file for signing, while LibreOffice is
  there) stayed out once LibreOffice was installed, or — copied flat — stayed
  in after it was removed; the manifest's own conditions, which the editor
  never showed, were dropped (a customised **Sign…** was offered on files with
  no request open); and an upgrade's new extensions never reached the action.
  The change is stored now — added and removed extensions and MIME types, and
  what was set differently — and applied at every read to the rule the app
  declares and the engines present at that moment. Keeping only extensions
  whose engine is missing offers the action on nothing, not on every file.
- **The install review says what the dry run already knows**: that an app of
  the same name is installed (with **Upgrade it instead**, from the same
  source), and which engines it needs that this server lacks. A repository
  that cannot be fetched is explained in the reader's language with what to
  check — not `filex-app.json not found in … http 404 from
  raw.githubusercontent.com` — and a tab's error no longer follows you onto
  the other tabs. A GitHub repository is a "repo" in Turkish, not a "depo"
  (which is a storage everywhere else).
- **The profile no longer saves an address that is not one**, nor reports an
  address that belongs to another account as saved: the e-mail and the
  username are checked while you type and again by the server before anything
  is written, and a refusal is said under the box, in words — no more
  `invalid username: 'ş' is not allowed …`. Adding a user checks the address
  the same way.
- **"Test now" on an identity provider tests it for real.** It answered "OK"
  for anything, including an LDAP entry with no address. LDAP now connects,
  binds with the service account and reads the base DN; OIDC fetches the
  discovery document, checks the issuer and its endpoints and asks the token
  endpoint whether it knows the client; the proxy header checks the trusted
  ranges and whether your own request came through one. Each step is listed
  with what it reached or why it failed, and what cannot be checked without a
  real sign-in (an OIDC redirect address, a client without a secret) says so.
  Local accounts and API tokens have nothing to test and no button. LDAP with
  StartTLS and no `ca_file` could never sign anyone in (the TLS layer was
  handed no server name); it can now.
- **Add user and New webhook say what they need before sending**: required
  fields are marked, an empty or malformed field is refused in the dialog, and
  what the server still refuses is shown inside the dialog. Toasts are drawn
  above any open dialog instead of behind its backdrop. Enter in a box of the
  Add user or New token dialog submits it (their buttons sit outside the form,
  and a form of several fields ignored Enter).

- **An outside signer's "See and approve" step shows the document again.**
  The page asks for the copy the app exposed as `/file/pub%3A0` — a client is
  right to encode a path segment — and chi hands a route parameter back still
  encoded whenever the escaping differs from Go's own, so the server looked
  for a copy literally named `pub%3A0` and answered 404 ("The document could
  not be loaded"), while `/file/pub:0` answered 200. Every parameter of the
  public link surfaces (`/api/public/*`, `/s/*`, `/d/*`) is now read through
  one decoding reader. The same fault answered 404 for a file in a shared
  folder whose name carries `&`, `,`, `;`, `=`, `:`, `@`, `+` or `$` — through
  the SPA's download link and through the no-JS page's own links alike; those
  open now too.
- **A signature request — or a share — on a file that is on the storage but
  not yet in the catalogue works.** The explorer lists such a file (it reads
  the storage when the catalogue has nothing yet), but a share points at a
  catalogue row, so the signing app's request failed with "Could not open the
  signing link…: not_found: no such file" and the Share dialog answered 404
  "file not found". Both now record the file in the catalogue first, exactly
  as a write through filex would — for an app, only a file its job was handed;
  for the dialog, only after the tenant, folder and editor checks, so a
  person who could not share the file writes nothing.
- **An app is told how long its links can live.** `share_create` clamps every
  link to the installation's share ceiling (Protection, 7 days by default),
  silently, so the e-Signature app offered 14-day links and said so in its
  review while the links lived 7. Every call an app gets now carries
  `share_max_ttl_days`, read from the setting the clamp reads.
- **Revoking or deleting an app's link on the Shares screen reaches the app.**
  A signing link revoked under My shares or Shares stopped working while its
  signature request stayed open for ever. The host now brings the app's
  hourly wake-up forward to a few seconds from now, and the app asks after
  its links (`share_state`; `pluginkit.ShareInfo`, `pluginkit.IsNotFound`) —
  no event, no second channel. The e-Signature app closes the request, names
  the ended link and releases the document.
- **filex's internal folders no longer surface anywhere a person looks.**
  Clicking a "Moved to trash" notification opened `.filex-trash` itself (an
  empty folder, nothing to restore), and a desktop "open with filex" edit left
  three notifications — "New file", "File changed", "Moved to trash" — each
  naming `a1b2c3d4e5f6-Bütçe Özeti.xlsx` in `/.filex-open/…` and opening
  `.filex-open` or `.filex-trash` when clicked. The same working copies were
  returned by the search box and the global search and listed in the Trash;
  the listing API returned `.filex-open` at every storage root (only the
  explorer's own filter hid it); the AI/MCP `file_list` and zip, the public
  share page and every protocol endpoint showed it outright, the first three
  showed `.versions` too, and the share ZIP packed it. Now the server never
  lists, searches, shares, archives or offers any of them; the trash, version
  and thumbnail trees are refused by path (404) on the file API,
  `/api/files/read` and `/api/files/stat`; and the explorer never lands inside
  one — a stale link or a typed address opens the storage instead.
  `.filex-open` itself is still served by exact path, because the desktop app
  reads its working copies that way. Version history is no longer kept for
  working copies, and a thumbnail backfill skips all of these folders.
- **Notifications, release-candidate sweep.** The "filex X is available"
  notice — and every other operator alarm (replica, quota, queue, sign-in and
  disk alarms) — reaches administrators only; a plain user's bell and badge
  no longer carry them (the row, the admin list and the webhook are
  unchanged). An app's notice names the app the way people know it ("İmzalar:
  …", from the manifest's label; new `meta.plugin_label_en`/`_tr`) instead of
  its install id ("sign: …"). The admin **Notifications** page names the
  person a row belongs to instead of `user #2`, says what kind of event each
  row is in the reader's language instead of `share.created`, localises the
  delivery state ("Gönderilmedi — tanımlı webhook yok" rather than
  "skipped— no webhook URL configured"), and counts the unread rows of its own
  list rather than the reader's bell. The default webhook moved to the
  **Webhooks** page, beside the targets, so there is one place to set where
  events go. The notification settings offer a switch only for events that
  can happen here (no virus switch with scanning off, no escrow switch
  without an escrow key, no app switch with apps off).
- **A read-only drive says so to everybody, and its menu offers only what
  can work there.** A non-administrator now sees "Read-only" on the drive
  (the flag came only from the admin storage list, which they cannot read,
  and the web app asked for it — and got a 403 — on every page load; it no
  longer asks). An app action that writes its result (a new file or a new
  version) is not offered on a read-only drive, and a read-only refusal from
  anywhere reads "This storage is read-only" instead of "Already exists /
  conflict" or "You are not allowed to do this".
- **Opening a document in its own tab keeps a non-administrator under
  `/drive/`** instead of sending them to an `/admin/files/edit` address.
- **A revoked link reads as revoked.** My shares said *Expired* for a link
  its owner had just revoked, and Shares showed it as "expires … 9 seconds
  ago" (revoking works by ending the link's expiry, and nothing recorded
  who ended it). Links revoked from now on say *Revoked* on both screens
  (migration 00053, `revoked_at`); earlier revokes still read as expired.
- **The revoke dialog asks instead of announcing.** Its title was the
  success message ("Share revoked" over "Revoke this share?"), and in
  Turkish both buttons said *iptal*. The title is now the question and the
  buttons the two answers — *Cancel* / *Revoke share* (*Vazgeç* /
  *Paylaşımı iptal et*).
- **Recent, Starred, tags and Home no longer ask for thumbnails that do not
  exist.** Every file on those views was sent a thumbnail request, and each
  docx, note or diagram answered "not ready" — ten requests, ten failures,
  on one Recent view. They now ask only where the server has rendered one.
- A regular user pressing *Create key* on a server with no encryption key
  was told to set an environment variable; they are now told the server
  cannot issue keys yet and an administrator has to set it up.
- **The public drop page asks the name its link asks for, and refuses a file
  before sending it.** *Ask uploader name* is on by default and the page had
  no field, so every submission folder arrived as `<date>_anon`. A type,
  size or count the link does not take was sent anyway and came back as
  "The app returned an error" — the app-plugin runtime's sentence; it is
  now refused on the page with the reason, and the rest of the drop still
  goes. One drop is one request again — it was one request, and one
  submission folder, per file, which also let a drop past the
  per-submission file cap.
- **A phone's row menu no longer lists keyboard shortcuts** ("Enter",
  "Ctrl+X", "F2"…) — on any touch-first screen, for every menu.
- **Home's storage cards say *read-only* on a line of their own**, the side
  panel's tag. It was glued to the size in a fixed-width caption, and in
  German the ellipsis ate exactly that part. A grid card's caption carries
  its whole text on hover, for the same reason (German dates lost their
  year).
- **An explorer plural form says the number, not "1".** The English and
  Turkish singular forms were written with a literal *1* ("1 item"); under
  a language whose *one* category also covers 0 (French) or 21 (Russian),
  a pack translated from them printed "1 élément" for an empty folder.
  They carry the count now (`{n} item`) — language packs pick this up at
  their next sync.
- The share dialog's "Create a link, then it will be sent to … ." no longer
  has a space before its full stop: it is one sentence with the address in
  it, which a language can also order its own way.
- **Searching a time zone by its offset works in every language.** "+3" or
  "UTC+3" found nothing under French ("UTC+3") or Arabic ("غرينتش+3"): the
  aliases were parsed out of the displayed, localised offset with a `GMT`
  pattern. They now come from the offset as a number.
- **An app's notification speaks the reader's language.** A notice kept
  only its English and Turkish, so a German reader of the e-Signature app —
  which ships German — got "admin@local asks you to sign a document" under
  "e-Signature:". Every language the app wrote is kept (at most 16 beyond
  English and Turkish), and the bell, the browser notification and the
  desktop use the reader's own, then its base language, then English.
- **The file menu no longer offers an app action the person could only be
  refused.** It never read what an action needs: a person with *viewer* on
  an RBAC storage was offered *Convert…*, *Sign…* and *Request signatures…*
  there, and each answered "insufficient permission". The menu now asks the
  level the server asks — *viewer* to read, *editor* to write the result
  back, and the action's own `min_role` — except on a file the same app has
  locked (its signers keep *Sign / Fill* on a document frozen for signing).
- **An app's page names storages and engines the way people read them**: a
  locked file's storage by its name (not "Storage #1"), and engines as
  *LibreOffice*, *librsvg* rather than `libreoffice`, `rsvg`.
- **The converter tells only an administrator which engines the server
  lacks** (filex-convert): the note and the list of formats each missing
  engine would unlock were shown to every account; everybody else now sees
  just the formats they can have.
- **An administrator is told what a missing engine would add, instead of an
  action quietly disappearing.** An app's action that needs an engine the
  server does not have was simply not in the menu, for everybody. Where the
  engine is the only thing in the way, an administrator now sees the row
  greyed with the reason ("LibreOffice is not installed on this server —
  install it on the server and restart filex"), and everyone else still sees
  nothing: the owner's rule for anything unconfigured. It is a platform
  mechanism (`gated` on the menu row), not a rule written into one app.

## [0.42.2] - 2026-09-19

A fix release for the issue 32 follow-up and three things that were wrong on
screen.

### Changed

- **Presigned URLs are off by default on S3 storages** (`disable_presign:
  true`). A presigned link hands the browser the bucket's endpoint, which on
  the LAN-only MinIO most self-hosters run turned every share download into a
  dead link (#32). Uploads and the downloads behind public share links now
  stream through filex unless the operator sets `disable_presign: false` —
  worth it only when the endpoint is reachable from users' browsers and
  accepts SDK-signed URLs. ⚠ A storage saved without the key streams from now
  on; set `false` explicitly to get the redirect back.
- **The top search field says what it searches.** It searches the whole
  storage (every storage at the root), never the open folder, so the
  placeholder now names the storage — "Search in Photos storage" — and
  "Search all storages" on Home, the root and the Recent/Starred/Shared/tag
  views. The folder-scoped box is still the filter bar's "Filter in this
  folder…".

### Fixed

- **The context menu is the same everywhere.** On Recent, Starred, Shared with
  me, tag views, the Home cards and the Recently-opened tray a row offered only
  Open/Download/Copy/Star — no Rename, Move, Delete or Share — or, if a
  writable folder had been opened first, all of them plus a meaningless Paste.
  Rows in those views never carried their own permission level and the
  folder's level leaked in. Every listed row now carries `perm` and
  `read_only` from the server, the views forget the previous folder's level on
  entry, and the menu is built per row; only New folder/Upload/Paste stay out
  where there is no folder to put things in.
- **Admin tables keep their actions column in view.** Every admin table — and
  the token/key panels in the explorer — now scrolls sideways when it is wider
  than the page and pins the actions column to the right edge, the way the
  explorer's list view pins its ⋮ menu; the column no longer squeezes off
  screen on narrow windows. One shared stylesheet (`web/src/styles/table.css`)
  and `ui/Table.vue`'s `pinned` column option.
- **The admin Shares page copies the right link.** It built the link from the
  browser's own address, so an administrator signed in on localhost or through
  a proxy copied a link nobody else could open (#32). `GET /api/admin/shares`
  rows now carry the canonical `url` from the configured public origin — the
  same one the share dialog has always used — and the page prefers it.
- The S3 "Disable presigned URLs" help text, `STORAGE.md` and `SHARING.md` say
  the switch governs share downloads too; the minimal compose example warns
  that `http://localhost:5212` is only right on the machine running it.
- **The admin sidebar no longer opens over the page on a phone.** Below
  1024px the sidebar is a drawer with a backdrop, and it started open on every
  admin page, so the first tap went to the backdrop instead of the page. It now
  starts closed there, closes when a page is chosen, and comes back as the
  column when the window is widened.
- The search field said "Search all storages" on every admin folder: the
  scope check looked at the multi-storage *mode* (which the admin explorer is
  always in) rather than at whether a storage was open. It now names the open
  storage.

### Upgrade notes

- No migrations. S3 storages without `disable_presign` now stream downloads
  (see Changed). `GET /api/admin/shares` rows gain `url`; the recent/starred/
  tagged listing rows gain `perm` and `read_only`.

## [0.42.1] - 2026-09-19

A fix release for four reports on 0.42.0.

### Added

- **Mount several folders at once** (#31). *Storages → Add* lists the folders
  directly under the root you typed — the bucket root included — and creates
  one storage per ticked folder with the same credentials, so a bucket with
  N top-level folders is one form, not N. The bucket root itself is still
  never mounted (`ROOT_PATH_FORBIDDEN`); this is how "the whole bucket" is
  offered instead. Behind it: `POST /api/admin/storages/discover`
  `{driver, config}` → `{ok, root_key, folders:[{name, root}]}`. Every driver
  with a root field (s3, local, sftp, ftp, webdav, smb).
- **Scan every (minutes)** on the storage form (#33). The per-storage poll
  cadence (`sync_interval_s`) existed on the row and in the API and was
  reachable from no form; empty = the server default (15 min).
- **A sign when `FILEX_PUBLIC_URL` is unset** (#32). Administrators see a
  banner in the panel until it is set: every share link, file-request link
  and mailed link is otherwise built on `http://localhost:5212`. The API says
  the same as `public_url_configured` on `GET /api/files/capabilities`. The
  report had set `FILEX_APPLICATION_URL`, a variable filex has never read;
  [CONFIGURATION.md → Public URL](docs/CONFIGURATION.md#public-url) now says
  so in as many words.

### Changed

- **One sync run per storage at a time** (#33). "Scan now" while a run is
  walking starts no second full walk over the same rows — it answers **202**
  with `status: "running"`, the scan asked for being the one in progress; a
  poll tick that finds the previous run still in flight is skipped and logged
  at INFO, not counted as a failure.
- **An S3 scan is one listing, not one request per folder** (#33). The S3
  driver hands the sync worker the whole tree in a single un-delimited
  `ListObjectsV2` pass (`storage.TreeWalker`), so 150,000 objects in a few
  thousand prefixes cost ~150 calls instead of a few thousand; the walk then
  reads directories out of memory. Over two million objects the worker falls
  back to the per-directory walk. Same rows, same order guarantees, measured
  against the per-directory walk.

### Fixed

- **A read-only storage looks read-only to its users** (#30). The navigation
  panel's storage row carries a *Read-only* tag and its Home card says so; on
  such a storage the panel's **+ New** menu, the toolbar's New folder / Upload
  and the write entries of the context menu are not offered. Before, only
  the admin list had an "RO" badge and every attempt ended in the server's
  403. Sharing a read-only file is still allowed — the ACL level is unchanged,
  only the write affordances go (`@brftech/filex-core` `permCanEdit` folds the
  listing's `read_only` in, so every embed gets the same).

### Upgrade notes

- No migrations. `POST /api/admin/storages/{id}/sync` answers 202 with
  `status: "running"` (instead of `"started"`) while a run is already in
  flight; nothing is started twice.
- A client that reads `GET /api/files/capabilities` sees one more boolean,
  `public_url_configured`.

## [0.42.0] - 2026-09-16

### Changed

- **A single click selects; a double click opens (mouse).** The default open
  gesture is now the classic desktop file-manager one — a single click selects a
  row/card, a double click opens it, and **Enter** opens the selection. This
  reverses 0.41.x's "any click opens" for a mouse; it is a per-viewer setting
  (`ExplorerConfig.openTrigger: 'single' | 'double'`, default `'double'`), and
  the desktop app exposes it under **Settings → Open files with**.
  - ⚠ **Touch is untouched.** A finger tap always opens (the mobile convention,
    and there is no hover-to-select on a touchscreen); the checkbox is still the
    one click that selects, on every device and in either mode.

### Added

- **`ExplorerConfig.openInHost`** — when set, opening a file emits `file-opened`
  and the explorer does NOT mount its own in-page preview; the host opens the
  file itself. The desktop app uses this to open **every document in its own
  window**, one per file (any type), so a future editor drops into the same
  path. Directories still navigate inline; Space quick-look still peeks in-page;
  E2E-encrypted files keep the in-page decrypted preview.
- **Desktop: frameless document + main windows with our own window controls.**
  The native OS caption is gone. On Windows/Linux the app draws its own
  minimize / maximize / close (the main window in a slim title bar, each document
  window in a reserved top bar so it never sits on the viewer's own top row —
  OnlyOffice's profile/share stays clear); on macOS the native traffic lights
  are kept (`titleBarStyle: 'hiddenInset'`, top-left) and no buttons are drawn.
  The top strip is the drag handle.
- **Window / tab titles name the open document.** A document window's title (and,
  on the web, the `/files/edit` browser tab) is the file's name rather than the
  server's Branding name; the main explorer window stays the whitelabel name, or
  `filex` when the server sets no branding. On the desktop the title is pinned in
  the main process (`page-title-updated` guard) so the admin SPA can't override
  it; on the web the router's per-route title (`lib/documentTitle`) names the
  file on the editor route.

## [0.41.5] - 2026-09-16

### Fixed

- **Desktop "Open with filex" editor window: the copy-editing banner no longer
  covers the document's own bottom bar.** When the desktop app opens an Office
  file from the OS (the scratch/twin flow, `openEditorWindow`), it injects a
  persistent strip along the bottom of the `/files/edit` page telling the user
  they are editing a copy and where saves land. The strip was
  `position: fixed; bottom: 0`, so it sat squarely on top of the viewer's own
  bottom bar — for a spreadsheet that is OnlyOffice's sheet-tab + zoom strip
  ("Sheet1 / Sheet2…"), the one control you need to switch sheets. The banner
  now measures its own height and reserves that band beneath the chromeless
  editor (shrinking the full-viewport card), so the sheet tabs and zoom sit
  clear above it. Measured on the live `/files/edit` page: a 29px overlap
  before, 0px after, with the tab strip fully visible. Only the desktop editor
  window injects this banner — a plain browser tab and the in-page preview were
  never affected.

## [0.41.4] - 2026-09-15

### Changed

- **Only the checkbox selects; any other click or tap opens** (#26, fourth
  round). In 0.41.2 a click opened only when it landed on the name itself — the
  reporter: "need to click precisely on name, if a lil bit on the right then it
  selects file, change it so if only clicking on checkbox it selects it, any
  other click will open." That is now the rule, with a mouse and on a phone
  alike:
  - A click or tap **anywhere** on a list row, grid card or gallery tile opens
    it — the name, the icon, the empty space to its right, the size and date
    cells — with or without a selection, and with Ctrl or Shift held.
  - The **checkbox** is the one click that selects. Shift on a checkbox still
    extends the range from the last tick. A right click or a long press still
    opens the menu; the star and the ⋮ button still do their own job.
  - **Grid and gallery cards have a checkbox now.** It shows on hover, when the
    card is focused or selected, and on every card once anything is selected —
    so on a phone a long press selects the first file and the boxes are there
    for the rest. In the grid it takes the type icon's place, so the name does
    not move; in the gallery it sits in the thumbnail's corner opposite the
    star. The recent and starred cards on Home have none: a click there opens,
    and there is no selection to add to.
  - On a phone the tap opens at the moment the finger lifts wherever it lands
    on the item (0.41.3 did that for the name only), so it never depends on the
    browser delivering a click.
  - Ticking a checkbox with the mouse leaves the keyboard focus where it was,
    so tick-then-Space quick-looks the file instead of unticking it again.
  - A double-click still opens only the first item: the second click lands on
    the listing that just opened and is ignored for half a second.

  For embedders: `GridView` and `GalleryView` take a `selectable` prop (default
  on) that draws the card checkbox; the `click-row` / `click-card` payload marks
  a checkbox click with `check: true`, and `FilePane` treats every other click
  as an open.

### Fixed

- **The release build of 0.41.3 stopped on a race in the SFTP server's test
  harness.** `Addr()` read the listener while `ListenAndServe` was still
  assigning it, and an interface value read in the middle of that assignment
  can carry its type with a nil pointer — `(*TCPListener).Addr` then panicked.
  The SFTP and NFS servers (the same code) now guard the listener, and a
  `Close` that arrives before the listener is up no longer leaves it open.
  `go test -race` is clean on both packages.

## [0.41.3] - 2026-09-15

Tagged, never published: its release build stopped on the SFTP test race fixed
in 0.41.4. Everything below ships in 0.41.4.

### Fixed

- **On a phone, one tap on a name opens it** (#26, third round). 0.41.2 made a
  tap on a name open on every device, and on a real phone the first tap still
  only highlighted the row. Two things stood in the way, and both are gone:
  - A tap reaches a page as a click only at the end of the browser's emulated
    mouse sequence, and iOS WebKit stops that sequence when the hover step
    reveals content — the star column and the grid star chip fade in on hover.
    A finger lifted on a **name** now opens the item right there, at
    `touchend`, and cancels the emulated mouse events that would follow.
  - The hover reveals (list star column, grid and gallery star chips, the
    gallery caption) now apply only where hovering exists
    (`@media (hover: hover)`), so a tap anywhere else on an item is not spent
    on a hover either. A screen that cannot hover shows the gallery caption
    outright.
  Chromium's touch emulation always delivers the click, which is why 0.41.2's
  phone-size tests passed while the phone did not. The new tests make the
  failure measurable there: a name tap has to open even when no click is ever
  delivered, and hovering on a touch screen must leave the star hidden. Both
  fail on 0.41.2's code.

- **The Test button says what the server's probe saw** (#17). "Not reachable"
  was all it printed, so a Document Server whose `/healthcheck` answered `502`
  — its own nginx up, docservice behind it stopped — looked like a network
  problem, while `curl` to the same host loaded the welcome page. The server
  leg now prints the status code, `no answer within 3s`, or the connection
  error, and for ONLYOFFICE a gateway error points at docservice
  ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#failure-document-wont-load-or-save)).
  `POST /api/admin/external/:name/test` returns it as `detail`.

## [0.41.2] - 2026-09-15

### Changed

- **A press on a file or folder name opens it, on every device** (#26). The
  first round made a tap open on a phone only while nothing was selected; after
  a long press every tap toggled the selection, so a name could no longer be
  opened. The rule is now the one the reporter asked for, on a phone and with a
  mouse alike: a click or tap on the **name** opens the item, with or without a
  selection · the **checkbox** selects, as before · a **right click** or a
  **long press** opens the menu · Ctrl/Shift on a name still add to or extend
  the selection · a click beside the name (the size or date cells) still
  selects with a mouse. A habitual double-click on a folder name opens that
  folder only: the second click is ignored instead of opening whatever the new
  listing put under the pointer. Measured in a real browser at phone size with
  touch and at desktop size with a mouse; the double-click guard was proven by
  removing it, which opened the sub-folder under the pointer.

- **The SSO button's label is yours** (#28). *Admin → Branding → SSO button
  label* (setting `branding.sso_label`, per tenant, up to 60 characters). Empty
  keeps the default, which no longer names a provider: it reads **Sign in with
  SSO** instead of "Sign in with SSO (Keycloak)", whatever the identity provider
  is ([docs/SSO.md](docs/SSO.md#3-sign-in)).

### Fixed

- **A black or white accent no longer hides the sign-in button** (#29). The
  branding accent was painted as the button's fill with the theme's label colour
  and nothing else, so a black accent drew a dark label on a black button on the
  dark card, and a white accent a white label on a white button on the light
  card. The button is now designed per theme: the label is picked from the
  accent itself, and whenever the fill does not stand out from the card of the
  theme it is shown in, the button draws an edge in that theme. Measured in a
  real browser for black and white in both themes: label contrast ≥ 3:1 on the
  fill, and fill or edge ≥ 1.6:1 against the card. The public share page's
  accent button follows the same rule.

- **Folder sizes follow a move, an upload or a delete right away** (#27). They
  were only recomputed at the end of a sync pass, so on a storage that syncs
  rarely — or manually — a moved file stayed counted in its old folder and
  missing from the new one for hours. Every change now schedules a recompute of
  that storage's folder sizes (2 s after a burst, at most 15 s into a long one)
  and refreshes the open listings, whichever surface made the change — the
  explorer, WebDAV, S3, SFTP or NFS. Measured on two MinIO storages: both folders
  show their new size within seconds; with the refresh removed, even an upload's
  folder stayed at 0.

- **A running move shows how far it has got** (#27). A queued operation counts
  what you selected, so moving one large file — or one folder — was "0 of 1"
  until the end: a bar frozen at 0% that then vanished. A transfer between two
  storages now reports the bytes it has moved and the total it measured, and
  both progress surfaces (the explorer's operations center and the admin tray)
  draw that; when there is no honest percentage they show a moving indicator
  instead of 0%. `GET /api/files/ops` carries `bytes_done` / `bytes_total` while
  such an operation runs ([docs/BACKEND.md](docs/BACKEND.md#get-apifilesops-)).

- **A file at a storage's root no longer shows a lone "—" under its name in
  grid view** — search results and the Recent, Starred and Shared views, where
  the card names the folder a result lives in. The gallery and the list's
  Location column already left it empty; the grid printed a dash that read as a
  stray character. Found while checking this release's screenshots.

## [0.41.1] - 2026-09-15

### Changed

- **The OIDC admin mapping now holds on every sign-in, not only when the account
  is created.** With `FILEX_OIDC_ROLE_CLAIM` and `FILEX_OIDC_ADMIN_GROUP` set,
  someone added to the admin group becomes an admin at their next sign-in, and
  someone **removed** from it goes back to `user` — before this, an ex-admin in
  the identity provider kept administering filex for good. The mapping owns the
  admin role and nothing else: a `viewer` set by hand stays a viewer unless the
  group now grants admin. Two accounts are never demoted, because demoting
  either could leave nobody able to administer filex: the account filex was set
  up with (the one the recovery sign-in admits) and the last admin; each such
  sign-in logs a `WARN`. Measured against a real OIDC provider: added to the
  group → `admin` on the next sign-in, removed → `user`, the setup account kept
  `admin` ([docs/SSO.md](docs/SSO.md#roles--admin-access)).

- **`Content-Range` is in the default CORS allow-list.** An explorer on another
  origin uploads a file past one chunk (8 MiB) as PUTs carrying that header, and
  the default preflight refused it — every small upload worked and every large
  one failed, which reads as a size limit. If you set `cors.allowed_headers`
  yourself, keep it in the list.

### Fixed

- **Moving a file larger than 8 MiB onto an S3 storage served over plain
  `http://` failed** (#27) with `failed to compute payload hash: failed to seek
  body to start, request stream is not seekable` — Garage or MinIO on a
  container network, for instance. Over `http://` the S3 signer hashes the body
  and rewinds it to send it, and a move hands the writer the source storage's
  stream, which cannot rewind. It is the #16 fault one method over: part uploads
  learned to accept such a body, whole-object writes had not. A body that is too
  large to hold and cannot rewind now goes out as a multipart upload in 8 MiB
  parts: memory stays bounded by one part, every part is retryable on its own,
  and a body that ends early aborts the upload instead of publishing a truncated
  object. Reproduced and verified against a real MinIO by moving 20 MiB between
  two S3 storages.

- **On a phone, a tap selected a file or folder instead of opening it** (#26),
  in every browser: the explorer spoke the mouse's grammar — click selects,
  double-click opens — and a finger has no double-click. A tap now opens what it
  lands on; a long press selects it (and opens its menu), and while something is
  selected a tap adds to or removes from the selection. The decision is made
  from the gesture, not the screen size, so a touch laptop's trackpad keeps
  click-to-select. The long-press code the list, grid and gallery each carried a
  copy of is one composable now.

- **The password reset button in *Users* asked "Delete user …?"** (#25) — its
  dialog showed the delete confirmation, so the key icon read as a second delete
  button. Confirming it anyway was worse: the password was reset and the account
  signed out everywhere, and the new password was never shown (the server
  answers `new_password`, the page read `password`). The list and the user page
  each had their own copy of the dialog, and the copies had drifted; there is
  one now. The same page offered two fields the server ignored: **Add user**
  marked the password optional and then refused every request without one — an
  account can now be created without a password, for SSO or API-token use — and
  an *OIDC subject* field was sent and dropped (SSO matches accounts by e-mail),
  so it is gone, as is the editable e-mail on the user page, which the server
  never changed.

- **Saving from the editor into a folder the catalogue had not seen yet** filed
  the new file at the storage root, where it listed under neither folder until
  the next scan. The folder rows are created on the way.

- **Operational notifications were written in English** on every panel —
  `filex 0.42.0 available`, the replica alarms. They are phrased on the reader's
  side now, in both languages, like the file events.

- **The dashboard's *Recent activity* and the audit log printed wire names**
  (`user.update` over `— · user:12`). They read `User: updated` and `User #12`,
  in the panel's language; the raw action stays in the tooltip. A gate reads the
  Go that writes audit rows and fails on an action with no translation. The
  dashboard's rows never named who acted — the payload carried no e-mail, so
  every line began with a dash; they do now. The storage card's bare count reads
  `12 files`.

- **Every `FILEX_USAGE_*` variable was declared and never read.** They now seed
  the *Usage & cost* settings on first boot, like the antivirus family.

- **Two explorers on one page shared one clock.** Each printed dates in the
  zone of whichever explorer mounted last; each now reads its own
  `config.timeZone` and account, while the viewer's own choice still applies to
  both.

- **On the sign-in page the desktop-app card covered the sign-in form** when SSO
  was offered too (1280×800: the element at the submit button's centre was the
  card's subtitle). When the card and the form would overlap, the page shows the
  corner chip every other page uses.

- The share dialog's detail line read `… 10:00 AM. · 3 downloads`; the API key
  name field suggested the address the page happened to talk to
  (`127.0.0.1:5297`, visible in the README screenshots) and now suggests a name;
  the list view printed "Folder" twice per row while the Type column was on; the
  service worker's source map shipped the build machine's temp path, user name
  included — `scripts/check-embed.mjs` now refuses any shipped map that names an
  absolute path.

- **The update manifest's `migrations` flag is derived from the tags.**
  `scripts/gen-update-manifest.py` marks a release whose tag holds a migration
  file no earlier tag held, and lists every published release. The hand-kept
  list it replaces had gone stale: the published manifest never marked v0.31.0.

## [0.41.0] - 2026-09-14

> ⚠ **0.40.0 was never finished.** Its npm packages and git tag were published,
> but the container images, the binaries, the desktop builds and the GitHub
> Release were not — so the app-store manifests that pinned `v0.40.0` pointed
> at an image that does not exist. This release is the first complete one after
> 0.39.1, it carries everything listed under 0.40.0 below except the **Drive**
> theme (removed here — see *Changed*), and it moves every pin to itself.

### Added

- **A new face for the whole product.** The explorer was rebuilt around the
  end-user shell [@alfatm](https://github.com/alfatm) designed on top of filex
  and put up for review in #14 — measured screen by screen and adopted as
  filex's own look rather than offered as a theme. One layout for the operator
  and the end user alike, in the admin app, the desktop app and every embed:
  a full-width top bar with the product mark, one search field, a **+ New**
  menu, a 192px navigation panel with **Home · Shared with me · Recent ·
  Starred · Trash**, the storages and the connection guides, a breadcrumb row
  with the view switcher, a **Type · People · Modified · Size** filter row with
  a sort control, a selection bar that replaces the filter row while anything is
  ticked, and an info panel split into **Details** and **Activity**. The palette,
  metrics, type scale and control heights are `--fe-*` tokens, so a theme or a
  host page restyles all of it without forking a stylesheet. The tab strip, the
  split pane, the gallery view, the palettes and the keyboard editor — filex's
  own additions — are kept.

- **Home is a view inside the explorer**, not a page beside it: your storages,
  what you opened last and what you starred, under the same panel and header as
  the files. It is where everybody lands, administrators included; an operator
  who prefers the dashboard picks it under **User settings → Preferences →
  Start page**.

- **User settings**, one dialog behind the avatar: profile and photo, language,
  time zone (a search field with the offset and local time on each row), start
  page, light/dark and palette, density, per-folder view memory, notification
  switches, password and two-factor. Language and theme moved here from the
  header, so no preference has two controls.

- **A listing that behaves like a table.** Resize a column, hide one, drag one
  to a new place; the table scrolls sideways when the columns outgrow the pane,
  with the actions column pinned right, instead of dropping a column. Name is an
  ordinary column you can narrow. **The grid and the list obey the same sort** —
  it used to be private state inside the list, so switching views reordered the
  rows under you.

- **Per-folder view memory** — optional, from user settings. The view mode and
  sort of each folder you set up, stored **per person on the server**
  (`GET/PUT /api/files/manager/view-prefs`, migration `00039`), so it follows you
  to another machine and never leaks to anyone else looking at the same folder.
  Capped and least-recently-used. An embed turns it off with
  `rememberFolderView: false`.

- **Who owns a file.** Every node records its owner and its last writer
  (migration `00038`); the list has an **Owner** column and the filter row a
  **People** filter; quota counts against the owner. A storage scan no longer
  attributes a whole bucket to whoever pressed *Scan now*. Search hits carry the
  storage name and the owner too, which lifts the old single-storage limit on
  content search in the advanced search dialog.

- **Download a selection as one archive.** Pick several files and folders and
  Download streams a ZIP built on the fly (`POST /api/files/archive/download`
  mints a single-use ticket, `GET /z/<token>` streams it): nothing is written
  into your storage, nothing is buffered in the tab, and a 700 MB archive costs
  the server under a megabyte. Every member is re-checked against the caller's
  own permissions on the server.

- **Move to / Copy to**, with a folder chooser that spans every storage, lists a
  read-only folder as read-only, and refuses a destination you cannot write to —
  and the server refuses it regardless of what the dialog offered.

- **New document** under **+ New**: a Word, Excel, PowerPoint or OpenDocument
  file, or any text or code format — name it, choose where it goes, and it opens
  in the editor that handles it. The Office templates are minimal valid
  documents compiled into the binary (verified by LibreOffice and by
  OnlyOffice's own converter), so this works on the slim image; a type this
  deployment could not then open is not offered, and the dialog says why.

- **Thumbnails you can read.** A PDF shows its first page, top-anchored so the
  title is in the card; a video its first frame that is not black; an Office
  document its rendered first page; a text, code or CSV file fills the card with
  its own content. A server missing ffmpeg, ghostscript or LibreOffice now says
  so in its log at boot instead of quietly drawing coloured rectangles.

- **Date headings in every view.** A listing sorted by Modified groups itself
  under **Today · Yesterday · This Week · This Month · *September 2026*** — in
  the list, the grid and the gallery. The ladder lives in one module
  (`packages/core/src/lib/dateGroups.ts`) and all three views read it.
  - ⚠ A heading is drawn **only when it is true**. Any other sort key draws
    none, and neither does a search's ranked answer.
  - ⚠ "This Week" is the six days before yesterday, not a calendar week.
  - In the grid the date headings **replace** "Files" rather than stacking on
    it; "Folders" stays as one run at the top.
  - The boundary between today and yesterday is midnight in the **viewer's**
    chosen time zone, not the browser's.

- **Tags you can follow.** A tag chip in the details panel opens the tag view:
  everything carrying that tag, folders as well as files, across storages, with
  the ordinary filter row and sort on top.

- **A notification bell in the top bar**, for every account. Non-admins were
  raised browser notifications but had no way to open the list, mark one read
  or follow one to what it was about.

- **The desktop-app offer is a chip in the corner** of the app, not a card over
  the file listing, with a permanent home under User settings. "Do not show this
  again" is remembered against the account, not the browser.

- **Browser notifications**, and a notification opens the thing it is about.

- **The split pane is one pane component rendered twice**, so the right-hand
  pane has the same breadcrumb, filter row, sort, view switcher and selection
  bar as the left — it used to be a separate, thinner implementation.

- **Time zones resolve the same way everywhere.** One ordered list decides the
  zone every date is printed in: **the viewer's own pick → the host page's
  `config.timeZone` → the account behind the token → the device.** The account
  tier applies only to a person's token; an `app` token shared by many visitors
  never imposes one account's zone on all of them. An embed gains a **Time zone**
  row in its `⋯` menu, stored in the browser, because it has no settings dialog;
  the web app and an embed use the same picker and the same resolver, so the two
  can no longer disagree. New: `config.timeZone`, and a `time-zone` attribute on
  `<filex-explorer>`.

- **Operator custom CSS.** A stylesheet pasted under *Settings* is served with
  the branding payload and applied last on every browser surface, the sign-in
  page included, so an installation can override the `--fe-*` tokens without
  forking anything. It is capped at 64 KB, stored as one global row (in
  multi-tenant mode only the supertenant may set it), and injected as the text
  of a single `<style>` element, never parsed as HTML. See
  [docs/INTEGRATION.md](docs/INTEGRATION.md#operator-custom-css).

- **Home tells everyone how full a storage is**, not only an administrator:
  `GET /api/files/quota/storages` answers the same figure the admin storage list
  carries, for the storages the caller may see and nothing about the others.

- **Recovery sign-in for SSO-only installations.** With no `local` driver
  enabled, the administrator filex created at installation can still sign in
  with its password — and no other account can — so an identity provider that
  is down, a client secret that expired or a broken realm no longer locks out
  the one person who can fix it. The login page offers it behind an
  *Administrator recovery sign-in* link; two-factor still applies and every
  such sign-in is logged at WARN. On by default, `FILEX_AUTH_RECOVERY_LOGIN=false`
  turns it off. Installations from before this release get the account worked
  out once at startup: the oldest administrator that has a local password. See
  [docs/SSO.md](docs/SSO.md#the-identity-provider-is-down-and-nobody-can-sign-in).

- **Brand config for embeds**: `config.brand` (`name`, `markUrl`). A host
  cannot fill any slot in `<filex-explorer>` — Vue projects light DOM only
  through a shadow root and the element deliberately has none — so this is how
  an embed puts its mark in the corner.

- **A duplicate-code gate** (`scripts/dup-scan.mjs`, run by the web test suite):
  near-duplicate fragments, the same concept implemented outside its one home,
  and listing surfaces that build their own chrome. The rule and how to answer it
  are in `docs/CONTRIBUTING.md`.

### Changed

- ⚠⚠ **`uiProfile: 'drive'` is removed.** It shipped as a third profile in
  0.32.0 and became an alias of `'simple'` during this cycle; there are now two
  profiles, `'standard'` and `'simple'`, and no alias of either.

  **If you pass `'drive'`, pass `'simple'` instead.** An unrecognised value —
  a typo, or this retired name — resolves to `'standard'` (the documented
  default) and logs one console line naming it. That direction is deliberate:
  mapping the retired name onto `'simple'` would be the alias again under
  another name, and it would also mean a plain typo silently REDUCED somebody's
  UI, which looks like features going missing and points at nothing. The
  argument is written out in `packages/core/src/lib/uiProfile.ts`.

- ⚠ **The Drive theme added in 0.40.0 is removed.** Its palette became the
  product's stock palette, so the theme had nothing left to change.

- **The product colour is blue** (`#2f6ceb` light, `#5b8cff` dark) — the mark,
  the favicon and PWA icon, the admin panel, the desktop app, the public share
  page and the project site all moved off indigo together.

- **Byte sizes are decimal everywhere** (1 KB = 1000 B). The explorer used 1024
  and the admin panel 1000, so the same file read `1.43 MB` in one and `1.5 MB`
  in the other; a quota typed as 10 GB read back as 9.31 GB in the side panel.
  Turkish gets its own decimal separator.

- **The sign-in page follows the operating system's light/dark setting and the
  browser's language**; both are chosen in user settings once you are in.

- **`/admin/profile` opens user settings.** The profile page is gone — every
  field it had lives in the user settings dialog, which a non-admin can open
  too. The address keeps working, because the startup banner and
  `<data>/.first-run.txt` on existing installs still point a new operator at it.

- **"Copy node id" left the right-click menu** and the selection bar; the id is
  in the details panel, beside Path and ETag, one click to copy.

- **`@brftech/filex-react` needs no stylesheet import** — the look is injected
  by the bundle. A bundler build needs the optional viewer packages
  externalized; see `docs/INTEGRATION.md`.

- ⚠ **MySQL needs 8.0.17 or newer, MariaDB 11.4 or newer.** Migration `00041`
  compares file names byte for byte with `utf8mb4_0900_bin`, which MySQL added
  in 8.0.17, and it rebuilds the `nodes` table — on a large catalogue that takes
  as long as an `ALTER TABLE` of that table takes on your server. The previous
  documentation promised MariaDB 10.5.2; MariaDB 10.x never got past migration
  `00001`. Measured versions are listed in
  [docs/DATABASES.md](docs/DATABASES.md#supported-versions).

### Fixed

- ⚠⚠ **A token is now limited to what the token grants, not to what its
  account could do.** An API token minted on an administrator's account was
  accepted on the admin routes (`/api/admin/*`, `/metrics`) whatever its scopes —
  a `read`-only or folder-confined token included — because the scopes were
  never consulted there. Administration now requires a token that carries the
  `admin` scope and is not confined to a folder; a signed-in session is
  unaffected. **If you handed scoped tokens minted on an admin account to
  scripts, CI jobs or agents, review them.**

- ⚠⚠ **A folder-confined token stays inside its folder on every surface.**
  Search, the manager's listing, stat and read by node id, share management,
  comments, the OnlyOffice config and tags/stars/recents all enforced tenant
  isolation but not a token's `root:` confinement, so a confined token could
  reach nodes outside its folder by search or by id. They now apply the same
  confinement rule as the file protocols, before anything is returned. WebDAV,
  S3, SFTP, FTPS and NFS were already correct.

- **A malformed `root:` scope is refused when the token is created** instead of
  being accepted and then silently ignored.

- ⚠⚠ **Sign-in with SSO only (`FILEX_AUTH_DRIVERS=oidc`) worked for nobody**
  (#24), and sessions that were already open died on the restart that applied
  it. Every sign-in ends in the same session cookie, but only the `local`
  driver ever read that session back; the OIDC driver answers "unauthorized" by
  design once its callback is done, and so does LDAP. Leave `local` out and the
  identity provider's callback minted a session the very next request refused,
  so the browser went back to the sign-in page, round and round — the setup
  `docs/DOCKER.md` recommends. Sessions are now validated whatever sign-in
  drivers are enabled, the way API tokens already were, without turning
  password sign-in back on. Measured end to end against a mock identity
  provider: the old build bounced to `/admin/login` with `401` on
  `/api/auth/me`, this one lands on Home. `ldap` alone had the same defect.

- ⚠⚠ **A grant on the trash bin no longer decides who may restore someone
  else's deleted file.** A trash entry is judged on the folder it came from.
  An old row with no recorded original path — its path still inside
  `.filex-trash/` — was judged on the bin instead, so an account holding a grant
  there saw other accounts' deleted files in its trash and could restore them.
  Such rows are no longer listed or restorable; an admin can still purge them.
  Found by [@alfatm](https://github.com/alfatm) while working on a fork.

- **Restoring onto a name that is taken no longer destroys what holds it.** A
  file restore overwrote the file now at that path and then failed with a 500;
  a folder restore poured its contents into the folder now at that path and
  answered 200. The restore now refuses with 409 `EXISTS` before anything
  moves, the entry stays in the trash, and the explorer and the admin trash page
  say which name is taken. Found by [@alfatm](https://github.com/alfatm) while working on a fork.

- **A folder restored on an object store comes back where it was.** It landed
  under `<folder>/.filex-trash/<key>/` while the restore reported success. A
  restore whose trashed objects had all vanished also reported success; it now
  leaves a `trash restore move failed` warning naming the trash key. Found by [@alfatm](https://github.com/alfatm) while working on a fork.

- **A copied folder no longer opens empty.** Only the top folder of a
  same-storage copy was recorded, so its contents stayed invisible until the
  next sync, and a copy of something the cache had not seen yet was not
  recorded at all. The whole copied tree is now catalogued as it lands.
  Found by [@alfatm](https://github.com/alfatm) while working on a fork.

- **Encrypted folders stay out of search.** When a sync or a copy catalogued a
  file before the folder's `.filex-e2e.json` marker, the content indexer read
  the file as unencrypted and indexed its text, permanently. The marker is now
  always catalogued first. Found by [@alfatm](https://github.com/alfatm) while working on a fork.

- **A move whose bookkeeping fails no longer invents a trash entry.** A stale
  row at the destination, or a row that could not be moved, sent the moved file
  to the trash while its bytes sat at the destination, and restoring it did
  nothing. The stale row is now dropped and the move recorded, and a folder's
  sub-rows stored without a leading slash move with it instead of getting a
  doubled path. Found by [@alfatm](https://github.com/alfatm) while working on a fork.

- ⚠⚠ **Moving a file onto a name that is taken no longer destroys the file
  that had it.** Moving `a.txt` into a folder that already held an `a.txt`
  finished as a success and replaced the file that was there — not into the
  trash, gone. A copy and a move between storages already kept both; a move
  within one storage now does the same, the moved item landing as `a-copy.txt`.
  Such a move says so when it is queued and offers no undo, because the undo
  would move the file that was already there.

- ⚠⚠ **SQLite: one cancelled request could lock every account out until a
  restart.** When a request was cancelled while its query was starting — a
  closed tab, a navigation away — the SQLite driver dropped the half-read result
  without finalizing its statement. SQLite never cleared its interrupt flag
  again, and filex runs SQLite on one connection, so from then on every
  statement answered `interrupted (9)`: sign-in said "invalid credentials" and
  no background job ran. `modernc.org/sqlite` is upgraded from v1.30.2 to
  v1.58.0, where the result is closed, and a test cancels three thousand
  queries and requires the connection to stay usable (the old driver failed it
  within a dozen).

- **MySQL: four paths the cross-engine gate never reached** (#23). The shared
  store still had SQLite-only SQL: `MAX(0, x)` in quota accounting (a syntax
  error on every upload, so usage never moved and no quota was enforced),
  `LIMIT -1` in version pruning (history grew without bound, silently), and
  `datetime('now', …)` in the sync history (500). And the default collation
  compared file names ignoring case and accents, so `README.md` beside
  `Readme.md` uploaded, never listed, and logged a duplicate key on every sync;
  migration `00041` makes names and paths byte-exact, and one sync then
  catalogues the files that were missing. CI now prepares every statement in
  the shared store on a real MySQL server and runs those four paths on every
  engine.

- **The OnlyOffice Test blamed the network for a refusal.** A document server
  answering error -4 was reported as unable to reach filex. Since ONLYOFFICE
  Docs 7.4 the same -4 is what a stock document server gives when it refuses to
  download from a private IP address — which a docker or podman network is —
  so the result now names that cause and the setting that lifts it, and
  `docs/ONLYOFFICE.md` shows the log line that tells the two apart (#17).

- **The web app and an embed disagreed about the time.** Every account was
  created with the time zone `UTC` — the column default and six creation paths —
  so the web app, which follows the account, printed UTC, while an embed, which
  cannot see the account, printed the browser's zone. Accounts now start with
  **no** zone, which means "the viewer's browser", and migration `00040` turns
  the existing `UTC` defaults into that. A zone somebody actually chose is kept.

- **The connection guides printed the wrong address in a proxied embed** — the
  host page's origin instead of filex's. Capabilities now publishes the
  operator's `public_url` (only when one is configured, never the built-in
  guess), and the guides use it first.

- **`filex thumb backfill` reported success doing nothing** on a storage that
  had never been synced. It now refuses that storage with the reason and the
  fix, processes the others, and exits non-zero.

- **The Location column doubled the storage name** (`My files/My files://Photos`)
  for a storage whose name was not URL-shaped — a space, an underscore, a
  leading digit. Three views carried their own copy of the parser; they share one.

- **Under `uiProfile: 'simple'` the split button was drawn and did nothing.** It
  is not offered there any more, in the tab strip or the command palette.

- **The escrow notification said "recovery key".** Opening an encrypted folder
  with the operator's escrow key was labelled as the owner's recovery key — in
  the notification text, the notification switch and the webhook event label,
  in both languages. Those are opposite facts about who touched the folder.

- **The admin dashboard's sync card never showed a run** (it read a field the
  endpoint does not send), the sync-run list ignored paging and its state
  filter, and every time on the audit page and in *Recent activity* printed a
  dash.

- **Counts read "1 items"** and similar across the explorer and the admin
  panel; singular and plural now come from the catalogue, and a gate fails a
  counted message without both.

- **A notification said `share.created`.** Most file events never set a title,
  so the bell, the browser notification and the desktop app's native one showed
  the event id — and the rest were written once, in the language of whoever
  caused them. Each surface now composes the sentence in its own reader's
  language from the event's facts, from one catalogue shared by all three; the
  server's own title and body stay for webhooks and the audit table.

- **Markdown lists had no bullets or numbers** in the viewer.

- **The React package rendered a blank page.** Two causes: the web component's
  `sideEffects` named the entry file while registration lives in a hashed chunk,
  so a bundler could drop it; and the React adapter routed every prop as an
  attribute, so the explorer received the string `"[object Object]"` as its
  config. All three packages now produce byte-identical screenshots, and a gate
  keeps the shipped stylesheets identical.

- **A folder archive no longer includes previous versions of its files.**
  Zipping a folder — including a public folder share and its cache — walked the
  `.versions/` directory the listings hide.

- **Moving a folder into its own subfolder is refused** with a reason, instead
  of being accepted and failing minutes later in the operations tray.

- **The details panel printed the browser's clock** while the row beside it
  printed the viewer's chosen time zone — a whole day apart for a viewer far
  from the machine's zone.

- **Recent, Starred and tag views had no dates** (every Modified cell was a
  dash) and printed a dash under every name; trash rows had no size.

- **The tag view was empty on every multi-storage install**: its rows arrived
  without a storage name and were dropped rather than guessed.

- **Search results are shown in the server's relevance order** instead of being
  re-sorted by the active column, which had put the best match fifteenth of
  seventeen. The sort control says so while a search is open.

- **The EPUB viewer hung on "Loading" forever**, the 3D viewer showed a black
  pane, and the PSD viewer printed a raw English exception, on a file they could
  not read.

- **A video shorter than a second got no thumbnail** while its row said
  *ready*; a video opening on a fade got a black one; a PDF thumbnail was cropped
  to the middle of the page.

## [0.40.0] - 2026-09-12

### Added

- **The menus say which key does this.** Twenty actions were remappable and
  four of them said so anywhere on screen. Rename has been `F2` since the
  beginning; the right-click menu never mentioned it and neither did the
  toolbar tooltip, so the only way to learn a key was to open the `?` sheet on
  your own initiative — which is not how anybody learns a shortcut. Every
  context-menu row now prints its key on the trailing edge and every toolbar
  tooltip reads "Upload (U)", both from the registry, so a remap reaches them.
  A verb with no binding prints nothing rather than an empty key cap.

- **Twelve more verbs became remappable**: new folder, upload, refresh,
  download, preview, share, tags, convert, open in a new tab, copy path, copy
  id and restore. Seven ship on a free key (`Shift+N`, `U`, `R`, `D`, `P`,
  `Shift+S`, `T`); the rare five ship unbound, which is a default the user can
  change rather than a key taken from them. The cheat sheet and the settings
  modal go from 20 rows to 32.

- **The settings modal refuses a combination the browser keeps for itself** —
  `Ctrl+W`, `Ctrl+T`, `Ctrl+Tab`, `F12` and friends — with the reason, instead
  of storing a binding that could never fire. The tab actions, which ship on
  exactly those combinations, are badged *Desktop app only*: they work in the
  desktop app and in an installed PWA, not in a browser tab.

- **A ninth theme, Drive**, measured off the end-user shell
  [@alfatm](https://github.com/alfatm) built on filex and put up for review
  (#14) — palette, radii and the per-file-type accents, mapped onto our own
  tokens. It is one registry entry, so it dresses every surface the explorer
  has rather than only the drive shell, and an embed picks it with one setting.
  Inter leads its font stack because the demo uses it; nothing is downloaded,
  so a machine without Inter falls back to the same system face as every other
  theme.

### Fixed

- **Dark mode put white text on the primary button** (`#60a5fa`): 2.54:1,
  against the 4.5 the palette file claims in its own header. Every primary
  button in dark mode. It is dark ink now, which measures 7.28. The `default`
  theme's gallery preview also still advertised `#3b82f6` after the stock
  primary was darkened to `#2f6fe0` for the same reason. Both are checks now
  rather than claims (`web/tests/api/themeContrast.test.ts`): every palette,
  both variants, plus a drift check between the preview map and
  `variables.css`.

## [0.39.1] - 2026-09-12

### Changed

- **Every key hint now reads the key that is actually bound.** Shortcuts are
  remappable, and several hints spelled a key out by hand: the quick-look
  legend, the drive shell's search chip, and two steps of the onboarding tour.
  Remap the command palette and the chip on the search field — the one control
  whose whole job is to teach that key — kept naming `Ctrl+K`.

  Worse than a stale label, the quick-look overlay also *compared* against the
  default key. Remapping quick-look onto `Q` gave you three different answers
  to one question: `Q` opened the peek, `Space` still closed it, and the pill
  said "Space". The overlay and the search field now ask the registry, so the
  key that is named, the key that opens and the key that closes are the same
  key.

  `shortcutHint(action)` and `eventMatchesShortcut(event, action)` are exported
  for embedders who render their own hints ([docs/API.md](docs/API.md#naming-a-key-on-screen)).
  Two gates keep it honest every release: one remaps an action and measures the
  surface that names it, the other fails the build on a key typed into a
  template or a locale string.

### Fixed

- **The quick-look key legend no longer fills the window** (#22). Pressing
  Space over a file opens the preview with a small pill at the bottom edge
  reading "Space close · ↑↓ previous/next · Enter open". In the web UI that
  pill was drawn as a giant rounded shape across the whole viewport, on top of
  the file being previewed.

  Nothing was wrong with the component. The hint carries the explorer's root
  class, which is how it reaches the theme variables, and the admin app sized
  the embedded explorer with a rule that reached *every* descendant carrying
  that class — a host selector, so it outranked the package's own. The pill
  inherited the full viewport height. The desktop app wraps the explorer in
  nothing of the sort, which is why the same build looked right there and
  wrong in the browser.

  The hint is now teleported under `<body>`, out of reach of any host
  container, the package rule that sizes it no longer depends on source order
  to win, and the admin app's own rule targets the explorer root alone instead
  of anything below it. `e2e/tests/101-quicklook-hint.spec.ts` measures the
  rendered pill in a real browser, because a cascade bug renders the same DOM
  either way and no unit test can see it.

## [0.39.0] - 2026-09-12

### Added

- **Usage & cost** (#20). filex does not meter your provider's bill; it reads
  the report the provider already writes, normalises it and prices it with a
  table you can edit. Backblaze B2 first: its daily CSVs land in a bucket of
  its own, which you attach as a read-only storage, and filex reads them over
  the same S3 API it already speaks — no new dependency and no new credential
  type. Prices and free allowances are settings rather than constants in a
  formula, so a price change is an edit.

  Three things the page refuses to do, each because it is how a cost view
  misleads: it never draws an empty chart for an unconfigured instance ("you
  spent nothing" and "nothing is set up" are the same zero), it never adds the
  provider's account-level row to its per-bucket rows (the same transactions
  are in both, and the sum is wrong by exactly the amount nobody notices), and
  it never presents the estimate as a bill. `GET /api/admin/usage`,
  supertenant-only, and [docs/USAGE.md](docs/USAGE.md).

- **Every storage now has an address that does not move.** A storage's name is
  the first path segment on every file protocol — `/dav/<name>/`, `/<name>/`
  over SFTP and NFS, the bucket over the S3-compatible API — so renaming one
  silently re-addresses it and every mount, bookmark and script written against
  the old name answers 404. The name has to stay editable, because it is the
  label people read, so the fix is a second address rather than a frozen first
  one: every storage carries a `uid`, assigned once when it is created, and all
  five protocols accept it in place of the name. A mount written against it
  survives every rename.

  Existing storages are given one by the migration, on all three engines. The
  admin page shows it under **Stable address**, and `GET /api/admin/storages`
  returns it as `uid`. A storage whose name happens to be uuid-shaped still
  resolves by name, so nothing that worked before this stops working.

  The rule lives in one place (`internal/storageref`) rather than in each
  server, and a test fails if any protocol goes back to resolving names on its
  own — five copies of "which identifier is this" is how a product ends up
  behaving differently depending on how you reach it.

## [0.38.2] - 2026-09-12

### Fixed

- **Editing a storage did not reach the running process** (#21). Creating a
  storage starts a syncer for it and deleting one stops it; editing one did
  neither. The row was written correctly — so the admin page reported a
  successful save — while every live consumer kept the copy it had taken at
  boot: the syncer's own snapshot of the storage (name, driver config, root
  path, schedule, enabled flag, and the driver it had initialised for itself)
  and the process-wide driver cache behind every read, download and thumbnail.

  So a corrected bucket or endpoint kept failing the old way, a storage
  switched off kept being walked on its schedule, and a renamed one kept
  writing its old name into the log. Only a restart applied any of it, and
  nothing said so. Saving a storage now drops the cached driver (closing it
  when the driver holds a connection), stops the syncer and starts a fresh one
  from the row just written. Deleting one drops the cached driver too, instead
  of holding a dead storage's connection and credentials for the life of the
  process.

- **`Content-Length` came from the catalogue, not from the storage** (#17). The
  OnlyOffice fetch endpoint and the public share download both declared the
  response length from the node row — the size the last sync saw. Whenever the
  catalogue is behind the object, that header is a lie the transport enforces:
  Go truncates a body longer than the declared length, and a client reading one
  shorter sees a short read. Either way the transfer dies inside the recipient's
  program, which reports its own generic failure and names nothing — OnlyOffice
  says `Download failed` while every reachability test passes, because the route
  is fine and it is the body that does not match its header. The length now
  comes from the storage (or the committed manifest for a file still in
  staging), a disagreement is logged with both numbers, and an object the
  storage cannot describe is served with no `Content-Length` at all rather than
  an unverified one.

### Added

- **The OnlyOffice fetch endpoint says why it refused.** `Download failed` in
  the editor is one sentence for five different causes, and the access log's
  status code could not tell a rejected signature from an unreachable bucket.
  Every refusal now writes `onlyoffice: the document server could not download
  this file` with the node, the storage and a `reason`: signature refused · no
  catalogue row · the storage could not be opened · the object is not on the
  storage · reading the object failed.

- **The storage edit form warns while the name field differs from what is
  saved.** The storage name is the first path segment on WebDAV, SFTP, NFS and
  the S3-compatible API, so renaming one makes every existing mount, bookmark
  and script answer 404 until it is updated. Nothing had ever said so.

## [0.38.1] - 2026-09-12

### Fixed

- **MySQL: concurrent enqueues of one dedup key all failed.** The coalescing
  INSERT reads the rows every concurrent caller is about to write, so InnoDB
  rolls one of them back as a deadlock victim — and without a retry every
  caller lost: ten concurrent enqueues of one key produced ten `Error 1213`s
  and no row at all, so the work nobody queued never happened. SQLite
  serialises writers and never does this, which is why the statement had been
  correct for years on the only engine anything ran against. Caught by the
  cross-engine gate added in v0.38.0, on its second run.

- **Renaming a folder put its contents in the trash** (#21, reported on
  v0.38.0 against S3 + PostgreSQL). The rename moved a single row, so every
  descendant kept the OLD path: the next storage sync could not find those
  paths and tombstoned them, while the files it did find at the new path had
  no row — and creating one collided with the live descendant still sitting
  under the same parent with the same name, which is the
  `duplicate key value violates unique constraint idx_nodes_storage_parent_name`
  line, once per file, on every sync run. A rename at the storage **root** was
  worse: `path.Dir("Leonid")` is `.`, which no lookup can resolve, and the
  failure branch soft-deleted the node it had been asked to move.

  Both cases were already handled correctly by the shared mover every other
  write surface uses (WebDAV, SFTP, S3, NFS, the AI/MCP tools). The HTTP
  manager — the surface a person actually clicks — was the one that had never
  been pointed at it.

- **Installs already in that state heal themselves.** The sync walk now repairs
  a live row that holds `(storage, parent, name)` but points at a path the
  storage no longer has: it is, by that index, the same object, so it is
  re-homed in place, keeping its id and with it its shares, comments and
  version history. No operator action, no re-import.

- **"30000 milliseconds exceeded" when pressing Sync** (same report). The run
  happened inside the HTTP request, so a large storage outlived the browser's
  own timeout: the operator was shown a failure for a sync that was fine, and
  the abandoned request cancelled the context the walk was using, stopping it
  halfway. `POST /api/admin/storages/{id}/sync` now answers **202** and the run
  continues detached, with a ceiling so a stuck driver cannot leak a goroutine.

## [0.38.0] - 2026-09-11

### Upgrade notes

- ⚠ **PostgreSQL and MySQL installs: this is the release where they work.**
  Reported from the outside (#19): migration `00029` aborted the first boot of
  every PostgreSQL install, because `binary` is a reserved word there — and in
  MySQL, and only SQLite accepts it unquoted. That column was the first thing
  that broke, not the only one. Nothing in the repository had ever run against
  another engine, so every test stayed green while neither engine could finish
  a first boot, and on PostgreSQL the file-operation queue and the background
  job queue both failed silently on a server that reported itself healthy.

- ⚠ **Three columns were renamed** to get out of the way of reserved words:
  `plugins.binary` → `binary_path`, and `key` → `setting_key` / `meta_key` on
  `settings`, `node_meta` and `user_node_meta`. Migrations 00034–00036 do it on
  every engine; nothing in the API or the UI changes name. Existing SQLite and
  PostgreSQL installs are migrated in place on the next start.

- ⚠ **`FILEX_QUEUE_DRIVER` unset now follows the database** instead of meaning
  `sqlite`. If you were relying on the old default while running PostgreSQL,
  you were relying on a queue that logged a syntax error on every poll and ran
  nothing; it now runs. An explicit driver still wins.

- **MySQL/MariaDB minimum is now stated**: MySQL 8.0.13, MariaDB 10.5.2 — two
  migrations need `DEFAULT` on a `TEXT`/`JSON` column and `RENAME COLUMN`.
  filex also fills in `parseTime`, `loc=UTC` and `time_zone='+00:00'` when a
  MySQL DSN omits them; without the third, the server's clock and filex's
  disagree and a scheduled job becomes runnable hours early or late.

### Added

- **The document server gets its own address.**
  `FILEX_ONLYOFFICE_CALLBACK_URL`, and the matching field under the Document
  Server URL in *Settings → External services*, is the address the document
  server uses to reach filex. Empty — the default — keeps using the public URL,
  so installs where one address serves both are untouched. It exists because
  `FILEX_PUBLIC_URL` builds two different things, the share links people click
  and the document URL the document server fetches, and issue #17's reporter
  needed those to be different addresses. Applies live, no restart.

- **The third leg is measured, not disclaimed.** The Test button used to report
  two legs and say the document server's route back to filex could not be
  checked, "because filex has no way to make another container issue a request
  on demand". It can: the document server's conversion endpoint takes a URL and
  downloads it, so filex hands it a one-shot, unguessable URL of its own and
  watches for the request to arrive. The admin page now answers *the document
  server reached filex* or *it did not*, with the exact URL it was given. A
  server that rejects the signature, or that does not answer the conversion
  endpoint at all, is reported as **unmeasured** rather than as a broken route.

- **[docs/DATABASES.md](docs/DATABASES.md)** — which engine to pick, what each
  one needs, how the queue follows it, and exactly what "supported" is checked
  to mean.

- **CI runs against real PostgreSQL and MySQL service containers**
  (`test:go:engines`). The suites skip themselves without a DSN, so nobody has
  to keep two database servers running to work on filex.

### Fixed

- **PostgreSQL: the first boot.** `00029` used a reserved word as a column
  name (#19). Renamed on every engine.
- **MySQL: everything.** Five migrations wrapped many statements in one goose
  block, which reaches the server as a single multi-statement query and is
  refused — `00001` failed. Migration `00009` was missing from the dialect
  entirely, so `CreateStorage` died on "Unknown column replica_target_id".
  Seventeen columns were `NOT NULL` with no default where SQLite has one. Every
  upsert in the shared store was SQLite-only syntax; they are rewritten into
  `ON DUPLICATE KEY UPDATE` at query time rather than kept as a second copy.
- **PostgreSQL: copy, move and delete.** The `pending_ops` table was created at
  boot from hand-written SQLite DDL, so on PostgreSQL it never existed and every
  file operation failed on "relation pending_ops does not exist" while the
  server answered `/healthz` with 200. It is a migration now, and the queue's
  statements are rebound to `$1…$n` with the id read back through `RETURNING`.
- **PostgreSQL and MySQL: background jobs.** The queue driver defaulted to
  `sqlite` regardless of the database. It follows the database now, and the
  SQLite driver serves MySQL under its own name with UTC time expressions,
  `FOR UPDATE SKIP LOCKED` and a claim whose result is actually checked — four
  workers used to be handed the same op, three of which then failed the ack.
- **MySQL: a fresh install had no external services.** Seeding a service that
  has never been health-checked bound a zero timestamp, which MySQL rejects in
  strict mode, so OnlyOffice, drawio and the converter were missing from the
  settings page.
- **Documentation that had stopped being true**: `CONFIGURATION.md` still said
  MySQL was for "read-mostly use", `DEPLOYMENT.md` repeated it, and
  `MULTI-TENANCY.md` listed the postgres/mysql CI job as pending.

## [0.37.0] - 2026-09-07

### Upgrade notes

- ⚠ **If OnlyOffice or drawio "tests fine" but fails when you open a file**,
  press Test again. It now probes from **your browser** as well as from the
  filex server and reports the two separately, because they answer different
  questions and only one of them was ever asked. A container-internal address
  like `http://onlyoffice` is reachable from filex and not from the browser
  that has to load the editor — the green light said "configured" and meant
  "the server can reach it". Reported from the outside, twice, by the same
  person before we saw it.

- ⚠ **`GET /metrics` was inside the admin group but registered with `r.Handle`**,
  so chi bound it to *every* method and the demo guard refused none of them.
  Read-only either way, so nothing was exposed that a GET did not already
  expose — closed so the rule has no exceptions left.

### Added

- **The Test button now probes from the browser too, and says which machine
  answered.** Issue #17's reporter came back: the v0.34.2 fix was real, and it
  did not close his problem. Three different machines have to reach three
  different addresses before the Office editor works — the **browser** loads
  the editor's JavaScript from the Document Server URL, the **filex process**
  polls the same URL, and the **Document Server** fetches the document and
  POSTs the save back to `FILEX_PUBLIC_URL` — and only the middle one was ever
  checked. So an operator on podman typed the container name
  `http://onlyoffice`, filex reached it, **Test went green**, and his browser
  could not resolve that name at all; the editor then failed with the same
  message as a missing configuration. The defect was never the probe. It was
  that the check was narrower than the badge implied — the same family as
  everything else fixed this week: a control that reads as verified and is not.

  - **Two probes, two results, one badge.** The admin page runs in the very
    browser that will open the editor, so it now tests that leg directly
    instead of disclaiming it. The result is reported as two separate
    sentences — *From the filex server: reachable* / *From this browser: not
    reachable* — plus a third line for the leg nobody can probe. When they
    disagree the page says what it means: a container-internal address filex
    can use and a browser cannot.
  - **The mechanism is the one the real viewer uses**, not a `fetch`. ⚠ A
    plain `fetch()` is blocked by CORS on a document server that is working
    perfectly, so a naive `catch` would report failure for a healthy service.
    OnlyOffice is probed with a `<script>` at
    `/web-apps/apps/api/documents/api.js` — load detection is not subject to
    CORS, and a successful load defines `window.DocsAPI`, which proves the
    thing that answered really is a Document Server. drawio is probed with a
    hidden `<iframe>` at `?embed=1&proto=json` and its `{"event":"init"}`
    handshake, the same one `DrawioViewer.vue` uses. Both were verified
    against **live** servers before being relied on. Each distinguishes
    *could not reach* from *reached, wrong thing* with a second, `no-cors`
    signal, and each is bounded by a timeout that reports as its own state.
  - **The badge no longer conflates "reachable" with "configured".**
    `Complete` is reserved for a service where both probes answered and
    nothing is warned about; a service the server can reach and the browser
    cannot reads `Server-reachable only`, in a different colour.
  - **The reverse path gets the only honest treatment available.** filex
    cannot make the Document Server issue a request on demand, so it does not
    claim a check it cannot perform: it warns on the shapes that certainly
    cannot work (a public URL of `localhost`, `127.0.0.1` or `0.0.0.0`) and
    notes the ones that are merely suspicious (a container-name public URL, a
    hostname filex itself cannot resolve). ⚠ A warning that fires on a working
    setup is worse than none, so every trigger is paired with a test that
    proves it stays silent on a setup that works — `https://office.example.com`,
    `http://192.168.1.10:8080`, and `http://localhost:8080` when filex itself
    is reached over localhost. That last suppression is the interesting one:
    a loopback document server is *correct* for somebody browsing from the
    same host, so it is never warned about there.
  - **Advisories ride on the list, not only on Test**, because the whole
    defect was a control that read as settled without anyone pressing
    anything. Opening the page is enough to see a browser-unreachable address.
  - **The three-address requirement moved to where the field is filled in** —
    the top of the admin page and the top of `docs/ONLYOFFICE.md`, with the
    two commands that tell an operator which half is wrong. It was already in
    the docs, around a prerequisites list further down the page, and he hit it
    anyway: a green Test outranks a prerequisites list.

  - ⚠ **Known limitation, deliberately not fixed here: filex has one public
    URL, and some setups need two.** `FILEX_PUBLIC_URL` is a single value that
    the OnlyOffice fetch/callback and every share link, invite mail, drop link,
    OIDC redirect and WebSocket ticket are all built from. An install whose
    Document Server can only reach filex by a container-network name
    (`http://filex:5212`), while its users need a browser-facing address,
    cannot express that today. The admin page now raises a **note** when it
    sees a container-name public URL rather than leaving it silent; splitting
    the value is tracked separately.

- **A release gate for the shop window — the surfaces a stranger touches
  before they trust us.** On 2026-09-07, hours before the public launch, a
  person looking at filex from outside found seven defects, and not one had
  been caught by a test, a lint, or any of the eleven steps of the release
  process. Several had been shipping for months: a dead `Issues` link on 104
  of the 105 published release pages, a public demo that answered all 101
  admin routes with no refusal, `GET /api/files/capabilities` handing
  anonymous callers the operator's internal hostname, a docs site built from
  the private tree, release bodies that were a commit hash, a headline
  `docker run` that put the reader's files in the database directory, and the
  demo's own advertised search returning nothing. The pattern is why this is
  a gate and not a checklist: **everything a stranger touches first is the
  least tested surface in the project.**

  It is split by what each check needs, because a check that cannot say
  *which* it is ends up either useless or dishonest:

  - **Offline, repo-only** — `web/tests/deploy/shopWindow.test.ts`, so it runs
    on every push, in `pnpm test`, and in CI, which already gates the tag. It
    covers the URL grammar (nothing pastes a GitLab route onto a GitHub host,
    and every GitLab route we link is one the export can *translate* — the
    export's own guard cannot see a route it drops the `/-/` from and gets
    wrong anyway), the headline `docker run` against the compose file it is a
    shorthand for, `site/` — which reaches filex.sh verbatim with no converter
    in the way — and the names the export does **not** rewrite, such as a bare
    IP address.
  - **Against a running instance** — `node scripts/check-shop-window.mjs
    --instance --boot bin/filex`. It boots a throwaway in demo mode on a pinned
    port with its own data directory, then proves a signed-in visitor is
    refused on the state-changing admin routes *and still gets the read-only
    ones*, that an anonymous capabilities call carries the feature flags and
    not the operator's host, and that the searches the demo advertises return
    the files they promise.
  - **Against the published product** — `--published`. Release bodies carry no
    `/-/` route and no bare commit hash where prose belongs, their links
    answer, and docs.filex.sh serves the released build with no private
    repository URL on it.

  Three exit codes, and the last two are the point: `0` passed, `1` **checked
  and wrong** — fail the release — and `2` **could not check** (no binary, no
  network, a GitHub rate limit, a fixture that is not set up). ⚠ A gate that
  turns an outage into a failed build is an outage of its own, so a network
  failure is never `1`; it is also never `0`, and the run names the check that
  did not happen. The same rule applies to a fixture: an instance with no
  external service configured would satisfy "the anonymous answer names no
  host" while leaking the moment an operator configured one, so that reports
  `skip`, not `ok`.

  Every check was proved against the defect it is for — the bug
  re-introduced, the check watched going red, then green: a `/-/` link, an
  unknown GitLab route, the old `docker run`, a production IP in a published
  file, the private repo on the landing page, the runbook publishing from the
  wrong tree, an advertised query nobody is shown, the redaction removed from
  `capabilities.go`, the demo guard removed from the router, an advertised
  query that answers nothing, and a fixture serving each published defect in
  turn. Release process step 12 in `docs/CONTRIBUTING.md` says what it does
  not cover.

- **The shop-window gate now walks the route table instead of sampling it, and
  covers four surfaces it had listed as gaps.** The gate above shipped with an
  honest account of what it did *not* reach; this closes what could be closed
  and says plainly what could not.

  - **Every route, not six of them.** The live check probes six admin routes,
    which proves the demo guard is installed and nothing more — a new operator
    surface at a *fourth* prefix would have passed, and that is exactly how the
    first hole appeared, because `/api/ai/admin` was the same admin panel behind
    a different front door. `backend/internal/api/shop_window_route_table_test.go`
    walks all 359 routes out of chi and classifies each one by **asking the
    running server**: a route an anonymous caller gets 401 on and a signed-in
    non-admin gets 403 on is an operator surface, one that answers both
    identically is not role-gated at all, and everything else is the product.
    Nothing in it names a route, so a new admin prefix goes red the day it is
    added with no list to update — and a second test measures the other
    direction, so the guard cannot be widened over the product to make the first
    one quiet. (Middleware would have been the obvious signal and chi cannot
    show it: `r.Group` bakes its chain into the handler before registration, and
    all 359 entries report the same four top-level middlewares.)
  - **`/metrics` was the fourth prefix**, found by that walk on its first run.
    It is mounted inside the admin-only group with `r.Handle`, so chi registers
    it for every method, and a public demo refused none of them. The exposition
    is read-only, so nothing was ever going to break — it is guarded now because
    that lets the walk state its rule with no exceptions at all.
    `GET`/`HEAD`/`OPTIONS` still pass: a Prometheus scrape job is untouched.
  - **All three external services are proved, not one.** The redaction covers
    OnlyOffice, drawio and the converter through one loop *and* three flat
    aliases blanked by name, so a single seeded host exercised the loop and left
    two aliases unproved. The booted instance now carries a distinct sentinel
    per service, and the anonymous payload is additionally asserted to carry
    **no `url` key anywhere** under `external` — the only form that reaches a
    service nobody has added yet (`mermaid` is already there, with no
    environment variable and no alias).
  - **The demo's own corpus, asked of the demo.** The instance check seeds the
    file it then searches for, so it proves the query grammar and not what
    demo.filex.sh holds — and the corpus is where the defect was. `--published`
    now signs in to the live demo with the credentials the demo itself
    publishes, and types the queries the splash advertises. Unreachable is a
    `2`, never a false green.
  - **GitHub's About box, and filex.sh.** Neither is code, so neither was ever
    checked. The About blurb had no source of truth at all — it is typed into a
    settings form and lived only in GitHub's database — so `REPO_ABOUT` in
    `scripts/shop-window-data.mjs` is now that source: asserted offline to fit
    GitHub's 350 characters, to **name every driver the backend registers**
    (measured from the `storage.Register` calls, and the blurb that was
    published when this was written still said five after SMB shipped), and to
    punctuate the way every other surface does. `--published` compares it with
    the live value and prints the exact line to paste, GitHub having no deploy
    step for it. The front page is checked for the hosts it has to link —
    docs.filex.sh above all, which `site/index.html` links in three places and
    the deployed page did not link at all.
  - **Screenshot staleness, with its limits written down.** A picture committed
    before the last change to the code that draws it cannot be showing that
    change. `SCREENSHOTS` declares what each README picture depicts — the one
    thing here a person has to know — and the offline half asserts every
    declared path still exists, so a renamed component cannot leave a picture
    looking fresh for ever. The threshold is measured, not chosen:
    `admin-plugins.png` shipped **six** releases stale, so six released versions
    of unfollowed change is the line, and lesser drift is reported and passes.
    ⚠ What it cannot see is whether a picture is actually *wrong*: it compares
    commit dates, so a comment counts and a theme change does not. Looking is
    still release step 2.

### Fixed

- **Two release-gate suites could report success while running no tests.**
  `describe.skipIf` skips every assertion inside it — including the "this list
  is not empty" guards written to stop those blocks passing vacuously, which had
  been placed inside the very blocks they guard. Measured across the suite: in
  the **published** tree, which CI runs, `siteAssets.test.ts` reported 0 of its
  8 tests and `shopWindow.test.ts` 8 of its 17, both green. Both files now
  assert the shape of the checkout *unconditionally*, and as one fact — the
  export withholds `site/` and `scripts/export-public.sh` together, so a tree
  holding exactly one of them is neither the source nor the published product,
  and says so instead of skipping in silence.


## [0.36.0] - 2026-09-07

### Upgrade notes

- ⚠⚠ **Running a public demo (`FILEX_DEMO_MODE`)? Upgrade before you link it
  anywhere.** The demo account is an administrator, and the guard covered
  storage creation and plugins only. Measured by walking the real route table:
  **all 101 routes under `/api/admin` answered, and not one returned 403** —
  resetting the shared account's password, deleting users, repointing an
  existing storage, making the server connect wherever a visitor pointed
  `smtp-test`, applying an update. `/api/ai/admin/*` is the same surface behind
  a token and was reachable the same way, proven end to end. Writes are refused
  now; reads are not, because showing the operator surfaces is what a demo is
  for.

- ⚠ **`GET /api/files/capabilities` no longer returns service URLs to
  anonymous callers.** It still says *whether* OnlyOffice, drawio and the
  converter are configured — that is what embedders probe it for — but not
  *where* they live, because it was handing the operator's internal hostname to
  anyone with curl. Signed-in responses are unchanged. **If you read
  `onlyoffice_url`, `drawio_url` or `convert_url` from an unauthenticated
  call**, send credentials; the first-party consumers all already do.

- **The container can drop root.** Set `PUID`/`PGID` (or `user:` /
  `runAsUser`) and filex runs as that user, chowning `/data` once. Nothing
  changes without them: no variable means root, exactly as before, and the
  upgrade path from a root-owned data directory was measured — the database
  survives byte-identical and `rm -rf ./data` stops needing `sudo`.

### Fixed

- **docs.filex.sh was built from the private tree, so it published an import
  path that does not compile.** `docs/PLUGINS.md` tells a plugin author to
  import `github.com/brf-tech/filex/backend/pkg/pluginsdk`; the published
  module is `github.com/brf-tech/filex/backend`. Anyone following the plugin
  guide copied a path that names a repository they cannot reach and does not
  build. `CONTRIBUTING` carried two more of the same. The release step now
  pushes the site's prose from the **export**, which is where every other
  public artifact comes from.

  It also explains a second symptom: the site had been serving a v0.33.0 build,
  so `/REALTIME` was a 404 while the README's first paragraph advertises
  real-time collaboration and nine pages link to it, `/CONFIGURATION` never
  mentioned ClamAV (nineteen times in the repo), and `/PROTECTION` still said
  WebDAV and AI/MCP writes were "not yet" scanned — a security page describing
  a gap that had been closed.

### Added

- **`PUID` / `PGID`.** The image installed `su-exec` and created a `filex`
  account, then ran everything as root and used neither — so `/data` came back
  owned by `root:root` and deleting one's own data directory needed `sudo`.
  There is now a `docker/entrypoint.sh`: set `PUID`/`PGID` and it takes
  ownership of the data directory once, records what it chowned to in a
  `.filex-uid` marker so later boots skip the walk, and drops privilege.

  ⚠ The default is unchanged — **still root**. An existing install whose data
  directory is full of root-owned files must keep working on upgrade, so
  opting in is the operator's decision. `docker run --user` / compose `user:`
  / Kubernetes `runAsUser` are detected and left alone (nothing to drop, and
  no permission to chown); `PUID` alongside them says so in the log rather
  than pretending. Measured on the published image: an old root-owned data
  directory, then `PUID=1000`, gives a 200 on `/healthz`, a byte-identical
  `installation.json`, and a `rm -rf ./data` that no longer needs `sudo`.

  ⚠ Only the DATA directory is chowned. Storage roots — a bind mount, an NFS
  or SMB share — are left as they are; they may be shared with other software
  and re-owning them is not a container's decision.

### Fixed

- **Every GitHub release page ended with two dead links, one of them
  "Issues".** `scripts/export-public.sh` rewrote the GitLab host to
  `github.com` but not the URL *grammar*: GitLab's `/-/` route separator
  arrived verbatim, so `https://github.com/…/filex/-/issues` — the link a
  reader clicks to report a bug — answered **404**. Measured 2026-09-07:
  **104 of the 105 published releases** carried it, not the recent few. The
  export now translates the route shape (and renames `merge_requests` →
  `pulls`, `pipelines` → `actions`), refuses to publish a tree where a
  `github.com/…/-/…` survives, and the release footer points at
  <https://docs.filex.sh> instead of raw markdown. All 105 published bodies
  were corrected.

- **Release notes said nothing.** GoReleaser builds the body from `git log`,
  and the config filters drop `docs:`, `test:`, `chore:` and `ci:` — so the
  published v0.34.2 page was, in full, a heading and one commit hash, while
  `CHANGELOG.md` carried 1,445 characters of prose for that same version. The
  release workflow now derives the body from `CHANGELOG.md` (extending
  `scripts/release-notes.mjs`, which already did this for the Umbrel store)
  and hands it to `goreleaser --release-notes`. A version with no changelog
  section fails the run **before** anything is published. ⚠ Capped at 20,000
  characters, cut at a group or bullet boundary, with a link to the full
  entry: measured on goreleaser v2.17.1, a body over 125,000 characters is
  truncated *silently*, and what it cuts is the footer.

- **The README's headline command left the reader in an empty file manager.**
  `-v $(pwd)/data:/data` mounts filex's own state directory — database, search
  index, thumbnail cache — so files dropped into `./data` were invisible and
  the UI said *"No storage configured"*. The command now seeds a local storage
  from `$PWD` and keeps `/data` in a named volume, which is also what
  `docs/INSTALLATION.md` told people to run.

### Changed

- **filex.sh links to the documentation site.** 18 anchors, and
  `docs.filex.sh` appeared in none of them: the nav "Docs" and the footer
  "Documentation" both pointed at raw GitHub markdown while a 38-page
  VitePress site sat unlinked.

### Security

- ⚠⚠ **A public demo no longer hands every visitor a working admin panel.**
  `FILEX_DEMO_MODE` publishes the credentials on purpose, which makes
  "admin-only" mean "public" on that instance — and until now the only thing
  refused was *adding a storage*. Measured on a local demo: all **101** admin
  routes answered, none with a 403, including
  `POST /api/admin/users/{id}/reset-password`,
  `DELETE /api/admin/users/{id}`, `PATCH /api/admin/settings`,
  `PATCH /api/admin/storages/{id}` (repointing an existing storage — the
  create-side guard never covered it), `POST /api/admin/webhooks` and
  `POST /api/admin/update/apply`. One visitor resetting the shared password
  locked out every other reader until the nightly restore.

  Every state-changing method under `/api/admin/…` and `/api/ai/admin/…` is
  now refused with 403 on a demo, as are changes to the shared account itself
  (`/api/auth/password`, `/api/auth/profile`, `/api/auth/totp/…`). **Reads are
  untouched** — a demo exists to show the operator surfaces — and so is every
  ordinary install, where the middleware is a pass-through. Full list:
  [docs/DEMO.md](docs/DEMO.md).

  ⚠ `/api/ai/admin/…` is the same admin surface behind an admin-scoped API
  token, and a visitor could mint one at `POST /api/admin/ai-tokens` (201) and
  then drive `PATCH /api/ai/admin/settings` (200) with it. Guarding one mount
  point without the other would only have moved the door.

- ⚠⚠ **`/api/capabilities` no longer tells anonymous callers where the
  operator's services live.** The endpoint is public by design (embedders probe
  it before logging in) and it published `external.<service>.url` plus the flat
  `onlyoffice_url` / `drawio_url` / `convert_url` to anybody who asked —
  measured on demo.filex.sh: `"url": "https://docs.example.com"`, with no
  credential. This affected **every install**, not just demos. Anonymous
  callers now get `enabled` and `state` and no host; authenticated callers see
  the payload unchanged, because the draw.io iframe and the convert modal need
  a real address. OnlyOffice's document-server URL was never needed here — the
  browser gets it from the authenticated `POST /api/files/onlyoffice/config`.

- On a demo, the audit log and the dashboard's `recent_activity` no longer
  print client IP addresses: with published credentials, one visitor's address
  is readable by the next. Ordinary installs still show them.

- On a demo, `GET /api/admin/auth-providers` no longer returns credentials in
  clear. Its `config_redacted` block masks by leaf NAME, and the admin UI saves
  a provider's whole config as one leaf called `config`, so an OIDC
  `client_secret` went out in full under a field named "redacted".

### Fixed

- The demo advertised two searches that returned nothing. The login splash
  promised `"invoice 2026" finds invoice_2026.pdf` and the search box suggested
  `tag:report`; on the demo corpus both answer 0 results — there is no file
  with "invoice" in its name and no tags at all — while `mian.go` and
  `package main` work. The examples now name files the demo actually holds, and
  a test pins them to the recorded corpus so a suggestion that stops being true
  fails a build instead of greeting visitors.

## [0.35.0] - 2026-09-07

### Upgrade notes

- ⚠⚠ **Running `FILEX_MULTI_TENANT`? Upgrade.** The tenant boundary was enforced
  on the routes that *list* rows and assumed on the routes that *name* one. An
  administrator of any tenant, pointed at another tenant's ids, reached fifteen
  of the sixteen such routes — including `POST /api/admin/users/{id}/reset-password`,
  which answered 200 and returned the other customer's **new password in the
  response body**, and `GET /api/admin/storages/{id}`, which returned the storage
  config: for an S3 or SFTP storage, the access key and secret. `/api/files` was
  worse still, because it serves bytes. All of it is closed. See **Security** below.

- ⚠⚠ **Everyone: thumbnails were readable without logging in.** `GET /api/files/thumb/{id}`
  sat outside every auth group and returned a rendered preview — the readable
  content of a document — to anyone who could guess a node id. This is not
  specific to multi-tenant installs. Thumbnail URLs now carry a short-lived
  signature, and an authenticated caller is checked. **If you build thumbnail
  URLs yourself**, take them from the listing rather than constructing them;
  a hand-built URL without the stamp is now a 401.

- ⚠ **Emptying the trash for one storage used to empty all of them.** If you
  have used *Empty trash* with a storage selected on an earlier version, it
  purged every storage's trash, permanently, while the dialog said otherwise.

- ⚠ **Directory logins now refuse rather than mis-home.** On a multi-tenant
  install, LDAP and proxy-header logins used to provision accounts into the
  supertenant, which sees every tenant. They now inherit the tenant from the
  request's host; where there is no host to read (LDAP over SFTP/FTPS/NFS) the
  login is **refused** unless you set `auth.ldap.provider` / `FILEX_LDAP_PROVIDER`,
  because the only alternative was the supertenant. Existing mis-homed accounts
  are **not** moved automatically — the boot log now names them and points at
  `PATCH /api/admin/users/{id}`.

- ⚠ **Version restore now requires `editor`.** It required nothing before: a
  read-only user could overwrite live file content with an old version.

- **Five admin Settings controls were removed** (`public_url`, `sync_interval_seconds`,
  `log_level`, `default_locale`, `default_timezone`). They wrote rows that
  nothing read; the environment variables that do work are named in their place.

- **`storage.sync_mode` is validated now.** An unsupported value is rejected
  instead of stored and silently polled. Rows that already carry `push` keep
  working (they poll, and say so once in the log); only a *change* to an
  unsupported mode is refused.

### Security

- **`GET /api/files/thumb/{id}` served rendered previews to anyone who could
  count.** The route sat outside every auth group, `checkSig` returned true when
  the `sig` parameter was **absent**, `manager.go` emitted `thumb_url` with no
  signature so no deployment was ever on the signed path, and nothing in the
  codebase ever wrote `settings.thumb_signing_key` — so the `key == ""` branch
  waved a supplied signature through too. Measured: an anonymous `curl` with no
  cookie and no token got **200, `image/jpeg` and the full body**, and
  `?sig=deadbeef` got the same, on a server where anonymous
  `GET /api/files/quota/me` correctly answered 401. Node ids are dense integers,
  so that was "walk the range and collect the rendered first page of every file
  on the instance" — and it was **not** a tenancy bug: a single-tenant install
  leaked exactly as much. This is the most widely exposed of the three fixed
  here.

  ⚠ The fix could not simply be "require auth". Thumbnails render into
  `<img src>`, which sends no `Authorization` header, and filex's session cookie
  is `SameSite=Lax`, so an `<img>` inside a third-party embed sends no cookie
  either — a blunt auth requirement would have closed the hole and blanked every
  embedded explorer in production, including a customer's. The endpoint now takes
  **one of two proofs**: a short-lived stamp on the URL (`?exp=&sig=`, an
  HMAC-SHA256 over node id + expiry under a key that is now actually generated),
  which the listing puts on every `thumb_url` it emits and which an `<img>` can
  carry; or an authenticated caller who clears the node's tenancy scope, the
  token's `root:` confinement and an ACL check at viewer level. Neither → 401.

  All four consumers were re-measured after the change: the admin SPA (cookie),
  the desktop app (bearer), an `<img>` in an embedded `@brftech/filex` explorer
  (the stamp, no credentials at all) and the public share page — which never used
  this endpoint and still does not, since it serves the same artefact through
  `/s/{token}/f/<path>?thumb=1`, scoped to the share token. A root-confined embed
  token now also stops at its own subtree: previously it could read previews of
  any node id on the instance, because a node id is not a path and
  `confine.Middleware` had nothing to rewrite.

  `FILEX_THUMBS_URL_TTL` (default `24h`, matching the endpoint's `Cache-Control`)
  bounds the stamp; the expiry is quantized to the hour so the URL string — the
  cache key for both the browser and `useThumbs` — stays stable within a window
  instead of forcing a re-download on every listing.

- **LDAP and proxy-header logins provisioned into the supertenant.**
  `db.Store.CreateUser` hard-codes `provider_id` to the `default` provider, and
  `default` is seeded `is_supertenant = 1` — which `CanAccessStorage` treats as
  confine-**exempt**. Measured on a two-tenant install: a header-trust login
  arriving on `diyetlif.local` created the account with `provider_id = 1`, and
  that account's own storage listing came back as **both** tenants' storages. The
  account was not mis-filed, it was privileged.

  A login has no caller whose tenant could be inherited — the account being
  created *is* the caller — so the signal is the request **Host**, the same one
  `multioidc.Dispatcher` already uses to pick a realm. `handlers.Auth.Login`
  stamps it onto the context (`auth.LoginDriver.Login` takes only a `ctx`, so a
  driver in the chain cannot see the request), the proxy-header driver stamps its
  own, and `auth.ProvisionUser` homes the new row — deleting it if homing fails,
  as the invite path already did.

  ⚠ The protocol logins (SFTP/FTPS/NFS, through `internal/protocolauth`) present
  a password on a socket and have **no Host at all**. There, on a multi-tenant
  install, the driver now **refuses to create** rather than falling back: the
  only fallback available is the supertenant, so "provision anyway" is the bug
  rather than a convenience. An account that already exists signs in over every
  protocol exactly as before. New `auth.ldap.provider` / `FILEX_LDAP_PROVIDER`
  (and `auth.header_proxy.provider` / `FILEX_HEADER_PROVIDER`, plus Helm
  `auth.ldap.provider` and a `auth.headerProxy` block that did not exist) let an
  operator name the tenant explicitly when they want it.

  ⚠ **Rows an older build already stranded are not migrated.** Nothing records
  which driver created a user, and the break-glass `admin@local` is deliberately
  in the supertenant, so a blanket re-home would take an operator's own account
  away from them. Instead a multi-tenant install with either driver enabled now
  logs **one WARN at boot** naming every non-admin account homed in the
  supertenant; re-homing stays `PATCH /api/admin/users/{id}` with `provider_id`.

- **`/api/files/versions` had no ownership or ACL check on any install.** The
  `Versions` handler had no `ACL *acl.Resolver` field at all — the only file
  surface in `routes.go` with no `AttachACL` call — and `versioning.Service`
  verifies only that a version belongs to the node it names before overwriting
  live bytes. Measured on a **single-tenant** install with a `viewer`-role
  account: `POST /api/files/versions/restore` answered 200 and `contract.txt` on
  disk went from its current content to the old version's, and
  `POST /api/files/versions/snapshot` answered 200 and wrote a version row. A
  read-only account could roll back, and force snapshots of, any file whose node
  id it could name.

  Every route in the file now resolves the node first and applies four gates —
  existence, tenancy, `root:` confinement, RBAC. Restore and snapshot require
  **editor**; listing the timeline stays at **viewer**, because somebody who can
  read the file is not being told its history is a secret. The admin hard-delete
  gains the same check (redundant behind `RequireAdmin` today, deliberately, so
  that un-admin-ing the route cannot silently drop authorization). The first
  three gates answer 404, identical to a node that never existed; only the RBAC
  refusal is 403.

  ⚠ One behaviour change beyond the permission gate: a **trashed** node is no
  longer reachable through these routes (404). A trashed row's live path is its
  `storage_key` — where restore would put it back — so restoring a version onto
  one wrote bytes to a path the catalogue says holds nothing. It is the same
  exclusion the comments handler already makes.

  ⚠ Restore is a destructive **write** and was the one write surface in filex
  that went through neither half of `writehook`. It now calls
  `BeforeOverwrite` first — so the bytes it replaces are snapshotted, and a
  snapshot that fails answers 503 `SNAPSHOT_FAILED` instead of destroying them —
  and `OnFileWritten` after, which is what finally makes a restore emit
  `file.updated` to webhook subscribers. `snapshot_current` now does work only
  when that guard is switched off, so the same bytes are never recorded twice.

### Fixed

- **A user could mute a notification event and keep receiving it.**
  `muted_events` and `in_app_enabled` round-tripped through
  `GET`/`PATCH /api/notifications/settings` from the day they were added and
  **no read path applied either of them**: the history query filtered on the
  user and the read flag only, so a muted event still appeared in the list and
  still counted towards the unread badge. A preference that saves and does
  nothing is worse than an absent one — the user believes the noise is handled.

  Both now gate the **read**: `in_app_enabled: false` empties the bell,
  `muted_events` drops those event ids from the list *and* the unread count.
  What deliberately did **not** change is as important: `Send` still records
  every event (the audit is not a preference), webhook delivery is untouched
  (it is global), and the admin/global view is never filtered by one user's
  mutes. The filter is applied in SQL rather than to the returned page, so a
  filtered page comes back full and its total counts what the caller will
  actually show. An unreadable settings row **fails open** — a display
  preference must not be able to hide an antivirus hit behind a DB hiccup.

  `docs/NOTIFICATIONS.md` said so in one place ("nothing applies them yet") and
  contradicted itself in another, prescribing them under *Too many
  notifications* as a live remedy. Both are now true.

- **`FILEX_AUTH_DRIVERS=proxy_header` enabled nothing.** The loader matched
  `proxy-header` with a hyphen; `docs/CONFIGURATION.md` (twice),
  `docs/ARCHITECTURE.md` and the Helm chart all told people to write the
  underscore. Following the documentation produced one
  `unknown auth driver` log line and an install with reverse-proxy SSO that
  reads as configured and silently is not. Driver names now fold `_` to `-`.

- **The release pipeline could publish the 510 MB image as `:slim`.** The
  default build was tagged `slim`/`slim-vX.Y.Z` and those tags were overwritten
  by the slim build that followed — which is only true when the second build
  runs. The job is `allow_failure` (the dind runner is unreliable) and pushed
  with `--all-tags`, which is not selective. The full build no longer claims
  those tags, and the push names the tags it built.

- Helm `Chart.yaml` credited `scripts/sync-chart-version.mjs` for keeping
  `appVersion` current. No such file exists; the script is
  `scripts/sync-deploy-versions.mjs`.

- The storage badge labelled a storage with no `sync_mode` as **On demand**,
  while the backend defaults an unset mode to **poll** — the opposite of what
  was running.

- **`FILEX_DEMO_PASS` broke the demo button it was supposed to configure.**
  The loader parsed it, `docs/CONFIGURATION.md` listed it, and no code read
  `config.Demo.Pass` — while the demo landing's only button submitted the
  literal string `demo` and the credentials hint under it printed the same
  literal. An operator who set the variable therefore *broke* the demo login
  while believing they had secured it, and the page went on advertising a
  password that no longer worked.

  The password now travels the same road the demo user already did: the
  capabilities response carries it and the CTA submits what it is given.
  ⚠ It is published **only when demo mode is on** — where the page prints the
  credentials next to the button anyway, which is the entire point of a demo —
  and a normal install returns nothing, whatever `FILEX_DEMO_PASS` says. Both
  fallbacks stay `demo`, so an install that never set the variable (including
  the public demo, whose account password is `demo` in the database) behaves
  exactly as before. The documentation now also says the thing that was
  missing: neither variable creates or changes the account.

- **`sync_mode` was never validated, and `push` was a mode with nothing behind
  it.** Any string the admin API decoded was persisted, and the sync worker's
  switch has no branch for it, so `fsnotifiy` stored happily and the storage
  ran the poll loop while the page displayed the typo back. `push` was the same
  defect wearing a nicer costume: a declared enum member, no receiver, silently
  polling.

  The gate lives in the **store**, not in one handler, because three writers
  reach that column — the admin API, the config seed and the CLI — and the
  message names the modes that exist. `push` is rejected as unimplemented, with
  a message pointing at `ondemand` + `POST /api/admin/storages/{id}/sync`,
  which is what an external writer actually wants.

  What happens to rows that already say `push`: **nothing is rewritten.** They
  keep polling as they always did, they stay editable — an unrelated rename or
  disable still saves, since refusing it would strand an operator with a row
  they cannot fix — and only a *change* to an unsupported mode is refused. The
  worker now logs `sync: unsupported sync_mode, falling back to poll` once per
  storage at startup, so the discrepancy is visible instead of silent, and the
  storages list labels such a row **Unsupported (polling)** instead of the raw
  i18n key. ⚠ The rejection currently surfaces as HTTP 500 with the message in
  the body; mapping it to 400 belongs in the storages handler.

- **Two load-bearing environment variables were undocumented.**
  `FILEX_TESSERACT_BIN` decides whether images are OCR'd into the content
  index — and when set it is *authoritative*, so a wrong path turns OCR off
  rather than falling back to `$PATH`, which is exactly the kind of thing an
  operator must be told before they debug an empty index. `FILEX_UPDATE_TARGET`
  is the one variable in `docs/CONFIGURATION.md` that filex **sets** rather than
  reads: it is exported into `FILEX_UPDATE_PRE_COMMAND`'s environment with the
  version about to be installed, so a backup command can name its dump after it.
  Both are now in `docs/CONFIGURATION.md`, with what happens when they are unset.

- **Helm `nameOverride` was rendered by every template and declared nowhere.**
  `_helpers.tpl` has always read `.Values.nameOverride`, so it worked — for
  anyone who read the templates. A value you can only discover by reading the
  chart's internals is not configurable in practice; it is now declared in
  `values.yaml` with what it changes (`helm template rel ./filex --set
  nameOverride=custom` → `rel-custom-*`).

- **Five of the six controls on the admin Settings page did nothing.**
  `public_url`, `sync_interval_seconds`, `log_level`, `default_locale` and
  `default_timezone` were PATCHed, written to the `settings` table, echoed back
  and re-rendered in the form — and no code on the server ever read those rows.
  The live values come from `FILEX_PUBLIC_URL`, `FILEX_SYNC_INTERVAL`,
  `FILEX_LOG_LEVEL` and `FILEX_DEFAULT_LOCALE`; there is no timezone knob at
  all. `site_name`, the one key with a reader (share-invite mail), sat on the
  same form and made the page look trustworthy, and the hints promised
  specifics — *"Used for share links and email templates"* under a field that
  changed no link.

  The five controls are gone, replaced by a line naming the variables that do
  work. Nothing is migrated: the stale rows are harmless and were never read.

- **A failed sync was painted the same grey as one that never ran.** The
  backend writes `"failed"` (`internal/sync/poll.go`); both `syncTone` copies —
  Storages and Dashboard — matched `'error'`, the spelling the *sync-runs* list
  translates to. So the badge text said "failed" while the dot, which is what
  an operator actually scans a list for, said "nothing to see". Both spellings
  are now accepted, and there is one copy of the mapping
  (`web/src/lib/syncTone.ts`) instead of two that drifted identically.

- **The dashboard's storage cards always read "0 B · 0 files".** They render
  the same rows as the storages list, which reads `stats.total_size_bytes`
  with the flat legacy field as a fallback; the dashboard read only the flat
  field, which the endpoint stopped filling when the nested `stats` object
  arrived.

- **The archive preview ignored `apiBase`.** `ArchiveViewer` hardcoded
  `fetch('/api/files/archive/list')` while every other call in
  `@brftech/filex-core` goes through the configured endpoints — so in the
  package's headline use case, an embed on a host page pointed at
  `https://files.example.com`, the zip preview posted to the *host page's*
  origin and 404'd. It now takes the endpoint from the explorer's config and
  falls back to the same-origin path.

- **`showInfoPanel` was documented public API that nothing read.** An embedder
  who set it false got the inspector toggle anyway. It now hides the toggle, as
  documented; default (and every existing embed) unchanged.

- **The SMB driver had no translations.** Seven i18n keys its descriptor names
  (`storages.driver.smb`, `fields.share`, `fields.domain`,
  `fields.dialTimeout`, `fieldHelp.smb{Share,Domain,Root}`) existed in neither
  catalogue, so SMB was the one driver whose form rendered in English inside
  the Turkish UI.

- **Documentation that described features which do not exist.** Each of these
  reads as configured and is not:
  - `FILEX_TLS_CERT` / `FILEX_TLS_KEY` (`docs/DOCKER.md`) — presented as
    "filex direct TLS". There is no TLS listener on the HTTP server and
    neither variable is read: an operator who set both served **plain HTTP**
    with no warning. The `cert_file`/`key_file` that do exist belong to FTPS.
  - `FILEX_TRUST_PROXY_HEADERS` (`docs/DOCKER.md` twice, `demo/README.md` in a
    copy-pasteable `docker run`) — never read. `X-Forwarded-*` is honoured
    unconditionally, so the security-relevant direction, `=false`, was the one
    that silently did nothing.
  - `FILEX_LIMITS_MAX_ARCHIVE_BYTES` "default 1 GiB" (`docs/BACKEND.md`) —
    neither the variable nor any archive size check exists.
  - `FILEX_LIMITS_MAX_UPLOAD_BYTES` in the nginx snippet — not a filex
    setting.
  - The audit log's response shape and its "standard action values": the key
    is `entries` (not `events`), rows wrap under `entry`, and eight of the
    listed actions are never written by anything — including `storage.add`,
    which is really `storage.create`, and `auth.login`/`auth.logout`, which
    are not audited at all. Filtering is an exact match, so those returned an
    empty page forever. The list is now the real one, generated from
    `internal/auth/audit_middleware.go`.
  - `POST /api/files/archive/extract` `"overwrite": false` — no such field;
    extraction always overwrites. `POST /api/files/archive/add` was documented
    with a `{paths, dest, compression}` body the handler rejects. `sourceDir`
    on move/delete and `mime` on `upload/init` are accepted and ignored.

- **Seven notification alert ids are declared and never emitted**:
  `replica_fail_spike`, `quota_near_full`, `quota_full`, `queue_stuck`,
  `auth_fail_spike`, `disk_full`, `update_applied`. A webhook target may name
  one, the subscription saves, and it waits forever — which reads exactly like
  a subsystem that never has a problem. The producers are not written here;
  what changed is that `docs/NOTIFICATIONS.md` now carries an **Emitted**
  column and `internal/notify/event.go` says which names have no producer, so
  nobody builds an alert on one by accident.

- **`acceptTypes`, `maxFileSizeMb` and `shareBase`** in `ExplorerConfig` are
  marked `@deprecated`/ignored — all three are declared, documented and read by
  nothing (there is no client-side size or type gate at all, and share URLs
  come from the server). `shareBase` was the example value in the
  webcomponent README, which is where an embedder would copy it from.

### Added

- **Helm: a first-class `antivirus:` block** (`enabled`, `mode`, `address`).
  v0.34.0 made ClamAV reachable as a daemon over TCP or a unix socket — the
  shape Kubernetes wants, since the filex image ships no scanner — but the
  chart documented only the `extraEnv` route. `extraEnv` still works for
  everything not modelled, including the deliberately env-only
  `FILEX_CLAMAV_BIN`. ⚠ `enabled` is tri-state (`null` = do not seed) and is
  tested for *presence*, not truth: `enabled: false`, the whole reason to write
  the key, is falsey in a Helm template and a plain `{{ if }}` would have
  dropped it silently. The chart's text says plainly that these are **first-boot
  seeds**, not live switches.

- **Umbrel's app card now carries `releaseNotes`, derived from `CHANGELOG.md`.**
  Every other store surface tells the user what changed. It is generated by
  `scripts/sync-deploy-versions.mjs` rather than typed, because a hand-written
  "what's new" that nobody remembers to update carries no version number and so
  never *looks* stale — the same failure that left three store manifests at
  `v0.4.0` for twenty-nine releases. A release with no changelog section is now
  a hard error, and `web/tests/deploy/deployVersions.test.ts` fails when the
  manifest stops matching what the changelog says.

### Removed

- `backend/Dockerfile.full`. Nothing built it — not `.gitlab-ci.yml`, not
  either GitHub workflow, which use `docker/Dockerfile` and
  `docker/Dockerfile.slim` — and it could not be built: its base image
  `brftech/filex:slim` does not exist (`pull access denied … repository does
  not exist`), and its package list used Debian names (`fonts-liberation`,
  `fonts-dejavu`) that Alpine's `apk` refuses. Its header also claimed
  "~250 MB"; the same package set, with the Alpine names it should have used,
  measures **1.43 GB**.

- `backend/Dockerfile`, for the same reasons and measured the same way. Nothing
  built it either — the real recipes are under `docker/` — it was last touched
  on 2026-05-08, its header claimed "~40 MB", and its ldflags put `$(VERSION)`
  inside single quotes with no `ARG`, so anything built from it would have
  stamped that literal string as its version. It was the base the deleted
  `Dockerfile.full` layered on; with that gone it had no remaining reader.

### Security

- **Multi-tenant isolation: the tenant boundary was enforced in about a dozen
  places and assumed everywhere else.** This closes the rest of it. It matters
  only to installs running `FILEX_MULTI_TENANT`; on a single-tenant install
  `TenantResolver` attaches no scope at all, absence means "unscoped", and every
  predicate added here passes — the ordinary admin still administers
  everything. Each change carries a single-tenant test, and those pass on the
  previous release too, which is the honest form of that claim.

  **Row ownership on the admin routes that take an `{id}`.** `tenantstore`
  confines three list queries; every route naming a row looked it up directly.
  An admin of one tenant, pointed at another tenant's ids over real HTTP,
  reached **fifteen of sixteen** of them. The worst was
  `POST /api/admin/users/{id}/reset-password`, which answered 200 and returned
  the other customer's **new cleartext password in the response body** — one
  request, one account, credential included. `GET /api/admin/storages/{id}`
  returned the storage's whole config blob, which for an S3 or SFTP storage is
  the access key and secret; `DELETE` on the same route removed the storage and
  cascaded its node rows. Also closed: storage `PATCH`/`sync`/`drift`/
  `sync-runs`, `quota/{user_id}` (both spellings), `versions/{id}`,
  `trash/{id}`, `grants/{id}`, `shares/{id}` revoke and delete,
  `sync-runs/{id}`, and `ai-tokens` — where an unchecked `user_id` minted a
  token bound to another tenant's user, which is not token management but
  identity takeover, needing no password and no login.

  Refusals are **404, not 403**: a 403 confirms the row exists, and repeated
  over an id range that is a census of the platform's other customers.

  **The same class on the user-facing API, which turned out to be worse.** The
  admin namespace was the part that had been enumerated; the `/api/files` twins
  never had been, and several of them leak bytes rather than names.
  `GET /api/files/read` accepted a raw `storage_id` straight from the client;
  `POST /api/files/share` minted a public link over any `node_id` on the
  instance (the `{path}` form of the same handler was safe, which is the tell);
  `POST /api/files/versions/restore` overwrote live bytes with no check of any
  kind; `POST /api/files/ops` and the copy/move/delete verbs enqueued work
  against another tenant's storage by id or by name; the OnlyOffice `node_id`
  form handed back a signed fetch URL redeemable at a public endpoint. Also
  closed: `stat`, the manager listing, share listing and deletion, drop-link
  creation, comments, the tag endpoints, star/recent, trash listing and
  restore, the ops queue listing, and the escrow challenge oracle.

  ⚠ These could not lean on the existing ACL check, because RBAC is not a
  tenant boundary: `storages.rbac_enabled` defaults to false, and with it off
  `acl.Effective` returns Editor for any plain user and Owner for any admin, on
  every storage. Where an endpoint's only gate was RBAC it had no tenant
  boundary at all.

  **`/api/admin/settings` is classified per key rather than gated.** One flat
  global table holds both the instance's OIDC issuer and a tenant's own logo, so
  neither a blanket gate nor a blanket pass is right. Tenant admins may write
  the bare `branding.*` namespace — which is rewritten under their own
  `tenant.<id>.` prefix — and nothing else. It is an **allowlist** on purpose:
  a denylist would make every key added later silently tenant-writable until
  somebody remembered to list it. The already-prefixed `tenant.<id>.branding.*`
  spelling is refused too, since it names a tenant explicitly and the rewrite
  passes it through unchanged. A mixed `PATCH` batch is classified in full
  before anything is written, so a refusal cannot leave a partial apply.

  **Newly gated on the supertenant**, because they drive one global row or
  process and have no per-tenant form: `/api/admin/webhooks` and the legacy
  `notifications/webhook-config` (one target list receives every tenant's event
  stream), `/api/admin/replication-targets` and `/api/admin/replica/*`,
  `/api/admin/search/{stats,rebuild}`, `/api/admin/queue`, and `/metrics`.

  **Scoped rather than gated**, because they are legitimate tenant features
  that were merely unfiltered — gating them would have removed a real
  capability: `/api/admin/duplicates` (which returned etags, so it confirmed
  that a file you already hold exists in another tenant), the `/api/admin/
  sync-runs` list, the dashboard's counters and recent-activity feed, and
  `POST /api/admin/trash/empty` — which was permanent, irreversible destruction
  of **every** tenant's trashed files by an admin of any one of them, answering
  200. The trash sweep is scoped inside the service, so the nightly retention
  worker, which carries no scope, still sweeps everything.

  **Ticketed WebSocket connections carried no tenant scope**, because the
  ticket is redeemed before the auth middleware runs; the socket then listed
  every storage on the instance and could subscribe to any tenant's folder,
  receiving live change frames and a presence roster with other tenants' names,
  e-mail local parts and avatars.

  **`POST /api/files/permissions/invite` with `create_user` was a privilege
  escalation.** `CreateUser` homes new accounts in the `default` provider, and
  `default` is the supertenant — which is confine-exempt — so a tenant admin
  inviting one address minted an account that could read every other customer's
  files. New accounts are now homed in the caller's tenant, and the
  half-created row is removed if that fails.
  `GET /api/files/permissions/resolve?email=` was a membership oracle over the
  whole platform for the same reason `ListUsers` could not catch it, and now
  answers `found:false` for a foreign account — the same shape as an address
  nobody has registered.

  See `docs/MULTI-TENANCY.md` §10 and the new §16, which named what this pass
  left open — including `GET /api/files/thumb/{id}`, which served rendered file
  previews to entirely unauthenticated callers on any install, and the two
  directory-login drivers that provisioned into the supertenant. ⚠ Those two,
  and the missing authorization on `/api/files/versions`, are closed in
  Unreleased above; §16 now carries a status column.

### Fixed

- **The handler test harness was missing the tenant-scoped store, so every
  multi-tenant handler test had been measuring the wrong thing.**
  `internal/server.New` hands handlers `tenantstore.New(store)` and keeps the
  raw store for background services; `testutil.NewTestServerWith` stopped one
  wrapper short, so `ListStorages`, `ListEnabledStorages` and `ListUsers`
  returned every tenant's rows in tests. A test asserting "this tenant sees only
  its own" could pass only if the handler happened to filter a second time by
  itself. Found by a dashboard assertion that expected another tenant's storage
  to be absent and watched it come back — the harness was wrong, not the
  product.

## [0.34.2] - 2026-09-06

### Fixed

- **An instance that was never told its public URL sent every browser to
  `localhost:5212`.** The realtime ticket built `ws_url` from `PublicURL`,
  whose default is a guess — so filex on any other port advertised a socket
  address nothing was listening on, the connection was refused, and the client
  dropped to 12-second polling **with nothing logged**. It reads as "live
  updates do not work" while the server is perfectly healthy, and it is the
  default state of any install that has not set `FILEX_PUBLIC_URL`.

  With no public URL configured the socket address now comes from the request,
  which is safe *here and only here*: the ticket goes back to the client that
  asked for it, so a forged `Host` can mislead nobody but its sender. Share
  links are mailed to third parties and still use the configured origin — a
  test pins that difference.

  The startup banner had the same fault, printing the guess under "Listening
  on". It prints the real listen address, and says so, when no public URL was
  chosen.

- Two browser tests asserted **translated strings** and so failed once English
  became the fallback language in v0.33.0, with nothing about the features
  having changed: `82-capability-gating` looked for the Turkish OnlyOffice
  message (now a stable selector), and `17-theme-locale` was fixed in 0.34.1.
  A test that fails when the UI is translated is testing the translation.

## [0.34.1] - 2026-09-06

### Fixed

- **A user who had chosen Turkish saw an English admin panel on any second
  device.** The language on the account (`users.locale`) was written by
  Profile → Save and then **never read**. Saving also set it in this browser's
  `localStorage`, so it looked right on the machine you saved it on; sign in
  from another browser, another device or a private window and the account's
  choice was ignored and the browser's language used instead.

  It was invisible while `tr` was the fallback for everybody — a Turkish user
  got Turkish either way, from the default rather than from their preference.
  v0.33.0 made the fallback `en`, which was the right change, and this
  never-read preference surfaced behind it.

  The account's language is applied wherever the user is loaded, ranked below a
  choice made on this device (the language switcher writes only locally, so it
  is the newer decision) and above the instance default.

- **A red CI suite could not stop a release, and had not.** `release.yml` now
  calls `ci.yml` as its first job and nothing publishes until it is green.

  Until now CI ran on the branch push and the release workflow on the tag
  pushed two seconds later — in parallel, unaware of each other, with no
  required status check anywhere in the repository. Measured 2026-09-06: **CI
  had been red since v0.31.0 and four tags shipped over it**, carrying the
  locale bug above. None of the release steps would have caught it: they check
  the README, the screenshots, the links, the anchors and the version
  manifests, and never run a test.

- `17-theme-locale.cy.ts` asserted the account's language while measuring the
  **browser's**: with no stored choice the app fell back to browser detection,
  so it passed on a Turkish workstation and failed on an English CI runner. It
  now pins the browser to English, so only the saved preference can satisfy it.

- `siteAssets.test.ts` failed on every public CI run: `site/` is withheld from
  the published tree, so the directory it compares does not exist there. It
  skips where the directory is absent and still gates where it is present. The
  failure also cascaded — the image-build job `needs` it, so v0.34.0's images
  were never verified on main.

## [0.34.0] - 2026-09-06

### Upgrade notes

- ⚠ **Webhook subscribers: a write that REPLACES a file now emits
  `file.updated`, not `file.uploaded`.** Until this release every write
  announced `file.uploaded` whether it had created a file or overwritten one,
  because the post-write gate hard-coded the id — no filter could tell the two
  apart. A target subscribed to `file.uploaded` in order to see edits will go
  quiet; tick `file.updated` beside it on Settings → Webhooks. A target with an
  empty event list still receives everything, and a target that only ever
  wanted new files needs no change and now gets a quieter feed.

- ⚠ **`FILEX_CLAMAV`, `FILEX_CLAMAV_BIN` and `FILEX_CLAMAV_MAX` are now
  first-boot seeds, not live switches.** The antivirus on/off state, its mode
  and the clamd address live in the settings table and are edited on
  Settings → Protection. An existing `FILEX_CLAMAV=0` survives the upgrade —
  it seeds the row — but changing it in `compose.yml` afterwards will have no
  effect, which is a change in what that variable means.

- **Redis queue users:** the pending queue converts from a list to an ordered
  set on first boot so that priority is honoured. Every queued op is kept, in
  the order the old build was about to serve them. Downgrading afterwards is
  not supported.

### Added

- **Three events the server has been emitting all along are finally
  subscribable, and a fourth that had no name at all now has one.** The
  webhook subscription checkboxes were driven by a hand-written copy of the
  backend's event list, and it had drifted: `file.upload_failed`,
  `file.infected` and `comment.added` were being delivered to targets with an
  empty allow-list but could not be ticked by anyone who wanted only those.
  `file.infected` is the one that matters — filex scans uploads for viruses and
  there was no way to ask it to tell you when it found one.

  A fifth event was worse off. The escrow announcement — *an encrypted folder
  was opened with the recovery key rather than its owner's passphrase*, about
  as security-relevant as this product gets — was written as an inline
  `notify.EventType("e2e.escrow_used")` inside a handler. It is emitted on
  every escrow unlock, and because it was not a constant, nothing that reads
  the event catalogue could see it. It is `EventE2EEscrowUsed` now and appears
  in the list with the rest.

  Every event in the list also gained a label an operator can read, in English
  and Turkish, with the wire name kept underneath the checkbox — the checkbox
  used to be labelled `file.trashed` and nothing else.

- **`file.updated` — a write that replaced an existing file no longer claims a
  new one arrived.** ⚠ **This is a behaviour change for existing webhook
  subscribers.** Until now every write on every surface emitted
  `file.uploaded`, whether the bytes created a file or overwrote one that was
  already there, so nothing downstream could tell "a document arrived in this
  folder" from "somebody edited that document" — and no filter could separate
  them, because they were literally the same event id.

  From this release the post-write gate splits them: **created →
  `file.uploaded`, replaced → `file.updated`.** A target subscribed to
  `file.uploaded` in order to see edits will go quiet and has to tick
  `file.updated` as well (the admin UI lists it next to `file.uploaded`); a
  target with an empty event list still receives everything, and one that only
  ever wanted new files needs no change and gets a quieter feed.

  It is one decision in one place — `writehook.OnFileWritten` now takes the
  kind, so every surface answers the same question from the fact it already
  had: the cache-row lookup it does anyway. That covers the browser upload
  form, WebDAV `PUT`, S3 `PutObject`/`CopyObject`, SFTP, FTPS, NFS, the AI/MCP
  write, the archive extractor and the ops-worker copy. Two paths are called
  out because they answer it differently:

  - The **staged (large-file) upload** asks the storage driver rather than the
    catalogue, one statement before the overwrite. It has to: its node row is
    published at commit time, before the bytes move, so by transfer time the DB
    has a row either way and has forgotten whether it minted it — and unlike a
    flag carried from commit, the driver's answer survives a restart in between.
  - Where a surface genuinely **cannot tell** (the DB mirror was unreachable,
    so there is no row to compare against) it reports `file.uploaded` — the
    value the event carried before, rather than a wrong claim that a file was
    edited.

- **The text editor announced nothing at all.** `/api/files/save-text` wrote
  the bytes, updated the row, re-indexed the file and scheduled the antivirus
  scan — and never emitted an event, so a file created or rewritten in the
  browser reached no webhook and no notification bell. It now goes through the
  shared gate like every other write surface (`file.uploaded` on create,
  `file.updated` on save), using the same `existing` lookup that already chose
  between an immediate and a debounced scan. It keeps its own debounced scan:
  the divergence is in the scan, never in the event.

### Fixed

- **The Office editor was the one write nothing looked at afterwards.** Every
  other write surface in filex fans out through the shared post-write gate —
  announce the change to open explorers, re-index for search, fire the webhook,
  queue an antivirus scan — and this release *extended* that gate to two more
  places. The OnlyOffice save-back was not one of them: it took its version
  snapshot, wrote the revision, refreshed the node row, and stopped. So a
  `.docx` edited in the browser kept its **pre-edit text in content search**,
  never reached a `file.updated` subscriber, left another browser with the
  folder open showing a stale listing, and — on an install where every upload
  is scanned — **was never scanned**. Office documents are exactly the file
  type macro-borne malware travels in, which is what made this the wrong gap to
  ship a release about scan coverage with.

  The callback now goes through the gate like everything else, as a
  **replacement** (`file.updated`, never `file.uploaded` — a save-back
  overwrites a document that was already there) stamped with a new
  `meta.origin: "onlyoffice"`, because the bytes are assembled and posted by
  the document server rather than by the browser that opened the file.

  ⚠ **Whether the scan runs now or on the save window is decided by the
  callback status, not by a rule of thumb.** The reason the browser's text
  editor got a debounced window is that it cannot tell a mid-session Ctrl+S
  from the last one. The document server does not make filex guess:

  - **status 2** (ready for saving) arrives once per editing session, after
    every editor has *closed* the document and the server has assembled the
    final revision — roughly ten seconds after the last one disconnects. The
    bytes are final and nobody is still typing, so it is scanned **immediately**,
    like an upload. Deferring it would coalesce nothing (there is one save) and
    would leave a finished document unscanned for up to a full window.
  - **status 6** (force save) is an *interim* save with the document still
    open. filex never asks for one and the document server does not send them
    by default, but an operator can switch them on, and then they repeat for as
    long as somebody keeps the document open — the shape of a Ctrl+S burst. It
    takes the **debounced** window, the same one the text editor uses.

  A session with force-save on therefore costs one scan per window while it is
  open plus one immediate scan when it closes. That last one is deliberate: a
  document server that dies mid-session never sends status 2, and then the
  debounced scan of the interim bytes is the only one there will ever be.

  ⚠ The driver `Stat` still lands on the row **before** the index runs, and
  that ordering is load-bearing: content re-extraction is triggered by the
  content fingerprint drifting, the fingerprint prefers the etag, and indexing
  against the pre-edit etag would refresh the metadata while leaving the **old
  text** searchable — most of the symptom being fixed.

- **`/api/admin/protection` let any tenant admin turn antivirus off for every
  tenant** — and three other instance-wide surfaces were open the same way.
  Everything under `/api/admin` has passed `RequireAdmin`, and in multi-tenant
  mode that means an admin of *some* tenant: the tenant resolver labels the
  request without denying anything, and the scoped store filters three list
  queries and nothing else. `/providers` and `/plugins` asked the extra
  question. These did not:

  - **`/protection`** — the antivirus switch, its mode and the clamd address
    moved into the global settings table in this release, so one customer's
    admin could disable scanning platform-wide or point the scanner at a host
    they control. Trash and version retention sat beside it.
  - **`/external`** — one document server, one converter, one shared JWT
    secret: repointing it is enough to read and rewrite every tenant's office
    documents in transit, and the Test button dials whatever it is given.
  - **`/auth-providers`** — the global `auth.*` rows that decide who can sign
    in to the instance at all (OIDC issuer and client secret, LDAP bind).
  - **`/update`** — replaces the binary every tenant is served by.

  All four now ask `requireSupertenant`, one predicate shared with `/providers`
  and `/plugins` rather than a second mechanism. ⚠ The check lives in the
  **handler**, not on the chi route, because the route is not the only door:
  `/api/ai/admin` mounts the same handler instances behind an `admin`-scoped
  API token, and the MCP admin tools drive those same methods in-process — a
  route middleware would have closed one of three.

  ⚠ **Reads are gated too, not only writes.** `/protection` returns the clamd
  address in force and a live reachability probe; `/external` returns where the
  document server lives. That is a map of the operator's internal
  infrastructure, handed to somebody with no standing to act on it.

  ⚠ **Single-tenant installs are unaffected, by construction.** The tenant
  resolver attaches no scope when multi-tenant mode is off, absence means
  "unscoped", and unscoped passes — there is no flag to set. The gate fails
  *closed* the other way: an authenticated user whose provider cannot be
  resolved already gets a deny-all scope, and it is refused rather than read as
  "no scope, therefore single-tenant".

  The surfaces that are still instance-wide and still ungated are now
  **named** rather than half-closed — `/settings` (the same global table, but
  `branding.*` keys are legitimately per-tenant, so it needs a per-key
  classification and not a route gate), webhooks, replication targets and
  rules, the search rebuild, the queue, the trash sweep, and a set of
  cross-tenant metadata reads. See
  [MULTI-TENANCY.md](docs/MULTI-TENANCY.md#instance-wide-admin-surfaces).

- **A 403 that had a sentence to say showed a machine code instead.** The admin
  SPA's error extractor preferred `data.error` over `data.message`, so a
  response carrying both — the two `supertenant_only` and `plugins_disabled`
  shapes — put `supertenant_only` on screen and threw away the explanation
  written for the person reading it. `message` now wins when both are present;
  the handlers that return only `error` are the majority and put the sentence
  there, so they are unchanged.

- **The plugins gate answered "plugins are disabled" before "not yours".** A
  tenant admin who may not touch the surface at all could still learn whether
  the operator had the subsystem switched on. The tenancy check runs first now.
  Single-tenant installs see no difference: no scope is attached, the gate
  passes, and a disabled subsystem still answers `503`.

- **The docs-site build no longer hands you somebody else's diff.**
  `cd docs-site && npm run build` is a mandatory release gate, so everybody
  runs it — and it was `npm run releases && vitepress build`, which refetched
  the GitHub releases and rewrote `docs/RELEASES.md` and
  `docs-site/data/releases.json` every single time, restamping today's date
  even when nothing had changed. Three people hit it in one day, each reverted
  it by hand, and one release nearly committed the churn under an unrelated
  message.

  The build now runs an offline check instead (`check-releases.mjs`: the page
  exists and lists at least one release, no network, no writes), and
  regenerating is an explicit `npm run releases` at the release step that means
  to do it. `docs/RELEASES.md` stays tracked and published — ignoring it was
  never an option, readers see that page.

  The generator is idempotent as well, which is the other half: `generatedAt`
  only moves when the release list actually moved, and neither file is written
  unless its bytes changed. So a curious `npm run releases` cannot dirty the
  tree either — only a real new release can.

- **Two gates now catch the drift that caused the above, instead of a user
  reporting it.** The UI's event list stays a hand-maintained mirror on purpose
  — the admin SPA is compiled into the server binary, so an endpoint listing
  the events could never disagree with the bundled UI and would only add a way
  for the checkbox list to render empty. What a mirror needs is a build-time
  check, so it has one: `web/tests/webhooks/eventCatalog.test.ts` parses the Go
  constants and fails when the two sets differ or an event is missing its
  English or Turkish label, and `backend/internal/notify/catalog_test.go`
  refuses an inline `EventType("x.y")` anywhere in the backend, so an event
  cannot be born somewhere the catalogue does not look.

- **The Redis queue driver ignored `Priority`.** Its pending set was a LIST
  consumed with `BLMOVE`, and a list is positional: an op's priority was
  persisted, returned by `Get` and rendered in the admin UI, and had no effect
  whatsoever on the order ops came out. So the rule the SQLite and Postgres
  drivers enforce — a scan the storage sync discovered sits one step below a
  person's upload, `ORDER BY priority DESC, enqueued_at ASC` — simply did not
  apply on Redis: a first import of 20 000 files was still FIFO and an upload's
  scan still waited behind all of it. Measured on a real Redis with that
  backlog, an interactive op waited **20.0 s** behind 18 000 sweep ops; it is
  now served **first, in 1 ms**.

  The pending set is a **sorted set** whose score encodes `priority DESC,
  arrival ASC` exactly, and claiming is **one Lua script**: the removal from
  pending, the move to running, the status write, the attempt bump and the
  release of the coalescing key happen as one indivisible step. That is
  stricter than what it replaces — `BLMOVE` moved the id and a *second*
  transaction flipped the hash, and a process that died in the gap left an op
  in no list at all, permanently `pending` in its own hash, which
  `RecoverOrphans` then dropped. Type filtering no longer mutates anything
  either: the script walks candidates in priority order and steps over the ones
  this worker cannot handle, instead of popping them and pushing them back.
  `Enqueue` is a script too, so it is one round trip rather than two and can no
  longer leave a coalescing claim behind a write that failed.

  ⚠ **What it costs:** the claim is no longer a blocking Redis command, because
  a script cannot block. A worker with nothing to do now waits on a capped
  doorbell list that every push into pending writes a token to, so an arriving
  op still wakes a worker in about a millisecond. Two honest differences: a
  token can be taken by a worker whose type filter does not match the op that
  produced it, and a drained burst can leave a few stale tokens behind. Each
  costs one extra script call, never a missed op.

  ⚠ **Upgrading:** an install already running the Redis driver has a LIST at
  the pending key, and `ZADD` against a list is a `WRONGTYPE` error. Startup
  converts it, keeping every queued op and the order the old build was about to
  serve them in — and improving it, since those ops now carry their priority.
  Downgrading afterwards is not supported.

- **A file replaced under a local storage was never noticed.** The storage sync
  exists to find changes filex did not make; on the drivers that report no
  etag — **local, SFTP, SMB, FTP**, and any WebDAV server that omits the header
  — it found none, ever. Drift was an etag comparison, and comparing two empty
  strings is never a difference, so a file swapped out underneath filex looked
  unchanged on every pass: its size stayed stale in the catalogue, its extracted
  text stayed stale in the search index (you found the old words, not the new
  ones), and since the sync began queueing antivirus scans, a clean file could
  be replaced with an infected one and nothing would ever read it again.

  Where the backend reports no etag, drift is now the file's **size and
  modification time** — the two fields every one of those drivers does report,
  both already in the listing the walk just made, so a full walk costs what it
  always did (20 000 files on local disk: 3.0–4.0 s per pass before, 3.0–4.0 s
  after, with zero drift reported on three consecutive unchanged passes).

  It catches an ordinary edit, a rewrite that keeps the same size, a file that
  grew or shrank with its mtime preserved, and a restore whose mtime is *older*
  than the row's. It does **not** catch a replacement that preserves both the
  size and the modification time, or a rewrite landing in the same clock second
  as the recorded mtime with the size unchanged — those need the content, and
  hashing every file on every pass is not a walk anyone can run. See
  [STORAGE.md → Drift detection](docs/STORAGE.md#drift-detection-what-a-replaced-file-looks-like).

  Times are compared **to the second** deliberately: Postgres stores
  microseconds, FTP's `MDTM` has no sub-second field and FAT keeps two-second
  steps, so a finer comparison would report drift on every file on every pass —
  which on an install with antivirus enabled is the whole storage re-scanned
  every sync interval, forever. Directories are exempt for the same reason:
  their row holds the cached *recursive* size, which a listing never reports.

- **`Store.MoveNode` did not update `storage_key`, so a renamed or moved file
  kept the key it had at its old path.** `storage_key` is not decoration:
  versioning (`Snapshot` *and* `Restore`), the antivirus quarantine, the
  id-addressed download in `Manager.Read` and the sync tombstone pass all hand
  it to the storage driver **in preference to** `path`. Every one of them fails
  *silently* on a miss, which is why this survived so long:

  - `Snapshot` stats the stale key, gets `ErrNotFound`, and returns "nothing to
    snapshot" — so the pre-overwrite guard passes with **zero versions written**
    and the destructive write goes ahead. A moved file lost its history.
  - The antivirus quarantine moves the stale key into `.filex-trash/`, tolerates
    the `ErrNotFound`, marks the file quarantined and drops it from the search
    index — **while the infected bytes stay live** at the real path, now
    invisible to any future rescan.
  - `Restore` writes the restored version to the stale path, leaving the real
    file untouched, then stamps the node with the restored size and etag.
  - A download by id 404s on a file the listing shows.
  - `confirmGone` stats the stale key, gets a miss and tombstones a file that
    is perfectly fine.

  The column now follows the path on a **live** row. It deliberately does *not*
  on a **trashed** one, where `storage_key` holds the original path: that is
  the only record of where `trash.Service.Restore` puts the file back, and what
  `sync.reconcileTrash` reads to tell a restorable deletion from a row that has
  to be hard-deleted.

  Existing rows are repaired by migration `00033`, because nothing else would:
  the periodic storage walk only touches `seen_at` and `UpdateNodeMeta`, and
  neither statement writes this column, so a wrong value survives every sync
  run indefinitely. The repair is live rows only, and leaves an empty
  `storage_key` alone (every reader already falls back to `path`).

- **A single write announced itself three to six times, and a 5 000-file
  extraction announced itself 5 000 times.** Both reached every open explorer
  over the WebSocket. Measured on 2026-09-06 against a real NFSv3 mount and a
  real browser.

  The NFS half is not a bug in filex's counting — NFSv3 has no "close", so
  go-nfs opens, writes and **closes** the handle on every write RPC, and filex
  commits and announces on each close. `cp -p` of a 5 MB file: 1 `CREATE` +
  5 × 1 MiB `WRITE` = **6 frames** for one file.

  What the browser actually did with 5 000 frames turned out to be the more
  interesting half, and it was not "re-render 5 000 times". The explorer already
  coalesced — with a plain trailing debounce, which **starves**: while frames
  arrive closer together than the 200 ms window, every one of them cancels the
  pending reload. Measured in a real browser watching the folder being filled:
  the first re-listing came **114 seconds** into the job, with a 40-second gap
  in the middle. The open folder sat empty while five thousand files landed in
  it.

  Two changes, both in shared code, so the web app, the desktop app and every
  `@brftech/filex` embed get them together:

  - The realtime hub coalesces per folder on the **leading edge**: the first
    change in a quiet room goes out immediately with nothing added to it, and
    everything after it is merged into one trailing frame per window (200 ms,
    doubling to 1.5 s while a burst continues, reset when the folder goes
    quiet). A merged frame carries `count`, and keeps the file's name only when
    every merged change was the same change — naming one of five thousand would
    be worse than naming none. **Nothing is dropped, only merged**: a burst
    always ends with a frame reflecting its final state.
  - The explorer's reload debounce grew a ceiling (`burstDebounce`), so a
    sustained stream from any source can no longer postpone the re-listing
    indefinitely.

  Measured, same steps, same instance, before → after:

  | | before | after |
  |---|---|---|
  | frames, one 5 MB `cp -p` over NFS | 6 | **2** |
  | frames, 5 000-file zip extraction | 5 000 | **134** (all 5 000 changes accounted for by `count`) |
  | first re-listing of the open folder | 114.1 s | **0.46 s** |
  | longest gap between re-listings | 40.5 s | **2.0 s** |
  | longest single main-thread block | 1 087 ms | **360 ms** |
  | single WebDAV PUT → frame | 24 ms | **21 ms** |
  | single NFS write → frame reflecting final state | 171 ms | **218 ms** |

  ⚠ The last two rows are the ones that decided the design. A window that
  batches nicely but delays one ordinary upload would be a regression however
  good the frame count looked, which is why the first change in a quiet room is
  never delayed at all.

  New page: [Realtime updates & presence](docs/REALTIME.md) — the socket, the
  frames, the coalescing contract, and the debounce-with-a-ceiling advice for
  anyone integrating against it.

- **The README showed a screenshot advertising the private GitLab repo.** The
  plugins picture had a `github.com/brf-tech/filex` footer in it, on a
  page whose whole job is to be read by strangers on GitHub. It had also
  outlived several releases while every other screenshot was retaken.

  The cause was not the picture, it was the gate. `admin-plugins.png` needs the
  example plugin built and really running, so the capture script builds it with
  `go build` — and on a Windows workstation the Go toolchain lives in WSL, so
  that is `ENOENT` every single time. The script logged one line, skipped the
  shot, and **exited 0**. A release step that reports success while quietly
  leaving the old file in place is not a gate.

  Two changes: the script now falls back to cross-building through WSL, and a
  shot it was asked for and could not take **fails the run** (`SHOTS_ALLOW_SKIP=1`
  for a deliberate partial run). A wrong `SHOTS_PLUGIN_BIN` is an error rather
  than a shrug, for the same reason.

- **Files that arrive *on* a storage were catalogued, indexed and never
  scanned.** Every write *through* filex is handed to ClamAV — uploads, the
  AI/MCP surface, ShareX, drop links, the editor, both restore paths. A file
  that turns up on the backend instead (`aws s3 cp` into the bucket, another
  process writing on a mounted disk, everything that was already there when the
  storage was pointed at the folder) reaches the catalogue only through the
  periodic sync walk, which created the row, fed the search index and queued
  content extraction — and never once handed the bytes to the scanner. That is
  the one place a reader most expects a scan, because an operator who turned
  scanning on believes the files in filex are scanned.

  Measured end to end on a real instance with a real ClamAV: an EICAR sample
  written straight into a storage folder, one sync pass — catalogued at the
  root, trash empty, nothing in the log. The same run now quarantines it into
  `.filex-trash/` and logs `antivirus: infected file detected … quarantined=true`.

  The walk enqueues a scan in exactly two cases: a file it **newly catalogues**,
  and a file whose **content drifted**. ⚠ Not "every file the walk sees" — the
  walk sees the same objects forever, so scanning on sight would re-scan the
  entire storage every sync interval, 96 times a day on the 15-minute default.
  Eligibility is the ordinary one, so directories, empty files, oversized files
  and anything under `.filex-trash/` or `.versions/` are refused exactly as on
  the upload path, and an infected file the sync found is quarantined exactly
  like an uploaded one.

  ⚠ **The first import of an existing storage was the case that decided the
  shape of this.** Point filex at a folder holding 20 000 files and one pass
  queues 20 000 scans. The backlog itself is right — those files genuinely have
  never been scanned — and it is cheap: 465 MiB drained in **53 s** on 4
  workers with `clamdscan`, the walk itself about a second slower (0.8 s and
  1.4 s over two runs), 1.95 MiB of queue rows. What was *not* right is where
  it left everybody else: the queue
  orders `priority DESC, enqueued_at ASC`, so at equal priority those 20 000
  rows sit ahead of whatever arrives next, and a probe standing in for an
  upload's scan, enqueued ten seconds into the import, waited **41 s** to be
  picked up. Scans the sync asks for are therefore enqueued one step **below**
  every other op filex queues; the same probe then waited **1 ms**, because an
  interactive scan only ever waits for a worker to finish the one scan it is
  holding.

  There is deliberately **no cap** on how many a pass may enqueue: a file is
  "newly catalogued" exactly once, so a per-pass cap would silently drop the
  scans it deferred and leave them unscanned forever — a bound that reads like
  safety and is really data loss. ⚠ Two honest limits go with it: without
  `clamav-daemon`, `clamscan` re-loads its ~112 MB signature database on every
  invocation — measured over the same code path, 0.25 scans/s on 4 workers,
  which puts that same import at roughly **22 hours** — and the **Redis** queue
  driver's pending list is positional and ignores `priority`, so on Redis the
  first import is FIFO.

- **External services tested green and then did not work: the admin UI and the
  running process were reading different configurations** (GitHub issue #17).
  An operator whose OnlyOffice / drawio / converter lived in a separate compose
  file configured them the only way open to them — the admin UI — watched the
  **Test** button answer 200 for each, saw `PATCH /api/admin/external/<name>`
  save and `GET /api/admin/external` reflect it, and then got **"OnlyOffice is
  not configured"** the moment they opened a `.ods`.

  Both halves were true at once because they came from different places. The
  admin API, the Test probe and `/api/files/capabilities` all read the
  `external_services` **table**. Everything that *used* the configuration read
  a snapshot of env/YAML taken once at boot: `server.New` built the OnlyOffice
  service only `if cfg.ExternalServices.OnlyOffice.URL != "" && …JWTSecret != ""`,
  so with nothing in the environment the service was **nil** and every editor
  endpoint answered `503 {"error":"onlyoffice not configured"}` forever; the
  converter URL was baked into the AI/MCP handlers the same way. Nothing an
  operator did in the UI could reach the process.

  ⚠ The reporter's "thumbnails still work" was the clue that named it. The
  thumbnailer shells out to local binaries and consults external services *not
  at all* — so the split was exactly "reads the DB" vs "reads the boot
  snapshot", and thumbnails were in neither camp.

  Reproduced against their stack (podman-shaped: a real Garage S3 over a plain
  `http://` endpoint, SQLite, admin-API configuration only). Before: Test →
  `{"reachable":true,"state":"ok"}`, capabilities → `onlyoffice_url` set,
  `POST /api/files/onlyoffice/config` → **HTTP 503**. After, same steps, same
  process: **HTTP 200** with a signed editor descriptor whose fetch URL serves
  the document (285 bytes, ZIP magic). `file_root`'s converter URL went from
  `null` to the configured URL the same way.

  **The DB row is now the single runtime source of truth**, read on every use
  (`internal/external`, 1-second cache, invalidated by the admin PATCH). Env
  and `config.yaml` are declarative configuration *for* that row: a service they
  name is re-asserted onto it at every boot, so editing `FILEX_ONLYOFFICE_URL`
  and restarting still takes effect. An admin-UI edit to such a service applies
  immediately and is reverted at the next start — `GET`/`PATCH
  /api/admin/external` return `env_managed: true` for those and the UI labels
  the card, rather than letting the operator discover it after a restart.

  ⚠ Also fixed on the way: `GET` redacts a stored secret to `"***"`, and the
  admin UI re-sends what it was shown, so a save that only changed the URL would
  have written six asterisks over a working JWT secret. `PATCH` now ignores that
  exact value.

  ⚠ And the other way the same symptom could still be reached: **a URL with no
  JWT secret**. The health probe hits `/healthcheck`, a Document Server with no
  secret configured in filex answers it perfectly happily, and the editor then
  refuses because there is nothing to sign the descriptor with — a green Test
  next to "not configured" all over again. OnlyOffice with no secret is now
  reported `unconfigured` without a probe (a reachable server proves nothing
  there), and the Test button says *which* half is missing. drawio and the
  converter need no secret and are unaffected.

- **Deleted files left their bytes in `thumbs` and `uploads`, so disk was never
  reclaimed** (GitHub issue #18).

  **`thumbs` was an unbounded leak, and had been since v0.1.** Every generator
  writes `<data>/thumbs/<node id>.jpg`; *nothing in the tree ever removed one*.
  The `thumbnails` row went away with its node through the FK cascade, so the
  catalogue looked clean while the directory only grew — a delete did not clear
  it, purging from the trash did not, removing the whole storage did not, and
  `filex thumb` had no prune. Measured against Garage: three files uploaded → 3
  cached JPEGs; trashed → still 3; purged → **still 3, 5642 B**, with the
  objects gone from S3 and the node rows gone from the database.

  Two mechanisms now release them. A **purge** (trash emptied, retention expiry,
  "delete permanently") drops the file and its row there and then, so the space
  comes back when the user asks for it. And a **reconciler** — at boot and every
  `FILEX_THUMBS_SWEEP_INTERVAL` (default 6 h, `0` disables) — deletes cached
  files whose node no longer exists, which is what repairs an install that has
  been accumulating orphans for versions. One log line per pass, including the
  quiet ones.

  ⚠ Deleting cached bytes is destructive, so the reconciler removes a file only
  when **all** of: the name is exactly `<digits>.jpg`; the database *positively*
  reports that id absent from `nodes` (a query error abandons the pass and
  deletes nothing — "I could not ask" is never read as "it is gone"); node ids
  are never reused, so an absent id cannot acquire a file later; and the file is
  older than a 10-minute grace window. **A trashed file keeps its thumbnail** —
  it is restorable, and a cleanup that cannot tell "deleted" from "restorable"
  is the failure this must not be. Measured: live kept, trashed kept and still
  servable after restore, orphan reaped (`scanned=3 removed=1 freed_bytes=2403
  kept=2`).

  **`uploads` was bounded but did not release what a purge made dead.** Staging
  for an abandoned or failed upload ages out of the idle sweeper after
  `FILEX_UPLOAD_STAGING_TTL` (24 h) — verified — but nothing shortened that when
  the file those bytes belonged to was deleted permanently. Measured against
  Garage with a failing transfer: **1,109,269 bytes** still held after the node
  had been trashed *and* purged. The staged row is now looked up **by node id**
  at purge and released with it, so only that node's bytes are touched; a row in
  state `committing` is still left alone, because its bytes are being read by a
  transfer right now. ⚠ Trashing releases nothing: for a failed transfer the
  staging area holds the file's only copy.

- **The storage sync un-deleted files, so a deletion undid itself and an
  infected file left quarantine on its own.** Deleting in filex is a *rename*:
  the bytes move to `.filex-trash/<unix>-<rand>__<name>` and the node row is
  soft-deleted and retagged to that key. The sync worker then walked the
  storage from `/` down with no idea any of that existed — it saw the object,
  found no *live* row for it, found the soft-deleted one, and cleared
  `deleted_at`.

  It fired on **every pass, on every driver, with no condition attached**:
  there is no incremental mode — poll, fsnotify and driver-watch all funnel
  into the same full `RunOnce` walk — so there is no configuration in which it
  does not happen, only ones where it happens sooner. Measured end to end
  against
  a real S3 backend (MinIO on a pinned port): delete `rapor.txt`, trash listing
  shows 1, run one sync, trash listing shows **0** and the row's `deleted_at`
  is back to `NULL` — plus a second row minted for the `.filex-trash` bucket
  itself.

  ⚠ Quarantine is the same operation. The antivirus job condemns a file by
  calling the *same* `SoftDeleteAndRetag` with the *same* key shape, so nothing
  in the catalogue distinguishes a quarantine from a user deletion — and the
  resurrection therefore expired the security control on a timer nobody set.
  There was no way for the sync to tell the two apart, and no need: both must
  survive a sync pass.

  ⚠ The rule was not careless. It was written for GitHub issue #5, where
  `UNIQUE(storage_id, path_hash)` counted soft-deleted rows and one stale
  trashed row permanently blocked a fresh create at the same path. Reviving the
  row was the only way out **of that index**. Migration `00032` makes the index
  live-only, exactly as `00018` already did for
  `(storage_id, parent_id, name)`, and takes the reason away.

  Two rules replace it:

  - **The walk skips `.filex-trash/`.** The rows for everything in there
    already exist, retagged to the very keys on the storage, and the trash
    service owns them — restore and retention purge, never a listing.
  - **A trashed row is never revived.** An object at a path where a trashed row
    still sits is catalogued as a **new node**; the trashed row stays in the
    trash, restorable and on the retention clock. Bytes that reappear at a path
    are not the file that was deleted there — someone restored something out of
    band, or a new file landed with an old name — and reviving the row would
    hand them another file's identity, history, comments and shares while
    nothing downstream ever looked at them again.

  ⚠ Anything found **live** inside `.filex-trash/` is now repaired, which is
  what heals an install that already ran the old code: a revived deletion is
  soft-deleted again (keeping `storage_key`, so restore still knows where it
  came from) and a row minted for the trash's own bytes is dropped outright.
  That distinction matters in both directions — hard-deleting the first kind
  destroys a restorable deletion, and soft-deleting the second puts a
  `.filex-trash` entry in the trash listing whose purge would delete the trash
  directory. Bytes are never touched either way.

  ⚠ `seen` no longer counts trashed objects, so a storage whose trash held more
  than 30 % of its objects trips the whole-listing tombstone guard **once** on
  the first pass after upgrading: one warning, one skipped tombstone pass, and
  the next run compares like with like.

- **Restoring never scanned anything, on either path.** Two operations put
  bytes live without passing an upload surface, and neither enqueued a scan.

  - **Version restore.** Snapshots in `.versions/` are deliberately not scanned
    when taken — every destructive write takes one, so scanning each would
    multiply the scan load by the edit rate for bytes nobody can reach — which
    left "overwrite the infected file with a clean one, then roll back" as a
    way to put an infected file live on an install where every upload is
    scanned. The scan now happens at restore, which is rare and is the moment
    the bytes become live again.
  - **Trash restore**, the neighbouring path, had the same gap and a stranger
    version of it: the trash is where quarantine puts an infected file, so a
    restore could release something ClamAV had condemned. Restoring a folder
    scans every file in the subtree it brings back, not just the row the user
    clicked.

  ⚠ Both are asynchronous, exactly like an upload: the file is live and
  unscanned until the verdict lands, and an infected verdict quarantines it
  straight back. That window is not specific to restore — it is the window
  every uploaded file has, and it is what stops a slow scanner stalling a
  write. Blocking would make restore the only write surface in filex that waits
  on ClamAV.

- **A file created in filex's own text editor was never virus-scanned**, on an
  install where every uploaded file is. Found while auditing write surfaces,
  and reproduced before it was fixed: a real instance with a scanner wired, an
  infected file typed into the browser editor, and an `ops_queue` that shows a
  `content_index` job for that file and no `antivirus_scan` job at all — the
  editor reached the storage driver, the catalogue and the search index, and
  never the scanner.

  `writehook.OnFileWritten` is the single post-write gate every upload path
  goes through, and among other things it enqueues a scan. `save-text` never
  called it, for a defensible reason: routing it through the gate would have
  queued a full ClamAV pass on every Ctrl+S. The answer taken was to scan
  nothing, which is how a text editor became a way to introduce an unscanned
  file.

  The two cases are now split, because they are genuinely different:

  - **Creating** a file in the editor — a write to a path with no catalogue row
    — scans **immediately**, exactly like an upload.
  - **Saving** over a file that already exists schedules **one** scan, half an
    hour out by default. Further saves inside that window are dropped rather
    than rescheduled, so the window starts at the first save and the scan
    cannot be pushed out indefinitely by someone who keeps typing. When it
    runs it reads the file as it stands **then** — the final state, not the
    content of the save that scheduled it. A burst of Ctrl+S costs exactly one
    scan.

  ⚠ The delay is a row in the operation queue (`ops_queue.not_before`), not a
  timer inside the process. A `time.AfterFunc` would die with the process and
  take every pending scan with it — on every deploy, which is exactly when a
  server is most likely to restart. Measured: with a two-minute window, killing
  the server mid-window and starting it again still quarantines the file.

  ⚠ "One pending scan per file" is a new `dedup_key` column on `ops_queue`,
  unique among **pending** rows only — so the key is released the moment a
  worker claims the scan, and a save arriving after that queues a fresh one.
  The asymmetry is deliberate: an extra scan wastes a scan, while a dropped one
  is the bug being fixed. Two saves landing at the same instant produce exactly
  one scan, never zero; the SQL drivers decide it with a guarded insert plus a
  partial unique index, redis with `SET NX`.

- **A Postgres-backed queue could never claim an operation.** `Dequeue` builds
  its candidate SELECT with one placeholder per requested op type, then reused
  that same argument list for the claim UPDATE, which references none of them —
  so Postgres answered `could not determine data type of parameter $1`
  (SQLSTATE 42P18) on every type-filtered dequeue. `queue.Pool` always passes
  its registered handler types, so on `FILEX_QUEUE_DRIVER=postgres` nothing in
  the queue ever ran: no antivirus scans, no content extraction, no async
  copy/move/delete. It survived because no test had ever executed this driver —
  the comment promising integration tests "when `FILEX_TEST_PG_DSN` /
  `FILEX_TEST_REDIS_URL` are set" described tests that did not exist. They do
  now, and they run the same contract against all three drivers.

- **One unhandled op type could stall a Redis-backed queue completely.** Ops
  enter the pending list on the left and are claimed from the right, but a type
  the worker has no handler for was pushed back on the **right** — returning it
  to exactly the position the next claim pops from. The worker re-examined the
  same op until it hit its retry guard and then reported an empty queue, while
  everything queued behind it waited, for as long as that op stayed pending.

- **Delayed queue operations fired at the wrong time on any server not running
  in UTC.** `ops_queue.not_before` is compared in SQL as a string, because
  SQLite has no date type, and the sqlite driver was binding a Go `time.Time`
  — which the SQL driver rendered with an offset and a monotonic-clock suffix
  (`2026-09-06 04:42:59.215071132 +0300 +03 m=+0.118370428`). That does not
  compare with `CURRENT_TIMESTAMP` at all. Measured on this driver: at
  `TZ=Europe/Istanbul` a 100 ms delay was still not runnable 400 ms later,
  while the identical test at `TZ=UTC` passed — which is why nobody had noticed,
  CI runs at UTC, and the one existing test overrode `not_before` rather than
  waiting for it. East of UTC a delayed op sat for the offset in extra hours;
  west of UTC it became runnable immediately, i.e. the delay was silently
  dropped. Times are now written the way SQLite writes them: UTC, second
  resolution. The postgres and redis drivers were always correct (a real
  `timestamptz`, and a ZSET scored by Unix seconds) and are unchanged. This
  affected the queue's retry backoff as well as the new save scans.

- **Half of filex's write surfaces put a file on the storage and told nobody
  it was there.** Reported as "I upload a file and it shows up on screen ten
  minutes later, not straight away". Ten minutes is the storage sync's poll
  interval (900 s by default), and leaning on it was the bug: that sync exists
  to *discover* changes filex did NOT make — an object dropped in a bucket with
  `aws s3 cp`, a file written on the mount by another process. A write filex
  performed itself needs no discovery.

  Measured, not read. Every surface below was driven against a running instance
  with the periodic sync disabled (`sync_mode=ondemand`), then checked three
  ways: is there a node row, is there a Bleve document, does a `change` frame
  reach a subscribed WebSocket. What that turned up:

  - **WebDAV, SFTP, FTPS, NFS and the S3 gateway** — 20 operations between
    them — recorded the row and indexed the file correctly (2-20 ms) and
    emitted **nothing**. `internal/protocolsync`, the package that exists so
    those five protocols share one implementation, had no reference to the
    realtime hub at all.
  - **The AI/MCP surface** (`/api/ai/upload`, MCP `file_write` / `file_mkdir` /
    `file_move` / `file_unzip` / `file_zip`, ShareX, the credential-free upload
    ticket) created the row and neither indexed nor emitted. A file an agent
    wrote was not findable by search and no open explorer was ever told.
  - **`file_delete` was worse than silent**: nothing removed the node from the
    search index, so a file an agent deleted stayed findable for good, under
    its `.filex-trash/…__name` alias, pointing at a path with nothing behind it.
  - **An MCP folder move left its children behind** — only the top row was
    re-homed, so every descendant row and index entry went on pointing at the
    old path, and the listing handed out a path that 404s.
  - **The async ops worker** (copy / move / delete started from the UI) updated
    the row and the index and emitted nothing — so the user who started the
    operation watched their own listing stay wrong.
  - **Archive extract and archive add wrote bytes and did nothing else at all**:
    no row, no index, no event, no frame. The extracted files existed on the
    storage and were invisible to every read path in filex.
  - **Restoring from trash never put the file back in the search index** (delete
    correctly removes it), and **restoring a version left the index holding the
    text of the version that had just been rolled back** — a content search
    returned the file for a phrase it no longer contains and missed the phrase
    it does. That one was not absent but wrong.
  - **Saving from the built-in editor** never re-indexed, so the document kept
    its pre-edit text indefinitely.

  Why the silence mattered more than it looks: an explorer with a live
  WebSocket **does not poll**. The 12 s timer in `useRealtime.ts` is the
  fallback for a socket that has *failed*. Measured in a real browser — a write
  that emitted no frame produced **zero** listing requests in 26 s and never
  appeared; the next announced write produced exactly one request 196 ms later.
  And the periodic sync does not rescue this either: it repairs rows and the
  index and emits nothing, so before this change the only thing that could
  refresh an open folder was the user navigating away and back.

  The fix adds no new mechanism. `internal/protocolsync` — already the one
  place five protocols share — gained the emitter and now announces from
  `Write`, `EnsureDirChain`, `Move`, `Trash` and `Delete`, so all five
  protocols and every future one are covered in one file. The emission is
  deferred, so it fires even on the give-up paths: the bytes are on the storage
  whether or not filex managed to write the row. The AI/MCP surface's private
  cache helpers, which had drifted into a second, worse copy of that package,
  now call into it — which is also what fixes the delete-index leak and the
  folder move, both of which `protocolsync` had solved years earlier.

  **Nothing was slow. Every failure was silence.** Against a 3-second
  acceptance line, all 46 measured operations now land three orders of
  magnitude inside it: the row appears in a listing in 1.4-13 ms, the file
  becomes findable by search in 5-29 ms, and the change frame reaches a
  subscribed client in under 100 ms — usually before the write call has even
  returned. End to end in a real browser, a WebDAV PUT into a folder an
  explorer already had open went from **never appearing** (35.8 s, zero listing
  requests, still absent) to **visible in 224 ms**, of which 200 ms is the
  frontend's deliberate debounce. No timer was shortened to achieve that; the
  numbers were already fast when the announcement existed at all.

- **A search for a file's name could report a hit with an empty index.**
  `/api/files/search` falls back to a SQL `LIKE` over node rows when Bleve
  returns nothing, gated on `storage_id`. That is deliberate and stays — but it
  means a scoped name search proves the *row* exists, not the document. Three
  of the surfaces above looked correctly indexed until they were re-probed
  without `storage_id`. No behaviour change; recorded here because it is the
  reason an earlier pass missed them.

- **Directories created over a protocol stored a non-canonical path.**
  `EnsureDirChain` built `dav/davdir` where every other producer writes
  `/dav/davdir`. `pathkey.Hash` normalizes internally, so lookups, dedupe and
  `parent_id` were all correct and the bug hid — it surfaced only in the string
  `/api/files/search` handed back. The sharp edge was `Move`, which re-homes
  descendants with `strings.TrimPrefix(n.Path, srcClean)`: an unslashed path
  matches no canonical prefix, the trim silently does nothing, and the new path
  comes out doubled.

### Changed

- **`docs/NOTIFICATIONS.md` was describing the notification system filex had
  before webhook v2.** Most visibly it stated there is *"no server-side
  per-event webhook filter"* — the filter has existed for releases and is the
  entire point of the checkboxes above. The whole page was written around a
  single global webhook, so it also said every event produces *exactly one*
  POST (it produces one per destination), that a row is `skipped` whenever
  `FILEX_WEBHOOK_URL` is empty (only when no target matches either), and that
  every file event carries `meta.origin` (only the six write events do). The
  payload table was missing `at`, `node`, `share` and `actor`; the header table
  was missing `X-Filex-Event`, `X-Filex-Delivery` and `X-Filex-Signature`, and
  scoped `Authorization` to every request rather than to the legacy webhook;
  the admin endpoint table did not mention the five `/api/admin/webhooks`
  routes; and both worked examples used `file_dropped`, an event id that was
  renamed to `drop.received`. The per-user `muted_events` / `in_app_enabled`
  fields are now marked for what they are — stored and returned, not yet
  applied by any read path.

- **ClamAV can now be a daemon on the other end of a socket, not just a binary
  on filex's own `$PATH`.** A new **mode** setting picks between `binary`
  (exec `clamdscan`/`clamscan`, as before) and `daemon` (talk clamd's protocol
  over TCP — `clamav:3310` — or a unix socket path). Set it on
  **Settings → Protection**, or seed it from `FILEX_CLAMAV_MODE` /
  `FILEX_CLAMAV_ADDR`.

  This closes a real gap rather than adding a preference. Under Docker, podman
  and Kubernetes, ClamAV is normally its own container (`clamav/clamav`), and
  the filex images ship no scanner — ClamAV plus its signature database is
  close to a gigabyte — so the only previous answer was "build your own image".
  Files are sent with clamd's `INSTREAM`, which streams the bytes down the same
  connection the command went out on, so **filex and clamd need a network route
  and not a shared filesystem**; `SCAN <path>` would have required the daemon
  to see the same file, which is exactly what a container split does not give
  you. It is also why the daemon path never writes a temp file, while the
  binary path still has to.

  ⚠⚠ **An unreachable daemon is visibly unavailable, never silently clean.**
  Every failure — a refused connection, a timeout, a reply filex does not
  recognise, clamd refusing a stream over its own `StreamMaxLength` — returns an
  error, so the queue op fails and retries instead of marking the file scanned.
  The Protection page probes clamd with `PING`/`PONG` and shows `reachable`
  with the error text beside it, because a configured-but-dead daemon would
  otherwise sit behind the same green badge as a working one.
  `GET /api/capabilities` gained `antivirus_mode` for the same reason: two very
  different deployments produce the same `"antivirus": true`.

  `deploy/compose/docker-compose.full.yml` gained a `clamav` service under the
  `clamav` profile, with a volume for the signature database.

- **The antivirus on/off switch moved into the database too**, joining the scan
  ceiling and the save-scan window. There is now a toggle on
  **Settings → Protection** instead of only `FILEX_CLAMAV=0` in compose.

  ⚠⚠ **It takes effect at the next restart, in both directions**, and the UI
  says so at the moment you flip it — turning scanning on does not start it and
  turning it off does not stop it until filex restarts, because the scan
  pipeline is wired once at boot. That is a choice: making "off" immediate
  while "on" stayed deferred would give you a control that is sometimes live
  and sometimes not, with no way to tell which by looking at it. The API
  reports `restart_pending` for as long as the stored configuration differs
  from what the running process booted with, and the page keeps a band up until
  the restart has actually happened. The mode and address settings are deferred
  the same way; the scan ceiling and save-scan window stay live, because they
  tune a pipeline that is already running.

  `FILEX_CLAMAV` is now a **seed** like its siblings: an install that had
  `FILEX_CLAMAV=0` keeps scanning off across the upgrade, and from then on the
  switch is on the Protection page. Before the row exists — the window between
  an upgrade and first-boot seeding — the variable is still honoured, so the
  documented kill switch never stops working mid-upgrade.

- `internal/dbsetting` grew **`BoolSpec`** and **`StringSpec`** beside the
  existing `IntSpec`, so a setting that is a switch or an address is a struct
  literal rather than a hand-rolled reimplementation of resolution order,
  first-boot seeding and validate-on-save. A string setting carries its own
  `Check` hook, because text has no bounds to clamp to: without one,
  `clamav 3310` is accepted at save time and discovered at scan time, by the
  file that did not get scanned.

- **Two antivirus settings moved out of the environment and into the database**,
  where the admin UI can change them without a restart: the **largest file
  scanned** (was `FILEX_CLAMAV_MAX`) and the new **editor save-scan window**.
  Both now have a field on **Settings → Protection**, both are validated when
  you save them rather than silently clamped later, and both take effect on the
  next scan rather than the next restart.

  ⚠⚠ **If you configure antivirus by environment variable, your next change to
  it will silently do nothing.** `FILEX_CLAMAV_MAX` and
  `FILEX_CLAMAV_SAVE_WINDOW_MINUTES` are now **seeds**: they are read on a boot
  where the setting has no stored row, and never again. After that the stored
  value wins, editing your compose file and restarting changes nothing, and no
  warning is printed — because a setting that already has a value is not
  unusual. Use the Protection page.

  Nobody loses their configuration on upgrade. The seed is applied per setting,
  not per family, so a setting whose row does not exist yet is seeded from its
  variable even if its siblings already have rows. `FILEX_CLAMAV_MAX` is in
  bytes while the stored setting is in megabytes — the number an admin types —
  and the conversion rounds **up**, so an upgrade can only leave the ceiling
  the same or very slightly larger, never smaller.

  `FILEX_CLAMAV_BIN` deliberately stays in the environment. The binary is a
  path this server *executes*: an admin-writable field for it would turn an
  admin account into arbitrary command execution as the filex process.
  (`FILEX_CLAMAV`, the kill-switch, has since moved to the database as well —
  see the next entry.)

- A `change` frame now carries the name of the item that changed, from every
  surface. The browser manager's own frames were the only ones that never did,
  so a client keying off `name` — to highlight the row that changed, or to let
  the hub carry a viewer's presence focus across a rename — got nothing from
  the oldest path in the product and something from all the newer ones. A
  single-item move or delete is now named; a batch still is not, because there
  is no one name to give.

- ⚠ The two rooms told about a move now hear **different** names. The source
  hears the name that left and what it became; the destination hears only the
  name that arrived. Sending the destination the source basename named a file
  that is not in that folder.

### Known issues

- **An idempotent delete now broadcasts a refresh for a no-op.** Deleting a
  path with no row and no bytes succeeds (the ops layer reports `ok`, S3
  correctly answers `204`) and now emits a `delete` frame where it previously
  emitted none. This falls out of the deliberate choice to announce on every
  path, and it matters most on S3, where rclone and restic issue speculative
  deletes routinely. Left as is: gating the emission on "a row actually
  changed" would give back exactly the case the deferral exists to protect.

## [0.33.0] - 2026-09-06

### Added

- An i18n key-parity test for the `@brftech/filex-core` catalogue
  (`web/tests/i18n/coreKeys.test.ts`). `web` already had one for its own
  `en.json`/`tr.json`; `packages/core` ships `en.ts`/`tr.ts` but has no test
  runner of its own, so the gate lives in `web`, which depends on the package.
  It also fails on an "English" value that is still Turkish — the shape the
  bug above would take once it wears a key.
- **Almost every absolute URL a multi-tenant install handed out was the
  operator's hostname, not the customer's.** Exactly one place in the codebase
  derived the origin per request — `Auth.redirectBase`, written for the OIDC
  callback and never reused. Everything else concatenated the global
  `FILEX_PUBLIC_URL`: the `/s/` and `/d/` links a tenant sends to their own
  clients, the `wss://…/api/ws` endpoint handed to the browser and opened
  verbatim, the `/s/` and `/u/` URLs returned to AI / MCP / ShareX clients, and
  four e-mails. The worst was the account-created mail: a customer's brand-new
  user received a temporary password next to a link to **someone else's** login
  page — a flow that cannot succeed, and a disclosure of the operator's
  hostname to every tenant that ever adds a user.

  The rule now lives in one place, `internal/tenanturl`, and every builder
  calls it. It has two entry points because not every URL is minted while a
  browser waits: `FromRequest` for the request-driven sites, and
  `ForStorage` / `ForProvider` for the ones reached only through a context (the
  AI/MCP share link and the upload ticket), where the tenant is taken from the
  node's storage rather than from a `Host` header that isn't there.

  **Host-header injection**: the request host is resolved with
  `GetProviderByHost`, which matches an *enabled* provider row exactly, and the
  origin is then assembled from that row's own `host` column — never from the
  request string. An unknown, disabled or absent host falls back to
  `PublicURL`, so `Host: evil.example` mints the operator's configured URL and
  `evil.example` never reaches a link or an e-mail.

  **Single-tenant installs are unchanged.** With `FILEX_MULTI_TENANT` off the
  resolver does not read the request at all (asserted, not assumed: the test
  counts store lookups and requires zero), so the call sites that have no
  request in scope lose nothing. Covered by a new multi-tenant suite that
  drives the real handlers — and, for the four e-mails, a real in-process SMTP
  server, so the assertion is on the bytes a recipient receives.- **The public export's link check had never run once.** `scripts/export-public.sh`
  ends by running `scripts/check-links.mjs` over the tree it just built and
  refusing the export if anything is dead — the guard added on 2026-09-05 after
  two dead links sat in the public README. It was wrapped in
  `if command -v node; else echo "node not found: skipped…"; fi`, and on the
  maintainer's machine it took the `else` branch **every time**: the script is
  invoked as `wsl bash -lc 'bash scripts/export-public.sh …'`, and that WSL had
  nvm and three node versions installed but `node` on no shell's PATH — the
  interactive guard in `~/.bashrc` returns before the nvm block for
  non-interactive shells, and for interactive ones a hard-coded `export PATH=`
  two lines later wiped what nvm had just added. The export succeeded, printed
  its own excuse, and nobody read the line.

  A guard that degrades to nothing is not a guard, so **no node is now fatal**,
  and the script looks for one before giving up: `PATH`, then the newest
  `$NVM_DIR/versions/node/*/bin/node`, then — under WSL — the Windows
  `node.exe`. ⚠ That last one is a *Windows* binary: handed `/mnt/g/…` it
  resolves the path against the current drive and dies with
  `Cannot find module 'G:\mnt\c\…'`, so both the checker and the tree are
  converted with `wslpath -w` first. All three branches were exercised:
  nvm node → `410 relative links … all resolve`; Windows node → the same 410;
  no node at all → exit 1 with a refusal. A deliberately dead link
  (`README.md` → `docs/MIGRATION.md`, which the export withholds) is refused
  with exit 1.

### Fixed

- **The admin UI named a private repository, and every published screenshot
  showed it.** The footer and the About page linked
  `github.com/brf-tech/filex` — the internal development repo, which
  answers 404 to anybody who is not us. `scripts/export-public.sh` rewrites that
  string on the way out, so the *shipped source* was right; a **screenshot** is
  a PNG and no rewrite reaches inside one, so the images in the public README
  showed the dead URL. They now name `github.com/BRF-Tech/filex`, which is where
  the product actually lives — including on our own installs, where a link to a
  repository the reader cannot open was never useful either.

- **Uploads over 8 MiB never reached an S3 storage over plain HTTP, and the
  sync then moved them to trash** (GitHub #16). Reported against a fresh Garage
  deployment: every upload looked like it worked, the bucket stayed empty, and
  the next storage sync put the files in the trash. Files under a few MB were
  fine. Reproduced end to end against a real Garage instance.

  Two independent defects, and the second is the one that cost the user data.

  **1. The commit could not sign a multipart part.** The browser posts files
  under 8 MiB in one request; above that they take the staged path, where the
  commit re-chunks the staging area into a driver multipart upload. Each part
  was cut with `io.LimitReader`, which drops the `Seek` method the staging
  reader has. SigV4 signs the SHA256 of the payload, so the SDK reads a part to
  hash it and then rewinds to send it — with nothing to rewind the request died
  inside the client, before a byte left the process:
  `upload part 1: operation error S3: UploadPart, failed to compute payload
  hash: failed to seek body to start, request stream is not seekable`.

  ⚠ It is the SCHEME that decides this, not the provider — measured, because
  the obvious guesses are wrong. Over `https://` the SDK sends
  `x-amz-content-sha256: UNSIGNED-PAYLOAD`: TLS already protects the body, it is
  never hashed, and a plain reader has always worked. Over `http://` the payload
  hash is what binds the body to the signature, so the body must be read twice.
  Every S3 endpoint filex had previously been pointed at is `https://`; the
  reporter's Garage, a container on a podman network, is not. The bug was
  waiting for the first plaintext endpoint, and it would have hit AWS, MinIO or
  Hetzner over plaintext just the same.

  Parts are now cut with a rewindable, length-bounded window over the staging
  reader, and `UploadPart` on the S3 driver makes any body rewindable before it
  hands it to the SDK — small ones in memory, large ones through a temp file,
  an already-seekable one passed through untouched. The driver's signature
  promises `io.Reader` and now honours it, instead of silently requiring a
  `Seeker` and failing with a message that names the SDK rather than the call.

  **2. A failed upload was silent.** The commit endpoint answers `202` as soon
  as the last chunk lands; the transfer happens afterwards in the ops worker. A
  transfer that failed there produced one `WARN` line in the server log and
  nothing else — the node stayed at `transfer_state="staged"`, which is
  indistinguishable from still-in-flight, no notification fired, and the browser
  had already drawn a finished upload. The client now waits for the transfer by
  default (the `transferring` phase, which was implemented but which no caller
  ever switched on) and reports the failure; the node moves to a new
  `transfer_state="failed"`; and a `file.upload_failed` notification, carrying
  the reason, goes to whoever uploaded.

  ⚠ While turning that wait on, a latent bug in it surfaced: it told a real
  verdict from an incidental fetch error by comparing the message to the literal
  string `"transfer failed"`, which only matches when the server sends no error
  text. Any real error message was swallowed and the poll span for ever. The two
  are now told apart by type, and a run of unreadable polls ends the wait
  instead of hanging.

- **Uploading a file over an existing one destroyed the old bytes, and nothing
  kept a copy anywhere.** `versioning.Service.Snapshot` documents its own
  contract — *"callers should invoke this BEFORE a destructive write… if the
  snapshot itself fails the caller should NOT proceed"* — and the package doc
  stated as fact that snapshots were taken on upload finalize and archive
  extract. Neither was true. Across the whole product `Snapshot` was reached
  from exactly two places: `save-text` and the versions endpoints. No upload,
  drop, ShareX, AI/MCP write, archive extract, OnlyOffice save-back, WebDAV
  `PUT` or S3 gateway write ever called it. Measured on a clean instance:
  uploading `up.txt` twice left `GET /api/files/versions?node_id=1` answering
  `{"versions":null}` with no `.versions/` tree on disk at all, while editing
  the *same* file in the browser recorded one — so version history looked like
  it worked right until the moment you needed it.

  Every destructive write now calls `writehook.BeforeOverwrite` first, which
  snapshots whatever is about to be replaced and **refuses the write** if that
  snapshot cannot be taken: 503 with `"code": "SNAPSHOT_FAILED"`, existing file
  untouched. The covered surfaces are the browser upload (single-POST and
  staged), the public drop link, the ticketed upload, the legacy presigned
  multipart finalize, ShareX, the AI/REST and MCP write/zip/unzip tools,
  archive extract and add, OnlyOffice's save-back, WebDAV `PUT`, and the S3
  gateway's `PutObject`, `CompleteMultipartUpload` and `CopyObject`.

  ⚠ On the staged path the guard runs at **commit**, not at the driver write.
  Two reasons, and both are load-bearing. Publishing the staged node flips it
  to `transfer_state="staged"`, after which `filebody` answers with the
  *incoming* bytes — a snapshot taken later would record the wrong content.
  And `transfer()` picks between two write mechanisms: on any driver
  implementing `storage.PartUploader` — i.e. S3, which is what real
  deployments run — it calls `streamMultipart` and never touches
  `storage.Writer.Write` at all. A guard hung off the driver write would have
  protected small uploads and silently skipped every large one on exactly the
  backend where it matters most. There is a test pinning the S3-shaped case
  that asserts the object really did arrive via `CompleteMultipart`.

  `save-text` is brought under the same rule rather than left as the
  exception: it used to log `snapshot failed (continuing with write)` and
  overwrite anyway, directly contradicting the contract quoted two lines above
  its own call.

  Archive extract and the AI/MCP `unzip` tool differ deliberately: they skip
  just the refused member and keep going, reporting a `refused` count distinct
  from the permanent, user-caused skips in the same loop (a zip-slip entry, a
  file/folder kind clash). If every member was refused they answer 503 rather
  than a misleading `200 {"count":0}`.

  `FILEX_VERSIONS_ON_OVERWRITE=0` turns the guard off for a deployment whose
  storage cannot afford the extra write; `FILEX_VERSIONS_FAIL_OPEN=1` keeps
  attempting the snapshot but lets a failed one through. Both default to the
  safe value and log a WARN at boot when they are not at it — the fail-closed
  default has a sharp edge worth naming, because if the object store fills up
  then every overwrite on the instance is refused until an operator changes an
  env var and restarts.

- **The S3 gateway exposed filex's own internal trees.** `.versions/`,
  `.thumbs/` and `.filex-trash/` were listable AND readable by known key
  through `/s3` — measured, `GET .versions/42/1` answered 200 — while `/dav`,
  `/sftp`, `/ftp`, `/nfs` and the browser listing had all hidden them since
  they were written. They are now refused at any depth on listing, read and
  write. This was always wrong and became urgent with the guard above: before
  it, `.versions/` held a handful of text-editor snapshots; after it, it holds
  a copy of every file any surface has ever replaced, so leaving it reachable
  would hand any S3-key holder the prior contents of files whose folders they
  may since have lost access to.

- **A failed upload named neither the file nor the reason.** The access log
  said only `method=POST path=/api/files/manager status=500`, and no
  write-failure branch logged a filename, so "which file failed yesterday
  afternoon" had no server-side answer. Both the browser upload path and the
  staged transfer — whichever of its two driver write mechanisms fails — now
  log `msg="upload failed"` with `storage`, `path`, `name`, `size` and
  `reason`.

- **`POST /api/files/archive/add` leaked a file descriptor on every error
  path.** Only the success path closed the temp file it built the archive in;
  each of the early returns dropped it. Closed with a `defer`, registered
  after the `defer os.Remove` so the two unwind in the right order.

- **filex spoke Turkish to English users, and no locale setting could stop
  it.** ~45 strings across the explorer, the viewers, the admin UI and the
  backend were hard-coded Turkish literals sitting *beside* the translation
  layer, not inside it. They are now keys in `en`/`tr` and go through `t()`.

  The widest one was the whole **convert modal**. It resolved its labels
  through a private `tt(key, fallback)` helper backed by an optional `t?` prop
  — and three separate things had to be true for it to ever produce English:
  the caller had to pass `t` (`FileExplorer.vue` never did), the `convert.*`
  keys had to exist (they existed in *neither* catalogue), and the prop's
  signature had to match `useLocale`'s (it did not — it declared
  `(key, fallback)` where the real `t` is `(key, vars)`, so passing the real
  one would have rendered the raw key, or spliced the fallback's characters in
  as `{0}`, `{1}` … substitutions). Every user in every locale read Turkish.
  The helper is gone: the modal now takes `locale` and calls `useLocale`, like
  every sibling modal, and its ten labels are real keys.

  Also: ~20 explorer toasts and the drag-and-drop overlay
  (`İşlem başarısız`, `Kopyalandı` / `Taşındı` / `Silindi`, `Kesildi`,
  `Aynı klasöre kesilemez`, `… kuyruğa alındı`, `… öğe geri getirildi`, the
  trash-retention notice, `Dosyaları buraya bırak`); the new-folder validation
  error; the presence bar's people count; the CSV viewer's row count; five
  strings in the preview modal (two of them sitting next to a correct
  `{{ t('viewer.download') }}`); and the SMTP password placeholder in
  `Settings.vue`, whose `label` and `hint` on the same line were already
  translated.

  Where a key already said the same thing it was reused rather than
  duplicated — the sharpest case being the paste toast, which was
  `t('split.cross_copy')` on the cross-storage branch and a Turkish literal on
  the same-storage branch of the *same expression*, while
  `split.copy_queued` ('Copy queued' / 'Kopyalama kuyruğa alındı') already
  existed and was exactly it.

- **A file drop notified the folder's owner in Turkish regardless of their
  language.** `Drop.notifyOwner` never consulted a locale, and its output is
  not confined to one surface: the same title and body go to the in-app
  notification bell, into the **webhook v2 `drop.received` payload** and into
  the owner's **e-mail**. The `Gönderen:` header written into the persisted
  `NOT.txt` beside the uploaded files had the same problem.

  These now follow the same `mailLangEN` selection `mail_templates.go` next
  door already used. The locale is the **folder owner's** (`users.locale`, via
  the new `Drop.ownerLocale`), not the uploader's: a drop link is opened by an
  anonymous visitor who has no account and therefore no locale, and the owner
  is the only person who reads any of it.

- The admin panel's SMTP **Test** mail sent a Turkish sentence followed by an
  English one to whatever address was typed. It now follows the acting
  admin's locale.

- `DrawioViewer`'s untranslated fallbacks were Turkish where every sibling
  fallback (e.g. `CsvViewer`'s `'Loading…'`) is English. The fallback is what
  an embedder who does not pass `t` actually reads.

- `desktop`: the drag-out diagnostic trail logged `'BULUNAMADI'` where every
  neighbouring `dragLog` value is English. This one is **not** a translation
  key — it is a developer log, and it is now `'not found'`.

### Changed

- **The storage sync no longer moves a file to trash just because the object is
  missing** (GitHub #16). This is a behaviour change, and it is the part that made the
  bug above data-shaped rather than merely broken: the upload was what failed,
  but the sync is what deleted the user's file.

  "The catalogue has a node and the backend does not list it" is not proof that
  anyone deleted anything, and it will keep happening for reasons that have
  nothing to do with deletion — a failed upload, a permissions change that hides
  a prefix, a driver that pages a listing badly, an object restored out of band.
  A sync run that answers all of those with "trash it" is a sync run that turns
  someone else's bug into lost data. The tombstone pass now has to be *right*
  about the deletion, which takes two questions:

  - **Did filex ever put the bytes there?** A node whose `transfer_state` is not
    `stored` has never been confirmed on the backend. Its absence is the
    *expected* state, not evidence, and it is never a reason to trash it. This
    also closes a race that existed even when everything worked: a sync landing
    between publishing the node and finishing the transfer would trash a
    perfectly healthy upload.
  - **Is the object really not there?** For the remaining candidates the driver
    is asked directly with `Stat`. Only a definite not-found counts as a
    deletion; an object that the listing missed but `Stat` can see is kept, and
    so is one we could not check at all, because "I could not check" must never
    read as "it is gone".

  A file genuinely deleted in the bucket still goes to trash, so deletions made
  outside filex are still reflected — the guard is about certainty, not about
  never deleting. The existing 30 %-drop guard is unchanged and still runs
  first; `Stat` only runs for nodes that survive it, so a healthy sync costs
  nothing extra.

- **A storage that has just been synced no longer reads "Never ran"** (GitHub #16).
  Two separate breaks, both on the way to the screen. `GET /api/admin/storages`
  never sent `last_sync_state` or `last_sync_error` — the admin UI has always
  read them and they exist only as the storage's last `sync_runs` row, so the
  badge fell through to its "Never ran" default no matter how many runs had
  succeeded. And the store's `syncNow` wrote an optimistic `running` and never
  refetched; since the endpoint runs the sync synchronously, that optimistic
  flip was the last thing written and the row stayed stale until a full page
  reload. The endpoint now sends both fields, and the store refreshes after a
  sync — including after a failed one, so a failure shows its state instead of
  spinning.

- **Source comments are in English.** filex was written as a private codebase, and
  a large part of its commentary — the parts that explain *why* a thing is the way
  it is, usually with a measurement or an incident behind it — was in Turkish.
  Anybody reading the code to contribute had to read those. They are now English,
  with the reasoning, the tone and the `⚠` / `⚠⚠` / `⭐` markers carried over
  rather than flattened into one-line summaries.

  Nothing that is Turkish *on purpose* was touched: the `tr` locale catalogues and
  mail templates (the product speaking Turkish to Turkish users), the Turkish
  filenames and file contents used precisely because they are non-ASCII test
  fixtures, the selectors that assert against the Turkish UI, and UI labels quoted
  inside a comment. Comments only — no code moved, and no behaviour changed.

### Documentation

- **What the site's slug-rule change actually cost, measured.** v0.32.0 moved
  docs.filex.sh onto GitHub's heading-id rule, and noted that deep links into
  headings containing `&`, `/`, an em dash, a dot, an apostrophe or a leading
  digit would change spelling. That warning is now a number: the site was built
  at both commits and the emitted `id=` attributes diffed page by page —
  **245 of 666 headings changed spelling, none disappeared**. Roughly 65 are on
  pages a stranger would deep-link (INSTALLATION, CONFIGURATION, STORAGE, MCP,
  SSO, LDAP, the docs index); the rest are `BACKEND.md`'s per-endpoint reference
  and the generated `RELEASES.md`.

  No `<a id>` aliases were added, and the reasoning is written down beside the
  anchor check in `docs/CONTRIBUTING.md` and pointed at from
  `docs-site/.vitepress/github-slug.mjs`: the old spellings existed only on the
  site and only for the seven weeks it used VitePress's rule, every link written
  against the GitHub rendering was already correct, an unmatched fragment lands
  the reader at the top of the right page rather than a 404 — and ⚠ a fragment
  is never sent to the server, so no redirect of any kind could have rescued one
  anyway.

## [0.32.0] - 2026-09-05

### Added

- **`uiProfile: 'drive'` — the explorer's shell, laid out the way an end user
  expects (GitHub #14).** The reporter of #14 came back to v0.30.1 with four
  annotated mockups: the navigation panel was right, and here is what the rest
  could look like. This is that, as a third **profile** of the same explorer —
  not a second UI. It is a strict superset of `simple`, so everything `simple`
  turns off stays off, and a non-admin signing in at `/drive` now lands on it.

  - **One primary "+ New" menu** in the panel, in place of the Upload / New
    folder pair: upload files · new folder · request files (the file-drop link,
    which opens the access modal straight on its own tab).
  - **One search field across the header**, with a ⌘K / Ctrl+K chip. The field
    searches the folder you are standing in — it sets the same `searchQuery`
    the toolbar box always did — and the chip (or the shortcut, pressed from
    inside the field) hands that query to the **command palette**, which is
    where "everywhere", saved searches and the commands live. One box, one
    shortcut, no third search surface.
  - **A filter row** under the breadcrumb: **Type · Modified · Size**.
  - **Folders and Files as labelled sections** in grid view, with the rows
    grouped rather than trusted to arrive grouped.
  - **The details panel split into Details and Activity**, with **People with
    access** (real grants from `GET /api/files/permissions`, hidden rather than
    faked when the server refuses them) and a **share-link row with a Create
    link button**. Activity is version history + comments.
  - **A storage line** under the navigation, from `GET /api/files/quota/me`.

  Nothing is deleted: density, theme, the shortcut editor, the tour and the
  other view modes moved into the header's "⋯" menu, and the view switcher and
  details toggle onto the breadcrumb row. Both locales, light and dark, a rail
  at 56px and a drawer under 560px.

  ⚠ **No People filter and no Owner column**, though the mockups draw both. A
  listing row carries no owner — `nodes.owner_id` is quota bookkeeping, nil for
  anything a sync discovered, and serialized by nothing — and the listing
  endpoint reads no owner parameter. The three chips that shipped are answered
  from fields every row already has; a fourth would have opened, offered names
  and changed nothing.

- **A Windows copy that runs without being installed.** Linux and macOS already
  had one — the AppImage runs unextracted, the mac `.zip` is unzip-and-run —
  and Windows shipped only an NSIS installer, so a locked-down work machine, a
  USB stick, or "I just want to look at my files on this laptop for ten
  minutes" had no answer at all. `filex-desktop-portable-x64.exe` is one file:
  put it anywhere and double-click it.

  **It keeps its data beside itself, not in `%APPDATA%`.** For an installed app
  the roaming profile is right; nobody wants a program scattering folders
  across their desktop. A run-and-delete copy is the opposite case, and what it
  promises is that deleting one `filex-data` folder leaves nothing of yours on
  a machine that is not yours. That covers the account store, settings, the
  log, the drag-out cache, open-with working copies — and the sync engine's
  bookkeeping, which needed the new **`FILEX_SYNC_DIR`** in the CLI: its store
  defaults to `~/.filex/sync` and holds the local trash, i.e. real copies of
  files it deleted, in somebody else's home directory. *Settings* shows the
  exact path with a button that opens it, because a promise the user cannot
  verify is only a claim.

  ⚠ **It does not update itself, and says so.** A portable `.exe` is not an
  installation: there is no install directory to patch and no installer to hand
  the running copy over to. So it takes the route the unsigned macOS build
  already established — the updater is never wired at all, nothing is
  downloaded that could not be applied, and *Settings* reports a new version
  with a **Download** button instead of sitting at "Checking…" forever. Its
  own words, not the macOS ones: updating means putting a new `.exe` over the
  old one, and the folder beside it keeps your accounts.

  ⚠⚠ **If the `.exe` sits somewhere it cannot write** — `C:\Program Files`, a
  read-only stick, a share — it falls back to
  `%APPDATA%\@brftech\filex-desktop-portable` and says so, with the path. The
  fallback is deliberately *not* the ordinary user-data directory: measured
  before it was fixed, that put the portable copy straight into the **installed
  app's own profile** — a copy carried in from outside reading and writing the
  accounts of whoever owns the machine, and, because the single-instance lock
  is keyed on that directory, exiting silently and raising the installed app's
  window whenever it was already running.

  ⚠ **Your accounts do not travel between machines.** Tokens are sealed with
  the machine's own keychain (Windows DPAPI), so a `filex-data` folder opened
  on another computer, or under another Windows account, fails to decrypt and
  you sign in again. That is the right way round for a stick left on a train.

  `scripts/portable-e2e.mjs` covers it, because a package that *opens* is not a
  package that *works* and "portable" is a claim about where files go that no
  packaging step checks: it launches the real self-extracting `.exe` and waits
  for a renderer to actually load a page, checks that the only things left in
  the folder are the `.exe` and `filex-data`, and asserts the artifact is named
  so it neither overwrites the installer (both targets emit `.exe`) nor carries
  a version (the download links resolve one fixed filename).

- **An existing installation can adopt key escrow.** v0.31.0 pinned
  `FILEX_INSTALLATION_E2E_ESCROW_KEY` at the first boot and refused *every*
  later change — including "no escrow" → "escrow". That read well and measured
  badly: both of our own deployments came out of the rollout with
  `"e2e_escrow_kid": ""`, so acting on the decision to turn escrow on meant
  discarding the data directory. And the general case is worse than ours: a
  product where escrow can only be chosen in the first second of an
  installation's life is a product where nobody chooses it, because nobody
  decides key-escrow policy before they have any files.

  Setting `FILEX_INSTALLATION_E2E_ESCROW_ADOPT=1` alongside the key adopts it,
  once. The flag is read only while the pinned record has no escrow key, so it
  is inert afterwards and can be dropped on the next deploy.

  ⚠⚠ **Adoption is not retroactive and cannot be made so.** A folder wraps its
  master key to the escrow identity when it is created; a folder that already
  exists carries no such copy, and writing one needs the folder password, which
  the server has never had. Folders older than the adoption are outside the
  escrow key permanently. filex says this in the refusal you get without the
  flag, in a WARN line on the boot that adopts, in the record, and in the
  unlock dialog of every affected folder — which now explains the missing
  Escrow tab instead of just not having one.

  The record keeps the two dates apart, because "when was this installed?" and
  "when did it gain a second key?" are different questions:
  `pinned_at` / `pinned_by` still describe the first boot, and
  `e2e_escrow_adopted_at` / `e2e_escrow_adopted_by` /
  `e2e_escrow_adoption_note` describe the adoption. `e2e_escrow_adopted_at` is
  the boundary an operator compares a folder's age against.

  ⚠ Still refused, flag or no flag: pointing the key at a **different** value,
  and **removing** it. Those two leave folders behind that the running
  configuration can no longer describe; adoption leaves nothing behind.

- **`scripts/check-doc-anchors.mjs` — a dead `#section` now fails the build.**
  `vitepress build` fails on a dead *page* link and says nothing whatsoever
  about the anchor half, so the green docs build the release process leans on
  was evidence that every page exists and evidence of nothing else. The new
  check resolves every in-page link in `README.md`, `CHANGELOG.md`, `docs/`,
  `desktop/README.md` and the npm package READMEs, and exits non-zero listing
  the ones that land nowhere.

  ⚠ It reads the heading ids out of the **built HTML** — and, for the pages the
  site does not build, out of VitePress's own `createMarkdownRenderer` loaded
  with this site's markdown options. It does not re-derive the slug rule. A
  second implementation drifts from the real one and starts reporting links
  that work, and a check that cries wolf gets deleted. The two paths are
  cross-checked against each other on every page that has both, so if they ever
  disagree the check exits 2 instead of answering confidently.

  Wired into the `lint` stage as `lint:docs`, which builds the site once and
  runs both gates against that build — the docs site had not been built in CI
  at all until now — and into `docs/CONTRIBUTING.md` → *Release process* step 3
  as a fourth mandatory closing command.

- **An existing encrypted folder can be given an escrow slot — by its owner.**
  Adopting escrow covers folders created after the adoption, which on an
  installation that has been running for a while is nobody's folders: the ones
  that matter already exist. An escrow key that reaches none of them is a key
  to an empty room.

  So when the installation has an escrow key and a folder does not, filex asks
  the folder's **owner**, at the one moment the folder password exists in a
  browser — immediately after a successful unlock. The notice says what
  accepting actually means rather than "enable escrow?": the operator gains a
  second, permanent way in, without the password, and the use-notification is
  an announcement rather than a control. It links to
  `docs/E2E-ENCRYPTION.md`, which says the same thing at length.

  Accepting rewrites only `.filex-e2e.json`. **No file is re-encrypted, moved
  or rewritten**, and the password and recovery key keep working unchanged.
  Doing nothing leaves the folder exactly as it was.

  Declining is a decision, not a delay: it is recorded in the folder's marker
  (`esc_declined`) and filex stops asking — a question that returns on every
  unlock is how people learn to click past security dialogs. The record lives
  in the marker rather than in browser storage because the unit of the decision
  is the folder: the same person on their phone is not asked again, and an
  answer that vanished with a cleared cache would be no answer. It travels with
  the folder through a move, a backup and a restore, exactly as the key slots
  do, and holds no key material.

  The way back for somebody who changes their mind is **Escrow key…** in the
  strip above an unlocked folder. It asks for the password again — the same
  proof of ownership the offer at unlock had.

  ⚠ This changes nothing about what the **operator** can do, and the threat
  model is unchanged: adding a slot needs the folder master key, which needs a
  credential the server has never held. No configuration change, admin action
  or future version reaches an existing folder. The door opens from the inside
  only. `e2e_escrow_adopted_at` keeps its meaning — the boundary of
  *automatic* coverage — and a folder whose owner granted a slot is simply no
  longer described by it.

  ⚠ The offer never appears where it could not be honoured: no installation
  key, an existing slot, a failed unlock, or a v1 marker (whose path is the
  recovery upgrade, which seals an escrow slot in the same step and discloses
  it in the same prompt).

### Fixed

- Three surfaces said an escrow key could **never** open a folder created
  before escrow — the locked-folder dialog ("today or ever"), the
  `e2e_escrow_adoption_note` the server writes into `installation.json`, and
  `docs/CONFIGURATION.md`. That was true of the operator and false of the
  folder, and it is exactly the shape of wrong information that reads as
  authoritative. All three now separate the two: no operator action reaches an
  existing folder, and its owner can grant one.

- **The backend spoke Turkish to every user, once.** The `file.infected`
  notification carried a Turkish title while all eleven of its siblings were
  English — the one message a person reads at the worst possible moment, when
  something on their server has been flagged as malware. `docs/PROTECTION.md`
  documented the Turkish string faithfully, so the page was honest and the
  product was the defect.

- **A page withheld from the public repository was published to the world, and
  the public README pointed at a file that is not there.** `docs/MIGRATION.md`
  is a migration guide for an in-tree package that was never public; the export
  script strips it. It was not in `srcExclude`, so the docs site built it, the
  sidebar linked it and search indexed it — while the *published* README and
  docs index linked a file the published tree does not contain. Neither half was
  catchable: the release process checks links in the source tree, where every
  file exists by definition, and the tree that actually ships had never been
  checked at all. `scripts/check-links.mjs` now checks it, the export script
  runs it on what it just built and refuses to call the export good if it fails.

- **A search stopped answering with a folder called Trash.** The explorer draws
  a virtual `.trash` row at a storage root, and it decided where to draw it from
  the listing's `dirname`. A search answers with the *scope* it searched, so
  `?action=search&path=main://&filter=notes` comes back carrying
  `dirname: "main://"` — byte-identical to the folder listing of that same path.
  The rule could not tell the two apart, so searching a storage root answered
  with the hits **plus** a 0-byte folder named Trash that is not a folder, is
  not a hit, and cannot be searched for.

  Measured rather than guessed: the server sends no `.trash` row for either
  action, and `isStorageRootDir` is true for both responses — the row was always
  the client's, and only the call site knows which of the two it just asked for.
  `injectTrashRow` now takes that answer explicitly. Same family as the
  `.trash` / `.shared` sentinel bugs fixed earlier in this cycle: a virtual row
  rendered somewhere it has no meaning.

  Found in a screenshot, which is the other half of the story — the picture the
  README was going to ship for `uiProfile: 'drive'` had been taken mid-search,
  so it showed the phantom next to the one real hit.

- **Search no longer typo-matches numbers.** v0.30.0's fuzzy pass applied to
  every query word, so `2026` returned `annual report 2025.docx` — ranked
  second — because one digit is one edit. All-digit words are now matched
  literally: a near-miss digit is a different year, invoice or order id, not a
  misspelling of the same one. Exact, prefix, substring and separator-blind
  matching on numbers are unchanged (`2026` still finds `invoice_2026.pdf` and
  `Budget-2026.csv`), mixed words like `v2024x` stay typo-tolerant, and a real
  word typo (`mian.go` → `main.go`) still resolves.

- The unlock-without-the-password dialog labelled the escrow field with the
  **installation's** key id whatever the folder's own escrow slot said, so a
  folder restored from another installation looked openable with a key that
  cannot open it. It now reads the folder's `kid` and names the case.

- **A phone scrolled sideways on the file explorer.** `/admin/explore` and
  `/drive/explore` measured 398 CSS pixels wide inside a 390-pixel viewport —
  the width of an iPhone 12/13/14, and the screen every non-admin lands on. The
  8 pixels were the "Admin panel" *label* in a row that is otherwise icon
  buttons; below `sm` the button keeps its icon and its accessible name and
  drops the text. The label, not the button, is what goes, because the same
  string is longer in every other language filex speaks.

- **The desktop app's pairing never finished for an admin who signed in with a
  password.** The browser half of the hand-off only ran when the app booted, so
  it caught the routes that reload the document — the OIDC callback, and the
  navigation to `/drive/explore` that a non-admin gets — and missed the one that
  does not: an admin's password login stays inside the SPA, nothing remounts,
  and the desktop app sat on its waiting screen until it timed out. The
  hand-off now runs when a session appears, whichever way it appeared. The
  pairing is still minted exactly once.

- **`filex serve` sent no `Content-Type` for `.webmanifest`.** It fell through
  to `http.DetectContentType`, which sees JSON text and answers
  `text/plain; charset=utf-8` — on every platform, not just Windows. It is now
  `application/manifest+json`, said explicitly. A *missing* manifest also 404s
  instead of being served `index.html` by the SPA fallback, which is the break
  that leaves an app un-installable while every status code says fine.

- **"Test connection" took half a minute on an unreachable endpoint.** The S3
  driver retries six times with a backoff capped at ten seconds so that a
  *sync run* rides out an object store's transient 503; run from a button, those
  attempts took a measured 23.6s and 29.7s before answering. The retry budget is
  right where it lives, so the probe got a deadline instead
  (`handlers.ProbeTimeout`, 10s) — which bounds a hung SFTP or WebDAV dial too —
  and a probe that runs out of time now says so rather than showing the SDK's
  paragraph about attempt counts. A local-path probe was and remains ~6ms.

- **A failed login told the server as little as it told the caller.**
  `local.Driver.Login` folded a wrong password, an unknown account, an account
  with no local password and *any error from the user lookup* into one
  `401 invalid credentials`. The answer is unchanged — a single unhelpful 401 is
  what stops an attacker enumerating accounts — but each case now names itself
  in the server's log, and a store failure is no longer reported as
  `ErrUnauthorized`, so `auth.LoginChain` can tell "wrong password" from "the
  directory is down". The two-factor refusals log their reason too.

- **98 of 366 in-page documentation links pointed at nothing on docs.filex.sh.**
  Every table of contents at the top of a `docs/*.md` page, the README's link
  into `MCP.md`, both npm package READMEs — anything whose target heading held
  an `&`, a `/`, an em dash, a dot, an apostrophe or a leading number.

  The links were not sloppy. They were **correct GitHub anchors**: these pages
  are read on two surfaces, and GitHub's slug rule is not VitePress's.
  `## Backup & restore` is `#backup--restore` on GitHub and was
  `#backup-restore` here; a heading reading *Token kinds — `user` vs `app`* is
  `#token-kinds--user-vs-app` there and was `#token-kinds-—-user-vs-app` here,
  em dash and all. Rewriting the 98 links to VitePress's ids would only have
  moved the breakage onto GitHub, so the site's slug rule was changed to
  GitHub's instead (`docs-site/.vitepress/github-slug.mjs`, measured against
  `api.github.com/markdown` and pinned by `web/tests/docs/anchorSlug.test.ts`).
  One rule, both surfaces — which is also what lets a single check guard both.

  ⚠ Anchors on docs.filex.sh that contained one of those characters have
  changed. An external deep link into a section whose heading holds an `&` or
  an em dash now needs the GitHub spelling, which is the one the docs
  themselves use.

- **Five headings carried a non-breaking hyphen, and the anchors into them were
  dead on both surfaces.** `## Read‑only mounts` in `STORAGE.md`, `Per‑path
  modes` and `3. Rules — per‑path modes` in `REPLICATION.md`, `S3 / S3‑compatible`
  in `STORAGE.md`, `Boot‑time backfill` in `thumbnails.md`. U+2011 is not a
  hyphen to either slugger: GitHub deletes it (`#readonly-mounts`) and VitePress
  kept it verbatim, so `#read-only-mounts` — the only spelling anyone would type,
  and the one three links used — matched neither. Invisible in every editor;
  found by comparing bytes, not appearances.

### Changed

- **The explorer no longer draws a Trash folder in listings when the navigation
  panel is already offering one.** The virtual `.trash` row exists for one
  reason: a listing with no other way into the bin. Once the panel carries a
  Trash entry that reason is gone, and what is left is a second door to the same
  place — a 0-byte row that is not a folder, sitting among real ones, while the
  panel's own Trash entry is a few inches to its left.

  The rule is about the panel, not the profile: the duplication is exactly as
  wrong in `standard` with the panel on as it is in `drive`. `trashVisible:
  false` still means no Trash anywhere, and where there is **no** panel —
  `sideNav: false`, or the default under `rootPath` — the row is unchanged,
  because that is the case it was invented for.

  Decided per panel *availability*, not per panel *visibility*: collapsed to the
  icon rail still counts (the entry is still there, one click, still labelled),
  and so does a closed drawer under 560px (the panel toggle is on screen at
  every width). Keying it to what is painted right now would add and remove a
  row from the listing every time somebody opened the drawer.

  ⚠ **Behaviour change for existing embeds.** An embed that mounts the panel —
  the default — will stop seeing the Trash row in its listings. That is the
  intent, and the destination is unchanged: the panel's Trash entry goes to the
  same place. An embed that wants the row back turns the panel off, and one that
  wants no Trash at all still sets `trashVisible: false`.

- **Two CI gates had been failing without anybody seeing them.** `gofmt -l` has
  been red on `main` since 2026-08-17: four files were genuinely unformatted,
  invisible because on a Windows checkout the same command flagged all 553 `.go`
  files for line endings, and the real signal sat under the noise. `pnpm -r lint`
  was red too — locally it printed 32 errors, 27 of them from a generated
  directory that no CI clone even has, which is how the five real ones went
  unread. Both gates are green and, more to the point, legible: the first thing
  the formatting gate did once the noise was gone was catch a file this release
  brought in unformatted.

- **The docs site is now checked, twice, by things that can fail.** Its build was
  in no pipeline at all, and a dead in-page anchor never failed anything —
  VitePress fails a build on a dead *page* link and says nothing about an anchor,
  so a green build was not evidence. It also could not build at all: an unquoted
  value containing a colon in the home page's front matter broke the YAML
  outright. The site now uses **GitHub's own heading-id rule** rather than its
  own, because these pages are read on both surfaces and the 98 in-page links
  that resolved nowhere on the site were all correct GitHub anchors — rewriting
  them would have moved the breakage rather than removed it. `lint:docs` builds
  the site and runs `scripts/check-doc-anchors.mjs` against what it emitted.
  ⚠ Deep links into docs.filex.sh headings containing `&`, `/`, an em dash, a
  dot, an apostrophe or a leading digit have changed spelling.

- **Line endings are the repository's decision, not the contributor's.** A new
  `.gitattributes` pins every text file to LF (`.editorconfig` has said so since
  the beginning; git never read it) and marks the binary fixtures explicitly.
  Nothing committed changes — every tracked blob was already LF — but a Windows
  *working tree* was CRLF, so a shell script from this checkout failed under WSL
  or Linux with `/usr/bin/env: 'bash\r': No such file or directory`, and nothing
  had ever stopped a CRLF blob from being committed by someone with
  `core.autocrlf=false`.

- **`docs/MULTI-TENANCY.md` said it was unfinished.** Its status line still read
  "in progress — Phase 1 landed on `feat/multi-tenant`" while nine of its ten
  phases were shipped, the branch was gone, and a ten-provider deployment had
  been running on the feature since v0.1.61. Three further claims in it were
  wrong in the same direction: it promised a `(provider_id, email)` unique
  migration "00015" that does not exist (00015 is `provider_cookie_domain`, and
  e-mail is still globally unique), it stated per-tenant e-mail uniqueness as
  fact, and it listed per-tenant branding as v2 when branding had shipped
  through prefixed `settings` keys.

## [0.31.0] - 2026-09-05

### Added

- **Encrypted folders can be recovered.** Until now, forgetting the password
  meant the files were gone — that was the documented behaviour and it was a
  bad one. Creating an encrypted folder now shows a recovery key **once**;
  filex never stores it and it opens the folder without the password.

  The file format did not change. Each file's key is still wrapped by exactly
  one key in its 97-byte header — that key is now the folder's master key
  rather than the password key, and the marker holds the master key wrapped
  once per way in. For a folder created before this release the two are the
  same thing, so **existing encrypted files are byte-identical and open
  unchanged**; there is a round-trip test against a frozen copy of the v0.30.1
  crypto module, in both directions, because that is the promise that matters
  most here. Such a folder cannot be given a recovery key by the server — it
  has no password to re-wrap with — so filex offers the upgrade at the one
  moment it holds one: the next successful unlock, behind a visible notice.

- **Optional operator escrow, fixed at install.** `FILEX_INSTALLATION_E2E_ESCROW_KEY`
  adds a second way into every folder created while it is set. It is RSA-OAEP:
  filex holds only the public half, `filex e2e-escrow keygen` prints the private
  half once and writes it nowhere, and the operator supplies it back when they
  need it. A stolen database therefore decrypts nothing.

  ⚠ **This is a backdoor, deliberately, and the documentation says so.** When
  the escrow key is used through filex the folder's owner is notified, and a
  forged "escrow was used" report is refused — the client must first decrypt a
  server-issued nonce sealed to the escrow key. But the notification is an
  announcement, not a control: an operator holding the private key can copy the
  marker and the ciphertext off disk and decrypt offline, with no request, no
  notification and no audit row, and filex cannot detect it. If that is not
  acceptable for your deployment, leave escrow off.

  `FILEX_INSTALLATION_` is a new prefix for settings that are fixed when the
  data directory is initialised. filex refuses to start if one of them changed,
  because for escrow the immutability is arithmetic rather than policy: a folder
  created while escrow was off has no escrow-wrapped key, and switching it on
  later cannot open it.

- **Star is a real action.** It was rendered in the list view and nowhere else,
  so v0.30.0 shipped a Starred view that a user in grid view had no way to fill.
  It is now in the context menu ("Star" / "Unstar", multi-selection aware), on
  grid and gallery cards (on hover or focus, and painted permanently once
  starred, so the Starred view is legible without hovering every tile), and on
  the keyboard as a remappable `S`.

- **Tags in the navigation panel.** Tagging has existed for a long time and
  there was no way to browse by tag inside the explorer — only an admin page.
  The panel now lists the tags that exist and opens the files carrying one.

### Fixed

- **A virtual view opened from its own URL said "Folder not found".** The
  explorer writes the current location into the address bar, so opening Trash
  put `#.trash` there — and on reload that sentinel was handed to the ordinary
  folder loader, which 404'd. Reported for Trash; Recent, Starred and Shared
  behaved the same way. Sentinels are now routed to their view before the
  request is made, so a reload lands back in the view with its own empty state,
  and an unknown dot-path is left alone because a user may own `.config`.

- **The details panel printed `.starred`.** Third surface of the same bug that
  put `.shared` in the tab strip in v0.30.0: the sentinel-to-label map had been
  written more than once. Every surface that renders a path segment now reads
  one map.

- **An app token could manage its owner's credentials.** An API token
  authenticates *as* its owner, and the embeds we run authenticate every visitor
  with one shared token injected by the host's proxy — so v0.30.0's "API keys"
  panel entry meant an embed visitor could list and revoke the credential the
  embed itself runs on, and mint S3, SSH and NFS credentials as the owner.

  A token now declares what it is. `user` is a person's own credential and
  nothing changes for it; `app` is an integration, and the four credential
  surfaces (`/api/tokens`, `/api/auth/s3-keys`, `/api/auth/ssh-keys`,
  `/api/auth/nfs-exports`) refuse it with a 403 that names the token and both
  ways out. The explorer leaves out the surfaces that belong to a single person
  — Recent, Starred, Shared with me, API keys — while Upload, the storages,
  Trash, Tags and "How to connect" stay. Existing tokens migrate to `app`,
  because the restricting direction is the safe default and these surfaces only
  matter when a browser UI is drawn.

- **`/api/files/capabilities` could refuse a caller.** Gating it on the token
  chain meant a revoked token, an unknown token username or a disabled account's
  cookie got a 403 from a public route — the login screen failing closed. It now
  annotates the caller when it can and answers everyone.

- **Moving a plaintext file into an encrypted folder is refused.** Uploads were
  encrypted client-side; move, copy and paste were plain server-side byte
  operations, so filex's own UI would put plaintext inside an encrypted folder
  with no warning. The guard is server-side and covers the ops queue, sync moves
  and the AI/MCP surface.

- **The install banner ate clicks.** Both banners are full-width fixed strips
  that paint a centred card, and without `pointer-events-none` the empty half
  sat on top of the sidebar behind it: measured, five destinations unreachable
  at 1440×900 and eleven on a taller menu. `PendingOpsTray`, the same shape in
  the same corner, already did this correctly.

- **`slim` was not slim.** The tag was built from the full recipe, so
  `docs/DOCKER.md` promised ~40 MB while the registry served **511 MB**
  compressed — `latest`, `slim` and `full` were the same image, and the two
  Dockerfiles differed by one package that nothing calls. There is now a real
  slim image: **43 MB compressed**, the binary and the embedded UI, with image
  thumbnails (pure Go) still working and everything that shells out to ffmpeg,
  ghostscript or libreoffice reported as unavailable rather than failing.

- **`docs/E2E-ENCRYPTION.md` was 183 lines of Turkish** in an English repo,
  linked from the README and published on the docs site. Translated, and
  corrected against the code while translating: it under-counted the places that
  filter the marker, missed two disabled surfaces and the refusal to nest
  encrypted folders, and its "v2 roadmap" listed six things none of which had
  shipped — those are now stated as limitations rather than promises.

### Changed

- **Docker images build natively per architecture.** arm64 was emulated, and the
  emulated part was `apk add libreoffice` plus a JRE — the worst possible thing
  to run under QEMU. Each architecture now builds on its own runner and the tags
  are joined from the digests, so no tag exists until both have landed. The
  docker job also no longer waits for goreleaser: it builds its own binary from
  source and took nothing from the Release, so the dependency only lengthened
  the critical path.

- **The browser suite is a gate.** Cypress defaulted to `https://fm.example.com` —
  the live deployment — and ran in no pipeline. It now boots its own instance
  and runs in CI. Getting there meant fixing 19 red specs, several of which had
  been passing for the wrong reason: one matched the sidebar link instead of the
  dashboard it was meant to assert on, and two asserted a capability slot that
  only existed because production still carried a row from an older version.
  227 of 227 pass, and the run went from 9m58s to 1m25s once the service worker
  stopped re-precaching the bundle between tests.

### Added

- **Encrypted folders can be recovered.** Until now, forgetting the password to
  an E2E-encrypted folder destroyed it — the documentation said so, and it was
  true. Every folder created from this release on gets a **user recovery key**:
  160 bits, shown exactly once when the folder is created, never stored by
  filex, and enough to open the folder without the password. It is a password
  equivalent, so keep it somewhere other than the password.

  The file format did not change. A per-file key was already wrapped by one
  folder key; that key is now a **folder master key** held in the marker,
  wrapped once per way of reaching it. Adding a recovery path costs one more
  wrapped copy of 32 bytes, not a re-encrypt of anything — which is why not a
  byte of anyone's existing data was touched.

- **Optional key escrow for operators**, fixed at install time via
  `FILEX_INSTALLATION_E2E_ESCROW_KEY`. `filex e2e-escrow keygen` mints the pair;
  the server gets the **public** half only, so it can seal new folders to the
  escrow identity and open nothing. The private half is the operator's, and a
  stolen filex database still decrypts nothing. Using the escrow key notifies
  the folder's owner, and the notification is *evidence*: the client must
  decrypt a server-issued challenge sealed to the escrow key before the event
  is recorded, so it cannot be forged — and, stated plainly in the docs, cannot
  be relied on either, because an operator holding the private key can decrypt
  offline without ever asking filex.

- **Install-time settings, as a convention.** Anything prefixed
  `FILEX_INSTALLATION_` is recorded on the first boot and frozen; filex refuses
  to start if it later disagrees with the environment, and says what changed and
  what the operator can and cannot do about it. Escrow is the first of these,
  and the reason is arithmetic rather than policy: a folder created while escrow
  was off carries no escrow-wrapped key, so switching it on later cannot open
  it, and a server that started anyway would be claiming a capability it has
  over only half its data.

### Fixed

- **filex's own UI would put a plaintext file inside an encrypted folder.**
  Uploads are intercepted and encrypted in the browser, but paste,
  drag-and-drop and duplicate are server-side byte copies that never touch the
  crypto — so a file dragged into an encrypted folder was stored exactly as it
  arrived, looked like its encrypted neighbours in the listing, and nothing
  warned anyone. The reverse was as bad: a file moved *out* stayed encrypted
  somewhere no password prompt would ever appear.

  The server cannot fix either by encrypting or decrypting, because it has no
  key, so it refuses: a copy or move may not cross an encryption boundary
  (HTTP 409, naming the file). The rule lives in one place and every transfer
  surface calls it — the ops queue, the synchronous move and the AI/MCP move —
  rather than in the one client that happened to notice.

### Changed

- **Folders created before this release keep working, untouched.** Their format
  is read as a first-class path, not a migration shim, and the test suite proves
  it against a frozen copy of the v0.30.1 module rather than a re-creation of
  it. They cannot be given a recovery key by the server, because that needs the
  folder password and filex does not have it — so filex asks at the one moment
  it does: the next successful unlock. The offer is a visible strip with the
  consequences spelled out, including that accepting on an escrow-enabled
  installation also gives the operator a key. Declining changes nothing.

- The create-folder warning no longer says recovery is impossible, because for
  new folders it is not.

## [0.30.1] - 2026-09-05

### Fixed

- **The tab strip printed the raw sentinel for the new views** — a tab opened on
  Recent, Starred or Shared with me read `.recent`, `.starred`, `.shared`.
  Trash was right, and that is the whole story: the sentinel-to-label map was
  written twice, and when the three new views arrived only the breadcrumb copy
  was extended. A second copy of a mapping is a second chance to forget it, and
  the symptom hides itself — the old view keeps working, so it reads as "only
  the new one is missing something" rather than as a bug. There is one map now
  (`lib/listing.ts`), and every surface that renders a path segment reads it.

## [0.30.0] - 2026-09-04

Everything here came out of two issues opened by the same person, and the most
useful thing in the release is the one we got wrong: v0.29.0 fixed filename
search and **nobody could see it**, because the index only gains new fields for
documents that are re-indexed and nothing ever rebuilt it. He measured our own
demo, correctly concluded that nothing had changed, and reported that content
search "doesn't work at all" — it was the same cause. A fix an existing install
cannot reach is not shipped.

### Added

- **A collapsible navigation panel in the explorer.** Upload as the primary
  action, then Recent, Starred, Shared with me and Trash, then the storages the
  caller can see, with the ones reached through a grant marked as shared. It
  collapses to a 56px icon rail rather than disappearing — a panel that vanishes
  takes its own way back with it — and below 560px it is a drawer instead of a
  column, so the listing keeps its width (measured: 388px with the drawer open
  and closed). The collapsed choice is remembered per viewer.

  It lives in `@brftech/filex-core`, so the web app, the desktop app and every
  embed get the same panel from one implementation. `sideNav` turns it on or
  off; the web component also takes a `sidenav` attribute for hosts that never
  touch JavaScript.

- **`uiProfile: 'standard' | 'simple'`.** The reporter's argument was that most
  of his users are not in IT and will not relearn a file manager: tabs, a split
  pane, four view modes and mount instructions are a power-user tool. `simple`
  turns those off and expands the panel; nothing is removed from the build, and
  an embedder can set either profile. The admin panel keeps today's defaults.

- **Connections and API keys from inside the explorer.** Both surfaces existed
  and neither was reachable: our own web app wired the buttons itself, so an
  embedder mounting `<filex-explorer>` gave their users no way to see how to
  mount a drive or to mint a token. `SelfTokensModal` has moved out of the web
  app into the shared component and the web copy is gone.

  Why it had never moved: the web version hid the write and delete scopes from
  viewer accounts by reading a store only the web app has. The shared component
  does not reproduce that. It offers every scope and lets the server refuse —
  which it already does, in words worth reading (`scope 'write' is not
  available here`). Asking is not granting, and a UI-side role check hides the
  surface from exactly the accounts that need it. For a year the only place to
  mint the token the FTPS guide names was the admin panel.

- **`GET /api/files/manager/shared-with-me`** — the nodes a caller holds a grant
  on. The data existed in `file_grants`; the only listing over it was
  path-scoped and owner-only, so "what has been shared with me" had no answer.
  Tenant-scoped explicitly, like search.

### Fixed

- **The search index now repairs itself after an upgrade.** On start it compares
  the document schema it was built with; if it is behind, it builds a
  replacement **alongside** the live index and swaps it in atomically. The old
  index answers every query until the swap, and extracted text is carried across
  document by document rather than re-derived — which is what makes this safe to
  do automatically. The blackout that argument was made against in v0.29.0 does
  not happen: measured on 20 202 documents, the rebuild took 2.96 s and 601
  searches issued during it returned 0 errors and 0 missing hits.

  An interrupted rebuild is discarded and retried; a crash during the swap
  restores the known-good index; it refuses and keeps serving the old index if
  the disk cannot hold both. `FILEX_SEARCH_AUTO_REBUILD=0` turns it off — but
  off by default would reproduce exactly the failure it exists to fix.

- **Filename ranking is now a port of VS Code's Quick Open scorer.** The
  reporter's words were "VS Code does this fine, I can't do it here", and he was
  pointing at a specific method, so we ported it: a subsequence match scored
  with position bonuses (start of name, after a path separator, after `_ - .`,
  camelCase humps, runs of consecutive characters), the query split into pieces
  matched independently so **word order does not matter**, and the filename
  scored separately from its folders and weighted above them. Bleve still
  retrieves the candidates; the scorer re-ranks them and, crucially, **drops
  candidates that do not answer every piece**.

  Measured against the corpus he tested on: `Code main` went from nine results
  to one. `main code` finds `Code/main.go` too. `Code/main.go` and
  `example/main.go` stopped being the same thing to the search. Exact filename
  still outranks prefix, which outranks everything fuzzy — now asserted by a
  test rather than emergent from merged relevance scores.

  Edit distance stays, ranked below the subsequence pass: `mian.go` finds
  `main.go`, which Quick Open itself would not.

- **Multi-word content search was an OR**, so adding a word *widened* it. That,
  not the filename side, was where most of the noise came from — seven of the
  nine results for `Code main` were files that merely contain the word "code".
  Every word is now required.

- **The typo pass almost never fired.** It was gated on the number of candidates
  the index returned rather than the number that survived filtering, so a query
  that retrieved plenty and kept none still counted as "enough".

- **The recently-opened tray read the wrong field** (`entries` from an endpoint
  that answers `nodes`), so it was empty on every server that ever served it.
  It was also unreachable — the toolbar declared the event that opens it and
  never emitted it. The panel is now the working surface.

- **Starred and Recent rows were unopenable in multi-storage mode**: the rows
  carried no storage name, so no `storage://path` could be built for them.

- **`docs/WEBDAV.md`, `docs/LDAP.md` and `docs/METRICS.md` sent people to
  "Settings → API tokens"** — a screen that has never existed in the product.

- **The web-component embedding example authenticated nothing.** It assigned
  `config` after the import that registers and mounts the element, so the first
  folder request went out without credentials: the explorer rendered and the
  listing said "could not load this folder".

- A query could arrive while the search index handle was being swapped, because
  the read lock was released before the query ran rather than after. One error
  in 269 hammered queries; now impossible by construction.

## [0.29.0] - 2026-09-04

### Added

- **Open an Office document that lives on your own computer, in the editor your
  server runs.** Double-click a `.docx`, `.xlsx` or `.pptx` (ten Office types in
  all) and the desktop app opens it — on a machine with no Office installed.
  Most Linux desktops have none, many Macs have none, and plenty of Windows
  machines have none either; filex already had a perfectly good editor and the
  documents on those disks had no way into it.

  Two routes, picked per document. A document inside a folder you keep on this
  computer opens as **itself** — no copy, no write-back, saving goes to the
  server and sync brings it down again. A *paused* pair is deliberately not
  treated as one: the save would reach the server and never come back, and you
  would believe you had saved. Anything else is copied to a scratch area on the
  server, edited there, and written back over the original path on every save,
  with a strip along the bottom of the window naming the file the whole time.

  The write-back is where an editor destroys work, so: the replace is atomic
  (temp file in the same directory, then rename — never across devices, never a
  partial write over the document); a document deleted while it was open is not
  resurrected, its bytes are kept beside it as `<name>.filex-recovered-<time>`;
  a refused rename keeps the edit, says so in a dialog *and* a notification, and
  names where it kept it; the scratch copy survives a grace period after the
  window closes, because OnlyOffice posts its save roughly ten seconds after the
  editor disconnects and deleting on close would discard the last edit of every
  session; and a sweep at next start clears whatever a crash left behind.

  Registration is deliberately conservative. The installer adds filex to the
  **"Open with"** list and changes nothing that is already set: electron-builder's
  own `fileAssociations` macro writes the *default* ProgId for each extension —
  it takes the file type at install time, which on a machine with no Office is
  precisely how filex would become the handler without anyone being asked — so
  the Windows registration is hand-written instead, and macOS registers as
  `Alternate` rather than `Default`. Making filex the default is always an
  explicit action: `xdg-mime` does it on Linux from Settings, Windows opens the
  default-apps pane (`UserChoice` is hash-protected and cannot be set by an
  application, and filex does not pretend otherwise), and macOS explains the
  Finder route. See [docs/DESKTOP.md](docs/DESKTOP.md#opening-documents-from-your-computer).

- **An end-user front door: `…/drive`.** A non-admin account has always landed
  in the file manager rather than the admin panel — the router redirected them
  out of it and every `/api/admin/*` route re-checked the role — but everything
  around that screen said otherwise. The URL was `/admin/explore`, the browser
  tab said "filex Admin", and the login form said *"Use your filex admin
  credentials"*. Deploy filex for a team and each of your users was told three
  times, before seeing a single file, that this was an administrator's tool.

  The same app is now also served at `/drive`, with a per-route document title
  and login copy written for everyone. `/admin` is untouched and every existing
  bookmark still resolves. (Reported as #14.)

- **`tag:` and `-tag:` filters in search.** Tags have been a filex feature for
  a long time and were not in the search index at all, so `tag:invoice` searched
  for a *file named* "tag:invoice" and came back empty. They are now a filter
  rather than a search term, combinable with free text (`invoice 2026
  tag:paid`), resolved against the database so a re-tag is visible immediately,
  and applied before the result limit so `limit` counts filtered rows. A tag
  that does not exist returns nothing — never the whole storage.

- **The filex.sh landing page is in the repository**, at `site/`, deployed with
  `scripts/sync-site.sh`, and covered by the release documentation audit. It had
  lived only on the static host, so there was nothing to edit and nobody edited
  it: it still described roughly v0.14 — no desktop app, no sync, no protocol
  endpoints, no `filex mount`, no LDAP, no plugins, no search, no
  multi-tenancy — and claimed five storage drivers when there are six plus a
  plugin API.

### Fixed

- **Filename search depended on which separator the file's name used.**
  `invoice 2026` did not find `invoice_2026.pdf`, and `main go` did not find
  `main.go`, while `foo bar` found `foo-bar.txt` — an inconsistency with a
  single cause. Name search was a disjunction of an analysed match and an
  unanalysed `*term*` wildcard: the wildcard half cannot match anything once the
  query contains a space, leaving only the analysed half, whose tokeniser splits
  on `-` but joins on `_` and keeps `main.go` whole. So whether search worked
  was decided by the file's punctuation.

  Names are now indexed a second time in a normalised form (every run of
  non-alphanumerics collapsed to one space) and the query is normalised the same
  way, so `.`, `-`, `_` and a space are interchangeable. Multi-word queries
  require one `*word*` wildcard per word instead of one over the whole string.
  The normalisation is done in Go rather than as a Bleve field mapping on
  purpose: a mapping is frozen into the index when it is created, so a fresh
  install and an upgraded one would have analysed the same filename two
  different ways. (Reported as #15.)

- **One typo is now forgiven.** `mian.go` finds `main.go`. The fuzzy pass runs
  only when the strict pass came back short of the limit, and its hits rank
  below every exact and prefix match. Edit distance scales with word length
  (none at three characters or fewer, one up to seven, two above).

- **Ranking is now decided, not inherited.** The order is exact filename →
  prefix → name → path → fuzzy → content, asserted by a test. It had been
  whatever merging two queries' relevance scores produced: measured before this
  change, `report-final.txt` ranked *above* `report.txt` for the query `report`.
  Exact matching compares the name both with and without its extension.

- **The same query meant different things in different boxes.** `GET
  /api/ai/search` and the MCP `file_search` tool passed the raw string through,
  so `tag:source` was read there as a filename and `invoice 2026` found nothing
  — while the toolbar and `/api/files/search` understood both. All four now
  share one parser and one fallback plan.

- **The explorer's onboarding tour described a search that does not exist.** It
  said "This box filters the current folder"; the toolbar search covers the
  whole storage, and every storage when the multi-storage root is open. Its
  placeholder said "File name". Both were wrong before this release and are
  fixed in the shared component, so every surface gets the correction.

- **The explorer told a non-admin to configure a storage.** With no visible
  storage it showed "No storages configured yet" — for a `user` account that
  almost always means nothing has been *shared* with them, so it sent people to
  fix something they have no permission to fix.

- **An installed PWA ejected itself into a browser tab.** The manifest scope was
  `/admin/`, so the first navigation to the new `/drive` front door left the
  installed app. The manifest scope now covers both; the service worker's scope
  and the manifest `id` are deliberately unchanged (a changed `id` turns every
  existing install into a second app).

## [0.28.0] - 2026-09-03

### Fixed

- **LDAP was configured, initialised, printed in the boot banner — and
  unreachable from every login path.** With `FILEX_AUTH_DRIVERS=local,ldap` a
  directory account was answered `401 invalid credentials` even with the right
  password, in roughly **350 microseconds**: less than one LDAPS round trip, so
  the request never reached the network at all. Nothing was logged, and the docs
  described the behaviour that was missing ("filex tries each enabled driver in
  order"), which made it read as a directory misconfiguration to everyone who
  hit it. Three separate gaps produced it, and each would have been enough on
  its own:

  - the bootstrap assigned the login handler's single `LoginDriver` in the
    `local` case only, so the directory driver was never the one it held;
  - `*ldap.Driver` did not satisfy `auth.LoginDriver` at all — no `Logout` — so
    it could not have been assigned even by hand. The compiler had never been
    asked the question;
  - `ldap.Login` ended with `return user, "", nil` under a comment saying the
    caller would mint the session. No caller did: a *correct* password would
    have handed back an empty cookie, a successful login presenting as a failed
    one.

  Password drivers are now chained (`auth.LoginChain`) in the order they appear
  in `auth.drivers` / `FILEX_AUTH_DRIVERS`. `local` first is deliberate — it is
  a hash compare against a row filex already holds, so `admin@local` and every
  break-glass password stay answerable while the directory is unreachable.

- **A directory account could sign in to the web UI and still be refused by
  WebDAV, SFTP, FTPS, S3 and NFS.** Those protocols check the password against
  `users.password_hash`, which is **empty by construction** for a directory
  account — filex never learns the password. The refusal was identical to a
  wrong one. They now ask the directory when the local table cannot judge
  (`auth.ldap.protocol_login`, on by default). The local hash is still tried
  first, TOTP accounts are still refused on every protocol, and a successful
  check is cached for five minutes exactly as a local one is — without that,
  each request of a WebDAV `PROPFIND` storm would be a fresh LDAPS bind.

- **A search failure and "no such user" were the same answer.** An unreachable
  directory, an expired service account and a typo in `base_dn` all came out as
  `unauthorized` with nothing in the log. Transport and protocol failures are
  now reported and logged apart from a rejected password.

- **`user_filter` silently broke with more than one placeholder.** It was filled
  with `fmt.Sprintf`, which consumes one argument per verb, so the standard
  Active Directory filter that accepts either address form became
  `(userPrincipalName=%!s(MISSING))` — a filter matching nobody. Every `%s` is
  now filled with the same escaped identifier.

- **Searches used a size limit of 1.** Active Directory answers a subtree search
  from the domain root with continuation references alongside the match, and a
  server counting those against a limit of 1 can answer "size limit exceeded"
  instead of the entry. The limit is 2, and a filter that genuinely matches two
  accounts is refused with a warning rather than resolved to whichever came
  first.

### Added

- **`auth.ldap.ca_file` / `FILEX_LDAP_CA_FILE`** — a PEM bundle for a private or
  internal CA, **appended** to the system trust store (the public roots keep
  working) and applied to `ldaps://` and StartTLS alike. Previously the only way
  to reach a directory behind an internal CA was to rebuild the container's
  `/etc/ssl/certs/ca-certificates.crt`. The file is read at boot, so a wrong
  path is a startup error rather than a login failure hours later.
- **`auth.ldap.protocol_login` / `FILEX_LDAP_PROTOCOL_LOGIN`** — set `false` to
  keep directory passwords on the login form only and require an API token on
  the file protocols.
- **The Helm chart can configure LDAP.** `auth.ldap.*` in `values.yaml` renders
  the `FILEX_LDAP_*` variables; the chart listed `ldap` as a valid driver while
  offering no way to configure it, so `drivers: "local,ldap"` produced a driver
  that failed `Init` and was skipped.

## [0.27.6] - 2026-09-01

### Changed

- **Release tags are signed from here on, and there is finally a key to sign
  them with.** `CONTRIBUTING.md` had said `git tag -s` for months while no
  signing key existed on the release machine, so every tag through v0.27.5 is a
  plain annotated one — an instruction nobody can follow is not a policy, it is
  a lie the document tells. The step now also requires `git tag -v` to answer
  `Good signature` before the tag is pushed, and says where the key and its
  passphrase live. Nothing in the shipped software changes in this release.

## [0.27.5] - 2026-09-01

### Fixed

- **A transient `503` from an object store sank the whole upload.** The retry
  budget was widened to six attempts a release ago, and a test proves a 503 is
  classified as retryable — but for an upload none of that could ever fire.
  The SDK rewinds a request body before retrying it, and every upload surface
  hands the S3 driver a plain stream (the handler sniffs the first bytes to
  detect the type and rejoins them), so there was nothing to rewind: the second
  attempt died before it was made and a brief upstream wobble became a
  permanent failure, reported as *"failed to rewind transport stream for retry,
  request stream is not seekable"* — a message about filex's plumbing rather
  than the outage behind it. The budget was real for listings and reads and a
  no-op for writes. An upload that declares a size of at most 8 MiB is now held
  in memory while it is sent, so a retry replays it byte for byte; a larger one
  streams through as before, because buffering every body would trade a rare
  failed upload for an out-of-memory kill. See
  [STORAGE.md](docs/STORAGE.md#s3--s3-compatible).

### Changed

- `pnpm run build:packages` / `build:web` / `build` now quote their workspace
  filters in a way `cmd.exe` also understands. They matched nothing on Windows
  — the single quotes reached pnpm literally — so `build:all`, a documented
  release step, could not run on a Windows workstation at all.
- `CONTRIBUTING.md` says what to do when Go lives in WSL and pnpm does not:
  `build:all` ends in a plain `go build`, and the screenshots boot that binary.

## [0.27.4] - 2026-08-29

### Fixed

- **The Helm chart shipped a 23-release-old image.** `values.yaml` leaves the
  image tag empty, which the chart resolves to `.Chart.appVersion` — so
  appVersion is the version a Helm user actually runs, and it had been sitting
  at `v0.4.0` since it was written. It now tracks the release, moved by
  `scripts/sync-chart-version.mjs` as part of the release steps, and
  `web/tests/deploy/chartVersion.test.ts` fails the build if the two ever drift
  again — and the release workflow itself now refuses to publish a tag whose
  chart is behind, before a single artefact is built. **Every release updates
  the chart; it is a step of the process, not a chore to remember.**

## [0.27.3] - 2026-08-29

### Fixed

- **A folder dragged out of the desktop app arrived empty.** The watcher that
  finds where a stand-in landed was started *after* `webContents.startDrag()` —
  and on Windows that call hands control to the operating system's own drag
  loop, which does not return until the user lets go. The watcher therefore
  went up after the drop had already happened. Worse, a recursive `fs.watch`
  whose event loop is blocked does not deliver the change late; it misses it
  outright (measured: a file created during a 4-second block was never
  reported, before or after). Two changes: the watcher is armed **before** the
  drag, and it runs in a **worker thread**, whose loop keeps running while the
  main thread is inside the drag loop. Single files were never affected,
  because a small selection is prepared in the background and handed to the OS
  as a real file — which is why this only ever showed up on folders.
- **The suite could not have caught it.** Its simulated drop happened after the
  drag call returned, and its test hook skipped `startDrag` entirely rather than
  blocking like the real one. Both are fixed: the drop is now performed *while*
  the drag is in flight, and the hook blocks for the same reason the OS does.

### Added

- **The desktop app keeps a log** at `<userData>/logs/filex-desktop.log`
  (rotated at 2 MB, one previous file kept). A packaged app has no console, so
  `console.log` went nowhere: when a drag-out failed, the only evidence was an
  empty folder. Every step of a drag and of a transfer is one line, and an
  uncaught exception in the main process lands there too.

## [0.27.2] - 2026-08-29

### Fixed

- **A file whose name is not ASCII no longer breaks a download — or the client
  reading it.** `Content-Disposition` carried the filename raw, so a name like
  `Türkçe adlı dosya.txt` put bytes over 127 in an HTTP header. Browsers guess
  their way through that, which is why it went unnoticed for years; a strict
  client does not. Electron's `net.fetch` threw
  `Cannot convert argument to a ByteString … value of 305` from inside its
  response handler — where no caller's try/catch can reach it — so the filex
  desktop app took an uncaught exception and a folder being dragged out stopped
  filling in halfway, silently. Every download now sends RFC 6266:
  `filename="ascii-fallback"` plus `filename*=UTF-8''percent-encoded`, from one
  shared helper used by the manager, share, share-browse and viewer endpoints.
- **The desktop app survives a badly-formed header from any server.** Its
  transfers moved from `net.fetch` (which validates response headers as
  ByteStrings) to `net.request` (which does not), so an older filex — or
  somebody else's server — can no longer stop a drag-out by naming a file in
  Turkish.
- **A failed drag-out no longer freezes the app.** The failure was reported with
  `dialog.showErrorBox`, which is modal: the box sat in front of a frozen window
  until it was clicked. It is a toast in the explorer now, plus an OS
  notification when the window is not in front. An uncaught exception in the
  main process is likewise logged and notified instead of ending in Electron's
  raw JavaScript error box.
- **Drag-outs leave a trail.** The transfer runs after the gesture is over, with
  no window of its own; when it went wrong the only symptom was a folder that
  stayed empty. Each step now prints one line (`[drag …]` / `[xfer …]`).

## [0.27.1] - 2026-08-29

### Fixed

- **A drag-out could fill in the wrong folder.** The drop watcher matched the
  stand-in by NAME across the local drives, so any file that happened to appear
  under the same name while a drag was in flight — a backup job, another
  download — looked like the drop and the user's file was written into that
  folder instead. What was handed to the shell is known exactly (an EMPTY
  stand-in, created inside this drag's window), so anything with content of its
  own is now ignored. Measured: with the guard off, a same-named decoy file
  received the transfer; with it on, the decoy is untouched and the real drop
  still lands.

## [0.27.0] - 2026-08-29

### Added

- **Copy in one storage, paste into another.** Copy/cut in one depo and paste in
  the next now does what it says: the queue carries a destination storage of its
  own and the worker streams the bytes between the two drivers — a whole tree,
  empty folders included, with each file's own mtime and a size check on the far
  side before anything is deleted. A cut across storages is a real move (the
  original is removed once the copy is verified); a **drag** across storages
  copies, the rule Explorer and Finder taught everyone. Pasting into a read-only
  depo is refused with a reason instead of failing later in the worker.
- **Drag files out of the app onto your desktop — at any size.** In the desktop
  app a selection can be dragged out to Explorer/Finder or into another program,
  and folders and multi-selections land as **separate real files and folders**,
  not one archive. The OS copies a dragged file from a path at the moment of the
  drop, so filex answers that in two ways and picks for you: anything it already
  has (kept on this computer, or a small selection prepared in the background the
  moment it was selected) is handed over as a real file, and everything else
  drags as an empty **stand-in** — the shell copies it in microseconds, filex
  finds the folder it landed in and downloads the real content there. A 100 GB
  file therefore starts dragging as fast as a 1 KB one, with no ceiling and no
  waiting ([docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)).
  ⚠ A drop onto an *application* rather than a folder writes nothing to disk, so
  the stand-in route cannot fill it in; the app says so instead of pretending it
  worked, and the prepared-copy route (which covers that case) is what small
  selections use. Downloads land on `.filexpart` and are renamed only when
  complete. In a **browser**, a single file can be dragged out the same way
  (Chromium's `DownloadURL`).
- **The agent surface moves between storages too.** `file_move` (MCP) and
  `POST /api/ai/move` no longer answer "cross-storage move not supported": they
  run the same transfer engine as the queue, verify every file on the far side
  and only then remove the source ([docs/MCP.md](docs/MCP.md#moving-files-between-storages)).

### Fixed

- **A cross-storage paste no longer lands in the wrong storage.** `POST
  /api/files/copy|move` resolved the storage from the SOURCES only and applied
  the target's relative path to it, so `alpha://a.txt` → `beta://hedef`
  answered `202` and wrote the file to `alpha://hedef/a.txt` — a folder invented
  inside the depo the user was copying *from*. The endpoint now resolves the
  target's storage, checks editor permission **there**, and refuses an unknown
  or read-only destination up front (measured 2026-08-29).
- **A copy pasted at a storage root is listed again.** The DB mirror looked its
  parent up as `path.Dir("file.txt")` — `"."`, a directory that is never in the
  index — so a copy made at the top of a depo was written to disk and never
  appeared in the listing until the next scan.

## [0.26.1] - 2026-08-27

### Fixed

- **Ticket refusals now say what to DO, not just what went wrong.** The redeem
  endpoint answered bare codes — `{"error":"ticket_expired"}`,
  `{"error":"file_too_large"}` — and the right reaction to each is different:
  an expired ticket needs a new one, an **oversize upload must be retried
  against the same still-valid ticket**, and a chunked body just needs
  `curl -T`. A caller reading a bare code either gives up or mints tickets it
  did not need. Every refusal now carries a `hint` naming the next step (413
  also echoes `sent_bytes`), including the storage-side ones (`503`/`507`).
- **A folder as the ticket destination is refused in the caller's own terms.**
  It used to surface the driver's `storage: path exists with a different kind`,
  which does not tell anyone what to send instead. The refusal now reads
  `"…" already exists as a FOLDER. …` and shows the shape of a correct path
  (`…/<filename>`).
- **The mint reply no longer assumes curl.** Alongside `curl` it returns a
  `powershell` line (`Invoke-WebRequest -Method Put -InFile`) and a `next`
  line saying to run it **on the machine holding the file** — and to hand the
  line to the user when the caller has no shell at all, which is the situation
  of an MCP-only client. Both new fields are exposed through the
  `file_upload_ticket` MCP tool.

## [0.26.0] - 2026-08-27

### Added

- **Upload tickets: an agent can finally upload a large local file.** Every
  agent-facing write surface carried its bytes inside the call — `/api/ai/upload`'s
  JSON body, the MCP `file_write` tool — and on an MCP transport that means through
  the model's context. A 130 MB file is ~173 MB of base64 there: not slow,
  impossible. The agent was left telling its user "I can't upload this" (one
  spreadsheet cost an hour on 2026-08-27).

  `POST /api/ai/upload/ticket` (scope `write`) now splits the job in two. The
  authorized half resolves and **pins** the destination under the caller's own
  token — confinement root, tenant scope and editor grant all checked there, and a
  folder as the destination is refused before a byte moves. The transfer half is a
  plain `PUT`/`POST` to **`/u/{ticket}`** that needs **no credentials at all**, so
  an agent with no filex token (a Coder workspace, a fleet agent) can finish the
  upload with the ready-made `curl -T <file> <url>` line the mint call returns.

  The URL is not a key to filex, it is a key to **one write**: single-use, minutes
  long by default (30 min, max 24 h), no read/list/delete, and the redeemer cannot
  name a path. The bytes are written as — and billed to — the minter, and land on
  the same staging/thumbnail/write-hook path as any other upload. Refusals are
  distinct and non-guessy: `404` unknown *or already redeemed* (the same answer on
  purpose), `410` expired, `409` in flight, `411` no `Content-Length`, `413` over
  the ticket ceiling (the ticket survives, so a corrected upload still works), and
  `503 storage_unavailable` / `507 quota_exceeded` when the backend is the problem.

  Also exposed as the MCP tool **`file_upload_ticket`**, and `file_write`'s
  description now sends anything already on disk there instead of to base64.

## [0.25.3] - 2026-08-25

### Fixed

- **A PIN-protected file-request page rendered blank.** The gate on a
  `/d/{token}` drop link came out with an empty `<title>`, an empty heading, an
  unlabelled PIN box and a blank submit button — nothing on the card told the
  visitor what it wanted. `Drop` reused the shared PIN template but passed no
  string table and no language: the template asks for `{{.T.pin_heading}}`,
  `html/template` renders a missing key as the empty string, and the `Execute`
  error was assigned to `_`. Every drop page (gate, uploader, error pages) now
  goes through the same one-language-per-visitor resolver the share pages use,
  so `?lang=` / `Accept-Language` / `default_locale` decide it — and the
  uploader script's own messages travel with the page instead of being
  hard-coded Turkish for every visitor on Earth.
- **A drop into unreachable storage answered `500` and logged nothing.** When
  the object store behind the folder refuses the write, the upload now answers
  **`503` `{"error":"storage_unavailable"}`** with a message that says the
  storage is down and the link is still good — instead of a generic "could not
  send, try again" that sends people retrying into a wall — and the failure is
  logged at `ERROR` with the storage name, driver, stage and destination, so it
  reaches the error tracker. Out of space is now its own answer: **`507`
  `{"error":"quota_exceeded"}`**. Found the hard way when Hetzner's object
  storage returned `503` for eleven hours and every drop hung for over a minute
  before failing silently.

## [0.25.2] - 2026-08-23

### Fixed

- **Error-tracker events carry the log line's context again.** The slog →
  Sentry/GlitchTip bridge forwarded the message and nothing a person could
  act on: eleven `thumb generate failed` events with no file and no error in
  the issue list or the API, `driver init attempt failed` without the driver.
  Every attribute of the record now travels with the event — as **tags**
  when short (`path`, `err`'s first line, `driver`, `attempt`, `node`, …,
  plus `source` = `file.go:line`), and in full under the **`log` context**
  (a multi-page ffmpeg transcript in `err` is kept whole there). Keys that
  name a credential (`token`, `password`, `secret`, `authorization`,
  `cookie`, `credential`, `private`, `*_key`) are replaced with `[filtered]`
  before either. Proven by a bridge test with a capture transport that fails
  on the previous code (`tags=map[]`, `client_secret` in clear).
- **A listener closed by a shutdown is no longer an error.** `ftps: listener
  stopped` (and its SFTP/NFS twins) logged at `ERROR` on every deploy and
  filed an issue each time. The line is `INFO` with `reason=shutdown` when
  the server is stopping or the listener returned cleanly; only a listener
  that dies while the server is meant to be running is `ERROR` with
  `reason=unexpected` and the error.
- **`driver init attempt failed` says which driver, which error, which
  attempt.** Intermediate attempts are `WARN` (`driver init attempt failed,
  will retry` with `driver`, `attempt`, `of`, `err`); only the last one is
  `ERROR` (`driver init failed after all attempts`). The OIDC caller's
  follow-up line (`oidc: SSO disabled until restart`) states the consequence
  without re-filing the error, so one failure is one issue.

## [0.25.1] - 2026-08-23

### Fixed

- **FTPS survives a DNS hiccup at start-up.** `FILEX_FTPS_PUBLIC_HOST` is
  resolved when the listener starts, and that single lookup was final: on
  the v0.25.0 rollout Docker's embedded DNS timed out four seconds into the
  container's life, the listener never started, and nothing said so except
  one ERROR line at boot — the host port stayed published, `/healthz` stayed
  green. The lookup is now retried for up to two minutes (every 5 s) before
  the listener gives up; a name that truly does not resolve still fails with
  the name in the message. Proven by a test that fails the lookup twice and
  expects the third to be used.

## [0.25.0] - 2026-08-23

### Added

- **Every new share link has a maximum life, and the admin sets it.** A new
  Protection setting, `share.max_ttl_days` (default **7 days**, `0` = no
  ceiling, seeded once from `FILEX_SHARE_MAX_TTL`), caps what a new link or
  file request may be given: a link created without an expiry gets `now +
  max`, a longer request is shortened, and the create response carries the
  stored `expires_at` plus `expiry_clamped: true` when the server changed the
  request. The share dialogs read the ceiling from `/api/capabilities`
  (`share_max_ttl_days`) and offer only choices the server will keep — a 7-day
  server shows *1 day / 7 days*, not *30 days* or *Never* — and print the real
  expiry under the fresh link. **Existing links are never modified**: the
  Protection page and `GET /api/admin/protection` (`shares_over_max_ttl`)
  report how many live links outlive the ceiling, the boot log prints the
  number, and revoking any of them stays a manual decision. One rule, one
  helper (`lib/shareTtl.ts`), every surface: web, desktop and the embeds
  behave the same.
- **The folder-ZIP warmer has a size ceiling; the download button does not.**
  `FILEX_SHAREZIP_WARM_MAX_BYTES` (default **2 GiB**, `0` = unlimited) stops
  the background warmer from pre-building folders bigger than that — the
  16.7 GB incident was three hours of object-storage reads for a link nobody
  opened. Such a folder is zipped the moment a visitor clicks, with the same
  progress page, and cached from then on; nothing is refused for being large.
  Logged once per folder, not once per pass.
- **No cached folder archive older than a week.** `FILEX_SHAREZIP_MAX_AGE`
  (default **7d**, `0` = keep for the share's life) sweeps an archive whatever
  its share's state; the next warm pass or download click rebuilds it. With
  the 7-day link life this bounds the cache to what is actually being shared
  this week.

### Fixed

- **FTPS re-reads its certificate when the files change.** The pair was
  loaded once at start-up, so a mounted, auto-renewing certificate (Caddy,
  certbot) would have been served expired from its first renewal on — with
  `/healthz` green and nothing in the log — which is why FTPS could only be
  run self-signed. Every handshake now checks the files' mtime and size and
  reloads on change; a renewal that lands half-written keeps the previous
  pair serving and warns once. Proven by a test that swaps the files under a
  running server and sees the new serial on the next connection.
- The standalone share dialog's close button was Turkish in every language.

### Changed

- **Release workflow split into `binaries` → `docker` + `desktop`.**
  goreleaser (which creates the GitHub Release) and the multi-arch Docker
  builds were one job, so the desktop installers waited ~25 minutes for
  arm64 QEMU emulation they never needed (v0.20.2: goreleaser done at
  minute 5, job done at minute 30). The installers now attach to the Release
  while the images are still building.
- **`ghcr.io/brf-tech/filex:slim` and `:slim-vX.Y.Z` are pushed.** The
  release notes and `docs/DOCKER.md` have advertised them since the first
  image, but the workflow only ever pushed `:vX.Y.Z`/`:latest` — every
  release page's `docker pull …:slim-vX` was a 404. Same image as `:latest`.

## [0.24.1] - 2026-08-22

### Fixed

- **An interrupted run — uploads included — continues from a checkpoint.**
  0.24.0 made an interrupted first run resume for DOWNLOADS (the copy carries
  the server's mtime, so twins adopt). Uploads could not: the server stamps
  its own mtime on what it receives, and with the history written only by
  the settle pass at the very end, a run killed at file 9,000 of 10,000
  started the next one with no history — every file this machine had pushed
  came back as a "(server copy)" conflict pair. The engine now writes the
  baseline every 50 settled transfers (or 15 seconds): download rows are
  recorded exactly, upload rows are resolved by listing each touched folder
  once, a cancelled run flushes on a short detached context, and the next
  run finishes only what was still pending. Measured in the test: 12
  downloads + 4 uploads, plug pulled — resume does exactly the 8 remaining
  uploads, zero conflicts.

## [0.24.0] - 2026-08-22

### Added

- **Transfers run in parallel.** The engine walked its plan one action at a
  time, so a tree of small files was priced at one full round-trip each —
  measured on a live deployment, 2 GB of ~400 KB files crawled at 0.24 MB/s
  with the network mostly idle. Uploads and downloads now run on a small
  worker pool (default 4, `--transfers` to tune, 1 restores the serial
  engine); directory creation stays first and deletes/conflicts stay serial
  in the planner's careful order.

### Fixed

- **An interrupted first run RESUMES instead of conflicting every finished
  file.** With no baseline, "present on both sides" always meant a conflict —
  so a restart mid-first-run turned every already-downloaded file into a
  "(remote copy)" duplicate; on the tree above that would have been ~7,800 of
  them. Downloads now stamp the server's own mtime on the local copy, and
  twins with no history that match by (size, mtime) are adopted silently.

- **Changing the mirror root keeps each pair's history.** Migration used to
  remove and re-add the pair, throwing the baseline away — and the first-run
  merge that followed conflicted every file the machine had ever uploaded.
  New `filex sync move <id> <path>` repoints a pair and keeps its baseline;
  the desktop root change uses it, and stops the account's watcher first so
  a mid-round rename can never read as a mass delete.

- **The remote walk lists eight folders at a time.** One round-trip per
  folder made the inventory the slow phase of a big sync: measured behind a
  CDN proxy (~0.35s per request), a 3,328-folder invoice tree took ~19
  minutes to list serially — twice per run, since the settle pass walks
  again. The walk is breadth-first by level now, eight listings in flight,
  with the snapshot merge kept single-threaded; the same tree lists in a
  couple of minutes.

- **A filename containing ".." is a filename, not a traversal.** Eleven API
  guards rejected any path CONTAINING the substring — so a real invoice
  named "… Tic. Sic. Gaz..pdf" could be stored but never previewed,
  downloaded or synced (400 "bad path" everywhere). Traversal needs a whole
  `../` segment, and that is what every guard now checks — the same rule
  `sync add` already applied.

- **No-history adoption tolerates coarse mtimes (±2s).** FAT stores mtimes
  in 2-second steps, and any tool that stamps times through float seconds
  can land a millisecond off — measured: a repair pass one millisecond short
  turned 1,667 identical files into conflict pairs. Change detection against
  a baseline stays exact; only the adopt rule for twins with no history is
  tolerant, rsync's modify-window logic.

- **A half-dead connection can no longer freeze a sync forever.** All the
  CLI's parallel streams ride one HTTP/2 connection; when a CDN proxy killed
  it silently mid-first-sync, every stream blocked — for good, since Go's
  http2 sends no health pings by default and the client had no transport
  limits at all. The client now pings an idle connection (ReadIdleTimeout
  30s), bounds dialing, TLS and response headers, and leaves bodies
  unbounded — a big transfer may take long, a hang may not.

- **A folder that could not be listed is not a folder that is gone.** The
  remote walk skipped ANY failed sub-listing as "vanished" — and it now runs
  eight listings wide through proxies the client expects to die under it.
  One 502 on a subtree would have read as "folder removed on the server"
  and binned the local copy of everything below it; in the settle pass it
  would have dropped the subtree from the baseline instead, and every
  uploaded file in it would have come back as a conflict pair next round.
  Only a 404 is skipped now; any other listing error fails the run, names
  the folder, and touches nothing.

- **A pair cannot be pointed at a path that is not there.** `sync move`
  accepted a non-existent path; the next run would have created it empty
  under a surviving baseline, and an empty mirror with history means "every
  file deleted here" — carried to the server. The path must exist and be
  the pair's kind: a folder for a folder pair, a file for a file pair.

- **A missing mirror with history refuses to run.** The engine created a
  missing sync folder and carried on, which turned an unplugged drive or a
  folder moved by hand into a mass delete on the server. A pair whose
  folder is gone while its baseline still remembers files now stops and
  says what to do — `sync move` if it moved, plug the drive back in, `sync
  remove` to stop syncing it. A pair that never synced a file still gets
  its folder created: there is nothing to lose.

- **Moving the filex folder to another drive keeps modification times.**
  The cross-device copy stamped every file "now", and change detection is
  (size, mtime) — so the very history `sync move` preserves would have read
  as every file edited here, and re-uploaded the whole tree. The copy now
  preserves timestamps. If a move fails halfway the pair follows whichever
  side holds the COMPLETE tree (a finished copy with a half-failed cleanup
  points at the new place; a failed copy discards its partial litter), and
  unpairs as the last resort — an unpaired folder syncs nothing and deletes
  nothing.

- **A replaced watcher's late exit no longer unhooks its successor.**
  Stopping an account's watcher for the root move and starting a new one
  could race: the old process's exit handler deleted the supervisor's entry
  for the NEW process, so the next reconcile started a second watcher for
  the same account — two engines over one baseline.

- **The ".." guard also reads Windows paths.** Segments are split on both
  separators, and a segment made only of dots and spaces is refused
  (Windows trims ".. " to "..").

## [0.23.0] - 2026-08-20

### Added

- **Availability at a glance.** Every row in the desktop explorer carries the
  glyph grammar every drive client already taught: ✓ kept on this computer,
  ◐ holding kept items somewhere below, ⟳ being synced right now, ☁
  online-only — so a root listing answers "is anything in here on my disk?"
  without drilling in. And while the engine works, a strip along the bottom
  of the window names the folder and shows live progress — counts and a
  percent bar — parsed by the shell from the same progress lines `--quiet`
  emits, so the CLI output stays the single source of truth.

- **Single FILES can be kept on this computer too.** The sync engine grew
  first-class file pairs (`filex sync add --file`): same planner, same rules,
  same 30-day local trash — the snapshots just carry one entry. The desktop's
  menu offers *Keep on this computer* on files as well; a kept file mirrors
  to `<root>/<storage>/<path>` beside everything else, syncs both ways, and
  *Open local folder* reveals it next to its neighbours instead of launching
  it.

- **Settings shows the mirror root, and can move it.** The root was chosen at
  the first keep and then lived nowhere the user could see. A card in
  Settings now names it, opens it, and changes it: kept mirrors migrate
  (rename + re-pair — the settling pass transfers nothing, and a file pair
  stays a file pair), hand-picked pairs outside the root stay put, and only
  effectively-empty leftovers are swept, never `rm -rf`.

### Fixed

- **Moving the filex folder to another drive no longer unpairs what it moves.**
  The migration removed each pair before relocating its mirror, and `rename`
  cannot cross devices — which is the usual reason to move the root at all — so
  a move to a second disk left every folder sitting where it was, no longer
  synced, with only a dialog to say so. Cross-device moves are copied across
  now, and any failure puts the pair back where its content actually is. A root
  inside the current one (or containing it) is refused outright rather than
  half-applied, and the sweep afterwards touches only the storage folders the
  mirrors emptied — never the root the user chose, and never a folder that was
  already there.

- **"Keep online only" no longer leaves an empty folder skeleton behind.**
  The mirror's intermediate directories (created at keep time) are swept
  after the local copy moves to the Trash — and a folder holding nothing but
  OS litter (`.DS_Store`, `Thumbs.db`, `desktop.ini`) counts as empty, since
  Finder plants `.DS_Store` in any folder the user merely looked at and the
  plain-rmdir sweep stopped dead on it. Anything with real content still
  stops the walk cold.

## [0.22.0] - 2026-08-20

### Security

- **A server cannot name a local folder outside the one you chose.** The path a
  kept folder mirrors under is built from the wire path in the server's own
  listing, so a hostile or compromised server answering with
  `docs://../../Documents` would have had the desktop app create — and then
  two-way sync — a folder outside the account's mirror root, uploading whatever
  it found there on the first pass. Climbing segments are dropped before a path
  is built, the keep is refused before anything is created, and `sync add`
  refuses such a remote outright, so the CLI and every other caller are covered
  rather than one screen.

### Fixed

- **Unkeeping says which folder it is about to bin.** "Keep online only" asked
  what should happen to the local copy without naming it, with *Move to Trash*
  pre-selected — right for a mirror the app made, one Enter away from binning a
  folder it did not for a pair made by hand in Settings. The dialog carries the
  path now, and anything outside the account's root defaults to leaving it.

- **"Open local folder" on a folder whose first sync has not reached it opens
  the nearest one that exists** instead of appearing to do nothing (opening a
  path that is not there yet fails silently).

- **A pair added while the watcher runs is picked up on the next round.**
  `filex sync run --watch` read pairs.json once at start, and the desktop app
  keeps one watcher per account alive for days — so a second folder paired
  later was silently never synced until the app restarted, while the panel
  looked exactly as if it were. The watcher now re-reads the pair list between
  rounds: adds join in, removes drop out. (Reported by the olivov deployment,
  which hit this whole cluster in one afternoon.)

- **Removing a pair before its first run finished no longer fails.** The
  baseline file only exists once a first run completes; unpairing earlier hit
  the missing file and handed the desktop a raw ENOENT error dialog — for a
  remove that had, in fact, already happened.

- **A failed unpair no longer leaves a ghost watcher.** The desktop only told
  the supervisor to reconcile after a successful remove, so the error above
  left a `filex sync run --watch` process syncing a pair that was already gone
  from pairs.json — measurably listing the server every 30 seconds, and
  unstoppable from the UI. Reconciliation now runs whether or not the remove
  threw.

- **The ad-hoc sealed macOS build tells the truth about updates.** Squirrel.Mac
  refuses to swap an app without a Developer ID signature, and electron-updater
  only finds that out after downloading — so "Check now" announced a version
  about to install itself, then fell to a permanent "could not check for
  updates". When the build's own signature says self-update cannot work, the
  app now skips the download entirely, reads the update feed directly, and
  offers the honest thing: the new version's number and a Download button.

### Added

- **Sync progress is visible while it happens — including under `--quiet`.**
  The engine reports phases now (inventory counts while the server tree is
  listed folder by folder, `transfer: 12/345`, settling), and the CLI prints
  them even in quiet mode, which is how the desktop app runs it. A large first
  sync used to spend its entire inventory phase — minutes, on a big tree —
  printing nothing at all, and looked broken enough to cancel; that cancel is
  exactly the path into the two unpair bugs above.

- **"Keep on this computer" — selective sync from the explorer's own menu.**
  Keeping a server folder local used to mean Settings → pair a folder → pick a
  directory, once per folder. Now the first keep asks for ONE root (default
  `~/filex/<server>`), and from then on right-clicking any folder — or a whole
  storage on the drives screen — offers *Keep on this computer*: it mirrors
  under `<root>/<storage>/<path…>` and syncs both ways, while everything else
  stays online-only in the window. Kept folders gain *Open local folder* and
  *Keep online only*; the latter asks, natively, whether the local copy goes
  to the OS Trash or stays. Keeping a parent absorbs already-kept subfolders
  into the one pair. The hooks ride `config.desktopSync` and exist only when
  the desktop shell mounts the explorer — the web admin and the embeds see
  none of it.

## [0.21.6] - 2026-08-19

### Changed

- **A demo instance no longer accepts any new storage backend.** 0.21.4 refused
  the `local` driver, which reaches the server's own filesystem. The remote
  drivers (`s3`, `sftp`, `webdav`, `smb`, …) are refused now too: "attach your
  own bucket" reads as harmless, but what it asks the SERVER to do is open a
  connection to an address a stranger chose — loopback, a private range, a cloud
  metadata endpoint. A demo ships with the storage it demonstrates, and every
  other surface works unchanged.

  A plugin driver is still allowed, because on a demo the plugin subsystem is
  off unless the operator deliberately turned it back on — at which point it is
  their own program.

- **A single failed sync run is no longer reported as an error.** A poll run
  reads the backend's listing; when an object store answers 503/504 under load
  and the retry budget is spent, the run gives up and the catalogue is refreshed
  on the next tick instead. Nothing is lost. Measured on one instance: fifteen
  such failures in six weeks, every one followed by a successful run — fifteen
  reports that meant "the internet had a hiccup".

  Failures are now noted at INFO and reported as a warning only once **three in
  a row** have failed (roughly 45 minutes of a storage genuinely not answering),
  with the streak in the message; recovery is logged too. What an error tracker
  holds should be things worth waking up for.


## [0.21.5] - 2026-08-19

### Fixed

- **A plugin no longer outlives filex on Windows.** Stop filex without letting
  it clean up — a crash, a hard kill, a service restart — and every plugin it
  launched kept running. Measured: two `memfs.exe` processes still alive, one of
  them an hour after the run that started it had gone.

  That is not merely untidy on this platform: a running plugin holds its own
  `.exe` open, so the next install or upgrade of it fails with a sharing
  violation, and the socket it still owns makes the next start look mysteriously
  broken. Plugins are now put in a **job object** with
  `KILL_ON_JOB_CLOSE`, so the kernel reaps them when filex's handles close,
  whether or not filex got to run a line of shutdown. Measured after: 1 plugin
  running, 0 surviving the same hard kill.

  Unix keeps its process group and deliberately does **not** use `Pdeathsig`:
  in Go it fires when the OS thread that forked exits, and the runtime retires
  idle threads, so a healthy plugin could be killed for no reason. An orphan is
  a nuisance; a plugin that dies at random is a bug report nobody can reproduce.


## [0.21.4] - 2026-08-19

### Security

- **A public demo no longer accepts a storage on the server's own filesystem.**
  The `local` driver means "a path on this host", and on a demo every
  admin-only door is a public door. Measured on this project's demo before the
  guard existed: storages rooted at `/data`, `/etc` and `/proc/1` were all
  accepted — the database, the configuration and the process environment of the
  machine. The check that was already there refused `/` and nothing else.

  Drivers that reach a backend the visitor brings (`s3`, `sftp`, `webdav`,
  `smb`, a plugin) are unaffected: a demo where nothing can be connected is not
  a demo.

### Fixed

- **A flaky test on the release path.** `TestStagedUpload_SuccessfulCommit…`
  sampled the node's state right after an asynchronous commit and expected to
  catch it in `staged`. On a loaded runner the worker got there first, so CI
  went red while the code was right — the worst way for a test to be wrong. The
  fake driver now blocks until the assertion has been made, which makes the
  window as long as the test needs instead of as long as the machine happens to
  allow.


## [0.21.3] - 2026-08-19

### Security

- **A demo instance no longer offers the plugin API.** `FILEX_DEMO_MODE`
  publishes an admin login — that is what a demo is for — and the plugin API is
  admin-only, so on a demo "admin-only" means anybody; installing a plugin makes
  filex execute an uploaded program on the host. Demo mode now turns the
  subsystem off unless `FILEX_PLUGINS_DISABLED=0` says otherwise in so many
  words.

  Found by measuring rather than reasoning: the project's own public demo was
  checked after the previous release, the published credentials logged in as
  `role=admin`, and `GET /api/admin/plugins` answered `200`. Nothing had been
  installed, and nothing was stopping it. If you run a filex demo with plugins
  from v0.21.0–0.21.2, set `FILEX_PLUGINS_DISABLED=1` now — upgrading also does
  it, but the switch works today.


## [0.21.2] - 2026-08-19

### Fixed

- **Signature enforcement could not be switched on.** `plugin.New` parsed and
  validated the trusted ed25519 keys into a local variable and never assigned
  them to the manager, so `requires_signature` stayed false and an unsigned
  plugin installed on an instance that had configured keys. Measured through a
  real server, not a unit test — every existing test set the field by hand,
  which is exactly why none of them noticed.
- **The trusted keys and the concurrency ceiling had no way in.** The rejection
  message named `FILEX_PLUGIN_TRUSTED_KEYS`, and nothing read it; `MaxInFlight`
  was likewise only reachable by an embedder. Both are configuration now
  (`FILEX_PLUGIN_TRUSTED_KEYS`, `FILEX_PLUGIN_MAX_INFLIGHT`).
- **A rejected plugin upload is a client error, not a server one.** Install
  failures were classified by searching the message for words like `sha256`, so
  a bad signature answered `500` while a missing one answered `400`. They are
  typed now (`plugin.RejectedError`) and both answer `400`.
- **The generated driver shapes had nothing checking them.** `gen/main.go`
  claimed a test asserted the committed file matched the generator; no such
  test existed. It does now — and it caught the generator emitting unformatted
  source on its first run.


**A plugin now has to prove what it claims, and it can be upgraded without
taking its storages down.**

- **Conformance — every declared capability is probed, and a plugin that fails
  its own claims is refused.** A plugin declares capabilities and filex acts on
  them: it registers a driver whose method set matches, and every surface then
  offers those operations. If the plugin declared `write` and its write is
  broken, the user meets an upload button that fails, a trash move that fails
  and a version snapshot that fails, and reads all three as **filex** being
  broken — the plugin is the faulty part, the product wears the fault. So the
  claims are measured in two places: at install and at every start, against a
  throwaway instance the plugin opens for the new `POST /v1/selftest`; and again
  whenever a storage on it is saved, against that storage's **real**
  configuration, in a scratch folder (`.filex-conformance-<random>`) that is
  removed afterwards. The second gate exists because the first cannot cover it —
  a self-test proves the code works, not that these credentials reach that
  bucket. Probes: `list`, `not_found`, `write`, `read` (bytes compared), `stat`
  (a size that lies breaks ranged serving, quota and sync three different ways),
  `list_after_write`, `range`, `set_mtime` (set then re-stat: a timestamp
  accepted and dropped makes every sync run copy everything again), `copy`,
  `move`, `mkdir`, `delete`, `delete_idempotent`, `presign`, `multipart`,
  `watch`. A failure names the probe, what was expected and what happened.
  `FILEX_PLUGIN_CONFORMANCE=enforce|warn|off`, `enforce` by default. A plugin
  with no self-test endpoint is still installed, but is marked **unverified**
  and probed when the first storage is saved on it. ⚠ What the probes cannot
  check is stated where it matters rather than hidden: `presign` is verified to
  return a URL that parses, **not** one a browser on another network can reach
  (filex may not share the client's network), and `watch` is verified to open a
  stream, not to deliver an event for every change.
- **`storage.Watcher` is finally consumed — and the previous release's
  documentation line about it is now wrong and has been corrected.** v0.21.1
  removed a promise that a change stream bought "change events without polling",
  because nothing subscribed to one. Now a storage in `fsnotify` mode resolves
  in order: inotify when the driver is local, **the driver's own stream** when
  it implements `Watcher` (today: a plugin), polling otherwise. Events are
  coalesced with the same 2-second debounce as the inotify loop and each batch
  triggers the same full run a poll would, so a missed or duplicated event costs
  a scan and never a wrong index — the stream is a hint about *when*, not a
  ledger of *what*. ⚠ A stream that **ends** (the plugin restarts, the
  connection drops) falls back to polling rather than leaving the storage frozen
  with a stale index.
- **Upgrade a plugin in place** — `POST /api/admin/plugins/{id}/upgrade`. The
  row, the name, the driver and every storage built on it survive: stop, swap
  the file, start, verify. Remove-then-install was the only route before, and it
  takes the registration with it — a storage whose driver has gone cannot open.
  **If the new binary does not come up, the previous one is restored and
  started**, and the call answers 400 with the plugin's current status attached,
  so a failed upgrade costs an error message rather than a plugin. ⚠ The plugin
  is stopped first on purpose: a running executable cannot be replaced on Linux
  (`ETXTBSY`).
- **Presigned URLs and multipart uploads over the plugin protocol**, with two
  new capabilities. `presign` lets a plugin hand out a URL the client uses
  directly — share downloads then redirect instead of streaming through filex.
  `multipart` is resumable upload in parts, used by the staged-upload commit
  path, which holds the bytes itself and therefore pushes each part through
  `PUT …/multipart/part` rather than handing out part URLs. New routes:
  `presign-upload`, `presign-download`, `multipart/init|part|complete|abort`,
  plus `POST /v1/selftest`. `multipart` without `write`+`delete` is refused at
  describe time: a resumable upload is still an upload. The Go SDK gains
  `Plugin[T].SelfTest` and the optional `Presigner` / `Multipart` interfaces.
- **ed25519 signature verification for plugin binaries.** With trusted keys
  configured (`plugin.Options.TrustedKeys`, hex or base64), install and upgrade
  both refuse an unsigned or badly signed binary, and the admin API reports
  `requires_signature` so a surface can ask for the signature before the
  rejection rather than after it. What is signed is the binary's lower-case hex
  **sha256**, so an operator can sign the digest they already publish. ⚠ The
  checksum an install already required only proves the file has not changed
  since it arrived — never who it came from; that is the gap this closes.
  ⚠⚠ There is **no environment variable for the keys yet**: the rejection
  message names `FILEX_PLUGIN_TRUSTED_KEYS`, but nothing reads it, so on a stock
  server signature enforcement is off.
- **A ceiling on what a plugin may cost filex.** A plugin is somebody else's
  program in filex's request path, and a backend that accepts connections and
  then says nothing is indistinguishable from one that is merely busy. Each
  plugin now gets 10 concurrent operations (`DefaultMaxInFlight`); a caller that
  cannot get a slot within 5 s is told the storage is saturated instead of
  joining a queue nobody drains. Metadata operations get a 60-second deadline;
  streaming reads and writes deliberately get none, because a 20 GB upload is
  legitimately slow. A plugin's stdout/stderr is rate-limited to 50 lines/s
  (burst 200) with the dropped count reported — a chatty debug build was
  otherwise filex filling the disk, whose first symptom is "the server ran out
  of space", not "the plugin is noisy". On Linux and macOS a plugin is started
  in its own **process group** and killed as one, so a helper it spawned cannot
  outlive it holding the socket. ⚠⚠ filex is **not** a sandbox and the code now
  says so plainly: memory and file-descriptor limits are not set (Go cannot
  apply an rlimit to a child between fork and exec, and capping the parent would
  cap filex), and Windows has neither process groups nor rlimits here.
- **Plugin metrics** — `filex_plugin_ops_total{plugin,op,outcome}`,
  `filex_plugin_op_duration_seconds`, `filex_plugin_in_flight`,
  `filex_plugin_restarts_total`, `filex_plugin_up`. `busy` is its own outcome
  because saturation is a sizing problem, not a fault to chase, and
  `restarts_total` is how a plugin that restarts in a loop becomes visible at
  all — filex retries the instance once, so single requests keep working while
  the process dies every few seconds. Conformance probes and the server-side
  multipart part push are deliberately outside both the ceiling and the
  counters.
- **The driver shapes are generated** (`internal/plugin/gen`, 20 combinations).
  filex reads capabilities by type-asserting optional interfaces at forty-odd
  call sites, so a plugin that cannot write must be handed over as a value with
  **no** `Write` method. With five optional axes that is twenty structs, and
  twenty hand-written structs is where somebody eventually embeds the wrong
  thing and a read-only plugin quietly becomes writable.
- **The Python example gained a self-test area and multipart**, and its
  `acceptance.sh` grew from 11 measured steps to 17 — conformance at install,
  a plugin deliberately edited to **lie** about its writes (accepted by the
  install call, then refused, driver never registered, storage impossible to
  create), multipart, upgrade, upgrade rollback, and the live load figures.

## [0.21.1] - 2026-08-19

- **Copy or move into a storage's root was refused — for every driver.**
  Pasting at the top of a storage sends `<storage>://` as the destination; the
  handler turned that into an empty destination (with a comment saying
  "storage root"), and the operations queue refuses an empty destination with
  `ops: dest required`. Two halves of the same feature disagreed about what
  empty meant, so the paste failed on local disks, S3 and everything else.
  Measured on a built-in local storage. The root is now `/`, which is also
  what the queue keys off to drop a file *into* a directory rather than rename
  it; the join uses `path.Join`, so the root case produces `f.txt` and not
  `/f.txt` — harmless on a disk, a real object with an empty first path
  segment on S3. Pasting into a root also skipped the permission check before
  (an empty destination was not checked); it is checked now.
- **The plugin docs promised a change stream nothing subscribes to.**
  `Watcher` was listed as buying "change events without polling". Nothing in
  filex consumes `storage.Watcher`, no built-in driver implements it, and the
  only event-driven sync mode works on the local driver alone. The protocol
  keeps the endpoint — it costs a plugin nothing and stays forward-compatible
  — but the docs, the SDK and the protocol comments now say plainly that a
  watch-driven sync mode does not exist yet.
- **A second example plugin, in Python, with no SDK**
  (`backend/examples/plugin-diskfs`): the same protocol implemented by hand,
  backed by a real directory, with every optional capability. Its
  `acceptance.sh` drives the whole subsystem through filex — including a 25 MB
  transfer, native ranged reads, trash and restore, a plugin killed with `-9`
  mid-life, the remote kind with a sealed token, and a read-only plugin whose
  driver has no write methods at all.

## [0.21.0] - 2026-08-19

**A storage driver can now live outside the binary.** Somebody who writes
their own storage system could not teach filex to speak it without forking
filex; now they write a program, install it from the admin panel, and their
driver appears in the ordinary storage picker.

- **Plugins** — a plugin is a separate process. filex launches it (or connects
  to one you run), asks what it can do over a small HTTP/JSON protocol, and
  registers it as `plugin:<driver>`. Any language can implement the protocol;
  a plugin crash cannot take filex down; a plugin ships on its own schedule.
  Its **config form comes from the plugin's own describe**, which is what lets
  a driver that did not exist when the frontend was built render in the admin
  UI with no frontend release.
- **What a plugin does not implement is either emulated or honestly absent.**
  Ranged reads, move and copy are emulated by the host; `set_mtime` is only
  offered when the plugin really stores it, because filex can tell "not
  supported" from "applied" but not "applied" from "pretended". A read-only
  plugin is handed to filex as a value with **no** write methods at all, so
  the UI does not offer an upload button that fails at the last moment.
- **Go SDK** (`backend/pkg/pluginsdk`) — implement three methods, call
  `Serve`. Capabilities are derived from the type, so a plugin cannot claim
  one it did not write or hide one it did. A complete example lives in
  `backend/examples/plugin-memfs`, and the test suite builds and runs it.
- **Admin → Plugins** — install by upload, by URL with a required SHA256, or
  as a remote service; enable, disable, restart, remove. The page says what
  will stop working before you remove a plugin that storages are using.
- Security posture, stated rather than implied: a plugin runs with filex's
  privileges and receives the credentials of every storage on it, so the UI
  says so before the install button; a remote plugin's token is sealed with
  `FILEX_SECRET_KEY` (registering one without that key is refused rather than
  stored in plaintext); a launched plugin must listen on loopback; and an
  installed binary's SHA256 is re-checked on **every** start, so a file that
  changed under filex is refused rather than run.
- `FILEX_PLUGINS_DISABLED=1` turns the subsystem off entirely. In
  multi-tenant mode the surface is supertenant-only.
- **Fixed while proving it on Windows:** a plugin uploaded without an
  extension was stored as `memfs` and never started — on Windows the
  extension is what makes a file executable, and Go reports that as
  `executable file not found in %PATH%`, which reads like the file is missing
  when it is sitting right there. Binaries now get `.exe` unless they already
  carry an executable extension.

Migration **00029** (`plugins`). Docs: [PLUGINS.md](docs/PLUGINS.md).

## [0.20.3] - 2026-08-19

Three desktop fixes that came out of a real macOS 26 (Apple Silicon)
deployment (Berk, PR #9), one Connections-page fix, and — as a consequence of
the first — **macOS packages** on the release for the first time.

- **Desktop pairing died in a browser that was already signed in.** The
  router's "signed-in users skip /login" redirect destroyed the query string
  before the login view could stash `desktop_state`/`desktop_challenge`, so
  the browser showed a file manager with no code and no error while the app
  waited forever. The stash now happens in the router guard, ahead of the
  first `await`; a failed hand-off keeps the overlay and says so instead of
  disappearing.
- **The embedded sync engine was x86_64 inside an arm64 app.** `fetch-cli.mjs`
  defaulted `GOARCH` to `amd64`; every launch on Apple Silicon raised macOS
  26's Rosetta deprecation alert. It now follows the host arch (an explicit
  `GOARCH` still wins; CI's x64 runners are unaffected).
- **macOS packages: `filex-desktop-arm64.dmg` + `.zip`**, built on a pinned
  `macos-14` runner. Unsigned, but *ad-hoc sealed* by an `afterPack` hook: a
  no-certificate electron-builder output is only linker-signed, and macOS 26
  treats that as tampering ("malware blocked and moved to Trash", no override);
  the deep ad-hoc re-seal turns it into the ordinary "unverified developer /
  Open Anyway" dialog. Auto-update on macOS stays inert until the app carries a
  Developer ID (Squirrel.Mac refuses to swap an unsigned app); the zip and
  `latest-mac.yml` ship anyway so the feed is right the day it does. The docs,
  the README and the web app's download banner now list macOS honestly —
  Apple Silicon only, unsigned, first-launch step included.
- **Connections page in dark mode: the panel painted its own page ground, and
  five theme tokens did not exist.** `.fe-conn` set `background: var(--fe-bg)`
  and drew a blue-black rectangle over the admin's zinc page (and a white one
  over the light page) that ended where the panel ended; the API-tokens box
  referenced `--fe-surface`, `--fe-muted`, `--fe-accent`, `--fe-surface-2` and
  `--fe-mono`, none declared, so it had no ground, un-muted muted text and a
  hardcoded-blue button next to a token-blue one. Fixed in the shared package
  (web admin and the desktop app render the same component), 17 phantom token
  uses corrected across core, and a test now refuses any `var(--fe-*)` that
  `variables.css` does not declare.
- docs site: the hourly release rebuild had failed silently since 08-11 (no
  `PATH` under cron); it now sets its own, reports failure, and refuses to
  publish an empty release list.

## [0.20.2] - 2026-08-17

Packaging only — the server is identical to 0.20.1. It exists because the
desktop CI job that 0.20.1 introduced had two faults, and both are the kind
that produce a package nobody ever receives:

- **The upload ran before the release existed.** The job built a 125 MB
  AppImage and an 87 MB `.deb` and attached neither, because `gh release
  upload` needs the GitHub Release that goreleaser creates in a different job.
- **The version came from `desktop/package.json`, not the tag.** That file is
  edited by hand and still said 0.20.0, so `latest.yml` — the auto-update feed
  — would have advertised 0.20.0. An app already on 0.20.0 would have fetched
  it, compared equal, and reported itself up to date for good.

## [0.20.1] - 2026-08-17

### Fixed

- **A host name in `FILEX_FTPS_PUBLIC_HOST` stopped the FTPS listener.** The
  setting is documented as "the address to advertise for passive connections",
  so a host name is the obvious value — and it made FTPS refuse to start with
  `invalid passive IP`, while `/healthz` answered 200 and every other endpoint
  came up. One protocol was simply absent, and the only trace was a single
  line in the startup log. A name is now resolved once at startup; a literal
  address still passes through, and empty still means "answer with the control
  connection's own address". The two remaining refusals name the setting and
  the fix, because the old message named neither.

### Changed

- **Desktop packages are built on every tag.** They were produced by hand,
  which is why they drifted: 0.20.0 shipped a server whose headline feature
  was the protocol endpoints while the installed desktop app was still 0.18.2,
  so the Connections and Tokens screens did not exist for anyone who had it.
  Windows and Linux build in CI and attach to the release. macOS is
  deliberately absent until there is a Developer ID certificate — an unsigned
  build is refused by Gatekeeper with a message that reads like a corrupt
  download.

## [0.20.0] - 2026-08-17

### Added

- **filex is now reachable as S3, SFTP, FTPS and NFSv3 — not only as HTTP.**

  The rule this follows: *whatever filex can connect to, it must be connectable
  as*. It could already use S3, SFTP, FTP and WebDAV as storages; now those
  clients can point at filex itself. `rclone`, `restic`, `aws s3`, `mc`, `s3fs`,
  OpenSSH, `sshfs`, WinSCP, FileZilla, `lftp`, `curl --ssl-reqd`, a scanner that
  only ever learned FTP and a media player that only ever learned NFS all land
  in the same tree — with the same RBAC grants, the same trash, the same quota,
  the same search index and the same audit trail as the web UI, because every
  protocol writes through one funnel.

  Each has a credential you can revoke on its own (S3 access keys, SSH public
  keys, API tokens, NFS export paths), and every one of them resolves its caller
  through a single door, `internal/protocolauth`. ⚠ That door exists because of
  a real incident: a protocol that authenticates outside the HTTP middleware
  chain starts with **no tenant scope**, and "no scope" means "see everything" —
  which is how a tenant admin who mapped `/dav` once got all ten tenants
  read-write. It is now impossible to attach an identity and forget the scope,
  because they arrive together or not at all.

  ⚠ An account with **2FA enabled cannot use its password** on any of these.
  None of these protocols has a channel for a second factor, so accepting the
  password would make each of them a documented 2FA bypass; such an account
  mints a token, a key or an access key instead.

  See [docs/PROTOCOLS.md](docs/PROTOCOLS.md). The connection instructions are in
  the app — *Connections → Connect* builds every command from the live
  deployment, so what is on screen is what works.

  - **S3** (`FILEX_S3`, on by default) — SigV4 verified by hand and checked
    against the SDK's own signer; a bucket **is** a storage; ListObjectsV2 *and*
    V1, delimiters, real ranges, composite ETags, `x-amz-meta-mtime`, multipart
    on filex's staging area, the modern `x-amz-checksum-*` contract in header
    *and* trailer form, aws-chunked bodies, and directory markers so `mkdir`
    works over `s3fs`. Path-style and virtual-hosted addressing both work.
  - **SFTP** (`FILEX_SFTP`, off by default, `:2022`) — password (a token) or a
    registered public key, posix-rename, `statvfs` reporting your quota so `df`
    is right, permission bits synthesised from your ACL level. ⚠ Only the `sftp`
    subsystem is served; `exec` and `shell` are refused, because answering an
    exec request is how a file server grows a command-execution surface.
  - **FTPS** (`FILEX_FTPS`, off by default, `:2121`) — explicit TLS **mandatory**
    on control *and* data, passive-only, ASCII conversion **off** (it rewrites
    line endings, which on a file a client guessed wrong about is silent
    corruption), `REST`/`APPE` resume.
  - **NFSv3** (`FILEX_NFS`, off by default, `:2049`) — ⚠⚠ unencrypted, so LAN or
    VPN only. NFSv3 cannot authenticate a request in a way filex can use, so the
    identity is bound to the **export path**, which carries 32 bytes of entropy:
    the path *is* the credential, the mount is pinned to one account, and the
    uid/gid on each request is discarded rather than trusted.

- **`filex mount`** — a remote filex server attached to a folder on this
  machine, over the same HTTPS the browser uses. The only one of these that
  works from anywhere: NFS needs a LAN, an SFTP mount needs sshfs or WinFsp
  configured, this needs a URL and a token, and it reaches the server through
  whatever proxy sits in between because underneath it is the REST API.

  ⚠ **It is not a sync.** Nothing is copied but a bounded read cache, so it
  opens one file out of a hundred thousand without downloading the rest;
  `filex sync` is still the answer for having the files offline.

  ⚠ Linux at first; Windows landed in the same release — see the next entry.

- **`filex mount` on Windows** — a real drive letter (`filex mount Z:`), over the
  same HTTPS as everywhere else, with the same permissions, trash and quota.
  This is what replaces the SMB server: the thing people wanted from SMB was a
  drive on Windows without installing anything unusual.

  ⚠ It is still ONE binary. The objection that made this look impossible was
  CGO — filex ships `CGO_ENABLED=0` everywhere — and cgofuse has a CGO-free path
  on Windows that loads WinFsp's DLL at run time instead of linking it. WinFsp
  (free, [winfsp.dev](https://winfsp.dev)) is installed once by the user; filex
  neither ships nor fetches it.

  ⛔ **macOS is not supported and the command refuses there**, rather than
  appearing to work and doing nothing: macFUSE's Go binding needs a C toolchain
  filex deliberately does not use, and its licence forbids a commercial program
  from installing it. Use folder sync or the desktop app.

- **API tokens can be minted from the connections surface** — in the admin
  panel, the web explorer and the desktop app, from the same component.

  ⚠ Three of the protocols take an API token as their password (FTPS, WebDAV,
  `filex mount`) and their guides say so. Until now the only screen that could
  make one was the admin panel's, so a normal user read the instruction and had
  nowhere to follow it. The route itself was never restricted — only the UI was
  missing, and being an admin hid that completely.

- **SMB / CIFS storage driver** — a NAS, a Windows file server or a Samba box as
  a filex storage. ⚠ The library choice was a licence decision: the maintained
  fork pulls an **LGPL-3.0** dependency into filex's statically linked binary,
  which would stop being satisfiable the day filex ships closed-source, so the
  **BSD-2** upstream was used instead.

- **Every account has a username**, and every surface accepts the e-mail **or**
  the username. An `@` in an SSH or FTP login has to be quoted in most clients'
  config files, which is what this is for.


- **Prometheus metrics at `GET /metrics`**, behind the same admin gate as every
  other operator endpoint (filex is routinely on the public internet, and the
  exposition names storages, counts accounts and shows traffic shape). Staged
  uploads in flight, bytes staged, commits, failures, chunk retries and aborts;
  the staging sweeper's passes and removals; every guard refusal, labelled;
  per-storage transfer duration and bytes; quota usage; and the Go runtime
  metrics. Scrape config and the alerts worth having are in
  **[docs/METRICS.md](docs/METRICS.md)**.

- **`internal/throughput` — one rolling bytes/sec per storage.** Published as
  `filex_storage_throughput_bytes_per_second` and read by
  `internal/filecache` to decide whether a storage is slow enough to be worth
  caching. Deliberately one signal with two consumers: a cache that measured
  slowness its own way would disagree with the dashboard the operator is looking
  at. `Rate` distinguishes "unknown" from "zero", because treating silence as
  slowness would make every fresh boot behave like a NAS on a phone line.

  It measures the time spent inside the driver's `Read` calls, not wall clock
  across the transfer: a download is paced by whoever is downloading, so wall
  clock would let one person on a bad connection mark a fast bucket slow for
  everybody — and publish that as *storage* throughput on the dashboard.
  `internal/filecache` keeps the policy (a measured-fast storage overrules an
  operator's `slow: true` flag; nothing is decided on fewer than three reads
  big enough to mean anything) and asks `throughput.StatAbove` for the rate and
  the evidence behind it, rather than keeping samples of its own.

- **The staging sweeper logs every pass**, including the ones that remove
  nothing — a sweeper that only speaks when it deletes something is
  indistinguishable from a sweeper that has stopped running, and this project
  has already lost 29 GB to temp files nobody was watching. The in-flight and
  staged-bytes gauges are re-measured against the directory on each pass, so a
  restart cannot leave the dashboard lying.

### Fixed

- **On Windows, deleting a file you had just uploaded could fail with a 500.**
  About three times in two hundred, measured. Deleting moves the file into
  `.filex-trash/` with a rename, and on Windows a rename fails outright while
  any handle is open on the file.

  ⚠⚠ And the handle was often filex's own. Go opens files without
  `FILE_SHARE_DELETE`, so the thumbnailer, the content indexer or a download in
  flight blocked the rename of the very file they were reading — filex standing
  on its own foot. A real-time virus scanner does the same on a freshly written
  file and is outside anybody's control, so the holder cannot be removed, only
  waited out; every one of them lets go in milliseconds. The local driver now
  retries a rename or an unlink for up to a second while the filesystem says
  the file is held, and only for that class of error — "no such file" is an
  answer and still comes back immediately. Unix is unaffected: a rename there
  succeeds while the file is open.

- **Revoking a credential now reaches the session it already opened.** "Delete
  the token" did what the operator asked and not what they meant: the token
  could no longer be used to log in, and the SFTP session it had opened kept
  reading and writing files. Same for disabling an account, suspending a tenant,
  revoking an SSH key or taking away a grant — every one of them was true for
  the next login and false for the connection in flight.

  Every credential check happens once, at authentication. Over HTTP that is the
  same as "on every request"; for a protocol where one authentication is
  followed by hours of file operations — or, for an NFS mount, days — it is not.
  Live sessions are now registered and re-checked every 30 seconds, and the ones
  whose credential no longer resolves are cut: the connection is closed for SFTP
  and FTPS, and marked for NFS, which has no connection to close. Deleting or
  disabling a credential also kicks its sessions immediately. ⚠ The sweep is the
  guarantee, not the kick — it is what covers the paths nobody wired: an admin
  disabling an account, an expiry passing, a row edited in the database.

  ⚠⚠ The quieter half of the same bug: the **grant set** was cached for the life
  of the caller, so a permission removed at 09:00 kept serving files until the
  user logged out. It now expires on the same TTL as the password cache — one
  number, one answer to "when does it stop working".

- **A folder grant was unreachable over SFTP, FTPS and NFS.** A grant is
  per-folder, so a caller can hold viewer on `main/projects/acme` and nothing on
  `main`. All three asked for viewer on the folder being *listed*, which refuses
  the two levels above the grant — so `ls /main` answered "no such file" to a
  user who had been granted a subfolder of it, and the folder they were actually
  given could not be reached. Listing and stat now use the traversal rule the web
  UI, `/dav` and the S3 listing always used; reading a file's bytes still
  requires viewer on the file itself, so traversal never becomes access.

- **`/dav` enforced no quota at all.** Every other write surface did — manager,
  AI, ShareX, S3, SFTP, FTPS, NFS. A user at their ceiling could keep writing
  indefinitely by mapping a drive, and because the bytes are counted *after* the
  write, the number in the admin panel simply climbed past the limit. It is now
  refused with **507 Insufficient Storage** before the upload starts.

- **WebDAV locks survive a restart.** They lived in a map, so a deploy silently
  forgot every one of them: a client that took a lock before the restart
  presented a token that named nothing, its save failed with 412, and the server
  would meanwhile have let somebody else lock the same file. The lock said
  "exclusive" and stopped being true without telling anybody.

- **An upload to a folder with no database row wrote the file and created no
  rows at all.** Uploading to `main://newdir/a.txt` left the bytes on disk, the
  subfolder listing found them through the driver fallback, and the level above
  was **empty** — a folder you just uploaded into that did not exist until the
  next sync run. It hit the web explorer, the CLI and the AI upload path equally.


- **A folder share's ZIP no longer outlives the share.** The cache of
  "download all" archives had exactly one cleanup — dropping *older signatures
  of a folder that is still shared* — so when a share expired, was revoked or
  ran out of downloads, its archive simply stayed. Measured on a live instance:
  a 16.7 GB folder was shared for **eleven minutes**, the warmer read the whole
  folder from S3 for **three hours** after the link had already died, wrote a
  **15 GB** archive that was never downloaded once, and that file then went into
  the backup and was mirrored to the disaster-recovery host three times over. A
  disk cleaned from 96 % to 90 % was back at 99 % two days later.

  Three changes, in order of what they save:

  - **Every warmer pass sweeps.** Any `<node>-<sig>.zip` whose node has no
    active folder share is deleted, along with the temp files of builds that
    died with a restart (unclaimed, older than an hour). It reads the same
    share listing the warmer builds from, so there is one definition of
    "active"; it deletes nothing at all until that listing has succeeded once,
    keeps the archives of nodes that are still shared, keeps files a build is
    writing, and does not touch any name that is not one of its own.
  - **A build stops when its share does.** The build still runs detached from
    the request that started it — a downloader who hangs up must not kill an
    archive other people are waiting for — but it now asks, during the walk and
    during a single long file, whether the share still exists, and abandons the
    build and its partial file when it does not. It refuses nothing: a live
    share is built however large it is, and the on-demand path is untouched.
  - **The cache moved out of the data directory** to `<data_dir>/cache/sharezips`
    (existing archives are moved on first start). It is regenerable, and it was
    sitting where every backup, rsync and restore would pick it up. Backup
    guidance in [DEPLOYMENT.md](docs/DEPLOYMENT.md) and
    [SHARING.md](docs/SHARING.md).

- **Quota accounting was dead code, and nobody could have noticed.**
  `quota.AddUsage` and `Store.SetNodeOwner` had **no callers anywhere in the
  tree**: `users.usage_bytes` was never incremented, `GetNodeOwner` always
  returned `nil`, and so the release at trash-purge — the one place it was
  called — could never run either. Nothing was counted, so nothing was ever
  refused. Measured on a real instance: with a **2 MiB** quota, an **8 MiB**
  upload returned `200`, the bytes landed on disk, and `GET
  /api/files/quota/me` still read `used_bytes: 0`.

  The fix is one place, not nine: `internal/quotastore`, a `db.Store`
  decorator over `CreateNode`, `UpdateNodeMeta` and `HardDeleteNode`. Every
  write surface — browser upload, staged upload, staged ingest, WebDAV `PUT`,
  the public file drop, ShareX, the AI/REST API, save-text, archive extract,
  copy — reaches it through the store, so none of them carries quota code and a
  path added later is counted the day it is written. The rules for overwrite,
  move, trash, restore, copy and purge are written down in
  **[docs/QUOTAS.md](docs/QUOTAS.md)**.

  Two attribution holes turned up while wiring it, each a bug of its own:

  - **WebDAV never put the authenticated account on the request context.** It
    authenticates itself (HTTP Basic) and so never ran `auth.Middleware`,
    leaving `auth.UserFrom` nil for every `/dav` write — nodes owned by nobody,
    and file events with **no actor**.
  - **`save-text` created no node row for a new file.** The bytes went to the
    driver and the catalogue only learned about them on the next storage scan,
    so the file was invisible until then and the row it eventually got belonged
    to nobody.

  `RecomputeUserUsage` also filtered `deleted_at IS NULL`, which put the
  reconciler at odds with the rule it was reconciling: a recompute forgave every
  trashed byte, and the purge then released them a second time (clamped at zero,
  so the drift never surfaced as an error).

- **The per-user ceiling now holds on the synchronous upload path too.** Only
  the staged path checked it, so anything under the staging threshold had no
  ceiling at all — a user could pass their limit a few megabytes at a time.

- **`FILEX_SYNC_INTERVAL` does something.** It was parsed into config and read
  by nothing, while the real fallback was a hardcoded `15m` in the poll loop
  that happened to equal the documented default — which is exactly why the dead
  knob was invisible.

### Removed

- **`FILEX_SYNC_WORKERS`.** It was documented as "concurrent storage sync
  workers" and parsed into a field nothing read. There is no pool to size: the
  sync worker runs one goroutine per enabled storage and always has. Setting it
  now has no effect and produces no error. Documenting a knob that does nothing
  is worse than either wiring it up or deleting it.

## [0.19.0] - 2026-08-14

### Added

- **The desktop app has a language setting.** *Settings → Language*: System,
  English or Türkçe. It followed the OS and offered nothing to choose, so
  somebody on an English Windows could not have a Turkish filex — and the
  server's own panel has had a language switcher all along.

  The choice moves the whole app at once, which is the part worth saying: this
  window, the tray menu (built in the main process, which had its labels
  hard-coded in English), and the file explorer inside it — a separate component
  with its own catalogue. Switching is immediate and keeps the folder you are
  looking at; `system` still resolves against the OS at read time, so a laptop
  that changes its language keeps working.

  ⚠ The explorer is updated through its `config` property, not the `locale`
  attribute: the component merges `{...attributes, ...config}` and config wins,
  so setting the attribute changed what the element *reported* while the list on
  screen stayed English. The first version of the test asked the element for its
  locale and passed — a screenshot is what caught it. `scripts/lang-e2e.mjs`
  now reads the rendered text instead, and checks the shell, the stored setting,
  the main process's own resolution, and a restart.

## [0.18.2] - 2026-08-14

### Changed

- **The Windows app installs per-user, always.** The silent update in 0.18.1 was
  only half a promise on a machine where the app had been installed for all
  users: `C:\Program Files` needs administrator rights to write, so every
  background update ended in a UAC prompt — the app could not update itself
  while nobody was at the keyboard, which is the whole point. Discord, Slack and
  VS Code's user installer all land in `%LOCALAPPDATA%` for the same reason.

  The installer no longer offers the choice (`perMachine: false`,
  `allowElevation: false`): one click on "for all users" was enough to put the
  app somewhere its own updater could not reach. Settings and accounts live in
  `%APPDATA%\@brftech\filex-desktop`, outside the install directory, so moving
  an existing install is uninstall-then-install and nothing is lost.

## [0.18.1] - 2026-08-14

### Fixed

- **The desktop app's update handed you an installer window.** Downloading was
  already quiet, and quitting the app already installed silently — but the tray
  entry and the Settings button called `quitAndInstall()`, whose default is
  `isSilent = false`: it runs the NSIS installer with its full wizard. So the
  one visible path through the feature was the one that made a background
  updater feel like being sent back through setup.

  The update now applies itself and the app comes back where it was — in the
  tray. Every install is silent and relaunches (`quitAndInstall(true, true)`),
  and the relaunch is treated like a hidden launch (`--updated`, the flag the
  installer passes us) so no window appears in front of whatever you were doing.

  Because this app lives in the tray for days, it no longer waits for a quit
  that may never come: once an update is downloaded it watches for a quiet
  moment — the machine idle for ten minutes with no window open — and swaps
  itself then, stopping the sync watchers first so no transfer is interrupted.
  Nothing prompts, nothing nags; the tray line now says the update installs
  itself, and clicking it only means "sooner".

  `scripts/update-e2e.mjs` grew two guards against exactly this regression: no
  `quitAndInstall` may omit the silent flags, and the post-update relaunch must
  stay in the tray.

## [0.18.0] - 2026-08-14

### Fixed

- **A share link capped at N downloads handed out more than N.** The cap was
  checked against a counter bumped only *after* the bytes had left, so every
  request that started while an earlier one was still streaming read the same
  pre-download count and was let through. Measured against the shipped build: a
  link capped at **one** download served **three complete files** to three
  overlapping clients; "3 downloads" became four whenever the next click landed
  before the previous transfer finished — which, for anything larger than a text
  file, is most of the time.

  A download is now claimed against the cap in a single statement *before*
  anything is served — on the file itself, on a folder's "download all" ZIP and
  on a single file fetched from a shared folder's browse page alike. A serve
  that fails before a single byte leaves gives its slot back; a transfer the
  visitor abandons half-way has still spent one. The claim is written on a
  context detached from the request, so a client that hangs up cannot make the
  record of its own download disappear.

- **The share dialog's "Create link" button was pushed out of place** by the
  download-limit control added in 0.16.2. The options were one wrapping flex row
  with the button shoved to its right end by `margin-left: auto`; that survives
  two controls and breaks with three. The options are a two-column grid now and
  the action sits underneath at full width, which holds its shape whatever gets
  added next.

### Added

- **The one-line `curl` is back in the share dialog.** It belonged to the old
  standalone share dialog and was left behind when link creation moved into the
  "Share / Permissions" panel — exactly how the download limit had been lost. A
  share link is regularly minted *for a server* ("pull this onto the box"), and
  that reader has no browser. Both surfaces build the command from one helper
  now, so they cannot drift: folder links get `?zip=wait` and a `.zip` output
  name, PIN-protected links carry their PIN.

- **Profile pictures.** Set one in the admin panel → *My profile*, and the
  explorer's collaboration strip draws it instead of your initials — for every
  client of the account, including the desktop app and any API key minted under
  it, which is what makes it worth setting once. Stored on the user row as a
  small data URI (migration 00023), downscaled to 160px in the browser. A shared
  proxy token keeps initials rather than wearing its owner's face, and a host
  proxy that re-identifies an end user may supply that person's own picture with
  `X-Filex-Presence-Avatar`.

### Changed

- **The README screenshots are retaken, in English, against the current UI.**
  `share-modal.png` was showing a share dialog with no download limit — a
  control two releases old — and `viewer-markdown.png` had Turkish buttons in
  it. They are reproducible now: `node e2e/shots/capture.mjs` boots an instance,
  seeds a demo tree and captures the set, and reviewing them is a numbered step
  in the release process (`docs/CONTRIBUTING.md`).

## [0.17.1] - 2026-08-12

### Fixed

- **The desktop package could ship without its updater.** In a pnpm workspace
  `desktop/node_modules/<dep>` is a symlink into the repo root's store, outside
  the app directory, and electron-builder does not follow those — so
  `electron-updater` was absent from the asar. Adding `node_modules/**/*` to
  `files` did not help: the installer came out byte for byte identical. The main
  process is bundled with esbuild now (electron external), so the package
  carries our code and nothing else and packaging no longer depends on how the
  install happens to be linked.

  The symptom was the quiet kind — the installer built, the app launched, and no
  window ever appeared: the main process threw on the import before creating
  one, with no dialog and no log.

  `desktop/scripts/update-e2e.mjs` now points a packaged app at a local feed
  advertising a newer version and watches it download and stage the update for
  real. "The installer built" is not "the app works", and "latest.yml returns
  200" is not "the app updates".

  ⚠ 0.17.0's packages were built from this fix; its tag was not. Rebuilding the
  desktop package from the 0.17.0 tag reproduces the broken one — this release
  is the first whose tag matches what ships.

## [0.17.0] - 2026-08-12

### Added

- **The desktop app keeps itself up to date.** A file manager that syncs folders
  in the background is exactly the kind of program nobody thinks to go and
  re-download; the packages were being installed by hand, so every fix reached
  only whoever remembered to fetch it.

  The feed is a plain static directory on filex.sh — deliberately not the GitHub
  provider, whose private repository would require a token shipped inside the
  app. It downloads quietly and installs **on quit**: an update that interrupts
  a transfer to restart itself is worse than one that waits, and the app
  normally lives in the tray, so quitting is a real moment. The tray grows a
  "Restart to update" line when one is staged, and Settings gained an Updates
  row (state + "Check now").

  Failures are silent by design — no network, a proxy, a feed that 404s: none of
  that is the user's problem while the app works — but the state is recorded so
  Settings can report it. `FILEX_NO_UPDATE=1` turns the whole thing off.

  ⚠ This is the first version that can update itself; reaching it still needs
  one manual install.

## [0.16.3] - 2026-08-12

### Fixed

- **The tab strip appeared in the desktop app but not on the web.** 0.16.0 added
  `tabStrip` and defaulted it to `'auto'`, opting only the desktop app into
  `'always'` — so tabs were permanent in the app and came and went in the web
  explorer and the embeds. This package exists so those surfaces are one
  product; a default that differs between them turns it back into three. The
  default is `'always'` everywhere now, and `'auto'` remains as a deliberate
  opt-out for an embed too short to spend a row on.

- **A scrollbar appeared under the tabs.** 0.15.0's themed-scrollbar block sat
  at the END of the stylesheet, where it outranked the strip's own
  `scrollbar-width: none` (same specificity, later wins) and put a bar across a
  30px row. The generic block now sits at the TOP, before the component rules —
  a general default belongs before the exceptions that override it.

- **The strip grew a vertical scrollbar with nothing to scroll.**
  `overflow-x: auto` alone makes the other axis compute to `auto` as well, so
  the row of tabs had a vertical scroll context it could never use.
  `overflow-y: hidden` is explicit now.

- **Enough tabs ran off the edge with no way to reach them.** The strip did not
  overflow — it GREW: `<filex-explorer>` is almost always a flex item, a flex
  item's default `min-width: auto` floors it at its content's min-content width,
  and so a wide row of tabs pushed the host (and the page) sideways instead of
  handing the overflow to the scroller inside it. Measured before: 63 tabs → a
  3256px host inside a 1264px window, layout running off the right edge, no
  scrollbar anywhere. The host is now `min-width: 0; max-width: 100%` and the
  strip scrolls sideways with a thin bar. Every embedder would otherwise have
  had to know to write that themselves.

- **"New tab" scrolled away with the tabs it creates.** The `+` lived inside the
  scrolling area; past a dozen tabs it was off the right edge, reachable only by
  scrolling a strip most people do not know scrolls. It is pinned outside it.

## [0.16.2] - 2026-08-12

### Fixed

- **A share link changed language when you entered its PIN.** The public pages
  grew one at a time and each picked its own: the PIN gate was English, the
  folder page behind it Turkish, and the "PIN accepted" screen managed both at
  once — an English `<title>` over a Turkish heading.

  There is no session and no user on these pages — a share link is opened by
  strangers — so the language now comes from the request: an explicit `?lang=`,
  then `Accept-Language`, then the server's own `default_locale`, then English.
  It is resolved ONCE per request and handed to the template, so a page cannot
  mix two languages. Covers the PIN gate, the wrong-PIN line, the unlocked
  screen, the ZIP progress page, the error pages and the folder listing.

  ⚠ The counts line ("1 klasör · 2 dosya" / "1 folder · 2 files") is formatted
  by the handler: the two languages do not share a word order, so a template
  that glued numbers to nouns could only ever be right in one of them.

- **The download limit had no way to be set.** It lived in the standalone share
  dialog, and when link creation moved into the Share / Permissions panel the
  field was left behind — the server has honoured `max_downloads` the whole
  time, and nothing could give it a value. It is back, next to Expiry:
  Unlimited / 1 / 3 / 5 / 10 / 25.

  `desktop/scripts/share-limit-e2e.mjs` drives the real panel and then asks the
  SERVER what it stored, because a control that renders but never reaches the
  API would pass a DOM-only check.

## [0.16.1] - 2026-08-12

### Changed

- **A shared folder's gallery tiles are rendered when the link is created**, not
  when the first visitor arrives. 0.16.0 stopped the page shipping originals;
  this makes the *first* visit fast too, which is the visit that matters —
  whoever creates a link normally opens it straight away to check it, and that
  is exactly when the page used to crawl. Bounded at 500 tiles per share (a
  photo archive must not be re-rendered wholesale because somebody minted a
  link); anything past the cap still renders on first view.

## [0.16.0] - 2026-08-12

### Changed

- **A shared folder's gallery served the original photos as its tiles.** The
  public browse page marks image tiles with `?thumb=1`, and that endpoint read
  the file and streamed it — so a folder of 5 MB photos shipped tens of
  megabytes to paint one screen, and the page crawled until it settled. It now
  serves the same cached thumbnail the app's own gallery uses, keyed by node id
  and rendered once. A file that has never had one rendered gets it rendered on
  the spot rather than dispatched to a background job, because the visitor is
  looking at that tile right now.

  Nothing is lost when there is no thumbnail to serve: an unindexed storage, a
  format the pipeline skips, or a source above 64 MiB falls through to the
  original exactly as before. Thumbnail fetches still do not count as downloads.

- **A folder share's ZIP is built when the link is created.** It used to be
  built when somebody clicked download — or by the background warmer, whichever
  came first, which meant a wait of up to five minutes landed on whoever opened
  the link. The person who just created it is usually that person. Creating a
  folder share now starts the build immediately, asynchronously (the response
  does not wait on a multi-gigabyte archive) and with a 30-minute ceiling so a
  build stuck on a sick storage backend releases its slot.

  This is the same build the warmer was going to do anyway, moved earlier — no
  new class of work, and file shares are untouched.

## [0.15.1] - 2026-08-12

### Fixed

- **The account rail put the two identities the wrong way round.** 0.15.0 drew
  the server's Branding logo as a fixed badge at the top of the rail and left
  the accounts underneath as e-mail initials. That is backwards: each row of
  that rail IS a server — a tenant — so the logo its admin set is the truest
  label the row can carry, while the application's own mark is the thing that
  must not move. The rows now carry their own server's logo (initials remain
  the fallback for servers with no branding), and the filex mark sits fixed
  above them.

  Branding is fetched for every signed-in account rather than only the active
  one, so a rail of three tenants paints three logos without being clicked
  through. Branded rows keep the rounded square at all times instead of the
  circle the initials use: logo artwork is authored square, and a 50% radius
  eats its corners — which row is selected is already said by the bar on the
  rail's edge.

## [0.15.0] - 2026-08-12

### Added

- **Day / Night / Automatic in the theme gallery.** The palette picker could
  change *which* theme painted but never whether it painted light or dark —
  that was the embedder's call, passed in `config.theme`, with no way for the
  person looking at the screen to override it. A three-way switch now sits at
  the top of the gallery, which is where people already go looking for "night
  mode". The preference is separate from the theme id, persists in
  `localStorage` (`filex.thememode`), syncs across tabs, and defaults to
  `'host'` — meaning existing embeds look exactly as they did until someone
  actually chooses. `'auto'` follows the operating system.

  ⚠ Every place that read `config.theme` reads the resolved mode instead. A
  choice that reached only the token resolution would leave the root class, the
  modals and the teleported context menus painting the old mode — a half-dark
  window.

- **`tabStrip: 'auto' | 'always'`.** The tab strip rendered only once a *second*
  tab existed, which also hid the `+` button — so in a fresh window tabs were a
  feature you could only reach by guessing the keyboard shortcut. `'always'`
  keeps the strip on screen with a single tab; `'auto'` (the default) preserves
  the old behaviour for small embeds. The desktop app opts in.

- **Themed scrollbars.** The platform scrollbar ignored the theme completely: a
  wide light-grey slab down the edge of a dark panel. Thin, rounded, transparent
  track, colours taken from the existing `--fe-*` tokens so every theme —
  including ones added later — gets a matching bar for free.

  ⚠ Scoped to `.fe` and the two teleported surfaces, never to `*`: this
  stylesheet is loaded by embedders, and a bare `*` rule would repaint the host
  page's scrollbars from inside an embedded file browser.

### Fixed

- **The desktop app's presence entry was the token label.** A user collaborating
  from the desktop app appeared in their own folder as `filex desktop — Win32`
  instead of as themselves. Presence deliberately shows the token *username* for
  API tokens, because every end user behind a shared proxy token maps to one
  filex account and the account name would be misleading — but a token with no
  username allow-list is not a shared proxy. It is one person's own client, and
  that person is the account owner. Such tokens now read
  `Ada (filex desktop)`: the person leads, the client qualifies. Shared proxy
  tokens are untouched.

- **The desktop app's "start when I sign in" registered a bare `electron.exe`.**
  `setLoginItemSettings({ openAtLogin })` with nothing else registers
  `process.execPath`, which in a development run is
  `node_modules/.../electron/dist/electron.exe` — no project path, so every
  sign-in afterwards opened Electron's own welcome window, and the entry
  outlived the checkout it pointed at. The login item is now packaged-only (a
  dev run says so instead of offering a dead switch), the command is written out
  explicitly, and it carries `--hidden`, which the app honours by staying in the
  tray — making true the promise the settings copy was already making. On Linux,
  where Electron has no login-item API at all, an XDG autostart entry is written
  instead of nothing.

- **The server's brand mark never reached the desktop app.** An admin who sets a
  logo under Branding has said what their install is called; the desktop client
  showed only the vendor's icon. The active account's logo now sits at the top
  of the rail and on the waiting screen. `GET /api/branding` is public and is
  called without the token — it is what the login page reads before there is a
  session.

## [0.14.0] - 2026-08-10

### Fixed

- **Office documents opened with "Config fetch 401" in the desktop app, and
  starred files and recently-opened were silently empty.** All three had one
  cause. The explorer accepts a bearer token as a string *or a function*, and
  the function form is the one a desktop app needs — the credential is fetched
  from the main process per call rather than sitting in the renderer between
  requests. Resolving it is therefore asynchronous, and the header builder the
  explorer handed to the preview modal and every viewer was the *synchronous*
  one, which drops a function token entirely. The request went out with no
  `Authorization` header at all; nothing threw, and the only symptom was a
  feature that quietly did not work.

  The builder is async now and every caller awaits it. `authHeadersSync` stays
  for the one caller that genuinely cannot await (XMLHttpRequest's header
  loop), but it remembers the last token it saw instead of emitting nothing —
  a stale credential is a far better failure than an anonymous request.
  `web/tests/api/authHeaders.test.ts` fails the build if a call loses its
  `await` again.

- **"Open in new tab" did nothing at all.** The standalone editor route is
  root-relative (`/files/edit`), which the browser resolves against the *page* —
  correct when the explorer is embedded in the app that serves that route, and
  wrong for every cross-origin embed. In the desktop app the page origin is
  `app://filex`, so the button asked the OS to open `app://filex/files/edit?…`;
  no handler for that scheme exists, so the call returned without error and
  without doing anything. The route is now resolved against `apiBase` when the
  API lives on another origin.

- **Markdown previewed as a blank pane.** `markdown-it` was an external in the
  web-component build, and every consumer of that bundle loads it as a plain
  `<script type="module">` where a bare specifier cannot resolve — so the
  dynamic import always failed, in the desktop app, work.example.com and fishapp
  alike. It is bundled now (~100 KB, lazily chunked, loaded only when a `.md`
  is opened), and if a renderer is ever missing again the pane says so instead
  of rendering nothing.

### Added

- **The desktop app follows the OS language.** Its own chrome — the connect
  screen, settings, the folder picker — was English while the file list beside
  it was Turkish. Both now resolve from one locale (Turkish or English).

- **A real connecting screen.** Between "the window opened" and "the files are
  listed" the app showed a dashed grey box in the top-left corner of an empty
  white window. It is a centred surface now, naming the server it is waiting
  for, with an honest error state and a retry. The window also opens on its own
  background colour and only once it has something to show, instead of flashing
  white first.

- **Images, video, audio and downloads work in the desktop app.** Those
  elements carry no headers by construction, so the app attaches the account's
  bearer to requests bound for its own server's origin — scoped to the
  signed-in origins, never a wildcard, and never overwriting a header the page
  set itself. Downloads of the app's own API URLs go through that session
  rather than being handed to a browser that may not be signed in.

- **`desktop/scripts/files-e2e.mjs`** — the file surface driven end to end
  against a real server: opening documents and media, downloading, renaming,
  searching, starring, deleting, and one blanket rule — nothing the window asks
  its server for may come back 401.

## [0.13.3] - 2026-08-07

### Added

- **The share button now works in the desktop app.** The explorer has always
  had one, gated on `typeof navigator.share === 'function'` — and Electron
  ships no Web Share API, so in the desktop app it simply never appeared.

  Rather than add a second share UI beside the product's own, the app
  polyfills the standard API onto a native handler, and the existing button
  lights up. On macOS that is the real system share sheet; on Windows and Linux
  it is a native menu (copy link, copy the message, email, open in browser),
  because the OS share sheet needs WinRT, which Electron does not expose. Said
  plainly rather than dressed up as something it is not.

### Fixed

- **The sync engine was reported missing when the app ran from source.** The
  bundled binary lives at `desktop/build/bin`, and the lookup only checked the
  packaged location and one level too high. ⚠ The suites passed throughout
  because they *told* the app where its engine was via `FILEX_CLI` — they no
  longer do, so the resolution itself is now under test, unpackaged and
  packaged.
- **The "choose a folder" dialog opened behind the settings panel** that
  launched it — present in the DOM, invisible on screen.
- **The rail icons were off-centre.** `⚙` and `+` were text glyphs, laid out
  against the font baseline, so centring the line box left the shape low and
  left. They are icons now, and the suite measures the offset (0.00 px) rather
  than trusting an eye.


## [0.13.2] - 2026-08-07

### Fixed

- ⭐ **Sixteen components rendered as raw, unstyled HTML in every embedded
  surface.** The share/permissions dialog was the visible one: no box, no
  backdrop, browser-default inputs flowing down the page. Also affected: the
  convert dialog, the presence bar, star/tag/recently-opened controls, and nine
  file viewers.

  Vue's `<style scoped>` compiles to `.cls[data-v-HASH]`. The web-component
  build compiles the components from source but imports CSS produced by a
  *different* build, so the two hashes are unrelated and every scoped rule was
  dead. Measured in the packaged desktop app:

  ```
  DOM element : data-v-b9443460
  CSS rule    : .fx-perm-modal[data-v-cc21190e]
  matches     : false      → position static, no background, no radius
  ```

  Nothing errored, which is why it survived: the components worked perfectly
  and simply had no styling. **This affected every embedder** — the desktop
  app, and any host using `<filex-explorer>` or `@brftech/filex-core` — not
  just one surface. All 16 now use ordinary styles; the class names were
  already prefixed (`fx-`/`fe-`/`filex-`), so `scoped` was buying nothing.

- **Adding a second account did not appear until restart.** The account was
  stored, but the sign-in happens in a different window and the main window was
  never told to repaint.

### Changed

- **The desktop account rail was rebuilt.** It was a pale strip with a lone
  square and a dashed `+` — a wireframe, not a product. It is now a dark rail
  with per-account colours, an edge marker on the active account, and a
  background-sync indicator per avatar.

  ⚠ The colour hash was wrong twice on the way: `h * 31` used only 5 of 10
  colours across 200 accounts, and the replacement returned a *negative* index
  for 44% of inputs — an invisible avatar. Both measured, both fixed.


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
  `https://fm.example.com/admin/files/edit?path=s3-test%3A%2F%2Fexample%2Fcube.glb&type=glb`
  now shows zero console warnings and a properly rendered cube. The
  v0.1.1 inline-style code change is what made the fixture-fix possible
  — both layers were needed.

## [0.1.1] - 2026-05-09

Patch release closing six bugs surfaced by the post-v0.1.0 production
sweep against `https://fm.example.com` (see `sweep-2026-05-09/sweep-report.md`
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

- The live `s3-test` storage on `fm.example.com` was retrofitted with
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

- `files.example.com` → `demo-fm.example.com` rename across `deploy/`,
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
