// Drives the REAL desktop app's sync panel against a REAL filex server.
//
// ⚠ No OS-level input. Playwright talks to the renderer; the operator keeps
// their mouse and keyboard.
//
// What this proves that a unit test cannot: the app finds the bundled CLI,
// spawns it with the right account's credentials, and files actually move —
// in both directions — while the app is the thing driving it. The engine's own
// correctness is covered by backend/internal/filesync; this is the wiring.
//
// Run: node scripts/sync-e2e.mjs
// Env: FILEX_SERVER, FILEX_EMAIL, FILEX_PASSWORD, FILEX_STORAGE (adapter name)

import fs, { readdirSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const DESKTOP = path.resolve(__dirname, '..');
const REPO = path.resolve(DESKTOP, '..');

const PNPM = path.join(REPO, 'node_modules/.pnpm');
const pwDir = readdirSync(PNPM).find((d) => d.startsWith('playwright-core@'));
const { _electron } = await import(
  pathToFileURL(path.join(PNPM, pwDir, 'node_modules/playwright-core/index.mjs')).href
);

const SERVER = process.env.FILEX_SERVER ?? 'https://fm.brf.sh';
const EMAIL = process.env.FILEX_EMAIL ?? '';
const PASSWORD = process.env.FILEX_PASSWORD ?? '';
const STORAGE = process.env.FILEX_STORAGE ?? 'docs';
const REMOTE = `${STORAGE}://desktop-sync-e2e`;

let failures = 0;
const check = (name, ok, detail = '') => {
  if (!ok) failures++;
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? `  — ${detail}` : ''}`);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function api(pathname, init = {}, token) {
  return fetch(`${SERVER}${pathname}`, {
    ...init,
    headers: { Authorization: `Bearer ${token}`, ...(init.headers ?? {}) },
  });
}

/** Plays the browser's half of the sign-in so the app ends up authenticated. */
async function browserHalf(authUrl) {
  const u = new URL(authUrl);
  const state = u.searchParams.get('desktop_state');
  const challenge = u.searchParams.get('desktop_challenge');
  const login = await fetch(`${SERVER}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD, remember: true }),
  });
  if (!login.ok) throw new Error(`browser login failed (${login.status})`);
  const cookie = (login.headers.getSetCookie?.() ?? []).map((c) => c.split(';')[0]).join('; ');
  const done = await fetch(`${SERVER}/api/auth/desktop/complete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Cookie: cookie },
    body: JSON.stringify({ state, challenge, label: 'filex desktop — sync e2e' }),
  });
  if (!done.ok) throw new Error(`complete failed (${done.status})`);
  const { code } = await done.json();
  const { token } = await (await fetch(`${SERVER}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
  })).json();
  return { code, adminToken: token };
}

// Hermetic everything: its own Electron profile AND its own HOME, so the run
// cannot read or damage the operator's real pairings in ~/.filex/sync.
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-sync-e2e-'));
const fakeHome = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-home-'));
const syncFolder = path.join(fakeHome, 'synced');
fs.mkdirSync(syncFolder, { recursive: true });

// ⚠ The path is checked but NOT passed to the app: the app has to find its own
// engine. Passing FILEX_CLI is what let a broken resolution ship.
const cli = path.join(DESKTOP, 'build', 'bin', process.platform === 'win32' ? 'filex.exe' : 'filex');
if (!fs.existsSync(cli)) {
  console.log(`FAIL  the CLI must be built first — run \`pnpm run fetch-cli\` (looked in ${cli})`);
  process.exit(1);
}

const app = await _electron.launch({
  args: [DESKTOP, `--user-data-dir=${profile}`],
  cwd: DESKTOP,
  env: {
    ...process.env,
    FILEX_NO_BROWSER: '1',
    HOME: fakeHome,
    USERPROFILE: fakeHome,
    // Pick the folder without a native dialog: showOpenDialog is OS chrome
    // Playwright cannot reach, and stubbing the app's own code would test the
    // stub. This env hook is honoured only when FILEX_NO_BROWSER is set.
    FILEX_TEST_PICK_DIR: syncFolder,
  },
});

let adminToken = null;
try {
  const connect = await app.firstWindow();
  await connect.waitForLoadState('domcontentloaded');
  await connect.locator('#server').fill(SERVER);
  await connect.locator('#go').click();
  await connect.locator('#authurl').waitFor({ timeout: 15_000 });
  const authUrl = await connect.locator('#authurl').inputValue();
  const half = await browserHalf(authUrl);
  adminToken = half.adminToken;
  await connect.locator('#code').fill(half.code);
  await connect.locator('#usecode').click().catch(() => {});

  const appWindow = await app.waitForEvent('window', { timeout: 60_000 });
  await appWindow.waitForLoadState('domcontentloaded');
  check('signed in', true);

  // Clean any leftover remote folder from a previous run.
  await api(`/api/files/manager?action=delete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items: [{ path: REMOTE, type: 'dir' }] }),
  }, adminToken).catch(() => {});

  // ── the sync panel ────────────────────────────────────────────────
  // Settings lives INSIDE the app window now (the gear at the bottom of the
  // account rail), not in a separate window.
  await appWindow.evaluate(() => document.querySelectorAll('#rail .rail-btn')[1].click());
  await appWindow.waitForTimeout(800);
  const sync = appWindow;

  const st0 = await sync.evaluate(() => window.filexApp.getState());
  check('the app found the bundled sync engine', st0.syncEngine === 'bundled', st0.syncEngine);
  check('no folders paired to begin with', st0.syncFolders.length === 0);

  // A remote without a storage name is ambiguous and must be refused.
  const refused = await sync.evaluate(async () => {
    try { await window.filexApp.addSync('just-a-path'); return null; }
    catch (e) { return String(e.message || e); }
  });
  check('a remote with no storage is refused', /storage:\/\/path/.test(refused ?? ''), refused ?? 'accepted!');

  // Put a file in the folder BEFORE pairing, so the first run has work to do.
  fs.writeFileSync(path.join(syncFolder, 'from-desktop.txt'), 'written on the PC');

  // The picker walks the server; feed it the remote path directly here since
  // the browsing itself is covered by shell-e2e.
  await sync.evaluate((remote) => window.filexApp.addSync(remote), REMOTE);
  await sync.waitForTimeout(1500);

  const st1 = await sync.evaluate(() => window.filexApp.getState());
  check('the pair is recorded', st1.syncFolders.length === 1,
    st1.syncFolders[0] ? `${st1.syncFolders[0].localPath} -> ${st1.syncFolders[0].remotePath}` : 'none');
  check('the pair carries the signed-in account',
    st1.syncFolders[0]?.accountId === st1.accounts[0]?.id);

  // ── does anything actually move? ──────────────────────────────────
  let uploaded = false;
  for (let i = 0; i < 40 && !uploaded; i++) {
    await sleep(1500);
    const r = await api(`/api/files/manager?action=index&path=${encodeURIComponent(REMOTE)}`, {}, adminToken);
    if (r.ok) uploaded = (await r.text()).includes('from-desktop.txt');
  }
  check('a local file reached the server without anyone pressing sync', uploaded);

  // The other direction: drop a file on the server and wait for the watcher.
  const form = new FormData();
  form.append('file', new Blob(['written on the server']), 'from-server.txt');
  await api(`/api/files/manager?action=upload&path=${encodeURIComponent(REMOTE)}`,
    { method: 'POST', body: form }, adminToken);

  const localCopy = path.join(syncFolder, 'from-server.txt');
  let downloaded = false;
  for (let i = 0; i < 40 && !downloaded; i++) {
    await sleep(1500);
    downloaded = fs.existsSync(localCopy);
  }
  check('a server file reached the PC on its own', downloaded);
  if (downloaded) {
    check('downloaded content is intact',
      fs.readFileSync(localCopy, 'utf8') === 'written on the server');
  }

  // ── the panel reports what happened ───────────────────────────────
  await sync.evaluate(() => window.filexApp.getState());
  await sync.waitForTimeout(600);
  const st2 = await sync.evaluate(() => window.filexApp.getState());
  const status = (st2.syncStatuses ?? [])[0];
  check('the panel shows a live watcher', status?.running === true,
    status ? `${status.lastLine}` : 'no status at all');
  await sync.screenshot({ path: path.join(REPO, 'desktop-sync-live.png') });

  // ── unpairing leaves the files alone ──────────────────────────────
  await sync.evaluate((id) => window.filexApp.removeSync(id), st2.syncFolders[0].id);
  await sync.waitForTimeout(1200);
  const st3 = await sync.evaluate(() => window.filexApp.getState());
  check('the pair is gone', st3.syncFolders.length === 0);
  check('unpairing did not delete the local files', fs.existsSync(localCopy));
} catch (e) {
  check('flow completed', false, String(e && e.message).split('\n')[0]);
} finally {
  if (adminToken) {
    await api(`/api/files/manager?action=delete`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ items: [{ path: REMOTE, type: 'dir' }] }),
    }, adminToken).catch(() => {});
  }
  await app.close().catch(() => {});
}

console.log(`\n==== ${failures === 0 ? 'ALL PASSED' : failures + ' FAILED'} ====`);
process.exit(failures === 0 ? 0 : 1);
