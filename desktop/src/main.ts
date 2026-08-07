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

let state: DesktopState = { accounts: [], activeId: null, syncFolders: [], runInBackground: true, launchAtLogin: false };
let mainWindow: BrowserWindow | null = null;
let shellWindow: BrowserWindow | null = null;
let tray: Tray | null = null;
let pendingAuth: PendingAuth | null = null;
let quitting = false;
let supervisor: SyncSupervisor | null = null;

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
      {
        label: 'Settings…',
        click: () => {
          route();
          mainWindow?.webContents.send('app:open-settings');
        },
      },
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
    // What the OS actually did with the request, not what we asked for. Login
    // items are refused often enough (policy, sandboxing, a user unticking it
    // elsewhere) that reporting our own intent back would be a lie.
    launchAtLoginEffective: app.getLoginItemSettings().openAtLogin,
    appVersion: app.getVersion(),
  };
}

/** Cache of the CLI's pairs, refreshed whenever they change. Reading the file
 *  through the CLI on every IPC call would fork a process per keystroke. */
let knownPairs: Pair[] = [];

async function refreshPairs(): Promise<void> {
  knownPairs = await listPairs();
  await supervisor?.reconcile(state.accounts, (id) => state.accounts.find((a) => a.id === id)?.token ?? null);
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
    const remote = String(remotePath || '').trim();
    // The remote side must name a storage. A bare path is ambiguous the moment
    // a server hosts more than one, and guessing would pair the wrong folder.
    if (!remote.includes('://')) {
      throw new Error('Enter the server folder as storage://path, for example docs://reports');
    }
    // The folder picker is OS chrome that an automated run cannot reach. Rather
    // than let the sync path go untested, the same env flag that suppresses the
    // browser also supplies the folder — so the test drives the REAL handler,
    // and this hook is unreachable in a normal run.
    const preset =
      process.env.FILEX_NO_BROWSER === '1' ? process.env.FILEX_TEST_PICK_DIR : undefined;
    let localDir = preset;
    if (!localDir) {
      const picked = await dialog.showOpenDialog({ properties: ['openDirectory', 'createDirectory'] });
      if (picked.canceled || !picked.filePaths[0]) return publicState();
      localDir = picked.filePaths[0];
    }
    await addPair(localDir, remote, acc.id);
    await refreshPairs();
    return publicState();
  });

  ipcMain.handle('sync:remove', async (_e, id: string) => {
    await removePair(id);
    await refreshPairs();
    return publicState();
  });

  ipcMain.handle('sync:refresh', async () => {
    await refreshPairs();
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
    // The supervisor keeps a `filex sync run --watch` alive per account. It is
    // started here, not when the Sync folders window opens: syncing that only
    // happens while a panel is on screen is not syncing.
    supervisor = new SyncSupervisor(() => {
      for (const w of BrowserWindow.getAllWindows()) w.webContents.send('sync:changed');
    });
    wireIpc();
    buildTray();
    route();
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
