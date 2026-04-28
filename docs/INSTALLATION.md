# Installation

`filex` ships as **a single Go binary** with the admin UI and web-component
bundle embedded at build time. Pick whichever option fits your environment.

- [Docker](#docker)
- [Pre-built binary](#pre-built-binary)
- [Self-compile](#self-compile)
- [First-run flow](#first-run-flow)

---

## Docker

### Single container, defaults (SQLite + local storage)

```bash
docker run -d \
  --name filex \
  -p 5212:5212 \
  -v $(pwd)/data:/data \
  brftech/filex:latest
```

Open <http://localhost:5212/admin>. The first run prints initial admin
credentials to the container log:

```bash
docker logs filex
```

### docker-compose with profiles

A reference `docker-compose.yml` lives at the repo root. It supports profiles
for optional services:

```bash
# clone or copy the compose file + .env example
git clone https://gitlab.com/brftech/filemanager.git
cd filemanager

cp .env.example .env       # optional, edit if you need OIDC / MinIO / etc.

# default: just filex (slim)
docker compose up -d

# slim filex + OnlyOffice (office-doc editing)
docker compose --profile onlyoffice up -d

# full stack: thumbnail-capable filex + Postgres + MinIO
docker compose --profile full --profile postgres --profile minio up -d
```

See [DOCKER.md](DOCKER.md) for profile details, reverse-proxy snippets, and
volume layout.

### Slim vs full image

| Tag                       | Size    | Includes                                                       | Use case |
|---------------------------|---------|----------------------------------------------------------------|----------|
| `brftech/filex:slim`      | ~40 MB  | Go binary only                                                 | Image thumbs only (Go GD); no PDF/office/video thumbs |
| `brftech/filex:latest`    | ~40 MB  | Alias for `slim`                                               | Same as slim |
| `brftech/filex:full`      | ~250 MB | + ffmpeg, vips-tools, ghostscript, poppler-utils, libreoffice  | Full thumbnail support |

The binary inside both images is identical; the difference is the runtime
toolchain. Set `FILEX_THUMBS_ENABLED=true` to actually render thumbnails (it's
the default in the `full` image).

---

## Pre-built binary

GitLab Releases publish prebuilt binaries for **linux / macOS / Windows**
on **amd64 / arm64**.

```bash
# Linux x86_64
curl -L -o filex.tar.gz \
  https://gitlab.com/brftech/filemanager/-/releases/permalink/latest/downloads/filex_linux_x86_64.tar.gz
tar xzf filex.tar.gz
./filex serve
```

```bash
# macOS arm64 (Apple Silicon)
curl -L -o filex.tar.gz \
  https://gitlab.com/brftech/filemanager/-/releases/permalink/latest/downloads/filex_darwin_arm64.tar.gz
tar xzf filex.tar.gz
./filex serve
```

Windows: download `filex_windows_x86_64.zip` from the
[releases page](https://gitlab.com/brftech/filemanager/-/releases), unzip, run
`filex.exe serve`.

**Verify checksums:**

```bash
curl -L -O https://gitlab.com/brftech/filemanager/-/releases/permalink/latest/downloads/checksums.txt
sha256sum -c checksums.txt 2>&1 | grep filex_linux_x86_64
```

### systemd service (Linux)

```ini
# /etc/systemd/system/filex.service
[Unit]
Description=filex file manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=filex
Group=filex
ExecStart=/usr/local/bin/filex serve
Restart=on-failure
RestartSec=5s
Environment=FILEX_DATA_DIR=/var/lib/filex
Environment=FILEX_LISTEN=127.0.0.1:5212

[Install]
WantedBy=multi-user.target
```

```bash
sudo useradd --system --home /var/lib/filex --create-home filex
sudo install -m 0755 filex /usr/local/bin/filex
sudo systemctl daemon-reload
sudo systemctl enable --now filex
sudo journalctl -u filex -f
```

---

## Self-compile

Requirements:
- Go 1.22+
- Node.js 20+
- pnpm 9+

```bash
git clone https://gitlab.com/brftech/filemanager.git
cd filemanager

pnpm install
pnpm run build:all     # packages -> admin UI -> sync embed -> Go binary

./bin/filex serve
```

`build:all` runs four sub-tasks:
1. `pnpm -r --filter='./packages/*' build` — builds the npm packages
2. `pnpm --filter='./web' build` — builds the admin UI Vue SPA
3. `node scripts/sync-embed.mjs` — copies dist into `backend/embed/{admin,web}`
4. `cd backend && go build ...` — produces `bin/filex`

You can run them individually if you only changed one layer.

### Cross-compile

`CGO_ENABLED=0` lets you cross-compile without a C toolchain. Example for
Linux arm64 from a Mac:

```bash
cd backend
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
  -trimpath -ldflags='-s -w' \
  -o ../bin/filex-linux-arm64 ./cmd/filex
```

For the full release matrix (8 binaries) use goreleaser:

```bash
pnpm run release:dry      # snapshot, no publish
```

---

## First-run flow

The very first time `filex serve` starts against an empty data directory:

1. The binary creates `${FILEX_DATA_DIR}/filex.db` (SQLite by default) and
   runs migrations.
2. A random initial admin password is generated.
3. The credentials are printed to stdout **once** and saved to
   `${FILEX_DATA_DIR}/.first-run.txt` (mode `0600`).
4. After you log in and change the password, the file is wiped on shutdown.

```
═══════════════════════════════════════════════════════════════
  filex v0.1.0 · self-hosted file manager
═══════════════════════════════════════════════════════════════
  Listening on:   http://0.0.0.0:5212
  Admin UI:       http://0.0.0.0:5212/admin
  Embed JS:       http://0.0.0.0:5212/embed.js

  First run detected. Initial admin user created:
    Email:    admin@local
    Password: kT9_x4Pq2Nm-BvLs
  Saved to:  ~/.filex/.first-run.txt (mode 0600, shown ONCE)
  Change at: /admin/profile
═══════════════════════════════════════════════════════════════
```

If you missed the credentials and the file is gone, recover via:

```bash
filex admin reset-password --email admin@local
```

### Data directory layout

```
$FILEX_DATA_DIR/
├── filex.db                 # SQLite DB (or DSN points elsewhere)
├── .first-run.txt           # one-shot credentials (deleted after first password change)
├── search/                  # Bleve index
├── thumbs/                  # Thumbnail cache
├── tmp/                     # Multipart upload staging, archive scratch
└── local-storage/           # Default 'local' driver root (override per storage)
```
