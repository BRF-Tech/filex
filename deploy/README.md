# `deploy/` — production artifacts

Drop-in files for either the Hetzner main box (CloudPanel + nginx) or
the brkip DR-site (Caddy with `tls internal`). See
[`docs/DEPLOY_BRF.md`](../docs/DEPLOY_BRF.md) for the full runbook —
this directory just collects the actual configuration that gets copied
onto the server.

The first ship is **demo-fm.brf.sh on brkip** (standalone, demo
purposes). brf-mono integration is a separate phase.

| File | Where it goes |
|------|---------------|
| `demo-fm.brf.sh.compose.yml` | `/opt/brkip-stack/filex-demo/docker-compose.yml` (brkip) **or** `/root/filex/docker-compose.yml` (main) |
| `.env.example`               | `<dest>/.env` after editing — chmod 600 |
| `Caddyfile.demo-fm.brf.sh`   | `/opt/brkip-stack/caddy/Caddyfile.d/demo-fm.brf.sh.caddy` (brkip) |
| `nginx.demo-fm.brf.sh.conf`  | `/etc/nginx/sites-available/demo-fm.brf.sh` (main host alternative) |
| `keycloak-client-filex.json` | imported via Keycloak admin UI |

## brkip standalone demo deploy (recommended for v0.1.0)

```bash
# 1. DNS — brf.sh zone, A record (proxied:false). Reuses the brkip
#    CF Tunnel for ingress; no LE cert via brkip itself, brkip Caddy
#    runs internal CA + the tunnel terminates TLS at Cloudflare.
cf_zone=15a5559714ccad6709385b135d89efd3
curl -fsS -X POST "https://api.cloudflare.com/client/v4/zones/${cf_zone}/dns_records" \
  -H "Authorization: Bearer $CF_TOKEN" -H "Content-Type: application/json" \
  -d '{"type":"A","name":"demo-fm","content":"88.228.71.208","proxied":false,"ttl":1}'

# 2. Drop the compose + Caddy fragment + env onto brkip.
ssh brkip "mkdir -p /opt/brkip-stack/filex-demo"
scp deploy/demo-fm.brf.sh.compose.yml brkip:/opt/brkip-stack/filex-demo/docker-compose.yml
scp deploy/.env.example                brkip:/opt/brkip-stack/filex-demo/.env
ssh brkip "chmod 600 /opt/brkip-stack/filex-demo/.env"
scp deploy/Caddyfile.demo-fm.brf.sh    brkip:/opt/brkip-stack/caddy/Caddyfile.d/demo-fm.brf.sh.caddy

# 3. Edit the .env on brkip — fill in FILEX_OIDC_CLIENT_SECRET, etc.
ssh brkip "vim /opt/brkip-stack/filex-demo/.env"

# 4. Boot.
ssh brkip "cd /opt/brkip-stack/filex-demo && docker compose pull && docker compose up -d"
ssh brkip "docker compose -f /opt/brkip-stack/filex-demo/docker-compose.yml logs -f filex"
# ↑ first-run banner prints the admin password — copy it before it scrolls

# 5. Caddy reload.
ssh brkip "docker exec brkip-caddy caddy reload --config /etc/caddy/Caddyfile"

# 6. Smoke.
curl -fsSL https://demo-fm.brf.sh/healthz
# {"status":"ok"}
```

## Migrating to main host (CloudPanel + nginx) later

`nginx.demo-fm.brf.sh.conf` is the alternative reverse-proxy template
for the main Hetzner box. Copy it to
`/etc/nginx/sites-available/demo-fm.brf.sh`, symlink into
`sites-enabled/`, run `nginx -t && systemctl reload nginx`. CloudPanel
ships the LE cert paths referenced in the file (`/etc/letsencrypt/
live/demo-fm.brf.sh/...`).
