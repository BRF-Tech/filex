---
title: Releases
description: Every filex release with a plain-English summary of what changed — generated from the GitHub releases at build time.
---

<!-- GENERATED FILE — do not edit by hand. Your edits will be overwritten.
     Source:      GitHub Releases for BRF-Tech/filex
     Generator:   docs-site/scripts/fetch-releases.mjs
     Summaries:   docs-site/data/release-highlights.json (hand-written)
     Regenerate:  cd docs-site && npm run releases -->

# Releases

Every published filex release, newest first. This page is generated from the
[GitHub releases](https://github.com/BRF-Tech/filex/releases) each time the site is built, so a new
tag shows up here without anyone writing it twice.

Whether filex installs a release by itself depends on which part of the version moved —
see [Updates](./UPDATES.md).

::: tip Latest — v0.13.4, 10 August 2026
A fix for a disk that fills up on its own. Uploads larger than 32 MiB are buffered to a temporary file, and those files were never removed — every request answered normally while the disk quietly drained (29 GB in two hours on a live server). Every upload surface now cleans up after itself, including requests it rejects.
:::

```bash
docker pull ghcr.io/brf-tech/filex:slim-v0.13.4
docker pull ghcr.io/brf-tech/filex:full-v0.13.4
```

## v0.13.4

<span class="filex-release-date">10 August 2026</span>

A fix for a disk that fills up on its own. Uploads larger than 32 MiB are buffered to a temporary file, and those files were never removed — every request answered normally while the disk quietly drained (29 GB in two hours on a live server). Every upload surface now cleans up after itself, including requests it rejects.

**Fixed**

- **Upload** — Multipart temp files outlived the response and filled the disk.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.13.4) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.13.4`

## v0.13.3

<span class="filex-release-date">7 August 2026</span>

The share button now appears in the desktop app. The explorer has always had one, but it was gated on the Web Share API, which Electron does not ship — so rather than build a second share UI, the app puts a native handler behind the standard API and the existing button lights up. On Windows and Linux that is a native menu (copy link, copy message, email, open in browser); the OS share sheet needs WinRT, which Electron does not expose. Also fixes the sync engine being reported missing when the app runs from source.

**New**

- **Desktop** — The share button works here too, and three things found by using it.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.13.3) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.13.3`

## v0.13.2

<span class="filex-release-date">7 August 2026</span>

Sixteen components were rendering as raw, unstyled HTML in every embedded surface — the share dialog, the convert dialog, the presence bar and nine file viewers. Vue's scoped styles compile to a build-specific hash, and the web-component build was pairing components from one build with CSS from another, so every scoped rule was dead. Nothing errored, which is why it survived. This affected **every** embedder, not only the desktop app.

**Fixed**

- **Core** — Scoped styles were dead in every embed — 16 components rendered raw.

**Other changes**

- **Desktop** — Say out loud which commit is being packaged.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.13.2) · `ghcr.io/brf-tech/filex:slim-v0.13.2`

## v0.13.1

<span class="filex-release-date">7 August 2026</span>

The explorer's onboarding tour sat on top of the desktop app's Settings panel. The tour attaches to `<body>`, so hiding the explorer left it exactly where it was.

**Fixed**

- **Desktop** — The onboarding tour covered Settings, and the test said it did not.

**Other changes**

- **Desktop** — Drive the PACKAGED app, not just the source tree.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.13.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.13.1`

## v0.13.0

<span class="filex-release-date">7 August 2026</span>

The desktop app is a file manager, not an admin console. Signing in used to land you on the server's dashboard because the shell embedded the whole admin SPA; it now shows the file explorer, an account rail down the left, and a gear for the app's own settings. Also fixes starred files, recently-opened, starring and tags in **any** cross-origin embed — four calls hardcoded credentialed requests, which a wildcard CORS response may not answer, so those lists came back empty with no error.

**Fixed**

- **Desktop** — The window is a file manager, not the admin console.

**Other changes**

- **Desktop** — The bundled engine no longer carries a second copy of the UI.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.13.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.13.0`

## v0.12.0

<span class="filex-release-date">7 August 2026</span>

Selective folder sync. A folder on your computer and a folder on a filex server are kept in step in both directions, in the background. The engine ships as `filex sync` in the CLI and the desktop app runs that same binary, so the terminal and the app can never disagree about what is paired. The first sync of a pair deletes nothing, a delete never beats an edit, a file changed in both places keeps both copies, and anything sync removes locally is kept for 30 days.

**New**

- **Sync** — Keep a local folder and a server folder in step.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.12.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.12.0`

## v0.11.0

<span class="filex-release-date">7 August 2026</span>

The desktop app, for Windows and Linux. It runs the same web UI this repo already ships, and sign-in happens in your browser — the app opens the server's own login page and waits, so installs behind an identity provider (OIDC, SSO, passkeys, MFA) work. Multiple accounts on multiple servers, a background tray, optional start-at-login, and tokens held in the OS keychain. Also fixes cross-origin embedding of the web app, which every split-origin deployment hit.

**New**

- Desktop app (Windows + Linux), browser sign-in, install prompts.

**Other changes**

- **Desktop** — Version the app with the product, not on its own.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.11.0) · `ghcr.io/brf-tech/filex:slim-v0.11.0`

## v0.10.2

<span class="filex-release-date">6 August 2026</span>

Completes the guard added in 0.10.1: a sweep found four more places that could still write a file onto a folder — archive extraction and creation, the OnlyOffice save-back, and version restore — plus replication, where a collision does its real damage. Also fixes uploads through the public file-drop link on S3 providers that require a `Content-Length`.

Refuses a file written onto a folder, on every write surface — and the reverse
(a folder created onto a file).

An object store accepts `X` and `X/…` side by side. A directory-backed mirror
cannot represent that: the prefix stops listing and the objects under it
silently lose their backup. Measured on a live DR mirror — 2760 syncs in 24h,
1016 versions of one PNG, a 43 MiB folder occupying 45 GB. Overwriting a file
with a file is unchanged; that was never the problem.

Also fixes uploads through the public file-drop link on providers that require
a Content-Length (411), and adds `filex storage scan-collisions` to report
damage that predates the guard.

Full notes: CHANGELOG.md (0.10.1 + 0.10.2).

Built and published manually: GitHub Actions was in a major outage on release
day, so no workflow run could be scheduled. Same steps as the release workflow
(frontend → embed → goreleaser). Binaries verified to report `0.10.2 (c3b1c53)`
and all six checksums verified before upload. Container images (amd64+arm64)
were published the same way to ghcr.io/brf-tech/filex.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.10.2)

## v0.10.0

<span class="filex-release-date">6 August 2026</span>

Two explorer changes reported by a deployment whose users mount WebDAV from macOS. Dot-prefixed files can now be shown or hidden and are hidden by default (a Mac leaves `.DS_Store` and `._name` litter in every folder it opens), and when exactly one storage is visible that storage opens directly instead of making you click through a one-row page. Shipped as a minor rather than a patch on purpose: a release that changes what the explorer does on screen has to be one the operator opts into.

**New**

- **UI** — Open the storage directly when only one is visible.
- **UI** — Show/hide dot-files, off by default.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.10.0) · `ghcr.io/brf-tech/filex:slim-v0.10.0`

## v0.9.0

<span class="filex-release-date">6 August 2026</span>

Closes the ten items a multi-tenant deployment filed against v0.8.0. The important one is a security fix: a tenant admin could reach every other tenant's storages over WebDAV, because `/dav` does its own Basic authentication and so ran outside the chain that applies the tenant scope. Also: uploads work on strict S3 providers that require a `Content-Length`, a transient 503 no longer kills a whole sync run, and per-user enable/disable and quota reporting land on the user object.

**New**

- **Users** — Carry used_bytes and quota_bytes on the user object.
- **Users** — Honour provider_id when creating a user.
- **Users** — Per-user enable/disable switch.

**Fixed**

- **WebDAV** — Scope /dav to the caller's tenant.
- **Grants** — Require a qualified path, and apply the tenant gate.
- **Quota** — 404 for an unknown user, and serve quota under /users/{id}.
- **S3** — Send Content-Length on PutObject so uploads work on strict providers.
- **S3** — Stat a prefix as a directory.
- **S3** — Transient 503 no longer kills a whole sync run + name the file that failed to thumbnail.
- **UI** — Surface upload failures to the user.
- **Users** — Confine Get/Update/Delete to the caller's tenant.

**Other changes**

- **Backend** — Correct the admin users surface, document delete semantics.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.9.0) · `ghcr.io/brf-tech/filex:slim-v0.9.0`

## v0.8.0

<span class="filex-release-date">29 July 2026</span>

filex now knows which releases exist, and can install them. What it does is decided by which part of the version moved: a patch applies itself when the policy allows, a minor is announced and applied with one click, a major is announced with instructions. Nothing moves until you opt in — the default only checks and tells you. Container installs never self-apply by design, because a binary replaced inside a running container disappears at the next `docker compose up` and the version silently reverts.

**New**

- **Update** — Release awareness + self-upgrade (x.y.z policy)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.8.0) · `ghcr.io/brf-tech/filex:slim-v0.8.0`

## v0.7.6

<span class="filex-release-date">29 July 2026</span>

Denials on the AI/MCP surface answer `403` instead of `500`. A `5xx` reads as "server glitch, retry", so agents and HTTP clients were retrying requests that could never succeed while the real cause — a path outside the token's confined root, or a missing grant — hid behind a generic server error.

**Fixed**

- **AI** — Map confinement and permission denials to 403 instead of 500.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.7.6) · `ghcr.io/brf-tech/filex:slim-v0.7.6`

## v0.7.5

<span class="filex-release-date">19 July 2026</span>

An internal refactor with no behaviour change: the storage-scoped path hash had been copy-pasted across nine call sites, so the same file could map to different rows if any copy drifted. It is now one function, moved verbatim, with a test pinning it byte-for-byte against the old implementation.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.7.5) · `ghcr.io/brf-tech/filex:slim-v0.7.5`

## v0.7.4

<span class="filex-release-date">18 July 2026</span>

Two explorer fixes: the trash bin now appears in the secondary pane of split view (the two panes were offset by a row), and tall listings scroll inside their pane instead of scrolling the whole page.

**Fixed**

- **UI** — v0.7.4 split view trash-row parity + standalone page-scroll.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.7.4) · `ghcr.io/brf-tech/filex:slim-v0.7.4`

## v0.7.3

<span class="filex-release-date">18 July 2026</span>

Split view's right-click menu now matches the main panel exactly — it was a shorter, separate list missing rename, delete, share, convert and tags. Dropping a file onto the folder it already lives in is a silent no-op rather than an error, and the two panes' breadcrumbs finally line up.

**Fixed**

- **UI** — v0.7.3 split view menu parity + same-dir drop no-op + breadcrumb alignment.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.7.3) · `ghcr.io/brf-tech/filex:slim-v0.7.3`

## v0.7.2

<span class="filex-release-date">18 July 2026</span>

Split-view polish: the main panel's breadcrumb no longer spans the full width, and right-clicking a row in the secondary pane opens a real menu instead of only selecting the row.

**Fixed**

- **UI** — v0.7.2 split view breadcrumb width + secondary pane context menu.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.7.2) · `ghcr.io/brf-tech/filex:slim-v0.7.2`

## v0.7.1

<span class="filex-release-date">18 July 2026</span>

A round of layout and accessibility fixes for embedders. The explorer no longer overflows its host by 2px (the outer scrollbar that produced in embeds is gone), the toolbar folds overflowing actions into a `⋯` menu instead of wrapping, split view renders both panes with the same view components, and the default accent colour was darkened so white text on primary buttons meets WCAG AA.

**Fixed**

- **UI** — v0.7.1 polish round.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.7.1) · `ghcr.io/brf-tech/filex:slim-v0.7.1`

## v0.7.0

<span class="filex-release-date">17 July 2026</span>

Three additions. **Branding** — a settings-driven identity (name, logo, accent, footer) for the public share, PIN, file-drop and folder-browse pages plus the admin login, with per-tenant overrides. **End-to-end encrypted folders (MVP)** — a password-protected folder encrypted in the browser, where the server never sees the password, a key or plaintext; there is no recovery, so read the threat model first. Plus a `FILEX_CLOUD` flag that is off by default and changes nothing while off.

**New**

- **Kimlik** — v0.7.0 identity & trust wave.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.7.0) · `ghcr.io/brf-tech/filex:slim-v0.7.0`

## v0.6.0

<span class="filex-release-date">17 July 2026</span>

Tabs and split view. Open several locations as tabs, split the active tab into two panes that navigate independently, and drag files between them to move (same storage) or copy (across storages). Also a gallery view, browsable public folder shares that open a navigable page instead of jumping straight to a ZIP, and per-file comment threads.

**New**

- **Calisma** — v0.6.0 workspace wave.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.6.0) · `ghcr.io/brf-tech/filex:slim-v0.6.0`

## v0.5.0

<span class="filex-release-date">17 July 2026</span>

A large interface release: eight built-in themes with independent light and dark variants, fully rebindable keyboard shortcuts, Quick Look (peek the selected file with Space), an operations centre that collects uploads and background jobs into one corner badge with retry, and a first-run tour. Accessibility work throughout — proper grid/listbox roles, a keyboard-navigable context menu, focus trapping in modals, and `prefers-reduced-motion` support.

**New**

- **Gorunum** — v0.5.0 appearance wave.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.5.0) · `ghcr.io/brf-tech/filex:slim-v0.5.0`

## Earlier releases

The 33 releases before v0.5.0, in brief. Full notes are on GitHub.

| Version | Date | What changed |
|---|---|---|
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

<small>Generated 2026-08-10 from 53 published releases.</small>
