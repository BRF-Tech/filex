# filex desktop (Electron)

The filex explorer as a desktop app: a window, a tray icon, and a sync engine
that keeps local folders in step with the server in the background.

What it is **not** is the admin panel in a frame. The window embeds
`<filex-explorer>` — the same web component every other surface embeds — and the
app adds the five things a browser tab cannot do:

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
5. **It can be the app that opens a document.** Double-click a `.docx` on the
   disk and it opens in the server's OnlyOffice editor, with the edits written
   back over the local file — see *Open with filex* below. A browser tab cannot
   be a file handler at all.

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
| `src/openwith.ts` | "Open with filex", the parts that can lose a document: argv classification, local path → synced twin, scratch naming, the atomic write-back, the sweeps. No Electron import — that is what makes it testable |
| `src/openwith-io.ts` | The six server calls that round trip does (list, stat, mkdir, upload, download, delete) |
| `src/preload-app.cts` | The window's only bridge: `window.filexApp` (state, settings, sync, updates) |
| `src/preload-shell.cts` | The narrower bridge for the chrome (rail/settings) |
| `src/preload-editor.cts` | One line, for the editor window: it gets no `filexApp` bridge, only the SPA's own "you are inside the desktop app" flag |
| `build/installer.nsh` | Windows file-type registration, written by hand — see *Open with filex* |
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
pnpm run dist:win        # installer + PORTABLE single .exe
pnpm run dist:linux      # .deb + AppImage
pnpm run dist:mac        # .dmg + .zip — host arch (arm64 on Apple Silicon), ad-hoc sealed
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

**Two builds can never apply an update in place** — the ad-hoc sealed macOS app
(see *Signing*) and the Windows portable `.exe` (see below). They take the same
route, which is worth understanding before adding a third: the updater is never
wired at all, so nothing is downloaded that could not be applied, and the same
cadence reads the static feed directly and reports an honest
`status: 'manual'` with a **Download** button. The failure this replaces is a
Settings card stuck at "Checking…" forever, waiting on an updater nobody wired.

## Portable (Windows)

`pnpm run dist:win` produces two artifacts: the installer, and
`filex-desktop-portable-x64.exe` — one self-extracting file that runs from
wherever it is put. Linux and macOS already had this (the AppImage runs
unextracted, the mac `.zip` is unzip-and-run); Windows was the only platform
with no way to run filex without an installer.

**Its data lives beside the `.exe`, not in `%APPDATA%`.** For an installed app
the roaming profile is right — nobody wants a program scattering folders across
their desktop. A run-and-delete copy is the opposite case: it is carried in on
a stick, run on a machine that is not the user's, and the promise is that
deleting one folder leaves nothing of theirs behind.

How, in `src/portable.ts`:

- The signal is **`PORTABLE_EXECUTABLE_DIR`**, which the portable stub sets
  before launching the app (`templates/nsis/portable.nsi` in app-builder-lib).
  ⚠ It is the *only* signal. `process.execPath` under this target is the
  extraction temp directory, which the stub deletes on exit — guessing from it
  would give an account store that empties itself between launches. No
  variable means not portable, and nothing is overridden.
- One `app.setPath('userData', …)` (plus `sessionData`), made at the **top of
  `main.ts`**, before `requestSingleInstanceLock()` and before anything has
  resolved a path. Everything else in the app already routes through
  `app.getPath('userData')`, so there is deliberately no second notion of
  "where our files go". `log.ts` in particular caches its path on the first
  line written, which is why `portable-e2e.mjs` asserts the log location: it is
  the one thing that catches a `setPath` that ran a moment too late.
- ⚠ The sync engine is the exception that had to be handled separately. It
  keeps its pairs, baselines and **local trash — real copies of files it
  deleted** — in `~/.filex/sync`, which on a borrowed machine is somebody
  else's home directory. `sync.ts` therefore passes **`FILEX_SYNC_DIR`** (new
  in the Go CLI, mirroring the existing `FILEX_CLI_CONFIG`) so that store lands
  inside `filex-data` too.
- ⚠⚠ **When the `.exe` sits somewhere unwritable** — `C:\Program Files`, a
  read-only stick, a share — the fallback is a **sibling** directory,
  `%APPDATA%\@brftech\filex-desktop-portable`, and Settings shows the path.
  Measured before it was fixed: falling back to the plain default put the
  portable copy straight into the *installed* app's profile — reading and
  writing the accounts of whoever owns the machine, and, because the
  single-instance lock is keyed on that directory, exiting silently and raising
  the installed app's window whenever it was already running.

Honest limits, both documented in `docs/DESKTOP.md`:

- **It does not update itself** (see *Updates* above).
- **Accounts do not travel between machines.** Tokens are sealed with
  `safeStorage`, i.e. Windows DPAPI, so a `filex-data` folder opened on another
  computer or under another Windows account fails to decrypt and the user signs
  in again. That is the right way round for a stick somebody leaves on a train.

⚠ Release step: the portable `.exe` has to be uploaded to
`https://filex.sh/desktop/` alongside the installer, or the **Download** button
in a portable copy's Settings points at a file that is not there. These
packages are uploaded by hand (goreleaser owns the CLI release), so nothing
does it for you.

## Language

*Settings → Language* — System / English / Türkçe, stored in the app state. One
resolver in the main process decides what "system" means, because three surfaces
read it: this window, the tray menu (main process) and the explorer inside it (a
separate component with its own catalogue). Covered by `scripts/lang-e2e.mjs`.

## Open with filex

Double-clicking a `.docx` opens it in the server's OnlyOffice editor. The user
story and the OS-by-OS limits are in
[docs/DESKTOP.md](../docs/DESKTOP.md#opening-documents-from-your-computer); what
matters when working on this code:

- **Three entry points, one queue.** Cold start (argv), a running app
  (`second-instance` argv) and macOS (`open-file`, which fires *before* `ready`
  on a cold start and must be queued or the first double-click after an install
  does nothing). `classifyArgv` splits documents from `filex://` sign-in links
  in one place, because two `argv.find(…)` calls drift apart.
- **Documents are released only after `refreshPairs()`.** Whether a file has a
  synced twin is the first question asked, and `knownPairs` is empty until that
  resolves — a cold start would otherwise copy a file that needed no copy.
- **The write-back is the dangerous part** (`writeBackAtomic`): temp file in the
  *same* directory then rename, never a write over the document, never a rename
  across drives (EXDEV), never resurrecting a document the user deleted while it
  was open, and a failure that is shown rather than logged. `keptAt` on the
  error is where the edit went instead.
- **The grace period after the window closes is not optional.** OnlyOffice posts
  its save callback ~10 s *after* the last editor disconnects, so deleting the
  scratch copy on close would discard the last edit of every session.
  `FILEX_OPENWITH_POLL_MS` / `_GRACE_MS` / `_QUIET_MS` shorten it for tests.
- ⚠⚠ **Windows registration is hand-written (`build/installer.nsh`) and must
  stay that way.** electron-builder's `fileAssociations` uses an NSIS macro that
  writes the DEFAULT ProgId of `.docx` — it takes the file type at install time,
  which on a machine with no Office (the exact machine this feature is for) is
  enough to make filex the handler without anyone being asked. The hand-written
  version adds an `OpenWithProgids` entry and a `SupportedTypes` list and
  changes nothing that already exists. macOS uses `mac.fileAssociations` with
  **`rank: Alternate`** for the same reason; Linux uses `linux.mimeTypes` (not
  `fileAssociations`, which would also ship our own `<mime-type>` XML
  redeclaring types shared-mime-info already defines).
- **The extension list lives in three places** — `OFFICE_EXTENSIONS` in
  `src/openwith.ts`, `mac.fileAssociations` + `linux.mimeTypes` in
  `electron-builder.yml`, and `build/installer.nsh`. A YAML file and an NSIS
  script cannot import TypeScript; widening one without the others gives an app
  that offers to open a type it then refuses.

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
- **nsis and portable both emit `.exe`.** Under the shared `artifactName`
  template they are the same filename written twice, and whichever target ran
  last wins — so `portable.artifactName` is an override, not a nicety. It obeys
  the no-version rule too: `filex-desktop-portable-x64.exe`.
- **The Windows feed does not list the portable build.** Measured:
  electron-builder writes only the installer into `latest.yml`, because that is
  the artifact its updater would apply. So the app builds that filename itself
  (`PORTABLE_ARTIFACT` in `main.ts`) to link a manual download, and
  `portable-e2e.mjs` asserts the two agree — a rename breaks a test rather than
  somebody's browser.

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
| `portable-e2e.mjs` | The portable `.exe` is named so it neither overwrites the installer nor carries a version; the real self-extracting `.exe` starts and loads a page; its data lands in one folder beside it; and Settings says this copy does not update itself instead of sitting at “Checking…” |
| `lang-e2e.mjs` | The language setting moves the shell, the file list and the stored state |
| `shell-e2e.mjs` | The shell windows (settings, pickers) open and answer |
| `dragout-e2e.mjs` | Dragging files OUT: what lands on this computer before an OS drag can start |
| `openwith-e2e.mjs` | A document double-clicked from outside every synced folder: second instance → editor window → server-side save → the bytes on the ORIGINAL local path change → the copy is gone after the window closes. Plus the synced-twin route, which makes no copy at all |
| `plumbing-smoke.mjs` | `app://`, preload injection and `safeStorage`, without a server |

Two more, which need neither a server nor Electron:

| Command | What it proves |
|---|---|
| `pnpm test` | The parts of "Open with filex" that can lose a document, measured directly (`test/openwith.test.ts`, Node's own runner via type stripping) |
| `pnpm test:red` | ⚠ The same cases against a deliberately naive implementation (`test/openwith-naive.ts`), and **fails if any of them passes there**. A case the first draft already satisfies measures nothing while looking like it does — this repo has shipped exactly that kind of test before |

⚠ `openwith-e2e.mjs` performs the editor's save the way OnlyOffice's callback
does — by writing new bytes over the scratch copy through the API. The document
server itself is a separate ~2 GB service that has to reach the filex instance
over the network, and a local run has none; everything on this side of that one
POST is the real product code.

⚠ `dragout-e2e.mjs` measures the **preparation** — which bytes reach the disk, a
folder's subtree, the cache making the second drag free, and that an unprepared
selection is refused. It never calls `dragStart` on a valid selection: that
opens the OS drag loop, which cannot be driven from a script and would leave a
modal drag hanging off the machine's mouse. Letting go over the desktop is the
one step a human has to do.

`dragout-folder-probe.mjs` is a diagnostic, not a suite: it drags a folder the
app has not prepared and then copies the stand-in with **Explorer's own copy
engine** (`Shell.Application.CopyHere`) rather than `fs.mkdirSync`, printing
every `[drag …]` / `[xfer …]` step. That difference is what caught the header
bug in 0.27.2 — the suite's simulated drop could not see it.

`look*.mjs` and `diag-*.mjs` are not suites — they open the app and take a
screenshot of one surface, for looking at a change rather than asserting it.
