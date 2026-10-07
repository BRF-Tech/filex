# AI / MCP & API tokens

filex exposes a **token-authenticated automation surface** at `/api/ai` so an AI
agent (Claude, etc.) or a host application can drive the file manager
programmatically - list, read, write, move, copy, delete, search, share and zip
files, run an app's action (Convert), work the operations queue, the trash,
versions, archives, its links and file requests, read the bell, star, comment
and set the permissions on an item it owns - with no browser session. The same surface speaks **Model Context
Protocol** (MCP) at `/api/ai/mcp`, so an MCP-capable model gets filex as a native
tool set.

The interface calls these credentials **API keys** (the admin panel's **API / MCP**
page, the explorer's **API keys** entry); the HTTP API and this page call them
tokens (`X-Filex-Token`, `fxt_…`) - they are the same thing.

Everything here is authenticated by an **API token** (never a cookie), scoped to
a set of verbs, optionally locked to a single sub-folder, and gated by the same
[RBAC](RBAC.md) grants that apply to the interactive UI. A token can therefore be
handed to an agent that only ever sees - and can only ever touch - one project's
folder.

- [API tokens](#api-tokens) - [Creating a token](#creating-a-token) · [Scopes](#scopes) · [Root confinement](#root-confinement)
- [REST surface (`/api/ai`)](#rest-surface-apiai)
- [MCP endpoint (`/api/ai/mcp`)](#mcp-endpoint-apiaimcp)
- [Tool set](#tool-set)
- [Security](#security)
- [Failure modes & troubleshooting](#failure-modes--troubleshooting)
- [See also](#see-also)

---

## API tokens

A token is a **64-character hex string** (32 random bytes). It is shown **once**
at creation time - only its **sha256 hash** is stored in the `api_tokens` table,
so a lost token cannot be recovered, only revoked and re-issued.

Every token is **bound to a user** and inherits that user's account role
(`admin` / `user` / `viewer`) and RBAC grants. Authenticating with a token is
exactly like that user signing in - minus the cookie. An optional
`expires_in_days` sets a hard expiry; an expired token authenticates as nobody
(401). filex stamps each token's last-used time on every request.

### Token kinds - `user` vs `app`

Because a token acts AS its owner, "who is calling" and "is there a **person**
behind this call" are two different questions. A token answers the second one
with its `kind`:

| Kind | What it is | Minted by | Identity surfaces |
|---|---|---|---|
| `user` | one person's own credential - their CLI, WebDAV/SFTP/FTPS/S3 client, `filex mount`, the desktop app | `POST /api/tokens` (self-service); the desktop app's sign-in (`POST /api/auth/desktop/complete`, browser session only - see [DESKTOP.md](DESKTOP.md#the-token-the-app-is-given)) | all of them |
| `app` | an integration - a host app's proxy, a bot, an MCP client | `POST /api/admin/ai-tokens` | suppressed |

For an `app` token filex refuses every **self-service credential surface** with
403 - `/api/tokens`, `/api/auth/s3-keys`, `/api/auth/ssh-keys`,
`/api/auth/nfs-exports`, and reading a share link's PIN back
(`GET /api/shares/{id}/pin`) - and the explorer's navigation panel drops **API
keys**, **Recent**, **Starred** and **Shared with me** (and **My shares**, where
the host draws it). Nothing else changes:
same scopes, same RBAC, same confinement, and the panel keeps Upload, the
storage list, Trash and "How to connect". Inside "How to connect" the guides
stay and the mint forms are replaced by a line saying this session cannot
create credentials.

All of them refuse from one implementation (`handlers.RequirePersonalCaller`),
because they answer one question: *show me, and let me mint, the credentials of
the person calling.* Gating only the first of them still let an embed visitor
mint an S3 access key bound to the token's owner - the same hole, a different
button.

Why: an embedded explorer is usually proxied with **one shared token** injected
by the host, so every visitor authenticates as the token's owner. Under a
`user` token that would mean any visitor could list - and revoke - the
credential the embed itself runs on, and "your Recent" would be somebody else's
history.

⚠ **Kind is not confinement and not role.** A `root:`-scoped token may perfectly
well be a person's, and a `viewer` is still a person.

⚠ **Every token that existed before this split reads as `app`** (migration
`00030`, which defaults both the column and the existing rows). That is the
restricting direction on purpose. If one of them is really a person's, an admin
hands it back with one call:

```bash
curl -X PATCH https://files.example.com/api/admin/ai-tokens/42   -H 'Content-Type: application/json' -b cookies.txt -d '{"kind":"user"}'
```

Clients that need to know which kind is calling read `caller_kind` from
`GET /api/files/capabilities` - `"user"` for a cookie/OIDC session as well as
for a `user` token, `"app"` only for an app token.

⚠ That route **never refuses**. It is fetched by the login screen and by the
public share/drop pages, so an unknown, revoked, expired or malformed token, a
disabled owner and an unknown `X-Filex-Token-User` all simply answer `200` with
`caller_kind: "user"` rather than describing a caller filex will not serve.

Send the token on **either** header:

```
X-Filex-Token: <token>
Authorization: Bearer <token>
```

> The `/api/ai` namespace is **token-only** - it never accepts a cookie/JWT
> session. (The interactive `/api/files` surface accepts *either*, so a host app
> can proxy the embedded explorer with a confined token.)

### Creating a token

Two creation paths. The admin door needs an **admin session**; the
self-service door takes a browser session **or a person's own `user`
token** - and since v0.43.0 a token caller may only mint a credential no
wider than itself (see *Ceiling rules*). An `app` token is refused outright:

**1. Admin - `POST /api/admin/ai-tokens`** (admin session). Full control: bind to
any user, set any scopes, label, and expiry.

```bash
curl -X POST https://files.example.com/api/admin/ai-tokens \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{
    "label": "claude-project-x",
    "user_id": 42,
    "scopes": "read,write,mcp,root:s3://projects/x",
    "expires_in_days": 90
  }'
# → { "token": "<64-hex - shown ONCE>", "row": { … } }
```

- `user_id` - omit to bind the token to the calling admin.
- `kind` - `"app"` (default here) or `"user"`. This surface issues
  integrations' credentials, so it defaults to `app`; pass `"user"` when an
  admin is minting a personal token on somebody's behalf. An unknown value is
  rejected (400) rather than folded into `app`.
- `scopes` - comma-separated allow-list (see [Scopes](#scopes)), **required:
  at least one verb**. An empty list is refused with `400 scopes_required`
  (since v0.43.0 - before, it granted every scope, `admin` included). `admin`
  is granted only when it is in the list. Any scope outside the canonical set
  is rejected up front (`400 scope_unknown`), so a typo can't silently grant
  nothing. `comments:rw` lets the token add and delete comments
  ([below](#comments-a-permission-with-a-level)).
- `label` / `expires_in_days` - optional.

`GET /api/admin/ai-tokens` lists all tokens (no secrets), each with
`permissions` - its level of every permission with one (`{"comments": "read"}`);
`PATCH /api/admin/ai-tokens/{id}` edits `label`, `usernames`, `kind` and
`permissions` (`{"permissions": {"comments": "rw"}}` - the verbs never change);
`DELETE /api/admin/ai-tokens/{id}` revokes one.

**2. Self-service - `POST /api/tokens`** (any authenticated user, including
`viewer`). The token is **force-bound to the caller** (a client-supplied
`user_id` is ignored), always minted as `kind: "user"` (not client-settable),
and scopes are **capped to the caller's ceiling**:

```bash
curl -X POST https://files.example.com/api/tokens \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{ "label": "my-agent", "scopes": "read,mcp", "expires_in_days": 30 }'
```

Ceiling rules (privilege-escalation guards):

- **`admin` scope is never allowed** here.
- A **viewer** account may only mint `read` + `mcp`; `write`/`delete` are rejected.
- **At least one verb is required**, exactly as on the admin door: an empty
  list (or a `root:` scope with no verb) is refused with `400 scopes_required`.
  Until v0.43.0 this door quietly filled a default instead.
- A `root:` scope must be **⊆ the caller's own grants** at that path (≥ viewer,
  or ≥ editor when the token also carries write/delete).
- ⚠⚠ **Since v0.43.0 the calling credential is a ceiling of its own.** When
  the caller is a token rather than a browser session, what it mints must
  hold a subset of the caller's verbs, a `root:` confinement inside the
  caller's, and an expiry no later than the caller's; and a narrow caller
  cannot borrow a wider parent token. A wider request is refused with
  **`403`, `reason: "token_ceiling"`**, naming what was too wide. The same
  rule guards `POST /api/auth/s3-keys`, `/api/auth/ssh-keys` and
  `/api/auth/nfs-exports`. A browser session has no ceiling to exceed, and
  credentials that already exist are untouched.

`GET /api/tokens` lists the caller's own tokens; `PATCH /api/tokens/{id}` edits
its label / usernames and the levels of its permissions (`{"permissions":
{"comments": "rw"}}`, never above a calling token's own); `DELETE
/api/tokens/{id}` revokes one (ownership-checked). `kind` is **not** editable here - only an admin changes it,
or an app token could promote itself out of the restriction.

⚠ The whole `/api/tokens` surface answers **403** to an app token (see
[Token kinds](#token-kinds---user-vs-app)). A cookie/OIDC session and a `user`
token both work exactly as before.

### Scopes

> ⚠ **The interface calls these permissions.** Since v0.43.0 the API keys
> panel, its table and the app install review all say *permission*; the
> request field, the error codes and this reference still say `scopes`,
> because that is the name the API accepts. Only the word a person reads
> changed - `{"scopes": "read,write,mcp"}` is unchanged on the wire.

`RequireScope` gates each verb. A token grants **exactly the scopes in its
list** - an empty list grants **nothing** (since v0.43.0; until then it
granted everything, `admin` included). No door issues an empty list any more,
and the upgrade to v0.43.0 rewrote every existing empty list as the explicit
full list `read,write,delete,mcp,admin`, so an old token kept exactly the
access it had - see the CHANGELOG's upgrade note, and review those tokens.

| Scope | Grants |
|-------|--------|
| `read` | `list` / `info` / `download` / `search` (read-only file ops), and the listings of the explorer's operations: app actions, the operations queue, the trash, a file's versions, one's own links; the bell (`notifications_list`, `notification_read`), a file's comments, an item's permissions and the people it could be shared with |
| `write` | `upload` / `mkdir` / `move` / `copy` **and** `share` / `unshare` / `zip` / `unzip`, app actions (`convert` …), stopping an operation, trash restore, version restore and snapshot, archives, file requests; a star, setting and revoking an item's permissions |
| `comments:rw` | adding and deleting a comment (`file_comment_add`, `file_comment_delete`, their REST twins and `/api/files/comments`) - with `read`; `write` does not include it ([below](#comments-a-permission-with-a-level)) |
| `delete` | `delete` (soft-delete to trash) |
| `mcp` | the streamable-HTTP MCP server at `/api/ai/mcp` |
| `admin` | the admin REST surface at `/api/ai/admin/*` **and** the `admin_*` MCP tools - a subset of the admin panel, listed [under Tool set](#tool-set) |

The verbs hold on **every** surface a token reaches, not only here: the web
explorer's `/api/files` routes (and so `filex client`), WebDAV, SFTP, FTPS, and
the S3 keys and NFS exports minted from a token - see
[RBAC.md → API tokens](RBAC.md#api-tokens-verbs-on-every-surface). Changing
the token's own account - its profile, password or two-factor setup - needs
`write` as well.

Scopes bound the **token**; the account behind it has its own permissions
([PERMISSIONS.md](PERMISSIONS.md)), and both must allow a call. The whole
`/api/ai` surface needs the account's `ai.use`, a public link its
`share.links`, and so on - a `403` from that layer names the permission
(see *Failure modes* below).

> **Least privilege.** Give an agent only what it needs - most read/write agents
> want `read,write,mcp` (and `comments:rw` if they comment). `admin` is a superuser scope (it can manage users,
> storages, settings, replica, queue …); reserve it for trusted operator tools.

> ⚠⚠ **Mint agent and embed tokens on a non-admin account** (`user_id`). Scopes
> are enforced on every surface a token reaches. The admin panel's own
> `/api/admin/*` routes and `/metrics` ask for both: an administrator account
> **and**, when a token calls, the `admin` scope on a token with no `root:`
> confinement (since v0.41.0; an administrator's token without `admin` gets
> `403 token missing scope: admin`, a confined one is refused too). The file
> routes, however, judge the account: a token bound to an administrator
> reaches every storage and every folder its verbs allow, because the
> administrator does. A token is only as narrow as its scopes **and** the
> account it is bound to.

#### Comments: a permission with a level

`comments` is held at a level: `read` - every token that does not name it,
every token minted before 0.53 among them - or `rw`, written `comments:rw`
(`comments:write` is read the same). Adding and deleting a comment ask
`comments:rw` on every door, `write` or not, and answer
`403 token missing scope: comments:write` without it; reading asks `read`.
Mint with `comments:rw`, or raise an existing token with `PATCH` and
`{"permissions": {"comments": "rw"}}`. An agent that comments needs it: since
0.53 `write` is not enough. The rule, and the default every permission added
later gets: [RBAC.md → Permissions with a
level](RBAC.md#permissions-with-a-level-comments).

### Root confinement

A `root:<adapter>://<rel>` scope locks the token to **one sub-folder** - a **hard
ceiling** it cannot escape. `<adapter>` is a storage name (see [STORAGE.md](STORAGE.md));
`<rel>` is a path within it. Example: `root:s3://projects/acme`.

- The ceiling is enforced on **both** the `/api/files` UI surface and the
  `/api/ai` REST + MCP surface - every path-bearing operation routes through a
  single chokepoint.
- A confined caller treats its root as `/`: a **bare relative path** (e.g.
  `"reports/q3.csv"`) resolves *under* the root, and an empty path means the root
  itself. Fully-qualified `adapter://root/...` paths are validated as-is.
  Anything outside is rejected **403**.
- The **`X-Filex-Root: <adapter>://<rel>`** request header can **narrow further**
  within the token root (it can only narrow - a header that tries to widen past
  the token ceiling is rejected). This header is applied by the `/api/files`
  confinement middleware, the path a host app uses when it proxies the embedded
  explorer per-request. On the direct `/api/ai` surface, confinement comes from
  the token's `root:` scope.
- The **notification bell** (`/api/notifications`: the list, the unread
  count, mark-read and mark-all-read) is confined the same way: a confined
  token reads - and marks read - only the notices about files inside its
  root, its owner's own notices included; a notice that names no file (the
  admin page's test, an app's notice about a list) stays readable. The admin
  history and the admin surface refuse a confined token outright.
- A confined agent should call **`GET /api/ai/root`** (or the **`file_root`** MCP
  tool) first: it reports whether you're confined, your root, the storage
  adapters you can address, and a hint on how to phrase paths - so the agent
  stops guessing adapter names.

> A token that only *knows* a folder id/name still cannot reach it: confinement
> is a server-side ceiling, not an argument the caller supplies.

---

## REST surface (`/api/ai`)

Token-only JSON over HTTP. All paths use the `adapter://relative/path` wire form
(adapter = storage name); an empty/relative path defaults to the first enabled
storage's root (or, when confined, your root). Every route below, with its
parameters, bodies and the verb it needs, is also in the OpenAPI 3.1
description [`backend/internal/api/openapi.json`](../backend/internal/api/openapi.json),
held to the router by a test ([BACKEND.md](BACKEND.md)).

| Method | Path | Scope | Body / query |
|--------|------|-------|--------------|
| GET | `/api/ai/root` | *(any valid token)* | - → confinement root + reachable storages |
| GET | `/api/ai/files?path=` | `read` | → `{entries:[…]}` - an entry inside an end-to-end encrypted folder (or a `.fxe`) says `encrypted: true` and `e2e_root` ([below](#encrypted-folders-and-fxe)) |
| GET | `/api/ai/info?path=` | `read` | → `{entry:{…}}` |
| GET | `/api/ai/download?path=` | `read` | → raw bytes (stream); `409 E2E_ENCRYPTED` for an end-to-end encrypted file ([below](#encrypted-folders-and-fxe)) |
| GET | `/api/ai/search?path=&q=` | `read` | → `{entries:[…]}` - names and `tag:` filters only; content search is the MCP `file_search` tool |
| GET | `/api/ai/tags?path=` | `read` | → `{path, tags:[{name, kind}], can_edit_team}` - the file's tags **as the token's user sees them** ([Tags](SEARCH.md#tags---personal-and-team)) |
| POST | `/api/ai/tags` | `write` | `{path, tags:[{name, kind}]}` - the tags that user can see become exactly this list (`[]` clears them); every item names its `kind` |
| POST | `/api/ai/upload` | `write` | `{path, content}` / `{path, content_base64}` / multipart `file`; `allow_plaintext` (JSON field or form field) to write into an encrypted folder |
| POST | `/api/ai/upload/ticket` | `write` | `{path, expires_in_seconds?, max_bytes?, allow_plaintext?}` → `{url, ticket, path, max_bytes, expires_at, curl}` |
| PUT/POST | `/u/{ticket}` | *(none - see below)* | raw body (`curl -T`) or multipart `file` → `{entry:{…}}` |
| POST | `/api/ai/mkdir` | `write` | `{path}` - `files.create` in the folder that gains it, as the explorer's New folder |
| POST | `/api/ai/move` | `write` | `{src, dst}` - across storages too. **Never overwrites**: a `dst` that is already taken answers `200` with the item under a free name beside it (`b-copy.txt`), so read `entry.path`; the entry is read off the storage where the item landed, so its `size`, `mime` and `last_modified` are the moved item's (until 0.50 every file came back `size: 0`); `409` with `code: NO_FREE_NAME` only when every name beside it is taken too ([below](#moving-files-between-storages)); `409 E2E_BOUNDARY` across an encryption boundary. The rename/move permission is asked on both items and on the destination folder, as the explorer asks it |
| POST | `/api/ai/delete` | `delete` | `{path}` → soft-delete to trash |
| POST | `/api/ai/share` | `write` | `{path, pin?, expires_in_days?, max_downloads?}` → `{url, token, pin?, encrypted?}` - needs edit rights on the item and the account's `share.links`, exactly as the explorer's Share dialog; never for an encrypted folder or anything in it (`409 E2E_ENCRYPTED`) |
| POST | `/api/ai/unshare` | `write` | `{token}` - your own link (any link for an admin), inside your tenant and your token's root, as `DELETE /api/files/share/{id}` asks; `404` otherwise |
| POST | `/api/ai/zip` | `write` | `{sources:[…], dest, allow_plaintext?}` (server-side) - `files.download` on every source, as the explorer's archive/create |
| POST | `/api/ai/unzip` | `write` | `{src, dest, allow_plaintext?}` (server-side) |
| POST | `/api/ai/copy` | `write` | `{src, dst}` → `202 {op}` - the explorer's paste, through the operations queue ([below](#copy-apps-operations-trash-versions-archives-links)) |
| GET | `/api/ai/apps/actions?path=` | `read` | → `{path, actions:[…]}` - the app actions that apply to that file |
| POST | `/api/ai/apps/run` | `write` | `{plugin, action, paths, params?}` → `202 {op, job_id}`, or the action's form (`{surface}`) when `params` is left out |
| POST | `/api/ai/convert` | `write` | `{path, target}` → `202 {op, job_id}` - the Convert app's action |
| GET | `/api/ai/ops?status=` | `read` | → `{ops:[…]}` - your operations |
| GET | `/api/ai/ops/{id}` | `read` | → the operation |
| POST | `/api/ai/ops/{id}/cancel` | `write` | → `{op}`; `409 FINISHED` / `NOT_CANCELLABLE` |
| GET | `/api/ai/trash?storage=&limit=&offset=` | `read` | → `{entries, total, limit, offset}` |
| POST | `/api/ai/trash/restore` | `write` | `{node_ids}` (at most 1000) → `202 {ops}` |
| GET | `/api/ai/versions?path=` | `read` | → `{versions, node_id, node}` |
| POST | `/api/ai/versions/restore` | `write` | `{path, version_id, snapshot_current?}` |
| POST | `/api/ai/versions/snapshot` | `write` | `{path}` |
| POST | `/api/ai/archive/create` | `write` | `{sources, dest, format?, password?, encrypt_filenames?, compression?, allow_plaintext?}` → `202 {op}` |
| POST | `/api/ai/archive/extract` | `write` | `{path, dest?, members?, password?, allow_plaintext?}` → `202 {op}` |
| GET | `/api/ai/shares?active=&limit=&offset=` | `read` | → your own links and file requests |
| POST | `/api/ai/share/request` | `write` | `{path, max_uploads?, drop_settings?, expires_in_days?, pin?}` → a file request (upload link) into a folder |
| GET | `/api/ai/notifications?unread=&limit=&offset=` | `read` | → your bell `{items, total, limit, offset}` ([below](#the-bell-stars-comments-permissions)) |
| POST | `/api/ai/notifications/read` | `read` | `{id}` or `{all: true}` - your own bookkeeping, not audited |
| POST | `/api/ai/star` | `write` | `{path, star?}` (`star` defaults to `true`) |
| GET | `/api/ai/comments?path=` | `read` | → `{comments, node_id}` |
| POST | `/api/ai/comments` | `read` + `comments:rw` | `{path, text}` |
| POST | `/api/ai/comments/{id}/delete` | `read` + `comments:rw` | your own comment (any, for an administrator) |
| GET | `/api/ai/permissions?path=` | `read` | → `{path, storage_rbac, direct, inherited, effective}` (owner level) |
| GET | `/api/ai/permissions/users?q=` | `read` | → `{users}` to grant to |
| POST | `/api/ai/permissions` | `write` | `{path, user_id \| group_id, level}` |
| POST | `/api/ai/permissions/{id}/revoke` | `write` | `{group?}` |
| `*` | `/api/ai/admin/*` | `admin` | the same admin handlers the panel calls, for the areas listed [under Tool set](#tool-set) |

Notes:

- **Upload** takes UTF-8 text (`content`), base64 (`content_base64`), or a
  `multipart/form-data` `file` field for large binaries.
- **Upload tickets** solve the one case an agent cannot: a large file already on
  its own disk. Anything sent through a tool call travels inside that call - on
  MCP, through the model's context - so a 130 MB file (~173 MB of base64) simply
  cannot go that way. `POST /api/ai/upload/ticket` splits the job: this
  authorized call pins the destination under *your* token, and the `url` it
  returns accepts exactly one upload **with no credentials at all**, so an agent
  that has no filex token can still finish the transfer. The reply hands over a
  ready `curl` line, a `powershell` equivalent for machines without curl, and a
  `next` line for the case with no shell at all (give it to the user). The ticket is
  single-use, short-lived (default 30 min, `max 24 h`), cannot read/list/delete,
  and cannot be pointed anywhere else - the redeemer never supplies a path. The
  bytes are written as, and billed to, the minter. Tickets live in memory, so a
  restart drops the unredeemed ones (mint another).
- **Share** mints a public `/s/<token>` link (folders download as a ZIP). The
  target must already be indexed - write or list it first. A generated PIN is
  returned **once**.
- **Zip / unzip run on the server**: the archive is assembled/extracted straight
  into storage and only metadata (the dest entry / a file count) crosses the
  wire. To hand a big zip to someone, `share` the `dest` - don't download it
  through the API. Both are zip-slip protected and confined to the token root.
- Errors map to HTTP status: `404` not found, `403` read-only / out of root /
  insufficient grant / one of filex's own names (`RESERVED_NAME`, an
  encrypted folder's key file too), `409` the other kind is already there (a
  file written onto a folder, a folder made onto a file) or a move found no
  free name (`code: NO_FREE_NAME`), `409` with a `code` for end-to-end
  encryption (`E2E_ENCRYPTED`, `E2E_PLAINTEXT_REFUSED`, `E2E_BOUNDARY`,
  [below](#encrypted-folders-and-fxe)), `413` an upload too large for this
  endpoint (use an upload ticket), `423` an app has locked the path (the
  message names the app, e.g. a document out for signature), `501`
  unsupported by the driver, `503` no storage configured or a transient
  refusal (a version of the file it would replace could not be kept, the
  backend could not say whether a name is free), `400` bad request.

```bash
# List a folder
curl -H 'X-Filex-Token: <token>' \
  'https://files.example.com/api/ai/files?path=s3://projects/acme'

# Write a text file
curl -X POST -H 'X-Filex-Token: <token>' -H 'Content-Type: application/json' \
  -d '{"path":"s3://projects/acme/notes.md","content":"# Notes\n"}' \
  https://files.example.com/api/ai/upload

# Upload a big LOCAL file: mint a ticket, then transfer without any token
curl -X POST -H 'X-Filex-Token: <token>' -H 'Content-Type: application/json' \
  -d '{"path":"s3://projects/acme/dataset.parquet"}' \
  https://files.example.com/api/ai/upload/ticket
# → {"url":"https://files.example.com/u/9f3c…","curl":"curl -T <local-file> …",…}

curl -T ./dataset.parquet 'https://files.example.com/u/9f3c…'
# → {"entry":{"name":"dataset.parquet","size":136314880,…}}
```

---

## MCP endpoint (`/api/ai/mcp`)

filex embeds a **Model Context Protocol** server over **streamable HTTP**
(stateless JSON-RPC: one request → one JSON response; a `GET` opens an SSE
stream). It is mounted at `POST|GET /api/ai/mcp` behind the `mcp` scope, so any
token used with it must carry `mcp`. Each file tool also needs the verb of its
REST twin - `read` for `file_list` / `file_info` / `file_read` / `file_search` /
`file_tags` / `app_actions` / `ops_list` / `op_get` / `trash_list` /
`file_versions` / `share_list` / `notifications_list` / `notification_read` /
`file_comments` / `file_comment_add` / `file_comment_delete` /
`file_permissions` / `file_permission_users`, `write` for
`file_write` / `file_upload_ticket` / `file_mkdir` / `file_move` / `file_copy` /
`file_share` / `file_unshare` / `file_zip` / `file_unzip` / `app_run` /
`file_convert` / `op_cancel` / `trash_restore` / `file_version_restore` /
`file_snapshot` / `archive_create` / `archive_extract` / `file_request_create` /
`file_star` / `file_permission_set` / `file_permission_revoke` (and for setting
tags), `delete` for `file_delete`; `file_root` needs none. `file_comment_add`
and `file_comment_delete` need `comments:rw` besides
([Comments](#comments-a-permission-with-a-level)). A tool the token cannot use
is not listed. The `admin_*` tools additionally need `admin` (and an unconfined token).

Connect an MCP client by pointing it at the endpoint and supplying the token as a
header. With the Claude Code CLI:

```bash
claude mcp add --transport http filex https://files.example.com/api/ai/mcp \
  --header "X-Filex-Token: <token>"
# (Authorization: Bearer <token> works too)
```

Other MCP clients: configure an HTTP/streamable-HTTP server with URL
`https://files.example.com/api/ai/mcp` and header `X-Filex-Token: <token>` (or
`Authorization: Bearer <token>`). Verify connectivity with a raw JSON-RPC call:

```bash
curl -X POST https://files.example.com/api/ai/mcp \
  -H 'X-Filex-Token: <token>' -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

> **Reverse proxy:** the `GET` transport opens a Server-Sent-Events stream, so
> the proxy in front of filex must allow SSE - don't buffer the response, keep
> the connection open, and don't strip the `X-Filex-Token` / `Authorization`
> header. (Nginx: `proxy_buffering off;` for this location.)

The tools each MCP session sees depend on the token's scopes: an `admin`-scoped
token additionally sees every `admin_*` tool (below); a non-admin token never
sees them in `tools/list` at all.

---

## Tool set

**Core file tools** (each listed for a token holding its verb - see above -
and gated by the bound
user's role + grants + confinement):

| Tool | What it does |
|------|--------------|
| `file_root` | Report your access scope: confinement root (if any) + addressable storages. **Call this first.** |
| `file_list` | List a directory (`adapter://dir`; empty = first storage root). Entries say `encrypted` and `e2e_root` ([below](#encrypted-folders-and-fxe)); an encrypted folder's key file is never listed. |
| `file_info` | Metadata (size, mime, type, modified time) for one path. |
| `file_read` | Read a file. UTF-8 text when the bytes are valid UTF-8, else base64. **Rejects files > 8 MiB** - use the REST `download` stream for those. An end-to-end encrypted file is refused with `E2E_ENCRYPTED`. |
| `file_write` | Create/overwrite a file (`content` text or `content_base64` binary). Content you generate - never a file off your disk. Into an encrypted folder only with `allow_plaintext` (else `E2E_PLAINTEXT_REFUSED`). |
| `file_upload_ticket` | Get a short-lived, **credential-free** URL (plus the ready `curl -T` line) for a LOCAL file of any size. The bytes never enter the conversation; the URL takes one upload to a fixed path. |
| `file_delete` | Soft-delete to filex trash (recoverable from the UI). |
| `file_move` | Move or rename a file/folder. **Never overwrites**: a destination that is already taken gets a free name beside it (`rapor-copy.txt`), so read the returned `entry.path` rather than assuming the one you asked for; the entry's `size`, `mime` and `last_modified` are read off the storage where the item landed (until 0.50 every file came back `size: 0`). When every candidate name is taken too, nothing moves and the tool's error starts with `NO_FREE_NAME:`. Works across storages: the bytes are copied and verified, then the source is removed - unless something was left behind (`entry.source_kept`, [below](#links-that-cannot-travel)). |
| `file_mkdir` | Create a directory (`files.create` in the folder that gains it). |
| `file_search` | Search file/folder names **and** (by default) extracted file contents in a storage. Forgiving on separators and typos; words may be in any order and, with the search index, may be answered by a folder (`main code` finds `Code/main.go`; without the index, or with `content=false`, every word has to be in the file's own name); supports `tag:` / `-tag:` filters (your personal and your team's tag of that name both count); `content=false` restores name-only. |
| `file_tags` | Read a file's tags (`{path}`), or set them (`{path, set:[{name, kind}]}`). Every tag says its **kind**: `personal` (only the token's user sees it) or `team` (everyone in the tenant who can see the file; adding or removing one needs edit permission - `can_edit_team` says whether you have it). There is **no default kind** on this surface: an agent names the kind of every tag it writes. Other people's personal tags and other tenants' tags are never shown or touched. |
| `file_share` | Public share link for a file/folder (folders → ZIP); optional PIN/expiry/max-downloads. Needs **edit** permission on the item and the account's `share.links` permission, as in the explorer - and the link answers only while the account keeps them ([PERMISSIONS.md](PERMISSIONS.md#public-links-follow-their-creator)). Use this to hand a file to someone instead of streaming it back. Never for an encrypted folder or anything in it (`E2E_ENCRYPTED`); a `.fxe` is shared as it is and the answer says `encrypted: true`. |
| `file_unshare` | Revoke a public link or a file request by its token: your own (any for an admin), inside your tenant and your token's root. |
| `file_zip` | Pack files/folders into a `.zip` **on the server** (dest lands in storage; share it to download). Needs `files.download` on every source, as a download does. |
| `file_unzip` | Extract a stored `.zip` into a directory **on the server** (zip-slip protected, stays within your root). Into an encrypted folder only with `allow_plaintext`. |

### Copy, apps, operations, trash, versions, archives, links

Since 0.50 an agent reaches the explorer's own operations too. These tools are
not a second implementation: each one runs **the handler the explorer's route
runs** (`/api/files/copy`, `/api/files/plugins/actions/…/run`,
`/api/files/ops`, the trash, version and archive routes, `/api/shares`,
`/api/files/share`) in process, so its permission checks, the tenant
boundary, an app's rules (`min_role`, administrator-only actions, `applies`,
hidden actions) and its answers are the explorer's. On top of them the AI
surface adds its own: paths in its form (a confined token's bare path resolves
under its root), its end-to-end encryption codes ([below](#encrypted-folders-and-fxe)),
and no key - an encrypted folder's key file is never written, copied over,
rolled back or extracted from here (`403 RESERVED_NAME`; an archive member of
that name is skipped).

A tool answers `{status, result}`: the HTTP status of its REST twin and that
route's JSON answer, the very body `/api/ai/<route>` returns. A refusal is a
tool error whose text starts with the code - the answer's `code`, or its
one-word `error` (`READ_ONLY: …`, `PERMISSION_DENIED: …`,
`APP_PLUGINS_DISABLED`) - and ends with the status (`(HTTP 403)`).

Work that takes time is a job on the **operations queue**: the tool answers
`202` with `{op: {id, …}}` and the job runs on the server. Follow it with
`op_get {id}` until `status` is `ok` (or `partial`, `failed`, `cancelled`);
stop it with `op_cancel`. You see only the operations you queued - an
administrator's unconfined key sees everybody's - and someone else's id
answers `404`, as an id that never existed.

| Tool | REST twin | What it does |
|------|-----------|--------------|
| `file_copy` | `POST /api/ai/copy` | Copy a file or folder to `dst` - its folder and its name, as `file_move`'s `dst` - within a storage or to another one (the bytes travel through the queue's transfer). **Never overwrites**: a taken name lands beside it (`rapor-copy.txt`). Edit rights on the source, `files.create` in the destination folder. An encrypted file does not leave its folder (`E2E_BOUNDARY`). A copy of a `.fxe`, or of a folder that holds an encrypted folder or a `.fxe`, is a new encryption where it lands and is asked of the [encryption policy](E2E-ENCRYPTION.md#who-may-encrypt) (`403 e2e_not_allowed` in the result). Queued. |
| `app_actions` | `GET /api/ai/apps/actions?path=` | The app actions that apply to a file or folder, as the explorer's right-click menu offers them to you: running apps, enabled actions, the administrator's restrictions, the app permissions your account holds, and the action's `applies` rule judged on that very file. |
| `app_run` | `POST /api/ai/apps/run` | Run one (`plugin`, `action` from `app_actions`) on `paths`, with the explorer's rules (`plugins.run`, your level on each file, the app's own permissions). Without `params`, an action that has a form answers the form (`{surface}`) - its fields are what `params` takes; with `params` (`{}` for an action without a form) the job is queued. An action an app starts itself is not offered here. |
| `file_convert` | `POST /api/ai/convert` | Convert a file to `target` (`pdf`, `docx`, `xlsx`, `png` …) with the **Convert** app - the explorer's *Convert…*. Needs the app installed by an administrator (`NOT_FOUND` otherwise; `APP_PLUGINS_DISABLED` when apps are off). The result lands beside the file under a free name. Queued. |
| `ops_list` | `GET /api/ai/ops` | Your operations, newest first (`status` filters). |
| `op_get` | `GET /api/ai/ops/{id}` | One operation's state. |
| `op_cancel` | `POST /api/ai/ops/{id}/cancel` | Stop a pending or running operation: `FINISHED` once it has ended, `NOT_CANCELLABLE` for one that finishes what it starts (a rename, a restore, a permanent delete). |
| `trash_list` | `GET /api/ai/trash` | What you can bring back: `{entries: [{id, path, name, deleted_at, deleted_by_self, …}], total}`, only entries you could see where they were, inside your root ([TRASH-VERSIONING.md](TRASH-VERSIONING.md#trash-endpoints)). |
| `trash_restore` | `POST /api/ai/trash/restore` | Bring entries back (`node_ids` = their `id`, at most 1000), on the queue: every entry is judged first (`files.create` where it came from, app locks) and nothing is queued unless all pass. `202 {ops}`, one per storage. |
| `file_versions` | `GET /api/ai/versions?path=` | A file's version history. |
| `file_version_restore` | `POST /api/ai/versions/restore` | Replace the file's content with a version (`files.modify`); the content it replaces is kept as a version first. |
| `file_snapshot` | `POST /api/ai/versions/snapshot` | Keep the current content as a version now (`files.modify`). |
| `archive_create` | `POST /api/ai/archive/create` | Pack files and folders into a **new** archive on the server: `zip`, `7z`, `tar`, `tar.gz`, `tar.bz2`, `tar.xz` (the administrator may allow fewer, [archive settings](CONFIGURATION.md)), optionally with a `password` (zip, 7z). An existing `dest` is refused (`TARGET_EXISTS`). `files.download` on every source. Into an encrypted folder only with `allow_plaintext`. Queued. |
| `archive_extract` | `POST /api/ai/archive/extract` | Extract an archive already in storage - zip, 7z, rar, the TAR family - into its own folder or `dest`, with `password` for a protected one (`PASSWORD_REQUIRED` without it); `members` picks entries. Queued. `file_unzip` stays the synchronous ZIP-only tool. |
| `share_list` | `GET /api/ai/shares` | The public links and file requests **you** made, with the `token` `file_unshare` revokes. Only links inside your root. |
| `file_request_create` | `POST /api/ai/share/request` | A **file request**: a public upload link into a folder ([SHARING.md](SHARING.md)), with `max_uploads`, `drop_settings`, `expires_in_days`, `pin`. Edit rights on the folder and the account's `share.upload_links`; never into an encrypted folder (`E2E_ENCRYPTED`). |

⚠ An archive's `password` goes to the job in memory only: no queue row, log
or audit row carries it.

### The bell, stars, comments, permissions

The same way - the explorer's own handlers, in process - an agent reads and
keeps what hangs off a file:

| Tool | REST twin | What it does |
|------|-----------|--------------|
| `notifications_list` | `GET /api/ai/notifications` | Your bell, newest first (`unread`, `limit`, `offset`): your notices and the broadcasts you may see. A token confined to a folder reads only the notices about files inside it, and the ones that name no file - as `/api/notifications` ([NOTIFICATIONS.md](NOTIFICATIONS.md#in-app-bell-endpoints)). |
| `notification_read` | `POST /api/ai/notifications/read` | Mark a notice read (`id`), or all you can see (`all: true`). Your own bookkeeping: `read` is enough and no audit row is written, as in the bell. |
| `file_star` | `POST /api/ai/star` | Star a file or folder for yourself (`star: false` takes it off); it is listed under **Starred**. |
| `file_comments` | `GET /api/ai/comments?path=` | The comments on an item; everyone who can see it reads them. |
| `file_comment_add` | `POST /api/ai/comments` | Comment on an item (`text`); its owner is told. Needs the account's `comments.write` and the token's `comments:rw`. |
| `file_comment_delete` | `POST /api/ai/comments/{id}/delete` | Delete your own comment (an administrator, any on the tenant's files). Needs the token's `comments:rw`. |
| `file_permissions` | `GET /api/ai/permissions?path=` | Who may open an item, as its Share dialog shows the **owner**: the grants on it (`direct`), the ones from a folder above (`inherited`), your own level (`effective`). |
| `file_permission_users` | `GET /api/ai/permissions/users?q=` | People in your tenant to grant to, by part of a name or e-mail. |
| `file_permission_set` | `POST /api/ai/permissions` | Grant a person (`user_id`) or a group (`group_id`) `viewer`, `editor` or `owner` on an item: owner level on it and the account's `share.users`, on a storage with access control; a viewer account is never given more than viewer ([RBAC.md](RBAC.md)). |
| `file_permission_revoke` | `POST /api/ai/permissions/{id}/revoke` | Take a grant away by its id (`group: true` for a group's row - the two are numbered apart). |

A star, a comment and a grant are written to the audit log the way the other
door tools are: `ai.file.star`, `ai.file.comment_add`,
`ai.file.comment_delete`, `ai.file.grant_set`, `ai.file.grant_revoke`, with
`via`.

### Encrypted folders and .fxe

filex holds no key for an [end-to-end encrypted folder](E2E-ENCRYPTION.md) or
a [single encrypted file](E2E-ENCRYPTION.md#single-encrypted-files-fxe) (`.fxe`):
the browser encrypts and decrypts, the server stores ciphertext. This surface
cannot decrypt anything either, so it says what it sees and refuses what would
go wrong. Recognising it is a catalogue lookup for the folder's key file and a
name test for `.fxe`; no content is read and no key is asked for.

**What comes back.** Every entry of `file_list`, `file_info`, `file_search`
(and `GET /api/ai/files`, `/info`, `/search`) carries two fields when they
apply:

| Field | Meaning |
|---|---|
| `encrypted: true` | The content is ciphertext filex cannot read: a file or folder inside an encrypted folder, the encrypted folder itself, or a `.fxe`. It is decided by where the item is, not by reading it, so a file written there with `allow_plaintext` is listed as encrypted too. |
| `e2e_root` | The encrypted folder the entry sits in (or is), as `adapter://path`. Absent for a `.fxe` outside any folder: such a file carries its own key slots. |

The folder's key file, `.filex-e2e.json`, is never listed or found by search.

**What is refused.** REST answers `409` with the code in `code`; an MCP tool
answers `isError` with the code as the first word of its text
(`E2E_ENCRYPTED: …`).

| Code | When | What to do |
|---|---|---|
| `E2E_ENCRYPTED` | `file_read` / `GET /api/ai/download` of an encrypted file; `file_unzip` / `archive_extract` of an encrypted archive; `app_actions` / `app_run` / `file_convert` on an encrypted file (an app cannot read it); `file_share` / `POST /api/ai/share` and `file_request_create` of an encrypted folder or anything in it (the explorer's Share answers the same) | Tell the person: they open it in the filex web UI with the folder unlocked, or download it there and run [`filex decrypt`](CLI.md#filex-decrypt---an-encrypted-folder-offline). There is no way to get the plaintext through this surface. |
| `E2E_PLAINTEXT_REFUSED` | `file_write`, `POST /api/ai/upload`, `file_upload_ticket` (at mint and at redeem), `file_zip`'s and `archive_create`'s `dest`, and `file_unzip`'s and `archive_extract`'s destination inside an encrypted folder; also ShareX uploads, which have no flag | The bytes would be stored there **unencrypted**. Upload through the web UI with the folder unlocked; or, when the person wants plaintext there, repeat with `allow_plaintext: true` (a form field `allow_plaintext=true` on a multipart upload). |
| `E2E_BOUNDARY` | `file_move`, `file_copy`, or `file_zip` / `archive_create` packing to another folder, would take an encrypted file out of its folder, a plaintext one into it, or cross two encrypted folders | The [transfer guard](E2E-ENCRYPTION.md#ways-plaintext-still-reaches-the-server) the explorer's paste and move obey. The encrypted folder itself may be moved or zipped whole; its key file travels with it. `allow_plaintext` lets a zip land in an encrypted folder; nothing lets ciphertext out. |

The key file is not yours to change from here: writing, renaming, moving or
deleting `.filex-e2e.json` (or creating one where there was none, which would
make an ordinary folder look encrypted) answers `403 RESERVED_NAME`, with or
without `allow_plaintext`. An archive member with that name is skipped.

A `.fxe` **can** be shared: the link hands it out as it is, the answer says
`encrypted: true`, and its recipient needs the file's password.

### What a write through this surface now does

⚠ Until v0.34.0 an agent's write reached the storage and the catalogue and
stopped there. It is worth knowing what changed, because two of these were
visible as "the agent's file is missing" rather than as an error:

- **it is indexed**, so a file an agent wrote is findable by search
  immediately, instead of only after the next storage sync;
- **it announces itself**, so a browser with that folder open sees it appear
  ([REALTIME.md](REALTIME.md));
- **it emits an event** - `file.uploaded` when the write created a file and
  **`file.updated`** when it replaced one. A `file_write` over an existing path
  is therefore a different event id than it used to be, for anybody with a
  webhook on it ([NOTIFICATIONS.md](NOTIFICATIONS.md));
- **it is virus-scanned**, on the same terms as a browser upload
  ([PROTECTION.md](PROTECTION.md));
- `file_delete` **removes the document from the search index**. It did not,
  so a file an agent deleted stayed findable for good, under its
  `.filex-trash/…` alias, pointing at a path with nothing behind it;
- `file_move` on a **folder** re-homes every descendant. It moved only the top
  row, so the listing afterwards handed out paths that 404;
- **it is audited once, by what it did.** Every tool that changes something
  (`file_write`, `file_upload_ticket`, `file_mkdir`, `file_move`, `file_delete`,
  `file_tags` with `set`, `file_share`, `file_unshare`, `file_zip`,
  `file_unzip`) leaves ONE audit row: the row its REST twin under `/api/ai`
  leaves - the same action (`ai.file.upload`, `ai.file.mkdir`, `ai.share.create`…)
  and target type, the token's user, the caller's address, `token_id`,
  `token_username` - with `via: "mcp"` where the REST row says `via: "api"`.
  Like the REST row it names no path, so nothing about a confined token's
  folder - or anything outside it - reaches the log. A read (and `initialize`,
  `tools/list`) leaves no row. ⚠ Until 0.50 every MCP request, reads included,
  wrote one `ai.file.mcp` row and no write said what it changed. The tools of
  the [explorer's operations](#copy-apps-operations-trash-versions-archives-links)
  follow the same rule, with what the explorer's handler adds to its row (the
  entries a restore brought back, the folder of a file request, the files an
  app's action read - each inside the token's root, which the handler checked
  first): `ai.file.copy`, `ai.file.restore` (trash), `ai.file.version_restore`,
  `ai.file.version_snapshot`, `ai.file.archive_create`,
  `ai.file.archive_extract`, `ai.file.op_cancel` (the operation's id as
  target), `ai.share.create` for a file request (the link's id and folder),
  and an app's action - `app_run`, `file_convert` - once queued is the one
  `app_plugin.action_run` row the explorer's menu writes, with the token and
  `via` on it.

**Admin tools** (`admin_*`) - registered **only** when the token carries the
`admin` scope. They cover these areas of the admin panel, and so does
`/api/ai/admin/*`: dashboard, users, storages, settings, sync runs, shares,
trash, search index, auth providers (also adding and deleting one, and which
tenants it serves: `admin_auth_providers_create`, `_delete`, `_set_tenants`),
tenants (`admin_tenants_*`, the platform operator's,
[TENANT-ADMIN.md](TENANT-ADMIN.md#mcp-tools)), external services (and their
test, `admin_external_test`, which takes the form's unsaved
`{enabled, url, secret, callback_url}` and measures ONLYOFFICE's way back to
filex through the door a document uses), replica,
replication targets, queue, notifications and webhook targets, audit, RBAC
grants, protection and archive settings, the files apps have locked, and
sign-in security (the [attempt limits](CONFIGURATION.md#sign-in-attempt-limits):
settings, locks, unlock, the sign-in trail). Each runs the same handler the
admin SPA calls and every **mutating call is written to the audit log
once** - action prefixed `ai.`, except the sign-in limit's own families
(`login.`, `login_security.`), which keep one name whatever the door - with
`via: "mcp"`, the token and the caller's address in the row. Each tool answers
`{status, result}`: the handler's HTTP status and its JSON answer (an object or
a list); a `4xx`/`5xx` is a tool error carrying the same. ⚠ Until 0.50 every
tool whose answer is an object - most of them, refusals included - came back
as a JSON-RPC error `validating tool output` although the handler HAD run, so
an agent was told a change it had just made failed.

On a **public demo** ([DEMO.md](DEMO.md)) the admin tools' writes are refused
like `/api/ai/admin`'s - `403`, `"demo": "read-only"` - because they reach the
same handlers in-process, past the route guard; their reads mask the visitors'
and the operator's addresses as the panel does.

- **Sign-in security:** `admin_login_security_get`, `_update`, `_locks`,
  `_unlock`, `_attempts` - the platform operator's only in multi-tenant mode
  (`403 supertenant_only` for a tenant's key). An unlock is audited as
  `login.unlocked` and a settings change as `login_security.update` - the
  names the panel's carry - with `via: "mcp"`, so the `_attempts` trail lists
  each once and says it came through MCP. `admin_settings_set` on a `login.*`
  key is the same `login_security.update`. `_get` also says what the
  trusted-proxy default `auto` resolved to and why (`trusted_proxies_auto`) and
  which peers send forwarded addresses without being trusted
  (`untrusted_forwarders`); `_update` takes
  `{"trusted_proxies": ["auto", "<address>"]}` to add one
  ([BACKEND.md](BACKEND.md#admin-sign-in-security)).
- **Grants:** `admin_grants_list` (each row `kind: user|group`),
  `admin_grant_set` (`user_id` or `group_id`), `admin_grant_revoke` (a
  person's grant) and `admin_group_grant_revoke` (a group's - the two are
  numbered apart).
- **Storages** (0.50): `admin_storages_sync` takes `path` to rescan one
  folder instead of the whole storage (REST `?path=`), `admin_storages_sync_runs`
  and `admin_storages_drift` read a storage's own runs and drift report, and
  `admin_storages_order` sets the order storages are listed in for everyone
  (`{ids}`, every storage's id; a person's own order still comes first for
  them, [STORAGE.md](STORAGE.md)).
- **Trash** (0.50): `admin_trash_restore` and `admin_trash_purge` take
  `queued: true` to run as a job of the operations queue - a restore then
  takes a batch, `body: {node_ids: [...]}` (at most 1000) - and answer `202`
  with the operation(s), which `op_get` follows and `op_cancel` stops
  ([TRASH-VERSIONING.md](TRASH-VERSIONING.md#trash-endpoints)).
- **Replica** (0.50): `admin_replica_failures_count`, `admin_replica_fix`
  (every unresolved failure) and `admin_replica_fix_one`
  (`{storage_id, path, op}`; 0.53 names the storage, a failure's path is
  relative to it). 0.53 adds `admin_replica_initial_copies`: each
  replicating storage's [initial copy](REPLICATION.md#initial-copy) and how
  far it has come, and `admin_replica_links`: the folder each storage writes
  into on its target. `admin_replication_targets_*` answer with a target's
  credentials masked (`***`).
- **Webhook targets** (0.50): `admin_webhooks_list`, `_create`, `_update`,
  `_delete`, `_test` - the targets of Admin → Notifications → Webhooks
  ([NOTIFICATIONS.md](NOTIFICATIONS.md)), beside the older single
  `admin_notifications_webhook_*`. A secret is written, never read back.
- **Protection and archives** (0.50): `admin_protection_get` / `_update` and
  `admin_archives_get` / `_update` / `_test` run the Protection and Archives
  pages' handlers, so a value out of bounds is refused (`400`) - unlike
  `admin_settings_set`, which writes a raw key without that check.
- **App locks** (0.50): `admin_app_locks_list` lists the files apps have
  frozen (a document out for signature) and `admin_app_unlock`
  (`{storage_id, path}`) lifts one by force; the unlock is ONE audit row,
  `ai.app_plugin.unlock` (the panel's own is `app_plugin.unlock`, one row too -
  until 0.50 it wrote a generic `app-plugins.delete` beside it).

The supertenant rule comes from the handlers themselves: webhook targets,
protection, archives, replica and app locks are the platform operator's in
multi-tenant mode (`403 supertenant_only` for a tenant's key), exactly as on
the panel's routes.

⚠ **Not the whole panel.** A tenant's own domains and its own sign-in
providers (Admin → My tenant), roles and a person's exceptions, groups
(a group's folder grants do have tools: `admin_grant_set` with `group_id`,
`admin_group_grant_revoke`), quotas, version purge, duplicates, themes,
storage discovery, the thumbnail tools, usage & cost, self-update and the AI
tokens themselves have no `admin_*` tool and no route under `/api/ai/admin`;
they are reachable only through the panel's own `/api/admin/*` routes.
Plugins - apps and storage plugins - have tools that **read** them, **leave
install requests** and lift an app's lock, and nothing else
([below](#plugin-tools)): an app's settings, its action overrides, going back
to its previous version, the signing certificate authority and the links apps
opened stay in the panel too. Examples:
`admin_users_create`, `admin_storages_create`, `admin_settings_set`,
`admin_grant_set`, `admin_trash_restore`, `admin_queue_retry`.
`admin_users_update` also switches an account on - approving one an SSO
sign-in opened waiting for approval - and removes an account's SSO bind
(`sso_unlink: true`); `admin_tenants_create` / `_update` take
`oidc_trust_email`, and an OIDC's `trust_email` goes in the provider's config
([SSO.md](SSO.md#which-account-an-sso-sign-in-opens)).

### Plugin tools

⚠⚠ **An agent cannot install a plugin.** Installing, upgrading, removing,
switching and re-permissioning a plugin - an app or a storage plugin - need an
administrator signed in to the admin panel; an API key is refused `403
session_required` on those routes whatever its scopes. An agent **leaves a
request**, filex freezes what the source answered (the manifest, the SHA-256,
the permissions), and an administrator approves or rejects it under **Admin →
Plugins → Install requests**. Approval installs exactly the frozen bytes, or
closes the request as `superseded` when the source has changed by then. The
model: [APP-PLUGINS.md → Install requests](APP-PLUGINS.md#install-requests).

| Tool | What it does |
|------|--------------|
| `admin_app_plugins_list` | The installed apps and the runtime: state, version, granted permissions, source, what the last update check found. |
| `admin_app_plugin_get` | One app (`{id}`): its manifest, the permissions it was granted with their reasons, settings, overrides, schedule, pending update. |
| `admin_app_plugin_logs` | An app's recent log lines (`{id, after?}`). |
| `admin_app_locks_list` | The files apps have frozen (a document out for signature), with the app and its reason (`filters: {storage_id}`). |
| `admin_app_unlock` | Lift one app lock by force (`{storage_id, path}`); the app is not asked. Audited once, `ai.app_plugin.unlock`. |
| `admin_app_plugins_check_updates` | Ask every app's source for a newer version now. Installs nothing. |
| `admin_file_types_list` | Default apps (0.50): every kind of file something besides filex handles, and every kind with a rule - who opens it and who draws its thumbnails, in order, the ones off, default or custom. Read only: a change needs an administrator signed in to the panel ([APP-PLUGINS.md → Default apps](APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)). |
| `admin_plugins_list` | The installed storage plugins: state, version, capabilities, conformance, pending update. |
| `admin_plugin_get` | One storage plugin (`{id}`). |
| `admin_plugins_check_updates` | Ask every storage plugin's source for a newer version now. Installs nothing. |
| `admin_plugin_request_install` | **Leave a request** to install one: `{kind: app, github_repo, ref?, reason}`, `{kind: app, manifest_url, url?, sha256?, reason}`, `{kind: storage, name, source \| url, reason}`. `reason` is required - the administrator reads it. Answers the request (`status: pending`, the frozen permissions and SHA-256) and says it waits for an administrator. |
| `admin_plugin_request_upgrade` | **Leave a request** to upgrade an installed plugin (`name` or `plugin_id`) to the newer version its own source has. |
| `admin_plugin_requests_list` | The requests: `filters: {status: pending (default) \| approved \| rejected \| expired \| superseded \| all}`. |
| `admin_plugin_request_get` | One request with its frozen manifest and review, its status, the decision note and - approved - the installed plugin. |

There is **no tool to approve or reject**, on purpose: approving is the
decision of an administrator signed in to the panel, and a key is not a
person. Asking twice
for the same source answers the waiting request; a request nobody decides
expires after 14 days. The same reads and requests are REST routes under
`/api/ai/admin/app-plugins`, `/api/ai/admin/plugins` and
`/api/ai/admin/plugin-requests`, and the panel's own
`/api/admin/plugin-requests` takes an admin-scoped key too
([BACKEND.md](BACKEND.md#admin-plugin-requests)).

---

## Security

- **Least privilege by scope.** Hand each agent only the verbs it needs; keep
  `admin` for trusted operator tooling. Every token names its scopes (an
  empty list is refused, and grants nothing), and `admin` is never implied. ⚠ Bind the token to a **non-admin**
  account: the panel's `/api/admin/*` routes need the token's `admin` scope as
  well as the account's role, but every file route judges the account, and an
  administrator reaches every folder ([Scopes](#scopes)).
- **Per-agent confinement.** A `root:<adapter>://<rel>` scope is a hard ceiling
  enforced server-side on every path across `/api/files` and `/api/ai`. In a
  multi-tenant deploy, give each project a token confined to its own folder.
- **Same ACL as the UI.** Every file op is gated by the bound user's RBAC grants,
  role ceiling and permissions ([PERMISSIONS.md](PERMISSIONS.md)) -
  identically to the interactive `/api/files` surface, where the token's verbs
  hold too. A `viewer`-bound token can read but never mutate; a token can only
  touch what its user was granted. Read-only storages return `403` for any
  write.
- **Each tool asks what its explorer twin asks** (v0.50, compared one by one):
  `file_zip` asks `files.download` on every source, as `archive/create` does
  (it asked only "may the caller see it", so an account whose downloads were
  taken away could pack a file and fetch the zip); `file_unshare` stays inside
  the tenant and the token's root, as `DELETE /api/files/share/{id}` does;
  `file_mkdir` and `file_move` ask in the folder that gains the entry, as New
  folder, rename and the queued move do. Packing asks through the same
  function the explorer does, and so do a public link and a revoke.
- **Administration stays with a person.** An admin-scoped token reads the
  admin surface and manages ordinary accounts, but it cannot install a plugin,
  make or change an administrator, hand out an admin-area permission or change
  who may encrypt: those answer `403 session_required` and are done by an
  administrator signed in to the panel.
- **Hashed at rest, shown once, revocable.** Only the sha256 hash is stored; the
  plaintext is displayed a single time; any token can be revoked instantly
  (`DELETE`) or aged out with `expires_in_days`.
- **No secret exfiltration via bulk transfer.** `zip`/`unzip` run server-side and
  `file_read` caps at 8 MiB, so large data leaves through auditable share links,
  not the tool channel.
- **App tokens cannot manage credentials.** A shared token proxied in front of
  many visitors is `kind: "app"`, and every self-service credential surface
  refuses it - API tokens, S3 access keys, SSH keys and NFS exports alike. So
  nobody reaching filex through that embed can enumerate or revoke the token
  the embed runs on, nor mint a fresh credential bound to its owner. Keep
  integration tokens `app`; give people their own `user` tokens.

---

## Failure modes & troubleshooting

### 401 Unauthorized (`missing api token` / `invalid api token` / `token expired`)
No token, an unknown token (revoked, mistyped, or wrong environment), or an
expired one. Re-issue and pass it on `X-Filex-Token` or `Authorization: Bearer`.
A bare **`GET /api/ai/mcp` with no token returns 401** - that's expected; an MCP
client must send the header on every request.

### 403 Forbidden (`token missing scope: <x>`)
The token lacks the scope for that verb (e.g. calling `upload` with a read-only
token, or an `admin_*` tool without the `admin` scope). Mint a token with the
needed scope - remember `write` also covers share/zip, `delete` is separate.
`token missing scope: comments:write` is a comment added or deleted by a token
without `comments:rw` - `write` does not cover it; raise the token with `PATCH
… {"permissions": {"comments": "rw"}}` ([Comments](#comments-a-permission-with-a-level)).

### 403 Forbidden (`… is authenticated by an app token …`, `reason: "app_token"`)
A self-service credential call - `/api/tokens`, `/api/auth/s3-keys`,
`/api/auth/ssh-keys`, `/api/auth/nfs-exports` - arrived on a token whose `kind`
is `app`. Either the caller
should be a person (sign in, or use that person's own `user` token), or this
really is one person's token that migration `00030` defaulted to `app` - flip it
with `PATCH /api/admin/ai-tokens/{id}` `{"kind":"user"}`. The message names the
token's label and id so it can be found from a proxy log.

### 403 Forbidden (`reason: "token_ceiling"`)
A token tried to create a credential wider than itself - a verb it does not
hold, a folder outside its `root:`, or an expiry past its own. The message
names what was too wide. Give the automation a token that holds what it hands
out, or create the credential from a signed-in browser (a session has no
ceiling to exceed). Credentials created before v0.43.0 are untouched.

### 403 Forbidden (`session_required`)
An API key asked for something only an administrator signed in to the panel
may do. No scope changes that.

- **A plugin route** - installing, upgrading, removing or switching an app or
  a storage plugin, or approving / rejecting a request. Leave a request with
  `admin_plugin_request_install` (or `POST /api/admin/plugin-requests`, which
  the refusal names in `request_endpoint`) and an administrator decides it
  ([Plugin tools](#plugin-tools)).
- **An administrator's credential** - `admin_users_create` with the `admin`
  role, `admin_users_update` promoting an account to administrator, changing
  an administrator's role or password or removing an administrator's SSO bind
  (`sso_unlink`), `admin_users_reset_password` on an administrator. Managing accounts that are not administrators works with a
  key ([RBAC.md](RBAC.md#administration-and-plugins-need-a-session)).
- **Who may encrypt** - changing a tenant's encryption policy or the operator's
  ceiling, deciding an encryption request, and writing the `e2e.policy` key
  through `admin_settings_set` or `admin_settings_update`
  ([E2E-ENCRYPTION.md → Who may encrypt](E2E-ENCRYPTION.md#who-may-encrypt)).

### 403 Forbidden (`permission_denied` / `your account lacks the … permission`)
The account behind the token lacks a permission for this
([PERMISSIONS.md](PERMISSIONS.md)) - the token's scopes are not the problem.
Without `ai.use` every `/api/ai` call answers `permission_denied`, naming the
`permission` and where the answer came from (`source`); a tool refused on one
file says `access denied: your account lacks the share.links permission` (or
the file permission it needed). An administrator changes it on the person's
role or as an exception.

### 403 Forbidden (path outside confined root
The path is outside the token's `root:` ceiling, outside an `X-Filex-Root`
narrowing, or the bound user lacks an RBAC grant there. Call `file_root` /
`GET /api/ai/root` to see your root and use a **bare relative path** under it.
**An agent that cannot write outside its folder is confinement working as
intended** - widen the token's `root:` scope (or grant) only if that's genuinely
required.

### MCP client won't connect / stream drops
Almost always the reverse proxy: it's buffering the SSE stream, timing out the
`GET`, or stripping the auth header. Allow SSE (disable response buffering) for
`/api/ai/mcp` and pass the `X-Filex-Token` / `Authorization` header through.
Confirm the backend directly with the `tools/list` curl above.

### `file too large for inline read`
`file_read` rejects files over 8 MiB to avoid stuffing a huge blob into a
JSON-RPC response. Use the REST `GET /api/ai/download?path=…` stream, or
`file_share` the file and fetch the link.

### `not indexed yet` when sharing
`file_share` needs the target in filex's node cache. Write or list it first so
the entry exists, then share - a write through this surface catalogues the file
before it returns, so this now really only affects a file that appeared on the
storage out of band, which waits for the next [sync](STORAGE.md#sync).

### Ticket redeem answers: `404`, `410`, `409`, `411`, `413`

Every refusal carries a **`hint`** saying what to do next, because the right
reaction differs and a bare code cannot express it:

| Status | `error` | What the `hint` tells you to do |
|---|---|---|
| 404 | `ticket_not_found` | Unknown **or already redeemed** (deliberately the same answer) - mint a new ticket. |
| 410 | `ticket_expired` | Mint a new ticket and upload to the new URL. |
| 409 | `ticket_in_use` | Another transfer is in flight - wait, don't start a second one. |
| 411 | `content_length_required` | The body came chunked; use `curl -T`, which always sends a length. The ticket survives. |
| 413 | `file_too_large` | **The ticket is still valid** - retry the *same* URL with a file within `max_bytes`. The reply also echoes `sent_bytes`. |
| 503 / 507 | `storage_unavailable` / `quota_exceeded` | The storage backend refused, not your request: retry later, or free space. |

Minting refuses in the caller's own terms too: pointing `path` at a folder
answers `"…" already exists as a FOLDER. …`path` must be the full destination
file path (e.g. "…/<filename>")` rather than a driver-level kind-conflict.

### `no storage configured` (503)
No enabled storage exists to serve the request. Add one (see
[STORAGE.md](STORAGE.md)) before pointing an agent at filex.

### `ENTRY_UNAVAILABLE` (409)
The path, or a folder above it, is an entry the storage could not answer for:
the sync asked whether it still exists and got neither "yes" nor "not found"
(0.50, issue #104). `file_list` shows such an entry with `unavailable: true`
and the storage's answer in `unavailable_reason`; every tool and REST call on
it, or on anything inside it, answers `409 {"code": "ENTRY_UNAVAILABLE", "path",
"reason"}` (an MCP tool says the same in its error text). Nothing is wrong with
the request: wait for the next sync, which lifts the mark when the storage
answers again. See [PLUGINS.md → An entry your Stat cannot answer
for](PLUGINS.md#an-entry-your-stat-cannot-answer-for).

---


## Moving files between storages

`file_move` (MCP) and `POST /api/ai/move` carry a file - or a whole folder -
from one storage to another. There is no server-side rename between two
backends, so filex streams the bytes through the same engine the queue uses for
a cross-storage paste: every file is verified on the far side, and only then is
the source removed.

```json
{ "src": "hot://raporlar/2026", "dst": "cold://arsiv/2026" }
```

⚠ The source is **deleted, not trashed** - moving between storages is done to
free the first one. A read-only destination, or a folder you have no editor
right on, is refused before a byte moves. Same-storage moves are a plain
rename.

### A move never overwrites

⚠⚠ If `dst` is already taken, the arriving item lands on a **free name beside
it** - `rapor.txt` → `rapor-copy.txt`, then `rapor-copy-2.txt` - exactly as a
paste in the web UI does. Nothing is replaced and nothing is lost; the
destination folder simply ends up holding both files. Moving an item onto its
own path is a no-op.

⭐ **So read `entry.path` in the answer.** It names where the file really is,
which is not always what you asked for:

```json
{ "entry": { "path": "cold://arsiv/rapor-copy.txt", "name": "rapor-copy.txt", "type": "file" } }
```

An agent that reports the path it requested - rather than the one it got back -
will tell its user the file is somewhere it is not. To *replace* a file on
purpose, `file_write` it (that one does overwrite, and the previous bytes are
kept as a version - see [TRASH-VERSIONING.md](TRASH-VERSIONING.md)).

⭐ `entry.type` is trustworthy too: `"dir"` when you moved a folder, `"file"`
when you moved a file - the same two words `file_list` and `file_info` use. (A
move used to answer `"file"` for everything, folders included, so do not carry
over a habit of ignoring it.)

### Links that cannot travel

A folder moved **between storages** may hold symlinks the source cannot follow -
broken, pointing outside the storage, or a remote link filex does not resolve -
or a folder link back into itself. Those are **left behind, never read**, the
rest of the folder is carried, and because a move deletes only what it carried,
⚠ **the source is kept**. The answer says so:

```json
{ "entry": { "path": "cold://arsiv/proje", "name": "proje", "type": "dir",
             "source_kept": true,
             "left_behind": [ { "path": "hot://proje/kirik", "reason": "broken" } ] } }
```

`reason` is `broken`, `outside_root`, `unresolved`, `cycle` (a folder link into
what was already carried), `too_deep` (more than 64 folders down - real data)
or `link`. It is a **success**: the copy at `entry.path` is complete without
those entries. Do not retry - a retry lands a second copy on a free name. Tell
your user what stayed, and delete the source only if they want it gone.

**`dst` is never written over - and the move is not refused either.** A taken
`dst` gets a free name beside it (above), so read `entry.path`. Only when
every candidate name is taken too does the move refuse - `409` with
`code: NO_FREE_NAME` on REST, an error starting `NO_FREE_NAME:` from the MCP
tool - and then nothing moves; `503` when the backend cannot say
whether the name is free. A move onto its own path changes nothing, and a
case-only rename is allowed. (Before v0.43.0 the move replaced the file that
had the name, and a transfer between storages wrote over the destination
file.) ⚠ An explorer **rename**, where a person typed the name, answers
`409 NAME_TAKEN` instead - de-collision is for an agent, which has nobody to
ask.

## See also

- [RBAC.md](RBAC.md) - per-file/folder permissions, self-service token endpoints, and the scope/grant ceiling model
- [STORAGE.md](STORAGE.md) - storage adapters and the `adapter://path` addressing tokens use
- [SSO.md](SSO.md) - interactive login (the account roles tokens inherit)
- [CONFIGURATION.md](CONFIGURATION.md) - global config/env reference
- [API.md](API.md) - the embeddable `<filex-explorer>` component (browser UI, not the token surface)
