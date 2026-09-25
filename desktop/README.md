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
   app **refuses to store the token** rather than writing it to disk — and says
   so on the sign-in window before a sign-in starts, not after the browser
   round trip (`src/keychain.ts`; on Linux Chromium's `basic_text` fallback
   counts as no keychain).
3. **Folder sync in the background, live.** `filex sync run --watch` runs per
   account, supervised by the app and shipped inside it (`build/bin/filex`), so
   the app and a terminal act on one implementation and one pairing file. The
   engine follows the server's change stream and the local file system, so an
   edit on either side arrives in about a second; its `live:` lines become the
   *Live / Polling / Offline* word under each synced folder
   (`src/syncstatus.ts`, which also keeps each folder's own error and clears
   it on that folder's next clean pass). ⚠ The live path lives in the ENGINE: an installed app
   gets it only with a build that bundles the new CLI.
   Also on this release: **Pause sync** in the tray menu and Settings,
   remembered across restarts, reboots and the hidden start at sign-in; a
   **bandwidth limit** and a **sync window** as Settings presets
   (`--limit-down` / `--limit-up` / `--window`); a first run that **holds back**
   a large re-upload and offers *Upload them* or *Move to local trash* with a
   count; transfer progress in bytes with an estimate on each folder's line;
   and the computer **kept awake while sync moves files** (the screen still
   locks). The unread notification count shows on the dock icon where the
   system has one.
4. **It keeps itself up to date, quietly.** See *Updates* below.
5. **It can be the app that opens a document.** Double-click a `.docx` on the
   disk and it opens in the server's ONLYOFFICE editor, with the edits written
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
| `src/keychain.ts` | Whether that store may be written at all, and what the sign-in window advises when not. No Electron import |
| `src/channel.ts` | Who installed this copy (Microsoft Store, snap, Flatpak, AUR, or a direct download) and everything that follows from it: who updates it, a snap's real home and engine state, the Linux desktop entry. No Electron import |
| `src/browser-auth.ts` | Browser sign-in (PKCE) + deep-link/manual code exchange |
| `src/sync.ts` | Supervises one `filex sync run --watch` per account; pairs, trash, status |
| `src/openwith.ts` | "Open with filex", the parts that can lose a document: argv classification, local path → synced twin, scratch naming, the atomic write-back, the sweeps. No Electron import — that is what makes it testable |
| `src/openwith-io.ts` | The six server calls that round trip does (list, stat, mkdir, upload, download, delete) |
| `src/preload-app.cts` | The window's only bridge: `window.filexApp` (state, settings, sync, updates) |
| `src/preload-shell.cts` | The narrower bridge for the chrome (rail/settings) |
| `src/preload-editor.cts` | One line, for the editor window: it gets no `filexApp` bridge, only the SPA's own "you are inside the desktop app" flag |
| `build/installer.nsh` | Windows file-type registration, written by hand — see *Open with filex* |
| `ui/app.html` | The app's own chrome — rail, settings, boot screens, string table, and the `config.brand` the explorer's top-bar mark comes from (a slot is unreachable in a web component) |
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
pnpm run dist:linux      # .deb + .rpm + AppImage (the .rpm needs rpmbuild: `apt install rpm`)
pnpm run dist:mac        # .dmg + .zip — host arch (arm64 on Apple Silicon), ad-hoc sealed
pnpm run dist:store      # Microsoft Store package (.appx) — Windows only; see "Microsoft Store"
pnpm run dist:snap       # Snap Store .snap (strict, core20 template; no snapcraft needed)
```

Linux packages are built on Linux; `electronuserland/builder` in Docker is
enough for all four, the snap included.

`FILEX_CLI_BIN=<path>` points `fetch-cli.mjs` at an already-built CLI instead of
compiling one; give it a binary of the **same version** you are packaging, built
without the embedded server UI (85 MB of admin SPA the app already ships in
`app/`).

The runtime is **Electron 44** (Node 24, Chromium 152), supported until
2027-03-02 — [releases.electronjs.org/schedule](https://releases.electronjs.org/schedule)
lists each major's end of life; only the latest three are patched.

- ⚠ `pnpm install` does **not** download the Electron binary any more (since
  Electron 42 there is no postinstall step). The first thing that needs it —
  `pnpm run dev`, an e2e suite, `require('electron')` — fetches it, and
  `node node_modules/electron/install.js` does it up front. (The package
  declares `engines.node >= 22.12`; the download itself also ran on Node 20.20
  when measured.) Packaging never touches it: electron-builder downloads its
  own copy of the runtime, so `dist:*` builds on the Node 20 the release
  workflow uses.
- ⚠ To run the suites against the SOURCE tree on another Electron (an A/B
  against the previous major, say), set `ELECTRON_OVERRIDE_DIST_PATH=<that
  runtime's dist folder>`. Do not hand Playwright an `executablePath` for it:
  that puts `--inspect` / `--remote-debugging-port` in front of the app path,
  argv[1] stops being the app, and the app takes its own folder for a
  document to open and never shows a window.
- Moving to another major: read Electron's
  [breaking changes](https://www.electronjs.org/docs/latest/breaking-changes)
  for **every** major in between, and raise `target` in
  `scripts/build-main.mjs` to the Node that release ships.

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

The feed is a plain static directory on filex.sh, not the GitHub provider: when
it was chosen this repo's mirror was private, and that provider would need a
token shipped inside the app. `FILEX_NO_UPDATE=1` turns the whole thing off.

**A store copy never touches the feed** (Microsoft Store, Flatpak, snap —
`src/channel.ts`). The store replaces the package itself; electron-updater does
not know it is inside one and would download the NSIS installer (or a `.deb`)
and run it from within the package. Settings says which store updates the copy
and offers its page instead.

**Two builds can never apply an update in place** — the ad-hoc sealed macOS app
(see *Signing*) and the Windows portable `.exe` (see below). They take the same
route, which is worth understanding before adding a third: the updater is never
wired at all, so nothing is downloaded that could not be applied, and the same
cadence reads the static feed directly and reports an honest
`status: 'manual'` with a **Download** button. The failure this replaces is a
Settings card stuck at "Checking…" forever, waiting on an updater nobody wired.

## Channels: who installs it, who updates it

**Names.** On Linux the desktop app is **`filex-app`** — the command
(`/usr/bin/filex-app`), the .deb/.rpm package, the desktop entry, the snap and
the AUR package (`filex-app-bin`). **`filex` is the CLI**, everywhere. The
window, the menu entry and the tray still say "filex", and the download files
keep their `filex-desktop-*` names on every platform. Until 0.43.x the Linux
desktop package was called `filex` and put `/usr/bin/filex` on PATH — the
CLI's command.

| Channel | Built by | Published to | Updated by |
|---|---|---|---|
| Windows installer | `dist:win` | GitHub Release + feed | the app, silently |
| Windows portable `.exe` | `dist:win` | GitHub Release | nobody: Settings offers the download |
| Microsoft Store | `dist:store` | Partner Center | the Store |
| AppImage | `dist:linux` | GitHub Release + feed | the app |
| `.deb` / `.rpm` (`filex-app`) | `dist:linux` | GitHub Release + feed | the app, through `pkexec` and dpkg / dnf / zypper (a password prompt) |
| Snap `filex-app` | `dist:snap` | Snap Store + GitHub Release | snapd |
| AUR `filex-app-bin` | `packaging/aur` (from the Release `.deb`) | AUR | pacman / the AUR helper |
| macOS `.dmg` / `.zip` | `dist:mac` | GitHub Release + feed | nobody until signed (see *Signing*) |

`src/channel.ts` recognises a copy that something else updates
(`process.windowsStore`, snapd's `SNAP` + `SNAP_NAME`, `FLATPAK_ID`, and the
`aur` the PKGBUILD writes into `resources/package-type`) and never wires the
updater there: Settings names who keeps the copy current and opens its page.

**Upgrading from the package called `filex` (≤ 0.43.x).** The new .deb/.rpm
`Conflicts`/`Replaces` (rpm: `Obsoletes`) the old one — bounded to
`filex (<< 0.44.0)`, so a future CLI package named `filex` is never touched —
and `apt`/`dnf` swap them in one step. At its first start the renamed app
carries over what the user set up under the old name
(`main.ts` → `migrateLegacyLinuxNames`): an autostart entry it wrote
(`~/.config/autostart/filex.desktop` → `filex-app.desktop`; somebody else's
file of that name is left alone), and "make filex the default" choices in
`mimeapps.list` (`filex.desktop` → `filex-app.desktop`, once no
`filex.desktop` is installed anywhere).

**Snap** `filex-app` (strict confinement, `base: core20` — electron-builder's
template; core24 arrives with electron-builder 26):

- `HOME` inside a snap is `~/snap/filex-app/<revision>`. The default filex
  folder is built from the real home (`SNAP_REAL_HOME`), so it is
  `~/filex/<host>` as everywhere else. The sync engine's state is
  `~/snap/filex-app/common/sync` (`SNAP_USER_COMMON` → `FILEX_SYNC_DIR`): the
  real `~/.filex` is a hidden directory the `home` interface does not reach.
  ⚠ A terminal `filex sync` outside the snap therefore does not see the snap's
  pairings.
- Two plugs do not connect by themselves: `snap connect
  filex-app:password-manager-service` (the keyring; until then the sign-in
  window says so — with the command spelled from the snap's own name — and
  does not start a sign-in) and `snap connect filex-app:removable-media`
  (folders under `/media`, `/run/media`, `/mnt`).
- `filex://` and "Open with" come from the desktop entry snapd installs
  (`filex-app_filex-app.desktop`). "Make filex the default" cannot reach the
  desktop from inside the snap, so Settings explains the file manager's *Open
  with* instead of offering the button.
- "Start when I sign in" writes
  `~/snap/filex-app/current/.config/autostart/filex-app.desktop` — exactly the
  file snapd's `autostart:` launches, `--hidden` included.
- Chromium's own sandbox is off (`--no-sandbox`, electron-builder's default
  under strict confinement); the confinement is the sandbox.
- snapd holds a refresh of a running app back for up to 14 days, and filex
  usually runs in the tray: a snap update lands on quit, or when that runs out.

**AppImage** installs nothing, so at each start it writes a hidden
`~/.local/share/applications/filex-appimage.desktop` pointing at itself and
makes it the `filex://` handler with `xdg-mime` — the only way the browser can
hand the sign-in back to a bare image. `TryExec` makes desktops ignore the
entry once the image is deleted. (The name did not follow the rename: the image
rewrites that very file at every start, and a new name would leave the old one
behind, still claiming the link.)

**Publishing (maintainer, once):**

- Snap Store: a Snapcraft (Ubuntu One) account with 2FA → `snapcraft register
  filex-app` (names are reviewed by hand, up to two working days) → `snapcraft
  export-login --snaps=filex-app --acls=package_access,package_push,package_update,package_release
  --expires=<date> creds.txt` → the file's content as the repository secret
  `SNAPCRAFT_STORE_CREDENTIALS`. Auto-connecting the two plugs above is a
  separate request on the Snapcraft forum (store-requests, a week's vote).
- AUR: an aur.archlinux.org account with its own SSH key → run
  `packaging/aur/update-pkgbuild.sh <version>` (the committed PKGBUILD is a
  template with SKIP checksums until the first `filex-app` release) and push
  `PKGBUILD` + `.SRCINFO` to `ssh://aur@aur.archlinux.org/filex-app-bin.git`
  once → the private key as the secret `AUR_SSH_PRIVATE_KEY`.
- The release job uploads the snap and pushes the PKGBUILD when those secrets
  exist; without them it attaches the files to the Release and says in the
  run summary what it did not publish.

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

Double-clicking a `.docx` opens it in the server's ONLYOFFICE editor. The user
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
- **The grace period after the window closes is not optional.** ONLYOFFICE posts
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
- **The extension list lives in five places** — `OFFICE_EXTENSIONS` in
  `src/openwith.ts`, `mac.fileAssociations` + `linux.mimeTypes` in
  `electron-builder.yml`, `build/installer.nsh` and `build/appx-extensions.xml`
  (Microsoft Store). YAML, NSIS and XML cannot import TypeScript; widening one
  without the others gives an app that offers to open a type it then refuses.
  `test/extension-lists.test.ts` fails when they disagree.

## Microsoft Store

`pnpm run dist:store` builds `release/filex-desktop-x64.appx` for Partner Center.
The Store signs it (no certificate of ours), hosts it and updates it; Store
installs get no SmartScreen warning. The NSIS installer and the portable `.exe`
stay on filex.sh for machines without the Store. Listing text, certification
notes and the IARC answers: `store/microsoft/listing.md`. CI leg (not yet in
the public workflow): `../packaging/ci/release-desktop-store.patch`.

- **Version.** The Store refuses a first number of 0 and keeps the fourth for
  itself, so the package carries a mapping that `scripts/appx-manifest.cjs`
  (the `appxManifestCreated` hook) writes into the manifest only: `0.43.1` →
  `1.0.4301.0`, and from filex `1.1.0` on the two numbers are equal. ⚠ filex
  never ships a `1.0.x` — it would sort below every 0.x already in the Store.
  The app itself keeps reporting its real version.
- **Identity.** `appx.identityName`, `publisher` and `publisherDisplayName`
  in `electron-builder.yml` must be Partner Center's *Product identity* values,
  character for character. The committed ones are placeholders.
- **Nothing is registered at runtime.** Registry writes from inside a package
  land in its private hive, so the `filex://` link, "Open with filex" and the
  login item are declared in `build/appx-extensions.xml`. The login item is a
  startup task, off until the user turns it on in Windows Settings → Apps →
  Startup (Settings sends them there); it passes `--hidden`, like the NSIS
  build's Run entry. ⚠ No `--` inside an XML comment in that file: makeappx
  rejects the whole manifest.
- **AppData is redirected.** New files under AppData go to the package's
  private LocalCache, which Explorer cannot see. The drag-out copies, the log
  folder and rescued edits therefore live under `~/.filex/desktop` on this
  build; everything only the app reads stays in userData.
- **Two copies.** A package reads the real AppData for a file it has no
  private copy of, so a Store copy on a machine that still has the NSIS install
  shares its accounts and folders. Settings says so and points at Windows'
  installed-apps list.
- `pnpm run e2e:store` (Windows, Developer Mode) installs an e2e *variant* —
  own identity, own `filex-e2e://` scheme, own userData name, so it cannot touch
  a real installation — and checks it as a Store copy: manifest, a cold
  protocol launch, the running app's channel, AppData redirection, the startup
  task. The variant is removed afterwards.

## Security posture (do not loosen)

`contextIsolation: true`, `nodeIntegration: false`, `sandbox: true`. The window
only ever loads `app://`; external links open in the OS browser; the preloads
expose the narrowest surface that works. Tokens live in the OS keychain.

## Signing

Releases are **unsigned** by design for now — Windows SmartScreen and macOS
Gatekeeper will warn. A code-signing certificate is a separate, paid decision,
not a defect. The one signed Windows build is the Microsoft Store package, and
Microsoft signs it (see *Microsoft Store*).

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
- **The .deb/.rpm package name is not `executableName`.** For a scoped npm
  name electron-builder names the package after productName (`filex`), so
  `deb.packageName`/`rpm.packageName` say `filex-app` explicitly — without them
  the renamed build was still `Package: filex` and `apt` upgraded the old one
  in place.
- **A URL scheme goes in `linux.mimeTypes`, not `linux.protocols`.**
  electron-builder 24 appends a protocol's scheme to the mimeTypes list once
  per target: `x-scheme-handler/filex` came out twice in the .deb, three times
  in the .rpm.
- **The after-remove script is ours** (`build/linux/after-remove.tpl`).
  electron-builder's gives `update-alternatives --remove` the link instead of
  the target and runs on upgrades too: on Fedora `dnf remove` failed the
  scriptlet and left `/usr/bin/filex-app` dangling.

## End-to-end suites

Each drives the real app with Playwright (`scripts/lib/harness.mjs`); most need
a server and credentials (`FILEX_SERVER`, `FILEX_EMAIL`, `FILEX_PASSWORD`), and
`FILEX_APP_BINARY` points them at a packaged build instead of the source tree.

| Script | What it proves |
|---|---|
| `ui-login-e2e.mjs` | Browser sign-in end to end, including the manual-code fallback |
| `signin-retry-e2e.mjs` | A sign-in that fails — a stale link, a refused code, a reload of the window — stays on the waiting screen of the same attempt; *Start again* begins a new one; *Cancel* is the only way back to the server address (issue #36) |
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

And three that MEASURE rather than assert — they answer "is this presentable"
and "does this window offer the same thing twice", which no pass/fail can:

| Script | What it shows |
|---|---|
| `look-chrome.mjs` | Photographs every surface the app draws ITSELF — connect, waiting, rail, settings (English + Türkçe, light + dark + a palette), the folder picker, the sync-trash listing, storage connections. `LOOK_OUT` / `LOOK_TAG` name the files; `LOOK_DEAD_SERVER` adds the "can't reach the server" shot |
| `diag-chrome-metrics.mjs` | Prints the chrome's control heights, type scale and colours beside the `--fe-*` tokens they are supposed to be — the measurement behind `test/chrome-tokens.test.ts` |
| `diag-duplicates.mjs` | Lists the explorer's header cluster, its "⋯" menu, the rail and the settings surface side by side, so "two controls, one job" is read off two lists instead of remembered |


Two more, which need neither a server nor Electron:

| Command | What it proves |
|---|---|
| `pnpm test` | The parts of "Open with filex" that can lose a document, measured directly (`test/openwith.test.ts`, Node's own runner via type stripping); the notification and portable-data decisions; and `test/chrome-tokens.test.ts` — the shell's chrome states no colour of its own, sizes its controls from `--fe-h-*`, and never becomes a SECOND writer of a preference the file list already owns |
| `pnpm test:red` | ⚠ The same cases against a deliberately naive implementation (`test/openwith-naive.ts`), and **fails if any of them passes there**. A case the first draft already satisfies measures nothing while looking like it does — this repo has shipped exactly that kind of test before |

⚠ `openwith-e2e.mjs` performs the editor's save the way ONLYOFFICE's callback
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
