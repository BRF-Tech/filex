<div align="center">

<img src="docs/logo.png" alt="filex logo" width="96">

# filex

**Self-hosted file manager & sharing with a web UI, in one Go binary - open-source alternative to Nextcloud, Dropbox and Google Drive.**<br>
Web-based file browser for local, S3, SFTP, WebDAV, FTP and SMB storage: private cloud served as S3, SFTP, NFS, WebDAV.<br>
SSO/LDAP, multi-tenant, encrypted folders, embeddable web component, desktop app, MCP server.

[![Try the live demo](https://img.shields.io/badge/Try_the_live_demo-f59e0b?style=for-the-badge)](https://demo.filex.sh)
[![Quick start](https://img.shields.io/badge/Quick_start-2f6ceb?style=for-the-badge)](#quick-start)
[![Documentation](https://img.shields.io/badge/Documentation-374151?style=for-the-badge)](https://docs.filex.sh)

[![Release](https://img.shields.io/github/v/release/BRF-Tech/filex?color=2f6ceb)](https://github.com/BRF-Tech/filex/releases)
[![License: MIT](https://img.shields.io/github/license/BRF-Tech/filex?color=22c55e)](LICENSE)

**English** · [Türkçe](README.tr.md) · [Deutsch](README.de.md) · [Español](README.es.md) · [Français](README.fr.md) · [简体中文](README.zh-CN.md)

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://filex.sh/shots/explorer-grid-dark.e032d7456831.png">
  <img src="https://filex.sh/shots/explorer-grid-light.de398671d43b.png" alt="filex explorer - thumbnail grid" width="900">
</picture>

</div>

<table>
<tr>
<td width="50%" valign="top">

**🗂️ Every storage, one tree**

Mount what you already have side by side, and copy or cut in one storage and paste in another. Point `rclone`, `restic`, WinSCP or FileZilla at the same tree.

[Storage and protocols →](#storage-and-protocols)

</td>
<td width="50%" valign="top">

**🔗 Share and protect**

Links with a PIN, an expiry and a download limit. Trash, version history and end-to-end encrypted folders.

[Sharing and protection →](#sharing-and-protection)

</td>
</tr>
<tr>
<td width="50%" valign="top">

**🔑 Your accounts, your roles**

OIDC, LDAP / Active Directory or the Windows and Linux accounts of the server. Roles, groups, multi-tenant.

[People and access →](#people-and-access)

</td>
<td width="50%" valign="top">

**💻 A desktop app with live folder sync**

Windows, Linux and macOS (Apple Silicon): an edit on either side arrives in about a second.

[Desktop app & CLI →](#desktop-app--cli)

</td>
</tr>
<tr>
<td width="50%" valign="top">

**🧩 Embeds in your product**

The same UI as a Vue 3 component, a React component and a `<filex-explorer>` web component.

[Embed in your app →](#embed-in-your-app)

</td>
<td width="50%" valign="top">

**🤖 Apps and AI agents**

Sandboxed apps - e-Signature, Convert, draw.io - and a built-in MCP server an agent can drive.

[Apps →](#apps) · [AI agents / MCP →](#ai-agents--mcp)

</td>
</tr>
</table>

<p align="center"><a href="#why-filex">Why filex</a> · <a href="#coming-from-nextcloud-dropbox-or-google-drive">Coming from another tool</a> · <a href="#self-host-with-compose-or-helm">Self-host</a> · <a href="#features">Features</a></p>

<br>

# Quick start

> [!TIP]
> **Look before you install:** [demo.filex.sh](https://demo.filex.sh) - sign in with
> `demo@demo.com` / `demo` (admin role, sandbox resets nightly).

Run your own, from the folder you want to serve:

```bash
docker run -p 5212:5212 \
  -e FILEX_DEFAULT_STORAGE_DRIVER=local -e FILEX_DEFAULT_STORAGE_PATH=/srv/files \
  -v filex-data:/data -v "$PWD:/srv/files" \
  ghcr.io/brf-tech/filex:latest
```

Open http://localhost:5212/admin - the first run prints admin credentials and embed
instructions to the console. That URL is the operator's; the people you give accounts to
get **http://localhost:5212/drive**, the same file manager without the panel around it.

<details>
<summary><b>What that command mounts, and which user it runs as</b></summary>

That serves **the folder you ran it in** - open the UI and your files are already
there. `/data` is filex's own directory (SQLite database, search index, thumbnail cache),
which is why it is a named volume and not the folder you drop files into; the two are
separate on purpose. Point `$PWD` somewhere else, or add more storages from the admin
panel later - a bucket with several top-level folders can be mounted as one storage
per folder in one go (*Storages → Add storage → Mount several folders at once*).

The container runs as **root** by default, so what it writes into `/data` is root-owned;
set `PUID`/`PGID` to run it as yourself
([docs/DOCKER.md](docs/DOCKER.md#which-user-the-container-runs-as)).

</details>

<details>
<summary><b>No Docker? Run the binary</b></summary>

Download the archive for your platform from the
[latest release](https://github.com/BRF-Tech/filex/releases/latest), extract it and run the
binary ([docs/INSTALLATION.md](docs/INSTALLATION.md#binary)):

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
    Password: <printed once>
  Saved to:  ~/.filex/.first-run.txt (mode 0600, shown ONCE)
  Change at: /admin/dashboard?settings=1
═══════════════════════════════════════════════════════════════
```

</details>

For a real deployment there are Compose stacks and a Helm chart:
[Self-host with Compose or Helm](#self-host-with-compose-or-helm). The image is on
[GitHub Packages](https://github.com/BRF-Tech/filex/pkgs/container/filex).

<br>

# Why filex

Most self-hosted file managers are either **too small** (a directory listing with uploads)
or **too big** (a groupware suite you deploy for the file tab). filex aims at the gap. The
tour below has eight parts, each with its screenshots folded under it. Every feature in
full, with its limits and the guide that documents it, is in [docs/FEATURES.md](docs/FEATURES.md).

<br>

## The explorer

**A file manager people already know how to use.**

- A navigation panel with **Home · My files · Shared with me · My shares · Recent · Starred ·
  Drafts · Trash**; list, grid and gallery views; tabs, tags and a ⌘K palette.
- Presence avatars and file changes arrive live, over WebSocket.
- Compose a theme in your own colours and make it the default: the sign-in page and every
  public link use it too, and a phone installs the web app under your instance's name and logo.
- English and Turkish are built in, other languages install as packs, and the interface turns
  right to left for the languages that read that way.

Every explorer feature in full: [docs/FEATURES.md → The explorer](docs/FEATURES.md#the-explorer).

<p align="center"><img src="https://filex.sh/shots/driveshell/driveshell-hero-1440.16ace59f07c3.png" alt="The filex shell" width="860"></p>

<details>
<summary><b>Screenshots of the explorer</b> - the shell, search, navigation, tags, the trash, the bell, themes, symlinks, CSV in ONLYOFFICE</summary>

<table>
<tr>
<td width="50%" valign="top">

![The filex shell](https://filex.sh/shots/driveshell/driveshell-hero-1440.16ace59f07c3.png)

<sub>The shell - what everybody lands on</sub>

</td>
<td width="50%" valign="top">

![Searching a folder](https://filex.sh/shots/driveshell/driveshell-search-1440.c0f3ceda0ec4.png)

<sub>Searching this folder; `⌘K` / `Ctrl K` hands the query to the palette</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Navigation panel](https://filex.sh/shots/sidenav/sidenav-expanded-1440.461d5aaaff2a.png)

<sub>Navigation panel - Home, Shared with me, My shares, Recent, Starred, Trash, and the storages you can reach</sub>

</td>
<td width="50%" valign="top">

![Collapsed to a rail](https://filex.sh/shots/sidenav/sidenav-rail-1440.843a2158380d.png)

<sub>Collapsed to the icon rail</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Personal and team tags](https://filex.sh/shots/tags/tags-kinds-1440.64e4d6a5ccbc.png)

<sub>Tags - your own, or your team's; a tag opens every file carrying it, from every folder they live in</sub>

</td>
<td width="50%" valign="top">

![The trash view](https://filex.sh/shots/sidenav/view-trash-1440.ab3cfb3b01cf.png)

<sub>Trash - what was deleted, where it came from, and how long is left before it goes</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![The bell with its unread badge, open](https://filex.sh/shots/signing/bell-badge-1440.61396b9ab714.png)

<sub>The bell - the unread count on it, every row going where it says</sub>

</td>
<td width="50%" valign="top">

![The full notification list over the explorer](https://filex.sh/shots/signing/notifications-list-1440.3176161f2110.png)

<sub>All of your notifications, inside the explorer - for everybody, not only administrators</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![The theme editor](https://filex.sh/shots/appearance/theme-editor-1440.5b28006557f8.png)

<sub>Appearance - compose a theme in your own colours, previewed as you type</sub>

</td>
<td width="50%" valign="top">

![The explorer wearing the operator's theme](https://filex.sh/shots/appearance/themed-explorer-1440.dbe464fc39c7.png)

<sub>Made the default, it is what everybody's explorer wears…</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![The sign-in page wearing the operator's theme](https://filex.sh/shots/appearance/themed-signin-1440.98801a84bea2.png)

<sub>…and the sign-in page, before anybody has signed in</sub>

</td>
<td width="50%" valign="top">

![A symlink that leaves the storage, badged](https://filex.sh/shots/symlinks/symlink-badge-1440.0d128385d287.png)

<sub>A symlink filex will not follow says so - in the listing, and in words in its details</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![A semicolon CSV open in ONLYOFFICE's spreadsheet](https://filex.sh/shots/csvoffice/csv-view-1440.83237ba55d3d.png)

<sub>A `.csv` opens in ONLYOFFICE's spreadsheet when one is connected - a look first, and no delimiter dialog: the file's own separator is passed along ([CSV files](docs/ONLYOFFICE.md#csv-files))</sub>

</td>
<td width="50%" valign="top">

![The CSV in ONLYOFFICE's editor, with the note on what a save keeps](https://filex.sh/shots/csvoffice/csv-edit-1440.4efc379a293d.png)

<sub>…and its editor, which says what a save as CSV keeps; the file goes back as the same kind of CSV, and the cells nobody changed keep their text - `007` stays `007` ([cells nobody changed](docs/ONLYOFFICE.md#cells-nobody-changed-keep-their-text))</sub>

</td>
</tr>
</table>

</details>

<br>

## Storage and protocols

**Every storage in one tree, and that tree reachable from anything.**

- Mount local disks, S3, FTP, SFTP, WebDAV and SMB/NAS shares side by side, and copy or cut
  in one and paste in another.
- Reach the same tree as **S3**, **SFTP**, **FTPS**, **NFSv3** and **WebDAV**: point `rclone`,
  `restic`, `aws s3`, WinSCP or FileZilla at it. The permissions, the trash and the quota are
  the web UI's.
- `filex mount` attaches a remote server over ordinary HTTPS: a folder on Linux, a drive
  letter on Windows (macOS is not supported).
- For a storage filex does not ship, install a plugin from the admin panel or from an app
  store's link.

Every storage and protocol feature in full: [docs/FEATURES.md → Storage and protocols](docs/FEATURES.md#storage-and-protocols).

<p align="center"><img src="https://filex.sh/shots/sidenav/connect-1440.8bfbe35780f4.png" alt="How to connect" width="860"></p>

<details>
<summary><b>Screenshots of storage and protocols</b> - the connection guides, API keys and a storage plugin</summary>

<table>
<tr>
<td width="50%" valign="top">

![How to connect](https://filex.sh/shots/sidenav/connect-1440.8bfbe35780f4.png)

<sub>How to connect - the guides, built from *your* deployment</sub>

</td>
<td width="50%" valign="top">

![API keys](https://filex.sh/shots/sidenav/apikeys-minted-1440.0ac3d81aa9f5.png)

<sub>API keys - mint your own, in the explorer or in an embed (a person's session or token; an embed proxied with one shared *app* token does not get this entry)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Connection guide](https://filex.sh/shots/connections-guide.98a4f82ecd9d.png)

<sub>Reaching filex from anything - S3, SFTP, FTPS, NFS, WebDAV. Every command is built from *your* deployment</sub>

</td>
<td width="50%" valign="top">

![Plugins](https://filex.sh/shots/admin-plugins.c25fa69cfc7c.png)

<sub>A storage filex does not ship - installed as a plugin on **Plugins → Storage plugins**, describing its own config form</sub>

</td>
</tr>
</table>

</details>

<br>

## Sharing and protection

**Share with a link you can limit and revoke. Take back a wrong delete or a wrong overwrite.**

- Public links with a PIN, an expiry and a download limit, and file requests for what should
  come in. **My shares** lists every link you created, to copy or to revoke.
- Share a folder with a person or a group and it appears under their **Shared with me**. The
  storage needs **Per-item access control (RBAC)** switched on, which it is not by default.
- Deletes are reversible within a retention window and writes keep snapshots, both inside the
  storage you already mounted.
- End-to-end encrypted folders: encrypted in the browser, and the server never receives a key.
  An app you allow edits such a file in your browser, which decrypts it for the app and
  encrypts what it saves. Optional ClamAV scanning of every file written.

Every sharing and protection feature in full: [docs/FEATURES.md → Sharing and protection](docs/FEATURES.md#sharing-and-protection).

<p align="center"><img src="https://filex.sh/shots/public-share.0e3ba07f7c88.png" alt="A public share link, as its recipient sees it" width="860"></p>

<details>
<summary><b>Screenshots of sharing and protection</b> - the Share dialog, what the recipient sees, My shares, who may encrypt</summary>

<table>
<tr>
<td width="50%" valign="top">

![Share modal](https://filex.sh/shots/share-modal.d6f6572228ea.png)

<sub>Sharing - PIN, expiry, download limit, one-line `curl`</sub>

</td>
<td width="50%" valign="top">

![Markdown viewer](https://filex.sh/shots/viewer-markdown.1789ecdcfbc5.png)

<sub>Markdown viewer</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![A public share link, as its recipient sees it](https://filex.sh/shots/public-share.0e3ba07f7c88.png)

<sub>…and what the person at the other end opens. filex has ONE outward-facing screen - a shared file, a folder, a file request, an app's signing page and the PIN in front of any of them are all this page, in your instance's name</sub>

</td>
<td width="50%" valign="top">

![My shares with a row's Actions menu open](https://filex.sh/shots/signing/my-shares-1440.6f8676cbb555.png)

<sub>My shares - the links you created, and their PINs when you need to pass one on</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Admin → Shares, a row's Actions menu open](https://filex.sh/shots/signing/admin-table-actions-1440.7e448107c5b9.png)

<sub>Every admin table - one pinned **Actions** menu per row, the same menu the explorer's ⋮ opens</sub>

</td>
<td width="50%" valign="top">

![Admin → Encryption: the approval policy and three requests waiting](https://filex.sh/shots/encryption/admin-encryption-1440.b74163acb93c.png)

<sub>Who may encrypt - off, administrators only, everyone whose role allows it, or after an administrator's approval; the requests waiting, with who asked and why ([who may encrypt](docs/E2E-ENCRYPTION.md#who-may-encrypt))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![The request for an encrypted folder, with its reason](https://filex.sh/shots/encryption/request-new-folder.e74894fba30f.png)

<sub>…and the person's side: the request the New folder dialog opens - one new encrypted folder, asked of an administrator with a reason</sub>

</td>
</tr>
</table>

</details>

<br>

## People and access

**The accounts people already have, and one role each.**

- Sign in with a local password, OIDC, LDAP / Active Directory, an authenticating proxy, or
  the Windows or Linux account of the machine filex runs on.
- Everyone has one role - Administrator, User, Viewer or one of your own - built from named
  permissions. Groups carry folder access and a role, can follow your directory's groups,
  and per-person exceptions beat the role.
- Guessing a password is slow: wrong passwords are counted per account and per address, on
  the web form, WebDAV, FTPS and SFTP alike.
- Multi-tenant by design, behind one switch: each tenant has its own sign-in providers, its
  own domain and certificate, and runs itself. Switched off, no screen shows a tenant.

Every feature for people and access in full: [docs/FEATURES.md → People and access](docs/FEATURES.md#people-and-access).

<p align="center"><img src="https://filex.sh/shots/roles/roles-list-1440.77477a2c7001.png" alt="Admin → Roles: the built-in roles and two custom ones" width="860"></p>

<details>
<summary><b>Screenshots of people and access</b> - roles, groups, sign-in security, a locked account, the admin panel and its menu</summary>

<table>
<tr>
<td width="50%" valign="top">

![Admin → Roles: the built-in roles and two custom ones](https://filex.sh/shots/roles/roles-list-1440.77477a2c7001.png)

<sub>Roles - Administrator, User, Viewer and roles of your own: who holds each, what it allows, where it differs by folder, its limits ([docs/PERMISSIONS.md](docs/PERMISSIONS.md))</sub>

</td>
<td width="50%" valign="top">

![Admin → Groups](https://filex.sh/shots/groups/groups-list-1440.73224b70e40b.png)

<sub>Groups - named sets of people with folder access and a role; members by hand or kept in step with the groups a sign-in carries ([docs/GROUPS.md](docs/GROUPS.md))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Sharing a folder with a group](https://filex.sh/shots/groups/share-group-1440.31c2a81e1eeb.png)

<sub>Sharing a folder with a group, beside people - Owner is asked for in the dialog, not granted by a click</sub>

</td>
<td width="50%" valign="top">

![Admin → Sign-in security](https://filex.sh/shots/loginsecurity/login-security-1440.077cec3f1b0a.png)

<sub>Sign-in security - the attempt limit and the addresses it never counts; trusted proxies, the locks and the sign-in trail follow on the same page ([sign-in attempt limits](docs/CONFIGURATION.md#sign-in-attempt-limits))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![The sign-in form on a locked account](https://filex.sh/shots/loginsecurity/login-locked-1440.3f4c6d3233f3.png)

<sub>…and what a locked account's sign-in form says, counting the lock down on its button</sub>

</td>
<td width="50%" valign="top">

![Admin dashboard](https://filex.sh/shots/admin-dashboard.d94a065baab6.png)

<sub>Admin panel</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Demo landing](https://filex.sh/shots/demo-landing.bb2b8d97d4ff.png)

<sub>Demo landing</sub>

</td>
<td width="50%" valign="top">

![The People & security panel of the admin menu, open over Admin → Users](https://filex.sh/shots/megamenu/people-panel-1440.2efdcb9a685a.png)

<sub>The admin menu - every page in three panels, **Files & storage**, **People & security** and **System**, with a short line under each; a phone gets the same pages in a drawer ([docs/ADMIN-PANEL.md](docs/ADMIN-PANEL.md))</sub>

</td>
</tr>
</table>

</details>

<br>

## Desktop app & CLI

**Folders on your computer stay in step with the server, live.**

- A Windows, Linux and macOS (Apple Silicon) app: the same explorer in a window, with several
  accounts or tenants side by side.
- Right-click a folder → **Keep on this computer** and it stays in step with the server in
  both directions, in about a second. Everything else stays online-only in the window.
- It signs in through your browser, so SSO and MFA behave as they do on the web, and it opens
  Office documents from your own disk in the editor your server runs.
- Headless machines get the same engine: `filex sync` and `filex client`.

**Install it** from the Microsoft Store (Windows 10/11) or the Snap Store (Ubuntu and
other Linux with snapd):

<p>
<a href="https://apps.microsoft.com/detail/9PKXDJLVZWXW"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/ms-store-light.svg"><img src="docs/badges/ms-store-dark.svg" alt="Download from the Microsoft Store" height="52"></picture></a>&nbsp;&nbsp;&nbsp;
<a href="https://snapcraft.io/filex-app"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/snap-store-white.svg"><img src="docs/badges/snap-store-black.svg" alt="Get it from the Snap Store" height="52"></picture></a>
</p>

or with a package manager:

```bash
brew install brf-tech/filex/filex-app       # macOS 13+ (Apple Silicon), Homebrew tap BRF-Tech/homebrew-filex
sudo snap install filex-app                 # the same snap as the badge above
```

<details>
<summary><b>More about installing it, and the CLI</b> - the Store build, winget, the release downloads, Linux, arm64, the client commands</summary>

The Store build (*filex File Manager*) is the one Windows copy that is code-signed -
Microsoft signs it - and the Store keeps it up to date. The desktop app's winget package
(`BRFTech.filex-app`) is submitted with every release and is waiting for its first review
by the winget moderators, so `winget install BRFTech.filex-app` does not find it yet.
Installer, portable `.exe`, AppImage, `.deb`, `.rpm` and `.dmg` are attached to the
[latest release](https://github.com/BRF-Tech/filex/releases/latest) - not code-signed yet,
so expect a SmartScreen prompt from the Windows installer. Details:
[docs/DESKTOP.md](docs/DESKTOP.md). The CLI alone: `brew install brf-tech/filex/filex`,
or `winget install BRFTech.filex` on Windows ([docs/CLI.md](docs/CLI.md)).

On Linux the `.deb` and `.rpm` need nothing; on Ubuntu 23.10 and later an AppImage needs a
one-time AppArmor profile ([AppImage on recent Ubuntu](docs/DESKTOP.md#appimage-on-recent-ubuntu)),
and the Snap runs inside the snap's strict confinement
([The snap and the sandbox](docs/DESKTOP.md#the-snap-and-the-sandbox)). Every release builds
for arm64 too: the server, the images, and the desktop app on Linux, Windows on Arm and Apple
Silicon ([docs/INSTALLATION.md → ARM (arm64)](docs/INSTALLATION.md#arm-arm64)). Everything the
app does: [docs/FEATURES.md → Desktop app, phone and CLI](docs/FEATURES.md#desktop-app-phone-and-cli).

The same binary is also a client for servers, scripts and headless machines:

```bash
filex client login --url https://files.example.com
filex client upload build/report.pdf docs://ci-artifacts/
filex client mv docs://ci-artifacts/report.pdf archive://2026/   # across storages, waits for the job
filex client run convert convert docs://data/table.csv --param target=xlsx

filex sync add ~/Documents/work docs://work   # the engine the desktop app uses
filex sync run --watch 30s
```

See [docs/CLI.md](docs/CLI.md) and [docs/SYNC.md](docs/SYNC.md).

</details>

<br>

## Embed in your app

**A real file manager inside your own product.**

The same UI ships as a Vue 3 component, a React component and a framework-agnostic
`<filex-explorer>` web component, backed by your own filex server. A confined token locks it
to a per-tenant folder, and the backend enforces that, not the widget. The web component
takes two lines:

```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212" sidenav connections ui-profile="simple"></filex-explorer>
```

<p align="center"><img src="https://filex.sh/shots/sidenav/embed-webcomponent-1440.c8c25d893d24.png" alt="Embedded web component" width="860"></p>

<details>
<summary><b>Vue 3 and React</b> - the other two wrappers, and where the details are</summary>

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

Styles, bundlers, the `sidenav` / `connections` / `ui-profile` attributes, confined tokens
for multi-tenant hosts, a session cookie from another origin and printing from an app:
[docs/INTEGRATION.md](docs/INTEGRATION.md), summed up in
[docs/FEATURES.md → Embedding](docs/FEATURES.md#embedding). ⚠ Keep the packages and the
server on one version ([docs/API.md](docs/API.md)).

<table>
<tr>
<td width="50%" valign="top">

![Shared with me](https://filex.sh/shots/sidenav/view-shared-1440.475ec2d8b49f.png)

<sub>Shared with me - folders other people granted you, no mount instructions</sub>

</td>
<td width="50%" valign="top">

![Embedded web component](https://filex.sh/shots/sidenav/embed-webcomponent-1440.c8c25d893d24.png)

<sub>Embedded in another product's page</sub>

</td>
</tr>
</table>

</details>

<br>

## Apps

**New things to do with files, with exactly the permissions you approved.**

An app is a WebAssembly module that runs inside filex in a sandbox, an interface of its own
in a sandboxed frame, or both. You install it from a store link or its GitHub address, read
every permission it asks for, and nothing updates itself. The people of your filex can browse a
trusted store on its **App store** screen and ask you for an app or a storage plugin. Five ship as public repositories:

| App | What it adds |
|---|---|
| [e-Signature](https://github.com/BRF-Tech/filex-sign) | Sign a PDF, or ask others to: people on this filex sign inside it, anybody else by a private link, behind a PIN by default |
| [Convert](https://github.com/BRF-Tech/filex-convert) | **Convert…** on any file or selection: images, video, audio, documents, e-books, archives, data, subtitles and fonts |
| [Office editor](https://github.com/BRF-Tech/filex-office-editor) | Word, Excel and PowerPoint documents in ONLYOFFICE's editor, running in the browser with no Document Server; its 0.2.0 and 0.3.0, made for filex 0.56, add encrypted folders and several people at once |
| [filextext](https://github.com/BRF-Tech/filextext-app) | An end-to-end encrypted text workspace in a single `.fxtxt` file |
| [draw.io](https://github.com/BRF-Tech/filex-drawio) | The draw.io diagram editor, on `.drawio` and `.dio` files |

An app you allow can edit a file **together** with other people - filex's relay puts their
changes in one order without reading them, and someone who may only read watches live - and
can open a file of an **encrypted folder**, which your browser decrypts for it and encrypts
again before it is saved.

A language pack is an app too: Spanish, German and French ship as examples.

Install one in a few minutes: [docs/APP-PLUGINS.md → Install one](docs/APP-PLUGINS.md#install-one). Every app feature in full: [docs/FEATURES.md → Apps](docs/FEATURES.md#apps).

<p align="center"><img src="https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png" alt="Placing the boxes on the document" width="860"></p>

<details>
<summary><b>Screenshots of apps</b> - a signature request from start to finish, installing an app, the converter, an app with its own interface, editing together, an encrypted folder's file in an app, a language pack, Default apps, installing from a store</summary>

Dana asks a colleague on the same filex and a partner outside it to sign an
agreement. The app is [e-Signature](https://github.com/BRF-Tech/filex-sign); every
screen is drawn by filex, and the link the partner gets is an ordinary share.

<table>
<tr>
<td width="50%" valign="top">

![Defining the boxes of a signature request](https://filex.sh/shots/signing/sign-define-1440.63ea72b13724.png)

<sub>Define the boxes - name each one and say whose it is; the document comes next</sub>

</td>
<td width="50%" valign="top">

![Placing the boxes on the document](https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png)

<sub>Place them - choose a box, tap the page where it goes</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![The outside signer's PIN gate](https://filex.sh/shots/signing/sign-outside-pin-1440.2cd8ccb99483.png)

<sub>The partner's link - filex's one public screen, in your instance's name, behind a PIN</sub>

</td>
<td width="50%" valign="top">

![The outside signer filling in their boxes](https://filex.sh/shots/signing/sign-outside-fill-1440.9f50aeb7abef.png)

<sub>…and what it opens: only their own boxes - here a name typed in the face the requester chose (drawn and uploaded are the other two)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![The document locked, its Signatures panel open](https://filex.sh/shots/signing/sign-status-1440.9642990b704d.png)

<sub>While it is out - the document frozen for everybody, who has signed in its details</sub>

</td>
<td width="50%" valign="top">

![The install wizard's permission review](https://filex.sh/shots/apps/apps-install-review-1440.cdb1a4ebf8f6.png)

<sub>Installing an app - every permission it asks for, in plain words, before anything runs</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![An installed app's detail](https://filex.sh/shots/apps/apps-detail-1440.4e27b40c5ce7.png)

<sub>An installed app - where it came from, its fingerprint, and every permission it holds in plain words (its settings and its actions follow, further down the page)</sub>

</td>
<td width="50%" valign="top">

![The converter's wizard](https://filex.sh/shots/apps/convert-wizard-1440.231ada006fd6.png)

<sub>The converter, another app - every target under its category, three steps</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![The install review of an app with its own interface](https://filex.sh/shots/apps/app-interface-review-1440.51df0aa460ad.png)

<sub>An app that brings its own interface - the review shows the package's fingerprint, every address outside it (a live one is a permission, in yellow) and what a browser cannot promise</sub>

</td>
<td width="50%" valign="top">

![An app's own interface open as a file's viewer](https://filex.sh/shots/apps/app-interface-viewer-1440.519b23618156.png)

<sub>…and that interface open on its own file type, where filex's preview would be. It reads and saves the file through filex, in a sandboxed frame (a small example app, written for these pictures)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Two people in one document: one editing, the other watching](https://filex.sh/shots/officeapp/coedit-watcher-1280.904b31e20f36.png)

<sub>Editing together - Dana types, and Sam, who may only read, watches every change and her cursor arrive through filex's relay ([Editing together](docs/APP-PLUGINS-API.md#editing-together-coedit-056); an example app written for these pictures)</sub>

</td>
<td width="50%" valign="top">

![A file of an encrypted folder open in an app, decrypted by the browser](https://filex.sh/shots/e2eapp/app-encrypted-folder-1280.856d3586a9e2.png)

<sub>A file of an end-to-end encrypted folder in an app you allowed - your browser decrypts it for the app and encrypts the save, and the server still holds only ciphertext (the same example app; [Editing in an app](docs/E2E-ENCRYPTION.md#editing-in-an-app))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![The Apps list, a language pack among the apps](https://filex.sh/shots/langpack/apps-list-1440.004f0c819981.png)

<sub>Every app on the instance, and a **language pack** among them - a manifest with nothing that runs, which says how much of this filex it translates and leaves with it</sub>

</td>
<td width="50%" valign="top">

![Plugins → Default apps](https://filex.sh/shots/defaultapps/default-apps-1440.27d3c64fe457.png)

<sub>Default apps - every kind of file something besides filex handles: who opens it and who draws its thumbnail, in the order you set ([Default apps](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Folders drawn with their newest files](https://filex.sh/shots/thumbnails/folders-grid-1440.2b408fb54f7e.png)

<sub>Folder previews - each folder drawn with the three files that came into it last; the SVGs are drawn by filex's built-in engine ([docs/thumbnails.md](docs/thumbnails.md#folder-previews))</sub>

</td>
<td width="50%" valign="top">

![The install review opened from a store's link](https://filex.sh/shots/store/store-review-1440.01ab061907d0.png)

<sub>Installing from a store - the same review, marked **From store**, the paid app's license key shown by its first characters ([Installing from a store](docs/APP-PLUGINS.md#installing-from-a-store))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![The first link from a store: trust it?](https://filex.sh/shots/store/store-trust-1440.94bebecac9fe.png)

<sub>The first link from a store - its address and the fingerprints of its keys, to compare before you trust it ([Trusted stores](docs/APP-PLUGINS.md#trusted-stores))</sub>

</td>
<td width="50%" valign="top">

![A paid app held, the band on an admin page](https://filex.sh/shots/store/store-license-held-1440.2dd9518d7e87.png)

<sub>A license the store revoked - the app held, *Unlicensed*, nothing removed, and a band on every admin page ([Paid apps](docs/APP-PLUGINS.md#paid-apps))</sub>

</td>
</tr>
</table>

</details>

<br>

## AI agents / MCP

**An agent works in the folder you gave it, and nowhere else.**

- A native **MCP server** at `/api/ai/mcp`, and a REST surface at `/api/ai` described in an
  OpenAPI 3.1 file.
- An API key carries permissions by verb and can be confined to a single folder, and nothing
  it hands out can be wider than the key itself.
- A large file goes through an **upload ticket**: a short-lived, single-use URL, so its bytes
  never travel through the model's context.

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

What an agent can do, what a key can never hand out, encrypted folders and upload tickets:
[docs/FEATURES.md → AI agents and MCP](docs/FEATURES.md#ai-agents-and-mcp); the reference is
[docs/MCP.md](docs/MCP.md).

<br>

# Coming from Nextcloud, Dropbox or Google Drive

filex is a file manager, not a groupware suite. What that means next to four things you may
be coming from (File Browser among them):

| Coming from | With filex |
|---|---|
| **Nextcloud**<br>A collaboration platform you host yourself or get from a provider: files, and around them calendar, contacts, mail, chat, video calls and an online office. A PHP application behind a web server, with a database. | The files part on its own.<br>One Go binary with SQLite inside (PostgreSQL or MySQL when you want them).<br>A desktop app with live folder sync, share links, SSO and LDAP.<br>Office documents, alone or together, in the office editor app or the ONLYOFFICE you connect.<br>No calendar, contacts, mail, chat or video calls. |
| **Dropbox**, **Google Drive**<br>Hosted services: your files live on the provider's servers, under its quota and its terms. | Your own server and the storage you already have: a disk, a NAS share, an S3 bucket.<br>The everyday things in place: a web UI, share links with a PIN and an expiry, file requests, trash and version history.<br>A desktop app that keeps the folders you choose in step. |
| **File Browser**<br>One binary that puts a web UI over one directory you point it at, with user accounts (each with its own scope and permission switches), allow and deny rules per path, and share links with a password and an expiry. Its repository is archived, and its [README](https://github.com/filebrowser/filebrowser) says there will be no further releases, bug fixes or security fixes. | The same one-command start.<br>Several storages side by side.<br>Roles and groups on top of per-folder access; OIDC single sign-on and LDAP built in.<br>Trash and versions, full-text search.<br>The same tree reachable as S3, SFTP, FTPS, NFSv3 and WebDAV. |

**What filex does not have**, whichever of these you come from:

- **An Android or iOS app.** On a phone filex is the web app, which offers itself to be
  installed like one ([On a phone or a tablet](docs/DESKTOP.md#on-a-phone-or-a-tablet-the-web-app))
  but needs a connection to show files; with it closed, notifications arrive as Web Push once you turn them on for the device.
- **Placeholder files in Finder or Explorer.** The desktop app copies the folders you keep on
  the computer, and everything else stays online in its own window
  ([docs/DESKTOP.md](docs/DESKTOP.md#keeping-folders-on-this-computer)).
- **An office editor built into the binary.** Office documents are edited - alone or
  together - by the office editor app you install
  ([`filex-office-editor`](https://github.com/BRF-Tech/filex-office-editor), ONLYOFFICE's
  editor in the browser, no Document Server), or in an ONLYOFFICE Document Server you
  connect; with neither they open in a read-only preview ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md)).

<details>
<summary><b>Moving in</b> - what comes with you, and what does not</summary>

- Where the files already sit somewhere filex can mount - a folder on a disk or a NAS share,
  a prefix in an S3 bucket, an SFTP, FTP or WebDAV server - moving in is a mount, not a
  migration. filex does not store files itself: point a storage at that folder and they are
  there, as they are.
- Files held in Dropbox or Google Drive have to be copied out to such a place first: filex
  ships no storage driver for either.
- Only the files move: the links, permissions and version history the old system kept are not
  imported.
- What filex adds - the trash, version history, drafts - sits in hidden folders at the
  storage's root, and a folder you encrypt end to end holds ciphertext from then on
  ([docs/STORAGE.md](docs/STORAGE.md), [docs/TRASH-VERSIONING.md](docs/TRASH-VERSIONING.md)).

</details>

<br>

# Self-host with Compose or Helm

The `docker run` above is enough to try filex out. For a real deployment,
ready-made stacks live in [`deploy/`](deploy/):

- **[`deploy/compose/`](deploy/compose/)** - Docker Compose:
  - **minimal** - filex + SQLite + local disk (one service, zero dependencies).
  - **full** - filex + PostgreSQL + Redis + Caddy (auto-HTTPS), plus toggleable
    add-ons: **OnlyOffice**, **Drawio** and an **S3 server** (Versity S3 Gateway). Turn each on/off with
    a Compose profile in `.env`. Conversion is the [Convert app](#apps), not a
    side-car.
- **[`deploy/helm/filex/`](deploy/helm/filex/)** - a Helm chart for Kubernetes
  (Deployment + PVC + optional Ingress), published with every release:
  `helm repo add brf-tech https://helm.brf.sh`, then `helm install filex brf-tech/filex`
  (or `oci://ghcr.io/brf-tech/charts/filex`). Every add-on above is an `enabled`
  toggle in `values.yaml` - bundle PostgreSQL / Redis / an S3 server, or wire external
  OnlyOffice / Drawio.

Step-by-step instructions for each tier are in
[docs/INSTALLATION.md](docs/INSTALLATION.md).

filex runs at the root of a host of its own or under a path of one it shares
(`https://example.com/filex/`): one setting, `FILEX_BASE_PATH`, and a proxy
that passes the full path - Caddy, nginx and Helm examples in
[docs/DEPLOYMENT.md → Serving filex under a sub-path](docs/DEPLOYMENT.md#serving-filex-under-a-sub-path).

<br>

# Documentation

The guides are published as a site at [docs.filex.sh](https://docs.filex.sh), and the
[full documentation index](docs/README.md) lists every page.

Start with [Installation](docs/INSTALLATION.md), [Configuration](docs/CONFIGURATION.md), the
[desktop app](docs/DESKTOP.md), [protocols](docs/PROTOCOLS.md), [apps](docs/APP-PLUGINS.md)
and [ONLYOFFICE](docs/ONLYOFFICE.md). [Features](docs/FEATURES.md) gives every feature an
entry with its limits and its guide.

<br>

# Features

| Area | What you get |
|---|---|
| **The explorer** | One layout in the admin app, the desktop app and every embed: a navigation panel, list, grid and gallery views, tabs, personal and team tags, a ⌘K command palette, remappable keyboard shortcuts, new documents from the **+ New** menu and themes an operator composes ([Integration](docs/INTEGRATION.md), [Themes](docs/INTEGRATION.md#themes)) |
| **Storage** | Local disks, S3, FTP, SFTP, WebDAV and SMB/NAS mounted side by side, copy and move across them, and a plugin for anything else, from the admin panel or an app store ([Storage](docs/STORAGE.md), [Storage plugins](docs/PLUGINS.md)) |
| **Without a browser** | The same tree served as S3, SFTP, FTPS, NFSv3 and WebDAV - a move or delete there is as safe beside a storage scan as one in the web UI - plus `filex mount` over HTTPS ([Protocols](docs/PROTOCOLS.md)) |
| **Sign-in** | OIDC, LDAP / Active Directory, Windows and Linux accounts, and sign-in attempt limits ([SSO](docs/SSO.md), [LDAP](docs/LDAP.md), [OS accounts](docs/OS-LOGIN.md)) |
| **People and access** | Roles and per-person permissions, groups, per-folder access and native multi-tenancy ([Permissions](docs/PERMISSIONS.md), [Groups](docs/GROUPS.md), [Multi-tenancy](docs/MULTI-TENANCY.md)) |
| **Sharing** | Public links with a PIN, an expiry and a download limit, file requests and share invites by email ([Sharing](docs/SHARING.md)) |
| **Protection** | Trash and version history, optional ClamAV scanning, end-to-end encrypted folders, replication to a second storage (with an initial copy of what is already there) and an audit log ([Trash & versioning](docs/TRASH-VERSIONING.md), [Protection](docs/PROTECTION.md), [E2E encryption](docs/E2E-ENCRYPTION.md), [Replication](docs/REPLICATION.md), [Audit log](docs/BACKEND.md#admin-audit-log)) |
| **Viewers and editors** | Images, video, audio, PDF, Markdown, code (Monaco), Office documents in the office editor app or through ONLYOFFICE, draw.io diagrams, Mermaid diagrams drawn and edited as text, 3D models, and archives in ZIP, 7z and TAR ([ONLYOFFICE](docs/ONLYOFFICE.md), [Archives](docs/ARCHIVES.md)) |
| **Search and thumbnails** | Embedded full-text search that respects permissions, and thumbnails for images, video, PDF, folders and, with ONLYOFFICE connected, Office documents ([Search](docs/SEARCH.md), [Thumbnails](docs/thumbnails.md)) |
| **Real-time** | Presence and live file updates over WebSocket, and editing a file together in an app, through filex's relay ([Realtime](docs/REALTIME.md), [Editing together](docs/APP-PLUGINS-API.md#editing-together-coedit-056)) |
| **Clients** | A desktop app with folder sync, a web app a phone installs under your instance's name and logo, a CLI, Vue / React / web component embeds, a REST API and an MCP server ([Desktop](docs/DESKTOP.md), [Sync](docs/SYNC.md), [CLI](docs/CLI.md), [Integration](docs/INTEGRATION.md), [MCP](docs/MCP.md)) |
| **Apps and languages** | Sandboxed apps (e-Signature, Convert, the office editor, filextext, draw.io) that print through filex, edit a file together through its relay and open a file of an encrypted folder only when you allow it, decrypted in your browser; language packs and a right-to-left layout ([Apps](docs/APP-PLUGINS.md), [Editing in an app](docs/E2E-ENCRYPTION.md#editing-in-an-app), [RTL](docs/RTL.md)) |
| **Running it** | One binary or one container; SQLite, PostgreSQL or MySQL; an admin panel with one search for every page, setting and record; webhooks, an in-app bell and Web Push to phones and browsers, with an optional digest; a usage and cost view (Backblaze B2 today); update checks, with patch releases that install themselves once you allow it ([Databases](docs/DATABASES.md), [Admin panel](docs/ADMIN-PANEL.md), [Notifications](docs/NOTIFICATIONS.md), [Usage](docs/USAGE.md), [Updates](docs/UPDATES.md)) |

Every feature as its own entry - how it behaves, its limits and the page that documents it:
[docs/FEATURES.md](docs/FEATURES.md).

<br>

# Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

<details>
<summary><b>The pieces at a glance</b> - drivers, protocols and packages in one diagram</summary>

```
┌─────────────────────────────────────────────────────────────┐
│  filex (Go binary; image ~62 MB slim / ~241 MB full)        │
├─────────────────────────────────────────────────────────────┤
│  HTTP API (chi)  │  Admin UI (Vue 3, embedded)              │
│  Auth Drivers:   │  local · oidc · ldap · proxy-header      │
│                  │  pam · windows (OS accounts)             │
│  Sign-in guard:  │  attempt limits · IP allow-list · realms │
│  Storage Drivers:│  local · s3 · ftp · sftp · webdav · smb  │
│  Served as:      │  s3 · sftp · ftps · nfs · webdav         │
│  DB Drivers:     │  sqlite (default) · mysql · postgres     │
│  Queue Drivers:  │  follows the DB · redis                  │
│  Realtime:       │  WebSocket presence + live updates       │
│  RBAC:           │  roles + groups + grants + share invites │
│  AI / MCP:       │  /api/ai REST + native MCP server        │
│  Sync Worker:    │  etag / size+mtime diff + tombstone      │
│  Replica Layer:  │  primary→replica + rules + reconcile     │
│  Protection:     │  trash + versions + ClamAV (bin/clamd)   │
│  E2E folders:    │  client-side WebCrypto (server blind)    │
│  Co-editing:     │  sealed relay for apps · watchers        │
│  Notifications:  │  webhook + in-app bell + read/unread     │
│  Search:         │  Bleve (full-text, embedded)             │
│  Thumbnails:     │  image · svg · video · pdf · office      │
│                  │  heic · text · zip · folders · apps      │
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

</details>

<br>

# How filex is built

filex has one maintainer, who builds it with AI coding agents (Claude Code). The maintainer
decides what filex is and how it works: the architecture, the data and security models, and
how each feature behaves. The agents write most of the code inside that direction, in
languages the maintainer works in. About 82% of the commits in the development repository
carry a `Co-Authored-By: Claude` line, so this is not hidden.

It is AI-assisted, not unreviewed generated code:

- Every change goes through the test chain before it lands: Go tests (also under the race
  detector), about 500 Vitest files, about 120 Playwright specs on Chromium, Firefox and
  WebKit, Cypress, and database tests on SQLite, PostgreSQL and MySQL. Every release is
  tagged only after GitHub's full matrix and a dry run of the release workflow pass on the
  exact commit.
- Changes to sign-in, permissions, sharing and encryption get extra review passes, and a
  red test is fixed, not loosened.
- Security reports are handled in the open: see [SECURITY.md](SECURITY.md) and the
  published advisories. There has been no independent audit yet.
- The German, Spanish, French and Chinese READMEs and the German, Spanish and French
  language packs are machine translations that no native speaker has reviewed yet.

# Contributing

[![CI](https://img.shields.io/github/actions/workflow/status/BRF-Tech/filex/ci.yml?branch=main&label=ci)](https://github.com/BRF-Tech/filex/actions)

Issues and pull requests are welcome; before a sizeable pull request, open an issue that
says what you are about to do. [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) has the
workflow, the tests a change has to pass and the documentation rules, and the project follows
the [Contributor Covenant](CODE_OF_CONDUCT.md). A security problem is reported privately, as
[SECURITY.md](SECURITY.md) describes. What changed in each release is in
[CHANGELOG.md](CHANGELOG.md). A release is cut every day at 10:00 Istanbul time from what
`main` holds then, when it holds something new; a pull request merged after the cut ships
with the next day's ([Release process](docs/CONTRIBUTING.md#release-process)).

<details>
<summary><b>Development</b> - build from source, and what lives where</summary>

```bash
git clone https://github.com/BRF-Tech/filex.git
cd filex
pnpm install
pnpm run build:all    # builds packages, web, then Go binary
./bin/filex serve
```

Subdirectories:
- `backend/` - Go HTTP service (cmd/filex, internal/*, db/queries, db/migrations)
- `packages/core` - `@brftech/filex-core` (Vue 3 SFC, source of truth)
- `packages/webcomponent` - `@brftech/filex` (Web Component wrapper)
- `packages/react` - `@brftech/filex-react` (React adapter via @lit/react)
- `web/` - Vue 3 admin UI (embedded into Go binary via `go:embed`)
- `desktop/` - Electron app (bundled main process, tray sync, auto-update)
- `demo/` - Standalone HTML demos for each framework
- `e2e/` - Playwright suites (web, embeds, packaged desktop app) + `shots/`, the
  scripts `pnpm shots` runs to take the screenshots above (published on filex.sh,
  not kept in the repository)
- `docker/` - Dockerfiles + compose
- `deploy/` - ready-made Compose stacks + Helm chart (see [`deploy/`](deploy/))
- `docs/` - Markdown documentation
- `docs-site/` - VitePress site published at [docs.filex.sh](https://docs.filex.sh)

</details>

<br>

# License

MIT - see [LICENSE](LICENSE).

The store badges in [`docs/badges/`](docs/badges/) are the stores' own artwork, used
unmodified and not covered by that licence: Microsoft and the Microsoft Store badge are
trademarks of the Microsoft group of companies; the Snap Store badge is © Canonical Ltd.,
licensed [CC BY-ND 2.0 UK](https://creativecommons.org/licenses/by-nd/2.0/uk/).

Nextcloud, Dropbox, Google Drive, File Browser and ONLYOFFICE are the names of other
parties' products, used here only to describe them. filex is not affiliated with, sponsored
by or endorsed by any of them.
