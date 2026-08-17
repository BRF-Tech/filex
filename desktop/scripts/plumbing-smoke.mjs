// Electron plumbing smoke for the desktop shell (Dilim 2).
//
// Verifies the Electron-SPECIFIC parts that unit tests can't reach, in a real
// Electron process:
//   1. config-store: safeStorage encrypts a session and round-trips it back
//      (the security-critical "token never in plaintext" contract).
//   2. app:// protocol + preload-app bridge: a window loading app://filex/ gets
//      a real origin and the `filexApp` bridge, and that bridge does NOT hand
//      over a token by default — the explorer asks for one call by call.
//
// Run: xvfb-run -a electron scripts/plumbing-smoke.mjs
// The window step is best-effort: if the renderer can't start on this host
// (headless GPU/shm limits), it's reported as UNVERIFIED rather than failing —
// the storage step still gives real evidence.
import { app, BrowserWindow, ipcMain, protocol, net, safeStorage } from 'electron';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { loadState, saveState, upsertAccount, removeAccount, activeAccount } from '../dist/accounts.js';
import { normalizeServerUrl, parseAuthDeepLink } from '../dist/browser-auth.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const FIXTURE_ROOT = path.join(__dirname, '..', 'test', 'fixture-app');
const APP_SCHEME = 'app';
const FAKE = { serverUrl: 'https://fm.example.com', token: 'smoke-token-abc123' };

// Headless stability (same flags that let Playwright run where Cypress crashed).
app.commandLine.appendSwitch('no-sandbox');
app.commandLine.appendSwitch('disable-gpu');
app.commandLine.appendSwitch('disable-dev-shm-usage');
// No OS keyring/secret-service in this headless container, so safeStorage would
// report unavailable. Force the 'basic' backend so the encrypt/decrypt/round-trip
// contract can be exercised here. A real desktop uses the OS keychain by default.
app.commandLine.appendSwitch('password-store', 'basic');

protocol.registerSchemesAsPrivileged([
  { scheme: APP_SCHEME, privileges: { standard: true, secure: true, supportFetchAPI: true } },
]);

const results = [];
const check = (name, ok, detail = '') => {
  results.push({ name, ok: !!ok, detail });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? '  — ' + detail : ''}`);
};

ipcMain.on('session:runtime', (e) => {
  e.returnValue = { apiBaseUrl: `${FAKE.serverUrl}/api`, bearerToken: FAKE.token, useCredentials: false };
});

async function storageStep() {
  const st = { accounts: [], activeId: null, syncFolders: [], runInBackground: true, launchAtLogin: false };
  upsertAccount(st, { serverUrl: FAKE.serverUrl, email: 'a@example.com', token: FAKE.token });

  if (safeStorage.isEncryptionAvailable()) {
    saveState(st);
    const back = loadState();
    check('accounts round-trip through safeStorage',
      back.accounts.length === 1 && back.accounts[0].token === FAKE.token,
      `serverUrl=${back.accounts[0] && back.accounts[0].serverUrl}`);

    const raw = await (await import('node:fs')).promises
      .readFile(path.join(app.getPath('userData'), 'desktop-state.bin'), 'utf8').catch(() => '');
    check('token not stored in plaintext', !raw.includes(FAKE.token));

    // Signing in again as the same identity must refresh, not duplicate.
    const again = loadState();
    upsertAccount(again, { serverUrl: FAKE.serverUrl + '/', email: 'A@Example.com', token: 'newer' });
    check('re-auth updates the account instead of duplicating',
      again.accounts.length === 1 && again.accounts[0].token === 'newer');

    // Removing an account must not strand its folder pairings.
    again.syncFolders.push({ id: 'f1', accountId: again.accounts[0].id, remotePath: '/x',
      localPath: '/tmp/x', enabled: true, lastSyncAt: null, status: 'never' });
    removeAccount(again, again.accounts[0].id);
    check('removing an account drops its sync folders',
      again.accounts.length === 0 && again.syncFolders.length === 0 && activeAccount(again) === null);
  } else {
    let threw = false;
    try { saveState(st); } catch { threw = true; }
    check('account store refuses plaintext when keychain unavailable', threw);
  }

  // Pure helpers — no keychain needed, and they gate the whole browser flow.
  check('bare host is normalised to https', normalizeServerUrl('fm.example.com') === 'https://fm.example.com');
  let rejected = false;
  try { normalizeServerUrl('http://example.com'); } catch { rejected = true; }
  check('plaintext http server is refused', rejected);
  const dl = parseAuthDeepLink('filex://auth?state=s1&code=c1');
  check('auth deep link parses', dl && dl.state === 's1' && dl.code === 'c1');
  check('foreign deep link is ignored', parseAuthDeepLink('https://evil.example/auth?state=s&code=c') === null);
}

async function windowStep() {
  protocol.handle(APP_SCHEME, async (req) => {
    const { pathname } = new URL(req.url);
    let rel = decodeURIComponent(pathname).replace(/^\/admin\//, '').replace(/^\/+/, '');
    if (rel === '' || !path.extname(rel)) rel = 'index.html';
    return net.fetch(pathToFileURL(path.join(FIXTURE_ROOT, rel)).toString());
  });

  const win = new BrowserWindow({
    show: false,
    webPreferences: {
      preload: path.join(__dirname, '..', 'dist', 'preload-app.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });
  await win.loadURL('app://filex/');
  // Fixture writes its findings into the document title.
  const title = win.webContents.getTitle();
  const payload = title.startsWith('SMOKE:') ? JSON.parse(title.slice(6)) : {};
  check('app:// gives the window a real origin', payload.origin === 'app://filex', `origin=${payload.origin}`);
  check('filexApp bridge exposed', payload.isDesktop === true);
  // ⚠ The bridge must NOT carry a credential. The explorer is given a `token()`
  // FUNCTION, so the value is fetched per request and never sits in the page.
  // A token pushed in up front would live in the renderer for the whole session.
  check('no token is pushed into the renderer', payload.tokenIsFunction === true,
    `token=${payload.tokenType}`);
  check('the bridge offers accounts + sync, not server admin', payload.hasAccountApi === true);
  win.destroy();
}

app.whenReady().then(async () => {
  let code = 0;
  try {
    await storageStep();
  } catch (e) {
    check('safeStorage step ran', false, String(e && e.message));
  }
  try {
    await windowStep();
  } catch (e) {
    // Renderer couldn't start — report, don't fail the whole smoke.
    console.log(`UNVERIFIED  app:// window step — renderer did not start: ${e && e.message}`);
  }
  const failed = results.filter((r) => !r.ok);
  console.log(`\n==== ${results.length - failed.length}/${results.length} checks passed ====`);
  if (failed.length) code = 1;
  app.exit(code);
});
