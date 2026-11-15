# `deploy/` - deployment artifacts

Everything you need to run filex somewhere, grouped by target. The image is
`ghcr.io/brf-tech/filex:vX.Y.Z` (multiarch amd64+arm64; `:latest` tracks the
newest release). Full walkthroughs: [`docs/INSTALLATION.md`](../docs/INSTALLATION.md).

| Directory / file | Target |
|---|---|
| [`compose/`](compose/) | Docker Compose - `docker-compose.minimal.yml` (one container, SQLite) and `docker-compose.full.yml` (Postgres + Redis + Caddy + opt-in draw.io / S3 server (Versity S3 Gateway) / ClamAV, and an optional ONLYOFFICE Document Server behind the `onlyoffice` profile, pinned to a tested version), plus a multi-tenant variant, and `docker-compose.filex-watch.yml` for the machine that owns a storage's disk when that is not the filex machine ([filex-watch](../docs/FILEX-WATCH.md)). Office documents need no container: install the [Office editor app](../docs/ONLYOFFICE.md#the-office-editor-app). |
| [`helm/filex/`](helm/filex/) | Kubernetes Helm chart (PVC + Ingress; optional bundled Postgres/Redis/S3 server, optional wiring to an ONLYOFFICE Document Server you run). Published with every release: `helm repo add brf-tech https://helm.brf.sh`, then `helm install filex brf-tech/filex` (or `oci://ghcr.io/brf-tech/charts/filex`). |
| [`umbrel/filex/`](umbrel/filex/) | Umbrel App Store package (`umbrel-app.yml` + app-proxy compose), proposed in [getumbrel/umbrel-apps#6375](https://github.com/getumbrel/umbrel-apps/pull/6375). |
| [`casaos/`](casaos/) | CasaOS / ZimaOS compose with the `x-casaos` store extension (app id `sh.filex.filex`). |
| [`runtipi/filex/`](runtipi/filex/) | Runtipi app package (`config.json` + dynamic `docker-compose.json`). |
| [`unraid/filex.xml`](unraid/) | Unraid Community Applications template, published from [BRF-Tech/unraid-templates](https://github.com/BRF-Tech/unraid-templates). |
| [`portainer/template.json`](portainer/) | Portainer App Templates (v3) definition. |

Each app-store directory has its own `README.md` with install steps and the
required settings. Common to all of them:

- **`FILEX_PUBLIC_URL`** - the only setting that must match your environment
  (the URL users open; share links + SSO redirects are built from it).
- **First run** - with an empty user table filex creates `admin@local` with a
  random password printed **once** in the logs (and saved to
  `/data/.first-run.txt`), unless `FILEX_ADMIN_EMAIL`/`FILEX_ADMIN_PASSWORD`
  preset it.
- **Volumes** - `/data` (DB, search index, thumbnail cache) and a files folder
  (default `/srv/files`, seeded as the first storage when
  `FILEX_DEFAULT_STORAGE_DRIVER=local`). Since 0.61 that storage uses live
  watching: a file written into the folder without filex shows up when its
  folder is opened, not at the next scan
  ([STORAGE.md](../docs/STORAGE.md#which-mode-a-new-storage-gets)).

## Legacy files (example.com demo deploy)

`demo-fm.example.com.compose.yml`, `Caddyfile.demo-fm.example.com`,
`nginx.demo-fm.example.com.conf`, `keycloak-client-filex.json` and `.env.example`
are the original demo-fm.example.com single-instance deploy artifacts - kept for
that environment, not templates for new installs.
