# filex - Unraid Community Applications template

`filex.xml` is the filex template of Unraid Community Applications. CA reads
it from [BRF-Tech/unraid-templates](https://github.com/BRF-Tech/unraid-templates)
(`templates/filex.xml` there, listed under *BRF Tech's Repository*). This file
is the source: a change here is copied there.

## Install

- **Community Applications**: open **Apps** on the Unraid server and search
  for `filex`.
- **By hand**: save `filex.xml` to `/boot/config/plugins/dockerMan/templates-user/`
  on the flash drive, then open **Docker** → **Add Container** and pick
  `filex` from the **Template** list.

## Settings

| Field | Required | Notes |
|---|---|---|
| Files (`/srv/files`) | yes | The share filex manages, for example `/mnt/user/files`. It becomes the first storage on the first start, with live watching. `/mnt/user` is Unraid's `shfs`, a FUSE filesystem a watch cannot be trusted on (a write through `/mnt/diskN`, the cache or the mover bypasses it), so filex reads every folder from the disk when it is opened: a file copied onto the share shows up when you open its folder ([Network filesystems](../../docs/LAZY-CATALOGUE.md#network-filesystems-no-watch-every-open-from-disk)). |
| Public URL (`FILEX_PUBLIC_URL`) | yes | The address you open filex at, for example `http://192.168.1.10:5212` or your reverse-proxy URL. Share links and sign-in redirects are built from it. A path in it (`https://example.com/filex`) is the sub-path filex is served under, and the proxy must pass that path through ([Base path](../../docs/CONFIGURATION.md#base-path)). |
| Data (`/data`) | yes | `/mnt/user/appdata/filex`: database (SQLite), search index, thumbnails. |
| Admin Email / Admin Password | no | Empty: the administrator `admin@local` is created with a random password, printed once in the container log and saved to `/mnt/user/appdata/filex/.first-run.txt`. |
| Secret Key (`FILEX_SECRET_KEY`) | no | 32 or more random characters. Needed for S3 access keys, push notifications and sign-in provider secrets saved in the admin panel ([The secret key](../../docs/CONFIGURATION.md#the-secret-key)). Back it up and keep it unchanged once set. |
| PUID / PGID (advanced) | no | `99` / `100` (nobody / users), so files filex writes to your shares get the usual Unraid owner. The container takes ownership of the Data folder once. Empty: runs as root. |

The administrator signs in at `http://<server>:5212/admin`; accounts you
create for other people sign in at `/drive`.

The template uses the `latest` tag, which follows the newest release, so
Unraid shows an update when one is published. To stay on one version, put a
release tag in **Repository**, `ghcr.io/brf-tech/filex:vX.Y.Z`
(a tag from [Releases](https://github.com/BRF-Tech/filex/releases)).
