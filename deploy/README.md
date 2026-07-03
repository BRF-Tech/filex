# `deploy/`

Ready-to-use deployment artifacts.

| Path | What it is |
|------|------------|
| [`compose/`](compose/) | Docker Compose stacks — **minimal** (filex + SQLite + local disk) and **full** (filex + PostgreSQL + Redis + OnlyOffice + MinIO + Caddy auto-HTTPS). |
| [`helm/filex/`](helm/filex/) | Helm chart for Kubernetes — a Deployment + PVC + optional Ingress, with optionally bundled PostgreSQL/Redis. |

Start here:

- **Docker Compose** → [`docs/INSTALLATION.md`](../docs/INSTALLATION.md#minimal-docker-compose) and
  [`compose/`](compose/).
- **Kubernetes / Helm** → [`docs/INSTALLATION.md`](../docs/INSTALLATION.md#helm-kubernetes) and
  [`helm/filex/`](helm/filex/).
- **Bare binary / systemd** → [`docs/INSTALLATION.md`](../docs/INSTALLATION.md#binary).

Reverse proxy, HTTPS, scaling and backup guidance lives in
[`docs/DEPLOYMENT.md`](../docs/DEPLOYMENT.md).
