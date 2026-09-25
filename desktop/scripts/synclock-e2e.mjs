// Drives the REAL desktop app while ANOTHER filex on the same computer is
// already syncing one of its folders — the second copy of the app (installed
// + Microsoft Store), or `filex sync run` in a terminal.
//
// ⚠ No OS-level input. Playwright talks to the renderer; the operator keeps
// their mouse and keyboard.
//
// What this proves that the Go and node:test suites cannot, with the real
// app, its real supervisor and the real engine binary:
//   - the app's engine leaves a pair another process holds alone, and the
//     folder's card says so — in English and in Turkish — instead of an
//     error;
//   - the supervisor does NOT restart its engine over it (same process the
//     whole time: no restart loop);
//   - when the other process is KILLED (no graceful stop) the app's engine
//     takes the folder over by itself and syncs it.
//
// Run: node scripts/synclock-e2e.mjs
// Env: FILEX_SERVER, FILEX_EMAIL, FILEX_PASSWORD, FILEX_STORAGE (adapter name)
// Needs the engine at build/bin (`pnpm run fetch-cli`, or a `go build` of this
// checkout — the lock is new, an older engine has none).
//
// ⚠ Run it against a throwaway server: it signs in, creates and deletes a
// folder there.

import { execFileSync, spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  DESKTOP, SERVER, STORAGE,
  api, check, finish, launchApp, signIn, sleep,
} from './lib/harness.mjs';

const REMOTE = `${STORAGE}://synclock-e2e`;
const BUSY_EN = 'Another filex on this computer is syncing this folder — this copy takes over when that one stops';
const BUSY_TR = 'Bu bilgisayardaki başka bir filex bu klasörü eşitliyor — o kapandığında bu kopya devralır';

const cli = path.join(DESKTOP, 'build', 'bin', process.platform === 'win32' ? 'filex.exe' : 'filex');
if (!fs.existsSync(cli)) {
  console.log(`FAIL  the CLI must be built first (looked in ${cli})`);
  process.exit(1);
}
const syncFolder = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-synclock-'));

const { app, home } = await launchApp({ env: { FILEX_TEST_PICK_DIR: syncFolder } });

/** Every process: pid, parent pid, command line. */
function processTable() {
  if (process.platform === 'win32') {
    const ps = 'Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,CommandLine | ConvertTo-Json -Compress';
    return JSON.parse(execFileSync('powershell', ['-NoProfile', '-Command', ps], { encoding: 'utf8', maxBuffer: 64 << 20 }))
      .map((p) => ({ pid: p.ProcessId, ppid: p.ParentProcessId, cmd: p.CommandLine ?? '' }));
  }
  return execFileSync('ps', ['-eo', 'pid=,ppid=,args='], { encoding: 'utf8' }).split('\n').filter(Boolean).map((l) => {
    const [pid, ppid, ...cmd] = l.trim().split(/\s+/);
    return { pid: Number(pid), ppid: Number(ppid), cmd: cmd.join(' ') };
  });
}

/**
 * The app's own `sync run --watch` processes (pid list, sorted): the engines
 * its supervisor spawned — children of the app's main process, which may sit
 * one level below the process Playwright launched.
 */
function appWatchers() {
  const all = processTable();
  const tree = new Set([app.process().pid]);
  for (let grew = true; grew;) {
    grew = false;
    for (const p of all) {
      if (tree.has(p.ppid) && !tree.has(p.pid)) { tree.add(p.pid); grew = true; }
    }
  }
  return all.filter((p) => tree.has(p.pid) && /sync run.*--watch/.test(p.cmd)).map((p) => p.pid).sort();
}

/** What the card for REMOTE shows right now. */
async function card(win) {
  return win.evaluate((remote) => {
    const c = [...document.querySelectorAll('[data-pair]')]
      .find((el) => el.querySelector('code')?.textContent === remote);
    if (!c) return null;
    const line = c.querySelector('[data-line]');
    return {
      pair: c.getAttribute('data-pair'),
      line: line?.textContent ?? null,
      err: line?.classList.contains('err') ?? false,
      busy: line?.hasAttribute('data-busy') ?? false,
      title: line?.getAttribute('title') ?? '',
      live: c.querySelector('[data-live]')?.textContent ?? null,
    };
  }, REMOTE);
}

async function until(fn, ms) {
  const t0 = Date.now();
  let last = null;
  while (Date.now() - t0 < ms) {
    last = await fn();
    if (last.ok) return { ...last, after: Date.now() - t0 };
    await sleep(200);
  }
  return last ?? { ok: false };
}

const removeRemote = (token) =>
  api('/api/files/manager?action=delete', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items: [{ path: REMOTE, type: 'dir' }] }),
  }, token).catch(() => {});

async function putRemote(token, name, body) {
  const form = new FormData();
  form.append('file', new Blob([body]), name);
  await api(`/api/files/manager?action=upload&path=${encodeURIComponent(REMOTE)}`, { method: 'POST', body: form }, token);
}

const waitForFile = (p, body, ms) =>
  until(async () => ({ ok: fs.existsSync(p) && fs.readFileSync(p, 'utf8') === body }), ms);

let adminToken = null;
let other = null;
let otherOut = '';
try {
  const { win, adminToken: tok } = await signIn(app, { label: 'filex desktop — synclock e2e' });
  adminToken = tok;
  await removeRemote(adminToken);
  // Settings: the gear is the rail's last button (after "+"), drawn once the
  // accounts are loaded.
  await win.waitForFunction(() => document.querySelectorAll('#rail .rail-btn').length >= 2, null, { timeout: 15000 });
  await win.evaluate(() => [...document.querySelectorAll('#rail .rail-btn')].pop().click());
  await win.waitForTimeout(600);

  // ── the app keeps a folder; its engine syncs it ─────────────────────
  fs.writeFileSync(path.join(syncFolder, 'seed.txt'), 'from the PC');
  await win.evaluate((r) => window.filexApp.addSync(r), REMOTE);
  const st = await win.evaluate(() => window.filexApp.getState());
  const pairId = st.syncFolders[0]?.id;
  check('the folder is paired', Boolean(pairId), pairId ?? 'none');
  const seeded = await until(async () => {
    const r = await api(`/api/files/manager?action=index&path=${encodeURIComponent(REMOTE)}`, {}, adminToken);
    return { ok: r.ok && (await r.text()).includes('seed.txt') };
  }, 30000);
  check("the app's engine syncs it", seeded.ok);

  // ── another filex takes the folder while the app is paused ──────────
  await win.evaluate(() => window.filexApp.setSettings({ syncPaused: true }));
  const stopped = await until(async () => ({ ok: appWatchers().length === 0 }), 10000);
  check('pausing stops the app\'s engine', stopped.ok);

  // The "other filex": the same engine binary, the same HOME (so the same
  // ~/.filex/sync), started from a terminal — as a second copy of the app
  // would start its own.
  const env = { ...process.env, HOME: home, USERPROFILE: home, FILEX_URL: SERVER, FILEX_TOKEN: adminToken };
  delete env.FILEX_SYNC_DIR;
  other = spawn(cli, ['sync', 'run', '--pair', pairId, '--watch', '30s', '--quiet'], { env, windowsHide: true });
  other.stdout.on('data', (c) => { otherOut += c; });
  other.stderr.on('data', (c) => { otherOut += c; });
  const otherReady = await until(async () => ({ ok: otherOut.includes(`${pairId}: already in step`) || /\d+\/\d+ done/.test(otherOut) }), 30000);
  check('the other filex syncs the folder', otherReady.ok, otherOut.split('\n').slice(-3).join(' | '));

  await win.evaluate(() => window.filexApp.setSettings({ syncPaused: false }));

  // ── the app's engine leaves it alone and says so ────────────────────
  // (after the engine's grace: a restart meets its own predecessor's lock
  // for a moment, and that is not reported)
  const busy = await until(async () => { const c = await card(win); return { ok: c?.busy === true, c }; }, 30000);
  check('the card says another filex is syncing the folder', busy.ok && busy.c.line === BUSY_EN,
    busy.c ? `${busy.c.line} (after ${busy.after} ms)` : 'no card');
  check('…as a state, not an error', busy.c?.err === false);
  check('…naming the other process in its tooltip', (busy.c?.title ?? '').includes(`process ${other.pid}`), busy.c?.title ?? '');
  check('…without a live word for a folder this copy does not sync', busy.c?.live === null, String(busy.c?.live));
  const watchers = appWatchers();
  check('the app runs exactly one engine for the account', watchers.length === 1, JSON.stringify(watchers));

  // While it waits, the folder is still synced — by the other process.
  await putRemote(adminToken, 'while-busy.txt', 'the other filex brings this');
  const viaOther = await waitForFile(path.join(syncFolder, 'while-busy.txt'), 'the other filex brings this', 20000);
  check('the folder keeps syncing through the other filex', viaOther.ok);

  await sleep(12000);
  const still = await card(win);
  check('twelve seconds later it is still waiting — no retry storm, no error', still?.busy === true && still?.err === false,
    still?.line ?? 'no card');
  check('…and the supervisor has NOT restarted its engine', JSON.stringify(appWatchers()) === JSON.stringify(watchers),
    `${JSON.stringify(watchers)} → ${JSON.stringify(appWatchers())}`);

  // ── the same card in Turkish ────────────────────────────────────────
  await win.evaluate(() => document.querySelector('#settings [data-locale="tr"]')?.click());
  const tr = await until(async () => { const c = await card(win); return { ok: c?.line === BUSY_TR, c }; }, 8000);
  check('in Turkish, with Turkish letters', tr.ok, tr.c?.line ?? 'no card');
  await win.evaluate(() => document.querySelector('#settings [data-locale="system"]')?.click());

  // ── the other filex is killed: the app takes the folder over ────────
  other.kill('SIGKILL'); // TerminateProcess on Windows: no graceful stop, nothing released by hand
  await new Promise((r) => (other.exitCode !== null || other.signalCode !== null ? r() : other.once('exit', r)));
  const taken = await until(async () => { const c = await card(win); return { ok: c !== null && c.busy === false && c.err === false, c }; }, 20000);
  check('the card stops saying busy once the other filex is gone', taken.ok,
    taken.c ? `${taken.c.line} (after ${taken.after} ms)` : 'no card');
  await putRemote(adminToken, 'after-takeover.txt', 'the app brings this');
  const viaApp = await waitForFile(path.join(syncFolder, 'after-takeover.txt'), 'the app brings this', 20000);
  check("the app's engine syncs the folder now", viaApp.ok);
  check('…the same engine process: taken over, not restarted', JSON.stringify(appWatchers()) === JSON.stringify(watchers),
    `${JSON.stringify(watchers)} → ${JSON.stringify(appWatchers())}`);
  const conflicts = fs.readdirSync(syncFolder).filter((n) => n.includes('server copy'));
  check('no conflict copies came out of the handover', conflicts.length === 0, conflicts.join(', '));
} catch (e) {
  check('flow completed', false, String(e && e.message).split('\n')[0]);
} finally {
  if (other && other.exitCode === null && other.signalCode === null) other.kill('SIGKILL');
  if (adminToken) await removeRemote(adminToken);
  await app.close().catch(() => {});
}

finish();
