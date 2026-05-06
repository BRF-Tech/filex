# filex deployment — `demo-fm.brf.sh`

End-to-end deployment runbook for the Hetzner main box. All paths are
production paths; substitute as needed.

## TL;DR

```bash
ssh main
mkdir -p /root/filex /opt/filex/data /opt/filex/storages
cd /root/filex
# copy compose + env + nginx + keycloak files from the repo's deploy/ dir
docker compose pull
docker compose up -d
docker compose logs filex            # banner — copy admin password ONCE
nginx -t && systemctl reload nginx   # bring https://demo-fm.brf.sh online
```

## 1. Prerequisites

- DNS: A record `demo-fm.brf.sh → 167.235.143.222` (`proxied: false` in
  Cloudflare zone `brf.sh`). The DR site can later point a CNAME at
  brkip if/when needed.
- Keycloak realm `brf` (auth.brf.sh) with admin access.
- Reverse proxy: CloudPanel-managed nginx already running on main.
- TLS: Let's Encrypt via certbot. CloudPanel handles the cert lifecycle
  in its standard flow.
- Backrest entry (`/opt/filex` should already be in the `all-services`
  plan; double-check the path includes `data/instance.sqlite`).

## 2. Cloudflare DNS

```bash
# From your laptop (uses CF API token in keychain / 1Password)
curl -X POST "https://api.cloudflare.com/client/v4/zones/15a5559714ccad6709385b135d89efd3/dns_records" \
  -H "Authorization: Bearer ${CF_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"type":"A","name":"demo-fm","content":"167.235.143.222","ttl":1,"proxied":false}'
```

Verify: `dig +short demo-fm.brf.sh` → `167.235.143.222`.

## 3. Keycloak realm setup

1. Sign in to https://auth.brf.sh as `brftech`.
2. Realm `brf` → **Clients** → **Create client** → **Import** →
   upload `deploy/keycloak-client-filex.json` from this repo.
3. After save, the Credentials tab shows the generated client secret.
   Copy it.
4. Realm roles → create `filex-admin`. Assign it to your user (`burak`).
   Without this role, OIDC users land as plain `user`s and the admin UI
   refuses to load.
5. (Optional) Realm settings → Login → enable WebAuthn passwordless
   so passkeys keep working on this client.

## 4. Compose deploy

```bash
ssh main
sudo mkdir -p /root/filex /opt/filex/data /opt/filex/storages
sudo chown -R 1000:1000 /opt/filex                 # match the alpine user inside the image

# Pull the artifacts from the repo
cd /tmp
git clone git@gitlab.com:brftech/filemanager.git
cp filemanager/deploy/demo-fm.brf.sh.compose.yml /root/filex/docker-compose.yml
cp filemanager/deploy/.env.example              /root/filex/.env
chmod 600 /root/filex/.env

# Edit /root/filex/.env — paste the Keycloak client secret + OnlyOffice JWT
nano /root/filex/.env

# Pull & start
cd /root/filex
docker compose pull
docker compose up -d
docker compose logs filex                          # admin@local password — copy ONCE
```

The first-run console banner is also written to
`/opt/filex/data/.first-run.txt` (mode 0600, root). Delete that file
once you've safely stored the password.

## 5. Nginx vhost

```bash
sudo cp /tmp/filemanager/deploy/nginx.demo-fm.brf.sh.conf \
        /etc/nginx/sites-available/demo-fm.brf.sh
sudo ln -s /etc/nginx/sites-available/demo-fm.brf.sh \
           /etc/nginx/sites-enabled/demo-fm.brf.sh

# Issue/renew the cert (CloudPanel may already do this on its own)
sudo certbot certonly --nginx -d demo-fm.brf.sh

sudo nginx -t && sudo systemctl reload nginx
```

Open https://demo-fm.brf.sh/admin → login with `admin@local` + the
banner password, OR click **Sign in with Keycloak** if you assigned
yourself the `filex-admin` role.

## 6. Backup

Filex stores everything in `/opt/filex/data`. Backrest's
`all-services` plan should pick this up automatically; verify with:

```bash
backrest snapshots --repo s3-hetzner | grep filex
```

For the SQLite file specifically, the pre-hook should run
`sqlite3 /opt/filex/data/instance.sqlite '.backup /opt/filex/data/instance.bak.sqlite'`
before the snapshot.

## 7. Notify integration

Filex ships a generic webhook channel + in-app bell — see SPEC §4.3 and
plan/04-notify.md.

### 7.1 Webhook (.env bootstrap)

```dotenv
FILEX_WEBHOOK_URL=https://portal.brf.sh/api/notify/v1/ingest
FILEX_WEBHOOK_TOKEN=bn_your_long_token_here
```

The Authorization header is `Bearer ${FILEX_WEBHOOK_TOKEN}`. Empty URL
disables outbound delivery without taking the bell down.

The payload schema is:

```json
{
  "event":   "replica_fail | replica_status_report | quota_full | …",
  "severity": "info | warning | error | critical",
  "title":   "Short headline",
  "body":    "Human-readable detail",
  "meta":    { "path": "...", "op": "write", "...": "..." },
  "ts":      "2026-05-06T13:42:11Z"
}
```

Ingest receivers map this to whatever shape they need (Slack/Discord
templates are not built-in — your /notify ingest does the
transformation).

### 7.2 Runtime override (admin UI)

`/admin/notifications` → "Webhook configuration" lets you change the
URL/token without a restart. Useful for swapping ingest endpoints in
production. Empty token clears the existing one (the token itself is
never echoed back in GET responses).

### 7.3 Bell (in-app)

Top-nav `<NotificationBell>` polls `/api/notifications/unread-count`
every 15s and renders the latest 15 events. Per-user mute matrix lives
in `notification_settings` — `/api/notifications/settings` exposes the
toggle.

### 7.4 Audit fall-back

If you'd rather pipe the backend's plain audit log into a notify group
instead of the live event stream, the legacy hook still works:

```sql
INSERT INTO settings (key, value, updated_at)
VALUES ('audit.notify_group', 'filex', CURRENT_TIMESTAMP);
```

But Round B's webhook is the recommended channel for replica/quota/
queue events — the legacy hook has no severity routing.

## 7b. Persistent queue

`FILEX_QUEUE_DRIVER=sqlite` (default) shares the application DB. For
HA setups switch to `postgres` (SELECT FOR UPDATE SKIP LOCKED) or
`redis` (BRPOPLPUSH work-list).

```dotenv
FILEX_QUEUE_DRIVER=postgres
FILEX_QUEUE_DSN=postgres://filex:${POSTGRES_PASSWORD}@postgres:5432/filex?sslmode=disable
FILEX_QUEUE_WORKERS=4
```

Admin UI: `/admin/queue` for stats + retry/cancel actions. Use
`/admin/replica/fix` to enqueue retries for every unresolved replica
failure in one shot.

## 7c. Replica + reconciliation

Configure a primary + replica storage in `/admin/storages`, then mark
the replica row's `role=replica` and `replica_of_id={primary.id}` (v0.1
sets these via SQL — admin UI auto-pairing is v0.2). Visit
`/admin/replica/settings` to enable cron status reports — pick a preset
(hourly, daily 3 AM, weekly) or paste a raw 5-field cron expression.
The webhook receives the full failed-paths list on each run; the bell
shows a summary line.

## 8. Migrating from `@brftech/file-explorer`

If brf-mono / fishapp consumers haven't been updated yet, see
[MIGRATION_FISHAPP.md](MIGRATION_FISHAPP.md) — that document spells
out the package rename + endpoint substitution.

## 9. Rollback

```bash
cd /root/filex
docker compose down
# Replace the image tag in docker-compose.yml with a previous build
docker compose up -d
```

Filex DB is forward-compatible across patch releases; minor releases
ship migrations and the `migrate` command can run them in either
direction (`docker compose run --rm filex migrate down 1`).

## 10. Healthchecks

| Check | Command |
|-------|---------|
| Container alive | `docker compose ps filex \| grep healthy` |
| HTTP responding | `curl -fsS https://demo-fm.brf.sh/healthz` → `{"status":"ok"}` |
| Capabilities    | `curl -fsS https://demo-fm.brf.sh/api/capabilities \| jq .external` |
| OIDC discovery  | `curl -fsS https://auth.brf.sh/realms/brf/.well-known/openid-configuration \| jq -r .issuer` |
| Audit + sync    | https://demo-fm.brf.sh/admin/dashboard → recent activity card |

Add the first three to `infra-watchdog` so they show up in the
existing notify channel.
