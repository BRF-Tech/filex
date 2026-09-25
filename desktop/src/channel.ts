// Which store installed this copy — and therefore who is allowed to update it.
//
// A copy that came from a store is the store's to update: Microsoft Store
// replaces an MSIX package itself, Flathub a Flatpak, the Snap Store a snap.
// electron-updater knows none of them. Left wired, it reads filex.sh's feed,
// downloads the NSIS installer (or tries `pkexec dpkg -i` on a .deb it thinks
// it is) and runs it from INSIDE the package — on Windows that lands a second,
// invisible copy in the package's private AppData, with its own uninstall
// entry that no Settings page shows. So a store copy never touches the feed at
// all; Settings says who updates it and offers the store page instead.
//
// ⚠ This is a PACKAGING branch, not a surface-specific behaviour: what the user
// sees and does in the app is the same on every channel. What differs is who
// owns the install (approved as a channel exception, 2026-09-24). Keep every
// channel decision in this file so there is one place to read them — the
// Linux sandbox's paths and desktop entry included.
//
// ⚠ No `electron` import, so node:test can drive it (same boundary as
// src/login-item.ts). main.ts and log.ts ask; nothing here writes.

import fs from 'node:fs';
import path from 'node:path';

// 'aur' is not a store, but for this file it behaves like one: the package
// manager (pacman, fed by an AUR helper) owns the install, so the app must not
// update itself. See packaging/aur/filex-app-bin/PKGBUILD.
export type StoreChannel = 'msstore' | 'flatpak' | 'snap' | 'aur';

export interface ChannelProbe {
  platform: string;
  /** Electron's `process.windowsStore`: true when running as an MSIX/AppX
   *  package — Microsoft Store installs, and a sideloaded test package. */
  windowsStore?: boolean;
  env: Record<string, string | undefined>;
  /** The contents of `resources/package-type`. electron-builder writes `deb`
   *  or `rpm` there (electron-updater picks its updater by it); the AUR
   *  package rewrites it to `aur`. Absent on AppImage, snap and every
   *  non-Linux build. */
  packageType?: string | null;
}

/** The store this copy was installed from, or null for everything filex.sh
 *  and GitHub hand out directly (NSIS, portable, AppImage, .deb, .rpm, .dmg). */
export function storeChannel(p: ChannelProbe): StoreChannel | null {
  if (p.platform === 'win32' && p.windowsStore === true) return 'msstore';
  if (p.platform === 'linux') {
    // Set by flatpak for every process inside the sandbox.
    if (p.env.FLATPAK_ID) return 'flatpak';
    // Both are set by snapd for every command a snap runs; one alone is
    // somebody's shell variable.
    if (p.env.SNAP && p.env.SNAP_NAME) return 'snap';
    // ⚠ The AUR package is the .deb's own tree, so without its marker this
    // file says `deb` — and electron-updater then runs `pkexec dpkg -i` on an
    // Arch machine ("Neither dpkg nor apt command found").
    if (p.packageType?.trim() === 'aur') return 'aur';
  }
  return null;
}

/** `resources/package-type`, or null when this build has none. */
function readPackageType(resourcesPath: string | undefined): string | null {
  if (!resourcesPath) return null;
  try {
    return fs.readFileSync(path.join(resourcesPath, 'package-type'), 'utf8');
  } catch {
    return null;
  }
}

/** This process's channel. Read once: none of the inputs change at runtime. */
export const CURRENT_CHANNEL: StoreChannel | null = storeChannel({
  platform: process.platform,
  windowsStore: (process as { windowsStore?: boolean }).windowsStore,
  env: process.env,
  // Only Linux packages carry one; nowhere else is it worth a disk read.
  packageType: process.platform === 'linux' ? readPackageType(process.resourcesPath) : null,
});

/**
 * The identities the stores know this app by.
 *
 * ⚠ `msstore` is the 12-character Store ID Partner Center assigns when the
 * name is reserved (Product management → Product identity). Until it is set,
 * the Store build opens the Store's own "Downloads and updates" page instead
 * of the product page — a working fallback, not an error.
 */
/**
 * The desktop app's name on Linux: the command (`/usr/bin/filex-app`), the
 * .deb/.rpm package, the desktop entry, the snap. `linux.executableName` in
 * electron-builder.yml, which test/linux-desktop-entry.test.ts holds equal.
 *
 * ⚠⚠ Not `filex`: that is the CLI's name, on every platform and in every
 * package manager (decision 2026-09-25). Until 0.43.x the desktop package
 * was called `filex` and put `/usr/bin/filex` on PATH — the same command a
 * CLI install answers to, and which of the two a terminal ran depended on
 * PATH order. The display name stays "filex" everywhere, and Windows/macOS
 * and the download file names do not change.
 */
export const LINUX_APP_NAME = 'filex-app';

/** The desktop entry the desktop package installed until 0.43.x, when it was
 *  called `filex` — what old autostart entries and the user's
 *  `mimeapps.list` may still name. */
export const LEGACY_LINUX_DESKTOP_ENTRY = 'filex.desktop';

export const STORE_IDS = {
  msstore: '',
  flatpak: 'sh.filex.Filex',
  // Also the snap's app name: electron-builder names both after
  // `linux.executableName`, and the desktop entry snapd installs is
  // `<snap>_<app>.desktop` (see linuxDesktopEntry).
  snap: LINUX_APP_NAME,
  // `-bin`: the AUR's naming rule for a package built from a prebuilt artefact.
  aur: `${LINUX_APP_NAME}-bin`,
} as const;

type StoreIds = { msstore: string; flatpak: string; snap: string; aur: string };

/** The page that updates this copy, as a link the OS already knows how to
 *  open: the Store app, the Linux software centre, the Snap Store — and for
 *  the AUR, which has no app, its web page. */
export function storePageUrl(ch: StoreChannel, ids: StoreIds = STORE_IDS): string {
  switch (ch) {
    case 'msstore':
      return ids.msstore
        ? `ms-windows-store://pdp/?ProductId=${encodeURIComponent(ids.msstore)}`
        : 'ms-windows-store://downloadsandupdates';
    case 'flatpak':
      return `appstream://${ids.flatpak}`;
    case 'snap':
      return `snap://${ids.snap}`;
    case 'aur':
      return `https://aur.archlinux.org/packages/${ids.aur}`;
  }
}

/**
 * Where to keep files that ANOTHER process has to see by the same path: the
 * drag-out copies Explorer picks up on a drop, the log folder Settings opens,
 * the folder an unfinished edit is rescued to.
 *
 * ⚠⚠ Inside an MSIX package, NEW files under AppData are silently redirected
 * to the package's private LocalCache (Microsoft: "Understanding how packaged
 * desktop apps run on Windows"). The app reads them back fine — but Explorer,
 * which runs outside the package, looks at the real `%APPDATA%` path and
 * finds nothing: a drop onto the desktop would copy nothing, and "open the
 * log folder" would open an empty folder. The home directory is not
 * virtualised, and the sync engine already keeps its state under `~/.filex`,
 * so the Store copy keeps these beside it.
 *
 * Turning the redirection off (desktop6 `FileSystemWriteVirtualization`)
 * needs the `unvirtualizedResources` capability, which the Store grants only
 * to "certain types of desktop PC games that are published by Microsoft and
 * our partners" — not an option.
 *
 * Everything that only THIS app reads (the account store, the Chromium
 * profile, the open-with session records) stays in userData on every channel.
 * (A snap's userData is `~/snap/filex-app/<rev>/.config/…`: a real host path the
 * file manager reads as it is, so nothing moves there.)
 */
export function explorerVisibleRoot(ch: StoreChannel | null, userData: string, home: string): string {
  return ch === 'msstore' ? path.join(home, '.filex', 'desktop') : userData;
}

/**
 * Files whose presence means the DIRECT-DOWNLOAD (NSIS) copy of filex is
 * installed for this user — checked by the Store copy, which then offers to
 * have it removed (Microsoft's own guidance for moving users from a web
 * install to a Store one).
 *
 * Why it matters: the Store copy reads the NSIS copy's account store (an MSIX
 * package falls back to the real AppData for a file it has no private copy
 * of), so with both installed the same accounts and the same sync folders are
 * served by two apps, two tray icons, two sets of notifications.
 *
 * Two markers, because the installer lets the user pick the folder
 * (`allowToChangeInstallationDirectory`): the default location, and the
 * Start-menu shortcut the installer creates wherever the app went. An MSIX
 * package creates neither. Reading them is allowed from inside the package;
 * only writes are redirected.
 */
export function directCopyMarkers(env: Record<string, string | undefined>): string[] {
  const out: string[] = [];
  if (env.LOCALAPPDATA) out.push(path.win32.join(env.LOCALAPPDATA, 'Programs', 'filex', 'filex.exe'));
  if (env.APPDATA) out.push(path.win32.join(env.APPDATA, 'Microsoft', 'Windows', 'Start Menu', 'Programs', 'filex.lnk'));
  return out;
}

// ─────────────────────────── Linux ───────────────────────────

/**
 * A Linux sandbox whose PACKAGE — not this app — tells the desktop about
 * filex: the `filex://` link and the "Open with" types are in the desktop
 * entry the store installs, and nothing done from inside reaches the host's
 * settings. A strict snap sees `$HOME` as `~/snap/filex-app/<rev>`, so
 * `xdg-mime default` writes a `mimeapps.list` no desktop reads; a Flatpak
 * writes into its own `~/.var/app` copy. (The AUR package is not a sandbox:
 * it installs the .deb's tree into /opt like any other package.)
 */
export function linuxSandbox(ch: StoreChannel | null): boolean {
  return ch === 'snap' || ch === 'flatpak';
}

/**
 * The desktop entry this copy is installed under.
 *
 * It is how a Linux desktop knows the app: Electron hands it to
 * `xdg-settings` when it registers `filex://`, and uses it as the Wayland app
 * id and the notification's `desktop-entry` hint. Electron reads it from
 * `CHROME_DESKTOP`, which it fills from `desktopName` in package.json and
 * otherwise from `<app.name>.desktop` — here `@brftech/filex-desktop.desktop`:
 * a file no package installs, and a name `xdg-settings` refuses (measured on
 * the v0.43.0 .deb: nothing registered, `xdg-mime query default
 * x-scheme-handler/filex` empty before and after a launch).
 *
 * - .deb / .rpm / AUR: `filex-app.desktop` (`linux.executableName`;
 *   package.json `desktopName` says the same, test/linux-desktop-entry.test.ts
 *   keeps the three in step).
 * - AppImage: the entry it writes for itself (see appImageDesktopEntry).
 * - snap: snapd installs `<snap>_<app>.desktop`.
 * - Flatpak: the application id.
 */
export function linuxDesktopEntry(ch: StoreChannel | null, appImage?: string | null, ids: StoreIds = STORE_IDS): string {
  if (ch === 'snap') return `${ids.snap}_${ids.snap}.desktop`;
  if (ch === 'flatpak') return `${ids.flatpak}.desktop`;
  if (appImage) return APPIMAGE_DESKTOP_ENTRY;
  return `${LINUX_APP_NAME}.desktop`;
}

/**
 * The file name an AppImage registers itself under, in
 * `$XDG_DATA_HOME/applications`.
 *
 * ⚠ Not the package's `filex-app.desktop`: a user entry of that name would
 * HIDE the .deb's or the .rpm's own entry (the user's data dir wins over
 * /usr/share) the day both are installed — and this one is NoDisplay.
 *
 * ⚠ And it did NOT follow the rename to `filex-app` (2026-09-25), on purpose.
 * The name identifies the channel, not the command, and an image rewrites
 * this very file at every start. Renamed, the first start of a new image would
 * write a second file and leave the old one behind, still claiming `filex://`
 * and the document types from the user's own applications folder, with the
 * user's `mimeapps.list` default pointing at it — an orphan nobody removes.
 * Kept, the new image simply overwrites it.
 */
export const APPIMAGE_DESKTOP_ENTRY = 'filex-appimage.desktop';

/**
 * The desktop entry a bare AppImage writes for itself at start, so the
 * browser can hand `filex://` back to it and "Open with" can find it.
 *
 * An AppImage installs nothing: its desktop entry lives inside the image,
 * where only an integrator (AppImageLauncher, Gear Lever, appimaged) copies
 * it out. Without one, the `filex://` registration Electron makes names an
 * entry that does not exist, and the link goes nowhere. So the running image
 * writes a hidden entry pointing at itself — rewritten at every start, so a
 * moved image re-points it; `TryExec` makes desktops ignore it once the image
 * is gone.
 *
 * Exec follows the Desktop Entry spec: `%` doubled, and a path with a
 * reserved character (a space, a quote, `$`…) double-quoted with `"` `` ` ``
 * `$` `\` backslash-escaped — then, because Exec is a string value, every
 * backslash escaped once more.
 *
 * ⚠ Quoted ONLY when the spec requires it. `xdg-settings` takes the first
 * word of Exec literally, quotes included, and `which '"/path"'` fails — so a
 * quoted Exec makes it refuse the entry ("file missing", exit 2; measured with
 * xdg-utils 1.1.3 on Ubuntu 24.04). The app registers with `xdg-mime`
 * instead, which does not look at Exec (src/main.ts → registerAppImage).
 */
export function appImageDesktopEntry(appImage: string, mimeTypes: readonly string[]): string {
  const bs = String.fromCharCode(92);
  const reserved = /[\s"'\\><~|&;$*?#()`]/.test(appImage);
  const exec = (reserved ? '"' + appImage.replace(/["`$\\]/g, (c) => bs + c) + '"' : appImage).replace(/%/g, '%%');
  const asString = (s: string) => s.split(bs).join(bs + bs);
  return [
    '[Desktop Entry]',
    'Type=Application',
    'Name=filex',
    'Comment=filex (AppImage): opens filex:// links and documents',
    `TryExec=${asString(appImage)}`,
    `Exec=${asString(exec)} %U`,
    'Terminal=false',
    // Not in the menu: an AppImage user launches the image, and an integrator
    // adds a menu entry of its own. This one only answers links and files.
    'NoDisplay=true',
    `MimeType=${mimeTypes.join(';')};`,
    '',
  ].join('\n');
}

/**
 * The home directory the user knows — the one their file manager opens.
 *
 * ⚠⚠ A strict snap runs with `HOME=$SNAP_USER_DATA` (`~/snap/filex-app/<rev>`),
 * and Electron's `app.getPath('home')` reads `HOME`. The default filex folder
 * built from it would land in the snap's per-revision directory: hidden from
 * the file manager, copied at every refresh, deleted with the snap. snapd
 * keeps the real one in `SNAP_REAL_HOME`, whose non-hidden files the `home`
 * interface lets the snap read and write.
 */
export function userHome(ch: StoreChannel | null, env: Record<string, string | undefined>, home: string): string {
  return ch === 'snap' && env.SNAP_REAL_HOME ? env.SNAP_REAL_HOME : home;
}

/**
 * Where the sync engine keeps its state (pairs, baselines, the local trash)
 * on this channel — handed to it as `FILEX_SYNC_DIR` — or null for the
 * engine's own default, `~/.filex/sync` (backend/internal/filesync/store.go).
 *
 * A snap cannot use the real `~/.filex`: it is a hidden directory, which the
 * `home` interface does not cover (that takes `personal-files`, a
 * store-approved declaration). Left alone, the engine would resolve
 * `$SNAP_USER_DATA/.filex/sync` — per revision, so every refresh copies it
 * and a revert rolls the pairings back with it. `SNAP_USER_COMMON`
 * (`~/snap/filex-app/common`) is the snap's directory that survives refreshes and
 * reverts unchanged. The price: a `filex` run in a terminal outside the snap
 * does not share this pairing file (desktop/README.md says so).
 */
export function engineStateDir(ch: StoreChannel | null, env: Record<string, string | undefined>): string | null {
  if (ch === 'snap' && env.SNAP_USER_COMMON) return path.posix.join(env.SNAP_USER_COMMON, 'sync');
  return null;
}

/**
 * How Settings' "make filex the default for these documents" works here.
 *
 *   settings      Windows: only the user may set it; open the OS page.
 *   xdg           Linux package: `xdg-mime default` really sets it.
 *   linux-manual  Linux sandbox (see linuxSandbox): nothing from inside
 *                 reaches the host, so Settings explains the file manager's
 *                 own "Open with" instead of a button that would report
 *                 success and change nothing.
 *   manual        macOS: Finder's "Change All…", explained.
 */
export type DefaultHandlerRoute = 'settings' | 'xdg' | 'linux-manual' | 'manual';

export function defaultHandlerRoute(platform: string, ch: StoreChannel | null): DefaultHandlerRoute {
  if (platform === 'win32') return 'settings';
  if (platform === 'linux') return linuxSandbox(ch) ? 'linux-manual' : 'xdg';
  return 'manual';
}

/**
 * `mimeapps.list` with every mention of the desktop entry `from` replaced by
 * `to`, or null when there is nothing to change.
 *
 * Why: "Make filex the default" wrote `…=filex.desktop` for the office types
 * (and the app registered `x-scheme-handler/filex` the same way). When the
 * package became `filex-app`, that entry disappeared with the old package,
 * and a desktop skips a default whose entry does not exist — so the user's
 * explicit choice would have silently fallen to whatever else claims .docx.
 * Only the three association sections are touched, only whole list items
 * equal to `from`, and an item already naming `to` is not doubled.
 */
export function retargetMimeapps(text: string, from: string, to: string): string | null {
  const sections = new Set(['[Default Applications]', '[Added Associations]', '[Removed Associations]']);
  let current = '';
  let changed = false;
  const out = text.split('\n').map((line) => {
    const trimmed = line.trim();
    if (trimmed.startsWith('[')) {
      current = trimmed;
      return line;
    }
    if (!sections.has(current)) return line;
    const eq = line.indexOf('=');
    if (eq < 0) return line;
    const items = line.slice(eq + 1).split(';');
    if (!items.includes(from)) return line;
    const next: string[] = [];
    for (const it of items) {
      const v = it === from ? to : it;
      if (v !== '' && next.includes(v)) continue;
      next.push(v);
    }
    changed = true;
    return line.slice(0, eq + 1) + next.join(';');
  });
  return changed ? out.join('\n') : null;
}
