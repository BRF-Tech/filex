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
- [Zero-touch seeding](#zero-touch-seeding)
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
| `FILEX_DEFAULT_LOCALE` | — | Pin the initial UI language (`en` / `tr`) for users who haven't chosen one, overriding browser detection. A user's explicit language switch still wins. |
| `FILEX_MULTI_TENANT` | `false` | Turn on native multi-tenancy — one install serves N tenants, each a host-bound auth realm (provider) confined to its own storage(s). **Off = a normal single-tenant install, behaviour unchanged.** See [MULTI-TENANCY.md](./MULTI-TENANCY.md). |
| `FILEX_COOKIE_DOMAIN` | — (host-only) | `Domain` attribute for the `filex_session` cookie, e.g. `.example.com` — subdomains of that domain then share the session. Applied on **both** set and clear, so logout removes the same cookie it created. Empty = host-only cookie (unchanged behaviour). `Secure`/`SameSite`/`HttpOnly` are unaffected. On a multi-tenant instance this is a single global value — when every tenant has its own apex domain, run one instance per tenant (or leave it empty); deriving it per provider host is not implemented. |
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
| `FILEX_OIDC_AUTO_REDIRECT` | **SSO-first login** (default `false`): the login page starts the OIDC flow immediately instead of showing the password form. Local login stays available behind a "Sign in with password" link (`/admin/login?local=1`) for break-glass/`admin@local`. The redirect is skipped on `?local=1`, after a failed IdP round-trip (`?error=oidc`) and on `?maintenance=1`, so a broken IdP can never cause a redirect loop. Requires `oidc` in `FILEX_AUTH_DRIVERS`. Multi-tenant: the flag is instance-global; the flow itself already dispatches per request host to the right tenant realm. |

**LDAP** (enable with `FILEX_AUTH_DRIVERS=local,ldap`):

| Env var | Description |
|---|---|
| `FILEX_LDAP_URL` | Directory URL, e.g. `ldaps://ldap.example.com` |
| `FILEX_LDAP_BIND_DN` | Service bind DN |
| `FILEX_LDAP_BIND_PASSWORD` | Service bind password |
| `FILEX_LDAP_BASE_DN` | Search base for users |
| `FILEX_LDAP_USER_FILTER` | User filter, e.g. `(mail=%s)` |
| `FILEX_LDAP_EMAIL_ATTR` | Attribute holding the email (e.g. `mail`) |
| `FILEX_LDAP_START_TLS` | `true` to upgrade a plain connection with StartTLS |

**Proxy‑header** — trust an authenticating reverse proxy (enable with
`FILEX_AUTH_DRIVERS=proxy_header`):

| Env var | Description |
|---|---|
| `FILEX_HEADER_EMAIL` | Header carrying the authenticated email (e.g. `X-Auth-Email`) |
| `FILEX_HEADER_GROUP` | Header carrying roles/groups (e.g. `X-Auth-Roles`) |
| `FILEX_HEADER_TRUSTED_IPS` | Comma list of proxy CIDRs allowed to set the headers |
| `FILEX_HEADER_ADMIN_GROUP` | Group value that elevates a user to admin |

> LDAP and proxy‑header can still be set under `auth.ldap.*` /
> `auth.header_proxy.*` in [config.yaml](#configyaml); the env vars above override
> those. See [SSO.md → other auth drivers](SSO.md#other-auth-drivers).

Local auth uses the `filex_session` cookie (12 h), bcrypt passwords, optional
TOTP 2FA. First boot creates `admin@local` (or seed a known admin — see
[Zero‑touch seeding](#zero-touch-seeding) and [INSTALLATION.md](INSTALLATION.md#first-run)).

---

## Zero-touch seeding

These variables **seed the database once, on first boot, only when the target
record is absent.** They let a fresh `docker compose up` / `helm install` come up
fully configured from env alone — no admin‑UI clicks. Once a record exists, later
operator edits in the UI **always win**; changing the env afterwards does **not**
re‑seed or overwrite. (OIDC/LDAP/header auth are read live from env every boot and
so are configured in [Authentication](#authentication), not here.)

**First admin** — created if the user table is empty:

| Env var | Default | Description |
|---|---|---|
| `FILEX_ADMIN_EMAIL` | `admin@local` | Email of the seeded admin account. |
| `FILEX_ADMIN_PASSWORD` | *(random, printed once)* | Password for that admin. Omit both to get a random `admin@local` (see [INSTALLATION.md → first run](INSTALLATION.md#first-run)). |

**SMTP** (mailer) — seeded when host, port and from are all set:

| Env var | Description |
|---|---|
| `FILEX_SMTP_HOST` | SMTP server host. |
| `FILEX_SMTP_PORT` | SMTP server port. |
| `FILEX_SMTP_USERNAME` | Auth username (optional). |
| `FILEX_SMTP_PASSWORD` | Auth password (optional). |
| `FILEX_SMTP_FROM` | From address on outbound mail. |
| `FILEX_SMTP_TLS` | `starttls` · `tls` · `none`. |

**Branding & trash:**

| Env var | Description |
|---|---|
| `FILEX_SITE_NAME` | Instance display name shown in the UI. |
| `FILEX_TRASH_RETENTION_DAYS` | Days to keep trashed items before purge (see [TRASH-VERSIONING.md](TRASH-VERSIONING.md)). |

**Default storage** — seeds one initial storage when **no storage exists yet**, so
a fresh install already has a working place for files. Leave
`FILEX_DEFAULT_STORAGE_DRIVER` empty to seed nothing. (See [STORAGE.md](STORAGE.md)
for the storage model.)

| Env var | Applies to | Description |
|---|---|---|
| `FILEX_DEFAULT_STORAGE_DRIVER` | both | `local` · `s3` (empty = seed no storage). |
| `FILEX_DEFAULT_STORAGE_NAME` | both | Display name / top‑level folder label. |
| `FILEX_DEFAULT_STORAGE_MOUNT` | both | Logical mount point (default `/`). |
| `FILEX_DEFAULT_STORAGE_PATH` | local | On‑disk directory to serve. |
| `FILEX_DEFAULT_STORAGE_S3_BUCKET` | s3 | Bucket name. |
| `FILEX_DEFAULT_STORAGE_S3_PREFIX` | s3 | Key prefix = storage root (keep non‑empty — root guard). |
| `FILEX_DEFAULT_STORAGE_S3_ENDPOINT` | s3 | Custom endpoint (MinIO/R2/Hetzner …); omit for AWS. |
| `FILEX_DEFAULT_STORAGE_S3_REGION` | s3 | e.g. `us-east-1`; `auto` for R2/MinIO. |
| `FILEX_DEFAULT_STORAGE_S3_ACCESS_KEY` | s3 | Access key. |
| `FILEX_DEFAULT_STORAGE_S3_SECRET_KEY` | s3 | Secret key. |
| `FILEX_DEFAULT_STORAGE_S3_PATH_STYLE` | s3 | `true` for path‑style addressing (MinIO/Hetzner/B2/R2). |

---

## External services

Each is optional — an empty URL disables it. Set via env or
`external_services.*`.

| Env var | Description |
|---|---|
| `FILEX_ONLYOFFICE_URL` | OnlyOffice Document Server URL (see [ONLYOFFICE.md](ONLYOFFICE.md)) |
| `FILEX_ONLYOFFICE_JWT` | Shared JWT secret — must match the Document Server |
| `FILEX_DRAWIO_URL` | Drawio embed URL (diagram editing) |
| `FILEX_CONVERT_URL` | External universal converter URL |

> **Mermaid needs no service.** Mermaid diagrams render entirely client‑side in
> the browser via a bundled `mermaid` library — there is nothing to deploy and no
> URL to set (the former `FILEX_MERMAID_URL` was removed).

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
  ldap:                            # also overridable via FILEX_LDAP_*
    url: ldaps://ldap.example.com
    bind_dn: "cn=svc,dc=example,dc=com"
    bind_password: "…"
    base_dn: "ou=people,dc=example,dc=com"
    user_filter: "(mail=%s)"
    email_attr: mail
    start_tls: false
  header_proxy:                    # trust an auth proxy — also FILEX_HEADER_*
    email_header: X-Auth-Email
    group_header: X-Auth-Roles
    trusted_ips: ["10.0.0.0/8"]
    admin_group: admin

external_services:
  onlyoffice: { url: https://office.example.com, jwt_secret: "…" }
  drawio:     { url: "" }        # mermaid renders client-side — no service
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

seed:                              # first-boot only-if-absent (see Zero-touch seeding)
  admin_email: ""
  admin_password: ""
  site_name: ""
  trash_retention_days: ""
  smtp:    { host: "", port: "", username: "", password: "", from: "", tls: starttls }
  storage: { driver: "", name: "", mount_path: "/", path: "",
             bucket: "", prefix: "", endpoint: "", region: "",
             access_key: "", secret_key: "", path_style: false }
```

Some settings (branding, default thumbnail policy) live in the database
`settings` table and are managed from the admin UI, not here.

---

## Gotchas

- `FILEX_SYNC_DEFAULT_INTERVAL` is **not** read — the correct var is
  `FILEX_SYNC_INTERVAL`.
- `FILEX_DEFAULT_STORAGE_*` only takes effect on a **fresh** install (it seeds a
  default storage when none exists yet); it never edits or replaces an existing
  storage. See [Zero‑touch seeding](#zero-touch-seeding).
- Booleans accept only `"1"` / `"true"`; anything else is false.
- LDAP and proxy‑header now have env vars (`FILEX_LDAP_*` / `FILEX_HEADER_*`); the
  env value overrides the matching `config.yaml` field.
