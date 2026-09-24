# filex documentation

Self‑hosted file manager — a Go backend with a Vue 3 / React / Web Component
frontend, and pluggable **storage**, **auth**, **DB** and **queue** drivers.

New here? Start with [Installation](INSTALLATION.md), then add a storage
([Storage](STORAGE.md)) and, if you want, sign‑in via [SSO](SSO.md).

## Getting started

- [Installation](INSTALLATION.md) — minimal → full Compose → Helm → binary
- [Configuration](CONFIGURATION.md) — every `FILEX_*` variable + `config.yaml`
- [Databases](DATABASES.md) — SQLite, PostgreSQL, MySQL/MariaDB: which to pick,
  what each needs, and what "supported" is checked to mean
- [Releases](RELEASES.md) — every release with a plain‑English summary
  (generated from the GitHub releases when a release is cut, by
  `npm run releases`)
- [Updates](UPDATES.md) — how filex checks for, and installs, a new release
- [Demo mode](DEMO.md) — running filex as a public playground: the published
  account, the read-only guard over the whole admin surface, and what a demo
  does *not* publish

## Storage

- [Storage](STORAGE.md) — how mounts work, adding one, and the adapters:
  local · S3 / S3‑compatible · SFTP · WebDAV · FTP · SMB/CIFS — including
  [symlinks inside a local storage](STORAGE.md#symlinks) and the
  *Follow symlinks that leave this folder* option
- [Moving files between storages](STORAGE.md#moving-files-between-storages) — what
  copy, cut and drag mean when the two ends are different storages
- [NAS over NFS / SMB](STORAGE.md#nas-nfs-smb-and-friends) — mount it with the
  OS, serve it with `local`, and the three traps that come with it
- [Slow storage](STORAGE.md#slow-storage) — what is already cached, and what is
  actually worth tuning
- [Usage & cost](USAGE.md) — reading the provider's own daily report, pricing it
  with a table you can edit, and the two rows that must never be added together
- [Storage plugins](PLUGINS.md) — teaching filex a backend it does not ship:
  installing one, upgrading it in place, and writing one (the protocol, the Go
  SDK, presigned URLs and multipart) — plus **conformance**, the probes that
  refuse a plugin which cannot do what it claims
- [Apps (app plugins)](APP-PLUGINS.md) — sandboxed WebAssembly apps that add
  actions to the file menu, screens filex draws for them, and public links for
  outside participants: the two that ship (**e-Signature**,
  `BRF-Tech/filex-sign`, and **Convert**, `BRF-Tech/filex-convert`), installing
  one from GitHub through the permission review, what the administrator
  controls, apps that wake up on their own,
  [signing documents end to end](APP-PLUGINS.md#signing-documents-end-to-end)
  (inside and outside signers, PINs, deadlines, the audit trail, verifying, your
  own certificate authority), [converting files](APP-PLUGINS.md#converting-files),
  what guards an app's public links, limits, troubleshooting
- [Writing an app plugin](PLUGIN-KIT.md) — the manifest, the six exports,
  every host function and its permission, the screen catalogue, the **hourly
  wake-up** that lets an app schedule its own work to the minute, the test kit
  that runs before the wasm build, and a signing walk-through with stock Go
- [App plugin wire contract](APP-PLUGINS-API.md) — the exact routes, JSON
  shapes and frontend conventions the explorer, admin panel and public shell
  are built against

## Language & direction

- [Writing a language pack](PLUGIN-KIT.md#writing-a-language-pack) — adding a
  language to filex without waiting for a release: what makes a manifest a
  language pack, the exported catalogue and the per-key context that comes with
  it, the byte limits, CLDR plural categories (`<key>_few` …), the `server.*`
  table that carries the text the server writes — mail, notifications, the
  pages behind a link — and the validator that refuses a pack this version
  would not accept. Spanish, German and French ship as examples, and
  `BRF-Tech/filex-lang-template` is the template to start from
- [Right-to-left languages](RTL.md) — how filex turns for Arabic, Hebrew,
  Persian, Urdu: the one list that decides, the `dir` rule for pages and
  embeds, what never mirrors (document space, machine text), mixed-direction
  text, and the logical-properties rule the RTL guard test enforces

## Reaching filex without a browser

- [Protocols](PROTOCOLS.md) — the map: which protocols filex connects *to*, which
  it can be *reached as*, and the credential each one takes
- [WebDAV](WEBDAV.md) — mapping a drive from Windows / macOS / Linux
- [`filex mount`](PROTOCOLS.md#filex-mount) — a remote server over ordinary
  HTTPS: a folder on Linux, a drive letter on Windows; ⚠ not a sync, and not
  available on macOS
- [CLI client](CLI.md) — `filex client` and `filex sync` against a remote server

## Authentication & access

- [SSO (OIDC)](SSO.md) — sign in with Keycloak / Auth0 / Authentik / Okta / …
- [LDAP & reverse‑proxy auth](LDAP.md) — Active Directory / LDAP, header auth
- [RBAC & permissions](RBAC.md) — account roles, per‑storage RBAC, per‑item grants

## Integrations

- [OnlyOffice](ONLYOFFICE.md) — in‑browser editing of Office documents
- [Converter side-car](CONVERT-INTEGRATION.md) — universal file conversion
  through a separate service (the [Convert app](APP-PLUGINS.md#converting-files)
  is the sandboxed alternative)

## Features

- [Desktop app](DESKTOP.md) — Windows/Linux/macOS app: multiple accounts, background sync,
  [dragging files out onto the desktop](DESKTOP.md#dragging-files-out),
  [opening Office documents off your own disk](DESKTOP.md#opening-documents-from-your-computer),
  [a portable Windows copy that installs nothing](DESKTOP.md#portable-windows)
- [Folder sync](SYNC.md) — how a folder on your PC is kept in step with the server
- [Uploads](UPLOADS.md) — the staged, resumable upload path: chunked, works on
  every driver, survives a dropped connection
- [Sharing & file requests](SHARING.md) — public download links + upload/file‑drop;
  **My shares** and reading a link's PIN back, the one branded public screen and
  its PIN lock-out, the maximum link life, and how the folder-ZIP cache is bounded
- [Thumbnails](thumbnails.md) — image / video / pdf / office previews
- [Search](SEARCH.md) — embedded full‑text index: forgiving filename
  matching (separators, several words in any order, folders, typos), VS
  Code-style subsequence scoring, ranked results, `tag:` filters, and an index
  that rebuilds itself after an upgrade without going dark
- [Realtime updates & presence](REALTIME.md) — the WebSocket an open explorer
  runs on: the ticket, the change and presence frames, how a burst is
  coalesced (and why a plain trailing debounce starves), and the 12 s polling
  fallback
- [Notifications](NOTIFICATIONS.md) — webhook + in‑app bell: the unread badge,
  the full list inside the explorer for everybody, and where a click goes
- [Trash & versioning](TRASH-VERSIONING.md) — soft‑delete/restore + file history
- [Replication](REPLICATION.md) — primary→replica mirroring & reconcile
- [Quotas](QUOTAS.md) — per‑user ceilings: what counts, when it is
  released, and how a public drop link is billed
- [Protection & antivirus](PROTECTION.md) — ClamAV scanning, through a local
  binary or a clamd daemon over TCP or a unix socket, plus the trash and version
  retention windows and the share-link life ceiling, behind one admin screen
- [End‑to‑end encryption](E2E-ENCRYPTION.md) — client‑side WebCrypto folders;
  the server stores ciphertext and never receives a key. Recovery keys, optional
  operator key escrow, and exactly what each one can and cannot open
- [Multi‑tenancy](MULTI-TENANCY.md) — provider/tenant mode, per‑tenant isolation
  on one instance
- [ShareX](SHAREX.md) — the screenshot‑upload endpoint and its custom uploader

## Deployment

- [Deployment](DEPLOYMENT.md) — reverse proxy, HTTPS, scaling, backup
- [Docker](DOCKER.md) — images, compose details, and which user the container runs as
- [Metrics](METRICS.md) — the Prometheus surface, how to scrape it, and
  the handful of alerts worth having
- Packaging in the repo: [`deploy/compose/`](../deploy/compose/) (minimal + full)
  and [`deploy/helm/filex/`](../deploy/helm/filex/) (Kubernetes)

## Develop & integrate

- [Architecture](ARCHITECTURE.md) — how the pieces fit
- [Backend](BACKEND.md) — internals
- [HTTP / component API](API.md)
- [Themes & appearance](INTEGRATION.md#themes) — the shipped palettes, an
  operator's own themes and instance default (Admin → **Appearance**), and the
  custom stylesheet that is off until you switch it on
- [Embedding the explorer](INTEGRATION.md) — Vue / React / Web Component, and the
  two options every wrapper shares: the **navigation panel** (`sideNav`) and how
  much of the explorer to show (`uiProfile`: `standard` · `simple` — two values,
  and the third one, `drive`, was **removed** after v0.40.0; pass `simple`)
- [AI & MCP](MCP.md) — API tokens (including the `user` / `app` token kinds), the permissions a token names — at least one, and never a blank list meaning all of them — the ceiling that stops a narrow token issuing a wider credential (`403 token_ceiling`), the MCP endpoint for agents, and credential-free upload tickets for large local files

## Repo only — not published to docs.filex.sh

These live in the repo and are deliberately kept off the published site
(`srcExclude` in `docs-site/.vitepress/config.mts`) — ⚠ a page VitePress builds
is reachable by URL and indexable whether or not anything links to it, so
"leave it out of the sidebar" is not the same as "do not publish it".

⚠ Linked by full URL rather than relatively: a relative link to a page the site
does not build is a **dead link that fails the docs build**, which is how this
list was written the first time.

- [Cloud preparation](https://github.com/BRF-Tech/filex/blob/main/docs/CLOUD.md)
  — scaffolding for a hosted offering that has **not** launched; publishing it
  would announce a service that does not exist
- **This page.** `docs/README.md` is the index GitHub renders when you open
  `docs/`, and it stays in the repository — but the site already has a home
  page and a sidebar, so publishing it put a second, competing table of
  contents at `/README`, one whose last section explains which pages are kept
  off the site. That is a note for contributors, not for readers

⚠ Three more are excluded from the site **and** from this repository, so they
are deliberately listed without links — a deployment runbook carrying one
installation's real host names and paths, and two migration guides written for
downstream applications that were never public (`MIGRATION.md`, off the in-tree
`@brftech/file-explorer`, and `MIGRATION_FISHAPP.md`). They would be dead links
here, which is exactly the failure the note above describes.

> `MIGRATION.md` was the third case and did not look like one: it was stripped
> from the public repository like the other two, but nothing removed the links
> to it, and it was not in `srcExclude` — so the published site carried it
> while the published README pointed at a file that is not there.

Handover notes under `docs/handovers/` are excluded by glob for the same
reason: they are working notes between maintainers, and the next one must be
excluded by default rather than by somebody remembering to add it.

---

Found something wrong or missing? Please open an issue — see
[CONTRIBUTING.md](CONTRIBUTING.md).
