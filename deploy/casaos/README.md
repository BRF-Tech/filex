# filex - CasaOS / ZimaOS package

`docker-compose.yml` is the filex app of the
[ZimaOS / CasaOS AppStore](https://github.com/IceWhaleTech/CasaOS-AppStore)
(`Apps/filex/` there, app id `sh.filex.filex`). This file is the source: the
store's copy is this file.

## Install

Without the store: CasaOS / ZimaOS → **App Store** → **⋮** →
**Install a customized app** → paste `docker-compose.yml`. Its icon, thumbnail
and screenshot links point at the store's copies under
`cdn.jsdelivr.net/gh/IceWhaleTech/CasaOS-AppStore@main/Apps/filex/`.

## Settings

| Setting | Notes |
|---|---|
| `FILEX_PUBLIC_URL` | Set it before installing to the address you open filex at, for example `http://192.168.1.10:5212` (the device's IP or your domain). Share links and sign-in redirects are built from it; the default, `http://localhost:5212`, only works on the device itself. A path in it (`https://example.com/filex`) is the sub-path filex is served under, and the proxy must pass that path through ([Base path](../../docs/CONFIGURATION.md#base-path)). |
| `FILEX_ADMIN_EMAIL` / `FILEX_ADMIN_PASSWORD` | Optional, empty by default. Empty: the administrator `admin@local` is created with a random password. |
| `FILEX_SECRET_KEY` | Optional, empty by default. 32 or more random characters; needed for S3 access keys, push notifications and sign-in provider secrets saved in the admin panel ([The secret key](../../docs/CONFIGURATION.md#the-secret-key)). Back it up and keep it unchanged once set. |
| `/DATA/AppData/$AppID/data` → `/data` | Database (SQLite), search index, thumbnail cache, first-run password file. |
| `/DATA/AppData/$AppID/files` → `/srv/files` | The first storage, created on the first start (`FILEX_DEFAULT_STORAGE_DRIVER` / `FILEX_DEFAULT_STORAGE_PATH`). |
| `/DATA` → `/media/DATA` | Add it as another **local** storage in the admin panel to manage the files on the device. filex does not touch it until then. |

## First sign-in

The app opens at `/admin`. On the first start filex prints the password of
`admin@local` once in the app log and saves it to
`/DATA/AppData/filex/data/.first-run.txt`. Accounts you create for other
people sign in at `/drive`.

## Updating the store listing

- A release stamps the image tag and `x-casaos.version` here
  (`node scripts/sync-deploy-versions.mjs`). Set `update_at` and
  `release_notes` by hand, then copy this file to `Apps/filex/docker-compose.yml`
  in a pull request to the store.
- Before the pull request, run the store's checks on its checkout:
  `docker compose -f Apps/filex/docker-compose.yml config -q`, its
  `validate_compose.py`, and `./scripts/build_dist.sh`.
- The pictures live in the store's `Apps/filex/`: `icon.svg` is the mark of
  `packages/core/src/components/LogoMark.vue` with `var(--fe-primary, #2f6ceb)`
  written as `#2f6ceb`, `icon.png` the same at 256 px (`rsvg-convert`),
  `thumbnail.png` the filex.sh social card at 1200×630, and `screenshot-1.png` /
  `screenshot-2.png` the published `driveshell/driveshell-hero-1440` and
  `explorer-grid-light` shots at 1280 px wide.
