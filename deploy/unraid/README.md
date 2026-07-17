# filex — Unraid Community Applications template

`filex.xml` follows the Unraid CA `<Container version="2">` template schema
(modelled on the selfhosters.net template repository).

## Install

- **Without a template repo**: Unraid → Docker → **Add Container** →
  Template dropdown stays empty; instead save `filex.xml` to
  `/boot/config/plugins/dockerMan/templates-user/` on the flash drive, then
  pick `filex` from the Template dropdown.
- **Via CA**: submit `filex.xml` to a CA-indexed template repository (e.g.
  a `docker-templates` GitHub repo registered with Community Applications).

## Required settings

| Field | Required | Notes |
|---|---|---|
| Files (`/srv/files`) | yes | The share to manage, e.g. `/mnt/user/files` — seeded as the default storage. |
| Public URL (`FILEX_PUBLIC_URL`) | yes | `http://<server-ip>:5212` or your reverse-proxy URL — share links are built from it. |
| Admin Email / Password | no | Empty → random `admin@local` password printed once in the container log. |

Data (DB, search index, thumbnails) lives in `/mnt/user/appdata/filex`.
The template pins `:latest` so CA's update checks work; pin
`ghcr.io/brf-tech/filex:v0.4.0` in the Repository field if you prefer fixed
versions.
