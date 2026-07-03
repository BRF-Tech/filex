# Deployment

Running filex in production. This doc assumes you've already got an instance up
(see [INSTALLATION.md](INSTALLATION.md)) and covers what sits **around** it: the
reverse proxy, TLS, the one setting that trips everyone up (`FILEX_PUBLIC_URL`),
scaling, backups, health checks, and a hardening pass.

filex is a single Go process that serves **everything from one origin** on port
`5212` — the admin SPA, the JSON API, public share (`/s/…`) and file‑drop
(`/d/…`) pages, the `/embed.js` web component, the MCP stream at
`/api/ai/mcp`, and `/healthz`. There is no separate frontend to host and no
second port. Production deployment is therefore mostly "put a good reverse proxy
in front of port 5212."

- [Reverse proxy](#reverse-proxy) — [Caddy](#caddy) · [nginx](#nginx)
- [HTTPS](#https)
- [PUBLIC_URL](#public_url)
- [Scaling / high availability](#scaling--high-availability)
- [Backup & restore](#backup--restore)
- [Health & monitoring](#health--monitoring)
- [Hardening checklist](#hardening-checklist)
- [See also](#see-also)

---

## Reverse proxy

Terminate TLS at a reverse proxy and forward **`/`** to `filex:5212` (Docker) or
`127.0.0.1:5212` (binary). filex speaks plain HTTP internally — don't try to make
it terminate TLS itself. Whatever proxy you pick must:

- **Set `FILEX_PUBLIC_URL`** to the external `https://…` URL (see
  [PUBLIC_URL](#public_url)) — this is on filex, not the proxy, but it only makes
  sense once you know the public hostname.
- **Pass the real client IP** as `X-Real-IP` and/or `X-Forwarded-For`. filex reads
  it for the **audit log** and the **file‑drop rate limiter** — without it every
  request looks like it came from the proxy.
- **Allow large request bodies** (e.g. `5G`) and **long timeouts** (~600 s) for
  uploads. For **S3** storages the browser PUTs multipart chunks straight to the
  bucket and only the small init/finalize JSON transits the proxy — but
  **local / SFTP / WebDAV / FTP** uploads stream the whole file through it, so the
  limits have to be generous.
- **Allow WebSocket / SSE upgrades.** The MCP endpoint at `/api/ai/mcp` is a
  long‑lived streamable‑HTTP transport (POST for requests, GET to open the SSE
  stream); download/upload streaming benefits from unbuffered proxying too.
- **gzip/zstd the admin SPA** and JSON responses (the hashed Vite assets are
  large; filex already sets long `Cache-Control` on them).

### Caddy

The bundled config — [`deploy/compose/Caddyfile`](../deploy/compose/Caddyfile) —
already does all of the above and gets HTTPS for free. A standalone equivalent:

```caddy
files.example.com {
	encode zstd gzip

	# Large uploads: local/SFTP uploads stream through here (S3 multipart
	# PUTs go browser→bucket directly, so only init/finalize JSON transits).
	request_body {
		max_size 5GB
	}

	# WebSocket/SSE (the MCP stream at /api/ai/mcp) upgrade automatically.
	# Caddy also adds X-Forwarded-For / -Proto / -Host on its own.
	reverse_proxy 127.0.0.1:5212 {
		header_up X-Real-IP {remote_host}
	}
}
```

That's the whole file. Caddy obtains and renews the certificate automatically
(see [HTTPS](#https)). In the Compose stack the upstream is `filex:5212` over the
internal Docker network instead of `127.0.0.1:5212`.

### nginx

A complete server block. The `map` at the top turns the `Upgrade` header into the
right `Connection` value so WebSocket/SSE upgrades work:

```nginx
# /etc/nginx/conf.d/filex.conf
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 443 ssl;
    http2  on;
    server_name files.example.com;

    ssl_certificate     /etc/letsencrypt/live/files.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/files.example.com/privkey.pem;

    # Large uploads (local/SFTP/WebDAV/FTP storages stream through the proxy).
    client_max_body_size 5g;

    # Compress the admin SPA + API JSON (hashed assets are already cached long).
    gzip            on;
    gzip_min_length 1024;
    gzip_types      text/css application/javascript application/json image/svg+xml;

    location / {
        proxy_pass         http://127.0.0.1:5212;
        proxy_http_version 1.1;

        # Real client IP — used for the audit log + file-drop rate limit.
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket / SSE upgrade.
        proxy_set_header Upgrade    $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        # Long timeouts for big uploads/downloads; don't buffer them to disk.
        proxy_read_timeout      600s;
        proxy_send_timeout      600s;
        proxy_request_buffering off;
    }

    # The MCP stream needs unbuffered, long-lived proxying so SSE tokens flush
    # immediately instead of piling up in nginx's buffer.
    location /api/ai/mcp {
        proxy_pass         http://127.0.0.1:5212;
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_set_header   Upgrade           $http_upgrade;
        proxy_set_header   Connection        $connection_upgrade;
        proxy_buffering    off;
        proxy_read_timeout 3600s;
    }
}

# Redirect http → https.
server {
    listen 80;
    server_name files.example.com;
    return 301 https://$host$request_uri;
}
```

---

## HTTPS

Always terminate TLS at the proxy and keep filex plain‑HTTP behind it.

- **Caddy** issues and renews certificates automatically via Let's Encrypt /
  ZeroSSL. Just point DNS at the host and open ports **80 and 443** so the
  ACME challenge succeeds. Nothing else to configure.
- **nginx** pairs with **certbot**: `certbot --nginx -d files.example.com` writes
  the `ssl_certificate` lines and sets up a renewal timer. Or terminate at a
  managed load balancer and forward plain HTTP to filex.
- **Kubernetes**: terminate at the Ingress with **cert‑manager** (a `ClusterIssuer`
  + a `tls` block / annotation on the Ingress). The Helm chart's `ingress.*` values
  wire this up — see [INSTALLATION.md → Kubernetes](INSTALLATION.md#kubernetes-helm).

Whichever you use, the external scheme must be `https` and must match
`FILEX_PUBLIC_URL`.

---

## PUBLIC_URL

`FILEX_PUBLIC_URL` (default `http://localhost:5212`) is **the externally
resolvable URL users open in a browser** — e.g. `https://files.example.com`.
Behind a proxy this is the proxy's public hostname, **not** the internal
`filex:5212`.

filex bakes this value into things it can't recompute from an incoming request:

- **Share and file‑drop links** (`/s/…`, `/d/…`) — the URLs handed to recipients.
- **The OIDC redirect** — the callback is `<public>/api/auth/oidc/callback`; it
  must match what's registered at your IdP.
- **OnlyOffice fetch/callback** — the Document Server fetches the file and posts
  edits back **server‑to‑server** using filex's public URL.

A wrong value fails quietly in production: share links point at `localhost` and
404 for everyone else, SSO login bounces to an unreachable/invalid redirect, and
OnlyOffice **saves fail** because the Document Server can't reach the callback.
Set it to the real HTTPS URL and keep it in sync with your DNS and IdP config.

> The binary/container listens on `FILEX_LISTEN` (default `0.0.0.0:5212`);
> `FILEX_PUBLIC_URL` is a separate, public‑facing value. They're almost never the
> same behind a proxy.

---

## Scaling / high availability

**Start with one vertically‑scaled node.** filex has no built‑in clustering or
leader election; a single instance backed by PostgreSQL handles real teams
comfortably. Reach for multiple replicas only when you actually need them, and
know the constraints first.

**The database is the gate.** The default **SQLite** store is a single file with
a process‑level lock — it's fine for one node but **cannot** be shared by
replicas. For more than one filex instance you must move the database off SQLite:

```bash
FILEX_DB_DRIVER=postgres
FILEX_DB_DSN=postgres://filex:…@db:5432/filex?sslmode=require
```

PostgreSQL is the recommended production driver (MySQL works for read‑mostly use;
a few upsert paths are SQLite/Postgres‑only — see
[CONFIGURATION.md → Database](CONFIGURATION.md#database)).

**The queue must be shared too.** The default `sqlite` queue lives in the app DB.
For multiple workers/nodes point it at Postgres or Redis:

```bash
FILEX_QUEUE_DRIVER=postgres    # uses SELECT … FOR UPDATE SKIP LOCKED
# or
FILEX_QUEUE_DRIVER=redis
FILEX_QUEUE_DSN=redis://redis:6379/0
```

**`/data` is per‑instance.** Beyond the SQLite DB, each node keeps two things on
local disk under `FILEX_DATA_DIR`:

- `search.bleve/` — the full‑text index (its embedded store takes an **exclusive
  file lock**, so it can't be shared read‑write either),
- `thumbs/` — the thumbnail cache.

Both are **rebuildable** (see [Backup & restore](#backup--restore)), so for a
multi‑replica deployment you have two honest options:

1. **Per‑node local `/data`** (an emptyDir‑style volume each) — every replica
   builds its own search index and thumbnail cache. Simplest; costs some
   duplicate work and each node's index lags until it syncs.
2. **A shared RWX volume** for `/data` — only safe if you're confident about the
   file‑lock semantics of your shared filesystem; when in doubt prefer option 1.

Sessions and API tokens are validated against the database, so a shared Postgres
keeps logins working across replicas without sticky sessions — just make sure
every replica shares the **same DB, the same secrets, and the same
`FILEX_PUBLIC_URL`**. Storage backends (S3, SFTP, …) are external and already
shared by definition.

> **Bottom line:** filex scales *up* trivially and scales *out* to a Postgres +
> Redis/Postgres‑queue topology with per‑node (or carefully‑shared) search/thumb
> state. It does not yet ship a turnkey active‑active cluster.

---

## Backup & restore

Two things hold real state; back up **both**:

1. **The database** — the SQLite file (`<data_dir>/instance.sqlite`) or your
   PostgreSQL/MySQL. This is the source of truth for users, storages,
   permissions, shares, tags, the file‑node cache, and audit history.
2. **The storage backends** — the actual file bytes. filex doesn't own these; a
   local disk, an S3 bucket, an SFTP server each have their own backup story.
   Back them up where they live.

**Rebuildable, so backing them up is optional:**

- `search.bleve/` — regenerate with `POST /api/admin/search/rebuild` (admin) or
  by deleting it and restarting.
- `thumbs/` — regenerate with `filex thumb backfill` (add `--retry-failed` to
  re‑run failed rows, `--storage <name|id>` to scope it).

**Restore** = restore the database, restore/attach the storage backends, start
filex. If you skipped the search index and thumbnail cache in your backup, filex
serves immediately and you trigger a rebuild/backfill to repopulate them.

**Upgrades run migrations on startup.** Pull the new image or binary and restart —
schema migrations apply automatically. **Back up the database before upgrading.**
To inspect or roll back one step manually:

```bash
filex migrate status     # what's applied / pending
filex migrate down       # roll back exactly one migration
# in Docker: docker exec filex filex migrate down
```

---

## Health & monitoring

- **Liveness / readiness:** `GET /healthz` returns `200` with `{"status":"ok"}`
  and needs no authentication. Wire it to your load balancer health check and to
  Kubernetes liveness+readiness probes. The Docker image's built‑in `HEALTHCHECK`
  already polls it every 30 s.
- **Error reporting:** set `FILEX_SENTRY_DSN` (and optionally
  `FILEX_SENTRY_ENVIRONMENT=production`) to ship crashes/errors to Sentry or a
  self‑hosted GlitchTip. Empty DSN = off. See
  [CONFIGURATION.md → Error reporting](CONFIGURATION.md#error-reporting).
- **Structured logs:** `FILEX_LOG_FORMAT=json` (with `FILEX_LOG_LEVEL=info|debug|…`)
  emits JSON lines for Loki / ELK / Datadog ingestion.
- **Operational surfaces** (admin session/token): the admin **Dashboard**, plus
  queue stats (`/api/admin/queue/stats`) and storage **sync‑runs / drift**
  (`/api/admin/storages/{id}/sync-runs`, `…/drift`) let you watch worker health
  and detect a backend that's drifting from the cache.

---

## Hardening checklist

- [ ] **Strong, unique secrets.** Generate them (`openssl rand -hex 24`) and keep
      them out of version control — never commit `.env`. The
      `ONLYOFFICE_JWT_SECRET` must be identical on filex and the Document Server;
      the OIDC client secret must match your IdP.
- [ ] **HTTPS everywhere.** TLS terminated at the proxy, `FILEX_PUBLIC_URL` on
      `https://`.
- [ ] **Restrict CORS if you embed.** The default `FILEX_CORS_ALLOWED_ORIGINS=*`
      is convenient but permissive. If you embed the web component (`/embed.js`)
      or the explorer from specific host apps, pin the list to those origins.
- [ ] **Read‑only storages where writes aren't needed.** Set `read_only: true` on
      archive/replica mounts so uploads, renames, moves and deletes are refused
      (see [STORAGE.md → Read‑only mounts](STORAGE.md#read-only-mounts)).
- [ ] **RBAC per storage.** Enable `rbac_enabled` on storages that shouldn't be
      visible to every authenticated user; grants then gate access (see
      [RBAC.md](RBAC.md)).
- [ ] **Least‑privilege API tokens.** Scope tokens to only the verbs they need
      (`read` / `write` / `delete` / `mcp` / `admin`) and use **`root:`
      confinement** to lock a token to one sub‑folder. Confinement is enforced in
      the backend, so a confined token can't escape its root even if it knows
      other paths.
- [ ] **TOTP 2FA for admins.** Enable it per admin under **Profile → Security**.
- [ ] **Keep the proxy the sole ingress.** If you use proxy‑header auth
      (`auth.header_proxy`), filex trusts identity headers like `X-Auth-Email` —
      so bind filex to localhost / the internal network (`FILEX_LISTEN` on
      `127.0.0.1:5212`, or don't publish the container port), set `trusted_ips` to
      the proxy, and make sure **nothing can reach 5212 except the proxy**, or a
      client could spoof those headers.
- [ ] **Disable demo mode.** `FILEX_DEMO_MODE` is `false` by default — keep it
      that way in production (it auto‑fills demo credentials on the login page).

---

## See also

- [INSTALLATION.md](INSTALLATION.md) — get an instance running first
- [CONFIGURATION.md](CONFIGURATION.md) — every `FILEX_*` variable
- [STORAGE.md](STORAGE.md) — storage backends, read‑only mounts, sync
- [RBAC.md](RBAC.md) — per‑storage / per‑file access control
- [SSO.md](SSO.md) · [ONLYOFFICE.md](ONLYOFFICE.md)
