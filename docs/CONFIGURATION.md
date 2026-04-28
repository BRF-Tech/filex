# Configuration

`filex` reads configuration from, in order of precedence:

1. **CLI flags** (`filex serve --listen 0.0.0.0:5212`)
2. **Environment variables** (`FILEX_LISTEN=0.0.0.0:5212`)
3. **`config.yaml`** (path via `--config` or `${FILEX_DATA_DIR}/config.yaml`)
4. **Built-in defaults**

For a containerised install, env vars are the easiest. For complex setups
(multiple storages, OIDC), prefer a `config.yaml`.

- [Full `config.yaml` reference](#full-configyaml-reference)
- [Environment variables](#environment-variables)
- [Storage drivers](#storage-drivers)
- [Auth drivers](#auth-drivers)
- [DB drivers](#db-drivers)
- [External services](#external-services)
- [Examples](#examples)

---

## Full `config.yaml` reference

```yaml
# config.yaml — every field is optional. Defaults shown.

# --- Server ---
listen: "0.0.0.0:5212"          # bind address
public_url: "http://localhost:5212"  # full URL the user types in browser; used for OIDC redirect, share links, OnlyOffice callback
data_dir: "/data"               # SQLite, search index, thumbs, tmp; can be ${FILEX_DATA_DIR}
log_level: "info"               # debug | info | warn | error
log_format: "text"              # text | json
trust_proxy_headers: false      # honour X-Forwarded-For/Proto/Host (set true behind nginx/traefik/caddy)

# --- Database ---
db:
  driver: "sqlite"              # sqlite | mysql | postgres
  dsn: "/data/filex.db"         # sqlite path | DSN string for mysql/postgres
  max_open_conns: 10
  max_idle_conns: 5
  conn_max_lifetime: "1h"

# --- Auth ---
auth:
  drivers: ["local"]            # any combination of: local, oidc, ldap, proxy_header
  session_ttl: "168h"           # 7 days
  cookie_name: "filex_session"
  cookie_secure: true           # set false only for plain HTTP dev
  cookie_samesite: "lax"        # strict | lax | none

  local:
    allow_signup: false
    bcrypt_cost: 12

  oidc:
    issuer: "https://auth.example.com/realms/myrealm"
    client_id: "filex"
    client_secret: "xxxxx"
    redirect_url: ""            # auto = ${public_url}/api/auth/oidc/callback
    scopes: ["openid", "profile", "email"]
    username_claim: "preferred_username"
    email_claim: "email"
    groups_claim: "groups"
    admin_groups: ["filex-admin"]
    auto_create_users: true

  ldap:
    url: "ldap://ldap.example.com:389"
    bind_dn: "cn=filex,ou=svc,dc=example,dc=com"
    bind_password: "xxxxx"
    user_base_dn: "ou=people,dc=example,dc=com"
    user_filter: "(uid=%s)"
    starttls: true
    insecure_skip_verify: false

  proxy_header:                 # for behind oauth2-proxy / authelia / Traefik forward-auth
    user_header: "X-Auth-Username"
    email_header: "X-Auth-Email"
    groups_header: "X-Auth-Groups"

# --- Storage ---
storage:
  default_driver: "local"       # used when seeding the first storage; can be any of: local, s3, sftp, webdav

  # Per-storage definitions are stored in the DB (admin UI). YAML can pre-seed
  # them, e.g. for a stateless Docker deploy. Each storage shows up as a top-level
  # folder in the file manager.
  storages:
    - name: "Local files"
      driver: "local"
      path: "/var/lib/filex/local-storage"
      readonly: false

    - name: "S3 archive"
      driver: "s3"
      bucket: "my-archive"
      region: "eu-central-1"
      endpoint: ""              # leave empty for AWS, set for Hetzner/MinIO/etc.
      access_key: "AKIAxxx"
      secret_key: "secretxxx"
      prefix: ""
      sse: ""                   # AES256 | aws:kms | ""
      sync_interval: "5m"

# --- Sync worker ---
sync:
  enabled: true
  interval: "5m"                # default per-storage; overrideable per-storage
  batch_size: 500
  tombstone_grace: "10m"        # don't delete cache row until storage has reported absent for this long

# --- Search (Bleve, embedded) ---
search:
  enabled: true
  index_path: "/data/search"    # auto = ${data_dir}/search
  reindex_on_start: false

# --- Thumbnails ---
thumbs:
  enabled: false                # default off; full image flips to true
  cache_dir: "/data/thumbs"
  max_pixels: 12000000          # ~12 MP cap for input
  ttl: "30d"
  ffmpeg_path: "ffmpeg"
  ghostscript_path: "gs"
  libreoffice_path: "soffice"

# --- External services (URL set = enabled, capability is auto-discovered) ---
external:
  onlyoffice:
    url: ""                     # https://docs.example.com
    jwt_secret: ""              # match OnlyOffice JWT_SECRET
  drawio:
    url: ""                     # https://embed.diagrams.net  (or self-hosted)
  mermaid:
    enabled: true               # client-side, no URL needed

# --- Sharing ---
share:
  default_ttl: "7d"
  max_ttl: "30d"
  max_downloads_default: 0      # 0 = unlimited
  pin_min_length: 4

# --- Rate limits ---
ratelimit:
  api_per_min: 600
  upload_per_min: 60
  share_attempt_per_min: 20
```

---

## Environment variables

Every `config.yaml` key has a `FILEX_…` env equivalent (uppercase, dots become
underscores). The most common ones:

| Variable                          | Default                       | Description |
|-----------------------------------|-------------------------------|-------------|
| `FILEX_LISTEN`                    | `0.0.0.0:5212`                | bind address |
| `FILEX_PUBLIC_URL`                | `http://localhost:5212`       | full external URL |
| `FILEX_DATA_DIR`                  | `~/.filex` / `/data` (Docker) | sqlite, search, thumbs, tmp |
| `FILEX_LOG_LEVEL`                 | `info`                        | `debug \| info \| warn \| error` |
| `FILEX_LOG_FORMAT`                | `text`                        | `text \| json` |
| `FILEX_TRUST_PROXY_HEADERS`       | `false`                       | honour X-Forwarded-* |
| `FILEX_DB_DRIVER`                 | `sqlite`                      | `sqlite \| mysql \| postgres` |
| `FILEX_DB_DSN`                    | `/data/filex.db`              | per-driver DSN |
| `FILEX_AUTH_DRIVERS`              | `local`                       | comma list: `local,oidc` |
| `FILEX_OIDC_ISSUER`               | (empty)                       | OIDC issuer URL |
| `FILEX_OIDC_CLIENT_ID`            | (empty)                       | OIDC client id |
| `FILEX_OIDC_CLIENT_SECRET`        | (empty)                       | OIDC client secret |
| `FILEX_OIDC_REDIRECT_URL`         | auto                          | `${public_url}/api/auth/oidc/callback` |
| `FILEX_LDAP_URL`                  | (empty)                       | `ldap://host:389` |
| `FILEX_LDAP_BIND_DN`              | (empty)                       | service-account DN |
| `FILEX_LDAP_BIND_PASSWORD`        | (empty)                       | service-account pw |
| `FILEX_DEFAULT_STORAGE_DRIVER`    | `local`                       | seed for first storage |
| `FILEX_THUMBS_ENABLED`            | `false`                       | enable thumb pipeline |
| `FILEX_ONLYOFFICE_URL`            | (empty)                       | OnlyOffice Document Server URL |
| `FILEX_ONLYOFFICE_JWT`            | (empty)                       | shared JWT secret |
| `FILEX_DRAWIO_URL`                | (empty)                       | Drawio embed URL |
| `FILEX_SESSION_TTL`               | `168h`                        | session lifetime |
| `FILEX_COOKIE_SECURE`             | `true`                        | set `false` only for HTTP dev |

A complete generated table is dumped by `filex serve --print-env`.

---

## Storage drivers

Each storage is a top-level folder in the UI. Add via the admin UI
(`/admin/storages`) or pre-seed in `config.yaml`.

### `local`
Uses an absolute path on the host filesystem.

```yaml
- name: "Local files"
  driver: "local"
  path: "/var/lib/filex/local-storage"
  readonly: false
```

### `s3`
Works with AWS S3, Hetzner Object Storage, MinIO, Wasabi, Backblaze B2 (S3
compat), Cloudflare R2.

```yaml
- name: "S3"
  driver: "s3"
  bucket: "my-bucket"
  region: "eu-central-1"
  endpoint: ""                  # empty for AWS; required for Hetzner/MinIO/etc.
  access_key: "..."
  secret_key: "..."
  prefix: ""                    # optional path prefix inside the bucket
  use_path_style: false         # set true for MinIO
  sync_interval: "5m"
```

### `sftp`
SSH key or password.

```yaml
- name: "SFTP"
  driver: "sftp"
  host: "files.example.com"
  port: 22
  user: "filex"
  password: ""                  # OR private_key
  private_key: |
    -----BEGIN OPENSSH PRIVATE KEY-----
    ...
  remote_path: "/home/filex"
  known_hosts: "/data/known_hosts"
```

### `webdav`
Generic WebDAV; tested against Nextcloud and Apache mod_dav.

```yaml
- name: "WebDAV"
  driver: "webdav"
  url: "https://cloud.example.com/remote.php/dav/files/burak/"
  user: "burak"
  password: "..."
  insecure_skip_verify: false
```

---

## Auth drivers

Multiple drivers can be enabled simultaneously. The login screen shows a button
per enabled driver. `local` always works as the bootstrap admin path.

See [BACKEND.md](BACKEND.md#auth) for endpoint details.

---

## DB drivers

### `sqlite` (default)
Pure Go (`modernc.org/sqlite`). No CGO, no extra binary deps.

```yaml
db:
  driver: "sqlite"
  dsn: "/data/filex.db"   # plain path
```

### `mysql`
```yaml
db:
  driver: "mysql"
  dsn: "filex:secret@tcp(db:3306)/filex?parseTime=true&loc=UTC&multiStatements=true"
```

### `postgres`
```yaml
db:
  driver: "postgres"
  dsn: "postgres://filex:secret@db:5432/filex?sslmode=disable"
```

Migrations run on startup via goose.

---

## External services

OnlyOffice, Drawio, and Mermaid are mounted dynamically when their URL is
configured. The frontend learns of them via `GET /api/capabilities`.

```yaml
external:
  onlyoffice:
    url: "https://docs.example.com"
    jwt_secret: "shared-with-onlyoffice-JWT_SECRET"
  drawio:
    url: "https://embed.diagrams.net"
  mermaid:
    enabled: true        # purely client-side
```

When `onlyoffice.url` is set, `.docx`/`.xlsx`/`.pptx` files get a "Open in
OnlyOffice" action. Otherwise the action hides itself.

---

## Examples

### Minimal: SQLite + local storage

`docker-compose.yml`:
```yaml
services:
  filex:
    image: brftech/filex:latest
    ports: ["5212:5212"]
    volumes: ["./data:/data", "./files:/var/lib/filex/local-storage"]
```

### Production: Postgres + OIDC + S3 + OnlyOffice

`config.yaml`:
```yaml
listen: "0.0.0.0:5212"
public_url: "https://files.example.com"
trust_proxy_headers: true
log_format: "json"

db:
  driver: "postgres"
  dsn: "postgres://filex:${POSTGRES_PASSWORD}@db:5432/filex?sslmode=verify-full"

auth:
  drivers: ["oidc"]
  oidc:
    issuer: "https://auth.example.com/realms/main"
    client_id: "filex"
    client_secret: "${OIDC_SECRET}"
    admin_groups: ["filex-admin"]
    auto_create_users: true

storage:
  storages:
    - name: "Documents"
      driver: "s3"
      bucket: "company-docs"
      region: "eu-central-1"
      endpoint: "https://nbg1.your-objectstorage.com"
      access_key: "${S3_AK}"
      secret_key: "${S3_SK}"

thumbs:
  enabled: true

external:
  onlyoffice:
    url: "https://docs.example.com"
    jwt_secret: "${ONLYOFFICE_JWT}"
```

### Behind nginx / Traefik (forward-auth)

```yaml
auth:
  drivers: ["proxy_header"]
  proxy_header:
    user_header: "X-Auth-Username"
    email_header: "X-Auth-Email"
    groups_header: "X-Auth-Groups"

trust_proxy_headers: true
```

The reverse proxy must terminate auth (Authelia, oauth2-proxy, etc.) and set
the headers above before forwarding to filex.
