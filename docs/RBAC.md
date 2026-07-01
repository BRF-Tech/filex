# filex — RBAC & per-file/folder permissions (API + MCP reference)

Added in v0.1.41+ (backend `internal/acl`). This documents the access model and
every endpoint / MCP tool the feature exposes. Backwards compatible: RBAC is
**off per storage by default**, so an untouched deployment behaves exactly as
before.

## Model

Two layers combine, then a ceiling is applied:

1. **Account role** (`users.role`): `admin` (full panel, exempt from all ACL),
   `user` (explorer only; read+write; can hold owner grants), `viewer`
   (explorer only; read-only — view/download, no edit/convert/mutate).
2. **Per-storage RBAC toggle** (`storages.rbac_enabled`, default `false`):
   - OFF → storage visible to every authenticated user; capability = account
     role (user→editor, viewer→viewer, admin→owner). No grants needed.
   - ON → storage hidden; a non-admin sees only paths granted to them (directly
     or inherited from a parent folder).
3. **Item grant level** (`file_grants`): `viewer` < `editor` < `owner`.
   - Inheritance: a folder grant cascades to descendants. Effective level =
     highest covering grant (direct or inherited), then **capped by the account
     role** (a viewer account stays viewer even if granted higher).
   - Only an `owner` of an item (or an admin) may see/manage its permissions.

Enforcement is server-side at every `/api/files/*` chokepoint AND the `/api/ai`
(REST + MCP) surface, keyed off the authenticated user — so cookie sessions are
filtered too, not just tokens. `internal/confine` (the token `root:` scope hard
ceiling) still composes on top.

## Endpoints — permissions panel (`/api/files/permissions`)

Mounted in the authenticated group. Every write requires the caller to be admin
**or** hold `owner` on the target path.

| Method | Path | Body / query | Notes |
|--------|------|--------------|-------|
| GET | `/api/files/permissions?path=<adapter>://<rel>` | — | `{direct[], inherited[], storage_rbac, effective}`. Owner/admin only. |
| POST | `/api/files/permissions` | `{path, user_id, level, is_dir?}` | Upsert a grant. 409 if storage RBAC off; 400 if granting a viewer account >viewer. |
| PATCH | `/api/files/permissions/{id}` | `{level}` | Change a grant's level. |
| DELETE | `/api/files/permissions/{id}` | — | Revoke. |
| GET | `/api/files/permissions/resolve?email=` | — | `{found, user?}` — existing account or not. |
| GET | `/api/files/permissions/users?q=` | — | `{users[]}` autocomplete of existing accounts. |
| POST | `/api/files/permissions/invite` | `{path, email, level, create_user?, role?}` | Existing user → grant; admin+`create_user` → new account+grant (temp password); else public share link. `{mode, url?, temp_password?, emailed}`. Mail sent only when SMTP is verified, else the link/password is returned for on-screen display. |

## Endpoints — self-service tokens (`/api/tokens`)

Any authenticated user (incl. non-admin) mints tokens **bound to themselves**,
capped server-side:

| Method | Path | Notes |
|--------|------|-------|
| GET | `/api/tokens` | The caller's own tokens (no secrets). |
| POST | `/api/tokens` | `{label, scopes, expires_in_days?}`. Verb-scope ceiling: viewer→`read`/`mcp` only; user→`read,write,delete,mcp`; **never `admin`**. Empty scopes are never stored (would be "all"→escalation). A `root:<adapter>://<rel>` scope must be ⊆ the caller's own grants. Plaintext returned once. |
| DELETE | `/api/tokens/{id}` | Ownership-checked. |

## Endpoints — admin (`/api/admin`, admin-only)

| Method | Path | Notes |
|--------|------|-------|
| GET | `/api/admin/grants` | Global overview: every grant enriched with `storage_name` + `user_email`. |
| DELETE | `/api/admin/grants/{id}` | Admin override revoke. |
| POST | `/api/admin/settings/smtp-test` | `{to?}` → `{ok, error?, sent?}`. Verifies the SMTP config (auth handshake) and, with `to`, sends a real test mail. SMTP config lives in the `smtp.*` settings keys (`host/port/tls/from/username/password`). |

`storages.rbac_enabled` is set via the normal storage create/update payloads
(`POST/PATCH /api/admin/storages`, field `rbac_enabled`).

## MCP admin tools

Exposed on `/api/ai/mcp` for an API token carrying the `admin` scope (alongside
the existing 59 `admin_*` tools):

| Tool | Input | Effect |
|------|-------|--------|
| `admin_grants_list` | — | List every grant (who/where/level). |
| `admin_grant_set` | `{body:{path, user_id, level}}` | Grant/upsert. Storage must have RBAC on; viewer accounts capped to viewer. |
| `admin_grant_revoke` | `{id}` | Revoke a grant by id. |

The AI file surface (`file_*` tools + `/api/ai/files|read|upload|...`) is already
gated by the bound user's grants + role ceiling via `aiOps` — a confined,
non-admin token only sees/mutates what its user was granted.

## Tests

`backend/internal/acl/acl_test.go` (resolution: Effective/CanSee/ceiling/prefix),
`backend/internal/api/handlers/tokens_self_test.go` (scope-ceiling / escalation),
`backend/internal/api/handlers/grants_test.go` (end-to-end: owner grant, viewer
ceiling, owner-only panel, self-token limits, admin overview, non-admin 403).
