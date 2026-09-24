# AI / MCP & API tokens

filex exposes a **token-authenticated automation surface** at `/api/ai` so an AI
agent (Claude, etc.) or a host application can drive the file manager
programmatically — list, read, write, move, delete, search, share, and zip
files — with no browser session. The same surface speaks **Model Context
Protocol** (MCP) at `/api/ai/mcp`, so an MCP-capable model gets filex as a native
tool set.

The interface calls these credentials **API keys** (the admin panel's **API / MCP**
page, the explorer's **API keys** entry); the HTTP API and this page call them
tokens (`X-Filex-Token`, `fxt_…`) — they are the same thing.

Everything here is authenticated by an **API token** (never a cookie), scoped to
a set of verbs, optionally locked to a single sub-folder, and gated by the same
[RBAC](RBAC.md) grants that apply to the interactive UI. A token can therefore be
handed to an agent that only ever sees — and can only ever touch — one project's
folder.

- [API tokens](#api-tokens) — [Creating a token](#creating-a-token) · [Scopes](#scopes) · [Root confinement](#root-confinement)
- [REST surface (`/api/ai`)](#rest-surface-apiai)
- [MCP endpoint (`/api/ai/mcp`)](#mcp-endpoint-apiaimcp)
- [Tool set](#tool-set)
- [Security](#security)
- [Failure modes & troubleshooting](#failure-modes--troubleshooting)
- [See also](#see-also)

---

## API tokens

A token is a **64-character hex string** (32 random bytes). It is shown **once**
at creation time — only its **sha256 hash** is stored in the `api_tokens` table,
so a lost token cannot be recovered, only revoked and re-issued.

Every token is **bound to a user** and inherits that user's account role
(`admin` / `user` / `viewer`) and RBAC grants. Authenticating with a token is
exactly like that user signing in — minus the cookie. An optional
`expires_in_days` sets a hard expiry; an expired token authenticates as nobody
(401). filex stamps each token's last-used time on every request.

### Token kinds — `user` vs `app`

Because a token acts AS its owner, "who is calling" and "is there a **person**
behind this call" are two different questions. A token answers the second one
with its `kind`:

| Kind | What it is | Minted by | Identity surfaces |
|---|---|---|---|
| `user` | one person's own credential — their CLI, WebDAV/SFTP/FTPS/S3 client, `filex mount`, the desktop app | `POST /api/tokens` (self-service); the desktop app's sign-in (`POST /api/auth/desktop/complete`, browser session only — see [DESKTOP.md](DESKTOP.md#the-token-the-app-is-given)) | all of them |
| `app` | an integration — a host app's proxy, a bot, an MCP client | `POST /api/admin/ai-tokens` | suppressed |

For an `app` token filex refuses every **self-service credential surface** with
403 — `/api/tokens`, `/api/auth/s3-keys`, `/api/auth/ssh-keys`,
`/api/auth/nfs-exports`, and reading a share link's PIN back
(`GET /api/shares/{id}/pin`) — and the explorer's navigation panel drops **API
keys**, **Recent**, **Starred** and **Shared with me** (and **My shares**, where
the host draws it). Nothing else changes:
same scopes, same RBAC, same confinement, and the panel keeps Upload, the
storage list, Trash and "How to connect". Inside "How to connect" the guides
stay and the mint forms are replaced by a line saying this session cannot
create credentials.

All of them refuse from one implementation (`handlers.RequirePersonalCaller`),
because they answer one question: *show me, and let me mint, the credentials of
the person calling.* Gating only the first of them still let an embed visitor
mint an S3 access key bound to the token's owner — the same hole, a different
button.

Why: an embedded explorer is usually proxied with **one shared token** injected
by the host, so every visitor authenticates as the token's owner. Under a
`user` token that would mean any visitor could list — and revoke — the
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
`GET /api/files/capabilities` — `"user"` for a cookie/OIDC session as well as
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

> The `/api/ai` namespace is **token-only** — it never accepts a cookie/JWT
> session. (The interactive `/api/files` surface accepts *either*, so a host app
> can proxy the embedded explorer with a confined token.)

### Creating a token

Two creation paths. The admin door needs an **admin session**; the
self-service door takes a browser session **or a person's own `user`
token** — and since v0.43.0 a token caller may only mint a credential no
wider than itself (see *Ceiling rules*). An `app` token is refused outright:

**1. Admin — `POST /api/admin/ai-tokens`** (admin session). Full control: bind to
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
# → { "token": "<64-hex — shown ONCE>", "row": { … } }
```

- `user_id` — omit to bind the token to the calling admin.
- `kind` — `"app"` (default here) or `"user"`. This surface issues
  integrations' credentials, so it defaults to `app`; pass `"user"` when an
  admin is minting a personal token on somebody's behalf. An unknown value is
  rejected (400) rather than folded into `app`.
- `scopes` — comma-separated allow-list (see [Scopes](#scopes)), **required:
  at least one verb**. An empty list is refused with `400 scopes_required`
  (since v0.43.0 — before, it granted every scope, `admin` included). `admin`
  is granted only when it is in the list. Any scope outside the canonical set
  is rejected up front (`400 scope_unknown`), so a typo can't silently grant
  nothing.
- `label` / `expires_in_days` — optional.

`GET /api/admin/ai-tokens` lists all tokens (no secrets); `DELETE
/api/admin/ai-tokens/{id}` revokes one.

**2. Self-service — `POST /api/tokens`** (any authenticated user, including
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
its label / usernames; `DELETE /api/tokens/{id}` revokes one
(ownership-checked). `kind` is **not** editable here — only an admin changes it,
or an app token could promote itself out of the restriction.

⚠ The whole `/api/tokens` surface answers **403** to an app token (see
[Token kinds](#token-kinds--user-vs-app)). A cookie/OIDC session and a `user`
token both work exactly as before.

### Scopes

> ⚠ **The interface calls these permissions.** Since v0.43.0 the API keys
> panel, its table and the app install review all say *permission*; the
> request field, the error codes and this reference still say `scopes`,
> because that is the name the API accepts. Only the word a person reads
> changed — `{"scopes": "read,write,mcp"}` is unchanged on the wire.

`RequireScope` gates each verb. A token grants **exactly the scopes in its
list** — an empty list grants **nothing** (since v0.43.0; until then it
granted everything, `admin` included). No door issues an empty list any more,
and the upgrade to v0.43.0 rewrote every existing empty list as the explicit
full list `read,write,delete,mcp,admin`, so an old token kept exactly the
access it had — see the CHANGELOG's upgrade note, and review those tokens.

| Scope | Grants |
|-------|--------|
| `read` | `list` / `info` / `download` / `search` (read-only file ops) |
| `write` | `upload` / `mkdir` / `move` **and** `share` / `unshare` / `zip` / `unzip` |
| `delete` | `delete` (soft-delete to trash) |
| `mcp` | the streamable-HTTP MCP server at `/api/ai/mcp` |
| `admin` | the admin REST surface at `/api/ai/admin/*` **and** the `admin_*` MCP tools — a subset of the admin panel, listed [under Tool set](#tool-set) |

> **Least privilege.** Give an agent only what it needs — most read/write agents
> want `read,write,mcp`. `admin` is a superuser scope (it can manage users,
> storages, settings, replica, queue …); reserve it for trusted operator tools.

> ⚠⚠ **Mint agent and embed tokens on a non-admin account** (`user_id`). Scopes
> are enforced on `/api/ai`; the admin panel's own `/api/admin/*` routes and
> `/metrics` are gated on the **account's role**, so a token is only as limited
> as the account it is bound to.

### Root confinement

A `root:<adapter>://<rel>` scope locks the token to **one sub-folder** — a **hard
ceiling** it cannot escape. `<adapter>` is a storage name (see [STORAGE.md](STORAGE.md));
`<rel>` is a path within it. Example: `root:s3://projects/acme`.

- The ceiling is enforced on **both** the `/api/files` UI surface and the
  `/api/ai` REST + MCP surface — every path-bearing operation routes through a
  single chokepoint.
- A confined caller treats its root as `/`: a **bare relative path** (e.g.
  `"reports/q3.csv"`) resolves *under* the root, and an empty path means the root
  itself. Fully-qualified `adapter://root/...` paths are validated as-is.
  Anything outside is rejected **403**.
- The **`X-Filex-Root: <adapter>://<rel>`** request header can **narrow further**
  within the token root (it can only narrow — a header that tries to widen past
  the token ceiling is rejected). This header is applied by the `/api/files`
  confinement middleware, the path a host app uses when it proxies the embedded
  explorer per-request. On the direct `/api/ai` surface, confinement comes from
  the token's `root:` scope.
- The **notification bell** (`/api/notifications`: the list, the unread
  count, mark-read and mark-all-read) is confined the same way: a confined
  token reads — and marks read — only the notices about files inside its
  root, its owner's own notices included; a notice that names no file (the
  admin page's test, an app's notice about a list) stays readable. The admin
  history and the admin surface refuse a confined token outright.
- A confined agent should call **`GET /api/ai/root`** (or the **`file_root`** MCP
  tool) first: it reports whether you're confined, your root, the storage
  adapters you can address, and a hint on how to phrase paths — so the agent
  stops guessing adapter names.

> A token that only *knows* a folder id/name still cannot reach it: confinement
> is a server-side ceiling, not an argument the caller supplies.

---

## REST surface (`/api/ai`)

Token-only JSON over HTTP. All paths use the `adapter://relative/path` wire form
(adapter = storage name); an empty/relative path defaults to the first enabled
storage's root (or, when confined, your root).

| Method | Path | Scope | Body / query |
|--------|------|-------|--------------|
| GET | `/api/ai/root` | *(any valid token)* | — → confinement root + reachable storages |
| GET | `/api/ai/files?path=` | `read` | → `{entries:[…]}` |
| GET | `/api/ai/info?path=` | `read` | → `{entry:{…}}` |
| GET | `/api/ai/download?path=` | `read` | → raw bytes (stream) |
| GET | `/api/ai/search?path=&q=` | `read` | → `{entries:[…]}` — names and `tag:` filters only; content search is the MCP `file_search` tool |
| GET | `/api/ai/tags?path=` | `read` | → `{path, tags:[{name, kind}], can_edit_team}` — the file's tags **as the token's user sees them** ([Tags](SEARCH.md#tags--personal-and-team)) |
| POST | `/api/ai/tags` | `write` | `{path, tags:[{name, kind}]}` — the tags that user can see become exactly this list (`[]` clears them); every item names its `kind` |
| POST | `/api/ai/upload` | `write` | `{path, content}` / `{path, content_base64}` / multipart `file` |
| POST | `/api/ai/upload/ticket` | `write` | `{path, expires_in_seconds?, max_bytes?}` → `{url, ticket, path, max_bytes, expires_at, curl}` |
| PUT/POST | `/u/{ticket}` | *(none — see below)* | raw body (`curl -T`) or multipart `file` → `{entry:{…}}` |
| POST | `/api/ai/mkdir` | `write` | `{path}` |
| POST | `/api/ai/move` | `write` | `{src, dst}` — across storages too (see [below](#moving-files-between-storages)). `409` when `dst` is already taken |
| POST | `/api/ai/delete` | `delete` | `{path}` → soft-delete to trash |
| POST | `/api/ai/share` | `write` | `{path, pin?, expires_in_days?, max_downloads?}` → `{url, token, pin?}` |
| POST | `/api/ai/unshare` | `write` | `{token}` |
| POST | `/api/ai/zip` | `write` | `{sources:[…], dest}` (server-side) |
| POST | `/api/ai/unzip` | `write` | `{src, dest}` (server-side) |
| `*` | `/api/ai/admin/*` | `admin` | the same admin handlers the panel calls, for the areas listed [under Tool set](#tool-set) |

Notes:

- **Upload** takes UTF-8 text (`content`), base64 (`content_base64`), or a
  `multipart/form-data` `file` field for large binaries.
- **Upload tickets** solve the one case an agent cannot: a large file already on
  its own disk. Anything sent through a tool call travels inside that call — on
  MCP, through the model's context — so a 130 MB file (~173 MB of base64) simply
  cannot go that way. `POST /api/ai/upload/ticket` splits the job: this
  authorized call pins the destination under *your* token, and the `url` it
  returns accepts exactly one upload **with no credentials at all**, so an agent
  that has no filex token can still finish the transfer. The reply hands over a
  ready `curl` line, a `powershell` equivalent for machines without curl, and a
  `next` line for the case with no shell at all (give it to the user). The ticket is
  single-use, short-lived (default 30 min, `max 24 h`), cannot read/list/delete,
  and cannot be pointed anywhere else — the redeemer never supplies a path. The
  bytes are written as, and billed to, the minter. Tickets live in memory, so a
  restart drops the unredeemed ones (mint another).
- **Share** mints a public `/s/<token>` link (folders download as a ZIP). The
  target must already be indexed — write or list it first. A generated PIN is
  returned **once**.
- **Zip / unzip run on the server**: the archive is assembled/extracted straight
  into storage and only metadata (the dest entry / a file count) crosses the
  wire. To hand a big zip to someone, `share` the `dest` — don't download it
  through the API. Both are zip-slip protected and confined to the token root.
- Errors map to HTTP status: `404` not found, `403` read-only / out of root /
  insufficient grant, `501` unsupported by the driver, `503` no storage
  configured, `400` bad request.

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
token used with it must carry `mcp`. The file tools need nothing more; the
`admin_*` tools additionally need `admin` (and an unconfined token).

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
> the proxy in front of filex must allow SSE — don't buffer the response, keep
> the connection open, and don't strip the `X-Filex-Token` / `Authorization`
> header. (Nginx: `proxy_buffering off;` for this location.)

The tools each MCP session sees depend on the token's scopes: an `admin`-scoped
token additionally sees every `admin_*` tool (below); a non-admin token never
sees them in `tools/list` at all.

---

## Tool set

**Core file tools** (available to any `mcp`-scoped token, gated by the bound
user's role + grants + confinement):

| Tool | What it does |
|------|--------------|
| `file_root` | Report your access scope: confinement root (if any) + addressable storages. **Call this first.** |
| `file_list` | List a directory (`adapter://dir`; empty = first storage root). |
| `file_info` | Metadata (size, mime, type, modified time) for one path. |
| `file_read` | Read a file. UTF-8 text when the bytes are valid UTF-8, else base64. **Rejects files > 8 MiB** — use the REST `download` stream for those. |
| `file_write` | Create/overwrite a file (`content` text or `content_base64` binary). Content you generate — never a file off your disk. |
| `file_upload_ticket` | Get a short-lived, **credential-free** URL (plus the ready `curl -T` line) for a LOCAL file of any size. The bytes never enter the conversation; the URL takes one upload to a fixed path. |
| `file_delete` | Soft-delete to filex trash (recoverable from the UI). |
| `file_move` | Move or rename a file/folder. **Never overwrites**: a destination that is already taken gets a free name beside it (`rapor-copy.txt`), so read the returned `entry.path` rather than assuming the one you asked for; `409 NO_FREE_NAME` when every candidate name is taken too. Works across storages: the bytes are copied and verified, then the source is removed — unless something was left behind (`entry.source_kept`, [below](#links-that-cannot-travel)). |
| `file_mkdir` | Create a directory. |
| `file_search` | Search file/folder names **and** (by default) extracted file contents in a storage. Forgiving on separators and typos; words may be in any order and, with the search index, may be answered by a folder (`main code` finds `Code/main.go`; without the index, or with `content=false`, every word has to be in the file's own name); supports `tag:` / `-tag:` filters (your personal and your team's tag of that name both count); `content=false` restores name-only. |
| `file_tags` | Read a file's tags (`{path}`), or set them (`{path, set:[{name, kind}]}`). Every tag says its **kind**: `personal` (only the token's user sees it) or `team` (everyone in the tenant who can see the file; adding or removing one needs edit permission — `can_edit_team` says whether you have it). There is **no default kind** on this surface: an agent names the kind of every tag it writes. Other people's personal tags and other tenants' tags are never shown or touched. |
| `file_share` | Public share link for a file/folder (folders → ZIP); optional PIN/expiry/max-downloads. Use this to hand a file to someone instead of streaming it back. |
| `file_unshare` | Revoke a share by its token. |
| `file_zip` | Pack files/folders into a `.zip` **on the server** (dest lands in storage; share it to download). |
| `file_unzip` | Extract a stored `.zip` into a directory **on the server** (zip-slip protected, stays within your root). |

### What a write through this surface now does

⚠ Until v0.34.0 an agent's write reached the storage and the catalogue and
stopped there. It is worth knowing what changed, because two of these were
visible as "the agent's file is missing" rather than as an error:

- **it is indexed**, so a file an agent wrote is findable by search
  immediately, instead of only after the next storage sync;
- **it announces itself**, so a browser with that folder open sees it appear
  ([REALTIME.md](REALTIME.md));
- **it emits an event** — `file.uploaded` when the write created a file and
  **`file.updated`** when it replaced one. A `file_write` over an existing path
  is therefore a different event id than it used to be, for anybody with a
  webhook on it ([NOTIFICATIONS.md](NOTIFICATIONS.md));
- **it is virus-scanned**, on the same terms as a browser upload
  ([PROTECTION.md](PROTECTION.md));
- `file_delete` **removes the document from the search index**. It did not,
  so a file an agent deleted stayed findable for good, under its
  `.filex-trash/…` alias, pointing at a path with nothing behind it;
- `file_move` on a **folder** re-homes every descendant. It moved only the top
  row, so the listing afterwards handed out paths that 404.

**Admin tools** (`admin_*`) — registered **only** when the token carries the
`admin` scope. They cover these areas of the admin panel, and so does
`/api/ai/admin/*`: dashboard, users, storages, settings, sync runs, shares,
trash, search index, auth providers, external services, replica, replication
targets, queue, notifications, audit, and RBAC grants. Each runs the same handler
the admin SPA calls and every **mutating call is written to the audit log**
(action prefixed `ai.`).

⚠ **Not the whole panel.** Tenants (providers), webhook targets, storage
plugins, quotas, version purge, duplicates, protection/antivirus, usage & cost,
self-update and the AI tokens themselves have no `admin_*` tool and no route
under `/api/ai/admin`; they are reachable only through the panel's own
`/api/admin/*` routes. Examples:
`admin_users_create`, `admin_storages_create`, `admin_settings_set`,
`admin_grant_set`, `admin_trash_restore`, `admin_queue_retry`.

---

## Security

- **Least privilege by scope.** Hand each agent only the verbs it needs; keep
  `admin` for trusted operator tooling. Every token names its scopes (an
  empty list is refused, and grants nothing), and `admin` is never implied. ⚠ Bind the token to a **non-admin**
  account: the panel's `/api/admin/*` routes check the account's role rather
  than the token's scopes ([Scopes](#scopes)).
- **Per-agent confinement.** A `root:<adapter>://<rel>` scope is a hard ceiling
  enforced server-side on every path across `/api/files` and `/api/ai`. In a
  multi-tenant deploy, give each project a token confined to its own folder.
- **Same ACL as the UI.** Every file op is gated by the bound user's RBAC grants
  and role ceiling — identically to the interactive `/api/files` surface. A
  `viewer`-bound token can read but never mutate; a token can only touch what its
  user was granted. Read-only storages return `403` for any write.
- **Hashed at rest, shown once, revocable.** Only the sha256 hash is stored; the
  plaintext is displayed a single time; any token can be revoked instantly
  (`DELETE`) or aged out with `expires_in_days`.
- **No secret exfiltration via bulk transfer.** `zip`/`unzip` run server-side and
  `file_read` caps at 8 MiB, so large data leaves through auditable share links,
  not the tool channel.
- **App tokens cannot manage credentials.** A shared token proxied in front of
  many visitors is `kind: "app"`, and every self-service credential surface
  refuses it — API tokens, S3 access keys, SSH keys and NFS exports alike. So
  nobody reaching filex through that embed can enumerate or revoke the token
  the embed runs on, nor mint a fresh credential bound to its owner. Keep
  integration tokens `app`; give people their own `user` tokens.

---

## Failure modes & troubleshooting

### 401 Unauthorized (`missing api token` / `invalid api token` / `token expired`)
No token, an unknown token (revoked, mistyped, or wrong environment), or an
expired one. Re-issue and pass it on `X-Filex-Token` or `Authorization: Bearer`.
A bare **`GET /api/ai/mcp` with no token returns 401** — that's expected; an MCP
client must send the header on every request.

### 403 Forbidden (`token missing scope: <x>`)
The token lacks the scope for that verb (e.g. calling `upload` with a read-only
token, or an `admin_*` tool without the `admin` scope). Mint a token with the
needed scope — remember `write` also covers share/zip, `delete` is separate.

### 403 Forbidden (`… is authenticated by an app token …`, `reason: "app_token"`)
A self-service credential call — `/api/tokens`, `/api/auth/s3-keys`,
`/api/auth/ssh-keys`, `/api/auth/nfs-exports` — arrived on a token whose `kind`
is `app`. Either the caller
should be a person (sign in, or use that person's own `user` token), or this
really is one person's token that migration `00030` defaulted to `app` — flip it
with `PATCH /api/admin/ai-tokens/{id}` `{"kind":"user"}`. The message names the
token's label and id so it can be found from a proxy log.

### 403 Forbidden (`reason: "token_ceiling"`)
A token tried to create a credential wider than itself — a verb it does not
hold, a folder outside its `root:`, or an expiry past its own. The message
names what was too wide. Give the automation a token that holds what it hands
out, or create the credential from a signed-in browser (a session has no
ceiling to exceed). Credentials created before v0.43.0 are untouched.

### 403 Forbidden (path outside confined root
The path is outside the token's `root:` ceiling, outside an `X-Filex-Root`
narrowing, or the bound user lacks an RBAC grant there. Call `file_root` /
`GET /api/ai/root` to see your root and use a **bare relative path** under it.
**An agent that cannot write outside its folder is confinement working as
intended** — widen the token's `root:` scope (or grant) only if that's genuinely
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
the entry exists, then share — a write through this surface catalogues the file
before it returns, so this now really only affects a file that appeared on the
storage out of band, which waits for the next [sync](STORAGE.md#sync).

### Ticket redeem answers: `404`, `410`, `409`, `411`, `413`

Every refusal carries a **`hint`** saying what to do next, because the right
reaction differs and a bare code cannot express it:

| Status | `error` | What the `hint` tells you to do |
|---|---|---|
| 404 | `ticket_not_found` | Unknown **or already redeemed** (deliberately the same answer) — mint a new ticket. |
| 410 | `ticket_expired` | Mint a new ticket and upload to the new URL. |
| 409 | `ticket_in_use` | Another transfer is in flight — wait, don't start a second one. |
| 411 | `content_length_required` | The body came chunked; use `curl -T`, which always sends a length. The ticket survives. |
| 413 | `file_too_large` | **The ticket is still valid** — retry the *same* URL with a file within `max_bytes`. The reply also echoes `sent_bytes`. |
| 503 / 507 | `storage_unavailable` / `quota_exceeded` | The storage backend refused, not your request: retry later, or free space. |

Minting refuses in the caller's own terms too: pointing `path` at a folder
answers `"…" already exists as a FOLDER. …`path` must be the full destination
file path (e.g. "…/<filename>")` rather than a driver-level kind-conflict.

### `no storage configured` (503)
No enabled storage exists to serve the request. Add one (see
[STORAGE.md](STORAGE.md)) before pointing an agent at filex.

---


## Moving files between storages

`file_move` (MCP) and `POST /api/ai/move` carry a file — or a whole folder —
from one storage to another. There is no server-side rename between two
backends, so filex streams the bytes through the same engine the queue uses for
a cross-storage paste: every file is verified on the far side, and only then is
the source removed.

```json
{ "src": "hot://raporlar/2026", "dst": "cold://arsiv/2026" }
```

⚠ The source is **deleted, not trashed** — moving between storages is done to
free the first one. A read-only destination, or a folder you have no editor
right on, is refused before a byte moves. Same-storage moves are a plain
rename.

### A move never overwrites

⚠⚠ If `dst` is already taken, the arriving item lands on a **free name beside
it** — `rapor.txt` → `rapor-copy.txt`, then `rapor-copy-2.txt` — exactly as a
paste in the web UI does. Nothing is replaced and nothing is lost; the
destination folder simply ends up holding both files. Moving an item onto its
own path is a no-op.

⭐ **So read `entry.path` in the answer.** It names where the file really is,
which is not always what you asked for:

```json
{ "entry": { "path": "cold://arsiv/rapor-copy.txt", "name": "rapor-copy.txt", "type": "file" } }
```

An agent that reports the path it requested — rather than the one it got back —
will tell its user the file is somewhere it is not. To *replace* a file on
purpose, `file_write` it (that one does overwrite, and the previous bytes are
kept as a version — see [TRASH-VERSIONING.md](TRASH-VERSIONING.md)).

⭐ `entry.type` is trustworthy too: `"dir"` when you moved a folder, `"file"`
when you moved a file — the same two words `file_list` and `file_info` use. (A
move used to answer `"file"` for everything, folders included, so do not carry
over a habit of ignoring it.)

### Links that cannot travel

A folder moved **between storages** may hold symlinks the source cannot follow
— broken, pointing outside the storage, or a remote link filex does not resolve
— or a folder link back into itself. Those are **left behind, never read**, the
rest of the folder is carried, and because a move deletes only what it carried,
⚠ **the source is kept**. The answer says so:

```json
{ "entry": { "path": "cold://arsiv/proje", "name": "proje", "type": "dir",
             "source_kept": true,
             "left_behind": [ { "path": "hot://proje/kirik", "reason": "broken" } ] } }
```

`reason` is `broken`, `outside_root`, `unresolved`, `cycle` (a folder link into
what was already carried), `too_deep` (more than 64 folders down — real data)
or `link`. It is a **success**: the copy at `entry.path` is complete without
those entries. Do not retry — a retry lands a second copy on a free name. Tell
your user what stayed, and delete the source only if they want it gone.

**`dst` is never written over — and the move is not refused either.** A taken
`dst` gets a free name beside it (above), so read `entry.path`. Only when
every candidate name is taken too does the move refuse, with
`409 NO_FREE_NAME`, and then nothing moves; `503` when the backend cannot say
whether the name is free. A move onto its own path changes nothing, and a
case-only rename is allowed. (Before v0.43.0 the move replaced the file that
had the name, and a transfer between storages wrote over the destination
file.) ⚠ An explorer **rename**, where a person typed the name, answers
`409 NAME_TAKEN` instead — de-collision is for an agent, which has nobody to
ask.

## See also

- [RBAC.md](RBAC.md) — per-file/folder permissions, self-service token endpoints, and the scope/grant ceiling model
- [STORAGE.md](STORAGE.md) — storage adapters and the `adapter://path` addressing tokens use
- [SSO.md](SSO.md) — interactive login (the account roles tokens inherit)
- [CONFIGURATION.md](CONFIGURATION.md) — global config/env reference
- [API.md](API.md) — the embeddable `<filex-explorer>` component (browser UI, not the token surface)
