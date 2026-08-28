# Storage

filex doesn't store files itself — it **mounts** one or more storage backends
and presents them as a unified tree. Each mounted storage shows up as a
top‑level folder you name. You can mix several at once (e.g. a local disk, an S3
bucket, and an SFTP server side by side).

Supported adapters: **local** filesystem · **S3** / S3‑compatible · **SFTP** ·
**WebDAV** · **FTP/FTPS** · **SMB/CIFS**.

That list is not the limit: a backend filex does not ship can be added as a
**[storage plugin](PLUGINS.md)** — a separate program that describes its own
config form and appears here as `plugin:<driver>`, behaving like any adapter
below. A plugin only gets that far by **proving what it claims**: filex probes
every capability it declares before registering it, and probes it again against
the configuration you type when you save a storage on it, so a driver that half
works is refused rather than offered.

A **NAS** (Synology, QNAP, TrueNAS, a Windows share…) is supported two ways:
the **`smb` driver** talks to the share directly, and for NFS you mount it with
the operating system and serve the mount point with the `local` adapter. Most
boxes also speak SFTP/FTP/WebDAV natively. See
[NAS](#nas-nfs-smb-and-friends).

> Reaching filex *from* somewhere else — as an S3 endpoint, an SFTP server, an
> FTPS server, an NFS export or a mounted drive — is the other direction, and
> lives in [PROTOCOLS.md](PROTOCOLS.md).

- [How storages work](#how-storages-work)
- [Adding a storage](#adding-a-storage)
- [The storage config](#the-storage-config)
- [Adapters](#adapters) — [local](#local) · [NAS / SMB / NFS](#nas-nfs-smb-and-friends) · [S3](#s3--s3-compatible) · [SFTP](#sftp) · [WebDAV](#webdav) · [FTP](#ftp--ftps)
- [Sync — staying in step with the backend](#sync)
- [Slow storage](#slow-storage)
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

`filex storage add` runs the same gates as the admin API before it writes:
unknown `--driver` is refused (with the registered names), `--config` must
parse as a JSON object, and the root‑path guard applies. Required fields the
driver declares but the config omits are reported as a warning, not an error —
an S3 storage on an instance role legitimately ships no keys.

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
| `sync_mode` | string | `poll` | `poll` · `fsnotify` (the local driver, **or a [plugin](PLUGINS.md) that streams its own changes**) · `ondemand`. |
| `sync_interval_s` | int (seconds) | `900` | Poll cadence. **Values < 5 s are clamped to 15 min.** |
| `enabled` | bool | `true` | Disabled storages are hidden and not synced. |
| `read_only` | bool | `false` | Block all writes to this mount. |
| `rbac_enabled` | bool | `false` | When true, per‑user [RBAC](RBAC.md) grants gate access; when false the storage is visible to all authenticated users. |

---

### Driver descriptors (`GET /api/admin/storage-drivers`)

Every driver declares its own config contract — the keys it reads, their type,
which one is the storage root, which hold credentials, defaults, placeholders
and an i18n key per label. The admin UI's storage form, the storage editor and
the replication‑target dialog all render from this endpoint, and the root‑path
guard reads the same declaration, so a driver's fields cannot drift away from
what the backend accepts.

```bash
curl -s https://files.example.com/api/admin/storage-drivers \
  -H "Authorization: Bearer $TOKEN" | jq '.[] | {driver, fields: [.fields[].key]}'
```

```json
{
  "driver": "s3",
  "label": "S3 / Hetzner / MinIO",
  "i18n_key": "storages.driver.s3",
  "capabilities": { "read": true, "write": true, "presign": true, "…": true },
  "fields": [
    { "key": "bucket", "type": "string", "required": true, "label": "Bucket",
      "i18n_key": "storages.fields.bucket", "placeholder": "my-bucket" },
    { "key": "prefix", "type": "string", "required": true, "root": true,
      "label": "Prefix", "i18n_key": "storages.fields.prefix" },
    { "key": "secret_key", "type": "password", "secret": true, "…": "…" }
  ]
}
```

`root: true` marks the field the [root‑path guard](#path-validation--errors)
checks. `aliases` lists older spellings of a key that the driver still reads,
so configs written before a rename keep working. Adding a driver on the backend
puts it in every picker without a frontend release.

`capabilities` on `/api/capabilities` still carries the plain
`storage_drivers: ["ftp","local","s3","sftp","webdav"]` name list for older
callers.

---

## Adapters

Each adapter's `config` object is passed verbatim to the driver. Only the keys
below are read; unknown keys are ignored. The same key lists are served
machine‑readably by
[`GET /api/admin/storage-drivers`](#driver-descriptors-get-apiadminstorage-drivers).

> A **plugin** driver appears in that same endpoint with the fields the plugin
> described, which is why the admin form renders it without a frontend release.
> See [PLUGINS.md](PLUGINS.md).

### local

> ⚠⚠ **Refused on a public demo.** `FILEX_DEMO_MODE` publishes an admin login,
> and this driver means "a path on this host" — measured on filex's own demo,
> storages rooted at `/data`, `/etc` and `/proc/1` were all accepted before the
> guard existed. Since 0.21.6 the **remote** drivers are refused on a demo as
> well: adding one asks the server to connect to an address the visitor chose.
> A demo ships with the storage it demonstrates.


Serves a directory on the host running filex.

| key | required | default | notes |
|---|---|---|---|
| `path` | yes* | — | Absolute path to serve. Created (`0755`) if missing. |
| `root` | yes* | — | Legacy alias for `path`. |

\*One of `path` / `root`. Example: `{"path": "/data/files"}`.
Capabilities: read, write, move, copy, delete, mkdir, **live change events**
(fsnotify). Path traversal (`..`) is rejected.

### NAS (NFS, SMB, and friends)

There are two ways, and since v0.20.0 the first one is usually better.

**1 — the `smb` driver (SMB / CIFS).** filex talks to the share itself: give it
the host, the share NAME alone (`media`, not `\\nas\media`), an account and
optionally a sub‑folder. No `/etc/fstab`, nothing to mount on the host, and the
whole configuration stays inside filex.

```jsonc
{ "name": "NAS", "driver": "smb", "mount_path": "/",
  "config": { "host": "nas.local", "share": "media",
              "user": "filex", "password": "…", "root": "projects" } }
```

⚠ There is **no `nfs` driver** — NFSv3 needs a privileged source port and, for
anything beyond trust-me-it's-uid-1000, Kerberos. Mount NFS with the OS and use
option 2. (The other direction *does* exist: filex can be **served as** NFSv3 —
see [PROTOCOLS.md](PROTOCOLS.md).)

**2 — mount it with the operating system** and serve the mount point with the
**[local](#local)** adapter. Still supported, and the only option for NFS. It
has three traps, and all three are below.

```bash
# NFS
sudo mount -t nfs nas.local:/volume1/files /mnt/nas

# SMB / CIFS — only if you prefer the OS mount to the smb driver above
sudo mount -t cifs //nas.local/files /mnt/nas \
  -o credentials=/etc/nas.cred,uid=1000,gid=1000
```

```jsonc
{ "name": "NAS", "driver": "local", "mount_path": "/",
  "config": { "path": "/mnt/nas/filex" } }
```

**You may not need the mount at all.** Most NAS boxes speak protocols filex
talks natively — **[SFTP](#sftp)**, **[FTP/FTPS](#ftp--ftps)**,
**[WebDAV](#webdav)**, or an S3 endpoint (e.g. MinIO running on the box) for the
**[S3](#s3--s3-compatible)** adapter. A native adapter keeps the whole
configuration inside filex instead of half of it in `/etc/fstab`, so prefer one
where the NAS offers it.

**Docker:** mount the share on the **host** and bind‑mount it into the container
(`-v /mnt/nas:/data/nas`), then point the storage at `/data/nas/…`. The official
image sets no `USER`, so it runs as root unless you override it — meaning the
permission that usually bites is on the **NAS side** (NFS `root_squash`, the SMB
share's ACL), not inside the container. If you *do* run the container as a
non‑root user, the `uid`/`gid` mount options have to match it: CIFS assigns
ownership at mount time, not from the file itself.

**Trap 1 — use `sync_mode: poll`, never `fsnotify`.** `fsnotify` is an OS‑local
watch (inotify / kqueue / ReadDirectoryChangesW). It sees what *this* machine
writes to the mount and **never sees what another machine writes to the NAS** —
so a file dropped on the share from a laptop would stay invisible until
something else triggered a sync. filex only leaves the OS watch when the driver
isn't `local` — and a mounted share *is* the `local` driver: nothing falls back,
nothing warns. Choose `poll` explicitly and set an interval that matches
how fresh you need the listing (`sync_interval_s`; `900` = 15 min is the default
when you don't set one, and 60 s is reasonable on a busy share).

**Trap 2 — mount before filex starts.** filex **creates a storage's root
directory if it is missing**, so a storage pointed at an *unmounted* path will
cheerfully serve an empty directory, and the next sync run reads "empty backend"
as "everything was deleted". The [tombstone guard](#sync) blocks the *first*
such run — it skips the delete pass when a run sees less than ~70 % of what the
previous run saw — but it only ever compares against the **previous run**. Once
that empty run is on record with a seen count of 0, the guard has nothing to
compare against and the next empty run soft‑deletes the tree from the cache. It
buys you one cycle, not safety. Nothing is deleted on the NAS itself, and a sync
against a properly mounted share restores the entries, but in between your users
see an empty folder. Put the share in `/etc/fstab` with `_netdev` (or use a
systemd `.mount` unit and order filex `After=` it).

**Trap 3 — a share is fast to browse and slow to transfer.** Listings, search
and thumbnails come from filex's own index and caches, so the explorer stays
quick over a slow mount; uploads and downloads move real bytes and run at the
speed of the network path. See [Slow storage](#slow-storage).

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
| `key_path` | one‑of | — | Path to a key file on the server, read at Init when `private_key` is empty. |
| `port` | no | `22` | Integer. |
| `root` | **yes** | `/` | Base directory. Must be a sub‑folder — the root guard rejects `/`. Aliases: `base_path`, `remote_path`. |
| `known_hosts` | no | `~/.filex/known_hosts` | Strict OpenSSH known_hosts path. |
| `host_key` | no | — | Pin a single host key. |
| `insecure_skip_host_key` | no | `false` | Disable host‑key checking (not recommended). |

`user` also accepts the legacy spelling `username`.
Provide **either** `password`, `private_key` **or** `key_path`. **Host‑key handling:**
if you don't pin a key or supply a known_hosts file, filex uses
**trust‑on‑first‑use** — it records the server key on first connect and refuses
if it later changes (a MITM signal). Example:
`{"host":"sftp.example.com","user":"filex","private_key":"-----BEGIN OPENSSH PRIVATE KEY-----\n…","root":"/srv/files"}`.

### WebDAV

Tested against **Nextcloud, ownCloud, Apache mod_dav, nginx‑dav, SabreDAV**.

| key | required | default | notes |
|---|---|---|---|
| `url` | **yes** | — | WebDAV base URL. |
| `user` | **yes** | — | Basic‑auth user. Alias: `username`. |
| `password` | no | — | Basic‑auth password. |
| `root` | **yes** | `""` | Sub‑folder under the base URL — the mount point. Aliases: `base_path`, `remote_path`. |

`root` is joined onto the base URL's path; a storage saved before the driver
read it (empty `root`) still mounts exactly at the URL, unchanged.
Only **Basic auth** is supported today (Bearer is planned). Example:
`{"url":"https://cloud.example.com/remote.php/dav/files/alice/","user":"alice","password":"…","root":"filex"}`.
`MKCOL`/`MOVE`/`COPY`/`DELETE`/`PROPFIND` back the file operations.

### FTP / FTPS

| key | required | default | notes |
|---|---|---|---|
| `host` | **yes** | — | Server hostname/IP. |
| `user` | **yes** | — | Username. Alias: `username`. |
| `password` | **yes** | — | Password (required, unlike SFTP). |
| `port` | no | `21` | Integer. |
| `root` | **yes** | `/` | Base directory. Must be a sub‑folder — the root guard rejects `/`. Aliases: `base_path`, `remote_path`. |
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
- **`fsnotify`** — event‑driven instead of timed. It resolves in this order:
  the **OS watch** (inotify / kqueue / ReadDirectoryChangesW) when the driver is
  `local`; otherwise the **driver's own change stream**, when it has one — today
  that means a [storage plugin](PLUGINS.md) declaring `watch`, since no built‑in
  remote driver implements it; otherwise it falls back to poll. Either way a
  2‑second debounce coalesces bursts like `tar -xf`, and every batch triggers the
  same full run a poll would, so an event stream affects *latency*, never
  correctness. ⚠ A driver stream that **ends** (a plugin restarts, a connection
  drops) drops the storage back to polling rather than leaving it frozen with a
  stale index.
- **`ondemand`** — only syncs when explicitly triggered
  (`POST /api/admin/storages/{id}/sync`).

**What a sync does:** new objects are indexed, changed objects (by **ETag diff**)
are updated, and objects gone from the backend are soft‑deleted from the cache.
A **tombstone guard** protects against transient backend glitches: if a run sees
fewer than ~70 % of the objects the previous run saw, the delete pass is skipped
(so a flaky S3 endpoint doesn't wipe your tree from the cache). ⚠ The comparison
is against the **previous run only**: a backend that stays empty records a run
with a seen count of 0, and the run after that has nothing to compare against
and deletes. The guard buys a cycle to notice the outage in — see
[NAS trap 2](#nas-nfs-smb-and-friends).

**Cadence is per storage.** The poll loop uses the storage row's
`sync_interval_s` (`900` when you don't set one; anything under 5 s is treated
as 15 minutes). Every enabled storage gets its own goroutine and walks its
backend sequentially — there is no shared worker pool, so set the interval on
the storage rather than looking for a global knob.

You can watch runs at `GET /api/admin/storages/{id}/sync-runs` and detect drift
with `GET /api/admin/storages/{id}/drift`.

---

## Slow storage

A NAS over a VPN, an SFTP box on the other side of the country, a bucket in
another region — filex is built so that the slow part stays the slow part.

**Already fast, nothing to configure:**
- **Listings** are served from the DB cache, not the backend. The driver is only
  consulted for a storage that has never synced (so a brand‑new mount isn't
  empty while the first walk runs).
- **Search** runs against filex's own index/database and never touches the
  backend at query time (see [SEARCH.md](SEARCH.md)).
- **Thumbnails** are generated once and cached on local disk, so the second
  visit to a photo folder costs nothing.

**Worth tuning:**

| Knob | Where | Why |
|---|---|---|
| `sync_interval_s` | storage row | Every poll is a **full recursive walk** of the backend. On a big, rarely changing share, raise it. |
| `sync_mode: ondemand` | storage row | Never walks on its own — you trigger it with `POST /api/admin/storages/{id}/sync` (e.g. from the job that writes to the share). |
| `filex thumb backfill` | CLI | Pays the first‑browse cost up front instead of making a user wait. Takes `--storage <id\|name>`, `--limit N`, `--concurrency N`, `--retry-failed`. |
| `FILEX_THUMB_BACKFILL_ON_BOOT=once` | env | Same thing, once, in the background at startup. |
| `disable_presign: true` | S3 `config` | The **opposite** of a speed‑up: it forces download bytes through filex instead of a redirect straight to the bucket. Use it only when presigned URLs don't work for your users (Hetzner/Ceph `SignatureDoesNotMatch`, or a bucket that isn't reachable from the browser). |

**Downloads support ranges.** `GET …?action=download|preview` answers
`Accept-Ranges: bytes` and serves `206` / `Content-Range` for a `Range`
request, so video and audio seek, a dropped download resumes from where it
stopped instead of restarting, and only the missing bytes are re-read from the
backend. All five drivers (`local`, `s3`, `sftp`, `ftp`, `webdav`) can start a
transfer at an offset; a driver that could not would answer
`Accept-Ranges: none` and serve the whole object, never a wrong window.
Public **share links** (`/s/…`) deliberately stay whole-object: one request
there is one download against the link's cap.

### Prepared copies for big downloads

Ranges make a download resumable and seekable, but they do not make a slow
backend fast. So when a **big** file lives on a **slow** storage, filex fetches
it to local disk once and says so while it happens:

1. the first `?action=download` is answered **`202`** — a progress page in a
   browser, `{"state":"preparing","percent":N}` for an API client;
2. the client polls `?action=download&…&cache=status` (the page does it for
   you) until `{"ready":true}`;
3. from then on the file is served from local disk — for **every** surface,
   with full `Range` support, at local-disk speed, without touching the backend
   again.

The copy is keyed on the file's identity (its ETag, or size+mtime for backends
that have none), so **a changed file invalidates itself**: the next request
prepares the new content rather than serving the old.

**When it happens.** Both conditions, together:

| Condition | How |
|---|---|
| The file is big | `size ≥ FILEX_CACHE_MIN_SIZE` (default 64 MiB) |
| The storage is slow | `"slow": true` in the storage's `config`, **or** measured below `FILEX_CACHE_SLOW_BPS` (default 10 MiB/s) |

```jsonc
{ "name": "nas", "driver": "local", "config": { "path": "/mnt/nas", "slow": true } }
```

**When it deliberately does not happen** — the rule being "never make it
worse":

* **Small files** are never prepared, whatever the flag says. One round trip
  beats a preparing screen.
* **A storage that measures fast** is not prepared even if you flagged it: a
  measurement at twice the threshold overrules the flag, because on a fast
  backend a prefetch replaces an instant stream with a wait. (Move a share onto
  a faster link and filex notices; you do not have to remember the flag.)
* **Previews** never wait. Scrubbing a video asks for a window, and it gets one.
  A preview still *uses* a copy that already exists.
* **`Range` requests** are never answered `202` — a resume or a seek is a client
  already committed to a body.
* **Public share links** are never answered `202` either: they spend one of the
  link's capped downloads before bytes leave, and "not yet" is not something to
  charge a visitor for. They do read from a copy that exists.
* **Files still being uploaded** (`transfer_state: staged`) are not prepared —
  their bytes are already on filex's local disk.

**Disk.** The cache directory (`<data_dir>/cache`) has a **global** ceiling,
`FILEX_CACHE_MAX_BYTES`, default 20 GiB, enforced with LRU eviction and counting
copies that are still being fetched. It is never unlimited. An entry a request
is currently reading is never evicted; when nothing can be freed, the new file
is simply not prepared and streams from the backend as before.

**What is not tuned away:** moving bytes still takes as long as the link takes.
An upload lands in filex's staging area first, so it is resumable and the client
stops waiting on the backend ([UPLOADS.md](UPLOADS.md)) — but the transfer to
the backend still runs at the backend's speed, and the *first* download of a big
file pays the full fetch before it is served. What the prepared copy buys is
that nobody pays it twice, and that the person waiting is told why.

---

## Moving files between storages

Copy, cut and drag work **across** storages, not only inside one. What each
gesture means is the rule every desktop file manager taught its users:

| Gesture | Same storage | Different storages |
|---|---|---|
| Ctrl+C → paste | copy | copy |
| Ctrl+X → paste | move | **move** — the bytes are streamed over, then the original is deleted |
| Drag onto a folder | move | **copy** — the original stays, exactly like dragging between two drives |

Across two storages there is no server-side rename to hand a driver (an S3
bucket cannot rename a file into an SFTP host), so filex streams the bytes
itself, one file at a time, through the queue you can watch in the ops tray:

- a whole tree travels, **empty folders included**;
- each file keeps **its own modification time** wherever the target can hold one,
  so a moved tree does not read as "everything changed just now" to the next
  sync run;
- every file is **stat-checked on the far side before anything is deleted** — a
  backend that accepts a write and stores fewer bytes fails the step instead of
  turning a move into data loss;
- a name already taken on the target becomes `name-copy`, `name-copy-2`, …;
  nothing is overwritten;
- filex's own `.filex-trash` and `.thumbs` are skipped — they belong to the
  storage they are in.

⚠ A cross-storage **move deletes the source outright**; it does not go through
the trash. Moving between storages is usually done to free the first one, and a
trashed copy would keep both the bytes and the quota until the trash is emptied.

Refusals happen at submit time, with a reason: an unknown target storage is a
`400`, a **read-only** target a `403` naming the storage, and a target folder you
have no editor rights on a `403` — the permission is checked in the
*destination's* storage, which is the one being written to.

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
