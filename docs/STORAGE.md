# Storage

filex doesn't store files itself — it **mounts** one or more storage backends
and presents them as a unified tree. Each mounted storage shows up as a
top‑level folder you name. You can mix several at once (e.g. a local disk, an S3
bucket, and an SFTP server side by side).

Supported adapters: **local** filesystem · **S3** / S3‑compatible · **SFTP** ·
**WebDAV** · **FTP/FTPS**.

- [How storages work](#how-storages-work)
- [Adding a storage](#adding-a-storage)
- [The storage config](#the-storage-config)
- [Adapters](#adapters) — [local](#local) · [S3](#s3--s3-compatible) · [SFTP](#sftp) · [WebDAV](#webdav) · [FTP](#ftp--ftps)
- [Sync — staying in step with the backend](#sync)
- [Read‑only mounts](#read-only-mounts)
- [Path validation & errors](#path-validation--errors)

---

## How storages work

A storage is a **row in filex's database**, not an environment variable. It
records the adapter (`driver`), a per‑adapter **config** blob (bucket, host,
credentials, …), a mount name, and options like read‑only and sync cadence.

filex keeps a **DB cache of the file tree** so listings are fast (a few ms)
instead of hitting the backend every time. A background **sync worker** keeps
that cache in step with the real backend (see [Sync](#sync)).

> **Root‑path guard.** A storage must point at a **sub‑folder / prefix**, never
> the bucket or filesystem root. This stops filex from ever shadowing pre‑existing
> objects at the root. See [Path validation](#path-validation--errors).

---

## Adding a storage

Three equivalent ways. **The admin UI is recommended** — it validates the path
and offers a "Test connection" before saving.

### Admin UI
Sign in as an admin → **Storages → Add**. Pick a driver, fill in the config
fields, click **Test connection**, then **Save**. The first sync starts
automatically.

### Admin API
`POST /api/admin/storages` (admin session/token). Body is the storage config;
`config` holds the per‑adapter map:

```bash
curl -X POST https://files.example.com/api/admin/storages \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{
    "name": "team-bucket",
    "driver": "s3",
    "mount_path": "/",
    "config": { "bucket": "my-bucket", "prefix": "filex", "region": "auto",
                "endpoint": "https://s3.example.com", "path_style": true,
                "access_key": "…", "secret_key": "…" },
    "read_only": false,
    "enabled": true
  }'
```

Test credentials **without saving** first:
`POST /api/admin/storages/test` with the same body → `{ok, sample_listing, object_count}`
or `{ok:false, error:"…"}` (the driver's error, verbatim).

### CLI
Good for automation / first boot:

```bash
filex storage add \
  --name team-bucket --driver s3 --mount / \
  --config '{"bucket":"my-bucket","prefix":"filex","region":"auto",
             "endpoint":"https://s3.example.com","path_style":true,
             "access_key":"…","secret_key":"…"}'

filex storage list
filex storage remove --name team-bucket
```

> ⚠ **`filex storage add` does not validate the root‑path guard** (the API/UI
> do). Always give a non‑empty `prefix`/`root`/`path` — never `/`.

### Connect a storage at install time (env / Compose / Helm)

You don't have to open the admin UI at all. A fresh install can come up with a
storage **already mounted**, seeded from environment on **first boot only, when
no storage exists yet**. The seed becomes a normal storage row you can edit
afterwards; changing the env later never re‑seeds. Leaving the driver empty
seeds nothing.

**The variables** (see [CONFIGURATION.md](CONFIGURATION.md#zero-touch-seeding)):

| Variable | For | Example |
|---|---|---|
| `FILEX_DEFAULT_STORAGE_DRIVER` | all | `local` · `s3` · `sftp` · `webdav` · `ftp` |
| `FILEX_DEFAULT_STORAGE_NAME` | all | `Files` (top‑level folder label) |
| `FILEX_DEFAULT_STORAGE_PATH` | local | `/srv/files` |
| `FILEX_DEFAULT_STORAGE_S3_*` (`BUCKET`/`PREFIX`/`ENDPOINT`/`REGION`/`ACCESS_KEY`/`SECRET_KEY`/`PATH_STYLE`) | s3 | see below |
| `FILEX_DEFAULT_STORAGE_CONFIG` | **any driver** | one line of the driver's [config JSON](#adapters) |

Use the dedicated vars for **local** and **S3**. To connect **any other existing
external storage** (sftp / webdav / ftp), set the driver name and put its config
JSON in `FILEX_DEFAULT_STORAGE_CONFIG`.

**Plain binary / systemd / `docker run` — set env directly:**

```bash
# an existing S3 bucket (AWS / Hetzner / R2 / Backblaze)
FILEX_DEFAULT_STORAGE_DRIVER=s3
FILEX_DEFAULT_STORAGE_S3_BUCKET=my-bucket
FILEX_DEFAULT_STORAGE_S3_PREFIX=filex
FILEX_DEFAULT_STORAGE_S3_REGION=eu-central-1
FILEX_DEFAULT_STORAGE_S3_ACCESS_KEY=AKIA...
FILEX_DEFAULT_STORAGE_S3_SECRET_KEY=...

# an existing SFTP / NAS server (any driver → one JSON line)
FILEX_DEFAULT_STORAGE_DRIVER=sftp
FILEX_DEFAULT_STORAGE_CONFIG={"host":"nas.example.com","port":22,"user":"filex","password":"s3cret","root":"/srv/files"}
```

**Docker Compose** — put the same vars in `.env`. The shipped
[`deploy/compose/.env.example`](../deploy/compose/.env.example) has ready
copy‑paste blocks for MinIO, an external S3 bucket, SFTP and WebDAV.

**Helm** — set them under `storage:` in your values
([`deploy/helm/filex/values.yaml`](../deploy/helm/filex/values.yaml)):

```yaml
# an existing S3 bucket
storage:
  type: s3
  s3:
    bucket: my-bucket
    prefix: filex
    region: eu-central-1
    endpoint: "https://s3.eu-central-1.amazonaws.com"
    accessKey: "AKIA..."
    secretKey: "..."
```
```yaml
# an existing SFTP / NAS — any driver via `config`
storage:
  type: sftp
  name: NAS
  config:
    host: nas.example.com
    port: 22
    user: filex
    password: "s3cret"
    root: /srv/files
```

---

## The storage config

| Field | Type | Default | Meaning |
|---|---|---|---|
| `name` | string | — | Display name + top‑level folder label. Required. |
| `driver` | string | — | `local` · `s3` · `sftp` · `webdav` · `ftp`. Required. |
| `config` | object | `{}` | Per‑adapter settings (see [Adapters](#adapters)). |
| `mount_path` | string | `/` | Logical mount point inside filex. |
| `sync_mode` | string | `poll` | `poll` · `fsnotify` (local only) · `ondemand`. |
| `sync_interval_s` | int (seconds) | `900` | Poll cadence. **Values < 5 s are clamped to 15 min.** |
| `enabled` | bool | `true` | Disabled storages are hidden and not synced. |
| `read_only` | bool | `false` | Block all writes to this mount. |
| `rbac_enabled` | bool | `false` | When true, per‑user [RBAC](RBAC.md) grants gate access; when false the storage is visible to all authenticated users. |

---

## Adapters

Each adapter's `config` object is passed verbatim to the driver. Only the keys
below are read; unknown keys are ignored.

### local

Serves a directory on the host running filex.

| key | required | default | notes |
|---|---|---|---|
| `path` | yes* | — | Absolute path to serve. Created (`0755`) if missing. |
| `root` | yes* | — | Legacy alias for `path`. |

\*One of `path` / `root`. Example: `{"path": "/data/files"}`.
Capabilities: read, write, move, copy, delete, mkdir, **live change events**
(fsnotify). Path traversal (`..`) is rejected.

### S3 / S3‑compatible

Works with **AWS S3, MinIO, Cloudflare R2, Backblaze B2 (S3), Hetzner Object
Storage / Ceph RGW**, and other S3‑compatible stores.

| key | required | default | notes |
|---|---|---|---|
| `bucket` | **yes** | — | Bucket name. |
| `prefix` | recommended | `""` | Key prefix = the storage root. **Must be non‑empty** (root guard). |
| `region` | no | `auto` | e.g. `us-east-1`. `auto` suits R2/MinIO. |
| `endpoint` | no | — | Custom endpoint for non‑AWS (e.g. `https://minio.example.com`). Omit for AWS. |
| `path_style` | no | auto | Path‑style addressing. **Auto‑enabled when `endpoint` is set** (MinIO/Hetzner/B2/R2 need it); AWS stays virtual‑host. |
| `access_key` | no | — | Static key. If omitted, the AWS default credential chain is used (env/IRSA/instance role). |
| `secret_key` | no | — | Static secret (with `access_key`). |
| `disable_presign` | no | `false` | Force filex to stream downloads itself instead of issuing presigned URLs. |

**Examples**

```jsonc
// AWS
{ "bucket": "my-bucket", "prefix": "filex", "region": "eu-central-1",
  "access_key": "AKIA…", "secret_key": "…" }

// MinIO / self-hosted
{ "bucket": "my-bucket", "prefix": "filex", "region": "auto",
  "endpoint": "https://minio.example.com", "path_style": true,
  "access_key": "…", "secret_key": "…" }

// Cloudflare R2
{ "bucket": "my-bucket", "prefix": "filex", "region": "auto",
  "endpoint": "https://<account>.r2.cloudflarestorage.com",
  "access_key": "…", "secret_key": "…" }
```

**Gotchas & failure modes**
- **Hetzner Object Storage / Ceph RGW** reject some AWS‑SDK presigned URLs with
  `SignatureDoesNotMatch`. If downloads fail there, set
  `"disable_presign": true` — filex then streams the bytes itself.
- Empty folders are represented by a hidden `.empty` marker object (created on
  mkdir, hidden from listings). Folder move/delete/copy recurse the prefix, so
  deleting or renaming a folder works even though S3 has no real directories.
- Filenames with spaces or non‑ASCII characters are fully supported (the copy
  source is URL‑encoded).
- `bucket` missing → the storage won't initialize (`Test connection` shows the
  error). Wrong keys/endpoint → `Test connection` fails with the SDK error.

### SFTP

| key | required | default | notes |
|---|---|---|---|
| `host` | **yes** | — | Server hostname/IP. |
| `user` | **yes** | — | SSH username. |
| `password` | one‑of | — | Password auth. |
| `private_key` | one‑of | — | PEM private key (string). Use instead of / with password. |
| `port` | no | `22` | Integer. |
| `root` | no | `/` | Base directory (use a sub‑folder). |
| `known_hosts` | no | `~/.filex/known_hosts` | Strict OpenSSH known_hosts path. |
| `host_key` | no | — | Pin a single host key. |
| `insecure_skip_host_key` | no | `false` | Disable host‑key checking (not recommended). |

Provide **either** `password` **or** `private_key`. **Host‑key handling:**
if you don't pin a key or supply a known_hosts file, filex uses
**trust‑on‑first‑use** — it records the server key on first connect and refuses
if it later changes (a MITM signal). Example:
`{"host":"sftp.example.com","user":"filex","private_key":"-----BEGIN OPENSSH PRIVATE KEY-----\n…","root":"/srv/files"}`.

### WebDAV

Tested against **Nextcloud, ownCloud, Apache mod_dav, nginx‑dav, SabreDAV**.

| key | required | default | notes |
|---|---|---|---|
| `url` | **yes** | — | Full WebDAV base URL (scope the path here — there is no separate `root`). |
| `user` | **yes** | — | Basic‑auth user. |
| `password` | no | — | Basic‑auth password. |

Only **Basic auth** is supported today (Bearer is planned). Example:
`{"url":"https://cloud.example.com/remote.php/dav/files/alice/filex","user":"alice","password":"…"}`.
`MKCOL`/`MOVE`/`COPY`/`DELETE`/`PROPFIND` back the file operations.

### FTP / FTPS

| key | required | default | notes |
|---|---|---|---|
| `host` | **yes** | — | Server hostname/IP. |
| `user` | **yes** | — | Username. |
| `password` | **yes** | — | Password (required, unlike SFTP). |
| `port` | no | `21` | Integer. |
| `root` | no | `/` | Base directory (use a sub‑folder). |
| `tls` | no | `false` | Explicit FTPS (AUTH TLS). |
| `passive` | no | `true` | PASV mode; set `false` to disable. |

FTP uses a **single serialized control connection**, so it's the slowest
adapter and copies stream through a temporary file. Prefer SFTP where possible.
`{"host":"ftp.example.com","user":"filex","password":"…","root":"/files","tls":true}`.

---

## Sync

filex serves listings from its DB cache, so it periodically reconciles that
cache with the real backend to catch changes made **outside** filex (e.g. a file
uploaded straight to the S3 console).

**Modes** (`sync_mode`):
- **`poll`** (default) — a full recursive walk every `sync_interval_s` seconds.
  Intervals below 5 s are clamped to 15 minutes.
- **`fsnotify`** — real‑time OS watch, **local driver only** (falls back to poll
  otherwise). 2‑second debounce coalesces bursts like `tar -xf`.
- **`ondemand`** — only syncs when explicitly triggered
  (`POST /api/admin/storages/{id}/sync`).

**What a sync does:** new objects are indexed, changed objects (by **ETag diff**)
are updated, and objects gone from the backend are soft‑deleted from the cache.
A **tombstone guard** protects against transient backend glitches: if a run sees
fewer than ~70 % of the objects the previous run saw, the delete pass is skipped
(so a flaky S3 endpoint doesn't wipe your tree from the cache).

**Global tuning (env):** `FILEX_SYNC_INTERVAL` (default `15m`) is the fallback
cadence, and `FILEX_SYNC_WORKERS` (default `4`) sizes the pool. Per‑storage
`sync_interval_s` takes precedence.

You can watch runs at `GET /api/admin/storages/{id}/sync-runs` and detect drift
with `GET /api/admin/storages/{id}/drift`.

---

## Read‑only mounts

Set `read_only: true` to expose a storage for browsing/download but block every
write (upload, rename, move, delete, share‑drop). Writes return **403
`storage is read-only`**. Useful for archives or a replica you don't want edited.

---

## Path validation & errors

**Root‑path guard.** The API/UI reject a storage whose prefix/root is empty or
`/` with:

```
ROOT_PATH_FORBIDDEN: storage prefix/path cannot be empty or root '/';
use a sub-folder like 'fileman' or 'data/files'
```

Always mount a sub‑folder (S3 `prefix`, or `root`/`path` for the others).

**Driver errors → HTTP:** `not found → 404`, `read-only → 403`,
`unsupported → 501`, `already exists → 409`, anything else `→ 500`. The
**Test connection** endpoint surfaces the raw driver error so you can debug
credentials/endpoints before saving.

---

## See also

- [CONFIGURATION.md](CONFIGURATION.md) — global config/env reference
- [RBAC.md](RBAC.md) — per‑storage and per‑file access control
- [INSTALLATION.md](INSTALLATION.md) · [DOCKER.md](DOCKER.md)
