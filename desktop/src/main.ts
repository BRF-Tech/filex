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
  dialog,
  ipcMain,
  nativeImage,
  net,
  protocol,
  shell,
} from 'electron';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import {
  activeAccount,
  loadState,
  removeAccount,
  saveState,
  upsertAccount,
  type DesktopState,
} from './accounts.js';
import { beginBrowserAuth, exchangeCode, parseAuthDeepLink, type PendingAuth } from './browser-auth.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const WEB_ROOT = path.join(__dirname, '..', 'app');
const UI_ROOT = path.join(__dirname, '..', 'ui');
const APP_SCHEME = 'app';
const APP_ORIGIN = `${APP_SCHEME}://filex`;
const START_URL = `${APP_ORIGIN}/admin/`;
const DEEP_LINK_SCHEME = 'filex';
// Packaged, build/ is not copied — electron-builder bakes the icon into the
// executable — so fall back to the source path for `electron .` runs.
const ICON_PATH = app.isPackaged
  ? path.join(process.resourcesPath, 'icon.png')
  : path.join(__dirname, '..', 'build', 'icon.png');

let state: DesktopState = { accounts: [], activeId: null, syncFolders: [], runInBackground: true, launchAtLogin: false };
let mainWindow: BrowserWindow | null = null;
let shellWindow: BrowserWindow | null = null;
let tray: Tray | null = null;
let pendingAuth: PendingAuth | null = null;
let quitting = false;

protocol.registerSchemesAsPrivileged([
  { scheme: APP_SCHEME, privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } },
]);

// ─────────────────────────── embedded bundle ───────────────────────────

function resolveEmbedded(root: string, urlPath: string, stripPrefix: RegExp): string {
  let rel = urlPath.replace(stripPrefix, '').replace(/^\/+/, '');
  if (rel === '' || !path.extname(rel)) rel = 'index.html';
  const resolved = path.normalize(path.join(root, rel));
  if (!resolved.startsWith(root)) return path.join(root, 'index.html');
  return resolved;
}

function registerAppProtocol(): void {
  protocol.handle(APP_SCHEME, async (request) => {
    const { host, pathname } = new URL(request.url);
    // Two surfaces on one scheme: app://filex/... is the web bundle,
    // app://shell/... is our own chrome (connect / settings / sync folders).
    const root = host === 'shell' ? UI_ROOT : WEB_ROOT;
    const strip = host === 'shell' ? /^\// : /^\/admin\//;
    const file = resolveEmbedded(root, decodeURIComponent(pathname), strip);
    const res = await net.fetch(pathToFileURL(file).toString());
    if (res.ok) return res;
    return net.fetch(pathToFileURL(path.join(root, 'index.html')).toString());
  });
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
    title: 'filex',
    icon: ICON_PATH,
    autoHideMenuBar: true,
    webPreferences: { preload: preload('preload-app.cjs'), contextIsolation: true, sandbox: true },
  });

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
    void shell.openExternal(url);
    return { action: 'deny' };
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

function refreshTray(): void {
  if (!tray) return;
  const acc = activeAccount(state);
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: acc ? `${acc.email} — ${new URL(acc.serverUrl).host}` : 'Not signed in', enabled: false },
      { type: 'separator' },
      { label: 'Open filex', click: () => route() },
      { label: 'Sync folders…', click: () => openShell('/sync', 'filex — Sync folders') },
      { label: 'Settings…', click: () => openShell('/settings', 'filex — Settings') },
      { type: 'separator' },
      {
        label: 'Quit filex',
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
  refreshTray();
  shellWindow?.close();
  openMainWindow();
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

function publicState() {
  return {
    accounts: state.accounts.map(({ token, ...rest }) => rest), // never hand the token to a renderer
    activeId: state.activeId,
    syncFolders: state.syncFolders,
    runInBackground: state.runInBackground,
    launchAtLogin: state.launchAtLogin,
  };
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
    refreshTray();
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
      refreshTray();
      // Reload rather than reuse: the runtime seam is injected at preload time,
      // so a different account means a fresh document.
      mainWindow?.destroy();
      mainWindow = null;
      openMainWindow();
    }
    return publicState();
  });

  ipcMain.handle('settings:set', (_e, patch: Partial<DesktopState>) => {
    if (typeof patch.runInBackground === 'boolean') state.runInBackground = patch.runInBackground;
    if (typeof patch.launchAtLogin === 'boolean') {
      state.launchAtLogin = patch.launchAtLogin;
      app.setLoginItemSettings({ openAtLogin: patch.launchAtLogin });
    }
    saveState(state);
    return publicState();
  });

  ipcMain.handle('sync:add', async (_e, remotePath: string) => {
    const acc = activeAccount(state);
    if (!acc) throw new Error('no active account');
    const picked = await dialog.showOpenDialog({ properties: ['openDirectory', 'createDirectory'] });
    if (picked.canceled || !picked.filePaths[0]) return publicState();
    state.syncFolders.push({
      id: `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
      accountId: acc.id,
      remotePath: remotePath || '/',
      localPath: picked.filePaths[0],
      enabled: true,
      lastSyncAt: null,
      status: 'never',
    });
    saveState(state);
    return publicState();
  });

  ipcMain.handle('sync:remove', (_e, id: string) => {
    state.syncFolders = state.syncFolders.filter((f) => f.id !== id);
    saveState(state);
    return publicState();
  });

  ipcMain.handle('sync:toggle', (_e, id: string) => {
    const f = state.syncFolders.find((x) => x.id === id);
    if (f) f.enabled = !f.enabled;
    saveState(state);
    return publicState();
  });

  // Test-only: feed a deep link straight in. Guarded by the same env flag that
  // suppresses the browser, so it cannot be reached in a normal run.
  ipcMain.handle('test:deepLink', async (_e, url: string) => {
    if (process.env.FILEX_NO_BROWSER !== '1') throw new Error('not available');
    await handleDeepLink(url);
    return publicState();
  });

  ipcMain.handle('shell:open', (_e, r: string) => {
    const titles: Record<string, string> = {
      '/settings': 'filex — Settings',
      '/sync': 'filex — Sync folders',
      '/connect': 'filex — Connect',
    };
    openShell(r, titles[r] ?? 'filex');
  });

  // The app preload reads this synchronously so window.__FILEX_RUNTIME__ exists
  // before the bundle's first request.
  ipcMain.on('session:runtime', (e) => {
    const acc = activeAccount(state);
    e.returnValue = acc
      ? { apiBaseUrl: `${acc.serverUrl}/api`, bearerToken: acc.token, useCredentials: false }
      : {};
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
    wireIpc();
    buildTray();
    route();

    // Windows/Linux deliver the launch deep link as an argv entry.
    const initial = process.argv.find((a) => a.startsWith(`${DEEP_LINK_SCHEME}://`));
    if (initial) void handleDeepLink(initial);

    app.on('activate', () => route());
  });

  app.on('before-quit', () => {
    quitting = true;
  });

  app.on('window-all-closed', () => {
    // Deliberately does NOT quit while background mode is on — that is the
    // whole point of the tray.
    if (!state.runInBackground && process.platform !== 'darwin') app.quit();
  });
}
