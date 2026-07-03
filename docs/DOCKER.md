# Docker

`filex` ships two pre-built images and a profile-driven `docker-compose.yml`
that lets you assemble the stack you actually need.

- [Images](#images)
- [Compose profiles](#compose-profiles)
- [Volume layout](#volume-layout)
- [Reverse proxies](#reverse-proxies)
- [TLS termination](#tls-termination)
- [Backups](#backups)
- [Upgrade](#upgrade)

---

## Images

| Tag                       | Size    | Includes                                                       |
|---------------------------|---------|----------------------------------------------------------------|
| `ghcr.io/brf-tech/filex:latest`    | ~40 MB  | Alias for `slim`. Pure Go binary. Image thumbs only.           |
| `ghcr.io/brf-tech/filex:slim`      | ~40 MB  | Same as `latest`.                                              |
| `ghcr.io/brf-tech/filex:full`      | ~250 MB | + ffmpeg, vips-tools, ghostscript, poppler-utils, libreoffice. |
| `ghcr.io/brf-tech/filex:vX.Y.Z`    | varies  | Pinned-version slim.                                           |
| `ghcr.io/brf-tech/filex:slim-vX.Y.Z` / `full-vX.Y.Z` | varies | Pinned-version explicit variants. |

The Go binary inside both is identical — `full` only adds runtime tools to
unlock PDF / office / video thumbnails.

### Build locally

```bash
docker build -t ghcr.io/brf-tech/filex:slim -f docker/Dockerfile .
docker build -t ghcr.io/brf-tech/filex:full -f docker/Dockerfile.full .
```

Both Dockerfiles are multi-stage:
1. `frontend-build` — node 20 + pnpm, builds packages + admin UI
2. `embed-prep` — stages the dist files
3. `backend-build` — golang 1.23, builds with `//go:embed` consuming the staged dist
4. runtime — `alpine:3.20`, slim/full diverge here

Pass build-args to embed version metadata into the binary:
```bash
docker build \
  --build-arg VERSION=v0.1.0 \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  -t ghcr.io/brf-tech/filex:slim -f docker/Dockerfile .
```

---

## Compose profiles

`docker-compose.yml` (repo root) defines:

| Service       | Profile      | Notes |
|---------------|--------------|-------|
| `filex`       | (default)    | Slim image, SQLite + local storage |
| `filex-full`  | `full`       | Full image with thumbnail tools |
| `onlyoffice`  | `onlyoffice` | OnlyOffice Document Server |
| `postgres`    | `postgres`   | Postgres 16 (set `FILEX_DB_DRIVER=postgres`) |
| `minio`       | `minio`      | S3-compatible blob store |

Bring up with:

```bash
docker compose up                                     # filex slim only
docker compose --profile full up                      # filex with thumb tools
docker compose --profile onlyoffice up                # filex + OnlyOffice
docker compose --profile postgres --profile minio up  # full self-hosted stack
```

You can mix profiles freely:
```bash
docker compose --profile full --profile onlyoffice --profile postgres --profile minio up -d
```

### `.env`

Create `.env` next to `docker-compose.yml`:

```bash
# --- filex ---
FILEX_PUBLIC_URL=https://files.example.com
FILEX_AUTH_DRIVERS=oidc
FILEX_OIDC_ISSUER=https://auth.example.com/realms/main
FILEX_OIDC_CLIENT_ID=filex
FILEX_OIDC_CLIENT_SECRET=changeme
FILEX_DB_DRIVER=postgres
FILEX_DB_DSN=postgres://filex:changeme@postgres:5432/filex?sslmode=disable

# --- OnlyOffice ---
ONLYOFFICE_JWT_SECRET=please-change-me-shared-with-filex
FILEX_ONLYOFFICE_URL=https://docs.example.com
FILEX_ONLYOFFICE_JWT=please-change-me-shared-with-filex

# --- Postgres ---
POSTGRES_PASSWORD=changeme

# --- MinIO ---
MINIO_USER=filex
MINIO_PASSWORD=changeme-very-long
```

`docker-compose.yml` references all of these with safe defaults; secrets that
have no safe default use `${VAR:?msg}` and will fail-fast if missing.

---

## Volume layout

```
./data                       # FILEX_DATA_DIR — sqlite, search, thumbs, tmp
./storage-local              # default 'local' driver root (mounted into /var/lib/filex/local-storage)
filex-onlyoffice-data/       # docker volume (OnlyOffice docs)
filex-onlyoffice-logs/       # docker volume
filex-postgres-data/         # docker volume
filex-minio-data/            # docker volume
```

Use bind mounts (`./data`) when you want easy host-side backup; use named
volumes for everything Docker itself creates.

---

## Reverse proxies

filex always assumes a reverse-proxy in production. Set
`FILEX_TRUST_PROXY_HEADERS=true` so it honours `X-Forwarded-*`.

### nginx

```nginx
server {
  listen 443 ssl http2;
  server_name files.example.com;
  ssl_certificate     /etc/letsencrypt/live/files.example.com/fullchain.pem;
  ssl_certificate_key /etc/letsencrypt/live/files.example.com/privkey.pem;

  client_max_body_size 5G;     # match FILEX_LIMITS_MAX_UPLOAD_BYTES
  proxy_request_buffering off;
  proxy_buffering off;
  proxy_read_timeout 600s;
  proxy_send_timeout 600s;

  location / {
    proxy_pass         http://127.0.0.1:5212;
    proxy_set_header   Host              $host;
    proxy_set_header   X-Real-IP         $remote_addr;
    proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header   X-Forwarded-Proto $scheme;
    proxy_set_header   X-Forwarded-Host  $host;
  }
}
```

### Traefik (docker labels)

```yaml
services:
  filex:
    # ...
    labels:
      - traefik.enable=true
      - traefik.http.routers.filex.rule=Host(`files.example.com`)
      - traefik.http.routers.filex.entrypoints=websecure
      - traefik.http.routers.filex.tls.certresolver=letsencrypt
      - traefik.http.services.filex.loadbalancer.server.port=5212
      - traefik.http.middlewares.filex-bigbody.buffering.maxRequestBodyBytes=5368709120
      - traefik.http.routers.filex.middlewares=filex-bigbody
```

### Caddy

```caddyfile
files.example.com {
  encode zstd gzip
  reverse_proxy filex:5212 {
    flush_interval -1
    transport http {
      response_header_timeout 600s
      read_timeout 600s
    }
  }
}
```

### Cloudflare Tunnel

filex is plain HTTP/2 + WebSocket-free, so it works through `cloudflared`
without QUIC issues. Add a public hostname pointing to
`http://filex:5212` and CF will set the correct `X-Forwarded-*` headers
automatically.

---

## TLS termination

Three options:

1. **Reverse proxy terminates** (recommended) — set
   `FILEX_PUBLIC_URL=https://...` and `FILEX_TRUST_PROXY_HEADERS=true`.
   filex itself listens plain HTTP on 5212.
2. **Cloudflare Tunnel** — same as above, but Cloudflare is the proxy.
3. **filex direct TLS** (NOT recommended for prod) — set
   `FILEX_TLS_CERT=/path/to/cert.pem` and `FILEX_TLS_KEY=/path/to/key.pem`.
   Useful only for one-off or air-gapped deploys.

---

## Backups

Stop-the-world isn't required if you back up the DB consistently:

### SQLite
```bash
sqlite3 data/filex.db ".backup '/backup/filex-$(date -u +%Y-%m-%dT%H%M%SZ).db'"
```

### Postgres
```bash
docker compose exec postgres pg_dump -U filex filex | gzip > /backup/filex.sql.gz
```

### Storage backends
Backup is per-storage-driver: snapshot the host path for `local`, lifecycle
S3 versioning + lifecycle for `s3`, etc. filex keeps no canonical state of
the file bytes — the storage is the source of truth.

### What's safe to lose

- `data/search/`  — Bleve index. Rebuilt from DB on next sync if missing.
- `data/thumbs/`  — Cache. Regenerated lazily.
- `data/tmp/`     — Multipart staging. In-flight uploads will need to retry.

What's **not** safe to lose:
- `data/filex.db` (or your Postgres/MySQL DB) — auth, shares, audit, sync metadata.
- `data/.first-run.txt` — initial admin password (only useful pre-first-login).

---

## Upgrade

```bash
docker compose pull
docker compose up -d
```

Migrations run automatically on container start (goose). Rollbacks are
single-step and only intended for the same release line — across major
versions, **back up before upgrading**.

To pin a version:
```yaml
services:
  filex:
    image: ghcr.io/brf-tech/filex:slim-v0.2.0
```
