# `deploy/` — deployment artifacts

Everything you need to run filex somewhere, grouped by target. The image is
`ghcr.io/brf-tech/filex:vX.Y.Z` (multiarch amd64+arm64; `:latest` tracks the
newest release). Full walkthroughs: [`docs/INSTALLATION.md`](../docs/INSTALLATION.md).

| Directory / file | Target |
|---|---|
| [`compose/`](compose/) | Docker Compose — `docker-compose.minimal.yml` (one container, SQLite) and `docker-compose.full.yml` (Postgres + Redis + Caddy + opt-in OnlyOffice/Drawio/Convert/MinIO), plus a multi-tenant variant. |
| [`helm/filex/`](helm/filex/) | Kubernetes Helm chart (PVC + Ingress; optional bundled Postgres/Redis/MinIO, OnlyOffice wiring). |
| [`umbrel/filex/`](umbrel/filex/) | Umbrel app package (`umbrel-app.yml` + app-proxy compose). |
| [`casaos/`](casaos/) | CasaOS compose with the `x-casaos` store extension. |
| [`runtipi/filex/`](runtipi/filex/) | Runtipi app package (`config.json` + dynamic `docker-compose.json`). |
| [`unraid/filex.xml`](unraid/) | Unraid Community Applications template. |
| [`portainer/template.json`](portainer/) | Portainer App Templates (v3) definition. |

Each app-store directory has its own `README.md` with install steps and the
required settings. Common to all of them:

- **`FILEX_PUBLIC_URL`** — the only setting that must match your environment
  (the URL users open; share links + SSO redirects are built from it).
- **First run** — with an empty user table filex creates `admin@local` with a
  random password printed **once** in the logs (and saved to
  `/data/.first-run.txt`), unless `FILEX_ADMIN_EMAIL`/`FILEX_ADMIN_PASSWORD`
  preset it.
- **Volumes** — `/data` (DB, search index, thumbnail cache) and a files folder
  (default `/srv/files`, seeded as the first storage when
  `FILEX_DEFAULT_STORAGE_DRIVER=local`).

## Legacy files (brf.sh demo deploy)

`demo-fm.brf.sh.compose.yml`, `Caddyfile.demo-fm.brf.sh`,
`nginx.demo-fm.brf.sh.conf`, `keycloak-client-filex.json` and `.env.example`
are the original demo-fm.brf.sh single-instance deploy artifacts — kept for
that environment, not templates for new installs.
