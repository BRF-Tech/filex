# filex - CasaOS package

Compose file with the `x-casaos` store extension, modelled on the official
[CasaOS AppStore](https://github.com/IceWhaleTech/CasaOS-AppStore) entries.

## Install

Easiest path (no store needed): CasaOS UI → **App Store** → **⋮** →
**Install a customized app** → paste `docker-compose.yml`.

For a store submission, copy this directory to `Apps/Filex/` in a CasaOS
app-store repo (the icon URL already points at the filex GitHub repo, the screenshot URLs at
filex.sh, where the published screenshots live).

## Required / notable settings

| Setting | Why |
|---|---|
| `FILEX_PUBLIC_URL` | Set to `http://<NAS-IP>:5212` (or your domain) - share links + SSO redirects are built from it. Only truly required change. A path in it (`https://example.com/filex`) is the sub-path filex is served under, and the proxy must pass that path through ([Base path](../../docs/CONFIGURATION.md#base-path)). |
| `/DATA/AppData/filex/data` → `/data` | DB, search index, thumbnails, first-run secret. |
| `/DATA/AppData/filex/files` → `/srv/files` | Default storage seeded on first boot. |
| `/DATA` → `/media/DATA` | Optional: add as an extra "local" storage in the admin UI to manage all NAS files. |

First login: `admin@local` + the one-time password from the container logs
(`docker logs filex`), unless `FILEX_ADMIN_EMAIL`/`FILEX_ADMIN_PASSWORD` are set.
