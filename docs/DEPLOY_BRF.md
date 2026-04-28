# filex deployment — `files.brf.sh`

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
nginx -t && systemctl reload nginx   # bring https://files.brf.sh online
```

## 1. Prerequisites

- DNS: A record `files.brf.sh → 167.235.143.222` (`proxied: false` in
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
  -d '{"type":"A","name":"files","content":"167.235.143.222","ttl":1,"proxied":false}'
```

Verify: `dig +short files.brf.sh` → `167.235.143.222`.

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
cp filemanager/deploy/files.brf.sh.compose.yml /root/filex/docker-compose.yml
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
sudo cp /tmp/filemanager/deploy/nginx.files.brf.sh.conf \
        /etc/nginx/sites-available/files.brf.sh
sudo ln -s /etc/nginx/sites-available/files.brf.sh \
           /etc/nginx/sites-enabled/files.brf.sh

# Issue/renew the cert (CloudPanel may already do this on its own)
sudo certbot certonly --nginx -d files.brf.sh

sudo nginx -t && sudo systemctl reload nginx
```

Open https://files.brf.sh/admin → login with `admin@local` + the
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

Add a notify group `filex` for capability/sync alerts and pipe the
backend's audit log into it. Quick recipe:

```sql
INSERT INTO settings (key, value, updated_at)
VALUES ('audit.notify_group', 'filex', CURRENT_TIMESTAMP);
```

Optional — `notify` HTTP webhook URL goes into the `external_services`
table once you've created the receiver.

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
| HTTP responding | `curl -fsS https://files.brf.sh/healthz` → `{"status":"ok"}` |
| Capabilities    | `curl -fsS https://files.brf.sh/api/capabilities \| jq .external` |
| OIDC discovery  | `curl -fsS https://auth.brf.sh/realms/brf/.well-known/openid-configuration \| jq -r .issuer` |
| Audit + sync    | https://files.brf.sh/admin/dashboard → recent activity card |

Add the first three to `infra-watchdog` so they show up in the
existing notify channel.
