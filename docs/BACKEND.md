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
- [The public surface](#the-public-surface)
- [Interface preferences](#interface-preferences)
- [Thumbnails](#thumbnails)
- [Versions](#versions)
- [Realtime (WebSocket)](#realtime-websocket)
- [Operations (long-running)](#operations-long-running)
- [Admin: storages](#admin-storages)
- [Admin: plugins](#admin-plugins)
- [App plugins](#app-plugins)
- [Admin: users](#admin-users)
- [Admin: quota](#admin-quota)
- [Admin: external services](#admin-external-services)
- [Admin: protection & antivirus](#admin-protection--antivirus)
- [Admin: webhooks](#admin-webhooks)
- [Admin: sync runs](#admin-sync-runs)
- [Admin: audit log](#admin-audit-log)

### Auth markers

| Symbol | Meaning |
|--------|---------|
| ![public](https://img.shields.io/badge/-public-lightgrey) | No auth |
| ![user](https://img.shields.io/badge/-user-blue)         | Any authenticated user |
| ![admin](https://img.shields.io/badge/-admin-red)         | Admin role required |
| ![signed](https://img.shields.io/badge/-signed-yellow)    | A session/token **or** a signed URL — see the route |

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
Deletes the session and clears the cookie.

**Body** (optional): `{"return_to": "/admin/login" | "/drive/login"}` — the
sign-in page of the front door the person was using. Anything else means
`/admin/login`; the address is never taken from the request.

**Response 200**: `{"ok": true}`, plus `"logout_url"` for an SSO session whose
identity provider can end sessions — its end-session URL with `id_token_hint`,
`client_id` and `post_logout_redirect_uri` (`<front door>?signed_out=1`). The
web app sends the browser there so the provider's session ends too
(RP-initiated logout, [SSO.md](SSO.md#signing-out)). Never present with
`FILEX_OIDC_LOGOUT=local`, for a password session, or for a session signed in
through a provider that has since been replaced.

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
believe worked. The SPA's user-settings dialog downscales what you pick to 160px
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

**Response 200** (abridged — the real body also carries the per-storage probe,
build metadata and a set of flat aliases kept for older embeds)
```json
{
  "version": "0.34.0",
  "upload": true, "move": true, "copy": true, "delete": true, "mkdir": true,
  "search": true, "versions": true, "ocr": false,
  "thumbs": {
    "enabled": true,
    "image": true, "video": true, "pdf": true, "office": false
  },
  "antivirus": true,
  "antivirus_mode": "daemon",
  "external": {
    "onlyoffice": { "enabled": true, "url": "https://docs.example.com", "state": "ok" },
    "drawio":     { "enabled": false, "url": "", "state": "" },
    "convert":    { "enabled": false, "url": "", "state": "" }
  },
  "onlyoffice_url": "https://docs.example.com",
  "drawio_url": "",
  "convert_url": "",
  "max_upload_size": 5368709120,
  "chunk_size": 8388608,
  "auth_drivers": ["local", "oidc"],
  "storage_drivers": ["ftp", "local", "s3", "sftp", "smb", "webdav"],
  "db_driver": "sqlite",
  "share_max_ttl_days": 7
}
```
Cached client-side for 1h. `share_max_ttl_days` is the longest life a new share
link may be given (0 = no ceiling; [PROTECTION.md](PROTECTION.md)).

⚠⚠ **An anonymous caller is told _whether_ a capability is on, never _where_ it
lives.** The endpoint is deliberately public — an embedder probes it before
anybody logs in ([INTEGRATION.md](INTEGRATION.md)) — so for a request carrying
no usable credential the `url` is dropped from every `external.<service>` entry
and the flat `onlyoffice_url` / `drawio_url` / `convert_url` aliases come back
empty. `enabled` and `state` are unchanged, which is what a feature probe
actually asks.

Signed-in callers see the payload above in full, because two consumers need a
real host in the browser: the draw.io iframe and the convert modal. OnlyOffice
does not — the browser gets its document-server URL from the authenticated
`POST /api/files/onlyoffice/config` (`documentServerUrl`) — so that host now
travels only with a credential as well.

Measured before this changed (2026-09-07, demo.filex.sh): an unauthenticated
`GET /api/files/capabilities` answered 200 with
`"url": "https://docs.example.com"` — the operator's internal document server,
published by every install that configured one.

`newdoc_types` is the other field worth naming, because it decides what a
"New document" menu may offer: the document types **this build can create**,
from a template registry compiled into the binary (`internal/newdoc`). Each row
is `{ ext, group, mime, requires, ext_required }`, where `requires` names the
external service the *editor* needs (`"onlyoffice"`, `"drawio"`, or absent for
the built-in code and markdown editors), and `ext_required` says whether the
file must carry the extension — `true` for office documents and diagrams, whose
editors find them by it, `false` for text types, which a person may name
anything (`LICENSE`, `test.conf`; #56). It is published as `false`, never
omitted: its absence is how a client recognises a server from before #56.

```json
"newdoc_types": [
  { "ext": "docx", "group": "document", "mime": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "requires": "onlyoffice", "ext_required": true },
  { "ext": "md",   "group": "text",     "mime": "text/markdown; charset=utf-8", "ext_required": false }
]
```

⚠ It answers "can the SERVER make these bytes", not "can this deployment open
them". A client crosses `requires` against the `external` block above and
offers only what is satisfied, which is what stops an install with no document
server from offering a `.docx` nobody there can then open — and what keeps the
client from carrying a hardcoded extension list that rots the moment the
registry grows a type. Published to anonymous callers too: it is a static
property of the build, identical on every install of this version, and names no
host.

⚠ `antivirus` means **configured**, not answering: the setting is on and either
a scanner binary resolved or a clamd address is set. Reachability costs a
network round trip and is probed on `GET /api/admin/protection`, where an
operator is waiting for the answer. `antivirus_mode` is `binary` or `daemon`
and is absent when `antivirus` is false — the two deployments produce the same
green light and behave very differently when one of them breaks.

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
      "is_image": false, "is_video": false, "thumb_url": "/api/files/thumb/42?exp=…&sig=…",
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

### Names filex keeps for itself

`.filex-trash`, `.versions`, `.thumbs`, the desktop app's `.filex-open` (at any
depth) and the empty-folder marker `.keepdir` are filex's own
(`backend/internal/syspath`). They are never listed or searched, and every
write that names one — creating, uploading, renaming, moving, copying,
extracting, saving, sharing, granting or restoring — answers:

```json
HTTP 403
{ "error": "\".filex-trash\" is reserved for filex's own use", "code": "RESERVED_NAME", "name": ".filex-trash" }
```

An archive member under one of them is skipped, the way a zip-slip entry is.
The single exception is the desktop's open-with round trip: `newfolder` of
`.filex-open` at the storage root, `upload` of `.filex-open/<hex session>-<name>`
(and the document editor's save of that copy), and `delete` of it.

### `GET /api/files/manager?action=changes` ![user](https://img.shields.io/badge/-user-blue)
Has anything under a folder changed since the caller last asked?

| Param   | Notes |
|---------|-------|
| `path`  | `<storage>://<folder>` (a file path works too) |
| `since` | the `cursor` from the previous answer; empty = never asked |

**Response 200** `{ "cursor": "<opaque>", "changed": true }`

Answered from an in-memory change log fed by the same event chain that
refreshes open explorers and folder sizes — every write surface (explorer,
WebDAV, S3, SFTP, FTPS, NFS, trash and version restores, the ops queue). Every
doubt reads as `changed`: no `since`, a cursor from before a restart, one older
than the log still holds (4,096 changes per storage), an event on the way to
the folder that does not say what it touched. Changes the catalogue learns from
a **storage scan** (bytes written straight into the backend) do not pass
through the log, which is why sync clients keep a slower full walk as a safety
net. `403` when the caller cannot see the folder; `400` on a `..` segment;
`501` on servers without the log.

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

⚠ `sourceDir` is **accepted and ignored** by the server. It decodes into the
request struct and no handler reads it; the undo it was said to "stamp" is
built entirely in the client. Root-confined callers do have it rewritten by the
confine layer, so it is not free to lie in — but nothing depends on it either.
Send it or don't.
```json
{
  "source": ["alpha://a.txt", "alpha://klasor"],
  "target": "beta://hedef",
  "sourceDir": "alpha://"
}
```
**Response 202** `{ "op": { "id": 12, "kind": "move", "storage_id": 1, "dest_storage_id": 2, … } }`
— the work is queued; poll `GET /api/files/ops`.

**Nothing is moved on top of something.** When the destination folder already
holds the name, the moved item lands beside it as `name-copy`, `name-copy-2`, …
— within one storage exactly as between two. A move into the folder the item is
already in changes nothing. (Before 0.41.0 a same-storage move replaced the
file that held the name.) A **rename** onto a taken name is refused instead —
see `POST /api/files/manager?action=rename` below.

### `POST /api/files/copy` ![user](https://img.shields.io/badge/-user-blue)
Same shape, same queued answer.

**The two ends may live in different storages.** `dest_storage_id` on the queued
op is the target's storage; when it differs from `storage_id`, the worker
streams the bytes between the two drivers instead of asking one driver to
rename — a whole tree, empty folders included, each file's mtime preserved where
the target can hold one, and every file stat-checked on the far side before a
move deletes anything. A cross-storage move removes the source outright (not to
the trash); a name already taken becomes `name-copy`. A symlink the source
cannot follow is left behind rather than read, a folder link back into the tree
is walked once, and either ends the op **`partial`** with the skipped entries
named in `error` — a **move** then keeps its source. Full behaviour:
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

### `POST /api/files/manager?action=rename` ![user](https://img.shields.io/badge/-user-blue)
```json
{ "path": "alpha://reports", "item": "alpha://reports/old.txt", "name": "new.txt" }
```
Renames one item inside its own folder and answers with the re-rendered
listing. `name` is a leaf: empty, `/`, `\`, `.` and `..` are refused with `400`.

**A rename never replaces what already has the name.** When a file or a folder
already holds it — or the listing still shows a file there whose bytes have
gone missing — the answer is `409 { "code": "NAME_TAKEN", "name": "new.txt" }`
and nothing moves. When the backend cannot say whether the name is free, it is
`503 { "code": "EXISTS_CHECK_FAILED" }`, never a rename on the chance. A rename
that only changes the case (`a.txt` → `A.txt`) is allowed, also on a
case-insensitive disk. Unlike a move, a rename is not given a `-copy` name: the
person chose this one.

⚠ Before this, the rename replaced the file that had the name — not into the
trash — and its catalogue row was dropped with its version history, shares and
comments. A folder renamed onto another folder's name on an object store was
merged into it.

**Finished even if the client leaves.** Once a rename has passed its checks it
runs to the end, whether or not anybody is still waiting for the answer. A
closed tab, or a proxy that stops waiting (nginx after 60 s by default), no
longer stops it half-way. The same holds for the synchronous `?action=move`
and `?action=delete`, `POST /api/files/manager/restore`,
`DELETE /api/admin/trash/{id}`, and `POST /api/ai/move` and `/api/ai/delete`
(with the MCP tools behind them). A client that gave up lists the folder again
to see the result.

⚠ Before this, the request's cancellation stopped the work between two
objects. A folder on an object store, which is changed one object at a time,
was left in two places with the catalogue still describing the old one, and a
retry answered `409` because the half that had arrived held the name.

### `POST /api/files/delete` ![user](https://img.shields.io/badge/-user-blue)
```json
{ "source": ["alpha://a.txt", "alpha://klasor"] }
```
**Response 202** `{ "op": { "id": 13, "kind": "delete", … } }` — queued like a
move; poll `GET /api/files/ops`. Every item goes to the trash.

The items of one delete job are trashed several at a time
(`FILEX_OPS_DELETE_WORKERS`, default 4 — [CONFIGURATION.md](CONFIGURATION.md)),
and the job's `done`/`failed` counters are written about once a second while it
runs. An item that lies inside another item of the same job is left to that one
(it counts as done with it), so a folder goes to the trash whole.

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

### `POST /api/files/save-text` ![user](https://img.shields.io/badge/-user-blue)
Writes the body of the built-in text / code / Markdown editor. Takes a version
snapshot of what it is about to replace, writes the bytes, updates the row and
re-indexes the file.

Which files it saves: those whose extension is a known text or code format
(`.txt`, `.md`, `.conf`, `.json`, `Dockerfile`, `Makefile` …), and — since #56
— an existing file under any other name (`LICENSE`, `NOTICE`, `notes.custom`)
whose catalogue row calls its bytes text (`text/*`, or JSON/XML/YAML). Anything
else answers `415`: a binary file under an unknown name, and a path with no
catalogue row that no known extension vouches for.

⚠ Creating and saving are **different events** and get **different scans**:

| | event | antivirus |
|---|---|---|
| the path has no row yet | `file.uploaded` | scanned **immediately**, like an upload |
| the path already holds a file | `file.updated` | **one** scan scheduled `antivirus.save_scan_window_minutes` out; further saves inside that window join it rather than rescheduling, and it reads the file as it stands *then* |

So a burst of Ctrl+S costs exactly one scan, and the window cannot be pushed
out indefinitely by somebody who keeps typing. The delay is a row in the
operation queue, not a timer in the process, so it survives a restart. See
[PROTECTION.md → Files written in the editor](PROTECTION.md#files-written-in-the-editor).

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
⚠ `mime` is **accepted and ignored here.** The type is re-derived at finalize
from the stored object and the extension, so sending a wrong one is harmless
and sending a right one buys nothing. (The staged endpoint,
`POST /api/files/upload/staged/init`, does honour it.)

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

Server-side zip handling.

⚠ **There is no archive size limit.** This page used to name a
`FILEX_LIMITS_MAX_ARCHIVE_BYTES` "default 1 GiB"; no such variable is read and
`internal/api/handlers/archive.go` performs no size check, so an operator who
believed the cap existed had none. Bound it at the proxy, or with the storage
quota, until the handler grows one.

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
  "members": ["sub/a.txt"]
}
```
`dest` defaults to the archive's own folder. `members` extracts just those
entries; omit it for the whole archive.

⚠ There is no `overwrite` flag. This page used to document `"overwrite": false`
and extraction has always overwritten by name — no handler field, no check —
so a caller who passed it got a 200 and the opposite of what they asked for.
(No endpoint uses `DisallowUnknownFields`, which is why the key vanished
silently.)

Returns `202 + { operation_id: "op_..." }` and runs in background.

### `POST /api/files/archive/add` ![user](https://img.shields.io/badge/-user-blue)
```json
{
  "path": "/storage1/bundle.zip",
  "files": [
    { "source": "/storage1/a.txt", "name": "a.txt" },
    { "source": "/storage1/sub/report.pdf", "name": "docs/report.pdf" }
  ]
}
```
`path` is the archive to write, `files[].source` is what to read and
`files[].name` is where it lands inside the zip. ⚠ The `{paths, dest,
compression}` body this page used to show was never the contract — the handler
requires `path` + `files` and answers `400 missing path or files` for anything
else.
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

### `GET /api/shares` ![user](https://img.shields.io/badge/-user-blue)

The links the **caller** created — the list behind **My shares**, for any
signed-in person, administrator or not. `?limit=&offset=`, and `?active=true`
for live links only. Answers `{"items": [...], "total", "page", "page_size"}`
with the same rows and tenant filter as `GET /api/admin/shares`, minus the
creator's address (on this list it is always the caller), and each row's
`url` built server-side exactly as the share dialog's is — never from the
browser's address. A `root:`-confined token sees only the links inside its
folder.

### `GET /api/shares/{id}/pin` ![user](https://img.shields.io/badge/-user-blue)

One link's PIN, for the person who created it or an administrator — **403**
for anybody else, and for an `app` token (`handlers.RequirePersonalCaller`: a
PIN is a credential, and an app token has no person behind it). An allowed
caller always gets **200**, carrying the PIN or one word saying why there is
none:

- `{"pin": "83459512"}`
- `{"pin": null, "reason": "no_pin"}` — the link has no PIN;
- `{"pin": null, "reason": "not_recoverable"}` — minted before migration
  00049, or while the instance had no key, so only the bcrypt hash was kept;
- `{"pin": null, "reason": "no_secret_key"}` — the instance has no
  `FILEX_SECRET_KEY` to open the sealed copy with.

The PIN is sealed with AES-256-GCM beside the bcrypt hash; the gate still
checks only the hash. Every call writes an audit row, `share.pin_revealed`,
whatever the answer. A link in another tenant, or outside a confined token's
folder, is the **404** an unknown id gets.

### `GET /s/:token` ![public](https://img.shields.io/badge/-public-lightgrey)

The link a stranger opens. **What it answers depends on who is asking** — see
[The public surface](#the-public-surface) for the rule and the escape hatches.
In short: a browser asking for HTML gets the SPA shell; anything else (curl,
wget, a download manager, the PIN form's own POST) gets the bytes or the
server-rendered no-JS page, exactly as before.

`POST /s/:token` is what the no-JS PIN form submits to.

### `POST /api/share/:token/verify` ![public](https://img.shields.io/badge/-public-lightgrey)
```json
{ "pin": "1234" }
```
Returns short-lived `download_token` to be used with `/api/share/:token/download`.

### `GET /api/share/:token/download?dt=…` ![public](https://img.shields.io/badge/-public-lightgrey)
Streams the file. Increments the download counter; rejects if exceeded.

---

## The public surface

Everything a stranger reaches through a filex link — a download share, a file
request, an app plugin's page — is **one surface**: one shell, one PIN gate,
one expiry story, one set of branding. The JSON below is what that shell is
built from; the server-rendered pages behind it are the no-JS fallback.

⚠ Every answer under `/api/public/*` is `Cache-Control: no-store` and
`X-Robots-Tag: noindex, nofollow`, except `/branding` and `/ui-locales/{code}`,
which are the same for every visitor and revalidate (`public, no-cache` with an
ETag, below). Nothing here is authenticated, and nothing here names the
creator, the storage or the file's path.

⚠ The same default holds for the whole API: every `/api` answer is
`Cache-Control: no-store` unless its handler sets a policy of its own
(`api.APINoStore`, PR #41 — a CDN rule that cached everything once served one
administrator's `/api/auth/me` to every visitor). The four answers that say who
the instance is — `/api/public/branding`, `/api/branding`, `/api/appearance`
and `/api/public/ui-locales/{code}` — are the only `public` ones, and a test
walks the route table to keep it that way.

⚠ **The PIN gate.** Five wrong PINs shut the gate for ten minutes, counted on
the share row — so it survives a restart and holds across two instances behind
one address. The *right* PIN during a lock is refused too: a lock the correct
answer lifts is no lock at all. Answering the PIN mints an HttpOnly cookie
(`fxp_<first 16 of sha256(token)>`, an HMAC over the token's hash and an
expiry, 12 h) that carries no PIN and opens only that one link. Before v3 this
lock existed only on app-plugin pages; a PIN on a `/s/` link could be walked
through at the speed of HTTP.

### `GET /api/public/branding` ![public](https://img.shields.io/badge/-public-lightgrey)

Who this instance says it is — the same record the admin **Branding** page
writes.

`Cache-Control: public, no-cache` with a strong **ETag** over the body: a reader
always asks whether its copy is current, and an unchanged answer is a `304`
with no body. ⚠ Not `max-age`: the offered languages ride this answer, and
while it was held for a minute an administrator who installed a language pack
did not see the language until the minute was up (v0.43.0). The tag is the
body’s hash, so it moves for an app installed, removed or upgraded, a theme
saved or a logo changed, with no version stamp for anybody to bump.
`Vary: Accept-Language`, because `locale` is resolved from the visitor’s own
header.

```json
{
  "name": "Acme Files",
  "logo_url": "https://…/logo.svg",
  "accent": "#2f6ceb",
  "footer_text": "Acme Ltd · support@acme.example",
  "hide_powered_by": false,
  "theme": "system",
  "locale": "tr",
  "locales": ["en", "tr"],
  "ui_locales": [{ "code": "es", "source": "plugin", "plugin": "lang-es", "rtl": false },
                 { "code": "ar", "source": "plugin", "plugin": "lang-ar", "rtl": true }]
}
```

`ui_locales` names the languages an installed **language pack** adds — the
codes only, never their strings — and `rtl` says which way a language lays the
interface out ([RTL](RTL.md)). One language's strings are
`GET /api/public/ui-locales/{code}`, fetched when somebody picks it; it is
cached the same way (a strong ETag with `no-cache`), which matters most there,
because a complete catalogue is ~300 KB.

`theme` is `system` | `light` | `dark`
anything else reads as `system`). ⚠ Deliberately **not** the `/api/branding`
payload: the SSO button label is not a stranger's business. (The operator's
custom stylesheet is not on `/api/branding` either any more — it moved to
`GET /api/me/custom-css`, behind auth, precisely so that no anonymous surface
can receive it.)

### `GET /api/public/s/{token}` ![public](https://img.shields.io/badge/-public-lightgrey)

```json
{
  "kind": "file",
  "needs_pin": true,
  "unlocked": false,
  "expired": false,
  "revoked": false,
  "locked": false,
  "expires_at": "2026-10-01T09:00:00Z",
  "visits_left": 3,
  "subject": "rapor.pdf",
  "node": { "name": "rapor.pdf", "size": 51234, "mime": "application/pdf" },
  "app":  { "plugin": "sign", "page": "signer", "title": {"tr": "…"},
            "files": [{ "ref": "pub:0", "name": "sozlesme.pdf", "size": 9 }] }
}
```

| Field | Meaning |
|---|---|
| `kind` | `file` · `folder` · `app` (the link carries an app plugin's page) |
| `needs_pin` / `unlocked` | whether there is a gate, and whether this browser is through it |
| `expired` | the clock ran out. An administrator's **Revoke** moves the expiry to the moment it happened, so it lands here too — there is no second column, and one that could disagree with this would be worse than one word doing both |
| `revoked` | dead for a reason that is **not** the clock: the visit/download ceiling is spent, the file is gone, or the app that answers it was stopped or removed |
| `locked` | the PIN gate is shut after five wrong answers. **Not** a reason the link is dead — it lifts by itself |
| `visits_left` | `max_downloads − download_count`, or `null` when there is no ceiling |
| `node` | present once unlocked, for `file` / `folder` only |
| `app` | present for `kind: "app"`; `files` are the copies the app exposed, once unlocked |

A live link has `expired` and `revoked` both false; "can I use this" is
`expired || revoked`.

**404** for an unknown token and for a token of the other kind (a `/d/` link
asked for here) — the same body for both, so an anonymous caller cannot
separate "no such link" from "somebody else's link". **410** for a link that
existed and is gone, carrying the *same object shape* so a client renders it
from what it already parses.

⚠ For `kind: "app"` the `node` is **omitted**: the file behind an app link is
the anchor its state and its follow-up job hang on, and the only bytes a
visitor may have are the copies the app exposed. No storage driver is opened
for an anonymous request.

### `POST /api/public/s/{token}/pin` ![public](https://img.shields.io/badge/-public-lightgrey)

`{"pin": "1234"}` → **200** with the object above (`unlocked: true`) plus the
unlock cookie · **401** `pin_wrong` · **429** `locked` (audited as
`share.pin_locked`; the audit row carries the token's hash, never the token).

### `POST /api/public/s/{token}/event` ![public](https://img.shields.io/badge/-public-lightgrey)

The app surface. `{state, event, action_id, data}`; an empty body means
`{"event": "open"}`, which counts a visit. Answers `{surface}`, or **202**
`{accepted: true, job_id}` when the surface asks for a job — the job runs as
the **link's creator**, on the link's document, with that user's ACL, and the
visitor never sees the queue row.

**401** `pin_required` until unlocked (checked *before* the app is consulted,
so a locked link never reports anything about the instance behind it) · **404**
when the link is not an app link or the runtime is unavailable · **410** when
the app is stopped or uninstalled, **or when the account that opened the link
is disabled or deleted** — one answer for all of them, so a stranger holding a
token cannot tell which. The same fact turns `revoked` on in
`GET /api/public/s/{token}`, and it is reversible: re-enabling the account
brings its links back untouched.

A job asked for here passes the **same submit-time gate as an authenticated
run** (handlers `enqueueAsCreator`): the action is resolved through the
registry — so one the administrator disabled or reserved to administrators is
refused, with the CREATOR's admin status deciding the reserved case, because
the creator's rights are what the job spends — the creator's ACL on the anchor
is re-read (viewer, editor when the job writes, higher on `min_role`), the
encrypted-folder and read-only refusals apply, and `params` are capped at the
same 64 KiB. **403** `no_access` / `permission_denied` · **409**
`link_unavailable` · **413** `params too large`. ⚠ All three carry ONE
sentence and no detail: which of the reasons it was would tell a stranger
holding a token how this instance is configured.

### `GET /api/public/s/{token}/file/{ref}` ![public](https://img.shields.io/badge/-public-lightgrey)

One copy an app link exposed (`pub:N`), Range-capable, `inline`, `nosniff`,
RFC 6266 filename. Behind the same PIN gate.

### `GET /api/public/d/{token}` ![public](https://img.shields.io/badge/-public-lightgrey)

The file request's state.

```json
{
  "kind": "drop",
  "needs_pin": false,
  "unlocked": true,
  "expired": false,
  "revoked": false,
  "locked": false,
  "folder": "Gelen kutusu",
  "expires_at": "2026-10-01T09:00:00Z",
  "uploads_left": 18,
  "limits": { "max_files": 20, "max_file_size_mb": 100, "allowed_ext": ["pdf"], "ask_name": true }
}
```

⚠ `folder` is the destination's **name**. Its contents are never listed and
never counted — a file request is a blind drop.

### `POST /api/public/d/{token}/pin` ![public](https://img.shields.io/badge/-public-lightgrey)

The same gate, the same cookie and the same statuses as the `/s/` PIN.

### `POST /api/public/d/{token}/upload` ![public](https://img.shields.io/badge/-public-lightgrey)

`multipart/form-data` with `file[]` (and `name` when `ask_name`). The PIN may
be a form field or already answered on this browser (the cookie). Same ingest,
limits, quota accounting and owner notification as the no-JS form — there is
one upload path. **401** `bad_pin` · **429** `locked` / `rate_limited` ·
**410** `expired` · **422** for a limit (`too_many_files`, `file_too_large`,
`ext_not_allowed`).

### Which answer `/s/` and `/d/` give

A `GET` whose `Accept` names `text/html` (or `application/xhtml+xml`) is a
browser navigating: it gets the **SPA shell**. Everything else gets the Go
page or the bytes:

- any other `Accept`, including `*/*` and an absent header — ⚠ this is what
  keeps `curl -O https://host/s/<token>` downloading a file rather than
  collecting an HTML page, and it is why every existing test keeps measuring
  the server-rendered pages;
- `POST` — the no-JS PIN form and the no-JS upload form submit to the same
  address;
- the `X-Filex-Pin` header;
- any of these query parameters: `nojs` (what the shell's own `<noscript>`
  points at), `zip`, `confirmed`, `pin`, `inline`, `download`, `thumb`.

When the frontend is not bundled there is no shell and every visitor gets the
Go pages, so nothing is ever unreachable.

The no-JS body of an **app** link lists the copies the app exposed as plain
links — an app surface cannot run without JavaScript, but a signer on a
locked-down browser can still read the document they were sent. Those links
point at `/api/public/s/{token}/file/{ref}`, behind the same gate.

### Retired: `/p/*` and `/api/p/*`

An app plugin's public page is a share now, so its link is `/s/<token>`.

| Was | Now |
|---|---|
| `GET /p/{token}` | **301** → `/s/{token}` |
| `GET /api/p/{token}` | **301** → `/api/public/s/{token}` |
| `GET /api/p/{token}/view` | **301** → `/api/public/s/{token}` (the opening surface is an ordinary event) |
| `POST /api/p/{token}/pin` · `/event` · `GET /file/{ref}` | **301** → the same leaf under `/api/public/s/{token}` |

⚠ Pages created under the old table (migration 00043) **cannot** be carried
over: it stored only `sha256(token)` while a share stores the token itself, so
there is no way to turn an existing `/p/<token>` into a `/s/<token>`. Those
pages are gone; the table shipped in no release.

---

## Interface preferences

What a person chose about the interface itself — theme, palette, density,
language — one JSON document per person **per surface** (migration 00047).

⚠ In the database and not in `localStorage`, which is per BROWSER and never
per person: a theme picked in one browser was simply not there in the other.
Distinct from `GET /api/files/manager/view-prefs`, which is how each **folder**
was left.

### `GET /api/me/prefs?surface=web` ![user](https://img.shields.io/badge/-user-blue)

`{"surface": "web", "prefs": { … }}`. `surface` is `web` or `desktop` (absent
= `web`); anything else is **400** — an unknown surface is refused rather than
folded into `web`, because a typo that silently wrote a desktop theme into the
browser row would look exactly like the bug this table exists to fix. Nothing
stored yet answers `{"prefs": {}}`, not a 404: it is every account's first day.
`Cache-Control: private, no-store`.

### `PUT /api/me/prefs?surface=web` ![user](https://img.shields.io/badge/-user-blue)

`{"prefs": { … }}` → `{"ok": true, "surface": "web"}`. The document must be a
JSON **object** and is capped at **64 KiB** (**413** past that). Its keys are
the client's; the server validates and bounds it but does not interpret it.

⚠ There is no user id in the path or the body. A caller only ever reaches
their own row, on the surface they named.

### `GET /api/me/custom-css` ![user](https://img.shields.io/badge/-user-blue)

`{"css": "…", "enabled": true}` — the operator's stylesheet (settings key
`ui.custom_css`), already sanitised and already wrapped in its
`@scope (:root) to (.fe-css-immune)` guard, plus the state of the switch
(`ui.custom_css_enabled`, **off** unless set), so a screen can tell "off" from
"on but empty" in one call. `Cache-Control: no-store`.

⚠⚠ **Behind auth, and that is the feature.** It used to ride the public
`GET /api/branding` payload, which the sign-in page fetches before a session
exists — so an operator stylesheet could repaint the sign-in form and every
anonymous share visitor was handed it. An anonymous caller here gets **401**
and cannot even download it. `/api/me/…` rather than `/api/admin/…` because
every signed-in person wears the sheet; only *writing* it is an operator's
privilege, enforced at the settings write (`allowSettingWrite` refuses every
non-`branding.*` key to a confined tenant admin, and both keys are `ui.*`).

Reading fails soft: an installation whose settings cannot be read answers
`{}` — an unstyled panel, which is the same thing an installation with no
custom CSS already sees. See
[INTEGRATION.md → Operator custom CSS](INTEGRATION.md#operator-custom-css).

### `GET /api/appearance` ![public](https://img.shields.io/badge/-public-lightgrey)

The operator's own themes and the instance default:
`{"themes": [{"id": "custom:acme", "name": "…", "light": {…}, "dark": {…}}], "default_theme_id": "custom:acme"}`.
Public, and cached the way `/api/public/branding` is — `no-cache` with an
ETag, so a theme just composed is on the next page load and an unchanged one
costs a `304`. It is public because the sign-in page and
an anonymous share visitor are painted with it before any session exists —
they wear the instance default, never a person's own palette. A signed-in
person's choice is theirs, in [`/api/me/prefs`](#interface-preferences).

They are composed on the **Appearance** screen (`/admin/appearance`) through
`GET /api/admin/themes`, `PUT /api/admin/themes/{key}` (create or replace; the
exported theme document is the body, so an export re-imports as it is) and
`DELETE /api/admin/themes/{key}` — **supertenant only**, because themes have no
tenant column and one tenant's theme would paint every other tenant's users.
The default is the setting `ui.default_theme`; deleting the theme it names
puts it back on the stock palette, and a person still pointing at a deleted
theme simply gets the stock palette too.

---

## Thumbnails

### `GET /api/files/thumb/{id}` ![signed](https://img.shields.io/badge/-signed-yellow)
**Query**
| Param | Type | Notes |
|-------|------|-------|
| `exp` | int  | Unix seconds the stamp expires at |
| `sig` | hex  | HMAC-SHA256 of `"<id>.<exp>"` under `settings.thumb_signing_key` |

One rendered size (there is no `size` parameter). Serves `image/jpeg` with
`Cache-Control: private, max-age=86400`, or **404** when the node's thumbnail
state is not `ready`.

Takes **one of two proofs**, and 401s with neither:

- the stamp above, which the file listing already puts on every `thumb_url` it
  emits — that is what lets a bare `<img src>` render, since it sends no
  `Authorization` header and the `SameSite=Lax` session cookie is not sent by an
  `<img>` in a third-party embed;
- or an authenticated caller (cookie / bearer) who clears the node's tenancy,
  the token's `root:` confinement and an ACL check at **viewer** level. An
  unreachable node answers **404**, a readable-but-not-permitted one **403**.

⚠ The stamp is a capability for one node's preview until `exp`, not an identity.
`FILEX_THUMBS_URL_TTL` (default 24h) bounds it. See [thumbnails.md](thumbnails.md).

⚠ The public folder-share page does not use this route; it serves the same
artefact via `/s/{token}/f/<path>?thumb=1`, scoped to the share token.

---

## Versions

Snapshots of a file's earlier content. Storage layout, retention and the
overwrite guard are in [TRASH-VERSIONING.md](TRASH-VERSIONING.md).

⚠ Every route here takes a raw `node_id`, so every one of them resolves the node
and authorizes it **before** acting: existence → tenancy → `root:` confinement →
RBAC. The first three answer **404** (indistinguishable from a node that does not
exist); the RBAC refusal is **403 `insufficient permission`**.

### `GET /api/files/versions` ![user](https://img.shields.io/badge/-user-blue)
`?node_id=N` — the version timeline. Requires **viewer** on the file: a viewer
can already read it, so its history is not withheld from them.

### `POST /api/files/versions/snapshot` ![user](https://img.shields.io/badge/-user-blue)
`{"node_id": N}` — record the current content as a new version. Requires
**editor**: it writes an object into the node's storage.

### `POST /api/files/versions/restore` ![user](https://img.shields.io/badge/-user-blue)
`{"node_id": N, "version_id": V, "snapshot_current": false}` — copy version `V`
back over the live file. Requires **editor**.

⚠ A destructive write: it goes through the same pre-write guard as every other
write surface, so the bytes it replaces are snapshotted first and a snapshot
that cannot be taken answers **503 `SNAPSHOT_FAILED`** rather than destroying
them. `snapshot_current` only does work when that guard is off
(`FILEX_VERSIONS_ON_OVERWRITE=0`) — otherwise the guard has already taken it and
recording the same bytes twice would spend a retention slot on a duplicate.

### `DELETE /api/admin/versions/{id}` ![admin](https://img.shields.io/badge/-admin-red)
Hard-delete one version row **and** its backing `.versions/…` object.

---

## Realtime (WebSocket)

### `POST /api/files/ws-ticket` ![user](https://img.shields.io/badge/-user-blue)
Mints a short-lived ticket for the socket, and returns the **absolute**
`ws_url` to open. An embedded client that cannot send a cookie uses this;
a same-origin browser can upgrade with its session instead.

### `GET /api/ws` ![user](https://img.shields.io/badge/-user-blue)
The WebSocket. Subscribe to the folders on screen and receive **presence**
frames (who else is here, what they have focused) and **change** frames
(something in this folder moved).

⚠ Two things break integrations if they are not known:

- an explorer with a healthy socket **does not poll** — the 12 s re-listing is
  the fallback for a socket that failed. A reverse proxy that does not pass the
  upgrade on this path turns live updates into a folder that refreshes twice a
  minute, silently;
- the same-origin, cookie-authenticated upgrade is **origin-checked**, so the
  proxy must preserve the real `Host` header.

Frame shapes, the coalescing contract (a burst is merged into one frame per
window, carrying `count`) and the client-side debounce advice are in
[REALTIME.md](REALTIME.md).

---

## Operations (long-running)

Copy / extract / archive create kick off background ops.

### `GET /api/files/ops` ![user](https://img.shields.io/badge/-user-blue)
List the caller's ops, newest first (at most 200). `?status=running` filters.

**Response 200**
```json
{
  "ops": [
    {
      "id": 42, "kind": "move", "storage_id": 3, "dest_storage_id": 4,
      "sources": ["videos/talk.mp4"], "source_count": 1, "source_dir": "videos",
      "dest": "archive",
      "total": 1, "done": 0, "failed": 0,
      "bytes_total": 20983257, "bytes_done": 8388608,
      "status": "running", "created_at": "...", "started_at": "..."
    }
  ]
}
```

- `total` / `done` / `failed` count **sources** — the items that were selected,
  not the files inside them. Moving one large file or one folder is `0` of `1`
  until it ends.
- `bytes_total` / `bytes_done` appear only while a transfer **between two
  storages** runs: the bytes streamed so far, and the total measured by walking
  the sources beside the transfer. `bytes_total` is `0` (omitted) until that
  walk finishes, or when the tree is too large to measure; draw a moving
  indicator then, not a percentage. They are live counters in the worker's
  memory and are gone once the operation ends.
- `sources` is a **preview** in this list: the first 5 paths, with
  `sources_truncated: true` when there were more. `source_count` is the full
  count, and `source_dir` the deepest folder holding every source (omitted at
  the storage root). A queued bulk delete stores every path it was given, so
  without the cut one row could weigh tens of KB and the list megabytes.
  `GET /api/files/ops/:id` returns every source.
- `kind: "trash-empty"` is an administrator's **empty the trash** (`POST
  /api/admin/trash/empty`, [TRASH-VERSIONING](TRASH-VERSIONING.md#trash-endpoints)).
  It names no files: no `sources`, no `dest`; `total` / `done` / `failed`
  count trashed rows, and `bytes_total` / `bytes_done` the bytes their files
  hold and the bytes freed so far. It is its tenant's — listed, read and
  cancelled by the tenant that asked for it (a supertenant sees every one) —
  and it runs beside the queue, never in the worker's line.

### `GET /api/files/ops/:id` ![user](https://img.shields.io/badge/-user-blue)
Single op detail with **every** source (no `source_count` / `source_dir`),
plus `error` when it failed.

`status` is one of `pending | running | ok | failed | partial | cancelled` —
`partial` when some sources failed and others did not, `cancelled` when
somebody stopped it.

### `POST /api/files/ops/:id/cancel` ![user](https://img.shields.io/badge/-user-blue)
Stops an op: a pending one never runs, a running one stops at its next item
(an item already under way is finished). `200` with the op; `409` when it has
already ended; `403` for somebody else's op unless the caller is an
administrator; `404` for an op the caller cannot see.

---

## Admin: storages

### `GET /api/admin/storage-drivers` ![admin](https://img.shields.io/badge/-admin-red)
The config contract every registered storage driver declares: its fields, their
type, which one is the storage root, which hold credentials, defaults and an
i18n key per label. Admin UIs render their storage forms from this instead of
hardcoding a field list, and the root‑path guard reads the same declaration.
`scan_fields` are the settings every storage has whatever its driver (today
`scan_exclude`); the storage form draws them, the replication‑target dialog
does not.
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
route answers `503 plugins_disabled`. ⚠ The two are checked in that order: a
tenant admin is refused *before* being told whether the operator has plugins
switched on. Full picture: [PLUGINS.md](PLUGINS.md).

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
**Response 200** — an array, in the administrator's order (placed storages by
`sort_order`, then the rest in creation order):
```json
[
  { "id": 1, "uid": "7f3a1b2c-…", "name": "Local", "driver": "local",
    "read_only": false, "sort_order": 1, "config": { "path": "/var/lib/filex/local-storage" },
    "stats": { "file_count": 12, "total_size_bytes": 4200000 }, "last_sync_state": "ok" }
]
```

### `PUT /api/admin/storages/order` ![admin](https://img.shields.io/badge/-admin-red)
The administrator's order — the one everybody's navigation panel starts from
([STORAGE.md → Ordering storages](STORAGE.md#ordering-storages)).
**Request** `{ "ids": [3, 1, 2] }` — the whole order, first = top. The listed
storages get `sort_order` 1…n and every other storage the caller administers
goes back to `null` (not placed: after the placed ones, in creation order).
`{ "ids": [] }` resets to creation order.
**Response 200** `{ "ok": true, "ids": [3, 1, 2] }`. **400** for bad JSON, a
duplicate id, or an id the caller cannot administer (another tenant's reads
exactly like one that does not exist); nothing is written then.

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

`config.scan_exclude` — every driver — holds the storage's
[scan exclusions](STORAGE.md#scan-exclusions): glob patterns, one per line (a
JSON array of strings is accepted too). A pattern that would exclude
everything, a `!`, a `..`, a broken glob or too many or too long patterns
answers **400** `{"error": "SCAN_EXCLUDE_INVALID", "message": "…"}`, and an
empty or `/` root **400** `{"error": "ROOT_PATH_FORBIDDEN", "message": "…"}` —
the `message` names the pattern and is in the reader's language (server
catalogue, `server.storage.*`). The same on `PATCH`.

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

### `PATCH /api/admin/storages/:id` ![admin](https://img.shields.io/badge/-admin-red)
Same body shape; partial updates allowed. A plugin storage is **re-probed on
every change** — the operator may have just pointed it at a different bucket,
and a configuration that half works fails the same way a half-working plugin
does: in the user's hands, looking like filex.

### `DELETE /api/admin/storages/:id` ![admin](https://img.shields.io/badge/-admin-red)
Removes the storage and its DB cache rows. Files in the underlying backend
are **not** deleted.

### `POST /api/admin/storages/:id/sync` ![admin](https://img.shields.io/badge/-admin-red)
Triggers an immediate **full** scan of the storage. The scan runs in the
background, so the answer comes at once:

```json
{ "ok": true, "status": "started", "note": "the sync runs in the background; watch its progress under sync runs" }
```

`status: "running"` (still `202`) means a scan was already walking this storage
and no second one was started. There is no run id in the answer: watch the run
under `GET /api/admin/storages/:id/sync-runs` or `GET /api/admin/sync-runs`.

**`?path=<folder>` rescans one catalogued folder** instead of the whole storage
— its subtree only, with the same rules as a full scan: new objects are
catalogued, changed ones updated, a staged upload whose bytes landed is settled,
and objects gone from inside the folder go to the trash, with the 70 % guard
comparing what the listing saw against the folder's **own** size. A listing that
failed part-way removes nothing. No sync-run row is written and the storage's
`last_sync_at` does not move. It answers when it is done (at most ten minutes):

```json
{ "ok": true, "path": "/Müşteri/2026", "scanned": 412, "added": 3, "updated": 1, "removed": 2, "reconciled": 0 }
```

`reconciled` counts staged uploads settled as stored; `removal_skipped`, when
present, says why nothing was removed (partial listing, or the guard tripped).

| Answer | When |
|---|---|
| `200` | done — the counts above |
| `202` `{status:"running"}` | a scan is already walking the storage; nothing was started |
| `400` | the path climbs out with `..`, names filex's own trees (`.versions/`, `.thumbs/`, `.filex-trash/`), is excluded from scanning on this storage ([scan exclusions](STORAGE.md#scan-exclusions)), or is a file |
| `404` | the storage is unknown, or the folder is not in the catalogue (rescan its parent, or run a full scan) |
| `504` | ten minutes were not enough: the counts so far, the rows reached are updated, nothing was removed |

`?path=` empty, `/`, or anything that cleans to the root is the full scan above.

### `POST /api/admin/storages/test` ![admin](https://img.shields.io/badge/-admin-red)
Validates a connection without persisting. ⚠ The candidate configuration is in
the body — there is no `:id` in this path, because the usual caller is the
create form, which has no storage to name yet.

---

## App plugins

The sandboxed WebAssembly apps of [APP-PLUGINS.md](APP-PLUGINS.md). The admin
routes are listed there; these are the ones the explorer and the public page
call. All under the user block unless marked public. Every answer when the
runtime is off: `404 app_plugins_disabled`.

### `GET /api/files/plugins/actions` ![user](https://img.shields.io/badge/-user-blue)

The rows this caller may see: `{actions: [{plugin, id, key, label, icon,
applies, view, view_placement, confirm, min_role, danger, output_mode}],
views: [{plugin, id, placement, label, icon, applies}]}`. `key` is
`plugin:<plugin>/<action>`; `applies` is the manifest rule merged with the
admin override; admin-only actions are absent for non-administrators, and
`hidden` actions are absent for everyone (a surface starts those). The
explorer mirrors `applies` client-side — including `state` / `no_state`,
which it matches against the row's `app_state` — and the server re-checks on
run. `view_placement` is `modal` (dialog) or `page` (the view opens as a full
page in a new tab).

### `POST /api/files/plugins/actions/{plugin}/{action}/run` ![user](https://img.shields.io/badge/-user-blue)

`{"paths": ["docs://reports/nda.pdf"], "params": {…}}` (adapter-qualified,
one storage; `{"storage_id", "paths"}` is accepted too). Checks, in order:
storage ownership, ACL ≥ viewer per path (≥ editor when the action writes,
`min_role` raises it; a file locked by THIS app is judged at the level the
caller would have without the lock), read-only storage → `409 read_only`,
encrypted folder → `403 encrypted`, the applies rule — state keys included —
against the real files → `422 not_applicable`. A hidden action answers `404`
here; only a surface may queue it. Answers `202 {op, job_id}` (queued; the `op` is an ops row
with `kind: "plugin-action"`, `plugin`, `action`, `label`) or, when the action
opens a screen and no `params` were sent, `200 {surface}`.

### `GET /api/files/plugins/views/{plugin}/{view}?path=…` · `POST …/event` ![user](https://img.shields.io/badge/-user-blue)

The opening surface, then events: `{"paths", "state", "event": "change" |
"submit" | "action", "action_id", "data": {"values", "row_id"}}` → `200
{surface}`, or `202 {op, job_id}` when the surface asked for a job (same
checks as run). A queued job may carry `output: {mode, name}`, the screen's
"new version / new file beside it / this name" choice, which replaces the
action's manifest output for that job; the required ACL level is computed
from the effective mode.

### File locks ![user](https://img.shields.io/badge/-user-blue)

An app holding `files:lock` may freeze one file (`file_lock` host function).
While a lock is live every caller's effective level on that path is capped at
viewer — administrators included — so listings carry `locked: true` and
`lock: {plugin, reason?, until?}` with `perm: "viewer"`, and renaming, moving
or deleting the file, or any folder above it, answers `423 {"error":
"locked", "plugin", "path", "reason", "until"}`. An upload that would overwrite the locked file is refused the same
way; uploads of OTHER names into the same folder and every sibling are
unaffected, and the locking app's own jobs still write.
It holds at **every** door, not only the explorer's: the document editor's
save (a document opened before the freeze and saved after it is refused),
WebDAV (423 Locked), SFTP, FTPS and NFS (their permission error — also for
a session that was already open when the freeze was taken), the S3 gateway
(AccessDenied), the AI/MCP write tools, archive extraction (a member that
would land on the file is skipped and counted as `locked`), trash and
version restores onto the path, and another app's output. One check does it
for all of them (`backend/internal/writegate`), the same one that refuses
filex's own folder names.
Administrators list and lift locks at `GET /api/admin/app-plugins/locks` and
`DELETE /api/admin/app-plugins/locks {storage_id, path}` (audited as
`app_plugin.unlock`).

### `GET /api/files/plugins/users?plugin=<name>&q=` ![user](https://img.shields.io/badge/-user-blue)

The people-picker's search: `{users: [{user_id, email, name}]}`, tenant-scoped,
at most 20, empty `q` lists nobody; `403 permission_denied` unless the plugin
is running and holds `users:lookup`.

### `POST /api/files/ops/{id}/cancel` ![user](https://img.shields.io/badge/-user-blue)

Ends a pending or running op the caller may see (their own, or any as an
administrator): `200 {op}` with status `cancelled`; `409` when it already
finished. Applies to every op kind, not only plugin jobs.

### Ops rows for plugin jobs

`GET /api/files/ops` rows with `kind: "plugin-action"` carry `plugin`,
`action`, `label` (in the caller's locale), `message` (the last progress
message or the app's result) and, once committed, `outputs: [{path}]`
(adapter-qualified). Progress rides on `bytes_done` / `bytes_total`.
Statuses: `pending | running | ok | failed | cancelled`.

### An app's public page IS a share

`share_create` opens a **real share**, so the visitor's link is `/s/<token>`
like every other public link — one revoke list, one expiry policy, one PIN
implementation, one visit counter. The JSON a visitor's browser talks to is
[The public surface](#the-public-surface); `/api/p/*` and `/p/*` are retired
and **301** there.

The share row carries `plugin_id`, `page_id`, `subject` and the app's own
`state_json` + `files_json` (migration 00046). `share_revoke` is the share's
revoke; `share_state` reads and replaces the app's record for one link.

A share points at a node, and a job's **output** has none until `runJob`
commits it — after the plugin has returned. So a `share_create` naming one of
this job's outputs is *promised*: the token, PIN and expiry are decided and
answered at once (`share.NewToken` + `CreateOpts.Token`), and the row is
written by `keepPromisedShares` when the bytes are committed and the node
resolved. A job that fails, or that never keeps the output, writes no row —
the link is created late rather than bound late, so there is never a share
pointing at nothing (`wasmplugin/public_promised.go` says why at length).

### `GET /api/admin/app-plugins/shares` ![admin](https://img.shields.io/badge/-admin-red)

The links apps opened — `?plugin=<name>` for one app's table, `?active=true`,
`?limit=`, `?offset=`. Same rows, same envelope and same tenant filter as
`GET /api/admin/shares`, which carries `plugin_name` and `share.page_id` on
every row so the Shares table can show a plugin/page column without a lookup
per row. An app that is not installed answers an empty page, never a 404, so a
panel polling one app keeps working through an uninstall.

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
`password` is optional. An account created without one has no local password:
every password check refuses it (login form, recovery login, `/dav`, SFTP, FTP)
and it signs in through SSO, where the account is matched by email, or with an
API token. `POST /api/admin/users/{id}/reset-password` gives it one later; the
answer carries the value once, as `new_password`.

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

### `GET /api/files/quota/storages` ![user](https://img.shields.io/badge/-user-blue)

"How full is this drive", for somebody who is not an administrator — the same
per-storage total `/api/admin/storages` carries in `stats.total_size_bytes`,
for the storages the caller is allowed to see.

```json
{ "storages": [ { "name": "team", "used_bytes": 85022, "file_count": 31 } ] }
```

⚠ It is **not** `/api/files/quota/me` under another name. That one is a
per-USER sum across every storage (`SUM(nodes.size) WHERE owner_id = me`);
printing it under one drive's name would be a number about the person wearing
a label about the drive. This is a property of the drive, and every reader of
the same drive gets the same figure.

The filter is the one the explorer's own root listing applies: list the enabled
storages, drop every one whose RBAC grant set is not `StorageVisible`. **A
storage the caller cannot open is not reported at all** — not its size, not its
name, not a zero row. Tenancy is closed before that (the handler reads the
tenant-scoped store), and a **root-confined** caller — an API token carrying
`root:<adapter>://<rel>`, or a trusted proxy's `X-Filex-Root` — is looking at a
folder rather than a drive, so it is answered with the storage only when the
confinement is the storage root, and with an empty list otherwise.

Cost: one `COUNT(*) + SUM(size)` aggregate per visible storage, memoised
process-wide for 15 s and keyed by storage id, so a page that shows every drive
costs one pass per drive per quarter-minute no matter how many people have it
open. A storage whose count fails is omitted rather than reported as `0`; the
caller's card falls back to naming the kind of thing. Its readers: Home's drive
cards, the explorer's storage line for a person without a quota (only for
drives the host sent no size for), and the admin top bar's storage chip, which
polls it every 60 s — all inside that one cache.

---

## Admin: external services

⚠ **Instance-wide, and in multi-tenant mode supertenant-only.** There is one document server, one converter, and one shared JWT secret behind them, so this surface decides where every tenant's documents are sent and what credential signs the handoff. A
tenant admin gets `403 supertenant_only` on **every** verb here, reads
included. Single-tenant installs are unaffected — the ordinary admin still
administers everything. See
[MULTI-TENANCY.md](MULTI-TENANCY.md#instance-wide-admin-surfaces).

### `GET /api/admin/external` ![admin](https://img.shields.io/badge/-admin-red)
**Response 200**
```json
{
  "entries": [
    { "Name": "onlyoffice", "Enabled": true, "URL": "https://docs.example.com",
      "SecretEnc": "***", "OptionsJSON": "{}",
      "LastCheck": "2026-09-06T08:28:32Z", "LastState": "ok",
      "env_managed": false },
    { "Name": "drawio", "Enabled": false, "URL": "", "SecretEnc": "",
      "OptionsJSON": "{}", "LastCheck": null, "LastState": "unconfigured",
      "env_managed": true }
  ]
}
```

`LastState` is one of `ok | unreachable | disabled | unconfigured | unknown`.
Secrets are never returned — a configured one reads `"***"`.

`env_managed` says the service is pinned by env/`config.yaml`. Its row is
re-asserted from there at every boot, so a `PATCH` to it applies immediately but
does not survive a restart.

### `PATCH /api/admin/external/:name` ![admin](https://img.shields.io/badge/-admin-red)
```json
{ "enabled": true, "url": "https://docs.example.com", "secret": "…",
  "options_json": "{}" }
```
Every field is optional; an omitted one keeps its stored value.

⚠ A `secret` of exactly `"***"` is **ignored**, because that is what `GET`
returns in place of a stored secret and a UI that re-sends what it was shown
would otherwise overwrite the real one.

**Response 200** — `{ "ok": true, "env_managed": false }`, plus a `note` when
`env_managed` is true.

The change is live: the running process reads this row on every use, so the
editor, the diagram embed and the converter pick it up on the next request. No
restart.

### `POST /api/admin/external/:name/test` ![admin](https://img.shields.io/badge/-admin-red)
Probes the service's health endpoint — `${url}/healthcheck` for `onlyoffice`,
`${url}/healthz` for `convert`, the bare URL for `drawio` — with a 3 s timeout,
stores the verdict on the row and returns
`200 + { ok, name, reachable, url, state, detail }`. An unknown name is `404`.

`detail` is what the probe saw when the answer was not a healthy one, and is
empty otherwise: `GET <url>/healthcheck returned HTTP 502`, `… no answer
within 3s`, or the connection error (`… connection refused`, a DNS or TLS
failure). For `onlyoffice`, a 502/503/504 adds that the Document Server's web
server answered but its docservice did not — the network is fine and the fix
is inside that container.

⚠ Reachable is not the same as configured: OnlyOffice also needs a JWT secret,
and a Document Server with no secret set in filex answers this probe happily
while refusing every editor session.

---

## Admin: protection & antivirus

⚠ **Instance-wide, and in multi-tenant mode supertenant-only.** Every value here is a single global row: switching antivirus off, or pointing clamd elsewhere, does it for the whole instance. The read is gated too — it returns the clamd address in force and a live reachability probe. A
tenant admin gets `403 supertenant_only` on **every** verb here, reads
included. Single-tenant installs are unaffected — the ordinary admin still
administers everything. See
[MULTI-TENANCY.md](MULTI-TENANCY.md#instance-wide-admin-surfaces).

### `GET /api/admin/protection` ![admin](https://img.shields.io/badge/-admin-red)
Returns the trash-retention window, the version keep count, the share-link life
ceiling and the whole `antivirus` block — the switch, the mode, the clamd
address, the size ceiling, the editor save-scan window — plus a **status**
sub-object describing what this process is actually doing: what would answer
(`clamscan` / `clamdscan` / `clamd`), whether it is `reachable`, its version,
and `restart_pending`.

⚠ `restart_pending` is true for as long as the stored configuration differs
from what the running process booted with. The switch, the mode and the address
take effect **at the next restart, in both directions**; the size ceiling and
the save window apply to the next file scanned.

### `PATCH /api/admin/protection` ![admin](https://img.shields.io/badge/-admin-red)
Partial update; echoes the fresh `GET` shape. Values are validated on save
rather than clamped later — an out-of-range number or an address like
`clamav 3310` is a `400`, and `daemon` mode with no address is refused as well.

⚠ The scanner **binary** is deliberately not settable here: it is a path this
server executes, so an admin-writable field would turn an admin account into
arbitrary command execution. It stays in `FILEX_CLAMAV_BIN`. Full semantics:
[PROTECTION.md](PROTECTION.md).

---

## Admin: webhooks

Five routes under `/api/admin/webhooks` manage **webhook v2 targets** — rows,
each with its own URL, its own signing secret and its own per-event allow-list.
`GET` masks the secret to a `secret_set` boolean and never returns the value.
One event produces one POST **per matching destination**.

| Route | Purpose |
|---|---|
| `GET /api/admin/webhooks` | List targets (secrets masked) plus each one's last delivery. |
| `POST /api/admin/webhooks` | Create: `name`, `url`, optional `secret`, optional `events` allow-list, `enabled`. |
| `PATCH /api/admin/webhooks/:id` | Partial update. |
| `DELETE /api/admin/webhooks/:id` | Remove the target. |
| `POST /api/admin/webhooks/:id/test` | Send a test delivery. |

⚠ `/api/admin/notify` governs the **legacy single** webhook
(`FILEX_WEBHOOK_URL`); these govern the v2 targets. The event catalogue —
including `file.updated`, which from v0.34.0 replaces `file.uploaded` for a
write that overwrote an existing file — is in
[NOTIFICATIONS.md](NOTIFICATIONS.md).

---

## Admin: sync runs

### `GET /api/admin/sync-runs` ![admin](https://img.shields.io/badge/-admin-red)
**Query**: `?storage_id=…&status=…&limit=50&offset=0` — runs of the last five
days, newest first. A tenant admin sees its own storages' runs only.

**Response 200**
```json
{
  "entries": [
    {
      "id": 12, "storage_id": 1, "status": "ok",
      "started_at": "...", "finished_at": "...",
      "seen_count": 1840, "added": 4, "updated": 2, "deleted": 1
    }
  ],
  "total": 1, "limit": 50, "offset": 0
}
```

`status` is one of:

| Status | Meaning |
|---|---|
| `running` | the run is walking the storage now (`finished_at` is absent) |
| `ok` | the run finished |
| `failed` | the run stopped on an error of its own — a backend that did not answer, a listing that failed; `error` says which |
| `aborted` | the run was cut short: its context was cancelled (shutdown, a storage edit restarting the syncer) or ran past its time limit, or the server stopped in the middle of it and closed the row when it next started (`error`: `interrupted: the server stopped during the scan`). The catalogue is behind the backend until a run finishes |

`seen_count` of the last run that finished `ok` is the tombstone guard's
baseline ([STORAGE.md → Sync](STORAGE.md#sync)). The same rows are listed per
storage at `GET /api/admin/storages/:id/sync-runs` (`entries`, `total`).

### `GET /api/admin/sync-runs/:id` ![admin](https://img.shields.io/badge/-admin-red)
`{run, conflicts}`: the run above plus the sync conflicts detected during it.

---

## Admin: audit log

### `GET /api/admin/audit` ![admin](https://img.shields.io/badge/-admin-red)
**Query**: `?user_id=&action=&from=&to=&limit=100`

**Response 200**
```json
{
  "entries": [
    {
      "entry": {
        "id": 9001,
        "user_id": 1,
        "action": "share.create",
        "target_type": "share",
        "target_id": "42",
        "metadata": { "ttl": "168h", "max_downloads": 10 },
        "ip": "1.2.3.4",
        "created_at": "2026-09-05T10:11:12Z"
      },
      "user_email": "admin@local",
      "user_name": "admin"
    }
  ],
  "total": 1,
  "limit": 100,
  "offset": 0
}
```

⚠ On a **demo** instance (`FILEX_DEMO_MODE`) `ip` comes back as
`hidden on the demo`. The page itself stays readable — it is one of the
operator surfaces a demo exists to show — but the addresses in it belong to the
other visitors, and on a public demo every visitor can read it. The same
masking applies to the `recent_activity` block of `GET /api/admin/dashboard`,
which carries the same rows. An ordinary install is untouched; see
[DEMO.md](DEMO.md).

⚠ Both the envelope and the action list on this page used to be invented. The
key is `entries` (not `events`), each row wraps the entry under `entry` with
`user_email` and `user_name` beside it, and the fields are `target_type` / `target_id` /
`metadata` / `created_at` — not `resource` / `meta` / `ts` / `ua`.

`user_name` is the person as every screen names them — display name, else
username, else email (`model.PersonLabel`, the Owner column's rule). The admin
Shares list carries the same for a link's creator as `creator_name`, the
dashboard's `recent_activity` rows as `user_name`, and permission grants as
`user_name`.

`action` values are produced by exactly one place,
`internal/auth/audit_middleware.go`, and this is the whole set:

`auth_provider.test` · `auth_provider.update` · `external.test` ·
`external.update` · `file.archive_add` · `file.archive_extract` ·
`file.delete` · `file.restore` · `file.star` · `file.tags_set` ·
`file.upload` · `file.upload_abort` · `profile.password_change` ·
`profile.update` · `search.rebuild` · `settings.update` · `share.create` ·
`share.delete` · `share.revoke` · `sharex.upload` · `storage.create` ·
`storage.delete` · `storage.sync_trigger` · `storage.test` ·
`storage.update` · `sync.action` · `totp.disable` · `totp.enroll` ·
`totp.verify` · `trash.empty` · `user.create` · `user.delete` ·
`user.password_reset` · `user.quota_recompute` · `user.quota_set` ·
`user.update` · `version.delete` · `version.restore` — plus AI-admin calls,
which carry the same names under an `ai.` prefix.

⚠ Filtering by `?action=` is an exact match, so the eight values this page
used to list and no code ever writes (`auth.login`, `auth.logout`,
`auth.failed`, `file.move`, `file.copy`, `storage.add`, `user.disable`,
`admin.config_change`) returned an empty page forever. Note in particular
`storage.create`, **not** `storage.add`. Sign-in and sign-out are **not
audited** at all today; do not build an alert on them.

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
