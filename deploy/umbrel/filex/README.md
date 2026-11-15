# filex - Umbrel app package

The source of the filex package for the
[Umbrel App Store](https://github.com/getumbrel/umbrel-apps), proposed in
[getumbrel/umbrel-apps#6375](https://github.com/getumbrel/umbrel-apps/pull/6375).
It follows that repository's current packaging rules: `manifestVersion: 1`,
`storage: dataRoot: data`, and an `app_proxy` entry in the compose file.

## How the store copy differs

The files here are the store package with two exceptions, both because
`scripts/sync-deploy-versions.mjs` updates them at every release:

- the image is pinned by tag only; the store copy adds that tag's multi-arch
  digest (`ghcr.io/brf-tech/filex:vX.Y.Z@sha256:...`, from
  `docker buildx imagetools inspect`);
- `releaseNotes` is derived from `CHANGELOG.md`; a first submission sends it
  empty (`releaseNotes: ""`) and `gallery: []`, and Umbrel adds the gallery
  pictures itself.

## Test on umbrelOS

```bash
# On the umbrelOS machine, with this directory copied over (without the
# .gitkeep files) and the digest added to the image line:
STORE=/home/umbrel/umbrel/app-stores/getumbrel-umbrel-apps-github-53f74447
sudo cp -r filex "$STORE/filex"
sudo umbreld client apps.install.mutate --appId filex
sudo umbreld client apps.state.query --appId filex
```

Check the package with the store's own linter first, in a checkout of
getumbrel/umbrel-apps: `npm run lint:apps -- filex --check-images`.

## What it sets up

- **Sign-in:** `admin@local` (or `admin`) with the password Umbrel shows for
  the app (`deterministicPassword: true`, `FILEX_ADMIN_PASSWORD=${APP_PASSWORD}`,
  read on the first start only).
- **Data:** `${APP_DATA_DIR}/data/filex` is `/data` (database, search index,
  thumbnails); `${APP_DATA_DIR}/data/files` is `/srv/files`, seeded as the
  Files storage with live watching, so a file put into the folder from outside
  filex shows up when its folder is opened. The **Files** folder setting
  (`folderAccess`) can point it at Home, a drive or a network share instead; on
  a network share filex reads every opened folder from the disk instead of
  watching it ([Network filesystems](https://docs.filex.sh/LAZY-CATALOGUE#network-filesystems-no-watch-every-open-from-disk)).
- **Secret key:** `FILEX_SECRET_KEY` comes from `derive_entropy` in
  `exports.sh`, so it stays the same across restarts and updates.
- **User:** the container runs filex as 1000:1000 through `PUID`/`PGID`.
- **Address:** `FILEX_PUBLIC_URL` is `http://${DEVICE_DOMAIN_NAME}:${APP_PROXY_PORT}`
  and can be changed in the app's settings.
- **Proxy:** `FILEX_TRUSTED_PROXIES` is `auto, ${NETWORK_IP}/16`. umbrelOS 2.0
  forwards requests from the Umbrel network's gateway, and without it every
  visitor would count as that one address in the sign-in limits and the logs.
- **Umbrel login:** stays on. `PROXY_AUTH_WHITELIST` opens only share links
  (`/s/*`), upload links (`/d/*`), the API (`/api/*`, for the desktop app and
  `filex mount`), WebDAV (`/dav`) and S3 (`/s3`); filex checks its own sign-in,
  token or link on each of them.
