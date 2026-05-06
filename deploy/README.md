# `deploy/` — production artifacts

Drop-in files for the Hetzner main box. See
[`docs/DEPLOY_BRF.md`](../docs/DEPLOY_BRF.md) for the full runbook;
this directory just collects the actual configuration that gets
copied onto the server.

| File | Destination on `main` |
|------|----------------------|
| `demo-fm.brf.sh.compose.yml` | `/root/filex/docker-compose.yml` |
| `.env.example`             | `/root/filex/.env` (after editing) |
| `nginx.demo-fm.brf.sh.conf`  | `/etc/nginx/sites-available/demo-fm.brf.sh` |
| `keycloak-client-filex.json` | imported via Keycloak admin UI |

For brkip DR (mirror), the same compose works as long as
`FILEX_PUBLIC_URL` is changed to `https://files.dr.brf.sh` and the
nginx vhost is replaced with the brkip Caddy snippet (Caddy is the
reverse proxy on the DR box).
