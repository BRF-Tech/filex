// filex desktop shell — Electron main process.
//
// Shape of the app:
//   • The window IS the filex explorer. Everything the user does with files
//     happens in the embedded web bundle, served over a custom `app://` scheme
//     (mandatory: the SPA uses createWebHistory('/admin/'), which collapses
//     under file:// — a standard secure scheme restores a real origin).
//   • Sign-in happens in the SYSTEM BROWSER, never here. A native form can only
//     do username+password, which locks out every install behind Keycloak/OIDC
//     — passkeys, MFA, corporate SSO. The browser already has that session. We
//     get handed a credential back over a `filex://` deep link.
//   • It is a PC app, so it keeps running in the tray and holds MULTIPLE
//     accounts; the two things it adds on top of the web app are Settings
//     (accounts) and Sync folders.
import {
  app,
  BrowserWindow,
  Menu,
  Tray,
  clipboard,
  dialog,
  ipcMain,
  nativeImage,
  Notification,
  nativeTheme,
  net,
  powerMonitor,
  powerSaveBlocker,
  protocol,
  session,
  shell,
} from 'electron';
import electronUpdater from 'electron-updater';
import { execFile, spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import {
  EMPTY_STATE,
  activeAccount,
  keychain,
  keychainRefusal,
  loadState,
  removeAccount,
  saveState,
  signIn,
  type Account,
  type DesktopState,
} from './accounts.js';
import { beginBrowserAuth, exchangeCode, parseAuthDeepLink, type PendingAuth } from './browser-auth.js';
import { failureOf, signInView, type SignInFailure } from './signin-flow.js';
import { keychainAdvice, snapConnectCommand } from './keychain.js';
import {
  APPIMAGE_DESKTOP_ENTRY,
  CURRENT_CHANNEL,
  LEGACY_LINUX_DESKTOP_ENTRY,
  appImageDesktopEntry,
  defaultHandlerRoute as handlerRouteFor,
  otherCopyOf,
  engineStateDir,
  explorerVisibleRoot,
  linuxDesktopEntry,
  linuxSandbox,
  retargetMimeapps,
  storePageUrl,
  userHome,
  type DefaultHandlerRoute,
} from './channel.js';
import {
  mount as mountDrive,
  unmount as unmountDrive,
  type DrivePlatform,
  type MountResult,
} from './drive.js';
import { DragOutCache, createPlaceholders, fulfilDrop, type DragItem } from './dragout.js';
import { localDriveRoots, watchForDrop } from './dropwatch.js';
import { remoteDownloadUrl, remoteSearchUrl, searchResults } from './remote-search.js';
import { log, logPath } from './log.js';
import {
  LEGACY_LINUX_AUTOSTART_NAME,
  LINUX_AUTOSTART_NAME,
  legacyAutostartAction,
  loginItemExecutable,
  loginItemWrite,
  osWillLaunch,
  preferenceAfterStartup,
  type LoginItemReport,
} from './login-item.js';
import { DesktopNotifier, opensInWindow, type NotificationRow } from './notifications.js';
// ⚠⚠ The badge's rule comes from the WEB package, exactly as the click
// destination (notificationTarget) and the sentence (notificationText) do:
// "exact to 99, `99+` above" is a product rule, and a counter written twice is
// a counter that disagrees with itself. Same boundary, same reason.
import { unreadBadgeCount, unreadBadgeLabel } from '../../web/src/lib/unreadBadge.ts';
import {
  OFFICE_EXTENSIONS,
  OpeningDocs,
  OFFICE_MIME_TYPES,
  SessionStore,
  WriteBackError,
  classifyArgv,
  extensionOf,
  hasChanged,
  isOfficeDocument,
  needsRecovery,
  newSessionId,
  orphanScratchEntries,
  recoveryPathFor,
  resolveSyncTwin,
  scratchBasename,
  scratchRemoteDir,
  scratchRemotePath,
  staleSessions,
  writeBackAtomic,
  type OpenWithSession,
} from './openwith.js';
import {
  deleteRemote,
  downloadFile,
  ensureScratchDir,
  listDir,
  listStorages,
  statRemote,
  uploadFile,
  type RemoteContext,
} from './openwith-io.js';
import {
  SyncSupervisor,
  addPair,
  cliPath,
  confirmHeld,
  discardHeld,
  listPairs,
  listTrash,
  movePair,
  removePair,
  type Pair,
} from './sync.js';
import {
  LIMIT_PRESETS_KIB,
  WINDOW_PRESETS,
  answerHold,
  heldItems,
  normLimit,
  normWindow,
  folderView,
  trayTooltip,
  watchPrefsKey,
  watcherAccounts,
  type WatchPrefs,
} from './sync-policy.js';
import { SleepGuard, quietMomentForUpdate, syncBusy } from './power.js';
import { DownloadTally, downloadEnding } from './download-guard.js';
import { anyError } from './syncstatus.js';
import { PORTABLE_DATA_DIRNAME, portableMode } from './portable.js';

// ─────────────────────────── portable build ───────────────────────────
//
// ⚠⚠ FIRST, before anything else in this file runs. Every path the app uses is
// derived from `app.getPath('userData')` — the account store, the log, the
// drag-out cache, the open-with session records — and each of them resolves it
// the first time it is asked. Moving it after any of that has happened would
// leave half the app writing to one place and half to another; moving it after
// `requestSingleInstanceLock()` would key the lock on the wrong directory, so
// a portable copy and an installed one could not tell each other apart.
//
// Off a portable build this is a no-op: portableMode() returns
// `portable: false` unless the portable stub's PORTABLE_EXECUTABLE_DIR is
// there, and then nothing is overridden at all.
const portable = portableMode();
if (portable.portable) {
  // ⚠⚠ When the .exe sits somewhere unwritable the fallback is NOT the ordinary
  // userData directory. Measured on this machine: running the portable copy
  // from a folder with write access denied put it straight into
  // `%APPDATA%\@brftech\filex-desktop` — the INSTALLED app's profile, the one
  // holding its accounts. Two consequences, both bad. A copy carried in on a
  // stick would have been reading and writing the accounts of whoever owns the
  // machine; and because the single-instance lock is keyed on this directory,
  // launching it while the installed app was running would have exited
  // silently and raised the installed app's window instead — an .exe that
  // "does nothing".
  //
  // So the fallback is a sibling directory of its own. The promise ("delete
  // one folder and nothing of mine is left") is already broken in this branch
  // and Settings says so with the path on screen — but the portable copy stays
  // a separate app with a separate lock, and there is still exactly one folder
  // to delete.
  const dir = portable.dataDir ?? `${app.getPath('userData')}-portable`;
  app.setPath('userData', dir);
  // sessionData defaults to userData, but only when nobody has overridden
  // either — writing it out keeps the Chromium profile (cookies, cache, the
  // service worker registry) inside the same folder instead of leaving it in
  // %APPDATA% for the user to find later.
  app.setPath('sessionData', dir);
}

// ─────────────────────────── Linux package identity ───────────────────────────
//
// Before anything asks the OS about us. Both are decided in src/channel.ts.
if (process.platform === 'linux') {
  // The desktop entry the package installed — what `xdg-settings` registers
  // `filex://` against and what Wayland matches the window to. package.json's
  // `desktopName` covers .deb/.rpm; a snap, a Flatpak and a bare AppImage go
  // by other names.
  process.env.CHROME_DESKTOP = linuxDesktopEntry(CURRENT_CHANNEL, process.env.APPIMAGE);
  // The sync engine's state, where the channel needs it moved (a snap's
  // revision-proof directory). Every engine invocation inherits process.env
  // (src/sync.ts engineEnv). An explicit FILEX_SYNC_DIR still wins.
  const stateDir = engineStateDir(CURRENT_CHANNEL, process.env);
  if (stateDir && !process.env.FILEX_SYNC_DIR) process.env.FILEX_SYNC_DIR = stateDir;
}

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const WEB_ROOT = path.join(__dirname, '..', 'app');
const UI_ROOT = path.join(__dirname, '..', 'ui');
const APP_SCHEME = 'app';
const APP_ORIGIN = `${APP_SCHEME}://filex`;
// The main window is OUR page (rail + explorer + app settings), not the admin
// SPA. app.html lives in ui/, the explorer bundle in app/ — both served from
// this one origin so the page can `import './filex.js'` as a same-origin module.
const START_URL = `${APP_ORIGIN}/`;
const DEEP_LINK_SCHEME = 'filex';
// Packaged, build/ is not copied — electron-builder bakes the icon into the
// executable — so fall back to the source path for `electron .` runs.
const ICON_PATH = app.isPackaged
  ? path.join(process.resourcesPath, 'icon.png')
  : path.join(__dirname, '..', 'build', 'icon.png');
// Passed to ourselves by the login item, so a launch the USER did not ask for
// stays in the tray instead of throwing a window at a desktop that is still
// loading. See setLoginItem().
const HIDDEN_FLAG = '--hidden';
// electron-updater's NSIS installer relaunches us with this after a silent
// update (NsisUpdater passes `--updated`). Treated exactly like HIDDEN_FLAG: an
// update the user never asked for must not end with a window appearing in front
// of whatever they were doing. See applyUpdateQuietly().
const UPDATED_FLAG = '--updated';

let state: DesktopState = structuredClone(EMPTY_STATE);
/** Watches the active account's bell and raises native notifications. */
let notifier: DesktopNotifier | null = null;
let mainWindow: BrowserWindow | null = null;
let shellWindow: BrowserWindow | null = null;
let tray: Tray | null = null;
let pendingAuth: PendingAuth | null = null;
/** Why the last sign-in hand-back failed, shown on the sign-in window until a
 *  new attempt starts, one succeeds, or the person cancels (issue #36). */
let signInFailure: SignInFailure | null = null;
let quitting = false;
let supervisor: SyncSupervisor | null = null;
/** Keeps the computer out of idle sleep while sync is moving files. */
let sleepGuard: SleepGuard | null = null;
/** Local copies for dragging files OUT onto the desktop. See dragout.ts. */
let dragCache: DragOutCache | null = null;
/** The paths the last successful prepare() produced, keyed by the selection,
 *  so `drag:start` never has to re-derive (or re-check) them mid-gesture. */
let dragReady: { key: string; paths: string[] } | null = null;
let dragPrepare: AbortController | null = null;
/** The placeholder drag in flight: its watcher, and the transfer it becomes. */
let dragDrop: { cancel: () => void; dir: string } | null = null;
/** What the updater is doing, as far as the UI is concerned. */
// 'store': a store copy (src/channel.ts) — the store updates it, `url` is its page.
let updateState: { status: 'idle' | 'checking' | 'available' | 'downloading' | 'ready' | 'error' | 'manual' | 'store'; version?: string; percent?: number; error?: string; url?: string } = { status: 'idle' };

protocol.registerSchemesAsPrivileged([
  { scheme: APP_SCHEME, privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } },
]);

// ─────────────────────────── embedded bundle ───────────────────────────

function safeJoin(root: string, rel: string): string | null {
  const resolved = path.normalize(path.join(root, rel.replace(/^\/+/, '')));
  return resolved.startsWith(root) ? resolved : null;
}

function registerAppProtocol(): void {
  protocol.handle(APP_SCHEME, async (request) => {
    const { host, pathname } = new URL(request.url);
    const rel = decodeURIComponent(pathname);

    // app://shell/  — the pre-login chrome (connect + waiting screens).
    if (host === 'shell') {
      const file = rel === '/' || !path.extname(rel)
        ? path.join(UI_ROOT, 'index.html')
        : safeJoin(UI_ROOT, rel) ?? path.join(UI_ROOT, 'index.html');
      return net.fetch(pathToFileURL(file).toString());
    }

    // app://filex/ — the app itself. The root is our page; every other path is
    // an asset of the explorer bundle. Serving both from one origin is what
    // lets app.html import the component as a module without CORS games.
    if (rel === '/' || rel === '/index.html') {
      return net.fetch(pathToFileURL(path.join(UI_ROOT, 'app.html')).toString());
    }
    const asset = safeJoin(WEB_ROOT, rel);
    if (asset) {
      const res = await net.fetch(pathToFileURL(asset).toString());
      if (res.ok) return res;
    }
    return new Response('not found', { status: 404 });
  });
}

// ─────────────────────────── credentials on the wire ───────────────────────────

function originOf(url: string): string | null {
  try {
    return new URL(url).origin;
  } catch {
    return null;
  }
}

/** The credential for whoever owns this origin — the ACTIVE account first, so
 *  two accounts on one server resolve to the one the window is showing. */
function tokenForOrigin(origin: string | null): string | null {
  if (!origin) return null;
  const active = activeAccount(state);
  if (active && originOf(active.serverUrl) === origin) return active.token;
  return state.accounts.find((a) => originOf(a.serverUrl) === origin)?.token ?? null;
}

/**
 * Attaches the account's bearer to requests the PAGE cannot put a header on.
 *
 * `<img>`, `<video>`, `<audio>` and a download link carry no headers by
 * construction — the explorer hands those elements a plain URL. On the web that
 * is fine because the browser has a session cookie for the same origin; in this
 * app the page lives on `app://filex` and the only credential is a bearer
 * token, so every image preview, media player and download came back 401.
 *
 * Scoped to the signed-in servers' origins only — never a wildcard — and it
 * never overwrites an Authorization header the page set itself.
 */
function wireAuthHeaderInjection(): void {
  const origins = [...new Set(state.accounts.map((a) => originOf(a.serverUrl)).filter(Boolean))] as string[];
  // ⚠ An EMPTY url list means "every request" to Electron, which would be the
  // opposite of what this is for. With no accounts, match nothing instead.
  const urls = origins.length ? origins.map((o) => `${o}/*`) : ['https://filex.invalid/*'];
  session.defaultSession.webRequest.onBeforeSendHeaders({ urls }, (details, done) => {
    const headers = details.requestHeaders;
    if (headers.Authorization || headers.authorization) {
      done({ requestHeaders: headers });
      return;
    }
    const token = tokenForOrigin(originOf(details.url));
    if (token) headers.Authorization = `Bearer ${token}`;
    done({ requestHeaders: headers });
  });
}

/** True for the account's API surface — bytes, not pages. */
function isApiUrl(url: string, serverUrl: string): boolean {
  const origin = originOf(serverUrl);
  try {
    const u = new URL(url);
    return u.origin === origin && u.pathname.startsWith('/api/');
  } catch {
    return false;
  }
}

/**
 * Where a URL the app wants to "open" should actually go.
 *
 * ⚠ Not everything belongs in the browser. The API serves FILES: handing
 * `…/api/files/manager?action=download` to the browser asks a browser that may
 * not be signed in to fetch them, while this app holds the credential — so
 * those download in place, through the session that carries the token. Pages
 * (`/files/edit`, `/admin/`) do belong in the browser, where the user's real
 * session and their extensions live.
 */
const DOWNLOAD_STRINGS: Record<string, [en: string, tr: string]> = {
  done: ['Downloaded', 'İndirildi'],
  failed: ['Download failed', 'İndirilemedi'],
};

function downloadText(key: string): string {
  const pair = DOWNLOAD_STRINGS[key];
  return effectiveLocale() === 'tr' ? pair[1] : pair[0];
}

/** Every download of the app's windows, for the dock / taskbar bar. */
const downloads = new DownloadTally();
let downloadSeq = 0;

/**
 * A download to disk says how far it has got and how it ended.
 *
 * ⚠ The explorer's Download and ⌘K save through the window's own download
 * (openOutward, remote:download), and nothing listened to it: no progress, no
 * end, and a failure said nothing at all. The window's bar moves with the
 * bytes of every download at once; the end is a notification, and clicking a
 * finished one shows the file in its folder.
 */
function watchDownloads(): void {
  session.defaultSession.on('will-download', (_e, item) => {
    const id = String(++downloadSeq);
    const paint = () => {
      const f = downloads.fraction();
      for (const w of BrowserWindow.getAllWindows()) w.setProgressBar(f);
    };
    downloads.update(id, item.getReceivedBytes(), item.getTotalBytes());
    paint();
    item.on('updated', () => {
      downloads.update(id, item.getReceivedBytes(), item.getTotalBytes());
      paint();
    });
    item.once('done', (_ev, state_) => {
      downloads.finish(id);
      paint();
      const ending = downloadEnding(state_);
      if (!ending) return;
      const name = item.getFilename();
      log('download', ending, { name });
      try {
        if (!Notification.isSupported()) return;
        const n = new Notification({ title: downloadText(ending), body: name });
        const saved = item.getSavePath();
        if (ending === 'done' && saved) n.on('click', () => shell.showItemInFolder(saved));
        n.show();
      } catch {
        /* a courtesy, never a failure path */
      }
    });
  });
}

function openOutward(url: string, from?: BrowserWindow | null): void {
  const acc = activeAccount(state);
  if (acc && isApiUrl(url, acc.serverUrl)) {
    (from ?? mainWindow)?.webContents.downloadURL(url);
    return;
  }
  if (/^(https?|mailto):/i.test(url)) {
    void shell.openExternal(url);
    return;
  }
  // ⚠ `app://`, `blob:` and `data:` have no OS handler. Passing them to
  // shell.openExternal returns without error and does NOTHING — which is
  // exactly how "Open in new tab" managed to be a dead button for a whole
  // release. Say it out loud instead of failing silently.
  console.warn(`[filex] refusing to open a URL the OS cannot handle: ${url}`);
}

// ─────────────────────────── windows ───────────────────────────

function preload(name: string): string {
  return path.join(__dirname, name);
}

/**
 * The colour Electron paints the window with before the document has rendered
 * a single pixel.
 *
 * ⚠ It has to be a literal, and it must not be a colour of our own choosing.
 * Both are satisfied by remembering what the RENDERER last resolved
 * `--fe-bg` to (state.themeBg): that is the active palette's own ground in the
 * active variant, so a person on Forest Dark opens onto Forest Dark rather
 * than onto a white rectangle that repaints a beat later.
 *
 * The fallback is the stock palette's two grounds, straight out of
 * packages/core/src/styles/variables.css, and it is reached exactly once — on
 * a first-ever launch, before any page has had a chance to report.
 */
function windowGround(): string {
  const remembered = state.themeBg;
  // A CSS colour, from our own renderer, but validated anyway: this string is
  // handed to a native API, and an unparseable value makes Electron throw at
  // window construction — an app that will not open.
  if (remembered && /^#[0-9a-fA-F]{3,8}$/.test(remembered)) return remembered;
  return nativeTheme.shouldUseDarkColors ? '#15171c' : '#ffffff';
}

function openShell(route: string, title: string, width = 720, height = 620): void {
  if (shellWindow && !shellWindow.isDestroyed()) {
    // ⚠⚠ RELOAD when the window is already on this route — loading the same
    // URL again no longer does. Up to Electron 31 (Chromium 126) `loadURL`
    // of the page's own `#fragment` URL was a full reload; Electron 44 makes
    // it a same-document navigation (measured: no did-finish-load, one
    // did-navigate-in-page, page state intact), and with the hash unchanged
    // no `hashchange` fires either. That is exactly the failed-sign-in path:
    // handleDeepLink() records the failure and calls openShell('/connect') on
    // a window that IS on /connect, so the page kept its old text and "that
    // link was from an earlier attempt" / "this code cannot be used again"
    // never appeared. signin-retry-e2e.mjs caught it (red on 44, green on 31).
    const target = `app://shell/#${route}`;
    if (shellWindow.webContents.getURL() === target) shellWindow.webContents.reload();
    else void shellWindow.loadURL(target);
    shellWindow.setTitle(title);
    shellWindow.focus();
    return;
  }
  shellWindow = new BrowserWindow({
    width,
    height,
    title,
    icon: ICON_PATH,
    autoHideMenuBar: true,
    // The sign-in window had no ground at all, so it opened as Electron's
    // default white and then repainted — on the product's FIRST screen.
    show: false,
    backgroundColor: windowGround(),
    webPreferences: { preload: preload('preload-shell.cjs'), contextIsolation: true, sandbox: true },
  });
  shellWindow.once('ready-to-show', () => shellWindow?.show());
  shellWindow.on('closed', () => {
    shellWindow = null;
  });
  void shellWindow.loadURL(`app://shell/#${route}`);
}

function openMainWindow(): void {
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.show();
    mainWindow.focus();
    return;
  }
  mainWindow = new BrowserWindow({
    width: 1280,
    height: 820,
    minWidth: 720,
    minHeight: 520,
    title: 'filex',
    icon: ICON_PATH,
    autoHideMenuBar: true,
    // ⚠ Both of these are about the first second of the app's life. Electron
    // paints a window WHITE before the document has rendered, so on a dark
    // desktop the app opened as a white rectangle and then repainted — and the
    // window appeared before the explorer had drawn a single row, which is what
    // made "Connecting…" the first thing anyone saw. Show it once it has
    // something to show, on a ground that matches the app.
    show: false,
    // ⚠ Was `nativeTheme.shouldUseDarkColors ? '#14181d' : '#ffffff'` — which
    // asked the OS a question the PRODUCT answers (somebody on a light desktop
    // who picked Dark opened onto a white flash), and whose dark value was not
    // a background in this product at all: #14181d was the old shell's text
    // colour. See windowGround().
    backgroundColor: windowGround(),
    // yeni-pencere:v1 — frameless: the native OS caption is gone; the app page
    // draws its own slim title bar with our minimize/maximize/close (Win/Linux),
    // or leaves room for the native traffic lights (macOS). See app.html.
    ...docWindowChrome(),
    webPreferences: { preload: preload('preload-app.cjs'), contextIsolation: true, sandbox: true },
  });
  mainWindow.once('ready-to-show', () => mainWindow?.show());

  // Closing the window parks the app in the tray instead of killing it. A sync
  // client that stops syncing the moment its window is shut is not a sync
  // client — this is the behaviour every file-sync app has.
  mainWindow.on('close', (e) => {
    if (!quitting && state.runInBackground) {
      e.preventDefault();
      mainWindow?.hide();
    }
  });
  mainWindow.on('closed', () => {
    mainWindow = null;
  });
  // External links belong in the browser, not in a window with a token in it.
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    openOutward(url, mainWindow);
    return { action: 'deny' };
  });

  // ⚠ A plain <a href> — the preview modal's download button is one — navigates
  // this window. Without this guard the whole app is replaced by whatever that
  // URL returns, and there is no back button to come home with: the file
  // manager simply becomes a JSON error page.
  mainWindow.webContents.on('will-navigate', (e, url) => {
    if (url.startsWith(APP_ORIGIN)) return;
    e.preventDefault();
    openOutward(url, mainWindow);
  });

  void mainWindow.loadURL(START_URL);
}

/** Shows whichever surface matches the current state. */
function route(): void {
  if (activeAccount(state)) openMainWindow();
  else openShell('/connect', 'filex — Connect');
}

// ─────────────────────── native notifications ───────────────────────
//
// ⚠ The DESTINATION of a click is not decided here. It is resolved by
// web/src/lib/notificationTarget.ts — the same module the browser bell uses —
// and handed to the page as a structured destination. That is what stops the
// desktop notification from opening one place while the bell opens another.

// ────────────── the count, on the icon ──────────────
//
// Rule 3, docs/NOTIFICATIONS.md → "The bell, and who can reach it": *the count
// lives ON the icon* — in the explorer, in the desktop app, and on mobile when
// it comes; exact to 99 and `99+` above that; the SAME rule on every surface.
//
// ⚠⚠ The desktop window is the explorer and has no bell in it, so there is
// no button here to draw a badge on. The icon this app has is the one in the
// dock / taskbar and the one in the tray, and that is where the number goes.
// Until now it went nowhere at all: the app polled the bell every 15 s, raised
// a toast, and then showed no trace that anything was waiting — a person who
// missed the toast had no way to learn they had 40 unread rows.
//
// ⚠ The `99+` ceiling is the WEB badge's, not this one's: it is about how
// much room a 16px circle has. macOS draws the number itself and is
// perfectly happy with 137, so `setBadgeCount` gets the real count
// (`unreadBadgeCount`) while the tray tooltip — which is text we lay out
// ourselves — gets the clamped label. One module decides both.

/** The last count we were told, per account id — so a rail switch shows the
 *  account in front of you rather than the one you left. */
let unreadByAccount: Record<string, number> = {};

function applyUnreadBadge(accountId: string, count: number): void {
  unreadByAccount = { ...unreadByAccount, [accountId]: count };
  paintUnread();
}

function paintUnread(): void {
  const acc = activeAccount(state);
  const count = acc ? (unreadByAccount[acc.id] ?? 0) : 0;
  try {
    // ⚠ Windows has no dock badge: `setBadgeCount` is a no-op there rather
    // than an error, and the tray tooltip below is what that platform reads.
    // Since Electron 44 the same holds on Linux: the one desktop that drew a
    // launcher badge was Unity, Electron dropped Unity support and the call
    // is macOS-only now — so Linux reads the tooltip too.
    app.setBadgeCount?.(unreadBadgeCount(count));
  } catch {
    /* a platform that will not take a badge still gets the tooltip */
  }
  paintTrayTip();
}

let lastTrayTip = '';

/**
 * The tray's tooltip: the pause, sync's state and the unread count, in ONE
 * sentence (sync-policy.ts trayTooltip). The pause used to be written by
 * refreshTray and overwritten the same moment by the unread count's own
 * tooltip, and sync was not in it at all.
 */
function paintTrayTip(): void {
  if (!tray) return;
  const acc = activeAccount(state);
  const count = acc ? (unreadByAccount[acc.id] ?? 0) : 0;
  const statuses = supervisor?.statuses() ?? [];
  const tip = trayTooltip(
    {
      paused: state.syncPaused === true,
      unreadLabel: unreadBadgeLabel(count),
      syncing: syncBusy(statuses),
      failing: statuses.some((st) => st.signedOut !== true && anyError(st)),
    },
    { paused: trayText('pausedState'), syncing: trayText('syncingState'), failing: trayText('failingState') },
  );
  if (tip === lastTrayTip) return;
  lastTrayTip = tip;
  tray.setToolTip(tip);
}

function startNotifier(): void {
  if (notifier) return;
  notifier = new DesktopNotifier({
    account: () => {
      const acc = activeAccount(state);
      // A token the server refused is not asked again every 15 seconds —
      // until Reconnect clears the mark.
      return acc && !acc.signedOut ? { id: acc.id, serverUrl: acc.serverUrl, token: acc.token } : null;
    },
    enabled: () => state.notifications !== false,
    fetchRows: async (acc, limit) => {
      const url = new URL('/api/notifications', acc.serverUrl);
      url.searchParams.set('unread', 'true');
      url.searchParams.set('limit', String(limit));
      const res = await net.fetch(url.toString(), { headers: { Authorization: `Bearer ${acc.token}` } });
      // The status travels as a property: isUnauthorized() reads that, not
      // the wording.
      if (!res.ok) throw Object.assign(new Error(`server said ${res.status}`), { status: res.status });
      const body = (await res.json()) as { items?: NotificationRow[]; total?: number };
      const items = body.items ?? [];
      // ⚠ `total` is the count for `unread=true` — the unread count, which
      // the badge below needs and which this one request already carries.
      return { items, total: typeof body.total === 'number' ? body.total : items.length };
    },
    onUnread: (accountId, count) => applyUnreadBadge(accountId, count),
    onUnauthorized: (accountId) => markSignedOut(accountId, 'the bell was refused twice in a row (HTTP 401)'),
    // The reader's language, read per row — see DesktopNotifierOptions.locale.
    locale: () => effectiveLocale(),
    show: (row, text, onClick) => {
      if (!Notification.isSupported()) return;
      const n = new Notification({
        // ⚠ Composed from the row's event + metadata by the SHARED renderer
        // (web/src/lib/notificationText.ts), in this window's language — not
        // taken from the server's `title`, which is written once in whatever
        // language the server was configured with and, for most file events,
        // is not written at all (`row.title` is then the literal event id).
        title: text.title,
        // ⚠ A notification carries a name, a count and a target — never file
        // content, never a credential. The renderer interpolates only paths,
        // names, counts and a comment excerpt, all of which the row already
        // carries in plain sight.
        body: text.body,
      });
      n.on('click', onClick);
      n.show();
    },
    onOpen: (accountId, dest) => {
      // ⚠ A share opens in the SYSTEM BROWSER. The app window is a file
      // manager pointed at one account with a bearer token; a public share
      // page is an anonymous web page, and loading it in here would replace
      // the app with a page it has no way back from.
      if (dest.kind === 'share') {
        const acc = state.accounts.find((a) => a.id === accountId);
        if (acc) void shell.openExternal(new URL(`/s/${encodeURIComponent(dest.token)}`, acc.serverUrl).toString());
        return;
      }
      // The window may be closed (running in the tray) — a notification the
      // user clicks has to be able to bring the app back, not silently do
      // nothing. openMainWindow() shows and focuses an existing one.
      openMainWindow();
      // A folder or the Trash view (opensInWindow); anything else has nothing
      // to open beyond the window.
      if (!opensInWindow(dest)) return;
      const send = () => mainWindow?.webContents.send('notify:open', { accountId, dest });
      if (mainWindow && mainWindow.webContents.isLoading()) {
        mainWindow.webContents.once('did-finish-load', send);
      } else {
        send();
      }
    },
    log: (msg, extra) => log('notify', msg, extra),
  });
  notifier.start();
}

// ─────────────────────────── tray ───────────────────────────

function buildTray(): void {
  if (tray) return;
  // Real artwork, downscaled for the tray. Falls back to an empty image rather
  // than crashing if the file is missing in some packaging layout.
  let img = nativeImage.createFromPath(ICON_PATH);
  img = img.isEmpty() ? nativeImage.createEmpty() : img.resize({ width: 16, height: 16 });
  tray = new Tray(img);
  refreshTray();
  tray.on('click', () => route());
}

/** Everything that has to follow a change to the account set: the tray label,
 *  and the credential injector — which is filtered on the signed-in origins and
 *  would otherwise keep serving the previous account's token. */
function accountsChanged(): void {
  refreshTray();
  wireAuthHeaderInjection();
}

/** The chosen language, or what the OS says when the choice is 'system'.
 *  One resolver for the tray, the window and the explorer — three places
 *  deciding this for themselves is how a Turkish menu ends up on an English
 *  window. */
function effectiveLocale(): 'en' | 'tr' {
  if (state.locale === 'en' || state.locale === 'tr') return state.locale;
  return app.getLocale().toLowerCase().startsWith('tr') ? 'tr' : 'en';
}

/** The tray's own strings. Tiny on purpose: the window has the real catalogue,
 *  and duplicating it into the main process would be two tables to keep in
 *  step. Anything not listed here has no business being in a tray menu. */
const TRAY_STRINGS: Record<string, [en: string, tr: string]> = {
  signedOut: ['Not signed in', 'Giriş yapılmadı'],
  open: ['Open filex', "filex'i aç"],
  signedOutSuffix: ['signed out', 'oturum kapalı'],
  pause: ['Pause sync', 'Eşitlemeyi duraklat'],
  resume: ['Resume sync', 'Eşitlemeyi sürdür'],
  pausedState: ['sync paused', 'eşitleme duraklatıldı'],
  syncingState: ['syncing…', 'eşitleniyor…'],
  failingState: ['a folder could not be synced — see Settings', 'bir klasör eşitlenemedi — Ayarlar’a bak'],
  updateReady: ['Update {v} ready — installs itself (or now)', '{v} güncellemesi hazır — kendiliğinden kurulur (ya da şimdi)'],
  settings: ['Settings…', 'Ayarlar…'],
  quit: ['Quit filex', "filex'ten çık"],
};

function trayText(key: string, vars: Record<string, string> = {}): string {
  const pair = TRAY_STRINGS[key];
  const raw: string = effectiveLocale() === 'tr' ? pair[1] : pair[0];
  return Object.entries(vars).reduce<string>((acc, [k, v]) => acc.replaceAll(`{${k}}`, v), raw);
}

function refreshTray(): void {
  if (!tray) return;
  const acc = activeAccount(state);
  const paused = state.syncPaused === true;
  // The tray icon is often all there is on screen: a paused client has to be
  // recognisable from it without opening anything (paintTrayTip, below via
  // paintUnread).
  // ⚠ The badge follows the ACTIVE account, and every caller of this function
  // is a moment the active one may just have changed (boot, a rail switch, a
  // sign-out). Repainting here keeps the number on the icon the number for the
  // server in front of you rather than for the one you left.
  paintUnread();
  tray.setContextMenu(
    Menu.buildFromTemplate([
      {
        label: acc
          ? `${acc.email} — ${new URL(acc.serverUrl).host}${acc.signedOut ? ` (${trayText('signedOutSuffix')})` : ''}`
          : trayText('signedOut'),
        enabled: false,
      },
      { type: 'separator' },
      { label: trayText('open'), click: () => route() },
      {
        label: trayText(paused ? 'resume' : 'pause'),
        click: () => void setSyncPaused(!paused).catch((e) => log('sync', 'pause failed', String(e))),
      },
      // The update installs itself — while you are away, or when you quit. This
      // line is for the person who would rather have it now than later, so it
      // says what will happen either way; it is not a prompt to act on.
      ...(updateState.status === 'ready'
        ? [{
            label: trayText('updateReady', { v: updateState.version ?? '' }),
            click: () => applyUpdateQuietly(),
          }]
        : []),
      {
        label: trayText('settings'),
        click: () => {
          route();
          mainWindow?.webContents.send('app:open-settings');
        },
      },
      { type: 'separator' },
      {
        label: trayText('quit'),
        click: () => {
          quitting = true;
          app.quit();
        },
      },
    ]),
  );
}

// ─────────────────────────── deep link ───────────────────────────

/** Finishes an authorization. THROWS on failure so a caller that has a UI (the
 *  manual code box) can show the reason inline instead of behind a modal. */
async function completeAuth(state_: string, code: string): Promise<void> {
  if (!pendingAuth) throw new Error('no sign-in is waiting — start again');
  // ⚠ Before the exchange, not at saveState: the exchange spends the one-time
  // code, and a token fetched only to be refused storage would also sit in
  // this process's state as a signed-in account until the app quits.
  const k = keychain();
  if (k !== 'ok') throw keychainRefusal(k);
  const attempt = pendingAuth;
  const { token, email } = await exchangeCode(attempt, state_, code);
  // Only clear the attempt once it actually worked: a mistyped code must leave
  // the user able to try again rather than sending them back to the start.
  pendingAuth = null;
  signInFailure = null;
  const { account, existed } = signIn(state, { serverUrl: attempt.serverUrl, email, token });
  saveState(state);
  accountsChanged();
  // ⚠ Signing in again to an account this computer already has keeps its id,
  // pairs and filex folder and replaces only the token — and its watcher was
  // started with the OLD token in its environment. reconcile() only starts
  // watchers that are missing, so without this the replaced (often revoked)
  // token kept being used until the app restarted.
  if (existed) supervisor?.stop(account.id);
  shellWindow?.close();
  const hadWindow = !!mainWindow && !mainWindow.isDestroyed();
  openMainWindow();
  if (existed && hadWindow) {
    // A reconnect: the window is showing the signed-out screen, or an explorer
    // built around the refused token. Start it over rather than patch it.
    mainWindow?.reload();
  } else {
    // ⚠ Tell the window. Adding a SECOND account happens in a different
    // window, and openMainWindow() only shows the existing one — it does not
    // reload it. Without this the new account was stored but the rail kept
    // showing one avatar until the app was restarted. Measured, not theorised.
    mainWindow?.webContents.send('sync:changed');
  }
  refreshTray();
  void refreshPairs();
}

/**
 * The server no longer accepts this account's token — it was revoked, or it
 * expired. Mark the account (stored, so a reboot does not retry it), stop
 * everything that uses the token, and say so ONCE.
 *
 * ⚠ What happened before: a watcher printing `HTTP 401` every 30 seconds, a
 * bell poll logging it every 15, a file view with only "Try again" on it — and
 * after a reboot, all of it again. While the mark is set the account has no
 * watcher (watcherAccounts) and no bell poll; the window offers Reconnect,
 * which signs in again in the browser and keeps the account's id, pairs and
 * filex folder (signIn clears the mark).
 */
function markSignedOut(accountId: string, why: string): void {
  const acc = state.accounts.find((a) => a.id === accountId);
  if (!acc || acc.signedOut) return;
  acc.signedOut = new Date().toISOString();
  log('auth', "the server no longer accepts this account's token; sync and the bell stop until Reconnect", {
    accountId,
    why,
  });
  try {
    saveState(state);
  } catch (e) {
    log('auth', 'could not store the signed-out mark', String((e as Error)?.message ?? e));
  }
  supervisor?.stop(accountId);
  refreshTray();
  for (const w of BrowserWindow.getAllWindows()) w.webContents.send('sync:changed');
  try {
    if (Notification.isSupported()) {
      const n = new Notification({
        title: syncText('signedOutTitle', { email: acc.email, host: new URL(acc.serverUrl).host }),
        body: syncText('signedOutBody'),
      });
      n.on('click', () => openMainWindow());
      n.show();
    }
  } catch {
    /* the notification is a courtesy; the window and Settings say it too */
  }
}

/**
 * OS-delivered deep link.
 *
 * ⚠⚠ A failure must NOT drop the person back on the server-address form
 * (issue #36). It used to — a modal, then `openShell('/connect')`, which
 * reloaded the sign-in page and threw away its waiting screen: no address to
 * copy, no code box, while the attempt was still pending here. The window now
 * comes back on the waiting screen of that same attempt with the reason on it
 * (src/signin-flow.ts decides what it shows); only when nothing is pending does
 * it show the server form.
 */
async function handleDeepLink(raw: string): Promise<void> {
  const parsed = parseAuthDeepLink(raw);
  if (!parsed) return;
  const pendingState = pendingAuth?.state ?? null;
  try {
    await completeAuth(parsed.state, parsed.code);
  } catch (err) {
    signInFailure = failureOf(pendingState, parsed.state, err);
    log('auth', 'a sign-in link did not finish', { kind: signInFailure.kind, detail: signInFailure.detail });
    openShell('/connect', 'filex — Connect');
  }
}

// ─────────────────────────── ipc ───────────────────────────

// ─────────────────────────── auto-update ───────────────────────────
//
// The app keeps itself current. A file manager that syncs folders in the
// background is exactly the kind of program nobody thinks to go and re-download
// — the desktop packages were being installed by hand, so every fix shipped
// only to whoever remembered to fetch it.
//
// The feed is a plain static directory on filex.sh, NOT the GitHub provider:
// the GitHub mirror of this repo is private, and that provider would need a
// token shipped inside the app to read its releases. The CLI's update manifest
// already lives on the same static site.
//
// Policy: the update happens BY ITSELF and the user never sees it — download
// quietly, install silently, come back in the tray. Nobody is asked to restart,
// and nobody is ever handed an installer window.
//
// ⚠⚠ The installer is only silent if we SAY so. `quitAndInstall()` defaults to
// `isSilent = false`, which runs the NSIS installer with its full wizard — that
// is what turned "your app updated itself" into "your app threw a setup screen
// at you", and it is why every call here passes (true, true): silent, then
// relaunch. The relaunch carries `--updated`, which starts us in the tray.
//
// When it happens: on quit (electron-updater does that for us), or — because
// this app lives in the tray for days at a time — once the machine has been
// idle for a while and no window is open. That is the Discord shape: it swaps
// itself out while you are away from the keyboard, not while you are typing.
//
// `FILEX_NO_UPDATE=1` turns the whole thing off (used by the E2E rig, which
// must not race a download).

const { autoUpdater } = electronUpdater;

/** Every window that is showing state gets the new state. */
function pushUpdateState(next: typeof updateState): void {
  updateState = next;
  for (const w of BrowserWindow.getAllWindows()) w.webContents.send('sync:changed');
}

// ─── builds that cannot swap themselves: honesty instead of a broken swap ───
//
// Two builds can never apply an update in place, for unrelated reasons:
//
//   • macOS without a Developer ID. Squirrel.Mac refuses to replace an app
//     that is not Developer-ID signed, and electron-updater only finds that
//     out AFTER the download: the check announced "version X installs itself
//     shortly", the download completed, the swap was refused — and the user
//     was left with a generic "could not check for updates". A permanent,
//     known limitation of the ad-hoc sealed build was being dressed up as a
//     transient error, on every check, forever.
//   • The Windows PORTABLE build. It is one self-extracting .exe, not an
//     installation: there is no NSIS install directory to differential-patch
//     and no Squirrel to hand the running copy over to. electron-updater would
//     download a whole installer and then have nowhere to put it — and running
//     that installer is precisely what somebody who chose the portable copy
//     said no to.
//
// Both take the same route, and it is the one the mac case established: the
// updater is never wired at all — nothing is downloaded that can never be
// applied — and the same check cadence (and the Settings button) instead reads
// the static feed directly and says the honest thing: a newer version exists,
// here is the download. Settings swaps its copy for the matching explanation
// rather than sitting in `status: 'checking'` forever.

const FEED_DIR_URL = 'https://filex.sh/desktop/';
// Per platform, because the feeds are separate files. Only the macOS and
// Windows names are reachable today (they are the two builds that cannot swap
// themselves); latest-linux.yml is named anyway rather than letting a future
// third case silently inherit the wrong filename.
const FEED_FILE =
  process.platform === 'darwin'
    ? 'latest-mac.yml'
    : process.platform === 'linux'
      ? 'latest-linux.yml'
      : 'latest.yml';
const FEED_URL = FEED_DIR_URL + FEED_FILE;

/** Why this build cannot update itself, or null when it can. Drives both the
 *  updater wiring and the words Settings uses. */
type ManualUpdateReason = 'macos-unsigned' | 'windows-portable';
let manualUpdates: ManualUpdateReason | null = null;

async function detectManualUpdates(): Promise<void> {
  if (!app.isPackaged) return;
  // ⚠ Checked before the mac branch and without touching the disk: a portable
  // copy is portable whether or not the folder beside it turned out to be
  // writable. Falling back to %APPDATA% for storage does not turn a
  // self-extracting .exe into something an installer can replace.
  if (portable.portable) {
    manualUpdates = 'windows-portable';
    return;
  }
  if (process.platform !== 'darwin') return;
  const out = await new Promise<string>((resolve) => {
    execFile('/usr/bin/codesign', ['-dv', app.getPath('exe')], (err, stdout, stderr) => {
      // codesign prints the signature details on stderr; an error still
      // carries them (or means no signature at all, which is also "manual").
      resolve(String(stderr || stdout || err?.message || ''));
    });
  });
  if (!/Developer ID Application/.test(out)) manualUpdates = 'macos-unsigned';
}

/** Numeric, segment-wise. Good for x.y.z; anything odd compares equal. */
function isNewerVersion(candidate: string, current: string): boolean {
  const a = candidate.split('.').map((n) => parseInt(n, 10) || 0);
  const b = current.split('.').map((n) => parseInt(n, 10) || 0);
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    if ((a[i] ?? 0) !== (b[i] ?? 0)) return (a[i] ?? 0) > (b[i] ?? 0);
  }
  return false;
}

/** The artifact THIS build's user should be handed: the disk image on macOS,
 *  the portable executable for a portable copy. Matched against the feed's own
 *  file list so a rename in electron-builder.yml cannot leave a dead link. */
function manualDownloadPattern(): RegExp {
  return manualUpdates === 'windows-portable'
    ? /^\s*-?\s*(?:url|path):\s*(\S*portable\S*\.exe)\s*$/im
    : /^\s*-?\s*(?:url|path):\s*(\S+\.dmg)\s*$/im;
}

/**
 * The portable artifact's filename, rebuilt from the same pieces
 * electron-builder.yml assembles it from (`${productName}-desktop-portable-
 * ${arch}.${ext}`).
 *
 * ⚠ Needed because the Windows feed does NOT list this file — measured:
 * electron-builder writes only the nsis installer into latest.yml, since that
 * is the artifact the updater would apply. Sending a portable user to the
 * installer is the one thing this build exists to avoid, so when the feed
 * names nothing usable the link is built here instead. scripts/portable-e2e
 * .mjs asserts this string against what the build actually produced, so a
 * rename breaks a test rather than somebody's download.
 */
const PORTABLE_ARTIFACT = `filex-desktop-portable-${process.arch}.exe`;

async function checkFeedForManualUpdate(): Promise<void> {
  pushUpdateState({ status: 'checking' });
  try {
    const res = await net.fetch(FEED_URL, { signal: AbortSignal.timeout(15_000) });
    if (!res.ok) throw new Error(`feed answered ${res.status}`);
    const yml = await res.text();
    const version = /^version:\s*(\S+)/m.exec(yml)?.[1];
    if (!version) throw new Error('feed carries no version');
    if (!isNewerVersion(version, app.getVersion())) {
      pushUpdateState({ status: 'idle' });
      return;
    }
    // Hand the browser the artifact itself when the feed names one; the plain
    // downloads directory otherwise.
    // ⚠ The Windows feed lists the INSTALLER, and a portable user must not be
    // sent one — so a name that does not match is not used at all, and the
    // directory listing is the honest fallback.
    const named = manualDownloadPattern().exec(yml)?.[1]
      ?? (manualUpdates === 'windows-portable' ? PORTABLE_ARTIFACT : null);
    const url = named ? new URL(named, FEED_DIR_URL).toString() : FEED_DIR_URL;
    pushUpdateState({ status: 'manual', version, url });
  } catch (e) {
    pushUpdateState({ status: 'error', error: String((e as Error)?.message ?? e).slice(0, 200) });
  }
}

function wireAutoUpdate(): void {
  // Unpackaged runs have no updater metadata and would log an error on every
  // check; a machine told not to update must not phone home at all.
  if (!app.isPackaged || process.env.FILEX_NO_UPDATE === '1') return;

  // ⚠⚠ A store copy is the store's to update, and this file must not even
  // LOOK at the feed for one. electron-updater has no idea it is inside an
  // MSIX package or a Flatpak: it would download the NSIS installer (or a
  // .deb) and run it from inside the package — on Windows that installs a
  // second copy into the package's private AppData, invisible to Settings and
  // to the Store's own uninstall. See src/channel.ts.
  if (CURRENT_CHANNEL) {
    pushUpdateState({ status: 'store', url: storePageUrl(CURRENT_CHANNEL) });
    return;
  }

  if (manualUpdates) {
    // Same cadence as the real updater — but only ever LOOKING. No download
    // starts on a machine that cannot apply it.
    setTimeout(() => void checkFeedForManualUpdate(), 30_000);
    setInterval(() => void checkFeedForManualUpdate(), 6 * 60 * 60 * 1000);
    return;
  }

  autoUpdater.autoDownload = true;
  // ⚠ The install must happen on quit, not mid-session: the sync engine is a
  // child process moving files, and replacing the app under it is how a
  // half-copied file becomes somebody's only copy.
  autoUpdater.autoInstallOnAppQuit = true;
  autoUpdater.logger = null;

  autoUpdater.on('checking-for-update', () => pushUpdateState({ status: 'checking' }));
  autoUpdater.on('update-available', (i) => pushUpdateState({ status: 'available', version: i?.version }));
  autoUpdater.on('update-not-available', () => pushUpdateState({ status: 'idle' }));
  autoUpdater.on('download-progress', (p) =>
    pushUpdateState({ status: 'downloading', percent: Math.round(p?.percent ?? 0), version: updateState.version }));
  autoUpdater.on('update-downloaded', (i) => {
    pushUpdateState({ status: 'ready', version: i?.version });
    refreshTray();
    watchForAQuietMoment();
  });
  // ⚠ Silent by design. No network, a feed that 404s, a machine behind a proxy
  // — none of that is the user's problem while the app itself works. The state
  // is recorded so Settings can say so if anyone looks.
  autoUpdater.on('error', (e) => pushUpdateState({ status: 'error', error: String(e?.message ?? e).slice(0, 200) }));

  const check = () => {
    autoUpdater.checkForUpdates().catch(() => {
      /* handled by the error event above */
    });
  };
  // Not at t=0: the first seconds after launch belong to the window and the
  // sync engine, and a machine that just woke up may not have a route yet.
  setTimeout(check, 30_000);
  setInterval(check, 6 * 60 * 60 * 1000);
}

/** How long the machine must be untouched before we swap the app underneath it.
 *  Ten minutes is "gone to a meeting", not "paused to read something". */
const IDLE_SECONDS_BEFORE_APPLY = 10 * 60;

let quietMomentTimer: ReturnType<typeof setInterval> | null = null;
let applying = false;

/**
 * Waits for a moment when applying the update costs the user nothing, then does
 * it — no prompt, no restart button, no installer window.
 *
 * Two conditions, and both matter:
 *   - the human is away (`powerMonitor.getSystemIdleTime()`), because the app
 *     disappears and comes back during the swap;
 *   - no window is open, because a window vanishing mid-look is the disruption
 *     this whole design exists to avoid.
 *
 * If neither ever happens, nothing is lost: electron-updater still installs
 * silently on quit. This only shortens the wait for an app that is never quit.
 */
function watchForAQuietMoment(): void {
  if (quietMomentTimer) return;
  quietMomentTimer = setInterval(() => {
    // ⚠ The engine counts as "someone": the swap stops every watcher, and an
    // idle machine with no window open is exactly what an overnight first sync
    // looks like. See quietMomentForUpdate (src/power.ts).
    const now = quietMomentForUpdate({
      ready: updateState.status === 'ready',
      applying,
      windowOpen: BrowserWindow.getAllWindows().some((w) => !w.isDestroyed() && w.isVisible()),
      idleSeconds: powerMonitor.getSystemIdleTime(),
      idleThreshold: IDLE_SECONDS_BEFORE_APPLY,
      syncBusy: syncBusy(supervisor?.statuses() ?? []),
    });
    if (now) applyUpdateQuietly();
  }, 60_000);
}

/**
 * Installs the downloaded update and comes back in the tray.
 *
 * ⚠ The sync watchers are stopped first. They are child processes moving files;
 * replacing the binary under one mid-copy is how a half-written file becomes
 * somebody's only copy. They start again by themselves on the next launch.
 */
function applyUpdateQuietly(): void {
  if (applying) return;
  applying = true;
  if (quietMomentTimer) {
    clearInterval(quietMomentTimer);
    quietMomentTimer = null;
  }
  supervisor?.stopAll();
  sleepGuard?.release();
  quitting = true;
  // (silent, relaunch) — see the ⚠⚠ note above: the defaults are (false, false),
  // which shows the installer and then leaves the app closed.
  autoUpdater.quitAndInstall(true, true);
}

// ─────────────────────────── start at login ───────────────────────────
//
// ⚠⚠ `app.setLoginItemSettings({ openAtLogin })` with nothing else registers
// `process.execPath`. In a PACKAGED app that is the installed executable and is
// correct; in a DEV run (`electron .`) it is
// node_modules/electron/dist/electron.exe — and a bare electron.exe with no
// project path opens Electron's own welcome window. Windows then keeps that
// command in HKCU\…\Run forever, long after the checkout it pointed at is gone.
// Measured on a real machine: `electron.app.Electron` →
// `G:\filex\node_modules\.pnpm\electron@31.7.7\…\electron.exe`, which is exactly
// what the user saw open at every sign-in.
//
// So: the login item is a packaged-only feature, the command is written out
// explicitly rather than implied, and it carries HIDDEN_FLAG — which is what
// makes the promise in the settings copy ("launches minimised to the tray")
// true instead of decorative.

/** The exact command the OS is asked to run — also the query key, because
 *  Windows' getLoginItemSettings only recognises an entry it is asked about
 *  with the SAME path and args it was created with. */
function loginItemSpec(): { path: string; args: string[] } {
  return { path: loginItemExecutable(process.env, process.execPath), args: [HIDDEN_FLAG] };
}

/** Linux has no login-item API in Electron; XDG autostart is the equivalent.
 *  Without this the toggle is dead on every Linux build — silently. */
function linuxAutostartFile(name = LINUX_AUTOSTART_NAME): string {
  return path.join(app.getPath('home'), '.config', 'autostart', name);
}

function setLinuxAutostart(on: boolean): void {
  const file = linuxAutostartFile();
  if (!on) {
    try {
      fs.rmSync(file, { force: true });
    } catch {
      /* nothing to remove */
    }
    return;
  }
  const { path: exe, args } = loginItemSpec();
  const body = [
    '[Desktop Entry]',
    'Type=Application',
    'Name=filex',
    'Comment=Keep your folders in sync',
    `Exec="${exe}" ${args.join(' ')}`,
    'Terminal=false',
    'X-GNOME-Autostart-enabled=true',
    '',
  ].join('\n');
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, body, 'utf8');
}

/** Whether this run can have a login item at all: a dev run refuses to write
 *  one (it could only register the bare electron binary — see above). */
function loginItemSupported(): boolean {
  return app.isPackaged || process.platform === 'linux';
}

/** What the OS says about our login item right now. */
function loginItemReport(): LoginItemReport {
  if (process.platform === 'linux') {
    return { platform: 'linux', autostartFile: fs.existsSync(linuxAutostartFile()) };
  }
  if (!app.isPackaged) return { platform: process.platform };
  const s = app.getLoginItemSettings(loginItemSpec());
  return {
    platform: process.platform,
    openAtLogin: s.openAtLogin,
    executableWillLaunchAtLogin: s.executableWillLaunchAtLogin,
    launchItems: s.launchItems,
    status: s.status,
  };
}

/** True when the OS will actually launch this app at the next sign-in — read
 *  back from the OS, never from our own intent. See src/login-item.ts for why
 *  that is not `openAtLogin` on Windows. */
function loginItemActive(): boolean {
  return osWillLaunch(loginItemReport());
}

/** ⚠ Called from the Settings switch ONLY. Startup never writes the login
 *  item — see reconcileLoginItem(). */
function setLoginItem(on: boolean): void {
  // ⚠ The Store package has nowhere to write one: a Run value set from inside
  // an MSIX package lands in the package's private registry hive, where the
  // shell never looks (electron/electron#42016). Its login item is the
  // startup task declared in build/appx-extensions.xml, switched on and off in
  // Windows Settings — which is where Settings sends the user instead.
  if (CURRENT_CHANNEL === 'msstore') return;
  if (process.platform === 'linux') {
    setLinuxAutostart(on);
    return;
  }
  // A dev run must not write a login item at all: the only command it could
  // write is the one described above.
  if (!app.isPackaged) return;
  app.setLoginItemSettings(loginItemWrite(on, process.platform, loginItemSpec()));
}

/**
 * At startup the PREFERENCE follows the OS — never the other way round.
 *
 * ⚠ This used to be "re-assert the login item whenever the preference is on",
 * so an install that had moved kept working — and so did a client the user had
 * disabled in Task Manager or removed from the OS list: it came back, with its
 * sync, at the next sign-in. Whoever switched it off out there meant it. If the
 * OS will no longer launch us, the switch in Settings goes off to match, and
 * nothing is written; turning it back on is one click, and that click is the
 * only thing that writes a login item.
 */
function reconcileLoginItem(): void {
  // The Store copy's login item belongs to Windows alone (see setLoginItem);
  // there is no preference of ours to reconcile with it.
  if (CURRENT_CHANNEL === 'msstore') return;
  const report = loginItemReport();
  const keep = preferenceAfterStartup(state.launchAtLogin, loginItemSupported(), report);
  if (keep === state.launchAtLogin) return;
  log('login', 'the OS will not start filex at sign-in any more; the preference follows it', report);
  state.launchAtLogin = keep;
  try {
    saveState(state);
  } catch (e) {
    log('login', 'could not store the preference', String((e as Error)?.message ?? e));
  }
}

/**
 * The desktop app was the package `filex` until 0.43.x and is `filex-app`
 * since (src/channel.ts → LINUX_APP_NAME). Two things the user set up under
 * the old name would otherwise be lost without a word, at the first start of
 * the renamed app:
 *
 *   - "Start when I sign in": `~/.config/autostart/filex.desktop` pointed at
 *     the old binary and is not the file the app looks for any more — the
 *     switch would read off and turn itself off (reconcileLoginItem). Moved
 *     when it is ours (login-item.ts → legacyAutostartAction), left alone
 *     when it is not.
 *   - "Make filex the default": `mimeapps.list` names `filex.desktop`, which
 *     left with the old package; a desktop skips a default whose entry is
 *     gone. Retargeted to the new entry — only once no `filex.desktop` exists
 *     anywhere a desktop looks, so an entry that is still installed (a
 *     different program's) keeps what the user gave it.
 *
 * ⚠ Linux packages only (not a dev run, which would point the entry at the
 * Electron binary; not a snap or Flatpak, which never had the old name and
 * cannot reach the host's files), and never in a test run (ders #274).
 */
function migrateLegacyLinuxNames(): void {
  if (process.platform !== 'linux' || !app.isPackaged || process.env.FILEX_NO_BROWSER === '1') return;
  if (linuxSandbox(CURRENT_CHANNEL)) return;
  const legacy = linuxAutostartFile(LEGACY_LINUX_AUTOSTART_NAME);
  if (!process.env.APPIMAGE) {
    const home = app.getPath('home');
    const dataDirs = [
      process.env.XDG_DATA_HOME || path.join(home, '.local', 'share'),
      ...(process.env.XDG_DATA_DIRS || '/usr/local/share:/usr/share').split(':'),
    ].filter(Boolean);
    const stillInstalled = dataDirs.some((d) => fs.existsSync(path.join(d, 'applications', LEGACY_LINUX_DESKTOP_ENTRY)));
    const list = path.join(process.env.XDG_CONFIG_HOME || path.join(home, '.config'), 'mimeapps.list');
    try {
      const next = stillInstalled ? null : retargetMimeapps(fs.readFileSync(list, 'utf8'), LEGACY_LINUX_DESKTOP_ENTRY, LINUX_DESKTOP_ENTRY);
      if (next != null) {
        fs.writeFileSync(`${list}.filex-tmp`, next, 'utf8');
        fs.renameSync(`${list}.filex-tmp`, list);
        log('login', 'mimeapps.list: defaults that named the pre-rename entry now name the new one', { from: LEGACY_LINUX_DESKTOP_ENTRY, to: LINUX_DESKTOP_ENTRY });
      }
    } catch {
      /* no mimeapps.list, or unreadable: nothing of ours to carry over */
    }
  }
  let content: string | null = null;
  try {
    content = fs.readFileSync(legacy, 'utf8');
  } catch {
    return;
  }
  const action = legacyAutostartAction(content);
  if (action === 'leave') return;
  try {
    if (action === 'move' && !fs.existsSync(linuxAutostartFile())) setLinuxAutostart(true);
    fs.rmSync(legacy, { force: true });
    log('login', `the pre-rename autostart entry was ${action === 'move' ? 'moved' : 'removed (it was switched off)'}`, { from: legacy });
  } catch (e) {
    log('login', 'could not carry over the pre-rename autostart entry', String((e as Error)?.message ?? e));
  }
}

/**
 * Accounts whose local filex folder is being moved (sync:setRoot). Their
 * watcher is stopped for the move and none is started until it ends — see
 * WatchGate.moving — and their folders say "moving".
 */
const rootMoves = new Set<string>();

function publicState() {
  const keychainNow = keychain();
  return {
    rootMoving: [...rootMoves],
    accounts: state.accounts.map(({ token, ...rest }) => rest), // never hand the token to a renderer
    activeId: state.activeId,
    // Pairings come from the CLI's own state file, not from a copy kept here.
    // Two records of what is paired is two records that can disagree, and the
    // one the engine reads would win silently.
    syncFolders: knownPairs.map((p) => ({
      id: p.id,
      accountId: p.account ?? '',
      remotePath: p.remote,
      localPath: p.local,
      enabled: !p.paused,
      // Items the engine is holding for a decision (0 = nothing to decide).
      held: heldItems(p),
      // What to say under this folder — decided in src/sync-policy.ts, so
      // the page only turns it into words.
      view: folderView({
        pairId: p.id,
        paused: state.syncPaused === true,
        signedOut: !!state.accounts.find((a) => a.id === p.account)?.signedOut,
        moving: rootMoves.has(p.account ?? ''),
        status: supervisor?.statuses().find((st) => st.accountId === p.account) ?? null,
        minuteOfDay: new Date().getHours() * 60 + new Date().getMinutes(),
      }),
    })),
    syncStatuses: supervisor?.statuses() ?? [],
    // Bandwidth limits and the sync window, with the presets Settings offers
    // (one list, here, rather than a copy in the page).
    limitDownKiB: normLimit(state.limitDownKiB),
    limitUpKiB: normLimit(state.limitUpKiB),
    syncWindow: normWindow(state.syncWindow),
    limitPresets: LIMIT_PRESETS_KIB,
    windowPresets: WINDOW_PRESETS,
    syncEngine: cliPath() ? 'bundled' : 'missing',
    runInBackground: state.runInBackground,
    launchAtLogin: state.launchAtLogin,
    locale: state.locale,
    notifications: state.notifications !== false,
    syncPaused: state.syncPaused === true,
    // The sign-in waiting in the browser. The URL carries the state and the
    // challenge HASH only — no secret (see auth:begin).
    pendingAuth: pendingAuth ? { serverUrl: pendingAuth.serverUrl, authUrl: pendingAuth.authUrl } : null,
    // ⚠ What the sign-in window DRAWS. The page keeps nothing of its own, so a
    // reload (the tray, the Dock, a second launch, a failed link) comes back on
    // the same screen (issue #36, src/signin-flow.ts).
    signIn: signInView(
      pendingAuth ? { serverUrl: pendingAuth.serverUrl, authUrl: pendingAuth.authUrl } : null,
      signInFailure,
    ),
    // What 'system' currently resolves to, so the window does not have to
    // re-derive it from navigator.language and disagree with the tray.
    effectiveLocale: effectiveLocale(),
    // What the OS actually did with the request, not what we asked for. Login
    // items are refused often enough (policy, sandboxing, a user unticking it
    // elsewhere) that reporting our own intent back would be a lie.
    launchAtLoginEffective: loginItemActive(),
    // A dev run deliberately refuses to write one (see setLoginItem), and the
    // settings panel has to say WHY rather than show a switch that does nothing.
    launchAtLoginSupported: loginItemSupported(),
    // The Store copy's login item is a startup task Windows switches, not a
    // switch of ours — Settings shows a way there instead (see setLoginItem).
    launchAtLoginInOsSettings: CURRENT_CHANNEL === 'msstore',
    // Which store updates this copy, if any: Settings names it.
    updateChannel: CURRENT_CHANNEL,
    // Whether an account may be stored at all, and if not what to do about it
    // (src/keychain.ts). Anything but 'ok' and the sign-in window explains it
    // instead of starting a sign-in it would have to throw away.
    keychain: keychainNow,
    keychainAdvice: keychainAdvice(keychainNow, process.platform, CURRENT_CHANNEL),
    // The exact command for a snap (its name comes from snapd, not from us).
    keychainCommand: CURRENT_CHANNEL === 'snap' ? snapConnectCommand(process.env) : null,
    // Another copy of filex on this computer: the Store copy found the
    // filex.sh copy ('direct'), or a .deb/.rpm/AppImage copy found the snap
    // ('snap'). Settings says so and how to remove one (src/channel.ts →
    // otherCopyOf).
    otherCopy: otherCopyOf(CURRENT_CHANNEL, process.platform, process.env, (p) => fs.existsSync(p)),
    appVersion: app.getVersion(),
    update: updateState,
    // Set on a build that can never apply an update in place — an ad-hoc
    // sealed macOS app, or the Windows portable .exe. Settings swaps its
    // "updates itself" copy for the honest download story when this is set,
    // and `updateManualReason` picks WHICH honest story.
    updateManualOnly: manualUpdates !== null,
    updateManualReason: manualUpdates,
    // Where this copy keeps its files, and whether it managed to keep them
    // next to itself. Only a portable build has anything to say here.
    portable: portable.portable
      // ⚠ Asked of Electron, not of our own intent: this is where files are
      // ACTUALLY going, which is the only version of it worth showing.
      ? { dataDir: app.getPath('userData'), besideExe: portable.dataDir !== null }
      : null,
  };
}

/** Cache of the CLI's pairs, refreshed whenever they change. Reading the file
 *  through the CLI on every IPC call would fork a process per keystroke. */
let knownPairs: Pair[] = [];

async function refreshPairs(): Promise<void> {
  knownPairs = await listPairs();
  // Paused hands the supervisor no accounts: every watcher stops and none
  // starts — at launch too. See watcherAccounts().
  await supervisor?.reconcile(
    watcherAccounts(state.accounts, { paused: state.syncPaused === true, moving: rootMoves }),
    (id) => state.accounts.find((a) => a.id === id)?.token ?? null,
  );
}

/** The limits and window every watcher is started with (Settings). */
function currentWatchPrefs(): WatchPrefs {
  return { limitDownKiB: state.limitDownKiB, limitUpKiB: state.limitUpKiB, syncWindow: state.syncWindow };
}

/** Restarts every watcher, so a changed limit or window takes effect now. A
 *  stopped engine flushes its checkpoint (SIGTERM; on Windows the kill is
 *  abrupt, and the checkpoint and resumable uploads bound what repeats). */
async function restartWatchers(): Promise<void> {
  for (const acc of state.accounts) supervisor?.stop(acc.id);
  await refreshPairs();
}

/**
 * Pause sync / Resume sync — the tray item and the Settings switch.
 *
 * ⚠ Stored, not just applied. "Quit it" was the only way to stop a client in a
 * bad state, and it lasted until the next sign-in started it again, hidden,
 * syncing. A pause survives the restart; resuming starts the watchers again.
 */
async function setSyncPaused(paused: boolean): Promise<void> {
  if ((state.syncPaused === true) === paused) return;
  state.syncPaused = paused;
  saveState(state);
  log('sync', paused ? 'paused by the user' : 'resumed by the user');
  refreshTray();
  await refreshPairs();
}

// ─────────────────────────── selective sync ───────────────────────────
//
// "Keep on this computer" — the explorer's folder menu drives these (the
// shared component takes the hooks via config.desktopSync). One ROOT folder
// per account, chosen the first time something is kept; every kept folder
// mirrors under it as `<root>/<storage>/<path…>`, so the disk reads like the
// server does. Everything else stays online-only in the window: the explorer
// is the view, the root folder is the subset that also lives here.

/** Native-dialog strings for the keep flow. The window carries the real
 *  catalogue; these render in OS dialogs, which the renderer cannot draw. */
const SYNC_STRINGS: Record<string, [en: string, tr: string]> = {
  rootTitle: ['Choose where filex keeps folders on this computer', 'filex klasörlerinin bu bilgisayarda tutulacağı yeri seç'],
  rootButton: ['Use this folder', 'Bu klasörü kullan'],
  unkeepTitle: ['Keep online only', 'Yalnızca çevrimiçi tut'],
  unkeepMessage: ['Stop keeping “{name}” on this computer?', '“{name}” bilgisayarda tutulmayı bıraksın mı?'],
  unkeepDetail: [
    'The folder stays on the server and in this window. What should happen to the local copy?',
    'Klasör sunucuda ve bu penceredeki görünümde durur. Yerel kopyaya ne olsun?',
  ],
  unkeepTrash: ['Move local copy to Trash', 'Yerel kopyayı Çöp Kutusuna taşı'],
  unkeepLeave: ['Leave the local copy', 'Yerel kopya yerinde kalsın'],
  cancel: ['Cancel', 'Vazgeç'],
  trashFailed: [
    'The folder is no longer kept, but its local copy could not be moved to the Trash: {err}',
    'Klasör artık tutulmuyor ama yerel kopya Çöp Kutusuna taşınamadı: {err}',
  ],
  moveFailed: [
    'Could not move {name} to the new folder: {err}',
    '{name} yeni klasöre taşınamadı: {err}',
  ],
  rootNested: [
    'Pick a folder that is not inside the current one (and does not contain it).',
    'Şu ankinin içinde olmayan (ve onu içermeyen) bir klasör seç.',
  ],
  unexpectedTitle: ['filex hit an unexpected error', 'filex beklenmedik bir hatayla karşılaştı'],
  discardTitle: ['Move held items to the local trash', 'Bekleyen öğeleri yerel çöpe taşı'],
  discardMessageOne: ['Move the 1 held item off this computer?', 'Bekleyen 1 öğe bu bilgisayardan kaldırılsın mı?'],
  discardMessage: ['Move the {n} held items off this computer?', 'Bekleyen {n} öğe bu bilgisayardan kaldırılsın mı?'],
  discardDetail: [
    'They go to the sync trash on this computer and are kept there for 30 days (Settings → Removed by sync). Nothing on the server changes.\n\n{local}',
    'Bu bilgisayardaki eşitleme çöpüne gider ve orada 30 gün saklanır (Ayarlar → Eşitlemenin sildikleri). Sunucuda hiçbir şey değişmez.\n\n{local}',
  ],
  discardButton: ['Move to local trash', 'Yerel çöpe taşı'],
  holdTitle: ['Items held for a decision', 'Karar bekleyen öğeler'],
  holdFailed: ['filex could not do that: {err}', 'filex bunu yapamadı: {err}'],
  signedOutTitle: ['{email} is signed out of {host}', '{email}, {host} oturumundan çıkarıldı'],
  signedOutBody: [
    "The server no longer accepts this computer's sign-in, so sync for this account has stopped. Open filex and choose Reconnect.",
    "Sunucu bu bilgisayarın oturumunu artık kabul etmiyor; bu hesabın eşitlemesi durdu. filex'i açıp Yeniden bağlan'ı seç.",
  ],
  dragFailedTitle: ['Drag out failed', 'Dışarı sürükleme başarısız'],
  dragFailedBody: [
    'The files were dropped in {dir} but could not be downloaded there: {err}',
    'Dosyalar {dir} klasörüne bırakıldı ama oraya indirilemedi: {err}',
  ],
};

function syncText(key: string, vars: Record<string, string> = {}): string {
  const pair = SYNC_STRINGS[key];
  const raw: string = effectiveLocale() === 'tr' ? pair[1] : pair[0];
  return Object.entries(vars).reduce<string>((acc, [k, v]) => acc.replaceAll(`{${k}}`, v), raw);
}

/** `docs://reports/` → `docs://reports`; the bare storage form `docs://`
 *  keeps its slashes — that is the whole-storage pair the engine takes. */
function normRemote(remote: string): string {
  const r = String(remote ?? '').trim();
  return r.endsWith('://') ? r : r.replace(/\/+$/, '');
}

/** True when `child` lives strictly inside `parent` (both wire-form). */
function remoteInside(child: string, parent: string): boolean {
  if (parent.endsWith('://')) return child.startsWith(parent) && child !== parent;
  return child.startsWith(parent + '/');
}

/** Windows refuses these characters in a path segment; everywhere else only
 *  the separator matters.
 *
 *  ⚠ `..` is dropped on EVERY platform, and that is a security guard, not
 *  tidiness: the wire path comes from the SERVER's listing, so a hostile or
 *  compromised server could answer with `docs://../../Documents`. Joined
 *  naively that escapes the account's mirror root, and the engine's first run
 *  merges both sides — it would upload whatever it found there. Segments are
 *  therefore filtered before they ever reach path.join. */
function fsSegment(seg: string): string {
  return process.platform === 'win32' ? seg.replace(/[<>:"\\|?*]/g, '_') : seg;
}

/** Path segments of a wire remote, with anything that could climb out of the
 *  root removed. Exported shape kept tiny on purpose — see fsSegment. */
function safeSegments(rel: string): string[] {
  return rel
    .split('/')
    .filter((s) => s && s !== '.' && s !== '..')
    .map(fsSegment);
}

/** True when `child` sits under `parent`. A different drive on Windows makes
 *  path.relative return an absolute path, which is NOT "inside". */
function isInsideDir(parent: string, child: string): boolean {
  const rel = path.relative(parent, child);
  return rel === '' || (!rel.startsWith('..') && !path.isAbsolute(rel));
}

/** `<root>/<storage>/<path…>` for a wire remote. */
function localMirrorPath(root: string, remote: string): string {
  const idx = remote.indexOf('://');
  const storage = remote.slice(0, idx);
  const rel = remote.slice(idx + 3).replace(/^\/+|\/+$/g, '');
  const segs = [...safeSegments(storage), ...safeSegments(rel)];
  return path.join(root, ...segs);
}

/**
 * One line per step of a drag-out, on stdout.
 *
 * ⚠ Not debug scaffolding to be removed later: the whole route runs AFTER the
 * gesture is over, with no window of its own, so when it goes wrong the only
 * thing the user sees is a folder that stays empty. Without a trail there is
 * nothing to look at — which is exactly the position this feature put us in the
 * first time a real folder failed to fill (2026-08-29).
 */
function dragLog(step: string, detail?: unknown): void {
  log('drag', step, detail);
}

/** Ends a placeholder drag that is still waiting to learn where it landed. */
function dragDropCancel(): void {
  if (!dragDrop) return;
  const { cancel, dir } = dragDrop;
  dragDrop = null;
  cancel();
  void fs.promises.rm(dir, { recursive: true, force: true }).catch(() => undefined);
}

/** Stable key for a dragged selection — the same one the explorer computes,
 *  so "is this still what you prepared?" has one answer on both sides. */
function dragKeyOf(items: Array<{ path: string }>): string {
  return items
    .map((i) => i.path)
    .sort()
    .join(' ');
}

/**
 * The key `dragReady` is remembered under: the selection AND whose it is.
 *
 * ⚠ #47 — paths alone are not an identity. Two accounts can hold the same
 * `name://rel` (two people on one server, or two servers with a drive of the
 * same name), and since ⌘K drags another account's hit through this same
 * shell, a copy prepared for one account was handed to the other's drag.
 * Measured (search-e2e.mjs): the outsider's own ortak-47.txt dragged out as the
 * administrator's cached copy, from the administrator's cache folder.
 */
function readyKeyOf(accountId: string, items: Array<{ path: string }>): string {
  return `${accountId}|${dragKeyOf(items)}`;
}

/** The sync engine's local copy of a wire path, when it keeps one. Used only
 *  to fill the drag cache without going over the network — the mirror itself
 *  is never handed to the OS drag (a drop completed as a move would take the
 *  user's synced file with it). */
function mirrorPathFor(accountId: string, remote: string): string | null {
  const wire = normRemote(remote);
  const pairs = accountPairs(accountId);
  const exact = pairs.find((p) => p.remote === wire);
  if (exact) return exact.local;
  const anc = pairs.find((p) => remoteInside(wire, p.remote));
  if (!anc) return null;
  const rest = anc.remote.endsWith('://')
    ? wire.slice(anc.remote.length)
    : wire.slice(anc.remote.length + 1);
  return path.join(anc.local, ...safeSegments(rest));
}

/**
 * Starts the OS drag — or, under the test hook, does everything except that.
 *
 * ⚠ `startDrag` opens the operating system's modal drag loop, attached to the
 * real mouse. An unattended run cannot let go of it, so a suite that called it
 * would hang the machine it runs on. FILEX_TEST_NO_OS_DRAG lets the rest of the
 * route — placeholders, the drop watcher, the transfer — be measured for real;
 * the hand-over to the OS is the one step a human has to do. Same convention as
 * FILEX_TEST_PICK_DIR for the native folder picker.
 */
function beginOsDrag(sender: Electron.WebContents, paths: string[]): void {
  if (process.env.FILEX_TEST_NO_OS_DRAG === '1') {
    // ⚠ The hook does not just skip the call — it BLOCKS the way the real one
    // does. A hook that returned immediately is what let the "watcher armed
    // too late" bug through a green suite: with no block, everything after
    // this line ran before the simulated drop, which is the opposite of what
    // Windows does.
    const until = Date.now() + Number(process.env.FILEX_TEST_DRAG_BLOCK_MS ?? 1500);
    const buf = new SharedArrayBuffer(4);
    const arr = new Int32Array(buf);
    while (Date.now() < until) Atomics.wait(arr, 0, 0, 50);
    return;
  }
  sender.startDrag({ file: paths[0]!, files: paths, icon: dragIcon() });
}

/** The image under the cursor while an OS drag is in flight. Electron requires
 *  one; an empty NativeImage makes the drag invisible on Windows. */
function dragIcon(): Electron.NativeImage {
  const img = nativeImage.createFromPath(ICON_PATH);
  return img.isEmpty() ? img : img.resize({ width: 48, height: 48 });
}

function accountPairs(accountId: string): Pair[] {
  return knownPairs.filter((p) => p.account === accountId);
}

/** Names the OS drops into folders it merely LOOKED at. A folder holding
 *  nothing else is empty in every sense the user means — measured: Finder
 *  planted .DS_Store in the mirror's parents and the plain-rmdir sweep
 *  stopped dead on it, leaving the "empty" skeleton the sweep exists to
 *  remove. Mirrors the engine's own skip list. */
const OS_LITTER = new Set(['.DS_Store', 'Thumbs.db', 'desktop.ini']);

/** Removes a dir that is empty apart from OS litter (the litter goes too).
 *  Anything with real content — including a `._*`-only AppleDouble we cannot
 *  be sure about — is left alone. */
async function removeIfEffectivelyEmpty(dir: string): Promise<boolean> {
  let entries: string[];
  try {
    entries = await fs.promises.readdir(dir);
  } catch {
    return false;
  }
  if (entries.some((e) => !OS_LITTER.has(e))) return false;
  for (const e of entries) {
    try {
      await fs.promises.unlink(path.join(dir, e));
    } catch {
      return false;
    }
  }
  try {
    await fs.promises.rmdir(dir);
  } catch {
    return false;
  }
  return true;
}

/** Sweep of effectively-empty dirs from `from` up to (never including)
 *  `stopAt`. The mirror layout mkdirs intermediate folders on keep; after an
 *  unkeep moves the mirror to the Trash, this takes the empty skeleton with
 *  it. Stops at the first dir with real content. */
async function pruneEmptyDirsUpTo(from: string, stopAt: string): Promise<void> {
  let dir = from;
  while (dir !== stopAt && dir.startsWith(stopAt + path.sep)) {
    if (!(await removeIfEffectivelyEmpty(dir))) return;
    dir = path.dirname(dir);
  }
}

/**
 * The folder picker, in ONE place.
 *
 * ⚠ The env hook is why this is shared rather than called inline: a native
 * dialog is OS chrome an automated run cannot reach, so the same flag that
 * suppresses the browser supplies the answer instead — and the test then
 * drives the REAL handler. A second call site with its own showOpenDialog is
 * a flow the suite cannot reach at all.
 */
let pickQueue: string[] | null = null;

/**
 * Where the last pick was made — the folder the user was looking AT, i.e. the
 * parent of the one they chose.
 *
 * ⚠ Electron 43 changed what a dialog with no `defaultPath` does: it used to
 * leave the start folder to the OS, which remembered where the user last was;
 * it now opens in Downloads every time and the OS no longer tracks anything
 * ("Dialog methods default to Downloads directory", breaking changes 43.0).
 * The keep flow always passes a start folder; "add a synced folder" in
 * Settings passes none, so without this every pick after the first would
 * begin in Downloads again. Per session, like the OS memory it replaces for
 * the common case of adding several folders in a row.
 */
let lastPickParent: string | undefined;

async function pickDirectory(opts: { title?: string; buttonLabel?: string; defaultPath?: string } = {}): Promise<string | null> {
  const preset = process.env.FILEX_NO_BROWSER === '1' ? process.env.FILEX_TEST_PICK_DIR : undefined;
  if (preset) {
    // One flow can open the picker TWICE with different answers — the first
    // keep chooses the root, Settings later moves it — so the hook takes a
    // queue (path.delimiter separated) and hands out one answer per call. A
    // single path (the common case) simply repeats.
    pickQueue ??= preset.split(path.delimiter).filter(Boolean);
    return pickQueue.length > 1 ? pickQueue.shift()! : (pickQueue[0] ?? null);
  }
  const dialogOpts = {
    ...opts,
    defaultPath: opts.defaultPath ?? lastPickParent,
    properties: ['openDirectory', 'createDirectory'] as const,
  };
  const picked = mainWindow
    ? await dialog.showOpenDialog(mainWindow, { ...dialogOpts, properties: [...dialogOpts.properties] })
    : await dialog.showOpenDialog({ ...dialogOpts, properties: [...dialogOpts.properties] });
  const dir = picked.canceled ? null : (picked.filePaths[0] ?? null);
  if (dir) lastPickParent = path.dirname(dir);
  return dir;
}

/** A native question, with the same test hook as the picker above: the index
 *  of the button the run would have clicked. */
async function askChoice(opts: Electron.MessageBoxOptions): Promise<number> {
  const preset = process.env.FILEX_NO_BROWSER === '1' ? process.env.FILEX_TEST_DIALOG_CHOICE : undefined;
  if (preset !== undefined && preset !== '') return Number(preset);
  const { response } = mainWindow
    ? await dialog.showMessageBox(mainWindow, opts)
    : await dialog.showMessageBox(opts);
  return response;
}

/** `~/filex/<host>`, in the home the user's file manager opens — not a
 *  snap's private `HOME` (src/channel.ts → userHome). */
function defaultSyncRoot(acc: Account): string {
  const base = path.join(userHome(CURRENT_CHANNEL, process.env, app.getPath('home')), 'filex');
  try {
    return path.join(base, new URL(acc.serverUrl).hostname);
  } catch {
    return base;
  }
}

/**
 * The account's mirror root, prompting on first use. The default —
 * `~/filex/<host>` — is pre-created so the dialog opens INSIDE it and a plain
 * "Use this folder" does the obvious thing; picking anywhere else works too.
 * Cancelling the dialog cancels the keep: null, nothing recorded.
 */
async function ensureSyncRoot(acc: Account): Promise<string | null> {
  if (acc.syncRoot) return acc.syncRoot;
  const def = defaultSyncRoot(acc);
  await fs.promises.mkdir(def, { recursive: true });
  const dir = await pickDirectory({
    title: syncText('rootTitle'),
    buttonLabel: syncText('rootButton'),
    defaultPath: def,
  });
  if (!dir) return null;
  acc.syncRoot = dir;
  saveState(state);
  return dir;
}

// ─────────────────────────── open with filex ───────────────────────────
//
// A .docx on the desktop opens in filex, which puts it in front of the
// OnlyOffice editor the server already runs. The reason is not novelty: most
// Linux desktops have no Microsoft Office, many Macs have none, and plenty of
// Windows machines have none either — and until now filex could only edit
// documents that already lived on a filex server.
//
// Two routes, chosen per document:
//
//   • **The file is already inside a synced folder.** Its remote twin is opened
//     directly. No copy, no write-back, no cleanup — saving reaches the server
//     and the sync engine brings the bytes down to that same local file. This
//     is checked FIRST because the alternative would create a second, diverging
//     copy of a file the user is watching sync.
//   • **Anywhere else.** The bytes are uploaded to a scratch folder on the
//     account, the editor opens against the copy, and every save the server
//     records is written back over the ORIGINAL local path. A banner in the
//     editor window names that path the whole time.
//
// ⚠⚠ Write-back is where documents get destroyed, so all of it is deliberate:
// the replace is atomic (openwith.ts), it may only ever touch the path the
// session was opened with, a failure is shouted rather than logged, and a
// session that outlives the app is recovered on the next start instead of being
// silently swept away.

/** A scratch session with a window attached — the in-memory half of the record
 *  on disk. */
interface LiveOpenWith {
  record: OpenWithSession;
  window: BrowserWindow;
  timer: ReturnType<typeof setInterval> | null;
  /** A poll is in flight; the next tick must not overlap it. */
  busy: boolean;
  /** The editor window is gone and we are in the grace period. */
  closing: boolean;
  /** When to stop waiting for a last save, and the ceiling on that. */
  until: number;
  hardUntil: number;
  /** At least one edit reached the local file. */
  wroteBack: boolean;
}

let sessionStore: SessionStore | null = null;
const liveOpenWith = new Map<string, LiveOpenWith>();
/** Documents handed over before the app was ready. macOS fires `open-file`
 *  BEFORE `ready` on a cold start, so without a queue the very first
 *  double-click of an install is the one that does nothing. */
let openWithQueue: string[] = [];
let openWithFlush: ReturnType<typeof setTimeout> | null = null;
let openWithArmed = false;
/** The last thing that went wrong, for Settings and for the E2E rig. */
let openWithError: string | null = null;

/** Batch window. Selecting five documents and hitting Enter delivers them as
 *  one argv on Windows and as five separate `open-file` events on macOS; both
 *  land here and are handled as one batch, so the account and the storage are
 *  resolved once. */
const OPEN_WITH_BATCH_MS = 150;

function openWithPollMs(): number {
  return Math.max(250, Number(process.env.FILEX_OPENWITH_POLL_MS ?? 2500));
}
/**
 * How long to keep watching after the editor window closes.
 *
 * ⚠ Not optional and not decoration. OnlyOffice does not write on every
 * keystroke — the document server posts its save callback about ten seconds
 * AFTER the last editor disconnects. Deleting the scratch copy the moment the
 * window closed would throw away the last edit of every session, which is the
 * single most likely way this feature could quietly lose work.
 */
function openWithGraceMs(): number {
  return Math.max(0, Number(process.env.FILEX_OPENWITH_GRACE_MS ?? 120_000));
}
/** Once a save HAS landed after the close, this much quiet is enough. */
function openWithQuietMs(): number {
  return Math.max(0, Number(process.env.FILEX_OPENWITH_QUIET_MS ?? 20_000));
}

const OPEN_WITH_STRINGS: Record<string, [en: string, tr: string]> = {
  ok: ['OK', 'Tamam'],
  notOfficeTitle: ['filex does not open this kind of file', 'filex bu tür dosyaları açmaz'],
  notOfficeBody: ['{names}', '{names}'],
  notOfficeDetail: [
    'filex opens office documents ({types}) in its own editor. Everything else stays with the app you already use for it.',
    'filex ofis belgelerini ({types}) kendi düzenleyicisinde açar. Diğer her şey zaten kullandığın uygulamada kalır.',
  ],
  signInTitle: ['Sign in to filex first', "Önce filex'e giriş yap"],
  signInBody: [
    'There is no filex account on this computer yet, so there is nowhere to open {name}.',
    'Bu bilgisayarda henüz filex hesabı yok, bu yüzden {name} açılacak bir yer yok.',
  ],
  signInDetail: [
    'Add your server in the window that just opened, then open the document again.',
    'Az önce açılan pencereden sunucunu ekle, sonra belgeyi yeniden aç.',
  ],
  openFailedTitle: ['filex could not open {name}', 'filex {name} dosyasını açamadı'],
  openingTitle: ['Opening {name}…', '{name} açılıyor…'],
  openingBody: [
    'filex is putting a working copy on your server; the editor opens when it is there.',
    'filex sunucuna bir çalışma kopyası koyuyor; kopya oraya varınca düzenleyici açılır.',
  ],
  openFailedDetail: [
    'The document on this computer has not been touched.',
    'Bu bilgisayardaki belgeye dokunulmadı.',
  ],
  bannerScratch: [
    'filex is editing a copy — every save is written back to {file}',
    'filex bir kopya üzerinde çalışıyor — her kayıt {file} dosyasına geri yazılır',
  ],
  bannerTwin: [
    'Synced folder — saving goes to the server, and sync brings it back to {file}',
    'Eşitlenen klasör — kayıt sunucuya gider, eşitleme {file} dosyasına geri getirir',
  ],
  savedBackTitle: ['filex saved your changes', 'filex değişikliklerini kaydetti'],
  savedBackBody: ['{name} on this computer is up to date.', 'Bu bilgisayardaki {name} güncel.'],
  writeFailedTitle: ['filex could not save {name}', 'filex {name} dosyasını kaydedemedi'],
  writeFailedKept: ['Your edit is safe here: {kept}', 'Düzenlemen şurada duruyor: {kept}'],
  writeFailedLost: [
    'The edit could not be written anywhere on this computer. Open the document in filex and download it before you quit the app.',
    'Düzenleme bu bilgisayarda hiçbir yere yazılamadı. Uygulamadan çıkmadan önce belgeyi filex’te açıp indir.',
  ],
  recoveredTitle: ['filex recovered an unsaved edit', 'filex kaydedilmemiş bir düzenlemeyi kurtardı'],
  recoveredBody: [
    '{name} was still open when filex last closed. The newer version is next to it, as {kept}.',
    '{name} filex en son kapandığında hâlâ açıktı. Yeni sürümü yanında {kept} olarak duruyor.',
  ],
  macDefaultTitle: ['Making filex the default', "filex’i varsayılan yapmak"],
  macDefaultBody: [
    'macOS has no way for an app to set itself as the default handler, so this is done in Finder.',
    "macOS bir uygulamanın kendini varsayılan yapmasına izin vermez, bu yüzden bu iş Finder’dan yapılır.",
  ],
  macDefaultDetail: [
    'Right-click any .docx → Get Info → "Open with" → filex → Change All…',
    'Herhangi bir .docx dosyasına sağ tıkla → Bilgi Al → “Şununla aç” → filex → Tümünü Değiştir…',
  ],
};

function openText(key: string, vars: Record<string, string> = {}): string {
  const pair = OPEN_WITH_STRINGS[key];
  const raw: string = effectiveLocale() === 'tr' ? pair[1] : pair[0];
  return Object.entries(vars).reduce<string>((acc, [k, v]) => acc.replaceAll(`{${k}}`, v), raw);
}

/**
 * Something the user has to be told.
 *
 * ⚠ Suppressed under the same flag that suppresses the browser: an unattended
 * run cannot dismiss a native dialog, and one left open blocks the rest of the
 * flow forever. The message still goes to the log, which is what the E2E rig
 * reads. Same convention as pickDirectory() and askChoice().
 */
async function tellUser(
  type: 'info' | 'warning' | 'error',
  title: string,
  message: string,
  detail?: string,
): Promise<void> {
  log('openwith', `${type}: ${title}`, { message, detail });
  if (process.env.FILEX_NO_BROWSER === '1') return;
  await dialog
    .showMessageBox({ type, title, message, detail, buttons: [openText('ok')] })
    .catch(() => undefined);
}

function openWithNotify(title: string, body: string): void {
  try {
    if (Notification.isSupported()) new Notification({ title, body }).show();
  } catch {
    /* a courtesy, never a failure path */
  }
}

function remoteCtx(acc: Account): RemoteContext {
  return { serverUrl: acc.serverUrl, token: acc.token };
}

/** Two paths naming the same document. Case-insensitive on Windows, where
 *  `C:\Docs\a.docx` and `c:\docs\A.DOCX` are one file. */
function pathsEqual(a: string, b: string): boolean {
  const norm = (p: string) => {
    const n = path.resolve(p);
    return process.platform === 'win32' ? n.toLowerCase() : n;
  };
  return norm(a) === norm(b);
}

/** Where an edit goes when it cannot go home. Under userData, not the OS temp
 *  dir: a folder the system may empty is not a place to keep the only copy of
 *  someone's work. The message names this path for the user to open, so on
 *  the Store build it is where Explorer can see it (src/channel.ts). */
function openWithRecoveryDir(): string {
  return path.join(explorerVisibleRoot(CURRENT_CHANNEL, app.getPath('userData'), app.getPath('home')), 'openwith-recovered');
}

/** Documents arriving from any of the three OS routes land here. */
function queueOpenWith(paths: string[]): void {
  for (const p of paths) {
    if (p && !openWithQueue.includes(p)) openWithQueue.push(p);
  }
  if (!openWithArmed || !openWithQueue.length) return;
  if (openWithFlush) clearTimeout(openWithFlush);
  openWithFlush = setTimeout(() => {
    openWithFlush = null;
    const batch = openWithQueue;
    openWithQueue = [];
    void openDocuments(batch);
  }, OPEN_WITH_BATCH_MS);
}

/** Called once the app is ready, to release anything the OS delivered early. */
function armOpenWith(): void {
  openWithArmed = true;
  if (openWithQueue.length) queueOpenWith([]);
}

async function openDocuments(paths: string[]): Promise<void> {
  const docs: string[] = [];
  const refused: string[] = [];
  for (const raw of paths) {
    const p = path.resolve(String(raw));
    const st = await fs.promises.stat(p).catch(() => null);
    if (!st?.isFile() || !isOfficeDocument(p)) {
      refused.push(p);
      continue;
    }
    docs.push(p);
  }
  if (refused.length) {
    await tellUser(
      'warning',
      openText('notOfficeTitle'),
      refused.map((p) => path.basename(p)).join('\n'),
      openText('notOfficeDetail', { types: OFFICE_EXTENSIONS.join(', ') }),
    );
  }
  if (!docs.length) return;

  const acc = activeAccount(state) ?? state.accounts[0] ?? null;
  if (!acc) {
    openShell('/connect', 'filex — Connect');
    await tellUser(
      'info',
      openText('signInTitle'),
      openText('signInBody', { name: path.basename(docs[0]!) }),
      openText('signInDetail'),
    );
    return;
  }

  for (const doc of docs) {
    try {
      await openOneDocument(acc, doc);
      openWithError = null;
    } catch (err) {
      const msg = String((err as Error)?.message ?? err);
      openWithError = msg;
      log('openwith', 'open failed', { doc, error: msg });
      await tellUser(
        'error',
        openText('openFailedTitle', { name: path.basename(doc) }),
        msg,
        openText('openFailedDetail'),
      );
    }
  }
}

/** Documents between the double-click and their editor (OpeningDocs). */
const openingDocs = new OpeningDocs(process.platform);

async function openOneDocument(acc: Account, localPath: string): Promise<void> {
  // ⚠ One document, one editor. Double-clicking a file that is already open —
  // easy to do, since the app does not put itself in front of you — would
  // otherwise make a SECOND working copy of the same document, with two
  // sessions writing back to one path: whichever saved last would win, and the
  // other person's edit would vanish without a word.
  const already = [...liveOpenWith.values()].find(
    (l) => !l.closing && pathsEqual(l.record.localPath, localPath),
  );
  if (already && !already.window.isDestroyed()) {
    already.window.show();
    already.window.focus();
    log('openwith', 'already open — focusing it', { localPath });
    return;
  }

  // ⚠ …and one that is still being opened. The check above only knows
  // documents whose editor is up, which is after the working copy has gone
  // up: a second double-click in that time opened a second session.
  if (!openingDocs.begin(localPath)) {
    log('openwith', 'already being opened', { localPath });
    return;
  }
  try {
    const twin = resolveSyncTwin(localPath, accountPairs(acc.id));
    if (twin) {
      log('openwith', 'synced twin', { localPath, remote: twin.remote, pair: twin.pairId });
      openEditorWindow(acc, twin.remote, localPath, 'twin');
      return;
    }
    // The upload is the slow part (up to 256 MB), and nothing was on screen
    // until the editor came up: said once it takes a moment.
    const slow = setTimeout(
      () => openWithNotify(openText('openingTitle', { name: path.basename(localPath) }), openText('openingBody')),
      1500,
    );
    try {
      await openViaScratch(acc, localPath);
    } finally {
      clearTimeout(slow);
    }
  } finally {
    openingDocs.end(localPath);
  }
}

/**
 * The account's scratch storage, discovered once and remembered.
 *
 * ⚠ Tries every storage rather than trusting the first. A read-only storage
 * (an archive mount, someone else's share) is a perfectly ordinary first entry
 * in the list, and picking it would make the feature fail for that account with
 * a permissions error every single time.
 */
async function scratchStorageFor(acc: Account, ctx: RemoteContext): Promise<string> {
  const all = await listStorages(ctx);
  if (!all.length) throw new Error('this account has no storage to work in');
  const ordered = acc.openWithStorage
    ? [acc.openWithStorage, ...all.filter((s) => s !== acc.openWithStorage)]
    : all;
  let last: Error | null = null;
  for (const storage of ordered) {
    try {
      await ensureScratchDir(ctx, storage);
      if (acc.openWithStorage !== storage) {
        acc.openWithStorage = storage;
        saveState(state);
      }
      return storage;
    } catch (err) {
      last = err as Error;
    }
  }
  throw last ?? new Error('no writable storage on this account');
}

async function openViaScratch(acc: Account, localPath: string): Promise<void> {
  const ctx = remoteCtx(acc);
  const bytes = await fs.promises.readFile(localPath);
  const storage = await scratchStorageFor(acc, ctx);
  const dir = scratchRemoteDir(storage);
  const id = newSessionId();
  const basename = scratchBasename(localPath, id);
  await uploadFile(ctx, dir, basename, bytes);
  const seen = await statRemote(ctx, dir, basename);
  const now = new Date().toISOString();
  const record: OpenWithSession = {
    id,
    accountId: acc.id,
    serverUrl: acc.serverUrl,
    storage,
    localPath,
    remote: scratchRemotePath(storage, basename),
    createdAt: now,
    updatedAt: now,
    seen,
    ownerPid: process.pid,
  };
  // ⚠ On disk BEFORE the window opens. A crash between the upload and the first
  // save must still leave something the next start can find, clean up and — if
  // the copy moved on — recover an edit from.
  await sessionStore?.put(record);
  log('openwith', 'scratch copy', { localPath, remote: record.remote });

  const win = openEditorWindow(acc, record.remote, localPath, 'scratch');
  const live: LiveOpenWith = {
    record,
    window: win,
    timer: null,
    busy: false,
    closing: false,
    until: 0,
    hardUntil: 0,
    wroteBack: false,
  };
  liveOpenWith.set(id, live);
  win.on('closed', () => void beginOpenWithGrace(id));
  live.timer = setInterval(() => void pollOpenWith(id), openWithPollMs());
}

/* === yeni-pencere:v1 — document windows open frameless (Burak, 2026-09-16) ===
 *
 * The OS title bar goes; the native window controls sit as an overlay in the
 * top-right OVER the document, and a thin strip along the very top edge drags
 * the window. OnlyOffice runs in a CROSS-ORIGIN iframe (docs.example.com), so its
 * own top bar cannot be made draggable from here — the top-edge strip is the
 * closest achievable to "grab OnlyOffice's top bar to move the window", and it
 * is kept thin on purpose so OnlyOffice's own menu row stays clickable.
 */
const DOC_CTL_H = 36; // the reserved top bar's height (our controls live in it)
const IS_MAC = process.platform === 'darwin';

/**
 * Frameless document-window chrome, cross-platform:
 *  - macOS: `hiddenInset` HIDES the bar but KEEPS the native traffic-light
 *    buttons (top-left, the Mac convention). We add only a drag strip.
 *  - Windows / Linux: no OS caption at all (`frame:false`); we draw our OWN
 *    minimize / maximize / close in the top-right (docChromeScript). OS window
 *    chrome is the one place surface-specific code is the right answer.
 */
function docWindowChrome() {
  return IS_MAC
    ? { titleBarStyle: 'hiddenInset' as const, trafficLightPosition: { x: 12, y: 11 } }
    : { frame: false };
}

/** Injected into a document window's page: a slim top BAR that RESERVES its own
 *  height (the page content is pushed down by it) so it never sits on top of the
 *  viewer's own top row — OnlyOffice's profile/share is in the top-right and our
 *  close button was landing on it. The bar is the drag handle; on Windows/Linux
 *  it carries OUR minimize/maximize/close on the right, on macOS it stays empty
 *  on the right and the native traffic lights float over its left (hiddenInset).
 *  Idempotent, re-applied on every load (the editor page navigates within itself
 *  — a sign-in bounce, a reload after save). Buttons call `window.filexWin.*`
 *  (preload-editor); they opt out of the drag region or it swallows their click.
 *
 *  ⚠ The reserve is padding-top on the chromeless backdrop (the same trick the
 *  bottom open-with banner uses, mirrored) — box-sizing:border-box + the card at
 *  height:100% shrinks the viewer to sit BELOW the bar. */
function docChromeScript(): string {
  const H = DOC_CTL_H;
  const controls = IS_MAC
    ? ''
    : `
      const ctl = document.createElement('div');
      ctl.style.cssText = 'margin-left:auto;display:flex;height:100%;-webkit-app-region:no-drag;';
      const mk = (label, svg, fn, danger) => {
        const b = document.createElement('button');
        b.type = 'button'; b.setAttribute('aria-label', label); b.title = label;
        b.innerHTML = svg;
        b.style.cssText = 'width:46px;height:100%;display:flex;align-items:center;justify-content:center;border:0;background:transparent;color:var(--fe-text-muted,#8a94a6);cursor:pointer;-webkit-app-region:no-drag;transition:background .12s,color .12s;';
        // The same danger pair as the main window's close button (ui/app.html
        // #winctl): one red for every window of the app, and the theme's own.
        b.onmouseenter = () => { b.style.background = danger ? 'var(--fe-danger,#dc2626)' : 'var(--fe-bg-hover,rgba(128,128,128,.16))'; b.style.color = danger ? 'var(--fe-text-on-primary,#ffffff)' : 'var(--fe-text,#e6eaf0)'; };
        b.onmouseleave = () => { b.style.background = 'transparent'; b.style.color = 'var(--fe-text-muted,#8a94a6)'; };
        b.onclick = fn;
        return b;
      };
      const S = 'width="11" height="11" viewBox="0 0 11 11" fill="none" stroke="currentColor" stroke-width="1.1"';
      ctl.appendChild(mk('Minimize', '<svg '+S+'><line x1="1" y1="6" x2="10" y2="6"/></svg>', () => window.filexWin && window.filexWin.minimize()));
      ctl.appendChild(mk('Maximize', '<svg '+S+'><rect x="1.2" y="1.2" width="8.6" height="8.6" rx="1"/></svg>', () => window.filexWin && window.filexWin.toggleMaximize()));
      ctl.appendChild(mk('Close', '<svg '+S+'><line x1="1.5" y1="1.5" x2="9.5" y2="9.5"/><line x1="9.5" y1="1.5" x2="1.5" y2="9.5"/></svg>', () => window.filexWin && window.filexWin.close(), true));
      bar.appendChild(ctl);`;
  return `(() => {
    if (!document.getElementById('filex-winbar-style')) {
      const st = document.createElement('style');
      st.id = 'filex-winbar-style';
      st.textContent = '.fe-modal__backdrop--chromeless{box-sizing:border-box!important;align-items:stretch!important;padding-top:${H}px!important}.fe-modal__card--chromeless{height:100%!important;max-height:100%!important}';
      document.head.appendChild(st);
    }
    if (!document.getElementById('filex-winbar')) {
      const bar = document.createElement('div');
      bar.id = 'filex-winbar';
      bar.style.cssText = 'position:fixed;top:0;left:0;right:0;height:${H}px;z-index:2147483647;-webkit-app-region:drag;display:flex;align-items:stretch;background:var(--fe-bg-elev,#1a1d23);border-bottom:1px solid var(--fe-border,rgba(128,128,128,.18));';${controls}
      (document.body || document.documentElement).appendChild(bar);
    }
  })();`;
}

/** The `/files/edit` URL for a remote path, in edit mode. */
function editRouteUrl(acc: Account, remote: string): string {
  const url = new URL('/files/edit', acc.serverUrl);
  url.searchParams.set('path', remote);
  url.searchParams.set('type', extensionOf(remote));
  url.searchParams.set('mode', 'edit');
  return url.toString();
}

/**
 * The shared shell for BOTH document windows — the in-app viewer
 * (openViewerWindow) and the OS open-with editor (openEditorWindow). Frameless,
 * our own controls (docWindowChrome + docChromeScript), the title pinned so the
 * admin SPA cannot overwrite it with the Branding name, and external links
 * pushed to the browser. `extraInject` adds to the page on every load — the
 * open-with flow uses it for its "editing a copy" banner.
 *
 * ⚠ NOT the app's preload. The page is remote content; handing it `filexApp`
 * would put account tokens and the sync engine one `window.filexApp` away from
 * whatever that origin serves. The credential arrives through the header
 * injector (wireAuthHeaderInjection); the editor preload carries only the
 * `filexDesktop` flag and the controls-only `filexWin` bridge.
 */
function makeDocumentWindow(
  acc: Account,
  url: string,
  title: string,
  extraInject?: () => string,
): BrowserWindow {
  const win = new BrowserWindow({
    width: 1280,
    height: 860,
    minWidth: 720,
    minHeight: 520,
    // The page retitles itself; this is the pre-load title so the taskbar entry
    // is never a blank "filex" (and page-title-updated below keeps it).
    title,
    icon: ICON_PATH,
    autoHideMenuBar: true,
    show: false,
    backgroundColor: windowGround(),
    // yeni-pencere:v1 — frameless: our controls (Win/Linux) / native traffic
    // lights (macOS); the reserved top bar + drag come from docChromeScript.
    ...docWindowChrome(),
    webPreferences: { preload: preload('preload-editor.cjs'), contextIsolation: true, sandbox: true },
  });
  win.once('ready-to-show', () => win.show());
  // ⚠ Keep the WINDOW title = the file name. /files/edit lives under the admin
  // SPA, which sets document.title to the Branding name ("BRF Teknoloji"); this
  // locks the taskbar entry to the `title` we set above.
  win.on('page-title-updated', (e) => e.preventDefault());
  win.webContents.setWindowOpenHandler(({ url: target }) => {
    openOutward(target, win);
    return { action: 'deny' };
  });
  // ⚠ The editor page carries plain <a href> links (a download among them); a
  // navigation would replace the document with no way back. Its own routes stay;
  // everything else goes to the browser.
  win.webContents.on('will-navigate', (e, target) => {
    if (originOf(target) === originOf(acc.serverUrl)) return;
    e.preventDefault();
    openOutward(target, win);
  });
  // ⚠ Re-applied on EVERY load, not once — the editor route navigates within
  // itself (a sign-in bounce, a reload after a save). The window chrome (drag +
  // controls) and any caller's `extraInject` (the open-with banner) ride along.
  win.webContents.on('did-finish-load', () => {
    const extra = extraInject ? extraInject() : '';
    void win.webContents
      .executeJavaScript(extra + docChromeScript(), true)
      .catch(() => undefined);
  });
  void win.loadURL(url);
  return win;
}

/**
 * A document window — every in-app "open" lands here (host-owned open: the
 * explorer's `config.openInHost` + the app page's `file-opened` listener). It
 * opens the REMOTE bytes directly, with no scratch copy and no write-back
 * banner — that belongs only to openEditorWindow's OS open-with flow.
 */
function openViewerWindow(acc: Account, remote: string): BrowserWindow {
  const title = remote.slice(remote.lastIndexOf('/') + 1) || remote;
  return makeDocumentWindow(acc, editRouteUrl(acc, remote), title);
}

/**
 * The OS "open with filex" editor window: edits a LOCAL file through a scratch/
 * twin copy on the server (openViaScratch), and injects the "you are editing a
 * copy" banner on every load (openText bannerScratch/bannerTwin). All the window
 * plumbing — frameless chrome, title pinning, external-link handling — lives in
 * makeDocumentWindow; only the banner is specific to this flow.
 */
function openEditorWindow(
  acc: Account,
  remote: string,
  localPath: string,
  mode: 'scratch' | 'twin',
): BrowserWindow {
  return makeDocumentWindow(acc, editRouteUrl(acc, remote), path.basename(localPath), () =>
    bannerScript(
      mode === 'twin'
        ? openText('bannerTwin', { file: localPath })
        : openText('bannerScratch', { file: localPath }),
    ),
  );
}

/** The persistent strip along the bottom of the editor window. Self-contained
 *  and idempotent: the page is not ours, so it gets one element with one id and
 *  no stylesheet of its own.
 *
 *  ⚠ Drawn from the PAGE's own tokens, with a literal only as a floor. The
 *  page this lands on is the filex editor route, which loads
 *  packages/core's stylesheet and therefore publishes `--fe-*` on :root — so
 *  the strip is the palette's elevated surface in the variant the page is
 *  already in. It used to be a fixed near-black (#14181d / #e8ecf1 / #2a313a),
 *  which is the desktop shell's OLD palette: a dark bar pinned across the
 *  bottom of a white document, in three colours the product no longer uses
 *  anywhere.
 *
 *  The fallbacks are CSS SYSTEM COLOURS rather than hexes, so a page that
 *  somehow has no tokens still gets a strip that follows the OS's own light or
 *  dark setting instead of a guess. */
function bannerScript(text: string): string {
  return `(() => {
    const id = 'filex-openwith-banner';
    let el = document.getElementById(id);
    if (!el) {
      el = document.createElement('div');
      el.id = id;
      el.style.cssText = [
        'position:fixed','left:0','right:0','bottom:0','z-index:2147483647',
        'padding:6px 12px',
        'font:var(--fe-text-xs, 12px)/1.4 var(--fe-font, system-ui), sans-serif',
        'background:var(--fe-bg-elev, Canvas)',
        'color:var(--fe-text-muted, GrayText)',
        'border-top:1px solid var(--fe-border, GrayText)',
        'white-space:nowrap','overflow:hidden','text-overflow:ellipsis',
        'pointer-events:none',
      ].join(';');
      document.body.appendChild(el);
    }
    el.textContent = ${JSON.stringify(text)};
    // ⚠ Reserve the strip's height so it sits BELOW the editor, not on top of
    // it. /files/edit fills the viewport with a chromeless modal whose card is
    // 100vh, and the viewers fill that card — so a fixed strip pinned at
    // bottom:0 lands squarely on the viewer's OWN bottom bar (for a spreadsheet
    // that is OnlyOffice's sheet-tab + zoom strip, which is exactly what the
    // user needs to switch sheets). Shrinking the chromeless card by the
    // measured strip height lifts that bar clear of the strip. Measured after
    // the text is set — the strip is one nowrap line, so its height is stable.
    const sid = 'filex-openwith-banner-style';
    let style = document.getElementById(sid);
    if (!style) {
      style = document.createElement('style');
      style.id = sid;
      document.head.appendChild(style);
    }
    const h = el.offsetHeight || 30;
    style.textContent =
      '.fe-modal__backdrop--chromeless{box-sizing:border-box!important;' +
        'align-items:stretch!important;padding-bottom:' + h + 'px!important}' +
      '.fe-modal__card--chromeless{height:100%!important;max-height:100%!important}';
  })();`;
}

/** One look at the scratch copy: newer than what we hold means an edit to bring
 *  home. */
async function pollOpenWith(id: string): Promise<void> {
  const live = liveOpenWith.get(id);
  if (!live || live.busy) return;
  live.busy = true;
  try {
    const acc = state.accounts.find((a) => a.id === live.record.accountId);
    if (!acc) return;
    const ctx = remoteCtx(acc);
    const dir = scratchRemoteDir(live.record.storage);
    const basename = live.record.remote.slice(live.record.remote.lastIndexOf('/') + 1);
    const current = await statRemote(ctx, dir, basename);
    if (!hasChanged(live.record.seen, current)) {
      if (live.closing && Date.now() >= live.until) await finishOpenWith(id);
      return;
    }
    const bytes = await downloadFile(ctx, live.record.remote);
    try {
      await writeBackAtomic(live.record.localPath, bytes, { fallbackDir: openWithRecoveryDir() });
      live.wroteBack = true;
      log('openwith', 'wrote back', { localPath: live.record.localPath, bytes: bytes.length });
      // A save that lands after the window is gone shortens the wait: the thing
      // the grace period exists for has happened.
      if (live.closing) live.until = Math.min(live.hardUntil, Date.now() + openWithQuietMs());
    } catch (err) {
      if (!(err instanceof WriteBackError)) throw err;
      openWithError = err.message;
      log('openwith', 'write-back FAILED', { error: err.message, keptAt: err.keptAt });
      // ⚠ Loud, not logged. The user pressed save, saw no error, and their
      // document did not change — the one outcome this feature must never
      // deliver quietly.
      const title = openText('writeFailedTitle', { name: path.basename(live.record.localPath) });
      const where = err.keptAt
        ? openText('writeFailedKept', { kept: err.keptAt })
        : openText('writeFailedLost');
      openWithNotify(title, where);
      await tellUser('error', title, err.message, where);
    }
    // Recorded either way. Retrying the same failing write every two seconds
    // would bury the machine in notifications and never succeed; the next
    // genuine save produces a new fingerprint and gets its own attempt.
    live.record.seen = current;
    live.record.updatedAt = new Date().toISOString();
    await sessionStore?.put(live.record);
  } catch (err) {
    // Network hiccup, server restart, expired token. Keep watching — the
    // document is still on the server and the next tick may well succeed.
    log('openwith', 'poll failed', String((err as Error)?.message ?? err));
  } finally {
    live.busy = false;
  }
}

/** The editor window closed. Keep watching — see openWithGraceMs(). */
async function beginOpenWithGrace(id: string): Promise<void> {
  const live = liveOpenWith.get(id);
  if (!live || live.closing) return;
  live.closing = true;
  const grace = openWithGraceMs();
  live.hardUntil = Date.now() + grace;
  live.until = live.hardUntil;
  log('openwith', 'window closed — waiting for a final save', { id, graceMs: grace });
  if (grace === 0) await finishOpenWith(id);
}

/** Deletes the scratch copy and forgets the session. */
async function finishOpenWith(id: string): Promise<void> {
  const live = liveOpenWith.get(id);
  if (!live) return;
  // Off the map FIRST: this can be reached from inside a poll, and a second
  // entry into the cleanup would delete the copy twice and fire two
  // notifications.
  liveOpenWith.delete(id);
  if (live.timer) clearInterval(live.timer);
  const acc = state.accounts.find((a) => a.id === live.record.accountId);
  if (acc) {
    try {
      await deleteRemote(remoteCtx(acc), scratchRemoteDir(live.record.storage), [live.record.remote]);
    } catch (err) {
      log('openwith', 'could not remove the scratch copy', String((err as Error)?.message ?? err));
    }
  }
  await sessionStore?.remove(id);
  log('openwith', 'session done', { id, wroteBack: live.wroteBack });
  if (live.wroteBack) {
    openWithNotify(
      openText('savedBackTitle'),
      openText('savedBackBody', { name: path.basename(live.record.localPath) }),
    );
  }
}

/**
 * What a previous run left behind.
 *
 * Two sweeps, because there are two ways a copy is orphaned. A session record
 * from another pid means the app died with a document open — its copy may hold
 * an edit that never came home, so that is recovered BESIDE the original (never
 * over it: the app was not running, and the local file may have moved on while
 * it was gone) before the copy is removed. A copy with no record at all comes
 * from a reinstall or a cleared profile, and is removed once it is old enough
 * that it cannot be anybody's working copy.
 */
async function sweepOpenWith(): Promise<void> {
  if (!sessionStore) return;
  const all = await sessionStore.list();
  const known = new Set(all.map((s) => s.remote.slice(s.remote.lastIndexOf('/') + 1)));

  for (const s of staleSessions(all, { currentPid: process.pid })) {
    const acc = state.accounts.find((a) => a.id === s.accountId);
    if (!acc) {
      await sessionStore.remove(s.id);
      continue;
    }
    const ctx = remoteCtx(acc);
    const dir = scratchRemoteDir(s.storage);
    const basename = s.remote.slice(s.remote.lastIndexOf('/') + 1);
    try {
      const current = await statRemote(ctx, dir, basename);
      if (needsRecovery(s, current)) {
        const bytes = await downloadFile(ctx, s.remote);
        const stamp = new Date().toISOString().replace(/[-:]/g, '').replace(/\..+$/, '');
        const kept = recoveryPathFor(s.localPath, stamp);
        await fs.promises.writeFile(kept, bytes);
        log('openwith', 'recovered an edit from a previous run', { localPath: s.localPath, kept });
        openWithNotify(
          openText('recoveredTitle'),
          openText('recoveredBody', { name: path.basename(s.localPath), kept: path.basename(kept) }),
        );
      }
      if (current) await deleteRemote(ctx, dir, [s.remote]);
    } catch (err) {
      // Server unreachable at boot is normal. Keep the record — the next start
      // will try again rather than leaking the copy forever.
      log('openwith', 'sweep deferred', { id: s.id, error: String((err as Error)?.message ?? err) });
      continue;
    }
    await sessionStore.remove(s.id);
  }

  for (const acc of state.accounts) {
    const storage = acc.openWithStorage;
    if (!storage) continue;
    try {
      const dir = scratchRemoteDir(storage);
      const entries = (await listDir(remoteCtx(acc), dir)).filter((e) => e.type === 'file');
      const dead = orphanScratchEntries(entries, known);
      if (!dead.length) continue;
      await deleteRemote(remoteCtx(acc), dir, dead.map((n) => scratchRemotePath(storage, n)));
      log('openwith', 'removed orphaned scratch copies', { account: acc.id, count: dead.length });
    } catch (err) {
      log('openwith', 'orphan sweep deferred', String((err as Error)?.message ?? err));
    }
  }
}

// ── becoming the default handler ─────────────────────────────────────
//
// ⚠⚠ Registration and DEFAULT are two different things, and only one of them is
// ours to do.
//
// The installer makes filex AVAILABLE for these types — a ProgId plus an
// `OpenWithProgids` entry on Windows (build/installer.nsh), `MimeType=` in the
// .desktop file on Linux, `CFBundleDocumentTypes` with rank `Alternate` on
// macOS. None of those take a file type away from whatever handles it today.
//
// Becoming the DEFAULT is always the user's explicit act:
//
//   Windows — impossible programmatically, by design. Since Windows 10 the
//     `…\FileExts\.docx\UserChoice` key is protected by a hash over the
//     extension, the user's SID and a salt; writing it without the hash is
//     ignored, and forging the hash is exactly the thing Microsoft built the
//     protection to stop. So the honest move is one click away from the finish:
//     open the OS's own Default apps page.
//   Linux — `xdg-mime default` genuinely sets it, so the button does it.
//     Except from inside a snap or a Flatpak, where it writes a mimeapps.list
//     no desktop reads: there Settings explains the file manager's own "Open
//     with" instead (src/channel.ts → defaultHandlerRoute).
//   macOS — `LSSetDefaultRoleHandlerForContentType` would do it, but Electron
//     exposes no binding for it and this app ships no native module. Finder's
//     "Change All…" is the real answer, so the button says so.

/** The desktop entry name the .deb/.rpm install (or the AppImage writes), and
 *  therefore xdg-mime, knows this app by (src/channel.ts → linuxDesktopEntry). */
const LINUX_DESKTOP_ENTRY = linuxDesktopEntry(CURRENT_CHANNEL, process.env.APPIMAGE);

/**
 * The AppImage's `filex://` registration: its own desktop entry
 * (`$XDG_DATA_HOME/applications/filex-appimage.desktop`, pointing at the
 * running image, with the link and the document types the packages declare),
 * then `xdg-mime default` for the scheme.
 *
 * ⚠ Not `app.setAsDefaultProtocolClient`: that runs `xdg-settings`, which
 * refuses an entry whose Exec path is quoted (see appImageDesktopEntry) — an
 * image in "~/My Apps" would register nothing. `xdg-mime default` writes the
 * same mimeapps.list line and never reads Exec. (Measured on the bare
 * AppImage before this: no entry, no default — `gio mime
 * x-scheme-handler/filex` answered "No default applications".)
 * ⚠ Only from the real registration path: a test run (FILEX_NO_BROWSER)
 * never reaches it, like every other OS registration here.
 */
function registerAppImage(appImage: string): void {
  const dataHome = process.env.XDG_DATA_HOME || path.join(app.getPath('home'), '.local', 'share');
  const file = path.join(dataHome, 'applications', APPIMAGE_DESKTOP_ENTRY);
  const scheme = `x-scheme-handler/${DEEP_LINK_SCHEME}`;
  const types = [...OFFICE_EXTENSIONS.map((e) => OFFICE_MIME_TYPES[e]).filter(Boolean), scheme] as string[];
  try {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, appImageDesktopEntry(appImage, types), 'utf8');
  } catch (e) {
    log('link', 'could not write the AppImage desktop entry; filex:// links will not reach this copy', String((e as Error)?.message ?? e));
    return;
  }
  execFile('xdg-mime', ['default', APPIMAGE_DESKTOP_ENTRY, scheme], (err, _out, stderr) => {
    if (err) log('link', 'xdg-mime could not make this AppImage the filex:// handler', (stderr || err.message).trim());
  });
}

function defaultHandlerRoute(): DefaultHandlerRoute {
  return handlerRouteFor(process.platform, CURRENT_CHANNEL);
}

async function makeFilexTheDefault(): Promise<{ route: string; ok: boolean; detail?: string }> {
  const route = defaultHandlerRoute();
  // Settings shows no button on this route; a call is a stale window.
  if (route === 'linux-manual') return { route, ok: false };
  if (route === 'settings') {
    // ms-settings: is a real OS handler, so this one does open — unlike the
    // app:// and blob: URLs openOutward() refuses.
    await shell.openExternal('ms-settings:defaultapps').catch(() => undefined);
    return { route, ok: true };
  }
  if (route === 'xdg') {
    const types = OFFICE_EXTENSIONS.map((e) => OFFICE_MIME_TYPES[e]).filter(Boolean) as string[];
    const detail = await new Promise<string>((resolve) => {
      execFile('xdg-mime', ['default', LINUX_DESKTOP_ENTRY, ...types], (err, _out, stderr) => {
        resolve(err ? (stderr || err.message).trim() : '');
      });
    });
    if (detail) return { route, ok: false, detail };
    return { route, ok: true };
  }
  await tellUser('info', openText('macDefaultTitle'), openText('macDefaultBody'), openText('macDefaultDetail'));
  return { route, ok: false, detail: openText('macDefaultDetail') };
}

/** What Settings needs to draw the panel honestly. */
function openWithPublicState() {
  return {
    extensions: [...OFFICE_EXTENSIONS],
    platform: process.platform,
    defaultRoute: defaultHandlerRoute(),
    // Associations are written by the INSTALLER. A run from source has none,
    // and a settings panel that offered to "make it the default" there would be
    // offering to make the OS point at a copy of Electron.
    registered: app.isPackaged,
    sessions: [...liveOpenWith.values()].map((l) => ({
      id: l.record.id,
      localPath: l.record.localPath,
      remote: l.record.remote,
      closing: l.closing,
      wroteBack: l.wroteBack,
    })),
    lastError: openWithError,
  };
}

/**
 * Runs an OS mount helper for src/drive.ts: writes `input` (a JSON payload or a
 * credential — never a command line) to the child's stdin and captures both
 * streams.
 *
 * ⚠ `windowsHide` so a mount never flashes a console window, and the token is
 * written to stdin and nowhere else. On Windows each helper is resolved to its
 * real system path rather than trusting PATH — but note powershell.exe does NOT
 * live in System32 itself (it is under System32\WindowsPowerShell\v1.0), so a
 * blanket "join System32" resolves it to a path that does not exist (ENOENT).
 */
function windowsHelperPath(file: string): string {
  const root = process.env.SystemRoot || 'C:\\Windows';
  if (file === 'powershell.exe') return path.join(root, 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe');
  if (file === 'net.exe') return path.join(root, 'System32', 'net.exe');
  return file;
}

function runMountHelper(
  file: string,
  args: string[],
  input: string,
): Promise<{ code: number; stdout: string; stderr: string }> {
  const resolved =
    process.platform === 'win32' && !file.includes('\\') && !file.includes('/')
      ? windowsHelperPath(file)
      : file;
  return new Promise((resolve) => {
    let child;
    try {
      child = spawn(resolved, args, { windowsHide: true });
    } catch (err) {
      resolve({ code: -1, stdout: '', stderr: String(err) });
      return;
    }
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (d) => (stdout += d.toString()));
    child.stderr.on('data', (d) => (stderr += d.toString()));
    child.on('error', (err) => resolve({ code: -1, stdout, stderr: `${stderr}${err}` }));
    child.on('close', (code) => resolve({ code: code ?? -1, stdout, stderr }));
    if (input) child.stdin.write(input);
    child.stdin.end();
  });
}

function wireIpc(): void {
  ipcMain.handle('state:get', () => publicState());

  ipcMain.handle('auth:begin', (_e, serverUrl: string) => {
    // ⚠ Refused before the browser opens: a sign-in whose token could not be
    // stored is a round trip through the browser that ends in an error. The
    // window already says why (publicState().keychain); this is the guard.
    const k = keychain();
    if (k !== 'ok') throw keychainRefusal(k);
    pendingAuth = beginBrowserAuth(serverUrl);
    signInFailure = null;
    // The URL is always handed back: the waiting screen shows it so a user
    // whose browser did not open (none installed, portable browser with no OS
    // handler, locked-down machine) can copy it and go there themselves. It
    // carries only the state and the challenge HASH — no secret.
    return { serverUrl: pendingAuth.serverUrl, authUrl: pendingAuth.authUrl };
  });

  // Manual fallback. The deep link is the happy path, but it does not always
  // arrive: no browser installed, a portable browser the OS has no handler
  // registration for, a locked-down machine, or the user finishing sign-in on
  // ANOTHER device. In all of those the browser still shows the code, and the
  // waiting screen accepts it typed in. state + verifier are already held here,
  // so the user only ever copies the short code — never a token.
  ipcMain.handle('auth:completeManual', async (_e, code: string) => {
    if (!pendingAuth) throw new Error('no sign-in is waiting — start again');
    const trimmed = String(code || '').trim();
    if (!trimmed) throw new Error('paste the code shown in your browser');
    try {
      await completeAuth(pendingAuth.state, trimmed);
    } catch (err) {
      // The server refused it, and a refused exchange uses the attempt up —
      // the window says so and offers a new one rather than a box that can
      // only fail again.
      if (pendingAuth) signInFailure = failureOf(pendingAuth.state, pendingAuth.state, err);
      throw err;
    }
    return publicState();
  });

  // Cancel on the waiting screen: the person chose the server form, so the
  // attempt ends here as well — otherwise the next reload would bring the
  // waiting screen back (issue #36 made the window follow the attempt).
  ipcMain.handle('auth:cancel', () => {
    pendingAuth = null;
    signInFailure = null;
    return publicState();
  });

  ipcMain.handle('auth:signOut', async (_e, id: string) => {
    removeAccount(state, id);
    saveState(state);
    accountsChanged();
    // ⚠ The account's watcher goes with it. Nothing reconciled here before:
    // the process kept syncing with the signed-out account's token — the one
    // credential the user had just asked this computer to forget — until the
    // app restarted. Its pairs stay in pairs.json, inert (no account, no
    // watcher); see docs/DESKTOP.md on signing out vs Reconnect.
    await refreshPairs();
    if (!activeAccount(state)) {
      mainWindow?.destroy();
      mainWindow = null;
      openShell('/connect', 'filex — Connect');
    } else mainWindow?.reload();
    return publicState();
  });

  ipcMain.handle('auth:switch', (_e, id: string) => {
    if (state.accounts.some((a) => a.id === id)) {
      state.activeId = id;
      saveState(state);
      accountsChanged();
      // ⚠ Forget the bell baseline. Without this, switching to a server whose
      // bell has older ids than the last one would announce its whole backlog
      // — or, the other way round, stay silent about everything new.
      notifier?.reset();
      // No window reload: the page re-mounts the explorer against the new
      // account itself. Tearing the window down would throw away the whole
      // explorer state on every click of the rail.
    }
    return publicState();
  });

  // The explorer talks to the server directly, so it needs a credential. It is
  // handed over one call at a time, for one account, rather than being pushed
  // into the page's state up front — `auth.token` accepts a function precisely
  // so the value does not have to sit in the renderer between requests.
  ipcMain.handle('account:token', (_e, id: string) => {
    const acc = state.accounts.find((a) => a.id === id);
    if (!acc) throw new Error('unknown account');
    return acc.token;
  });

  // The server's own admin panel opens in the BROWSER. It is a web console, it
  // wants the user's real session, and burying it inside a desktop file manager
  // is how the file manager stops looking like a file manager.
  ipcMain.handle('account:openAdmin', (_e, id: string) => {
    const acc = state.accounts.find((a) => a.id === id);
    if (!acc) throw new Error('unknown account');
    void shell.openExternal(new URL('/admin/', acc.serverUrl).toString());
  });

  ipcMain.handle('auth:add', () => {
    openShell('/connect', 'filex — Add an account');
  });

  // Reconnect: the same browser sign-in, for the SAME server — so a user
  // whose token was revoked gets it back without retyping the address, and
  // without signing out (which would forget the account's synced folders).
  // Signing in as the same person replaces the token and keeps everything
  // else; see completeAuth().
  ipcMain.handle('auth:reconnect', (_e, id: string) => {
    const acc = state.accounts.find((a) => a.id === id);
    if (!acc) throw new Error('unknown account');
    pendingAuth = beginBrowserAuth(acc.serverUrl);
    openShell('/reconnect', 'filex — Reconnect');
    return publicState();
  });

  // Host-owned open: a file opens in its OWN frameless document window. The
  // explorer emits `file-opened` (config.openInHost) and the app page calls this.
  ipcMain.handle('doc:open', (_e, accountId: string, remote: string) => {
    const acc = state.accounts.find((a) => a.id === accountId);
    if (!acc) throw new Error('unknown account');
    openViewerWindow(acc, remote);
  });

  // Our OWN window controls — frameless windows have no native caption on
  // Windows/Linux (macOS keeps its traffic lights). Each acts on the window that
  // SENT the call, so these three serve the main window and every document
  // window alike.
  ipcMain.handle('win:minimize', (e) => {
    BrowserWindow.fromWebContents(e.sender)?.minimize();
  });
  ipcMain.handle('win:toggleMaximize', (e) => {
    const w = BrowserWindow.fromWebContents(e.sender);
    if (!w) return;
    if (w.isMaximized()) w.unmaximize();
    else w.maximize();
  });
  ipcMain.handle('win:close', (e) => {
    BrowserWindow.fromWebContents(e.sender)?.close();
  });

  // ⚠ The explorer's multi-storage root does NOT discover storages by itself —
  // it mirrors the list the embedder hands it. Without this the window opened on
  // an empty "/" and never issued a single listing request, which looks exactly
  // like a broken connection. Measured before the fix: capabilities, ops and
  // ws-ticket were all requested; `manager?action=index` never was.
  ipcMain.handle('remote:storages', async (_e, accountId: string) => {
    const acc = state.accounts.find((a) => a.id === accountId);
    if (!acc) throw new Error('unknown account');
    const url = new URL('/api/files/manager', acc.serverUrl);
    url.searchParams.set('action', 'index');
    const res = await net.fetch(url.toString(), {
      headers: { Authorization: `Bearer ${acc.token}` },
    });
    // ⚠ A 401 here is the server saying the token is dead, not "try again":
    // the window used to show "Can't reach" with a Try again that could never
    // work. Mark the account; the window asks the main process and offers
    // Reconnect instead.
    if (res.status === 401) markSignedOut(acc.id, 'the file listing was refused (HTTP 401)');
    if (!res.ok) throw new Error(`server said ${res.status}`);
    const body = (await res.json()) as { storages?: string[] };
    return (body.storages ?? []).map((name) => ({ name }));
  });

  // #47 — ⌘K across every account on the rail. The mounted explorer holds a
  // credential for ONE account (its own, fetched per call); it reaches the
  // others only through `config.accountSearch`, and these two answer with the
  // token kept here, which never crosses into the page. Addresses:
  // src/remote-search.ts.
  ipcMain.handle(
    'remote:search',
    async (_e, accountId: string, query: string, opts?: { limit?: number; scope?: string }) => {
      const acc = state.accounts.find((a) => a.id === accountId);
      // A signed-out account has nothing to say; it is not an error to report
      // in the middle of somebody else's search.
      if (!acc || acc.signedOut) return [];
      const res = await net.fetch(remoteSearchUrl(acc.serverUrl, query, opts), {
        headers: { Authorization: `Bearer ${acc.token}` },
      });
      if (res.status === 401) {
        markSignedOut(acc.id, 'a search was refused (HTTP 401)');
        return [];
      }
      if (!res.ok) throw new Error(`server said ${res.status}`);
      return searchResults(await res.json());
    },
  );

  ipcMain.handle('remote:download', (e, accountId: string, remote: string) => {
    const acc = state.accounts.find((a) => a.id === accountId);
    if (!acc) throw new Error('unknown account');
    // ⚠ The credential is put on the request HERE rather than left to
    // wireAuthHeaderInjection: that one picks a token by ORIGIN, the active
    // account first, and two accounts on one server share an origin — the
    // download would go out as whoever the window is showing, and come back as
    // that person's 403 (or, worse, as that person's file of the same name).
    // Same save path as every other download in this window (openOutward).
    const win = BrowserWindow.fromWebContents(e.sender) ?? mainWindow;
    win?.webContents.downloadURL(remoteDownloadUrl(acc.serverUrl, remote), {
      headers: { Authorization: `Bearer ${acc.token}` },
    });
  });

  // The server's own identity — the logo and name an admin set under Branding.
  // A desktop client that shows the vendor's mark while the server it is looking
  // at has its own is a client that looks like it belongs to someone else.
  //
  // ⚠ Public endpoint, deliberately called WITHOUT the token: /api/branding is
  // what the login page reads before there is a session, and sending a bearer
  // to it would make the one screen that must work before sign-in depend on one.
  // A site-relative logo is absolutised here — the page lives on app://filex, so
  // a bare "/uploads/logo.png" would resolve against the app, not the server.
  ipcMain.handle('remote:branding', async (_e, accountId: string) => {
    const acc = state.accounts.find((a) => a.id === accountId);
    if (!acc) throw new Error('unknown account');
    const res = await net.fetch(new URL('/api/branding', acc.serverUrl).toString());
    if (!res.ok) throw new Error(`server said ${res.status}`);
    const body = (await res.json()) as { name?: string; logo_url?: string; accent?: string };
    let logo = String(body.logo_url ?? '').trim();
    if (logo && !/^(https?:|data:)/i.test(logo)) {
      try {
        logo = new URL(logo, acc.serverUrl).toString();
      } catch {
        logo = '';
      }
    }
    return { name: String(body.name ?? '').trim(), logoUrl: logo, accent: String(body.accent ?? '').trim() };
  });

  // Walks the server's real folder tree for the sync picker. Typing
  // `storage://some/path` by hand is a guess about someone else's server.
  ipcMain.handle('remote:browse', async (_e, accountId: string, remotePath: string) => {
    const acc = state.accounts.find((a) => a.id === accountId);
    if (!acc) throw new Error('unknown account');
    const url = new URL('/api/files/manager', acc.serverUrl);
    url.searchParams.set('action', 'index');
    if (remotePath) url.searchParams.set('path', remotePath);
    const res = await net.fetch(url.toString(), {
      headers: { Authorization: `Bearer ${acc.token}` },
    });
    if (res.status === 401) markSignedOut(acc.id, 'the folder picker was refused (HTTP 401)');
    if (!res.ok) throw new Error(`server said ${res.status}`);
    const body = (await res.json()) as {
      storages?: string[];
      files?: { basename: string; type: string }[];
    };
    // At the root the server lists storages, not files; inside one it lists
    // entries. Both are folders as far as this picker is concerned.
    if (!remotePath) {
      return (body.storages ?? []).map((s) => ({ name: s, path: `${s}://` }));
    }
    const base = remotePath.endsWith('://') || remotePath.endsWith('/') ? remotePath : `${remotePath}/`;
    return (body.files ?? [])
      .filter((f) => f.type === 'dir')
      .map((f) => ({ name: f.basename, path: `${base}${f.basename}` }));
  });

  ipcMain.handle('sync:trash', async () => {
    const out: { rel: string; deleted: string }[] = [];
    for (const p of knownPairs) {
      for (const it of await listTrash(p.id)) out.push({ rel: it.rel, deleted: it.deleted });
    }
    return out;
  });

  // ── "Mount as a drive" (WebDAV) ────────────────────────────────────────
  //
  // Attaches the account's filex server as an OS drive, and detaches it. The
  // commands and the address forms are shared with the connection guide
  // (src/drive.ts → @brftech/filex-core), so the drive the button attaches is
  // the one the guide describes.
  //
  // ⚠ The credential is the account's own token, handed to src/drive.ts as an
  // argument and delivered to the OS mounter on stdin — never on a command line
  // (drive.ts enforces and its tests measure that). The password field of a
  // filex WebDAV login accepts the API token, so no new credential is minted.
  //
  // The mounts are tracked only in memory: they are per-session (persistent:no),
  // so a restart honestly starts with none rather than claiming a drive that a
  // reboot dropped.
  const driveMounts = new Map<string, { letter?: string; mountDir?: string; storage?: string }>();
  const driveKey = (accountId: string, storage?: string) => `${accountId}\u0000${storage ?? ''}`;

  ipcMain.handle('drive:state', () => ({
    platform: process.platform,
    mounts: [...driveMounts.entries()].map(([key, m]) => {
      const accountId = key.split('\u0000')[0];
      return { accountId, storage: m.storage, letter: m.letter, mountDir: m.mountDir };
    }),
  }));

  ipcMain.handle('drive:mount', async (_e, accountId: string, storage?: string): Promise<MountResult> => {
    const acc = state.accounts.find((a) => a.id === accountId);
    if (!acc) return { ok: false, problem: 'failed', detail: 'unknown account' };
    const res = await mountDrive(
      {
        platform: process.platform as DrivePlatform,
        serverUrl: acc.serverUrl,
        storage: storage || undefined,
        user: acc.email,
        password: acc.token,
      },
      { exec: runMountHelper, exists: (p) => fs.existsSync(p), log, home: app.getPath('home') },
    );
    if (res.ok) driveMounts.set(driveKey(accountId, storage), { letter: res.letter, mountDir: res.mountDir, storage: storage || undefined });
    return res;
  });

  ipcMain.handle('drive:unmount', async (_e, accountId: string, storage?: string): Promise<MountResult> => {
    const key = driveKey(accountId, storage);
    const m = driveMounts.get(key);
    const res = await unmountDrive(
      { platform: process.platform as DrivePlatform, letter: m?.letter, mountDir: m?.mountDir },
      { exec: runMountHelper, exists: (p) => fs.existsSync(p), log, home: app.getPath('home') },
    );
    if (res.ok) driveMounts.delete(key);
    return res;
  });

  // Where the log lives, so a report can name it and Settings can open it.
  ipcMain.handle('app:logPath', () => logPath());

  ipcMain.handle('shell:openPath', (_e, target: string) => {
    void shell.openPath(target);
  });

  // ⚠ Electron has NO Web Share API — measured: navigator.share is undefined in
  // this shell. The explorer already has a share button, gated on exactly that,
  // so in the desktop app it simply never appeared. Rather than bolt a second
  // share UI next to the product's own one, the page polyfills navigator.share
  // onto this handler, and the existing button lights up.
  //
  // What "native" can honestly mean per platform:
  //   macOS   — the real system share sheet (Electron's ShareMenu).
  //   Windows — the OS share sheet needs WinRT, which Electron does not expose
  //             and which no amount of wishing will summon. A native context
  //             menu with the two things people actually do with a link is the
  //             honest substitute; it is a real OS menu, not a drawn imitation.
  //   Linux   — same.
  ipcMain.handle('app:share', async (e, data: { title?: string; text?: string; url?: string }) => {
    const url = data?.url ?? '';
    const text = data?.text ?? '';
    const body = [text, url].filter(Boolean).join('\n');
    if (!body) throw new Error('nothing to share');

    const win = BrowserWindow.fromWebContents(e.sender) ?? mainWindow ?? undefined;

    if (process.platform === 'darwin') {
      const { ShareMenu } = await import('electron');
      const menu = new ShareMenu({
        texts: text ? [text] : undefined,
        urls: url ? [url] : undefined,
      });
      menu.popup({ window: win });
      return { via: 'system-share-sheet' };
    }

    return await new Promise<{ via: string }>((resolve, reject) => {
      let settled = false;
      const finish = (via: string) => {
        if (!settled) { settled = true; resolve({ via }); }
      };
      // ⚠ Electron 44 rebuilt `clipboard` on the W3C Clipboard API: writeText
      // returns a Promise now. Resolving the share before it settles would
      // report "copied" for a write that may have failed, and a rejection
      // nobody awaits surfaces in the process-wide unhandledRejection handler
      // instead of at the caller. The choice is claimed SYNCHRONOUSLY (so the
      // menu-close callback below cannot turn a completed pick into an
      // AbortError while the write is in flight); the answer waits for the write.
      const copy = (textToCopy: string, via: string) => {
        if (settled) return;
        settled = true;
        clipboard.writeText(textToCopy).then(() => resolve({ via }), reject);
      };
      const menu = Menu.buildFromTemplate([
        {
          label: data?.title ? `Share “${data.title}”` : 'Share link',
          enabled: false,
        },
        { type: 'separator' },
        {
          label: 'Copy link',
          click: () => copy(url || body, 'clipboard'),
        },
        {
          label: 'Copy message with link',
          click: () => copy(body, 'clipboard-full'),
        },
        { type: 'separator' },
        {
          label: 'Send by email…',
          click: () => {
            const subject = encodeURIComponent(data?.title ?? 'filex');
            void shell.openExternal(`mailto:?subject=${subject}&body=${encodeURIComponent(body)}`);
            finish('mail');
          },
        },
        {
          label: 'Open in browser',
          enabled: !!url,
          click: () => { void shell.openExternal(url); finish('browser'); },
        },
      ]);
      menu.popup({
        window: win,
        // Dismissing the menu is a completed interaction in the Web Share
        // contract too — it rejects with AbortError, which the caller ignores.
        callback: () => {
          if (!settled) { settled = true; reject(new Error('AbortError')); }
        },
      });
    });
  });

  // "Check now" from Settings — the same check the timer runs.
  ipcMain.handle('update:check', () => {
    if (!app.isPackaged || process.env.FILEX_NO_UPDATE === '1') return publicState();
    // A store copy has no check of its own to run; Settings offers the store page.
    if (CURRENT_CHANNEL) return publicState();
    if (manualUpdates) {
      void checkFeedForManualUpdate();
      return publicState();
    }
    pushUpdateState({ status: 'checking' });
    autoUpdater.checkForUpdates().catch(() => {});
    return publicState();
  });

  // Opens the manual download in the browser. Only meaningful on a build that
  // cannot swap itself; the URL comes from the feed, never the renderer.
  // On a store copy the same button opens the store's page for filex.
  ipcMain.handle('update:download', () => {
    if ((updateState.status === 'manual' || updateState.status === 'store') && updateState.url) {
      void shell.openExternal(updateState.url);
    }
    return publicState();
  });

  // The Store copy's "Start when I sign in" lives in Windows Settings → Apps →
  // Startup (the startup task in build/appx-extensions.xml). A fixed URL from
  // here, never one from the renderer.
  ipcMain.handle('login:osSettings', () => {
    if (CURRENT_CHANNEL === 'msstore') void shell.openExternal('ms-settings:startupapps');
    return publicState();
  });

  // Windows' installed-apps list, where the filex.sh copy is removed. The app
  // does not run that copy's uninstaller itself: launched from inside the
  // package, its registry clean-up would land in the package's private hive
  // and leave the real uninstall entry behind.
  ipcMain.handle('app:osAppsSettings', () => {
    if (CURRENT_CHANNEL === 'msstore') void shell.openExternal('ms-settings:appsfeatures');
    return publicState();
  });

  // "Install it now" from Settings — the same silent swap the idle watcher does
  // on its own, just earlier. Never the wizard.
  ipcMain.handle('update:install', () => {
    if (updateState.status !== 'ready') return publicState();
    applyUpdateQuietly();
    return publicState();
  });

  ipcMain.handle('settings:set', async (_e, patch: Partial<DesktopState>) => {
    if (typeof patch.syncPaused === 'boolean') await setSyncPaused(patch.syncPaused);
    // Limits and the window are engine flags: a change restarts the watchers.
    const watchBefore = watchPrefsKey(currentWatchPrefs());
    if ('limitDownKiB' in patch) state.limitDownKiB = normLimit(patch.limitDownKiB);
    if ('limitUpKiB' in patch) state.limitUpKiB = normLimit(patch.limitUpKiB);
    if ('syncWindow' in patch) state.syncWindow = normWindow(patch.syncWindow);
    const watchChanged = watchPrefsKey(currentWatchPrefs()) !== watchBefore;
    if (typeof patch.runInBackground === 'boolean') state.runInBackground = patch.runInBackground;
    if (typeof patch.notifications === 'boolean') state.notifications = patch.notifications;
    if (typeof patch.launchAtLogin === 'boolean') {
      state.launchAtLogin = patch.launchAtLogin;
      setLoginItem(patch.launchAtLogin);
    }
    // Not a preference — the ground the window is painting right now, so the
    // NEXT launch can open on it instead of flashing. See windowGround().
    if (typeof patch.themeBg === 'string' && /^#[0-9a-fA-F]{3,8}$/.test(patch.themeBg)) {
      state.themeBg = patch.themeBg;
    }
    if (patch.locale === 'system' || patch.locale === 'en' || patch.locale === 'tr') {
      state.locale = patch.locale;
      // The tray is drawn by the main process and would otherwise keep the
      // language it was built with until the next restart — a menu in the old
      // language next to a window in the new one.
      refreshTray();
    }
    saveState(state);
    if (watchChanged) {
      log('sync', 'limits or window changed; restarting the watchers', currentWatchPrefs());
      await restartWatchers();
    }
    return publicState();
  });

  ipcMain.handle('sync:add', async (_e, remotePath: string) => {
    const acc = activeAccount(state);
    if (!acc) throw new Error('no active account');
    const remote = String(remotePath || '').trim();
    // The remote side must name a storage. A bare path is ambiguous the moment
    // a server hosts more than one, and guessing would pair the wrong folder.
    if (!remote.includes('://')) {
      throw new Error('Enter the server folder as storage://path, for example docs://reports');
    }
    const localDir = await pickDirectory();
    if (!localDir) return publicState();
    await addPair(localDir, remote, acc.id);
    await refreshPairs();
    return publicState();
  });

  ipcMain.handle('sync:remove', async (_e, id: string) => {
    try {
      await removePair(id);
    } finally {
      // Reconcile EVEN IF the remove threw. The watcher process only re-reads
      // the pair list when the supervisor restarts it; skipping this on error
      // left a process syncing a pair that was in fact already gone from
      // pairs.json — visibly listing the server every 30s until someone killed
      // it by hand. Measured, not theorised.
      await refreshPairs();
    }
    return publicState();
  });

  ipcMain.handle('sync:refresh', async () => {
    await refreshPairs();
    return publicState();
  });

  // ── items the engine holds for a decision (`hold_new` / `held`) ──
  //
  // A first sync that would push a stale mirror's worth of files into a
  // server folder with content holds them instead. The two answers are the
  // engine's own commands; afterwards the pair's watcher is restarted so the
  // decision is acted on now rather than at the next tick.

  const holdAnswer = async (pairId: string, act: (id: string) => Promise<string>): Promise<void> => {
    const pair = knownPairs.find((p) => p.id === pairId);
    if (!pair) return; // the list was stale; the next refresh says so
    // The order — watcher stopped first — is answerHold's (sync-policy.ts).
    const said = await answerHold({
      stop: () => {
        if (pair.account) supervisor?.stop(pair.account);
      },
      act: () => act(pair.id),
      refresh: () => refreshPairs(),
      onError: (e) =>
        tellUser('error', syncText('holdTitle'), syncText('holdFailed', { err: String((e as Error)?.message ?? e) })),
    });
    if (said !== null) log('sync', 'hold answered', { pairId, said });
  };

  ipcMain.handle('sync:holdUpload', async (_e, pairId: string) => {
    await holdAnswer(String(pairId), confirmHeld);
    return publicState();
  });

  ipcMain.handle('sync:holdDiscard', async (_e, pairId: string) => {
    const pair = knownPairs.find((p) => p.id === String(pairId));
    if (!pair) return publicState();
    const n = heldItems(pair);
    // ⚠ Asked natively, defaulting to Cancel: these are files on this
    // computer, and one misplaced click next to "Upload them" must not be
    // what moves them. They go to the sync trash, not away.
    const response = await askChoice({
      type: 'question',
      title: syncText('discardTitle'),
      message: n === 1 ? syncText('discardMessageOne') : syncText('discardMessage', { n: String(n) }),
      detail: syncText('discardDetail', { local: pair.local }),
      buttons: [syncText('discardButton'), syncText('cancel')],
      defaultId: 1,
      cancelId: 1,
      noLink: true,
    });
    if (response !== 0) return publicState();
    await holdAnswer(pair.id, discardHeld);
    return publicState();
  });

  // ── dragging files OUT onto the desktop ──────────────────────────
  //
  // Two calls, because the bytes have to be on this computer before an OS drag
  // can start (the shell copies from a path at DROP time — there is no
  // virtual-file API to borrow). `drag:prepare` materialises the selection and
  // `drag:start` hands the paths to the OS. The explorer only calls the second
  // one after the first said ready, so an internal move never waits on a
  // download it does not need.

  ipcMain.handle('drag:prepare', async (_e, accountId: string, items: DragItem[]) => {
    const acc = state.accounts.find((a) => a.id === accountId);
    if (!acc) throw new Error('unknown account');
    if (!dragCache || !Array.isArray(items) || items.length === 0) return { ready: false };
    const key = readyKeyOf(acc.id, items);
    if (dragReady?.key === key) return { ready: true, paths: dragReady.paths };

    // A new selection cancels the previous preparation: the user has moved on,
    // and two walks of two folder trees compete for the same network.
    dragPrepare?.abort();
    const ctrl = new AbortController();
    dragPrepare = ctrl;
    const res = await dragCache.prepare(
      items,
      {
        accountId: acc.id,
        serverUrl: acc.serverUrl,
        token: acc.token,
        mirrorFor: (remote) => mirrorPathFor(acc.id, remote),
        onProgress: (pr) => {
          if (!ctrl.signal.aborted) mainWindow?.webContents.send('drag:progress', pr);
        },
      },
      ctrl.signal,
    );
    if (dragPrepare === ctrl) dragPrepare = null;
    if (res.ready) dragReady = { key, paths: res.paths };
    // The paths travel back with the answer: the page is ours, a local path is
    // not a secret (Settings shows the mirror roots), and a contract whose
    // result can be looked at is one that can be MEASURED — the OS drag loop
    // itself cannot be driven from a script.
    return { ready: res.ready, error: res.error, paths: res.ready ? res.paths : [] };
  });

  ipcMain.handle('drag:start', async (e, accountId: string, items: DragItem[]) => {
    const acc = state.accounts.find((a) => a.id === accountId);
    if (!acc || !dragCache || !Array.isArray(items) || items.length === 0) return false;

    // Route 1 — the bytes are already here. Hand the OS the real files: correct
    // for every drop target, including an application that reads the file the
    // moment it arrives.
    //
    // ⚠ Every path is re-checked rather than trusted from prepare(): a drop
    // completed as a MOVE takes the cache entry with it, and a drag started on
    // a path that is gone is a gesture that silently does nothing.
    const key = readyKeyOf(acc.id, items);
    if (dragReady?.key === key && dragReady.paths.length > 0 && dragReady.paths.every((f) => fs.existsSync(f))) {
      // Logged like route 2 is, so which copies went to which account's drag
      // can be read back (search-e2e.mjs does).
      dragLog('prepared copies', { account: acc.id, paths: dragReady.paths });
      beginOsDrag(e.sender, dragReady.paths);
      return 'files';
    }

    // Route 2 — placeholders. Empty stand-ins copy in microseconds, so the drag
    // starts NOW whatever the size; we then find where they landed and put the
    // real bytes there. See dropwatch.ts for what this can and cannot see.
    dragDropCancel();
    const session = await createPlaceholders(dragCache.rootDir, acc.id, items);
    dragLog('stand-ins', { dir: session.dir, names: session.paths.map((p) => path.basename(p)) });
    // ⚠⚠ THE WATCHER GOES UP FIRST, AND IT IS NOT A STYLE CHOICE.
    // `startDrag` hands control to the operating system's own drag loop, and on
    // Windows that loop is MODAL: the call does not return until the user lets
    // go. Arming the watcher after it therefore arms it after the drop has
    // already happened — the creation event is long gone, nothing is ever
    // found, and the folder the user dropped stays exactly as the shell left
    // it: EMPTY. (Measured 2026-08-29, Burak, translated from Turkish: "I drag a
    // folder onto the desktop and its insides still come over empty". Single
    // files worked because a small selection is prepared in the background and
    // handed over as a real file, which needs no watcher at all — that is why
    // this only ever showed up on folders.)
    const watch = watchForDrop({
      names: session.paths.map((p) => path.basename(p)),
      ignoreDirs: [dragCache.rootDir],
      timeoutMs: 60_000,
    });
    dragDrop = { cancel: watch.cancel, dir: session.dir };
    // ⚠ Wait for the watchers to be UP, not merely asked for. Bounded, because
    // a drag that never starts is worse than one whose first moments are
    // unwatched: if the worker cannot arm in two seconds, go anyway and let the
    // timeout report it.
    await Promise.race([watch.ready, new Promise((r) => setTimeout(r, 2000))]);
    dragLog('watching', { roots: localDriveRoots(), ignore: dragCache.rootDir });

    beginOsDrag(e.sender, session.paths);
    dragLog('os drag returned', { note: 'on Windows this means the user has let go' });
    void watch.promise
      .then(async (loc) => {
        dragLog('watch result', loc ?? 'not found');
        await fs.promises.rm(session.dir, { recursive: true, force: true }).catch(() => undefined);
        if (!loc) {
          // Nothing landed anywhere we can see: either the drag was let go over
          // this window (an internal move — normal, and dragDropCancel already
          // ran) or it went into an application, which never writes a file.
          // Saying so is the honest end; pretending it worked is not.
          if (dragDrop) mainWindow?.webContents.send('drag:progress', { done: 0, total: items.length, finished: true, error: 'drop_not_found' });
          dragDrop = null;
          return;
        }
        mainWindow?.webContents.send('drag:progress', { done: 0, total: items.length, name: loc.name, dropped: loc.dir });
        dragLog('filling in', { dir: loc.dir, items: items.length });
        const res = await fulfilDrop(dragCache!, loc.dir, items, {
          accountId: acc.id,
          serverUrl: acc.serverUrl,
          token: acc.token,
          mirrorFor: (remote) => mirrorPathFor(acc.id, remote),
          onProgress: (pr) => mainWindow?.webContents.send('drag:progress', { ...pr, dropped: loc.dir }),
        });
        dragDrop = null;
        dragLog('fill result', res.ok ? { ok: true, written: res.written } : { ok: false, error: res.error });
        if (!res.ok) {
          // ⚠⚠ NOT dialog.showErrorBox. This runs long after the gesture, on
          // the main process, and showErrorBox is MODAL: the box freezes the
          // whole app until somebody clicks it — which is what a user gets for
          // having dragged a folder (measured 2026-08-29, Burak, translated from
          // Turkish: "the filex you opened is throwing an error"). The failure is
          // reported where the user is looking (the explorer's toast) and, if the
          // window is not in front, as an OS notification they can ignore.
          const body = syncText('dragFailedBody', { dir: loc.dir, err: res.error ?? '' });
          mainWindow?.webContents.send('drag:progress', {
            done: 0,
            total: items.length,
            dropped: loc.dir,
            finished: true,
            error: body,
          });
          if (Notification.isSupported() && !mainWindow?.isFocused()) {
            new Notification({ title: syncText('dragFailedTitle'), body }).show();
          }
        }
      })
      .catch((e) => {
        dragLog('fill threw', String((e as Error)?.stack ?? e));
        dragDrop = null;
      });

    return 'placeholder';
  });

  // The drag ended inside our own window (an internal move): stop watching the
  // drives and take the stand-ins away. Without this the watcher would sit
  // there for its whole timeout after every in-app drag.
  ipcMain.handle('drag:cancel', () => {
    dragDropCancel();
    return true;
  });

  // ── selective sync — the explorer's "keep on this computer" menu ──

  ipcMain.handle('sync:kept', (_e, accountId: string) =>
    accountPairs(String(accountId)).map((p) => ({ remote: p.remote, local: p.local })));

  ipcMain.handle('sync:keep', async (_e, accountId: string, remotePath: string, isFile?: boolean) => {
    const acc = state.accounts.find((a) => a.id === accountId);
    if (!acc) throw new Error('unknown account');
    const remote = normRemote(remotePath);
    if (!remote.includes('://')) throw new Error(`not a server folder: ${remote}`);
    // Refused here as well as in the engine, and BEFORE any mkdir: the wire
    // path comes from the server's listing, and a rejected keep should not
    // leave a directory behind for a folder it never paired.
    if (remote.split('/').includes('..')) throw new Error(`not a server folder: ${remote}`);
    const pairs = accountPairs(acc.id);
    // Covered already — by itself or an ancestor pair — is a no-op, not an error.
    if (pairs.some((p) => p.remote === remote || remoteInside(remote, p.remote))) return publicState();
    const root = await ensureSyncRoot(acc);
    if (!root) return publicState(); // the root prompt was cancelled — so is the keep
    // Keeping a parent absorbs kept children: their mirrors already sit at
    // exactly the paths the parent pair walks (same root, same mapping), so
    // dropping the child pairs first avoids the engine's overlap refusal and
    // loses nothing. A hand-made pair with a custom local path is the one
    // case that re-downloads — visible in the sync panel, and acceptable.
    for (const child of pairs.filter((p) => remoteInside(p.remote, remote))) {
      await removePair(child.id);
    }
    const local = localMirrorPath(root, remote);
    // A folder mirror IS a folder; a file mirror needs only its parents.
    await fs.promises.mkdir(isFile ? path.dirname(local) : local, { recursive: true });
    try {
      await addPair(local, remote, acc.id, isFile === true);
    } finally {
      await refreshPairs(); // reconcile even on failure — the children are already gone
    }
    return publicState();
  });

  ipcMain.handle('sync:unkeep', async (_e, accountId: string, remotePath: string) => {
    const remote = normRemote(remotePath);
    const pair = accountPairs(String(accountId)).find((p) => p.remote === remote);
    if (!pair) return publicState(); // already gone — the menu was stale
    const name = remote.endsWith('://')
      ? remote.slice(0, -'://'.length)
      : remote.slice(remote.lastIndexOf('/') + 1);
    // ⚠ Which button is the default depends on WHOSE folder it is. A mirror
    // this app created under the account's root is the app's to bin. A pair
    // made by hand in Settings points at a folder the user already had —
    // their Documents, a photo library — and "Move to Trash" pre-selected
    // there is one Enter away from binning it. Say the path out loud, and
    // default to leaving anything we did not create.
    const acc = state.accounts.find((a) => a.id === accountId);
    const ours = !!acc?.syncRoot && isInsideDir(acc.syncRoot, pair.local);
    const response = await askChoice({
      type: 'question',
      title: syncText('unkeepTitle'),
      message: syncText('unkeepMessage', { name }),
      detail: `${syncText('unkeepDetail')}\n\n${pair.local}`,
      buttons: [syncText('unkeepTrash'), syncText('unkeepLeave'), syncText('cancel')],
      defaultId: ours ? 0 : 1,
      cancelId: 2,
      noLink: true,
    });
    if (response === 2) return publicState();
    try {
      await removePair(pair.id);
    } finally {
      await refreshPairs();
    }
    if (response === 0) {
      // To the OS trash, not deletion — same restore story every user knows.
      try {
        await shell.trashItem(pair.local);
        // The mirror's empty ancestor skeleton (mkdir'ed at keep time) goes
        // too — bare folders left behind read as "it deleted my files but
        // kept the folders". Only under the account's own root: `ours` is
        // the same judgement the dialog default was based on.
        if (ours && acc?.syncRoot) {
          await pruneEmptyDirsUpTo(path.dirname(pair.local), acc.syncRoot);
        }
      } catch (e) {
        // Same rule as the nested-root refusal above: a modal box here freezes
        // the reply the settings panel is waiting on.
        await tellUser(
          'error',
          syncText('unkeepTitle'),
          syncText('trashFailed', { err: String((e as Error)?.message ?? e) }),
        );
      }
    }
    return publicState();
  });

  ipcMain.handle('sync:reveal', async (_e, accountId: string, remotePath: string) => {
    const remote = normRemote(remotePath);
    const pairs = accountPairs(String(accountId));
    const exact = pairs.find((p) => p.remote === remote);
    let local = exact?.local ?? null;
    if (!local) {
      // Kept via a parent: the mirror sits at the parent's local path plus
      // the remainder of the wire path, mapped exactly like localMirrorPath.
      const anc = pairs.find((p) => remoteInside(remote, p.remote));
      if (anc) {
        const rest = anc.remote.endsWith('://')
          ? remote.slice(anc.remote.length)
          : remote.slice(anc.remote.length + 1);
        local = path.join(anc.local, ...safeSegments(rest));
      }
    }
    // A file mirror is revealed beside its neighbours — openPath would
    // LAUNCH it instead.
    if (local) {
      const st = await fs.promises.stat(local).catch(() => null);
      if (st?.isFile()) {
        shell.showItemInFolder(local);
        return publicState();
      }
    }
    // A folder kept a moment ago — or one inside a parent whose first run has
    // not reached it yet — has no directory on disk, and openPath on a missing
    // path fails silently. Climb to the nearest ancestor that does exist so
    // the menu entry always opens SOMETHING the user can see, rather than
    // looking broken.
    while (local && !fs.existsSync(local)) {
      const up = path.dirname(local);
      local = up === local ? null : up;
    }
    if (local) await shell.openPath(local);
    return publicState();
  });

  // Settings: view or change the account's mirror root. Kept folders MOVE
  // with it — each pair is removed, its mirror renamed under the new root,
  // and the pair re-added there. The re-added pair's first run walks two
  // identical trees, so it settles without transferring a byte; the removed
  // baseline only costs that one settling pass.
  ipcMain.handle('sync:setRoot', async (_e, accountId: string) => {
    const acc = state.accounts.find((a) => a.id === accountId);
    if (!acc) throw new Error('unknown account');
    // ⚠ One move at a time. A move to another drive copies for hours, and a
    // second press of "Change…" meanwhile started a second move of the same
    // mirrors.
    if (rootMoves.has(acc.id)) return publicState();
    const def = acc.syncRoot ?? defaultSyncRoot(acc);
    await fs.promises.mkdir(def, { recursive: true });
    const newRoot = await pickDirectory({
      title: syncText('rootTitle'),
      buttonLabel: syncText('rootButton'),
      defaultPath: def,
    });
    if (!newRoot || newRoot === acc.syncRoot) return publicState();
    const oldRoot = acc.syncRoot ?? null;
    // ⚠ One root inside the other cannot be moved: renaming a folder into its
    // own child fails, and the sweep afterwards would be walking the tree it
    // just filled. Say so instead of half-moving.
    if (oldRoot && (isInsideDir(oldRoot, newRoot) || isInsideDir(newRoot, oldRoot))) {
      // ⚠⚠ tellUser, NOT dialog.showErrorBox — the rule this file already
      // states 200 lines up and this call site broke. showErrorBox is
      // SYNCHRONOUS and modal on the main process: it blocks the IPC reply the
      // renderer is awaiting until a human clicks OK. Measured 2026-09-12 — the
      // keep suite drives exactly this refusal, and the run stopped dead with an
      // "Error" window on the operator's desktop that nothing could dismiss.
      // tellUser logs, honours the FILEX_NO_BROWSER hook the rest of this file
      // uses, and does not freeze anything.
      await tellUser('error', syncText('rootTitle'), syncText('rootNested'));
      return publicState();
    }
    if (oldRoot) {
      // The watcher stops FIRST. It holds the pair list in memory for the
      // round it is in, and a mirror renamed under it mid-run reads as a
      // mass local delete — which, now that baselines survive migration,
      // would become a mass REMOTE delete. refreshPairs() restarts it.
      //
      // ⚠ And it stays stopped until the move ends: rootMoves keeps every
      // other refreshPairs() — a hold, a folder added, a crashed engine's
      // restart — from starting it in the middle (WatchGate.moving).
      rootMoves.add(acc.id);
      for (const w of BrowserWindow.getAllWindows()) w.webContents.send('sync:changed');
      supervisor?.stop(acc.id);
      try {
        // Only mirrors under the old root move; a hand-picked pair living
        // elsewhere was placed there on purpose and stays put.
        const mine = accountPairs(acc.id).filter((p) => isInsideDir(oldRoot, p.local) && p.local !== oldRoot);
        // Remember which top-level dirs (the storage names) we emptied, so the
        // sweep below touches only those — the root may be a folder the user
        // already had things in, and their empty folders are not ours to bin.
        const touched = new Set<string>();
        for (const p of mine) {
          const rel = path.relative(oldRoot, p.local);
          touched.add(rel.split(path.sep)[0]!);
          const dest = path.join(newRoot, rel);
          // `arrived` says the content is COMPLETE at dest: the rename went
          // through, or the cross-device copy finished (whatever the rm of the
          // source did afterwards). A rollback flips it back. It decides which
          // side the pair follows if something fails halfway — see the catch.
          let arrived = false;
          try {
            await fs.promises.mkdir(path.dirname(dest), { recursive: true });
            // ⚠ A move to another DRIVE is the usual reason to change the
            // root at all, and rename cannot cross devices (EXDEV). Copy and
            // remove instead — slower, but it is what the user asked for.
            const relocate = async (from: string, to: string) => {
              try {
                await fs.promises.rename(from, to);
                arrived = to === dest;
                return;
              } catch (e) {
                if ((e as NodeJS.ErrnoException)?.code !== 'EXDEV') throw e;
              }
              // ⚠ preserveTimestamps is not optional: the engine detects
              // change by (size, mtime), so a copy stamped "now" reads as every
              // file edited here and re-uploads the whole tree — the history
              // `sync move` keeps below would be worth nothing across drives.
              await fs.promises.cp(from, to, {
                recursive: true,
                force: true,
                errorOnExist: false,
                preserveTimestamps: true,
              });
              arrived = to === dest;
              await fs.promises.rm(from, { recursive: true, force: true });
            };
            await relocate(p.local, dest);
            try {
              // `sync move` keeps the pair's BASELINE, so the next run is an
              // ordinary incremental pass. The old remove + re-add threw it
              // away, and the first-run merge that followed conflicted every
              // file this machine had ever uploaded.
              await movePair(p.id, dest);
            } catch (e) {
              await relocate(dest, p.local); // pointer unmoved — put the folder back
              throw e;
            }
            await pruneEmptyDirsUpTo(path.dirname(p.local), oldRoot);
          } catch (e) {
            // Whatever failed, make the POINTER agree with where the content
            // is COMPLETE: with `sync move` the pair was never removed, but a
            // pair aimed at a partial tree plus a SURVIVING baseline reads as
            // a mass local delete on the next round — and becomes a mass
            // remote one. Two half-states exist: the copy finished and only
            // the rm of the old tree failed partway (dest complete, old path
            // a partial leftover → follow dest, even though the old path still
            // exists), or the copy itself failed (old path intact, dest is our
            // partial litter → leave the pair alone and discard the litter).
            if (arrived && fs.existsSync(dest)) {
              try {
                await movePair(p.id, dest);
              } catch {
                // The pointer cannot be made to agree with the content. An
                // unpaired folder syncs nothing — and deletes nothing; a pair
                // left on the partial side would. The dialog says the move
                // failed; the user re-keeps the folder from the explorer.
                await removePair(p.id).catch(() => {});
              }
            } else if (!arrived && fs.existsSync(dest) && fs.existsSync(p.local)) {
              await fs.promises.rm(dest, { recursive: true, force: true }).catch(() => {});
            }
            await tellUser(
              'error',
              syncText('rootTitle'),
              syncText('moveFailed', { name: p.remote, err: String((e as Error)?.message ?? e) }),
            );
          }
        }
        // Sweep what the mirrors left behind — litter-aware rmdir only, and
        // ONLY the storage dirs we just emptied. The old root itself stays: the
        // user chose that folder, and it may be one they already had.
        for (const entry of touched) {
          await removeIfEffectivelyEmpty(path.join(oldRoot, entry)).catch(() => false);
        }
      } finally {
        rootMoves.delete(acc.id);
      }
    }
    acc.syncRoot = newRoot;
    saveState(state);
    await refreshPairs();
    return publicState();
  });

  // Live sync state for the explorer's badges and its bottom progress strip.
  // The pair id from the supervisor is resolved to its remote here, so the
  // component never learns about pair ids at all.
  ipcMain.handle('sync:status', (_e, accountId: string) => {
    const st = supervisor?.statuses().find((s) => s.accountId === accountId);
    if (!st) return { running: false, lastError: null, active: null, live: null };
    const remote = st.active
      ? (knownPairs.find((p) => p.id === st.active?.pairId)?.remote ?? null)
      : null;
    return {
      running: st.running,
      // The explorer's contract has one error per account: the account's own,
      // else any pair's (each pair's card shows its own — see syncstatus.ts).
      lastError: st.lastError ?? Object.values(st.pairs).find((h) => h.error)?.error ?? null,
      live: st.live,
      active: st.active && remote
        ? { remote, phase: st.active.phase, done: st.active.done, total: st.active.total }
        : null,
    };
  });

  // "Open with filex" — what Settings shows, and the one button that can move
  // the OS's own default.
  ipcMain.handle('openwith:state', () => openWithPublicState());
  ipcMain.handle('openwith:setDefault', () => makeFilexTheDefault());

  // Test-only: feed a deep link straight in. Guarded by the same env flag that
  // suppresses the browser, so it cannot be reached in a normal run.
  ipcMain.handle('test:deepLink', async (_e, url: string) => {
    if (process.env.FILEX_NO_BROWSER !== '1') throw new Error('not available');
    await handleDeepLink(url);
    return publicState();
  });

  // Test-only: the same entry point the OS uses, without the OS. Lets a run
  // measure the document round trip without needing a registered file type on
  // the machine it runs on.
  ipcMain.handle('test:openWith', async (_e, paths: string[]) => {
    if (process.env.FILEX_NO_BROWSER !== '1') throw new Error('not available');
    await openDocuments(paths);
    return openWithPublicState();
  });

}

// ─────────────────────────── lifecycle ───────────────────────────

// One instance only: a second launch (or a deep link opening the app) must feed
// the running process, not start a rival one holding the same account store.
if (!app.requestSingleInstanceLock()) {
  app.exit(0);
} else {
  app.on('second-instance', (_e, argv) => {
    // ⚠ A second launch carries EITHER a sign-in deep link OR documents to
    // open, and until "Open with filex" existed the else-branch here was
    // "someone ran the app again, show them the window". Double-clicking a
    // .docx while filex was already running would have done exactly that:
    // raise the file manager and forget the document.
    const { deepLinks, files } = classifyArgv(argv, { defaultApp: process.defaultApp });
    for (const link of deepLinks) void handleDeepLink(link);
    if (files.length) queueOpenWith(files);
    else if (!deepLinks.length) route();
  });
  app.on('open-url', (e, url) => {
    e.preventDefault();
    void handleDeepLink(url);
  });
  // macOS hands documents over here, not in argv — and on a COLD start it fires
  // BEFORE `ready`, which is why queueOpenWith holds them until armOpenWith().
  // Without the queue the very first double-click after an install is the one
  // that silently does nothing.
  app.on('open-file', (e, filePath) => {
    e.preventDefault();
    queueOpenWith([filePath]);
  });

  // ⚠⚠ A file manager must not die of a background error. Electron's default
  // for an uncaught exception in the main process is a raw JavaScript error
  // box — modal, in the user's face, with a stack trace in it — and the app
  // sits frozen behind it. Burak got exactly that (2026-08-29) because a
  // response header made a transfer throw from inside an event handler, where
  // no try/catch could reach it. Anything that escapes is logged and shown as
  // an ordinary notification; the app keeps running, and the trail says what
  // happened.
  process.on('uncaughtException', (err) => {
    log('error', 'uncaught exception in the main process', String(err?.stack ?? err));
    try {
      if (Notification.isSupported()) {
        new Notification({
          title: syncText('unexpectedTitle'),
          body: String(err?.message ?? err).slice(0, 300),
        }).show();
      }
    } catch {
      /* the notification is a courtesy; never let it throw here */
    }
  });
  process.on('unhandledRejection', (reason) => {
    log('error', 'unhandled rejection in the main process', String(reason));
  });

  app.whenReady().then(() => {
    // No application menu. It is a file manager window, not an editor: the
    // default Edit/View/Window scaffolding only offers devtools and reload.
    Menu.setApplicationMenu(null);
    watchDownloads();

    if (process.env.FILEX_NO_BROWSER === '1') {
      // ⚠⚠ A test run registers NOTHING with the operating system. The scheme
      // lives in the user's registry (HKCU\Software\Classes\filex on
      // Windows), and a suite run from a checkout used to point it at
      // `electron.exe <checkout>` — so the next browser sign-in of the app the
      // person actually uses opened a dev build instead, until that app
      // happened to restart and re-register itself. The suites sign in through
      // the manual-code path (harness.mjs signIn) and never need the link.
    } else if (process.defaultApp) {
      // Dev runs are `electron .`, so the scheme has to point at the binary
      // plus the project path or Windows hands the link to a bare electron.
      if (process.argv.length >= 2) {
        app.setAsDefaultProtocolClient(DEEP_LINK_SCHEME, process.execPath, [path.resolve(process.argv[1])]);
      }
    } else if (CURRENT_CHANNEL === 'msstore') {
      // Declared in the package manifest (build/appx-extensions.xml). A
      // runtime registration from inside the package "will return true for all
      // calls but the registry key it sets won't be accessible by other
      // applications" (Electron's own documentation) — a success that did
      // nothing, so it is not made.
    } else if (linuxSandbox(CURRENT_CHANNEL)) {
      // Declared by the desktop entry the store installs (electron-builder.yml
      // `linux.mimeTypes` → `MimeType=…x-scheme-handler/filex;`), which the
      // host indexes like any other. A registration from inside the sandbox
      // cannot change the host's default (src/channel.ts → linuxSandbox).
    } else if (process.platform === 'linux' && process.env.APPIMAGE) {
      // A bare AppImage has no desktop entry on the system for the link to
      // name; it writes its own (src/channel.ts → appImageDesktopEntry).
      registerAppImage(process.env.APPIMAGE);
    } else {
      app.setAsDefaultProtocolClient(DEEP_LINK_SCHEME);
    }

    registerAppProtocol();
    state = loadState();
    {
      // Said once, at start: the sign-in window explains it to the user, and
      // this is what a bug report carries (src/keychain.ts).
      const k = keychain();
      if (k !== 'ok') log('keychain', 'no usable OS keychain — accounts are neither read nor stored', { state: k });
    }
    wireAuthHeaderInjection();
    // The supervisor keeps a `filex sync run --watch` alive per account. It is
    // started here, not when the Sync folders window opens: syncing that only
    // happens while a panel is on screen is not syncing.
    sleepGuard = new SleepGuard(powerSaveBlocker, (msg) => log('power', msg));
    supervisor = new SyncSupervisor({
      onChange: () => {
        // While any pair is being worked on the machine does not idle-sleep;
        // the moment none is, it may again. See src/power.ts.
        sleepGuard?.update(syncBusy(supervisor?.statuses() ?? []));
        paintTrayTip();
        for (const w of BrowserWindow.getAllWindows()) w.webContents.send('sync:changed');
      },
      onSignedOut: (accountId) => markSignedOut(accountId, 'the sync engine was refused (HTTP 401)'),
      // A run held items: re-read the pair list (it carries the count the
      // notice shows) unless it already says exactly this.
      onHold: (_accountId, pairId, count) => {
        const known = knownPairs.find((p) => p.id === pairId);
        if (known && heldItems(known) === count && known.hold_new === true) return;
        void refreshPairs();
      },
      watchPrefs: () => currentWatchPrefs(),
      // An engine that stopped on its own is started again from here, after
      // the supervisor's backoff (sync-policy.ts restartDelay).
      restart: () => void refreshPairs().catch((e) => log('sync', 'restart failed', String(e))),
    });
    // Local copies for dragging files out. Under userData rather than the OS
    // temp dir: the point of keeping them is that the SECOND drag of the same
    // file is instant, and a folder the OS may empty at any moment cannot
    // promise that. Entries older than a week are swept here.
    // ⚠ Explorer reads these copies on the drop, so they sit where Explorer
    // sees the same path — not in userData on the Store build (src/channel.ts).
    dragCache = new DragOutCache(path.join(explorerVisibleRoot(CURRENT_CHANNEL, app.getPath('userData'), app.getPath('home')), 'drag-cache'));
    void dragCache.sweep();
    // "Open with filex" session records. Under userData for the same reason the
    // drag cache is: a folder the OS may empty at any moment is not a place to
    // keep the only note of where an unfinished edit has to go home to.
    sessionStore = new SessionStore(path.join(app.getPath('userData'), 'openwith'));
    wireIpc();
    buildTray();
    // The bell, read by a process that can put something on screen. Started
    // with the app rather than with the window: the point is to reach somebody
    // who is NOT looking at filex.
    startNotifier();
    // Whether this build can swap itself decides WHICH updater to wire, so it
    // runs first.
    void detectManualUpdates().then(wireAutoUpdate);
    // The login item is NOT re-asserted here. It used to be, whenever the
    // preference was on — which brought back a client the user had disabled
    // in Task Manager (setLoginItemSettings also clears that flag). Now the
    // preference follows the OS; see reconcileLoginItem(). The cost: after an
    // install that MOVED (per-machine → per-user), the old entry points
    // elsewhere, the switch reads off, and one click in Settings writes the
    // new one.
    // Before the preference is compared with the OS: an autostart entry
    // still under the pre-rename name would read as "no login item" and
    // switch the preference off (see migrateLegacyLinuxNames).
    migrateLegacyLinuxNames();
    reconcileLoginItem();
    // A launch the user did not initiate stays in the tray. Opening a window at
    // sign-in — on top of whatever else the desktop is still restoring — is the
    // behaviour that makes people turn the setting off again.
    // Windows/Linux deliver BOTH the launch deep link and the documents to open
    // as argv entries.
    const launch = classifyArgv(process.argv, { defaultApp: process.defaultApp });
    // ⚠ A launch that came from double-clicking a document must not also throw
    // the file manager at the user: they asked for one window, and it is the
    // editor. Measured against the old unconditional route(): opening a .docx
    // put the explorer in front of it every time.
    // ⚠ `openWithQueue` is checked too, not just argv: on macOS the documents
    // arrived as `open-file` events that fired before this callback ran.
    if (
      !launch.files.length &&
      !openWithQueue.length &&
      !process.argv.includes(HIDDEN_FLAG) &&
      !process.argv.includes(UPDATED_FLAG)
    ) {
      route();
    }

    for (const link of launch.deepLinks) void handleDeepLink(link);

    // ⚠ Documents are released only AFTER the pairs are loaded. Whether a file
    // has a synced twin is the first question openDocuments asks, and
    // `knownPairs` is empty until refreshPairs() returns — so a cold start on a
    // document inside a synced folder would have taken the copy-and-write-back
    // route for a file that needed neither.
    void refreshPairs().finally(() => {
      queueOpenWith(launch.files);
      armOpenWith();
      // What a previous run left on the server: copies to remove, and edits
      // that never made it home.
      void sweepOpenWith();
    });

    app.on('activate', () => route());
  });

  app.on('before-quit', () => {
    quitting = true;
    // Kill the watchers explicitly. Orphaned CLI processes would keep syncing
    // after the app is gone, which is both surprising and impossible to stop
    // from the UI that no longer exists.
    supervisor?.stopAll();
    sleepGuard?.release();
  });

  app.on('window-all-closed', () => {
    // Deliberately does NOT quit while background mode is on — that is the
    // whole point of the tray.
    if (!state.runInBackground && process.platform !== 'darwin') app.quit();
  });
}
