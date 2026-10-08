# Docker

`filex` ships two pre-built images and a profile-driven `docker-compose.yml`
that lets you assemble the stack you actually need.

- [Images](#images)
- [Compose profiles](#compose-profiles)
- [Volume layout](#volume-layout)
- [Which user the container runs as](#which-user-the-container-runs-as)
- [The init process (PID 1)](#the-init-process-pid-1)
- [Reverse proxies](#reverse-proxies)
- [TLS termination](#tls-termination)
- [Backups](#backups)
- [Upgrade](#upgrade)

---

## Images

Sizes are what you download: the compressed layers the registry serves for
v0.52.0 on linux/amd64 (arm64: **~60 MB** slim, **~235 MB** full). On disk after
`docker pull` they unpack to more - **164 MB** for `slim` (`docker images`,
v0.31.0) and **~550 MB** for `full`. Both numbers are real; the compressed one
is what a registry page shows you and the other is what your disk loses, so
neither belongs in a sentence alone.

⚠ **0.50 took LibreOffice out of `full`.** Office documents - their thumbnails
and the apps' office conversions - come from the ONLYOFFICE Document Server
you connect under External services, on every tag, and filex runs no
LibreOffice anywhere. The full recipe with LibreOffice and its Java runtime
measured **642 MB** compressed and **1.6 GB** on disk on alpine:3.24; without
them, **225 MB** and **550 MB** (the gzip and the size of the image's
filesystem, built from `docker/Dockerfile` on 2026-10-02).

| Tag | Size | Includes |
|---|---|---|
| `ghcr.io/brf-tech/filex:latest` | ~241 MB | The full toolchain. Alias for `full`. |
| `ghcr.io/brf-tech/filex:full` | ~241 MB | + ffmpeg, ghostscript, poppler-utils, ImageMagick with `imagemagick-heic` and libheif's HEVC decoder `libheif-libde265` (HEIC, HEIF and AVIF thumbnails through libheif, since 0.50; +3 MB), rsvg-convert (the SVG fallback; SVG thumbnails are built in on every image since 0.50), fonts. No LibreOffice and no JRE since 0.50: office documents are the connected ONLYOFFICE's on every tag. On `:slim` a file whose tool is missing is listed under Admin → Tools → Thumbnail repair with the tool it needs. |
| `ghcr.io/brf-tech/filex:slim` | **~62 MB** | The Go binary and the embedded admin UI. Nothing else. |
| `:vX.Y.Z` / `:full-vX.Y.Z` | ~241 MB | Pinned full. |
| `:slim-vX.Y.Z` | ~62 MB | Pinned slim. |

The Go binary is identical in both - `slim` simply has none of the programs the
thumbnailer shells out to.

**Which one do you want?** `latest` if you want previews of PDFs, video and
audio, which is most people (office documents are previewed by a connected
ONLYOFFICE on either). `slim` if filex is a file manager
for you and not a preview generator: it pulls in seconds and carries a fraction
of the attack surface. Image thumbnails work in both, because those are
produced in pure Go.

filex probes for each external tool at start and reports what it found on
`/api/files/capabilities`, so on `slim` a video thumbnail is a disabled feature
with a stated reason - not a crash and not a silent failure. It also says so in
the log on the way up, naming the kinds it cannot draw and the package each one
wants, because the visible symptom is a grid of plain type tiles and that reads
as a design choice rather than a missing program.

> ⚠ **`slim` was not slim before v0.30.x.** The tag was built from the full
> recipe, so this table promised ~40 MB while the registry served 511 MB. The
> numbers above are measured, not aspirational: the sum of the layers the
> registry serves for the tag (v0.52.0, 2026-10-07; the slim image's grew from
> ~43 MB as the binary did).

### Build locally

```bash
docker build -t ghcr.io/brf-tech/filex:full -f docker/Dockerfile .
docker build -t ghcr.io/brf-tech/filex:slim -f docker/Dockerfile.slim .
```

Both Dockerfiles are multi-stage:
1. `frontend-build` - node 20 + pnpm, builds packages + admin UI
2. `embed-prep` - stages the dist files
3. `backend-build` - golang 1.25, builds with `//go:embed` consuming the staged dist
4. runtime - `alpine:3.24`; this is the only stage where slim and full differ.
   Both start `tini` as PID 1, which runs the entrypoint, which `exec`s filex -
   see [The init process](#the-init-process-pid-1)

Pass build-args to embed version metadata into the binary:
```bash
docker build \
  --build-arg VERSION=v0.1.0 \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  -t ghcr.io/brf-tech/filex:full -f docker/Dockerfile .
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
| `versitygw`   | `versitygw`  | S3 server (Versity S3 Gateway; [STORAGE.md → A local S3 server](STORAGE.md#a-local-s3-server)) |

The production-shaped stack in [`deploy/compose/docker-compose.full.yml`](../deploy/compose/docker-compose.full.yml)
adds two more profiles - `drawio` and **`clamav`** (the `convert` side-car was
removed in 0.48: conversion is the Convert app). The last one
is how antivirus is meant to be run under Docker: **the filex images ship no
scanner** (ClamAV plus its signature database is close to a gigabyte), so
`clamav/clamav` runs as its own container and filex streams each file to it
over the network. Two settings and nothing else:

```yaml
services:
  filex:
    environment:
      FILEX_CLAMAV_ADDR: "clamav:3310"   # seeds daemon mode on first boot
  clamav:
    image: clamav/clamav:latest
    profiles: ["clamav"]
    volumes:
      - clamav-db:/var/lib/clamav        # keep signatures across restarts
```

⚠ `FILEX_CLAMAV_ADDR` - like every `FILEX_CLAMAV*` variable except `_BIN` - is
a **seed**, read on a boot where the setting has no stored row and never again.
After that the switch, the mode and the address live on *Settings → Protection*
([PROTECTION.md](PROTECTION.md#antivirus-clamav)).

Bring up with:

```bash
docker compose up                                     # filex slim only
docker compose --profile full up                      # filex with thumb tools
docker compose --profile onlyoffice up                # filex + OnlyOffice
docker compose --profile postgres --profile versitygw up  # full self-hosted stack
```

You can mix profiles freely:
```bash
docker compose --profile full --profile onlyoffice --profile postgres --profile versitygw up -d
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

# --- S3 server (versitygw) ---
S3_ACCESS_KEY=filex
S3_SECRET_KEY=changeme-very-long
```

`docker-compose.yml` references all of these with safe defaults; secrets that
have no safe default use `${VAR:?msg}` and will fail-fast if missing.

---

## Volume layout

```
./data                       # FILEX_DATA_DIR - sqlite, search.bleve, thumbs,
                             #   cache, uploads, ssh, ftps, plugins, dav
./storage-local              # default 'local' driver root (mounted into /var/lib/filex/local-storage)
filex-onlyoffice-data/       # docker volume (OnlyOffice docs)
filex-onlyoffice-logs/       # docker volume
filex-postgres-data/         # docker volume
filex-s3-data/               # docker volume (S3 objects, plain files)
filex-s3-versions/           # docker volume (older object versions)
```

Use bind mounts (`./data`) when you want easy host-side backup; use named
volumes for everything Docker itself creates.

---

## Which user the container runs as

**By default, as root.** That is the honest answer and it has consequences
you should know before you bind-mount anything: everything under
`FILEX_DATA_DIR` is created owned by `root:root`, so on the host you need
`sudo` to read or delete your own `./data` directory.

Two ways to change it. Both are opt-in, because a default that dropped
privilege would leave every existing install unable to open a database it
already owns as root.

### `PUID` / `PGID` (the usual self-hosted way)

```bash
docker run -p 5212:5212 \
  -e PUID=$(id -u) -e PGID=$(id -g) \
  -v filex-data:/data \
  ghcr.io/brf-tech/filex:latest
```

The entrypoint takes ownership of the **data directory** once, writes a
`.filex-uid` marker recording what it chowned to, and drops to that
uid/gid with `su-exec`. Later boots read the marker and skip the walk, so
the cost is paid on the first start and never again.

This is safe to turn on for an install that has been running as root: the
chown is what makes the existing database readable to the new user. It is
one-way in practice - after it, removing `PUID` puts you back to root,
which can still read files owned by anyone.

### `--user` / `user:` / `runAsUser` (Docker's and Kubernetes' way)

```yaml
services:
  filex:
    image: ghcr.io/brf-tech/filex:latest
    user: "1000:1000"
```

The container starts unprivileged, so there is nothing to drop and nothing
it is allowed to chown. **You must make the data directory writable by that
uid yourself** before the first start:

```bash
sudo chown -R 1000:1000 ./data
```

`PUID` is ignored here and the entrypoint says so in the log rather than
pretending to honour it.

### What is *not* chowned

⚠ Only the data directory. The folders holding your files - a `local`
storage root, an NFS or SMB mount, anything you bind at `/srv/files` - are
left exactly as they are. They may be shared with other software, they may
be enormous, and re-owning them is not a container's decision to make. If
filex cannot write to a storage after you set `PUID`, fix that folder's
permissions.

| | runs as | chowns `/data` | you must prepare `/data` |
|---|---|---|---|
| default | `root` | no | no |
| `PUID`/`PGID` | that uid | yes, once | no |
| `--user` / `runAsUser` | that uid | no (cannot) | **yes** |

---

## The init process (PID 1)

Both images start **[tini](https://github.com/krallin/tini)** as PID 1:

```
ENTRYPOINT ["/sbin/tini", "-s", "--", "/usr/local/bin/docker-entrypoint.sh"]
CMD ["serve"]
```

tini runs the entrypoint, the entrypoint `exec`s filex (as root, or as your
`PUID`/`PGID`), and tini stays behind to do the two things PID 1 has to do in a
container: pass `docker stop`'s SIGTERM on to filex, and **collect every
orphaned process**.

⚠ The second one is why it is there. Images up to v0.42.2 ran filex itself as
PID 1, and PID 1 inherits every process whose parent exits. LibreOffice, which
drew office-document thumbnails until 0.50, left helper processes behind (`gpgconf`,
`gpgsm`, `gpg`); the Go runtime only ever waits for the children it started
itself, so nobody collected them. Measured on one deployment: **19,110 zombie
processes in ten days**, until the container's task limit was full - at which
point the healthcheck could not fork `wget` and every thumbnail failed with
`can't fork`. On an older image this counts them, and `init: true` (below) is
the workaround:

```bash
docker exec filex sh -c "grep -l '^State:.*Z' /proc/[0-9]*/status | wc -l"
```

**`init: true` / `docker run --init`** is harmless alongside it, and no longer
needed. Docker's own init becomes PID 1 and tini runs under it; `-s` registers
tini as a subreaper, so it still collects what is orphaned beneath it instead of
warning that it cannot.

**Kubernetes** needs nothing either - the image's entrypoint is tini whatever
the pod spec says, as long as you do not replace it.

⚠ **If you override the entrypoint** (`--entrypoint`, compose `entrypoint:`, a
Kubernetes `command:`), you replace tini too. Keep it in front -
`["/sbin/tini", "-s", "--", …]` - or run with `init: true`.

---

## Reverse proxies

filex assumes a reverse proxy in production. The forwarded headers are read
two ways:

- **The client's address** (`X-Forwarded-For`, `X-Real-IP`) is believed
  **only from a trusted proxy** - `FILEX_TRUSTED_PROXIES`, and by default
  **`auto`**: in a container on a network of its own, filex trusts the other
  containers on that network (a Caddy, nginx, Traefik or cloudflared
  container in front of it) - never the network's gateway, never its own
  address, never the LAN. It is the address the sign-in attempt limit counts
  by and the access and audit logs record. Up to 0.49 this header was believed
  from anyone ([CONFIGURATION.md → Trusted proxies](CONFIGURATION.md#trusted-proxies)).
- **`X-Forwarded-Proto`**: `https` from any peer marks the session cookie
  `Secure` (it can only add the flag). `http`, which makes a tenant's links -
  the ones filex mails, too - `http://`, is believed **only from a trusted
  proxy**, like the client's address (since 0.54).

What `auto` trusts in Docker, measured on Docker 29:

| The proxy | It reaches filex from | Trusted by `auto` |
|---|---|---|
| A container on the same network (`reverse_proxy filex:5212`) | its own address on that network, e.g. `172.18.0.3` | **yes** |
| A container on another network filex is also attached to | its address on that network | **yes** (every network filex is on counts) |
| On the **host**, through a port published on `127.0.0.1` (`proxy_pass http://127.0.0.1:5212`) | the network's **gateway**, e.g. `172.18.0.1` - `docker-proxy` relays it | **no** |
| On the host, dialling the container's address | the gateway | **no** |
| On another machine of the LAN, through the published port | its own LAN address | **no** |
| filex with `network_mode: host` | - | only `127.0.0.1` / `::1` |
| A peer on a macvlan / ipvlan network filex is on | its LAN address | **no** (a bridge network filex is also on still counts) |

⚠ **A proxy on the host or on another machine must be listed**, or every
visitor resolves to the proxy's address and shares one sign-in counter. The
**Sign-in security** page names a peer that sends forwarded addresses without
being trusted and offers to add it (`auto, <address>`); by hand:

```yaml
services:
  filex:
    environment:
      # nginx on the host, reaching the container through 127.0.0.1:5212
      FILEX_TRUSTED_PROXIES: "auto, 172.30.0.1"
networks:
  default:
    ipam:
      config:
        - subnet: 172.30.0.0/24   # a fixed subnet, so the gateway stays 172.30.0.1
```

Trust the gateway **only when the published port is reachable by your proxy
alone** - published on `127.0.0.1` (`127.0.0.1:5212:5212`) or firewalled:
whatever `docker-proxy` relays arrives from that address, and a stranger who
reaches the port could otherwise choose the address they are counted by. A
proxy on another machine is listed by its own address. A proxy that reaches
filex over `100.64.0.0/10` (Tailscale, Cloudflare WARP) and a CDN in front of
your proxy are listed too.

⚠ Earlier revisions of this page told you to set `FILEX_TRUST_PROXY_HEADERS`.
No such variable is read anywhere in filex; the one that decides is
`FILEX_TRUSTED_PROXIES`. Published with **no** proxy in front, nothing needs
setting: `auto` trusts no client on the LAN (until 0.50's `auto`, the rule was
to set `none` there).

### nginx

nginx on the **host** reaches the container through the published port, so
it arrives from the Docker gateway: list it (above), or run nginx as a
container on filex's network.

```nginx
server {
  listen 443 ssl http2;
  server_name files.example.com;
  ssl_certificate     /etc/letsencrypt/live/files.example.com/fullchain.pem;
  ssl_certificate_key /etc/letsencrypt/live/files.example.com/privkey.pem;

  client_max_body_size 5G;     # big enough for one upload chunk; see FILEX_UPLOAD_CHUNK_SIZE
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

Add a public hostname pointing to `http://filex:5212` and CF will set the
correct `X-Forwarded-*` headers automatically. A `cloudflared` container on
filex's network is trusted by `auto`; one installed on the host reaches the
published port from the gateway and is listed like nginx on the host.

⚠ **Leave WebSocket support on.** filex serves a WebSocket at `GET /api/ws`,
and an open explorer that has one **does not poll** - the 12 s re-listing is
only the fallback for a socket that failed. Block the upgrade and every
browser silently degrades to a folder that refreshes twice a minute, which is
the shape of "I upload a file and it shows up ten minutes later". The MCP
stream at `/api/ai/mcp` needs the same. See
[Realtime](REALTIME.md) and [Deployment](DEPLOYMENT.md).

⚠ **Keep cache rules off filex's host.** Every `/api/` answer carries
`Cache-Control: no-store` - thumbnails, file content and share downloads say
`private, …`, and the four answers that say who the instance is (branding,
themes, the offered languages and a language's strings) say `public, no-cache`
with an ETag, the same for every visitor - and Cloudflare honors it by default. A rule whose edge TTL *ignores* origin headers does not. A zone-wide
"cache everything" rule written for a website on the same domain once kept
`/api/auth/me` for two hours and showed one user's identity to everyone who
asked. Scope such rules to the website's own hosts, e.g.
`http.host in {"example.com" "www.example.com"}`.

---

## TLS termination

Three options:

1. **Reverse proxy terminates** (recommended) - set
   `FILEX_PUBLIC_URL=https://...`. filex itself listens plain HTTP on 5212 and
   reads `X-Forwarded-Proto` from the proxy; the client address in
   `X-Forwarded-For` / `X-Real-IP` is believed only from a trusted proxy -
   by default (`auto`) a container on filex's own network
   ([Reverse proxies](#reverse-proxies)).
2. **Cloudflare Tunnel** - same as above, but Cloudflare is the proxy.

⚠ There is no third option. This page used to offer "filex direct TLS" via
`FILEX_TLS_CERT` / `FILEX_TLS_KEY`: **the HTTP server has no TLS listener** and
neither variable is read, so an operator who set both got plain HTTP on 5212
with no warning - the worst possible outcome for a setting whose entire purpose
is encryption. (The `cert_file` / `key_file` pair that does exist belongs to the
**FTPS** endpoint; see [PROTOCOLS.md](PROTOCOLS.md).) Put a proxy in front.

---

## Backups

Stop-the-world isn't required if you back up the DB consistently:

### SQLite

⚠ Check the filename against your own `FILEX_DB_DSN` first. filex's default is
`<data-dir>/instance.sqlite`; the `docker-compose.yml` in this repo pins
`FILEX_DB_DSN=/data/filex.db`, which is the name below.

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
the file bytes - the storage is the source of truth.

### What's safe to lose

- `data/search.bleve/` - Bleve index. Rebuilt from the DB if missing.
- `data/thumbs/`  - Cache. Regenerated lazily; a cached file is released when
  its node is purged, and orphans are swept every `FILEX_THUMBS_SWEEP_INTERVAL`.
- `data/cache/`   - read cache for slow storages.
- `data/uploads/` - staging for chunked and resumable uploads. In-flight
  uploads will need to retry. ⚠ For a transfer that has not committed yet,
  this is the file's **only** copy.

What's **not** safe to lose:
- `data/instance.sqlite` - or `data/filex.db` under this repo's compose, or your
  Postgres/MySQL DB: auth, shares, audit, sync metadata.
- `data/ssh/` + `data/ftps/` - SFTP host keys and the FTPS certificate.
  Regenerating them is a changed host key, and every client that connected
  before refuses the next connection until it is cleared.
- `data/.first-run.txt` - initial admin password (only useful pre-first-login).

---

## Upgrade

```bash
docker compose pull
docker compose up -d
```

Migrations run automatically on container start (goose). Rollbacks are
single-step and only intended for the same release line - across major
versions, **back up before upgrading**.

To pin a version:
```yaml
services:
  filex:
    image: ghcr.io/brf-tech/filex:slim-vX.Y.Z
```

⚠ The registry is `ghcr.io/brf-tech/filex`. A bare `brftech/filex` is a Docker
Hub name nobody publishes, and a compose file that names it fails the pull with
*"repository does not exist"*.
