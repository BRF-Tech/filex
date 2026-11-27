---
title: Releases
description: Every filex release with a plain-English summary of what changed - generated from the GitHub releases at release time.
---

<!-- GENERATED FILE - do not edit by hand. Your edits will be overwritten.
     Source:      GitHub Releases for BRF-Tech/filex
     Generator:   docs-site/scripts/fetch-releases.mjs
     Summaries:   docs-site/data/release-highlights.json (hand-written)
     Regenerate:  cd docs-site && npm run releases -->

# Releases

Every published filex release, newest first. This page is generated from the
[GitHub releases](https://github.com/BRF-Tech/filex/releases) by `npm run releases`, which is
run once when a release is cut - not by the site build, which would rewrite this
file on every contributor who ran it.

Whether filex installs a release by itself depends on which part of the version moved -
see [Updates](./UPDATES.md).

::: tip Latest - v0.61.3, 20 November 2026
Security patch for every install that uses file requests (GHSA-rcrp-r83c-g8hv, reported by @rockmelodies). A file request is a blind drop: the people it is sent to put files into its folder and are never shown what is already there. The download page took the same link, though, and served the folder as if it had been shared - it listed every file in it and handed the whole folder out as a ZIP to anyone holding the link, and a PIN on the request did not stop it, since its uploaders are given the PIN. Every release with file requests through 0.61.2 is affected. One rule now decides which links each public address takes: a file request answers only at its upload addresses, a download link only at the download ones, and the other kind gets the 404 of an unknown link. A file request's PIN also answers with the request's own state again, so its upload box keeps its limits and its folder's name. No migration and no setting: updating the server is the whole fix.
:::

```bash
docker pull ghcr.io/brf-tech/filex:slim-v0.61.3
docker pull ghcr.io/brf-tech/filex:full-v0.61.3
```

## v0.61.3

<span class="filex-release-date">20 November 2026</span>

Security patch for every install that uses file requests (GHSA-rcrp-r83c-g8hv, reported by @rockmelodies). A file request is a blind drop: the people it is sent to put files into its folder and are never shown what is already there. The download page took the same link, though, and served the folder as if it had been shared - it listed every file in it and handed the whole folder out as a ZIP to anyone holding the link, and a PIN on the request did not stop it, since its uploaders are given the PIN. Every release with file requests through 0.61.2 is affected. One rule now decides which links each public address takes: a file request answers only at its upload addresses, a download link only at the download ones, and the other kind gets the 404 of an unknown link. A file request's PIN also answers with the request's own state again, so its upload box keeps its limits and its folder's name. No migration and no setting: updating the server is the whole fix.

## What changed

### Added

- **A document window that freezes, crashes or cannot reach the server now
  says so in the desktop app's log** (task #366). The windows a document
  opens in - the viewer, the "Open with filex" editor and the app store -
  left no trace when the page stopped answering or a request never came
  back, so an editor that took no keystrokes for half an hour could not be
  explained afterwards. Each now writes `[docwin]` lines for the page
  hanging and answering again, its process or a frame inside it ending, a
  load that failed and its console errors (at most ten a minute, repeats
  counted), and `[docreq]` lines for its requests to the account's server
  that fail, answer with an error or wait longer than 15 seconds. No line
  carries a password, a token or a header: credential-looking values are
  written as `*`. [DESKTOP.md → Troubleshooting](./DESKTOP.md#troubleshooting).

### Changed

- **The nightly builds are made on GitHub, from a commit anyone can look
  at** (task #361). Every night the tip of the development branch goes to
  this repository's `nightly` branch as one commit on top of `main`, GitHub's
  full test matrix runs on it, and only a green run builds the nightly image
  (`ghcr.io/brf-tech/filex:nightly`), the desktop packages on
  [filex.sh/nightly](https://filex.sh/nightly/) and both snaps on the Snap
  Store's `edge` channel - all from that commit, which the version names
  (`0.62.0-nightly.<night>+<commit>`) and the image's labels link to its test
  run. [INSTALLATION.md → Nightly builds](./INSTALLATION.md#nightly-builds).

### Security

- **A file request's link no longer opens its folder at the download address**
  ([GHSA-rcrp-r83c-g8hv](https://github.com/BRF-Tech/filex/security/advisories/GHSA-rcrp-r83c-g8hv)).
  A file request (`/d/<token>`) is a blind drop: the people it is sent to put
  files into its folder and are never shown what is already there. The
  download page took the same token at `/s/<token>`, though, and served the
  folder as if it had been shared: it listed every file already in it and
  handed the whole folder out as a ZIP (`?zip=`), to anyone holding the link -
  an uploader it was sent to, or anybody it was forwarded to. A PIN on the
  request did not stop it, since its uploaders are given the PIN. Every
  release with file requests through 0.61.2 is affected. One rule now decides
  which links each public address takes: a file request answers only at `/d/`
  and `/api/public/d/`, and at the download page, a file under a shared
  folder, the embed metadata (`/api/files/share/<token>`) and `/api/public/s/`
  it answers `404` like an unknown token, before its PIN is looked at; a
  download link is refused the same way at a file request's addresses, its
  PIN included. A file request's PIN now answers with the request's own
  state, so its upload box keeps its limits and its folder's name after the
  PIN. [SHARING.md](./SHARING.md#file-requests-upload--file-drop) has it.
  Reported by @rockmelodies.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0613---2026-11-20)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.61.3) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.61.3`

## v0.61.2

<span class="filex-release-date">18 November 2026</span>

A patch for installs with an ONLYOFFICE Document Server, thanks to @trivnvdev-41 (PR #138). filex checks the Document Server's health before it offers the editor, and one slow answer used to hide the editor from everybody for two minutes: the check waited three seconds, once. It now waits up to five seconds, takes a second look after a quick failure such as a refused connection, and a failed check is trusted for 30 seconds instead of two minutes. A server that does not answer at all is not asked twice: it holds up the start of filex and the capabilities answer for one five-second wait, not two.

## What changed

### Changed

- **A server patch is published only once the whole test suite passed on
  it, and is offered to automatic updates at once** (task #361). Until now a
  patch that changed the server alone went out as soon as GitHub had built
  it, with the rest of the test suite still running behind it, and installs
  that update themselves took it only once that suite had passed, usually
  within the hour. Now every release, a server patch too, is tagged only
  after GitHub's full test matrix and the release's dry run passed on its
  commit, and the images it publishes are the ones that dry run built, pulled
  by digest and checked again before the tag: they report the release, serve
  the admin interface and the embeddable explorer, and refuse writes in demo
  mode. [UPDATES.md → What "automatic" actually checks](./UPDATES.md#what-automatic-actually-checks).

### Fixed

- **One slow answer from the Document Server no longer hides ONLYOFFICE for
  minutes** (by @trivnvdev-41 in
  [PR #138](https://github.com/BRF-Tech/filex/pull/138)). The health probe
  behind `/api/capabilities` and the admin page's **Test now** waited 3
  seconds once, and a failed verdict was kept for two minutes for every
  client, so one latency spike took the editor away from everybody. A probe
  now waits up to 5 seconds, asks once more after a quick failure (a refused
  connection or an error status) within those 5 seconds, and a failed verdict
  is kept for 30 seconds. A probe that got no answer is not asked again, so a
  silent service holds the capabilities answer and the server's start no
  longer than one timeout. The probe's detail says `(2 attempts)` when it
  asked twice.
  [BACKEND.md → Test an external service](./BACKEND.md#post-apiadminexternalnametest-).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0612---2026-11-18)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.61.2) · `ghcr.io/brf-tech/filex:slim-v0.61.2`

## v0.61.1

<span class="filex-release-date">16 November 2026</span>

A patch for SQLite installs that were upgraded across many releases at once. Before 0.38 filex made its operations queue at start, outside the database migrations, and from 0.27 on added a column to it the same way; the migration that took the table over in 0.38 assumed every install had that column. An install whose last start was 0.26.1 or older and that was taken straight to 0.38 or later - a v0.10.0 install upgraded to 0.61.0, for one - kept the table without it, and the operations list answered 500 while the server reported itself healthy. Such an install is now repaired at start: the column is added with its default, the queue's rows are kept, and the log says so in one line. PostgreSQL and MySQL installs were never affected, and the upgrade runs no database migration.

## What changed

### Fixed

- **An install upgraded from 0.26.1 or older lists its operations again**
  (task #356). Before 0.38 filex made its operations queue (`pending_ops`) at
  start, outside the migrations, and 0.27 to 0.37 added its `dest_storage_id`
  column the same way; migration 00036 took the table over assuming every
  install had that column. A SQLite install whose last start was 0.26.1 or
  older and that was upgraded straight to 0.38 or later kept the table
  without it, and every `GET /api/files/ops` answered 500 with
  `no such column: dest_storage_id` while `/healthz` was fine. **Upgrade note:
  v0.10.0-era SQLite installs upgraded to 0.38 or later (0.61.0 included) are
  fixed automatically on start**: the column is added with its default, the
  rows are kept, and the log says so in one INFO line (`db: schema repaired`).
  An install that added the column by hand is left as it is. PostgreSQL and
  MySQL were never affected. The repair is not a migration, so this is a
  patch. A test now takes the schema six releases left (0.1.57 to 0.61.0) to
  this one and compares it with a fresh install's, table, column and index.
  [DATABASES.md → Upgrading an old SQLite install](./DATABASES.md#upgrading-an-old-sqlite-install).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0611---2026-11-15)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.61.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.61.1`

## v0.61.0

<span class="filex-release-date">15 November 2026</span>

A file written into a storage's folder without filex now shows up without waiting for a scan. A new local storage is watched live: the folder somebody opens is read from the disk, catalogued and watched, so a file a download client, another container or the NAS's own share wrote there is listed when its folder is opened; on a network filesystem or a FUSE mount (NFS, SMB, Unraid's /mnt/user) nothing is watched and every opened folder is read from the disk. Existing storages keep their mode, and a local one that polls is offered Switch to live watching in one click; on screen the modes are now Live watching (lazy) and Watch the whole storage (fsnotify). When a storage's disk is on another machine - a NAS share, an SFTP server - filex-watch, a small program with a release line of its own (Linux, Windows and macOS, a service or the ghcr.io/brf-tech/filex-watch image), runs there and reports the folders that change, with a key made on the storage's page that opens nothing else, so the change reaches filex in seconds. The storage form says what a scan interval would cost, measured on the storage's last full scan - how busy it keeps the disk, a longer interval when that is too much, a warning near the memory limit - and a full scan records its peak memory. Extracting a selected 7z, RAR or encrypted ZIP member whose name holds a line break no longer does nothing and says nothing: it is named in the result as skipped, as when the whole archive is extracted. The upgrade runs one database migration.

## What changed

### Added

- **The storage form says what a scan interval would cost, measured on the
  storage's last scan** (task #353). Under **Scan every (minutes)** filex says
  how long the last full scan took, for how many files and folders, and the
  share of the time scans at the typed interval keep the storage busy; from a
  quarter of the time it suggests a rounder, longer interval - *every 5 min,
  about 80% of the time; suggested 50 min* - with a button that puts it in the
  field, and a scan that outlasts the interval is said to scan all the time.
  When the last scan took filex to 80% of the memory it was given (the
  container's limit, else the machine's RAM) it warns that the system would
  stop filex. The server judges it while you type
  (`GET /api/admin/storages/{id}/interval-advice`); any interval still saves.
  Saving a storage answers with the same judgement (`interval_advice`), so an
  agent using the `admin_storages_create` / `_update` MCP tools hears it too.
  A full scan records the most memory the process held while it walked
  (`peak_mem_bytes` on the run, migration 00139; **Peak memory** in the
  storage's run table).
  [STORAGE.md → How long a scan takes](./STORAGE.md#how-long-a-scan-takes-and-the-interval-filex-suggests).
- **Switch to live watching, in one click** (task #351). A `local` storage that
  polls - every one made before this release - is marked in the storage list
  and offered the switch on its page; the click saves the mode alone.
  [STORAGE.md → Which mode a new storage gets](./STORAGE.md#which-mode-a-new-storage-gets).
- **filex-watch: changes made on another machine reach filex in seconds**
  (task #352). A watch only sees what its own machine writes, so a storage whose
  disk is elsewhere - a NAS share mounted over the network, the folder an SFTP
  storage points at - learned of a change made there at its next sync.
  filex-watch is a small program for that machine (Linux, Windows and macOS on
  amd64 and arm64, a systemd unit, a launchd daemon, a Windows service, the
  image `ghcr.io/brf-tech/filex-watch`) that watches the folder and reports the
  folders that change to `POST /api/storages/{id}/watch/events`; filex looks at
  those folders alone - a Live watching storage reconciles each one, any other
  mode rescans its subtree - and a start, a lost connection, a full queue or
  a filex that started again since its last answer asks for one resync of the
  storage. The storage's page has a card for it (not for an object store such as S3, whose driver says there is nothing to watch):
  make a key for a machine (shown once), see what each key's agent last said,
  copy the install commands, revoke a key; the admin MCP tools
  `admin_storages_watch`, `admin_storages_watch_key_create` and
  `admin_storages_watch_key_delete` do the same. The key is an API key of its
  own kind: it opens that storage's change reports and nothing else - no file
  API, no protocol, no admin route - and reports as the administrator who made
  it, as they are now. filex-watch has a release line of its own
  (`filex-watch-v0.1.0`), never GitHub's "latest" release, and neither the
  update check nor the Releases page lists it.
  [docs/FILEX-WATCH.md](./FILEX-WATCH.md).

### Changed

- **A new local storage is watched live instead of scanned every 15 minutes**
  (task #351). A file copied into a storage's folder without filex - over the
  NAS's own share, by a download client, by another container - stayed
  invisible until the next scan, and a folder listed from the disk during the
  first scan looked empty again once it ended. A storage nobody chose a mode
  for now gets its driver's default, decided in one place and read by the
  environment seed every store package uses (CasaOS, Umbrel, Unraid, TrueNAS),
  the admin API, `filex storage add` and the storage form: live watching
  (`sync_mode: lazy`) for `local`, so the opened folder is read from the disk
  and watched, and `poll` for every other driver. The scan interval stays as
  the safety net. Existing storages keep their mode.
  [STORAGE.md → Which mode a new storage gets](./STORAGE.md#which-mode-a-new-storage-gets).
- **On a network filesystem nothing is watched** (task #351). A watch only hears
  what passes through this machine, so on NFS, SMB/CIFS, 9P or a FUSE mount
  (Unraid's `/mnt/user` included) a live-watched storage places none and reads
  every opened folder from the disk; the storage page says so and names
  [filex-watch](./FILEX-WATCH.md), which reports changes from the machine
  the disk is attached to.
  A storage set to **Watch the whole storage** (`fsnotify`) on such a
  filesystem is polled at its own interval instead, the way it already was on
  a driver that is not local: the server logs it once at start and the storage
  page says it is scanned at its interval.
  [LAZY-CATALOGUE.md → Network filesystems](./LAZY-CATALOGUE.md#network-filesystems-no-watch-every-open-from-disk).
- **The sync modes have new names on screen** (task #351): `lazy` is **Live
  watching** (it was *Lazy catalog*) and `fsnotify` is **Watch the whole
  storage** (it was *Live watch*, the name the new default now carries). The
  values in the API and the configuration do not change.

### Fixed

- **On MySQL a scan no longer ends before it began.** A sync run's start
  is kept to the microsecond and its end was written to the whole second, so
  a run that started and ended within one second was stored as ending first.
  The end is now as precise as the start.

- **Selecting an archive member whose name holds a line break no longer does
  nothing.** 7-Zip 25.01 and newer (every version filex accepts, the Docker
  image's included) list a line break in a member's name as `_`, so a 7z, RAR
  or encrypted ZIP showed `line_break.txt` for `line` + line break +
  `break.txt`, and extracting that selection wrote nothing and reported
  nothing. filex now finds the member the listed name stands for, and it is
  skipped with `name_not_allowed` and named in the result, as when the whole
  archive is extracted.
  [ARCHIVES.md → Safety model](./ARCHIVES.md#safety-model).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0610---2026-11-15)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.61.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.61.0`

## v0.60.2

<span class="filex-release-date">12 November 2026</span>

A patch for single sign-on set up in the environment. The OIDC provider filex builds from FILEX_OIDC_* asked the identity provider for openid, profile and email and nothing else, so an identity provider that sends a person's groups only for a scope of its own - VoidAuth, Authentik - never sent them, and the admin mapping, the allowed groups and the SSO-linked groups had nothing to read. FILEX_OIDC_EXTRA_SCOPES (or auth.oidc.extra_scopes in the config file), space- or comma-separated, adds scopes to the three; the Helm chart and the compose files take it too. It is read like the Scopes box of a provider added on the Identity providers page, and a scope named twice is now asked for once in both.

## What changed

### Fixed

- **The OIDC provider the environment defines can ask for more scopes**
  ([#137](https://github.com/BRF-Tech/filex/issues/137)). It asked for
  `openid profile email` and nothing else, so with an environment-only setup an
  identity provider that sends a person's groups only for a scope of its own
  (VoidAuth, Authentik) never sent them, and `FILEX_OIDC_ROLE_CLAIM` had
  nothing to read. `FILEX_OIDC_EXTRA_SCOPES` (or `auth.oidc.extra_scopes`),
  space- or comma-separated, adds scopes to the three; it is read like the
  Scopes box of a provider added on the Identity providers page, and a scope
  named twice is now asked for once in both.
  [SSO.md → Groups behind an extra scope](./SSO.md#groups-behind-an-extra-scope).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0602---2026-11-12)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.60.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.60.2`

## v0.60.1

<span class="filex-release-date">12 November 2026</span>

A patch: the pages of an app's public address are templates, and the per-address limits hold under a flood. What a short link's address answers when it leads nowhere, after too many tries or while it is unavailable - and a page the app answers with, such as a short link's + preview - is now a template an administrator rewrites under Settings → Templates, in the address's branding. The limits of file requests, the bot check, upload tickets, deletion links and share mail stay one step per request however many fresh addresses pour in, and count an IPv6 client per /64. In an app's setup wizard the Actions menu inside the dialog takes its clicks and a host of its own no longer gets the route's prefix, and an app waiting for its setup says so in the reader's language only. A ShareX template for any filex server sits beside the Uploaders page, the test kit for app authors reads a view's placement, and every action the release workflows run is pinned to a full commit.

## What changed

### Added

- **A ShareX template for any filex server.**
  [docs/uploaders/filex.sxcu](https://github.com/BRF-Tech/filex/blob/main/docs/uploaders/filex.sxcu) (also on the docs
  site, beside the Uploaders page) is the custom uploader file filex makes for
  an uploader, with `YOUR-FILEX-HOST` and `fxu_YOUR_KEY` in place of the
  address and the key: import it, fill in the two, and ShareX uploads to
  `/api/uploads`. A test holds it to the server's own file.
  [UPLOADERS.md → ShareX](./UPLOADERS.md#sharex).

### Changed

- **The pages of an app's public address are templates.** What a short
  link's address answers when it leads nowhere, after too many tries and when
  it is unavailable is `page.error` (`error.key` `route_miss`, `route_busy`,
  `route_unavailable`), and a page the app answers with - a short link's `+`
  preview - is the new `page.route`: an administrator edits them under
  *Settings → Templates* like the other public pages, and they wear the
  address's branding. They still load nothing from elsewhere (no logo, inline
  styles only, `default-src 'none'`), so they work on a host of the app's own,
  and each is drawn once per language, kind and tenant and served again for a
  minute, compressed for a browser that takes gzip.
  [TEMPLATES.md → An app's public address](./TEMPLATES.md#an-apps-public-address).

### Fixed

- **A flood of fresh addresses no longer slows down the per-address limits.**
  The limits of file request uploads, the bot check, upload tickets, deletion
  links and share mail swept every address they knew on each new one, so a
  flood from many addresses cost the server the square of its size. Each
  request is now one step, a limit holds at most 200,000 addresses at a time,
  and a count runs over the clock's current window and the one before it: at
  most the limit in any stretch one window long, so an hour's turn is no
  fresh start, and a client that reached the limit waits until the next
  window ends (two windows at most). An IPv6 address is counted per /64, as
  the short-link limiter counts it: keyed by the full address, one host with
  a /64 was as many fresh clients as it liked, past every limit and able to
  fill the 200,000.
- **For app authors: the test kit no longer calls an app's home list a dead
  end.** `plugintest.InspectSurfaceFor(manifest, viewID, surface)` and
  `CheckSurfaceFor` read a screen with its view's placement: a `home`, `page`
  or `inspector` screen with no buttons stays where it is drawn and is not
  warned about, while a `modal` step and the `setup` wizard still are, and a
  view the manifest does not declare is an error. `InspectSurface` and
  `CheckSurface` are unchanged.
  [PLUGIN-KIT.md → What the assertions catch](./PLUGIN-KIT.md#what-the-assertions-catch).
- **An app waiting for its setup says so in the reader's language only.** The
  Apps table's State cell read "Needs setup" followed by the server's English
  "waiting to be set up" in every language; the list no longer sends that
  reason as the app's error.
- **A row's Actions menu inside a dialog can be clicked.** In the setup
  wizard's Addresses step the menu opened under the dialog and the "Add
  address" box took its clicks. Menus, a table's column menu and lists opened
  inside an open dialog are now drawn in that dialog, and Escape closes the
  menu without closing the dialog.
- **A host of its own no longer gets the route's prefix.** Switching an
  address to "A host of its own" kept the default prefix of filex's own
  address, so `l.example.com` became `https://l.example.com/l`. Each kind of
  address now keeps its own prefix, and a host of its own starts with none.

### Security

- **The release runs its third-party actions at fixed commits.** Every action
  the GitHub workflows use is pinned to the full commit its version tag named,
  the tag beside it in a comment, so a moved or hijacked tag cannot change what
  a release or a test run executes. The jobs that read a publishing secret (the
  stores, winget, Homebrew, CircleCI) run in a `release` environment that only
  `main` and `v*` tags may deploy to, and every workflow says at the top what
  its token may do. Nothing was found tampered with; this is a second layer.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0601---2026-11-12)

Snap package: published with v0.60.2 (Snap Store outage).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.60.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.60.1`

## v0.60.0

<span class="filex-release-date">12 November 2026</span>

Uploaders, mail and page templates, and short links through an app. An uploader is a key for one program - ShareX, curl, a phone shortcut, a script - that drops files into one folder and answers each with a public or a private link and a deletion link: Account settings → Uploaders makes one and hands you ShareX's settings file with your address and the key already in it, and the old POST /api/sharex/upload keeps working, deprecated. Every mail filex sends and every page a link opens is now a Liquid template the administrator, or a tenant's administrator, rewrites under Settings → Templates, with a live preview in every language and a test mail; an edited template runs in a WebAssembly sandbox and carries no script, and mails go out as HTML with a plain-text part. A link's pages wear the branding of the tenant it was made in, at any address. Apps can now ask to be set up once in a wizard, keep data of their own beside filex's database (FILEX_APP_DATA_DSN when several replicas share it), answer public addresses of their own, and hold the link-shortener role, which puts Get short link in the share dialog, on file requests, in My shares and on an uploader's answers. The upgrade runs three database migrations.

## What changed

### Added

- **Short links, through an app.** An app can hold the link-shortener role:
  the share dialog, file requests and My shares offer **Get short link**, the
  short address is the one to copy, a mailed link carries it, and an
  uploader can answer each upload with it (**Short link**). The app
  keeps its links; filex answers each visit with a redirect, a page it draws,
  or one "not available" page for every miss, holds a guesser back (20 misses
  in 10 minutes, 100 a day) and keeps a flood off the rest of the server.
  [APP-PLUGINS.md → Short links](./APP-PLUGINS.md#short-links).
- **Setting an app up.** An app can ask to be set up once before it runs: it
  installs as *Needs setup*, and its wizard (its own questions, the addresses
  of its routes, who may use it) opens after the install. **Reconfigure**
  erases what the app keeps and opens the wizard again; removing an app that
  keeps data asks for its name typed back, then erases it.
  [APP-PLUGINS.md → Setting an app up](./APP-PLUGINS.md#setting-an-app-up).
- **For app authors:** public routes (`routes`, answered on warm instances),
  an app's own data (`data_*`, never in filex's database; `FILEX_APP_DATA_DSN`
  for a database shared by replicas), the `setup` block and placement, the
  `link_shortener` role and `pkg/pluginkit/shortcode`.
  [APP-PLUGINS-API.md → Public routes](./APP-PLUGINS-API.md#public-routes-060).
- **Uploaders: a key for one program that drops files into one folder and
  answers with a link.** **Account settings → Uploaders** (web app, admin panel,
  desktop app) makes one: a name, a storage and folder, a **public** link (a
  share link that opens in the browser) or a **private** one (the file in filex,
  behind sign-in and the reader's permission), and a link life that the form
  says the server's ceiling for (`share.max_ttl_days`). The key is shown once,
  with **Download ShareX settings (.sxcu)** - one file ShareX lists under
  images, text and files, built by the server with your address and the key -
  and a `curl` line; curl, PowerShell, phone shortcuts and Flameshot use the
  same request. **New key** replaces a lost one, **Delete** ends it
  ([UPLOADERS.md](./UPLOADERS.md)).
- **`POST /api/uploads`, the upload door.** Multipart `file`, the key as
  `Authorization: Bearer`; answers `{url, deletion_url, thumbnail_url}`, or
  `{error, code}` with `error` written for the person a tool shows it to. The
  key is not an API key: no other route looks it up, the request names no
  folder, and an upload is always a new name - a leaked tool config adds files
  to one folder and does nothing else. Each upload is checked as its owner as
  the account is now (`access.api`, `share.links` for a public link, the
  folder's storage by its id and tenant) and written to the audit log
  (`uploader.upload`). Managing uploaders over the API: `/api/auth/uploaders`.
- **A deletion link for every upload, `/x/{token}`.** Opening it never deletes,
  because link previews in chat apps fetch every URL: it shows a one-language
  page with the file's name and a **Delete** button; the button moves that one file
  to its owner's trash and revokes its public link. One use; it names the
  file, not its path; it outlives the uploader that made it
  (`uploader.file_delete` in the audit log).
- **Mail and page templates: every mail filex sends and every page a link
  opens is HTML and CSS you can rewrite.** **Admin → Settings → Templates**
  lists them: the nine mails and the frame they share, the public pages and
  theirs (the PIN gate, "this link is not available", the waits while a ZIP or
  a download is prepared, the pages for a browser without JavaScript, a
  deletion link's two pages), the public link
  page's frame, and short texts on a share's or a file request's page (a
  title, an introduction, a footer line). Each is written in
  [Liquid](https://shopify.github.io/liquid/) in filex's code editor (the
  file preview's Monaco: colours, line numbers, the line the engine refused
  marked), with its variables listed, a live preview in every language the
  server speaks and a test mail to your own address. The install's administrator edits the install's; on a
  multi-tenant install a tenant's administrator edits their own tenant's;
  **Back to the default** removes an edit. Colours, logo, name and footer
  still come from Branding, and every template wears them. An edited template
  runs in a WebAssembly sandbox with ceilings on time, memory and output,
  includes nothing but filex's own pieces, and is sanitized: no script, event
  handler or `javascript:` address reaches a mail or a page, and the public
  pages carry a nonce-based Content-Security-Policy. A template that fails
  when it is used is replaced by filex's own for that mail or page, and the
  editor shows the error ([TEMPLATES.md](./TEMPLATES.md); API
  `/api/admin/templates`).

### Changed

- **Apps answer with far less work per call.** filex's copy of the extism SDK
  (`backend/third_party/extism-go-sdk`, one patch, its `FILEX.md` says what
  and why) keeps the functions an app instance calls instead of building them
  again on every call: a warm call of an app's public route allocates 5.7 KB
  instead of 262 KB. Every app call (actions, screens, thumbnails, routes)
  goes through it.
- **Mails are HTML with a plain-text part.** Every mail - a share, a file
  request, access given, a new account, a notification and the digest, the
  SMTP test, the cloud sign-up code, an app's mail - is sent as
  `multipart/alternative`: its template's HTML, in the branding's colours and
  logo, and a plain-text part made from it (or written by the administrator).
  Until now every mail was plain text. The sentences are the language packs'
  as before.
- **A link's pages wear the templates and branding of the tenant the link was
  made in.** On a multi-tenant install a link's pages - the public link page
  (its title, icon, logo, name and colours, asked from the new
  `GET /api/public/{s,d}/{token}/branding`), and the server-drawn ones: its
  PIN gate, a folder or an upload page without JavaScript, the ZIP wait, a
  deletion link's pages - used the branding of the address they were opened
  at; they now use the link's tenant's, at any address. A page that belongs
  to no link, and every "this link is not available" page, still uses the
  address's, so it says nothing about whose link a token was. A mail uses the
  tenant of the link it carries, else the addressee's, else the sender's
  ([TEMPLATES.md](./TEMPLATES.md#who-edits-which-templates)).
- **Go dependencies.** Added `github.com/osteele/liquid` v1.9.2 (the template
  language) and `github.com/andybalholm/cascadia` v1.3.5 (CSS selectors, to
  move a mail's stylesheet into its elements). Moved with them:
  `github.com/spf13/cobra` 1.8.1 → 1.10.2, `github.com/spf13/pflag` 1.0.5 →
  1.0.10, `github.com/fatih/color` 1.17.0 → 1.18.0 and
  `github.com/mattn/go-colorable` 0.1.13 → 0.1.14 - the CLI's command line
  and colours. Its help and its errors read exactly as before; `filex completion` now prints cobra 1.10's scripts, which complete the same
  commands and flags (bash handles the active help itself, and the PowerShell
  script also works in Constrained Language Mode). Install the completion
  script again after updating if you saved it to a file.
- **For the maintainers: the release train's merge queue checks a merge
  before `main` moves to it.** `pnpm merge-queue` makes each merge in a
  worktree of its own (`.claude/worktrees/merge-queue`), builds there the
  workspace packages its `--check` commands import (`packages/core` and the
  ones it imports, from that merge's own source, or proves their dist is
  built from it), runs every check there, and fast-forwards `main` only to a
  green merge. A red check used to stay on `main` as a merge commit, and a
  web check read whatever `packages/core/dist` the checkout held
  ([CONTRIBUTING.md → The merge queue](./CONTRIBUTING.md#the-merge-queue-pnpm-merge-queue---queue-file)).

### Deprecated

- **`POST /api/sharex/upload` is deprecated and is removed in the next major
  release.** It keeps working exactly as before - an API key with `write`, the
  `folder` field, the `{"url": …}` answer - and now also answers a
  `Deprecation: true` header. Its file events and webhooks keep
  `origin: "sharex"` until it is removed, so a webhook consumer reading the old
  path sees no change; uploads through the new door carry `origin: "uploader"`.
  Moving: make an uploader, import its `.sxcu`, delete the API key you made
  for ShareX ([UPLOADERS.md](./UPLOADERS.md#moving-from-the-sharex-endpoint)).

### Removed

- **The hand-edited ShareX configs in `docs/sharex/`.** They needed the host
  and an API key typed into three files; an uploader's settings file is made
  for you with both in it. `docs/SHAREX.md` points to Uploaders.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0600---2026-11-11)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.60.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.60.0`

## v0.59.1

<span class="filex-release-date">9 November 2026</span>

Reviewing an app update no longer waits for the whole app to download. The permission review of an app from a repository or an address - an install, an update, a store link, a plugin request - downloaded the module, the interface and the office engine before it answered; for the Office editor that is 140 MB from GitHub, and Review update answered 504 from a reverse proxy or came minutes later. The review now reads the app's filex-app.json alone, for every version an app has published: its permissions, and the SHA-256 it pins for each file. Install and Upgrade then download the files and refuse any that does not match what was reviewed.

## What changed

### Fixed

- **"Review update" no longer waits for the whole app to download.** The
  permission review of an app from a repository or an address (an install, an
  update, a store link, a plugin request) downloaded the module, the interface
  bundle and the office engine before it answered. For the Office editor that
  is 140 MB from GitHub, and the review answered 504 from a reverse proxy or
  came minutes later. The review now reads the source's `filex-app.json` alone,
  for every version an app has published: the permissions, and the SHA-256 the
  manifest pins for each file. **Install** and **Upgrade** download the files
  and refuse any that does not match what was reviewed (`from_manifest` in the
  dry run's answer: [APP-PLUGINS-API.md](./APP-PLUGINS-API.md)).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0591---2026-11-09)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.59.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.59.1`

## v0.59.0

<span class="filex-release-date">6 November 2026</span>

The certificate check is now each connection's own: every S3, WebDAV and FTPS storage and every replication target has its own Skip certificate verification switch in its advanced settings, so a NAS with a self-signed certificate no longer takes the check away from the bucket beside it (GitHub #134, thanks to @kladiv). The 0.58.2 variable FILEX_STORAGE_TLS_SKIP_VERIFY writes the switch on to the storages and targets that exist at the first start and is not read again, so nothing stops connecting across the upgrade. The CLI (filex client login --insecure-skip-tls-verify, kept with that one address) and the desktop app (Advanced at sign-in, or the account in App settings) reach a filex server with a self-signed certificate too. The FTPS endpoint can permit plain FTP for a scanner that cannot do TLS, off by default, on Login security, and every setting that turns a protection off now carries the same warning under it. A saved password, key or client secret is no longer sent to a new address or over a weaker connection: a new SMTP host, identity provider address or storage endpoint, or a certificate check, host-key check or encryption turned off, needs it typed again. Security fixes, both reported by @mappygrid: switching an account off now stops its API tokens on every route, its open sockets and its links, and suspending a tenant stops what its people already hold and takes a person signed in, not an API key (GHSA-58wc-42xh-89v8); and what a tenant's people made on a storage - an upload ticket, a link, a queued job, an editor's save - stops when the platform operator moves that storage to another tenant (GHSA-fxm3-597v-wpgj). Upgrading is recommended.

## What changed

### Added

- **The certificate switch is each connection's own**
  ([GitHub #134](https://github.com/BRF-Tech/filex/issues/134), reported by
  @kladiv). Every S3, WebDAV
  and FTPS storage has **Skip certificate verification (insecure)** in its advanced
  settings (`insecure_skip_tls_verify`), and a replication target has its own: a
  self-signed NAS no longer takes the check away from the bucket beside it.
  `FILEX_STORAGE_TLS_SKIP_VERIFY` becomes a one-time seed: the first start writes
  the switch on to every such storage and target, says which in the log, and never
  reads the variable again, so nobody loses a storage across the upgrade.
  Turning the switch on for a saved connection needs its password or key typed
  again: a saved credential does not go to a server that is no longer checked.
  [STORAGE.md](./STORAGE.md#private-and-self-signed-certificates) has it.
- **The CLI and the desktop app reach a filex server with a self-signed
  certificate.** `filex client login --insecure-skip-tls-verify` saves the switch
  with the address, for `filex sync`, `filex mount` and the vault commands too; it
  goes to no other address ([CLI.md](./CLI.md#a-server-with-a-self-signed-certificate)).
  The desktop app has the switch under **Advanced** at sign-in and on every account
  in App settings, for the window and the sync engine
  ([DESKTOP.md](./DESKTOP.md#signing-in)).
- **A server may permit plain FTP.** **Allow plain FTP on the FTPS endpoint**
  (`ftps.allow_plain`, first value from `FILEX_FTPS_ALLOW_PLAIN`), off by default,
  for the scanner that cannot do TLS. A client that asks for TLS stays on it on both
  channels, and the connection guide says what the server does
  ([PROTOCOLS.md](./PROTOCOLS.md#ftps)).
- **A warning under every setting that turns a protection off.** One sentence (not
  safe, not recommended, apply it only if you know what you are doing) and what that
  setting exposes, on the storage and replication forms, the provider forms, Login
  security, SMTP and the tenant pages
  ([ADMIN-PANEL.md](./ADMIN-PANEL.md#settings-that-turn-a-protection-off)).

### Security

- **A saved password goes only to the address it was saved for, on every page
  that keeps one.** A new SMTP host, a new identity provider address (an LDAP
  `url`, an OIDC `issuer`) or a new issuer on a tenant's OIDC needs the saved
  password or client secret typed again; before, the SMTP test, a provider's save
  or **Test now**, and a sign-in sent the saved one to the new address (measured).
  The OIDC redirect address is not one of them: the browser comes back there with
  the sign-in code and no secret goes to it, so changing it asks for nothing.
- **A saved password does not go over a connection less protected than the one it
  was saved for.** SMTP encryption changed to None, LDAP StartTLS turned off on an
  `ldap://` address, a new LDAP CA (`ca_pem`, `ca_file`), the SFTP host-key check
  turned off or another pinned key or `known_hosts` file, and FTP without TLS need
  the saved password typed again, on the save and on every test (the SMTP test,
  **Test now**, **Test connection**), for a tenant's own providers too. Before, each
  sent the saved password over the weakened connection, the plain ones in the clear
  (measured; SMTP without encryption already sent a password only to this machine).
  Turning a protection back on, or editing another field, asks for nothing.
- **What a tenant's member made on a storage stops when the storage moves to
  another tenant**
  ([GHSA-fxm3-597v-wpgj](https://github.com/BRF-Tech/filex/security/advisories/GHSA-fxm3-597v-wpgj)).
  The credential-free `/u/` upload ticket resolved its storage by the name
  saved at the mint, with no tenant scope and a copy of the minting account:
  when the platform operator moved that storage to another tenant, or deleted
  it and created one of the same name for another tenant, while the ticket
  lived, its one write landed in the other tenant's file. A ticket now names
  the storage by its id, in the minter's tenant, and the upload asks again as
  the minter - account, the API token it was minted with, tenant, storage -
  and answers like a used ticket when any of them no longer stands. The same
  question is now asked by everything else that was made on a storage and
  used later: a share link, a file request and an app's page stop answering
  (`404`, as if they did not exist) while their storage is in another tenant,
  and answer again when it is back; a queued operation, an app job's result,
  an ONLYOFFICE save over the document, the editor's fetch URL, a signed
  thumbnail and an editing-together membership are refused for a storage the
  person's tenant no longer has; a resumable upload takes no chunk or commit
  for it; an app job's chosen output folder is held to its storage by id. A
  storage renamed in its own tenant keeps its tickets, which used to fail. A
  link made on a storage that was in no tenant, or in the platform's own,
  keeps answering when the storage is given to a tenant.
  [MULTI-TENANCY.md](./MULTI-TENANCY.md#168-a-storage-moved-to-another-tenant-what-was-made-on-it-before---closed-059)
  has what was measured and what each door asks. Reported by @mappygrid.
- **Switching an account off stops what it holds, its API tokens on every
  route first**
  ([GHSA-58wc-42xh-89v8](https://github.com/BRF-Tech/filex/security/advisories/GHSA-58wc-42xh-89v8)).
  The agent API and its MCP server (`/api/ai`) and the ShareX upload
  (`/api/sharex`) checked a token with a copy of their own that never asked
  whether the token's account was still on: a disabled account's token went
  on reading and writing its files, running MCP tools and minting public links,
  and an administrator's admin token kept the admin surface, from which it
  could switch its own account back on. Every route a token opens now asks one
  shared check and answers a disabled account as a session is answered,
  `403` with `reason: account_disabled`. A live-updates or editing-together
  socket ticket the account took before it was switched off no longer opens
  either, and a live-updates socket it has open is closed within about half a
  minute. Its share links, file requests and upload tickets answer like ones
  that do not exist and take nothing - until now a link kept serving the files
  and a file request kept writing into the account's folder - an ONLYOFFICE
  editor it left open no longer saves over the document, and an
  editing-together member leaves the session at its next touch. Switched back
  on, the same tokens, links and PINs work again
  ([SHARING.md](./SHARING.md#a-link-follows-its-creator)). Reported by
  @mappygrid.
- **Only a person signed in to the panel switches an administrator's account
  on or off.** An admin-scoped API key, on `/api/admin`, `/api/ai/admin` or as
  the `admin_users_update` MCP tool, now gets `403 session_required` for it,
  as it already did for promoting an account or setting an administrator's
  password - another administrator's account and the key's own alike.
  Ordinary accounts are still a key's to switch.
- **Suspending a tenant stops what its people already hold.** Suspension
  refused a new sign-in only: a session that was open, and every API key the
  tenant's people held, kept working on every route until it ran out or was
  revoked. Both now answer as the sign-in does, `403` with
  `reason: tenant_suspended`; a socket ticket no longer opens and an open
  live-updates socket is closed, and its people's links, file requests, upload
  tickets and open editors pause as a switched-off account's do. Resuming the
  tenant gives all of it back.
  Suspending and resuming a tenant now need an administrator signed in to the
  panel: an API key gets `403 session_required` for them on
  `/api/admin/providers`, `/api/ai/admin/providers` and `admin_tenants_update`,
  and so does deleting a tenant together with its accounts (`?force=1`,
  `admin_tenants_delete` with `force`).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0590---2026-11-06)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.59.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.59.0`

## v0.58.2

<span class="filex-release-date">6 November 2026</span>

A file on a mount inside a local storage can now be deleted. The trash lives at the storage root, so a delete from an rclone mount, an NFS share or a second disk under that root was a rename across filesystems, which the operating system refuses, and the error said "not a directory" about a file that was plainly there. A delete, a move or a restore across such a boundary now copies and then removes the source, only once all of it has arrived. On a cloud mount that means the bytes travel to the trash; adding the mount as a storage of its own keeps a delete there a rename. Thanks to @juan11perez for the report and the notes that pinned it down. S3, WebDAV and FTPS storages can also reach a server with a self-signed certificate now: FILEX_STORAGE_TLS_SKIP_VERIFY=true turns the check off, and the docs show the safer way that keeps it on. Thanks to @kladiv for asking.

## What changed

### Added

- **S3, WebDAV and FTPS storages can reach a server with a private or
  self-signed certificate**
  ([GitHub #134](https://github.com/BRF-Tech/filex/issues/134), reported by
  @kladiv). `FILEX_STORAGE_TLS_SKIP_VERIFY=true` turns the certificate check off
  for those storages, and the log says so at startup. It is insecure:
  [STORAGE.md](./STORAGE.md#private-and-self-signed-certificates) has what
  that exposes, and the way that keeps the check on by trusting the certificate
  with one read-only mount.

### Fixed

- **A file on a mount inside a local storage can be deleted**
  ([GitHub #133](https://github.com/BRF-Tech/filex/issues/133), reported by
  @juan11perez). The trash lives at the storage root, so deleting from an rclone
  mount, an NFS share or a second disk under that root was a rename across
  filesystems, which the operating system refuses. A move or a restore across
  such a boundary failed the same way. The local driver now copies and then
  deletes: the source is removed only once all of it has arrived, a link
  travels as a link, and a file that lands in a folder while it is being moved
  stays where it landed.
  [STORAGE.md](./STORAGE.md#a-mount-inside-a-local-storage) has what that
  costs on a cloud mount, and how to avoid it.
- **A delete that fails says why.** A file on a local storage whose move into
  the trash failed reported `open …: not a directory`, whatever the reason was,
  because the fallback for object-store folders answered in its place.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0582---2026-11-06)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.58.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.58.2`

## v0.58.1

<span class="filex-release-date">5 November 2026</span>

Mail now goes out through servers that take AUTH LOGIN and not AUTH PLAIN. filex logged in with PLAIN whatever the server offered, so such a server answered 504 and no mail left. It now reads what the server offers and logs in with PLAIN or LOGIN by itself, with nothing to configure. A wrong password is not tried twice, and the username and password still go only over an encrypted connection. Thanks to @kladiv for the report and the tests that pinned it down.

## What changed

### Fixed

- **Mail goes out through servers that take AUTH LOGIN and not AUTH PLAIN**
  ([GitHub #132](https://github.com/BRF-Tech/filex/issues/132), reported by
  @kladiv). filex logged in with PLAIN whatever the server offered, so such a
  server answered `504 5.7.4 Unrecognized authentication type` and no mail
  left. filex now reads the mechanisms the server offers and logs in with
  PLAIN or LOGIN accordingly, and a mechanism the server answers with 504 is
  followed by the other one on a new connection. A wrong password is not tried
  twice. LOGIN keeps PLAIN's rule: the username and password go only over an
  encrypted connection, or to this machine. **Send test** now calls a 504 a
  failed login, with the server's words below it, instead of "The mail server
  refused the message."

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0581---2026-11-05)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.58.1) · `ghcr.io/brf-tech/filex:slim-v0.58.1`

## v0.58.0

<span class="filex-release-date">3 November 2026</span>

The forms anybody can post can now ask for a bot check: the sign-in, a demo's one-click sign-in, the PIN of a protected link and the upload into a file request, each on or off by itself on the new Admin, Bot protection page, with Cloudflare Turnstile or Google reCAPTCHA. It is off until you turn it on. When the provider cannot be reached, or its keys cannot work, the form is neither left open nor shut: the browser does a short proof of work instead and the administrators are told. The keys can also come from CAPTCHA_* environment variables, which - unlike filex's other settings - are read live whenever the page leaves a field empty.

## What changed

### Added

- **Bot protection on the forms anybody can post.** The sign-in, a demo's
  one-click sign-in, the PIN of a protected link (a share, an app's page, a
  file request) and the upload into a file request can now ask for a bot
  check: [Cloudflare Turnstile](https://developers.cloudflare.com/turnstile/)
  (free, cookieless, invisible to most people) or Google reCAPTCHA v2. It is
  set on the new **Admin → Bot protection** page: the provider,
  the site key, the secret (stored sealed with `FILEX_SECRET_KEY`, never shown
  again), the forms one by one, and **Test the keys**, which asks the provider with the
  keys on the page. It is **off** until you turn it on. The check runs before
  anything else: a refused check is not a wrong password and spends none of a
  PIN's attempts. The widget is one shared component: the sign-in page (the
  desktop app signs in on it too) and the public link pages show the same one.
  [Bot protection](./BOT-PROTECTION.md).
- **When the provider cannot be reached, the form neither opens nor shuts.**
  No connection, no answer in four seconds (asked twice), a server error, an
  answer that is not the provider's, or keys that cannot work (no site key, no
  secret, a secret the provider refuses or that no longer opens): the browser
  does a short proof of work instead - one to three seconds on a desktop - and
  sends the form again by itself, and the administrators are told
  (`captcha_fallback`, `captcha_misconfigured`; `filex_captcha_fallback_total`
  on `/metrics`). The proof of work is accepted only while the provider is
  down, so a bot cannot skip the widget with it, and a refusal from the
  provider - a 429 included - is a refusal, never an outage. Several instances
  share the outage and the once-only marks through the database.
- **The environment can set it too**: `CAPTCHA_PROVIDER`, `CAPTCHA_SITE_KEY`,
  `CAPTCHA_SECRET`, `CAPTCHA_FORMS` (and `CAPTCHA_FALLBACK`,
  `CAPTCHA_VERIFY_TIMEOUT_MS`). ⚠ Unlike filex's other database settings,
  these are **read live, not seeded**: each field is the page's when the page
  holds one, else the variable's, else off, and a field cleared on the page is
  the variable's again ([Configuration](./CONFIGURATION.md#bot-protection-captcha)).

### Changed

- **While a link's PIN or a file request's upload is protected, the pages
  without JavaScript cannot answer it.** The bot check runs in the browser, so
  the no-JavaScript PIN form, the classic upload page and a PIN in the address
  (`?pin=`, `X-Filex-Pin`) answer *JavaScript needed* (422) instead of a form
  that cannot succeed; nothing changes while those forms are not protected.
  `filex client login` with a password on a protected sign-in says to use an
  API key (`FILEX_TOKEN`) instead.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0580---2026-11-03)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.58.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.58.0`

## v0.57.7

<span class="filex-release-date">30 October 2026</span>

A tall picture can now be read in the image viewer, thanks to @chengjie (PR #131). An image still opens fitted to the window, where a long scan or screenshot is a narrow strip; two new buttons beside the zoom controls change that. Fit width makes the picture as wide as the viewer and lets it scroll down to its last line, which stays clear of the controls, and Original size shows it at its own pixels. Zoom steps from the chosen mode, the percentage takes the picture back to the window, and the next image opens fitted again.

## What changed

### Added

- **A tall picture can be read in the image viewer** (by @chengjie in
  [PR #131](https://github.com/BRF-Tech/filex/pull/131)). An image opens
  fitted to the window, so a long scan or screenshot showed as a narrow strip
  whose text could not be read. Two buttons beside the zoom controls change
  that: **Fit width** makes the picture as wide as the viewer and lets it
  scroll down to its last line, which stays clear of the controls, and
  **Original size** shows it at its own pixels, scrolling either way. Zoom
  steps from the chosen mode, the percentage takes the picture back to the
  window, and the next image opens fitted again. The controls wrap onto two
  rows on a narrow screen, and the chosen mode is announced to screen
  readers.

### Fixed

- **The macOS, Microsoft Store and snap packages offer "Open with filex"
  for every kind the app store's apps open again.** These packages declare
  their file types when they are built, from the store's signed catalog.
  The Office editor 0.4.0 came to the store after 0.57.6 was built and opens
  the older Office formats too (`.doc`, `.xls`, `.ppt`, `.rtf`, their
  templates and macro-enabled forms, and the OpenDocument flat and template
  files), so those packages did not offer filex for them. They now declare
  62 kinds instead of 23. Windows' installer and Linux learn the kinds from
  your servers while the app runs, and already had them.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0577---2026-10-30)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.57.7) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.57.7`

## v0.57.6

<span class="filex-release-date">26 October 2026</span>

Three fixes, two of them by @chengjie. A folder shared with a group shows the group in the details panel's people list, by its name, instead of "#undefined" with a question mark (PR #129, by @chengjie). An app with its own interface fills the window below its heading and scrolls inside its own frame, on a phone and in a short window too, instead of sitting in a card that made the whole page scroll (PR #130, by @chengjie). And cancelling a job at the very moment the queue picks it up works the first time, and a job waiting for a storage scan keeps showing why it waits instead of flickering to "Deleting".

## What changed

### Fixed

- **Cancelling a job at the instant the queue picks it up works the first
  time.** For a moment between the queue taking a job and the job starting
  (or going back to wait for a storage scan), the cancel button answered
  "could not be cancelled", and only a second press got through. The cancel
  is now noted in that moment, and the job ends without running. A rename or
  restore can still be cancelled until it starts, and not after.
- **A job waiting for a storage scan keeps showing why it waits.** While the
  scan held the storage, the queue picked the waiting job up again every few
  seconds only to put it back, and each time the operations centre showed it
  as running ("Deleting") for a moment, without the "waiting for its turn"
  reason. The queue now leaves that storage's jobs alone while the scan
  holds it, and still starts the job the moment the scan lets go.
- **A group's grant shows the group in the details panel** (fixed by
  @chengjie in [PR #129](https://github.com/BRF-Tech/filex/pull/129)). When
  a folder was shared with a group, directly or through a folder above it,
  the details panel's people list read the grant as a person's and showed
  `#undefined` with a `?` in place of the group. It now names the group (its
  number, for a group without a name), says it is a group, and keeps the
  permission level and the "inherited" note. Only the display changed: who
  can open the folder is as before.
- **An app with its own interface fills the window below its heading**
  (fixed by @chengjie in
  [PR #130](https://github.com/BRF-Tech/filex/pull/130)). An app's home page
  with its own interface, opened from the Apps menu, sat in a card whose
  minimum height did not count the heading above it, so in a short window
  the whole page scrolled as well as the app. The app now gets the space
  below the heading, without the card's border and inner padding, and
  scrolls inside its own frame, on a phone too. Home pages drawn from a
  Surface keep their card and scroll as a document, and the app's sandbox
  and permissions are unchanged.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0576---2026-10-26)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.57.6) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.57.6`

## v0.57.5

<span class="filex-release-date">21 October 2026</span>

Two fixes reported by @chengjie. An upload from Safari no longer stops for good after its first chunk: Safari could get the server's whole answer to a chunk sent straight from a file on disk and still never finish the request (#125). Each chunk is now read into memory before it is sent, and a chunk whose bytes stop moving for two minutes is retried from what the server already has (PR #126, by @chengjie). And a delete, move, rename or restore queued while its storage is being scanned starts at once instead of waiting minutes for the whole scan (#127): the scan finishes the folder in hand and lets the job through, and while a job waits its row says why instead of only "Queued".

## What changed

### Fixed

- **An upload from Safari no longer stops for good after its first chunk**
  ([GitHub #125](https://github.com/BRF-Tech/filex/issues/125), fixed by
  @chengjie in [PR #126](https://github.com/BRF-Tech/filex/pull/126)). In
  Safari a chunk sent straight from a file on disk could reach the server and
  get its whole answer back, yet the request never finished, so the upload
  waited on that chunk forever and its retries never ran. Each chunk is now
  read into memory before it is sent (one chunk at a time, so the extra
  memory is the chunk size per running upload, not the file size), and a
  chunk whose bytes stop moving for two minutes is cut off and handed to the
  retry path, which first asks the server how much arrived and sends only the
  rest. A slow upload that keeps moving is never cut off, and the stall
  message is in the explorer's own language.
- **A delete, move, rename or restore queued while its storage is being
  scanned starts at once instead of waiting for the whole scan**
  ([GitHub #127](https://github.com/BRF-Tech/filex/issues/127), reported by
  @chengjie). On a local storage of tens of thousands of entries a folder
  delete sat in the operations centre as "Delete · Queued" for the scan's
  four or five minutes and ran two seconds after it ended. The scan holds the
  storage's row gate for one directory at a time and takes it again a few
  microseconds after letting go; the queue put the job back whenever the scan
  held the gate and asked again only at its next round, so every try met the
  scan. A job put back now tells the gate it is waiting: the scan finishes the
  directory in hand and lets the job through before the next one, and the
  queue is woken as soon as that directory is done. The gate gives the same
  turn to every change that waits for it (a WebDAV or SFTP rename, a delete
  in the explorer). A job that is not run within 10 seconds of saying it waits
  (its queue busy with another storage's job) no longer holds the scan back.
  While a job waits for the scan its row says so in the reader's language,
  and the operations centre shows that instead of "Queued": the ops rows
  carry `wait_reason` (`storage_scan`), `wait_text` and `waiting_since`
  ([BACKEND.md](./BACKEND.md#get-apifilesops-)). New metrics
  `filex_rowgate_waiting_changes` and `filex_rowgate_lapsed_wants_total`
  ([METRICS.md](./METRICS.md#the-row-gate)).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0575---2026-10-21)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.57.5) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.57.5`

## v0.57.4

<span class="filex-release-date">20 October 2026</span>

The built-in viewer shows a PDF in Chrome again. Since 0.48.0 filex's pages name the frames they may show of filex by path, and Chrome checks a PDF in the viewer against that list, so it showed the "could not show" fallback instead of the document (#124). A PDF's preview now has a path of its own, the one API path filex's pages may frame: it answers only a PDF, always as a preview, with the same checks as before. The fallback's Open in new tab opens the PDF instead of downloading it, and Download still downloads.

## What changed

### Fixed

- **The built-in viewer shows a PDF in Chrome again, and its "Open in new
  tab" opens the PDF instead of downloading it**
  ([GitHub #124](https://github.com/BRF-Tech/filex/issues/124), reported by
  @chengjie). Since 0.48.0 filex's pages name the frames they may show of
  filex by path, and Chrome checks a PDF in the viewer's `<object>` against
  that list: the preview URL was not on it, so Chrome showed the "could not
  show" fallback instead of the document. A PDF's preview now has a path of
  its own, `GET /api/files/pdf/{name}?path=`, the one API path filex's pages
  may frame. It answers only a PDF, always as a preview, with the manager's
  checks and `Range`; servers that have it say `pdf_preview` in their
  capabilities, and the explorer uses it for PDFs there (an embed on an older
  server keeps the manager's URL). The fallback's **Open in new tab** pointed
  at the download; it now opens the preview, and **Download** still
  downloads ([BACKEND.md](./BACKEND.md#get-apifilespdfname-)).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0574---2026-10-20)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.57.4) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.57.4`

## v0.57.3

<span class="filex-release-date">19 October 2026</span>

A PDF or an office file can no longer take the server down while its text is read for search. Some PDFs of about a megabyte sent the PDF reader into memory without end, and the kernel killed filex (#123). Their text is now read in a process of its own, with a memory ceiling and a two-minute limit; a file that passes either is found by its name only, and filex goes on. The ceiling is a new setting, Memory for reading one file, on Admin → Settings → General, shared from the indexing memory by default. A queued job whose attempts are spent is no longer handed out again after a crash. An embedded explorer now speaks its visitor's language and has a Language setting of its own, the desktop app stops waiting for a server's languages after ten seconds, and @brftech/filex's type declarations are valid TypeScript again. This patch is not installed automatically: update by hand.

## What changed

### Changed

- **An embedded explorer speaks its visitor's language, and has a Language
  setting of its own.** `<filex-explorer>` - and the explorer in any host that
  does not keep the person's preferences on their account - put the host's
  `config.locale` above everything, so every visitor read the host's language
  whatever their browser asked for. It now speaks the language picked in this
  browser under **⋯ → Language** (kept in the browser, sent nowhere), then the
  account's language when a person is behind the credential, then the
  browser's languages that the server offers (language packs included: the
  embed loads the server's list when it mounts), then `config.locale`, then
  English. In such a personal embed a language picked there is saved to the
  account too, and the host's `config.locale` is no longer written to an
  account that has no language. The filex web app and desktop app keep the
  account's language ([INTEGRATION.md → Language](./INTEGRATION.md#language),
  task #275).

### Fixed

- **A PDF or office file can no longer take the server down while its text is
  read** ([GitHub #123](https://github.com/BRF-Tech/filex/issues/123)). Some
  PDFs of about a megabyte sent the PDF reader into memory without end: one
  install's filex reached 27 GB and the kernel killed it, three times in a
  minute. PDF and office files are now read in a process of their own, with a
  memory ceiling and a two-minute limit; one that passes either is stopped,
  the file is found by its name only, and filex goes on. The ceiling is a new
  setting on **Admin → Settings → General → Search settings**, **Memory for
  reading one file** (`search.extract_memory_mb`, first value from
  `FILEX_SEARCH_EXTRACT_MEMORY_MB`): `0`, the default, shares the indexing
  memory budget between the files read at once (256 MB to 1 GB). A job
  cancelled while filex stops is no longer recorded as a file without text
  ([SEARCH.md → Content search](./SEARCH.md#content-search)).
- **A queued job whose attempts are spent is not handed out again after a
  crash.** The orphan recovery at startup put every job left running back in
  the queue, so a job that brought filex down came back at every start; one
  whose attempts are spent is now failed (`interrupted: filex stopped while running it, and its attempts are spent`), on SQLite, MySQL, PostgreSQL and
  Redis ([GitHub #123](https://github.com/BRF-Tech/filex/issues/123)).
- **The desktop app no longer waits forever for a server's languages.** A
  server that accepted the connection and never answered kept the window on
  "Connecting" with no Retry; the list is given up after ten seconds and the
  file list opens in English or Turkish (task #275).
- **`@brftech/filex`'s type declarations are valid TypeScript again.**
  `dist/index.d.ts` carried `loadLocales` as its source - a function body and a
  default value in a declaration file - which a project that type-checks its
  libraries refused; it is now declared with `@brftech/filex-core`'s type
  (task #275).
- **The `locale` attribute of `<filex-explorer>` takes a language pack's
  language** (`locale="es"`), as the package README says; it took only `en`
  and `tr`, and anything else was dropped (task #275).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0573---2026-10-19)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.57.3) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.57.3`

## v0.57.2

<span class="filex-release-date">19 October 2026</span>

Security patch: a file request's total cap now holds when uploads overlap. Uploads sent at the same time all passed the max_uploads check, so a link capped at one file took fifteen (GHSA-4pc6-66f4-q2x4); a submission's files are now claimed against the cap before the first one is written. This patch is not installed automatically: update by hand. In the desktop app, the file list follows an account's language from a language pack (contributed by @chengjie, #122), the snap says what the keyring and removable-media plugs allow before it shows the command that connects them, and the admin panel's one-column menus open under their own button.

## What changed

### Security

- **A file request's total cap holds when uploads overlap**
  ([GHSA-4pc6-66f4-q2x4](https://github.com/BRF-Tech/filex/security/advisories/GHSA-4pc6-66f4-q2x4)).
  `max_uploads` was checked against the count the request had read and the
  count moved only after the files were written, so uploads sent at the same
  time all passed: a link capped at one file took fifteen concurrent uploads,
  and where the owner's account has no quota the files could keep coming.
  A submission's files are now claimed against the cap in one database
  statement before the first is written, and a file the storage did not take
  gives its place back. Reported by @Refaelyk.

### Fixed

- **The snap says what a plug allows before it asks you to connect it.** The
  Snap Store leaves `password-manager-service` and `removable-media` for you
  to connect, so the desktop app now asks snapd (`snapctl is-connected`)
  instead of guessing. With the keyring plug not connected, the sign-in window
  says that connecting it lets filex keep its tokens encrypted in GNOME
  Keyring or KWallet, gives the snap access to the secrets stored there and
  lets other apps that reach the keyring read what filex puts there, and only
  then shows the command; with the plug connected and no keyring answering,
  it says to start or unlock the keyring. A sync folder chosen under
  `/media`, `/run/media` or `/mnt` while `removable-media` is not connected
  ended in a permission error that did not say why; it is now refused with
  what the plug allows and a button that copies the command
  ([DESKTOP.md → The plugs you connect](./DESKTOP.md#the-plugs-you-connect)).
- **The desktop app's file list speaks the account's language from a
  language pack.** The explorer inside the desktop window stayed in English or
  Turkish when the account used a language a language pack adds on the
  server: the window never loaded the server's list of languages, and handed
  the explorer its own built-in fallback instead of the account's language.
  It now loads the active server's languages before the explorer opens,
  follows the account's language (the window's own menus keep English or
  Turkish), forgets the previous server's languages on a server switch and
  ignores a late answer from it. Contributed by @chengjie (#122).
- **The admin panel's one-column menus open under their own button.** In the
  top bar, *People* and *Maintenance & records* opened their panel at the
  bar's start, under the dashboard link, two or three buttons away from the
  one that was pressed. A one-column panel now hangs from its own button (in
  a right-to-left interface, from its right edge); the wide panels with
  several columns keep the bar's start, where they have room.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0572---2026-10-19)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.57.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.57.2`

## v0.57.1

<span class="filex-release-date">17 October 2026</span>

A decimal typed into a CSV in ONLYOFFICE keeps the file's comma: in the Turkish or the German editor a number like 4,5 typed into a semicolon file no longer goes back to the file as 4.5 (or 1,1 as 1.1000000000000001), wherever its column writes decimal commas - and a CSV saved from the Office editor app gets the same rule. Contributed by Berk Başarır.

## What changed

### Fixed

- **A decimal typed into a CSV in ONLYOFFICE keeps the file's comma.** In the
  Turkish or the German editor a decimal typed with a comma is a number, and
  the save wrote it with a point: `3,5` typed over as `4,5` in a semicolon
  file went to the file as `4.5`, and `1,1` as `1.1000000000000001`, in a
  column of `3,5` and `1250,75` that ONLYOFFICE kept as they were (measured on
  Docs 9.4.0). A number written with a point that somebody typed, or put in a
  row they added, is now written with a decimal comma where its column writes
  one - a file not separated by commas, a column with decimal commas and no
  decimal point, still standing where it stood, and a number that did not
  move there from elsewhere in the file - with 16 or 17 significant digits as
  the shortest decimal that is the same number (`1,1`). An exponent, a
  percent and a date are written as before. A CSV saved from the Office
  editor app gets the same rule
  ([ONLYOFFICE.md → A decimal you type keeps the file's comma](./ONLYOFFICE.md#a-decimal-you-type-keeps-the-files-comma)).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0571---2026-10-17)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.57.1) · `ghcr.io/brf-tech/filex:slim-v0.57.1`

## v0.57.0

<span class="filex-release-date">17 October 2026</span>

Office documents no longer need a Document Server: the Office editor app (0.4.0 or later) edits Word, Excel and PowerPoint in the browser and, on the server, converts them and draws their thumbnails with ONLYOFFICE's own converter and page layout compiled to WebAssembly, so a default install no longer runs an ONLYOFFICE container (one can still be connected). The explorer gains an AI assistant - Claude, OpenAI or a local Ollama - that works through filex's own tools with the person's own permissions; it stays off until an administrator turns it on. Every admin setting has one home: the admin menu has a Settings panel, the server's defaults are settings there (environment variables only seed them), and a tenant's administrator sees only the tenant's pages. A whole storage can be given to people and groups, and previews load no picture from another server until you ask. On the desktop, "Open with filex" takes any kind of file an app on your servers opens and moves files of any size without holding them in memory, and folder sync deletes from the computer only what the server still has. File names follow one rule at every door: control characters are refused, a right-to-left override can no longer hide an extension, and archives keep every name. Nightly builds of the desktop app are published every night. Security fixes are listed in the changelog.

## What changed

### Added

- **A picture from another server in a file's preview waits for you.** A
  Markdown file, a notebook's cells and HTML outputs and an app's release
  notes on the update review loaded every picture they named, so opening a
  shared file told the server a picture was on that the file was opened, and
  from which IP address. Such a picture is now a placeholder with its alt
  text and its server's name; clicking it asks in filex's own dialog, which
  names the server: *Load image* loads that one, *Load all remote images in
  this file* the others too until the file is closed. A `srcset` or
  `<picture>` source from elsewhere is dropped. The new **User settings →
  Preferences → Load remote images in previews** (off by default, kept on the
  account) loads them without asking. A loaded picture goes with no referrer.
  Thumbnails, folder previews and the file cards never load one, whatever the
  setting says. The same in the web interface, the desktop app and every
  embed ([FEATURES.md](./FEATURES.md#viewers-editors-search-and-thumbnails)).
- **A Mermaid diagram and an EPUB book ask no other server either.** A
  diagram's image node (`@{ img: ... }`), a picture in an HTML label and a
  `themeCSS` url() were fetched while Mermaid laid the diagram out, and an
  EPUB chapter loaded every picture, style sheet and font it named from
  elsewhere. A diagram is now laid out in a frame of its own that fetches
  nothing (Mermaid's `sandbox` level; `themeCSS` and `dompurifyConfig` can no
  longer be set from a diagram), and its pictures from elsewhere are
  placeholders like a Markdown file's. An EPUB chapter is drawn in a frame
  that loads only the book's own files and sends no referrer; the reader says
  when pictures were held, and *Load all remote images in this file* or the
  setting draws it again with its pictures and style sheets (a url() in the
  chapter's own styles and a font from elsewhere never load).
- **Give a whole storage to people and groups.** A storage's page in the admin
  panel has an **Access** section: pick a person or a group and a level, and
  they reach every file and folder of the storage, however deep, on every
  surface at once - the web interface, the desktop app, WebDAV, SFTP, FTPS,
  NFS, the S3-compatible API and an AI agent. The level is changed in the row,
  **Revoke all access** takes back the storage-wide assignments and leaves
  every folder grant as it is, and where a folder grant and the storage's both
  apply the higher one counts (nothing narrows access). With per-item access
  control off the section says why nobody can be given the storage yet and
  turns it on in place, asking first who keeps access. The explorer offers the
  same on a storage row to whoever may manage it - an administrator, or a
  person given the storage as owner (the storage list now says each caller's
  level at every root, `storage_info[].perm`): **Access…** opens the access
  dialog on the storage, with people and groups only - no link and no file
  request. An assignment is an ordinary grant on the storage's root
  (`POST /api/files/permissions {"path": "<storage>://"}`), so no table and no
  route were added. **Folder access** and a group's page name such a grant
  "Whole storage" instead of "/"
  ([RBAC.md](./RBAC.md#a-whole-storage-for-people-and-groups)). Requested by
  @gguglietti ([GitHub #117](https://github.com/BRF-Tech/filex/issues/117)).
- **Nightly builds of the desktop app, every night: on
  [filex.sh/nightly](https://filex.sh/nightly/) and the Snap Store's `edge`
  channel.** Every night the nightly test run passes on a change, the desktop
  app of that night is built for Windows (x64, arm64), macOS and Linux
  (AppImage, `.deb`, `.rpm`; x64 and arm64) and goes to filex.sh/nightly, with
  the night, the commit and the checksums, and as a snap to `edge` on x64 and
  arm64: `sudo snap install filex-app --edge`. The last nights are kept on
  ghcr.io (`ghcr.io/brf-tech/filex-desktop-nightly`). Its version names the
  night and the commit (`0.57.0-nightly.20261014+2ca25999`). A nightly is not
  supported and not signed; try it on a copy of your data
  ([INSTALLATION.md](./INSTALLATION.md#nightly-builds)).

- **What a nightly image is, read off the image.** `ghcr.io/brf-tech/filex:nightly`
  now carries labels for the public commit it was built on
  (`sh.filex.nightly.public-base`) and its night's test result
  (`sh.filex.nightly.tests`) beside its commit, and a new
  [Nightly builds](./INSTALLATION.md#nightly-builds) section says how to
  try one, how to read which commit it is, and why it is not for the data you
  keep.
- **Search settings in the admin panel.** Whether filex reads the text inside
  files, the largest file it reads, how much memory indexing may take and
  whether an outdated index is repaired at startup are now settings an
  administrator changes on **Admin → Settings → General** (the Search index
  page links there; and through
  `GET`/`PATCH /api/admin/search/settings` and the admin MCP tools
  `admin_search_settings_get` / `_update`), instead of variables that need a
  restart. The first three apply at once. `FILEX_SEARCH_CONTENT`,
  `FILEX_SEARCH_CONTENT_MAX`, `FILEX_SEARCH_MEMORY_PERCENT` and
  `FILEX_SEARCH_AUTO_REBUILD` (and their `config.yaml` keys) give these
  settings their first value and are ignored after that, so an upgrading
  install keeps what it had set
  ([SEARCH.md](./SEARCH.md#configuration)). Follows up
  [GitHub #115](https://github.com/BRF-Tech/filex/issues/115) (task #261).
- **An AI assistant in the explorer.** The disabled "coming soon" button is
  gone; in its place, once an administrator sets it up on **Admin → Settings
  → AI assistant**, is an assistant that finds, reads, summarises, tidies up
  and moves files - against **Anthropic** (Claude), **OpenAI** or any
  OpenAI-compatible server (OpenRouter, vLLM, LM Studio), or a local
  **Ollama** that keeps everything on your network. It works through filex's
  own MCP tools, called inside the server **as the person who asks** - their
  folder access, the `files.download` rule, their tenant and their root, no
  key minted for it. Its panel streams the reply, shows every step a tool
  took and takes the selection as context. A change that can be undone
  (move, rename, copy, write, delete to the trash) happens at once with an
  **Undo**; one that cannot (a public or upload link, an access change, a
  delete with no trash) waits on an **approval card**. Encrypted folders,
  `.fxe` files and the vault never reach a model - refused before the tool
  runs and taken out of every listing - and every file content sent is in the
  audit log (`assistant.content_sent`: who, which file, which provider and
  model). **The assistant comes off; its new permission comes on for
  everyone.** `ai.assistant` says who gets the button: every preset holds
  it, and the first start of this version gives it to every saved role,
  custom ones included - once, so a role an administrator takes it away from
  keeps it away ([PERMISSIONS.md](./PERMISSIONS.md#things-to-know)).
  *Hide the button* closes the assistant for everybody and keeps the
  settings. The API key is
  sealed with `FILEX_SECRET_KEY` and never shown again; tool steps, reply
  length, a token budget per request, a timeout and a per-person rate are
  limits on the same page. Conversations are kept per person on the server
  for a set number of days (30 by default) and can be deleted from **User
  settings → AI assistant**. The settings are also `GET`/`PATCH /api/admin/assistant` and the admin MCP tools `admin_ai_assistant_get` /
  `_update` / `_test` ([AI-ASSISTANT.md](./AI-ASSISTANT.md)) (task #269).
- **An app that edits a CSV is told how the file is written, and may say how
  its save is.** `file.read` of a `.csv` answers `text_dialect`
  (`{delimiter, bom, crlf, encoding}`, read by the server from the file; text
  is decoded in the file's own encoding), and `file.save` takes
  `csv: {delimiter, encoding, bom, eol}`, which the server checks against the
  bytes. Apps written before it keep working: the server reads the bytes
  either way
  ([APP-PLUGINS-API.md → CSV files: how they are written](./APP-PLUGINS-API.md#csv-files-how-they-are-written)).
- **Office conversions and office thumbnails without a Document Server.** The
  Office editor app brings an office engine - ONLYOFFICE's converter and page
  layout, compiled to WebAssembly - and filex runs it on the server: the
  conversions apps ask for (`engines:office`: the Convert app, MCP's
  `file_convert`) and the thumbnails of office documents, the work a Document
  Server did. Apps need no change. filex runs the engine in a process of its
  own (`filex office-engine`, with no network and none of filex's
  environment), never inside its own binary; the engine comes with the app's
  release as `engine.zip`, pinned by its manifest's new `engines.office` block
  (derived permission `office-engine`); the install wizard's **Upload files**
  takes it in a field of its own beside the interface bundle, and says which
  file a manifest names is not chosen before it uploads anything. It is compiled once in the background
  after the app is installed or updated, or filex is - 3.5 to 5 minutes, then
  about a second from the cache in `<data dir>/office-engine/` - and is
  *being prepared* meanwhile: a connected Document Server takes the work, and
  without one a conversion fails as `office_preparing` and thumbnails wait.
  When both are there, **Admin → Plugins → Default apps → Office engine**
  chooses (the app's engine first by default) and shows how each stands
  (`GET /api/admin/file-types` → `engines`, `PUT`/`DELETE /api/admin/file-types/engines/office`). The thumbnail drawer stays
  ONLYOFFICE whichever back end draws, so existing pictures stay valid.
  `FILEX_OFFICE_ENGINE_MAX_MB` (default 2048, 512 to 4096) caps the memory of
  each of its modules; a process holding about twice that is ended on Linux
  and Windows. Documents with SVG, EMF or WMF pictures, HTML to Markdown and
  text in legacy code pages convert as a Document Server converts them; a
  picture or style sheet a document links from another server is left out,
  as a Document Server without a network leaves it, and nothing is fetched
  ([ONLYOFFICE.md → The office engine without a Document Server](./ONLYOFFICE.md#the-office-engine-without-a-document-server),
  [APP-PLUGINS-API.md → An app's office engine](./APP-PLUGINS-API.md#an-apps-office-engine-enginesoffice-057)).
  The app kit's test host gains `PrepareOfficeEngine()`
  ([PLUGIN-KIT.md → The office engine](./PLUGIN-KIT.md#the-office-engine)).
- **Desktop: "Open with filex" takes every kind of file an app on your
  servers opens.** A server with an app that opens PSD files opens a `.psd`
  double-clicked on your own disk, in that app, with the same working copy and
  write-back as an office document. The desktop asks every server it is signed
  in to which kinds its apps open (their `viewer` views, Default apps'
  switches and order included) at start, on an account change, every half
  hour, when its window comes to the front and before a double-click; a
  server that cannot be asked keeps what it said last. The installed Windows
  app adds those kinds to "Open with" under the user's own hive (ProgId
  `filex.AppDocument`) and takes them back when no server opens them, the
  uninstaller removing them all; the `.deb`, `.rpm` and AppImage write a
  hidden desktop entry and, for an extension the system has no type for, a
  type of filex's own at a lower weight than a system one, removed with the
  package. macOS, the Microsoft Store package and the snap, which cannot add a
  type while they run, declare every kind the app store's apps open, at build
  time, from the store's signed catalog (`desktop/build/app-kinds.json`,
  `pnpm --dir desktop app-kinds:refresh`; the release audit checks it). Where
  more than one server opens a kind the desktop asks which, and can remember
  it; where none does it says so, and nothing is uploaded. filex never makes
  itself the default for a kind. *App settings → Open documents with filex* shows
  *Kinds filex opens: N, from the apps on your servers*, the list, and
  **Refresh**
  ([DESKTOP.md → Which kinds open with filex, and where](./DESKTOP.md#which-kinds-open-with-filex-and-where)).
- **Desktop: "Open with filex" takes a large file, up to the server's own
  limit.** The working copy goes up the way the explorer sends a large file -
  from 8 MiB in parts, over the staged upload, the server checking its hash -
  and the saved version comes back in ranged parts; a dropped connection costs
  one part (the server says where to go on from), and a file saved again while
  it comes back is taken whole. The 256 MB ceiling of the app's own is gone:
  the limit is the server's `max_upload_size`.
- **Desktop: "Open with filex" moves a large file without holding it in
  memory.** The working copy goes up read from the document one part at a
  time, and a save comes back streamed into a file in the app's data folder
  (`openwith-spool`), hashed as it arrives and checked again as it replaces the
  document; memory stays at a few parts whatever the size of the file. A
  document saved again while it goes up is not committed half old and half
  new: that upload is dropped and the document sent again
  ([DESKTOP.md → What happens to the file](./DESKTOP.md#what-happens-to-the-file)).
- **Desktop: "Open with filex" opens a kind in the app you chose for it.**
  Where a server has more than one app for a kind, the account's own
  **Always open with** choice - the one the browser follows - comes before
  the administrator's Default apps order, as it does in the browser; an app
  switched off for the kind is still not taken. On Linux an extension the
  system gives several media types (`.otf`: a font and an OpenDocument
  formula template) is registered for the ones the app says it opens.
- **An app saves a legacy document beside it, as a Document Server's save
  is.** The Office editor opens a `.doc`, `.xls` or `.ppt` and writes OOXML;
  it says so (`file.save` `beside: 'docx'`), and filex writes the save
  beside the file, which is left as it was, under a name filex chooses
  (`rapor.docx`, `rapor (2).docx` when that is taken) and for a person who
  may create files in that folder; the answer says where it went
  (`{saved, name, beside}`), the person is told in the bell, the audit log
  writes `file.app_saved_beside`, and later saves of the same opening go to
  that file - in an editing session for every member (the session records
  it: migration 00133). One writer for both editors: the Document Server's
  callback now calls the same code (`internal/officesave`)
  ([APP-PLUGINS-API.md → Saving beside the file, conflict copies](./APP-PLUGINS-API.md#saving-beside-the-file-conflict-copies)).
- **A document changed while an app had it open gets a conflict copy, as
  with a Document Server.** An app that asks for it (`file.save`
  `conflict: 'copy'`) has a save of a file somebody else changed after it
  was read written as `<name>.filex-conflict-<UTC time>.<ext>` beside it,
  named in the answer, the other change kept; editing together, the
  session's own record decides. Every read of an app's file names the
  version handed over (`X-Filex-Version`), which the explorer sends back with
  such a save. An app that does not ask (every app built for 0.56) gets the
  0.56 answer, unchanged.
- **The editor language fixed for everybody reaches the Office editor app
  too.** The administrator's *Editor language* (External services →
  ONLYOFFICE, `FILEX_ONLYOFFICE_LANG`) is handed to an app's interface as
  `session.editor_lang`, read even with no Document Server configured
  ([ONLYOFFICE.md → The editor's language](./ONLYOFFICE.md#the-editors-language)).
- **Dictionaries for the office editor's other languages.** The languages
  the app's release offers are listed on its page, *Admin → Plugins → Apps →
  the app → Dictionaries*, and **Install** downloads one, every file checked
  against the release's sha256 (the manifest's `ui.extras.dictionaries.catalog`,
  a `filex-extras/1` catalog; `GET …/extras-catalog/{kind}`, `POST …/extras-catalog/{kind}/{item}`). Any other Hunspell dictionary (`.aff`,
  `.dic`, `hyph_<lang>.dic`) can be uploaded there. The section shows only
  for an app whose manifest names the kind (`ui.extras`): draw.io has none.
  The editor checks that language from then on. The files are the app's, kept outside its
  signed package: an upgrade keeps them, removing the app removes them. The
  app's interface reads them under its own package address
  (`extra/dictionaries/<lang>/<file>.txt`) with the `ui:package-fetch` grant
  it already has; the install review's sentence for that grant now says so.
  `GET /api/admin/app-plugins/{id}/extras`, `PUT` / `DELETE …/extras/<path>`; a change needs a signed-in administrator and is audited
  (`app_plugin.extra_add`, `app_plugin.extra_remove`)
  ([APP-PLUGINS.md → Dictionaries for the office editor](./APP-PLUGINS.md#dictionaries-for-the-office-editor)).
- **A CSV in Greek (Windows-1253) or Baltic (Windows-1257) is recognised.**
  The code page of a `.csv` is judged from its letters, now also by how well
  they read as one language's alphabet, so a Lithuanian, Latvian or Greek
  file opens with its own letters and is saved back in its page; Western,
  Turkish, Central European and Cyrillic files are judged as before.
- **Desktop: "Open with filex" edits in the Office editor app when the
  server has no Document Server.** A Word, Excel, PowerPoint, OpenDocument,
  RTF or CSV file double-clicked on your own disk opens in the app's editor
  (`/files/edit?…&app=office-editor/editor`) whenever no ONLYOFFICE Document
  Server answers and the app opens that type - also where a person or an
  administrator once picked filex's own viewer, which cannot edit an office
  document without a Document Server. With a Document Server the window opens
  what it opened before, and an app an administrator switched off for a type
  stays off. An edited `.doc`, `.xls` or `.ppt` comes home beside the document
  as `rapor.docx` (`rapor (2).docx` when taken), the window's later saves over
  that same file, as with a Document Server: the app's saves into the
  desktop's working copy are taken as the Document Server's are
  ([DESKTOP.md → Opening documents from your computer](./DESKTOP.md#opening-documents-from-your-computer)).
- **An office document opens in the Office editor app, in the browser as on
  the desktop, when there is no Document Server.** An earlier pick of
  filex's own viewer for .docx and the rest - a person's "Always open with",
  a Default apps rule, an "Open with" link - only said a Document Server is
  missing; where an app opens the kind it is no longer chosen (it stays in
  *Open with*). With a Document Server every pick holds as before.

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0570---2026-10-17).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.57.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.57.0`

## Earlier releases

The 140 releases before v0.57.0, in brief. Full notes are on GitHub.

| Version | Date | What changed |
|---|---|---|
| [v0.56.3](https://github.com/BRF-Tech/filex/releases/tag/v0.56.3) | 13 October 2026 | df, Explorer and Mountain Duck show the real disk instead of a made-up 1 PB: every surface that is asked how much room there is - SFTP, FTPS, NFS, WebDAV and filex mount - gives the same answer, the storage's own disk (or the… |
| [v0.56.2](https://github.com/BRF-Tech/filex/releases/tag/v0.56.2) | 12 October 2026 | Video, audio and PDF thumbnails read a file on a local storage in place instead of copying it whole to the temp directory first, so a folder of films no longer fills /tmp, and the copies a killed process left behind are removed… |
| [v0.56.1](https://github.com/BRF-Tech/filex/releases/tag/v0.56.1) | 12 October 2026 | Every table in the product lays each row out under its name when its pane is too narrow to be a table - the details panel, the explorer on a phone or in a narrow pane, an admin page on a phone - instead of cutting the values to… |
| [v0.56.0](https://github.com/BRF-Tech/filex/releases/tag/v0.56.0) | 11 October 2026 | Apps can edit a file of an end-to-end encrypted folder, or of a vault: the browser decrypts it for an app the person allowed, seals what the app keeps while it edits and encrypts what it saves before it leaves the browser, so the… |
| [v0.55.0](https://github.com/BRF-Tech/filex/releases/tag/v0.55.0) | 9 October 2026 | An app store lists storage plugins beside apps: the same link and review, every build pinned and signed by the store, the plugin checked by the store's validator before it is listed, and a paid one held while its license does not… |
| [v0.54.0](https://github.com/BRF-Tech/filex/releases/tag/v0.54.0) | 8 October 2026 | The server does the server's work: the rules, numbers and sentences the web app, the desktop app and the packages used to keep copies of now come from the server, so every surface says the same thing in the reader's language, and… |
| [v0.53.0](https://github.com/BRF-Tech/filex/releases/tag/v0.53.0) | 7 October 2026 | Replication finally copies: a storage linked to a replication target now writes its files there, each storage into a folder of its own, and an existing one gets its first full copy on its own (before 0.53 it never copied… |
| [v0.52.0](https://github.com/BRF-Tech/filex/releases/tag/v0.52.0) | 5 October 2026 | LDAP and Active Directory groups reach filex: a filex group can name directory groups and follows them at every sign-in, a directory sync opens accounts, brings the directory's groups in and can switch off people the directory no… |
| [v0.51.0](https://github.com/BRF-Tech/filex/releases/tag/v0.51.0) | 4 October 2026 | Who may encrypt is now an organisation's decision, contributed by @berkbasarir (#83): the tenant policy (Off, Administrators only, Permitted, or Approval), a new files.encrypt permission and, on multi-tenant installs, a ceiling… |
| [v0.50.0](https://github.com/BRF-Tech/filex/releases/tag/v0.50.0) | 3 October 2026 | Office documents open, convert and get their thumbnails through the connected ONLYOFFICE Document Server: LibreOffice is gone from every image and from filex, and the Test on the ONLYOFFICE card now downloads the way a document… |
| [v0.49.0](https://github.com/BRF-Tech/filex/releases/tag/v0.49.0) | 29 September 2026 | Roles and per-person permissions, contributed by @manjotsc (#75): 28 named permissions, one role per person built from them, per-person exceptions on top, and refusals that name the permission and the role in the reader's… |
| [v0.48.1](https://github.com/BRF-Tech/filex/releases/tag/v0.48.1) | 28 September 2026 | Apps can bring an interface of their own - HTML and JavaScript filex serves from the approved package into a sandboxed frame, opened as the viewer of a file type, as a kind of file in New document, or handing you a file to keep -… |
| [v0.47.0](https://github.com/BRF-Tech/filex/releases/tag/v0.47.0) | 26 September 2026 | filex runs under a sub-path behind a reverse proxy (a FILEX_PUBLIC_URL with a path works across the web app, WebDAV, S3, the desktop app, the CLI and Helm), apps keep themselves up to date within the filex version range they… |
| [v0.46.1](https://github.com/BRF-Tech/filex/releases/tag/v0.46.1) | 26 September 2026 | version as the release followed by the full commit hash and the build time, |
| [v0.46.0](https://github.com/BRF-Tech/filex/releases/tag/v0.46.0) | 26 September 2026 | storage row, or use its menu (right click, a long press, or Shift+F10): |
| [v0.45.1](https://github.com/BRF-Tech/filex/releases/tag/v0.45.1) | 25 September 2026 | 0.45.0 as it was meant to ship. **v0.45.0 was tagged but never published**: |
| [v0.44.2](https://github.com/BRF-Tech/filex/releases/tag/v0.44.2) | 25 September 2026 | The release 0.44.0 was meant to be, and the one to install the desktop app from, including through Homebrew, winget and Snap. |
| [v0.44.1](https://github.com/BRF-Tech/filex/releases/tag/v0.44.1) | 25 September 2026 | Tagged, but it stopped at the same winget step as 0.44.0; its container images, npm packages and CLI binaries were published. |
| [v0.44.0](https://github.com/BRF-Tech/filex/releases/tag/v0.44.0) | 25 September 2026 | Big local storages open at once: the new lazy catalogue lists a folder straight from disk and catalogues it behind the listing, with a throttled pass that fills in the rest (or only the folders people visit). |
| [v0.43.2](https://github.com/BRF-Tech/filex/releases/tag/v0.43.2) | 24 September 2026 | The release that 0.43.1 was meant to be: container images for everything in 0.43.0 (whose images never published), the storage-credential scrub, Berk Başarır's per-tile thumbnail rendering and a desktop sign-in fix for servers… |
| [v0.43.0](https://github.com/BRF-Tech/filex/releases/tag/v0.43.0) | 24 September 2026 | Apps. filex gains a second kind of plugin: a sandboxed WebAssembly app that adds actions to the file menu, screens filex draws for it, and public pages for people without an account - installed from a GitHub repository, a file or… |
| [v0.42.2](https://github.com/BRF-Tech/filex/releases/tag/v0.42.2) | 19 September 2026 | A fix release for the issue 32 follow-up and three things that were wrong on screen. The context menu is now the same on Recent, Starred, Shared with me, tag views, the Home cards and the Recently-opened tray as in a folder -… |
| [v0.42.1](https://github.com/BRF-Tech/filex/releases/tag/v0.42.1) | 19 September 2026 | A fix release for four reports on 0.42.0. A bucket with several top-level folders is one form now, not one per folder: Storages → Add lists the folders under the root you typed and creates one storage per ticked folder with the… |
| [v0.42.0](https://github.com/BRF-Tech/filex/releases/tag/v0.42.0) | 16 September 2026 | gesture is now the classic desktop file-manager one - a single click selects a |
| [v0.41.4](https://github.com/BRF-Tech/filex/releases/tag/v0.41.4) | 15 September 2026 | A fix release that carries 0.41.3, whose release build never finished. Only the checkbox selects now, and any other click or tap on a file or folder opens it - beside the name, on the size and date cells, with a selection, with… |
| [v0.41.2](https://github.com/BRF-Tech/filex/releases/tag/v0.41.2) | 15 September 2026 | A fix release for four reports on 0.41.1. A press on a file or folder name now opens it on every device - a tap on a phone, a click on a desktop - even while something is selected; the checkbox selects, and a right click or a… |
| [v0.41.1](https://github.com/BRF-Tech/filex/releases/tag/v0.41.1) | 14 September 2026 | A fix release for three reports and the rough edges left after 0.41.0. Moving a file larger than 8 MiB onto an S3 storage served over plain http (Garage or MinIO on a container network) works again (#27). |
| [v0.41.0](https://github.com/BRF-Tech/filex/releases/tag/v0.41.0) | 14 September 2026 | The explorer has one face now, on every surface. It is rebuilt around the end-user shell a contributor designed on top of filex (#14): a top bar with one search field and + New, a panel with Home, Shared with me, Recent, Starred… |
| [v0.39.1](https://github.com/BRF-Tech/filex/releases/tag/v0.39.1) | 12 September 2026 | The quick-look key legend is a small pill again. Pressing Space over a file opens the preview with a legend at the bottom edge; in the web UI it was drawn as a giant rounded shape across the whole window, on top of the file being… |
| [v0.39.0](https://github.com/BRF-Tech/filex/releases/tag/v0.39.0) | 12 September 2026 | Two things a storage was missing. First, an address that does not move: a storage's name is the first path segment on WebDAV, SFTP, NFS and the S3-compatible API, so renaming one silently re-addressed it and every mount written… |
| [v0.38.2](https://github.com/BRF-Tech/filex/releases/tag/v0.38.2) | 12 September 2026 | Editing a storage now takes effect on the running process. Creating one started its syncer and deleting one stopped it, but editing one did neither: the row was written and the save reported as successful while the syncer and… |
| [v0.38.1](https://github.com/BRF-Tech/filex/releases/tag/v0.38.1) | 11 September 2026 | Renaming a folder no longer puts its contents in the trash. The rename moved one row and left every file and subfolder pointing at the old path, so the next sync tombstoned them and then collided with itself once per file; a… |
| [v0.38.0](https://github.com/BRF-Tech/filex/releases/tag/v0.38.0) | 11 September 2026 | The release where PostgreSQL and MySQL started actually working. Both were listed as supported and neither had ever been run against a test: `binary` is a reserved word on both engines, one MySQL migration had never been written,… |
| [v0.37.0](https://github.com/BRF-Tech/filex/releases/tag/v0.37.0) | 7 September 2026 | A release about checks that were narrower than they looked. The External Services Test button probed only from the filex server, so a container-internal address answered it happily while the browser that has to load the editor… |
| [v0.36.0](https://github.com/BRF-Tech/filex/releases/tag/v0.36.0) | 7 September 2026 | The release that came out of looking at filex the way a stranger arriving from a link would. |
| [v0.35.0](https://github.com/BRF-Tech/filex/releases/tag/v0.35.0) | 7 September 2026 | A security release. On a multi-tenant install the tenant boundary was enforced on the routes that list rows and assumed on the routes that name one: an administrator of any tenant, pointed at another tenant's ids, reached fifteen… |
| [v0.34.2](https://github.com/BRF-Tech/filex/releases/tag/v0.34.2) | 6 September 2026 | Two things that only ever bit installs nobody had configured by hand. filex guesses http://localhost:5212 as its own address when FILEX_PUBLIC_URL is unset, and the realtime ticket handed that guess to the browser as fact -- so… |
| [v0.34.1](https://github.com/BRF-Tech/filex/releases/tag/v0.34.1) | 6 September 2026 | A patch for two things v0.34.0 shipped over. A user who had chosen Turkish saw an English admin panel on any second device: the language stored on the account was written by Profile and never read, so it only ever worked in the… |
| [v0.34.0](https://github.com/BRF-Tech/filex/releases/tag/v0.34.0) | 6 September 2026 | The release where writes stopped being silent. Half of filex's write surfaces - WebDAV, SFTP, FTPS, NFS, the S3 gateway, the AI/MCP API, the archive extractor, the async copy worker and the built-in editor - put a file on the… |
| [v0.33.0](https://github.com/BRF-Tech/filex/releases/tag/v0.33.0) | 5 September 2026 | A release of things that were quietly wrong. A large upload to an S3 endpoint served over plain HTTP died inside the client and the storage sync then moved the file to the trash - reported from the outside, and worth knowing why… |
| [v0.32.0](https://github.com/BRF-Tech/filex/releases/tag/v0.32.0) | 5 September 2026 | The explorer gained a third shape. `uiProfile: 'drive'` lays it out the way someone arriving from Google Drive expects - one **+ New** menu, a single search field across the header with the command palette behind it, Type /… |
| [v0.31.0](https://github.com/BRF-Tech/filex/releases/tag/v0.31.0) | 5 September 2026 | Forgetting the password on an encrypted folder no longer means losing the files. Creating one now shows a recovery key once - filex never stores it, and it opens the folder without the password. |
| [v0.30.1](https://github.com/BRF-Tech/filex/releases/tag/v0.30.1) | 4 September 2026 | A one-line patch. Opening Recent, Starred, Shared or Trash put the raw internal name of the view in the tab - `.shared` instead of Shared. |
| [v0.30.0](https://github.com/BRF-Tech/filex/releases/tag/v0.30.0) | 4 September 2026 | The explorer grew a collapsible navigation panel: Recent, Starred, Shared with me, tags and Trash on the left, with Upload as the primary action. |
| [v0.29.0](https://github.com/BRF-Tech/filex/releases/tag/v0.29.0) | 4 September 2026 | Open an Office document that lives on your own computer in the editor your filex server already runs: the desktop app registers for the usual extensions, so a machine with no Word or Excel installed can still edit one. |
| [v0.28.0](https://github.com/BRF-Tech/filex/releases/tag/v0.28.0) | 3 September 2026 | If you run filex against LDAP or Active Directory, this is the release where that actually works. |
| [v0.27.6](https://github.com/BRF-Tech/filex/releases/tag/v0.27.6) | 1 September 2026 | them with.** `CONTRIBUTING.md` had said `git tag -s` for months while no |
| [v0.27.5](https://github.com/BRF-Tech/filex/releases/tag/v0.27.5) | 1 September 2026 | budget was widened to six attempts a release ago, and a test proves a 503 is |
| [v0.27.4](https://github.com/BRF-Tech/filex/releases/tag/v0.27.4) | 29 August 2026 | image tag empty, which the chart resolves to `.Chart.appVersion` - so |
| [v0.27.3](https://github.com/BRF-Tech/filex/releases/tag/v0.27.3) | 29 August 2026 | finds where a stand-in landed was started *after* `webContents.startDrag()` - |
| [v0.27.2](https://github.com/BRF-Tech/filex/releases/tag/v0.27.2) | 29 August 2026 | reading it.** `Content-Disposition` carried the filename raw, so a name like |
| [v0.27.1](https://github.com/BRF-Tech/filex/releases/tag/v0.27.1) | 29 August 2026 | stand-in by NAME across the local drives, so any file that happened to appear |
| [v0.27.0](https://github.com/BRF-Tech/filex/releases/tag/v0.27.0) | 28 August 2026 | the next now does what it says: the queue carries a destination storage of its |
| [v0.26.1](https://github.com/BRF-Tech/filex/releases/tag/v0.26.1) | 27 August 2026 | endpoint answered bare codes - `{"error":"ticket_expired"}`, |
| [v0.26.0](https://github.com/BRF-Tech/filex/releases/tag/v0.26.0) | 27 August 2026 | agent-facing write surface carried its bytes inside the call - `/api/ai/upload`'s |
| [v0.25.3](https://github.com/BRF-Tech/filex/releases/tag/v0.25.3) | 25 August 2026 | `/d/{token}` drop link came out with an empty `<title>`, an empty heading, an |
| [v0.25.2](https://github.com/BRF-Tech/filex/releases/tag/v0.25.2) | 23 August 2026 | A patch for whoever reads the error tracker. Events forwarded from filex's log used to arrive as a bare message - "thumb generate failed" eleven times with no file and no error to act on. |
| [v0.25.1](https://github.com/BRF-Tech/filex/releases/tag/v0.25.1) | 23 August 2026 | A same-day fix for FTPS: the public host name is looked up when the listener starts, and a single DNS timeout in the container's first seconds used to leave FTPS off for good while everything else reported healthy. |
| [v0.25.0](https://github.com/BRF-Tech/filex/releases/tag/v0.25.0) | 23 August 2026 | Share links now have a ceiling on how long they live, and the admin holds it: a new Protection setting (default seven days) caps every new link and file request - a link created without an expiry gets one, a longer request is… |
| [v0.24.1](https://github.com/BRF-Tech/filex/releases/tag/v0.24.1) | 22 August 2026 | A follow-up to 0.24.0's resumable first run: it now covers uploads too. The engine writes its sync history while it works - every 50 transfers or 15 seconds - instead of only at the very end, so a run interrupted at file 9,000 of… |
| [v0.24.0](https://github.com/BRF-Tech/filex/releases/tag/v0.24.0) | 22 August 2026 | A night of syncing a real 10,000-file tree, and what it taught the engine. Transfers and server listings now run several at a time, so a tree of small files is no longer priced at one round-trip each - the tree that crawled at… |
| [v0.23.0](https://github.com/BRF-Tech/filex/releases/tag/v0.23.0) | 20 August 2026 | The rest of “keep on this computer”, the same day it shipped. Every row now says where it lives - ✓ on this computer, ◐ holding kept items below, ⟳ syncing right now, ☁ online-only - and a strip along the bottom of the window… |
| [v0.22.0](https://github.com/BRF-Tech/filex/releases/tag/v0.22.0) | 20 August 2026 | Folders you also want on the computer are now one right-click away. “Keep on this computer” mirrors a server folder - or a whole storage - under a single filex folder chosen once per account, while everything else stays… |
| [v0.21.6](https://github.com/BRF-Tech/filex/releases/tag/v0.21.6) | 19 August 2026 | Two decisions about noise and blast radius. A demo instance no longer accepts any new storage backend - 0.21.4 stopped it reaching the server's own filesystem, and now the remote drivers go too, because "attach your own bucket"… |
| [v0.21.5](https://github.com/BRF-Tech/filex/releases/tag/v0.21.5) | 19 August 2026 | Plugins no longer outlive filex on Windows. If filex was stopped without a chance to clean up - a crash, a hard kill, a service restart - every plugin it had launched kept running; and since a running plugin holds its own .exe… |
| [v0.21.4](https://github.com/BRF-Tech/filex/releases/tag/v0.21.4) | 19 August 2026 | The other half of the demo hardening in 0.21.3. A demo instance publishes an admin login, and the `local` storage driver means "a path on this host" - so on a demo it was possible to add a storage rooted at /data, /etc or /proc/1… |
| [v0.21.3](https://github.com/BRF-Tech/filex/releases/tag/v0.21.3) | 19 August 2026 | A security default, found by measuring the project's own public demo. A demo instance publishes an admin login - that is what a demo is for - and the plugin API is admin-only, so on a demo "admin-only" means anybody, and… |
| [v0.21.2](https://github.com/BRF-Tech/filex/releases/tag/v0.21.2) | 19 August 2026 | A plugin now has to PROVE what it claims before filex will use it. Every capability a plugin declares is probed - at install against the plugin's own throwaway area, and again when you save a storage on it - and one that fails… |
| [v0.21.1](https://github.com/BRF-Tech/filex/releases/tag/v0.21.1) | 19 August 2026 | Pasting a file into the top level of a storage failed - on every driver, not just the new plugins: the explorer asks for the storage root, and the operations queue read that as "no destination given". |
| [v0.21.0](https://github.com/BRF-Tech/filex/releases/tag/v0.21.0) | 19 August 2026 | filex can now be taught a storage it has never heard of. A plugin is a separate program - in any language - that filex launches (or connects to) and speaks a small HTTP/JSON protocol to; once it is running, its driver appears in… |
| [v0.20.3](https://github.com/BRF-Tech/filex/releases/tag/v0.20.3) | 18 August 2026 | The desktop app now ships for macOS (Apple Silicon) - unsigned but ad-hoc sealed, so first launch is the ordinary Open Anyway step rather than a refusal; auto-update on the Mac waits for a signed build. |
| [v0.20.2](https://github.com/BRF-Tech/filex/releases/tag/v0.20.2) | 17 August 2026 | Packaging only - the server is identical to 0.20.1. It exists because the |
| [v0.20.1](https://github.com/BRF-Tech/filex/releases/tag/v0.20.1) | 17 August 2026 | setting is documented as "the address to advertise for passive connections", |
| [v0.20.0](https://github.com/BRF-Tech/filex/releases/tag/v0.20.0) | 17 August 2026 | filex is now reachable without a browser. It could already use S3, SFTP, FTP and WebDAV as storages; now those clients can point at filex itself - as an S3 endpoint, an SFTP server, an FTPS server and an NFSv3 export, alongside… |
| [v0.19.0](https://github.com/BRF-Tech/filex/releases/tag/v0.19.0) | 14 August 2026 | The desktop app gets a language setting - System, English or Türkçe - where before it followed the operating system and offered nothing to choose. |
| [v0.18.2](https://github.com/BRF-Tech/filex/releases/tag/v0.18.2) | 14 August 2026 | The Windows app installs per-user now, and that is what makes the quiet update in 0.18.1 real: an app under C:Program Files needs administrator rights to replace its own files, so every background update ended in a UAC prompt -… |
| [v0.18.1](https://github.com/BRF-Tech/filex/releases/tag/v0.18.1) | 14 August 2026 | The desktop app updates itself the way it always should have. Downloading was quiet and quitting installed silently, but the tray entry and the Settings button ran the installer with its wizard - so the one visible path through… |
| [v0.18.0](https://github.com/BRF-Tech/filex/releases/tag/v0.18.0) | 14 August 2026 | Share links keep their word, and you get a face. A link capped at three downloads could hand out four: the cap was checked against a counter that was only bumped after the bytes had left, so any request that started while an… |
| [v0.17.1](https://github.com/BRF-Tech/filex/releases/tag/v0.17.1) | 12 August 2026 | A packaging fix, and the first release whose tag matches what ships. The desktop package could be built without the updater inside it: in a pnpm workspace the dependency is a symlink pointing outside the app directory and… |
| [v0.17.0](https://github.com/BRF-Tech/filex/releases/tag/v0.17.0) | 12 August 2026 | The desktop app keeps itself up to date. It checks a static feed on filex.sh (not GitHub - that mirror is private and the provider would need a token inside the app), downloads quietly, and installs when you quit, because an… |
| [v0.16.3](https://github.com/BRF-Tech/filex/releases/tag/v0.16.3) | 12 August 2026 | The tab strip, fixed properly. It was permanent in the desktop app and came and went on the web, because 0.16.0 gave the two surfaces different defaults - this package exists so they are one product, so the default is now the… |
| [v0.16.2](https://github.com/BRF-Tech/filex/releases/tag/v0.16.2) | 12 August 2026 | Two things a share link got wrong. It changed language when you entered its PIN - the gate was English, the page behind it Turkish, and the screen in between managed both at once. |
| [v0.16.1](https://github.com/BRF-Tech/filex/releases/tag/v0.16.1) | 12 August 2026 | Follow-up to 0.16.0: a shared folder's gallery tiles are now rendered when the link is created rather than when the first visitor arrives, so the first open is fast too - which is the open that matters, since whoever creates a… |
| [v0.16.0](https://github.com/BRF-Tech/filex/releases/tag/v0.16.0) | 12 August 2026 | Two things a shared folder was doing the slow way. Its gallery tiles were the original photos - the page asked for a thumbnail and the server streamed the whole file - so a folder of a few dozen photos shipped tens of megabytes… |
| [v0.15.1](https://github.com/BRF-Tech/filex/releases/tag/v0.15.1) | 12 August 2026 | The desktop app's account rail had its two identities the wrong way round. Each row of that rail is a server - a tenant - so it now carries that server's own Branding logo, which is a better label than initials taken from an… |
| [v0.15.0](https://github.com/BRF-Tech/filex/releases/tag/v0.15.0) | 12 August 2026 | Mostly the desktop app, and one thing that was writing to your operating system. "Start when I sign in" registered whichever executable happened to be running the app - which, for anyone who had ever run it from source, was a… |
| [v0.14.0](https://github.com/BRF-Tech/filex/releases/tag/v0.14.0) | 10 August 2026 | The desktop app can reach its own server again. Opening any office document showed "Config fetch 401", starred files and recently-opened were silently empty, and "Open in new tab" did nothing at all - one cause under the first… |
| [v0.13.4](https://github.com/BRF-Tech/filex/releases/tag/v0.13.4) | 10 August 2026 | A fix for a disk that fills up on its own. Uploads larger than 32 MiB are buffered to a temporary file, and those files were never removed - every request answered normally while the disk quietly drained (29 GB in two hours on a… |
| [v0.13.3](https://github.com/BRF-Tech/filex/releases/tag/v0.13.3) | 7 August 2026 | The share button now appears in the desktop app. The explorer has always had one, but it was gated on the Web Share API, which Electron does not ship - so rather than build a second share UI, the app puts a native handler behind… |
| [v0.13.2](https://github.com/BRF-Tech/filex/releases/tag/v0.13.2) | 7 August 2026 | Sixteen components were rendering as raw, unstyled HTML in every embedded surface - the share dialog, the convert dialog, the presence bar and nine file viewers. |
| [v0.13.1](https://github.com/BRF-Tech/filex/releases/tag/v0.13.1) | 7 August 2026 | The explorer's onboarding tour sat on top of the desktop app's Settings panel. The tour attaches to `<body>`, so hiding the explorer left it exactly where it was. |
| [v0.13.0](https://github.com/BRF-Tech/filex/releases/tag/v0.13.0) | 7 August 2026 | The desktop app is a file manager, not an admin console. Signing in used to land you on the server's dashboard because the shell embedded the whole admin SPA; it now shows the file explorer, an account rail down the left, and a… |
| [v0.12.0](https://github.com/BRF-Tech/filex/releases/tag/v0.12.0) | 7 August 2026 | Selective folder sync. A folder on your computer and a folder on a filex server are kept in step in both directions, in the background. |
| [v0.11.0](https://github.com/BRF-Tech/filex/releases/tag/v0.11.0) | 7 August 2026 | The desktop app, for Windows and Linux. It runs the same web UI this repo already ships, and sign-in happens in your browser - the app opens the server's own login page and waits, so installs behind an identity provider (OIDC,… |
| [v0.10.2](https://github.com/BRF-Tech/filex/releases/tag/v0.10.2) | 6 August 2026 | Completes the guard added in 0.10.1: a sweep found four more places that could still write a file onto a folder - archive extraction and creation, the OnlyOffice save-back, and version restore - plus replication, where a… |
| [v0.10.0](https://github.com/BRF-Tech/filex/releases/tag/v0.10.0) | 6 August 2026 | Two explorer changes reported by a deployment whose users mount WebDAV from macOS. Dot-prefixed files can now be shown or hidden and are hidden by default (a Mac leaves `.DS_Store` and `._name` litter in every folder it opens),… |
| [v0.9.0](https://github.com/BRF-Tech/filex/releases/tag/v0.9.0) | 6 August 2026 | Closes the ten items a multi-tenant deployment filed against v0.8.0. The important one is a security fix: a tenant admin could reach every other tenant's storages over WebDAV, because `/dav` does its own Basic authentication and… |
| [v0.8.0](https://github.com/BRF-Tech/filex/releases/tag/v0.8.0) | 29 July 2026 | filex now knows which releases exist, and can install them. What it does is decided by which part of the version moved: a patch applies itself when the policy allows, a minor is announced and applied with one click, a major is… |
| [v0.7.6](https://github.com/BRF-Tech/filex/releases/tag/v0.7.6) | 29 July 2026 | Denials on the AI/MCP surface answer `403` instead of `500`. A `5xx` reads as "server glitch, retry", so agents and HTTP clients were retrying requests that could never succeed while the real cause - a path outside the token's… |
| [v0.7.5](https://github.com/BRF-Tech/filex/releases/tag/v0.7.5) | 19 July 2026 | An internal refactor with no behaviour change: the storage-scoped path hash had been copy-pasted across nine call sites, so the same file could map to different rows if any copy drifted. |
| [v0.7.4](https://github.com/BRF-Tech/filex/releases/tag/v0.7.4) | 18 July 2026 | Two explorer fixes: the trash bin now appears in the secondary pane of split view (the two panes were offset by a row), and tall listings scroll inside their pane instead of scrolling the whole page. |
| [v0.7.3](https://github.com/BRF-Tech/filex/releases/tag/v0.7.3) | 18 July 2026 | Split view's right-click menu now matches the main panel exactly - it was a shorter, separate list missing rename, delete, share, convert and tags. |
| [v0.7.2](https://github.com/BRF-Tech/filex/releases/tag/v0.7.2) | 18 July 2026 | Split-view polish: the main panel's breadcrumb no longer spans the full width, and right-clicking a row in the secondary pane opens a real menu instead of only selecting the row. |
| [v0.7.1](https://github.com/BRF-Tech/filex/releases/tag/v0.7.1) | 18 July 2026 | A round of layout and accessibility fixes for embedders. The explorer no longer overflows its host by 2px (the outer scrollbar that produced in embeds is gone), the toolbar folds overflowing actions into a `⋯` menu instead of… |
| [v0.7.0](https://github.com/BRF-Tech/filex/releases/tag/v0.7.0) | 17 July 2026 | Three additions. **Branding** - a settings-driven identity (name, logo, accent, footer) for the public share, PIN, file-drop and folder-browse pages plus the admin login, with per-tenant overrides. |
| [v0.6.0](https://github.com/BRF-Tech/filex/releases/tag/v0.6.0) | 17 July 2026 | Tabs and split view. Open several locations as tabs, split the active tab into two panes that navigate independently, and drag files between them to move (same storage) or copy (across storages). |
| [v0.5.0](https://github.com/BRF-Tech/filex/releases/tag/v0.5.0) | 17 July 2026 | A large interface release: eight built-in themes with independent light and dark variants, fully rebindable keyboard shortcuts, Quick Look (peek the selected file with Space), an operations centre that collects uploads and… |
| [v0.4.2](https://github.com/BRF-Tech/filex/releases/tag/v0.4.2) | 17 July 2026 | Cleanup release. Moving a folder to trash no longer wedges storage sync (a trashed folder's leftovers could block sync from ever re-creating those names), `versions.keep_n` above 20 works instead of being silently capped, and… |
| [v0.4.1](https://github.com/BRF-Tech/filex/releases/tag/v0.4.1) | 17 July 2026 | Packaging and documentation. Ready-to-submit app-store manifests for Umbrel, CasaOS, Runtipi, Unraid and Portainer, a refreshed Helm chart, and this documentation site. |
| [v0.4.0](https://github.com/BRF-Tech/filex/releases/tag/v0.4.0) | 17 July 2026 | An inspector panel (press `i`) with metadata, version history, effective permissions and share links for the selected item. |
| [v0.3.0](https://github.com/BRF-Tech/filex/releases/tag/v0.3.0) | 17 July 2026 | Connectivity release. A WebDAV server so you can mount filex as a network drive from Windows, Finder, rclone or davfs2 with full RBAC enforcement; a `filex client` CLI against any remote filex; and multiple webhook targets with… |
| [v0.2.0](https://github.com/BRF-Tech/filex/releases/tag/v0.2.0) | 17 July 2026 | Content search. filex now indexes what is inside your files - text, source code, CSV/JSON/YAML, PDF text layers and Office documents - extracted asynchronously into an embedded index, with highlighted snippets in the results. |
| [v0.1.84](https://github.com/BRF-Tech/filex/releases/tag/v0.1.84) | 17 July 2026 | A design pass over the explorer: a command palette (`Ctrl/Cmd+K`), a shortcuts sheet sourced from one registry so it cannot drift, sortable and date-grouped list columns, a density toggle, an undo snackbar for rename/move/trash,… |
| [v0.1.83](https://github.com/BRF-Tech/filex/releases/tag/v0.1.83) | 16 July 2026 | screens, mobile touch): three compounding layout issues fixed. The |
| [v0.1.82](https://github.com/BRF-Tech/filex/releases/tag/v0.1.82) | 10 July 2026 | read-only and no-preview labels) and the presence-bar toggle tooltip now |
| [v0.1.81](https://github.com/BRF-Tech/filex/releases/tag/v0.1.81) | 9 July 2026 | list of usernames (first = default); the audit log, shares (`created_via`) |
| [v0.1.80](https://github.com/BRF-Tech/filex/releases/tag/v0.1.80) | 9 July 2026 | as API calls (bearer/proxy) and rendered from blob object-URLs, so embedded |
| [v0.1.79](https://github.com/BRF-Tech/filex/releases/tag/v0.1.79) | 9 July 2026 | stamp `X-Filex-Presence-Name` (RFC 2047) + `X-Filex-Presence-Key`, spoofing |
| [v0.1.78](https://github.com/BRF-Tech/filex/releases/tag/v0.1.78) | 8 July 2026 | Ws - Resolve embedded confined subscribes to the absolute room + per-client frame paths. |
| [v0.1.77](https://github.com/BRF-Tech/filex/releases/tag/v0.1.77) | 8 July 2026 | Ws - Authorize ticketed subscribes as the ticket's user (RBAC) |
| [v0.1.76](https://github.com/BRF-Tech/filex/releases/tag/v0.1.76) | 8 July 2026 | Ws - Allow ticket-only (embedded/cross-origin) WebSocket connections. |
| [v0.1.75](https://github.com/BRF-Tech/filex/releases/tag/v0.1.75) | 8 July 2026 | Realtime - Embed WS live-collab in the core (ticket auth + polling fallback) |
| [v0.1.74](https://github.com/BRF-Tech/filex/releases/tag/v0.1.74) | 8 July 2026 | updates in the core component (native UI *and* embedded contexts via |
| [v0.1.73](https://github.com/BRF-Tech/filex/releases/tag/v0.1.73) | 7 July 2026 | Share - Add GET /api/files/share list endpoint so the modal lists existing links. |
| [v0.1.72](https://github.com/BRF-Tech/filex/releases/tag/v0.1.72) | 7 July 2026 | Share - Native "Paylaş" (Web Share API) button under the mail row. |
| [v0.1.71](https://github.com/BRF-Tech/filex/releases/tag/v0.1.71) | 7 July 2026 | Explorer - 'folder not found' for dead deep links - phantom prefixes 404, denied dirs render as not-found. |
| [v0.1.70](https://github.com/BRF-Tech/filex/releases/tag/v0.1.70) | 7 July 2026 | Web - Don't double the explorer hash in the login redirect. |
| [v0.1.69](https://github.com/BRF-Tech/filex/releases/tag/v0.1.69) | 6 July 2026 | pasting a link opens that folder, login preserves the hash. |
| [v0.1.68](https://github.com/BRF-Tech/filex/releases/tag/v0.1.68) | 6 July 2026 | redirects.** Measured live (nginx `$upstream_http_set_cookie` vs the |
| [v0.1.67](https://github.com/BRF-Tech/filex/releases/tag/v0.1.67) | 5 July 2026 | Previously the session cookie never set `Secure`, and the OIDC state cookie |
| [v0.1.66](https://github.com/BRF-Tech/filex/releases/tag/v0.1.66) | 5 July 2026 | successful (or failed) IdP round-trip the callback bounced the user to |
| [v0.1.65](https://github.com/BRF-Tech/filex/releases/tag/v0.1.65) | 5 July 2026 | were already cross-built for arm64 (goreleaser), but the container images |
| [v0.1.64](https://github.com/BRF-Tech/filex/releases/tag/v0.1.64) | 5 July 2026 | `adminIDFiltersIn` type and two files had drifted from gofmt, which kept |
| [v0.1.63](https://github.com/BRF-Tech/filex/releases/tag/v0.1.63) | 5 July 2026 | flag on and `oidc` among the auth drivers, the login page starts the OIDC |
| [v0.1.62](https://github.com/BRF-Tech/filex/releases/tag/v0.1.62) | 5 July 2026 | A real zero renders as `0 B` instead of `-`, and rows without a backend |
| [v0.1.61](https://github.com/BRF-Tech/filex/releases/tag/v0.1.61) | 5 July 2026 | tenants: a *provider* = an auth realm (OIDC or local) bound to a host and |
| [v0.1.60](https://github.com/BRF-Tech/filex/releases/tag/v0.1.60) | 5 July 2026 | "last activity" date added in 0.1.59 is derived from descendant file mtimes; |
| [v0.1.59](https://github.com/BRF-Tech/filex/releases/tag/v0.1.59) | 5 July 2026 | added in 0.1.58, each folder's row reports a "last activity" date - the |
| [v0.1.58](https://github.com/BRF-Tech/filex/releases/tag/v0.1.58) | 4 July 2026 | size (the sum of its descendant files). Sizes are computed once at the end of |
| [v0.1.57](https://github.com/BRF-Tech/filex/releases/tag/v0.1.57) | 4 July 2026 | Storage - Connect any external storage from env (sftp/webdav/ftp/s3) + fix JSON port. |

---

<small>Last refreshed 2026-11-20 from 160 published releases.</small>
