# Backend HTTP API

Base URL: `${FILEX_PUBLIC_URL}` (default `http://localhost:5212`).

All endpoints under `/api/*` return JSON. All write endpoints expect
`Content-Type: application/json` unless explicitly noted.

- [Auth & sessions](#auth--sessions)
- [Capabilities](#capabilities)
- [File browsing](#file-browsing)
- [Uploads (multipart)](#uploads-multipart)
- [Archives](#archives)
- [Sharing](#sharing)
- [Thumbnails](#thumbnails)
- [Operations (long-running)](#operations-long-running)
- [Admin: storages](#admin-storages)
- [Admin: users](#admin-users)
- [Admin: external services](#admin-external-services)
- [Admin: sync runs](#admin-sync-runs)
- [Admin: audit log](#admin-audit-log)

### Auth markers

| Symbol | Meaning |
|--------|---------|
| ![public](https://img.shields.io/badge/-public-lightgrey) | No auth |
| ![user](https://img.shields.io/badge/-user-blue)         | Any authenticated user |
| ![admin](https://img.shields.io/badge/-admin-red)         | Admin role required |

Auth is provided either by a session cookie (`filex_session`) or a Bearer
token (`Authorization: Bearer <jwt>`). Both are accepted on the same routes.

---

## Auth & sessions

### `POST /api/auth/login` ![public](https://img.shields.io/badge/-public-lightgrey)
Local-driver password login.

**Request**
```json
{ "email": "admin@local", "password": "kT9_x4Pq2Nm-BvLs" }
```
**Response 200**
```json
{
  "user": { "id": 1, "email": "admin@local", "username": "admin", "role": "admin" },
  "token": "eyJhbGc...",
  "expires_at": "2026-05-05T12:00:00Z"
}
```
The session cookie is set by the same response. The Bearer token is for SPA
embeds that prefer header auth.

**Status codes:** `200` ok · `401` invalid creds · `429` rate-limited.

### `POST /api/auth/logout` ![user](https://img.shields.io/badge/-user-blue)
Invalidates the session cookie / token.

### `GET /api/auth/oidc/start` ![public](https://img.shields.io/badge/-public-lightgrey)
Redirects (302) the browser to the configured OIDC issuer authorise URL.

**Query**: `?next=/path/to/return/to` (optional)

### `GET /api/auth/oidc/callback` ![public](https://img.shields.io/badge/-public-lightgrey)
OIDC redirect target. Validates `code`, exchanges for tokens, creates/updates
the user, sets the session cookie, redirects to `next`.

### `GET /api/auth/me` ![user](https://img.shields.io/badge/-user-blue)
**Response 200**
```json
{
  "user": {
    "id": 1, "email": "admin@local", "username": "admin",
    "role": "admin", "groups": ["filex-admin"],
    "avatar_url": "data:image/jpeg;base64,…"
  }
}
```

### `PATCH /api/auth/profile` ![user](https://img.shields.io/badge/-user-blue)
Patches the caller's own `email`, `display_name`, `locale`, `timezone` and
`avatar_url`. Absent fields are left alone.

`avatar_url` is the **profile picture**: a `data:image/…` URI (≤ 48 KB) or an
`http(s)` / site-relative URL; an explicit `""` removes it. Anything else is a
`400` rather than a silent drop — the person is looking at an upload they
believe worked. The admin SPA's profile page downscales what you pick to 160px
before encoding, so the cap is not something a user meets.

The picture belongs to the **account**, which is what makes it appear
everywhere: the explorer's collaboration strip draws it instead of initials for
that user on every client of the account — browser session, desktop app, and any
API key minted under it. Two deliberate exceptions, because the alternative is
drawing the wrong face on somebody's row:

- A token with a **username allow-list** is a shared proxy, not a person (its
  presence entry reads "work"), so no picture is attached.
- When a trusted host proxy re-identifies a connection as a different end user
  via `X-Filex-Presence-Name`, only *that* person's picture may be drawn —
  supplied by the proxy as `X-Filex-Presence-Avatar` (same accepted shapes, same
  cap). Without it the row falls back to initials.

The cap is small on purpose: the avatar rides inside every presence frame the
collaboration socket broadcasts, so it is paid for again on each join, leave and
focus change — unlike the branding logo, which is fetched once per page.

---

## Capabilities

### `GET /api/capabilities` ![user](https://img.shields.io/badge/-user-blue)
Tells the frontend what features are available — used to hide buttons for
disabled features.

**Response 200**
```json
{
  "version": "0.1.0",
  "thumbs": {
    "enabled": true,
    "image": true, "video": true, "pdf": true, "office": false
  },
  "external": {
    "onlyoffice_url": "https://docs.example.com",
    "drawio_url": "",
    "mermaid": true
  },
  "auth": {
    "drivers": ["local", "oidc"],
    "allow_signup": false
  },
  "limits": {
    "max_upload_bytes": 5368709120,
    "max_archive_bytes": 1073741824
  },
  "share_max_ttl_days": 7
}
```
Cached client-side for 1h. `share_max_ttl_days` is the longest life a new share
link may be given (0 = no ceiling; [PROTECTION.md](PROTECTION.md)).

---

## File browsing

### `GET /api/files/manager` ![user](https://img.shields.io/badge/-user-blue)
List the contents of a directory.

**Query**
| Param  | Type   | Default | Notes |
|--------|--------|---------|-------|
| `path` | string | `/`     | URL-encoded; e.g. `/storage1/sub/folder` |
| `sort` | enum   | `name`  | `name \| size \| modified` |
| `dir`  | enum   | `asc`   | `asc \| desc` |
| `limit`| int    | `1000`  | max items per page |
| `offset`| int   | `0`     | pagination offset |

**Response 200**
```json
{
  "path": "/storage1/sub",
  "entries": [
    {
      "name": "report.pdf", "type": "file", "size": 102400,
      "modified": "2026-04-22T10:00:00Z",
      "mime": "application/pdf",
      "etag": "abc123",
      "is_image": false, "is_video": false, "thumb_url": "/api/files/thumb?token=...",
      "id": 4711
    },
    {
      "name": "photos", "type": "dir", "size": 0,
      "modified": "2026-04-23T08:00:00Z", "id": 4712
    }
  ],
  "total": 2,
  "storage": { "name": "storage1", "driver": "s3", "readonly": false }
}
```

**Status codes:** `200` ok · `403` forbidden · `404` path missing.

### Filenames in `Content-Disposition`

Every endpoint that serves bytes (`action=download` / `preview`, share
downloads, the share browser, the viewer) sends RFC 6266:

```
Content-Disposition: attachment; filename="T_rk_e adl_ dosya.txt"; filename*=UTF-8''T%C3%BCrk%C3%A7e%20adl%C4%B1%20dosya.txt
```

⚠ The header is **always pure ASCII**. A raw non-ASCII byte in a header value is
outside the specification, and while browsers cope, strict clients throw while
parsing the response — Electron's `net.fetch` raises
`Cannot convert argument to a ByteString …` from inside its response handler,
which no caller's try/catch can catch. The ASCII `filename` is the fallback;
`filename*` carries the real name and is what every current browser uses.

### `GET /api/files/raw?path=…` ![user](https://img.shields.io/badge/-user-blue)
Stream the raw file bytes. Sends `Content-Type`, `Content-Length`, and
honours `Range:` for partial GETs (video / audio scrub).

### `POST /api/files/move` ![user](https://img.shields.io/badge/-user-blue)
**Request** — sources and the destination FOLDER, both adapter-qualified.
`sourceDir` is where the drag/cut came from (it stamps the undo).
```json
{
  "source": ["alpha://a.txt", "alpha://klasor"],
  "target": "beta://hedef",
  "sourceDir": "alpha://"
}
```
**Response 202** `{ "op": { "id": 12, "kind": "move", "storage_id": 1, "dest_storage_id": 2, … } }`
— the work is queued; poll `GET /api/files/ops`.

### `POST /api/files/copy` ![user](https://img.shields.io/badge/-user-blue)
Same shape, same queued answer.

**The two ends may live in different storages.** `dest_storage_id` on the queued
op is the target's storage; when it differs from `storage_id`, the worker
streams the bytes between the two drivers instead of asking one driver to
rename — a whole tree, empty folders included, each file's mtime preserved where
the target can hold one, and every file stat-checked on the far side before a
move deletes anything. A cross-storage move removes the source outright (not to
the trash); a name already taken becomes `name-copy`. Full behaviour:
[Moving files between storages](STORAGE.md#moving-files-between-storages).

**Refusals** are at submit time, not in the worker: `400` unknown target adapter
· `403` read-only target storage (with a `hint`) · `403` no editor permission on
the source, or on the target folder **in the destination's storage** · `400`
mixed-adapter *sources* (one batch, one source storage).

⚠ Before v0.27.0 the destination's `<adapter>://` prefix was dropped and the
remaining relative path applied to the SOURCE storage, so a cross-storage paste
answered `202` and wrote the file into the depo it was copied from.

### `POST /api/files/ops` ![user](https://img.shields.io/badge/-user-blue)
The unified form behind the three per-verb endpoints:
```json
{ "kind": "copy", "storage_id": 1, "dest_storage_id": 2,
  "sources": ["a.txt"], "dest": "hedef/" }
```
`dest_storage_id` may be omitted or `0`, which means "the sources' storage".

### `POST /api/files/mkdir` ![user](https://img.shields.io/badge/-user-blue)
```json
{ "path": "/storage1/new-folder" }
```

### `POST /api/files/rename` ![user](https://img.shields.io/badge/-user-blue)
```json
{ "path": "/storage1/old.txt", "new_name": "new.txt" }
```

### `POST /api/files/delete` ![user](https://img.shields.io/badge/-user-blue)
```json
{ "paths": ["/storage1/a.txt", "/storage1/sub/"] }
```
Returns `200 + { deleted: ["..."], failed: [{ path: "...", error: "..." }] }`.

### `GET /api/files/manager/shared-with-me` ![user](https://img.shields.io/badge/-user-blue)

What other people have shared with the caller — the items they reach through a
per-item grant rather than their own role. Newest grant first.

| Param | Default | Meaning |
|---|---|---|
| `limit` | `100` | Page size, max 500. |
| `offset` | `0` | Page offset. |

```json
{ "files": [ /* listing entries, same shape as /api/files/manager */ ],
  "storages": ["marketing"], "total": 2, "limit": 100, "offset": 0 }
```

Each entry carries `perm` (the grant's level), `shared: true` and `shared_at`.
A grant on a folder lists **the folder**, not its contents — the row is a `dir`
whose `path` is adapter-qualified, so opening it navigates in the ordinary way.
`storages` names the storages the caller reaches only through a grant: those are
the "shared drives", and a storage-wide grant is reported there rather than as a
row with an empty name. Results are filtered by tenant scope and by the caller's
root confinement. See [RBAC.md](RBAC.md).

### `GET /api/files/search?q=…` ![user](https://img.shields.io/badge/-user-blue)
Bleve full-text + metadata search. Same response shape as `/api/files/manager`
but with `path` echoing the matching entry's full path.

| Param | Default | Meaning |
|---|---|---|
| `q` (or `query`) | — | Search text. May carry `tag:x` / `-tag:x` filters. |
| `storage_id` | `0` (all) | Restrict to one storage. Also what enables the SQL LIKE fallback. |
| `limit` | `50` | Max results. |
| `scope` | `all` | `name` \| `content` \| `all`. |

`POST /api/files/search` takes the same fields as a JSON body. Hits come back in
a defined rank order — exact filename, prefix, name, path, fuzzy, then
content-only. Full reference: [SEARCH.md](SEARCH.md).

---

## Uploads (multipart)

For files >5 MB. Smaller files can use `POST /api/files/upload` (single-shot
`multipart/form-data`).

### `POST /api/files/upload/init` ![user](https://img.shields.io/badge/-user-blue)
**Request**
```json
{
  "storage_id": 1,
  "path": "storage1://big.iso",
  "filename": "big.iso",
  "size": 5368709120,
  "mime": "application/octet-stream",
  "chunk_bytes": 16777216
}
```
`storage_id` may be omitted when `path` carries an adapter prefix; `filename` is
optional and folded onto `path` when both are sent (an upload to a storage root
arrives as `path: "adapter://"` plus a filename). `chunk_bytes` is a request:
the server raises anything below 5 MiB and re-balances so an upload never
exceeds 10 000 parts — **use the `part_size` it answers with**.

**Response 200**
```json
{
  "upload_id": "u_AbCdEf",
  "part_urls": [
    "https://s3.example.com/...&partNumber=1&X-Amz-Sig=...",
    "https://s3.example.com/...&partNumber=2&X-Amz-Sig=..."
  ],
  "part_size": 16777216,
  "part_count": 320,
  "expires_at": "2026-04-29T00:00:00Z"
}
```
`part_urls` is a **flat list of URLs**, one per part in order — the browser PUTs
each chunk straight to its own URL, then calls `/finalize` (or `/abort`).

> ⚠ There is **no chunk-through-filex fallback on this endpoint**. A driver that
> cannot do multipart at all (local, sftp, ftp, webdav) answers
> **`501 storage does not support multipart upload`** at `init` — earlier
> versions of this page described a `POST /api/files/upload/chunk` route as the
> fallback; that route does not exist. Measured 2026-08-19.

> ⚠⚠ A [plugin](PLUGINS.md) storage that declares `multipart` passes the check
> at `init` and then usually answers **no part URLs** (`part_urls: null`),
> because a plugin's multipart is built for the staged-upload commit, where
> filex pushes the parts itself. There is nothing for the browser to PUT to —
> use the staged path for plugin storages.

> ⚠ This whole endpoint is the **older** browser-chunked path, kept for older
> embedders. No filex client speaks it any more: the staged path
> ([UPLOADS.md](UPLOADS.md)) replaced it everywhere, works on every driver, and
> is the only one that can resume.

### `POST /api/files/upload/finalize` ![user](https://img.shields.io/badge/-user-blue)
```json
{
  "upload_id": "u_AbCdEf",
  "etags": [
    { "part": 1, "etag": "..." },
    { "part": 2, "etag": "..." }
  ]
}
```
**Response 200** `{ "id": 99, "path": "/storage1/big.iso", "size": 5368709120, "etag": "..." }`

### `POST /api/files/upload/abort` ![user](https://img.shields.io/badge/-user-blue)
```json
{ "upload_id": "u_AbCdEf" }
```
Cancels the upload and discards staged chunks.

---

## Archives

Server-side zip handling. Limited to `FILEX_LIMITS_MAX_ARCHIVE_BYTES`
(default 1 GiB).

### `POST /api/files/archive/list` ![user](https://img.shields.io/badge/-user-blue)
**Request**
```json
{ "path": "/storage1/archive.zip" }
```
**Response 200**
```json
{
  "entries": [
    { "name": "a.txt", "size": 100, "is_dir": false, "modified": "..." },
    { "name": "sub/", "size": 0, "is_dir": true, "modified": "..." }
  ]
}
```

### `POST /api/files/archive/extract` ![user](https://img.shields.io/badge/-user-blue)
```json
{
  "path": "/storage1/archive.zip",
  "dest": "/storage1/extracted/",
  "overwrite": false
}
```
Returns `202 + { operation_id: "op_..." }` and runs in background.

### `POST /api/files/archive/add` ![user](https://img.shields.io/badge/-user-blue)
```json
{
  "paths": ["/storage1/a.txt", "/storage1/sub/"],
  "dest": "/storage1/bundle.zip",
  "compression": "deflate"
}
```
Returns `202 + { operation_id: "op_..." }`.

---

## Sharing

PIN-protected, time-limited, optionally download-capped public links.

### `POST /api/files/share` ![user](https://img.shields.io/badge/-user-blue)
**Request**
```json
{
  "path": "/storage1/report.pdf",
  "ttl": "168h",
  "max_downloads": 10,
  "pin": "1234",
  "comment": "for the auditors"
}
```
**Response 200**
```json
{
  "id": 42,
  "url": "https://files.example.com/s/Xy3kPq",
  "token": "Xy3kPq",
  "expires_at": "2026-05-05T12:00:00Z",
  "expiry_clamped": false,
  "max_downloads": 10
}
```

`expires_at` is what was **stored**, not what was asked: every new link is
capped at the admin's maximum link life (`share.max_ttl_days`, default 7 days —
[PROTECTION.md](PROTECTION.md)). A request with no expiry gets one, a longer
request is shortened, and `expiry_clamped: true` marks either case. The ceiling
itself is public in `GET /api/capabilities` as `share_max_ttl_days` so a client
can offer only expiries the server will keep.

### `GET /api/files/share` ![user](https://img.shields.io/badge/-user-blue)
List shares the caller owns.

**Response 200**
```json
{
  "shares": [
    { "id": 42, "path": "/storage1/report.pdf", "token": "Xy3kPq",
      "expires_at": "...", "max_downloads": 10, "downloads": 3, "created_at": "..." }
  ]
}
```

### `DELETE /api/files/share/:id` ![user](https://img.shields.io/badge/-user-blue)
Revokes a share.

### `GET /s/:token` ![public](https://img.shields.io/badge/-public-lightgrey)
HTML viewer page (server-rendered Vue island).

### `POST /api/share/:token/verify` ![public](https://img.shields.io/badge/-public-lightgrey)
```json
{ "pin": "1234" }
```
Returns short-lived `download_token` to be used with `/api/share/:token/download`.

### `GET /api/share/:token/download?dt=…` ![public](https://img.shields.io/badge/-public-lightgrey)
Streams the file. Increments the download counter; rejects if exceeded.

---

## Thumbnails

### `GET /api/files/thumb` ![public-signed](https://img.shields.io/badge/-public%2Fsigned-yellow)
**Query**
| Param   | Type   | Notes |
|---------|--------|-------|
| `token` | string | HMAC-signed `(file_id, size, exp)` |
| `size`  | int    | `64 \| 128 \| 256 \| 512` |

Token is generated by the backend and embedded in the file listing payload —
not user-craftable. Public so that `<img>` works without sending the session
cookie.

---

## Operations (long-running)

Copy / extract / archive create kick off background ops.

### `GET /api/files/ops` ![user](https://img.shields.io/badge/-user-blue)
List the caller's ops.

**Response 200**
```json
{
  "ops": [
    {
      "id": "op_AbCd", "kind": "copy", "status": "running",
      "progress": 0.42, "started_at": "...", "eta_seconds": 120
    }
  ]
}
```

### `GET /api/files/ops/:id` ![user](https://img.shields.io/badge/-user-blue)
Single op detail; same shape + final `error` if failed.

`status` is one of `queued | running | completed | failed | cancelled`.

### `POST /api/files/ops/:id/cancel` ![user](https://img.shields.io/badge/-user-blue)
Best-effort cancel. Returns `200` regardless; check `status` afterwards.

---

## Admin: storages

### `GET /api/admin/storage-drivers` ![admin](https://img.shields.io/badge/-admin-red)
The config contract every registered storage driver declares: its fields, their
type, which one is the storage root, which hold credentials, defaults and an
i18n key per label. Admin UIs render their storage forms from this instead of
hardcoding a field list, and the root‑path guard reads the same declaration.
**Response 200**
```json
[
  { "driver": "s3", "label": "S3 / Hetzner / MinIO", "i18n_key": "storages.driver.s3",
    "capabilities": { "read": true, "write": true, "presign": true },
    "fields": [
      { "key": "bucket", "type": "string", "required": true, "secret": false,
        "label": "Bucket", "i18n_key": "storages.fields.bucket" },
      { "key": "prefix", "type": "string", "required": true, "root": true,
        "label": "Prefix", "i18n_key": "storages.fields.prefix" }
    ] }
]
```
See [STORAGE.md → Driver descriptors](STORAGE.md#driver-descriptors-get-apiadminstorage-drivers).
`GET /api/capabilities` keeps its plain `storage_drivers: []string` name list.

> A **plugin** driver (`plugin:<name>`) appears in this list too, with the
> fields the plugin described — which is what lets an admin form render a
> driver that did not exist when the frontend was built. See
> [PLUGINS.md](PLUGINS.md).

---

## Admin: plugins

Storage drivers that live outside the binary. Instance-wide, and in
multi-tenant mode **supertenant-only** (a tenant admin gets `403
supertenant_only`, not an empty list). With `FILEX_PLUGINS_DISABLED=1` every
route answers `503 plugins_disabled`. Full picture: [PLUGINS.md](PLUGINS.md).

### `GET /api/admin/plugins` ![admin](https://img.shields.io/badge/-admin-red)
Every registered plugin plus its live state — the row is the admin's intent,
the state is what the manager sees right now.
**Response 200**
```json
{
  "dir": "/data/plugins",
  "requires_signature": false,
  "conformance": "enforce",
  "plugins": [
    { "id": 1, "name": "memfs", "kind": "binary", "binary": "memfs",
      "sha256": "9f2c…", "enabled": true, "version": "1.0.0",
      "driver": "memfs", "state": "running", "restarts": 0,
      "label": "In-memory (example)", "field_count": 1, "in_use": 1,
      "capabilities": { "write": true, "delete": true, "set_mtime": true,
                        "presign": false, "multipart": true },
      "conformance": {
        "verified": true, "scratch": "selftest",
        "ran_at": "2026-08-19T09:14:02Z",
        "results": [
          { "name": "write", "status": "pass", "took_ms": 1180400 },
          { "name": "presign", "status": "skip", "detail": "not declared", "took_ms": 0 }
        ]
      },
      "load": { "in_flight": 0, "waited": 0, "rejected": 0, "max_in_flight": 10 } }
  ]
}
```
`state` is one of `running` · `starting` · `failed` · `refused` · `disabled`;
`state_error` carries the reason for the last two. `in_use` counts storages on
this plugin's driver.

Top level: **`requires_signature`** says this instance refuses unsigned binaries
(trusted keys are configured), and **`conformance`** is the *mode* —
`enforce` · `warn` · `off`. Both are published so a surface can state the rules
before an install instead of after a rejection.

Per plugin: **`conformance`** is the last probe *report* — `verified`, `scratch`
(`selftest` when it ran against the plugin's own throwaway instance, `storage`
when it ran against a real storage), and one `results` entry per probe with
`status` `pass` · `fail` · `skip` and a `detail` written for the plugin's
author. **Absent means never probed** — "unverified", which is not the same as
failed. **`load`** is live: in-flight operations, callers that had to wait, and
callers that were **rejected** (anything above 0 is a user meeting an error
because the plugin is saturated).

> ⚠ Two different things are called `conformance` in one document: a **mode**
> at the top level, a **report** inside each plugin. Read the level before the
> name.

> ⚠ `took_ms` is a Go `time.Duration`, which `encoding/json` writes as
> **nanoseconds** despite the field name. Divide by 1e6 before printing
> milliseconds.

### `POST /api/admin/plugins` ![admin](https://img.shields.io/badge/-admin-red)
Install, in one of three shapes — the Content-Type picks which:

| Shape | Body |
|---|---|
| upload | `multipart/form-data` with `name`, `file` and optionally `signature` |
| download | `{"name":"…","url":"https://…","sha256":"…","signature":"…"}` — the hash is **required** |
| remote | `{"name":"…","kind":"remote","address":"http(s)://…","token":"…"}` |

**201** with the same object as above. `409` when the name is taken, `400` for
a bad name (`[a-z0-9][a-z0-9_-]{0,31}`), a missing hash, a remote plugin
with no `FILEX_SECRET_KEY` configured to seal its token, or — when
`requires_signature` is true — a missing or unverifiable `signature` (a detached
ed25519 signature over the binary's lower-case hex sha256).

> ⚠ **201 does not mean usable.** Installing writes the row and starts the
> plugin; describe and the conformance probes happen after that, asynchronously.
> A plugin that declares a capability it cannot perform is accepted here and
> then lands in `refused` with `state_error` containing *"fails its own
> claims"*, and its driver is never registered. Poll `GET /api/admin/plugins`
> for the state rather than treating the 201 as the answer.

### `POST /api/admin/plugins/{id}/upgrade` ![admin](https://img.shields.io/badge/-admin-red)
`multipart/form-data` with `file` (and `signature` when required). Replaces a
**binary** plugin's file while keeping the row, the name, the driver and every
storage built on it — remove+install would take the registration with it, and a
storage whose driver has gone cannot open.

Sequence: stop → swap the file → start → describe → conformance. **200** with
the plugin's status when the new binary comes up. Otherwise **400** with
`{"error": "…the previous one was restored", "plugin": {…}}` — the old binary is
put back and started again, and the body carries the status so a page can show
what is running now instead of leaving the operator guessing. `400` too for a
remote plugin: it is upgraded where it runs.

### `PATCH /api/admin/plugins/{id}` ![admin](https://img.shields.io/badge/-admin-red)
`{"enabled": true|false}`. Disabling unregisters the driver, so storages on it
stop opening — they are not deleted.

### `POST /api/admin/plugins/{id}/restart` ![admin](https://img.shields.io/badge/-admin-red)
Stop and start it. The way out of `refused` once the cause is fixed. The
conformance probes run again on every start, so a fixed plugin proves itself
without an extra step.

### `DELETE /api/admin/plugins/{id}` ![admin](https://img.shields.io/badge/-admin-red)
**204.** Removes the registration and, for a binary plugin, its directory.
Storages created on it are left in place.

### `GET /api/admin/storages` ![admin](https://img.shields.io/badge/-admin-red)
**Response 200**
```json
{
  "storages": [
    { "id": 1, "name": "Local", "driver": "local", "readonly": false,
      "config_summary": "/var/lib/filex/local-storage", "last_sync": "..." }
  ]
}
```

### `POST /api/admin/storages` ![admin](https://img.shields.io/badge/-admin-red)
**Request** (driver-specific fields)
```json
{
  "name": "Hetzner archive",
  "driver": "s3",
  "config": {
    "bucket": "...", "region": "...", "endpoint": "...",
    "access_key": "...", "secret_key": "..."
  },
  "readonly": false,
  "sync_interval": "5m"
}
```
**Response 200** `{ "id": 7, "name": "Hetzner archive", ... }`

> ⚠ A storage on a **plugin** driver (`plugin:<name>`) is probed against this
> exact configuration *before the row is written*: filex opens the driver,
> exercises every capability the plugin declared inside a scratch folder
> (`.filex-conformance-<random>`, removed afterwards) and answers **400** with
> the failing probe if it does not hold up — including `the plugin providing
> "plugin:x" is not running` when the driver is not currently registered.
> Built-in drivers are not probed; `FILEX_PLUGIN_CONFORMANCE=off` (or `warn`)
> skips the gate. The whole check is bounded at 2 minutes, so a plugin that
> accepts connections and then says nothing cannot hang the save.
> See [PLUGINS.md → Conformance](PLUGINS.md#conformance-a-plugin-has-to-prove-its-claims).

### `PUT /api/admin/storages/:id` ![admin](https://img.shields.io/badge/-admin-red)
Same body shape; partial updates allowed. A plugin storage is **re-probed on
every change** — the operator may have just pointed it at a different bucket,
and a configuration that half works fails the same way a half-working plugin
does: in the user's hands, looking like filex.

### `DELETE /api/admin/storages/:id` ![admin](https://img.shields.io/badge/-admin-red)
Removes the storage and its DB cache rows. Files in the underlying backend
are **not** deleted.

### `POST /api/admin/storages/:id/sync` ![admin](https://img.shields.io/badge/-admin-red)
Triggers an immediate sync run. Returns `202 + { run_id: "..." }`; poll via
`/api/admin/sync/runs/:id`.

### `POST /api/admin/storages/:id/test` ![admin](https://img.shields.io/badge/-admin-red)
Validates the connection without persisting.

---

## Admin: users

### `GET /api/admin/users` ![admin](https://img.shields.io/badge/-admin-red)
List users. In multi-tenant mode the list is confined to the caller's tenant
(the supertenant sees all). Each row carries `used_bytes` and `quota_bytes`
(`0` = unlimited) and `enabled`, so a usage table costs one call.

### `GET /api/admin/users/{id}` ![admin](https://img.shields.io/badge/-admin-red)

### `POST /api/admin/users` ![admin](https://img.shields.io/badge/-admin-red)
```json
{
  "email": "newuser@example.com",
  "display_name": "New User",
  "role": "user",
  "password": "...",
  "provider_id": 3
}
```
`provider_id` homes the user in a tenant. Omit it and the user lands in the
**caller's** tenant. A tenant admin may only name their own provider (`403`
otherwise); an id that matches no provider is `400`. There is no foreign key
behind the column, so it is validated here.

### `PATCH /api/admin/users/{id}` ![admin](https://img.shields.io/badge/-admin-red)
Partial update — only the fields present in the body are touched:
`password`, `display_name`, `role`, `locale`, `timezone`, `enabled`,
`provider_id`.

`enabled: false` cuts access without deleting anything: the account cannot
log in (local, OIDC or `/dav`), existing sessions stop working, and every API
token it minted is refused. Files, quota and grants are untouched. Disabling
— like deleting or demoting — the **last admin** is refused with `409`.

`provider_id` re-homes the user into another tenant. Restricted to an
unscoped or supertenant caller (`403` otherwise).

### `GET|POST|PATCH /api/admin/users/{id}/quota` ![admin](https://img.shields.io/badge/-admin-red)
Read or set one user's quota — see [Admin: quota](#admin-quota). `POST
/api/admin/users/{id}/quota/recompute` rebuilds `used_bytes` from node sizes.

### `DELETE /api/admin/users/{id}` ![admin](https://img.shields.io/badge/-admin-red)
Deletes the account row. The last remaining admin cannot be deleted (`409`),
not even by itself.

**What happens to their files: nothing.** No storage object is ever removed.
The node rows survive and `nodes.owner_id` becomes `NULL`, so the files
become **unowned** — still present, still listed, still reachable by anyone
whose access does not depend on that user. Deletion is not a way to reclaim
space; move or delete the files first if that is the intent.

Precisely, on `DELETE`:

| Kept, with the user dropped (`SET NULL`) | Removed with the user (`CASCADE`) |
| --- | --- |
| `nodes.owner_id` — the files themselves | `sessions` |
| `shares.created_by` — see below | `api_tokens` |
| `file_grants.created_by` | `file_grants.user_id` — access granted **to** them |
| `audit_log.user_id` — history stays readable | `notifications`, `user_node_meta`, `node_comments` |

Share links the user created **stay live**: public resolution never looks at
`created_by`. But revoking one does, and a `NULL` creator matches nobody — so
an orphaned link can only be revoked by an admin, via
`DELETE /api/admin/shares/{id}`. Audit those before deleting a user who
shared a lot.

Their `usage_bytes` row goes with the account, so those bytes stop counting
toward any per-user total while the files remain on storage. To keep the
account's history and quota intact, prefer `enabled: false` over deletion.

---

## Admin: quota

Quota is **per user**. There is no per-provider (tenant) quota — see
[MULTI-TENANCY.md](MULTI-TENANCY.md). Every id in this section is a **user
id**; passing a provider id answers `404`.

Two spellings, same handlers:

| Nested (preferred) | Flat (original) |
| --- | --- |
| `GET /api/admin/users/{id}/quota` | `GET /api/admin/quota/{user_id}` |
| `POST` / `PATCH /api/admin/users/{id}/quota` | `POST /api/admin/quota/{user_id}` |
| `POST /api/admin/users/{id}/quota/recompute` | `POST /api/admin/quota/{user_id}/recompute` |

### `GET …/quota` ![admin](https://img.shields.io/badge/-admin-red)
```json
{ "used_bytes": 1234, "quota_bytes": 5368709120, "percent_used": 0.00002, "unlimited": false }
```
A user who has never had a quota set reads back `quota_bytes: 0`,
`unlimited: true` — that is not an error. An id that names no user is `404`.

### `POST|PATCH …/quota` ![admin](https://img.shields.io/badge/-admin-red)
```json
{ "quota_bytes": 5368709120 }
```
`0` means unlimited; negative is `400`. Returns the fresh snapshot.

### `POST …/quota/recompute` ![admin](https://img.shields.io/badge/-admin-red)
Rebuilds `used_bytes` from the summed size of the nodes the user owns.
Worth running after bulk imports, or after deleting a user whose files were
left behind (their bytes stop being attributed to anyone).

The caller's own snapshot is at `GET /api/files/quota/me`.

---

## Admin: external services

### `GET /api/admin/external` ![admin](https://img.shields.io/badge/-admin-red)
**Response 200**
```json
{
  "services": [
    { "name": "onlyoffice", "url": "https://docs.example.com",
      "enabled": true, "healthy": true, "last_check": "..." },
    { "name": "drawio", "url": "", "enabled": false, "healthy": null }
  ]
}
```

### `PUT /api/admin/external/:name` ![admin](https://img.shields.io/badge/-admin-red)
```json
{ "url": "https://docs.example.com", "jwt_secret": "..." }
```

### `POST /api/admin/external/:name/test` ![admin](https://img.shields.io/badge/-admin-red)
Probes `${url}/healthcheck` (or the service's equivalent). Returns
`200 + { healthy: true, version: "...", latency_ms: 23 }`.

---

## Admin: sync runs

### `GET /api/admin/sync/runs` ![admin](https://img.shields.io/badge/-admin-red)
**Query**: `?storage_id=…&limit=50&offset=0`

**Response 200**
```json
{
  "runs": [
    {
      "id": 12, "storage_id": 1, "status": "completed",
      "started_at": "...", "finished_at": "...",
      "added": 4, "updated": 2, "removed": 1, "errors": 0
    }
  ]
}
```

### `GET /api/admin/sync/runs/:id` ![admin](https://img.shields.io/badge/-admin-red)
Includes per-error detail array.

---

## Admin: audit log

### `GET /api/admin/audit` ![admin](https://img.shields.io/badge/-admin-red)
**Query**: `?user_id=&action=&from=&to=&limit=100`

**Response 200**
```json
{
  "events": [
    {
      "id": 9001, "ts": "...", "user_id": 1, "user_email": "admin@local",
      "action": "share.create", "resource": "/storage1/x.pdf",
      "ip": "1.2.3.4", "ua": "Mozilla/...",
      "meta": { "ttl": "168h", "max_downloads": 10 }
    }
  ]
}
```

Standard `action` values: `auth.login`, `auth.logout`, `auth.failed`,
`file.upload`, `file.delete`, `file.move`, `file.copy`, `share.create`,
`share.revoke`, `storage.add`, `storage.delete`, `user.create`,
`user.disable`, `admin.config_change`.

---

## Error envelope

All error responses use the same shape:

```json
{
  "error": "validation_failed",
  "message": "size must be > 0",
  "details": { "field": "size" }
}
```

Common error codes: `unauthorised`, `forbidden`, `not_found`,
`validation_failed`, `rate_limited`, `conflict`, `internal`,
`storage_unreachable`, `quota_exceeded`.
