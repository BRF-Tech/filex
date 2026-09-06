# Architecture

`filex` is a self-hosted file manager designed for the "single binary" deploy
model: one Go executable that serves both the HTTP API and the embedded admin
SPA, talking to pluggable storage / auth / DB drivers.

- [Component diagram](#component-diagram)
- [Request lifecycle](#request-lifecycle)
- [Driver model](#driver-model)
- [DB schema](#db-schema)
- [Sync worker](#sync-worker)
- [Realtime hub](#realtime-hub)
- [Operation queue](#operation-queue)
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
│  │ local · s3 · sftp │  │  full-text +     │    │ local · oidc ·   │    │
│  │ webdav · ftp· smb │  │  metadata        │    │ ldap · proxy_hdr │    │
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
│  │ etag / size+ │         │ image · video · │        │ copy/move/  │    │
│  │ mtime diff + │         │  pdf · office   │        │ extract bg  │    │
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
interval (default **15 min**, per storage).

A write that filex performed itself is **not** left to that: every write
surface calls the shared post-write gate, which indexes the file, emits the
webhook event, queues the antivirus scan and announces the change on the
[realtime hub](#realtime-hub). The sync exists to discover what filex did not
do.

---

## Driver model

Three driver families, each with a Go interface + a built-in registry. Add
a new driver = implement the interface + register at init.

> A **storage** driver no longer has to be compiled in: a
> [plugin](PLUGINS.md) is a separate program filex speaks HTTP/JSON to, and it
> is registered in the same registry as `plugin:<name>` — with the capabilities
> it declares probed before anything is registered at all.

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

Built-ins: `local`, `s3`, `sftp`, `webdav`, `ftp`, `smb` — plus any installed
[plugin](PLUGINS.md), registered as `plugin:<name>`.

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
can be enabled simultaneously. A driver that redirects (`oidc`) gets its own
button; the two that read a **password** (`local`, `ldap`) share the one form and
are chained — `auth.LoginChain` tries them in the configured order and the first
to accept wins, so `local` first keeps `admin@local` answerable while the
directory is down. The same chain feeds the file protocols through
`protocolauth.Directory`.

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

The tables an operator or an integrator is most likely to need. ⚠ This is a
map, not the schema: the real one has roughly twice as many tables and 33
migrations, and some names here are the older ones (`files` is `nodes`,
`operations` is `ops_queue`, `thumbs` is `thumbnails`). Read
`backend/db/migrations/` for the truth. Names use `singular_or_plural` to match
Laravel conventions of the sister projects.

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
| `thumbs`                     | thumbnail cache index (bytes live on disk; released when the node is purged, and a reconciler sweeps orphans at boot and every `FILEX_THUMBS_SWEEP_INTERVAL`) |
| `migration_lock`             | goose migration lock |

ER diagram (high-level):

```
users 1───1 sessions
users 1──*  shares  1──* share_downloads
users 1──*  audit_events
storages 1──* files (parent_path indexed)
storages 1──* sync_runs
files 1──1 thumbs
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
      if entry.path is inside .filex-trash/:  # filex's own bookkeeping
        skip                                  # -- not catalogue content
      seen.add(entry.path)
      upsert(files, storage_id, entry)

    # anything LIVE inside .filex-trash/ is a defect, and both kinds are fixed
    for f in db.files where storage_id=$id and path under .filex-trash/ and not deleted:
      if f.storage_key points OUTSIDE the trash:  # a deletion something revived
        soft_delete(f)                            # -> back in the trash
      else:                                       # a row minted for trash bytes
        hard_delete(f)                            # -> dropped, bytes untouched

    # tombstone pass — a node not seen this run is a CANDIDATE, not a verdict
    if seen < 0.7 * previous_run.seen:      # the whole listing looks wrong
      skip the pass entirely
    for f in db.files where storage_id=$id and seen_at < run_started:
      if f.transfer_state != "stored":      # filex never put the bytes there
        keep
      elif storage.Stat(f.path) is found:   # the listing missed it
        keep
      elif Stat failed for any other reason: # we could not check
        keep
      else:
        soft_delete(f)                       # genuinely gone → trash

    finish_sync_run(run)
```

⚠ Absence from a listing is not proof of deletion, and answering it with
"move to trash" turns any unrelated bug into lost data — which is exactly
what happened in GitHub #16, where uploads that never reached S3 were trashed
by the next run. So the pass has to be *right* about the deletion, and three
things stand between a missing object and the trash:

1. **A whole-listing guard.** If the run saw more than 30 % fewer entries than
   the previous one, no deletions happen at all — that shape is a backend
   glitch, not a mass deletion.
2. **Did filex ever store it?** A node whose `transfer_state` is not `stored`
   is in staging or is a failed upload. Its absence from the backend is the
   *expected* state and is never a deletion. This also closes the race where a
   run landing between publishing a node and finishing its transfer would
   trash a healthy upload.
3. **A direct second opinion.** For the remaining candidates the driver is
   asked with `Stat`. Only a definite not-found counts; an object the listing
   missed but `Stat` can see is kept, and so is one that could not be checked
   at all (permissions, timeout, 503) — "I could not check" must never read as
   "it is gone".

A file genuinely deleted in the bucket still goes to trash, so deletions made
outside filex are still reflected. `Stat` runs only for candidates that
survive step 1, so a healthy sync costs nothing extra.

### The walk and the trash

⚠⚠ **The walk does not enter `.filex-trash/`, and it never un-deletes a row.**

Deleting a file in filex is a *rename*: the bytes move to
`.filex-trash/<unix>-<rand>__<name>` and the node row is soft-deleted and
retagged to that key ([TRASH-VERSIONING.md](TRASH-VERSIONING.md)). Quarantine
is the same operation — the antivirus job produces an identical row
([PROTECTION.md](PROTECTION.md)) — so nothing in the catalogue tells the two
apart.

The walk used to descend into the trash bucket, find an object with no *live*
row, find the soft-deleted one, and clear `deleted_at` on it. A file the user
deleted therefore came back at the next pass, and an infected file left
quarantine at the next pass: the security control expired on a timer nobody
set. It fired on every pass, on every driver, with no condition attached —
poll, fsnotify and driver-watch all funnel into the same full `RunOnce` walk,
so there is no incremental mode in which it did not happen.

Two rules now stand in its place:

1. **`.filex-trash/` is skipped.** The rows for everything in there already
   exist, retagged to the very keys sitting on the storage, and they are
   maintained by the trash service — restore, retention purge — never by a
   listing. (A consequence: `seen` no longer counts trashed objects, so a
   storage whose trash held more than 30 % of its objects trips the
   whole-listing guard once on the first pass after upgrading.)
2. **A trashed row is never revived.** When an object turns up at a path where
   a trashed row still sits — an out-of-band restore, or simply a new file with
   an old name — the trashed row stays in the trash and the object is
   catalogued as a **new node**, which is indexed and treated as new
   everywhere. Bytes that reappear at a path are not the file that was deleted
   there; reviving the row would hand them another file's identity, version
   history, comments and shares, and nothing downstream would ever look at
   them again. Migration `00032` makes the `(storage_id, path_hash)` unique
   index live-only so the new row is possible — the same thing `00018` did for
   `(storage_id, parent_id, name)`.

   > "Treated as new" means catalogued, indexed **and scanned**. The walk
   > hands every file it newly catalogues — and every file whose content
   > drifted — to the antivirus queue, so a file that appears on a storage out
   > of band is scanned whether it lands on a fresh path or on one a trashed
   > row once held. See
   > [PROTECTION.md](PROTECTION.md#files-the-sync-discovers).

Anything found **live** inside `.filex-trash/` is repaired on the spot, which
is what heals an install that ran an earlier version: a revived deletion is
soft-deleted again (keeping `storage_key`, so restore still knows where it came
from), and a row the old walk minted for the trash's own bytes is dropped
outright. Bytes are never touched by either.

`storage.Sync` compares the backend's etag when the driver reports one (S3,
WebDAV PROPFIND) and the object's **size and modification time** when it does
not — which is local, SFTP, SMB and FTP. Either way the walk is a full re-list;
the fingerprint decides which rows it updates. What each of those catches, and
the two kinds of external change that slip past the second one, is in
[STORAGE.md → Drift detection](STORAGE.md#drift-detection-what-a-replaced-file-looks-like).

---

## Realtime hub

An open explorer is told what changed rather than asking. `internal/realtime`
holds one hub per process; a browser mints a short-lived ticket at
`POST /api/files/ws-ticket`, opens `GET /api/ws`, and subscribes to the folders
it has on screen. It gets two kinds of frame: **presence** (who else is here,
what they have focused) and **change** (something in this folder moved).

Two properties matter to everything else in this document:

- **An explorer with a healthy socket does not poll.** The 12 s re-listing in
  `useRealtime.ts` is the fallback for a socket that failed. So a write that
  emits no frame is not "slow to appear" — it does not appear at all until the
  user navigates.
- **Bursts are coalesced on the leading edge.** The first change in a quiet
  room goes out immediately and unmodified; everything after it is merged into
  one frame per window (200 ms, doubling to 1.5 s while the burst continues,
  reset when the folder goes quiet). A merged frame carries `count`. Nothing is
  dropped — a burst always ends with a frame reflecting its final state.

Every write surface reaches the hub through the same post-write gate, and the
five protocol servers reach it through `internal/protocolsync`, the one package
they already shared. The contract, the frame shapes and the debounce advice for
integrators are in [REALTIME.md](REALTIME.md).

---

## Operation queue

`ops_queue` is the durable work list behind antivirus scans, content
extraction, thumbnails, and async copy/move/delete. Three drivers implement one
interface — **sqlite** (in the app DB), **postgres** (`SELECT … FOR UPDATE SKIP
LOCKED`) and **redis** — and a shared contract test runs the same suite against
all three.

Three columns carry more meaning than their names suggest:

- `priority` — claimed `ORDER BY priority DESC, enqueued_at ASC` on all three
  drivers, which is what keeps an interactive scan ahead of a twenty-thousand
  file first import. ⚠ The redis driver ignored it entirely until v0.34.0; its
  pending set is now a **sorted set** whose score encodes priority and arrival,
  claimed by a single Lua script, converted from the old LIST at startup.
  Downgrading past that conversion is not supported.
- `not_before` — a delayed operation, used by the editor's debounced save-scan.
  Deliberately a row rather than a timer in the process, so a deploy does not
  take every pending scan with it.
- `dedup_key` — unique among **pending** rows only, so "one pending scan per
  file" holds while a save burst is arriving and is released the moment a
  worker claims the scan.

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
| `/drive/*`       | `embed/admin/`               | The same SPA, end-user front door. vue-router reads its history base from the prefix that served the document, so a session that starts here stays on `/drive/…` |
| `/files/edit`    | `embed/admin/`               | The standalone editor, outside both prefixes (`openPageBase`) |
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

OnlyOffice, Drawio and the converter are used only when they're configured;
Mermaid renders client-side and needs no service at all. The capability is
exposed via `GET /api/capabilities` and the front-end hides UI affordances when
a capability is `false`.

⚠ **The `external_services` row is the single runtime source of truth**, read
on every use behind a one-second cache that the admin `PATCH` invalidates. It
used to be a snapshot of env/YAML taken once at boot, which is how an operator
could configure OnlyOffice in the admin UI, watch **Test** answer 200, and
still get `503 onlyoffice not configured` from the editor forever (GitHub #17).
Env and `config.yaml` are declarative configuration *for* that row: a service
they name is re-asserted onto it at every boot and reported `env_managed: true`,
so an admin-UI edit to one of those applies immediately and is reverted at the
next start.

```
admin PATCH /api/admin/external/onlyoffice ──┐
                                             ▼
                                      external_services
                                             │
                              read on every use (1 s cache,
                              invalidated by the PATCH)
                                             ▼
                            editor descriptor, capabilities,
                            the converter URL handed to AI/MCP
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
│   ├── Dockerfile                  # the :latest / :full image, ~510 MB
│   ├── Dockerfile.slim             # the :slim image, ~43 MB, binary only
│   └── Dockerfile.local            # local hot-fix builds from a host dist
│
├── scripts/
│   └── sync-embed.mjs              # web/dist + wc/dist → backend/embed
│
├── docs/                           # this directory
│
├── .gitlab/                        # CI helpers (placeholders)
├── .gitlab-ci.yml                  # pipeline
├── .goreleaser.yml                 # release matrix
├── docker-compose.yml              # full stack with profiles
├── package.json                    # workspace root
├── pnpm-workspace.yaml             # pnpm workspaces
└── README.md
```
