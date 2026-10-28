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

::: tip Latest - v0.57.6, 26 October 2026
Three fixes, two of them by @chengjie. A folder shared with a group shows the group in the details panel's people list, by its name, instead of "#undefined" with a question mark (PR #129, by @chengjie). An app with its own interface fills the window below its heading and scrolls inside its own frame, on a phone and in a short window too, instead of sitting in a card that made the whole page scroll (PR #130, by @chengjie). And cancelling a job at the very moment the queue picks it up works the first time, and a job waiting for a storage scan keeps showing why it waits instead of flickering to "Deleting".
:::

```bash
docker pull ghcr.io/brf-tech/filex:slim-v0.57.6
docker pull ghcr.io/brf-tech/filex:full-v0.57.6
```

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

## v0.56.3

<span class="filex-release-date">13 October 2026</span>

df, Explorer and Mountain Duck show the real disk instead of a made-up 1 PB: every surface that is asked how much room there is - SFTP, FTPS, NFS, WebDAV and filex mount - gives the same answer, the storage's own disk (or the storages added up at the root), capped by the account's quota. Indexing a large existing tree no longer eats the server's memory: extracted texts are written to the search index together, and indexing keeps to a memory budget, by default 25 percent of the memory filex was given (FILEX_SEARCH_MEMORY_PERCENT).

## What changed

### Fixed

- **`df` and a mapped drive show the real disk, not 1 PB.** A client that asked
  how much room there was got a made-up figure unless the account had a quota,
  and a different one on every surface: `sftp df` and the Linux `filex mount`
  said 1 PiB with nothing used, NFS 32 TiB, FTPS 1 TiB, the Windows mount 4 TiB
  half full, and WebDAV nothing at all. Every surface now gives one answer:
  inside a storage, the disk it is on (a local disk, an SMB share's volume, an
  SFTP server that answers `statvfs`, a WebDAV server's quota); at the root
  that lists the storages, those storages added up, a disk two of them share
  counted once; either capped by the account's quota. SFTP's `statvfs`, FTPS's
  `AVBL`, NFS's `FSSTAT`, WebDAV's RFC 4331 quota properties (Windows' mapped
  drive) and `filex mount` (through the new `GET /api/files/manager?action=space`)
  all read it. Only a location nothing can measure, such as an S3 bucket, still
  answers a large empty disk
  ([PROTOCOLS.md](./PROTOCOLS.md#free-space)). Reported by @gguglietti
  ([GitHub #116](https://github.com/BRF-Tech/filex/issues/116)).

- **Indexing a large existing tree no longer eats the server's memory.** Every
  extracted text was written to the search index on its own, so the index made
  one segment per file and spent its time merging them: measured on 13,500
  files, about 5 GB resident and 6.5 GB of index files mid-merge after fifteen
  minutes, rising with every file, where the settled index needed a fraction of
  that. On a large RAID volume it took 63 GB and the host with it. Texts that
  are ready together are now written together, and indexing keeps to a memory
  budget: `FILEX_SEARCH_MEMORY_PERCENT` (default 25, at most 50) percent of the
  memory filex was given - the container's limit, or the machine's RAM - above
  which extraction and rebuilds wait for the index to give memory back
  ([SEARCH.md](./SEARCH.md#content-search)). Reported by @gguglietti
  ([GitHub #115](https://github.com/BRF-Tech/filex/issues/115)).

- **A plugin in a long data directory starts.** A storage or app plugin talks
  to filex over a Unix socket under `<data>/plugins/<name>/run/`, and a
  socket's path may not be longer than about 108 characters (104 on macOS):
  with a data directory longer than about 48 characters, such as a container
  volume's host path, the plugin could not open its socket and the log said
  only that it "did not print its handshake line". When the real path is too
  long, filex now reaches the socket through a short private link in the temp
  directory; the socket stays where it was, with its owner and permissions.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0563---2026-10-13)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.56.3) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.56.3`

## v0.56.2

<span class="filex-release-date">12 October 2026</span>

Video, audio and PDF thumbnails read a file on a local storage in place instead of copying it whole to the temp directory first, so a folder of films no longer fills /tmp, and the copies a killed process left behind are removed at startup.

## What changed

### Fixed

- **A video thumbnail no longer copies the whole film first, and no copy
  outlives a restart.** To draw one frame, filex copied every video, audio
  file and PDF whole into the system temp directory before ffmpeg or
  Ghostscript ran: on a folder of films on a CIFS mount, a 7 GB copy within
  17 s of opening the folder and 75% iowait, and a killed process left the
  copy in `/tmp` (14 GB of them on one install). A file on a local storage (a
  disk, or a share mounted into the container) is now handed to the tool in
  place, and ffmpeg reads only the opening seconds. Other storages still
  copy, into `<cache_dir>/.work/`, which filex empties at startup together
  with the copies older versions left in the temp directory
  ([thumbnails.md](./thumbnails.md#how-it-works)). Reported by @eLeKaeMe
  ([GitHub #114](https://github.com/BRF-Tech/filex/issues/114)).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0562---2026-10-12)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.56.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.56.2`

## v0.56.1

<span class="filex-release-date">12 October 2026</span>

Every table in the product lays each row out under its name when its pane is too narrow to be a table - the details panel, the explorer on a phone or in a narrow pane, an admin page on a phone - instead of cutting the values to their minimum width; selection, the right-click menu, Actions, sorting and the column menu work as before. The protocol docs now say whose permissions WebDAV, SFTP, FTPS, NFS and the S3 endpoint share: filex's own grants per person, group and folder, not the disk's POSIX bits or ACLs. For maintainers, a release can be cut off Windows: the Store copy's check waits for a Windows machine, on a person's word.

## What changed

### Changed

- **A release can be cut off Windows: the Store copy's check waits for a
  Windows machine, on a person's word.** `pnpm release` on Linux or macOS
  stopped red at the pretag's Store gate, which installs and activates an
  MSIX and so needs Windows. It now names the gate as deferred and waits for
  `--ack store-deferred`, bound to the release commit; the run's record,
  `--status`, the deploy stage and the ship list the steps a Windows machine
  takes until `--ack store-checked` records that they ran green. The Store
  still gets the bundle the release workflow checks and submits, and on
  Windows the gate runs as before
  ([CONTRIBUTING.md](./CONTRIBUTING.md), Release process, step 6).
- **The docs say which permissions the protocols share.** They are filex's own, per
  person, group and folder; the POSIX bits and ACLs on the disk are not mapped to filex
  users ([PROTOCOLS.md](./PROTOCOLS.md)).

### Fixed

- **A table in a narrow place lays each row out under its name instead of
  cutting it.** In the 300px details panel the e-Signature signer table
  showed State and When at their 56px minimums, as "Invited ..." and
  "until S...". Every table in the product (the one table, `DataTable`) now
  measures its own pane: narrower than the lead and the first three columns
  side by side at their minimums (`TableLayout.stackBelow`), each row is its
  tick, its lead and its Actions on one line and every other value after
  them on one line under the lead that wraps only when it must, each with
  its column's name small in front of it and " · " between them ("Type
  Plain text · Owner You · Size 10 B"), whole - the details panel, the
  explorer's listing on a phone or in a narrow pane, an admin page on a
  phone. Selection, the right-click menu, the Actions control, sorting and
  the column menu work as before, the cells keep their roles and their
  column headers, and a pane that holds the lead and three columns is still
  a table that scrolls sideways to the rest (a wide spreadsheet preview
  stays a table).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0561---2026-10-11)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.56.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.56.1`

## v0.56.0

<span class="filex-release-date">11 October 2026</span>

Apps can edit a file of an end-to-end encrypted folder, or of a vault: the browser decrypts it for an app the person allowed, seals what the app keeps while it edits and encrypts what it saves before it leaves the browser, so the server still holds only ciphertext. Several people can edit one file together through filex's own relay, in ordinary and encrypted folders alike, with watchers and nothing lost to a dropped connection; the office editor app edits Word, Excel and PowerPoint that way with no document server. The Helm chart installs from a Helm repository (helm repo add brf-tech https://helm.brf.sh). The installed web app, its install offer and its notifications wear the instance's own name and logo. Storage plugins and apps get a compatibility window: an old form stays accepted for at least six minor releases or twelve months, so the checksum-only plugin signature 0.55 meant to refuse is still accepted. Fixed: a folder dropped from the desktop arrives as a folder in every browser, an app's interface knows who is looking, and a file of an encrypted folder no longer gives its name away on hover or leaves through a drag out of Cmd+K or Recent.

## What changed

### Added

- **An app edits a file of an end-to-end encrypted folder** (#189). An app
  whose manifest says `"encrypted_folders": {"open": true}` (filex 0.56; it
  needs an interface, a `viewer` view and `files:read`) is granted the
  derived permission `files:e2e-plaintext`, which the install review lists
  in its own words - the app sees every such file opened with it, the server
  still holds only encrypted data - and which is never written into
  `permissions`; an update that starts asking is a new grant, and a module
  app's describe must say the same. With the folder unlocked, **Open** and
  **Open with** offer such an app beside filex's own viewer (the Default
  apps rule for the kind applies; never ONLYOFFICE, never an app without
  the grant). The browser does both halves, the CryptPad way: the app's read
  door answers the ciphertext (`?e2e=1`), the explorer decrypts it and
  transfers the plaintext to the app's sandboxed frame (`FileInfo.plaintext`
  says so; no `blob:` address is made of it), and encrypts what the app
  saves before sending it to the save door (`?e2e=1`), which writes a new
  version only over the file as it was opened (`expect`; `412 changed`
  otherwise) and refuses a body that is not an encrypted file (`422 not_encrypted`). While the frame holds such a file, save-as, `engine.call`
  and `job.submit` answer `unavailable`; **Lock** closes the app. In a vault
  too (its own entry below). Not for a `.fxe`, at most 200 MiB a file
  (`LIMITS.maxEncryptedBytes`). The administrator's switch
  `apps.e2e_plaintext` (on; seeded once from `FILEX_APPS_E2E_PLAINTEXT`;
  `capabilities.apps_e2e_plaintext`) turns it off for every app; every
  handover and save is an audit row (`app_plugin.e2e_read`,
  `app_plugin.e2e_save`). An app's save over a file also takes `expect` and
  answers the version's `modified`
  ([E2E-ENCRYPTION.md → Editing in an app](./E2E-ENCRYPTION.md#editing-in-an-app),
  [APP-PLUGINS-API.md](./APP-PLUGINS-API.md#a-file-of-an-encrypted-folder-in-the-clear-encrypted_folders-056)).
- **In a folder whose names are encrypted too, the browser picks the app**
  (#189). At level 2 the server stores each name as ciphertext and cannot
  tell the kind of a file, so an app was not offered there. Now the explorer
  decrypts the name and chooses the app by its extension - the views granted
  `files:e2e-plaintext` that apply to it, the administrator's rule for the
  kind, the person's choice, filex's own viewer last - with one rule every
  surface uses (core `plaintextOpenOf`); neither the name nor its extension
  is ever sent, and *Choose an app…* offers no "Always use this app" for
  such a file (that choice is kept on the account). The app doors do not ask
  a kind of a stored name that is ciphertext - a save there is held to the
  grant, the encrypted folder, the person's write right, `expect` and
  ciphertext only - and keep holding every name they can read to its kind
  ([E2E-ENCRYPTION.md → The kind is the browser's](./E2E-ENCRYPTION.md#the-kind-is-the-browsers)).
- **What an app keeps while it edits an encrypted file is sealed** (#189).
  `state.set` in an encrypted session goes to a store of its own,
  `appStateE2e` in the account's preferences: one ledger per account, app and
  folder, encrypted in the browser with a key derived from the folder key
  (HKDF, the signed-in account and the app's name in the context and in the
  associated data, so a member's ledger copied into another member's
  preferences does not open), so a "setting" can no longer carry plaintext to
  the server. It is apart from the app's ordinary store; the same limits hold,
  measured on what is stored (the server enforces them on the sealed store
  too, `413 APP_STATE_TOO_LARGE`); a locked folder reads as an empty store
  ([E2E-ENCRYPTION.md → The app's own store, sealed](./E2E-ENCRYPTION.md#the-apps-own-store-sealed)).
- **An app edits a file of a vault** (#189, encryption level 3). The same
  app, granted the same `files:e2e-plaintext`, is offered a file of a vault
  the person has opened and edits it there - the vault's own way, all in the
  browser: the explorer picks the app by the name it read from the vault's
  encrypted index (the server never learns a vault's names), its vault engine
  reads the file from the packs for the app's `file.read` and writes the
  app's `file.save` as the vault's next generation, under the vault's write
  lock, only over the version the app read (`failed` / `changed` otherwise; a
  file that went meanwhile is not made again). One writer at a time: a save
  while somebody else writes the vault is refused in the server's words
  (`failed` / `vault_locked`), and one whose write lock ended before the
  commit writes nothing and says why and what was not saved (`failed` /
  `vault_lock_lost`). When the vault locks - **Lock**, or its own lock after
  15 idle minutes - the app closes with it and the person is told which file
  lost the changes the app had not saved; the app's reads, saves and
  `ui.dirty` count as activity in the vault. A minute before the vault would
  lock itself while the app holds unsaved changes, filex warns above the app
  in the server's words (the vault's `state` answer now carries `words`) and
  offers **Keep it open**. The app's own store there is the sealed one, keyed
  from the vault's own key and the account; where the page cannot tell which
  account it is signed in as, `state.set` now says so instead of calling the
  folder locked (folders too). No server door is used and no
  endpoint was added: the server sees packs and an index, never the file's
  name, its path or that an app opened it, and its app doors keep refusing a
  vault (`403 encrypted`, whose sentence now says where a vault's file is
  opened). The SDK's `FileInfo.plaintext` comes beside `encrypted: "vault"`
  too ([E2E-ENCRYPTION.md → In a vault](./E2E-ENCRYPTION.md#in-a-vault),
  [APP-PLUGINS-API.md](./APP-PLUGINS-API.md#a-file-of-an-encrypted-folder-in-the-clear-encrypted_folders-056)).

- **Editing a file together, through filex's relay** (#189). An app whose
  manifest says `"co_edit": {"open": true}` (filex 0.56; it needs an
  interface, a `viewer` view and `files:read`) is granted the derived
  permission `files:co-edit` - the review says it in its own words, it is
  never written into `permissions` - and edits a file with other people: the
  bridge's `coedit.*` methods 0.55 defined now answer. filex's page seals what the
  app appends with the session's key, the relay (`internal/coedit`) puts the
  sealed entries in one order without reading them, and every member's page
  reads the whole log through the reader that refuses one the writers did
  not write; the app never holds a key or opens a connection. A conflict is
  filex's to retry, the changes lease goes to one writer at a time and only
  to one that has seen every change, a member's cursor reaches the others
  unkept, a save with `through` is recorded in the log, a member whose page
  went away is dropped after 45 s, and a session keeps its unsaved changes
  for the next person who opens the file (30 days). Who joins and whether
  they may write are the server's answer (two people behind one proxy token
  are two members, each request carries the member's token, rights are read
  again while a member is in). The relay's state is in the database
  (migration 00107, compare-and-set per session: several processes on one
  database serve one session), its blobs under `FILEX_COEDIT_DIR`. In an
  ordinary folder the session's key is the server's, kept sealed with
  `FILEX_SECRET_KEY` (without it the app edits alone); in an encrypted
  folder, see the next entry. With the
  office editor app's 0.3.0, editing office documents - alone or together -
  needs **no ONLYOFFICE Document Server**; one you have stays supported and
  optional (filex's office thumbnails and server-side office conversions
  still use it). New: `FILEX_COEDIT`, `FILEX_COEDIT_DIR`,
  `FILEX_COEDIT_MAX_MEMBERS`, `FILEX_COEDIT_MAX_LOG_MB`,
  `FILEX_COEDIT_MAX_BLOB_MB`, `FILEX_COEDIT_UNSAVED_DAYS`; the SDK's
  `CoEditHello.created`, `coedit.dropped`'s `reason` and
  `fx.save(data, {through})`
  ([APP-PLUGINS-API.md → Editing together](./APP-PLUGINS-API.md#editing-together-coedit-056),
  [CONFIGURATION.md → Editing together](./CONFIGURATION.md#editing-together)).
- **Editing together in an encrypted folder, watching, and nothing lost to a
  dropped connection** (#189). A file of an end-to-end encrypted folder is
  edited together by an app also granted `files:e2e-plaintext`: the member
  that starts the session makes its key in the page and gives the server
  only that key sealed with the folder key (`POST …/coedit/{sid}/key`,
  once); a member that joins (`e2e: true`) is handed it as it is
  (`sealed_key`) and opens it with the folder key - the server opens neither
  the key nor anything of the session; not in a vault, not a `.fxe`. A
  person who may only read the file joins as a **watcher** (`canEdit: false`): every change reaches them live, the relay refuses whatever they
  would write, and the audit says `app_plugin.coedit_watch`. Every change is
  kept in the browser's own IndexedDB until the relay placed it (sealed with
  the folder key in an encrypted folder) and a request that got no answer is
  sent again: what a lost connection, a closed tab or a crash left is sent
  when the file is opened again - at once when nobody wrote since, otherwise
  after the person's answer to a question above the editor in the server's
  words (`unsent_words`, English and Turkish); a tab still open keeps its own
  (Web Locks), so none is sent twice. Cursors reach the members connected to
  every filex process: each member's last sealed cursor is a row (migration
  00109, `coedit_cursors`), written at most twice a second, read with the
  log. A save in a session is held to the file the session holds (its base
  or its last save, by any member) instead of the page's `expect`: two
  members' saves no longer refuse each other, and a version written outside
  the session meanwhile is refused (`412 changed`) in an ordinary folder too
  ([APP-PLUGINS-API.md → Editing together](./APP-PLUGINS-API.md#editing-together-coedit-056),
  [E2E-ENCRYPTION.md → Editing in an app](./E2E-ENCRYPTION.md#editing-in-an-app)).

- **The Helm chart installs from a Helm repository.** `helm repo add brf-tech https://helm.brf.sh`, then `helm install filex brf-tech/filex`; the same
  chart is at `oci://ghcr.io/brf-tech/charts/filex`. Every release's chart is
  published there within ten minutes of the release, from 0.55.0 on, by
  [BRF-Tech/helm-charts](https://github.com/BRF-Tech/helm-charts); `--version`
  pins a chart version, and `helm search repo brf-tech/filex --versions` shows
  which filex release each one installs. The chart's source stays in
  `deploy/helm/filex`
  ([INSTALLATION.md → Kubernetes (Helm)](./INSTALLATION.md#kubernetes-helm)).

### Changed

- **A storage plugin build signed over its SHA-256 alone is still accepted:
  plugins and apps get a compatibility window.** 0.55 said this old form would
  be refused from 0.56; it is not. A storage plugin or an app built for a
  release keeps working, unchanged, for at least 6 minor releases or 12 months
  after it, whichever is longer: an old form it sends (a signature format, a
  manifest field, a host function, a bridge call) stays accepted with a warning
  through that window, and is refused only after the CHANGELOG of an earlier
  release has announced it. The old signature form
  (`storage-build-signature-sha256`) is accepted at least until filex 0.61 and
  2027-10-09, whichever comes later; the plugin's row and its log name that
  end ([PLUGINS.md → Compatibility window](./PLUGINS.md#compatibility-window),
  [CONTRIBUTING.md → Compatibility window for plugins and apps](./CONTRIBUTING.md#compatibility-window-for-plugins-and-apps)).
- The co-editing relay prototype (`internal/e2eoffice`, in memory) is
  replaced by `internal/coedit`, which keeps the same rules in the database
  (#189).
- **The README and the first-steps guides are shorter, and start with the
  short path** (#218; a reader on r/selfhosted called the documentation
  "extremely verbose and lengthy"). The README is about 6,200 words instead of
  20,270: each tour part keeps its short version and its screenshots, and the
  long text and the full feature list are one entry per feature on the new
  [FEATURES.md](./FEATURES.md); its five translations follow the new
  structure. [CONFIGURATION.md](./CONFIGURATION.md#quick-start),
  [DESKTOP.md](./DESKTOP.md#in-five-minutes),
  [PROTOCOLS.md](./PROTOCOLS.md#quick-start),
  [ONLYOFFICE.md](./ONLYOFFICE.md#prerequisites) and
  [APP-PLUGINS.md](./APP-PLUGINS.md) open with what to do first; "since
  0.xx" stories leave the guides (the release that made a change is in this
  file) and the reasons and measurements behind them are in
  [CONTRIBUTING.md → Background notes from the user guides](./CONTRIBUTING.md#background-notes-from-the-user-guides).
  The arm64 table moved to [INSTALLATION.md](./INSTALLATION.md#arm-arm64).
  API.md's version note said 0.54, PLUGIN-KIT.md's `engine_ext` still named
  LibreOffice as the office engine: both corrected.
- **`pnpm release` builds its two images from the release commit, in
  minutes** (#165). The pretag's image gates stream `git archive` of the
  commit into `docker build -` instead of building `.`: on a workstation whose
  docker reads the checkout through WSL's 9P bridge, BuildKit's walk of it
  (`.git`, every worktree, the build output) took 30-52 minutes per image at
  0.54.0 and 0.55.0, and 0.55.0's four pretag runs 4.5 hours. The gate first
  checks that `.dockerignore` leaves out no tracked file the Dockerfile copies
  (BuildKit applies none to a tar on stdin), the images start beside the UI
  build instead of after the binary, and each is taken from the gate cache
  while what it is made of is unchanged and its image is still under its
  tag. The pretag goes from 66-74 minutes to about 12-15.
- **The release gate re-runs a part red on the runner, once, by itself, and
  asks GitHub again when it does not answer** (#165). A part whose every
  failed step only sets the machine up (GitHub's setup, a checkout, a
  toolchain, an artifact, the browser install through apt) is re-run with
  `gh run rerun --failed` once the run has finished; a red test never is. A
  5xx, a timeout or a dropped connection is asked again at the next poll
  instead of stopping the gate. The `ci` stage waits for the tag's run to
  finish before it reads what it published, and the export's workflow-guard
  gate is taken from the gate cache on an unchanged tree and workflows
  ([CONTRIBUTING.md → Release process](./CONTRIBUTING.md#release-process)).
- **The `-race` shards are cut from measured times, and none is expected to
  take over 35 minutes** (#174). The longest one is how long CI's full run
  takes, and that run is the release's land → green gate (50 minutes). 0.53's
  split was an estimate: by 0.55 the handlers' rest shard had taken every new
  test file (244 → 437 tests) and ran 53-57 minutes, the run 71-75.
  `scripts/test-times.json` keeps the median seconds of every test file and
  package from the `-race` jobs of five GitHub runs (0.53-0.55);
  `node scripts/test-shards.mjs rebalance --write` cuts the handlers and
  wasmplugin units from it, `times` re-measures from a run's `race-*`
  artifacts, and `estimate` says what each shard will take. Still 14 `-race`
  jobs: wasmplugin runs in three instead of four (`Go (wasmplugin-3-4)` is
  now `Go (wasmplugin-3)`), and a new `auth` shard (the auth packages, and
  the SFTP, FTP and NFS servers) takes what `db-io` (37 minutes) and `rest`
  (41) had too much of. Estimated on GitHub's runner: 26-30 minutes a shard,
  against 22-61 before. `web/tests/deploy/testShards.test.ts` is red when a
  shard is estimated past 35 minutes with the tree as it is, so the rest
  shard's growth shows before a release waits for it. The build host's chain
  reads the same list, with GitHub's seconds scaled to the host
  ([CONTRIBUTING.md → Shards](./CONTRIBUTING.md#shards)).
- **The ship publishes the Helm chart itself and waits for it** (#247).
  `filex-ship.sh` has a `helm` step, waiting only for the preflight (a red
  Releases page does not hold it back): it starts the Helm charts
  repository's publishing (`SHIP_HELM_CMD`, e.g.
  `gh workflow run publish.yml -R BRF-Tech/helm-charts`) and reads the
  repository's index (`SHIP_HELM_INDEX`) every 30 seconds until it lists the
  chart at the release's `appVersion`, for at most `SHIP_HELM_WAIT` minutes
  (15), red after that. An index that already lists the release is green
  without starting anything. That repository publishes on a ten-minute
  schedule, which GitHub had not started for hours, so a release could sit
  without its chart and nobody saw it. Without the settings the step is left
  to a person and listed at the end
  ([CONTRIBUTING.md → The ship](./CONTRIBUTING.md#the-ship-filex-shipsh)).

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0560---2026-10-11).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.56.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.56.0`

## v0.55.0

<span class="filex-release-date">9 October 2026</span>

An app store lists storage plugins beside apps: the same link and review, every build pinned and signed by the store, the plugin checked by the store's validator before it is listed, and a paid one held while its license does not hold. A storage plugin's signature now names the plugin, its version and its platform; the old checksum-only form is still accepted with a warning (0.55 said it would be refused from 0.56; 0.56 withdrew that: it stays accepted at least until 0.61 and 2027-10-09). An app's interface can frame pages of its own package, read blob: addresses and print through filex, which asks every time and opens the print dialog only on the person's Allow - the platform the coming office editor app is built on; where the web component runs on another site, that site needs to be in FILEX_FRAME_ANCESTORS for its apps to print. Every file row says whether it is end-to-end encrypted, and an encrypted file is offered to no app and refused at every app door. A rename, move or delete over WebDAV, SFTP, FTPS, NFS, the S3 gateway or the AI surface no longer loses rows to a storage scan running beside it, and a long one no longer holds up the scan or the queue. Every refusal and update state on the Apps and Storage plugins pages and the app store is the server's sentence, in the reader's language. The snap moves to core24, and every desktop release is tested as an update of the previous one before it ships. Embedders: the 0.55 packages need a 0.55 server. Security hardening across app printing, encrypted files in apps, the store's plugin validator, storage plugin signatures and paid plugin licenses; upgrading is recommended.

## What changed

### Added

- **Storage plugins from an app store** (#215). A store link (or the
  store screen's new **Storage plugins** tab, through a request and its
  approval) installs a storage plugin the store lists: the review reads the
  release's own `filex-storage.json` the store reviewed, holds this server's
  build to the store's pin and says - in the reader's language, from the
  server - that the plugin is a program running with filex's rights outside
  any sandbox, what the store's plugin validator measured and on which
  platform, and whether the build's signature satisfies
  `FILEX_PLUGIN_TRUSTED_KEYS` (refused when it must and cannot); **Install**
  downloads the build, refuses other bytes before anything runs and installs
  it as a binary plugin, which filex starts and probes as any other, keeping
  its repository as its source. A paid storage plugin's license is the
  store's, kept under its own row and checked daily; while it does not hold
  the plugin is **held** (new state: nothing runs, its storages do not open,
  nothing is removed), with **License…** on its row. The plugin list says
  which store a plugin came from (`from_store`, `license`). The store screen's
  row states are the server's answer (`state`), and every refusal of a
  storage link is the server's sentence (`detail.kind: "storage"`). The
  Storage plugins tab has the store screen's own audience; a store lists only
  self-contained builds (`linux/amd64` and `linux/arm64` required, `windows/*`
  and `darwin/*` optional and signed when present). New `filex plugin-validator`
  command: the network-less conformance runner a store uses
  ([PLUGINS.md → Installing from a store](./PLUGINS.md#installing-from-a-store),
  [CLI.md](./CLI.md#filex-plugin-validator---an-app-stores-conformance-runner)).
- **A frame of its own package and `blob:` reads for an app's interface**
  (#189). Two narrow exceptions to an interface's sandbox, for an editor
  that runs in the browser. `ui.frame_package` (permission
  `ui:frame-package`) lets the interface open pages of its own package in
  frames of its own: this version's pages only - `frame-src` and `child-src`
  name the package's path, never `data:`, `blob:`, another app, a page of
  filex or the network - each served under the same policy, as an opaque
  origin of its own, with the script that removes WebRTC; filex's bridge
  still answers only the frame filex drew. Every interface page now carries
  no `frame-ancestors` (it was `*`, which does not match the opaque origin
  of an interface framing its own page - Chromium refused it), and filex's
  security-headers middleware adds none to them. `ui.connect_blob` (permission
  `ui:connect-blob`) puts `blob:` in `connect-src`, so the interface reads a
  `blob:` address it made with `fetch` or `XMLHttpRequest`; nothing else
  opens. Both are off unless the manifest asks, derived like the interface's
  other permissions and part of the module's described interface, shown in
  the install review in the administrator's language, and an update that
  adds one stops at the review. An app that asks needs filex 0.55
  (`"filex": ">=0.55.0"`): an older filex refuses the field. The office
  editor app needs both - ONLYOFFICE's editor opens its page in a frame and
  loads the document from a `blob:` address
  ([APP-PLUGINS.md → What the review shows](./APP-PLUGINS.md#what-the-review-shows),
  [APP-PLUGINS-API.md → An app's own interface](./APP-PLUGINS-API.md#an-apps-own-interface-v4)).
- **Printing for an app's interface: `ui.print`** (#189). A sandboxed
  frame may not open the browser's print dialog, so an interface with the new
  `ui.print` (permission `ui:print`, the same kind as `ui:download` - the
  print dialog can save the PDF) hands filex a PDF and filex prints it from a
  page of its own, `GET /_print/`: its policy frames only the `blob:` PDF it
  makes from the bytes posted to it, and filex's pages frame it by path
  beside `/_appui/` and `/z/` (theirs still frame no `blob:`). filex asks
  every time, and the print dialog opens on the person's click on the
  question's Allow, a button of the print page itself; at most 64 MiB
  (`LIMITS.maxPrintBytes`), `%PDF-` bytes only; `fx.print(name, pdf)` in
  `@brftech/filex-app-ui`. Where the web component runs in another site, that
  site needs to be in `FILEX_FRAME_ANCESTORS` for its apps to print
  ([CONFIGURATION.md → Security headers and framing](./CONFIGURATION.md#security-headers-and-framing)).
  An app that asks needs filex 0.55
  ([APP-PLUGINS-API.md → Printing a PDF](./APP-PLUGINS-API.md#printing-a-pdf-uiprint-055)).
- **An app's interface is told when a file is end-to-end encrypted**
  (#189). Every file row the server answers with says it - the folder
  listing, the explorer's search and `/api/files/search`, Recent, Starred, a
  tag view, Shared with me, a file's own `/api/files/stat` and the id-based
  listing: `encrypted: "folder"` inside an encrypted folder, `"vault"` inside
  a vault, `"file"` for a single encrypted file (`.fxe`), by one server rule -
  and an app's interface reads it as `FileInfo.encrypted`, whichever of those
  views it was opened from; a plain file and a folder row carry no such field.
  The server says it, no client guesses it. It is information: filex 0.55
  decrypts nothing for an app. Shared with me rows also say `e2e_root` now, as
  the other views' rows do, and an encrypted file in any of those views is
  offered no app ("Open with", an app's action) and no ONLYOFFICE, as in its
  folder
  ([APP-PLUGINS-API.md → session.get](./APP-PLUGINS-API.md#which-rows-say-encrypted)).
- **Editing together in the app SDK, defined** (#189). The interface
  bridge knows `coedit.join`, `coedit.subscribe`, `coedit.append`,
  `coedit.lease`, `coedit.cursor`, `coedit.blob.put` / `coedit.blob.get` and
  `coedit.leave`, their events (`coedit.entry`, `coedit.cursor`,
  `coedit.dropped`) and `file.save`'s `through`, so the office editor app can
  be written against them (`fx.coedit` in `@brftech/filex-app-ui`); filex
  0.55 offers no relay yet and answers each `unavailable`
  ([APP-PLUGINS-API.md → Editing together](./APP-PLUGINS-API.md)).
- **The test chain can move what else its host runs out of the way** (#194).
  `CHAIN_PAUSE_CMD` runs once `scripts/chain/run.sh` holds the build lock,
  before the run measures the memory it may take, and when it succeeded
  `CHAIN_RESUME_CMD` runs once when the run ends - green, red or stopped by a
  signal - for a hand run, a CI run and the nightly run alike. A pause that
  fails is logged and the run goes on; `result.json` `host.pause` and the
  morning report say what it did
  ([CONTRIBUTING.md → The whole chain on one Linux host](./CONTRIBUTING.md#the-whole-chain-on-one-linux-host)).

### Changed

- **The snap is built on core24** (#68). The Snap Store package `filex-app`
  moves from the `core20` base to `core24` (Ubuntu 24.04) with Snapcraft's
  GNOME extension, on x64 and arm64: the first install also brings the GNOME
  46 runtime, the graphics libraries and the GTK themes as shared content
  snaps. It stays strictly confined, asks for the same interfaces (no
  `allow-sandbox`) and still opens through X11 (XWayland on a Wayland
  desktop). The desktop packages are built with electron-builder 26 (from
  24), on Node 22; the installers, their names and the update feeds are
  unchanged ([DESKTOP.md → The snap and the sandbox](./DESKTOP.md#the-snap-and-the-sandbox)).
  The `.deb` and the `.rpm` keep electron-builder 24's install script: the
  sandbox helper stays setuid, as every version since 0.50 opened with
  (electron-builder 26's would have swapped it for an AppArmor profile).
  The AppImage keeps electron-builder 24's entry point: 26's adds
  `--no-sandbox` by itself where it cannot create a user namespace, so on
  Ubuntu 23.10 and later without the AppArmor profile the image would have
  opened with Chromium's sandbox off instead of saying what to do
  ([DESKTOP.md → AppImage on recent Ubuntu](./DESKTOP.md#appimage-on-recent-ubuntu)).
- **Every release now updates the previous one before it ships** (#68). On
  real Windows (x64 and arm64) and Ubuntu (x64 and arm64) machines, the
  previous release's installer, `.deb`, AppImage and snap are installed and
  opened, and the new release is installed over them the way the app updates
  itself; it must leave one copy, of the new version, in the same place, with
  the app's data folder the old copy wrote ([DESKTOP.md → Updates](./DESKTOP.md#updates)).
- **The screenshots show 0.55's new screens** (#215, #189). The store scene
  takes the store screen's Storage plugins tab and a storage plugin's store
  review (the review only from a store served over https, as the build
  host's test chain serves it); a new scene takes the question filex asks
  before an app's PDF is printed. Each in English and
  Turkish, light and dark, at 1280 and 390 px, measured for fit. A page can
  name a picture before it is published (`<!-- shot: <name> | <alt> -->`):
  publishing it turns the line into the picture
  ([e2e/shots/README.md](https://github.com/BRF-Tech/filex/blob/main/e2e/shots/README.md#writing-a-scene)).
- **The office editor's page says what the app does on a phone and how it
  prints** (#189): ONLYOFFICE's open-source phone apps only read, so a phone
  opens the document to read and **Edit** opens the folded desktop editor;
  Print goes through filex's `ui.print`
  ([E2E-OFFICE.md](./E2E-OFFICE.md#on-a-phone)).

### Fixed

- **A rename or delete over WebDAV, SFTP, FTPS, NFS, the S3 gateway or the AI
  surface no longer loses rows to a storage scan running beside it** (#201).
  0.54 gave each storage a row gate that the queue and the explorer's rename
  and move hold from the first byte to the last row (#192); the protocol
  servers, the AI and MCP move and delete, the explorer's delete, saving and
  discarding a draft (into the trash, or for good on a storage that keeps
  none), a restore made inside the request, a cross-storage
  move's source delete and the antivirus quarantine made their two steps
  without it. A scan between them could drop a moved row with its shares,
  versions and comments, or a trashed row with the trash entry it could be
  restored from. Every protocol server now goes through one shared frame
  (`protocolsync` `Relocate`, `Discard`, `Purge`), and the other surfaces hold
  the gate the same way ([ARCHITECTURE.md](./ARCHITECTURE.md), "The row
  gate").
- **New document no longer types its suggestion over the name being typed.**
  The dialog fills the name field with the server's first free
  `Untitled.<ext>` once its dry run answers; on a slow server that answer
  could arrive after the first keystrokes and replace them mid-word
  (`Untitled.txtnutes.txt`). A suggestion now only fills a name nobody has
  typed.
- **A file in a vault names its owner, not "System"** (#94). Every row inside
  a vault is the vault folder's - the server knows no file in it and the index
  records no author - and the explorer drew them with no owner, which the
  Owner column says as "System", the word for a file nobody put there through
  filex. The vault's `state` and `create` answers now carry the folder's
  `owner_id`, `owner_name` and `owner_self`, and every row in the vault shows
  them.
- **The trash restore and an app on a read-only storage say the server's
  sentence** (#209). Restoring onto a name that is taken now answers the one
  refusal shape (`409 name_taken` with its sentence in the reader's language;
  `code: EXISTS`, `name` and `path` stay), and the admin Trash page shows the
  server's `message` for every refusal instead of a sentence of its own or the
  code in `error`. The explorer no longer words an app action's refusal
  itself: a read-only storage (`read_only`) and a selection the action does
  not apply to (`422 not_applicable`, whose sentence was English for every
  reader and is now the server's) are said in the server's words.
- **An app's refusals and every app store refusal are said in the server's
  sentence** (#209). An app's run, screen and save doors answered
  `permission_denied`, `encrypted`, `not_found`, `handler_off`, `not_granted`
  and `not_applicable` with an English sentence of their own for every reader
  ("insufficient permission: docs/a.txt", "plugins cannot read files in an
  encrypted folder"); the codes stay and `message` is now the reason in the
  reader's language, and a path outside a folder-limited key is said the same
  way on every door. A store link's refusal (an app's or a language pack's,
  the trust, the connection, a license key) carried English as well, and the
  admin panel threw it away for a copy of its own built from the code; the
  server now writes it, with `detail.kind` and its English detail in
  `detail.reason`, and the panel prints it
  ([API-ERRORS.md](./API-ERRORS.md#app-store-refusals)). A plugin
  request's refusal (the store screen's request, the Requests panel) is the
  server's sentence as well, and the store screen's own copies of "too many
  requests" and "installed already" are gone.
- **The Apps and Storage plugins lists say an update's state in the server's
  sentences** (#209). A newer version that needs another filex, what an
  approval adds, an update undone, no source to check, an installed app
  outside its range and the version kept to go back to were lines the panel
  built from the row's fields (deciding there too which one a row got); the
  list answers them in `update_said`, `compat.message` and `previous.message`,
  in the reader's language and on their clock, and the panel prints them. A
  storage plugin's source that could not be read is said too instead of the
  check's English error, and the Apps tab's header (apps on or off, the
  processor, signed apps only, a development build, the update check and its
  last run) comes from the server as `runtime.said`.
- **The install review says an app's filex range in the server's sentence**
  (#209). A range that leaves this filex out was a sentence the install wizard
  built from `compat.requires` and `compat.filex`; the dry run now answers it
  in `compat.message`, in the reader's language, and the wizard prints it.
- **An app's install, upgrade and update refusals, and the refusals a request
  cannot be read with, are said by the server too** (#209). The install wizard
  and the Apps list built an install refusal's sentence themselves
  (`fetch_failed`, `manifest_invalid`, `permissions_incomplete`,
  `incompatible` …); the server now writes it in `message` in the reader's
  language, with its English in `detail`, and says an update check's stored
  refusal again for whoever reads the list. The app, store, plugin request,
  storage plugin and Default apps doors no longer answer an English phrase or
  Go's error text: a body that is not JSON, no file or too many, a storage
  that is not there, a full job queue, a chunked save's offset, a server fault
  (`internal_error`), the host's own refusals in an app call (busy, timeout,
  out of memory), the session gates, a Default apps rule, a storage plugin's
  failed install and the platform operator's gate on every admin door
  (`supertenant_only`) each carry a code and the server's sentence
  ([API-ERRORS.md](./API-ERRORS.md)). Some codes that were English phrases
  are codes now (`bad json` -> `bad_json`, `too many paths` ->
  `too_many_paths`, `paths are required` -> `paths_required`, `ops queue unavailable` -> `queue_unavailable`, `bad id` -> `bad_id`, a storage
  plugin's install error -> `install_failed` with the reason in `detail`).
- **A store link opened after the session ended comes back to the store page
  after the sign-in** (#199). The panel went to the sign-in page the moment a
  request was refused, while it still believed in the session; the sign-in
  page sent that "signed-in" reader on to Home, whose own refusal named Home
  as the place to come back to, and the link was lost. A refused request now
  asks the server whether the session is over first: if it is, the sign-in
  page names the page you were on, with nothing in between; if it is not (a
  wrong current password, a one-time code), you stay where you are.
- **"Delete permanently" in the Trash no longer closes having deleted
  nothing** (#199). Every listing of the trash cleared the selection, so a
  refresh that arrived while the confirmation was open (a live update, the
  Refresh button) left it nothing to delete. A reload of the trash on screen
  keeps what was selected.
- **The app SDK no longer promises an app the plaintext of an encrypted
  file** (#189). `FileInfo.encrypted` said filex decrypts in the browser and
  encrypts the save; filex 0.55 does neither for an app. The interface now
  gets `readOnly: true` for such a file and its `file.save` is answered
  `read_only` in the frame (`@brftech/filex-app-ui` protocol,
  [APP-PLUGINS-API.md](./APP-PLUGINS-API.md#which-rows-say-encrypted)).
- **A table narrower than its words cuts them cleanly.** A column header too
  narrow for its name ends in an ellipsis instead of stopping mid-letter
  ("Store che"), and a sortable header's tooltip names the column ("Sort by
  Store checks"). A pill in a cell (a request's "Waiting", "Installed", the
  explorer's lock and link badges) keeps its border and dot and shortens its
  words inside it, with the whole text on hover, instead of running out
  through its edge. The frozen Actions column draws its edge while columns
  continue beneath it, so a column cut there reads as one that goes on. One
  fix in the shared table, so every table has it.

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0550---2026-10-09).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.55.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.55.0`

## v0.54.0

<span class="filex-release-date">8 October 2026</span>

The server does the server's work: the rules, numbers and sentences the web app, the desktop app and the packages used to keep copies of now come from the server, so every surface says the same thing in the reader's language, and every refusal carries the server's own sentence next to a stable code. Notifications reach a phone or a browser with filex closed (Web Push, iPhone and iPad from iOS 16.4), with the same kinds, mutes and digest as the bell, and a notification is translated at its last stop, for whoever receives it. The ONLYOFFICE editor opens in the person's language (FILEX_ONLYOFFICE_LANG pins one), the desktop app follows its account's language, and the CLI installs with winget. The vault (encryption level 3) arrives in the explorer and the CLI behind FILEX_E2E_VAULT, off by default. Search, Recent, Starred, Shared with me and tag pages are narrowed and sorted on the server, and every listed link says where it stands. The presigned S3 multipart upload is gone; the staged upload works on every driver. Embedders: the 0.54 packages need a 0.54 server. Security hardening across ONLYOFFICE save callbacks, apps, encrypted folders, share e-mails, quotas and the S3 gateway; upgrading is recommended.

## What changed

> ⚠ **Upgrading to 0.54:**
>
> - **Embedders: update the server with the packages.** `@brftech/filex`,
>   `@brftech/filex-core` and `@brftech/filex-react` 0.54 need a filex 0.54
>   server: which files open for editing, the input limits and the version
>   line come from its capabilities, with no list to fall back on
>   ([API.md](./API.md)).
> - **API clients:** the refusals that put an English sentence in `error` put
>   a code there now, and every refusal carries the server's sentence in
>   `message` - show that ([API-ERRORS.md](./API-ERRORS.md)). The presigned
>   S3 multipart upload (`POST /api/files/upload/init`, `/finalize`, `/abort`)
>   is gone; the staged upload works on every driver
>   ([UPLOADS.md](./UPLOADS.md)). The notification lists no longer read
>   `lang=`: a row comes in the account's language.
> - **Migrations** 00096, 00097, 00098 and 00105 run at the first start (the
>   vault's lock, Web Push devices, a symlink's reason, a webhook's language);
>   nothing to do by hand.
> - **The desktop app has no language of its own:** an install that had
>   pinned one hands it to its account once, if the account had none.

### Added

- **Push notifications while filex is closed** (#191). **Push notifications
  on this device** in user settings → Notifications (a browser tab, the
  installed app, and the app on an iPhone's or iPad's Home Screen, iOS 16.4
  or later) sends what the person's bell tells them to that phone or browser
  with filex closed: the same kinds, the same mutes and the same digest - an
  urgent kind at once, a held kind as its digest - in their language, a tap
  opening what the bell would. Web Push (RFC 8030, 8291, 8292) with a VAPID
  key made at the first start and stored sealed with `FILEX_SECRET_KEY` (no
  key, no push); only the browsers' push services are accepted as endpoints
  (`FILEX_PUSH_HOSTS` adds one), each row is pushed to a device once however
  many servers run, devices are listed and removable, a test push is one
  click, signing out forgets the browser, and **Admin → Notifications → Push
  notifications** rotates the key
  ([NOTIFICATIONS.md → Web Push](./NOTIFICATIONS.md#web-push)).
- **The ONLYOFFICE editor speaks the person's language** (#214,
  [GitHub Discussion #93](https://github.com/BRF-Tech/filex/discussions/93)).
  It opened in English for everybody; the server now chooses its language and
  regional setting (`editorConfig.lang`, `editorConfig.region`) for every
  surface that opens it: the administrator's fixed language, else the one the
  request names, the language on the person's screen (which the viewer sends),
  their account's, `FILEX_DEFAULT_LOCALE`, English - a language pack's
  language too, whenever ONLYOFFICE offers it, and the nearest one it offers
  for a regional tag (`de-AT` → `de`, `zh-HK` → `zh-TW`). **External services
  → ONLYOFFICE → Editor language** sets *Automatic* (the default) or one of
  the editor's 46 languages for everybody, and `FILEX_ONLYOFFICE_LANG` pins
  it like `FILEX_ONLYOFFICE_URL`
  ([ONLYOFFICE.md → The editor's language](./ONLYOFFICE.md#the-editors-language)).
- **The vault, encryption level 3, in the explorer** (#94). Behind the server's
  `FILEX_E2E_VAULT` switch (off by default, `capabilities.e2e_vault`), the
  encrypted-folder dialog offers level 3 for a new folder, with its cost and
  the pack size (4 MiB or 16 MiB). An unlocked vault lists from its encrypted
  index in this tab - the server never hears a path below the vault folder -
  opens and downloads by byte ranges of its packs, and takes the write lock
  at the first change (upload, new folder, rename, move or copy inside the
  vault, delete), committing each change as one generation. A strip says who
  writes and counts down both clocks: the person's idle time (1 to 10
  minutes, a new row in Settings → Preferences), after which the writer goes
  back to read-only, and the 15 minutes after which an unused vault drops
  its keys and asks for the password again. The same code runs in the web
  app, the desktop app and the embeds (`packages/core`, `lib/e2evault/*`,
  `useE2eVault`), held byte for byte to the format's test vectors. An upload
  whose name is taken asks first, in the explorer's "already there" dialog,
  whether it goes up under the free name (a vault keeps no earlier version,
  so nothing is replaced). The folder chooser (Move to, Copy to, an app's
  chooser) says which folder is a vault, offers only that vault for what is
  inside it and no vault for anything else, and nobody sees the vault's
  layout on the storage (`v/`) as folders - the server's listing now marks a
  vault folder (`e2e_vault`) and a listing inside one (`e2e_vault_root`).
  What the Go writer writes the browser reads, and the other way round:
  `testdata/vault-go` and `testdata/vault-web`, frozen fixtures each side opens.
  The vault's engine is loaded with the first vault opened or made, not
  with the explorer, so the main chunk stays within workbox's 2 MiB
  precache limit (`lib/e2evault`, `e2eVaultEngine`; a test walks the static
  import graph).
- **Narrow a search on the server** (#207). `type` (`file`, `dir` or a kind:
  `image`, `spreadsheet`...), `mime`, `modified_after` / `modified_before`,
  `min_size` / `max_size`, `under` / `not_under`, `owner` and `hidden` are
  parameters of `/api/files/search`, the explorer's name search, `/api/ai/search`,
  the MCP `file_search` tool and `filex client search` (one flag each), applied
  to every candidate before the limit counts it; a value the server cannot read
  is a `400 bad_filter`. Search answers carry `total`, and every hit its
  `score` and `kind` ([SEARCH.md → Narrowing a search](./SEARCH.md#narrowing-a-search)).
- **A new link's answer carries its download command** (#210). `POST /api/files/share`, `POST /api/ai/share` and the MCP `file_share` tool return
  `download_command`: the `curl` and PowerShell lines that fetch the link,
  written by the server (`-L` for an S3 redirect, `?zip=wait` for a folder,
  the PIN as `?pin=` on the creator's own answer). The share dialog shows both
  lines and no longer builds a command itself; an agent passes them on
  ([SHARING.md → Command line](./SHARING.md)).
- **The vault's server half, behind a switch** (#94). With
  `FILEX_E2E_VAULT=1` (off by default, and `capabilities.e2e_vault` says
  which) the server serves `/api/files/e2e/vault/*`: a new empty vault made in
  one request (folder, key file, generation 1's index, undone if a step
  fails), its state and a paged listing of packs and index files with the
  server's clock, the write lock (one session at a time, a 60-second lease,
  each person's idle time of 1 to 10 minutes, a break by the folder's owner or
  an administrator), packs stored whole and once, index files committed only
  as the next generation through a temporary file renamed into place and
  abandoned after 60 seconds, and the lock holder's garbage collection that
  never deletes the three newest generations. The lock lives in the database
  (`vault_locks`, migration 00096) with compare-and-set updates, so several
  filex processes on one database agree; its token is kept only as its
  SHA-256. Inside a vault folder only that API writes: the explorer, the
  queue, the agent API and MCP, archives, apps, the document server's save,
  WebDAV, S3, SFTP, FTPS and NFS are refused there (`403 VAULT_PATH`), and
  the key file keeps its vault block and its place (`409 VAULT_KEYFILE`). `vault.create`, `vault.lock`,
  `vault.unlock` and `vault.lock_break` are audited, `vault.generation` and
  `vault.lock` are realtime frames, and the `filexvlt` magic joins the
  content sniff. [BACKEND.md](./BACKEND.md#vault-encryption-level-3),
  [CONFIGURATION.md](./CONFIGURATION.md#end-to-end-encryption-the-vault).
- **Vaults from the command line** (#94): `filex decrypt` reads a vault (level
  3) from a copy or straight from the server (`filex decrypt docs://Kasa`, no
  lock, `--generation N` for an older state still kept), `filex vault mount`
  serves one from a WebDAV server on 127.0.0.1 that the system mounts (net
  use, mount_webdav, gio or davfs2; no FUSE) - the write lock at the first
  change, a commit within 5 seconds of the last write, out after 15 idle
  minutes - and `filex vault prune` collects and repacks. They work against
  a server with the vault API on (`FILEX_E2E_VAULT`).
  [CLI.md](./CLI.md#filex-vault---a-vault-on-a-server).
- **The vault level's format, written down** (#94). Level 3 of end-to-end
  encryption - built in this release, behind `FILEX_E2E_VAULT` (above) - has
  a normative format and protocol,
  [E2E-VAULT-FORMAT.md](./E2E-VAULT-FORMAT.md): equal packs (4 MiB, or
  16 MiB chosen at creation) filled with random bytes, an encrypted index of
  the whole tree padded with Padmé, keys derived from the folder key with
  HKDF so that every key encrypts exactly one plaintext, one writer at a time
  under a lock the server keeps (idle after 3 minutes by default, at most 10),
  garbage collection by the lock holder, and test vectors from an independent
  reference implementation (`backend/internal/e2edecrypt/testdata/gen_vault_vectors.mjs`,
  `node:crypto`) that the browser, the server and the command line are held
  to. The [roadmap](./E2E-ROADMAP.md#3-the-vault-level) records the
  decisions that replaced its open questions. What the first runs of all
  three together settled is in it too: the listing's `e2e_vault` /
  `e2e_vault_root`, an upload whose name is taken asked about first (a vault
  replaces nothing), `release` with `locked_idle` only from a session that
  still holds the write lock, and a repack branch of the test vectors
  (generations 4 to 6) that holds both writers to one repack layout.
- **`access.changed` on the live socket** (#196). When a grant, a role, a
  group, a permission rule, the tenant's encryption policy or an encryption
  approval changes, the server tells the people it can concern -
  `{"type":"access.changed"}` to a grant's or a request's person, a group's
  members, `"scope":"all"` to every socket of the tenant the change was made
  in (never another tenant's), or to every open socket for a change at the
  platform level - and their explorers ask the answers their menus depend on
  again. The frame names no path, no person and no reason; a burst is one frame
  ([REALTIME.md → When access changes](./REALTIME.md#when-access-changes)).
- **Every listed link says where it stands** (#210). `GET /api/shares` and
  `GET /api/admin/shares` give each link `state`: `active`, `expired`,
  `exhausted` (its download, visit or upload cap is used up) or `revoked`.
- **Recent, Starred, Shared with me and a tag page and sort on the server**
  (#207): `offset`, `sort`, `total` and `truncated` on each, `opened_at` /
  `starred_at` on the rows; the explorer says when a view holds more than it
  loaded and loads the rest on request ([BACKEND.md](./BACKEND.md)).
- **The server checks an e-mail address and a username while they are typed**
  (#209). `POST /api/auth/account/check` answers with the save's own rules and
  words, in the reader's language; the profile and the "Add user" form ask it
  instead of a copy of the rules in the browser, which had already drifted (it
  let `a,b@x` and `ada.@x` through). Whether an address is taken is told only
  where the save would tell it ([BACKEND.md](./BACKEND.md#post-apiauthaccountcheck-)).
- **`GET /api/public/strings`** (#210): the public pages' sentences
  (`server.public.*`) in one language, for the JavaScript share and
  file-request pages ([BACKEND.md](./BACKEND.md)).
- **`share_link_max_days` in `GET /api/capabilities`** (#210): the longest
  life a new link made by THIS person may get - the install's ceiling or
  their permission rules' **Maximum share-link lifetime**, whichever is
  shorter.
- **Editing encrypted office documents: the design and a protocol
  prototype** (#189). Nothing offers it yet. The ONLYOFFICE editor would run
  in the browser from its own unchanged files, with only its socket.io client
  replaced by a bridge that answers it the way a Document Server does; what
  the other editors need goes sealed (a session key under the folder key,
  AES-256-GCM, each entry bound to its place in the log and chained to the
  one before) through a filex relay that orders it without reading it, gives
  the right to write changes only to an editor that has every change before
  them, and records who joined, who left and what a save holds. Every bridge
  runs the Document Server's lock rules on the same sequence, so the first
  request for a paragraph or a range wins everywhere. Who saves is the same
  answer in every browser: every 10 minutes while changes are unsaved, on
  Save, and by the last writer to leave; unsaved work waits 30 days; a vault
  edits alone. filex's half of the prototype is the relay
  (`backend/internal/e2eoffice`, in memory, no route) and the keys and the
  log reader (`packages/core/src/lib/e2eoffice.ts`, `e2eofficeSave.ts`). The
  editor's half - the bridge, the socket.io stand-in and the x2t driver - is
  AGPL and is not part of filex: it is the start of an app of its own,
  [filex-office-editor](https://github.com/BRF-Tech/filex-office-editor)
  (AGPL-3.0-or-later, its own repository and versions), that needs no
  Document Server; it has no release yet ([E2E-OFFICE.md](./E2E-OFFICE.md)).

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0540---2026-10-08).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

## Security advisories

- [GHSA-5896-rqg6-cqjr](https://github.com/BRF-Tech/filex/security/advisories/GHSA-5896-rqg6-cqjr) - a forged ONLYOFFICE save callback signed with an editor configuration's token was accepted (Critical, 9.6)
- [GHSA-25hj-4g7f-8c2c](https://github.com/BRF-Tech/filex/security/advisories/GHSA-25hj-4g7f-8c2c) - a non-owner who could write an encrypted folder could delete its key file history (High, 8.1)
- [GHSA-w8mc-47jv-jp74](https://github.com/BRF-Tech/filex/security/advisories/GHSA-w8mc-47jv-jp74) - the share e-mail sent a caller-supplied link under the instance's mail identity (Medium, 4.1)

All three are fixed in 0.54.0; upgrading is recommended.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.54.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.54.0`

## v0.53.0

<span class="filex-release-date">7 October 2026</span>

Replication finally copies: a storage linked to a replication target now writes its files there, each storage into a folder of its own, and an existing one gets its first full copy on its own (before 0.53 it never copied anything, see #91). The admin panel gets one search for pages, settings, people, groups, API keys, apps, storages and files, in English and in the panel's language, on a phone too. Apps can be browsed and requested from an App store screen inside filex and in the desktop app, and the stores an administrator trusts decide what appears there. Notifications can be held and sent as a digest, urgent ones still at once. Multi-tenant mode is a switch in the admin panel that warns before turning off and never deletes a tenant. A document that changes on disk while it is open in ONLYOFFICE is reloaded when it is clean and asked about when it is not, and with FILEX_ONLYOFFICE_FRAME_ORIGIN the editor runs in a frame on the Document Server's own origin. Comments became a permission of their own for API keys (an existing key that should add comments needs comments: rw), no screen uses a native drop-down any more, and a phone is offered to install filex as a web app, whose notifications now reach it while it is open. Security hardening in the normal train: a folder-confined key is held to its folder whatever the request's content type, and storage credentials are no longer sent back to the admin pages.

## What changed

### Added

- **The App store inside filex** (#162). The navigation panel's **Apps → App
  store** opens filex's own page over a trusted store's catalog - read and
  verified on the server from the store's signed index, its icons through
  filex, no frame of the store and no change to the Content-Security-Policy;
  a catalog is kept ten minutes and served marked stale while the store is
  down; the desktop app shows the same screen under the same rule, in a
  window of its own. A person asks for an app with a reason and follows
  **My requests**;
  the request lands on **Install requests** beside an API key's. Approving it
  asks the store for a **fresh** install link and opens the store review -
  the same SHA-256, permission and license steps as any install link - and
  that install closes the request. **Admin → Plugins → Apps → Store screen**
  turns it on, picks the stores and who sees it (everyone, built-in roles or
  groups), per tenant; **Trusted stores → Connect** binds this filex to a
  store with the one-time code its "My instances" page makes (an ed25519 key
  sealed with `FILEX_SECRET_KEY`, every request signed with a timestamp and a
  single-use nonce). Reading a link, trusting, connecting and installing stay
  the platform operator's, signed in to the panel
  ([APP-PLUGINS.md → The store screen](./APP-PLUGINS.md#the-store-screen)).
- **The whole test chain runs on GitHub, split into parts that run side by
  side.** `ci.yml` is a matrix on every push of `main`: the Go suite in the
  shards of `scripts/test-shards.json` beside PostgreSQL, MySQL, Redis and
  Samba and again under `-race`, the migrations on three engines and through
  the CLI (up, three steps down, up), the unit suites in UTC and on
  Istanbul's clock, the typechecks and the docs gates, Cypress, Playwright on
  Chromium, Firefox and WebKit in parts with the Document Server specs again
  with one, the store and S3 lines, both images, and one `All tests (full)`
  job that is green only when every part was. Every part is named by
  `scripts/ci-parts.mjs`, and the release gate reads the run part by part: a
  red part is named at once, and a run without a part is no full matrix.
  A pull request runs the same less `-race`, Firefox, WebKit and the Document
  Server, and a push is never cancelled by the next one
  ([CONTRIBUTING.md → Release process](./CONTRIBUTING.md#release-process)).
- **A tag run promotes what the dry run of its commit built.** The dry run of
  an untagged version is the release candidate: it pushes both images by
  digest with no tag and keeps every desktop row's files with their sums, and
  the tag run checks each image on its own architecture (built from the
  tag's commit, `filex --version` naming it) and each file byte for byte
  before it tags or publishes anything - the images in minutes instead of a
  second build. The release workflow no longer runs the test suite itself:
  the push run of the commit is the test, `verify` asks that it was the full
  matrix, and the dry run packages at once. `-f macos=false` starts a dry run
  without the macOS row while GitHub has no macOS runner.
- **The release can be packaged off GitHub.**
  `node scripts/release/package-local.mjs X.Y.Z [--run [--publish]]` runs the
  release workflow's packaging on the tag's tree from a maintainer's machine -
  goreleaser and the GitHub Release, both images for amd64 and arm64, the
  Windows and Linux desktop packages - and says what it leaves to GitHub
  (macOS, the arm64 snap) and to a person (npm, the stores)
  ([CONTRIBUTING.md → When GitHub Actions is
  down](./CONTRIBUTING.md#when-github-actions-is-down)).
- **The Playwright and Go suites run in parts, side by side.**
  `node e2e/run.mjs local --shard i/N` runs part i of N with Playwright's own
  split - taken after `--grep` and the engines, so the parts add up to the
  whole run - each part on a server, port, output directory and server log of
  its own. The Go tests have one shard list, `scripts/test-shards.json`, for
  every runner: `internal/api/handlers` and `internal/wasmplugin` are cut by
  test file into exact `-run` groups, the other packages run as package lists,
  and a test file or package the list does not name runs in a rest group.
  `node scripts/test-shards.mjs check` holds the list to `go list ./...` and
  `go test -list`, `e2e-check` holds the parts to the whole run, and
  `rebalance` re-cuts a package from the times a run measured
  ([CONTRIBUTING.md → Shards](./CONTRIBUTING.md#shards)).
- **Comments are an API key's own permission** - `comments`, at **Read**
  (`read`) or **Read and write** (`comments:rw`), the first permission a key
  holds at a level. Adding and deleting a comment ask `comments:rw` on every
  door - `/api/files/comments`, `/api/ai/comments` and the MCP
  `file_comment_add` / `file_comment_delete` tools - through one check in the
  handler they all run; reading them asks `read`. A key is minted with it on
  both API keys screens (**Comments: Read / Read and write**, buttons, not a
  list), raised or lowered later with **Edit** on Admin → API / MCP, the
  row's **Comments: allow writing** on the API keys panel, or
  `PATCH /api/tokens/{id}` / `PATCH /api/admin/ai-tokens/{id}`
  `{"permissions": {"comments": "rw"}}` - the verbs never change - and every
  key list answers each key's levels in `permissions`. A key never gives a key
  a level above its own (`403 token_ceiling`). The rule for every such
  permission added from now on is in the code and the docs: each declares its
  default, `read` - existing keys get it with no migration - or none for a
  super-administrator kind, and `tokenperm_test.go` is red for one that does
  not ([RBAC.md → Permissions with a
  level](./RBAC.md#permissions-with-a-level-comments),
  [CONTRIBUTING.md → Adding a permission](./CONTRIBUTING.md#adding-a-permission)).
- **Multi-tenant mode is a switch**: Admin → Multi-tenant mode (System →
  Customization), the platform operator's alone - a tenant's administrator is
  refused the page and its API (`403 supertenant_only`), an API key the change
  (`403 session_required`). `FILEX_MULTI_TENANT` or the config file's
  `multi_tenant`, when set, pin the mode and the switch shows it locked with
  the variable to change instead; unset, the switch decides, off until somebody
  turns it on. A change takes effect when filex is restarted and the page says
  so until then (the mode is handed to the route groups, the sign-in providers
  and the SFTP, FTPS, NFS, WebDAV and S3 servers once, at start). Turning the
  mode off while the install has tenants asks for their number first: they go
  into maintenance mode, nothing is deleted, and turning it back on brings
  every tenant back as it was. Both directions are in the audit log
  (`tenancy.enable`, `tenancy.disable`). `/api/capabilities` says
  `multi_tenant` (the mode in force) on every install, and every screen follows
  it. `GET`/`PUT /api/admin/tenancy`. ([MULTI-TENANCY.md → Mode
  gating](./MULTI-TENANCY.md), [ADMIN-PANEL.md](./ADMIN-PANEL.md))
- **The notification digest - one notification instead of a flood, if you
  want it.** Optional and off out of the box: every kind of notification is
  still told at once, so an upgrade changes nobody's notifications. A kind
  turned off - by an administrator for everybody in their tenant (**Admin →
  Notifications**, which also sets the window, 1-15 minutes, default 1), or by
  a person for themselves (user settings → Notifications, an *Urgent* switch
  beside every kind and one for all the administrator alerts) - is held for
  the window and told in ONE notification that says, folder by folder, what
  changed - "Rapor: 30 files added; Fotoğraflar: 3 files moved to the trash, 1
  comment" - so a folder that receives 30 files in a minute is one badge step,
  one browser pop-up and one desktop toast, not 30. Every event still keeps
  its own row the moment it happens (the history, the admin list, the audit
  log and every webhook are unchanged); a held row is in the person's list,
  read, until the digest carries it. A digest names and counts only what the
  person's own bell shows (tenant and grants), survives a restart (the window
  is in the database) and is never written twice. A file-request owner who
  holds those notices gets one email per window too. Webhooks can subscribe to the new
  `notification.digest` event; only a target that ticks it receives it. API:
  `urgent_overrides` and `digest` on `/api/notifications/settings`,
  `GET`/`PATCH /api/admin/notifications/digest`. Migration 00087
  ([NOTIFICATIONS.md → The digest](./NOTIFICATIONS.md#the-digest)).
- **A search for the whole admin panel** (task #168). The top bar's search
  button opened the file search page, and a phone had no search at all. Now one
  search, beside the menu - **Ctrl+K** / **⌘K** (the explorer's palette key, as
  you have it bound), and a button with a layer over the window on a phone -
  finds a page and its tabs, a single setting (*Trash retention*, *Require
  two-factor authentication*), an installed app and what it does, a person, a
  group, an API key (by its name, never its value), a storage or a share, by
  its name in the interface's language or in English and by its synonyms
  (*LDAP* finds *Identity providers*), with Turkish letters and capitals
  folded. Files come on demand: the first three and a *Search files* row, or
  `file:` for files alone; `user:`, `app:`, `key:`, `group:`, `storage:` and
  `setting:` keep one kind. You find only what you may open: the pages are the
  menu's own, and every record comes from the list its page reads, through the
  same permission - a delegated administrator holding `admin.users` finds
  people and groups, a tenant's administrator their own tenant's. Your recent
  searches are kept on the server (the newest 20, migration 00090), so they
  follow you to another browser; remove one or all of them, and they are never
  in the audit log. New: `GET /api/admin/panel-search`, `GET` · `POST` ·
  `DELETE /api/admin/panel-search/recent`, `DELETE …/recent/{id}`, and
  `?stats=none` on `GET /api/admin/storages`; core exports `PanelSearch`, its
  rules and `foldText` ([ADMIN-PANEL.md → Search](./ADMIN-PANEL.md#search)).
- **CircleCI stands in for GitHub Actions at the release gate.**
  `.circleci/config.yml` runs, on every push of `main`, the Go suite in the
  shards of `scripts/test-shards.json` beside PostgreSQL and MySQL (and red
  unless both engines really ran), the web and package unit suites in UTC,
  the desktop unit tests and the Playwright suite in Chromium, in parts. When
  Actions is down, `pnpm release X.Y.Z --resume --gate circleci` reads that
  workflow on the export commit instead of `ci.yml` and the release dry run,
  runs here the heavy suites CircleCI does not, and records which CI passed
  the commit. Once Actions is back, the tag run publishes that commit: its
  `verify` takes the green CircleCI workflow when GitHub lacks its full
  matrix and dry run, and with no dry run to promote the tag run builds the
  images and every desktop package itself (the public repository needs a
  `CIRCLECI_TOKEN` secret for it).
  `node scripts/test-shards.mjs go --node i/N` gives copy i of a CI job its
  shard, and refuses a copy count that is not the list's
  ([CONTRIBUTING.md → When GitHub Actions is
  down](./CONTRIBUTING.md#when-github-actions-is-down)).
- **The language packs keep up with `main` between releases.**
  `node scripts/langpacks.mjs status` names, for every language pack checkout,
  the strings it does not translate yet, the ones whose English changed since
  the pack was translated (which `pack.mjs sync` used to keep without a word)
  and the ones filex dropped; `todo` writes the translator's worklists, and
  `apply` checks every answer against the language pack validator and the
  fixed names (filex, ONLYOFFICE) before it writes anything, then runs the
  pack's own sync, build and validators and commits locally. `release X.Y.Z`
  is the whole release-day step of a pack: the release's catalogue, one patch
  version up, the README's status block, the validators, a commit, and the
  signed tag and push commands printed, not run
  ([CONTRIBUTING.md → Translations and language packs](./CONTRIBUTING.md#translations-and-language-packs)).
- **The language packs are translated every night, by an agent, on the
  build host.** A timer of its own (`scripts/chain/install-langpacks.sh`
  installs it, the driver's copy and the pack checkouts) starts
  `scripts/langpacks-nightly.mjs run`, which waits for the nightly test run
  to end, fetches `origin/main` into a worktree of its own and, when a pack
  lacks something, gives each pack's worklist to a Claude Code session that
  can only read and edit its own directory of copies (no command, web or MCP
  tool, none of the host user's settings, a HOME of its own) with the
  project's Claude account asked from the work server at every run; only the
  answers are taken from it, refused answers or a red validator go back to it
  once, `apply --commit` commits each pack on the build host, and one
  notification says per language what was translated and what is left.
  Nothing is pushed or tagged there: on release day `node scripts/langpacks.mjs pull` fast-forwards a maintainer's packs to their `nightly` remote,
  `release` refuses a pack not pulled yet and prints the push of the release
  commit back. Worklist items now carry the answer right after the key, and
  every worklist's rules ask for the language's own letters and one term per
  concept
  ([CONTRIBUTING.md → Translations and language packs](./CONTRIBUTING.md#translations-and-language-packs)).
- **The release train's tools** (`scripts/train/`), for the maintainers:
  `pnpm train X.Y.Z` writes the day's release note (the train rule, read from
  CONTRIBUTING; the 10:00 cut; `main` since the last tag up to the cut; what
  came after it; the merge queue; the closing "every task to Done");
  `pnpm merge-queue --queue <note>` merges a train's branches one after another
  with `--no-ff` and their own messages, merges `CHANGELOG.md` conflicts by
  Keep a Changelog section, leaves `[Unreleased]` with each heading once, stops
  for a person on any other conflict and builds and vets the Go module after
  every merge; `bash scripts/train/filex-ship.sh X.Y.Z` takes a green tag run
  to everything read back in one command - backup, the trusted-proxy check,
  the deploy instance by instance with an automatic rollback, both update
  feeds and the CDN purge, the Releases page, docs.filex.sh, npm and the
  embeds, then `pnpm release X.Y.Z --resume --only deploy` - with a log per
  step; and `when-done.mjs` runs a long command and wakes whoever waits for it
  when it ends. Hosts and keys are settings (`scripts/train/train.env.example`),
  never in the repository. ([CONTRIBUTING.md → The release train's
  tools](./CONTRIBUTING.md#the-release-trains-tools))
- **A `.csv` opens with filex on the desktop too** (#151). *Open with filex*
  handles eleven types now: the ten office ones and `.csv`, registered the
  same way on every system (Windows' "Open with" list, the Microsoft Store
  package, `text/csv` in the Linux desktop entry and `xdg-mime`, macOS with
  rank *Alternate*), never taking the type over. With ONLYOFFICE on the server
  it opens in the spreadsheet editor, and a save as CSV - which the server
  writes back in the file's own dialect, the text of every cell nobody changed
  kept - goes over the `.csv` as any save goes over its document; a save that
  comes back as a spreadsheet goes beside it as `<name>.xlsx`
  ([DESKTOP.md → Opening documents from your computer](./DESKTOP.md#opening-documents-from-your-computer)).

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0530---2026-10-07).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.53.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.53.0`

## v0.52.0

<span class="filex-release-date">5 October 2026</span>

LDAP and Active Directory groups reach filex: a filex group can name directory groups and follows them at every sign-in, a directory sync opens accounts, brings the directory's groups in and can switch off people the directory no longer lists, and a group can make its members administrators - contributed by @manjotsc (#90). Apps can now come from a store: a store such as filex Apps (apps.filex.sh) sends you to your own filex with an install link, which opens the same permission review, holds the app to the store's pins and installs nothing until you press Install. A store is trusted once, after you compare its key fingerprints, and a paid app's license is the store's: checked every day, and an app whose license does not hold is held, never removed. A CSV saved from ONLYOFFICE keeps the cells nobody changed (#88), asking what is below a folder no longer reads the whole storage (#89) and the README is a short page with the detail one click away (#87), all contributed by @berkbasarir; following his report (#86), Admin > Roles points out a role an older version's page may have saved without Encrypt, with one click to give it back. The Snap runs inside the snap's strict confinement and needs no snap connect step. Security: an app is now held to a folder-confined API token's folder, and the token learns nothing about what lies outside it.

## What changed

### Added

- **The README in five more languages** - Turkish, German, Spanish, French and
  Simplified Chinese (`README.tr.md`, `README.de.md`, `README.es.md`,
  `README.fr.md`, `README.zh-CN.md`), each linked from a language line under
  the badges of all six. An interface label is written as filex's interface
  shows it in that language - the built-in Turkish catalogue, the German,
  Spanish and French language packs - so a reader finds on screen what the page
  named; there is no Chinese interface yet, so the Chinese page keeps the
  English label and glosses it. Code blocks, commands, link targets and
  screenshots are the English README's, unchanged. Each translation names the
  commit it was made from and says that the English text holds where the two
  differ; the German, Spanish, French and Chinese pages are machine
  translations awaiting review by a native speaker, and say so.
  `scripts/check-links.mjs` and `scripts/check-doc-anchors.mjs` read
  the translations too, so a renamed docs heading names the link to fix in each
  of them ([CONTRIBUTING.md](./CONTRIBUTING.md#docs)). Contributed by Berk
  Başarır ([#84](https://github.com/BRF-Tech/filex/pull/84)).
- **Installing from an app store, and paid apps.** An app store's **Install**
  opens an install link on your filex (`/admin/store-install#store=…&intent=…`)
  that lands on the same install review a repository gets, filled in from the
  store and marked *From store &lt;origin&gt;*; the administrator still decides.
  Before the review opens, filex checks that it trusts the store - the first
  link asks an administrator to compare the store's key fingerprints and trust
  it (trust on first use; or `FILEX_APP_STORE_URLS` + `FILEX_APP_STORE_KEYS`),
  and a store whose keys change is asked about again - that the link is signed
  by one of the store's `index` keys (ed25519 over the sha256 of the canonical
  JSON, the module-signature rule), current and not used here before, and
  that the repository serves exactly the manifest, module, interface and
  permissions the store approved; **Install** reads and checks it all again.
  A link is made for one filex (`filex_origin`, signed) and names the commit
  the store reviewed, which is what filex reads. A store link installs or
  upgrades - from the same store and repository only - never downgrades, and
  the store is told how the link ended. A **paid app**'s license is issued and kept by the store:
  filex keeps the key sealed with `FILEX_SECRET_KEY` (shown by its prefix only,
  never in an answer, a log or the audit log), asks the store at the install
  and every day, and HOLDS the app - installed, nothing removed, nothing run,
  state *Unlicensed* - when the store says revoked, expired, invalid, out of
  seats or for another app, or once the grace the store signed has ended
  without an answer; turning the server's clock back does not stretch a grace.
  Admin → Apps gets *Trusted stores* and, on a paid app's page, *License*
  (status, licensee, seats, dates, key, **Verify now**); a band on every admin
  page names a held app. An app reads its own license's status and dates with
  `fx.license.get()` (`@brftech/filex-app-ui`). `FILEX_APP_GITHUB_RAW_BASE`
  points GitHub installs at a mirror. Migration 00081. ([APP-PLUGINS.md →
  Installing from a store](./APP-PLUGINS.md#installing-from-a-store),
  [APP-PLUGINS-API.md → The store contract](./APP-PLUGINS-API.md#the-store-contract-0520))
- **LDAP groups** ([docs/LDAP.md → Groups](./LDAP.md#groups), migration
  00084). A group can name LDAP / Active Directory groups, by DN or by name,
  beside its SSO groups: every sign-in to the web UI reads the person's
  directory groups (`group_attr`, or a search with `group_filter`) and joins
  and leaves the linked groups as the directory says. A failed group read
  never refuses a sign-in or takes anybody out of a group; the file protocols
  never move memberships. New settings: `group_filter`, `group_base_dn`
  (`FILEX_LDAP_GROUP_*`), also on Admin → Identity providers. Contributed by
  Manjot Singh ([#90](https://github.com/BRF-Tech/filex/pull/90)), with the
  directory sync, several directories, permanent ids, administrators through
  a group, Users, Groups, Add user and Identity providers entries below. The
  tenant boundary and partial-answer rules of directory sync, and the
  session gate on its doors, were added on top of it before the release.
- **LDAP directory sync** ([docs/LDAP.md → Directory sync](./LDAP.md#directory-sync)).
  Admin → Identity providers → an LDAP provider → **Sync now**, and every
  `sync_interval` on its own: filex reads every person the directory lists,
  opens the accounts nobody has signed in to yet (as their first sign-in
  would: the first sign-in rule decides) and brings everyone's LDAP-linked
  group memberships in step; accounts the directory stopped listing lose
  them, and are switched off with `sync_disable_missing`. A search that finds
  nobody changes nothing. It also brings **every directory group in as a
  filex group** (`sync_groups`, on by default; `sync_group_filter` picks
  which), followed by its permanent id (or, with none, its DN): renamed
  with it, and flagged *Removed from LDAP* - never deleted - when it is
  gone. Which groups exist
  is managed on the directory. Each LDAP provider syncs its own directory,
  accounts and groups. New settings `sync_interval`, `sync_filter`,
  `sync_disable_missing`, `sync_groups`, `sync_group_filter`
  (`FILEX_LDAP_SYNC_*`). A run in which an account could not be looked up
  counts nobody as no longer listed, and a person whose entry lost its
  e-mail is still listed by their permanent id; a `group_filter` that names
  people by their sign-in name (`%u`) leaves memberships to the sign-in. On a
  multi-tenant install sync reaches only the accounts the directory already
  holds and those of its own tenant - another account at the same address
  waits for its person's sign-in in their realm - and a tenant's own
  directory opens its groups in that tenant. **Sync now** needs an
  administrator signed in to the panel, not an API key.
- **Each LDAP directory keeps to its own** ([docs/LDAP.md → Several directories](./LDAP.md#several-directories),
  migration 00084). An account belongs to the LDAP provider that made it -
  another never signs it in - and `email_domains` limits a directory to its
  own addresses; the Users page makes no local account at an address a
  directory of that account's tenant owns. An account from before 0.52 with no password here and no
  SSO identity is taken by the first directory that signs it in, as any
  directory could sign it in before; one with a password here is the main
  directory's alone.
- **LDAP: switched off there, switched off here** ([docs/LDAP.md → Directory sync](./LDAP.md#directory-sync),
  migration 00085). Directory sync switches off the account of a person the
  directory has switched off - Active Directory's "account disabled",
  389-ds's `nsAccountLock`, an OpenLDAP password-policy lock with no end -
  administrators included (never the last one), so their sessions, API keys
  and SFTP keys stop with their password. An account with a password of its
  own here is left on, and the report says so. An account sync switched off (this
  way or with `sync_disable_missing`) comes back on when the directory lets
  the person back in; one switched off or on by hand stays as the
  administrator left it. The Users list and a person's page say **Disabled
  by LDAP**.
- **LDAP: people known by their permanent id** ([docs/LDAP.md → Who is who](./LDAP.md#who-is-who),
  migration 00085). A sign-in or sync finds a person by `entryUUID` /
  `objectGUID` before their e-mail: someone whose address changes in the
  directory keeps their account and files, and its e-mail follows; an
  address the directory gives to someone new no longer signs them in to the
  previous owner's account - sign-in is refused and sync lists the problem.
- **A group can make its members administrators** ([docs/GROUPS.md → Administrators](./GROUPS.md#administrators),
  migration 00086). A group's role can be **Administrator (full access)**:
  linked to an LDAP or SSO group, the directory decides who administers
  filex, so the local administrator from setup can go. Its LDAP links are
  full DNs and count only for people of the group's own directory. Leaving
  the group gives back the earlier level; the last administrator of a tenant
  is never demoted; an administrator made by hand is never demoted by a
  group. Only a signed-in full administrator sets one up or changes who is
  in it.
- **Where people come from.** The Users page has a **Source** column -
  Local, SSO, LDAP or Proxy - and a **Groups** column (two groups a row,
  those that give a role or folder access first, then "+N") with a group
  filter; a person's page and a group's member list show the source too. The
  Groups page says whether a group's members are added by hand or come from
  SSO or LDAP, and filters by it.
- **Add user, clearer** - people of an LDAP directory arrive at sign-in or
  with directory sync. The dialog suggests the username and display name
  from the e-mail, says what the role gives, and offers three ways in: set a
  password (generate, show, copy), send an invitation, or no password (an
  SSO account made ahead of its first sign-in, or API keys only). It adds the
  account to groups made here, and has **Create and add another**.
- **Identity providers, one tab per kind.** The page is a set of summary
  cards, one tab per kind of sign-in (LDAP first, Windows and PAM too); a
  card opens the provider's own page - for LDAP its settings in sections
  (Connection, People, Groups, Directory sync) and its sync.

### Changed

- **The Snap runs without Chromium's own sandbox again, inside the snap's
  strict confinement.** 0.50 and 0.51 asked the Snap Store for
  `browser-support` with `allow-sandbox: true` so that Chromium could build its
  sandbox inside the snap. Snapcraft grants that to trusted publishers only,
  never connects it by itself and reviews it by hand: those revisions waited
  in manual review, the stable channel stayed on 0.49, and the newer snap did
  not open until `sudo snap connect filex-app:browser-sandbox` was run. The
  snap now asks for no such permission, as Snapcraft advises for Electron
  apps: the app starts with `--no-sandbox`, and snapd's AppArmor profile,
  seccomp filter and namespaces confine it as a whole. For you: no
  `snap connect` step, and snap updates reach the stable channel by themselves
  again. What it costs: the confinement keeps the app away from the rest of
  the system, but unlike Chromium's sandbox it does not wall the pages the app
  shows off from the app itself. The `.deb`, the `.rpm` and the AppImage keep
  Chromium's sandbox, and the launcher still refuses to start them without it.
  The release checks the snap's confinement now, where it checked the sandbox
  ([DESKTOP.md](./DESKTOP.md#the-snap-and-the-sandbox)).

- **A release is tagged only after GitHub has tested its commit (#76).** `pnpm release` pushes both `main` branches untagged, starts a dry run of `release.yml` on the export commit and waits until it and CI have passed there; only then are the tags made, and the tag run's new `verify` job publishes nothing without those two runs on its commit. A red run spends no version number, a resume never goes back past a pushed tag, and `--resume --only deploy` re-reads the deploy on the tagged commits ([CONTRIBUTING.md → Release process](./CONTRIBUTING.md#release-process)).
- Nothing changes on upgrade: migrations 00084 to 00086 add the LDAP group
  table, empty, a label saying where each account comes from (an account
  with an OIDC subject is SSO, one with a password here Local; one with
  neither has no label yet and takes the label, and for LDAP the directory,
  of its next sign-in),
  the permanent id column and the administrator switch, off.
- The Roles page's introduction says one role per person.

- **The README reads top to bottom as a short page, with the detail one click away.**
  `README.md` is titled `filex` and opens with a three-line description of
  what filex is, three buttons (live demo, quick start, documentation), the
  picture and six cards in place of one long paragraph. *Why filex* is a tour
  of eight parts - the explorer, storage and protocols, sharing and
  protection, people and access, desktop app & CLI, embedding, apps, AI
  agents - each a line that says what it is for, up to four bullets, one
  picture and a block that opens on a click and holds the text that stood
  there before with its screenshots as a gallery, each over its caption. A new
  section, *Coming from Nextcloud, Dropbox or Google Drive*, sets out in one
  table what each of them and File Browser is and what filex is beside it,
  then what filex does not have: no calendar, contacts, mail or chat, no
  Android or iOS app, no placeholder files, no office editor of its own. *Try
  it now* and *Quick start - binary* are one *Quick start*; the documentation
  index, folded by topic, moved above *Features*, which leads with a
  thirteen-row table and keeps its 55 entries in a block that opens on a
  click. *Development* sits under a new *Contributing* section. Sections are
  first-level headings and the parts of the tour second-level, with a line of
  air before each. No picture and no link target was dropped: the container
  and live demo badges became a link and a button, and the CI badge moved to
  *Contributing*. About 2,500 words are in view where 13,900 were. The five
  translations are left whole at the commit they name
  ([CONTRIBUTING.md → Docs](./CONTRIBUTING.md#docs)), and step 1 of the
  release process says where a new surface goes in this layout, so the page
  does not grow back into a wall
  ([CONTRIBUTING.md → Release process](./CONTRIBUTING.md#release-process)).
  Contributed by Berk Başarır
  ([#87](https://github.com/BRF-Tech/filex/pull/87)).

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0520---2026-10-05).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.52.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.52.0`

## v0.51.0

<span class="filex-release-date">4 October 2026</span>

Who may encrypt is now an organisation's decision, contributed by @berkbasarir (#83): the tenant policy (Off, Administrators only, Permitted, or Approval), a new files.encrypt permission and, on multi-tenant installs, a ceiling the platform operator sets per tenant must all say yes. Under Approval people ask with a reason and an administrator approves once, on the new Admin > Encryption page. With ONLYOFFICE configured a .csv opens in its spreadsheet editor and keeps its own delimiter, byte order mark and line ends when it is saved (#81), and an office file in an older format is saved beside the document with the right extension instead of getting newer-format bytes under its old name. The admin panel's navigation is a mega menu: three panels of grouped pages, each two clicks away at the address it always had (#82). An upgrade changes nobody's access: roles and exceptions that could create files can also encrypt. Security: a folder-confined API token is now held to its folder however a request is sent; upgrade if you rely on root: scopes.

## What changed

### Added

- **Who may encrypt is an organisation's decision** ([E2E-ENCRYPTION.md → Who may encrypt](./E2E-ENCRYPTION.md#who-may-encrypt)).
  Three layers must all say yes before anybody starts encrypting:
  - the platform operator's **tenant ceiling** (`e2e_allowed`, Admin → Encryption → Tenants; multi-tenant installs only);
  - the **tenant policy** - Off (administrators included), Administrators only, Permitted (the default: today's behaviour), or **Approval**;
  - the new permission **`files.encrypt`** - per role, per person, per folder. The built-in User role and the Standard and Upload-only presets hold it (a drop-box account could encrypt before, and still can); a Viewer never does.
  - Under **Approval**, **Request encryption…** leaves a request with a reason; the tenant's administrators approve or reject it under Admin → Encryption. An approval is for that person, is used **once**, and lasts **7 days**, and it opens only its own kind, in the folder it was asked for and never in one below: a folder **encrypted where it is**, **one new encrypted folder** directly inside a folder (never spent on a folder that already holds something), or **one new encrypted file** in a folder (a single file's approval is its folder's). The explorer asks for the kind it would use and is answered from the same approvals the create doors spend, so it offers what the server accepts. A request must name a folder or a file that is there (`404 path_missing`), and one person has at most **20** waiting (`429 too_many_pending`). A request nobody answers lapses after 7 days too; an hourly sweep closes both, and answering one that lapsed, or one that was answered already, is `409 not_pending`.
  - "Encrypting" is creating a key file where there is none, or a new `.fxe` - and landing on one of those names by a rename or a move (the explorer and the queue, the agent API, WebDAV, SFTP, FTPS and NFS ask it like a create at the destination), unless it only carries what is encrypted already: a folder of any name, a `.fxe` that stays a `.fxe`, or a key file that stays its own folder's. A `.fxe` given the key file's name, or a key file moved into another folder, is asked. A **copy** of what is encrypted makes a second one and is asked like a create where it lands, whatever its name: a `.fxe` or a key file copied anywhere, and a folder that holds either anywhere below it (asked once, as a new encrypted folder) - the explorer's paste and Copy to, the queue, the agent API's and MCP's `file_copy`, and WebDAV `COPY`. A move or a rename of the same item stays free. A queued rename, move or copy that was free because its item was a folder fails when it runs if a file has taken the folder's place. "Creates" means there is no file at the path: overwriting a key file or a `.fxe` that is there stays free - a password change cannot be told from new key material, so the policy cannot stop the re-keying of what is encrypted already (an honest limit, in the docs) - and a folder with the name does not count as the file. Opening, adding to, re-keying and decrypting what is already encrypted stay free, and so do restores from the trash or a version and the document server's save.
  - Asked on every door: the web and desktop apps, uploads (single and chunked), the text editor, New document and a draft's save, the agent API (upload, move, MCP `file_write` and `file_move`, ShareX, upload tickets, and 0.50's `file_copy`, `archive_create`, `archive_extract` and `app_run`), file requests, archives (a refused member is skipped), apps, WebDAV, SFTP, FTPS, NFS and S3 (over HTTP a refusal is `403 e2e_not_allowed` with a `reason`; an MCP tool answers an error result naming it; the protocols refuse in their own words). On the agent surface an encrypted folder's key file is refused before the rule, `403 RESERVED_NAME` - a folder's too: that surface never writes one. A file request is judged for the link's creator and never spends the creator's approval: under Approval its key file or `.fxe` is refused. When a store failure leaves the rule undecided - a lookup behind the rule, or the storage row, the app's account or the file request's creator a door looks up to ask it - the door fails the write as its own server failure, not as a refusal: the web app's routes and the agent REST API `500`, an MCP tool an error result, an upload ticket and a file request `503 storage_unavailable`, an archive skips the member, WebDAV `500`, S3 `InternalError`, SFTP `SSH_FX_FAILURE`, FTPS `550` saying so, NFS `ACCES` for a create. The rule's own log line, one for each write it could not decide, names the storage, the person and the error, never the path or a member's name; an app job that fails this way is also reported by the queue, which names the output.
  - The new **Admin → Encryption** page holds the policy, the requests and - for the platform operator - every tenant's ceiling. On a multi-tenant install the platform operator sees every tenant's requests there and answers only the platform's own; another tenant's is that tenant's administrators' decision (its row says so, and the API answers `403 not_decidable`). The explorer asks ahead (`POST /api/files/e2e/allowed`, up to 1000 paths, each with the kind it would use; the answer carries the refusing layer in `reasons`) and offers **Create encrypted folder…** and **Encrypt with E2EE…** - or, under Approval, **Request encryption…** - only where it would be allowed; capabilities carry `e2e_policy` for signed-in callers. `filex encrypt` asks the same question before it asks for a password, and says why not in words.
  - **New notifications and audit rows.** `e2e.request_created` (to whoever decides it: a tenant's administrators, as one broadcast the platform operator's bell does not take; the platform's own request to the supertenant's administrators; webhooks get every tenant's, once each) and `e2e.request_decided` (the person who asked); their two switches in a person's notification settings are offered only where they can reach that person - under Approval with the tenant's ceiling on - and greyed elsewhere, also for an administrator whose own tenant is under another policy. Audit: `e2e_request.create|approve|reject|expire|use` - the `use` row names the approval and, in `encrypted`, the folder that was actually encrypted - `e2e_policy.update` and `e2e_tenant.update`.
  - **Changing who may encrypt is a signed-in administrator's:** an API key gets `403 session_required` on `/api/admin/e2e`, on a tenant's ceiling, on the decision of a request, and on the `e2e.policy` setting through `/api/admin/settings`, `/api/ai/admin/settings` and the MCP settings tools (a session's write there is audited as `e2e_policy.update`, like the page's own). A tenant's policy and the operator's ceiling are written one column at a time, so neither save can undo the other; saving the value already stored writes nothing; the ceiling answers `409 single_tenant` on a single-tenant install.
  - Migration `00080` adds `providers.e2e_allowed`, `providers.e2e_policy` and the `e2e_requests` table. A single-tenant install keeps its policy in the `e2e.policy` setting; when it turns multi-tenant mode on, the first start copies that setting to the platform's own tenant once (logged and audited), so an install that had switched encryption off does not find it on again.
  - Contributed by Berk Başarır ([#83](https://github.com/BRF-Tech/filex/pull/83)). The copy rule, the separate approval kinds, the operator's role, the request limits, the policy's carry into multi-tenant mode and the CLI's question were added on top of it before the release.
- **A `.csv` opens in ONLYOFFICE** ([ONLYOFFICE.md → CSV files](./ONLYOFFICE.md#csv-files), [GitHub #81](https://github.com/BRF-Tech/filex/issues/81)).
  With ONLYOFFICE configured, a CSV opens in its spreadsheet editor and is edited there; without it, in filex's read-only table, as before. It is the product's default, not a setting each install makes, and only `.csv`.
  - **A choice like any other.** ONLYOFFICE is an open handler of `.csv` (`onlyoffice`): *Open with* lists it beside filex's table, **Choose an app…** and *Always use this app* keep it on the account (`PUT /api/me/open-with/csv`), and Admin → Plugins → Default apps lists `.csv` with ONLYOFFICE first while it is configured (`PUT /api/admin/file-types/csv`). Switched off, a `.csv` opens in the table without an error and a choice or rule that names ONLYOFFICE is kept for when it is back; an administrator sees *Open with ONLYOFFICE* greyed with where to set it up.
  - **Opened without the "Choose CSV options" question.** filex reads the file's first 64 KiB and passes its delimiter (comma, semicolon, tab or `|`) and UTF-8 to the editor. A file that is not UTF-8 gets ONLYOFFICE's question, rather than a guess.
  - **Saved as the same kind of CSV.** ONLYOFFICE writes an edited CSV comma-separated with a UTF-8 byte order mark whatever it was (measured on Docs 9.4); filex puts the file's own delimiter, byte order mark (or none) and line ends back before it writes it. A document server set to `assemblyFormatAsOrigin: false` saves it as XLSX: filex converts that back to CSV through the document server and never writes XLSX bytes under a `.csv` name. Anything else is not written: the file stays as it was, the log says why, the people who edited it get a bell notice in their language (`file.upload_failed`) and the audit log a row (`file.office_save_refused`).
  - **What CSV cannot keep is said before it is lost:** a line under the viewer's bar, in the person's language, while a CSV is open for editing (only the active sheet's values; not formatting, formulas or other sheets).

### Changed

- **The admin panel's navigation is a mega menu** ([ADMIN-PANEL.md](./ADMIN-PANEL.md),
  [#82](https://github.com/BRF-Tech/filex/issues/82)). The long sidebar is gone:
  the top bar holds the **Dashboard** link and three panels - **Files & storage**,
  **People & security** and **System** - each laid out in sections (Files, Apps,
  Storage; People & access, Security; Plugins & integrations, Customization,
  Maintenance & records), with a short line under every page. Every page is two
  clicks away and keeps its address, so links and bookmarks still work, and who
  is offered which page is unchanged. A panel opens on a click or a key, never on
  hover; with the keyboard, ↓ enters a panel, ↑ / ↓ / Home / End move inside it,
  ← / → move along the bar and Esc goes back to the button; a screen reader hears
  a named landmark, which panel each button opens and whether it is open, lists
  labelled by their headings and the current page. Below 1024 pixels a **Menu**
  button opens a drawer with the same pages as a headed list, every section open.
  The menu is one component in the core package (`MegaMenu`); the grouping is one
  list in the admin app (`web/src/lib/adminNav.ts`).
- The trail under the bar names the menu section between the dashboard and the
  page (*Dashboard › People & access › Users*), as words rather than a link.
- **Auth providers** is called **Identity providers** in English, as the guides
  already called it (the Turkish was already *Kimlik sağlayıcılar*), and the
  menu's **Search** is **Search index**, the page's own title.
- **An upgrade still changes nobody's access:** on first start, every saved role
  (the built-in User role, and each custom role and its folder parts) that holds
  `files.create` also gets `files.encrypt`, and so does every person whose own
  exceptions allow `files.create` - creating a key file or a `.fxe` needed only
  `files.create` before. A rename, a move or a copy onto one of those names now
  needs `files.encrypt` beside its own permission, so a role that may rename or
  move but not create cannot give a file such a name any more. The catalogue a server has seen is kept in the
  `permissions.catalogue` setting, so a permission added later is merged once and
  never again; the setting only ever grows, so a version started after a later
  one does not make the later one's permissions new again.
- ⚠ **Rolling back to a version without `files.encrypt`** (0.50 or older): the
  older Roles and People pages cannot save a custom role's list or a person's
  exceptions the upgrade gave `files.encrypt` to (`400`, an unknown permission)
  until the server is upgraded again. Two saves go through without a word and
  leave `files.encrypt` out: the built-in User role (the older page does not
  show the permission and writes the list back without it) and a custom role's
  folder part when the role's own list does not hold `files.encrypt`; applying
  a preset to a custom role on the older page does the same. Upgrading again
  does not give it back to those, nor where an administrator took it away in
  between: it was merged once, at the first upgrade. After upgrading again,
  check that the User role and those folder parts still allow encrypting
  ([PERMISSIONS.md](./PERMISSIONS.md)). The release after 0.51.0 points them
  out on Admin → Roles, with one click to give `files.encrypt` back or to
  dismiss it as on purpose.
- A refused encryption in the explorer is said in words (the server's reason)
  instead of "could not create the encrypted folder", and a folder made before
  its key file was refused is listed at once.
- ⚠ **File request links whose creator is not on record** - the creator was
  deleted before 0.49.0, so the link names nobody - keep taking ordinary files
  but no longer take an encrypted folder's key file or a `.fxe`: a file request
  is judged for its creator, and there is nobody to judge. Make a new link if
  such a drop is needed.
- ⚠ **MySQL: migration `00080` makes two columns byte-exact.** The new
  `e2e_requests.path` is `utf8mb4_0900_bin` (the collation `00041` chose for
  names and paths), so an approval for `Muhasebe` is not one for `muhasebe`. And
  `settings.setting_key`, which `00001` made case- and accent-insensitive, is
  altered to the same collation, so that no spelling of a setting key reaches
  another key's row: the settings API guards `e2e.policy` (a session, one of the
  four values, an audit row) by comparing the key exactly, and on MySQL
  `E2E.POLICY` or `e2é.policy` would have passed that guard and still read and
  written the `e2e.policy` row. The `ALTER TABLE` rebuilds `settings`, a small
  table, and cannot fail on existing data (keys that were unique ignoring case
  and accent are unique byte for byte). filex's own keys are lower-case and
  are read in the spelling they were written in; only something that leaned on
  MySQL matching a key by its case or accent (a hand-written row, a script)
  stops matching. **Rolling back** restores the old collation and stops with
  error 1062 if two keys that differ only by case or accent were written after
  the upgrade; rename or remove one of each first. SQLite and PostgreSQL
  already compared keys exactly.

- **The repository no longer describes a maintainer's machine.** The README
  and the API reference show the first-run admin password as `<printed once>`
  instead of an example that looked real, the end-to-end tests look for the
  local language packs in a sibling checkout (`../filex-lang-es`) instead of
  at a fixed drive path, comments no longer name the project's own machines,
  and a unit test refuses a drive-letter path in any file the export publishes.
- **The release's macOS jobs run on `macos-15`.** GitHub retires its macOS 14
  images on 2026-11-02. The runner stays pinned, never `macos-latest`: its
  architecture is the package's (arm64).

### Fixed

- **A choice made just before the tab closes is no longer lost.** Preferences are sent 400 ms after the last change, and anything still waiting when the page was hidden or closed never left the browser - most visibly an app interface's `state.set`, which the next open read back as the older value. The waiting document is now sent at once on `pagehide` and when the page turns hidden, with `keepalive`, from the same shared code on every surface.
- **An office file in an older format no longer gets newer-format bytes under its old name** ([ONLYOFFICE.md → A save in another format](./ONLYOFFICE.md#a-save-in-another-format)).
  ONLYOFFICE writes no Word 97, Excel 97 or PowerPoint 97 file: an edited `.doc` comes back as DOCX and a `.xls` as XLSX (measured on Docs 9.4 with its defaults), and filex wrote those bytes over the old file. Now the old file is not touched: the edit is saved beside it under the format's extension (`rapor.doc` stays, `rapor.docx` is the edit; `rapor (2).docx` when that name is taken), the people who edited it are told in their language, and the audit log writes `file.office_saved_beside`. The new file is created only for an editor of that session who may create it in that folder (their account, their tenant, `files.create` on the new name); when nobody may, nothing is written and they are told to download the edit from ONLYOFFICE before closing. A save that is not one of the document formats, or not the package it says it is, is not written at all (`file.office_save_refused`).
- The admin panel's menu button on a phone was announced as "Dashboard"; it is
  **Menu** now, and says whether the drawer is open. While closed, the drawer is
  out of the tab order (its links could be reached with Tab off screen).
- The trail on a file's versions page read *Dashboard › admin-files › Versions*;
  the middle step is *File history*, the menu's name for the page.
- The Audit page names `e2e.folder_cleanup`, `e2e.fxe_header_rewritten` and
  `e2e.key_file_rewritten` - written since 0.48.0 - in every language; they
  showed as their raw ids.
- **An app's interface can save a new file.** `file.saveAs` (`fx.saveAs` in
  the SDK) answered `unavailable` everywhere since app interfaces arrived in
  0.48.1: no screen that draws an interface gave it the folder dialog the
  documentation promised. Now filex asks in its own folder dialog - the one
  *Move to…* uses, titled with the app's name and the file's, opened in the
  file's folder - in the viewer and in an app's dialog, page and details
  section alike, in the web app, the desktop app and an embed
  ([APP-PLUGINS-API.md](./APP-PLUGINS-API.md#the-interface-bridge)).

- **Every Go test runs from the backend module alone (#141).** A thumbnail
  test read its clip from `e2e/fixtures`, outside the module the release tool
  copies, and the public export's Go gate failed on it with "no such file". The
  test makes its clip with ffmpeg now, and a web test fails when a Go test
  reaches for a file outside `backend/`.

- **The release's export no longer stops on build output it does not
  publish.** Its check for a private name, a server address or a credential
  read every file in the public checkout, ignored ones included, and at 0.50
  stopped half-way on the previous release's UI left in `backend/embed/web`.
  It now reads only the files the export publishes, and empties the two embed
  directories the release fills with its own UI.

- **The release's browser suite installs the echo test app it just built.** On
  Windows the `go: echo.wasm fixture` gate built the module only in its WSL
  mirror, so the checkout's copy, the one Playwright installs, could predate
  the change a spec tested (the v0.50.0 release check lost an hour to it). The
  gate now builds in the Go module and writes the result back into the
  checkout, and the specs that install the app fail on a module older than its
  sources, naming `bash scripts/build-wasm-fixture.sh`, instead of running it.

- **The folder dialog's first breadcrumb shows its whole focus ring.** The breadcrumb line scrolls sideways, and a scroller clips its children on both axes, so the browser's default outside ring lost its left, top and bottom edge when the first crumb was focused (all three engines). The ring is now drawn inside the crumb, like the list rows'.

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0510---2026-10-04).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.51.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.51.0`

## v0.50.0

<span class="filex-release-date">3 October 2026</span>

Office documents open, convert and get their thumbnails through the connected ONLYOFFICE Document Server: LibreOffice is gone from every image and from filex, and the Test on the ONLYOFFICE card now downloads the way a document does. Groups, contributed by @manjotsc (#78): named sets of people with folder access and a role, kept in step with the groups a sign-in carries. People can sign in with the server's own Windows or Linux (PAM) accounts, wrong passwords are counted per account, and FILEX_TRUSTED_PROXIES names the proxies whose forwarded address is believed. Multi-tenant installs gain tenant realms, sign-in providers bound to tenants and self-service for a tenant's own domain; an OIDC sign-in finds its account by its SSO identity inside its own tenant, and a tenant's administrator can no longer change the built-in roles, found and fixed by @berkbasarir (#77). Also: SVG, HEIC, text, archive and folder thumbnails, Default apps per file type, e-Signature for PDFs only, and Chromium's sandbox required by every Linux desktop package. Read the upgrade notes first: LDAP accounts, realms, provider bindings and SSO trust change on upgrade.

## What changed

> ⚠ **Desktop app on Linux: Chromium's sandbox is no longer optional**
> (Security). On Ubuntu 23.10 and later (24.04 LTS included) an
> AppImage needs a one-time AppArmor profile before it opens, and says so when
> it is missing - including an AppImage added to the menu by AppImageLauncher,
> Gear Lever or appimaged, whose entry used to turn the sandbox off. The Snap
> asks snapd for the sandbox: until the Snap Store connects that by itself
> (it reviews the permission by hand, so the new revision can wait for the
> review before it reaches stable), run
> `sudo snap connect filex-app:browser-sandbox` once; the app says so too. The
> `.deb` and the `.rpm` need nothing
> ([DESKTOP.md](./DESKTOP.md#appimage-on-recent-ubuntu)).
>
> ⚠ **LDAP: an entry with no e-mail attribute is `name@local` now**
> (Changed). An account an older filex opened under the bare name
> (`alex`) is adopted at that person's next sign-in - web or file protocol -
> and becomes `alex@local` (`alex@<realm>.local` in a tenant's realm on a
> multi-tenant install): the same account, its files, shares, permissions,
> role and quota unchanged, with an audit row `auth.account_adopted`. A local
> password on it is kept; an account bound to SSO is signed in to as it is,
> not renamed. Nobody is refused and nothing is left to do by hand. Set
> `FILEX_OS_LOGIN_EMAIL_TOKEN` **before** upgrading if `local` is not the
> token you want - it cannot be changed later. On a multi-tenant install only
> an account in the sign-in's own tenant is adopted: one in another tenant is
> left alone and the platform operator is told once
> (`ldap_legacy_account_elsewhere`), so re-home the directory accounts an older
> build left in the supertenant before upgrading. A file-protocol sign-in says
> which tenant it is for by its address or by `realm/name`; one that names
> nothing is the platform's own and adopts nothing (or pin `provider`)
> ([LDAP.md](./LDAP.md#accounts-an-older-filex-opened-under-the-bare-name)).
>
> ⚠ **Multi-tenant: on the platform's address an empty realm is the platform's
> own tenant** (Added, tenant realms). A tenant's people who used to
> sign in there with their e-mail alone type their tenant's **realm** in the new
> Realm field - or sign in on their tenant's own address, where the field is
> filled in for them. Over SFTP (and FTPS with no SNI, and WebDAV on the
> platform's address) they write `realm/name`.
> Existing tenants' realms are their slugs; an API token or an SSH key needs no
> realm. Single-tenant installs are unaffected.
>
> ⚠ **Multi-tenant on SQLite or PostgreSQL: two tenants whose slugs differ only
> in case stop the upgrade.** Migration 00073 gives every tenant the realm
> `LOWER(slug)` under a unique index, so `Acme` and `acme` cannot both have
> one: the migration fails and the server does not start until one of them is
> renamed (MySQL's default collation never let such a pair exist). Look
> before upgrading, and change one slug while still on 0.49:
> `SELECT LOWER(slug), COUNT(*) FROM providers GROUP BY LOWER(slug) > HAVING COUNT(*) > 1;`. The realm is copied once and never changes after
> that
> ([MULTI-TENANCY.md → Realms](./MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)).
>
> ⚠ **Multi-tenant: sign-in providers are bound to tenants now**
> (Added, tenant self-service). The upgrade binds **every** provider
> that exists (OIDC, LDAP, the proxy header, Windows, Linux PAM, environment
> and page alike) to **every** tenant that exists, which is what happened
> before: one directory served every tenant. **Review the bindings** on Admin →
> Identity providers (the page says so until you have) and remove the tenants
> a provider should not serve. A provider added after the upgrade serves the
> platform's own tenant only, and a tenant created after it has no shared
> provider until one is bound. `FILEX_LDAP_PROVIDER` / `FILEX_HEADER_PROVIDER`
> keep working and count as a binding to the pinned tenant. A tenant's own
> OIDC on its provider row is unchanged. Single-tenant installs read no
> bindings ([TENANT-ADMIN.md](./TENANT-ADMIN.md#moving-existing-installs-over-nothing-breaks-on-upgrade)).
>
> ⚠ **SSO: which account a sign-in opens** (Security). An OIDC
> sign-in finds its account inside the tenant it is for only, by the SSO
> identity the account is bound to (`iss` + `sub`), and by the email address
> only while the account is bound to none - and then only when the provider
> says `email_verified: true` or its addresses are trusted. On upgrade:
> - **Every OIDC provider that exists keeps its old behaviour**: its new
>   setting **Trust this provider's email addresses** comes on, and its card
>   says the upgrade set it. Switch it off on a provider that sends
>   `email_verified`. A provider added after the upgrade starts with it off.
>   The environment's OIDC follows `FILEX_OIDC_TRUST_EMAIL`; unset, it trusts
>   when the database had accounts that came through SSO (decided once, at
>   the first start of 0.50), and not on a fresh install.
> - With trust off, an unverified address opens no existing account that is
>   bound to no identity yet (`email_unverified`), and a new account for it is
>   opened **switched off** (`account_pending`): switching it on in Admin →
>   Users approves it.
> - An account is bound to the identity it first signs in with. **Changing
>   the identity provider, or its issuer address (a renamed Keycloak realm),
>   refuses its accounts** (`identity_mismatch`) until each account's SSO
>   bind is removed (Admin → Users → Remove SSO bind, `sso_unlink`).
> - Multi-tenant: an SSO callback that comes back without its flow cookie is
>   refused (`expired`); a sign-in in flight during the upgrade starts again.
>   A tenant's account signing in through OIDC in maintenance mode gets the
>   generic answer now, not `maintenance`.
> ([SSO.md](./SSO.md#which-account-an-sso-sign-in-opens))
>
> ⚠ **A forwarded client address is believed only from a trusted proxy now**
> (Changed, `FILEX_TRUSTED_PROXIES`). Up to 0.49 every
> `X-Forwarded-For` was believed, whoever wrote it. The default is `auto`,
> worked out from where filex runs: this machine (loopback) and, in a
> container on a network of its own (Docker, Podman, a Kubernetes pod), the
> other containers on that network - never the network's gateway, never
> filex's own address, never the LAN, never a macvlan or ipvlan network. A
> proxy container next to filex, and a proxy on the same machine as a plain
> install, need nothing. **A proxy on the host in front of filex's container**
> (nginx reaching a port published on `127.0.0.1` arrives from the Docker
> gateway), **a proxy on another machine**, an ingress controller on another
> Kubernetes node, a proxy that reaches filex over `100.64.0.0/10` (Tailscale,
> Cloudflare WARP) and a CDN in front of your proxy must be listed, keeping
> the word - `FILEX_TRUSTED_PROXIES=auto, 172.18.0.1`. Until they are, every
> visitor resolves to the proxy's address: the access log, the audit log, the
> file-request limit and the new sign-in limit all see that one address, and
> one visitor's wrong passwords lock out everybody who arrives through it.
> **Admin → Sign-in security** names such a proxy - a peer that sends
> forwarded addresses without being trusted - and adds it in one click; the
> log names it too. Helm: list the cluster's pod network
> (`trustedProxies: "auto, 10.244.0.0/16"`). Clients on a LAN that reach
> filex with no proxy in front need nothing set
> ([CONFIGURATION.md → Trusted proxies](./CONFIGURATION.md#trusted-proxies)).
>
> ⚠ **Apps and storage plugins are downloaded from public addresses only**
> (Security). An app installed from an address or a repository,
> an install request, the update check and a storage plugin from a URL or a
> source now refuse an address on this machine, on the private network or of
> the cloud metadata service - judged after DNS and on every redirect - and a
> redirect from `https://` to plain `http://`. An administrator who installed
> from an internal server is refused now: upload the files instead (an app
> installed that way earlier says *Could not check* at the update check).
> `FILEX_PLUGIN_LOOPBACK_SOURCES=1` opens this machine, and nothing else, for
> development and tests - never on a server
> ([CONFIGURATION.md](./CONFIGURATION.md#storage-plugins)).
>
> ⚠ **Storage plugins** (Security): a plugin keeps the driver
> name it was first accepted with - another plugin may not take it, even while
> the first is off; a remote plugin that answers with a redirect is refused
> (register the address it points to); and a share download goes straight to a
> plugin's presigned address only when that address is `https://`.
>
> ⚠ **The bundled S3 server is Versity S3 Gateway, not MinIO** (Changed).
> `minio/minio` is no longer published, so a Compose stack or Helm release that
> bundled MinIO copies its bucket across once: the `versitygw` Compose profile
> and `versitygw.enabled` in Helm replace `minio`, and MinIO's own data format
> is not readable by the new server. Nothing is deleted for you: Compose keeps
> the `minio-data` volume, and the chart refuses to render while
> `minio.enabled` is `true`, with the commands that keep the old volume and
> MinIO serving while you copy
> ([STORAGE.md → Moving off the bundled MinIO](./STORAGE.md#moving-off-the-bundled-minio)).
>
> ⚠ **LibreOffice is gone: office conversions and thumbnails need a connected
> ONLYOFFICE** (Changed, Removed). The full image
> drops LibreOffice and its Java runtime (about 420 MB less to download and
> 1.1 GB less on disk: 642 → 225 MB compressed), and filex no longer runs a
> LibreOffice installed next to the bare binary either. Office documents are
> the ONLYOFFICE Document Server's you connect under **External services**:
> their thumbnails, and the apps' office engine - the Convert app's Word,
> Excel, PowerPoint and OpenDocument conversions, PDF/A included. With
> ONLYOFFICE connected there is nothing to do: the engine uses the editor's
> address, secret and callback address and turns on without a restart, and
> the thumbnails LibreOffice drew before are kept and drawn again by
> ONLYOFFICE, one document at a time, as they are listed (or all at once with
> a *Fix* repair). Without it, office documents show their type icon instead
> of a thumbnail and the Thumbnail repair tab says "ONLYOFFICE is not
> configured", the converter's office targets are greyed for an administrator
> with "connect it under External services", and a job that asks anyway says
> so - nothing fails silently; documents still open in the read-only preview
> and **+ New** still makes them. An app that asks for `engines:libreoffice`
> keeps working: it is the same engine as `engines:office`, and the same
> grant. ONLYOFFICE does not make HTML from a spreadsheet or one CSV per
> sheet, as LibreOffice did
> ([ONLYOFFICE.md](./ONLYOFFICE.md#office-conversions-for-apps-the-office-engine),
> [thumbnails.md → Office through OnlyOffice](./thumbnails.md#office-through-onlyoffice)).
>
> ⚠ **A page on another origin that changes things with filex's session cookie
> needs that origin in `FILEX_CORS_ALLOWED_ORIGINS`** (Security).
> The default `*` does not grant it, and a page on a sibling subdomain is
> another origin too. An embed with a bearer token, a host that proxies with a
> key (the recommended pattern), the desktop app, the installed web app, a
> dashboard that frames filex, share and drop links, upload tickets, WebDAV and
> S3 clients, and scripts are unaffected. A refused change answers
> `403 cross_origin_refused`.
>
> Also on upgrade:
> - The operating-system providers (`windows`, `pam`) are switched on from
>   **Admin → Identity providers** only, after their test. Written into
>   `FILEX_AUTH_DRIVERS` or `auth.drivers`, either is refused at start with a
>   warning, and the other providers start as usual.
> - A link into docs.filex.sh at a heading that held a long dash has a new
>   fragment (`#token-kinds--user-vs-app` is `#token-kinds---user-vs-app`);
>   the old one opens the top of the right page (Changed).
> - The CLI's saved session (`~/.filex/cli.yaml`) is used only at the address
>   it was saved with: a script that reaches the same server by another name
>   or an IP signs in there once, or passes `FILEX_TOKEN` (Security).
> - An agent (REST `/api/ai`, MCP) or a ShareX setup that writes into an
>   end-to-end encrypted folder is refused now (`409 E2E_PLAINTEXT_REFUSED`):
>   pass `allow_plaintext` where storing plaintext there is meant
>   (Security).
> - Read Security if you handed out folder-confined API tokens
>   (one could change sharing grants outside its folder, on any install) or
>   ran 0.49.0 multi-tenant (a tenant's administrator could rewrite the
>   built-in roles), and if you run OIDC (an identity provider could sign a
>   person in to another account, another tenant's on a multi-tenant install).

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0500---2026-10-02).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.50.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.50.0`

## v0.49.0

<span class="filex-release-date">29 September 2026</span>

Roles and per-person permissions, contributed by @manjotsc (#75): 28 named permissions, one role per person built from them, per-person exceptions on top, and refusals that name the permission and the role in the reader's language; a custom role can carry its name in more than one language. Apps can declare permissions of their own, decided per role and per person on the same screens (the e-signature app's “Request signatures”), and are told which of them the person holds. An agent or an API key no longer installs or approves an app: it leaves an install request an administrator decides on the Plugins page. API-key scopes apply on every surface, account changes need write, a public link answers only while its creator may still make it, and the admin page called Permissions is now Folder access.

## What changed

Everyone has one role now - Administrator, User, Viewer or a custom role with
its own list of permissions - and an app can declare permissions of its own
that an administrator hands out the same way. An API key is held to its
`read`, `write` and `delete` verbs on every route and protocol, and the acts
that make somebody an administrator or install a plugin are a signed-in
person's: a key leaves an install request instead. Roles and per-user
permissions are manjotsc's pull request
([#75](https://github.com/BRF-Tech/filex/pull/75)).

> ⚠ **An API key's verbs now hold everywhere** (Security): an
> integration that changes or deletes files - through `/api/files` (and so
> `filex client`), the MCP file tools, WebDAV, SFTP, FTPS, or an S3 key or NFS
> export minted from a token - with a token that does not name `write` or
> `delete` gets `403 token missing scope: <verb>`. Changing the account's
> profile, password or two-factor with a token needs `write` too; the desktop
> app's pairing carries it for every role, a viewer's included, and a viewer
> who paired before 0.49.0 signs the app in again once (**Reconnect**). Give
> each token the verbs it uses; tokens issued before 0.43.0 carry the full
> list and are unaffected.

> ⚠ **Some acts are a signed-in administrator's only** (Security):
> installing, upgrading, going back, switching, removing and re-permissioning
> an app or a storage plugin; creating an administrator, promoting an account,
> changing an administrator's role or setting their password; and allowing an
> admin-area permission (`admin.*`) through an exception, a custom role or a
> built-in role. An API key gets `403 session_required` - for a plugin it
> leaves a request instead (`POST /api/admin/plugin-requests`). Taking a right
> away stays open to a key.

> ⚠ **A public link follows its creator's right to make it**
> (Security): a download link answers only while the person who
> made it still holds `share.links` and edit rights on the item (a file
> request: `share.upload_links`). A link whose creator has since become a
> viewer or lost the folder answers `404` after the upgrade, and answers again
> once the right is given back. Deleting an account now deletes the download
> links and file requests it opened. An app's own public pages are not
> affected, and neither are links whose creator was deleted before 0.49.0 -
> they name no creator.

> ⚠ **For app authors** (Added): filex before 0.49.0 refuses a
> manifest that carries `user_permissions` or `requires` (unknown fields are
> refused), so a manifest that uses them says `"filex": ">=0.49.0"`. An older
> server cannot read even that range: its update check says *Could not check*
> for such a release, rather than *needs a newer filex*. From 0.49.0 the
> update check says *needs a newer filex* for a version that carries a field
> this filex does not know (Fixed).

### Added

- **Everyone has one role, and a role is a list of permissions you choose**
  ([docs/PERMISSIONS.md](./PERMISSIONS.md)). 28 permissions - file actions
  (download, create, modify, rename, move, delete, permanent delete, tag),
  sharing, comments, the agent API, apps, each protocol, API keys, the desktop
  app, editing one's own account, and five admin areas - granted through
  **roles**. Everyone has **one role**, picked in the Role field on their page:
  Administrator, User, Viewer (the last two now editable), or a **custom
  role** - its own list of permissions, optionally different in some folders
  ("no delete, except in Scratch"). A custom role can be the starting role of
  new SSO accounts in given groups. Per-person exceptions beat the role. A
  custom role can carry limits - share-link lifetime and password, blocked
  file types, largest file, required two-factor authentication. Enforced on
  every door: the web app and REST API, the agent API, WebDAV, SFTP, FTPS, S3
  and NFS, and API keys. A refusal says which permission and which role, in
  the reader's language.
- **An app can declare permissions an administrator hands out per role and
  per person** ([docs/APP-PLUGINS.md → App permissions](./APP-PLUGINS.md#app-permissions)).
  The manifest's `user_permissions` names them - a signing app can put
  *Request signatures* behind one while signing what somebody sent you needs
  none - and an action or a screen that `requires` one is left out of the menu,
  and refused with `403 permission_denied` naming the permission and where the
  answer came from, for an account that does not hold it. A key is
  `app.<app>.<id>`; the answer is an administrator's always, else the person's
  exception, else their custom role's (`settings.apps`), else the built-in
  role's (`/api/admin/roles/builtin` → `apps`), else the app's own default -
  `viewer`, `user` or `admin`. `GET /api/admin/roles/catalogue` lists the
  installed apps' permissions under `apps`. **Admin → Roles** shows them as an
  **Apps** group in every role's editor, each *Default (…)* / *Allow* /
  *Deny*, *Default* saying what it comes to for that role; a person's page
  shows the same rows among their exceptions, beside the answer and where it
  comes from (the exceptions answer carries `effective.apps`). A preset and
  *Clear exceptions* leave a person's app exceptions as they are; the Apps
  group resets them on its own. A delegated administrator leaves a person's
  app permissions as they are, and sees them read-only.
- **An app is told which of its own permissions the person holds**
  ([docs/PLUGIN-KIT.md → User permissions](./PLUGIN-KIT.md#user-permissions-what-an-administrator-hands-out)).
  A job (`action_run`), a screen's event and an interface's `ui_call` carry
  `actor.permissions`: the ids of the app's `user_permissions` the person
  holds, decided by the same question filex asks at the door, so an
  administrator holds every one. A public page's event and work nobody
  started carry none. The Go SDK reads it with `Actor.Can(id)`, and
  `plugintest`'s default actor, an administrator, holds every permission the
  manifest declares. An app uses it to leave out a hint to an action the
  reader cannot run.
- **An API key asks for a plugin; an administrator installs it** (migration
  00070, `plugin_requests`). An API key - an agent, a script, the CLI - leaves
  a request to install or upgrade an app or a storage plugin, with a required
  reason. filex resolves the source at once and freezes what it answered: the
  manifest, the SHA-256 the bytes must have and the permissions it asks for.
  An administrator approves or rejects it under **Admin → Plugins → Install
  requests** (the explorer's table; the review shows the install wizard's
  permission list with the app's reasons). Approval installs exactly the
  frozen bytes with exactly the frozen permissions; a source that serves
  anything else by then closes the request as **Source changed**
  (`superseded`) and installs nothing. One waiting request per source; a
  request nobody decides expires after 14 days
  (`FILEX_PLUGIN_REQUEST_TTL_DAYS`). Supertenant-only in multi-tenant mode.
  REST: [BACKEND.md → Admin: plugin requests](./BACKEND.md#admin-plugin-requests).
  - **Plugin tools for agents** (admin MCP): `admin_app_plugins_list`,
    `admin_app_plugin_get`, `admin_app_plugin_logs`,
    `admin_app_plugins_check_updates`, `admin_plugins_list`,
    `admin_plugin_get`, `admin_plugins_check_updates`,
    `admin_plugin_request_install`, `admin_plugin_request_upgrade`,
    `admin_plugin_requests_list`, `admin_plugin_request_get` - reading and
    requesting only; there is no tool to approve or reject
    ([MCP.md → Plugin tools](./MCP.md#plugin-tools)).
  - **`filex client plugins request` / `filex client plugins requests`** -
    leave and follow a request from the command line
    ([CLI.md](./CLI.md#plugin-requests)).
  - **`plugin_requested`** notification: the administrators hear of a new
    request in the bell and on every webhook that carries operator alarms.
  - Audit entries `plugin_request.create`, `.approve`, `.reject`, `.expire`,
    `.supersede`.
- **Administration can be delegated**: an account without the admin role can
  be given users, folder grants, shares, audit or monitoring - never an
  administrator's account, never more than it holds. A change is judged by
  what the account would hold afterwards, so lifting a Deny, ending a
  restrictive role or creating a User account counts too; a password is reset
  or set only for an account that holds nothing the delegate lacks.
- **The desktop app for Windows on Arm and Linux arm64.** An arm64 installer
  and portable `.exe` for Windows, and an arm64 AppImage, `.deb`, `.rpm` and
  Snap Store revision for Linux - built by every release, installed and opened
  on arm64 machines before they are published, and updated from their own
  feeds (Windows: one feed, the x64 installer first, so existing installs see
  what they saw before). The Microsoft Store gets x64 and arm64 in one bundle,
  and the winget manifest names both installers. 0.48.1's arm64 packages were
  added to its release afterwards.
- **Every release runs the arm64 CLI, server and images on arm64 machines**
  (Linux, Windows, macOS): serve, sign in, upload, download byte for byte,
  move, delete - before anything is published.
- Admin → Users shows each person's role, exceptions and where every answer
  comes from; Admin → Roles lists the built-in and custom roles, in the
  explorer's table like every other admin list. Changes are audited with
  before and after.
- **A custom role in every language** (migration 00071): the role editor's
  *Name and description in other languages* gives a role its name and
  description in each interface language the server offers - the built-in
  ones and every installed language pack's. Everyone sees the role in their
  own language on the Roles, Users and person pages, and a refusal names it in
  the reader's account language. The role's own name stays required and is
  the fallback; `source.rule_name` and the audit log keep it.
  `POST` / `PUT /api/admin/roles` take `names` and `descriptions`
  (`{"tr": "…"}`); a language the server does not offer is refused
  ([PERMISSIONS.md → API](./PERMISSIONS.md#api)).
- SSO sign-ins record the provider's groups claim; a new account whose groups
  a custom role names starts with that role.

### Changed

- Every account keeps what its role could do before: migration 00069 only
  adds tables and columns, and the built-in User and Viewer roles start as the
  Standard user and Read-only presets.
- **Admin → Permissions is now Admin → Folder access** (*Klasör erişimi*):
  the page that lists every per-file and per-folder grant, apart from Roles.
- **The explorer reads the account's permissions itself** when its host
  passes none (`GET /api/auth/me`): the desktop app and the embedded explorer
  now hide the actions a role refuses, as the web app does, instead of
  offering them and answering `403`. An administrator is not narrowed, and an
  answer without permissions changes nothing.
- Explorer rename checks the new name as well as the old one, and the
  invite-by-email fallback that creates a public link needs the right to
  create public links.
- **A viewer's desktop app changes its own account.** Its pairing now carries
  `read,write` (never `delete`), so the account menu saves the profile, the
  password and two-factor as the web app does; the viewer role still refuses
  every file change, link and grant. A viewer who paired before 0.49.0
  presses **Reconnect** once ([DESKTOP.md](./DESKTOP.md#the-token-the-app-is-given)).

### Fixed

- **The Users page's search box filters the list.** It sent the text to a
  server that returns every account and never read it, so typing changed
  nothing.
- **The CLI inside the desktop app says which release it is.** It answered
  `filex --version` with "0.1.0-dev" in every package; it is now stamped with
  the version, commit and build date like the released CLI.
- **A dialog behind another no longer takes the focus from the one in
  front.** Every dialog puts the focus in itself shortly after it opens. When
  a second dialog opened over it in that moment (the "Close this draft?"
  question, when a draft was closed right after it was opened), the dialog
  behind took the focus a moment later: "Keep in Drafts" lost it to the
  draft's text box, and an Enter went into the text instead of answering the
  question. Only the dialog in front takes the focus now.
- **A file dropped through a file request over its creator's size limit
  answers `413 file_too_large`** with the drop page's own sentence, not
  `503 storage_unavailable`.
- **The Releases page on docs.filex.sh** keeps a release note's references to
  its own sections as plain words; they used to land in an older release's
  section of the same page.
- **Every door that makes a link is hidden without its permission.**
  *New → Request files* stayed in the menu for an account without
  `share.upload_links`, and the server refused it. Now the explorer asks
  the same permissions the server asks, per folder when the role says so, in
  every host: *Request files* and the share dialog's file-request section need
  `share.upload_links`, the dialog's link switch and options and the details
  panel's *Create link* need `share.links`, the people section and *Manage*
  need `share.users`. *Share* (and its key, Shift+S) is offered only when one
  of them applies - a file takes no file request.
- **The explorer's per-folder question works with a read-only token.**
  `POST /api/files/manager?action=allowed` changes nothing and now needs
  `read`; under `write` a read-only token's question was refused, and the
  explorer, reading that as "unknown", offered every action that differs from
  folder to folder.
- **An app update that needs a newer filex says so, even when this filex
  cannot read all of it.** A newer version whose manifest carries a field this
  filex does not know (or a newer `manifest_version`) made the update check
  answer *Could not check* with `unknown field`, as if the app were broken.
  The check now reads its name, version and range, and lists it as needing a
  newer filex - or steps over it to an older release that fits. Installing or
  upgrading to such a manifest is still refused.

### Security

- Harden plugin installation: installing, upgrading, going back, switching,
  removing and changing the action overrides of an app
  (`/api/admin/app-plugins`), and installing, upgrading, `PATCH` and `DELETE`
  of a storage plugin (`/api/admin/plugins`), now need an administrator signed
  in to the admin panel; an API key gets `403 session_required`, naming
  `/api/admin/plugin-requests`, where it leaves a request. Reading - the lists,
  one plugin, the logs, the update check and the install review
  (`?dry_run=1`) - stays open to an admin-scoped key
  ([APP-PLUGINS.md → Install requests](./APP-PLUGINS.md#install-requests)).
- Harden API token permissions: a token's verbs - `read`, `write`, `delete` -
  are now enforced on every route and protocol a token can reach: the web
  explorer's `/api/files` routes (and so `filex client`), the MCP file tools,
  WebDAV, SFTP, FTPS, and the S3 keys and NFS exports minted from a token.
  Without `write` a token keeps what a viewer keeps (preferences, stars,
  recently opened, personal tags, comments, notifications) and the document
  editor opens read-only; MCP `tools/list` offers only the file tools the
  token can use. Changing the account itself - `PATCH /api/auth/profile`,
  `POST /api/auth/password` and the three `/api/auth/totp/*` routes - needs
  `write` as well. ⚠ An integration that changes or deletes files with a token
  that does not name `write` or `delete` now gets `403 token missing scope`:
  give its token the verbs it uses. Tokens issued before 0.43.0 carry the full
  list and are unaffected. See [RBAC.md](./RBAC.md#api-tokens-verbs-on-every-surface).
- Harden public links: one rule makes a link on every surface - the explorer,
  `POST /api/ai/share` and the MCP `file_share` tool alike - edit rights on the
  item and the `share.links` permission (`share.upload_links` for a file
  request). A link answers only while its creator still holds that right on
  the item, and answers again when the right comes back; `/s/`, its metadata,
  folder browsing, `/d/` and `/api/public/*` ask the same question. A file
  request applies its creator's blocked file types and largest file to what is
  dropped ([SHARING.md](./SHARING.md#a-link-follows-its-creator)).
- Harden account deletion: deleting an account deletes the download links and
  file requests it opened, in the same transaction, on every path that deletes
  one - an administrator, a tenant deletion, a rolled-back provisioning. They
  used to stay with no creator, and a link with no creator kept answering.
  The deletion's audit entry records `links_closed`, the number still open.
  An app's own public pages stay.
- Harden administrator accounts: creating an administrator, promoting an
  account to administrator, changing an administrator's role, setting or
  resetting an administrator's password, and allowing an admin-area
  permission - as a person's exception, in a custom role (its list or its
  folder part), or in a built-in role - need an administrator signed in to
  the admin panel; an API key gets `403 session_required`, on `/api/admin`,
  `/api/ai/admin` and the `admin_users_*` MCP tools alike. Managing accounts
  that are not administrators, and taking a permission away, are unchanged.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0490---2026-09-28)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.49.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.49.0`

## v0.48.1

<span class="filex-release-date">28 September 2026</span>

Apps can bring an interface of their own - HTML and JavaScript filex serves from the approved package into a sandboxed frame, opened as the viewer of a file type, as a kind of file in New document, or handing you a file to keep - and nothing updates itself any more: every newer version of an app waits for an administrator's review, and Back to version undoes one. A new document is a draft until its first save (#71). Encrypted folders get a level, contents only (the default) or contents and names; a folder you already have, or a single file as a .fxe, is encrypted where it is, and an unlocked folder downloads decrypted. The desktop app has the notification bell and your account menu in its window. The iframe converter is gone (the Convert app replaces it), a dashboard that shows filex in a frame now needs FILEX_FRAME_ANCESTORS, an item deleted outside filex no longer shows in the Trash (#74), and several hardening fixes; upgrade. (0.48.0 was tagged but its release run failed a test and nothing was published; 0.48.1 carries it.)

## What changed

> 0.48.0 was tagged but its release run failed a test (where the draft
> close question put the focus, now fixed); nothing was published - 0.48.1
> carries it.

Apps can bring an interface of their own, a new document stays a draft until
its first save, encrypted folders can hide their names too and be made from a
folder you already have - or from a single file - and the desktop app gets the
notification bell and the account menu in its window. Every newer version of
an app now waits for an administrator, and the iframe converter is gone.

> ⚠ **Nothing updates itself any more** (Changed): 0.47 installed
> a newer version of an app by itself when it asked for no new permission.
> Now every version waits for an administrator's **Review update**; the
> per-app automatic-update switch is gone and `PATCH …/app-plugins/{id}` with
> `auto_update` answers `400`.

> ⚠ **filex in someone else's frame** (Changed): a dashboard that
> shows filex in an `<iframe>` (Homarr, Organizr and the like) needs its
> origin in the new `FILEX_FRAME_ANCESTORS`; otherwise the browser refuses the
> frame. The embedded explorer is not a frame and is not affected.

> ⚠ **The iframe converter is removed** (Removed): conversion is
> the Convert app. `FILEX_CONVERT_URL`, the compose `convert` profile and the
> Helm `convert` values are gone; a server that still sets the variable says
> so once, at start.

> ⚠ **For app authors** (Changed): the app platform API is
> narrower - paths an app passes must be the call's inputs or files it keeps
> state on, `share_create` and `file_lock` need **editor**, an `http:`
> permission names a host, and screen calls are capped per app. The two
> published apps are unaffected.

> ⚠ **For integrators** (Changed): `@brftech/filex-core` now
> depends on `@headlessui/vue`, `lucide-vue-next` and the new
> `@brftech/filex-app-ui`.

> ⚠ **Encrypted folders get a level, and level 2 is refused by older filex**
> (Added): level 1 (contents only) stays the default and keeps its
> v2 key file, which every filex since 0.31 opens. Level 2 (contents and
> names) - chosen in the create dialog, or later in the folder's settings -
> writes marker v3, which filex 0.47 and older will not open: on purpose,
> because they would upload files under their plaintext names next to the
> encrypted ones. Every folder that already exists stays at level 1.

> ⚠ **Large and single encrypted files need filex 0.48** (Added): a
> file over 200 MB in an encrypted folder is now written as a stream (header
> `0x02`) and a single encrypted file is a `.fxe`; filex 0.47 and older open
> neither. The folder, and its files up to 200 MB, still open there.

### Added

- **An app may bring its own interface**
  ([docs/APP-PLUGINS.md](./APP-PLUGINS.md#an-apps-own-interface)). Besides
  the screens filex draws, an app can ship HTML, CSS and JavaScript of its
  own - an editor, a viewer - and an app can be only that, with no
  WebAssembly module at all.
  - filex serves the interface from the app's package (a zip pinned by its
    SHA-256, checked once at install and never unpacked) and runs it in a
    sandboxed frame whose policy allows no connection, no storage, no cookie
    and no reach into the page around it. The page policy is built from what
    you approved, never from the package: scripts only from the package,
    `connect-src 'none'`.
  - Everything the interface does goes through filex over one checked
    channel: reading the files it was opened with, saving over them (a new
    version, or the draft it is), a new file where the person picks, asking
    the app's own module (`ui_call`), a toast, a question. The server checks
    every save again.
  - A new placement, **viewer**: an app opens a file type the way filex's
    own viewers do, and the file menu gets **Open with &lt;app>**. Unsaved
    changes are asked about before the interface closes; drafts (#71) work
    unchanged.
  - The install review has an **Interface** group: the package, every
    address outside it - **mirrored** (filex downloads it once and serves it
    itself) or **live** (the reader's browser fetches it; a permission, in
    yellow, with what that means) - the script-policy exceptions, and a
    plain note that a browser cannot fully stop a page sending data out
    (WebRTC). An upgrade that adds an address asks again.
  - An interface may **read its own package** at run time
    (`ui.package_fetch`, permission `ui:package-fetch`, on the review as
    "reads its own package") - what an editor that loads its own shape
    libraries and translations needs. Only that version's files: the page's `connect-src` and
    Chrome's `Connection-Allowlist` name the package's own path; another
    app, filex's API and the network stay closed. Every interface's
    allowlist now names its package path instead of filex's whole origin.
  - **New document** can make an app's kind of file (`new_documents`,
    [docs/APP-PLUGINS-API.md](./APP-PLUGINS-API.md#new-documents)): a row
    in the app's own words under **Apps**, made from the app's template (or
    empty) and opened in the app's viewer - a draft until its first save.
    Each kind is on the install review (`ui-new:.<ext>`); the rows are told
    to signed-in people only, while the app runs.
  - An interface may **hand you a file for your own disk** (`ui.download` in
    the manifest, `fx.download` in the SDK, permission `ui:download`): filex
    does it, on your click in the interface or your yes, never over 256 MiB -
    in Chromium streamed into the file you pick, elsewhere a download.
  - **The editor tab** (`/files/edit`, *Open in new tab*) opens a file in the
    app that views it, by the explorer's own rule (*Open with*'s choice
    travels as `app=<plugin>/<view>`); before, the tab showed filex's own
    viewer.
  - What holds an interface in: an address outside its package is a plain
    public https address - no IP, no internal name; its page is revalidated
    at every opening, so a narrowed grant takes effect at once; a signed
    app's module must describe the same interface as its manifest, and a
    mirrored file is served only as the kind it was declared; a viewer names
    the file types it opens, each a line of the review (`ui-viewer:.md`), so
    an update that widens them asks again; a save counts against the quota,
    and "save as" keeps to the view's file type and never writes into an
    encrypted folder; the interface is told an error code, never the
    server's text; a job or a clipboard write needs a gesture in its frame or
    the person's yes, and everything filex draws for it names the app; one
    frame's calls are capped, and its calls to the module share the app's
    screen-call ceiling; filex's pages frame interfaces by path
    (`<host>/_appui/`), so an interface cannot send its frame to another
    filex page; and the interface's page switches off every powerful browser
    feature (devices, sensors, credentials, ads).
  - For authors: the `@brftech/filex-app-ui` SDK (a few KB, no
    dependency), `pluginkit` `Plugin.UI` + `plugintest` `Harness.UICall`,
    and [Writing an interface](./PLUGIN-KIT.md#writing-an-interface).
    `FILEX_APP_PLUGIN_MAX_UI_MB` (128) caps a package; migration `00065`
    adds `app_plugins.ui_sha256`.
- **A new document is a draft until its first save**
  ([#71](https://github.com/BRF-Tech/filex/issues/71)). Choosing New
  document, naming it and closing the editor used to leave an empty file in
  the folder. Now Create makes a draft - a real file with the name and type
  you chose, kept in your own drafts area of that storage - and the folder
  gets nothing until you press **Save**
  ([docs/ONLYOFFICE.md](./ONLYOFFICE.md#drafts-nothing-is-in-the-folder-until-you-save)).
  - The editor opens on the draft under a bar that says where it will go,
    and every editor writes into it as you type: the text, code and Markdown
    editors every few seconds, the document server and the diagram editor
    through their own saves. The browser asks before a tab with unsaved
    changes is closed, and so does the desktop app's document window.
  - **Save** puts it in its folder. If a file has taken the name meanwhile,
    filex asks - *save as `report (2).txt` instead?* - and nothing moves
    before the answer; a save never replaces a file.
  - Closing a draft that was never saved asks **Save to disk**, **Keep in
    Drafts** (the default) or **Discard**, which moves it to the Trash;
    restoring it from there puts it back in Drafts.
  - **Drafts**, in the navigation panel beside Recent, Starred and Trash,
    lists your drafts from every storage in the explorer's own table - name,
    where it will be saved, storage, modified - with Open, Save to disk and
    Delete, and the panel shows how many there are. No notifications.
  - A draft is its owner's alone: not in any listing, search, share, WebDAV,
    S3, SFTP, FTPS or NFS view, desktop sync, storage usage figure or version
    history, and not shown to anybody else - administrators included. On
    disk it is `.filex-drafts/<user id>/<key>/<name>`, one of the names filex
    keeps for itself.
  - At most **50 drafts per person** by default: *Admin → Protection →
    Drafts* (1-1000), or `FILEX_DRAFTS_LIMIT` on a fresh install. At the limit
    New document says so and offers Drafts; it never creates the file in the
    folder instead.
  - For integrators: `POST /api/files/drafts` takes `newfile`'s body, with
    `GET`, `…/count`, `…/{key}`, `POST …/{key}/save` (`409 TARGET_TAKEN` with
    a `suggested` name) and `DELETE …/{key}`
    ([docs/BACKEND.md](./BACKEND.md#drafts)). Drafts belong to a person, so
    an app token and an embed confined to one folder by its host keep
    creating the file directly; `capabilities.drafts` is present exactly when
    the caller's New document makes drafts. Migration `00064` adds the
    `drafts` table.
- **Encryption levels, and encrypted file and folder names.** An encrypted
  folder has a level: **1 · Contents only** (the default) or **2 · Contents
  and names**, chosen in the create dialog, shown in the strip, changed in the
  folder's new **Encryption settings…** (which also hold the password and the
  escrow slot). At level 2 the names are encrypted in the browser too -
  AES-SIV (RFC 5297) under a name key sealed by the folder key, with the id of
  the folder a name is in as associated data, so the same name in two folders
  is two different stored names; base64url on the server, a folder's id
  carried in its own name, long names shortened with a sidecar. The server
  stores and indexes only ciphertext names; every surface of the explorer -
  listing, split pane, breadcrumb, tabs, destination picker, Recent, Starred,
  tags, Home, trash, search, the details panel, uploads, downloads - shows the
  plaintext while the folder is unlocked and "🔒 Encrypted item" while it is
  not. The vault level is designed, not built, and not offered.
  [E2E-ENCRYPTION.md → Encryption levels and names](./E2E-ENCRYPTION.md#encryption-levels-and-names)
- **Encrypt a single file.** Right-click any file and choose *Encrypt with
  E2EE…*: it becomes one self-contained `<name>.fxe`, encrypted in the browser
  under a password of its own, with a recovery key shown once and - where the
  installation has key escrow - an escrow slot, said up front. Open, preview
  and download decrypt in the browser; *Change password…* rewrites the header
  only; *Remove encryption…* puts the plaintext back. The name can be hidden
  too (`encrypted-<hex>.fxe`). The dialog says what the server already saw of
  the original (the trash, its versions, a thumbnail, backups) before
  anything happens.
  [E2E-ENCRYPTION.md → Single encrypted files](./E2E-ENCRYPTION.md#single-encrypted-files-fxe)
- **Desktop app: the notification bell and your account menu in the top bar.**
  The app raised a native notification for everything new and had nowhere in
  its window to read one, mark it read or follow it - and no account menu at
  all. The top bar now ends as the web app's does: the **bell** (the unread
  count on it, the newest fifteen, *Mark all read*, *View all* with pages and
  *Unread only*) and the **avatar**, which holds your settings as a person:
  *User settings* opens the web app's settings dialog **inside the window**
  (profile, time zone, appearance, compact list, default folder view,
  notification switches, password, two-factor sign-in), *Admin panel ↗* for an
  administrator, and the file list's shortcut and tour rows that sat behind
  the "…". A click on a notification lands in the window: the folder with the
  file selected, the Trash with the item selected, or an app's home view. They
  are the web app's own components, not a second copy - see *Changed*.
  Signing out stays where the app's own settings are, *Settings → Accounts*,
  with the language, sync and the other accounts.
  ([docs/DESKTOP.md → Notifications and your account](./DESKTOP.md#notifications-and-your-account))
- **Encrypt a folder you already have, in place.** Right-click a folder →
  **Encrypt with E2EE…**: the same dialog as creating one (level 1 by
  default), and every file is encrypted where it is, resumably; at level 2 the
  names follow. The server keeps no plaintext version of those writes, and
  when it finishes drops the thumbnails and extracted text it held for the
  folder and - the owner's choice, on by default - its older versions and
  trash entries (`POST /api/files/e2e/cleanup`). The key file carries the new
  required feature `conv` while it runs, which filex 0.47 and older refuse.
  [E2E-ENCRYPTION.md → Encrypting a folder you already have](./E2E-ENCRYPTION.md#encrypting-a-folder-you-already-have)
- **Raise a folder you already have to level 2.** **Encryption settings… →
  Change level…**, after saying what changes; nothing is proposed on unlock.
  It renames only (no content is touched), writes the key file first, and an
  interrupted run continues where it stopped. **Fix names** in the same place
  renames stray plaintext names written later over WebDAV or the CLI, and
  repairs names moved in from another folder outside filex.
- **Change an encrypted folder's password.** **Encryption settings… → Change
  password…**, proved with the current password or the recovery key. A
  folder created since v0.31 rewrites only its key file - no file is touched,
  the recovery key, escrow and encrypted names keep working. A folder from
  before v0.31, whose key comes from its password, is re-keyed: a new folder
  key, every file's key re-wrapped (the contents are not re-encrypted),
  resumable, with a new recovery key; any folder can be re-keyed on purpose
  ("Also replace the folder key"), which is also how a leaked recovery key is
  revoked. Opening a folder with its recovery key now asks for a new password
  straight away. Every change is recorded in the audit log and the folder's
  owner is notified (`e2e.password_changed`, subscribable by webhook).
  [E2E-ENCRYPTION.md → Changing the password](./E2E-ENCRYPTION.md#changing-the-password)
- **No size limit in encrypted folders.** A file over 200 MB is encrypted in
  the browser as a STREAM (chunked AES-256-GCM, 1 MiB chunks, truncation and
  reordering detected) while it uploads, and decrypted as it downloads -
  nothing is held in memory. These files use header version `0x02`, which
  filex 0.47 and older refuse (the folder and its smaller files still open
  there); files up to 200 MB are written exactly as before.
  [E2E-ENCRYPTION.md → Streaming content](./E2E-ENCRYPTION.md#streaming-content-stream)
- **Download an encrypted folder decrypted.** Downloading an unlocked
  encrypted folder, a subfolder or a selection inside one now gives a zip with
  the real names and the decrypted content, made in the browser as it is
  saved; *Download encrypted copy* keeps the server's ciphertext zip. Chrome,
  Edge and the desktop app save it straight to disk at any size; Firefox and
  Safari up to 1 GB, and say so above that.
  [E2E-ENCRYPTION.md → Downloading a decrypted copy](./E2E-ENCRYPTION.md#downloading-a-decrypted-copy)
- **The escrow key opens a single encrypted file - and its owner is told.**
  On an installation with key escrow, the unlock dialog of a `.fxe` sealed to
  its key offers *Use the escrow key*: the same flow as an encrypted folder's,
  the browser proving it holds the private key and the server notifying the
  file's owner before anything is decrypted. `/api/files/e2e/escrow/challenge`
  and `/used` accept a `.fxe` path.
  [E2E-ENCRYPTION.md → Key escrow](./E2E-ENCRYPTION.md#key-escrow-optional-operator-recovery)
- **A `.fxe` password change is announced, and the server sees it too.** The
  owner is notified and the audit log records `e2e.password_change`, as for a
  folder; independently, every rewrite of a `.fxe` on any surface is audited
  by the server as `e2e.fxe_header_rewritten` (password, recovery key, escrow,
  content…), and a changed password deletes the versions that would still
  open the file's current contents with the old one.
  [E2E-ENCRYPTION.md → Who is told](./E2E-ENCRYPTION.md#who-is-told)
- **Delete the original for good while encrypting it (administrators).** The
  encrypt dialog offers an administrator to delete the original's versions and
  its trash entry as soon as the encrypted copy is saved; for someone else's
  file it names the owner and asks a second, explicit confirmation.
  [E2E-ENCRYPTION.md → What the server already saw](./E2E-ENCRYPTION.md#what-the-server-already-saw-of-the-original)
- **Over 1 GB in Firefox or Safari, the decrypted download says so and hands
  over the way out.** Instead of an error line, a dialog - before the password
  is asked, for a `.fxe` - with *Download encrypted file* (or the encrypted
  folder as a zip) and the `filex decrypt` command to copy. Chrome, Edge and
  the desktop app still stream it to disk at any size.
  [E2E-ENCRYPTION.md → Where a decrypted download goes](./E2E-ENCRYPTION.md#where-a-decrypted-download-goes)
- **`filex decrypt`** - decrypts a downloaded encrypted folder (or its zip, or
  one file) on your own machine, with the folder password or the recovery
  key, offline, into a folder with the original names. The password is read
  from the terminal or standard input, never from an argument; a wrong password
  or a damaged file stops it with no partial output. Reads every marker version.
  [CLI.md → filex decrypt](./CLI.md)
- **`filex decrypt file.fxe`** writes a single encrypted file back under its
  original name (or `-o`), and `filex decrypt` streams `0x02` folder files; a
  wrong password (exit 5) or a damaged, truncated or extended file (exit 6)
  leaves nothing behind.
  [CLI.md → A single encrypted file](./CLI.md#a-single-encrypted-file-fxe)
- **The server records every rewrite of an encrypted folder's key file** - an
  audit row `e2e.key_file_rewritten` with which slots changed (`password`,
  `recovery_key`, `escrow`, `level`, `rekey`) and the surface it came from,
  whether or not the client announced it. When the password or recovery slot
  changed, the key file's earlier versions are deleted: each of them still
  opened the folder with the old password.
  [E2E-ENCRYPTION.md → Who is told](./E2E-ENCRYPTION.md#who-is-told)
- **Notifications never print an encrypted name.** A row about an item inside
  an encrypted folder carries `meta.e2e_root`; the bell, the notifications
  page, the browser toast and the desktop app show "🔒 Encrypted item" - or
  the real name, where this tab's explorer has the folder unlocked - instead
  of the ciphertext the server stores.
  [NOTIFICATIONS.md](./NOTIFICATIONS.md)

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0481---2026-09-28).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.48.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.48.1`

## v0.47.0

<span class="filex-release-date">26 September 2026</span>

filex runs under a sub-path behind a reverse proxy (a FILEX_PUBLIC_URL with a path works across the web app, WebDAV, S3, the desktop app, the CLI and Helm), apps keep themselves up to date within the filex version range they declare, and the desktop app is in the Microsoft Store, which signs, installs and updates it. Twelve pull requests by Berk Başarır move long folder jobs out of the request and have them say what they are doing; reviewing them closed security holes in the operations queue, so upgrade. Also fixed: uploads from a LAN address over plain HTTP, and a refresh that undid a switch to Starred, Recent, Shared or a tag.

## What changed

filex runs under a sub-path behind a reverse proxy, apps keep themselves up
to date, and long folder jobs leave the request and say what they are doing.
The last of these is twelve pull requests by Berk Başarır
([#58](https://github.com/BRF-Tech/filex/pull/58)-[#69](https://github.com/BRF-Tech/filex/pull/69)),
from an audit of a production install where a person reported "no positive
or negative message, nothing at all". A review of them found and closed a
few older holes in the operations queue on the way in.

> ⚠ **Security fixes in the operations queue** (Security): the
> generic queue endpoint took any job kind a client named and ignored a
> folder-confined token's root, a trash restore did not check the entry's
> tenant, and every member of a storage could read every other member's
> queued operations. Upgrade.
>
> ⚠ **For integrators** (Changed): `POST /api/files/ops` now
> takes only `copy`, `move` and `delete` (`400 BAD_KIND` for anything else),
> and a refused change is also said inside the explorer; hosts that show
> their own message can turn that off with `refusalToasts: false`.
>
> ⚠ **Behind a proxy that strips a path** (Added): if
> `FILEX_PUBLIC_URL` has a path (`https://example.com/filex`) and the proxy
> takes it off before filex sees the request, switch the proxy to pass the
> full path - that path is now filex's base, and filex takes it off itself.
>
> ⚠ **For app authors** (Added): a `filex-app.json` that declares
> the new `filex` version range installs on filex 0.47 and later only; 0.46
> and earlier refuse a manifest field they do not know.

### Added

- **The desktop app is in the Microsoft Store**, as
  [filex File Manager](https://apps.microsoft.com/detail/9PKXDJLVZWXW): the
  one Windows build Microsoft signs, so there is no SmartScreen prompt, and
  the Store installs and updates it. The "Install the desktop app" prompt
  and filex.sh put it first on Windows; the installer and the portable
  `.exe` stay for machines without the Store. Every release is submitted to
  the Store by the release itself from now on
  ([docs/DESKTOP.md](./DESKTOP.md)).
- **filex runs under a sub-path - `https://example.com/filex/` behind a
  reverse proxy - as well as at the root of its own host**
  ([#70](https://github.com/BRF-Tech/filex/issues/70)).
  - One setting: `FILEX_BASE_PATH=/filex` (`base_path`), or nothing at all
    with `FILEX_PUBLIC_URL=https://example.com/filex`, whose path is then the
    base. It is checked at startup (leading slash, no trailing slash, no
    `.`/`..`, unreserved characters); a bad one, or one that disagrees with
    the public URL's path, stops the server with a message that says what to
    write instead, and the startup log names the base in effect and where it
    came from.
  - The proxy passes the **full** path (Caddy `handle`, not `handle_path`;
    nginx `proxy_pass` without a URI part). filex takes the prefix off itself
    and answers 404 to everything outside it before any sign-in check runs,
    except `/healthz`, which also answers at the root for container health
    checks.
  - Every address filex hands a browser carries the base - redirects, the
    OIDC bounce and callback, share and file-request links and the no-JS
    pages behind them, links in e-mails, the realtime socket, a tenant's
    origin in multi-tenant mode - and every cookie it sets is scoped to it
    (`Path=/filex`).
  - The web app is built once and served for any base: the server rewrites
    `index.html` and the PWA manifest as it serves them (and serves both byte
    for byte at the root, so an installed app keeps its identity), and the
    service worker is registered under the base.
  - WebDAV lives at `/filex/dav/`, with every `PROPFIND` href and
    `MOVE`/`COPY` destination under it; path-style S3 at `/filex/s3`, the
    signature checked against the path the client signed. A dedicated S3
    host (`FILEX_S3_DOMAIN`) is served at its own root as before.
  - The desktop app keeps a server address's path (the sign-in field used to
    drop it); the CLI already did. The Helm chart has a `basePath` value.
  - Caddy and nginx examples, measured against a real filex:
    [DEPLOYMENT.md → Serving filex under a sub-path](./DEPLOYMENT.md#serving-filex-under-a-sub-path);
    the setting: [CONFIGURATION.md → Base path](./CONFIGURATION.md#base-path).
- **Apps follow their source and update themselves, and say which filex
  versions they work with.** Once a day, and on **Check for updates** in
  Admin → Apps, filex asks where each app came from for a newer version this
  filex can run: a GitHub app installed at a tag follows the repository's
  releases, a language pack follows its branch, an app installed from an
  address follows that manifest address.
  - A newer version that asks for no new permission installs by itself,
    through the same verified fetch and atomic upgrade as a manual install.
    One that asks for a new permission, or a pack that now brings a module,
    waits as *Needs approval*; **Review update** shows the version jump, the
    new permissions marked **New**, and the ones it dropped.
  - Automatic updates are switched per app from its Actions menu: on by
    default, off for a URL install pinned by SHA-256, never applied on a
    signed-only instance. Every one is logged, audited (`app_plugin.update`)
    and announced to administrators (`app_updated`, and once per version
    `app_update_available`, `app_update_needs_approval`,
    `app_update_failed`). `FILEX_APP_PLUGIN_UPDATE_CHECK=0` turns the daily
    check off; demo instances never check.
  - `filex-app.json` gains `filex`, a version range (`">=0.47.0 <0.60.0"`).
    Installs and upgrades outside it are refused (`incompatible`) and the
    review says so first; updates take the newest version that fits; an
    installed app filex has outgrown keeps running with a warning. Release
    candidates count as their release, development builds check no range,
    and `min_filex` is honoured now. Migration 00063.
  - [docs/APP-PLUGINS.md](./APP-PLUGINS.md).
- **The Trash says who deleted each item.** A delete now records the person
  on the item, and on every file inside a deleted folder, whether it ran in
  the request or later through the operations queue ([#64](https://github.com/BRF-Tech/filex/pull/64)).
  - The explorer's Trash shows it in a "Deleted by" column ("You" for your
    own deletes); the admin's Trash page shows the name.
  - An item nobody in filex deleted shows a dash, with a hover text saying
    why: the scanner found it gone from the storage, or it was deleted
    before this release, when nothing was recorded.
  - A restore clears the record. The trash listing carries
    `deleted_by_id`, `deleted_by_name` and `deleted_by_self`. Migration 00061
    adds `nodes.deleted_by`; nothing is backfilled.
- **Renaming a folder, restoring from the trash and deleting permanently run
  as jobs of the operations queue.** On an object store a folder is one
  request per object, so these waited with nothing on screen until a proxy
  gave up, then said the change had failed while the server carried on
  ([#61](https://github.com/BRF-Tech/filex/pull/61), [#63](https://github.com/BRF-Tech/filex/pull/63)).
  - `POST /api/files/manager?action=rename`, `POST /api/files/manager/restore`
    (which then takes a `node_ids` batch, at most 1,000) and
    `DELETE /api/admin/trash/{id}` take `queued=1`. The same checks answer at
    once; what they allow becomes a job (`202 {op}` / `{ops}`, kinds `rename`,
    `restore`, `purge`), listed under `capabilities.queued`.
  - The explorer and the admin's Trash page use them on a server that lists
    them. The dialog closes at once, the row says "Restoring…" or "Deleting
    permanently…", and the listing follows when the job ends - with the undo
    a rename always offered.
  - These jobs run in a lane of their own, so a long folder rename never
    holds up the copies, moves, deletes and upload commits queued behind it.
    Once running they are not cancelled half-way (`cancellable: false`;
    cancelling answers `409 NOT_CANCELLABLE`).
  - A server restart does not lose them: a restore stopped between entries
    carries on at the next start, and a rename interrupted half-way finishes
    the move instead of failing on the half that had arrived.
  - A purge takes what is in the trash when it runs. An entry restored while
    the purge waited in the queue is left alone.
- **"Delete permanently" in the explorer's Trash deletes.** Everyone was
  offered it; its dialog said the items "will be moved to trash", about items
  already there, and then nothing was deleted. Someone the server lets purge
  now purges, after a dialog that says it is for good; anyone else no longer
  sees it, and the Delete key says how the trash empties itself
  ([#69](https://github.com/BRF-Tech/filex/pull/69)). A press of many items is
  one job batch: one "Deleting N items permanently…", one summary with the
  reason for anything that could not be deleted, one reload.
- **A copy, move or delete of one folder says how far it has got.** Moving a
  folder of 4,000 files on an object store read "0/1" with a 0% badge for the
  minutes it took. The storage driver now counts the objects it works
  through, a running operation carries `objects_total` / `objects_done`, and
  the operations centre says "25 of 100 items"
  ([#67](https://github.com/BRF-Tech/filex/pull/67)). Queued renames,
  restores and purges count their objects too.
- **The explorer says what became of what you asked it to do**
  ([#59](https://github.com/BRF-Tech/filex/pull/59), [#65](https://github.com/BRF-Tech/filex/pull/65), [#69](https://github.com/BRF-Tech/filex/pull/69)):
  - A paste, drag-move, duplicate or copy the server refused is said on
    screen, in the reader's words; it went out as the explorer's `error`
    event only, which looked exactly like a change that worked. A refusal
    that arrives after its dialog has closed is said as a notice.
  - A dialog whose request is on its way - Rename, New folder, Delete,
    Delete permanently, the archive dialogs, the destination picker, an
    app's screen, the converter - keeps its buttons shut with a label that
    says so, and Escape, a click outside and × wait for the answer. A long
    one that can be stopped (the converter) asks inside the dialog before it
    closes. Paste, Restore and a multi-item Download take one press at a
    time too.
  - Undo says "Undoing…" and then what it did: queued, undone in part
    ("2 of 5"), or undone.
  - A listing being read again draws a thin moving bar over the rows it
    will replace, and fades them, after 300 ms.
  - "Catalog everything" follows the scan it started and says how it ended.
  - "Move to…" says a move is queued only when it was, keeps the Undo of a
    move made at once, and says so when the items are already there.
  - Restore from the trash says what did not come back, and why.
  - An upload whose bytes are all in filex says "Saving to the storage…"
    while the server writes it, instead of 100%.
  - The converter names its step (reading, converting, saving) and waits up
    to 30 minutes; ⌘K's "Everywhere" says when it could not search; the
    archive preview says why a listing takes long; restoring a version and
    taking a snapshot say so while they run; an app's screen opens under the
    action's name while its answer is on the way.
- **The admin panel says what its long jobs are doing**
  ([#66](https://github.com/BRF-Tech/filex/pull/66)): "Sync now" says
  "started" or "already running" and when the scan is done, failed or
  stopped (`GET /api/admin/storages` carries `running`); "Rebuild index"
  follows the rebuild to its end and says it failed, and why, when it did
  (search stats carry `last_rebuild_error` / `last_rebuild_finished_at`);
  deleting a large storage says it is still deleting until it is gone;
  replica "Fix all" queues each retry once (`{queued, already_queued}`); a
  storage plugin's install waits up to 180 s.
- **Desktop: the app says what sync, downloads and drag-out are doing**
  ([#68](https://github.com/BRF-Tech/filex/pull/68)):
  - A folder no pass has finished yet reads "waiting for its first check",
    and every phase keeps its figures ("listing the server - 48,211 items so
    far", "97 changes to make").
  - An engine that stopped on its own is started again after 5 s, 15 s, a
    minute, then every five minutes, and the line shows the engine's own
    reason. A restart that brought no engine up is tried again.
  - A download to disk moves the dock / taskbar bar, and a notification says
    "Downloaded" (click to show it in its folder) or "Download failed".
  - A folder dragged out counts its files, at most four reports a second,
    and **Stop** ends the file in flight too; two drops at once each have
    their own Stop.
  - The tray tooltip carries the pause, sync's state and the unread count.
- **Embedding: `ExplorerConfig.dragOut.stop` and a `files` count on the
  drag-out progress** let a host stop a drag-out and say how far it got
  ([docs/INTEGRATION.md](./INTEGRATION.md)).
- **An embedded explorer whose `apiBase` has a path keeps it for every
  address it builds.** A multi-selection ZIP and the drag-out link
  (`/z/<ticket>`) kept only the origin, so behind a host proxy such as
  `/files-proxy` they went to the host's root and failed; the "open in a new
  tab" editor route and the connection guides (the WebDAV address, the
  Cyberduck path) dropped it too. The server's relative addresses
  (`thumb_url`, a download's `/z/<ticket>`) are joined onto `apiBase`
  ([INTEGRATION.md](./INTEGRATION.md#serving-filex-under-a-sub-path)).

### Changed

- **`POST /api/files/ops` takes only `copy`, `move` and `delete`.** Every
  other kind answers `400 BAD_KIND`; renames, restores and purges are asked
  for through their own endpoints with `queued=1`, which run their checks
  first.
- **A refused change is said inside the explorer as well as emitted as
  `error`.** Hosts that already show their own message set
  `refusalToasts: false` ([docs/API.md](./API.md)).
- **The trash listing pages over everything the caller may see.** It is
  ordered by deletion time (`deleted_at DESC, id DESC`), and a `limit` above
  500 reads as 50.
- **Mount as a drive uses `~/filex-drives/filex-<storage>` on macOS and
  Linux.** macOS mounted onto `/Volumes/filex-<storage>`, a folder a user
  cannot make, and ignored the failure; it now says so, in the app's
  language, when the folder cannot be made. Not yet verified end to end on a
  Mac.

### Fixed

- **Switching to Starred, Recent, Shared or a tag could be undone by a
  refresh.** The view's address moved only when its rows arrived, so a
  refresh landing in between (a live change, the Refresh button, the end of
  an action) loaded the view being left again, and when that answer came
  last it was put back on screen under a panel that no longer marked the
  view you had clicked. The address now moves with the click.
- **A folder renamed, moved, trashed, restored or purged on an object store
  could be left half done when the client stopped waiting.** These ran
  under the request's context, which a closed tab or a proxy that stops
  waiting (nginx after 60 s, Cloudflare after 100 s) cancels. The folder was
  left in two places and a retry was refused by the half that had arrived.
  Once its checks have passed, the change now runs to the end whether or not
  anybody is still waiting - in the explorer, the trash, the agent surface
  (`/api/ai/move`, `/api/ai/delete`) and WebDAV (`DELETE`, `MOVE`)
  ([#60](https://github.com/BRF-Tech/filex/pull/60)).
- **A purge queued before its entry was restored no longer deletes the
  restored folder.** The job hard-deleted whatever it found under the id,
  live or not, with its shares, tags, comments and versions. A purge of an
  entry already gone counts as done instead of failing on "no rows".
- **A storage made from the admin panel is switched on.** The form never
  sends `enabled`, and the server saved it disabled: no scan, and "Sync now"
  answered 404.
- **Saving a storage no longer restarts its scan for nothing** - including
  on PostgreSQL and MySQL, which spell a stored configuration differently -
  and a new setting stops every scan of the old one.
- **Deleting a large storage finishes, and a failed delete leaves the
  storage as it was.**
- **The trash listing reached only its first page** when some entries were
  hidden from the caller (it reported the page's length as the total).
- **"Deleted by" is not given to a person for files the scanner had found
  gone** under a folder that was deleted in place.
- **The archive dialogs and "Send by email" send once per press**, Enter
  included; "Extract here" says it is reading the archive and a second
  choice starts nothing.
- **A file request says the server is saving, and a drop that arrived is not
  called failed.** The server finishes writing a drop that has fully arrived
  when a proxy gives up, and a gateway timeout after every byte was sent
  reads "Sent, but the server did not confirm it in time". A `502` still
  reads as a failure: a down server answers it after the last byte too.
- **The archive preview** no longer lets a listing land on the next file's
  screen, and closing it stops the server's download.
- **An error message that mentions "already exists"** is no longer read as
  "something with that name is already there" unless it is the queue's own.
- **The operations centre** no longer announces a colleague's job to an
  administrator's explorer, names a restore or purge by its item count, and
  shows a count only where it can move.
- **Desktop:**
  - moving the filex folder runs once, and sync stays off until it ends;
  - "Open with filex" opens a document once, and finds the synced copy under
    a path with a Turkish "İ" in it (it could open the wrong file);
  - "Stop syncing" and "Keep online only" stop the pass under way;
  - a pair list that could not be read no longer stops every sync watcher;
  - Settings stops losing clicks while sync prints;
  - the update card says "Up to date" only after a check, and the tray's
    Settings opens Settings on a hidden start.
- **Uploading from a page opened over plain http at an address other than
  localhost did nothing.** The explorer named every upload with
  `crypto.randomUUID()`, which browsers only define on https or localhost;
  picking a file stopped at "crypto.randomUUID is not a function" before a
  byte was sent.
- **Apps:** an upgrade that failed and was rolled back no longer breaks the
  app at the next restart; an app that is switched off stays off after an
  upgrade; an upgrade interrupted by a crash is undone at the next start;
  and a language pack installs from its manifest address alone (the install
  looked for a module address, which a pack does not have).
- **Two source files were stored as binary by git** (a raw NUL byte), so
  their changes could not be reviewed; every source file is now checked.

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0470---2026-09-26).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.47.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.47.0`

## Earlier releases

The 127 releases before v0.47.0, in brief. Full notes are on GitHub.

| Version | Date | What changed |
|---|---|---|
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

<small>Last refreshed 2026-10-26 from 147 published releases.</small>
