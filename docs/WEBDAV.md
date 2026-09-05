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
(mint one under **API / MCP** in the admin UI, or — for any account — from the
file explorer's navigation panel under **Connections → API keys**). Failures return `401` with
`WWW-Authenticate: Basic realm="filex"`.

⚠ The **API keys** entry is missing when the explorer is an embed proxied with
one shared *app* token — those credentials belong to a person, and an app token
is not one ([MCP.md](MCP.md#token-kinds--user-vs-app)). Sign in to filex
directly, or ask an admin to mint the token at `POST /api/admin/ai-tokens`.

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

> 💡 **filex generates this page filled in.** Everything below is also
> available inside the app with your real host, your storage name and your
> own username already substituted, plus a copy button per command:
>
> - web: **Connections → How to connect** (admins), or the plug icon in the
>   file explorer's header (every signed-in user);
> - desktop app: **Settings → Storage connections → How to connect**.
>
> Both surfaces render the same component from `@brftech/filex-core`, so they
> cannot drift apart from each other — but they *can* drift from this file.
> A correction here belongs in `packages/core/src/lib/connectionGuides.ts`
> too, and the other way round.

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

Tips — and Windows has three built-in limits that will look like filex bugs if
you do not know about them. All three live under
`HKLM\SYSTEM\CurrentControlSet\Services\WebClient\Parameters`, and the
**WebClient service must be restarted** after any change
(`net stop webclient && net start webclient`).

- **Transfers stop at ~47.7 MB.** `FileSizeLimitInBytes` defaults to
  **50,000,000 bytes**, not 4 GB — 4 GB (`0xFFFFFFFF`) is the largest value you
  may set, not the default. Raise it with:

  ```bat
  reg add "HKLM\SYSTEM\CurrentControlSet\Services\WebClient\Parameters" /v FileSizeLimitInBytes /t REG_DWORD /d 4294967295 /f
  ```

- **Folders with roughly a thousand files fail to open**, often reported as
  *"Disk is not formatted"* or error 31. `FileAttributesLimitInBytes` defaults
  to 1,000,000 bytes, which is the total size of the properties returned for one
  collection — about 1,000 entries. See
  [Microsoft KB 912152](https://learn.microsoft.com/en-us/troubleshoot/windows-client/networking/cannot-access-webdav-web-folder).

  ```bat
  reg add "HKLM\SYSTEM\CurrentControlSet\Services\WebClient\Parameters" /v FileAttributesLimitInBytes /t REG_DWORD /d 20000000 /f
  ```

- **HTTPS is mandatory.** `BasicAuthLevel` defaults to `1`, meaning "Basic
  authentication over SSL only" — over plain `http://` Windows silently refuses
  to send your credentials and the mount fails with no useful message. Do not
  set it to `2`; use TLS.

- **The mapped drive will not survive a sign-out.** Since Windows 7, Basic
  authentication credentials cannot be persisted by Credential Manager — this is
  by design ([KB 2673544](https://learn.microsoft.com/en-us/troubleshoot/windows-client/networking/cannot-automatically-reconnect-dav-share)),
  and `/persistent:yes` does not change it. Re-run `net use` from a logon script
  if you need the drive back automatically.

- If mounting fails at all, make sure the **WebClient** service is running
  (`sc config WebClient start= auto && net start WebClient`). On Windows Server
  it ships only with the *WebDAV Redirector* feature installed.

- A slow first connection is usually proxy auto-detection: untick **Automatically
  detect settings** in Internet Options → Connections → LAN settings.

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

- **DELETE goes to the trash**, exactly like the web UI. A `DELETE` — of a file
  or of a whole collection — renames the object into the hidden
  `.filex-trash/` bucket and flags the node row; the item then appears in the
  trash listing and can be restored, and it is only destroyed for good when the
  retention window expires or an admin empties the trash. See
  [TRASH-VERSIONING.md](TRASH-VERSIONING.md).
  - A **collection** goes in as **one restorable unit**: restoring the folder
    brings its whole subtree back with it.
  - Trashed bytes **still count against the owner's quota** until they are
    purged — the same rule the web UI follows. Deleting over WebDAV does not
    free space; emptying the trash does.
  - The only way a WebDAV delete destroys data outright is a storage backend
    that supports neither move nor copy, since there is then no way to preserve
    the bytes. None of the shipped drivers (local, S3, SFTP, FTP, WebDAV) are in
    that category. When it does happen the item is *not* placed in the trash, so
    nothing offers a restore that could not work, and the emitted event is
    `file.deleted` rather than `file.trashed`.
- **This changed in the release that added `trash.Put`.** Earlier
  documentation described WebDAV `DELETE` as permanent. Treat a sync client's
  delete as recoverable, but note the flip side: a large
  `rclone sync --delete` run fills the trash rather than freeing space.
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
- Multi-tenant installs: `/dav` is **tenant-scoped**. The scope comes from the
  authenticated user's provider — not the request Host — so it matches what
  the JSON API and the web UI apply. A caller sees only their own provider's
  storages in the root, and any path under another provider's storage answers
  `404` (a foreign storage is indistinguishable from one that isn't there).
  Admins of the **supertenant** provider stay confine-exempt and see
  everything; `role: admin` on a regular tenant means admin *of that tenant*.
  Suspended-tenant users are refused at login.

  Before this, `/dav` resolved storages globally by name, so any tenant admin
  could list, read, write and permanently delete every other tenant's files.
