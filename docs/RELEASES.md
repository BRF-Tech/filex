---
title: Releases
description: Every filex release with a plain-English summary of what changed — generated from the GitHub releases at build time.
---

<!-- GENERATED FILE — do not edit by hand. Your edits will be overwritten.
     Source:      GitHub Releases for BRF-Tech/filex
     Generator:   docs-site/scripts/fetch-releases.mjs
     Summaries:   docs-site/data/release-highlights.json (hand-written)
     Regenerate:  cd docs-site && npm run releases -->

# Releases

Every published filex release, newest first. This page is generated from the
[GitHub releases](https://github.com/BRF-Tech/filex/releases) each time the site is built, so a new
tag shows up here without anyone writing it twice.

Whether filex installs a release by itself depends on which part of the version moved —
see [Updates](./UPDATES.md).

::: tip Latest — v0.23.0, 20 August 2026
The rest of “keep on this computer”, the same day it shipped. Every row now says where it lives — ✓ on this computer, ◐ holding kept items below, ⟳ syncing right now, ☁ online-only — and a strip along the bottom of the window shows what the engine is doing while it does it. Single files can be kept, not just folders. The filex folder itself is now visible in Settings and can be moved, kept folders and all: a move to another drive is copied across rather than failing, a folder inside the current one is refused rather than half-applied, and if anything goes wrong the pair goes back where its files are instead of quietly unpairing. Unkeeping also stops leaving an empty folder skeleton behind — including one holding nothing but the .DS_Store the Finder dropped in it.
:::

```bash
docker pull ghcr.io/brf-tech/filex:slim-v0.23.0
docker pull ghcr.io/brf-tech/filex:full-v0.23.0
```

## v0.23.0

<span class="filex-release-date">20 August 2026</span>

The rest of “keep on this computer”, the same day it shipped. Every row now says where it lives — ✓ on this computer, ◐ holding kept items below, ⟳ syncing right now, ☁ online-only — and a strip along the bottom of the window shows what the engine is doing while it does it. Single files can be kept, not just folders. The filex folder itself is now visible in Settings and can be moved, kept folders and all: a move to another drive is copied across rather than failing, a folder inside the current one is refused rather than half-applied, and if anything goes wrong the pair goes back where its files are instead of quietly unpairing. Unkeeping also stops leaving an empty folder skeleton behind — including one holding nothing but the .DS_Store the Finder dropped in it.

**New**

- **Core** — A folder holding kept items says so — the partial badge.
- **Core** — Availability badges on every row, and a live progress strip for the folder being synced.
- **Core** — Folder menus offer keeping a folder on this computer when a desktop shell mounts the explorer.
- **Desktop** — Settings shows the mirror root and can move it.
- **Desktop** — Files can be kept too, and online-only sweeps the empty skeleton.
- **Desktop** — Selective sync — one root folder, keep and unkeep from the explorer menu.
- **Desktop** — The supervisor parses engine progress into typed per-account state.
- **Sync** — Single-file pairs — keep one file, not its whole folder.

**Fixed**

- **Desktop** — Mirror-root containment goes through isInsideDir everywhere.
- **Desktop** — The skeleton sweep is not defeated by .DS_Store, and a migrated file pair stays a file pair.
- **Desktop** — Unpairing cannot leave a ghost watcher, and the ad-hoc mac build stops promising self-updates.
- **Sync** — A first sync you can see, cancel safely, and add pairs to while it runs.

**Other changes**

- Merge origin/main (v0.22.0) — keep the maintainer's security guard, dialog helpers and reveal climb under the polish work.
- **Changelog** — Availability badges and the progress strip.
- **Changelog** — Keep on this computer.
- **Changelog** — The desktop sync findings, under Unreleased.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.23.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.23.0`

## v0.22.0

<span class="filex-release-date">20 August 2026</span>

Folders you also want on the computer are now one right-click away. “Keep on this computer” mirrors a server folder — or a whole storage — under a single filex folder chosen once per account, while everything else stays online-only in the window; kept folders offer the way back, and unkeeping asks whether the local copy goes to the Trash or stays. Sync also stopped looking dead on a big first run: it reports what it is doing even in quiet mode, a folder paired while the app runs is picked up without a restart, and unpairing one mid-first-sync no longer fails or leaves a background process syncing a folder you removed. The macOS build stopped promising a self-update it cannot perform and offers the download instead. Security: a server can no longer name a local folder outside the one you chose — a remote path containing “..” is refused rather than resolved.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.22.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.22.0`

## v0.21.6

<span class="filex-release-date">19 August 2026</span>

Two decisions about noise and blast radius. A demo instance no longer accepts any new storage backend — 0.21.4 stopped it reaching the server's own filesystem, and now the remote drivers go too, because "attach your own bucket" asks the server to connect wherever a stranger points it. And a single failed sync run is no longer reported as an error: an object store answering 504 under load costs one skipped refresh, not an incident. Three failures in a row still is one.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.21.6) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.21.6`

## v0.21.5

<span class="filex-release-date">19 August 2026</span>

Plugins no longer outlive filex on Windows. If filex was stopped without a chance to clean up — a crash, a hard kill, a service restart — every plugin it had launched kept running; and since a running plugin holds its own .exe open, the next install or upgrade of that plugin failed with a sharing violation. Plugins now live in a job object, so the kernel reaps them with filex.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.21.5) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.21.5`

## v0.21.4

<span class="filex-release-date">19 August 2026</span>

The other half of the demo hardening in 0.21.3. A demo instance publishes an admin login, and the `local` storage driver means "a path on this host" — so on a demo it was possible to add a storage rooted at /data, /etc or /proc/1 and read the machine's database, configuration and process environment. Measured, then closed: demo mode refuses the local driver. Backends a visitor brings (S3, SFTP, WebDAV, SMB, plugins) still work, because a demo where nothing connects is not a demo.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.21.4) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.21.4`

## v0.21.3

<span class="filex-release-date">19 August 2026</span>

A security default, found by measuring the project's own public demo. A demo instance publishes an admin login — that is what a demo is for — and the plugin API is admin-only, so on a demo "admin-only" means anybody, and installing a plugin makes filex run an uploaded program on the host. Demo mode now turns the plugin subsystem off unless you explicitly say otherwise. If you run a filex demo on 0.21.0–0.21.2, set FILEX_PLUGINS_DISABLED=1 or upgrade.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.21.3) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.21.3`

## v0.21.2

<span class="filex-release-date">19 August 2026</span>

A plugin now has to PROVE what it claims before filex will use it. Every capability a plugin declares is probed — at install against the plugin's own throwaway area, and again when you save a storage on it — and one that fails its own claims is refused rather than registered. That is the difference between a plugin that is broken and an app that looks broken: without it, a plugin declaring `write` with a broken write hands the user an upload button, a trash move and a version snapshot that each fail at the last moment. Plugins can also be upgraded in place now (a failed upgrade rolls back and costs you an error, not a plugin), their change streams are actually consumed instead of polled, and there is a ceiling on what one plugin may cost the server. Three things that were written but unreachable are wired up: signature enforcement (`FILEX_PLUGIN_TRUSTED_KEYS`) could not be switched on at all, the concurrency ceiling had no setting, and a rejected upload was reported as a server error.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.21.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.21.2`

## v0.21.1

<span class="filex-release-date">19 August 2026</span>

Pasting a file into the top level of a storage failed — on every driver, not just the new plugins: the explorer asks for the storage root, and the operations queue read that as "no destination given". It works now, and it checks permissions on the way in, which the root case had been skipping. The plugin documentation also stopped promising something it does not do: a plugin can serve a change stream, but nothing in filex subscribes to one yet. Plus a second example plugin, written in Python without the Go SDK, and the acceptance run that drives it — including killing it mid-transfer to show that a storage survives its plugin crashing.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.21.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.21.1`

## v0.21.0

<span class="filex-release-date">19 August 2026</span>

filex can now be taught a storage it has never heard of. A plugin is a separate program — in any language — that filex launches (or connects to) and speaks a small HTTP/JSON protocol to; once it is running, its driver appears in the ordinary storage picker with the config form the plugin itself describes, and behaves like any built-in one. Whatever a plugin does not implement is either emulated (ranged reads, move, copy) or honestly absent, so a read-only plugin never shows an upload button that fails at the last moment. Writing one in Go is three methods and a Serve call, and the example plugin in the repository is built and driven by filex's own tests. Install by upload, by URL with a required checksum, or point filex at a service you run yourself.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.21.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.21.0`

## v0.20.3

<span class="filex-release-date">18 August 2026</span>

The desktop app now ships for macOS (Apple Silicon) — unsigned but ad-hoc sealed, so first launch is the ordinary Open Anyway step rather than a refusal; auto-update on the Mac waits for a signed build. Two desktop fixes from the same real-world install: pairing now works from a browser that is already signed in, and the sync engine inside the Mac app is arm64 (no more Rosetta alert on every launch). The Connections page stopped painting its own background over the admin page in dark mode, and the API-tokens box on it got real theme colours.

**Fixed**

- **Core** — The connections panel painted its own page ground; five theme tokens did not exist.
- **Desktop** — Compile the embedded CLI for the host arch, not amd64.
- **Release** — The pull command names ghcr.io, not a registry that does not exist.
- **Web** — Desktop pairing survives an already-signed-in browser.
- **Docs-site** — The release cron never rebuilt — no PATH under cron, and two guards.

**Other changes**

- **Desktop** — Ship macOS packages — dmg + zip, ad-hoc sealed.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.20.3) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.20.3`

## v0.20.2

<span class="filex-release-date">17 August 2026</span>

**Other changes**

- **Desktop** — The tag is the version, not desktop/package.json.
- **Desktop** — Wait for the release before uploading to it.
- Release: v0.20.2 — desktop packaging fixes take effect.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.20.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.20.2`

## v0.20.1

<span class="filex-release-date">17 August 2026</span>

**Other changes**

- Release: v0.20.1 — FTPS starts when you give it a host name.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.20.1) · `ghcr.io/brf-tech/filex:slim-v0.20.1`

## v0.20.0

<span class="filex-release-date">17 August 2026</span>

filex is now reachable without a browser. It could already use S3, SFTP, FTP and WebDAV as storages; now those clients can point at filex itself — as an S3 endpoint, an SFTP server, an FTPS server and an NFSv3 export, alongside the WebDAV it already had. rclone, restic, aws s3, WinSCP, FileZilla, a scanner that only ever learned FTP and a media player that only ever learned NFS all land in the same tree, with the same permissions, the same trash and the same quota as the web UI. Each has a credential you can revoke on its own, and revoking one now cuts the session it already opened rather than only the next login. Off-LAN there is `filex mount`, which attaches a remote server to a folder over ordinary HTTPS — not a sync: it opens one file out of a hundred thousand without downloading the rest. A NAS can also be a storage now, over SMB. And three quieter fixes with teeth: a folder grant was unreachable over the new protocols, /dav enforced no quota at all, and WebDAV locks vanished on every restart while still telling clients they were exclusive.

**Other changes**

- **Desktop** — The package README described an app that no longer exists.
- Release: v0.20.0 — the protocol gateway.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.20.0) · `ghcr.io/brf-tech/filex:slim-v0.20.0`

## v0.19.0

<span class="filex-release-date">14 August 2026</span>

The desktop app gets a language setting — System, English or Türkçe — where before it followed the operating system and offered nothing to choose. The choice moves the whole app at once: the window, the tray menu built by a different process, and the file explorer inside it, which is a separate component with its own catalogue. Switching is immediate and keeps the folder you are looking at.

**New**

- **Desktop** — Choose the app's language.

**Other changes**

- **Desktop** — Say how the update actually behaves, and why per-user matters.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.19.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.19.0`

## v0.18.2

<span class="filex-release-date">14 August 2026</span>

The Windows app installs per-user now, and that is what makes the quiet update in 0.18.1 real: an app under C:Program Files needs administrator rights to replace its own files, so every background update ended in a UAC prompt — it could never update itself while nobody was at the machine. The installer no longer offers the all-users choice, because one click on it put the app somewhere its own updater could not reach. Settings and accounts live outside the install directory, so moving an existing install loses nothing.

**Fixed**

- **Desktop** — Install per-user, because a Program Files install cannot update itself.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.18.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.18.2`

## v0.18.1

<span class="filex-release-date">14 August 2026</span>

The desktop app updates itself the way it always should have. Downloading was quiet and quitting installed silently, but the tray entry and the Settings button ran the installer with its wizard — so the one visible path through the feature was the one that made a background updater feel like being sent back through setup. Every install is silent now, and the app comes back in the tray rather than throwing a window at you. It also stops waiting for a quit that may never come: an app that lives in the tray applies the update at a quiet moment — ten minutes idle, no window open — after stopping its sync watchers so nothing is interrupted.

**Fixed**

- **Desktop** — The update stops handing you an installer window.

**Other changes**

- **Release** — Publish an unversioned linux binary, so the documented install works.
- **Contributing** — The screenshot step's install command needs `run`

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.18.1) · `ghcr.io/brf-tech/filex:slim-v0.18.1`

## v0.18.0

<span class="filex-release-date">14 August 2026</span>

Share links keep their word, and you get a face. A link capped at three downloads could hand out four: the cap was checked against a counter that was only bumped after the bytes had left, so any request that started while an earlier one was still streaming read the same old count and was let through — measured on a live instance, a one-download link served three complete files to three overlapping clients. A download is now claimed before anything is served. The share dialog also gets its one-line curl back (a link is often made for a server, and that reader has no browser) and its Create link button stops being pushed out of place by the download-limit control. New: profile pictures — set one on your profile and the collaboration bar shows it instead of your initials, for every client of the account, the desktop app and API keys included.

**New**

- **Profile** — A profile picture, shown wherever that account is present.

**Fixed**

- **Share** — A download cap that could be exceeded, and a dialog that lost its curl.

**Other changes**

- **Screenshots** — Retake them in English, and make checking them a release step.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.18.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.18.0`

## v0.17.1

<span class="filex-release-date">12 August 2026</span>

A packaging fix, and the first release whose tag matches what ships. The desktop package could be built without the updater inside it: in a pnpm workspace the dependency is a symlink pointing outside the app directory and electron-builder does not follow those, so the app launched and simply never opened a window — no dialog, no log. The main process is bundled now, so the package carries its own code and nothing else. There is also a suite that points a packaged app at a feed advertising a newer version and watches it download and stage the update, because "the installer built" is not "the app works".

**Fixed**

- **Desktop** — The package shipped without its updater, and nothing said so.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.17.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.17.1`

## v0.17.0

<span class="filex-release-date">12 August 2026</span>

The desktop app keeps itself up to date. It checks a static feed on filex.sh (not GitHub — that mirror is private and the provider would need a token inside the app), downloads quietly, and installs when you quit, because an update that interrupts a file transfer to restart itself is worse than one that waits. The tray offers "Restart to update" once one is staged and Settings gained an Updates row. Failures stay silent while the app works. Note that this is the first version that can update itself: reaching it still takes one manual install.

**New**

- **Desktop** — The app keeps itself up to date.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.17.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.17.0`

## v0.16.3

<span class="filex-release-date">12 August 2026</span>

The tab strip, fixed properly. It was permanent in the desktop app and came and went on the web, because 0.16.0 gave the two surfaces different defaults — this package exists so they are one product, so the default is now the same everywhere. A scrollbar had appeared across the 30px row of tabs (a themed-scrollbar rule added at the end of the stylesheet outranked the strip's own), the strip had grown a vertical scrollbar it could never use, and — the real one — enough tabs did not overflow the strip but GREW it, pushing the whole layout off the right edge with no scrollbar anywhere: an embedded custom element is nearly always a flex item, and a flex item will not shrink below its content unless told to. Also: "new tab" no longer scrolls away with the tabs it creates.

**Fixed**

- **Tabs** — The strip differed between surfaces, wore a scrollbar it should not have, and grew instead of scrolling.

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.16.3) · `ghcr.io/brf-tech/filex:slim-v0.16.3`

## Earlier releases

The 59 releases before v0.16.3, in brief. Full notes are on GitHub.

| Version | Date | What changed |
|---|---|---|
| [v0.16.2](https://github.com/BRF-Tech/filex/releases/tag/v0.16.2) | 12 August 2026 | Two things a share link got wrong. It changed language when you entered its PIN — the gate was English, the page behind it Turkish, and the screen in between managed both at once. |
| [v0.16.1](https://github.com/BRF-Tech/filex/releases/tag/v0.16.1) | 12 August 2026 | Follow-up to 0.16.0: a shared folder's gallery tiles are now rendered when the link is created rather than when the first visitor arrives, so the first open is fast too — which is the open that matters, since whoever creates a… |
| [v0.16.0](https://github.com/BRF-Tech/filex/releases/tag/v0.16.0) | 12 August 2026 | Two things a shared folder was doing the slow way. Its gallery tiles were the original photos — the page asked for a thumbnail and the server streamed the whole file — so a folder of a few dozen photos shipped tens of megabytes… |
| [v0.15.1](https://github.com/BRF-Tech/filex/releases/tag/v0.15.1) | 12 August 2026 | The desktop app's account rail had its two identities the wrong way round. Each row of that rail is a server — a tenant — so it now carries that server's own Branding logo, which is a better label than initials taken from an… |
| [v0.15.0](https://github.com/BRF-Tech/filex/releases/tag/v0.15.0) | 12 August 2026 | Mostly the desktop app, and one thing that was writing to your operating system. "Start when I sign in" registered whichever executable happened to be running the app — which, for anyone who had ever run it from source, was a… |
| [v0.14.0](https://github.com/BRF-Tech/filex/releases/tag/v0.14.0) | 10 August 2026 | The desktop app can reach its own server again. Opening any office document showed "Config fetch 401", starred files and recently-opened were silently empty, and "Open in new tab" did nothing at all — one cause under the first… |
| [v0.13.4](https://github.com/BRF-Tech/filex/releases/tag/v0.13.4) | 10 August 2026 | A fix for a disk that fills up on its own. Uploads larger than 32 MiB are buffered to a temporary file, and those files were never removed — every request answered normally while the disk quietly drained (29 GB in two hours on a… |
| [v0.13.3](https://github.com/BRF-Tech/filex/releases/tag/v0.13.3) | 7 August 2026 | The share button now appears in the desktop app. The explorer has always had one, but it was gated on the Web Share API, which Electron does not ship — so rather than build a second share UI, the app puts a native handler behind… |
| [v0.13.2](https://github.com/BRF-Tech/filex/releases/tag/v0.13.2) | 7 August 2026 | Sixteen components were rendering as raw, unstyled HTML in every embedded surface — the share dialog, the convert dialog, the presence bar and nine file viewers. |
| [v0.13.1](https://github.com/BRF-Tech/filex/releases/tag/v0.13.1) | 7 August 2026 | The explorer's onboarding tour sat on top of the desktop app's Settings panel. The tour attaches to `<body>`, so hiding the explorer left it exactly where it was. |
| [v0.13.0](https://github.com/BRF-Tech/filex/releases/tag/v0.13.0) | 7 August 2026 | The desktop app is a file manager, not an admin console. Signing in used to land you on the server's dashboard because the shell embedded the whole admin SPA; it now shows the file explorer, an account rail down the left, and a… |
| [v0.12.0](https://github.com/BRF-Tech/filex/releases/tag/v0.12.0) | 7 August 2026 | Selective folder sync. A folder on your computer and a folder on a filex server are kept in step in both directions, in the background. |
| [v0.11.0](https://github.com/BRF-Tech/filex/releases/tag/v0.11.0) | 7 August 2026 | The desktop app, for Windows and Linux. It runs the same web UI this repo already ships, and sign-in happens in your browser — the app opens the server's own login page and waits, so installs behind an identity provider (OIDC,… |
| [v0.10.2](https://github.com/BRF-Tech/filex/releases/tag/v0.10.2) | 6 August 2026 | Completes the guard added in 0.10.1: a sweep found four more places that could still write a file onto a folder — archive extraction and creation, the OnlyOffice save-back, and version restore — plus replication, where a… |
| [v0.10.0](https://github.com/BRF-Tech/filex/releases/tag/v0.10.0) | 6 August 2026 | Two explorer changes reported by a deployment whose users mount WebDAV from macOS. Dot-prefixed files can now be shown or hidden and are hidden by default (a Mac leaves `.DS_Store` and `._name` litter in every folder it opens),… |
| [v0.9.0](https://github.com/BRF-Tech/filex/releases/tag/v0.9.0) | 6 August 2026 | Closes the ten items a multi-tenant deployment filed against v0.8.0. The important one is a security fix: a tenant admin could reach every other tenant's storages over WebDAV, because `/dav` does its own Basic authentication and… |
| [v0.8.0](https://github.com/BRF-Tech/filex/releases/tag/v0.8.0) | 29 July 2026 | filex now knows which releases exist, and can install them. What it does is decided by which part of the version moved: a patch applies itself when the policy allows, a minor is announced and applied with one click, a major is… |
| [v0.7.6](https://github.com/BRF-Tech/filex/releases/tag/v0.7.6) | 29 July 2026 | Denials on the AI/MCP surface answer `403` instead of `500`. A `5xx` reads as "server glitch, retry", so agents and HTTP clients were retrying requests that could never succeed while the real cause — a path outside the token's… |
| [v0.7.5](https://github.com/BRF-Tech/filex/releases/tag/v0.7.5) | 19 July 2026 | An internal refactor with no behaviour change: the storage-scoped path hash had been copy-pasted across nine call sites, so the same file could map to different rows if any copy drifted. |
| [v0.7.4](https://github.com/BRF-Tech/filex/releases/tag/v0.7.4) | 18 July 2026 | Two explorer fixes: the trash bin now appears in the secondary pane of split view (the two panes were offset by a row), and tall listings scroll inside their pane instead of scrolling the whole page. |
| [v0.7.3](https://github.com/BRF-Tech/filex/releases/tag/v0.7.3) | 18 July 2026 | Split view's right-click menu now matches the main panel exactly — it was a shorter, separate list missing rename, delete, share, convert and tags. |
| [v0.7.2](https://github.com/BRF-Tech/filex/releases/tag/v0.7.2) | 18 July 2026 | Split-view polish: the main panel's breadcrumb no longer spans the full width, and right-clicking a row in the secondary pane opens a real menu instead of only selecting the row. |
| [v0.7.1](https://github.com/BRF-Tech/filex/releases/tag/v0.7.1) | 18 July 2026 | A round of layout and accessibility fixes for embedders. The explorer no longer overflows its host by 2px (the outer scrollbar that produced in embeds is gone), the toolbar folds overflowing actions into a `⋯` menu instead of… |
| [v0.7.0](https://github.com/BRF-Tech/filex/releases/tag/v0.7.0) | 17 July 2026 | Three additions. **Branding** — a settings-driven identity (name, logo, accent, footer) for the public share, PIN, file-drop and folder-browse pages plus the admin login, with per-tenant overrides. |
| [v0.6.0](https://github.com/BRF-Tech/filex/releases/tag/v0.6.0) | 17 July 2026 | Tabs and split view. Open several locations as tabs, split the active tab into two panes that navigate independently, and drag files between them to move (same storage) or copy (across storages). |
| [v0.5.0](https://github.com/BRF-Tech/filex/releases/tag/v0.5.0) | 17 July 2026 | A large interface release: eight built-in themes with independent light and dark variants, fully rebindable keyboard shortcuts, Quick Look (peek the selected file with Space), an operations centre that collects uploads and… |
| [v0.4.2](https://github.com/BRF-Tech/filex/releases/tag/v0.4.2) | 17 July 2026 | Cleanup release. Moving a folder to trash no longer wedges storage sync (a trashed folder's leftovers could block sync from ever re-creating those names), `versions.keep_n` above 20 works instead of being silently capped, and… |
| [v0.4.1](https://github.com/BRF-Tech/filex/releases/tag/v0.4.1) | 17 July 2026 | Packaging and documentation. Ready-to-submit app-store manifests for Umbrel, CasaOS, Runtipi, Unraid and Portainer, a refreshed Helm chart, and this documentation site. |
| [v0.4.0](https://github.com/BRF-Tech/filex/releases/tag/v0.4.0) | 17 July 2026 | An inspector panel (press `i`) with metadata, version history, effective permissions and share links for the selected item. |
| [v0.3.0](https://github.com/BRF-Tech/filex/releases/tag/v0.3.0) | 17 July 2026 | Connectivity release. A WebDAV server so you can mount filex as a network drive from Windows, Finder, rclone or davfs2 with full RBAC enforcement; a `filex client` CLI against any remote filex; and multiple webhook targets with… |
| [v0.2.0](https://github.com/BRF-Tech/filex/releases/tag/v0.2.0) | 17 July 2026 | Content search. filex now indexes what is inside your files — text, source code, CSV/JSON/YAML, PDF text layers and Office documents — extracted asynchronously into an embedded index, with highlighted snippets in the results. |
| [v0.1.84](https://github.com/BRF-Tech/filex/releases/tag/v0.1.84) | 17 July 2026 | A design pass over the explorer: a command palette (`Ctrl/Cmd+K`), a shortcuts sheet sourced from one registry so it cannot drift, sortable and date-grouped list columns, a density toggle, an undo snackbar for rename/move/trash,… |
| [v0.1.83](https://github.com/BRF-Tech/filex/releases/tag/v0.1.83) | 16 July 2026 | Embed — Height-constrained embeds could not scroll. |
| [v0.1.82](https://github.com/BRF-Tech/filex/releases/tag/v0.1.82) | 10 July 2026 | Locale-clean viewer/presence strings, AI-surface thumbnail dispatch, fresh demo landing card (#2 #3 #4) |
| [v0.1.81](https://github.com/BRF-Tech/filex/releases/tag/v0.1.81) | 9 July 2026 | Tokens — Per-token username identities — audit, shares and presence attribution. |
| [v0.1.80](https://github.com/BRF-Tech/filex/releases/tag/v0.1.80) | 9 July 2026 | Core — Authenticated grid thumbnails + expandable presence bar. |
| [v0.1.79](https://github.com/BRF-Tech/filex/releases/tag/v0.1.79) | 9 July 2026 | Ws — Real presence identity + confined-subscribe fixes (v0.1.79) |
| [v0.1.78](https://github.com/BRF-Tech/filex/releases/tag/v0.1.78) | 8 July 2026 | Ws — Resolve embedded confined subscribes to the absolute room + per-client frame paths. |
| [v0.1.77](https://github.com/BRF-Tech/filex/releases/tag/v0.1.77) | 8 July 2026 | Ws — Authorize ticketed subscribes as the ticket's user (RBAC) |
| [v0.1.76](https://github.com/BRF-Tech/filex/releases/tag/v0.1.76) | 8 July 2026 | Ws — Allow ticket-only (embedded/cross-origin) WebSocket connections. |
| [v0.1.75](https://github.com/BRF-Tech/filex/releases/tag/v0.1.75) | 8 July 2026 | Realtime — Embed WS live-collab in the core (ticket auth + polling fallback) |
| [v0.1.74](https://github.com/BRF-Tech/filex/releases/tag/v0.1.74) | 8 July 2026 | Folder-share ZIP cache + warmer + progress page, ShareX uploader, live-collab WebSocket. |
| [v0.1.73](https://github.com/BRF-Tech/filex/releases/tag/v0.1.73) | 7 July 2026 | Share — Add GET /api/files/share list endpoint so the modal lists existing links. |
| [v0.1.72](https://github.com/BRF-Tech/filex/releases/tag/v0.1.72) | 7 July 2026 | Share — Native "Paylaş" (Web Share API) button under the mail row. |
| [v0.1.71](https://github.com/BRF-Tech/filex/releases/tag/v0.1.71) | 7 July 2026 | Explorer — 'folder not found' for dead deep links — phantom prefixes 404, denied dirs render as not-found. |
| [v0.1.70](https://github.com/BRF-Tech/filex/releases/tag/v0.1.70) | 7 July 2026 | Web — Don't double the explorer hash in the login redirect. |
| [v0.1.69](https://github.com/BRF-Tech/filex/releases/tag/v0.1.69) | 6 July 2026 | Explorer — Address bar mirrors the current folder for copy-paste deep links. |
| [v0.1.68](https://github.com/BRF-Tech/filex/releases/tag/v0.1.68) | 6 July 2026 | Auth — OIDC callback lands via 200 HTML bounce so a CDN can't strip Set-Cookie. |
| [v0.1.67](https://github.com/BRF-Tech/filex/releases/tag/v0.1.67) | 5 July 2026 | Auth — Mark session + OIDC state cookies Secure on HTTPS (X-Forwarded-Proto aware) |
| [v0.1.66](https://github.com/BRF-Tech/filex/releases/tag/v0.1.66) | 5 July 2026 | Multi-tenant — OIDC callback redirects to tenant host + per-provider cookie domain. |
| [v0.1.65](https://github.com/BRF-Tech/filex/releases/tag/v0.1.65) | 5 July 2026 | Docker — Multi-arch images (linux/amd64 + linux/arm64) |
| [v0.1.64](https://github.com/BRF-Tech/filex/releases/tag/v0.1.64) | 5 July 2026 | Lint — Drop unused adminIDFiltersIn + gofmt config/storage. |
| [v0.1.63](https://github.com/BRF-Tech/filex/releases/tag/v0.1.63) | 5 July 2026 | Auth — SSO-first login (FILEX_OIDC_AUTO_REDIRECT) + FILEX_COOKIE_DOMAIN. |
| [v0.1.62](https://github.com/BRF-Tech/filex/releases/tag/v0.1.62) | 5 July 2026 | Explorer — Empty file/folder size+date + real Trash size/date/icon. |
| [v0.1.61](https://github.com/BRF-Tech/filex/releases/tag/v0.1.61) | 5 July 2026 | Native multi-tenancy (FILEX_MULTI_TENANT) — provider=tenant, two-layer isolation. |
| [v0.1.60](https://github.com/BRF-Tech/filex/releases/tag/v0.1.60) | 5 July 2026 | Sync — Backfill missing backend_mtime + split npm into its own release job. |
| [v0.1.59](https://github.com/BRF-Tech/filex/releases/tag/v0.1.59) | 5 July 2026 | Folder "last activity" date + full/empty trash icon. |
| [v0.1.58](https://github.com/BRF-Tech/filex/releases/tag/v0.1.58) | 4 July 2026 | Folder size+date in explorer (cached, N+1-free) + FILEX_DEFAULT_LOCALE. |
| [v0.1.57](https://github.com/BRF-Tech/filex/releases/tag/v0.1.57) | 4 July 2026 | Storage — Connect any external storage from env (sftp/webdav/ftp/s3) + fix JSON port. |

---

<small>Generated 2026-08-22 from 79 published releases.</small>
