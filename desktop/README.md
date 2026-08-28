# filex desktop (Electron)

The filex explorer as a desktop app: a window, a tray icon, and a sync engine
that keeps local folders in step with the server in the background.

What it is **not** is the admin panel in a frame. The window embeds
`<filex-explorer>` — the same web component every other surface embeds — and the
app adds the four things a browser tab cannot do:

1. **Several accounts at once.** A rail down the left switches between servers
   (and tenants); each keeps its own token, branding and sync pairs.
2. **A durable session, kept out of plaintext.** Sign-in happens in your real
   browser (PKCE, `src/browser-auth.ts`), and the resulting token is stored
   through the OS keychain (`safeStorage`). If the keychain is unavailable the
   app **refuses to store the token** rather than writing it to disk.
3. **Folder sync in the background.** `filex sync run --watch` runs per account,
   supervised by the app and shipped inside it (`build/bin/filex`), so the app
   and a terminal act on one implementation and one pairing file.
4. **It keeps itself up to date, quietly.** See *Updates* below.

Settings also opens a full-screen **Connections** surface, and that too is the
shared component (`<filex-connections>`) rather than an app-specific screen: it
manages the account's storages and mints the credentials for reaching the same
server *without* a browser — S3 access keys, SSH keys for SFTP, NFS exports and
the API tokens FTPS, WebDAV and `filex mount` sign in with. ⚠ On Windows the
bundled CLI also mounts a real drive letter (`filex mount Z:`, needs the free
[WinFsp](https://winfsp.dev)); see
[docs/PROTOCOLS.md](../docs/PROTOCOLS.md).

## Layout

| Path | Role |
|------|------|
| `src/main.ts` | Electron main: windows, tray, `app://` protocol, IPC, updates, login item |
| `src/accounts.ts` | Accounts + settings, `safeStorage`-encrypted at `<userData>/desktop-state.bin` |
| `src/browser-auth.ts` | Browser sign-in (PKCE) + deep-link/manual code exchange |
| `src/sync.ts` | Supervises one `filex sync run --watch` per account; pairs, trash, status |
| `src/preload-app.cts` | The window's only bridge: `window.filexApp` (state, settings, sync, updates) |
| `src/preload-shell.cts` | The narrower bridge for the chrome (rail/settings) |
| `ui/app.html` | The app's own chrome — rail, settings, boot screens, string table |
| `scripts/sync-web.mjs` | Copies the built explorer bundle → `app/` for embedding |
| `scripts/fetch-cli.mjs` | Puts the `filex` CLI into `build/bin` (fails the build if missing) |
| `scripts/build-main.mjs` | Bundles the main process with esbuild (see *Packaging traps*) |
| `electron-builder.yml` | Windows / Linux / macOS packaging + the update feed |

## Build & run

```bash
# from the repo root — the explorer bundle has to exist first
pnpm run build:packages
cd desktop
pnpm run build      # sync the web bundle + bundle the main process
pnpm run dev        # build, then run it

# installers (unsigned)
pnpm run dist:win
pnpm run dist:linux     # .deb + AppImage
pnpm run dist:mac       # .dmg + .zip — host arch (arm64 on Apple Silicon), ad-hoc sealed
```

`FILEX_CLI_BIN=<path>` points `fetch-cli.mjs` at an already-built CLI instead of
compiling one; give it a binary of the **same version** you are packaging, built
without the embedded server UI (85 MB of admin SPA the app already ships in
`app/`).

## Updates

The app updates itself and nobody is asked about it: it checks a few times a
day, downloads in the background, and installs at a moment that costs nothing —
when you quit, or once the machine has been idle for ten minutes with no window
open — then comes back in the tray. The sync watchers are stopped before the
swap, so an update never lands mid-transfer.

Two things make that possible, and both are easy to undo by accident:

- **Every install call must be silent.** `autoUpdater.quitAndInstall()` defaults
  to `isSilent = false`, which runs the NSIS installer with its full wizard.
  Guarded by `scripts/update-e2e.mjs`.
- **The Windows app installs per-user.** An install under `C:\Program Files`
  needs administrator rights to replace its own files, so every background
  update would stop at a UAC prompt. `electron-builder.yml` therefore pins
  `perMachine: false` + `allowElevation: false`.

The feed is a plain static directory on filex.sh, not the GitHub provider: this
repo's mirror is private, and that provider would need a token shipped inside
the app. `FILEX_NO_UPDATE=1` turns the whole thing off.

## Language

*Settings → Language* — System / English / Türkçe, stored in the app state. One
resolver in the main process decides what "system" means, because three surfaces
read it: this window, the tray menu (main process) and the explorer inside it (a
separate component with its own catalogue). Covered by `scripts/lang-e2e.mjs`.

## Security posture (do not loosen)

`contextIsolation: true`, `nodeIntegration: false`, `sandbox: true`. The window
only ever loads `app://`; external links open in the OS browser; the preloads
expose the narrowest surface that works. Tokens live in the OS keychain.

## Signing

Releases are **unsigned** by design for now — Windows SmartScreen and macOS
Gatekeeper will warn. A code-signing certificate is a separate, paid decision,
not a defect.

macOS specifics, because the failure mode there is not a warning but a wall:

- A no-certificate electron-builder output is only *linker-signed*, and macOS
  26 treats that half-signature on a downloaded app as tampering: **"malware
  blocked and moved to Trash"**, no override offered. `scripts/adhoc-sign.cjs`
  (an `afterPack` hook) therefore re-seals the bundle with a deep **ad-hoc**
  signature, which downgrades the verdict to the honest "unverified developer"
  dialog.
- First launch of a downloaded copy: macOS blocks once — open **System
  Settings → Privacy & Security → Open Anyway** (or right-click → Open on
  older versions). A locally built copy has no quarantine flag and just opens.
- **Self-update is impossible on macOS until real signing lands**: Squirrel.Mac
  refuses to swap an app without a Developer ID signature, and electron-updater
  finds that out only *after* the download. So the build reads its own
  signature once at startup (`codesign -dv`, `main.ts`) and, when it is the
  ad-hoc one, never wires the auto-updater at all: it reads `latest-mac.yml`
  from the feed on the same cadence and Settings offers the new version's
  `.dmg` as a **Download** button, rather than announcing an install it cannot
  perform. The `zip` target and `latest-mac.yml` ship anyway so the feed is
  already correct the day a Developer ID certificate (and notarization)
  arrives — which is the actual fix for all of the above.

## Packaging traps (each of these shipped once)

- **`files:` must list `node_modules/**/*`.** An explicit list replaces the
  default, and the default is what pulls dependencies in. Without it
  `electron-updater` was simply absent from the asar: the installer built, the
  app launched, and no window ever appeared. The main process is bundled with
  esbuild now so the package carries its own code either way.
- **`fetch-cli.mjs` fails the build when the CLI is missing** rather than
  producing a package that looks armed and syncs nothing.
- **Artifact names carry no version** (`filex-desktop-x64.exe`): the download
  links point at `releases/latest/download/<name>`, which only resolves for a
  fixed filename.

## End-to-end suites

Each drives the real app with Playwright (`scripts/lib/harness.mjs`); most need
a server and credentials (`FILEX_SERVER`, `FILEX_EMAIL`, `FILEX_PASSWORD`), and
`FILEX_APP_BINARY` points them at a packaged build instead of the source tree.

| Script | What it proves |
|---|---|
| `ui-login-e2e.mjs` | Browser sign-in end to end, including the manual-code fallback |
| `chrome-e2e.mjs` | The app's own chrome: rail, tabs, theme, scrollbars |
| `files-e2e.mjs` · `share-e2e.mjs` | Listing, upload, preview; share links |
| `share-limit-e2e.mjs` | A capped link hands out exactly that many downloads |
| `sync-e2e.mjs` | A paired folder actually syncs, both directions |
| `update-e2e.mjs` | The updater downloads and stages a newer version — and installs silently |
| `lang-e2e.mjs` | The language setting moves the shell, the file list and the stored state |
| `shell-e2e.mjs` | The shell windows (settings, pickers) open and answer |
| `dragout-e2e.mjs` | Dragging files OUT: what lands on this computer before an OS drag can start |
| `plumbing-smoke.mjs` | `app://`, preload injection and `safeStorage`, without a server |

⚠ `dragout-e2e.mjs` measures the **preparation** — which bytes reach the disk, a
folder's subtree, the cache making the second drag free, and that an unprepared
selection is refused. It never calls `dragStart` on a valid selection: that
opens the OS drag loop, which cannot be driven from a script and would leave a
modal drag hanging off the machine's mouse. Letting go over the desktop is the
one step a human has to do.

`look*.mjs` and `diag-*.mjs` are not suites — they open the app and take a
screenshot of one surface, for looking at a change rather than asserting it.
