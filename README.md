<div align="center">

<img src="docs/logo.png" alt="filex logo" width="96">

# filex — self-hosted file manager that embeds anywhere

[![Release](https://img.shields.io/github/v/release/BRF-Tech/filex?color=2f6ceb)](https://github.com/BRF-Tech/filex/releases)
[![CI](https://img.shields.io/github/actions/workflow/status/BRF-Tech/filex/ci.yml?branch=main&label=ci)](https://github.com/BRF-Tech/filex/actions)
[![License: MIT](https://img.shields.io/github/license/BRF-Tech/filex?color=22c55e)](LICENSE)
[![Container](https://img.shields.io/badge/ghcr.io-brf--tech%2Ffilex-2496ed?logo=docker&logoColor=white)](https://github.com/BRF-Tech/filex/pkgs/container/filex)
[![Live demo](https://img.shields.io/badge/live_demo-demo.filex.sh-f59e0b)](https://demo.filex.sh)

A single Go binary with a full-featured web UI, pluggable storage/auth/DB drivers,
**real-time collaboration**, **an embeddable web component**, a **desktop app whose
folder sync is live** — an edit on either side arrives in about a second — a
**built-in MCP server** so AI agents can drive it natively, and **apps**: sandboxed
WebAssembly plugins that teach it new things to do with files, starting with
**signing documents** with people inside and outside your organisation. A
**language pack** is an app too, so filex can be translated without waiting for a
release — and it lays itself out **right to left** for the languages that read
that way.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/v0.43.0/explorer-grid-dark.png">
  <img src="docs/screenshots/v0.43.0/explorer-grid-light.png" alt="filex explorer — thumbnail grid" width="900">
</picture>

</div>

## Try it now

**Live demo:** [demo.filex.sh](https://demo.filex.sh) — sign in with `demo@demo.com` / `demo`
(admin role, sandbox resets nightly). Or run your own:

```bash
docker run -p 5212:5212 \
  -e FILEX_DEFAULT_STORAGE_DRIVER=local -e FILEX_DEFAULT_STORAGE_PATH=/srv/files \
  -v filex-data:/data -v "$PWD:/srv/files" \
  ghcr.io/brf-tech/filex:latest
```

That serves **the folder you ran it in** — open the UI and your files are already
there. `/data` is filex's own directory (SQLite database, search index, thumbnail cache),
which is why it is a named volume and not the folder you drop files into; the two are
separate on purpose. Point `$PWD` somewhere else, or add more storages from the admin
panel later — a bucket with several top-level folders can be mounted as one storage
per folder in one go (*Storages → Add → Mount several folders at once*).

The container runs as **root** by default, so what it writes into `/data` is root-owned;
set `PUID`/`PGID` to run it as yourself
([docs/DOCKER.md](docs/DOCKER.md#which-user-the-container-runs-as)).

Open http://localhost:5212/admin — the first run prints admin credentials and embed
instructions to the console. That URL is the operator's; the people you give accounts to
get **http://localhost:5212/drive**, the same file manager without the panel around it.

Prefer a window over a browser tab? The **desktop app** (Windows / Linux / macOS) signs in
to any filex server and syncs folders in the background — and on every platform there is a
copy that runs **without being installed** (a portable `.exe`, an AppImage, a `.zip`):
[latest release](https://github.com/BRF-Tech/filex/releases/latest) ·
[docs/DESKTOP.md](docs/DESKTOP.md).

## Why filex

Most self-hosted file managers are either **too small** (a directory listing with uploads)
or **too big** (a groupware suite you deploy for the file tab). filex aims at the gap:

- **A browser client for your users, not just for you** — hand someone a `user` or
  `viewer` account and `…/drive` and they get the file manager itself: their storages,
  uploads, sharing, search, the editor. No admin panel to walk through, no separate
  frontend to deploy. `…/admin` is the operator's door to the same app.
- **Navigation people already know** — a left panel with a prominent **+ New** menu and
  **Home · My files · Shared with me · My shares · Recent · Starred · Trash**, plus the
  storages you can reach; a storage someone shared with you simply appears there, one click, no mount
  instructions. Anyone can collapse it to an icon rail from the top bar. **Home** is a
  view *in* the app, not a page beside it — your drives, what you opened last and what
  you starred, under the same sidebar and the same header as the files. This is the
  shell everybody gets: a single search field across the header with its ⌘K palette
  hint, a Type / People / Modified / Size filter row, Folders and Files as labelled
  sections, Details and Activity in the info panel, and a storage line. For people who
  want a file drive rather than a file manager, `uiProfile: 'simple'` presets the rest
  of the chrome off — one pane, one folder, list or grid. One explorer in every case:
  there is no second UI to keep in step.
- **Embeds anywhere** — the same UI ships as a Vue 3 component, a React component and a
  framework-agnostic `<filex-explorer>` web component. Put a real file manager inside
  *your* product, backed by your own filex server and locked to a per-tenant folder.
  The navigation panel comes with it — `<filex-explorer sidenav ui-profile="simple">`
  is the whole opt-in for a host page that never touches JavaScript.
- **AI-agent-native** — a REST surface (`/api/ai`) bounded by an API key's permissions, plus a native
  **MCP server** (`/api/ai/mcp`). Hand an agent a token confined to one folder and it can
  list, read, write, share and zip — nothing else.
- **Apps that can only do what you approved** — signing a contract with a partner who
  has no account, converting a video, anything a manifest describes, added as an
  **app**: a WebAssembly module that runs inside filex with exactly the permissions
  you read and granted at install — no filesystem, no network, no program on your
  server. Two ship as public repositories, **e-Signature** and **Convert**; install
  one from its GitHub address ([Apps](#apps)).
- **In your language, and in your direction** — English and Turkish ship in the
  binary, and anything else is a **language pack**: an app with nothing that runs,
  installed from a repository like any other, which translates the explorer, the
  admin panel, the public pages a stranger opens *and the text filex's server
  writes* — mail, notifications, the no-JavaScript pages behind a link. A pack says
  how much of this version it covers, and whatever it lacks shows in English; plural
  forms follow CLDR, so a language gets the forms it actually has. For Arabic,
  Hebrew, Persian and Urdu the interface **turns right to left** — and stops where
  mirroring would be wrong, in document space and in machine text
  ([docs/RTL.md](docs/RTL.md), [write a pack](docs/PLUGIN-KIT.md#writing-a-language-pack)).
- **It wears your brand, not ours** — compose a theme in your own colours on the
  **Appearance** screen and make it the default: the sign-in page and every public
  link wear it too, and a signature request from your instance carries your name,
  not filex's.
- **Real-time** — presence avatars (a profile picture set once on the account, shown for
  every client signed in as you) and live file updates over WebSocket, in the native UI
  *and* in embedded contexts (short-lived ticket auth, API-polling fallback). A batch job
  is coalesced on the way out, so extracting a five-thousand-file archive costs an open
  explorer a bounded trickle of frames rather than five thousand
  ([docs/REALTIME.md](docs/REALTIME.md)).
- **On your desktop too** — the same explorer ships as a Windows/Linux/macOS app that keeps
  local folders in step with the server from the tray — **live**, in about a second,
  in both directions — updates itself, and holds several
  accounts (or tenants) side by side. Right-click a folder → **Keep on this computer** and
  it mirrors under one filex folder; everything else stays online-only in the window.
  Headless machines get the same engine as `filex sync` / `filex client`.
- **Speaks the protocols both ways** — filex can *connect to* local disks, S3, FTP, SFTP,
  WebDAV and SMB/NAS shares, and it can *be reached as* **S3**, **SFTP**, **FTPS**,
  **NFSv3** and **WebDAV**. Point `rclone`, `restic`, `aws s3`, WinSCP, FileZilla, a
  scanner that only learned FTP or a media player that only learned NFS at filex, and
  they land in the same tree, with the same permissions, the same trash and the same
  quota as the web UI. Off-LAN there is also **`filex mount`**, which attaches a remote
  server over ordinary HTTPS — a folder on Linux, a drive letter on Windows
  ([docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **Multi-tenant by design** — storage-per-tenant with native tenancy mode, RBAC roles +
  per-item grants, confined API tokens, per-token identities for audit trails, and
  app-vs-user token kinds so a shared embed credential cannot manage anybody's keys.
  A token names the permissions it holds — an empty list is refused rather than read
  as "everything" — and **no credential it issues is ever wider than itself**: an API
  key, an S3 key, an NFS export or an SSH key minted through a narrow token cannot
  exceed its verbs, leave its folder or outlive its expiry (`403 token_ceiling`).
  The tenant boundary is enforced on every route that names a row, not only on the
  ones that list them, and instance-wide settings are reserved to the supertenant.
- **Boringly deployable** — one binary or one container; SQLite by default, Postgres/MySQL
  when you want them; every driver switched by env vars. All three engines are
  migrated, compared against each other and written to by CI on every change,
  because "supported" used to mean "compiles" ([docs/DATABASES.md](docs/DATABASES.md)).

```
┌─────────────────────────────────────────────────────────────┐
│  filex (Go binary; 43 MB slim / 511 MB w/ thumbnails)       │
├─────────────────────────────────────────────────────────────┤
│  HTTP API (chi)  │  Admin UI (Vue 3, embedded)              │
│  Auth Drivers:   │  local · oidc · ldap · proxy-header      │
│  Storage Drivers:│  local · s3 · ftp · sftp · webdav · smb  │
│  Served as:      │  s3 · sftp · ftps · nfs · webdav         │
│  DB Drivers:     │  sqlite (default) · mysql · postgres     │
│  Queue Drivers:  │  follows the DB · redis                  │
│  Realtime:       │  WebSocket presence + live updates       │
│  RBAC:           │  roles + per-item grants + share invites │
│  AI / MCP:       │  /api/ai REST + native MCP server        │
│  Sync Worker:    │  etag / size+mtime diff + tombstone      │
│  Replica Layer:  │  primary→replica + rules + reconcile     │
│  Protection:     │  trash + versions + ClamAV (bin/clamd)   │
│  E2E folders:    │  client-side WebCrypto (server blind)    │
│  Notifications:  │  webhook + in-app bell + read/unread     │
│  Search:         │  Bleve (full-text, embedded)             │
│  Thumbnails:     │  image · video · pdf · office            │
│  Plug & Play:    │  OnlyOffice · Drawio · Mermaid           │
│  Apps (wasm):    │  sandboxed · e-Signature · Convert       │
│  Languages:      │  en · tr + language packs · RTL layout   │
│  Appearance:     │  operator themes · instance default      │
└─────────────────────────────────────────────────────────────┘
                          ▲
                          │ HTTP API
       ┌──────────────────┼──────────────────┐
       │                  │                  │
   @brftech/         @brftech/          @brftech/
   filex-core        filex             filex-react
   (Vue 3 SFC)       (Web Component)   (React adapter)
       │                  │                  │
       ▼                  ▼                  ▼
   Vue 3 apps       Any framework      React apps
                    (vanilla, Angular,
                    Svelte, Solid, …)

   Same API, no server plugins:  desktop app (Electron, Windows/Linux/macOS)
                                 CLI client (filex client · filex sync)
```

## Screenshots

### Apps — signing a document, the first of them

Dana asks a colleague on the same filex and a partner outside it to sign an
agreement. The app is [e-Signature](https://github.com/BRF-Tech/filex-sign); every
screen is drawn by filex, and the link the partner gets is an ordinary share.

| Define the boxes — name each one and say whose it is; the document comes next | Place them — choose a box, tap the page where it goes |
|---|---|
| ![Defining the boxes of a signature request](docs/screenshots/v0.43.0/signing/sign-define-1440.png) | ![Placing the boxes on the document](docs/screenshots/v0.43.0/signing/sign-place-1440.png) |

| The partner's link — filex's one public screen, in your instance's name, behind a PIN | …and what it opens: only their own boxes — here a name typed in the face the requester chose (drawn and uploaded are the other two) |
|---|---|
| ![The outside signer's PIN gate](docs/screenshots/v0.43.0/signing/sign-outside-pin-1440.png) | ![The outside signer filling in their boxes](docs/screenshots/v0.43.0/signing/sign-outside-fill-1440.png) |

| While it is out — the document frozen for everybody, who has signed in its details | Installing an app — every permission it asks for, in plain words, before anything runs |
|---|---|
| ![The document locked, its Signatures panel open](docs/screenshots/v0.43.0/signing/sign-status-1440.png) | ![The install wizard's permission review](docs/screenshots/v0.43.0/apps/apps-install-review-1440.png) |

| An installed app — where it came from, its fingerprint, and every permission it holds in plain words (its settings and its actions follow, further down the page) | The converter, another app — every target under its category, three steps |
|---|---|
| ![An installed app's detail](docs/screenshots/v0.43.0/apps/apps-detail-1440.png) | ![The converter's wizard](docs/screenshots/v0.43.0/apps/convert-wizard-1440.png) |

| Every app on the instance, and a **language pack** among them — a manifest with nothing that runs, which says how much of this filex it translates and leaves with it |
|---|
| ![The Apps list, a language pack among the apps](docs/screenshots/v0.43.0/langpack/apps-list-1440.png) |

### Your own things, wherever you are

| The bell — the unread count on it, every row going where it says | All of your notifications, inside the explorer — for everybody, not only administrators |
|---|---|
| ![The bell with its unread badge, open](docs/screenshots/v0.43.0/signing/bell-badge-1440.png) | ![The full notification list over the explorer](docs/screenshots/v0.43.0/signing/notifications-list-1440.png) |

| My shares — the links you created, and their PINs when you need to pass one on | Every admin table — one pinned **Actions** menu per row, the same menu the explorer's ⋮ opens |
|---|---|
| ![My shares with a row's Actions menu open](docs/screenshots/v0.43.0/signing/my-shares-1440.png) | ![Admin → Shares, a row's Actions menu open](docs/screenshots/v0.43.0/signing/admin-table-actions-1440.png) |

### Your brand

| Appearance — compose a theme in your own colours, previewed as you type | Made the default, it is what everybody's explorer wears… |
|---|---|
| ![The theme editor](docs/screenshots/v0.43.0/appearance/theme-editor-1440.png) | ![The explorer wearing the operator's theme](docs/screenshots/v0.43.0/appearance/themed-explorer-1440.png) |

| …and the sign-in page, before anybody has signed in | A symlink filex will not follow says so — in the listing, and in words in its details |
|---|---|
| ![The sign-in page wearing the operator's theme](docs/screenshots/v0.43.0/appearance/themed-signin-1440.png) | ![A symlink that leaves the storage, badged](docs/screenshots/v0.43.0/symlinks/symlink-badge-1440.png) |

### The file manager

| Sharing — PIN, expiry, download limit, one-line `curl` | Markdown viewer |
|---|---|
| ![Share modal](docs/screenshots/v0.43.0/share-modal.png) | ![Markdown viewer](docs/screenshots/v0.43.0/viewer-markdown.png) |

| …and what the person at the other end opens. filex has ONE outward-facing screen — a shared file, a folder, a file request, an app's signing page and the PIN in front of any of them are all this page, in your instance's name |
|---|
| ![A public share link, as its recipient sees it](docs/screenshots/v0.43.0/public-share.png) |

| Admin panel | Demo landing |
|---|---|
| ![Admin dashboard](docs/screenshots/v0.43.0/admin-dashboard.png) | ![Demo landing](docs/screenshots/v0.43.0/demo-landing.png) |

| The shell — what everybody lands on | Searching this folder; `⌘K` / `Ctrl K` hands the query to the palette |
|---|---|
| ![The filex shell](docs/screenshots/v0.43.0/driveshell/driveshell-hero-1440.png) | ![Searching a folder](docs/screenshots/v0.43.0/driveshell/driveshell-search-1440.png) |

| Navigation panel — Home, Shared with me, My shares, Recent, Starred, Trash, and the storages you can reach | Collapsed to the icon rail |
|---|---|
| ![Navigation panel](docs/screenshots/v0.43.0/sidenav/sidenav-expanded-1440.png) | ![Collapsed to a rail](docs/screenshots/v0.43.0/sidenav/sidenav-rail-1440.png) |

| Tags — your own, or your team's; a tag opens every file carrying it, from every folder they live in | Trash — what was deleted, where it came from, and how long is left before it goes |
|---|---|
| ![Personal and team tags](docs/screenshots/v0.43.0/tags/tags-kinds-1440.png) | ![The trash view](docs/screenshots/v0.43.0/sidenav/view-trash-1440.png) |

| Shared with me — folders other people granted you, no mount instructions | Embedded in another product's page |
|---|---|
| ![Shared with me](docs/screenshots/v0.43.0/sidenav/view-shared-1440.png) | ![Embedded web component](docs/screenshots/v0.43.0/sidenav/embed-webcomponent-1440.png) |

| How to connect — the guides, built from *your* deployment | API keys — mint your own, in the explorer or in an embed (a person's session or token; an embed proxied with one shared *app* token does not get this entry) |
|---|---|
| ![How to connect](docs/screenshots/v0.43.0/sidenav/connect-1440.png) | ![API keys](docs/screenshots/v0.43.0/sidenav/apikeys-minted-1440.png) |

| Reaching filex from anything — S3, SFTP, FTPS, NFS, WebDAV. Every command is built from *your* deployment |
|---|
| ![Connection guide](docs/screenshots/v0.43.0/connections-guide.png) |

| A storage filex does not ship — installed as a plugin on **Plugins → Storage plugins**, describing its own config form |
|---|
| ![Plugins](docs/screenshots/v0.43.0/admin-plugins.png) |

## Quick start — binary

```bash
# Download from https://github.com/BRF-Tech/filex/releases
./filex serve
```

```
═══════════════════════════════════════════════════════════════
  filex · self-hosted file manager
═══════════════════════════════════════════════════════════════
  Listening on:   http://0.0.0.0:5212
  Admin UI:       http://0.0.0.0:5212/admin
  Files UI:       http://0.0.0.0:5212/drive
  Embed JS:       http://0.0.0.0:5212/embed.js

  First run detected. Initial admin user created:
    Email:    admin@local
    Password: kT9_x4Pq2Nm-BvLs
  Saved to:  ~/.filex/.first-run.txt (mode 0600, shown ONCE)
  Change at: /admin/dashboard?settings=1
═══════════════════════════════════════════════════════════════
```

## Self-host with Compose or Helm

The `docker run` above is enough to try filex out. For a real deployment,
ready-made stacks live in [`deploy/`](deploy/):

- **[`deploy/compose/`](deploy/compose/)** — Docker Compose:
  - **minimal** — filex + SQLite + local disk (one service, zero dependencies).
  - **full** — filex + PostgreSQL + Redis + Caddy (auto-HTTPS), plus toggleable
    add-ons: **OnlyOffice**, **Drawio**, **MinIO** (S3) and the legacy universal
    **converter** side-car (only offered when the [Convert app](#apps) is not
    installed — it is being retired).
    Turn each on/off with a Compose profile in `.env`.
- **[`deploy/helm/filex/`](deploy/helm/filex/)** — a Helm chart for Kubernetes
  (Deployment + PVC + optional Ingress). Every add-on above is an `enabled`
  toggle in `values.yaml` — bundle PostgreSQL / Redis / MinIO, or wire external
  OnlyOffice / Drawio / converter.

Step-by-step instructions for each tier are in
[docs/INSTALLATION.md](docs/INSTALLATION.md).

## Embed in your app

### Vue 3
```bash
pnpm add @brftech/filex-core
```
```vue
<script setup>
import { FileExplorer } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';
</script>
<template>
  <FileExplorer :config="{ apiBase: 'http://localhost:5212', auth: { kind: 'bearer', token: '…' } }" />
</template>
```

### React
```bash
pnpm add @brftech/filex-react
```
```jsx
import { FileManager } from '@brftech/filex-react';
<FileManager config={{ apiBase: 'http://localhost:5212' }} onError={(e) => console.error(e)} />
```

There is **no stylesheet to import** — the look travels inside the bundle and is
injected on mount, so nothing is missing from that snippet. ⚠ A bundler will need the
optional viewer packages externalized (`monaco-editor` and friends), which
[docs/INTEGRATION.md](docs/INTEGRATION.md) shows in one `rollupOptions.external`
line; every one of those imports is guarded, so the viewers degrade rather than break.

### Vanilla JS / any framework
```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212" sidenav connections ui-profile="simple"></filex-explorer>
```

`sidenav` turns the navigation panel on (it is on by default; the attribute is
there so a host page can state it either way), `connections` adds its "How to
connect" and "API keys" entries, and `ui-profile="simple"` presets the
power-user chrome off. All three are ordinary `config` keys, so the Vue and
React wrappers set them the same way — see
[docs/INTEGRATION.md](docs/INTEGRATION.md).

Multi-tenant hosts typically proxy the API server-side, inject a **confined token**
(`root: tenant-folder`) per request, and strip client headers — the sandbox is enforced by
the backend, not the widget. Such a token is `kind: "app"`, so the panel hides the
surfaces that belong to one person — API keys, Recent, Starred, Shared with me —
while Upload, the storages, Trash and "How to connect" stay. See
[docs/INTEGRATION.md](docs/INTEGRATION.md) and
[docs/MCP.md](docs/MCP.md#token-kinds--user-vs-app).

## Desktop app & CLI

The explorer also ships as a **Windows / Linux / macOS desktop app** — the same component
the web UI and the embeds render, not a separate half-copy:

- **Several accounts at once** — a rail of servers/tenants, each showing its own branding.
- **Drag files out** — drag a selection onto the desktop or into another app: folders and
  multi-selections arrive as separate real files and folders. Anything already kept on
  this computer drags instantly; the rest is fetched once and cached
  ([docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)).
- **Keep on this computer** — right-click any folder, file or whole storage to mirror it
  under one filex folder on the machine (movable from Settings); everything else stays
  online-only, and every row says which it is (✓ ◐ ⟳ ☁). "Keep online only" hands the
  local copy back to the Trash, or leaves it
  ([docs/DESKTOP.md](docs/DESKTOP.md#keeping-folders-on-this-computer)).
- **Folder sync** — pair a local folder with a server folder and they stay in step both
  ways while the app sits in the tray, **live**: a save in the browser is on disk in about
  a second and a local save on the server just as fast (the engine follows the server's
  change stream and the file system, with a full check every 30 s as the safety net),
  both versions kept when both sides change at once, parallel transfers and listings, a
  first run that resumes where it was interrupted, 30-day local trash, and an engine that
  refuses to turn a missing folder into a mass delete ([docs/SYNC.md](docs/SYNC.md)).
- **Opens Office documents off your own disk** — double-click a `.docx`/`.xlsx`/`.pptx`
  (or any of the ten Office types) and it opens in the editor your server runs, on a
  machine with no Office installed. A document inside a folder you keep on this computer
  opens as itself; anything else is copied up, edited, and written back over the original
  ([docs/DESKTOP.md](docs/DESKTOP.md#opening-documents-from-your-computer)).
- **Signs in through your browser**, so SSO and MFA behave exactly as they do on the web.
- **Updates itself** — downloads quietly, installs on quit; `FILEX_NO_UPDATE=1` opts out.
- **Runs without being installed**, if that is what you need: the Windows **portable**
  `.exe`, the Linux AppImage and the macOS `.zip` all run from wherever you put them. The
  portable Windows copy keeps everything it has in one `filex-data` folder beside itself,
  so deleting that folder leaves nothing of yours on a machine that is not yours — the
  trade is that it does not update itself.

Installer, portable `.exe`, AppImage, `.deb` and `.dmg` are attached to the
[latest release](https://github.com/BRF-Tech/filex/releases/latest) — not code-signed yet,
so expect a SmartScreen prompt on Windows. Details: [docs/DESKTOP.md](docs/DESKTOP.md).

The same binary is also a client for servers, scripts and headless machines:

```bash
filex client login --url https://files.example.com
filex client upload build/report.pdf docs://ci-artifacts/

filex sync add ~/Documents/work docs://work   # the engine the desktop app uses
filex sync run --watch 30s
```

See [docs/CLI.md](docs/CLI.md) and [docs/SYNC.md](docs/SYNC.md).

## AI agents / MCP

filex ships a token-authenticated automation surface at `/api/ai` (list, read, write,
move, delete, search, share, zip) and speaks **Model Context Protocol** at `/api/ai/mcp`:

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

An API key carries permissions by verb (`read,write,delete,share`), optionally **confined to a
single folder**, gated by the same RBAC grants as the UI, and stamped with per-key identities
so audit logs, shares and presence show *who* (which integration) did what. A key must name
at least one permission — an empty list is refused, never read as "all of them" — and
**what it hands out can never be wider than the key itself**: asking through a read-only or
folder-confined key for an API token, an S3 access key, an NFS export or an SSH key with more
verbs, a root outside its own, or a longer life is refused with `403 token_ceiling` naming what
was too wide. An
agent's **move never overwrites**: an item headed for a name that is taken lands
beside it under a free one (`report-copy.txt`), exactly as a move in the UI does,
and the answer names the path it really landed on.

A large file already on the agent's disk never fits through a tool call — its bytes would
have to travel through the model's context. **Upload tickets** fix that: one authorized
call pins the destination and returns a short-lived, single-use URL that needs **no
credentials**, so even an agent with no filex token can finish the transfer with
`curl -T bigfile <url>`. Details: [docs/MCP.md](docs/MCP.md).

## Apps

A **storage plugin** teaches filex a backend it has never heard of. An **app**
teaches it a *thing to do with files* — sign them, convert them, send them to
somebody outside — and it is a different kind of plugin on purpose: a
**WebAssembly module that runs inside filex, in a sandbox** that hands it nothing
it was not granted. No filesystem, no network, no environment, no program on your
server: only the host functions its manifest asks for, each shown to the
administrator in plain words before anything is installed, and only the files
the person who ran it actually selected. The heavy engines an app may want
(LibreOffice, ffmpeg, ImageMagick, Ghostscript, poppler, rsvg) are the server's
own, offered one permission per engine.

What an app adds lives where everything else does: rows in the file menu, screens
filex draws for it (an app never ships HTML into your browser), jobs in the same
queue as a copy — with progress, **Cancel** and a result that is versioned,
scanned and indexed like any other write — a section in a file's details, a home
screen under **Apps** in the navigation, and, when it needs somebody without an
account, a link that is an ordinary **share**: in the same list, under the same
PIN lock-out and expiry policy, revocable by you like every other link. An app
that asks for it is also woken once an hour to do its own scheduled work — a
signature request that closes itself at its deadline and sends the reminders you
asked for.

Not every app runs code. A **language pack** is a manifest of strings and nothing
else: it installs from the manifest alone — no module, no Go, no release — never
starts a runtime, and adds its language to the explorer, the admin panel, the
public pages and the text the server writes. **Plugins → Apps** lists it as a
*Language pack* with its coverage of the running version, and anything it lacks
shows in English. Spanish, German and French ship as examples, and
`BRF-Tech/filex-lang-template` walks a translator from export to install.

Two apps ship alongside filex, as public repositories you can install, read and
fork:

| App | What it adds |
|---|---|
| **[e-Signature](https://github.com/BRF-Tech/filex-sign)** — `BRF-Tech/filex-sign` | **Sign…**, **Request signatures…**, **Sign / Fill** and **Verify** on a PDF (an office document is turned into one first, when the server has LibreOffice). A request is a short wizard: who signs — people on this filex, who sign inside it, and anybody else by name or email, who gets a **private link**, behind a PIN unless you say otherwise — in what order, the boxes named and given to each signer, then placed on the page; how long it stays open, whether the file is **frozen** meanwhile, and whether an **audit trail PDF** is written at the end. The result is a PAdES-signed PDF that is **certified and sealed**: the first signature certifies the document so later ones may only fill in and sign, and when the last one lands **filex itself seals the whole file** with the installation's own seal, locked so that any change after it is reported as not permitted. The **SHA-256 of exactly those sealed bytes**, the seal's fingerprint and how to check them go to the requester and to every signer, inside and outside, and into the audit trail. Optionally the signed file stays **locked in filex** until an administrator lifts it. **Signing keys never leave the server**: the instance's own certificate authority (or one you import) issues a certificate per signer, and the key that made a signature is destroyed seconds later — the seal's key is the one exception, held by the host and never handed out. |
| **[Convert](https://github.com/BRF-Tech/filex-convert)** — `BRF-Tech/filex-convert` | **Convert…** on any file: images, video, audio, documents, e-books, archives, data, subtitles and fonts. The target is picked from buttons grouped under their category, then only the settings that matter for it, then a review. Most conversions run in pure Go inside the sandbox; the rest use the server's engines when they are installed, and a target that needs a missing one says so instead of being silently absent. |

**Install one from GitHub** — *Admin → Plugins → **Apps** → **Install an app** →
GitHub repository*: type `BRF-Tech/filex-sign` and the release tag. filex reads the
repository's `filex-app.json`, downloads the module it names and refuses it
unless its SHA-256 matches, then stops at the **permission review**. Nothing is
installed until you have read every permission and ticked *I understand*; the
grant is exactly that list, and an upgrade that asks for more stops at the review
again. `FILEX_PLUGIN_TRUSTED_KEYS` makes signed modules mandatory (a GitHub
install carries no signature, so on such an instance upload the module with its
signature instead). Apps are off in demo mode.

**Operator's guide:** [docs/APP-PLUGINS.md](docs/APP-PLUGINS.md) — installing and
controlling apps, the signing round end to end, the converter, scheduled
wake-ups, and what guards an app's public links. **Writing one** (stock Go,
`GOOS=wasip1`, with a test kit): [docs/PLUGIN-KIT.md](docs/PLUGIN-KIT.md); the
wire contract: [docs/APP-PLUGINS-API.md](docs/APP-PLUGINS-API.md). The other kind
of plugin, a storage backend: [docs/PLUGINS.md](docs/PLUGINS.md).

## Features

- **Multi-storage** — mount many storages at once (local, S3, FTP, SFTP, WebDAV, SMB/NAS); each appears as a top-level folder. Each also carries an address that never moves: the storage's name is the first path segment on WebDAV, SFTP, NFS and the S3 API, so renaming one would re-address it — a mount written against its **uid** survives every rename. **Copy or cut in one and paste in another**: filex streams the tree between the two drivers, keeps each file's timestamp, and only removes the original once the copy is verified.
- **Drag files out to your desktop** — in the desktop app, drag a selection into Explorer/Finder or another program and it lands as separate real files and folders, not an archive; in a browser, a single file drags out the same way ([docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)).
- **Storage plugins** — a storage filex has never heard of is a **separate program** you install from the admin panel: it describes its own config form, filex speaks a small HTTP/JSON protocol to it, and its driver then behaves like any built-in one. Any language; a Go SDK makes it three methods. filex **probes every capability a plugin claims** — at install, and again against the configuration you type when you save a storage on it — and refuses one that cannot do what it says, because a half-working driver produces failures that look like filex being broken. Upgrades replace the binary in place and roll back if the new one does not come up ([docs/PLUGINS.md](docs/PLUGINS.md)).
- **Apps** — a second kind of plugin: a **sandboxed WebAssembly** module that adds actions to the file menu (*Request signatures…*, *Convert…*), screens filex draws for it, a section in a file's details, a home screen under **Apps** in the navigation, and links an outside participant opens without an account. Installed from a GitHub repository through a **permission review** — the app gets exactly what you approved and nothing else: no filesystem, no network, no program on your server; the heavy engines (ffmpeg, LibreOffice, …) are the server's own, offered one permission at a time. The screens filex draws obey filex's rules whoever wrote them — every choice visible rather than hidden in a dropdown, nothing folded behind "advanced", one question per step. The link an app sends to an outside signer is an ordinary **share**, so you see and revoke it in the same list as everything else, and it is never worth more than its creator: a job started from it passes the same checks as one started inside filex (an action you switched off stays off, the creator's access to the document is read again), and it stops working when the creator's account is switched off — until it is switched back on. An app you grant `schedule` is woken once an hour to do its own work at the minute it chose, as an ordinary job in the queue. Two ship as public repositories: **e-Signature** and **Convert** ([Apps](#apps), [docs/APP-PLUGINS.md](docs/APP-PLUGINS.md), [write one](docs/PLUGIN-KIT.md)).
- **e-Signature** ([`BRF-Tech/filex-sign`](https://github.com/BRF-Tech/filex-sign), an app) — sign a PDF yourself, or ask others to: people on this filex sign inside it, from a notification that opens the right screen; anybody else gets a private link, behind a PIN by default — one filex keeps for you (see *Sharing*). The boxes are **defined first** — a name, whose it is, required or not, a date's layout — and **placed on the page after**, two questions on two screens. The document can be **frozen** for everybody, administrators included, while it is out; reminders and the deadline run on their own; the requester follows every signer in the file's details and on the app's home screen; and the result is a PAdES-signed PDF that is **certified** by its first signature and **sealed by filex** after its last, so a reader reports any change made afterwards as not permitted — with the **SHA-256 of the sealed bytes** and the seal's fingerprint sent to the requester and every signer, an **audit trail PDF** when you ask for one, a receipt for every signer, and an option to keep the finished file locked until an administrator lifts it. **Verify** reports any signed PDF: every signature, the certification, the seal, and whether this is the file whose hash was sent. Signing keys never leave the server: the instance's own certificate authority, or one you import, issues each signer a certificate, and the key that made a signature is destroyed seconds later ([docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#signing-documents-end-to-end)).
- **Convert, as an app** ([`BRF-Tech/filex-convert`](https://github.com/BRF-Tech/filex-convert)) — *Convert…* on any file or selection: images, video, audio, documents, e-books, archives, data, subtitles and fonts. The target is a button under its category, then only the settings that matter for it, then a review; most routes run in pure Go inside the sandbox, the rest through the server's engines, and a target that needs a missing engine is listed as such rather than silently absent ([docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#converting-files)). Separate from the optional converter side-car below.
- **Protocol gateway** — the same tree is reachable as **S3** (SigV4; aws-cli, rclone, restic, mc, s3fs), **SFTP** (OpenSSH, WinSCP, FileZilla, sshfs), **FTPS** (explicit TLS, for the equipment that only learned FTP; hand it your reverse proxy's auto-renewing certificate — it is re-read on change), **NFSv3** (LAN NAS clients, media players) and **WebDAV** — each with its own credential you can revoke on its own, and all of them behind the same permissions, trash and quota as the UI ([docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **`filex mount`** — attach a remote filex server to a folder over ordinary HTTPS: a folder on Linux, a **drive letter on Windows** (`filex mount Z:`, needs the free [WinFsp](https://winfsp.dev)). Not a sync: nothing is copied but a bounded read cache, so it opens one file out of a hundred thousand without downloading the rest.
- **Real-time collaboration** — presence bar with live avatars + focus, instant file-change updates over WebSocket, polling fallback. One write is announced the moment it lands; a burst (a zip extraction, a folder upload, an NFS client writing chunk after chunk) is merged into one frame per window so the folder stays live without flooding the page ([docs/REALTIME.md](docs/REALTIME.md)).
- **A listing that behaves like a table** — resize a column, hide one, drag one to a new place; the table scrolls sideways rather than dropping a column when it runs out of room, and the actions column stays pinned to the right. Sort by name, type, date or size, in either direction, and **the grid and the list obey the same sort** — until this release "sorted by size" was a fact about one view, and switching views reordered the rows under you. Sorted by date, all three views group the rows under **Today · Yesterday · This week · This month** and then month by month, in **your** time zone rather than the browser's.
- **A folder remembers how you left it** — optional, from user settings: the view mode and the sort of each folder you actually set up, kept **per person on the server** so they follow you to another machine and to the desktop app, and never leak to anyone else looking at the same folder. Off by default, in which case your last choice simply applies everywhere ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Who owns a file** — every node carries its owner, the listing has an **Owner** column and the filter row an **Owner** entry, and quota counts against the owner rather than whoever last touched the file.
- **Take a selection with you** — pick several files and folders and **Download** streams them as one archive, built on the fly: no temporary file is written into your storage, nothing is buffered in the tab, and a 700 MB archive costs the server under a megabyte of memory. **Move to** and **Copy to** open a folder chooser that spans every storage and refuses a destination you cannot write to — server-side, not just in the dialog.
- **New document** — create a Word, Excel, PowerPoint or OpenDocument file, or any text or code format, from the **+ New** menu: name it, choose where it goes, and it opens in the editor that handles it. The templates are real, minimal, valid documents compiled into the binary, so this works on an install with no LibreOffice; a type this deployment could not then open is not offered in the first place, and the dialog says why.
- **RBAC + item permissions** — roles, per-file/folder grants with inheritance, share invites by email (SMTP), grant-aware search and listings. **Shared with me** answers the reverse question from the recipient's side — what other people granted you, and which storages you reach only through a grant.
- **The shell** — one layout, for the operator and the end user alike, in the admin app, the desktop app and every embed: a top bar spanning the full width with the collapse control and the product mark at its left edge, one **search field** whose ⌘K / Ctrl+K chip hands the query to the command palette (the field searches this folder; the palette is where "everywhere", saved searches and commands live), a primary **+ New** menu (upload files · new folder · **new document** · request files), a **Type · People · Modified · Size** filter row under the breadcrumb, **Folders** and **Files** as labelled sections in grid view, an info panel split into **Details** (with "People with access" and a share-link row) and **Activity** (version history and comments), and a **storage line** under the navigation. Theme, palette, language, density, the time zone, the start page and the notification switches all live in **user settings**, reached from the avatar — and the web app keeps your theme, palette, density and language on your **account**, not in the browser, so they are waiting for you in the next one; the keyboard editor and *Restart the tour* are in the same menu. Nothing is removed from the build — an embed, which has no settings dialog, keeps a "⋯" menu that still holds them ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Home, inside the shell** — the landing view for everybody, admins included: your storages, what you opened last and what you starred, as cards in the content area with the same navigation panel and the same header as the files. Moving between Home and a folder changes the content and nothing else. An operator who would rather land on the admin dashboard chooses it in their profile settings.
- **Navigation panel** — the **+ New** menu as the primary action, the destinations Home / My files / Shared with me / **My shares** / Recent / Starred / Trash, the storages you can see, an **Apps** section when an installed app has a home screen, and **How to connect** + **API keys**: the per-protocol guides and the self-service token manager, opened from inside the explorer so an embedded copy's users can mint the credential WebDAV/FTPS/`filex mount` ask for instead of asking an administrator. Collapsible to an icon rail (remembered per browser) from the top bar, a drawer instead of a column under 560px. On by default in the web app, the desktop app and every embed; `uiProfile: 'simple'` additionally turns off the tab strip, the split pane, the gallery view mode and the "How to connect" surface without removing any of them from the build ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Sharing** — public links with PIN, expiry and max-downloads, under an admin-set **maximum link life** (default 7 days — the dialog only offers what the server will keep); folder links stream as ZIP (cached, pre-warmed up to a size ceiling, swept after a week); **file-request** upload links for inbound drops; ShareX-compatible upload endpoint. **My shares** lists the links you created — for everybody, not only administrators — with *Copy link*, *Copy PIN* and *Revoke*: a link's PIN is kept sealed beside the hash that guards it, so its creator or an administrator can read it back when somebody needs it again, and every read is written to the audit log. Five wrong PINs shut any public link for ten minutes. A download link, a file request and an app's page are **one branded public screen** — your instance's name, logo and colours, one PIN gate, one expiry story and a language picker ([docs/SHARING.md](docs/SHARING.md)).
- **Desktop app + folder sync** — Windows/Linux/macOS app: tray-resident two-way sync, **selective sync** (right-click → *Keep on this computer*, one root folder per account, the rest online-only), several accounts at once, **opens Office documents from your own disk** in the server's editor, self-updating (macOS: unsigned build, updates by re-download until it is signed). Each document opens in **its own window** (titled with the file's name), the windows are **frameless** with the app's own controls (native traffic lights on macOS), and **Settings → Open files with** chooses single- or double-click to open ([docs/DESKTOP.md](docs/DESKTOP.md), [docs/SYNC.md](docs/SYNC.md)).
- **Trash & version history** — deletes are reversible within a retention window, writes keep snapshots; both live in the storage you already mounted ([docs/TRASH-VERSIONING.md](docs/TRASH-VERSIONING.md)).
- **Write protection** — optional ClamAV scanning of every file written — the built-in editor included, and files the storage sync finds on the backend rather than through filex — reached through a local binary or a clamd container over the network; plus trash/version retention behind one admin surface. The switch, the scanner mode and address, the size ceiling and the editor save-scan window live on **Settings → Protection**; the `FILEX_CLAMAV*` variables seed them on a first boot and then step aside (the scanner's binary path stays environment-only, deliberately — it is a command this server executes) ([docs/PROTECTION.md](docs/PROTECTION.md)).
- **E2E encrypted folders** — client-side WebCrypto; the server stores ciphertext and never receives a key. Each folder gets a **recovery key**, shown once, so a forgotten password is not automatically lost data; an operator can optionally enable **key escrow** — at install, or adopted later on a running installation; it never reaches existing folders on its own, but their owners are offered the choice at unlock — and its use notifies the folder's owner ([docs/E2E-ENCRYPTION.md](docs/E2E-ENCRYPTION.md)).
- **Native multi-tenancy** — provider/tenant mode with per-tenant isolation on one instance ([docs/MULTI-TENANCY.md](docs/MULTI-TENANCY.md)).
- **Driver-pluggable everything** — storage / auth / DB / queue drivers opt-in via env (`FILEX_AUTH_DRIVERS=local,oidc`, `FILEX_QUEUE_DRIVER=postgres`, …).
- **OIDC SSO-first** — optional auto-redirect to your IdP with break-glass local login (`?local=1`), and the admin role follows an IdP group at every sign-in.
- **LDAP / Active Directory** — directory accounts sign in on the same password form as local ones, and on WebDAV/SFTP/FTPS/S3/NFS too; private-CA support, and `local` stays first so `admin@local` works while the directory is down ([docs/LDAP.md](docs/LDAP.md)).
- **Replica + reconciliation** — primary→replica fan-out (mirror / append-only / skip per path-glob rule), read fallback, scheduled status report, one-click "Fix all".
- **Persistent op queue** — restart-safe queue in your own database (SQLite / Postgres / MySQL) or in Redis, worker pool with retries + cancel + admin dashboard. Every driver orders by priority, so the antivirus scan for a file somebody just uploaded is served ahead of the twenty thousand a first import queued. Unset, the driver follows the database rather than defaulting to SQLite — pointing SQLite statements at a Postgres server is a syntax error on every poll and no job ever runs.
- **DB-backed file tree** — listings come from the DB cache (1-5 ms), not the storage backend (~100 ms); a periodic sync catches out-of-band changes, by etag where the backend reports one and by size + modification time where it does not. A storage's **Paths to exclude from scanning** (`.*`, `downloads/incomplete/**`, `*.tmp`) keeps the parts of an existing tree filex has no use for out of the walk, the catalogue, the search index and the virus scanner — a cost control, not an access control ([docs/STORAGE.md](docs/STORAGE.md#scan-exclusions)).
- **Viewers & editors** — image/video/audio, PDF, Markdown (split editor + preview), CSV, code (Monaco), Office via OnlyOffice, Drawio + Mermaid diagrams, 3D models.
- **Universal converter (legacy side-car)** — an optional separate service that converted between document/image formats from the UI. It is **being retired** in favour of the [Convert app](#apps) above: filex offers it only when the app is not installed and the service is configured, never beside the app's own *Convert…*, and **External services** marks it as legacy. It, OnlyOffice and drawio are configured in the admin panel and apply to the running server, with no restart ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md), [docs/CONVERT-INTEGRATION.md](docs/CONVERT-INTEGRATION.md)).
- **Notifications** — generic JSON webhooks (Slack/Discord-agnostic): any number of targets, each with its own signing secret and its own per-event subscription, plus an in-app bell with read/unread and a per-user mute matrix. The unread count is a **badge on the bell** — exact to 99, `99+` above, and on the desktop app's dock icon where the system has one — a row is clickable exactly when it has somewhere to go (a signature request opens the signing screen, not a notifications page), and **View all** opens every one of your notifications over the explorer, for everybody rather than only administrators. A write that **creates** a file and a write that **replaces** one are different events (`file.uploaded` / `file.updated`), and the ones an operator most wants on their own — an infected upload quarantined, a failed upload, an encrypted folder opened with its recovery key — are subscribable individually ([docs/NOTIFICATIONS.md](docs/NOTIFICATIONS.md)).
- **Search** — Bleve embedded, full-text + metadata, permission-aware. VS Code-style filename scoring: folders count and word order does not (`main code` finds `Code/main.go`), separators and typos forgiven (`invoice 2026` finds `invoice_2026.pdf`, `mian.go` finds `main.go`) while numbers are matched literally (`2026` never means `2025`), `tag:` filters, exact matches ranked first.
- **Thumbnails you can read** — a PDF shows its **first page**, top-anchored so the title is in the card; a video its first frame that is not black (opening on a fade used to produce a black square, and a clip shorter than a second produced nothing at all while the row still said "ready"); an Office document its rendered first page; and a text, code or CSV file **fills the card with its own first lines** rather than repeating the extension the row already prints. image, video (ffmpeg), PDF (ghostscript), Office (libreoffice); capability-aware, and a server missing one of those binaries now says so in its log at boot instead of silently drawing coloured rectangles. A cached thumbnail is released when the file it belongs to is deleted for good, and a periodic reconciler reclaims the orphans an older install accumulated ([docs/thumbnails.md](docs/thumbnails.md)).
- **Tabs, themes & deep links** — several folders open side by side, light/dark/auto theme, and an address bar that tracks the open folder so a pasted link lands there. Eight palettes ship in the theme gallery, each one a map of the `--fe-*` tokens rather than a second stylesheet, so a host page or an embed can pick one — or set its own values — without forking any CSS; an operator can add their own (see *Appearance*).
- **Appearance: your colours, everywhere** — the admin panel's **Appearance** screen composes named themes — twelve colours for light and for dark, a corner radius, a font stack — previewed as you type, and makes one the **instance default**. The text on a coloured button is chosen by contrast rather than assumed to be white, the rest of the palette is derived on the server, and the theme reaches the sign-in page and every public link, because branding that stops at the login is not branding: a signed-out page wears the instance default, never the palette of whoever last used that browser, and a signed-in person's own pick wins. Themes export and import as one JSON file. A **custom stylesheet** is the dangerous tool beside it, and it is now off until you switch it on, never served to anyone who is not signed in, cannot fetch anything, and cannot reach the screen that turns it off ([docs/INTEGRATION.md](docs/INTEGRATION.md#themes)).
- **One table, everywhere** — there is one table left in filex, the explorer's, and every other list is it: the admin panel's menus, **My shares**, an app's own screens. Each freezes its first column on the left and its actions on the right, resizes, reorders and sorts the same way, and ends each row in **one pinned Actions menu** holding everything that row can do — the same menu the explorer's ⋮ opens, so a second table cannot drift away from the first. An installed app with a home screen gets its own row under **Apps** in the panel's navigation.
- **Symlinks, at the storage boundary** — a link inside a `local` storage that points inside it is followed and opens as what it points at; one that leaves the storage is **listed with a badge and the reason**, and refused for reading, writing and deleting — unless you switch on *Follow symlinks that leave this folder* for that storage ([docs/STORAGE.md](docs/STORAGE.md#symlinks)).
- **Open the way each device expects** — with a **mouse**, a single click selects and a **double click opens** (Enter opens the selection) — the classic file-manager gesture, and a per-viewer preference (`ExplorerConfig.openTrigger`, default `'double'`; the desktop app exposes it as **Settings → Open files with**, and `'single'` restores one-click open). On a **touchscreen** a tap always opens — there is no hover-to-select. On every device the **checkbox** is the one click or tap that selects (Shift extends the range) and a right click or long press opens the menu; list rows, grid cards and gallery tiles all carry it.
- **Keyboard, and it says so** — every verb in the right-click menu and the toolbar prints the key that runs it, read from the registry so it follows a remap. Thirty-two actions are remappable from *Shortcut settings* (stored per browser); the handful of combinations a browser takes for itself, like `Ctrl+W`, are refused with a reason instead of stored as a key that would never fire.
- **Usage & cost** — filex does not meter your provider's bill; it reads the report the provider already writes, normalises it and prices it with a table you can edit. Backblaze B2's daily CSVs are read over the same S3 API filex already speaks, so no new dependency and no new credential type. Free allowances are their own fields rather than constants in a formula, and the page keeps the provider's account-level row apart from its per-bucket rows — summing them counts the same transactions twice, by exactly the amount nobody notices ([docs/USAGE.md](docs/USAGE.md)).
- **Audit log** — every mutation recorded with actor, integration identity and metadata.
- **CLI client** — the same binary reaches a remote server (`filex client`, `filex sync`) with no server-side plugin ([docs/CLI.md](docs/CLI.md)).
- **Self-updating** — patch releases install themselves, minor ones are announced for one-click upgrade ([docs/UPDATES.md](docs/UPDATES.md)).
- **Single binary** — goreleaser matrix: linux/macOS/Windows × amd64/arm64. CGO=0, modernc.org/sqlite.
- **i18n** — English + Turkish out of the box, **public links included**: a
  share link, a PIN gate, a file-request page or an app's signing screen
  renders in the visitor's language, and the public shell **offers a picker**,
  because a stranger's browser language is a guess and the person reading a
  contract should be able to correct it. The plain no-JS pages resolve
  `?lang=`, then `Accept-Language`, then the server default. **The text the
  server writes comes from the same catalogue** — mail, the notification
  phrases, the no-JavaScript pages and an install's permission review — each
  addressed to the reader it has always had, falling back to English per key,
  and a translation whose placeholders do not match the English is not used at
  run time, so a mail never loses its link or PIN.
- **Language packs** — any other language is an **app with nothing that runs**:
  a manifest of strings, installed from a GitHub repository, an upload or a URL
  like any other app, listed under **Plugins → Apps** with its coverage of the
  running version (*Español — 97% translated · the rest shows in English*). Its
  language joins every picker — the settings dialog, the admin header, public
  share pages — and translates the explorer, the admin panel and the public
  pages alike. Plural forms follow **CLDR categories**, so a pack writes `zero`,
  `one`, `two`, `few`, `many` and `other` where its language has them. Spanish,
  German and French ship as examples, and a template repository plus
  `scripts/i18n-export.mjs` / `i18n-validate.mjs` walk a translator from export
  to install ([write one](docs/PLUGIN-KIT.md#writing-a-language-pack)).
- **Right-to-left layout** — Arabic, Hebrew, Persian, Urdu and any other
  right-to-left language lay the whole interface out right to left: the
  navigation panel, the tables, the drag-and-drop and column-resize geometry,
  the menus and the direction icons. What must **not** mirror does not — a PDF
  field editor works in document space, and a path, a command or any other
  machine text is isolated so it reads left to right inside a right-to-left
  sentence, including sentences the server wrote. The rule is enforced by a
  guard test: layout is written in logical CSS properties only
  ([docs/RTL.md](docs/RTL.md)).
- **Tags, personal or your team's** — a tag is either **personal** — yours
  alone, never named to anybody else — or a **team** tag shared inside the
  tenant, which takes edit permission on the file to add or remove. A tag opens
  every file carrying it, from every folder they live in, and `tag:` narrows a
  search. Capitals are kept as typed.
- **Identity providers, managed from the panel** — **Admin → Identity
  providers** now drives sign-in rather than storing settings nothing read:
  OIDC, LDAP and the local password form, each with a **Test now** that really
  probes it and says which leg it verified. What is set in the environment or
  in `config.yaml` **wins, visibly**; a client secret or bind password is
  write-only and sealed at rest with `FILEX_SECRET_KEY`, never sent back; and
  the last way in cannot be switched off from the page
  ([docs/SSO.md](docs/SSO.md), [docs/LDAP.md](docs/LDAP.md)).

## Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Documentation

**Getting started** — [Installation](docs/INSTALLATION.md) ·
[Configuration](docs/CONFIGURATION.md) · [Databases](docs/DATABASES.md) ·
[Releases](docs/RELEASES.md) · [Updates](docs/UPDATES.md) ·
[Demo mode](docs/DEMO.md)

**Clients** — [Desktop app](docs/DESKTOP.md) · [Folder sync](docs/SYNC.md) ·
[CLI](docs/CLI.md) · [Integration / embedding](docs/INTEGRATION.md) ·
[AI & MCP](docs/MCP.md)

**Without a browser** — [Protocols (S3 · SFTP · FTPS · NFS · WebDAV ·
`filex mount`)](docs/PROTOCOLS.md) · [WebDAV](docs/WEBDAV.md)

**Apps** — [Apps: install, control, sign, convert](docs/APP-PLUGINS.md) ·
[Write an app](docs/PLUGIN-KIT.md) · [App wire contract](docs/APP-PLUGINS-API.md)

**Language & layout** —
[Write a language pack](docs/PLUGIN-KIT.md#writing-a-language-pack) ·
[Right-to-left languages](docs/RTL.md)

**Storage & access** — [Storage](docs/STORAGE.md) · [Storage plugins](docs/PLUGINS.md) ·
[Usage & cost](docs/USAGE.md) · [Uploads & resume](docs/UPLOADS.md) ·
[Quotas](docs/QUOTAS.md) · [SSO (OIDC)](docs/SSO.md) ·
[LDAP & proxy auth](docs/LDAP.md) · [RBAC & permissions](docs/RBAC.md) ·
[Multi-tenancy](docs/MULTI-TENANCY.md)

**Data & features** — [Sharing & file requests](docs/SHARING.md) ·
[ShareX](docs/SHAREX.md) ·
[Trash & versioning](docs/TRASH-VERSIONING.md) · [Protection](docs/PROTECTION.md) ·
[E2E encryption](docs/E2E-ENCRYPTION.md) · [Search](docs/SEARCH.md) ·
[Realtime & presence](docs/REALTIME.md) ·
[Notifications](docs/NOTIFICATIONS.md) · [Thumbnails](docs/thumbnails.md) ·
[Replication](docs/REPLICATION.md) · [Themes & appearance](docs/INTEGRATION.md#themes)

**Operate & extend** — [Deployment](docs/DEPLOYMENT.md) · [Docker](docs/DOCKER.md) ·
[Metrics](docs/METRICS.md) · [Architecture](docs/ARCHITECTURE.md) ·
[Backend API spec](docs/BACKEND.md) · [Component API](docs/API.md) ·
[OnlyOffice](docs/ONLYOFFICE.md) ·
[Converter side-car](docs/CONVERT-INTEGRATION.md)

[Full documentation index](docs/README.md)

## Development

```bash
git clone https://github.com/BRF-Tech/filex.git
cd filex
pnpm install
pnpm run build:all    # builds packages, web, then Go binary
./bin/filex serve
```

Subdirectories:
- `backend/` — Go HTTP service (cmd/filex, internal/*, db/queries, db/migrations)
- `packages/core` — `@brftech/filex-core` (Vue 3 SFC, source of truth)
- `packages/webcomponent` — `@brftech/filex` (Web Component wrapper)
- `packages/react` — `@brftech/filex-react` (React adapter via @lit/react)
- `web/` — Vue 3 admin UI (embedded into Go binary via `go:embed`)
- `desktop/` — Electron app (bundled main process, tray sync, auto-update)
- `demo/` — Standalone HTML demos for each framework
- `e2e/` — Playwright suites (web, embeds, packaged desktop app) + `shots/`, the
  scripts `pnpm shots` runs to retake every screenshot above
- `docker/` — Dockerfiles + compose
- `deploy/` — ready-made Compose stacks + Helm chart (see [`deploy/`](deploy/))
- `docs/` — Markdown documentation
- `docs-site/` — VitePress site published at [docs.filex.sh](https://docs.filex.sh)

Contributions welcome — see [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).

## License

MIT — see [LICENSE](LICENSE).
