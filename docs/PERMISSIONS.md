# filex — roles and per-user permissions

Backend `internal/perm`. What an account may **do**, one permission at a time —
through **roles** (named sets of permissions) and per-person **exceptions** —
alongside the folder grants in [RBAC.md](RBAC.md), which decide where an
account may **reach**.

Every person has **one role**, picked in the Role field on their own page:
a **built-in role** — Administrator (everything, locked), User or Viewer — or
a **custom role**, which is its own list of permissions, ticked the same way.
Roles are managed on Admin → Roles. (In the API and the database a custom
role is a *permission rule* — the names below use both.)

An upgrade changes nobody's access: every account starts from exactly what its
role could do before this existed, until an administrator edits a role, gives
one, or sets an exception.

- [How an answer is reached](#how-an-answer-is-reached)
- [The permissions](#the-permissions)
- [Built-in roles and presets](#built-in-roles-and-presets)
- [Custom roles](#custom-roles)
- [Delegated administration](#delegated-administration)
- [Where each permission is enforced](#where-each-permission-is-enforced)
- [API](#api)
- [Refusals](#refusals)
- [Audit](#audit)
- [Things to know](#things-to-know)

## How an answer is reached

A file action needs **both** of these:

1. **The path** — the folder grant level (RBAC.md): viewer to read, editor to
   change, owner to share with a person. A `viewer` account is capped at viewer
   everywhere; an app lock caps a frozen file at viewer for everyone.
2. **The permission** — resolved per account, lowest layer first:

| Layer | Source |
|---|---|
| Built-in role | the **User** role's permissions (the install defaults) or the **Viewer** role's; Administrator: everything |
| Custom role | the one custom role the account holds, if it is enabled — its own list **replaces** the built-in role's; its "different in some folders" part applies per path |
| Exceptions | the account's own Allow / Deny (*overrides* in the API). **Always win** over roles |

Then two hard lines nothing crosses:

- `admin.full` follows the **built-in role** alone — no custom role or
  exception grants it.
- A `viewer` account never holds a file-changing or sharing permission, even if
  something allows it.

**Administrators are bound by no custom role and no exception** (they could
edit them anyway), including role limits such as Require 2FA.

Every answer carries its **source** — the built-in role, a named custom role,
or this person's exception — which the Users page shows beside each permission
and every refusal names.

## The permissions

28 in five groups. Keys are stored by name, never by position.

### Files (path-checked — also need the folder level)

| Key | Allows |
|---|---|
| `files.download` | Download, zip download, reading over WebDAV / SFTP / FTPS / S3 / NFS, desktop sync. **Previews stay open** — a browser that renders a file has its bytes; this governs the explicit save. |
| `files.create` | Upload a new file, new folder or file, copy into a folder, extract an archive, restore from trash |
| `files.modify` | Overwrite, edit (text editor, ONLYOFFICE), save, restore a version, add to an archive |
| `files.rename` | A new name in the **same** folder |
| `files.move` | Into **another** folder (or storage). The protocols rename and move with one operation, so each one is judged by its two paths: same folder = rename, same name elsewhere = move, both at once needs both, at both ends. A copy is not a move — it needs `files.create` at the destination |
| `files.delete` | Delete to the trash |
| `files.purge` | Permanent delete — emptying the trash, and a delete on a storage that has no trash |
| `files.tag` | Add and remove tags |

A write decides create vs modify **per file**: replacing a file that is there is
`files.modify`, adding one is `files.create`.

### Sharing and features

| Key | Allows |
|---|---|
| `share.links` | Public download links (web, agent API, the invite fallback) |
| `share.upload_links` | Upload-only "drop" links |
| `share.users` | Sharing with another account (the per-item permissions panel, invites) |
| `comments.write` | Posting comments |
| `ai.use` | The agent REST + MCP API at `/api/ai` |
| `plugins.run` | Running app actions and app view events |

### Access outside the web app (checked at login)

| Key | Allows |
|---|---|
| `access.webdav` | WebDAV (checked every request) |
| `access.sftp` | SFTP by password or key; registering SSH keys |
| `access.ftp` | FTPS |
| `access.s3` | S3 — creating keys and signing with them |
| `access.nfs` | NFS — creating exports and mounting them |
| `access.api` | Creating API keys **and using any API key** |
| `access.desktop` | Desktop sign-in, and using the key a desktop pairing made |

### Own account

| Key | Allows |
|---|---|
| `account.edit` | Changing own name, email, picture and password. Off locks a shared or demo login. |

### Admin area — see [Delegated administration](#delegated-administration)

`admin.users`, `admin.grants`, `admin.shares`, `admin.audit`, `admin.monitor`,
and `admin.full` (the Administrator role).

## Built-in roles and presets

On Admin → Roles, **User** and **Viewer** are edited like any role (a grid of
permissions, with presets to start from); **Administrator** is locked. The
User role's permissions are the *defaults* every regular account starts from.
The Viewer role can never be given file changes or sharing — those stay off
however it is edited.

| Preset | What it is |
|---|---|
| Full admin | Everything |
| Standard user | Everything outside the admin area — what a `user` could do before permissions existed. **The User role until it is edited.** |
| Read-only | Standard minus file changes and sharing — what a `viewer` could do before. **The Viewer role until it is edited.** |
| Upload-only | `files.create` and `account.edit` — a drop-box account |
| Guest | `files.download` only |

A preset on a person's page pins every permission of that one account to the
preset (as exceptions); *Clear exceptions* goes back to inheriting.

## Custom roles

Admin → Roles → *New role*. Creating and editing roles is for administrators —
a role reaches everybody who holds it, so a delegated administrator could
otherwise write one that grants themselves anything. **Giving** a role is done
in the Role field on the person's page (Users → a person), by anyone with
`admin.users`, who can give only roles whose every *allow* they hold
themselves.

**One role per person.** Picking a custom role replaces the person's previous
one; picking a built-in role ends the custom one. An administrator is bound
by no role, so picking a custom role for one makes them stop being an
administrator (never the last one) — one save, one server call.

A role has:

- **Permissions** — its own list, ticked like the built-in roles' (a preset
  fills it in one click). A new role starts with nothing ticked.
- **Limits**:

  | Limit | Enforced |
  |---|---|
  | Maximum share-link lifetime (days) | A link asked for with no expiry, or a later one, is **capped** (reported as `expiry_clamped`, like the install-wide cap) |
  | Share links need a password | A link asked for without one gets a **generated PIN**, returned to the creator; an emailed invite link carries it |
  | Blocked file types | Upload, create, save or rename **to** a blocked extension is refused on every door (`exe`, `tar.gz`; case-insensitive). Existing files of that type can still be read and deleted |
  | Largest file | Every write door, including the agent API, the text editor and chunked uploads |
  | Require 2FA | See below |

- **Different in some folders** (optional) — pick **storages** and/or
  **folders**, then Allow or Deny file actions and links there (`files.*`
  except tag, `share.links`, `share.upload_links`); everywhere else the list
  applies. So "no delete, except in Scratch" is one role. A pattern names a
  folder and everything in it (`Archive` = `Archive/**`); `*` matches within
  one name, `**` across folders (`**/*.psd` anywhere, `Clients/*/Contracts`).
  The server applies it per path, and a refusal names the role.
- **Enabled** — a role can be switched off without deleting it. A
  switched-off role gives its people **nothing** (not even sign-in to the
  API) until it is on again — never the built-in User role instead, which
  for a role that takes things away would be *more* access. Exceptions still
  apply. Refusals say the role is switched off.
- **Deleting** a role nobody holds just deletes it. If people hold it, you
  choose what they get instead — User, Viewer or another role
  (`DELETE /api/admin/roles/{id}?to=user|viewer|<id>`; without `to` the server
  answers `409` with the count).

**The level underneath.** filex keeps a folder-access level for everyone —
Viewer can only look, User can also change. A custom role's people get it
automatically: **User** if the role can add, change, delete or share
anything (anywhere, or in some folders), **Viewer** otherwise — so a
read-only role stays read-only on every door, even against a mistaken
exception. Editing the role moves its people with it.

- **Starting role for SSO groups** (optional) — see below.

### Starting role for SSO groups

A role can name SSO groups. When a **new** account is created at its first SSO
sign-in and the provider's `role_claim` (ID token or access token; dotted
paths like `realm_access.roles` work) carries one of them, the account starts
with that role (the lowest-numbered enabled one, if several match) and its
level. **Only at creation**: after that the role is the person's, changed on
their page like anyone else's, and a later sign-in never hands it back or
takes it away. An account the admin mapping makes an administrator gets no
role. filex still stores each sign-in's groups (replaced every time), but
they no longer change what an existing account may do. LDAP sign-ins carry no
groups.

### Require 2FA

An account bound by it that has not enrolled TOTP can still sign in, but its
session reaches only who-am-I, its permissions, enrolment and sign-out — every
other call answers `403 "2fa_required"`, and the web app holds it at a screen
that opens the Security settings. Its **password** logins over WebDAV / SFTP /
FTPS are refused until it enrols. Exempt: administrators, SSO accounts (their
second factor is the identity provider's), API keys.

## Delegated administration

An account without the Administrator role but with an `admin.*` permission
gets an admin panel showing only its pages:

| Permission | Pages / routes |
|---|---|
| `admin.users` | Users (list, create, edit, delete, reset password, quotas), each person's exceptions, giving and taking custom roles |
| `admin.grants` | The folder-grants overview; removing grants |
| `admin.shares` | Everyone's shares; revoke, delete |
| `admin.audit` | The audit log |
| `admin.monitor` | Dashboard, usage, sync history, the job queue — read-only |

Everything else — storages, settings, SSO, plugins, updates, branding,
webhooks, and creating or editing roles — stays the Administrator's. New admin
routes are admin-only unless they name a permission.

A delegated administrator **never** edits, deletes, sets the quota of or
resets the password of an administrator, never gives a built-in role above
their own, and can **allow only permissions they hold** — as an exception or
through a custom role (they can take any away). Delegation works from a
signed-in session only, never an API key.

## Where each permission is enforced

The server enforces everything; the web app only hides what would be refused.

| Door | Login | File actions |
|---|---|---|
| Web app / REST | session or API key (`access.api` / `access.desktop` for keys) | per route and per file — `perm_route_table_test.go` fails the build on any state-changing route that is not classified |
| Agent API `/api/ai` | `ai.use` (+ the key's `access.api`) | the same file permissions |
| WebDAV | `access.webdav`, every request | GET = download; PUT = create/modify; MKCOL = create; DELETE = delete (+purge without trash); MOVE = rename and/or move; COPY = create at the destination. PROPFIND stays on the grant |
| SFTP | `access.sftp` | read = download; write = create/modify; mkdir = create; rename = rename and/or move (+modify when it replaces); remove = delete (+purge without trash) |
| FTPS | `access.ftp` | RETR = download; STOR = create/modify; MKD = create; RNFR/RNTO = rename and/or move; DELE = delete |
| S3 | `access.s3` | GET = download (HEAD stays open); PUT, multipart, copy destination = create/modify; DELETE = delete |
| NFS | `access.nfs` at mount | as SFTP |

Taking a protocol's permission away ends access at the next login. Inside an
open session, file permissions follow the same short reload the grants have.

## API

| Method & path | Who | |
|---|---|---|
| `GET /api/auth/me` | anyone signed in | now also `permissions` (account-wide), `permission_settings`, `two_factor_required` |
| `GET /api/auth/me/permissions` | anyone signed in | effective permissions with sources, limits, role ids, path-limited role ids |
| `GET /api/admin/roles/catalogue` | `admin.users` | the 28 permissions and the presets |
| `GET /api/admin/roles/exceptions` | `admin.users` | user id → exceptions |
| `GET` / `PUT /api/admin/users/{id}/exceptions` | `admin.users` | exceptions + effective / `{"overrides":{"files.delete":"deny"}}` — `{}` clears |
| `GET` / `PUT /api/admin/users/{id}/roles` | `admin.users` | a person's one role, set in one call: `{"role_id":3}` (a custom role — also sets the level underneath), `{"role_id":null}`, or `{"role":"viewer"}` (a built-in role; ends the custom one). An administrator given a custom role stops being one — never the last administrator (`409`) |
| `GET /api/admin/roles/builtin[?role=viewer]` | `admin.users` | a built-in role's permissions |
| `PUT /api/admin/roles/builtin[?role=viewer]` | admin | `{"permissions":[…]}` |
| `GET /api/admin/roles` | `admin.users` | the custom roles, and `assignments`: user id → the role they hold |
| `POST /api/admin/roles` | admin | create a custom role |
| `PUT` / `DELETE /api/admin/roles/{id}` | admin | replace / delete a custom role |

A custom role body:

```json
{
  "name": "Contractor",
  "enabled": true,
  "permissions": ["files.download", "files.create", "files.modify", "files.rename", "files.move", "account.edit"],
  "targets": [{ "kind": "sso_group", "value": "contractors" }],
  "effects": { "files.delete": "allow" },
  "conditions": { "paths": ["Scratch"] },
  "settings": { "share_link_max_days": 7, "blocked_extensions": ["exe"], "max_upload_bytes": 104857600, "require_2fa": true }
}
```

`permissions` is the role's list. `effects` (Allow/Deny) is only for the
folders in `conditions` and is refused without them. `targets` only name SSO
groups (the starting role); any other target is refused — a person is given
a role on their own page.

Every custom role is its own list (migration 00064). Migration 00065 drops the
old `base_role` column; a role that still had no list of its own is switched
off with an empty one, so its people get nothing until an administrator fills
it in — never more than they had.

## Refusals

A permission refusal is `403`:

```json
{
  "error": "permission_denied",
  "permission": "files.delete",
  "source": { "kind": "rule", "rule_id": 3, "rule_name": "Contractor" },
  "message": "The role “Contractor” does not allow you to delete files."
}
```

`message` is in the reader's language (`source.kind` keeps the API's name,
`rule`, for a custom role). A refusal caused by the **path** (no grant, a
read-only account, a lock) keeps its historical body. A blocked file type
answers `{"error":"blocked_file_type","extension":"exe"}`; a file over the
limit `413 {"code":"FILE_TOO_LARGE"}` in the web app and each protocol's own
"over the limit" answer elsewhere. Protocol logins that are refused look
exactly like a wrong password.

## Audit

| Action | Details |
|---|---|
| `user.permissions_set` | `before` / `after` exceptions |
| `user.roles_set` | `before` / `after` custom role id (or null) |
| `permissions.defaults_set` | the built-in `role`, `before` / `after` |
| `permission_rule.create` / `.delete` | the custom role |
| `permission_rule.update` | `before` / `after` |

## Things to know

- **Download is a deterrent, not DRM.** Previews still send a file's bytes to
  the browser.
- **The web app hides account-wide.** A role limited to some folders is decided
  by the server per path; the menu does not know it. The desktop app and
  embeds show every action and rely on the server's answer.
- **Desktop keys paired before this feature** read as API keys: removing
  `access.api` stops them until the desktop is paired again.
- **Existing sessions:** changes apply at once to the web app (checked per
  request); protocol sessions keep going until they reconnect.
- **Multiple processes** on one database see a role change within 3 seconds.
