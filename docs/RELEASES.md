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

::: tip Latest - v0.54.0, 8 October 2026
The server does the server's work: the rules, numbers and sentences the web app, the desktop app and the packages used to keep copies of now come from the server, so every surface says the same thing in the reader's language, and every refusal carries the server's own sentence next to a stable code. Notifications reach a phone or a browser with filex closed (Web Push, iPhone and iPad from iOS 16.4), with the same kinds, mutes and digest as the bell, and a notification is translated at its last stop, for whoever receives it. The ONLYOFFICE editor opens in the person's language (FILEX_ONLYOFFICE_LANG pins one), the desktop app follows its account's language, and the CLI installs with winget. The vault (encryption level 3) arrives in the explorer and the CLI behind FILEX_E2E_VAULT, off by default. Search, Recent, Starred, Shared with me and tag pages are narrowed and sorted on the server, and every listed link says where it stands. The presigned S3 multipart upload is gone; the staged upload works on every driver. Embedders: the 0.54 packages need a 0.54 server. Security hardening across ONLYOFFICE save callbacks, apps, encrypted folders, share e-mails, quotas and the S3 gateway; upgrading is recommended.
:::

```bash
docker pull ghcr.io/brf-tech/filex:slim-v0.54.0
docker pull ghcr.io/brf-tech/filex:full-v0.54.0
```

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

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.54.0) · `ghcr.io/brf-tech/filex:slim-v0.54.0`

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

## v0.46.1

<span class="filex-release-date">26 September 2026</span>

## What changed

### Fixed

- **The version line no longer runs off the menu.** The server reports its
  version as the release followed by the full commit hash and the build time,
  and the line printed all of it: the explorer's avatar menu grew a sideways
  scroll bar, the sign-in page carried the 40-character hash twice, and the
  About page ran it off its card. The account menus, user settings and the
  sign-in page now name the release only (`filex v0.46.1`); the About page
  shows the release with the commit shortened to seven characters and the
  build date under it, and its copy button still copies the whole string.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0461---2026-09-26)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.46.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.46.1`

## v0.46.0

<span class="filex-release-date">26 September 2026</span>

## What changed

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

- **New document: name the file anything - `LICENSE`, `Makefile`,
  `test.conf`, `example.custom` (#56).** The dialog drew the type's extension
  as a read-only suffix, so a Plain text document was always `<name>.txt`. The
  name field now holds the whole file name: the type prefills it
  (`Untitled.txt`), focusing it selects the stem like a rename, and a type
  switch keeps what you typed (only swapping the previous type's default
  extension). The type still decides the contents and the editor - `LICENSE`
  made as Plain text opens in the text editor, `README` made as Markdown in
  the markdown editor. Office documents and diagrams keep their extension,
  because their editors find them by it: the dialog says "will be created as
  `report.docx`" instead of locking the field, and an empty `x.docx` made as
  Plain text is refused. Behind it: `POST /api/files/manager?action=newfile`
  takes `exact_name: true` (without it the extension is appended exactly as
  before, so older clients are unchanged), `newdoc_types` rows carry
  `ext_required`, and a refused borrowed extension answers `400 EXT_NEEDS_TYPE`. Name checks - a leaf, no `..`, reserved names - are as
  strict as they were.
- **A text file whose name has no known extension opens as text and can be
  saved (#56).** `LICENSE`, `NOTICE` or `notes.custom` fell through to the
  viewer's Download fallback, and save-text - which allows by extension -
  refused them with `415`. The viewer now opens such a file as plain text when
  the server's mime for it says text, and save-text saves an existing file the
  catalogue calls text (a file it calls anything else, or a path with no
  catalogue row, is refused as before). The listing draws such a file with the
  text icon and names it "Plain text" instead of "?" and "-".

### Fixed

- **A new document opens once in the desktop app.** It came up twice: in its
  own window and in the viewer over the explorer, because creating a document
  opened the in-page viewer before telling the app. It now takes the same path
  as every other open (the app's window only). Found while building #56.
- **An app screen speaks the language ON SCREEN, not the account's.** An
  embedded explorer (`<filex-explorer>` with `config.locale: 'tr'`) draws the
  language its host page chose, and nobody asked the account - whose language
  defaults to English. The server told every app the ACCOUNT's language
  (Accept-Language ranks below it on purpose), so an app's plain strings came
  out English in a Turkish popup: the signing app's wizard asked "Identity"
  with "One signer per line…" under it. The explorer now names its language on
  every app call (`?lang=` on `…/run`, the view `GET`, every `…/event`), and
  the server puts that explicit choice first - then the account's, then
  Accept-Language, as before. The standalone app is unchanged: its language
  and the account's are the same one.
- **The operations tray says an app's job in the reader's language.** The job
  row froze the action's label and the app's result in ONE language when the
  job was created, so a Turkish embed read "Convert…" and an English result.
  The row now keeps every language the app wrote and the tray's poll
  (`/api/files/ops?lang=`) reads them in its reader's - the label, the result,
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
  members off their storage - most of the time on a remote store - was worth
  0-10% of the bar, counted per member, and the built-in ZIP and the write of
  the result reported nothing. Measured in production, 175 files off S3: two
  minutes between 0% and 9%, and the person who started it saw no sign of it.
  Each phase now moves in bytes while it runs - reading 0-45%, compressing
  45-90%, writing 90-99% - and creating or extracting an archive opens the
  operations panel on the job, so its name, progress bar and Cancel are on
  screen when the dialog closes rather than a small badge in the corner.

- **Typing in the code editor no longer triggers the explorer's shortcuts.**
  In Chrome and Edge the editor types through the browser's EditContext, so
  its focused element is not a text field, and the explorer took the
  keystrokes: `I` opened the info panel, `S` starred the file, Space opened
  quick look and Backspace went up a folder - "MIT License" typed into a new
  file arrived as "MLcene". The shortcut guard and quick look's arrow keys now
  treat the editor (and any `role="textbox"`) as typing. Found while building
  #56.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0460---2026-09-26)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.46.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.46.0`

## v0.45.1

<span class="filex-release-date">25 September 2026</span>

## What changed

0.45.0 as it was meant to ship. **v0.45.0 was tagged but never published**:
one of its unit tests read a file only the maintainers' private repository has,
the release workflow's test gate failed on it in the public tree, and nothing -
no images, binaries, npm packages or desktop apps - went out. Everything
0.45.0 carried is below, in this release, together with the fix and a new
release gate that runs the web tests in the public tree before anything is
tagged.

### Security

- **A token confined to one folder could reach files outside it through the
  app doors and the selection archive.** A `root:`-scoped API token (the kind
  a host application hands an embed, confined to the tenant's folder) could
  run an app on any file of the storage - its result written next to that
  file - and put files from outside its folder into a selection archive
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
  at the drop it is checked again as its owner - account, token, tenant host,
  tenant storage and at least viewer on the file. Every drag-out download is in
  the audit log (`file.download_link`, `file.download_link_refused`); the link
  itself never is. Folders are refused (`409 IS_FOLDER`). A cookie session
  keeps its plain download URL.

- **Desktop: Mount as a drive.** One button in Settings attaches a filex
  server as a drive of the operating system over WebDAV, and one detaches it -
  the one-click counterpart of the connection guide, whose commands now come
  from the same code. The account's own API token is the WebDAV credential and
  reaches the OS on stdin, never on a command line. On Windows it uses
  WNetAddConnection2, starts the WebClient service if it is stopped, and says
  up front what WebClient needs (HTTPS for a password). The macOS and Linux
  paths are there but not yet verified on those systems (#36).
- **⌘K search results you can act on.** A result in the palette's
  *Everywhere* group has a Download button (a folder arrives as one zip) and
  drags out like a row of the file list - through the desktop app's own drag,
  or as the browser's single-file download where the session is a cookie. A
  drag let go on the palette itself no longer starts an upload (#47).
- **Desktop: ⌘K searches every account on the rail.** Results are grouped
  under one badge per account, the one you are looking at first. Each account
  is searched, downloaded and dragged with its own sign-in, and opening another
  account's result switches the rail to it. Embedders get the same through the
  new `accountSearch` config hook
  ([docs/INTEGRATION.md](./INTEGRATION.md)) (#47).
- **`pnpm release X.Y.Z` cuts a release in order, and a red gate stops it.**
  The release process in [docs/CONTRIBUTING.md](./CONTRIBUTING.md) is one
  command now: preflight, the README/screenshot/docs audit, the doc gates, the
  version stamp, the whole pre-tag chain (both images, three database engines,
  TZ=UTC, Cypress, Playwright), the public export and its guards, then the
  signed tags, the push, the workflow's output and the deploy. No option skips
  a gate. The tool never signs, pushes or deploys: at those steps it prints the
  commands, and on `--resume` it checks what was done - each tag's signature
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
  save, and answering it committed an empty upload over the file - so every
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
  ([docs/LAZY-CATALOGUE.md](./LAZY-CATALOGUE.md)).
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
  transfer is now bounded per attempt - connecting (DNS included), the TLS
  handshake, the answer after the request, an answer that stops arriving, an
  upload the store stops taking - and retries are held to a time budget: with
  the defaults a dead store is reported within 15 s as
  `s3 endpoint … is unavailable: <reason> (N attempts in Xs)`. A transfer that
  keeps moving is never cut; copies, renames and multipart completion wait up
  to 10 minutes. A certificate that does not verify is no longer retried, and
  uploads over 2 MB (`Expect: 100-continue`) have an answer limit too. New
  storage settings `attempt_timeout_s` (10), `max_attempts` (6) and
  `total_timeout_s` (15) appear in the storage form; see
  [docs/STORAGE.md](./STORAGE.md).
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
  [docs/APP-PLUGINS-API.md](./APP-PLUGINS-API.md) lists the theme tokens
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
  in both copies was synced by two apps that did not know about each other -
  duplicate uploads, conflict copies, a delete reaching the wrong side. A
  `.deb`, `.rpm`, AppImage or AUR copy now says so in Settings, with the
  commands to keep one.
- **`filex self-update` leaves a distribution package alone.** A filex in
  `/usr/bin` (or `/usr/sbin`, `/bin`, `/sbin`) was put there by a package
  manager, and is now treated like a Homebrew, winget or Snap install: no
  self-replacement, upgrade with the package manager. A hand install in
  `/usr/local/bin`, where [docs/CLI.md](./CLI.md) puts it, is unchanged.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0451---2026-09-25)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.45.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.45.1`

## v0.44.2

<span class="filex-release-date">25 September 2026</span>

The release 0.44.0 was meant to be, and the one to install the desktop app from, including through Homebrew, winget and Snap. 0.44.0 and 0.44.1 both published their container images, npm packages and CLI binaries, then stopped at the winget step before the desktop packages and the stores. Everything 0.44.0 describes is in 0.44.2.

## What changed

0.44.0 as it was meant to ship, and **the one to install the desktop app
from**. v0.44.0 and v0.44.1 both published their npm packages, container
images and CLI binaries and then stopped at the winget step, so neither
shipped the desktop packages or reached the stores. Everything 0.44.0
describes below is in 0.44.2.

### Fixed

- **The release reaches the desktop packages and the stores.** GoReleaser
  accepts a publisher's repository token only as a single `.Env` variable
  reference - nothing around it, not even an `index` lookup - and checks it
  only when it publishes. The winget token is written that way now, and the
  template test checks every token against GoReleaser's own rule and that the
  release hands each variable to the GoReleaser step.
- **A desktop page that cannot load now stops the release.** Nothing ran
  the desktop unit tests before, which is how 0.43.x shipped a main window
  whose script never parsed; the release's gate now runs them. Found and
  proposed by Berk Başarır ([#52](https://github.com/BRF-Tech/filex/pull/52)).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0442---2026-09-25)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.44.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.44.2`

## v0.44.1

<span class="filex-release-date">25 September 2026</span>

Tagged, but it stopped at the same winget step as 0.44.0; its container images, npm packages and CLI binaries were published. Use 0.44.2.

> ⚠ **Use [v0.44.2](https://github.com/BRF-Tech/filex/releases/tag/v0.44.2) instead.** v0.44.1 published its container images, CLI binaries and npm packages, then stopped at a packaging step of the release (a GoReleaser template), so it has **no desktop app and no store packages**. v0.44.2 carries everything in this release plus those. Nothing published here is broken; it is just incomplete.

## What changed

0.44.0 as it was meant to ship, and **the one to install the desktop app
from**. v0.44.0's npm packages, container images and CLI binaries went out,
but its release stopped at the winget step, so its desktop packages and every
store and package-manager step behind it never ran. Everything 0.44.0
describes below is in 0.44.1.

### Fixed

- **The release reaches the desktop packages and the stores again.** The
  winget and Homebrew-tap sections of `.goreleaser.yml` guarded their tokens
  with a template function the release's GoReleaser does not define
  (`envOrDefault`); it was evaluated only when publishing, after the Release,
  npm and the images were out. They use `&#123;&#123; index .Env "NAME" }}` now, and a
  test fails on any template function GoReleaser does not have.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0441---2026-09-25)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.44.1) · `ghcr.io/brf-tech/filex:slim-v0.44.1`

## v0.44.0

<span class="filex-release-date">25 September 2026</span>

Big local storages open at once: the new lazy catalogue lists a folder straight from disk and catalogues it behind the listing, with a throttled pass that fills in the rest (or only the folders people visit). Archives gain 7z, the TAR family and password-protected ZIP and 7z, contributed by Alex (@ahjephson) and hardened on the way in. The desktop app and the CLI install with Homebrew, winget and Snap, and the desktop app's blank main window from 0.43.0-0.43.2 is fixed.

> ⚠ **Use [v0.44.2](https://github.com/BRF-Tech/filex/releases/tag/v0.44.2) instead.** v0.44.0 published its container images, CLI binaries and npm packages, then stopped at a packaging step of the release (a GoReleaser template), so it has **no desktop app and no store packages**. v0.44.2 carries everything in this release plus those. Nothing published here is broken; it is just incomplete.

## What changed

Big storages open at once, archives come in every common format, and the
desktop app arrives through the stores. A **lazy catalogue** for local
storages (`sync_mode: lazy`, the idea from Alex / @ahjephson in #45) lists a
folder straight from disk the moment it is opened and catalogues it behind the
listing, with a throttled pass that fills in the rest - or, for a huge
archive, only the folders people visit. **Archives** gain 7z, TAR and its
compressed forms and password-protected ZIP and 7z (RAR too, where the
server's 7-Zip has it), contributed by Alex in #48 and hardened on the way in. The **desktop app and the CLI** ship
through Homebrew, winget and Snap, with a Microsoft Store build and an `.rpm`
beside them.

⚠ **Upgrade the desktop app if it is on 0.43.0 - 0.43.2**: its main window
stayed blank (see *Fixed*). ⚠ On Linux the desktop app's command is now
`filex-app`. ⚠ The desktop app needs macOS 13 or later (Electron 44). ⚠ The
full container image moves to Alpine 3.24 and grows by about 390 MB
unpacked (see *Changed*).

### Added

- **The desktop app and the CLI in package managers.** The desktop app is
  `filex-app` everywhere and the CLI is plain `filex`: `sudo snap install filex-app` (also in Ubuntu's App Center), `brew install brf-tech/filex/filex-app` and `brew install brf-tech/filex/filex` from the new
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
  reports the release, and no update policy ever replaces the binary - the
  package manager would otherwise keep recording the old version and write
  over ours at its next upgrade (a snap is read-only). **Ops → Updates** names
  the manager and shows its command instead of **Upgrade now**.
  `FILEX_INSTALL_MODE` also takes `package`, `homebrew`, `winget` and `snap`
  (a `.deb`/`.rpm`/AUR package can declare itself). See
  [UPDATES.md](./UPDATES.md#package-manager-installs).
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
    a special file - also when 7-Zip shows one only by its file mode (a
    `zip -y` or `7zz -snl` symlink, a FIFO or device in a TAR, a RAR5 file
    copy) - or a member that does not declare its size. The size limit stops
    a TAR-family archive at the exact byte, a gzip whose trailer lies
    included, because filex reads those itself. The archive type comes from
    the file name, never from its bytes; a password reaches 7-Zip on its
    standard input, never on its command line; and every member lands
    through the same gate as any other write, so it cannot replace a document
    an app has locked or land in filex's own folders.

- **Lazy catalogue for big local storages - `sync_mode: lazy`**
  ([#45](https://github.com/BRF-Tech/filex/issues/45); the idea is Alex's,
  @ahjephson). Every other mode catalogues a storage by walking all of it first;
  on a multi-terabyte NAS that is hours before the first folder is right. Now
  the folder somebody opens is listed straight from disk at once, with what the
  catalogue already knows laid over it, and is catalogued first in the
  background. Two behaviours per storage: **click first, fill in the
  background** (the default - a slow pass catalogues the rest, slowing down
  while people use the storage, honouring scan exclusions and carrying on after
  a restart) and **only on open** (nothing runs in the background; an
  administrator can catalogue everything once). Opened folders are watched for
  outside changes within a budget (`lazy_max_watches`, `lazy_watch_ttl`), and a
  desktop sync pair gets its whole subtree catalogued and kept current. ⚠ A
  folder nobody visited is never treated as deleted: rows are only removed from
  a folder that was just listed in full, each one confirmed gone. See
  [docs/STORAGE.md](./STORAGE.md#lazy-catalogue) and the design in
  [docs/LAZY-CATALOGUE.md](./LAZY-CATALOGUE.md).
- **The storage form offers the sync mode.** It could only be set through the
  API; the new and edit forms now have **Sync mode**, and the lazy catalogue's
  settings drawn from the driver descriptor (`lazy_fields`).
- **Search, folder sizes and drive usage say when they do not cover a whole
  storage.** One line above the listing and the search results names the
  reason (a first sync still running, a lazy catalogue still filling, a storage
  catalogued only on open); a folder whose size leaves something out reads
  `≥ 1.2 GB`, or `-` when nothing below it is catalogued yet; Home's drive card
  says *at least … used*. The storage page shows the catalogue's progress, the
  background pass and the watch budget.

### Changed

- **Linux: the desktop app's command is now `filex-app`,** and so is its
  package (`.deb`, `.rpm`) and desktop entry - `filex` is the CLI's name, and
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
- **Desktop app (0.43.0 - 0.43.2): the main window runs again.** An HTML
  comment in the Settings template quoted a function name in backticks inside
  a JavaScript template literal, which ended the literal early; the browser
  rejected the page's whole script, so after signing in the window drew its
  static frame and nothing worked. A test now parses every inline script of
  the desktop pages.
- **Desktop app (0.43.0 - 0.43.2): Settings shows your synced folders again.**
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
  syncing - the desktop app and `filex sync run` in a terminal, or an
  installed copy of the app and the Microsoft Store one, which reads the same
  sign-ins and, living in its own virtualised profile, does not see the other
  copy's single-instance lock. Both planned from the same history, so files
  were uploaded twice, conflict copies appeared out of nothing and a delete
  could travel to the side that did not ask for it - silently. Every run now
  holds an operating-system lock on its pair (`~/.filex/sync/locks/`,
  `LockFileEx` on Windows, `flock` elsewhere), released by the OS however the
  process ends. A pair another process holds is left untouched: `filex sync run` says `lock: busy`, syncs the rest and exits with status 4; `--watch`
  waits and takes the pair over when the other process stops; the desktop app
  shows *Another filex on this computer is syncing this folder* under it
  instead of an error, and does not restart its engine.

- **During a storage's first sync, a folder shows everything in it.** A
  partly catalogued folder - above all the root of a big storage - used to list
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
  Electron 31, whose support ended on 2025-01-14 - every Chromium security fix
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

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0440---2026-09-25)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.44.0) · `ghcr.io/brf-tech/filex:slim-v0.44.0`

## v0.43.2

<span class="filex-release-date">24 September 2026</span>

The release that 0.43.1 was meant to be: container images for everything in 0.43.0 (whose images never published), the storage-credential scrub, Berk Başarır's per-tile thumbnail rendering and a desktop sign-in fix for servers behind a private CA. Its desktop packages have a blank main window - use 0.44.0.

> [!WARNING]
> **Desktop app: don't install the v0.43.0-v0.43.2 desktop packages.** Their main window stays blank - a backtick inside an HTML comment in the Settings template ends a template literal early, so the window's script never runs. The fix shipped in [v0.44.2](https://github.com/BRF-Tech/filex/releases/tag/v0.44.2): install the desktop app from there (the in-app updater offers it too; on macOS, install it by hand - the Mac build is not code-signed yet). The server, the CLI, the container images and the npm packages of v0.43.2 are not affected.

## What changed

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
Everything the 0.43.0 notes below describe is in 0.43.1 - **including its
security fixes, which an install that runs the image gets only now** - and two
more fixes: from Berk Başarır, a big folder no longer re-renders itself for
every thumbnail that arrives, and the desktop app signs in to a server behind a
private CA.

### Security

- **Container, Helm and app-store installs get 0.43.0's security fixes only
  with this release - upgrade promptly.** With no v0.43.0 image, every install
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
  no longer published. No filex install used it - it only ever wrote demo
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
  build reaches outside `packages/` and `web/` - imports, their type
  declarations and the paths they read - and fails if either image does not
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
  home CA - trusted by the browser, so the browser half worked - failed there
  after the server had already spent the code. It goes through Electron's
  network stack now, like every other request the app makes, and a test fails
  if anything in the main process uses the other client again
  ([#36](https://github.com/BRF-Tech/filex/issues/36)).
- **A release page that has to be shortened ends on a line break.** The
  GitHub release body is capped, and when an entry's opening paragraph alone
  ran past the cap the cut fell mid-word; it now falls back to the last line
  break.

### Tests

- **"Empty trash" leaves what is deleted while it runs - now proven on a run
  longer than one batch, and on one that waits its turn.** v0.43.0 already
  bounds an empty by the moment it was asked for: the ops row's `created_at`,
  on the database's clock, which a restart does not lose. Berk Başarır's field
  report - on the pull request's earlier runner, a 1 h 48 min run purged
  61,845 rows of a total of 61,844, the extra one a file a member deleted six
  minutes before the end - came with a test that deletes a file while a run of
  more than one batch is under way. It now runs against the job at both the
  trash and the ops level, beside a new one for a run queued behind another
  tenant's ([#47](https://github.com/BRF-Tech/filex/pull/47)).
- **The viewer audit opens draw.io and ONLYOFFICE when they answer.** e2e 100
  waited for a capability state of `reachable`, which the server has never
  sent (it says `ok`), so both rich viewers were skipped on every run, even
  with the services configured.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0432---2026-09-24)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

## Security advisories

- [GHSA-gj84-9xx7-2g9f](https://github.com/BRF-Tech/filex/security/advisories/GHSA-gj84-9xx7-2g9f) - an unsigned ONLYOFFICE save callback was accepted (High, 7.5)
- [GHSA-55vh-f285-5wv3](https://github.com/BRF-Tech/filex/security/advisories/GHSA-55vh-f285-5wv3) - a restricted API token could issue credentials wider than itself (High, 8.8)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.43.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.43.2`

## v0.43.0

<span class="filex-release-date">24 September 2026</span>

Apps. filex gains a second kind of plugin: a sandboxed WebAssembly app that adds actions to the file menu, screens filex draws for it, and public pages for people without an account - installed from a GitHub repository, a file or a URL, and always through a review of every permission it asks for. Two ship alongside this release, each as a public repository you can read and fork: e-Signature (filex-sign) sends a document round for signature - the boxes defined, then placed, a PIN-protected link for a signer without an account, the document frozen for everyone while it is out if you ask for it, a PAdES signature from the instance’s own signing authority, and a final seal by filex itself whose SHA-256 goes to every party - and Convert (filex-convert) turns a file into another format with the engines the server has, asking where to put the result when the file sits on a storage it cannot write to. An app can lock a file until it lifts the lock, keep per-file state and list its own work, fetch a pinned asset once, share the file its own job is writing, send addressed notifications and mail, open a home page of its own under Apps, and be woken on the hour to act at the minute it named; the SDK ships a test kit that runs before the module is built.

Language packs. A manifest with nothing that runs adds a language to filex - no module, no Go, no release - and translates the explorer, the admin panel and the public pages alike, with its coverage of this version shown beside it. The text filex’s server writes goes through the same catalogue: e-mails, notification phrases, the no-JavaScript pages behind a link and the install review all answer in the reader’s language. Plural forms follow CLDR, so a pack may write the forms its language actually has. And the interface lays itself out right to left for Arabic, Hebrew, Persian and Urdu - mirrored where mirroring is right, never in document space or in machine text. A template repository walks a translator from export to install, and Spanish, German and French ship as examples.

Around them: a download share, a file request and an app’s page are one branded shell with one PIN gate, and five wrong PINs shut any of them for ten minutes. People see the links they created under My shares and can read their PINs back. The Appearance screen composes themes for the whole instance, sign-in page and share visitors included. Admin → Identity providers now really drives sign-in, with a Test now that tests it. Tags are personal or shared with the team. Preferences follow the person rather than the browser, and a folder can remember the view and sort you left it in. Desktop folder sync is live in both directions - an edit on either side arrives in about a second instead of on the next 30-second lap - with bandwidth limits, a sync window, and a first run that holds back a re-upload and asks. The bell carries its count, and everybody can read all of their notifications. Symlinked folders inside a local storage open, a link that cannot is badged with the reason, and follow_symlinks decides where the storage ends. Every table in filex is the explorer’s table, in the admin panel too, each row ending in one pinned Actions menu.

Security - upgrade promptly. With ONLYOFFICE configured, the save callback was accepted without a JWT even when filex held a secret, so anybody who could reach the server could have a file overwritten; it is now refused. A Document Server running with JWT_ENABLED off must have it turned on, or its saves will fail. Separately, every door that issues a credential - desktop pairing, self-service API tokens, S3 access keys, NFS exports, SSH keys - trusted the account behind the caller rather than the caller, so a read-only or folder-confined token could mint a full-rights one; they all refuse now with 403 token_ceiling, and desktop pairing takes a signed-in browser only. An API token with no scopes no longer means every scope. Tags were visible to every account on the server, an app’s freeze held only in the explorer, anybody who could write could write into filex’s own folders, a local storage could be escaped on Windows with a backslash path, and symlinks leaving a local storage were followed by everything including recursive delete - all fixed.

Upgrade notes: migrations 00042-00055 run on the first start. Tokens created with no scopes are rewritten to an explicit list that includes admin - review and narrow them. Identity providers saved on the admin page before this release come back switched off, marked for review. Existing tags become team tags, and a client that sends no kind makes personal ones. Custom CSS stays off until you switch it on again under Appearance, and custom_css is gone from GET /api/branding. Live desktop sync needs the new desktop build, and a reverse proxy must pass WebSocket upgrades on /api/ws. A rename onto a taken name answers 409 instead of replacing the file; an agent’s move still takes a free name. A program that wants the 202 “preparing” answer must ask for it with X-Filex-Accept-Prepare: 1. Symlinks already inside a local storage are re-catalogued on the next scan: an in-root link gains its contents, an out-of-root one stops being indexed, scanned and counted against quota unless follow_symlinks is on.

> [!WARNING]
> **The v0.43.0 container images were not published** - the image build failed on this tag. If you run filex in Docker, use **v0.43.2**, which carries the same changes with working images (v0.43.1 was tagged but never published). The binaries, desktop packages and npm packages on this page are complete.

## What changed

The release that makes filex extensible. **Apps** are a second kind of
plugin - a sandboxed WebAssembly module that adds things to *do* with
files - and two ship alongside it as public repositories: **e-Signature**
and **Convert**.

A **language pack** is an app with nothing that runs, so filex can be
translated without waiting for a release; the text the server writes - mail,
notifications, the pages behind a link - comes from the same catalogue; and
the interface lays itself out **right to left** for the languages that read
that way.

Alongside them: desktop folder sync is now **live** in both directions, the
explorer's table is the only table left in the product, an operator composes
the instance's **theme**, tags are personal or shared with the team, and
every door that issues a credential refuses to issue one wider than the
caller.

> ⚠⚠ **Security - upgrade promptly.** Every filex up to v0.42.2 with ONLYOFFICE
> configured accepts an unsigned save callback, which lets anybody who can
> reach the server overwrite a file (Security). filex has never
> enabled the editor without a JWT secret - an install with no secret has
> editing off and is unaffected - so the one thing to check is the other
> side: a Document Server running with `JWT_ENABLED` off sends unsigned
> callbacks, and after this upgrade its saves fail. Turn JWT on there, with
> the same secret filex holds.
>
> ⚠⚠ **A narrow API token could mint a wide one.** Every door that issues
> a credential - the desktop pairing endpoint, self-service API tokens, S3
> access keys, NFS exports and SSH keys - measured the account behind the
> caller instead of the caller itself, so a token restricted to reading, or
> confined to one folder, could create a full-rights credential for the same
> account and then use it (Security). Every one of them now
> refuses with `403 token_ceiling`. The desktop door was found and fixed by
> Berk Başarır ([#35](https://github.com/BRF-Tech/filex/pull/35)).
>
> ⚠⚠ **Desktop sync could replace real files - upgrade the server AND the
> desktop app.** A field report, traced and fixed by Berk Başarır
> ([#35](https://github.com/BRF-Tech/filex/pull/35)), found filex's own sync client writing a server's
> `202 "preparing"` status report to disk as the file and uploading it over
> the original, conflict copies nesting by the thousand, a stale mirror
> re-uploaded over a cleaned-up server, and a rename onto a taken name
> destroying the file that had it (Fixed). The server fix protects
> every client already installed; the new desktop build carries the rest.
>
> ⚠⚠ **A cache in front of filex could hand one person's answers to
> everybody.** API answers carried no `Cache-Control`, so a CDN rule that
> caches everything kept `GET /api/auth/me` for two hours and served one
> administrator's identity to every visitor. Every `/api` answer is now
> `no-store` unless it is one of the four answers that say who the instance
> is (Security). Found and fixed by Berk Başarır
> ([#41](https://github.com/BRF-Tech/filex/pull/41)).
>
> ⚠⚠ **The notification bell named files its reader could not open.** Queued
> copies, moves and deletes were announced to every account, a member of an
> RBAC storage read the names of files in folders they have no grant on, one
> person's "mark all read" read everybody's alerts, and a tenant admin could
> read every tenant's notification history (Security). Found and
> fixed by Berk Başarır ([#42](https://github.com/BRF-Tech/filex/pull/42), [#43](https://github.com/BRF-Tech/filex/pull/43)).
>
> ⚠ **Before upgrading, read Upgrade notes**: API/MCP tokens
> created with no scopes are rewritten to an explicit list that includes
> `admin` - review and narrow them; identity providers saved on the admin
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

- **Apps (app plugins) - a sandboxed plugin that adds things to *do* with
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
- **Language packs - filex can be translated without a release.** An app can
  add a language to filex, and it now does so end to end. A manifest that only carries `ui_locales` is a *language pack*:
  it installs from the manifest alone (upload, GitHub repository or URL - no
  module, no Go, no release), never starts a runtime, and is listed in
  **Plugins → Apps** as a *Language pack* with each language's coverage of the
  running version's catalogue (*Español - 97% translated · the rest shows in
  English*). Its language joins every picker - the settings dialog, the admin
  header, public share pages - and translates the explorer, the admin panel
  and the public pages alike; anything it lacks shows in English. Integrity
  moves to the manifest (the sha256 pin and, with `FILEX_PLUGIN_TRUSTED_KEYS`,
  the signature are over the manifest). A right-to-left language lays the
  interface out right to left (see *Right-to-left layout*). Format, grammar and limits:
  `PLUGIN-KIT.md` → *Writing a language pack*; a template repository,
  `BRF-Tech/filex-lang-template`, walks a translator from export to install.
- **Right-to-left layout.** Arabic, Hebrew, Persian, Urdu and every other
  right-to-left language a language pack adds now lays the whole interface
  out right to left - the explorer, the admin panel, the public share, PIN
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
  refresh, media controls and **document space** - a PDF page's signature
  boxes, images - never are. Commands, paths and URLs stay left to right;
  file names, paths and people are isolated (`<bdi>`), and number pairs,
  `tag:…` tokens and names inside a right-to-left sentence are isolated so
  they keep their order. List fields (recipients, extensions, tags,
  identities) accept the Arabic `،`, ideographic `、` and fullwidth `，`
  commas. A source check (`web/tests/quality/rtlLogical.test.ts`) keeps
  physical `left`/`right` from coming back. Docs: `docs/RTL.md`.
- **Appearance: the instance can wear your colours.** A new admin screen
  (**Appearance**, `/admin/appearance`) where an operator composes
  named themes - twelve colours per light/dark variant, a corner radius and a
  font stack - and picks one as the instance default. Ten more tokens are
  derived server-side and stored with the theme, so the browser does no colour
  maths at paint time, and the text colour on a coloured button is chosen by
  WCAG contrast rather than assumed to be white. Served to the login page and
  to anonymous share visitors as well, because a palette that stops at the
  sign-in screen is not branding. Themes export and import as one JSON
  document - the exported file *is* the upload body (migration 00051).
- **Desktop sync is live: an edit on either side arrives in about a second.**
  A save in the web app's text editor, an ONLYOFFICE save, an upload or a
  delete now reaches the synced folder on the desktop as it happens, and a
  save on the desktop reaches the server just as fast - instead of waiting for
  the engine's next 30-second lap. Measured on one Windows machine against a
  local server, six edits each at random moments: browser text save → file on
  disk 6.2-24.8 s (median 15.2 s) before, 0.19-0.22 s (median 0.20 s) now;
  ONLYOFFICE callback → disk 0.5-25.9 s before, 0.19-0.21 s now; local save →
  server 9.4-29.0 s (median 25.3 s) before, 0.43-0.61 s (median 0.46 s) now.
  `filex sync run --watch` subscribes to the server's change stream (the same
  WebSocket the web explorer uses, authenticated with the engine's own token
  through a ws-ticket) and watches the local folders with file-system events;
  a change reconciles just the folder it happened in (one listing, not a walk
  of the tree), and the `--watch` interval is the safety net - it no longer
  walks every pair, see *Changed*. `--live=false` leaves only the interval. The
  engine prints `live: connected|polling|offline - …` and the desktop app
  shows it as one word under each synced folder (*Live* / *Polling* /
  *Offline*). A newly paired folder starts syncing at once instead of on the
  next lap. See [Folder sync](./SYNC.md#how-fast-a-change-arrives).
- **Realtime: recursive, presence-less `watch` subscriptions.**
  `{"type":"watch","paths":[…]}` registers any number of roots per socket,
  authorised exactly like a room subscribe (confinement, RBAC, tenant), always
  acknowledged with `watching` (the capability probe for older servers), and
  delivered as `tree_change` frames with root-relative folders, coalesced like
  rooms and never dropped on a full queue. Folder-size refresh frames are not
  delivered to watches. See [Realtime](./REALTIME.md#watching-a-whole-tree-sync-clients).
- **Conditional uploads.** The multipart upload and the staged commit accept
  `expect` (`none`, or the `<size>:<last_modified>` a listing showed) and
  answer `412 PRECONDITION_FAILED` without writing when the file changed in
  between. The sync engine sends it on every upload, so a browser save that
  lands in the same second as a desktop save is kept beside it instead of
  being replaced. See [Uploads](./UPLOADS.md#conditional-uploads-expect).
- **A first sync that would re-upload a stale copy holds it and asks.** With no
  history, "new here" and "deleted on the server" look the same, and one client
  put 9,665 cleaned-up files back on a server. A first run that would upload
  more than 100 local-only files into a server folder that already has files
  holds them (and any file that differs) and runs the rest: `filex sync confirm <pair>` sends them, `filex sync discard <pair>` moves them to the local sync
  trash so the folder matches the server. `sync list --json` carries `hold_new`
  / `held`; the desktop app shows the count with **Upload them** and **Move to
  local trash**. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Bandwidth limits and a sync window.** `filex sync run --limit-down` /
  `--limit-up <KiB/s>` cap all transfers of a run together (the bodies are
  paced, never the connection), and `--window HH:MM-HH:MM` only syncs in that
  part of the day - outside it nothing talks to the server, not even the change
  stream, and a pass still busy when it closes stops cleanly and continues in
  the next window. The desktop app offers both as presets in Settings. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Transfer progress in bytes, with an estimate:** `transfer: 120/11704 (1.2 GiB of 52.6 GiB, about 8h 10m left)`, printed at least every 5 seconds. The desktop
  app shows it on each folder's line, in its own language. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **`GET /api/files/manager?action=changes&path=…&since=<cursor>`** answers "has
  anything under this folder changed since my cursor?" as `{ cursor, changed }`,
  from an in-memory change log fed by every write surface. Every doubt - no
  cursor, a restart, a cursor older than the log - is `changed`, and a change
  counts only if the caller can see what it touched. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Rescan one folder: `POST /api/admin/storages/{id}/sync?path=<folder>`.** The
  same walk over one catalogued folder, synchronously, answering `{path, scanned, added, updated, removed, reconciled}` (504 after ten minutes). Only
  rows inside the folder can be removed, a listing that failed part-way removes
  nothing, and no sync-run row is written. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Scan exclusions: tell a storage what not to catalogue.** A new storage
  setting, *Paths to exclude from scanning* (`config.scan_exclude`, on every
  driver), takes glob patterns relative to the storage root, one per line:
  `*` within a name, `**` across folders, and a pattern without a `/` names an
  entry at any depth (`.*` skips every hidden file and folder, `@eaDir` every
  Synology thumbnail folder, `*.tmp` every temp file), while `/build` or
  `downloads/incomplete/**` is anchored at the root. The scan does not go
  **into** a matching folder - a `.git`, a `.snapshots` or a download client's
  `incomplete/` costs nothing - and nothing matching is catalogued, indexed,
  thumbnailed or virus-scanned. One rule decides every walk that catalogues
  (the full scan, the one-pass object-store listing, a folder rescan - which
  refuses an excluded folder with 400 - the catalogue of a copied folder) and
  the `fsnotify` watcher, where a change to an excluded path no longer starts
  a scan. **A cost control, not an access control:** the files stay on the
  storage and are still served by path, over the file protocols and to the AI
  tools. Rows catalogued before a pattern was added stay as they are and are
  never moved to the trash for being unseen; filex's own names (`.filex-open`,
  `.keepdir`, an encrypted folder's marker) are outside the patterns; a pattern
  that would exclude everything, a `!`, a `..` or a broken glob is refused on
  save with `SCAN_EXCLUDE_INVALID` and a sentence naming the pattern in the
  reader's language - as is an empty or `/` storage root (`ROOT_PATH_FORBIDDEN`,
  which used to answer one English sentence in `error`; the code stays there
  and the sentence moved to `message`). Docs: [STORAGE.md → Scan exclusions](./STORAGE.md#scan-exclusions).
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
  the explorer shows "More results than shown - narrow your search". ([#35](https://github.com/BRF-Tech/filex/pull/35))

- **Apps: the platform seal.** `cert_issue {purpose: "platform"}` hands an app
  with `sign` the installation's own seal: one key per tenant and app, kept by
  the host (never destroyed - `key_destroy` refuses it), CN *filex document
  seal*, OU the app's name, issued by the live authority and re-issued by the
  next one after a rotation; it signs through `host_sign` from jobs only. The
  signing app seals every completed request with it (the owner, 2026-09-22:
  "the final bytes are sealed by filex itself"). SDK: `pluginkit.PlatformSeal`,
  `plugintest.Host.PlatformSeal`.
- **Apps: a lock until lifted, and a lock on the job's own output.**
  `file_lock` takes `ttl_days: -1` (`pluginkit.LockUntilLifted`) - no end,
  until the app or an administrator (audited) lifts it - and a `ref` naming one
  of the job's OWN outputs, which is promised and taken when the output is
  committed (never when the job fails). How the signing app keeps a finished
  document locked for good, when the request asks for it.

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0430---2026-09-24).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

## Security advisories

- [GHSA-gj84-9xx7-2g9f](https://github.com/BRF-Tech/filex/security/advisories/GHSA-gj84-9xx7-2g9f) - an unsigned ONLYOFFICE save callback was accepted (High, 7.5)
- [GHSA-55vh-f285-5wv3](https://github.com/BRF-Tech/filex/security/advisories/GHSA-55vh-f285-5wv3) - a restricted API token could issue credentials wider than itself (High, 8.8)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.43.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.43.0`

## v0.42.2

<span class="filex-release-date">19 September 2026</span>

A fix release for the issue 32 follow-up and three things that were wrong on screen. The context menu is now the same on Recent, Starred, Shared with me, tag views, the Home cards and the Recently-opened tray as in a folder - rows carry their own permission level, so Rename, Move, Delete and Share no longer vanish there (or leak in from the last folder). Every admin table pins its actions column to the right edge and scrolls sideways instead of squeezing it off screen. The admin Shares page copies the link the server builds from the configured public URL, not the browser's address. And presigned URLs are off by default on S3 storages: downloads and share links stream through filex, so a LAN-only MinIO no longer turns share downloads into dead links - set disable_presign: false to get the redirect back.

Upgrade notes: no migrations. S3 storages saved without disable_presign now stream downloads. GET /api/admin/shares rows gain url; recent/starred/tagged rows gain perm and read_only. The search placeholder now names the storage it searches.

## What changed

A fix release for the issue 32 follow-up and three things that were wrong on
screen.

### Changed

- **Presigned URLs are off by default on S3 storages** (`disable_presign: true`). A presigned link hands the browser the bucket's endpoint, which on
  the LAN-only MinIO most self-hosters run turned every share download into a
  dead link (#32). Uploads and the downloads behind public share links now
  stream through filex unless the operator sets `disable_presign: false` -
  worth it only when the endpoint is reachable from users' browsers and
  accepts SDK-signed URLs. ⚠ A storage saved without the key streams from now
  on; set `false` explicitly to get the redirect back.
- **The top search field says what it searches.** It searches the whole
  storage (every storage at the root), never the open folder, so the
  placeholder now names the storage - "Search in Photos storage" - and
  "Search all storages" on Home, the root and the Recent/Starred/Shared/tag
  views. The folder-scoped box is still the filter bar's "Filter in this
  folder…".

### Fixed

- **The context menu is the same everywhere.** On Recent, Starred, Shared with
  me, tag views, the Home cards and the Recently-opened tray a row offered only
  Open/Download/Copy/Star - no Rename, Move, Delete or Share - or, if a
  writable folder had been opened first, all of them plus a meaningless Paste.
  Rows in those views never carried their own permission level and the
  folder's level leaked in. Every listed row now carries `perm` and
  `read_only` from the server, the views forget the previous folder's level on
  entry, and the menu is built per row; only New folder/Upload/Paste stay out
  where there is no folder to put things in.
- **Admin tables keep their actions column in view.** Every admin table - and
  the token/key panels in the explorer - now scrolls sideways when it is wider
  than the page and pins the actions column to the right edge, the way the
  explorer's list view pins its ⋮ menu; the column no longer squeezes off
  screen on narrow windows. One shared stylesheet (`web/src/styles/table.css`)
  and `ui/Table.vue`'s `pinned` column option.
- **The admin Shares page copies the right link.** It built the link from the
  browser's own address, so an administrator signed in on localhost or through
  a proxy copied a link nobody else could open (#32). `GET /api/admin/shares`
  rows now carry the canonical `url` from the configured public origin - the
  same one the share dialog has always used - and the page prefers it.
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

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0422---2026-09-19)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.42.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.42.2`

## v0.42.1

<span class="filex-release-date">19 September 2026</span>

A fix release for four reports on 0.42.0. A bucket with several top-level folders is one form now, not one per folder: Storages → Add lists the folders under the root you typed and creates one storage per ticked folder with the same credentials (#31) - the bucket root itself is still never mounted. A read-only storage looks read-only to the people using it: a tag on its row, no + New menu, no New folder or Upload on it (#30). A scan of a large S3 storage is one listing instead of one request per folder, only one run per storage walks at a time, and the per-storage scan interval is on the form (#33). And when FILEX_PUBLIC_URL was never set, administrators see a banner instead of finding out from a share link to localhost (#32).

Upgrade notes: no migrations. POST /api/admin/storages/{id}/sync answers status "running" instead of starting a second scan while one is in flight; GET /api/files/capabilities gains public_url_configured; POST /api/admin/storages/discover is new.

## What changed

A fix release for four reports on 0.42.0.

### Added

- **Mount several folders at once** (#31). *Storages → Add* lists the folders
  directly under the root you typed - the bucket root included - and creates
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
  [CONFIGURATION.md → Public URL](./CONFIGURATION.md#public-url) now says
  so in as many words.

### Changed

- **One sync run per storage at a time** (#33). "Scan now" while a run is
  walking starts no second full walk over the same rows - it answers **202**
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
  403. Sharing a read-only file is still allowed - the ACL level is unchanged,
  only the write affordances go (`@brftech/filex-core` `permCanEdit` folds the
  listing's `read_only` in, so every embed gets the same).

### Upgrade notes

- No migrations. `POST /api/admin/storages/{id}/sync` answers 202 with
  `status: "running"` (instead of `"started"`) while a run is already in
  flight; nothing is started twice.
- A client that reads `GET /api/files/capabilities` sees one more boolean,
  `public_url_configured`.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0421---2026-09-19)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.42.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.42.1`

## v0.42.0

<span class="filex-release-date">16 September 2026</span>

## What changed

### Changed

- **A single click selects; a double click opens (mouse).** The default open
  gesture is now the classic desktop file-manager one - a single click selects a
  row/card, a double click opens it, and **Enter** opens the selection. This
  reverses 0.41.x's "any click opens" for a mouse; it is a per-viewer setting
  (`ExplorerConfig.openTrigger: 'single' | 'double'`, default `'double'`), and
  the desktop app exposes it under **Settings → Open files with**.
  - ⚠ **Touch is untouched.** A finger tap always opens (the mobile convention,
    and there is no hover-to-select on a touchscreen); the checkbox is still the
    one click that selects, on every device and in either mode.

### Added

- **`ExplorerConfig.openInHost`** - when set, opening a file emits `file-opened`
  and the explorer does NOT mount its own in-page preview; the host opens the
  file itself. The desktop app uses this to open **every document in its own
  window**, one per file (any type), so a future editor drops into the same
  path. Directories still navigate inline; Space quick-look still peeks in-page;
  E2E-encrypted files keep the in-page decrypted preview.
- **Desktop: frameless document + main windows with our own window controls.**
  The native OS caption is gone. On Windows/Linux the app draws its own
  minimize / maximize / close (the main window in a slim title bar, each document
  window in a reserved top bar so it never sits on the viewer's own top row -
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

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0420---2026-09-16)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.42.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.42.0`

## v0.41.4

<span class="filex-release-date">15 September 2026</span>

A fix release that carries 0.41.3, whose release build never finished. Only the checkbox selects now, and any other click or tap on a file or folder opens it - beside the name, on the size and date cells, with a selection, with Ctrl held (#26). Grid and gallery cards gained a checkbox of their own, shown on hover and on every card once something is selected, so a phone can pick several files after a long press. From 0.41.3: on a phone one tap opens, even where the browser swallows the click, and the external services Test button says what its probe saw - an HTTP status, a timeout or the connection error - with a docservice hint for an ONLYOFFICE 502 (#17).

Upgrade notes that matter: a click beside a name, or a Ctrl/Shift click, no longer selects - tick the checkbox. POST /api/admin/external/:name/test gains a detail field. No migrations.

## What changed

### Changed

- **Only the checkbox selects; any other click or tap opens** (#26, fourth
  round). In 0.41.2 a click opened only when it landed on the name itself - the
  reporter: "need to click precisely on name, if a lil bit on the right then it
  selects file, change it so if only clicking on checkbox it selects it, any
  other click will open." That is now the rule, with a mouse and on a phone
  alike:
  - A click or tap **anywhere** on a list row, grid card or gallery tile opens
    it - the name, the icon, the empty space to its right, the size and date
    cells - with or without a selection, and with Ctrl or Shift held.
  - The **checkbox** is the one click that selects. Shift on a checkbox still
    extends the range from the last tick. A right click or a long press still
    opens the menu; the star and the ⋮ button still do their own job.
  - **Grid and gallery cards have a checkbox now.** It shows on hover, when the
    card is focused or selected, and on every card once anything is selected -
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
  can carry its type with a nil pointer - `(*TCPListener).Addr` then panicked.
  The SFTP and NFS servers (the same code) now guard the listener, and a
  `Close` that arrives before the listener is up no longer leaves it open.
  `go test -race` is clean on both packages.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0414---2026-09-15)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.41.4) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.41.4`

## Earlier releases

The 115 releases before v0.41.4, in brief. Full notes are on GitHub.

| Version | Date | What changed |
|---|---|---|
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

<small>Last refreshed 2026-10-08 from 135 published releases.</small>
