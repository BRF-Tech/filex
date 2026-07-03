# filex documentation

Self‑hosted file manager — a Go backend with a Vue 3 / React / Web Component
frontend, and pluggable **storage**, **auth**, **DB** and **queue** drivers.

New here? Start with [Installation](INSTALLATION.md), then add a storage
([Storage](STORAGE.md)) and, if you want, sign‑in via [SSO](SSO.md).

## Getting started

- [Installation](INSTALLATION.md) — minimal → full Compose → Helm → binary
- [Configuration](CONFIGURATION.md) — every `FILEX_*` variable + `config.yaml`

## Storage

- [Storage](STORAGE.md) — how mounts work, adding one, and the adapters:
  local · S3 / S3‑compatible · SFTP · WebDAV · FTP

## Authentication & access

- [SSO (OIDC)](SSO.md) — sign in with Keycloak / Auth0 / Authentik / Okta / …
- [LDAP & reverse‑proxy auth](LDAP.md) — Active Directory / LDAP, header auth
- [RBAC & permissions](RBAC.md) — account roles, per‑storage RBAC, per‑item grants

## Integrations

- [OnlyOffice](ONLYOFFICE.md) — in‑browser editing of Office documents
- [Converter](CONVERT-INTEGRATION.md) — universal file conversion

## Features

- [Sharing & file requests](SHARING.md) — public download links + upload/file‑drop
- [Thumbnails](thumbnails.md) — image / video / pdf / office previews
- [Search](SEARCH.md) — embedded full‑text index
- [Notifications](NOTIFICATIONS.md) — webhook + in‑app bell
- [Trash & versioning](TRASH-VERSIONING.md) — soft‑delete/restore + file history
- [Replication](REPLICATION.md) — primary→replica mirroring & reconcile

## Deployment

- [Deployment](DEPLOYMENT.md) — reverse proxy, HTTPS, scaling, backup
- [Docker](DOCKER.md) — images & compose details
- Packaging in the repo: [`deploy/compose/`](../deploy/compose/) (minimal + full)
  and [`deploy/helm/filex/`](../deploy/helm/filex/) (Kubernetes)

## Develop & integrate

- [Architecture](ARCHITECTURE.md) — how the pieces fit
- [Backend](BACKEND.md) — internals
- [HTTP / component API](API.md)
- [Embedding the explorer](INTEGRATION.md) — Vue / React / Web Component
- [AI & MCP](MCP.md) — API tokens, scopes, and the MCP endpoint for agents

---

Found something wrong or missing? Please open an issue — see
[CONTRIBUTING.md](CONTRIBUTING.md).
