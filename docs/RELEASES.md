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

::: tip Latest - v0.59.1, 9 November 2026
Reviewing an app update no longer waits for the whole app to download. The permission review of an app from a repository or an address - an install, an update, a store link, a plugin request - downloaded the module, the interface and the office engine before it answered; for the Office editor that is 140 MB from GitHub, and Review update answered 504 from a reverse proxy or came minutes later. The review now reads the app's filex-app.json alone, for every version an app has published: its permissions, and the SHA-256 it pins for each file. Install and Upgrade then download the files and refuse any that does not match what was reviewed.
:::

```bash
docker pull ghcr.io/brf-tech/filex:slim-v0.59.1
docker pull ghcr.io/brf-tech/filex:full-v0.59.1
```

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

## Earlier releases

The 133 releases before v0.53.0, in brief. Full notes are on GitHub.

| Version | Date | What changed |
|---|---|---|
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

<small>Last refreshed 2026-11-09 from 153 published releases.</small>
