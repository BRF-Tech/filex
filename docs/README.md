# filex documentation

Self‑hosted file manager — a Go backend with a Vue 3 / React / Web Component
frontend, and pluggable **storage**, **auth**, **DB** and **queue** drivers.

New here? Start with [Installation](INSTALLATION.md), then add a storage
([Storage](STORAGE.md)) and, if you want, sign‑in via [SSO](SSO.md).

## Getting started

- [Installation](INSTALLATION.md) — minimal → full Compose → Helm → binary
- [Configuration](CONFIGURATION.md) — every `FILEX_*` variable + `config.yaml`
- [Releases](RELEASES.md) — every release with a plain‑English summary
  (generated from the GitHub releases at build time)
- [Updates](UPDATES.md) — how filex checks for, and installs, a new release

## Storage

- [Storage](STORAGE.md) — how mounts work, adding one, and the adapters:
  local · S3 / S3‑compatible · SFTP · WebDAV · FTP · SMB/CIFS
- [NAS over NFS / SMB](STORAGE.md#nas-nfs-smb-and-friends) — mount it with the
  OS, serve it with `local`, and the three traps that come with it
- [Slow storage](STORAGE.md#slow-storage) — what is already cached, and what is
  actually worth tuning
- [Storage plugins](PLUGINS.md) — teaching filex a backend it does not ship:
  installing one, upgrading it in place, and writing one (the protocol, the Go
  SDK, presigned URLs and multipart) — plus **conformance**, the probes that
  refuse a plugin which cannot do what it claims

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
- [Converter](CONVERT-INTEGRATION.md) — universal file conversion

## Features

- [Desktop app](DESKTOP.md) — Windows/Linux/macOS app: multiple accounts, background sync
- [Folder sync](SYNC.md) — how a folder on your PC is kept in step with the server
- [Uploads](UPLOADS.md) — the staged, resumable upload path: chunked, works on
  every driver, survives a dropped connection
- [Sharing & file requests](SHARING.md) — public download links + upload/file‑drop
- [Thumbnails](thumbnails.md) — image / video / pdf / office previews
- [Search](SEARCH.md) — embedded full‑text index
- [Notifications](NOTIFICATIONS.md) — webhook + in‑app bell
- [Trash & versioning](TRASH-VERSIONING.md) — soft‑delete/restore + file history
- [Replication](REPLICATION.md) — primary→replica mirroring & reconcile
- [Quotas](QUOTAS.md) — per‑user ceilings: what counts, when it is
  released, and how a public drop link is billed
- [Protection & antivirus](PROTECTION.md) — ClamAV scanning plus the trash and
  version retention windows, behind one admin screen
- [End‑to‑end encryption](E2E-ENCRYPTION.md) — client‑side WebCrypto folders;
  the server stores ciphertext and never receives a key
- [Multi‑tenancy](MULTI-TENANCY.md) — provider/tenant mode, per‑tenant isolation
  on one instance
- [ShareX](SHAREX.md) — the screenshot‑upload endpoint and its custom uploader

## Deployment

- [Deployment](DEPLOYMENT.md) — reverse proxy, HTTPS, scaling, backup
- [Docker](DOCKER.md) — images & compose details
- [Metrics](METRICS.md) — the Prometheus surface, how to scrape it, and
  the handful of alerts worth having
- Packaging in the repo: [`deploy/compose/`](../deploy/compose/) (minimal + full)
  and [`deploy/helm/filex/`](../deploy/helm/filex/) (Kubernetes)

## Develop & integrate

- [Architecture](ARCHITECTURE.md) — how the pieces fit
- [Backend](BACKEND.md) — internals
- [HTTP / component API](API.md)
- [Embedding the explorer](INTEGRATION.md) — Vue / React / Web Component
- [AI & MCP](MCP.md) — API tokens, scopes, and the MCP endpoint for agents
- [Migration](MIGRATION.md) — moving off `@brftech/file-explorer`

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

⚠ Two more are excluded from the site **and** from this repository, so they are
deliberately listed without links — a deployment runbook carrying one
installation's real host names and paths, and a migration guide written for a
single downstream application. They would be dead links here, which is exactly
the failure the note above describes.

Handover notes under `docs/handovers/` are excluded by glob for the same
reason: they are working notes between maintainers, and the next one must be
excluded by default rather than by somebody remembering to add it.

---

Found something wrong or missing? Please open an issue — see
[CONTRIBUTING.md](CONTRIBUTING.md).
