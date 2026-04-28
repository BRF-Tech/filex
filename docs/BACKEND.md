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
    "role": "admin", "groups": ["filex-admin"]
  }
}
```

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
  }
}
```
Cached client-side for 1h.

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

### `GET /api/files/raw?path=…` ![user](https://img.shields.io/badge/-user-blue)
Stream the raw file bytes. Sends `Content-Type`, `Content-Length`, and
honours `Range:` for partial GETs (video / audio scrub).

### `POST /api/files/move` ![user](https://img.shields.io/badge/-user-blue)
**Request**
```json
{ "from": "/storage1/a.txt", "to": "/storage1/sub/a.txt", "overwrite": false }
```
**Response 200** `{ "ok": true }`

### `POST /api/files/copy` ![user](https://img.shields.io/badge/-user-blue)
Same shape as move; may return `202 + operation_id` for large copies, see
[operations](#operations-long-running).

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

### `GET /api/files/search?q=…` ![user](https://img.shields.io/badge/-user-blue)
Bleve full-text + metadata search. Same response shape as `/api/files/manager`
but with `path` echoing the matching entry's full path.

---

## Uploads (multipart)

For files >5 MB. Smaller files can use `POST /api/files/upload` (single-shot
`multipart/form-data`).

### `POST /api/files/upload/init` ![user](https://img.shields.io/badge/-user-blue)
**Request**
```json
{
  "path": "/storage1/big.iso",
  "size": 5368709120,
  "mime": "application/octet-stream",
  "chunk_size": 16777216
}
```
**Response 200**
```json
{
  "upload_id": "u_AbCdEf",
  "presigned_urls": [
    { "part": 1, "url": "https://s3.example.com/...&X-Amz-Sig=...", "expires_at": "..." },
    { "part": 2, "url": "https://s3.example.com/...&X-Amz-Sig=...", "expires_at": "..." },
    "..."
  ],
  "expires_at": "2026-04-29T00:00:00Z"
}
```
Browser PUTs each chunk directly to the URL. For drivers that don't support
presigned multipart (sftp, webdav), `presigned_urls` is null and the client
posts each chunk to `POST /api/files/upload/chunk?upload_id=…&part=…`.

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
  "max_downloads": 10
}
```

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

### `PUT /api/admin/storages/:id` ![admin](https://img.shields.io/badge/-admin-red)
Same body shape; partial updates allowed.

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
List users. `?q=…` for search, `?role=admin|user` filter.

### `POST /api/admin/users` ![admin](https://img.shields.io/badge/-admin-red)
```json
{
  "email": "newuser@example.com",
  "username": "newuser",
  "role": "user",
  "password": "..."
}
```
For OIDC users, omit password.

### `PUT /api/admin/users/:id` ![admin](https://img.shields.io/badge/-admin-red)
Partial update (role, disabled, password reset).

### `DELETE /api/admin/users/:id` ![admin](https://img.shields.io/badge/-admin-red)

### `POST /api/admin/users/:id/disable` ![admin](https://img.shields.io/badge/-admin-red)
### `POST /api/admin/users/:id/enable` ![admin](https://img.shields.io/badge/-admin-red)

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
