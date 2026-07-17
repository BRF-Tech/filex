# filex — Runtipi app package

Runtipi dynamic-compose package (`schemaVersion: 2`,
`https://schemas.runtipi.io/v2/dynamic-compose.json`), modelled on the official
[runtipi-appstore](https://github.com/runtipi/runtipi-appstore) apps.

## Install

Add this directory as `apps/filex/` in a Runtipi app store repo (your own
custom store works: Settings → App Stores → add repo), then install from the
Runtipi UI.

Missing store asset: `metadata/logo.jpg` (Runtipi requires a logo per app —
not included here; export one from `docs/logo.png`).

## What's required

- **No mandatory env.** The install form asks for an optional admin e-mail +
  password (`FILEX_ADMIN_EMAIL` / `FILEX_ADMIN_PASSWORD`); leave empty for a
  random `admin@local` password printed once in the app logs.
- `FILEX_PUBLIC_URL` is derived automatically from Runtipi
  (`${APP_PROTOCOL:-http}://${APP_DOMAIN}`) — correct for both LAN and
  "expose app" modes.
- Data: `${APP_DATA_DIR}/data/filex` → `/data` (DB/index/thumbs) and
  `${APP_DATA_DIR}/data/files` → `/srv/files` (default storage, seeded on
  first boot).
