# Architecture

`filex` is a self-hosted file manager designed for the "single binary" deploy
model: one Go executable that serves both the HTTP API and the embedded admin
SPA, talking to pluggable storage / auth / DB drivers.

- [Component diagram](#component-diagram)
- [Request lifecycle](#request-lifecycle)
- [Driver model](#driver-model)
- [DB schema](#db-schema)
- [Sync worker](#sync-worker)
- [Embed flow](#embed-flow)
- [Plug & play external services](#plug--play-external-services)
- [Repository layout](#repository-layout)

---

## Component diagram

```
┌──────────────────────────────────────────────────────────────────────────┐
│                                  Browser                                  │
│                                                                           │
│  Admin UI (Vue 3 SPA, embedded)        Embedded <filex-explorer> WC      │
│  - login / config / users              - any host page                   │
│  - storages / sync runs / audit        - same backend, different shell   │
│                                                                           │
│         │ /api/...                              │ /api/...               │
└─────────┼───────────────────────────────────────┼────────────────────────┘
          │                                        │
          ▼                                        ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                              filex (Go binary)                            │
│                                                                           │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │            HTTP layer (chi router, middleware stack)                │ │
│  │   logging · auth (JWT/cookie) · rate-limit · CORS · tracing         │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                  │                                        │
│         ┌────────────────────────┼─────────────────────────┐             │
│         ▼                        ▼                         ▼             │
│  ┌─────────────┐         ┌─────────────┐          ┌─────────────────┐   │
│  │  File API   │         │  Admin API  │          │   Auth API      │   │
│  │ /api/files  │         │ /api/admin  │          │ /api/auth       │   │
│  └──────┬──────┘         └──────┬──────┘          └────────┬────────┘   │
│         │                       │                          │             │
│         ▼                       ▼                          ▼             │
│  ┌───────────────────┐  ┌──────────────────┐    ┌──────────────────┐    │
│  │  Storage drivers  │  │  Search (Bleve)  │    │   Auth drivers   │    │
│  │ local · s3 ·      │  │  full-text +     │    │ local · oidc ·   │    │
│  │ sftp · webdav     │  │  metadata        │    │ ldap · proxy_hdr │    │
│  └─────────┬─────────┘  └─────────┬────────┘    └─────────┬────────┘    │
│            │                       │                       │             │
│            ▼                       ▼                       ▼             │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                    DB drivers (sqlite / mysql / postgres)         │   │
│  │   files (cache) · users · sessions · shares · audit · sync_runs   │   │
│  │   storages · uploads · operations · external_services             │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                  │                                        │
│         ┌────────────────────────┼──────────────────────────┐            │
│         ▼                        ▼                          ▼            │
│  ┌──────────────┐         ┌─────────────────┐        ┌─────────────┐    │
│  │ Sync worker  │         │  Thumb pipeline │        │ Op runner   │    │
│  │ ETag diff +  │         │ image · video · │        │ copy/move/  │    │
│  │ tombstone    │         │  pdf · office   │        │ extract bg  │    │
│  └──────┬───────┘         └────────┬────────┘        └─────────────┘    │
│         │                          │                                      │
│         ▼                          ▼                                      │
│  Storage backends           ffmpeg / vips / gs / soffice                  │
│  (local FS, S3, SFTP,       (full image only; absent in slim)             │
│   WebDAV)                                                                 │
│                                                                           │
└──────────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼ on capability discovery
                       ┌────────────────────────┐
                       │  External services     │
                       │  (URL = enabled)       │
                       │  - OnlyOffice          │
                       │  - Drawio              │
                       │  - Mermaid (CSR-only)  │
                       └────────────────────────┘
```

---

## Request lifecycle

A typical "list directory" call:

```
1. GET /api/files/manager?path=/storage1/sub
   └─ middleware: log → cors → auth → rate-limit
2. Handler resolves user, asserts read perm on /storage1/sub
3. Looks up cached listing in DB:
       SELECT * FROM files
       WHERE storage_id = $1 AND parent_path = '/sub'
       ORDER BY name LIMIT 1000
   (1-5 ms even on 50k entries — indexed on (storage_id, parent_path))
4. For each row, mints a signed thumb_url (HMAC) if applicable
5. Returns JSON { entries, total, storage }
```

Read paths **never** hit the storage backend. Writes (mkdir, upload, move,
delete) write through to both the storage driver and the DB cache so they
stay consistent within the request.

If a backend file changed outside filex (e.g. someone uploaded via S3
console), the [sync worker](#sync-worker) reconciles within a configurable
interval (default 5 min).

---

## Driver model

Three driver families, each with a Go interface + a built-in registry. Add
a new driver = implement the interface + register at init.

### Storage driver

```go
type Storage interface {
    List(ctx context.Context, path string) ([]Entry, error)
    Stat(ctx context.Context, path string) (Entry, error)
    Open(ctx context.Context, path string, off, n int64) (io.ReadCloser, error)
    Put(ctx context.Context, path string, r io.Reader, size int64) (string, error) // returns etag
    Delete(ctx context.Context, path string) error
    Mkdir(ctx context.Context, path string) error
    Move(ctx context.Context, from, to string) error

    // Optional:
    PresignPut(ctx context.Context, path string, parts int) ([]string, error)
    Sync(ctx context.Context, since time.Time, cb SyncCallback) error
}
```

Built-ins: `local`, `s3`, `sftp`, `webdav`.

### Auth driver

```go
type Authenticator interface {
    Login(ctx context.Context, creds Credentials) (User, error)
}

type Provisioner interface {
    Provision(ctx context.Context, claims map[string]any) (User, error)
}
```

Built-ins: `local` (bcrypt), `oidc`, `ldap`, `proxy_header`. Multiple drivers
can be enabled simultaneously; the login screen renders one button per driver.

### DB driver

The DB layer is split:
- **Migrations** via [goose](https://github.com/pressly/goose) — same SQL,
  conditionally guarded for SQLite vs MySQL vs Postgres syntax differences.
- **Queries** generated by [sqlc](https://sqlc.dev/) so handlers don't write
  hand-rolled SQL.

Built-ins:
- `sqlite` — `modernc.org/sqlite` (pure Go, no CGO)
- `mysql`  — `go-sql-driver/mysql`
- `postgres` — `jackc/pgx/v5`

---

## DB schema

15 tables. Names use `singular_or_plural` to match Laravel conventions of the
sister projects.

| Table                        | Purpose |
|------------------------------|---------|
| `users`                      | account row; bcrypt hash for local; OIDC `sub`/`iss` |
| `sessions`                   | session cookies + Bearer token cache |
| `storages`                   | named storage instances + driver config (encrypted) |
| `files`                      | DB-cached file tree, indexed by `(storage_id, parent_path)` |
| `file_metadata`              | lightweight extended attrs (mime override, label, color tag) |
| `shares`                     | public links: token, PIN, expiry, max_downloads, owner |
| `share_downloads`            | individual download events (audit) |
| `uploads`                    | multipart upload state (parts, etags, expires) |
| `operations`                 | long-running ops (copy, extract, archive); progress, status |
| `sync_runs`                  | per-storage sync history with counts and errors |
| `audit_events`               | all auditable user actions |
| `external_services`          | OnlyOffice/Drawio config + last_check |
| `thumbs`                     | thumbnail cache index (bytes live on disk) |
| `tombstones`                 | grace-period removal of files seen-then-not-seen |
| `migration_lock`             | goose migration lock |

ER diagram (high-level):

```
users 1───1 sessions
users 1──*  shares  1──* share_downloads
users 1──*  audit_events
storages 1──* files (parent_path indexed)
storages 1──* sync_runs
files 1──1 thumbs
storages 1──* tombstones
users 1──* uploads
users 1──* operations
external_services (config row per service)
```

---

## Sync worker

Background goroutine that reconciles each storage with the DB cache.

```
loop:
  for each storage with sync_interval elapsed:
    run := create_sync_run(storage_id)
    seen := {}

    for entry in storage.Sync(since=last_run_started):
      seen.add(entry.path)
      upsert(files, storage_id, entry)

    # tombstone guard: only mark missing after N consecutive misses
    for f in db.files where storage_id=$id and path not in seen:
      tombstones.upsert(...)
      if tombstones.misses >= grace_count:
        files.delete(...)

    finish_sync_run(run)
```

The tombstone grace prevents transient API failures (S3 503 mid-list) from
deleting cache rows. Default grace = 10 minutes / 2 misses.

`storage.Sync` is etag-diff-based when the driver supports it (S3 list
versions, WebDAV PROPFIND). Otherwise it's a full re-list.

---

## Embed flow

The Go binary embeds two front-end bundles via `//go:embed`:

```
backend/embed/
├── admin/        # web/dist (Vue 3 SPA)
│   ├── index.html
│   ├── assets/...
└── web/          # packages/webcomponent/dist
    ├── filex.js
    └── filex.css
```

In Go:

```go
//go:embed embed/admin/*
var adminFS embed.FS

//go:embed embed/web/*
var webFS embed.FS
```

These are mounted at:

| Route            | Source                       | Notes |
|------------------|------------------------------|-------|
| `/admin/*`       | `embed/admin/`               | SPA — fallthrough to `index.html` for client routing |
| `/embed.js`      | `embed/web/filex.js`         | Web Component bundle |
| `/embed.css`     | `embed/web/filex.css`        | Optional (the WC ships CSS-in-JS too) |

Build flow:

```
1. pnpm -r --filter='./packages/*' build      # core, webcomponent, react
2. pnpm --filter='./web' build                # admin SPA
3. node scripts/sync-embed.mjs                # copy dist into backend/embed
4. cd backend && go build ./cmd/filex         # //go:embed picks up the dirs
```

The same flow drives Docker (multi-stage), goreleaser (CI artifacts), and
local development (`pnpm run build:all`).

---

## Plug & play external services

OnlyOffice, Drawio, and Mermaid are mounted only when they're configured.
The capability is exposed via `GET /api/capabilities` and the front-end
hides UI affordances when a capability is `false`.

```
admin sets ONLYOFFICE_URL ──┐
                            ▼
                     external_services
                            │
                       on next /api/capabilities
                            ▼
                   { external: { onlyoffice_url: "..." } }
                            │
                            ▼
                     UI shows "Open in OnlyOffice" action
```

Adding a new external service is mechanical:

1. Add row in `config.yaml` schema + `external_services` table seed.
2. Implement health probe (`internal/external/<svc>/probe.go`).
3. Surface it in `/api/capabilities`.
4. Front-end conditional UI block.

---

## Repository layout

```
filemanager/
├── backend/                        # Go service
│   ├── cmd/filex/                  # main.go (cobra CLI)
│   ├── internal/
│   │   ├── api/                    # chi handlers
│   │   ├── auth/                   # auth drivers + middleware
│   │   ├── capability/             # /api/capabilities aggregator
│   │   ├── config/                 # YAML + env loader
│   │   ├── db/                     # sqlc-generated + migrations
│   │   ├── model/                  # domain types
│   │   ├── search/                 # Bleve wrapper
│   │   ├── server/                 # HTTP wiring
│   │   ├── share/                  # public-link handlers
│   │   ├── storage/                # storage drivers
│   │   ├── sync/                   # background sync worker
│   │   └── thumb/                  # thumbnail pipeline
│   ├── db/
│   │   ├── migrations/             # goose .sql files
│   │   └── queries/                # sqlc input
│   ├── embed/                      # populated by sync-embed.mjs
│   ├── go.mod
│   └── sqlc.yaml
│
├── packages/
│   ├── core/                       # @brftech/filex-core (Vue SFC)
│   ├── webcomponent/               # @brftech/filex (WC wrapper)
│   └── react/                      # @brftech/filex-react (lit/react)
│
├── web/                            # Vue 3 admin SPA (embedded)
├── demo/                           # standalone HTML demos
├── docker/
│   ├── Dockerfile                  # slim
│   └── Dockerfile.full             # +ffmpeg/gs/libreoffice
│
├── scripts/
│   └── sync-embed.mjs              # web/dist + wc/dist → backend/embed
│
├── docs/                           # this directory
│
├── .github/                        # issue/PR templates + CI workflows
│   └── workflows/ci.yml            # build/test pipeline
├── .goreleaser.yml                 # release matrix
├── docker-compose.yml              # full stack with profiles
├── package.json                    # workspace root
├── pnpm-workspace.yaml             # pnpm workspaces
└── README.md
```
