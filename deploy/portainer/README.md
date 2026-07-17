# filex — Portainer App Template

`template.json` follows the Portainer **App Templates v3** format
(`{"version": "3", "templates": [{"id": …}]}`, Portainer 2.19+; matches the
official portainer/templates `v3` branch).

## Install

Portainer → **Settings → App Templates** → set the URL to the raw file:

```
https://raw.githubusercontent.com/BRF-Tech/filex/main/deploy/portainer/template.json
```

Then **App Templates** → *filex* → adjust env → **Deploy the container**.

## Required settings

| Env | Required | Notes |
|---|---|---|
| `FILEX_PUBLIC_URL` | yes | The URL users open (`http://<host>:5212` or your proxy domain) — share links are built from it. |
| `FILEX_ADMIN_EMAIL` / `FILEX_ADMIN_PASSWORD` | no | Empty → random `admin@local` password, printed once in the container logs. |

Volumes `/data` (DB/index/thumbs) and `/srv/files` (managed files) are created
as anonymous Docker volumes by default — edit the mappings at deploy time to
bind host paths. The template uses `:latest`; substitute
`ghcr.io/brf-tech/filex:v0.4.0` to pin.
