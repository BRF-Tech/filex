# Installation

Pick the path that matches how far you want to go:

| Path | What you get | Best for |
|---|---|---|
| [**Minimal**](#minimal) | One container, SQLite, a local folder. No external services. | Trying it out, small/personal use |
| [**Full stack (Compose)**](#full-stack-docker-compose) | filex + PostgreSQL + Redis + OnlyOffice + MinIO + Caddy (auto‑HTTPS). | Real self‑hosting, teams |
| [**Kubernetes (Helm)**](#kubernetes-helm) | Helm chart with toggles for the same components. | Clusters |
| [**Binary**](#binary) | A single static binary. | No Docker, systemd, edge devices |

All paths end at the same place: the admin UI at `…/admin`, with a first‑run
admin account (see [First run](#first-run)).

**Images.** `ghcr.io/brf-tech/filex:latest` is the full‑featured image
(thumbnails for image/video/pdf/office included). `…:slim` is a smaller image
without the thumbnail toolchain — thumbnails then degrade to placeholder cards.
The binary inside both is identical; only the runtime tooling differs.

---

## Minimal

The fastest way to a running instance — SQLite, embedded search + thumbnails,
one local storage folder, nothing external.

```bash
docker run -d --name filex -p 5212:5212 \
  -e FILEX_PUBLIC_URL=http://localhost:5212 \
  -v filex-data:/data \
  ghcr.io/brf-tech/filex:latest
```

Open <http://localhost:5212/admin> and grab the first‑run password from the logs
(`docker logs filex`).

Prefer Compose? Use [`deploy/compose/docker-compose.minimal.yml`](../deploy/compose/docker-compose.minimal.yml):

```bash
cd deploy/compose
docker compose -f docker-compose.minimal.yml up -d
docker compose -f docker-compose.minimal.yml logs -f      # first-run creds
```

It also mounts `./files` into the container at `/srv/files` — after logging in,
add a **local** storage in the UI pointing at `/srv/files` and drop files there
(see [STORAGE.md](STORAGE.md)).

> Set `FILEX_PUBLIC_URL` to the URL people actually open. Behind a reverse proxy
> that's your `https://…` domain — it's baked into share links, the OIDC
> redirect, and OnlyOffice callbacks.

---

## Full stack (Docker Compose)

A production‑shaped, one‑command stack: filex + **PostgreSQL** (database) +
**Redis** (queue) + **OnlyOffice** (document editing) + **MinIO** (S3 storage) +
**Caddy** (reverse proxy with automatic HTTPS). Files in
[`deploy/compose/`](../deploy/compose/).

**1. Configure.**

```bash
cd deploy/compose
cp .env.example .env
$EDITOR .env          # set domains + secrets
```

Set `FILEX_DOMAIN` / `ONLYOFFICE_DOMAIN` and strong secrets
(`openssl rand -hex 24`). The **`ONLYOFFICE_JWT_SECRET` must be identical**
between filex and OnlyOffice — the compose file already wires the same variable
to both.

> **Zero‑touch config.** The admin account, SSO/LDAP/header auth, SMTP, branding
> and an initial storage can all be set through `FILEX_*` env in `.env`, so the
> stack comes up already configured (no first‑run UI clicks). See
> [CONFIGURATION.md](CONFIGURATION.md) (Authentication + Zero‑touch seeding) and
> the [`deploy/compose/`](../deploy/compose/) examples.

**2. DNS.** Point `FILEX_DOMAIN` and `ONLYOFFICE_DOMAIN` (and
`MINIO_CONSOLE_DOMAIN` if you keep the console) at the host. Open ports **80 and
443** so Caddy can issue certificates.

**3. Launch.**

```bash
docker compose -f docker-compose.full.yml --env-file .env up -d
docker compose -f docker-compose.full.yml logs -f filex      # first-run creds
```

Caddy fetches HTTPS certs automatically; open `https://<FILEX_DOMAIN>/admin`.

**4. Wire the bundled MinIO as a storage.** Create a bucket in the MinIO console
(`https://<MINIO_CONSOLE_DOMAIN>`), then **Storages → Add** an S3 storage:

- endpoint `http://minio:9000` (filex reaches MinIO over the internal network)
- `path_style` = true, `region` = `auto`
- `bucket` = your bucket, `prefix` = `filex`
- `access_key` / `secret_key` = your `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD`

OnlyOffice is already connected — open any Office file to edit it. See
[ONLYOFFICE.md](ONLYOFFICE.md) and [STORAGE.md](STORAGE.md).

> **Want a lighter "full"?** Delete the services you don't need from the compose
> file and drop the matching `FILEX_*` env from the `filex` service — everything
> is additive.

---

## Kubernetes (Helm)

A Helm chart lives at [`deploy/helm/filex/`](../deploy/helm/filex/). It deploys
filex with a PVC for `/data` and an Ingress, and can pull in PostgreSQL, Redis,
OnlyOffice and MinIO as optional dependencies.

```bash
# Minimal: filex + SQLite + a local PVC.
helm install filex ./deploy/helm/filex \
  --set ingress.host=files.example.com

# Full: enable the bundled dependencies.
helm install filex ./deploy/helm/filex -f deploy/helm/filex/values-full.yaml \
  --set ingress.host=files.example.com \
  --set onlyoffice.ingress.host=office.example.com
```

Key `values.yaml` toggles: `postgresql.enabled`, `redis.enabled`,
`onlyoffice.enabled`, `minio.enabled`, `persistence.size`, `ingress.*`,
`resources`. See the chart's `values.yaml` for the full list.

> **Zero‑touch config.** Auth (OIDC/LDAP/header), the first admin, SMTP, branding
> and a default storage can all be supplied as `FILEX_*` env in the chart values,
> so a fresh release boots fully configured with no admin‑UI setup. See
> [CONFIGURATION.md](CONFIGURATION.md) (Authentication + Zero‑touch seeding) and
> the [`deploy/helm/filex/`](../deploy/helm/filex/) values examples.

---

## Binary

filex ships as a single static binary (CGO‑free) for linux/macOS/Windows ×
amd64/arm64. Download the archive for your platform from the **Releases** page,
extract, then:

```bash
./filex serve
```

Configuration is via `FILEX_*` environment variables (see
[CONFIGURATION.md](CONFIGURATION.md)) or a `config.yaml`
(`filex serve --config /path/to/config.yaml`, or the `FILEX_CONFIG` env). Data
goes to `~/.filex` by default (override with `FILEX_DATA_DIR`).

**systemd** (Linux):

```ini
# /etc/systemd/system/filex.service
[Unit]
Description=filex file manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=filex
ExecStart=/usr/local/bin/filex serve
Restart=on-failure
RestartSec=5s
Environment=FILEX_DATA_DIR=/var/lib/filex
Environment=FILEX_LISTEN=127.0.0.1:5212
Environment=FILEX_PUBLIC_URL=https://files.example.com

[Install]
WantedBy=multi-user.target
```

```bash
sudo useradd --system --home /var/lib/filex --create-home filex
sudo install -m 0755 filex /usr/local/bin/filex
sudo systemctl enable --now filex
```

Put it behind a reverse proxy for TLS (see [Behind a reverse proxy](#behind-a-reverse-proxy)).

### Build from source

Requires Go 1.25+, Node 20+, pnpm 9+.

```bash
git clone https://github.com/BRF-Tech/filex.git
cd filex
pnpm install
pnpm run build:all      # packages -> admin UI -> embed -> Go binary
./bin/filex serve
```

`CGO_ENABLED=0` means you can cross‑compile without a C toolchain
(`GOOS=linux GOARCH=arm64 go build ./backend/cmd/filex`).

---

## First run

On the very first start (empty user table) filex creates an admin account and
prints the password **once**:

```
First run detected. Initial admin user created:
    Email:    admin@local
    Password: <generated 16-char password>
  Saved to:  <data-dir>/.first-run.txt  (mode 0600, shown ONCE)
```

Sign in at `…/admin`, then change the password at **Profile**. Lost it? Reset
from the CLI:

```bash
filex admin random-password --email admin@local
# in Docker:  docker exec filex filex admin random-password --email admin@local
```

Enable TOTP 2FA per user under **Profile → Security**. Add SSO/LDAP via
[SSO.md](SSO.md) / [CONFIGURATION.md](CONFIGURATION.md).

---

## Behind a reverse proxy

filex serves everything from **one origin** on port `5212` — the SPA, the API,
public share (`/s/…`) and file‑drop (`/d/…`) pages, `/embed.js`, `/healthz`.
Forward `/` to `filex:5212` and:

- set `FILEX_PUBLIC_URL` to the external `https://…` URL,
- pass the real client IP (`X-Real-IP` / `X-Forwarded-For`) — used for audit and
  file‑drop rate limiting,
- allow large bodies (e.g. `5G`) and long timeouts for uploads,
- allow WebSocket/SSE upgrades (the MCP stream at `/api/ai/mcp`),
- enable gzip for the admin SPA.

The bundled Caddy config ([`deploy/compose/Caddyfile`](../deploy/compose/Caddyfile))
already does all of this; an nginx example is in [DEPLOYMENT.md](DEPLOYMENT.md).

---

## Data & backup

Everything filex owns lives under `FILEX_DATA_DIR` (`/data` in Docker):

- `instance.sqlite` — the database (unless you use PostgreSQL/MySQL),
- `search.bleve/` — the full‑text index (rebuildable),
- `thumbs/` — the thumbnail cache (regenerable),
- `.first-run.txt` — the initial admin secret.

Back up the **database** (the SQLite file, or your Postgres) and your **storage
backends**. The search index and thumbnail cache can be rebuilt
(`POST /api/admin/search/rebuild`, `filex thumb backfill`), so backing them up
is optional.

---

## Upgrading

Pull the new image (or binary) and restart — **migrations run automatically on
startup**. Back up the database first. To roll a schema back one step manually:
`filex migrate down`.

---

## See also

- [CONFIGURATION.md](CONFIGURATION.md) — every `FILEX_*` variable
- [STORAGE.md](STORAGE.md) · [SSO.md](SSO.md) · [ONLYOFFICE.md](ONLYOFFICE.md)
- [DEPLOYMENT.md](DEPLOYMENT.md) — reverse proxy, HTTPS, scaling, backup
