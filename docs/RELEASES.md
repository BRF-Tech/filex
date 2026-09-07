---
title: Releases
description: Every filex release with a plain-English summary of what changed — generated from the GitHub releases at release time.
---

<!-- GENERATED FILE — do not edit by hand. Your edits will be overwritten.
     Source:      GitHub Releases for BRF-Tech/filex
     Generator:   docs-site/scripts/fetch-releases.mjs
     Summaries:   docs-site/data/release-highlights.json (hand-written)
     Regenerate:  cd docs-site && npm run releases -->

# Releases

Every published filex release, newest first. This page is generated from the
[GitHub releases](https://github.com/BRF-Tech/filex/releases) by `npm run releases`, which is
run once when a release is cut — not by the site build, which would rewrite this
file on every contributor who ran it.

Whether filex installs a release by itself depends on which part of the version moved —
see [Updates](./UPDATES.md).

::: tip Latest — v0.35.0, 7 September 2026
A security release. On a multi-tenant install the tenant boundary was enforced on the routes that list rows and assumed on the routes that name one: an administrator of any tenant, pointed at another tenant's ids, reached fifteen of the sixteen such routes. Resetting a password returned the other customer's new password in the response body; reading a storage returned its S3 or SFTP secret; minting an API token bound it to somebody else's user. The file API was worse, because it serves bytes rather than JSON. Separately, and on every install rather than only multi-tenant ones, thumbnails were readable without logging in — a rendered preview of any document to anyone who could guess a node id — so thumbnail URLs now carry a short-lived signature that an &lt;img> tag can use and an anonymous request cannot forge. Emptying the trash for one storage emptied all of them, permanently, while the dialog promised otherwise. Restoring a version required no permission at all, so a read-only account could overwrite live files. Directory logins provisioned accounts into the tenant that sees every other one. And a family of controls that saved and were never read is gone or wired up: five admin settings that wrote rows nothing consulted, a demo password that broke the demo login it claimed to secure, and a storage sync mode that accepted typos and silently polled.
:::

```bash
docker pull ghcr.io/brf-tech/filex:slim-v0.35.0
docker pull ghcr.io/brf-tech/filex:full-v0.35.0
```

## v0.35.0

<span class="filex-release-date">7 September 2026</span>

A security release. On a multi-tenant install the tenant boundary was enforced on the routes that list rows and assumed on the routes that name one: an administrator of any tenant, pointed at another tenant's ids, reached fifteen of the sixteen such routes. Resetting a password returned the other customer's new password in the response body; reading a storage returned its S3 or SFTP secret; minting an API token bound it to somebody else's user. The file API was worse, because it serves bytes rather than JSON. Separately, and on every install rather than only multi-tenant ones, thumbnails were readable without logging in — a rendered preview of any document to anyone who could guess a node id — so thumbnail URLs now carry a short-lived signature that an &lt;img> tag can use and an anonymous request cannot forge. Emptying the trash for one storage emptied all of them, permanently, while the dialog promised otherwise. Restoring a version required no permission at all, so a read-only account could overwrite live files. Directory logins provisioned accounts into the tenant that sees every other one. And a family of controls that saved and were never read is gone or wired up: five admin settings that wrote rows nothing consulted, a demo password that broke the demo login it claimed to secure, and a storage sync mode that accepted typos and silently polled.

## What changed

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

**This release has more to it than fits on one page.** The rest of the
entry — and every earlier release — is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0350---2026-09-07).

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.35.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.35.0`

## v0.34.2

<span class="filex-release-date">6 September 2026</span>

Two things that only ever bit installs nobody had configured by hand. filex guesses http://localhost:5212 as its own address when FILEX_PUBLIC_URL is unset, and the realtime ticket handed that guess to the browser as fact -- so on any other port the socket connection was refused and the client fell back to twelve-second polling with nothing logged anywhere. It reads as "live updates do not work" while the server is entirely healthy. The socket address now follows the request when no public URL was chosen, which is safe there and nowhere else: that response goes back to the client that asked for it, while a share link is mailed to somebody else and still uses the configured origin. The startup banner had the same fault and printed a URL nothing was serving. Also two browser tests that asserted Turkish sentences and went red when English became the fallback language, without either feature having changed.

## What changed

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

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0342---2026-09-06)

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.34.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.34.2`

## v0.34.1

<span class="filex-release-date">6 September 2026</span>

A patch for two things v0.34.0 shipped over. A user who had chosen Turkish saw an English admin panel on any second device: the language stored on the account was written by Profile and never read, so it only ever worked in the browser you saved it in. It had been invisible while Turkish was the fallback for everybody, and surfaced when v0.33.0 correctly made that fallback English. And the release could not be stopped by a failing test suite -- CI ran on the branch push while the release ran on the tag two seconds later, in parallel and unaware of each other, with no required check anywhere. CI had been red since v0.31.0 and four releases went out over it, carrying exactly that locale bug. Nothing publishes now until the suite is green.

## What changed

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

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0341---2026-09-06)

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.34.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.34.1`

## v0.34.0

<span class="filex-release-date">6 September 2026</span>

The release where writes stopped being silent. Half of filex's write surfaces — WebDAV, SFTP, FTPS, NFS, the S3 gateway, the AI/MCP API, the archive extractor, the async copy worker and the built-in editor — put a file on the storage and told nothing else, so a browser with that folder open never showed it: an explorer with a live socket does not poll, and the periodic sync repairs rows without announcing anything. They all announce now, and a burst is merged into one frame per window instead of one per file, so extracting a five-thousand-file archive no longer leaves the folder empty for two minutes. Antivirus got the other half of the attention. ClamAV can now be a daemon in its own container reached over the network — the filex images ship no scanner, so under Docker or Kubernetes the previous answer was `build your own image` — and the switch, the mode, the address, the size ceiling and a new editor save-scan window moved onto Settings → Protection, with the `FILEX_CLAMAV*` variables demoted to first-boot seeds. Four write paths that were never scanned are now: the text editor, restoring a version, restoring from the trash, and the OnlyOffice save-back — which also announced nothing and re-indexed nothing, so an edited document kept its old text in search. So are files the storage sync finds on the backend rather than through filex, which is the case an operator most assumes is covered. ⚠ One behaviour change to check before upgrading: a write that **replaced** an existing file now emits `file.updated` rather than `file.uploaded`, so a webhook subscribed to the latter in order to see edits has to tick the new one as well. Four long-standing data bugs go with it. The storage sync used to un-delete files on every pass — and since quarantine is the same operation, it let infected files out of quarantine on a timer nobody set. A moved or renamed file lost its version history silently, because the row kept pointing at its old path. A deleted file never gave back its cached thumbnail or its upload staging bytes, so disk only grew. And on backends that report no ETag — local, SFTP, SMB, FTP — a file replaced outside filex was never noticed at all, so its size, its indexed text and its virus verdict stayed as they were forever. On a multi-tenant install, the instance-wide admin surfaces — antivirus and retention, the external editor, the auth providers, the self-upgrade — had no supertenant gate, so any tenant admin could turn scanning off for everybody or point the scanner at a host of their own; they are gated now. Finally the queue: all three drivers honour priority now, so an upload's scan is served ahead of a twenty-thousand-file first import; the Postgres driver, which could never claim a job at all, works; ⚠ and a Redis queue converts its pending list at startup, keeping every queued job, after which downgrading is not supported.

## What changed

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
  [MULTI-TENANCY.md](./MULTI-TENANCY.md#instance-wide-admin-surfaces).

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
  [STORAGE.md → Drift detection](./STORAGE.md#drift-detection-what-a-replaced-file-looks-like).

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

**This release has more to it than fits on one page.** The rest of the
entry — and every earlier release — is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0340---2026-09-06).

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.34.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.34.0`

## v0.33.0

<span class="filex-release-date">5 September 2026</span>

A release of things that were quietly wrong. A large upload to an S3 endpoint served over plain HTTP died inside the client and the storage sync then moved the file to the trash — reported from the outside, and worth knowing why it had never been seen here: SigV4 hashes the request body over `http` and does not over `https`, so filex was requiring a rewindable stream it never asked for, and the bug sat waiting for the first plaintext endpoint. Overwriting a file now keeps the old bytes: the snapshot happens before the write at every destructive surface, and if it cannot be taken the write is refused rather than losing the file — until now only the in-browser text editor versioned anything, so uploading over a file destroyed it. The S3 gateway stopped exposing filex's own `.versions/`, `.thumbs/` and `.filex-trash/` trees, which every other protocol had hidden since it was written. On a multi-tenant install every absolute URL now names the tenant's host rather than the operator's — share links, the WebSocket endpoint, and the account-created e-mail that carried a temporary password to somebody else's login page. And the product stopped speaking Turkish to people who never asked: the convert dialog, eighteen notifications, the file-request e-mail, and an embed that defaulted to Turkish now follow the locale, or the browser's, or English.

## What changed

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
  `&#123;&#123; t('viewer.download') }}`); and the SMTP password placeholder in
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

**This release has more to it than fits on one page.** The rest of the
entry — and every earlier release — is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0330---2026-09-06).

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.33.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.33.0`

## v0.32.0

<span class="filex-release-date">5 September 2026</span>

The explorer gained a third shape. `uiProfile: 'drive'` lays it out the way someone arriving from Google Drive expects — one **+ New** menu, a single search field across the header with the command palette behind it, Type / Modified / Size filters, Folders and Files as labelled sections, and Details / Activity in the info panel. It is a preset of the same explorer rather than a second UI, so the npm packages and embeds get it too, and a non-admin lands on it. Windows finally has a copy that runs without being installed: one .exe that keeps its data beside itself, so deleting one folder leaves nothing behind on a machine that is not yours — Linux and macOS already had this. On encrypted folders, an installation that already exists can now adopt key escrow, and — the part that makes escrow reach anything real — the owner of a folder created before escrow was turned on can grant it themselves, from the unlock dialog, with the consequence spelled out. Search stopped treating numbers as typos, so `2026` no longer returns last year's report. Several things that had been quietly broken were fixed rather than found by users: the install banner covered the sign-in button, middle-click did nothing in gallery view, a search at a storage root answered with a folder called Trash that is not a folder, and the file-infected notification spoke Turkish to everybody.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.32.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.32.0`

## v0.31.0

<span class="filex-release-date">5 September 2026</span>

Forgetting the password on an encrypted folder no longer means losing the files. Creating one now shows a recovery key once — filex never stores it, and it opens the folder without the password. The file format did not change: existing encrypted files are byte-identical and open unchanged, verified by a round-trip test against a frozen copy of the previous release's crypto module. A folder made before this release cannot be given a recovery key by the server, because the server has no password to re-wrap with, so filex offers the upgrade at the one moment it holds one — the next successful unlock. Operators who need a second way in can set an escrow key at install time; it is RSA-OAEP, filex keeps only the public half, and the documentation says plainly that this is a backdoor and what its notification can and cannot promise. Also here: starring is a real action rather than a badge that only existed in list view, tags are browsable from the navigation panel, opening a virtual view like Trash from its own URL no longer says "Folder not found", and API tokens are split into user and app kinds so a shared embed credential can no longer manage its owner's keys.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.31.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.31.0`

## v0.30.1

<span class="filex-release-date">4 September 2026</span>

A one-line patch. Opening Recent, Starred, Shared or Trash put the raw internal name of the view in the tab — `.shared` instead of Shared.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.30.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.30.1`

## v0.30.0

<span class="filex-release-date">4 September 2026</span>

The explorer grew a collapsible navigation panel: Recent, Starred, Shared with me, tags and Trash on the left, with Upload as the primary action. It is part of the shared component, so it reaches the npm packages and every embed, not just the admin app — and `uiProfile: 'simple'` turns the admin-facing parts off for an end user. Connecting a client and minting an API key moved into the explorer too; both surfaces existed but lived only in the admin app. On the search side, the index now repairs itself after an upgrade instead of waiting for someone to rebuild it by hand, filename ranking is a port of VS Code's Quick Open scorer, and multi-word content search was an OR — so adding a word made the result set larger rather than smaller.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.30.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.30.0`

## v0.29.0

<span class="filex-release-date">4 September 2026</span>

Open an Office document that lives on your own computer in the editor your filex server already runs: the desktop app registers for the usual extensions, so a machine with no Word or Excel installed can still edit one. Non-admin accounts get a front door of their own at `/drive` rather than landing in the admin shell. Search stopped depending on which separator a filename happened to use, forgives one typo, and has a stated ranking order instead of an inherited one — and `tag:` and `-tag:` filters put a feature that had existed for a long time within reach of the search box.

**Other changes**

- **Site** — Hand-written summary for v0.28.0.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.29.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.29.0`

## v0.28.0

<span class="filex-release-date">3 September 2026</span>

If you run filex against LDAP or Active Directory, this is the release where that actually works. The directory driver could be configured, initialised and printed in the boot banner while being unreachable from every login path: a directory account with the correct password was refused in under a millisecond — less than one LDAPS round trip, so the request never left the machine. Directory users can now sign in on the normal password form, and on WebDAV, SFTP, FTPS, S3 and NFS as well; their accounts hold no password hash in filex, which is why those protocols used to refuse them forever. Local login is still tried first, so your break-glass admin keeps working while the directory is down. Also here: a `ca_file` option for a private CA (no more rebuilding the container's trust store), a filter that fills every placeholder rather than the first one, and search failures that are finally distinguishable from a wrong password in the log.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.28.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.28.0`

## v0.27.6

<span class="filex-release-date">1 September 2026</span>

**- the first signed tag.**

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.6) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.6`

## v0.27.5

<span class="filex-release-date">1 September 2026</span>

**- a transient 503 no longer sinks an upload.**

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.5) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.5`

## v0.27.4

<span class="filex-release-date">29 August 2026</span>

**- the Helm chart is part of every release now.**

**Fixed**

- **Helm** — The chart shipped a 23-release-old image, and now it cannot again.

**Other changes**

- **Release** — Refuse to publish a tag whose Helm chart is behind.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.4) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.4`

## v0.27.3

<span class="filex-release-date">29 August 2026</span>

**- a dragged folder fills in.**

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.3) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.3`

## v0.27.2

<span class="filex-release-date">29 August 2026</span>

**- a non-ASCII filename no longer breaks a download.**

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.2`

## v0.27.1

<span class="filex-release-date">29 August 2026</span>

**- a drag-out fills in the folder it was dropped on.**

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.1`

## v0.27.0

<span class="filex-release-date">28 August 2026</span>

**- paste into another storage, drag out at any size.**

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.0`

## v0.26.1

<span class="filex-release-date">27 August 2026</span>

**- a refused upload says what to do about it.**

**Fixed**

- **AI** — Ticket refusals say what to do next; no curl, no problem.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.26.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.26.1`

## v0.26.0

<span class="filex-release-date">27 August 2026</span>

**- agents can upload big local files without a token.**

**New**

- **AI** — Credential-free upload tickets for large local files.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.26.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.26.0`

## Earlier releases

The 85 releases before v0.26.0, in brief. Full notes are on GitHub.

| Version | Date | What changed |
|---|---|---|
| [v0.25.3](https://github.com/BRF-Tech/filex/releases/tag/v0.25.3) | 25 August 2026 | - file-request pages render their text; dead storage says so. |
| [v0.25.2](https://github.com/BRF-Tech/filex/releases/tag/v0.25.2) | 23 August 2026 | A patch for whoever reads the error tracker. Events forwarded from filex's log used to arrive as a bare message — "thumb generate failed" eleven times with no file and no error to act on. |
| [v0.25.1](https://github.com/BRF-Tech/filex/releases/tag/v0.25.1) | 23 August 2026 | A same-day fix for FTPS: the public host name is looked up when the listener starts, and a single DNS timeout in the container's first seconds used to leave FTPS off for good while everything else reported healthy. |
| [v0.25.0](https://github.com/BRF-Tech/filex/releases/tag/v0.25.0) | 23 August 2026 | Share links now have a ceiling on how long they live, and the admin holds it: a new Protection setting (default seven days) caps every new link and file request — a link created without an expiry gets one, a longer request is… |
| [v0.24.1](https://github.com/BRF-Tech/filex/releases/tag/v0.24.1) | 22 August 2026 | A follow-up to 0.24.0's resumable first run: it now covers uploads too. The engine writes its sync history while it works — every 50 transfers or 15 seconds — instead of only at the very end, so a run interrupted at file 9,000 of… |
| [v0.24.0](https://github.com/BRF-Tech/filex/releases/tag/v0.24.0) | 22 August 2026 | A night of syncing a real 10,000-file tree, and what it taught the engine. Transfers and server listings now run several at a time, so a tree of small files is no longer priced at one round-trip each — the tree that crawled at… |
| [v0.23.0](https://github.com/BRF-Tech/filex/releases/tag/v0.23.0) | 20 August 2026 | The rest of “keep on this computer”, the same day it shipped. Every row now says where it lives — ✓ on this computer, ◐ holding kept items below, ⟳ syncing right now, ☁ online-only — and a strip along the bottom of the window… |
| [v0.22.0](https://github.com/BRF-Tech/filex/releases/tag/v0.22.0) | 20 August 2026 | Folders you also want on the computer are now one right-click away. “Keep on this computer” mirrors a server folder — or a whole storage — under a single filex folder chosen once per account, while everything else stays… |
| [v0.21.6](https://github.com/BRF-Tech/filex/releases/tag/v0.21.6) | 19 August 2026 | Two decisions about noise and blast radius. A demo instance no longer accepts any new storage backend — 0.21.4 stopped it reaching the server's own filesystem, and now the remote drivers go too, because "attach your own bucket"… |
| [v0.21.5](https://github.com/BRF-Tech/filex/releases/tag/v0.21.5) | 19 August 2026 | Plugins no longer outlive filex on Windows. If filex was stopped without a chance to clean up — a crash, a hard kill, a service restart — every plugin it had launched kept running; and since a running plugin holds its own .exe… |
| [v0.21.4](https://github.com/BRF-Tech/filex/releases/tag/v0.21.4) | 19 August 2026 | The other half of the demo hardening in 0.21.3. A demo instance publishes an admin login, and the `local` storage driver means "a path on this host" — so on a demo it was possible to add a storage rooted at /data, /etc or /proc/1… |
| [v0.21.3](https://github.com/BRF-Tech/filex/releases/tag/v0.21.3) | 19 August 2026 | A security default, found by measuring the project's own public demo. A demo instance publishes an admin login — that is what a demo is for — and the plugin API is admin-only, so on a demo "admin-only" means anybody, and… |
| [v0.21.2](https://github.com/BRF-Tech/filex/releases/tag/v0.21.2) | 19 August 2026 | A plugin now has to PROVE what it claims before filex will use it. Every capability a plugin declares is probed — at install against the plugin's own throwaway area, and again when you save a storage on it — and one that fails… |
| [v0.21.1](https://github.com/BRF-Tech/filex/releases/tag/v0.21.1) | 19 August 2026 | Pasting a file into the top level of a storage failed — on every driver, not just the new plugins: the explorer asks for the storage root, and the operations queue read that as "no destination given". |
| [v0.21.0](https://github.com/BRF-Tech/filex/releases/tag/v0.21.0) | 19 August 2026 | filex can now be taught a storage it has never heard of. A plugin is a separate program — in any language — that filex launches (or connects to) and speaks a small HTTP/JSON protocol to; once it is running, its driver appears in… |
| [v0.20.3](https://github.com/BRF-Tech/filex/releases/tag/v0.20.3) | 18 August 2026 | The desktop app now ships for macOS (Apple Silicon) — unsigned but ad-hoc sealed, so first launch is the ordinary Open Anyway step rather than a refusal; auto-update on the Mac waits for a signed build. |
| [v0.20.2](https://github.com/BRF-Tech/filex/releases/tag/v0.20.2) | 17 August 2026 | Desktop packaging fixes take effect. |
| [v0.20.1](https://github.com/BRF-Tech/filex/releases/tag/v0.20.1) | 17 August 2026 | FTPS starts when you give it a host name. |
| [v0.20.0](https://github.com/BRF-Tech/filex/releases/tag/v0.20.0) | 17 August 2026 | filex is now reachable without a browser. It could already use S3, SFTP, FTP and WebDAV as storages; now those clients can point at filex itself — as an S3 endpoint, an SFTP server, an FTPS server and an NFSv3 export, alongside… |
| [v0.19.0](https://github.com/BRF-Tech/filex/releases/tag/v0.19.0) | 14 August 2026 | The desktop app gets a language setting — System, English or Türkçe — where before it followed the operating system and offered nothing to choose. |
| [v0.18.2](https://github.com/BRF-Tech/filex/releases/tag/v0.18.2) | 14 August 2026 | The Windows app installs per-user now, and that is what makes the quiet update in 0.18.1 real: an app under C:Program Files needs administrator rights to replace its own files, so every background update ended in a UAC prompt —… |
| [v0.18.1](https://github.com/BRF-Tech/filex/releases/tag/v0.18.1) | 14 August 2026 | The desktop app updates itself the way it always should have. Downloading was quiet and quitting installed silently, but the tray entry and the Settings button ran the installer with its wizard — so the one visible path through… |
| [v0.18.0](https://github.com/BRF-Tech/filex/releases/tag/v0.18.0) | 14 August 2026 | Share links keep their word, and you get a face. A link capped at three downloads could hand out four: the cap was checked against a counter that was only bumped after the bytes had left, so any request that started while an… |
| [v0.17.1](https://github.com/BRF-Tech/filex/releases/tag/v0.17.1) | 12 August 2026 | A packaging fix, and the first release whose tag matches what ships. The desktop package could be built without the updater inside it: in a pnpm workspace the dependency is a symlink pointing outside the app directory and… |
| [v0.17.0](https://github.com/BRF-Tech/filex/releases/tag/v0.17.0) | 12 August 2026 | The desktop app keeps itself up to date. It checks a static feed on filex.sh (not GitHub — that mirror is private and the provider would need a token inside the app), downloads quietly, and installs when you quit, because an… |
| [v0.16.3](https://github.com/BRF-Tech/filex/releases/tag/v0.16.3) | 12 August 2026 | The tab strip, fixed properly. It was permanent in the desktop app and came and went on the web, because 0.16.0 gave the two surfaces different defaults — this package exists so they are one product, so the default is now the… |
| [v0.16.2](https://github.com/BRF-Tech/filex/releases/tag/v0.16.2) | 12 August 2026 | Two things a share link got wrong. It changed language when you entered its PIN — the gate was English, the page behind it Turkish, and the screen in between managed both at once. |
| [v0.16.1](https://github.com/BRF-Tech/filex/releases/tag/v0.16.1) | 12 August 2026 | Follow-up to 0.16.0: a shared folder's gallery tiles are now rendered when the link is created rather than when the first visitor arrives, so the first open is fast too — which is the open that matters, since whoever creates a… |
| [v0.16.0](https://github.com/BRF-Tech/filex/releases/tag/v0.16.0) | 12 August 2026 | Two things a shared folder was doing the slow way. Its gallery tiles were the original photos — the page asked for a thumbnail and the server streamed the whole file — so a folder of a few dozen photos shipped tens of megabytes… |
| [v0.15.1](https://github.com/BRF-Tech/filex/releases/tag/v0.15.1) | 12 August 2026 | The desktop app's account rail had its two identities the wrong way round. Each row of that rail is a server — a tenant — so it now carries that server's own Branding logo, which is a better label than initials taken from an… |
| [v0.15.0](https://github.com/BRF-Tech/filex/releases/tag/v0.15.0) | 12 August 2026 | Mostly the desktop app, and one thing that was writing to your operating system. "Start when I sign in" registered whichever executable happened to be running the app — which, for anyone who had ever run it from source, was a… |
| [v0.14.0](https://github.com/BRF-Tech/filex/releases/tag/v0.14.0) | 10 August 2026 | The desktop app can reach its own server again. Opening any office document showed "Config fetch 401", starred files and recently-opened were silently empty, and "Open in new tab" did nothing at all — one cause under the first… |
| [v0.13.4](https://github.com/BRF-Tech/filex/releases/tag/v0.13.4) | 10 August 2026 | A fix for a disk that fills up on its own. Uploads larger than 32 MiB are buffered to a temporary file, and those files were never removed — every request answered normally while the disk quietly drained (29 GB in two hours on a… |
| [v0.13.3](https://github.com/BRF-Tech/filex/releases/tag/v0.13.3) | 7 August 2026 | The share button now appears in the desktop app. The explorer has always had one, but it was gated on the Web Share API, which Electron does not ship — so rather than build a second share UI, the app puts a native handler behind… |
| [v0.13.2](https://github.com/BRF-Tech/filex/releases/tag/v0.13.2) | 7 August 2026 | Sixteen components were rendering as raw, unstyled HTML in every embedded surface — the share dialog, the convert dialog, the presence bar and nine file viewers. |
| [v0.13.1](https://github.com/BRF-Tech/filex/releases/tag/v0.13.1) | 7 August 2026 | The explorer's onboarding tour sat on top of the desktop app's Settings panel. The tour attaches to `<body>`, so hiding the explorer left it exactly where it was. |
| [v0.13.0](https://github.com/BRF-Tech/filex/releases/tag/v0.13.0) | 7 August 2026 | The desktop app is a file manager, not an admin console. Signing in used to land you on the server's dashboard because the shell embedded the whole admin SPA; it now shows the file explorer, an account rail down the left, and a… |
| [v0.12.0](https://github.com/BRF-Tech/filex/releases/tag/v0.12.0) | 7 August 2026 | Selective folder sync. A folder on your computer and a folder on a filex server are kept in step in both directions, in the background. |
| [v0.11.0](https://github.com/BRF-Tech/filex/releases/tag/v0.11.0) | 7 August 2026 | The desktop app, for Windows and Linux. It runs the same web UI this repo already ships, and sign-in happens in your browser — the app opens the server's own login page and waits, so installs behind an identity provider (OIDC,… |
| [v0.10.2](https://github.com/BRF-Tech/filex/releases/tag/v0.10.2) | 6 August 2026 | Completes the guard added in 0.10.1: a sweep found four more places that could still write a file onto a folder — archive extraction and creation, the OnlyOffice save-back, and version restore — plus replication, where a… |
| [v0.10.0](https://github.com/BRF-Tech/filex/releases/tag/v0.10.0) | 6 August 2026 | Two explorer changes reported by a deployment whose users mount WebDAV from macOS. Dot-prefixed files can now be shown or hidden and are hidden by default (a Mac leaves `.DS_Store` and `._name` litter in every folder it opens),… |
| [v0.9.0](https://github.com/BRF-Tech/filex/releases/tag/v0.9.0) | 6 August 2026 | Closes the ten items a multi-tenant deployment filed against v0.8.0. The important one is a security fix: a tenant admin could reach every other tenant's storages over WebDAV, because `/dav` does its own Basic authentication and… |
| [v0.8.0](https://github.com/BRF-Tech/filex/releases/tag/v0.8.0) | 29 July 2026 | filex now knows which releases exist, and can install them. What it does is decided by which part of the version moved: a patch applies itself when the policy allows, a minor is announced and applied with one click, a major is… |
| [v0.7.6](https://github.com/BRF-Tech/filex/releases/tag/v0.7.6) | 29 July 2026 | Denials on the AI/MCP surface answer `403` instead of `500`. A `5xx` reads as "server glitch, retry", so agents and HTTP clients were retrying requests that could never succeed while the real cause — a path outside the token's… |
| [v0.7.5](https://github.com/BRF-Tech/filex/releases/tag/v0.7.5) | 19 July 2026 | An internal refactor with no behaviour change: the storage-scoped path hash had been copy-pasted across nine call sites, so the same file could map to different rows if any copy drifted. |
| [v0.7.4](https://github.com/BRF-Tech/filex/releases/tag/v0.7.4) | 18 July 2026 | Two explorer fixes: the trash bin now appears in the secondary pane of split view (the two panes were offset by a row), and tall listings scroll inside their pane instead of scrolling the whole page. |
| [v0.7.3](https://github.com/BRF-Tech/filex/releases/tag/v0.7.3) | 18 July 2026 | Split view's right-click menu now matches the main panel exactly — it was a shorter, separate list missing rename, delete, share, convert and tags. |
| [v0.7.2](https://github.com/BRF-Tech/filex/releases/tag/v0.7.2) | 18 July 2026 | Split-view polish: the main panel's breadcrumb no longer spans the full width, and right-clicking a row in the secondary pane opens a real menu instead of only selecting the row. |
| [v0.7.1](https://github.com/BRF-Tech/filex/releases/tag/v0.7.1) | 18 July 2026 | A round of layout and accessibility fixes for embedders. The explorer no longer overflows its host by 2px (the outer scrollbar that produced in embeds is gone), the toolbar folds overflowing actions into a `⋯` menu instead of… |
| [v0.7.0](https://github.com/BRF-Tech/filex/releases/tag/v0.7.0) | 17 July 2026 | Three additions. **Branding** — a settings-driven identity (name, logo, accent, footer) for the public share, PIN, file-drop and folder-browse pages plus the admin login, with per-tenant overrides. |
| [v0.6.0](https://github.com/BRF-Tech/filex/releases/tag/v0.6.0) | 17 July 2026 | Tabs and split view. Open several locations as tabs, split the active tab into two panes that navigate independently, and drag files between them to move (same storage) or copy (across storages). |
| [v0.5.0](https://github.com/BRF-Tech/filex/releases/tag/v0.5.0) | 17 July 2026 | A large interface release: eight built-in themes with independent light and dark variants, fully rebindable keyboard shortcuts, Quick Look (peek the selected file with Space), an operations centre that collects uploads and… |
| [v0.4.2](https://github.com/BRF-Tech/filex/releases/tag/v0.4.2) | 17 July 2026 | Cleanup release. Moving a folder to trash no longer wedges storage sync (a trashed folder's leftovers could block sync from ever re-creating those names), `versions.keep_n` above 20 works instead of being silently capped, and… |
| [v0.4.1](https://github.com/BRF-Tech/filex/releases/tag/v0.4.1) | 17 July 2026 | Packaging and documentation. Ready-to-submit app-store manifests for Umbrel, CasaOS, Runtipi, Unraid and Portainer, a refreshed Helm chart, and this documentation site. |
| [v0.4.0](https://github.com/BRF-Tech/filex/releases/tag/v0.4.0) | 17 July 2026 | An inspector panel (press `i`) with metadata, version history, effective permissions and share links for the selected item. |
| [v0.3.0](https://github.com/BRF-Tech/filex/releases/tag/v0.3.0) | 17 July 2026 | Connectivity release. A WebDAV server so you can mount filex as a network drive from Windows, Finder, rclone or davfs2 with full RBAC enforcement; a `filex client` CLI against any remote filex; and multiple webhook targets with… |
| [v0.2.0](https://github.com/BRF-Tech/filex/releases/tag/v0.2.0) | 17 July 2026 | Content search. filex now indexes what is inside your files — text, source code, CSV/JSON/YAML, PDF text layers and Office documents — extracted asynchronously into an embedded index, with highlighted snippets in the results. |
| [v0.1.84](https://github.com/BRF-Tech/filex/releases/tag/v0.1.84) | 17 July 2026 | A design pass over the explorer: a command palette (`Ctrl/Cmd+K`), a shortcuts sheet sourced from one registry so it cannot drift, sortable and date-grouped list columns, a density toggle, an undo snackbar for rename/move/trash,… |
| [v0.1.83](https://github.com/BRF-Tech/filex/releases/tag/v0.1.83) | 16 July 2026 | Embed — Height-constrained embeds could not scroll. |
| [v0.1.82](https://github.com/BRF-Tech/filex/releases/tag/v0.1.82) | 10 July 2026 | Locale-clean viewer/presence strings, AI-surface thumbnail dispatch, fresh demo landing card (#2 #3 #4) |
| [v0.1.81](https://github.com/BRF-Tech/filex/releases/tag/v0.1.81) | 9 July 2026 | Tokens — Per-token username identities — audit, shares and presence attribution. |
| [v0.1.80](https://github.com/BRF-Tech/filex/releases/tag/v0.1.80) | 9 July 2026 | Core — Authenticated grid thumbnails + expandable presence bar. |
| [v0.1.79](https://github.com/BRF-Tech/filex/releases/tag/v0.1.79) | 9 July 2026 | Ws — Real presence identity + confined-subscribe fixes (v0.1.79) |
| [v0.1.78](https://github.com/BRF-Tech/filex/releases/tag/v0.1.78) | 8 July 2026 | Ws — Resolve embedded confined subscribes to the absolute room + per-client frame paths. |
| [v0.1.77](https://github.com/BRF-Tech/filex/releases/tag/v0.1.77) | 8 July 2026 | Ws — Authorize ticketed subscribes as the ticket's user (RBAC) |
| [v0.1.76](https://github.com/BRF-Tech/filex/releases/tag/v0.1.76) | 8 July 2026 | Ws — Allow ticket-only (embedded/cross-origin) WebSocket connections. |
| [v0.1.75](https://github.com/BRF-Tech/filex/releases/tag/v0.1.75) | 8 July 2026 | Realtime — Embed WS live-collab in the core (ticket auth + polling fallback) |
| [v0.1.74](https://github.com/BRF-Tech/filex/releases/tag/v0.1.74) | 8 July 2026 | Folder-share ZIP cache + warmer + progress page, ShareX uploader, live-collab WebSocket. |
| [v0.1.73](https://github.com/BRF-Tech/filex/releases/tag/v0.1.73) | 7 July 2026 | Share — Add GET /api/files/share list endpoint so the modal lists existing links. |
| [v0.1.72](https://github.com/BRF-Tech/filex/releases/tag/v0.1.72) | 7 July 2026 | Share — Native "Paylaş" (Web Share API) button under the mail row. |
| [v0.1.71](https://github.com/BRF-Tech/filex/releases/tag/v0.1.71) | 7 July 2026 | Explorer — 'folder not found' for dead deep links — phantom prefixes 404, denied dirs render as not-found. |
| [v0.1.70](https://github.com/BRF-Tech/filex/releases/tag/v0.1.70) | 7 July 2026 | Web — Don't double the explorer hash in the login redirect. |
| [v0.1.69](https://github.com/BRF-Tech/filex/releases/tag/v0.1.69) | 6 July 2026 | Explorer — Address bar mirrors the current folder for copy-paste deep links. |
| [v0.1.68](https://github.com/BRF-Tech/filex/releases/tag/v0.1.68) | 6 July 2026 | Auth — OIDC callback lands via 200 HTML bounce so a CDN can't strip Set-Cookie. |
| [v0.1.67](https://github.com/BRF-Tech/filex/releases/tag/v0.1.67) | 5 July 2026 | Auth — Mark session + OIDC state cookies Secure on HTTPS (X-Forwarded-Proto aware) |
| [v0.1.66](https://github.com/BRF-Tech/filex/releases/tag/v0.1.66) | 5 July 2026 | Multi-tenant — OIDC callback redirects to tenant host + per-provider cookie domain. |
| [v0.1.65](https://github.com/BRF-Tech/filex/releases/tag/v0.1.65) | 5 July 2026 | Docker — Multi-arch images (linux/amd64 + linux/arm64) |
| [v0.1.64](https://github.com/BRF-Tech/filex/releases/tag/v0.1.64) | 5 July 2026 | Lint — Drop unused adminIDFiltersIn + gofmt config/storage. |
| [v0.1.63](https://github.com/BRF-Tech/filex/releases/tag/v0.1.63) | 5 July 2026 | Auth — SSO-first login (FILEX_OIDC_AUTO_REDIRECT) + FILEX_COOKIE_DOMAIN. |
| [v0.1.62](https://github.com/BRF-Tech/filex/releases/tag/v0.1.62) | 5 July 2026 | Explorer — Empty file/folder size+date + real Trash size/date/icon. |
| [v0.1.61](https://github.com/BRF-Tech/filex/releases/tag/v0.1.61) | 5 July 2026 | Native multi-tenancy (FILEX_MULTI_TENANT) — provider=tenant, two-layer isolation. |
| [v0.1.60](https://github.com/BRF-Tech/filex/releases/tag/v0.1.60) | 5 July 2026 | Sync — Backfill missing backend_mtime + split npm into its own release job. |
| [v0.1.59](https://github.com/BRF-Tech/filex/releases/tag/v0.1.59) | 5 July 2026 | Folder "last activity" date + full/empty trash icon. |
| [v0.1.58](https://github.com/BRF-Tech/filex/releases/tag/v0.1.58) | 4 July 2026 | Folder size+date in explorer (cached, N+1-free) + FILEX_DEFAULT_LOCALE. |
| [v0.1.57](https://github.com/BRF-Tech/filex/releases/tag/v0.1.57) | 4 July 2026 | Storage — Connect any external storage from env (sftp/webdav/ftp/s3) + fix JSON port. |

---

<small>Last refreshed 2026-09-07 from 105 published releases.</small>
