# Backend HTTP API

Base URL: `${FILEX_PUBLIC_URL}` (default `http://localhost:5212`).

All endpoints under `/api/*` return JSON. All write endpoints expect
`Content-Type: application/json` unless explicitly noted.

**OpenAPI.** The routes under `/api/files` and `/api/ai` are described in
[`backend/internal/api/openapi.json`](../backend/internal/api/openapi.json)
(OpenAPI 3.1): every operation, its parameters, the request bodies and the
answers of the main ones, the token verb or scope each needs. A test holds it
to the router in both directions - no operation the server does not serve, no
`/api/files` or `/api/ai` route left out - so it is safe to generate a client
from. The admin API is not in it; this page is its reference, and the
routes of the areas under
[Admin: routes described on their own page](#admin-routes-described-on-their-own-page)
are described on that area's page.

- [Auth & sessions](#auth--sessions)
- [Capabilities](#capabilities)
- [File browsing](#file-browsing)
- [Encryption policy](#encryption-policy)
- [Drafts](#drafts)
- [Uploads (multipart)](#uploads-multipart)
- [Archives](#archives)
- [Sharing](#sharing)
- [The public surface](#the-public-surface)
- [Interface preferences](#interface-preferences)
- [Thumbnails](#thumbnails)
- [Versions](#versions)
- [Comments](#comments)
- [Realtime (WebSocket)](#realtime-websocket)
- [Operations (long-running)](#operations-long-running)
- [Admin: storages](#admin-storages)
- [Admin: plugins](#admin-plugins)
- [Admin: plugin requests](#admin-plugin-requests)
- [Admin: encryption policy](#admin-encryption-policy)
- [App plugins](#app-plugins)
- [Admin: users](#admin-users)
- [Admin: roles and permissions](#admin-roles-and-permissions)
- [Admin: groups](#admin-groups)
- [Admin: tenants](#admin-tenants)
- [Admin: routes described on their own page](#admin-routes-described-on-their-own-page)
- [Admin: quota](#admin-quota)
- [Admin: external services](#admin-external-services)
- [Admin: protection & antivirus](#admin-protection--antivirus)
- [Admin: sign-in security](#admin-sign-in-security)
- [Admin: webhooks](#admin-webhooks)
- [Admin: sync runs](#admin-sync-runs)
- [Admin: audit log](#admin-audit-log)

### Auth markers

| Symbol | Meaning |
|--------|---------|
| ![public](https://img.shields.io/badge/-public-lightgrey) | No auth |
| ![user](https://img.shields.io/badge/-user-blue)         | Any authenticated user |
| ![admin](https://img.shields.io/badge/-admin-red)         | Admin role required - or, where the section says so, the delegated administration permission it names (`admin.users`, `admin.monitor`, `admin.audit`, …; [PERMISSIONS.md](PERMISSIONS.md#delegated-administration)). When an API token calls, the token must also carry the `admin` scope and no `root:` confinement (since v0.41.0): an administrator's token without it gets `403 token missing scope: admin` |
| ![signed](https://img.shields.io/badge/-signed-yellow)    | A session/token **or** a signed URL - see the route |
| ![session](https://img.shields.io/badge/-admin%20session-darkred) | An administrator **signed in to the panel**: an API key is refused `403 session_required`, whatever its scopes ([Admin: plugin requests](#admin-plugin-requests), [Admin: encryption policy](#admin-encryption-policy)) |

Auth is provided either by a session cookie (`filex_session`) or a Bearer
token (`Authorization: Bearer <jwt>`). Both are accepted on the same routes.
An API token (`X-Filex-Token: <token>` or `Authorization: Bearer <token>`) is
accepted there too, limited by its verbs - `read`, `write`, `delete` - on every
route: see [RBAC.md → API tokens](RBAC.md#api-tokens-verbs-on-every-surface).
Every caller is also held to the account's permissions
([PERMISSIONS.md](PERMISSIONS.md)); a refusal from that layer is
`403 {"error": "permission_denied", "permission", "source", "message"}`.

A change (any method but `GET`, `HEAD`, `OPTIONS`, and a WebSocket upgrade) that
a browser sends from another origin with only the session cookie is refused
before any route runs: `403 {"error": "cross_origin_refused", "message"}`. A
Bearer or `X-Filex-Token` header, and a client that is not a browser, are not
affected; another origin is trusted only when it is in
`FILEX_CORS_ALLOWED_ORIGINS` ([CONFIGURATION.md → Requests from other origins](CONFIGURATION.md#requests-from-other-origins)).

---

## Auth & sessions

### `POST /api/auth/login` ![public](https://img.shields.io/badge/-public-lightgrey)
Password login. Every running password provider is asked in turn: the local
accounts, and LDAP, Windows and Linux (PAM) when they are switched on (Admin →
Identity providers). `email` is what the person typed - an e-mail address or a
username, and with a Windows provider also `.\alex` or `CORP\alex`.

**Request**
```json
{ "email": "admin@local", "password": "<printed once>" }
```
**Response 200**
```json
{
  "user": { "id": 1, "email": "admin@local", "username": "admin", "role": "admin" },
  "token": "eyJhbGc..."
}
```
The session cookie is set by the same response. The Bearer token is for SPA
embeds that prefer header auth.

**Status codes:** `200` ok · `401` invalid creds · `403` the account is
disabled (`"disabled": true`), or its tenant may not sign in now - suspended,
or maintenance mode (`"maintenance": true`) · `403 cross_origin_refused`
the form was posted from another origin (sign-in forgery; a script that sends
no `Origin`, `Sec-Fetch-Site` or `Referer` is not affected) · `429` too
many failed attempts (see below) · `503` busy (see below).

**Response 401** - the machine-readable `error` is unchanged; what a person needs
is added beside it, worded in their language (`Accept-Language`):
```json
{
  "error": "invalid credentials",
  "message": "Wrong credentials. 3 attempts left; the account is locked at failed attempt 5.",
  "remaining": 3, "limit": 5, "scope": "account"
}
```
`scope` is `account` or `ip` - whichever counter has fewer tries left. The body
is the same for an account that exists and one that does not (the counter is keyed
by the identifier typed). On a public demo the shared account has no account
counter, so its replies are always `scope: "ip"` - the address's tries - and never
promise an account lock ([CONFIGURATION.md → Demo mode](CONFIGURATION.md#demo-mode)). A wrong second-factor code answers the same way with
`"error": "invalid two-factor code", "totp_required": true`; a *missing* code does
not count. See [Sign-in attempt limits](CONFIGURATION.md#sign-in-attempt-limits).

**Response 429** - the attempt that trips the limit, and every attempt while the
lock lasts (even with the right password), with a `Retry-After` header:
```json
{ "error": "too many attempts", "message": "Too many failed attempts: this account is locked. Try again in 1 minute.",
  "locked": true, "scope": "account", "retry_after": 60 }
```

**Response 503** `{"error": "busy", "message": "…"}` with `Retry-After: 3` -
the sign-in provider is at its limit of simultaneous sign-ins (a Linux PAM
provider's `max_concurrent`). It is not a judgement of the password and is not
counted as a wrong attempt.

**`realm`** (multi-tenant installs only; a single-tenant install ignores it) -
the tenant realm the person typed in the form's Realm field
([MULTI-TENANCY.md → Realms](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)):
```json
{ "email": "alex", "password": "…", "realm": "acme" }
```
Empty (or absent) is the tenant the request's host names, else the platform's
own. The account lookup never leaves the tenant, and the account signed in to is
checked against it once more. A realm nobody has, and a realm that is not the
host's tenant, answer exactly like a wrong password (`401`, the same body,
counted). The attempt limit counts `<realm>/<name>`.

**Response 200, handed to the tenant's address** - when the realm names a tenant
that has a `host` of its own and the request came in on another host, no
session is opened here (no cookie, no token):
```json
{ "handoff": { "origin": "https://files.acme.example", "code": "Zk…" } }
```
The client continues on the tenant's address, on the same sign-in page it
started from (`/drive/login` or `/admin/login`, with the same query), with the
code in the URL fragment (`<origin>/drive/login#handoff=<code>`); that page
redeems it with
[`POST /api/auth/handoff`](#post-apiauthhandoff-). The code is bound to the one
account that just signed in, lives 60 seconds, is spent by its first use and is
honoured on the tenant's host only.

### `POST /api/auth/handoff` ![public](https://img.shields.io/badge/-public-lightgrey)
The tenant's-address half of a sign-in typed on the platform's address
(multi-tenant). **Body** `{"code": "…"}` - the code a `POST /api/auth/login`
answered with.

**Response 200** - as for a sign-in: `{"user", "token"}` and the session cookie,
set on this host. **Response 401** `{"error": "invalid or expired handoff"}` -
the code is unknown, spent, expired, issued for another purpose, presented on
another host than the tenant's (it is spent all the same), or its account may
no longer sign in or is no longer this host's tenant's. The same answer for
every reason; the reason is in the audit row `auth.handoff_refused`
(`reason`: `expired`, `wrong_host`, `wrong_purpose`, `wrong_tenant`,
`actor_gone`, `subject_gone`, `actor_is_not_subject`), beside
`auth.handoff_issued` and `auth.handoff_used` (`purpose`, `actor_id`,
`subject_id`, `host`). The code is never logged or audited; an unknown or spent
one is not audited at all. `404` on an install where the handoff does not exist
(single-tenant).

### `POST /api/auth/logout` ![public](https://img.shields.io/badge/-public-lightgrey)
Deletes the session and clears the cookie (with no session it only clears the
cookie).

**Body** (optional): `{"return_to": "/admin/login" | "/drive/login"}` - the
sign-in page of the front door the person was using. Anything else means
`/admin/login`; the address is never taken from the request.

**Response 200**: `{"ok": true}`, plus `"logout_url"` for an SSO session whose
identity provider can end sessions - its end-session URL with `id_token_hint`,
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
    "id": 5, "email": "ayse@example.com", "username": "ayse",
    "role": "user",
    "avatar_url": "data:image/jpeg;base64,…"
  },
  "permissions": ["files.download", "files.create", "files.modify", "share.links", "…"],
  "permissions_in_folders": ["files.delete"],
  "permissions_by_folder": ["files.delete"],
  "permission_settings": { "share_link_max_days": 7 },
  "two_factor_required": false,
  "realm": "acme"
}
```
What the account may do ([PERMISSIONS.md](PERMISSIONS.md)), so a client can
hide what would only answer `403`: `permissions` holds account-wide,
`permissions_in_folders` only in some folders, and `permissions_by_folder`
lists every permission whose answer differs from folder to folder - a client
asks `POST /api/files/manager?action=allowed` about the selected paths before
offering those. The explorer reads this itself when its host passes no
`permissions` ([INTEGRATION.md](INTEGRATION.md)). The effective answer with the
source of each permission is `GET /api/auth/me/permissions`.

`realm` (multi-tenant installs, a tenant's account only) is the account's tenant
realm - what it writes in front of its name where no address names its tenant
(`acme/alex` over SFTP). Absent on a single-tenant install and for the
platform's own accounts. `GET /api/auth/ssh-keys` answers the SFTP/FTPS `login`
already written that way.

### `PATCH /api/auth/profile` ![user](https://img.shields.io/badge/-user-blue)
Patches the caller's own `email`, `display_name`, `locale`, `timezone` and
`avatar_url`. Absent fields are left alone.

Needs the `account.edit` permission; an API token needs `write` too - as do
`POST /api/auth/password` and `POST /api/auth/totp/enroll`, `…/verify` and
`…/disable`: a token that may only read cannot change the account it belongs
to (`403 token missing scope: write`).

`avatar_url` is the **profile picture**: a `data:image/…` URI (≤ 48 KB) or an
`http(s)` / site-relative URL; an explicit `""` removes it. Anything else is a
`400` rather than a silent drop - the person is looking at an upload they
believe worked. The SPA's user-settings dialog downscales what you pick to 160px
before encoding, so the cap is not something a user meets.

The picture belongs to the **account**, which is what makes it appear
everywhere: the explorer's collaboration strip draws it instead of initials for
that user on every client of the account - browser session, desktop app, and any
API key minted under it. Two deliberate exceptions, because the alternative is
drawing the wrong face on somebody's row:

- A token with a **username allow-list** is a shared proxy, not a person (its
  presence entry reads "work"), so no picture is attached.
- When a trusted host proxy re-identifies a connection as a different end user
  via `X-Filex-Presence-Name`, only *that* person's picture may be drawn -
  supplied by the proxy as `X-Filex-Presence-Avatar` (same accepted shapes, same
  cap). Without it the row falls back to initials.

The cap is small on purpose: the avatar rides inside every presence frame the
collaboration socket broadcasts, so it is paid for again on each join, leave and
focus change - unlike the branding logo, which is fetched once per page.

---

## Capabilities

### `GET /api/capabilities` ![public](https://img.shields.io/badge/-public-lightgrey)
Tells the frontend what features are available - used to hide buttons for
disabled features.

**Response 200** (abridged - the real body also carries the per-storage probe,
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
    "drawio":     { "enabled": false, "url": "", "state": "" }
  },
  "onlyoffice_url": "https://docs.example.com",
  "drawio_url": "",
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

`realm` is present on a **multi-tenant** install only - the sign-in form's Realm
field ([MULTI-TENANCY.md → Realms](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)):
`{"enabled": true, "locked_realm": null}` on the platform's address (the field
is empty and free), `{"enabled": true, "locked_realm": "acme"}` on a tenant's
own address (filled in and read-only). A single-tenant answer carries no
`realm` key at all.

⚠⚠ **An anonymous caller is told _whether_ a capability is on, never _where_ it
lives.** The endpoint is deliberately public - an embedder probes it before
anybody logs in ([INTEGRATION.md](INTEGRATION.md)) - so for a request carrying
no usable credential the `url` is dropped from every `external.<service>` entry
and the flat `onlyoffice_url` / `drawio_url` aliases come back
empty. `enabled` and `state` are unchanged, which is what a feature probe
actually asks.

Signed-in callers see the payload above in full, because one consumer needs a
real host in the browser: the draw.io iframe. OnlyOffice
does not - the browser gets its document-server URL from the authenticated
`POST /api/files/onlyoffice/config` (`documentServerUrl`) - so that host now
travels only with a credential as well.

Measured before this changed (2026-09-07, demo.filex.sh): an unauthenticated
`GET /api/files/capabilities` answered 200 with
`"url": "https://docs.example.com"` - the operator's internal document server,
published by every install that configured one.

`newdoc_types` is the other field worth naming, because it decides what a
"New document" menu may offer: the document types **this build can create**,
from a template registry compiled into the binary (`internal/newdoc`). Each row
is `{ ext, group, mime, requires, ext_required }`, where `requires` names the
external service the *editor* needs (`"onlyoffice"`, `"drawio"`, or absent for
the built-in code and markdown editors), and `ext_required` says whether the
file must carry the extension - `true` for office documents and diagrams, whose
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
server from offering a `.docx` nobody there can then open - and what keeps the
client from carrying a hardcoded extension list that rots the moment the
registry grows a type. Published to anonymous callers too: it is a static
property of the build, identical on every install of this version, and names no
host.

A signed-in person also gets the rows **running apps** add to the menu
(`new_documents`, [APP-PLUGINS-API.md](APP-PLUGINS-API.md#new-documents)):
`group` and `requires` are `"app"`, `key` is what the create call sends
(`app:<plugin>:<ext>`), and `app` names the app, the view that opens the new
file and the app's own label. They are not a property of the build, so an
anonymous caller or an app token is never told them.

A signed-in caller also gets `e2e_policy: { "available": true, "policy": "permitted" }` -
the caller's own tenant's row of [who may encrypt](E2E-ENCRYPTION.md#who-may-encrypt):
`available` is the platform operator's switch for the tenant, `policy` the
tenant's choice (`off` · `admins` · `permitted` · `approval`). Not an anonymous
caller's (a tenant's policy is not a property of the build), and absent on a
server from before the policy, which the explorer reads as "offer encryption as
before". The explorer's menus are not read off this row - the platform operator,
looking at another tenant's storage, is answered by that tenant's policy - but
asked per path (`POST /api/files/e2e/allowed`, [Encryption
policy](#encryption-policy)); the row tells the notification settings and the
Webhooks screen when the two request events can happen. The server decides
every write.

⚠ `antivirus` means **configured**, not answering: the setting is on and either
a scanner binary resolved or a clamd address is set. Reachability costs a
network round trip and is probed on `GET /api/admin/protection`, where an
operator is waiting for the answer. `antivirus_mode` is `binary` or `daemon`
and is absent when `antivirus` is false - the two deployments produce the same
green light and behave very differently when one of them breaks.

---

## File browsing

### `GET /api/files/manager` ![user](https://img.shields.io/badge/-user-blue)
List the contents of a directory (`action=index`, the default). The same
route reads a file (`action=preview`, `action=download`, both honour `Range`),
lists only the subfolders (`action=subfolders`) and asks what changed
(`action=changes`, below).

**Query**
| Param  | Notes |
|--------|-------|
| `action` | `index` (default) \| `subfolders` \| `changes` \| `search` \| `preview` \| `download` |
| `path` | `<storage>://<folder>`, e.g. `docs://reports/2026`; empty = the first storage's root |

**Response 200**
```json
{
  "adapter": "docs",
  "storages": ["docs", "archive"],
  "dirname": "docs://reports",
  "read_only": false,
  "perm": "editor",
  "files": [
    {
      "id": 4711, "path": "docs://reports/report.pdf", "basename": "report.pdf",
      "type": "file", "extension": "pdf", "size": 102400,
      "mime_type": "application/pdf", "last_modified": 1776852000000,
      "storage": "docs", "perm": "editor", "etag": "abc123",
      "thumb_url": "/api/files/thumb/4711?exp=…&sig=…"
    },
    { "id": 4712, "path": "docs://reports/photos", "basename": "photos", "type": "dir", "size": 0, "storage": "docs" }
  ]
}
```

`last_modified` is Unix milliseconds. `id` is the catalogue id (versions, tags
and comments are addressed by it); a folder listed straight from the storage
before a scan catalogued it carries rows without one. Inside an end-to-end
encrypted folder the answer also carries `e2e` and `e2e_root`, and an
encrypted folder's own row `e2e: true`.

**Status codes:** `200` ok · `403` forbidden · `404` path missing.

### Names filex keeps for itself

`.filex-trash`, `.versions`, `.thumbs`, the desktop app's `.filex-open` (at any
depth), the drafts area `.filex-drafts` and the empty-folder marker `.keepdir`
are filex's own (`backend/internal/syspath`). They are never listed or searched, and every
write that names one - creating, uploading, renaming, moving, copying,
extracting, saving, sharing, granting or restoring - answers:

```json
HTTP 403
{ "error": "\".filex-trash\" is reserved for filex's own use", "code": "RESERVED_NAME", "name": ".filex-trash" }
```

An archive member under one of them is skipped, the way a zip-slip entry is.
The single exception is the desktop's open-with round trip: `newfolder` of
`.filex-open` at the storage root, `upload` of `.filex-open/<hex session>-<name>`
(and the document editor's save of that copy), and `delete` of it.

A draft is the other door, and it opens for one person only: the file at
`.filex-drafts/<user id>/<key>/<name>` can be read (`preview`, `download`,
`info`) and saved into (`save-text`, the document server's callback, an app
editor's save) by the account whose id is in its path - nobody else, an
administrator included, gets anything but `404`/`403`. Everything else about a
draft goes through [the drafts endpoints](#drafts).

### `GET /api/files/manager?action=changes` ![user](https://img.shields.io/badge/-user-blue)
Has anything under a folder changed since the caller last asked?

| Param   | Notes |
|---------|-------|
| `path`  | `<storage>://<folder>` (a file path works too) |
| `since` | the `cursor` from the previous answer; empty = never asked |

**Response 200** `{ "cursor": "<opaque>", "changed": true }`

Answered from an in-memory change log fed by the same event chain that
refreshes open explorers and folder sizes - every write surface (explorer,
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
parsing the response - Electron's `net.fetch` raises
`Cannot convert argument to a ByteString …` from inside its response handler,
which no caller's try/catch can catch. The ASCII `filename` is the fallback;
`filename*` carries the real name and is what every current browser uses.

### `GET /api/files/read` ![user](https://img.shields.io/badge/-user-blue)
Stream one file's bytes, addressed by its catalogue id or by storage and path:

| Query | Notes |
|-------|-------|
| `id` | the node id (`entries[].id` of a listing) - preferred |
| `storage` + `path` | the storage's numeric id and the path inside it, when there is no id |
| `download=1` | `attachment` instead of `inline`; needs the `files.download` permission as well |

Sends `Content-Type`, `Content-Length` and an RFC 6266 `Content-Disposition`.
Needs viewer on the file. It does **not** honour `Range:`; for seeking in video
and audio, or resuming a download, use `GET /api/files/manager?action=preview`
or `action=download` with `path=<adapter>://<path>`, which honour it (`206`)
on every storage that can read a range. A file in filex's own trash, version or
thumbnail folders is the `404` a missing one gets. (A `GET /api/files/raw` this
page once described was never served.)

### `POST /api/files/move` ![user](https://img.shields.io/badge/-user-blue)
**Request** - sources and the destination FOLDER, both adapter-qualified.

⚠ `sourceDir` is **accepted and ignored** by the server. It decodes into the
request struct and no handler reads it; the undo it was said to "stamp" is
built entirely in the client. Root-confined callers do have it rewritten by the
confine layer, so it is not free to lie in - but nothing depends on it either.
Send it or don't.
```json
{
  "source": ["alpha://a.txt", "alpha://klasor"],
  "target": "beta://hedef",
  "sourceDir": "alpha://"
}
```
**Response 202** `{ "op": { "id": 12, "kind": "move", "storage_id": 1, "dest_storage_id": 2, … } }` -
the work is queued; poll `GET /api/files/ops`.

**One source under a new name.** With exactly one `source`, an optional
`name` is the name it gets inside `target`:

```json
{ "source": ["alpha://a.txt"], "target": "beta://hedef", "name": "b.txt" }
```

The move (or copy) and the rename are one step of the queue, so nothing can
stop between them. `name` is one path segment: not `.` or `..`, no `/`, `\`
or NUL, at most 255 bytes. With more than one source, without `target`, or on
`/api/files/delete` it is `400`. A `name` already taken in `target` is
treated like any taken destination (below): the item lands as `name-copy` -
except in a folder whose names are end-to-end encrypted, where the server
cannot make up a name and the operation fails instead
([E2E-ENCRYPTION.md → Folder ids](E2E-ENCRYPTION.md#folder-ids): the explorer
uses `name` to re-seal a name for the folder it moves into).

**Nothing is moved on top of something.** When the destination folder already
holds the name, the moved item lands beside it as `name-copy`, `name-copy-2`, … -
within one storage exactly as between two. A move into the folder the item is
already in changes nothing. (Before 0.41.0 a same-storage move replaced the
file that held the name.) A **rename** onto a taken name is refused instead -
see `POST /api/files/manager?action=rename` below.

### `POST /api/files/copy` ![user](https://img.shields.io/badge/-user-blue)
Same shape, `name` included, same queued answer.

**The two ends may live in different storages.** `dest_storage_id` on the queued
op is the target's storage; when it differs from `storage_id`, the worker
streams the bytes between the two drivers instead of asking one driver to
rename - a whole tree, empty folders included, each file's mtime preserved where
the target can hold one, and every file stat-checked on the far side before a
move deletes anything. A cross-storage move removes the source outright (not to
the trash); a name already taken becomes `name-copy`. A symlink the source
cannot follow is left behind rather than read, a folder link back into the tree
is walked once, and either ends the op **`partial`** with the skipped entries
named in `error` - a **move** then keeps its source. Full behaviour:
[Moving files between storages](STORAGE.md#moving-files-between-storages).

**Refusals** are at submit time, not in the worker: `400` unknown target adapter
· `403 { "code": "READ_ONLY" }` when the op would change a read-only storage -
a copy or a move into it (with a `hint`), a move or a delete out of it; a copy
out of it is fine · `403` no editor permission on
the source, or on the target folder **in the destination's storage** · `400`
mixed-adapter *sources* (one batch, one source storage).

A caller confined to a folder (a `root:` token, or `X-Filex-Root`) is answered
`403 {"error":"path outside confined root"}` for a `source`, a `target` or a
`sourceDir` outside it, before anything else is asked: the same answer, byte
for byte, whatever the body's `Content-Type` (JSON, `text/plain`, none) and
whether the storage, the folder or the file exists or is read-only. Up to
0.51.0 a body not labelled JSON reached the checks above as written, and an
unknown storage answered `400`, a read-only one `READ_ONLY` with its name.

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
The paths are relative to their storage: a source or a `dest` with `://` in
it, or with a `..` segment (`..\` too), is refused with
`400 { "code": "BAD_PATH" }`. Up to v0.50.0 such a path was judged without its
`<name>://` prefix and carried out with it, so `"dest": "b/c://a/"` was checked
as `a` and written to `b/c:/a/`.
`kind` is `copy`, `move` or `delete`; any other kind is refused with
`400 { "code": "BAD_KIND" }`. The queue runs more kinds than these (a rename,
a restore, a purge, an upload commit, an app's action), but each of them is
queued by the endpoint that judges it (`?action=rename&queued=1`,
`POST /api/files/manager/restore?queued=1`, …), never through this one.

The same refusals as the per-verb endpoints apply, `READ_ONLY` included (up to
v0.46.0 this endpoint asked neither end of the op about the read-only flag, and
the per-verb ones only the destination).

A caller confined to a folder (a `root:` token, or `X-Filex-Root`) is held to
it here as on every other door: a source or a destination outside the root -
another storage included, or a `storage_id` that does not exist - answers
`403 {"error":"path outside confined root"}`, before `BAD_PATH` and the
read-only check, the same whatever the body's `Content-Type`. Up to
v0.46.0 this endpoint was the one that did not: the confinement layer rewrites
the path fields it knows (`source`, `target`, …), and this body names its
paths `sources` and `dest` beside a bare `storage_id`. Up to 0.51.0 a body not
labelled JSON was answered `400 BAD_PATH` for a path climbing out, and the
root's refusal named the path.

⚠ Up to v0.46.0 this endpoint passed the client's `kind` straight to the
queue (an upload commit or an app's action could be queued here unjudged), and
once the queue also ran restores, `{"kind":"restore","sources":["<node id>"]}`
would have restored any trash entry by id - another tenant's included -
without the restore's own checks.

### `POST /api/files/manager?action=newfolder` ![user](https://img.shields.io/badge/-user-blue)
```json
{ "path": "alpha://reports", "name": "2026" }
```
Creates the folder `name` inside `path` and answers with the re-rendered
listing of `path`, like every other action of this route. `name` is one
segment: empty or containing `/` or `\` is `400 bad folder name`. Needs
`files.create` (editor) in `path`; a token needs `write`. `403` on a read-only
storage, `409` when a file already has that name, `501` when the storage
cannot make folders. (A `POST /api/files/mkdir` this page once described was
never served.)

### `POST /api/files/manager?action=rename` ![user](https://img.shields.io/badge/-user-blue)
```json
{ "path": "alpha://reports", "item": "alpha://reports/old.txt", "name": "new.txt" }
```
Renames one item inside its own folder and answers with the re-rendered
listing. `name` is a leaf: empty, `/`, `\`, `.` and `..` are refused with `400`.

**A rename never replaces what already has the name.** When a file or a folder
already holds it - or the listing still shows a file there whose bytes have
gone missing - the answer is `409 { "code": "NAME_TAKEN", "name": "new.txt" }`
and nothing moves. When the backend cannot say whether the name is free, it is
`503 { "code": "EXISTS_CHECK_FAILED" }`, never a rename on the chance. A rename
that only changes the case (`a.txt` → `A.txt`) is allowed, also on a
case-insensitive disk. Unlike a move, a rename is not given a `-copy` name: the
person chose this one.

⚠ Before this, the rename replaced the file that had the name - not into the
trash - and its catalogue row was dropped with its version history, shares and
comments. A folder renamed onto another folder's name on an object store was
merged into it.

**Finished even if the client leaves.** Once a rename has passed its checks it
runs to the end, whether or not anybody is still waiting for the answer. A
closed tab, or a proxy that stops waiting (nginx after 60 s by default), no
longer stops it half-way. The same holds for the synchronous `?action=move`
and `?action=delete`, `POST /api/files/manager/restore`,
`DELETE /api/admin/trash/{id}`, `POST /api/ai/move` and `/api/ai/delete`
(with the MCP tools behind them), and WebDAV's `DELETE` and `MOVE` of a
folder. A client that gave up lists the folder again to see the result.

⚠ Before this, the request's cancellation stopped the work between two
objects. A folder on an object store, which is changed one object at a time,
was left in two places with the catalogue still describing the old one, and a
retry answered `409` because the half that had arrived held the name.

**As a job: `?action=rename&queued=1`.** The same body and the same checks, so a
refusal is still answered at once, as above. What they allow is queued instead
of run: **202** `{ "op": { "kind": "rename", "sources": ["reports/old"],
"dest": "reports/new", … } }`, followed with `GET /api/files/ops` like a move.
The explorer asks for it for a folder, which on an object store is one request
per object, when the server lists `rename` under `capabilities.queued`; a file
is still renamed inside the request. The job never picks another name the way a
move does: a name taken by the time it runs fails it (`something with that
name already exists here`), and nothing is replaced. Once running it is not
cancelled half-way; while it waits in the queue it can be. A server with no
queue renames inside the request, as above.

Queued renames, restores and purges run **one at a time on a lane of their
own**, beside the worker that runs copies, moves, deletes and upload commits:
a large folder renamed, restored or purged on an object store no longer holds
every other queued operation of the instance behind it.

A server that stops while one runs does not record it as `cancelled`: the
entry in hand is finished (a rename that completes is `ok`), and a restore or
purge of several entries stops between two of them and stays `running`; the
next start requeues it and carries it on. A rename the process died in the
middle of is carried on too: on an object store part of the folder is already
at the new name, and that half is the rename's own, not a name taken by
something else - the job finishes the move instead of failing with `something
with that name already exists here` (which is what it did up to this release,
leaving the folder in two places). Stopping waits for it at most ten
seconds. Up to this release the job was recorded `cancelled` - terminal, so
the entries it had not reached stayed in the trash - and stopping waited for
the whole folder.

### `POST /api/files/delete` ![user](https://img.shields.io/badge/-user-blue)
```json
{ "source": ["alpha://a.txt", "alpha://klasor"] }
```
**Response 202** `{ "op": { "id": 13, "kind": "delete", … } }` - queued like a
move; poll `GET /api/files/ops`. Every item goes to the trash.

The items of one delete job are trashed several at a time
(`FILEX_OPS_DELETE_WORKERS`, default 4 - [CONFIGURATION.md](CONFIGURATION.md)),
and the job's `done`/`failed` counters are written about once a second while it
runs. An item that lies inside another item of the same job is left to that one
(it counts as done with it), so a folder goes to the trash whole.

### `GET /api/files/manager/shared-with-me` ![user](https://img.shields.io/badge/-user-blue)

What other people have shared with the caller - the items they reach through a
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
A grant on a folder lists **the folder**, not its contents - the row is a `dir`
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
| `q` (or `query`) | - | Search text. May carry `tag:x` / `-tag:x` filters. |
| `storage_id` | `0` (all) | Restrict to one storage. Also what enables the SQL LIKE fallback. |
| `limit` | `50` | Max results. |
| `scope` | `all` | `name` \| `content` \| `all`. |

`POST /api/files/search` takes the same fields as a JSON body. Hits come back in
a defined rank order - exact filename, prefix, name, path, fuzzy, then
content-only. Full reference: [SEARCH.md](SEARCH.md).

### `POST /api/files/save-text` ![user](https://img.shields.io/badge/-user-blue)
Writes the body of the built-in text / code / Markdown editor. Takes a version
snapshot of what it is about to replace, writes the bytes, updates the row and
re-indexes the file.

Which files it saves: those whose extension is a known text or code format
(`.txt`, `.md`, `.conf`, `.json`, `Dockerfile`, `Makefile` …), and - since #56 -
an existing file under any other name (`LICENSE`, `NOTICE`, `notes.custom`)
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

## Encryption policy

Who may start encrypting ([E2E-ENCRYPTION.md → Who may encrypt](E2E-ENCRYPTION.md#who-may-encrypt)).
A door that would create an encrypted folder's key file or a `.fxe` - or land
an item on one of those names by a rename or a move, unless the item is a
folder, a `.fxe` that stays a `.fxe` or a key file that stays its own
folder's, or copy what is encrypted (a `.fxe`, a key file, a folder holding
either) - and is refused answers
`403 {"error":"e2e_not_allowed","reason":"tenant_disabled|policy_off|admins_only|permission|approval_required","message":…}`
(`message` is the reason in the reader's language; a file request's is the
sentence it gives any file the link does not take). An MCP tool answers an
error result naming the reason. The agent surface (`/api/ai`, MCP, ShareX,
upload tickets) refuses a key file's name before the rule, folder or file:
`403 RESERVED_NAME`. A door that could not decide the rule - a lookup failed,
the rule's or one it made to ask (the storage's row, the account a write is
judged for) - answers its own server failure, never a refusal:
`500 {"error":"could not check the encryption policy"}` on the web app's routes
and `/api/ai`, an error result in the same words over MCP,
`503 storage_unavailable` for an upload ticket and a file request, and
`500 save_failed` for an app's *save as*. The rule's own log line, one for each
write it could not decide, names the storage, the person and the error, never
the path; an app job that fails this way is also reported by the queue, which
names the output.

### `POST /api/files/e2e/allowed` ![user](https://img.shields.io/badge/-user-blue)
The explorer's question before it offers **Create encrypted folder…**,
**Encrypt with E2EE…** or **Request encryption…**. Body
`{ "items": [{ "path": "Docs://Reports", "kind": "new_folder" }, …] }` - at most 1000
(`400 {"error":"too many items"}`; a body that is not JSON is
`400 {"error":"bad json"}`); answer
`{ "encrypt": ["allowed" | "request" | "denied", …], "reasons": ["" | "tenant_disabled" | "policy_off" | "admins_only" | "permission" | "approval_required", …] }`,
positional, in the order of the items. `kind` says which encryption: `folder`
(the folder, encrypted where it is), `new_folder` (a new encrypted folder made
in it), `file` (the file); without it, the kind of what is there. The answer
counts exactly the approvals the create door would spend for that kind, so
what the explorer offers is what the server accepts. A `kind` it does not know
is `denied`. It changes nothing - an approval is looked for, never spent - so
it needs only the `read` verb. It is asked for administrators too: the policy
`off` and the tenant's ceiling stop them. `filex encrypt` asks it before the
password.

A path it cannot place - an unknown storage, one outside the caller's tenant,
`..` - is `denied`, and so is a path outside the root of a root-confined caller
(a token's `root:` scope, `X-Filex-Root`): nothing of the rule is heard there.

### `POST /api/files/e2e/requests` ![user](https://img.shields.io/badge/-user-blue)
Asks to encrypt, under the `approval` policy. A session, or a token with the
`write` verb. Body
`{ "path": "Docs://Reports", "kind": "folder" | "new_folder" | "file", "reason": "…" }` -
`folder` asks to encrypt that folder where it is, `new_folder` to make one new
encrypted folder directly inside it, `file` to encrypt that file; the file's
request is kept under the folder it goes into (a single file's approval is its
folder's). An approval opens its kind there and nothing else. It is always
filed `pending`, for the caller's own tenant and user: nothing in the body can
name another. `201 { request, created: true }`, or `200 { request, created: false }`
when the same request is already waiting.

| Status | `error` | When |
|---|---|---|
| 400 | `not_requestable` | the answer for that path is not `request` - no approval is needed there, or none could help (the body also carries `answer` and `reason`) |
| 400 | `kind_mismatch` | `kind` is not what is there: `folder` or `new_folder` for a folder, `file` for a file |
| 400 | `reason_required` | an empty reason (a reason is cut at 2000 characters) |
| 400 | `bad_request` | anything else that is wrong with the request, `message` saying what: a body that is not JSON, a path that names no storage or holds `..`, a `kind` that is neither, a file request that names no file, a path that names a key file or a `.fxe` |
| 403 | `path outside confined root` | a root-confined caller asking outside its root |
| 404 | `not_found` | an unknown storage, or one outside the caller's tenant |
| 404 | `path_missing` | nothing is at the path: a request names a folder or a file that is there |
| 429 | `too_many_pending` | the caller has 20 requests waiting already; one must be answered or lapse first |

### `GET /api/files/e2e/requests` ![user](https://img.shields.io/badge/-user-blue)
The caller's own requests, every state, newest first (up to 200):
`{ requests: [...] }`. A session, or a token with the `read` verb. A
root-confined token sees the ones inside its root.

A request, as these routes answer it:

```json
{ "id": 12, "path": "Docs://Reports", "storage": "Docs", "kind": "folder",
  "reason": "salary sheets", "status": "approved", "requester": "Ada",
  "requester_id": 7, "decider": "Grace", "decided_at": "2026-10-01T09:12:03Z",
  "decision_note": "", "expires_at": "2026-10-08T09:12:03Z", "used_at": null,
  "created_at": "2026-10-01T08:40:00Z", "tenant_id": 3, "decidable": false }
```

`status` is `pending` · `approved` · `rejected` · `expired` · `used`.
`decidable` says whether the administrator reading the list may answer it:
`false` on the platform operator's list for another tenant's request, and on
a person's own list.
`expires_at` is when a waiting request lapses, and once it is approved, when the
approval does: 7 days from the request, and 7 days from the approval. `used_at`
is when the approval was spent.

---

## Drafts

A new document is a **draft** until its first save (#71): a real file in the
person's own `.filex-drafts/<user id>/<key>/` folder of the storage it was made
for, with a row that remembers where it is meant to go. Nothing is created in
that folder until the draft is saved. The user guide is
[ONLYOFFICE.md → Drafts](ONLYOFFICE.md#drafts-nothing-is-in-the-folder-until-you-save).

Every route answers for the **caller's own** drafts; another person's key is
`404`. They need a signed-in person: an app token, and a caller confined to a
root (an embed's shared proxy token), get `403 {"code":"DRAFTS_UNAVAILABLE"}`
and create documents with `newfile` as before. `GET /api/files/capabilities`
carries `drafts: { limit }` exactly when the caller may use them.

A draft, as these routes answer it:

```json
{ "key": "3f9c0a1b2c3d4e5f", "name": "minutes.txt",
  "path": "docs://.filex-drafts/7/3f9c0a1b2c3d4e5f/minutes.txt",
  "storage": "docs", "target_dir": "docs://Reports", "target": "docs://Reports/minutes.txt",
  "type": "txt", "size": 11, "mime": "text/plain; charset=utf-8",
  "created_at": "2026-09-27T09:12:03Z", "modified_at": "2026-09-27T09:14:40Z" }
```

`path` is the file an editor opens and saves into; `type` is the `newdoc_types`
key it was made as.

### `POST /api/files/drafts` ![user](https://img.shields.io/badge/-user-blue)
Makes a draft: the same body as `newfile` - `{ path, name, type, exact_name? }`
where `path` is the folder it is for - and the same bytes (an office type gets
its template). `201 { path, name, ext, size, mime, draft }`, shaped like
`newfile`'s answer so a client opens `path` the same way. The caller needs
≥ editor on the folder, and the name and type rules are `newfile`'s. At the
limit (`drafts.limit`,
[PROTECTION.md → Drafts](PROTECTION.md#drafts)) it is
`409 { "code": "DRAFT_LIMIT", "limit": 50, "count": 50 }` and nothing is created.

### `GET /api/files/drafts` ![user](https://img.shields.io/badge/-user-blue)
`{ drafts: [draft…], count, limit }` - the caller's live drafts on every
enabled storage, the most recently made first.

### `GET /api/files/drafts/count` ![user](https://img.shields.io/badge/-user-blue)
`{ count, limit }` - what the navigation panel's badge reads.

### `GET /api/files/drafts/{key}` ![user](https://img.shields.io/badge/-user-blue)
One draft. An editor that was handed only a path asks this to learn where the
draft is meant to go.

### `POST /api/files/drafts/{key}/save` ![user](https://img.shields.io/badge/-user-blue)
Moves the draft to its folder: body `{}` for its own name, or `{ "as": "<name>" }`
for another. `200 { ok, path, name, target_dir }`; the file keeps its catalogue
row (an editor still open on it goes on saving into the saved document) and
from then on is an ordinary file - versioned, listed, announced.

- The name is taken: `409 { "code": "TARGET_TAKEN", "name", "suggested",
  "target_dir" }`, where `suggested` is the first free `name (2).ext`,
  `name (3).ext` … Nothing moves. The client asks, and sends `{ "as": suggested }`
  if the person agrees. ⚠ A save never replaces a file.
- The folder is gone: `409 { "code": "FOLDER_GONE" }`; the draft stays.
- The server cannot tell whether the name is free: `503 { "code":
  "EXISTS_CHECK_FAILED" }` - refused rather than risk a replace.
- ≥ editor on the folder is checked **now**, not when the draft was made:
  `403` if the caller lost it meanwhile; a read-only storage is `403` too.

### `DELETE /api/files/drafts/{key}` ![user](https://img.shields.io/badge/-user-blue)
Discards the draft into the trash: `200 { ok: true, trashed: true }`. It is then
in the caller's own trash listing with `draft: true` and its name as its path,
and restoring it puts it back in Drafts ([TRASH-VERSIONING.md → Discarded
drafts](TRASH-VERSIONING.md#discarded-drafts)). On a storage that cannot keep
deleted bytes it is deleted outright (`trashed: false`).

---

## Uploads (multipart)

For files >5 MB. Smaller files can use `POST /api/files/manager?action=upload`
(single-shot `multipart/form-data`); the driver-agnostic, resumable path is the
staged upload, `POST /api/files/upload/begin` ([UPLOADS.md](UPLOADS.md)).

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
`POST /api/files/upload/begin`, keeps it for a file whose bytes name no type.)

`storage_id` may be omitted when `path` carries an adapter prefix; `filename` is
optional and folded onto `path` when both are sent (an upload to a storage root
arrives as `path: "adapter://"` plus a filename). `chunk_bytes` is a request:
the server raises anything below 5 MiB and re-balances so an upload never
exceeds 10 000 parts - **use the `part_size` it answers with**.

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
`part_urls` is a **flat list of URLs**, one per part in order - the browser PUTs
each chunk straight to its own URL, then calls `/finalize` (or `/abort`).

> ⚠ There is **no chunk-through-filex fallback on this endpoint**. A driver that
> cannot do multipart at all (local, sftp, ftp, webdav) answers
> **`501 storage does not support multipart upload`** at `init` - earlier
> versions of this page described a `POST /api/files/upload/chunk` route as the
> fallback; that route does not exist. Measured 2026-08-19.

> ⚠⚠ A [plugin](PLUGINS.md) storage that declares `multipart` passes the check
> at `init` and then usually answers **no part URLs** (`part_urls: null`),
> because a plugin's multipart is built for the staged-upload commit, where
> filex pushes the parts itself. There is nothing for the browser to PUT to -
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

Archives are listed, extracted and created on the server, in every format
[ARCHIVES.md](ARCHIVES.md#providers-and-formats) lists: ZIP, 7z, the TAR
family (TAR, TAR.GZ, TAR.BZ2, TAR.XZ), bare gzip, bzip2 and xz, and RAR when
the server's 7-Zip reads it. filex reads plain ZIP, TAR, TAR.GZ, TAR.BZ2,
gzip and bzip2 itself; 7z, xz (TAR.XZ included), a password-protected ZIP and
RAR need 7-Zip on the server, and so does creating anything but a plain ZIP. A
password works for ZIP and 7z. Paths are
`<adapter>://<path>` (a bare path, with `storage_id`, is the older form).

Packing a selected item takes its bytes, so every door that packs asks what a
download asks: `files.download` on the item (viewer and up) and never one of
filex's own names. `POST /api/files/archive/create` and the AI surface's
server-side zip (`POST /api/ai/zip`, the MCP `file_zip` tool) ask it through
one function (`internal/api/handlers/pack_source_rule.go`); the AI zip asked
only "may the caller see it" until v0.50. The AI zip also keeps the
end-to-end encryption boundary: no encrypted file is packed into a zip
outside its folder (`409 E2E_BOUNDARY`,
[MCP.md](MCP.md#encrypted-folders-and-fxe)).

**Limits.** **Settings → Archives** holds the live policy: whether 7-Zip is
used, the formats allowed for creation and the default one, the most members
an archive may hold, the most bytes it may expand to, and a timeout
([ARCHIVES.md](ARCHIVES.md#process-configuration)). Listing, extracting and
creating are all held to it; over a limit answers `413`, naming the limit.

**Errors** carry a `code`: `401 PASSWORD_REQUIRED` (the archive is encrypted and
no `password` was sent: ask for one and send the same request again with it),
`401 BAD_PASSWORD`, `400 UNSUPPORTED_FORMAT`, `400 PASSWORD_CHARSET` (a ZIP
password takes ASCII only), `413 ARCHIVE_LIMIT_EXCEEDED`, `503
PROVIDER_UNAVAILABLE` (7-Zip is off or missing), `503 SNAPSHOT_FAILED` (a
version of a file the write would replace could not be kept), `502
ARCHIVE_PROVIDER_FAILED`.

Downloading a selection as one archive is a different pair of requests
(`POST /api/files/archive/download`, then a credential-free GET):
[API.md → Downloading a selection is two requests](API.md#downloading-a-selection-is-two-requests).

### `POST /api/files/archive/list` ![user](https://img.shields.io/badge/-user-blue)
**Request**
```json
{ "path": "main://bundles/archive.7z", "password": "optional" }
```
**Response 200**
```json
{
  "entries": [
    { "name": "a.txt", "size": 100, "mtime": "2026-04-22T10:00:00Z", "is_dir": false },
    { "name": "sub/", "size": 0, "mtime": "2026-04-22T10:00:00Z", "is_dir": true }
  ]
}
```
Needs viewer on the archive. A member read through 7-Zip may also say
`encrypted`, `is_link` or `special` (an archive holding a link or a special file
is refused when extracted).

### `POST /api/files/archive/extract` ![user](https://img.shields.io/badge/-user-blue)
```json
{
  "path": "main://bundles/archive.zip",
  "dest": "main://extracted",
  "members": ["sub/a.txt"],
  "password": "optional"
}
```
`dest` defaults to the archive's own folder and must be on the **same
storage** (`400` otherwise). `members` extracts just those entries; omit it for
the whole archive. Needs viewer on the archive and `files.create` (editor) on
`dest`.

**Response 202** - the work is queued, with progress and Cancel in the
operations center:
```json
{ "op": { "id": 3, "kind": "archive-extract", "storage_id": 1, "sources": ["bundles/archive.zip"], "dest": "/extracted", "total": 12, "done": 0, "status": "pending" } }
```
Follow it with `GET /api/files/ops/{id}` ([Operations](#operations-long-running)).
A password is checked while the request is still open, so a missing or wrong
one answers `401` here rather than failing the queued job.

A member whose name is already taken in `dest` **replaces** that file (the
replaced bytes are kept as a version first, as for any overwrite); a member
that would land on a folder of its name, or a folder on a file, is skipped.
There is no `overwrite` flag. An archive holding a member that would land
outside `dest` (zip-slip), a link or a special file is refused whole, before
anything is written.

### `POST /api/files/archive/create` ![user](https://img.shields.io/badge/-user-blue)
```json
{
  "dest": "main://out/bundle.7z",
  "sources": ["main://reports", "main://notes/a.txt"],
  "format": "7z",
  "password": "optional",
  "encrypt_filenames": true,
  "compression": 7,
  "solid": true,
  "dictionary_size_mb": 64
}
```
Writes a new archive from files and folders already in filex (from any
storage the caller can read). Only `dest` and `sources` are required:

| Field | Notes |
|---|---|
| `format` | `zip`, `7z`, `tar`, `tar.gz`, `tar.bz2`, `tar.xz`; omitted, it is read from `dest`'s extension, then the policy's default. RAR and the bare compressors are extraction-only (`400 UNSUPPORTED_FORMAT`), and so is a format the policy does not allow |
| `password` | ZIP (ASCII only) and 7z |
| `encrypt_filenames` | 7z only: the member names are encrypted too, so even the listing asks for the password (a ZIP cannot hide its names; there it is ignored) |
| `compression` | `0`-`9` |
| `solid`, `dictionary_size_mb` | 7z only; the dictionary is `1`, `2`, `4`, `8`, `16`, `32`, `64`, `128` or `256` |

A field that does not fit the format answers `400 INVALID_ARCHIVE_OPTIONS`.
Needs `files.create` (editor) at `dest` and `files.download` on every source.
**An occupied `dest` is refused** before any work is done, `409
TARGET_EXISTS` - never replaced. `409` also when the selection holds no
readable file or two members would get the same name.

**Response 202** `{ "op": { "id": 4, "kind": "archive-create", … } }`, followed like
an extraction.

### `POST /api/files/archive/add` ![user](https://img.shields.io/badge/-user-blue)
```json
{
  "path": "main://bundle.zip",
  "files": [
    { "source": "main://a.txt", "name": "a.txt" },
    { "source": "main://sub/report.pdf", "name": "docs/report.pdf" }
  ]
}
```
The older, **ZIP-only** update: `path` is the zip to write, created when it is
not there; `files[].source` is what to read (on the zip's own storage) and
`files[].name` is where it lands inside the zip. A member of that name already
in the zip is replaced. It runs in the request and answers **`200 {"path",
"size"}`** - no queued job. Needs `files.create` for a new zip, `files.modify`
for an existing one. For a new archive in any other format, use
`archive/create`. ⚠ The `{paths, dest, compression}` body this page once showed
was never the contract: anything without `path` and `files` is `400 missing
path or files`.

---

## Sharing

PIN-protected, time-limited, optionally download-capped public links.

### `POST /api/files/share` ![user](https://img.shields.io/badge/-user-blue)
Needs **editor** on the item and the `share.links` permission (a file request,
`kind: "drop"`: `share.upload_links`); an API token needs `write`. The same rule
answers `POST /api/ai/share` and the MCP `file_share` tool. A link then
answers only while its creator still holds that right on the item -
[SHARING.md → A link follows its creator](SHARING.md#a-link-follows-its-creator).

An end-to-end encrypted folder, and anything inside it, is never linked:
`409 {"code":"E2E_ENCRYPTED"}` for a download link and a file request alike
(a visitor would get ciphertext, and a file request would store their uploads
in the folder unencrypted). A single encrypted file (`.fxe`) is linked as it
is ([E2E-ENCRYPTION.md](E2E-ENCRYPTION.md#feature-trade-offs)).

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
capped at the admin's maximum link life (`share.max_ttl_days`, default 7 days -
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
Revokes a share: the caller's own link (any link for an administrator), inside
the caller's tenant and token root - `404` outside them, the answer an unknown
id gets; `403` for somebody else's link. `POST /api/ai/unshare` and the MCP
`file_unshare` tool ask the same rule (they asked only the last part until
v0.50).

### `GET /api/shares` ![user](https://img.shields.io/badge/-user-blue)

The links the **caller** created - the list behind **My shares**, for any
signed-in person, administrator or not. `?limit=&offset=`, and `?active=true`
for live links only. Answers `{"items": [...], "total", "page", "page_size"}`
with the same rows and tenant filter as `GET /api/admin/shares`, minus the
creator's address (on this list it is always the caller), and each row's
`url` built server-side exactly as the share dialog's is - never from the
browser's address. A `root:`-confined token sees only the links inside its
folder.

### `GET /api/shares/{id}/pin` ![user](https://img.shields.io/badge/-user-blue)

One link's PIN, for the person who created it or an administrator - **403**
for anybody else, and for an `app` token (`handlers.RequirePersonalCaller`: a
PIN is a credential, and an app token has no person behind it). An allowed
caller always gets **200**, carrying the PIN or one word saying why there is
none:

- `{"pin": "83459512"}`
- `{"pin": null, "reason": "no_pin"}` - the link has no PIN;
- `{"pin": null, "reason": "not_recoverable"}` - minted before migration
  00049, or while the instance had no key, so only the bcrypt hash was kept;
- `{"pin": null, "reason": "no_secret_key"}` - the instance has no
  `FILEX_SECRET_KEY` to open the sealed copy with.

The PIN is sealed with AES-256-GCM beside the bcrypt hash; the gate still
checks only the hash. Every call writes an audit row, `share.pin_revealed`,
whatever the answer. A link in another tenant, or outside a confined token's
folder, is the **404** an unknown id gets.

### `GET /s/:token` ![public](https://img.shields.io/badge/-public-lightgrey)

The link a stranger opens. **What it answers depends on who is asking** - see
[The public surface](#the-public-surface) for the rule and the escape hatches.
In short: a browser asking for HTML gets the SPA shell; anything else (curl,
wget, a download manager, the PIN form's own POST) gets the bytes or the
server-rendered no-JS page, exactly as before.

`POST /s/:token` is what the no-JS PIN form submits to (`pin` as a form field).

**A link behind a PIN, from a script.** There is no separate verify or
download route (a `/api/share/:token/verify` and `/api/share/:token/download`
this page once described were never served). Send the PIN with the request
for the bytes: `GET /s/:token` with an `X-Filex-Pin: 1234` header (or
`?pin=1234`). A right PIN streams the file and sets the link's unlock cookie,
so the next request needs no PIN; a wrong one answers the PIN form again, and
five wrong answers lock the link for ten minutes. A download is counted
before the first byte leaves, and a link that has used up its `max_downloads`
answers `404`, as an expired one does. The JSON surface the shell itself uses
is [`POST /api/public/s/{token}/pin`](#post-apipublicstokenpin-) below.

---

## The public surface

Everything a stranger reaches through a filex link - a download share, a file
request, an app plugin's page - is **one surface**: one shell, one PIN gate,
one expiry story, one set of branding. The JSON below is what that shell is
built from; the server-rendered pages behind it are the no-JS fallback.

⚠ Every answer under `/api/public/*` is `Cache-Control: no-store` and
`X-Robots-Tag: noindex, nofollow`, except `/branding` and `/ui-locales/{code}`,
which are the same for every visitor and revalidate (`public, no-cache` with an
ETag, below). Nothing here is authenticated, and nothing here names the
creator, the storage or the file's path.

⚠ The same default holds for the whole API: every `/api` answer is
`Cache-Control: no-store` unless its handler sets a policy of its own
(`api.APINoStore`, PR #41 - a CDN rule that cached everything once served one
administrator's `/api/auth/me` to every visitor). The four answers that say who
the instance is - `/api/public/branding`, `/api/branding`, `/api/appearance`
and `/api/public/ui-locales/{code}` - are the only `public` ones, and a test
walks the route table to keep it that way.

⚠ **The PIN gate.** Five wrong PINs shut the gate for ten minutes, counted on
the share row - so it survives a restart and holds across two instances behind
one address. The *right* PIN during a lock is refused too: a lock the correct
answer lifts is no lock at all. Answering the PIN mints an HttpOnly cookie
(`fxp_<first 16 of sha256(token)>`, an HMAC over the token's hash and an
expiry, 12 h) that carries no PIN and opens only that one link. Before v3 this
lock existed only on app-plugin pages; a PIN on a `/s/` link could be walked
through at the speed of HTTP.

### `GET /api/public/branding` ![public](https://img.shields.io/badge/-public-lightgrey)

Who this instance says it is - the same record the admin **Branding** page
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

`ui_locales` names the languages an installed **language pack** adds - the
codes only, never their strings - and `rtl` says which way a language lays the
interface out ([RTL](RTL.md)). One language's strings are
`GET /api/public/ui-locales/{code}`, fetched when somebody picks it; it is
cached the same way (a strong ETag with `no-cache`), which matters most there,
because a complete catalogue is ~300 KB.

`theme` is `system` | `light` | `dark`
anything else reads as `system`). ⚠ Deliberately **not** the `/api/branding`
payload: the SSO button label is not a stranger's business. (The operator's
custom stylesheet is not on `/api/branding` either any more - it moved to
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
| `expired` | the clock ran out. An administrator's **Revoke** moves the expiry to the moment it happened, so it lands here too - there is no second column, and one that could disagree with this would be worse than one word doing both |
| `revoked` | dead for a reason that is **not** the clock: the visit/download ceiling is spent, the file is gone, or the app that answers it was stopped or removed |
| `locked` | the PIN gate is shut after five wrong answers. **Not** a reason the link is dead - it lifts by itself |
| `unavailable` | present (`true`) once unlocked when the item, or a folder above it, is an entry its storage could not answer for ([PLUGINS.md](PLUGINS.md#an-entry-your-stat-cannot-answer-for)). The link is alive and may answer again; until then `/s/<token>` answers `409` with a page that says so |
| `visits_left` | `max_downloads - download_count`, or `null` when there is no ceiling |
| `node` | present once unlocked, for `file` / `folder` only |
| `app` | present for `kind: "app"`; `files` are the copies the app exposed, once unlocked |

A live link has `expired` and `revoked` both false; "can I use this" is
`expired || revoked`.

**404** for an unknown token and for a token of the other kind (a `/d/` link
asked for here) - the same body for both, so an anonymous caller cannot
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
`{accepted: true, job_id}` when the surface asks for a job - the job runs as
the **link's creator**, on the link's document, with that user's ACL, and the
visitor never sees the queue row.

**401** `pin_required` until unlocked (checked *before* the app is consulted,
so a locked link never reports anything about the instance behind it) · **404**
when the link is not an app link or the runtime is unavailable · **410** when
the app is stopped or uninstalled, **or when the account that opened the link
is disabled or deleted** - one answer for all of them, so a stranger holding a
token cannot tell which. The same fact turns `revoked` on in
`GET /api/public/s/{token}`, and it is reversible: re-enabling the account
brings its links back untouched.

A job asked for here passes the **same submit-time gate as an authenticated
run** (handlers `enqueueAsCreator`): the action is resolved through the
registry - so one the administrator disabled or reserved to administrators is
refused, with the CREATOR's admin status deciding the reserved case, because
the creator's rights are what the job spends - the creator's ACL on the anchor
is re-read (viewer, editor when the job writes, higher on `min_role`), the
encrypted-folder and read-only refusals apply, and `params` are capped at the
same 64 KiB. **403** `no_access` / `permission_denied` · **409**
`link_unavailable` · **413** `params too large`. ⚠ All three carry ONE
sentence and no detail: which of the reasons it was would tell a stranger
holding a token how this instance is configured.

### `GET /api/public/s/{token}/file/{ref}` ![public](https://img.shields.io/badge/-public-lightgrey)

One copy an app link exposed (`pub:N`), Range-capable, `inline`, `nosniff`,
RFC 6266 filename. Behind the same PIN gate. An active kind (HTML, SVG, XML,
unknown) carries `Content-Security-Policy: sandbox; default-src 'none'…`, as
every file body filex serves from its own origin does (`httpx.ProtectServedFile`:
the preview, `/api/files/read`, a share's `?inline=1`, a shared folder's
entries).

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
never counted - a file request is a blind drop.

### `POST /api/public/d/{token}/pin` ![public](https://img.shields.io/badge/-public-lightgrey)

The same gate, the same cookie and the same statuses as the `/s/` PIN.

### `POST /api/public/d/{token}/upload` ![public](https://img.shields.io/badge/-public-lightgrey)

`multipart/form-data` with `file[]` (and `name` when `ask_name`). The PIN may
be a form field or already answered on this browser (the cookie). Same ingest,
limits, quota accounting and owner notification as the no-JS form - there is
one upload path. **401** `bad_pin` · **429** `locked` / `rate_limited` ·
**410** `expired` · **404** `not_found` (also when the link's creator no longer
holds `share.upload_links` there) · **413** `file_too_large` (the link's own
limit, or its creator's largest file) · **415** `ext_not_allowed` (outside the
link's list, or a type its creator's role blocks) · **422** `too_many_files` /
`exceeds_remaining`.

### Which answer `/s/` and `/d/` give

A `GET` whose `Accept` names `text/html` (or `application/xhtml+xml`) is a
browser navigating: it gets the **SPA shell**. Everything else gets the Go
page or the bytes:

- any other `Accept`, including `*/*` and an absent header - ⚠ this is what
  keeps `curl -O https://host/s/<token>` downloading a file rather than
  collecting an HTML page, and it is why every existing test keeps measuring
  the server-rendered pages;
- `POST` - the no-JS PIN form and the no-JS upload form submit to the same
  address;
- the `X-Filex-Pin` header;
- any of these query parameters: `nojs` (what the shell's own `<noscript>`
  points at), `zip`, `confirmed`, `pin`, `inline`, `download`, `thumb`.

When the frontend is not bundled there is no shell and every visitor gets the
Go pages, so nothing is ever unreachable.

The no-JS body of an **app** link lists the copies the app exposed as plain
links - an app surface cannot run without JavaScript, but a signer on a
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

What a person chose about the interface itself - theme, palette, density,
language - one JSON document per person **per surface** (migration 00047).

⚠ In the database and not in `localStorage`, which is per BROWSER and never
per person: a theme picked in one browser was simply not there in the other.
Distinct from `GET /api/files/manager/view-prefs`, which is how each **folder**
was left.

### `GET /api/me/prefs?surface=web` ![user](https://img.shields.io/badge/-user-blue)

`{"surface": "web", "prefs": { … }}`. `surface` is `web` or `desktop` (absent
= `web`); anything else is **400** - an unknown surface is refused rather than
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

⚠ **`openWith` is the account's, not the surface's** (0.50). GET carries the
person's "always open this kind with this app" choices as `openWith` (a JSON
string, the same on every surface); PUT drops that key, so a surface saving
its theme from a copy it read at boot cannot undo a choice made since on
another surface. The choices change through `/api/me/open-with` (below)
only.

### `GET|DELETE /api/me/open-with` ![user](https://img.shields.io/badge/-user-blue)

"Always open this kind with this app" (0.50): ONE record per account, read
and written the same by the browser, the desktop app and every embed. `GET`
→ `{"choices": {"drawio": "app:drawio/editor", "board": "builtin"}}`;
`PUT /api/me/open-with/{ext}` `{"handler": "builtin" | "app:<app>/<view>"}`
(or `"onlyoffice"` for `csv`, 0.51: [ONLYOFFICE.md → CSV files](ONLYOFFICE.md#csv-files))
keeps one (**400** `bad_kind`, `bad_handler`, `too_many_choices` past 512),
`DELETE /api/me/open-with/{ext}` forgets one, `DELETE /api/me/open-with`
forgets them all; each answers the set as it now stands. Stored as the
user_prefs row of the internal surface `account` (not a surface a client may
name in `/api/me/prefs`). Choices an earlier build kept inside a surface
document are adopted once, from the document written last. A choice is not
checked against what is installed: the explorer uses it only where that
handler is on ([APP-PLUGINS.md → Default apps](APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)).

### `GET /api/me/custom-css` ![user](https://img.shields.io/badge/-user-blue)

`{"css": "…", "enabled": true}` - the operator's stylesheet (settings key
`ui.custom_css`), already sanitised and already wrapped in its
`@scope (:root) to (.fe-css-immune)` guard, plus the state of the switch
(`ui.custom_css_enabled`, **off** unless set), so a screen can tell "off" from
"on but empty" in one call. `Cache-Control: no-store`.

⚠⚠ **Behind auth, and that is the feature.** It used to ride the public
`GET /api/branding` payload, which the sign-in page fetches before a session
exists - so an operator stylesheet could repaint the sign-in form and every
anonymous share visitor was handed it. An anonymous caller here gets **401**
and cannot even download it. `/api/me/…` rather than `/api/admin/…` because
every signed-in person wears the sheet; only *writing* it is an operator's
privilege, enforced at the settings write (`allowSettingWrite` refuses every
non-`branding.*` key to a confined tenant admin, and both keys are `ui.*`).

Reading fails soft: an installation whose settings cannot be read answers
`{}` - an unstyled panel, which is the same thing an installation with no
custom CSS already sees. See
[INTEGRATION.md → Operator custom CSS](INTEGRATION.md#operator-custom-css).

### `GET /api/appearance` ![public](https://img.shields.io/badge/-public-lightgrey)

The operator's own themes and the instance default:
`{"themes": [{"id": "custom:acme", "name": "…", "light": {…}, "dark": {…}}], "default_theme_id": "custom:acme"}`.
Public, and cached the way `/api/public/branding` is - `no-cache` with an
ETag, so a theme just composed is on the next page load and an unchanged one
costs a `304`. It is public because the sign-in page and
an anonymous share visitor are painted with it before any session exists -
they wear the instance default, never a person's own palette. A signed-in
person's choice is theirs, in [`/api/me/prefs`](#interface-preferences).

They are composed on the **Appearance** screen (`/admin/appearance`) through
`GET /api/admin/themes`, `PUT /api/admin/themes/{key}` (create or replace; the
exported theme document is the body, so an export re-imports as it is) and
`DELETE /api/admin/themes/{key}` - **supertenant only**, because themes have no
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
  emits - that is what lets a bare `<img src>` render, since it sends no
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
`?node_id=N` - the version timeline. Requires **viewer** on the file: a viewer
can already read it, so its history is not withheld from them.

### `POST /api/files/versions/snapshot` ![user](https://img.shields.io/badge/-user-blue)
`{"node_id": N}` - record the current content as a new version. Requires
**editor**: it writes an object into the node's storage.

### `POST /api/files/versions/restore` ![user](https://img.shields.io/badge/-user-blue)
`{"node_id": N, "version_id": V, "snapshot_current": false}` - copy version `V`
back over the live file. Requires **editor**.

⚠ A destructive write: it goes through the same pre-write guard as every other
write surface, so the bytes it replaces are snapshotted first and a snapshot
that cannot be taken answers **503 `SNAPSHOT_FAILED`** rather than destroying
them. `snapshot_current` only does work when that guard is off
(`FILEX_VERSIONS_ON_OVERWRITE=0`) - otherwise the guard has already taken it and
recording the same bytes twice would spend a retention slot on a duplicate.

### `DELETE /api/admin/versions/{id}` ![admin](https://img.shields.io/badge/-admin-red)
Hard-delete one version row **and** its backing `.versions/…` object.

---

## Comments

A flat, oldest-first thread on any file or folder - the **Comments** section of
a file's details. Every route takes a raw `node_id`, so, as for versions, a node
that does not exist, is in the trash, is another tenant's or lies outside a
`root:` token's folder answers the same **404**, and a node the caller cannot
see answers **403 `insufficient permission`**. A token needs `read` only: a
comment is the person's own, not a change to the file.

### `GET /api/files/comments` ![user](https://img.shields.io/badge/-user-blue)
`?node_id=N` - the live comments. Needs viewer on the node.

```json
{ "node_id": 42, "comments": [
  { "id": 7, "node_id": 42, "user_id": 3, "author_name": "Ayşe Yılmaz",
    "body": "Figures checked.", "created_at": "2026-09-30T09:12:00Z",
    "updated_at": "2026-09-30T09:12:00Z", "can_delete": true } ] }
```

`can_delete` says whether the caller may delete that one (its author or an
administrator).

### `POST /api/files/comments` ![user](https://img.shields.io/badge/-user-blue)
`{"node_id": N, "body": "…"}` → `200 {"comment": {…}}`. Needs viewer on the node
and the `comments.write` permission ([PERMISSIONS.md](PERMISSIONS.md)). `body`
is 1-5000 characters after trimming, else `400`. Emits `comment.added`
([NOTIFICATIONS.md](NOTIFICATIONS.md)) with the first 200 characters.

### `DELETE /api/files/comments/{id}` ![user](https://img.shields.io/badge/-user-blue)
Removes one comment → `200 {"ok": true}`. Its author or an administrator only
(`403` for anybody else, `404` for an id that does not exist or, in a
multi-tenant install, a comment on another tenant's file).

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

- an explorer with a healthy socket **does not poll** - the 12 s re-listing is
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

Whose ops that is: an administrator's view covers every op in reach (a
tenant's administrator the tenant's storages, the supertenant and a
single-tenant install's administrators every storage); everybody else is
shown only the ops they queued. A row names its sources and its destination,
and nothing about a storage says which of its folders a member may see. A row
that names nobody (queued before `actor_id` was recorded, or by something that
is not a person, such as a scheduled app job) is an administrator's only.

"An administrator" here is the **credential**, not the account behind it: an
administrator's session, or a token that carries the `admin` scope and no
`root:` confinement. A token minted on an administrator's account without the
`admin` scope, a `root:` token, and a session narrowed with `X-Filex-Root` are
all shown only the ops they queued - here, on `GET /api/files/ops/{id}` and on
its cancel.

A caller confined to a folder is shown, of those, only the ops whose every
path lies inside its folder (the sources and destination of a copy, move,
delete, rename, archive or app action; a restore's trash entry; an upload
commit's destination): one account commonly stands behind a `root:` token per
project, and each project sees its own. Up to v0.50.0 each saw, and could
cancel, all of them.

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

- `total` / `done` / `failed` count **sources** - the items that were selected,
  not the files inside them. Moving one large file or one folder is `0` of `1`
  until it ends.
- `bytes_total` / `bytes_done` appear only while a transfer **between two
  storages** runs: the bytes streamed so far, and the total measured by walking
  the sources beside the transfer. `bytes_total` is `0` (omitted) until that
  walk finishes, or when the tree is too large to measure; draw a moving
  indicator then, not a percentage. They are live counters in the worker's
  memory and are gone once the operation ends.
- `objects_total` / `objects_done` appear while a copy, move, delete, rename,
  restore or purge **within one storage** runs on a driver that works through
  a folder object by object (S3 and the S3-compatible stores, and the trash's
  own per-object walk). They count the objects found inside the sources and
  the objects finished, so one folder is no longer just `0` of `1`; the empty
  folder-marker objects cleaned up after a folder are not counted. `objects_total` grows as each source's
  listing arrives. Like the bytes, they are live counters and are gone once the
  operation ends. A local disk moves a folder in one rename and reports none.
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
  hold and the bytes freed so far. It is its tenant's - listed, read and
  cancelled by the administrators of the tenant that asked for it (a
  supertenant sees every one) - and it runs beside the queue, never in the
  worker's line.

### `GET /api/files/ops/:id` ![user](https://img.shields.io/badge/-user-blue)
Single op detail with **every** source (no `source_count` / `source_dir`),
plus `error` when it failed. The same ops as the list: the caller's own, or
any in reach for an administrator. Anything else answers `404`, as an id that
does not exist does, so walking the sequential ids counts nothing.

`status` is one of `pending | running | ok | failed | partial | cancelled` -
`partial` when some sources failed and others did not, `cancelled` when
somebody stopped it.

### `POST /api/files/ops/:id/cancel` ![user](https://img.shields.io/badge/-user-blue)
Stops an op: a pending one never runs, a running one stops at its next item
(an item already under way is finished). `200` with the op; `409
{ "code": "FINISHED" }` when it has already ended; `409 { "code":
"NOT_CANCELLABLE" }` for a rename, a restore or a purge that has started -
those run to their end (the row's `cancellable` is `false`), and the result is
still on its way; `404` for an op the caller cannot see - somebody else's,
unless the caller is an administrator.

---

## Admin: storages

### `GET /api/admin/storage-drivers` ![admin](https://img.shields.io/badge/-admin-red)
The config contract every registered storage driver declares: its fields, their
type, which one is the storage root, which hold credentials, defaults and an
i18n key per label. Admin UIs render their storage forms from this instead of
hardcoding a field list, and the root-path guard reads the same declaration.
`scan_fields` are the settings every storage has whatever its driver (today
`scan_exclude`); the storage form draws them, the replication-target dialog
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
> fields the plugin described - which is what lets an admin form render a
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

⚠⚠ Install, upgrade, `PATCH` and `DELETE` are **session-only**: an API key
gets `403 {"error": "session_required", "request_endpoint":
"/api/admin/plugin-requests"}` and may [leave a request](#admin-plugin-requests)
instead. Reading (the list, one plugin, the update check) and `restart` stay
open to an admin-scoped key.

### `GET /api/admin/plugins` ![admin](https://img.shields.io/badge/-admin-red)
Every registered plugin plus its live state - the row is the admin's intent,
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
      "load": { "in_flight": 0, "waited": 0, "rejected": 0, "max_in_flight": 10 },
      "source": "acme/filex-memfs",
      "update": { "checked_at": "…", "status": "available", "version": "1.1.0",
                  "sha256": "…", "platform": "linux/amd64", "notes": "…" } }
  ],
  "update_check": true,
  "updates_checked_at": "2026-09-27T03:00:00Z"
}
```
`state` is one of `running` · `starting` · `failed` · `refused` · `disabled`;
`state_error` carries the reason for the last two. `in_use` counts storages on
this plugin's driver. `source` is where newer versions are published and
`update` what the last check of it found - `status` `current` · `available` ·
`incompatible` (`requires`) · `check_failed` (`error`); both absent without a
source ([PLUGINS.md → Updates from a source](PLUGINS.md#updates-from-a-source)).
Nothing updates itself.

Top level: **`requires_signature`** says this instance refuses unsigned binaries
(trusted keys are configured), and **`conformance`** is the *mode* -
`enforce` · `warn` · `off`. Both are published so a surface can state the rules
before an install instead of after a rejection.

Per plugin: **`conformance`** is the last probe *report* - `verified`, `scratch`
(`selftest` when it ran against the plugin's own throwaway instance, `storage`
when it ran against a real storage), and one `results` entry per probe with
`status` `pass` · `fail` · `skip` and a `detail` written for the plugin's
author. **Absent means never probed** - "unverified", which is not the same as
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
Install, in one of four shapes - the Content-Type picks which (an upload or a
download may also carry `source`, kept for the daily check):

| Shape | Body |
|---|---|
| upload | `multipart/form-data` with `name`, `file` and optionally `signature` |
| download | `{"name":"…","url":"https://…","sha256":"…","signature":"…"}` - the hash is **required** |
| from its source | `{"name":"…","source":"owner/name"}` (or the https address of a `filex-storage.json`) - the build for this platform, held to the feed's SHA-256; the source is kept |
| remote | `{"name":"…","kind":"remote","address":"http(s)://…","token":"…"}` |

**201** with the same object as above. `409` when the name is taken, `400` for
a bad name (`[a-z0-9][a-z0-9_-]{0,31}`), a missing hash, a remote plugin
with no `FILEX_SECRET_KEY` configured to seal its token, or - when
`requires_signature` is true - a missing or unverifiable `signature` (a detached
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
storage built on it - remove+install would take the registration with it, and a
storage whose driver has gone cannot open.

Sequence: stop → swap the file → start → describe → conformance. **200** with
the plugin's status when the new binary comes up. Otherwise **400** with
`{"error": "…the previous one was restored", "plugin": {…}}` - the old binary is
put back and started again, and the body carries the status so a page can show
what is running now instead of leaving the operator guessing. `400` too for a
remote plugin: it is upgraded where it runs.

`{"from_source": true}` (JSON) is the administrator's approval of the newer
version the plugin's source has: the build for this platform is downloaded and
refused (`400 … sha256 mismatch`, nothing stopped) unless its SHA-256 is the
one the feed names, then upgraded as above. `400` when the source has nothing
newer, or only a version for another filex.

### `POST /api/admin/plugins/updates/check` ![admin](https://img.shields.io/badge/-admin-red)
Reads every binary plugin's source now and records what it found - it
installs nothing. **200** `{"report": {"checked_at", "checked", "available":
[names], "failed": [names]}, "plugins": [...]}`.

### `PATCH /api/admin/plugins/{id}` ![admin](https://img.shields.io/badge/-admin-red)
`{"enabled": true|false}` and/or `{"source": "owner/name"}` (`""` clears it;
`400` for anything but `owner/name` or an https address, and for a remote
plugin). Disabling unregisters the driver, so storages on it stop opening -
they are not deleted.

### `POST /api/admin/plugins/{id}/restart` ![admin](https://img.shields.io/badge/-admin-red)
Stop and start it. The way out of `refused` once the cause is fixed. The
conformance probes run again on every start, so a fixed plugin proves itself
without an extra step.

### `DELETE /api/admin/plugins/{id}` ![admin](https://img.shields.io/badge/-admin-red)
**204.** Removes the registration and, for a binary plugin, its directory.
Storages created on it are left in place.

### `GET /api/admin/storages` ![admin](https://img.shields.io/badge/-admin-red)
**Response 200** - an array, in the administrator's order (placed storages by
`sort_order`, then the rest in creation order):
```json
[
  { "id": 1, "uid": "7f3a1b2c-…", "name": "Local", "driver": "local",
    "read_only": false, "sort_order": 1, "config": { "path": "/var/lib/filex/local-storage" },
    "stats": { "file_count": 12, "total_size_bytes": 4200000 }, "last_sync_state": "ok" }
]
```

### `PUT /api/admin/storages/order` ![admin](https://img.shields.io/badge/-admin-red)
The administrator's order - the one everybody's navigation panel starts from
([STORAGE.md → Ordering storages](STORAGE.md#ordering-storages)).
**Request** `{ "ids": [3, 1, 2] }` - the whole order, first = top. The listed
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

A storage is created **enabled** unless the body says `"enabled": false`. (Until
this release a body without the key, which is what the admin panel's form sends,
created it disabled.)

`config.scan_exclude` - every driver - holds the storage's
[scan exclusions](STORAGE.md#scan-exclusions): glob patterns, one per line (a
JSON array of strings is accepted too). A pattern that would exclude
everything, a `!`, a `..`, a broken glob or too many or too long patterns
answers **400** `{"error": "SCAN_EXCLUDE_INVALID", "message": "…"}`, and an
empty or `/` root **400** `{"error": "ROOT_PATH_FORBIDDEN", "message": "…"}` -
the `message` names the pattern and is in the reader's language (server
catalogue, `server.storage.*`). The same on `PATCH`.

> ⚠ A storage on a **plugin** driver (`plugin:<name>`) is probed against this
> exact configuration *before the row is written*: filex opens the driver,
> exercises every capability the plugin declared inside a scratch folder
> (`.filex-conformance-<random>`, removed afterwards) and answers **400** with
> the failing probe if it does not hold up - including `the plugin providing
> "plugin:x" is not running` when the driver is not currently registered.
> Built-in drivers are not probed; `FILEX_PLUGIN_CONFORMANCE=off` (or `warn`)
> skips the gate. The whole check is bounded at 2 minutes, so a plugin that
> accepts connections and then says nothing cannot hang the save.
> See [PLUGINS.md → Conformance](PLUGINS.md#conformance-a-plugin-has-to-prove-its-claims).

### `PATCH /api/admin/storages/:id` ![admin](https://img.shields.io/badge/-admin-red)
Same body shape; partial updates allowed. A plugin storage is **re-probed on
every change** - the operator may have just pointed it at a different bucket,
and a configuration that half works fails the same way a half-working plugin
does: in the user's hands, looking like filex.

The change applies without a restart. The storage's scanner is rebuilt only
when the save changed something a scan reads: the driver, its configuration
(root, credentials, scan exclusions), the sync mode, the interval, or
`enabled`. A rebuild stops every scan of the storage, including one started by
`POST …/sync`, and records it as `aborted`. Renaming the storage, switching it
read-only, changing its access control or pairing a replica leaves a running
scan alone.

### `DELETE /api/admin/storages/:id` ![admin](https://img.shields.io/badge/-admin-red)
Removes the storage and its DB cache rows. Files in the underlying backend
are **not** deleted.

A large storage takes minutes to remove. The delete finishes even when the
client stops waiting, bounded at 30 minutes. If it fails, the storage keeps
its scanner, as it was before the request. The admin panel, when its request
outlives the proxy, reads `GET /api/admin/storages` again until the storage has
left it (one read per tick, shared with any scan it is following): meanwhile
the row says **Deleting…** and offers nothing but its place in the order, and
the end is said.

### `POST /api/admin/storages/:id/sync` ![admin](https://img.shields.io/badge/-admin-red)
Triggers an immediate **full** scan of the storage. The scan runs in the
background, so the answer comes at once:

```json
{ "ok": true, "status": "started", "note": "the sync runs in the background; watch its progress under sync runs" }
```

`status: "running"` (still `202`) means a scan was already walking this storage
and no second one was started. The answer comes only once the scan holds the
storage, so a second request straight after the first is answered `"running"`.

There is no run id in the answer. `GET /api/admin/storages` says whether a scan is
walking each storage right now (`running`); the run itself is under
`GET /api/admin/storages/:id/sync-runs` or `GET /api/admin/sync-runs`. The scan
belongs to the storage's scanner: a save that changes its scan settings, a
delete, or a shutdown stops it.

**`?path=<folder>` rescans one catalogued folder** instead of the whole storage -
its subtree only, with the same rules as a full scan: new objects are
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
| `200` | done - the counts above |
| `202` `{status:"running"}` | a scan is already walking the storage; nothing was started |
| `400` | the path climbs out with `..`, names filex's own trees (`.versions/`, `.thumbs/`, `.filex-trash/`), is excluded from scanning on this storage ([scan exclusions](STORAGE.md#scan-exclusions)), or is a file |
| `404` | the storage is unknown, or the folder is not in the catalogue (rescan its parent, or run a full scan) |
| `504` | ten minutes were not enough: the counts so far, the rows reached are updated, nothing was removed |

`?path=` empty, `/`, or anything that cleans to the root is the full scan above.

### `POST /api/admin/storages/test` ![admin](https://img.shields.io/badge/-admin-red)
Validates a connection without persisting. ⚠ The candidate configuration is in
the body - there is no `:id` in this path, because the usual caller is the
create form, which has no storage to name yet.

---

## Admin: plugin requests

An API key cannot install, upgrade, remove, switch or re-permission a plugin -
of either kind ([Admin: plugins](#admin-plugins),
[APP-PLUGINS.md → Admin API](APP-PLUGINS.md#admin-api)). It **leaves a
request**, and an administrator signed in to the panel approves or rejects it
(Plugins → Install requests). The model, and why:
[APP-PLUGINS.md → Install requests](APP-PLUGINS.md#install-requests).

Instance-wide like the plugins: in multi-tenant mode only the supertenant's
administrators may leave, read or decide one (`403 supertenant_only`).

### `POST /api/admin/plugin-requests` ![admin](https://img.shields.io/badge/-admin-red)
An API key (scope `admin`) or a session. filex resolves the source now - the
install review's dry run - and freezes what it answered on the request.

| Kind | Body |
|---|---|
| app from GitHub | `{"kind":"app","github_repo":"owner/name","ref":"v1.2.0","reason":"…"}` |
| app by address | `{"kind":"app","manifest_url":"https://…/filex-app.json","url":"https://…/plugin.wasm","sha256":"…","reason":"…"}` (a language pack or an interface-only app: `manifest_url` alone) |
| storage plugin from its source | `{"kind":"storage","name":"myfs","source":"owner/name","reason":"…"}` (`name` defaults to the feed's) |
| storage plugin by address | `{"kind":"storage","name":"myfs","url":"https://…/myfs","reason":"…"}` - `sha256` optional: the server downloads and hashes the binary (it does not run it) and freezes that; a `sha256` that does not match is refused `400 sha256_mismatch` |
| upgrade | `{"kind":"app"\|"storage","op":"upgrade","name":"…"}` (or `plugin_id`) - the newer version the plugin's own source has; an app may name another `github_repo` / `manifest_url` |

`reason` is required (`400 reason_required`). **201** `{"request": {…},
"created": true, "message": "…waiting for an administrator's approval…"}`;
**200** with `"created": false` and the pending request when one is already
waiting for the same source. `409 already_installed` (request an upgrade
instead), `409 incompatible`, and the install review's own refusals
(`fetch_failed`, `manifest_invalid`, …) with their codes.

A request on the wire:
```json
{ "id": 7, "kind": "app", "op": "install", "name": "sign",
  "label": { "en": "e-Signature", "tr": "e-İmza" },
  "source_kind": "github", "source": { "github_repo": "BRF-Tech/filex-sign", "ref": "v0.1.1" },
  "version": "0.1.1", "sha256": "4ddd…", "manifest_sha256": "…",
  "permissions": ["files:read", "…"],
  "permission_rows": [{ "id": "files:read", "label": "Read your files", "reason": { "en": "…" } }],
  "requested_by": 3, "requester": "Ayşe", "token_label": "work-agent",
  "reason": "…", "status": "pending",
  "expires_at": "2026-10-12T10:00:00Z", "created_at": "2026-09-28T10:00:00Z" }
```
`status` is `pending` · `approved` · `rejected` · `expired` · `superseded`;
once decided, `decided_by` / `decider` / `decided_at`, `decision_note` (a
rejection's reason, or why it was superseded) and - approved - `result`, the
installed plugin. `permission_rows` labels each permission in the reader's
language and carries the app's own reason.

### `GET /api/admin/plugin-requests` ![admin](https://img.shields.io/badge/-admin-red)
`?status=pending` (the default) · `approved` · `rejected` · `expired` ·
`superseded` · `all`. **200** `{"requests": [...], "ttl_days": 14}`, newest
first. A pending request past its expiry is closed as `expired` before the
answer (and hourly).

### `GET /api/admin/plugin-requests/{id}` ![admin](https://img.shields.io/badge/-admin-red)
**200** `{"request": {…, "manifest": …, "review": …}}` - the frozen manifest
(an app's `filex-app.json`, a storage plugin's feed) and the review the dry
run gave.

### `POST /api/admin/plugin-requests/{id}/approve` ![session](https://img.shields.io/badge/-admin%20session-darkred)
Installs exactly what the request froze: the source is fetched again, the
bytes are held to the frozen SHA-256 (an app's manifest to its own), and the
grant is the frozen permission list. **200** `{"request": {…approved},
"plugin": {…}}`. **409 `superseded`** with the closed request when the source
now serves anything else - nothing is installed. `409 not_pending` for a
request already decided or expired. Any other failure (a network error, a
name taken since) leaves the request pending, with the refusal in `result`.
An API key: `403 session_required` - there is no key door and no MCP tool.

### `POST /api/admin/plugin-requests/{id}/reject` ![session](https://img.shields.io/badge/-admin%20session-darkred)
`{"reason": "…"}` (optional). **200** with the rejected request. Session only.

## Admin: encryption policy

The administrator's half of [who may encrypt](E2E-ENCRYPTION.md#who-may-encrypt):
the tenant's policy, the requests the `approval` policy leaves and - for the
platform operator - each tenant's ceiling.

Writes need an administrator signed in to the admin panel; an API key gets
`403 session_required`. That includes writing the `e2e.policy` setting - a
single-tenant install's policy - through `/api/admin/settings`,
`/api/ai/admin/settings` and the `admin_settings_*` MCP tools, which reach the
same handlers: a batch that holds it is refused whole. From a session the
setting takes one of the four policies (`400 invalid_policy` otherwise) and is
audited as `e2e_policy.update`, the same as the page's own `PATCH`.

### `GET /api/admin/e2e` ![admin](https://img.shields.io/badge/-admin-red)
`{ "available": true, "policy": "permitted", "scope": "tenant" | "instance", "tenant": { "id": 3, "name": "…" } | null, "pending": 0 }` -
in multi-tenant mode every administrator reads their own tenant
(`scope: "tenant"`; the platform operator reads the supertenant's row); on a
single-tenant install the instance policy (`scope: "instance"`, setting
`e2e.policy`). `available` is the tenant's ceiling, and `pending` the number of
requests waiting for this administrator's answer, counted among the newest
500 (for the platform operator, the platform's own: another tenant's is not
theirs to answer). An account whose tenant cannot be
resolved (a broken tenancy record) is `403 no_tenant`, here and on `PATCH`.

### `PATCH /api/admin/e2e` ![session](https://img.shields.io/badge/-admin%20session-darkred)
`{ "policy": "off" | "admins" | "permitted" | "approval" }` → the same shape.
A body that is missing, not JSON or without `policy` is `400 bad_request`; a
`policy` that is not one of the four is `400 invalid_policy`. The policy is written as its own column: a
tenant administrator's save can never put back a ceiling the operator switched
off meanwhile. Saving the value that is already stored writes nothing, and
leaves no audit row. An unknown tenant is `404`; a lookup that fails is `500`,
and logged.

### `GET /api/admin/e2e/tenants` ![admin](https://img.shields.io/badge/-admin-red)
Supertenant only (`403 supertenant_only` for a tenant's own administrator).
`{ "multi_tenant": true, "tenants": [{ "id", "slug", "name", "is_supertenant", "e2e_allowed", "e2e_policy" }] }`;
a single-tenant install answers its one tenant with `"multi_tenant": false`.

### `PATCH /api/admin/e2e/tenants/{id}` ![session](https://img.shields.io/badge/-admin%20session-darkred)
Supertenant only (`403 supertenant_only`). `{ "e2e_allowed": false }` → the
tenant row. The ceiling is written as its own column, so it never undoes a policy the tenant's
administrator saved meanwhile, and saving the value already stored writes
nothing. `409 single_tenant` on a single-tenant install, where no ceiling is
read (a stored `false` would switch encryption off the day multi-tenant mode is
turned on); `404` for an unknown tenant; `500`, logged, for a lookup that
failed.

### `GET /api/admin/e2e/requests?status=pending|all` ![admin](https://img.shields.io/badge/-admin-red)
`{ "requests": [...], "ttl_days": 7 }`, newest first and at most 500 - a
tenant's administrator sees their tenant's; the platform operator every
tenant's, each with `decidable` (only the platform's own is the operator's to
answer). `status` is `pending` (the
default), `approved`, `rejected`, `expired`, `used` or `all`; anything else is
`400 bad_request`. A request past its 7 days is closed as `expired` before the
answer (and hourly).

### `POST /api/admin/e2e/requests/{id}/approve` ![session](https://img.shields.io/badge/-admin%20session-darkred)
`{ "note": "…" }` (optional) → `{ request }`. The approval is good for that
person, folder and kind **once**, for 7 days from now. `409 not_pending` for a
request that was answered already, or whose 7 days passed - it is closed as
`expired`, not approved; another tenant's request is `404 not_found` for a
tenant's administrator and `403 not_decidable` for the platform operator, who
sees it but leaves it to that tenant; an API key gets `403 session_required`.

### `POST /api/admin/e2e/requests/{id}/reject` ![session](https://img.shields.io/badge/-admin%20session-darkred)
`{ "reason": "…" }` (optional here; the panel asks for one, because the
requester reads it) → `{ request }` (`note` is accepted too). Same refusals.

## App plugins

The sandboxed WebAssembly apps of [APP-PLUGINS.md](APP-PLUGINS.md). The admin
routes are listed there ([Admin API](APP-PLUGINS.md#admin-api) - install,
upgrade, `PATCH /{id} {enabled}`, `POST /{id}/rollback`, `POST /updates/check` and the
rest; [APP-PLUGINS-API.md](APP-PLUGINS-API.md) has the shapes); these are the
ones the explorer and the public page call. All under the user block unless marked public. Every answer when the
runtime is off: `404 app_plugins_disabled`.

### `GET /api/files/plugins/actions` ![user](https://img.shields.io/badge/-user-blue)

The rows this caller may see: `{actions: [{plugin, id, key, label, icon,
applies, view, view_placement, confirm, min_role, danger, output_mode,
requires}], views: [{plugin, id, placement, label, icon, applies, requires}]}`.
`key` is `plugin:<plugin>/<action>`; `applies` is the manifest rule merged
with the admin override; admin-only actions are absent for non-administrators,
`hidden` actions are absent for everyone (a surface starts those), and an
action or view that `requires` an app permission (`app.<app>.<id>`) is absent
for a caller who does not hold it
([PERMISSIONS.md → App permissions](PERMISSIONS.md#app-permissions)). The
explorer mirrors `applies` client-side - including `state` / `no_state`,
which it matches against the row's `app_state` - and the server re-checks on
run. `view_placement` is `modal` (dialog) or `page` (the view opens as a full
page in a new tab). 0.50: `open_rules` - the administrator's order for the
handlers that open a kind, `{ext: {order, off}}`, which the explorer applies
to the `viewer` views and filex's own viewer
([APP-PLUGINS-API.md → Default apps](APP-PLUGINS-API.md#default-apps-050)).

### `POST /api/files/plugins/actions/{plugin}/{action}/run` ![user](https://img.shields.io/badge/-user-blue)

`{"paths": ["docs://reports/nda.pdf"], "params": {…}}` (adapter-qualified,
one storage; `{"storage_id", "paths"}` is accepted too). Checks, in order:
storage ownership, a `root:` token's folder (asked before the storage is read,
so outside it a storage that exists and one that does not are the same `403`;
the job is held to that folder when it runs too - what `state_list` tells it,
the files it may name by path),
ACL ≥ viewer per path (≥ editor when the action writes,
`min_role` raises it; a file locked by THIS app is judged at the level the
caller would have without the lock), read-only storage → `409 read_only`,
encrypted folder → `403 encrypted`, the applies rule - state keys included -
against the real files → `422 not_applicable`. An action that `requires` an
app permission the caller does not hold answers `403 permission_denied` with
`permission: "app.<app>.<id>"` before any of that - as do a view's opening
and events, and an interface's `save` and `call`. A hidden action answers `404`
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
from the effective mode. A screen opened on no file (a home page,
`?storage_id=` with no path) opens for a `root:` token only on its root's own
storage; any other `storage_id`, there or not, answers the `403` every app
door gives a path outside the root (0.52.0).

### File locks ![user](https://img.shields.io/badge/-user-blue)

An app holding `files:lock` may freeze one file (`file_lock` host function).
While a lock is live every caller's effective level on that path is capped at
viewer - administrators included - so listings carry `locked: true` and
`lock: {plugin, reason?, until?}` with `perm: "viewer"`, and renaming, moving
or deleting the file, or any folder above it, answers `423 {"error":
"locked", "plugin", "path", "reason", "until"}`. An upload that would overwrite the locked file is refused the same
way; uploads of OTHER names into the same folder and every sibling are
unaffected, and the locking app's own jobs still write.
It holds at **every** door, not only the explorer's: the document editor's
save (a document opened before the freeze and saved after it is refused),
WebDAV (423 Locked), SFTP, FTPS and NFS (their permission error - also for
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

### `GET /api/files/plugins/license/{plugin}` ![user](https://img.shields.io/badge/-user-blue)

What an app reads about its own license (0.52.0, `plugins.run`; the bridge's
`license.get`): `{status, valid_until?, updates_until?}`, `{status: "free"}`
for an app that is not paid; `404` for no app of that name. Never the key, the
licensee or the store ([APP-PLUGINS-API.md → The app reads its
license](APP-PLUGINS-API.md#the-app-reads-its-license)).

A plugin job is cancelled like any other op:
[`POST /api/files/ops/:id/cancel`](#post-apifilesopsidcancel-) (`409 FINISHED`
once it has ended).

### Ops rows for plugin jobs

`GET /api/files/ops` rows with `kind: "plugin-action"` carry `plugin`,
`action`, `label` (in the caller's locale), `message` (the last progress
message or the app's result) and, once committed, `outputs: [{path}]`
(adapter-qualified). Progress rides on `bytes_done` / `bytes_total`.
Statuses: `pending | running | ok | failed | partial | cancelled`.

### An app's public page IS a share

`share_create` opens a **real share**, so the visitor's link is `/s/<token>`
like every other public link - one revoke list, one expiry policy, one PIN
implementation, one visit counter. The JSON a visitor's browser talks to is
[The public surface](#the-public-surface); `/api/p/*` and `/p/*` are retired
and **301** there.

The share row carries `plugin_id`, `page_id`, `subject` and the app's own
`state_json` + `files_json` (migration 00046). `share_revoke` is the share's
revoke; `share_state` reads and replaces the app's record for one link.

A share points at a node, and a job's **output** has none until `runJob`
commits it - after the plugin has returned. So a `share_create` naming one of
this job's outputs is *promised*: the token, PIN and expiry are decided and
answered at once (`share.NewToken` + `CreateOpts.Token`), and the row is
written by `keepPromisedShares` when the bytes are committed and the node
resolved. A job that fails, or that never keeps the output, writes no row -
the link is created late rather than bound late, so there is never a share
pointing at nothing (`wasmplugin/public_promised.go` says why at length).

### `GET /api/admin/app-plugins/shares` ![admin](https://img.shields.io/badge/-admin-red)

The links apps opened - `?plugin=<name>` for one app's table, `?active=true`,
`?limit=`, `?offset=`. Same rows, same envelope and same tenant filter as
`GET /api/admin/shares`, which carries `plugin_name` and `share.page_id` on
every row so the Shares table can show a plugin/page column without a lookup
per row. An app that is not installed answers an empty page, never a 404, so a
panel polling one app keeps working through an uninstall.

### `GET /api/admin/file-types` · `PUT|DELETE /api/admin/file-types/{ext}` ![admin](https://img.shields.io/badge/-admin-red)

Default apps (0.50): which handler opens a kind of file and which draws its
thumbnails, per kind. `GET` answers `{kinds, enabled, editable}`; `PUT`
takes `{open?, thumbnail?}` (each `{order, off}`, or `null` for the default),
`DELETE` puts both back. Supertenant only; a change needs an administrator
signed in to the panel and is refused on a demo; audited
`file_association.update` / `.reset`. Shapes:
[APP-PLUGINS-API.md → Default apps](APP-PLUGINS-API.md#default-apps-050).

## Admin: users

### `GET /api/admin/users` ![admin](https://img.shields.io/badge/-admin-red)
List users. In multi-tenant mode the list is confined to the caller's tenant
(the supertenant sees all). Each row carries `used_bytes` and `quota_bytes`
(`0` = unlimited) and `enabled`, so a usage table costs one call. Since 0.50 a
row also carries `disabled_reason` when the server switched the account off
(`pending_approval`: an SSO sign-in opened it and its identity provider did not
confirm the address) and `sso_linked: true` when it is bound to an SSO identity
(the identity itself is never sent) - [SSO.md](SSO.md#which-account-an-sso-sign-in-opens).

### `GET /api/admin/users/{id}` ![admin](https://img.shields.io/badge/-admin-red)

### `POST /api/admin/users` ![admin](https://img.shields.io/badge/-admin-red)
Creating an account with `"role": "admin"` needs an administrator signed in to
the panel: an API key gets `403 session_required`
([RBAC.md](RBAC.md#administration-and-plugins-need-a-session)).
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
and it signs in through SSO - found by the SSO identity it is bound to, or by
its email address inside its tenant until it is bound
([SSO.md](SSO.md#which-account-an-sso-sign-in-opens)) - or with an API token. `POST /api/admin/users/{id}/reset-password` gives it one later; the
answer carries the value once, as `new_password`.

`provider_id` homes the user in a tenant. Omit it and the user lands in the
**caller's** tenant. A tenant admin may only name their own provider (`403`
otherwise); an id that matches no provider is `400`. There is no foreign key
behind the column, so it is validated here.

### `PATCH /api/admin/users/{id}` ![admin](https://img.shields.io/badge/-admin-red)
Partial update - only the fields present in the body are touched:
`password`, `display_name`, `role`, `locale`, `timezone`, `enabled`,
`provider_id`, `sso_unlink`.

`enabled: true` on an account waiting for approval (`disabled_reason:
pending_approval`) approves it; switching an account on or off clears the
server's reason.

`sso_unlink: true` removes the account's SSO bind (issuer and subject): its
next SSO sign-in is matched by its email address again, as a first sign-in is
([SSO.md](SSO.md#removing-an-accounts-sso-bind)). The request's audit row
carries `sso_unlinked` (whether there was a bind). On an administrator it needs
a session, like setting an administrator's password.

`enabled: false` cuts access without deleting anything: the account cannot
log in (local, OIDC or `/dav`), existing sessions stop working, and every API
token it minted is refused. Files, quota and grants are untouched. Disabling -
like deleting or demoting - the **last admin** is refused with `409`.

`provider_id` re-homes the user into another tenant. Restricted to an
unscoped or supertenant caller (`403` otherwise).

⚠ Promoting an account to `admin`, changing an administrator's `role`,
setting an administrator's `password` and removing an administrator's SSO bind
need an administrator signed in to the panel: an API key gets `403
session_required`. So does
`POST /api/admin/users/{id}/reset-password` on an administrator. The same
holds on `/api/ai/admin` and the `admin_users_*` MCP tools; a role left as it
was is not a role change.

### `GET|POST|PATCH /api/admin/users/{id}/quota` ![admin](https://img.shields.io/badge/-admin-red)
Read or set one user's quota - see [Admin: quota](#admin-quota). `POST
/api/admin/users/{id}/quota/recompute` rebuilds `used_bytes` from node sizes.

### `DELETE /api/admin/users/{id}` ![admin](https://img.shields.io/badge/-admin-red)
Deletes the account row. The last remaining admin cannot be deleted (`409`),
not even by itself.

**What happens to their files: nothing.** No storage object is ever removed.
The node rows survive and `nodes.owner_id` becomes `NULL`, so the files
become **unowned** - still present, still listed, still reachable by anyone
whose access does not depend on that user. Deletion is not a way to reclaim
space; move or delete the files first if that is the intent.

Precisely, on `DELETE`:

| Kept, with the user dropped (`SET NULL`) | Removed with the user (`CASCADE`) |
| --- | --- |
| `nodes.owner_id` - the files themselves | `sessions` |
| `shares.created_by` of an app's own public page - see below | `api_tokens` |
| `file_grants.created_by` | `file_grants.user_id` - access granted **to** them |
| `audit_log.user_id` - history stays readable | `notifications`, `user_node_meta`, `node_comments` |
| | `shares` the user created - download links and file requests (0.49.0) |

**The links the user opened are deleted with the account**, in the same
transaction (`db.Store.DeleteUser`, so a tenant deletion and every other path
that deletes an account do the same), and the audit entry of the deletion
records `links_closed` - how many of them were still open. They used to stay
with `created_by` set to `NULL`, and a link with no creator has nobody to ask
whether it may still answer ([SHARING.md → A link follows its creator](SHARING.md#a-link-follows-its-creator)).
An **app's own public page** (a signing page) stays, with `created_by`
cleared: the app opened it. Links left without a creator by a deletion before
0.49.0 still answer; revoking one looks at `created_by`, which matches nobody,
so only an admin revokes it, via `DELETE /api/admin/shares/{id}`.

Their `usage_bytes` row goes with the account, so those bytes stop counting
toward any per-user total while the files remain on storage. To keep the
account's history and quota intact, prefer `enabled: false` over deletion.

---

## Admin: roles and permissions

What each account may **do** - the 29 permissions, the built-in and custom
roles, a person's exceptions and the permissions installed apps declare. The
model, the limits and every body are in
[PERMISSIONS.md → API](PERMISSIONS.md#api); the routes:

| Route | Who | |
|---|---|---|
| `GET /api/auth/me/permissions` | any signed-in caller | the caller's effective permissions, each with its source, the role's limits, role ids |
| `GET /api/admin/roles/catalogue` | `admin.users` | `{permissions, presets, apps}` - `apps` lists every installed app's own permissions: `{key: "app.sign.request", app, app_label, id, label, description, default, default_for}` - `default_for` is what `default` comes to on each built-in role when nobody has decided (`{"viewer": false, "user": true, "admin": true}`, perm.AppDefaultFor), which the role editors' *Default (…)* reads |
| `GET /api/admin/roles/exceptions` | `admin.users` | user id → that account's exceptions |
| `GET` · `PUT /api/admin/users/{id}/exceptions` | `admin.users` | `{"overrides": {"files.delete": "deny", "app.sign.request": "allow"}}`, the whole map; the answer's `effective.apps` is each app permission's answer and `inherited` (without the person's exception) |
| `GET` · `PUT /api/admin/users/{id}/roles` | `admin.users` | the person's one role: `{"role_id": 3}`, `{"role_id": null}` or `{"role": "viewer"}`; the answer also carries `group_role: {role_id, group_id, group_name}` - the role a group gives them when they have none of their own, else `null` |
| `GET /api/admin/roles` | `admin.users` | the custom roles and who holds which; also `group_assignments` - user id → the role a group gives them |
| `GET /api/admin/roles/builtin[?role=viewer]` | `admin.users` | `{permissions, preset, apps}` - `apps` is the built-in role's decisions about app permissions |
| `PUT /api/admin/roles/builtin[?role=viewer]` | admin (multi-tenant: the supertenant's) | `{"permissions": […], "apps": {"app.sign.request": "deny"}, "shown": […]}` - `apps` absent keeps them, `{}` hands every one back to the app's default; `shown` is what the editor showed (perm.NoteGapsSaved). A key the stored list holds that this version does not know is kept. A tenant's admin gets `403 supertenant_only` |
| `GET /api/admin/roles/gaps` | admin | `{"gaps": [{id, key, from, role \| rule_id, rule_name}]}` - the roles the caller may edit that allow `files.create` but not `files.encrypt` and nobody dismissed (perm/gaps.go) |
| `POST /api/admin/roles/gaps/restore` · `POST /api/admin/roles/gaps/dismiss` | admin (a built-in role's: the supertenant's) | `{"id": "builtin:user:files.encrypt"}` → the gaps left; restore adds the key (or an Allow of it to the folder part), dismiss records it as on purpose. `404 gap_gone` otherwise |
| `POST /api/admin/roles` · `PUT` · `DELETE /api/admin/roles/{id}` | admin | a custom role; its `settings.apps` holds its app decisions, `names` / `descriptions` its name and description in other interface languages. `DELETE …?to=user\|viewer\|<id>` moves its people |
| `POST /api/admin/roles/preview` | admin | `{permissions, effects, conditions}` → `{"holder_role": "user" \| "viewer"}` - the built-in role a role being edited would put its people on; nothing is checked or stored |

⚠ An API key gets `403 session_required` where a change would make an account
an administrator in all but name: a role to or from Administrator, and an
exception, a custom role or a built-in role that **allows** an `admin.*`
permission. Taking one away stays open to a key. A delegated administrator
cannot change a person's `app.*` exceptions (`403`).

---

## Admin: groups

A group is a named set of people in one tenant; it can be given folder access
and a role ([GROUPS.md](GROUPS.md)). The routes - bodies and answers are in
[GROUPS.md → API](GROUPS.md#api):

| Route | Who | |
|---|---|---|
| `GET` · `POST /api/admin/groups` | `admin.users` | the tenant's groups; create one |
| `GET` · `PUT` · `DELETE /api/admin/groups/{id}` | `admin.users` | one group, with its members and its folder grants |
| `POST /api/admin/groups/{id}/members` · `DELETE …/members/{user_id}` | `admin.users` | add people (`{"user_ids": [3, 4]}`), remove one |
| `GET /api/admin/users/{id}/groups` | `admin.users` | the groups one person is in |
| `DELETE /api/admin/grants/groups/{id}` | `admin.grants` | revoke a group's folder grant - numbered apart from a person's |
| `GET /api/files/permissions/groups?q=` | signed in | the groups the caller could share with |
| `PATCH` · `DELETE /api/files/permissions/groups/{id}` | owner + `share.users` | change or revoke a group's grant from the sharing panel |

A folder is shared with a group through `POST /api/files/permissions` with
`group_id` in place of `user_id`.

---

## Admin: tenants

Multi-tenant installs. A tenant is a provider row, managed at
`/api/admin/providers` by the platform operator only: any other caller gets
`403 supertenant_only`. The model and the lifecycle:
[MULTI-TENANCY.md → Tenant lifecycle](MULTI-TENANCY.md#11-tenant-lifecycle).

| Route | |
|---|---|
| `GET /api/admin/providers` | `{providers, multi_tenant}` - every tenant, each with `storage_ids` and `user_count` |
| `GET /api/admin/providers/{id}` | one tenant |
| `GET /api/admin/providers/realm-suggestion?slug=` | the realm the tenant screen offers for a slug: marks dropped, the first free variant ([TENANT-ADMIN.md](TENANT-ADMIN.md#the-tenant-screen)) |
| `POST /api/admin/providers` | create a tenant - the provider row only |
| `PATCH /api/admin/providers/{id}` | edit one; `is_supertenant: true` moves the platform flag to it; `oidc_trust_email` is its own OIDC's "trust this provider's email addresses" ([SSO.md](SSO.md#trust-this-providers-email-addresses)), audited when it changes; the answer's `oidc_trust_email_by_upgrade` says the value is the one the upgrade to 0.50 set |
| `DELETE /api/admin/providers/{id}` | `409` while it has users, unless `?force=1`, which deletes its users too. Storages are unlinked, never deleted |
| `POST /api/admin/providers/{id}/storages` · `DELETE …/storages/{storageID}` | link a storage (`{"storage_id": 4}`), unlink one |

`realm` is read on create only - the slug when none is given. Refused:
`400 realm_invalid`, `400 realm_reserved`, `400 realm_empty`,
`409 realm_taken`. A `PATCH` may send the tenant's own realm back; any other is
`400 realm_immutable`. The supertenant cannot be deleted, disabled or
un-flagged (`400`). A tenant's first administrator is made with
`POST /api/admin/users` and its `provider_id`.

---

## Admin: routes described on their own page

These routes are described, bodies and answers, on the page of the feature they
serve; the table says where. Each also answers under `/api/ai/admin` for an
admin-scoped key where its page says so.

| Routes | What | Described in |
|---|---|---|
| `GET` · `POST /api/admin/auth-providers`, `PATCH` · `DELETE …/{name}`, `PUT …/{name}/tenants`, `POST …/{name}/test`, `GET` · `POST …/{name}/sync` | the sign-in providers: list, another instance of a driver, change, delete, the tenants that sign in through one, the test, an LDAP directory sync's state and starting one (`POST` 202, 409 while one runs; an administrator signed in to the panel, an API key is refused - [LDAP.md](LDAP.md#directory-sync)). An OIDC's `config.trust_email` ([SSO.md](SSO.md#trust-this-providers-email-addresses)); a provider's `set_by_upgrade` names the fields whose value the upgrade to 0.50 set | [TENANT-ADMIN.md](TENANT-ADMIN.md#sign-in-providers-bound-to-tenants), [OS-LOGIN.md](OS-LOGIN.md#api), [SSO.md](SSO.md), [LDAP.md](LDAP.md) |
| `GET /api/admin/tenant`, `POST /api/admin/tenant/auth-providers`, `PATCH` · `DELETE …/auth-providers/{name}`, `POST …/auth-providers/{name}/test`, `POST /api/admin/tenant/domains`, `POST …/domains/{id}/check`, `PUT` · `DELETE …/domains/{id}/certificate`, `DELETE …/domains/{id}`, `PUT /api/admin/tenant/insecure` | a tenant's own screen: its own OIDC and LDAP, its domains and certificates, the operator's insecure switch | [TENANT-ADMIN.md](TENANT-ADMIN.md#the-tenant-screen) |
| `GET /api/tls/ask`, `GET /api/tls/certificate` | the reverse proxy's questions about the tenants' addresses (`FILEX_TLS_MODE=proxy`); the proxy itself only | [TENANT-ADMIN.md](TENANT-ADMIN.md#tls-an-installation-setting-plus-a-tenants-own-certificate) |
| `GET /api/auth/methods?realm=` | public: how a sign-in for the address's tenant (or the realm named) may go - the password form, the recovery form, its SSO buttons | [TENANT-ADMIN.md](TENANT-ADMIN.md#the-sign-in-page-per-realm) |
| `GET` · `POST /api/admin/tools/thumbnails/repair`, `GET …/problems`, `GET …/generators`, `GET` · `PATCH …/settings` | Admin → Tools → Thumbnail repair, the files without a thumbnail, who drew what, folder previews and the SVG limits | [thumbnails.md](thumbnails.md#admin--tools--thumbnail-repair) |
| `GET /api/admin/plugins/{id}/logs`, `GET /api/admin/app-plugins/{id}/logs` | a storage plugin's log, an app's log | [PLUGINS.md](PLUGINS.md#plugin-log), [APP-PLUGINS.md](APP-PLUGINS.md) |
| `GET` · `POST /api/admin/app-plugins/stores`, `DELETE …/stores?store=` | the app stores this filex trusts (trust on first use names the key fingerprints shown, or `FILEX_APP_STORE_URLS`); a signed-in platform administrator only, an API key of any kind `403 session_required` | [APP-PLUGINS.md](APP-PLUGINS.md#trusted-stores), [APP-PLUGINS-API.md](APP-PLUGINS-API.md#filexs-side-apiadminapp-plugins) |
| `POST /api/admin/app-plugins/store-intent`, `POST …/store-intent/install`, `POST …/store-intent/cancel` | a store's install link: its review (or the trust question), the install of what it names, its cancellation; the same gate | [APP-PLUGINS.md](APP-PLUGINS.md#installing-from-a-store), [APP-PLUGINS-API.md](APP-PLUGINS-API.md#the-store-contract-0520) |
| `GET /api/admin/app-plugins/licenses`, `GET` · `PUT /api/admin/app-plugins/{id}/license`, `POST …/{id}/license/verify` | paid apps' licenses: the status and the facts (never the key), a new key, a check now; the same gate | [APP-PLUGINS.md](APP-PLUGINS.md#paid-apps) |
| `GET` · `PATCH /api/admin/archives`, `POST /api/admin/archives/test` | **Settings → Archives**: the live archive policy, the providers' status, an encrypted round trip (platform operator only) | [ARCHIVES.md](ARCHIVES.md#process-configuration) |
| `GET /api/files/onlyoffice/diagnose?path=` (or `?id=`) | what filex last answered the document server for a document, and when its editor was last opened - this process only | [ONLYOFFICE.md](ONLYOFFICE.md#failure-editor-shows-download-failed) |

---

## Admin: quota

Quota is **per user**. There is no per-provider (tenant) quota - see
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
`unlimited: true` - that is not an error. An id that names no user is `404`.

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

"How full is this drive", for somebody who is not an administrator - the same
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
storage the caller cannot open is not reported at all** - not its size, not its
name, not a zero row. Tenancy is closed before that (the handler reads the
tenant-scoped store), and a **root-confined** caller - an API token carrying
`root:<adapter>://<rel>`, or a trusted proxy's `X-Filex-Root` - is looking at a
folder rather than a drive, so it is answered with the storage only when the
confinement is the storage root, and with an empty list otherwise.

Cost: one `COUNT(*) + SUM(size)` aggregate per visible storage, memoised
process-wide for 15 s and keyed by storage id, so a page that shows every drive
costs one pass per drive per quarter-minute no matter how many people have it
open. A storage whose count fails is omitted rather than reported as `0`; the
caller's card falls back to naming the kind of thing. Its readers: Home's drive
cards, the explorer's storage line for a person without a quota (only for
drives the host sent no size for), and the admin top bar's storage chip, which
polls it every 60 s - all inside that one cache.

---

## Admin: external services

⚠ **Instance-wide, and in multi-tenant mode supertenant-only.** There is one document server, one converter, and one shared JWT secret behind them, so this surface decides where every tenant's documents are sent and what credential signs the handoff. A
tenant admin gets `403 supertenant_only` on **every** verb here, reads
included. Single-tenant installs are unaffected - the ordinary admin still
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
Secrets are never returned - a configured one reads `"***"`.

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

**Response 200** - `{ "ok": true, "env_managed": false }`. When `env_managed`
is true it also carries `env_var` (`FILEX_ONLYOFFICE_URL`, `FILEX_DRAWIO_URL`)
and a `note`: the change, switching the service off included, lasts until
filex restarts, and removing the variable switches it off for good. The audit
row (`external.update`) carries `enabled`, and `env_managed` with the same
`note`.

The change is live: the running process reads this row on every use, so the
editor, the diagram embed and the converter pick it up on the next request. No
restart.

### `POST /api/admin/external/:name/test` ![admin](https://img.shields.io/badge/-admin-red)
Probes the service's health endpoint - `${url}/healthcheck` for `onlyoffice`,
`${url}/healthz` for `convert`, the bare URL for `drawio` - with a 3 s timeout,
stores the verdict on the row and returns
`200 + { ok, name, reachable, url, state, detail }`. An unknown name is `404`.

The body is optional. With none, the **saved** configuration is tested. With
`{ "enabled", "url", "secret", "callback_url" }` (any of them, the same fields
as `PATCH`), those values are laid over the saved row and tested **as they
are, without saving anything**: the row's verdict is not touched, the running
configuration is not changed, and the answer carries `"unsaved": true` when
they differ from the row. An empty `secret` (or `"***"`) keeps the stored one.
An address that is not one is `400 url_invalid`, as on `PATCH`. This is what
the admin page's **Test now** sends - the values in the form - so typing a new
address and pressing Test tests that address, not the saved one (since 0.50.0;
the MCP tool `admin_external_test` takes the same optional body).

`detail` is what the probe saw when the answer was not a healthy one, and is
empty otherwise: `GET <url>/healthcheck returned HTTP 502`, `… no answer
within 3s`, or the connection error (`… connection refused`, a DNS or TLS
failure). For `onlyoffice`, a 502/503/504 adds that the Document Server's web
server answered but its docservice did not - the network is fine and the fix
is inside that container.

⚠ Reachable is not the same as configured: OnlyOffice also needs a JWT secret,
and a Document Server with no secret set in filex answers this probe happily
while refusing every editor session.

For `onlyoffice`, when the server leg is healthy, `service_to_filex` measures
the Document Server's route back: it is handed a one-shot URL on
`/api/files/onlyoffice/fetch`, signed and checked like a document's, and the
object says `checked`, `ok` (the request arrived **and** filex served it),
`url`, the Document Server's error `code`, `detail`, `advice` (`check_jwt`,
`jwt_off`, `route` or `filex_refused`) and `jwt_enforced`: the answer to a
second conversion request sent **without** a token (`true` when it was refused
with -8, `false` when it was taken, absent when it could not be told). A
Document Server that took it adds the `jwt_not_enforced` warning to
`advisories` ([ONLYOFFICE.md](ONLYOFFICE.md#three-machines-three-addresses)).

---

## Admin: protection & antivirus

⚠ **Instance-wide, and in multi-tenant mode supertenant-only.** Every value here is a single global row: switching antivirus off, or pointing clamd elsewhere, does it for the whole instance. The read is gated too - it returns the clamd address in force and a live reachability probe. A
tenant admin gets `403 supertenant_only` on **every** verb here, reads
included. Single-tenant installs are unaffected - the ordinary admin still
administers everything. See
[MULTI-TENANCY.md](MULTI-TENANCY.md#instance-wide-admin-surfaces).

### `GET /api/admin/protection` ![admin](https://img.shields.io/badge/-admin-red)
Returns the trash-retention window, the version keep count, the share-link life
ceiling, the drafts limit (`drafts_limit`, with `drafts_limit_min` /
`drafts_limit_max`) and the whole `antivirus` block - the switch, the mode, the clamd
address, the size ceiling, the editor save-scan window - plus a **status**
sub-object describing what this process is actually doing: what would answer
(`clamscan` / `clamdscan` / `clamd`), whether it is `reachable`, its version,
and `restart_pending`.

⚠ `restart_pending` is true for as long as the stored configuration differs
from what the running process booted with. The switch, the mode and the address
take effect **at the next restart, in both directions**; the size ceiling and
the save window apply to the next file scanned.

### `PATCH /api/admin/protection` ![admin](https://img.shields.io/badge/-admin-red)
Partial update; echoes the fresh `GET` shape. Values are validated on save
rather than clamped later - an out-of-range number or an address like
`clamav 3310` is a `400`, and `daemon` mode with no address is refused as well.

⚠ The scanner **binary** is deliberately not settable here: it is a path this
server executes, so an admin-writable field would turn an admin account into
arbitrary command execution. It stays in `FILEX_CLAMAV_BIN`. Full semantics:
[PROTECTION.md](PROTECTION.md).

---

## Admin: sign-in security

⚠ **Instance-wide, supertenant-only** in multi-tenant mode, on every verb: a
tenant's administrator gets `403 supertenant_only`. The limits, the allow-list
and the locks apply to every tenant's sign-ins. Also
mounted at `/api/ai/admin/login-security` (admin-scoped token) and as the MCP
tools `admin_login_security_get` / `_update` / `_locks` / `_unlock` /
`_attempts`. What the limit does and why: [CONFIGURATION.md → Sign-in attempt
limits](CONFIGURATION.md#sign-in-attempt-limits).

### `GET /api/admin/login-security` ![admin](https://img.shields.io/badge/-admin-red)
```json
{
  "settings": {
    "enabled": true, "account_max_fails": 5, "ip_max_fails": 10,
    "window_seconds": 600, "lock_base_seconds": 60, "lock_max_seconds": 900,
    "ip_allowlist": ["192.0.2.0/24"], "trusted_proxies": []
  },
  "limits": { "account_max_fails": { "min": 1, "max": 1000, "default": 5 }, "…": {} },
  "trusted_proxies_effective": ["loopback", "172.18.0.0/16"],
  "trusted_proxies_source": "auto",
  "trusted_defaults": { "auto": true, "loopback": false, "private": false, "link_local": false },
  "trusted_addresses": [],
  "trusted_proxies_auto": {
    "in_use": true,
    "environment": "container", "runtime": "docker",
    "networks": ["172.18.0.0/16"],
    "excluded_gateways": ["172.18.0.1"], "excluded_self": ["172.18.0.5"],
    "interfaces": [
      { "name": "eth0", "kind": "veth", "addresses": ["172.18.0.5/16"], "trusted": true, "reason": "container-network" }
    ],
    "warning": "", "resolved_at": "2026-10-01T09:00:00Z"
  },
  "untrusted_forwarders": [
    { "address": "172.18.0.1", "first_seen": "2026-10-01T08:00:00Z", "last_seen": "2026-10-01T09:00:00Z",
      "count": 1204, "public": false, "relay": true }
  ],
  "untrusted_forwarders_total": 1,
  "your_ip": "203.0.113.7", "your_ip_allowlisted": false
}
```
`trusted_proxies` is the `login.trusted_proxies` setting as stored;
`trusted_proxies_effective` is what is in force, with `auto` spelled out - the
words of the address classes it takes first (`loopback`, which `auto` always
brings, `private`, `link-local`), then the container networks `auto` resolved
to, then its own addresses and networks; inside them,
`trusted_proxies_auto.excluded_gateways` and `excluded_self` are never trusted
when `auto` is in use. `trusted_proxies_source` says where the list came from:
`setting`, `env` (`FILEX_TRUSTED_PROXIES`) or `auto` (neither is set - the
built-in default). The same list, taken apart: `trusted_defaults` says which
words it takes - `auto`, and the three classes, each Go's `net/netip` definition
(`IsLoopback`, `IsPrivate` (the RFC 1918 / RFC 4193 private ranges),
`IsLinkLocalUnicast`), not a range list of filex's own - and `trusted_addresses`
is the rest. `your_ip` is the address filex sees this request from - what to put
on the allow-list to keep oneself able to sign in.

`trusted_proxies_auto` is what `auto` resolves to right now, and why - answered
whether or not the list in force uses it (`in_use`). `environment` is `plain`
(not in a container: this machine only), `container` (a container on a network
of its own: its networks are trusted), `host-network` (the container sees the
host's NICs and bridges: this machine only), `kubernetes` (a pod: the pod's own
subnet), `podman-rootless` (rootless Podman said so: this machine only) or
`unknown` (it could not tell: this machine only). `runtime` is `docker`,
`podman`, `kubernetes`, `containerd`, `container` (a marker without a name), or
`""` outside a container. `networks` are the subnets trusted besides this
machine; `excluded_gateways` (route next hops and each network's first host
address) and `excluded_self` (filex's own addresses) are carved out of them.
`interfaces` lists each network interface looked at - `kind` is the kernel's
link kind (`veth`, `macvlan`, `ipvlan`, `tun`, `bridge`, ...) or `physical` -
with `reason`: `container-network` (trusted), `lan` (macvlan / ipvlan), `tunnel`
(tun / tap), `host` (a NIC or bridge of the host), `link-local-only`, `down`,
`rootless` or `other`. `warning` is `""`, `unreadable` (something it depends on
could not be read) or `ambiguous` (what it read contradicts itself) - either
means this machine only - with `warning_detail` saying what, in English. The
set is worked out at start and again every minute; the rule and the measurements
behind it are in [CONFIGURATION.md → Trusted proxies](CONFIGURATION.md#trusted-proxies).

`untrusted_forwarders` are peers that sent `X-Forwarded-For` or `X-Real-IP`
without being trusted - their header was ignored, so every request they relayed
counts as theirs: the address, when it was first and last seen, how many requests
carried the header (one per request), `public` (not loopback, private or
link-local) and `relay` (an address `auto` carves out because relayed connections
arrive from it: a gateway, or filex's own address). The busiest come first, at
most 50; `untrusted_forwarders_total` is how many are remembered (at most 256, a
day each; a peer that is trusted since is dropped). Only the peer's address is
kept, never what it wrote. On a public demo (`FILEX_DEMO_MODE`) the addresses of
the forwarders and of `trusted_proxies_auto` read `hidden on the demo`.

⚠ On a **public demo** (`FILEX_DEMO_MODE`) every address and network in
`settings.ip_allowlist`, `settings.trusted_proxies`, `trusted_proxies_effective`
and `trusted_addresses` reads `hidden on the demo` (the class words stay): an
allow-listed address is exempt from the per-address limit, the most useful
address on the page to somebody guessing passwords. `your_ip` - the reader's
own - is shown. The same answer through `/api/ai/admin` and the
`admin_login_security_get` tool is masked the same way ([DEMO.md](DEMO.md)).

Every answer here is the running limiter's **memory** - the settings and the
counters it decides with (see [CONFIGURATION.md → Sign-in attempt
limits](CONFIGURATION.md#sign-in-attempt-limits)). With several instances the
settings are shared (each reads them again every minute, so a save here is in
force everywhere within a minute), while the counters - the lock list - are each
instance's own.

### `PATCH /api/admin/login-security` ![admin](https://img.shields.io/badge/-admin-red)
Partial body with the same keys as `settings`; echoes the `GET` shape. Every field
is validated first and **nothing is written unless all are valid**:
`400 {"error":"invalid_setting","field":"…","message":"…"}`. `ip_allowlist` and
`trusted_proxies` take a JSON array or one text (comma / space / newline
separated) of addresses and CIDR networks; `trusted_proxies` also understands
the words `auto`, `loopback`, `private` and `link-local` (a list replaces the
default, so it names what it keeps: `["auto", "192.0.2.10"]` adds a proxy to
the automatic set) and `none` (trust no proxy). An empty list clears the
setting (the environment's list, or `auto`, is in force again). `lock_max_seconds` may not be below `lock_base_seconds`. Each value is
written to the database and put in force at once - the next attempt, and the
next request's address, use it. A write the database refuses answers `500`; the
fields before it are written and in force, the rest are not. Audited as
`login_security.update` with the names and values of the changed fields and
`changed_fields` - ONE name whatever the door: through an admin API key
(`/api/ai/admin`) or an MCP tool the row is the same, its metadata saying which
(`via: "api"` or `"mcp"`, with `token_id` / `token_username`; a session's row
has no `via`). A sign-in setting written through the generic settings API
(`PUT /api/admin/settings/login.…`, `admin_settings_set`, or a
`PATCH /api/admin/settings` whose keys are all sign-in settings) is filed as
the same `login_security.update`; a batch that mixes them with other settings
stays one `settings.update` row naming them in `login_security_fields`.

### `GET /api/admin/login-security/locks` ![admin](https://img.shields.io/badge/-admin-red)
Query: `locked=1` (only locks in force), `scope=account|ip`, `limit` (default
200, at most 1000).
```json
{ "items": [ { "id": "3f1c…", "scope": "account", "subject": "ada@example.com", "fails": 0, "limit": 5,
    "lock_level": 2, "locked": true, "locked_until": "2026-09-30T10:04:00Z", "retry_after": 87,
    "window_start": "…", "last_fail_at": "…", "last_ip": "203.0.113.7", "last_protocol": "web" } ],
  "now": "2026-09-30T10:02:33Z" }
```
`subject` is the counter's key: the identifier normalised (trimmed, lower-case),
or the address. An operating-system name's `CORP\` or `.\` prefix is dropped
(`CORP\alex` and `.\alex` count as `alex`), and on a multi-tenant install the
tenant's realm leads the key: `acme/alex`. `last_protocol` is `web`, `dav`, `ftp` or `sftp`. The list is
the limiter's memory - what decides right now. Listing also runs the sweep: it
notices locks that have run out and writes their `login.unlocked` audit row, and
writes the database any counter it still owes. `id` names the row - stable
while the server runs, unique per counter, telling nothing about the subject -
for a page to key its rows by.

⚠ On a public demo an address lock's `subject` and every `last_ip` read
`hidden on the demo`: two locked addresses then look alike and are still two
rows (`id`). A masked subject names nothing that could be unlocked - and a demo
refuses the unlock anyway. An account counter's `subject` is what somebody
typed: shown when it names an account of the instance, `hidden on the demo`
when it names none (a visitor's own e-mail) or is an address. The demo's
shared account has no account counter at all
([CONFIGURATION.md → Demo mode](CONFIGURATION.md#demo-mode)).

### `POST /api/admin/login-security/unlock` ![admin](https://img.shields.io/badge/-admin-red)
`{ "scope": "account", "subject": "ada@example.com" }` (the identifier as the
person typed it - it is normalised), `{ "scope": "ip", "subject": "203.0.113.7" }`
(one address), or `{ "all": true }` for every lock in force. A tenant's account
is named with its realm, as the locks list shows it: `"subject": "acme/alex"`.
Clears the counter and the escalation - at once in memory; a delete the
database refuses is retried. Answers `{ "ok": true, "unlocked": n }`, or `503`
when the limiter is not running; audited as `login.unlocked` (`reason:
"admin"`, scope, subject) - through an admin API key or an MCP tool too, with
`via` in the metadata.

### `GET /api/admin/login-security/attempts` ![admin](https://img.shields.io/badge/-admin-red)
The sign-in trail, newest first: the audit log filtered to `login.` and
`login_security.` - every wrong attempt, lock, release and allow-list pass, and
every change to these settings. Query:
`action=login.failed|login.locked|login.unlocked|login.allowlist_pass|login_security.update`,
`from`, `to` (RFC 3339), `limit` (default 50, at most 500), `offset`.
```json
{ "items": [
    { "id": 812, "action": "login.failed", "identifier": "ada@example.com",
      "ip": "203.0.113.7", "protocol": "web", "reason": "invalid_credentials",
      "at": "2026-09-30T10:00:01Z", "metadata": { "remaining": 3 } },
    { "id": 813, "action": "login.unlocked", "identifier": "ada@example.com",
      "reason": "admin", "scope": "account", "via": "mcp", "at": "…" },
    { "id": 814, "action": "login_security.update", "via": "panel",
      "changed_fields": ["account_max_fails"], "at": "…" } ],
  "total": 3 }
```
Never a password: the limiter's rows hold an identifier, an address, the door
(`protocol`) and the reason (`invalid_credentials` or `invalid_totp`). An
ADMINISTRATOR's row - an unlock (`reason: "admin"`) or a settings change - says
what it acted on instead: the account (`identifier`) or the address (`ip`) that
was unlocked (the administrator's own address stays on the audit row), the
`changed_fields`, and `via`, the door they used: `panel` (a session), `api` (an
admin API key) or `mcp` (an MCP tool). Each event is in the trail exactly once,
whichever door: the families keep one name through every door, and an
`/api/ai/admin` write is audited once (it was written twice until 0.50, the
second row empty). An unlock of every lock has `metadata.all` and
`metadata.unlocked`. A demo account's wrong attempts carry
`metadata.account_exempt`.

⚠ On a public demo every address in these rows - `ip`, an identifier typed as
an address, the subject of an unlock, the allow-list in a settings change -
reads `hidden on the demo`, and so does an `identifier` (or an account
unlock's subject) that is no account of the instance: a visitor's own e-mail
typed at the form is theirs. The instance's own accounts stay readable, by
e-mail or username, with or without a realm in front ([DEMO.md](DEMO.md)).

---

## Admin: webhooks

Five routes under `/api/admin/webhooks` manage **webhook v2 targets** - rows,
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

⚠ `GET` / `PATCH /api/admin/notifications/webhook-config` governs the **legacy
single** webhook (`FILEX_WEBHOOK_URL`); these govern the v2 targets. The event catalogue -
including `file.updated`, which from v0.34.0 replaces `file.uploaded` for a
write that overwrote an existing file - is in
[NOTIFICATIONS.md](NOTIFICATIONS.md).

---

## Admin: sync runs

### `GET /api/admin/sync-runs` ![admin](https://img.shields.io/badge/-admin-red)
**Query**: `?storage_id=…&status=…&limit=50&offset=0` - runs of the last five
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
| `failed` | the run stopped on an error of its own - a backend that did not answer, a listing that failed; `error` says which |
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

⚠ On a **demo** instance (`FILEX_DEMO_MODE`) every address in a row reads
`hidden on the demo`: `ip`, a `target_id` that is an address (an address lock's
`login.locked` / `login.unlocked`), `target_name`, and any address inside
`metadata` (an unlock's `subject`, an identifier typed as an address, the
allow-list and proxies of a sign-in settings change); on a sign-in row
(`target_type: "login"`) a typed name that is no account of the instance -
`target_id`, `identifier`, `subject`, `target_name` - is masked too. The page itself stays
readable - it is one of the operator surfaces a demo exists to show - but the
addresses in it belong to the other visitors, and on a public demo every
visitor can read it. The same masking applies to the `recent_activity` block of
`GET /api/admin/dashboard`, which carries the same rows, and to both through
`/api/ai/admin` and the `admin_audit_list` / `admin_dashboard` tools. An
ordinary install is untouched; see [DEMO.md](DEMO.md).

⚠ Both the envelope and the action list on this page used to be invented. The
key is `entries` (not `events`), each row wraps the entry under `entry` with
`user_email` and `user_name` beside it, and the fields are `target_type` / `target_id` /
`metadata` / `created_at` - not `resource` / `meta` / `ts` / `ua`.

`user_name` is the person as every screen names them - display name, else
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
`user.update` · `version.delete` · `version.restore` · `login.unlocked` ·
`login_security.update` - plus AI-admin calls, which carry the same names under
an `ai.` prefix, except the sign-in families (`login.`, `login_security.`):
those keep one name through every door, the door in the row's metadata.

**One row per write, and the door in it.** A request is audited once however
many audit middlewares it passes (`/api/ai/admin` writes were written twice
until 0.50). A row written through a token carries `token_id`,
`token_username` and `via`: `api` for a REST call, `mcp` for an MCP tool; a
session's row has none. The AI file surface (`/api/ai/<verb>`) is audited as
`ai.file.<verb>` / `ai.share.create` / `ai.share.delete`, and each MCP tool that
writes leaves the row its REST twin leaves, with `via: "mcp"`; an MCP read, a
`tools/list` or an `initialize` leaves none ([MCP.md](MCP.md)).

Handlers write rows of their own beside these; among them the plugin install
requests (`internal/pluginreq`), one row per event whichever door it came in
by: `plugin_request.create` · `plugin_request.approve` ·
`plugin_request.reject` · `plugin_request.expire` · `plugin_request.supersede`
(target `plugin_request` and its id; metadata the plugin, version, SHA-256
and, when an API key asked, its id and label).

So does who may encrypt (`internal/e2epolicy`, [Admin: encryption
policy](#admin-encryption-policy)): `e2e_policy.update` (target `e2e_policy`
and the tenant's id, none for a single-tenant install's; metadata `before` and
`after`) · `e2e_tenant.update` (target `providers` and the tenant's id; `before`
and `after`) · `e2e_request.create` · `e2e_request.approve` ·
`e2e_request.reject` · `e2e_request.expire` · `e2e_request.use` (target
`e2e_request` and its id; metadata the folder, kind, requester and state, and
on `.use` the folder that was actually encrypted, as `encrypted`).

⚠ Filtering by `?action=` is an exact match, so the eight values this page
used to list and no code ever writes (`auth.login`, `auth.logout`,
`auth.failed`, `file.move`, `file.copy`, `storage.add`, `user.disable`,
`admin.config_change`) returned an empty page forever. Note in particular
`storage.create`, **not** `storage.add`. A successful sign-in and a sign-out
are **not audited** today; do not build an alert on them. Wrong attempts,
locks and their releases are - the `login.` family above
([CONFIGURATION.md → Sign-in attempt limits](CONFIGURATION.md#sign-in-attempt-limits)).

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
`storage_unreachable`, `quota_exceeded`, `cross_origin_refused`.
