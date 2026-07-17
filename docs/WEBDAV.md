# WebDAV server

filex serves every configured storage over **WebDAV** at:

```
https://<your-filex>/dav/<storage-name>/<path>
```

Mount your filex drives in Windows Explorer, macOS Finder, or any WebDAV
client (rclone, Cyberduck, WinSCP, davfs2, Kodi, Documents by Readdle, …) —
uploads, downloads, rename/move, delete and folder creation all work, and
every change is mirrored into the filex index (listings, search, thumbnails)
just like an upload through the web UI.

- [Enable / disable](#enable--disable)
- [Authentication](#authentication)
- [Connecting](#connecting)
  - [Windows (map network drive)](#windows-map-network-drive)
  - [macOS (Finder)](#macos-finder)
  - [rclone](#rclone)
  - [Linux (davfs2 / GNOME / KDE)](#linux-davfs2--gnome--kde)
- [Permissions](#permissions)
- [Limits & behavior notes](#limits--behavior-notes)

---

## Enable / disable

The WebDAV surface is **on by default**. To turn it off entirely set:

```
FILEX_DAV=0
```

(or `dav.enabled: false` in `config.yaml`). The whole `/dav` subtree then
answers 404. Class-2 locking (`LOCK`/`UNLOCK`, in-memory) is always on when
the server is enabled — Windows refuses to mount a read-write drive without
it.

## Authentication

Every request needs **HTTP Basic** credentials:

| Field | Value |
|-------|-------|
| Username | your filex account **e-mail** |
| Password | your **account password**, *or* a filex **API token** |

Both secrets are accepted in the same password field — filex first tries the
account password, then falls back to interpreting the value as an API token
(mint one under **API / MCP** in the admin UI, or **Settings → API tokens**
for non-admin accounts). Failures return `401` with
`WWW-Authenticate: Basic realm="filex"`.

Notes:

- **Use HTTPS.** Basic auth sends the secret with every request; only expose
  `/dav` behind TLS (Windows additionally refuses Basic over plain HTTP by
  default).
- **Accounts with TOTP/2FA enabled cannot use their password here** (Basic
  auth has no second-factor slot). Mint an API token and use that instead —
  this is also the recommended setup for any always-on mount.
- API tokens are honored with their **verb scopes**: `read` covers browsing
  and downloads, `write` covers uploads/mkdir/move/copy/locks, `delete`
  covers deletes. A token with no scopes grants everything its user may do.
- Tokens carrying a **`root:` confinement scope are rejected** on `/dav` —
  the WebDAV tree has no confinement middleware, so accepting a
  subtree-limited token would widen its reach. Use an unconfined token (or
  RBAC grants) for WebDAV.

## Connecting

### Windows (map network drive)

1. Open **File Explorer** → right-click **This PC** → **Map network drive…**
2. Pick a drive letter, and as the folder enter:
   `https://fm.example.com/dav/` (or a single storage:
   `https://fm.example.com/dav/depo/`)
3. Check **Connect using different credentials**, then sign in with your
   e-mail + password/token as above.

Command-line equivalent:

```bat
net use Z: "https://fm.example.com/dav/" /user:you@example.com <password-or-token> /persistent:yes
```

Tips:

- Windows' WebDAV redirector caps file transfers at ~4 GB by default
  (`HKLM\SYSTEM\CurrentControlSet\Services\WebClient\Parameters\FileSizeLimitInBytes`).
- If mounting fails, make sure the **WebClient** service is running.

### macOS (Finder)

1. Finder → **Go → Connect to Server…** (⌘K)
2. Enter `https://fm.example.com/dav/` and connect.
3. Authenticate with your e-mail + password/token.

The drive appears under **Locations**; each storage is a top-level folder.

### rclone

```ini
# ~/.config/rclone/rclone.conf
[filex]
type = webdav
url = https://fm.example.com/dav
vendor = other
user = you@example.com
pass = <output of: rclone obscure "your-password-or-token">
```

```bash
rclone lsd filex:                # list storages
rclone lsl filex:depo            # list one storage
rclone copy ./local filex:depo/backup   # upload a tree
rclone mount filex: /mnt/filex   # FUSE mount (Linux/macOS)
```

### Linux (davfs2 / GNOME / KDE)

```bash
sudo mount -t davfs https://fm.example.com/dav/ /mnt/filex
# or in GNOME Files / Dolphin: davs://fm.example.com/dav/
```

## Permissions

WebDAV enforces exactly the same authorization model as the web UI:

- The `/dav/` root lists **only the storages you may see**. On an
  RBAC-enabled storage that means: at least one grant.
- On RBAC storages, per-folder/file **grants** apply: `viewer` can browse and
  download, `editor`/`owner` can also write. Paths outside your grants
  answer **404** (not 403), so the tree never leaks what exists.
- **Read-only storages** and grant levels below editor make every mutation
  (`PUT`, `DELETE`, `MKCOL`, `MOVE`, `COPY`, `LOCK`) return **403**.
- Admin accounts see and write everything (subject only to the storage
  read-only flag).

## Limits & behavior notes

- **DELETE is permanent.** Unlike the web UI (which soft-deletes into the
  filex trash), a WebDAV delete removes the object from the backing storage
  directly. Empty the client-side confirmation with care.
- **Cross-storage MOVE is not supported** (drivers can't rename across
  backends) — the server answers `502`; do COPY + DELETE instead. COPY
  across storages works (it streams through the server).
- Uploads are **spooled server-side** and written to the backing driver as a
  whole object on close — very large files need matching temp-dir space on
  the filex host.
- Locks are **in-memory**: they don't survive a server restart and are not
  shared between replicas. They exist to satisfy class-2 clients (Windows,
  Office); filex itself does not arbitrate concurrent edits beyond them.
- The filex-internal buckets (`.filex-trash`, `.versions`, `.thumbs`) are
  hidden and unreachable over WebDAV.
- Changes made over WebDAV are indexed **best-effort right away** (node
  cache, search, thumbnails); if anything hiccups, the storage's scheduled
  sync run reconciles later.
- Multi-tenant installs: `/dav` currently resolves storages **globally by
  name** (host-based tenant scoping does not apply to this surface yet).
  Suspended-tenant users are refused at login.
