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
  nativeTheme,
  net,
  powerMonitor,
  protocol,
  session,
  shell,
} from 'electron';
import electronUpdater from 'electron-updater';
import { execFile } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import {
  activeAccount,
  loadState,
  removeAccount,
  saveState,
  upsertAccount,
  type Account,
  type DesktopState,
} from './accounts.js';
import { beginBrowserAuth, exchangeCode, parseAuthDeepLink, type PendingAuth } from './browser-auth.js';
import { SyncSupervisor, addPair, cliPath, listPairs, listTrash, removePair, type Pair } from './sync.js';

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

let state: DesktopState = { accounts: [], activeId: null, syncFolders: [], runInBackground: true, launchAtLogin: false, locale: 'system' };
let mainWindow: BrowserWindow | null = null;
let shellWindow: BrowserWindow | null = null;
let tray: Tray | null = null;
let pendingAuth: PendingAuth | null = null;
let quitting = false;
let supervisor: SyncSupervisor | null = null;
/** What the updater is doing, as far as the UI is concerned. */
let updateState: { status: 'idle' | 'checking' | 'available' | 'downloading' | 'ready' | 'error' | 'manual'; version?: string; percent?: number; error?: string; url?: string } = { status: 'idle' };

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

function openShell(route: string, title: string, width = 720, height = 620): void {
  if (shellWindow && !shellWindow.isDestroyed()) {
    shellWindow.loadURL(`app://shell/#${route}`);
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
    webPreferences: { preload: preload('preload-shell.cjs'), contextIsolation: true, sandbox: true },
  });
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
    backgroundColor: nativeTheme.shouldUseDarkColors ? '#14181d' : '#ffffff',
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

// ─────────────────────────── tray ───────────────────────────

function buildTray(): void {
  if (tray) return;
  // Real artwork, downscaled for the tray. Falls back to an empty image rather
  // than crashing if the file is missing in some packaging layout.
  let img = nativeImage.createFromPath(ICON_PATH);
  img = img.isEmpty() ? nativeImage.createEmpty() : img.resize({ width: 16, height: 16 });
  tray = new Tray(img);
  tray.setToolTip('filex');
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
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: acc ? `${acc.email} — ${new URL(acc.serverUrl).host}` : trayText('signedOut'), enabled: false },
      { type: 'separator' },
      { label: trayText('open'), click: () => route() },
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
  const attempt = pendingAuth;
  const { token, email } = await exchangeCode(attempt, state_, code);
  // Only clear the attempt once it actually worked: a mistyped code must leave
  // the user able to try again rather than sending them back to the start.
  pendingAuth = null;
  upsertAccount(state, { serverUrl: attempt.serverUrl, email, token });
  saveState(state);
  accountsChanged();
  shellWindow?.close();
  openMainWindow();
  // ⚠ Tell the window. Adding a SECOND account happens in a different window,
  // and openMainWindow() only shows the existing one — it does not reload it.
  // Without this the new account was stored but the rail kept showing one
  // avatar until the app was restarted. Measured, not theorised.
  mainWindow?.webContents.send('sync:changed');
  void refreshPairs();
}

/** OS-delivered deep link. No UI is waiting on it, so failures surface as a
 *  dialog and drop the user back on the connect screen. */
async function handleDeepLink(raw: string): Promise<void> {
  const parsed = parseAuthDeepLink(raw);
  if (!parsed) return;
  try {
    await completeAuth(parsed.state, parsed.code);
  } catch (err) {
    dialog.showErrorBox('filex — sign-in failed', String((err as Error)?.message ?? err));
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

// ─── macOS without a Developer ID: honesty instead of a broken swap ───
//
// Squirrel.Mac refuses to replace an app that is not Developer-ID signed, and
// electron-updater only finds that out AFTER the download: the check announced
// "version X installs itself shortly", the download completed, the swap was
// refused — and the user was left with a generic "could not check for
// updates". A permanent, known limitation of the ad-hoc sealed build was being
// dressed up as a transient error, on every check, forever.
//
// So the build's own signature is read once at startup. When it turns out to
// be ad-hoc, electron-updater is never wired at all — nothing is downloaded
// that can never be applied — and the same check cadence (and the Settings
// button) instead reads the static feed's latest-mac.yml directly and says the
// honest thing: a newer version exists, here is the download.

const MAC_FEED_URL = 'https://filex.sh/desktop/latest-mac.yml';

let macManualUpdates = false;

async function detectMacManualUpdates(): Promise<void> {
  if (process.platform !== 'darwin' || !app.isPackaged) return;
  const out = await new Promise<string>((resolve) => {
    execFile('/usr/bin/codesign', ['-dv', app.getPath('exe')], (err, stdout, stderr) => {
      // codesign prints the signature details on stderr; an error still
      // carries them (or means no signature at all, which is also "manual").
      resolve(String(stderr || stdout || err?.message || ''));
    });
  });
  macManualUpdates = !/Developer ID Application/.test(out);
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

async function checkFeedForManualUpdate(): Promise<void> {
  pushUpdateState({ status: 'checking' });
  try {
    const res = await net.fetch(MAC_FEED_URL, { signal: AbortSignal.timeout(15_000) });
    if (!res.ok) throw new Error(`feed answered ${res.status}`);
    const yml = await res.text();
    const version = /^version:\s*(\S+)/m.exec(yml)?.[1];
    if (!version) throw new Error('feed carries no version');
    if (!isNewerVersion(version, app.getVersion())) {
      pushUpdateState({ status: 'idle' });
      return;
    }
    // Hand the browser the dmg itself when the feed names one; the plain
    // downloads directory otherwise.
    const dmg = /^\s*-\s*url:\s*(\S+\.dmg)\s*$/m.exec(yml)?.[1];
    const url = dmg
      ? new URL(dmg, 'https://filex.sh/desktop/').toString()
      : 'https://filex.sh/desktop/';
    pushUpdateState({ status: 'manual', version, url });
  } catch (e) {
    pushUpdateState({ status: 'error', error: String((e as Error)?.message ?? e).slice(0, 200) });
  }
}

function wireAutoUpdate(): void {
  // Unpackaged runs have no updater metadata and would log an error on every
  // check; a machine told not to update must not phone home at all.
  if (!app.isPackaged || process.env.FILEX_NO_UPDATE === '1') return;

  if (macManualUpdates) {
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
    if (applying || updateState.status !== 'ready') return;
    const windowOpen = BrowserWindow.getAllWindows().some((w) => !w.isDestroyed() && w.isVisible());
    if (windowOpen) return;
    if (powerMonitor.getSystemIdleTime() < IDLE_SECONDS_BEFORE_APPLY) return;
    applyUpdateQuietly();
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
  // Under an AppImage, execPath is the extracted temp mount — a path that does
  // not survive the next launch. The image itself is what has to be run.
  const exe = process.env.APPIMAGE || process.execPath;
  return { path: exe, args: [HIDDEN_FLAG] };
}

/** Linux has no login-item API in Electron; XDG autostart is the equivalent.
 *  Without this the toggle is dead on every Linux build — silently. */
function linuxAutostartFile(): string {
  return path.join(app.getPath('home'), '.config', 'autostart', 'filex.desktop');
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

/** True when the OS will actually launch this app at the next sign-in — read
 *  back from the OS, never from our own intent. */
function loginItemActive(): boolean {
  if (process.platform === 'linux') return fs.existsSync(linuxAutostartFile());
  if (!app.isPackaged) return false;
  return app.getLoginItemSettings(loginItemSpec()).openAtLogin;
}

function setLoginItem(on: boolean): void {
  if (process.platform === 'linux') {
    setLinuxAutostart(on);
    return;
  }
  // A dev run must not write a login item at all: the only command it could
  // write is the one described above.
  if (!app.isPackaged) return;
  app.setLoginItemSettings({ openAtLogin: on, ...loginItemSpec() });
}

function publicState() {
  return {
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
    })),
    syncStatuses: supervisor?.statuses() ?? [],
    syncEngine: cliPath() ? 'bundled' : 'missing',
    runInBackground: state.runInBackground,
    launchAtLogin: state.launchAtLogin,
    locale: state.locale,
    // What 'system' currently resolves to, so the window does not have to
    // re-derive it from navigator.language and disagree with the tray.
    effectiveLocale: effectiveLocale(),
    // What the OS actually did with the request, not what we asked for. Login
    // items are refused often enough (policy, sandboxing, a user unticking it
    // elsewhere) that reporting our own intent back would be a lie.
    launchAtLoginEffective: loginItemActive(),
    // A dev run deliberately refuses to write one (see setLoginItem), and the
    // settings panel has to say WHY rather than show a switch that does nothing.
    launchAtLoginSupported: app.isPackaged || process.platform === 'linux',
    appVersion: app.getVersion(),
    update: updateState,
    // True on a macOS build whose signature cannot carry a self-update
    // (ad-hoc sealed). Settings swaps its "updates itself" copy for the
    // honest download story when this is set.
    updateManualOnly: macManualUpdates,
  };
}

/** Cache of the CLI's pairs, refreshed whenever they change. Reading the file
 *  through the CLI on every IPC call would fork a process per keystroke. */
let knownPairs: Pair[] = [];

async function refreshPairs(): Promise<void> {
  knownPairs = await listPairs();
  await supervisor?.reconcile(state.accounts, (id) => state.accounts.find((a) => a.id === id)?.token ?? null);
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

function accountPairs(accountId: string): Pair[] {
  return knownPairs.filter((p) => p.account === accountId);
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
async function pickDirectory(opts: { title?: string; buttonLabel?: string; defaultPath?: string } = {}): Promise<string | null> {
  const preset = process.env.FILEX_NO_BROWSER === '1' ? process.env.FILEX_TEST_PICK_DIR : undefined;
  if (preset) return preset;
  const dialogOpts = { ...opts, properties: ['openDirectory', 'createDirectory'] as const };
  const picked = mainWindow
    ? await dialog.showOpenDialog(mainWindow, { ...dialogOpts, properties: [...dialogOpts.properties] })
    : await dialog.showOpenDialog({ ...dialogOpts, properties: [...dialogOpts.properties] });
  return picked.canceled ? null : (picked.filePaths[0] ?? null);
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

/**
 * The account's mirror root, prompting on first use. The default —
 * `~/filex/<host>` — is pre-created so the dialog opens INSIDE it and a plain
 * "Use this folder" does the obvious thing; picking anywhere else works too.
 * Cancelling the dialog cancels the keep: null, nothing recorded.
 */
async function ensureSyncRoot(acc: Account): Promise<string | null> {
  if (acc.syncRoot) return acc.syncRoot;
  let def: string;
  try {
    def = path.join(app.getPath('home'), 'filex', new URL(acc.serverUrl).hostname);
  } catch {
    def = path.join(app.getPath('home'), 'filex');
  }
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

function wireIpc(): void {
  ipcMain.handle('state:get', () => publicState());

  ipcMain.handle('auth:begin', (_e, serverUrl: string) => {
    pendingAuth = beginBrowserAuth(serverUrl);
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
    await completeAuth(pendingAuth.state, trimmed);
    return publicState();
  });

  ipcMain.handle('auth:signOut', (_e, id: string) => {
    removeAccount(state, id);
    saveState(state);
    accountsChanged();
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
    if (!res.ok) throw new Error(`server said ${res.status}`);
    const body = (await res.json()) as { storages?: string[] };
    return (body.storages ?? []).map((name) => ({ name }));
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
      const menu = Menu.buildFromTemplate([
        {
          label: data?.title ? `Share “${data.title}”` : 'Share link',
          enabled: false,
        },
        { type: 'separator' },
        {
          label: 'Copy link',
          click: () => { clipboard.writeText(url || body); finish('clipboard'); },
        },
        {
          label: 'Copy message with link',
          click: () => { clipboard.writeText(body); finish('clipboard-full'); },
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
    if (macManualUpdates) {
      void checkFeedForManualUpdate();
      return publicState();
    }
    pushUpdateState({ status: 'checking' });
    autoUpdater.checkForUpdates().catch(() => {});
    return publicState();
  });

  // Opens the manual download in the browser. Only meaningful on a mac build
  // that cannot swap itself; the URL comes from the feed, never the renderer.
  ipcMain.handle('update:download', () => {
    if (updateState.status === 'manual' && updateState.url) void shell.openExternal(updateState.url);
    return publicState();
  });

  // "Install it now" from Settings — the same silent swap the idle watcher does
  // on its own, just earlier. Never the wizard.
  ipcMain.handle('update:install', () => {
    if (updateState.status !== 'ready') return publicState();
    applyUpdateQuietly();
    return publicState();
  });

  ipcMain.handle('settings:set', (_e, patch: Partial<DesktopState>) => {
    if (typeof patch.runInBackground === 'boolean') state.runInBackground = patch.runInBackground;
    if (typeof patch.launchAtLogin === 'boolean') {
      state.launchAtLogin = patch.launchAtLogin;
      setLoginItem(patch.launchAtLogin);
    }
    if (patch.locale === 'system' || patch.locale === 'en' || patch.locale === 'tr') {
      state.locale = patch.locale;
      // The tray is drawn by the main process and would otherwise keep the
      // language it was built with until the next restart — a menu in the old
      // language next to a window in the new one.
      refreshTray();
    }
    saveState(state);
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

  // ── selective sync — the explorer's "keep on this computer" menu ──

  ipcMain.handle('sync:kept', (_e, accountId: string) =>
    accountPairs(String(accountId)).map((p) => ({ remote: p.remote, local: p.local })));

  ipcMain.handle('sync:keep', async (_e, accountId: string, remotePath: string) => {
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
    await fs.promises.mkdir(local, { recursive: true });
    try {
      await addPair(local, remote, acc.id);
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
      } catch (e) {
        dialog.showErrorBox(
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

  // Test-only: feed a deep link straight in. Guarded by the same env flag that
  // suppresses the browser, so it cannot be reached in a normal run.
  ipcMain.handle('test:deepLink', async (_e, url: string) => {
    if (process.env.FILEX_NO_BROWSER !== '1') throw new Error('not available');
    await handleDeepLink(url);
    return publicState();
  });

}

// ─────────────────────────── lifecycle ───────────────────────────

// One instance only: a second launch (or a deep link opening the app) must feed
// the running process, not start a rival one holding the same account store.
if (!app.requestSingleInstanceLock()) {
  app.exit(0);
} else {
  app.on('second-instance', (_e, argv) => {
    const link = argv.find((a) => a.startsWith(`${DEEP_LINK_SCHEME}://`));
    if (link) void handleDeepLink(link);
    else route();
  });
  app.on('open-url', (e, url) => {
    e.preventDefault();
    void handleDeepLink(url);
  });

  app.whenReady().then(() => {
    // No application menu. It is a file manager window, not an editor: the
    // default Edit/View/Window scaffolding only offers devtools and reload.
    Menu.setApplicationMenu(null);

    if (process.defaultApp) {
      // Dev runs are `electron .`, so the scheme has to point at the binary
      // plus the project path or Windows hands the link to a bare electron.
      if (process.argv.length >= 2) {
        app.setAsDefaultProtocolClient(DEEP_LINK_SCHEME, process.execPath, [path.resolve(process.argv[1])]);
      }
    } else {
      app.setAsDefaultProtocolClient(DEEP_LINK_SCHEME);
    }

    registerAppProtocol();
    state = loadState();
    wireAuthHeaderInjection();
    // The supervisor keeps a `filex sync run --watch` alive per account. It is
    // started here, not when the Sync folders window opens: syncing that only
    // happens while a panel is on screen is not syncing.
    supervisor = new SyncSupervisor(() => {
      for (const w of BrowserWindow.getAllWindows()) w.webContents.send('sync:changed');
    });
    wireIpc();
    buildTray();
    // The signature check decides WHICH updater to wire, so it runs first.
    void detectMacManualUpdates().then(wireAutoUpdate);
    // Re-assert the login item on every packaged start. The command stored in
    // the registry is a full path, and an install that MOVES leaves it pointing
    // at nothing — which is what an upgrade from a per-machine install to a
    // per-user one does. Rewriting it here costs a registry write and keeps the
    // setting honest across reinstalls.
    if (state.launchAtLogin && !loginItemActive()) setLoginItem(true);
    // A launch the user did not initiate stays in the tray. Opening a window at
    // sign-in — on top of whatever else the desktop is still restoring — is the
    // behaviour that makes people turn the setting off again.
    if (!process.argv.includes(HIDDEN_FLAG) && !process.argv.includes(UPDATED_FLAG)) route();
    void refreshPairs();

    // Windows/Linux deliver the launch deep link as an argv entry.
    const initial = process.argv.find((a) => a.startsWith(`${DEEP_LINK_SCHEME}://`));
    if (initial) void handleDeepLink(initial);

    app.on('activate', () => route());
  });

  app.on('before-quit', () => {
    quitting = true;
    // Kill the watchers explicitly. Orphaned CLI processes would keep syncing
    // after the app is gone, which is both surprising and impossible to stop
    // from the UI that no longer exists.
    supervisor?.stopAll();
  });

  app.on('window-all-closed', () => {
    // Deliberately does NOT quit while background mode is on — that is the
    // whole point of the tray.
    if (!state.runInBackground && process.platform !== 'darwin') app.quit();
  });
}
