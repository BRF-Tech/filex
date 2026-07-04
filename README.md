# filex — Self-hosted File Manager

> **Status:** v0.1.0 in active development · MIT licensed

`filex` is a self-hosted file manager with a Go backend and a multi-framework frontend (Vue 3, React, plain Web Component). Designed to be a single binary or a single Docker container, with pluggable **storage**, **auth**, and **DB** drivers.

```
┌─────────────────────────────────────────────────────────────┐
│  filex (Go binary, ~40 MB slim / 250 MB w/ thumbnails)     │
├─────────────────────────────────────────────────────────────┤
│  HTTP API (chi)  │  Admin UI (Vue 3, embedded)             │
│  Auth Drivers:   │  local · oidc · ldap · proxy-header     │
│  Storage Drivers:│  local · s3 · ftp · sftp · webdav       │
│  DB Drivers:     │  sqlite (default) · mysql · postgres    │
│  Queue Drivers:  │  sqlite (default) · redis · postgres    │
│  Sync Worker:    │  ETag diff + tombstone guard            │
│  Replica Layer:  │  primary→replica + rules + reconcile    │
│  Notifications:  │  webhook + in-app bell + read/unread    │
│  Search:         │  Bleve (full-text, embedded)            │
│  Thumbnails:     │  image · video · pdf · office           │
│  Plug & Play:    │  OnlyOffice · Drawio · Mermaid          │
└─────────────────────────────────────────────────────────────┘
                          ▲
                          │ HTTP API
       ┌──────────────────┼──────────────────┐
       │                  │                  │
   @brftech/         @brftech/          @brftech/
   filex-core        filex             filex-react
   (Vue 3 SFC)       (Web Component)   (React adapter)
       │                  │                  │
       ▼                  ▼                  ▼
   Vue 3 apps       Any framework      React apps
                    (vanilla, Angular,
                    Svelte, Solid, …)
```

## Quick start — Docker

```bash
docker run -p 5212:5212 -v $(pwd)/data:/data ghcr.io/brf-tech/filex:latest
```

Open http://localhost:5212/admin — first run prints credentials and embed instructions to the console.

## Quick start — binary

```bash
# Download from https://github.com/brf-tech/filex/releases
./filex serve
```

Output:
```
═══════════════════════════════════════════════════════════════
  filex v0.1.0 · self-hosted file manager
═══════════════════════════════════════════════════════════════
  Listening on:   http://0.0.0.0:5212
  Admin UI:       http://0.0.0.0:5212/admin
  Embed JS:       http://0.0.0.0:5212/embed.js

  First run detected. Initial admin user created:
    Email:    admin@local
    Password: kT9_x4Pq2Nm-BvLs
  Saved to:  ~/.filex/.first-run.txt (mode 0600, shown ONCE)
  Change at: /admin/profile
═══════════════════════════════════════════════════════════════
```

## Self-host with Compose or Helm

The bare `docker run` above is enough to try filex out. For a real deployment,
ready-made stacks live in [`deploy/`](deploy/):

- **[`deploy/compose/`](deploy/compose/)** — Docker Compose:
  - **minimal** — filex + SQLite + local disk (one service, zero dependencies).
  - **full** — filex + PostgreSQL + Redis + Caddy (auto-HTTPS), plus toggleable
    add-ons: **OnlyOffice**, **Drawio**, universal **converter**, **MinIO** (S3).
    Turn each on/off with a Compose profile in `.env`.
- **[`deploy/helm/filex/`](deploy/helm/filex/)** — a Helm chart for Kubernetes
  (Deployment + PVC + optional Ingress). Every add-on above is an `enabled`
  toggle in `values.yaml` — bundle PostgreSQL / Redis / MinIO, or wire external
  OnlyOffice / Drawio / converter.

Step-by-step instructions for each tier are in
[docs/INSTALLATION.md](docs/INSTALLATION.md).

## Embed in your app

### Vue 3
```bash
pnpm add @brftech/filex-core
```
```vue
<script setup>
import { FileExplorer } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';
</script>
<template>
  <FileExplorer :config="{ apiBase: 'http://localhost:5212', auth: { kind: 'bearer', token: '…' } }" />
</template>
```

### React
```bash
pnpm add @brftech/filex-react
```
```jsx
import { FileManager } from '@brftech/filex-react';
<FileManager config={{ apiBase: 'http://localhost:5212' }} onError={(e) => console.error(e)} />
```

### Vanilla JS / any framework
```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212"></filex-explorer>
```

## Features

- **Multi-storage** — Mount many storages at once. Each appears as a top-level folder named by you.
- **Driver-pluggable everything** — Storage / Auth / DB / Queue drivers are opt-in via env (`FILEX_AUTH_DRIVERS=local,oidc`, `FILEX_QUEUE_DRIVER=postgres`, …).
- **Replica + reconciliation** — Primary→replica fan-out (mirror / append-only / skip per path-glob rule), read fallback, scheduled status report, one-click "Fix all" UI.
- **Persistent op queue** — Restart-safe `ops_queue` table (or Redis / Postgres). Worker pool with retries + cancel + admin dashboard.
- **Notifications** — Generic JSON webhook (Slack/Discord-agnostic) + in-app bell with read/unread + per-user mute matrix.
- **DB-backed file tree** — Listings come from the DB cache (1-5 ms), not the storage backend (~100 ms).
- **Storage sync** — Periodic ETag-diff polling catches manual changes (e.g. someone uploads from S3 console).
- **Storage path guard** — Empty / `"/"` prefix is rejected; pre-existing files at the bucket root are never silently shadowed.
- **Single binary** — `goreleaser` matrix: linux/macOS/Windows × amd64/arm64. CGO=0, modernc.org/sqlite.
- **Plug & play services** — OnlyOffice, Drawio, Mermaid mount only if URL is configured.
- **Multi-framework component** — One Vue 3 source, exported as Vue / React / Web Component.
- **Sharing** — PIN, expiry, max-downloads, public viewer.
- **Search** — Bleve embedded, full-text + metadata.
- **Thumbnails** — Image (GD), video (ffmpeg), PDF (gs), Office (libreoffice). Capability-aware.
- **Code editor** — Monaco eager-loaded, falls back to highlight.js.

## Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Documentation

- [Installation](docs/INSTALLATION.md)
- [Configuration](docs/CONFIGURATION.md)
- [Backend API spec](docs/BACKEND.md)
- [Component API](docs/API.md)
- [Docker](docs/DOCKER.md)
- [Full documentation index](docs/README.md)

## Development

```bash
git clone https://github.com/brf-tech/filex.git
cd filex
pnpm install
pnpm run build:all    # builds packages, web, then Go binary
./bin/filex serve
```

Subdirectories:
- `backend/` — Go HTTP service (cmd/filex, internal/*, db/queries, db/migrations)
- `packages/core` — `@brftech/filex-core` (Vue 3 SFC, source of truth)
- `packages/webcomponent` — `@brftech/filex` (Web Component wrapper)
- `packages/react` — `@brftech/filex-react` (React adapter via @lit/react)
- `web/` — Vue 3 admin UI (embedded into Go binary via `go:embed`)
- `demo/` — Standalone HTML demos for each framework
- `docker/` — Dockerfiles + compose
- `deploy/` — ready-made Compose stacks + Helm chart (see [`deploy/`](deploy/))
- `docs/` — Markdown documentation

## License

MIT — see [LICENSE](LICENSE).
