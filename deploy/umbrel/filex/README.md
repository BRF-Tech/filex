# filex — Umbrel app package

Package for the [Umbrel community app store](https://github.com/getumbrel/umbrel-apps)
(schema: `manifestVersion: 1` + the `app_proxy` compose pattern, modelled on the
official `file-browser` app).

## Install (community app store / local test)

```bash
# On the Umbrel host — install straight from this directory for testing:
sudo cp -r filex ~/umbrel/app-stores/getumbrel-umbrel-apps-github-53f74447/filex
umbreld client apps.install.mutate --appId filex
```

For a proper store submission, open a PR that adds this `filex/` directory to
[getumbrel/umbrel-apps](https://github.com/getumbrel/umbrel-apps) and add the
gallery images (`1.jpg`, `2.jpg`, `3.jpg` — 1600×1000, referenced in
`umbrel-app.yml`; not included here).

## What's required

- No mandatory env: Umbrel supplies `APP_DATA_DIR`, `APP_PASSWORD`,
  `DEVICE_DOMAIN_NAME`, `UMBREL_ROOT`.
- Login: **admin@local** + the password Umbrel shows in the app's
  "Show password" dialog (`deterministicPassword: true` →
  `FILEX_ADMIN_PASSWORD=${APP_PASSWORD}`).
- Data: `${APP_DATA_DIR}/data` (DB/index/thumbs), `${APP_DATA_DIR}/files`
  (default storage). Umbrel's shared `data/storage` folder is mounted at
  `/srv/umbrel` — add it as an extra local storage in the admin UI if wanted.
