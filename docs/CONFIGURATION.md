# Configuration

filex reads configuration in this order (highest precedence first):

1. **Environment variables** (`FILEX_*`)
2. **`config.yaml`** (path via `--config`, `FILEX_CONFIG`, or `~/.filex/config.yaml` if present)
3. **Built-in defaults**

For containers, environment variables are easiest. For rich setups (LDAP,
proxy‑header auth, custom CORS) a `config.yaml` is handier because a few settings
are **file‑only** (noted below). Individual storages are **not** configured here
— they're database records; see [STORAGE.md](STORAGE.md).

- [Server & networking](#server--networking)
- [Logging](#logging)
- [Database](#database)
- [Authentication](#authentication)
- [External services](#external-services)
- [Storage sync](#storage-sync)
- [Thumbnails](#thumbnails)
- [Search](#search)
- [Queue](#queue)
- [Notifications](#notifications)
- [CORS](#cors)
- [Error reporting (Sentry/GlitchTip)](#error-reporting)
- [Demo mode](#demo-mode)
- [config.yaml](#configyaml)
- [Gotchas](#gotchas)

> **Booleans** are true only for `"1"` or (case‑insensitive) `"true"`. Any other
> non‑empty value is treated as false.

---

## Server & networking

| Env var | Default | Description |
|---|---|---|
| `FILEX_LISTEN` | `0.0.0.0:5212` | Bind address. |
| `FILEX_PUBLIC_URL` | `http://localhost:5212` | **The external URL users open.** Baked into share links, the OIDC redirect and OnlyOffice fetch/callback — set it to your real `https://…` domain behind a proxy. |
| `FILEX_DATA_DIR` | `~/.filex` (`/data` in Docker) | Holds the SQLite DB, search index, thumbnail cache, first‑run secret. |
| `FILEX_CONFIG` | — | Path to `config.yaml` (same as `--config`). |

---

## Logging

| Env var | Default | Description |
|---|---|---|
| `FILEX_LOG_LEVEL` | `info` | `debug` · `info` · `warn` · `error` |
| `FILEX_LOG_FORMAT` | `text` | `text` · `json` |

---

## Database

| Env var | Default | Description |
|---|---|---|
| `FILEX_DB_DRIVER` | `sqlite` | `sqlite` · `postgres` · `mysql` |
| `FILEX_DB_DSN` | — | Connection string. Empty + sqlite → `<data_dir>/instance.sqlite`. |

DSN examples:
- postgres: `postgres://user:pass@host:5432/dbname?sslmode=require`
- mysql: `user:pass@tcp(host:3306)/dbname?parseTime=true&loc=UTC&charset=utf8mb4`

Migrations **run automatically on startup**; also `filex migrate up|down|status`.
SQLite (pure Go, CGO‑free) is a fine default; **PostgreSQL is recommended for
teams/HA**. MySQL is supported for read‑mostly use (a few upsert paths are
SQLite/Postgres‑only). See [database drivers](#database) note above.

---

## Authentication

Pick drivers with `FILEX_AUTH_DRIVERS` (comma list, tried in order, first match
wins). The **API‑token driver is always on** regardless.

| Env var | Default | Description |
|---|---|---|
| `FILEX_AUTH_DRIVERS` | `local` | e.g. `local,oidc`, `local,ldap`, `proxy_header` |

**OIDC / SSO** (see [SSO.md](SSO.md)):

| Env var (legacy `FILEX_AUTH_OIDC_*` also accepted) | Description |
|---|---|
| `FILEX_OIDC_ISSUER` | IdP issuer URL |
| `FILEX_OIDC_CLIENT_ID` | Client ID |
| `FILEX_OIDC_CLIENT_SECRET` | Client secret |
| `FILEX_OIDC_REDIRECT_URL` | `<public>/api/auth/oidc/callback` |
| `FILEX_OIDC_ROLE_CLAIM` | Claim carrying roles/groups |
| `FILEX_OIDC_ADMIN_GROUP` | Value that elevates to admin |

**LDAP** and **proxy‑header** have **no env vars** — configure them under
`auth.ldap.*` / `auth.header_proxy.*` in [config.yaml](#configyaml). See
[SSO.md → other auth drivers](SSO.md#other-auth-drivers).

Local auth uses the `filex_session` cookie (12 h), bcrypt passwords, optional
TOTP 2FA. First boot creates `admin@local` (see [INSTALLATION.md](INSTALLATION.md#first-run)).

---

## External services

Each is optional — an empty URL disables it. Set via env or
`external_services.*`.

| Env var | Description |
|---|---|
| `FILEX_ONLYOFFICE_URL` | OnlyOffice Document Server URL (see [ONLYOFFICE.md](ONLYOFFICE.md)) |
| `FILEX_ONLYOFFICE_JWT` | Shared JWT secret — must match the Document Server |
| `FILEX_DRAWIO_URL` | Drawio embed URL (diagram editing) |
| `FILEX_MERMAID_URL` | Mermaid render URL |
| `FILEX_CONVERT_URL` | External universal converter URL |

---

## Storage sync

Global fallback cadence for the [sync worker](STORAGE.md#sync). Per‑storage
`sync_interval_s` overrides this.

| Env var | Default | Description |
|---|---|---|
| `FILEX_SYNC_INTERVAL` | `15m` | Go duration (`30s`, `15m`, `1h`). |
| `FILEX_SYNC_WORKERS` | `4` | Concurrent storage sync workers. |

> ⚠ The variable is `FILEX_SYNC_INTERVAL`, **not** `FILEX_SYNC_DEFAULT_INTERVAL`.

---

## Thumbnails

| Env var | Default | Description |
|---|---|---|
| `FILEX_THUMBS_ENABLED` | `true` | Master switch. |
| `FILEX_THUMB_BACKFILL_ON_BOOT` | — | Set `once` to backfill missing thumbnails on startup. |

Kinds and their tool requirements (auto‑detected on `PATH`; the full Docker
image bundles them): images = built‑in; video/audio = `ffmpeg`; PDF = `gs` or
`pdftoppm`; office = `libreoffice`; SVG = `rsvg-convert`. Missing tool → that
kind gets a generic placeholder card. Cache dir + formats are `config.yaml`
only (`thumbs.cache_dir`, `thumbs.formats`). See [thumbnails.md](thumbnails.md).

---

## Search

| Env var | Default | Description |
|---|---|---|
| `FILEX_SEARCH_ENABLED` | `true` | Embedded Bleve full‑text index. |

Index path is `config.yaml` only (`search.index_path`, default
`<data_dir>/search.bleve`). See [SEARCH.md](SEARCH.md).

---

## Queue

| Env var | Default | Description |
|---|---|---|
| `FILEX_QUEUE_DRIVER` | `sqlite` | `sqlite` · `postgres` · `redis` |
| `FILEX_QUEUE_DSN` | — | `postgres://…` or `redis://…` (ignored for sqlite — shares the app DB) |
| `FILEX_QUEUE_WORKERS` | `4` | Worker pool size. |
| `FILEX_QUEUE_ENABLED` | `true` | Disable to run without the persistent queue. |

Use **redis** or **postgres** for multi‑node deployments (postgres uses
`SELECT … FOR UPDATE SKIP LOCKED`). sqlite is fine single‑node.

---

## Notifications

| Env var | Default | Description |
|---|---|---|
| `FILEX_NOTIFY_ENABLED` | `true` | In‑app bell + webhook. |
| `FILEX_WEBHOOK_URL` | — | Generic JSON POST per event (empty = in‑app only). |
| `FILEX_WEBHOOK_TOKEN` | — | Sent as `Authorization: Bearer` to the webhook. |

See [NOTIFICATIONS.md](NOTIFICATIONS.md).

---

## CORS

| Env var | Default | Description |
|---|---|---|
| `FILEX_CORS_ALLOWED_ORIGINS` | `*` | Comma list. Restrict when embedding the component from specific origins. |

`allowed_methods` / `allowed_headers` are `config.yaml` only. Default allowed
headers: `Authorization, Content-Type, X-Filex-Pin`. If you use API‑token root
confinement from a browser, add `X-Filex-Token` / `X-Filex-Root`.

---

## Error reporting

Optional Sentry‑wire reporting (works with self‑hosted GlitchTip). Empty DSN =
off.

| Env var | Default | Description |
|---|---|---|
| `FILEX_SENTRY_DSN` | — | Sentry/GlitchTip DSN. |
| `FILEX_SENTRY_ENVIRONMENT` | — | Tag events (e.g. `production`). |

---

## Demo mode

| Env var | Default | Description |
|---|---|---|
| `FILEX_DEMO_MODE` | `false` | Renders an "Open the demo" CTA on the login page. |
| `FILEX_DEMO_USER` | `demo@demo.com` | Demo credentials the CTA submits. |
| `FILEX_DEMO_PASS` | `demo` | (Keep the DB user in sync.) |

---

## config.yaml

Every field is optional; pass with `--config /path/to/config.yaml`.

```yaml
listen: "0.0.0.0:5212"
public_url: "https://files.example.com"
data_dir: "/data"

log:   { level: info, format: text }
db:    { driver: sqlite, dsn: "" }

auth:
  drivers: [local, oidc]           # local | oidc | ldap | proxy_header
  oidc:
    issuer: https://id.example.com/realms/main
    client_id: filex
    client_secret: "…"
    redirect_url: https://files.example.com/api/auth/oidc/callback
    role_claim: realm_access.roles
    admin_group: filex-admin
  ldap:                            # file-only
    url: ldaps://ldap.example.com
    bind_dn: "cn=svc,dc=example,dc=com"
    bind_password: "…"
    base_dn: "ou=people,dc=example,dc=com"
    user_filter: "(mail=%s)"
    email_attr: mail
    start_tls: false
  header_proxy:                    # file-only — trust an auth proxy
    email_header: X-Auth-Email
    group_header: X-Auth-Roles
    trusted_ips: ["10.0.0.0/8"]
    admin_group: admin

external_services:
  onlyoffice: { url: https://office.example.com, jwt_secret: "…" }
  drawio:     { url: "" }
  mermaid:    { url: "" }
  convert:    { url: "" }

sync:   { default_interval: 15m, workers: 4 }
thumbs: { enabled: true, formats: [image, video, pdf, office], cache_dir: "" }
search: { enabled: true, index_path: "" }
cors:
  allowed_origins: ["*"]
  allowed_methods: [GET, POST, PUT, DELETE, PATCH, OPTIONS]
  allowed_headers: [Authorization, Content-Type, X-Filex-Pin]
queue:  { driver: sqlite, dsn: "", workers: 4, enabled: true }
notify: { enabled: true, webhook_url: "", webhook_token: "" }
demo:   { mode: false, user: demo@demo.com, pass: demo }
sentry: { dsn: "", environment: "" }
```

Some settings (branding, default thumbnail policy) live in the database
`settings` table and are managed from the admin UI, not here.

---

## Gotchas

- `FILEX_SYNC_DEFAULT_INTERVAL` is **not** read — the correct var is
  `FILEX_SYNC_INTERVAL`.
- `FILEX_DEFAULT_STORAGE_DRIVER` is not read by anything (storages are DB rows).
- Booleans accept only `"1"` / `"true"`; anything else is false.
- LDAP and proxy‑header are **config.yaml only** (no env overrides).
