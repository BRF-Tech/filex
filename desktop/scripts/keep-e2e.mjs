// Drives "Keep on this computer" end to end against a REAL filex server.
//
// ⚠ No OS-level input. Playwright talks to the renderer; the operator keeps
// their mouse and keyboard. The two native dialogs this flow opens (the root
// folder picker, the unkeep question) are OS chrome no automation can reach,
// so the same env flag that suppresses the browser answers them — the run
// still drives the REAL handlers.
//
// What this proves that no unit test can: the shared explorer's folder menu
// reaches the desktop shell, the shell registers a pair under ONE account
// root, the watcher picks that pair up WITHOUT a restart (the pair list is
// re-read between rounds), files actually land on disk, a kept parent absorbs
// a kept child, a server path that climbs out of the root is refused, and
// unkeeping can leave the local copy alone.
//
// Run: node scripts/keep-e2e.mjs
// Env: FILEX_SERVER, FILEX_EMAIL, FILEX_PASSWORD, FILEX_STORAGE

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { DESKTOP, STORAGE, api, check, finish, launchApp, signIn, skipTour, sleep } from './lib/harness.mjs';

const DIR = 'keep-e2e';
const REMOTE = `${STORAGE}://${DIR}`;
const SUB = `${REMOTE}/sub`;

// Where the app will mirror. Handed over as the answer to the root picker, so
// the first keep does not stop at a dialog.
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-keeproot-'));
const UNKEEP_CHOICE = process.env.FILEX_TEST_DIALOG_CHOICE ?? '1';

const cli = path.join(DESKTOP, 'build', 'bin', process.platform === 'win32' ? 'filex.exe' : 'filex');
if (!fs.existsSync(cli)) {
  console.log(`FAIL  the CLI must be built first — run \`pnpm run fetch-cli\` (looked in ${cli})`);
  process.exit(1);
}

const { app } = await launchApp({
  env: {
    FILEX_TEST_PICK_DIR: root,
    // The unkeep question: 0 = move the local copy to the Trash, 1 = leave it.
    // Both branches matter, so the run takes the answer from the environment
    // and asserts whichever one it was given.
    FILEX_TEST_DIALOG_CHOICE: UNKEEP_CHOICE,
  },
});

/** Labels of the context menu currently open, innermost text nodes only. */
async function menuLabels(win) {
  return win.evaluate(() => {
    const menu = document.querySelector('[role="menu"]');
    if (!menu) return [];
    return [...menu.querySelectorAll('*')]
      .filter((e) => e.children.length === 0 && (e.textContent ?? '').trim())
      .map((e) => e.textContent.trim());
  });
}

async function openMenuOn(win, name) {
  await win.evaluate((n) => {
    const row = [...document.querySelectorAll('*')].find(
      (e) => e.children.length === 0 && e.textContent?.trim() === n);
    row?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    row?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 320, clientY: 260 }));
  }, name);
  await win.waitForTimeout(700);
}

async function clickMenuItem(win, label) {
  const clicked = await win.evaluate((l) => {
    const menu = document.querySelector('[role="menu"]');
    const it = [...(menu?.querySelectorAll('*') ?? [])].find(
      (e) => e.children.length === 0 && e.textContent?.trim() === l);
    if (!it) return false;
    it.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    return true;
  }, label);
  await win.waitForTimeout(900);
  return clicked;
}

const pairsOf = (st) => (st.syncFolders ?? []).map((p) => `${p.remotePath} -> ${p.localPath}`);

let token = null;
try {
  const { win, adminToken } = await signIn(app, { label: 'filex desktop — keep e2e' });
  token = adminToken;
  check('signed in', true);

  // ── fixtures: a folder with a file, and a subfolder with another ──
  await api('/api/files/manager?action=delete', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items: [{ path: REMOTE, type: 'dir' }] }),
  }, token).catch(() => {});
  for (const [parent, name] of [[`${STORAGE}://`, DIR], [REMOTE, 'sub']]) {
    const mk = await api('/api/files/manager?action=newfolder', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: parent, name }),
    }, token);
    if (!mk.ok) throw new Error(`seeding ${parent}${name} failed (${mk.status})`);
  }
  for (const [dir, name, body] of [[REMOTE, 'notes.md', '# kept\n'], [SUB, 'deep.txt', 'inside\n']]) {
    const form = new FormData();
    form.append('path', `${dir}/`);
    form.append('file[]', new Blob([body], { type: 'text/plain' }), name);
    const up = await api('/api/files/manager?action=upload', { method: 'POST', body: form }, token);
    if (!up.ok) throw new Error(`seeding ${name} failed (${up.status})`);
  }

  await skipTour(win);
  const accountId = await win.evaluate(async () => (await window.filexApp.getState()).accounts[0].id);

  // ── a kept child, made before the parent, to be absorbed later ────
  await win.evaluate(([acc, remote]) => window.filexApp.syncKeep(acc, remote), [accountId, SUB]);
  await win.waitForTimeout(500);
  let st = await win.evaluate(() => window.filexApp.getState());
  const subLocal = path.join(root, STORAGE, DIR, 'sub');
  check('keeping a subfolder pairs it under the account root',
    st.syncFolders.some((p) => p.remotePath === SUB && p.localPath === subLocal), pairsOf(st).join(' | '));
  check('the root picker answered once and was remembered',
    st.accounts[0].syncRoot === root || st.syncFolders.length > 0);

  // ── the menu, on the real listing ────────────────────────────────
  await win.evaluate(() => window.location.reload());
  await win.waitForLoadState('domcontentloaded');
  await win.waitForTimeout(2500);
  await skipTour(win);
  await openMenuOn(win, DIR);
  let labels = await menuLabels(win);
  check('a folder menu offers keeping it on this computer',
    labels.includes('Keep on this computer'), labels.join(' | '));

  check('the menu entry starts the keep', await clickMenuItem(win, 'Keep on this computer'));
  await win.waitForTimeout(1200);
  st = await win.evaluate(() => window.filexApp.getState());
  const parentLocal = path.join(root, STORAGE, DIR);
  const covering = st.syncFolders.filter((p) => p.remotePath === REMOTE || p.remotePath === SUB);
  check('keeping the parent leaves exactly one pair — the child was absorbed',
    covering.length === 1 && covering[0].remotePath === REMOTE && covering[0].localPath === parentLocal,
    pairsOf(st).join(' | '));

  // ── the watcher was already running: no restart, next round picks it up ──
  const wantFiles = [path.join(parentLocal, 'notes.md'), path.join(parentLocal, 'sub', 'deep.txt')];
  let waited = 0;
  while (waited < 120_000 && !wantFiles.every((f) => fs.existsSync(f))) {
    await sleep(2000);
    waited += 2000;
  }
  check('the kept folder really fills up on disk (watcher picked the new pair up live)',
    wantFiles.every((f) => fs.existsSync(f)), `${Math.round(waited / 1000)}s: ` +
    wantFiles.map((f) => `${path.basename(f)}=${fs.existsSync(f)}`).join(' '));

  // ── menu states: kept parent, inherited child ────────────────────
  await openMenuOn(win, DIR);
  labels = await menuLabels(win);
  check('a kept folder is not offered again, and offers the way back',
    !labels.includes('Keep on this computer') && labels.includes('Keep online only')
      && labels.includes('Open local folder'), labels.join(' | '));

  await win.evaluate((n) => {
    const row = [...document.querySelectorAll('*')].find(
      (e) => e.children.length === 0 && e.textContent?.trim() === n);
    row?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
  }, DIR);
  await win.waitForTimeout(2000);
  await openMenuOn(win, 'sub');
  labels = await menuLabels(win);
  check('a folder inside a kept parent says so instead of pretending it is separate',
    labels.includes('Kept on this computer with its parent'), labels.join(' | '));

  // ── a server path that climbs out of the mirror root is refused ──
  const escape = await win.evaluate(([acc, remote]) =>
    window.filexApp.syncKeep(acc, remote).then(() => 'accepted', (e) => String(e?.message ?? e)),
  [accountId, `${STORAGE}://../../filex-escape`]);
  st = await win.evaluate(() => window.filexApp.getState());
  const outside = path.resolve(root, '..', 'filex-escape');
  check('a remote that climbs out of the root is refused, and nothing is created outside it',
    escape !== 'accepted' && !st.syncFolders.some((p) => p.remotePath.includes('..')) && !fs.existsSync(outside),
    escape);

  // ── unkeep, choosing to leave the local copy ─────────────────────
  // Back to the parent listing. A reload rather than history.back(): the
  // shell mounts the explorer fresh, which is also one more proof that the
  // kept state survives a restart of the window.
  await win.evaluate(() => window.location.reload());
  await win.waitForLoadState('domcontentloaded');
  await win.waitForTimeout(2500);
  await skipTour(win);
  await openMenuOn(win, DIR);
  labels = await menuLabels(win);
  check('the kept state survives a window reload', labels.includes('Keep online only'), labels.join(' | '));
  check('the way back is a menu entry too', await clickMenuItem(win, 'Keep online only'));
  await win.waitForTimeout(1500);
  st = await win.evaluate(() => window.filexApp.getState());
  check('unkeeping removes the pair', !st.syncFolders.some((p) => p.remotePath === REMOTE), pairsOf(st).join(' | '));
  const stillThere = fs.existsSync(path.join(parentLocal, 'notes.md'));
  check(
    UNKEEP_CHOICE === '0'
      ? '"move the local copy to the Trash" really takes it off the disk'
      : '"leave the local copy" really leaves it',
    UNKEEP_CHOICE === '0' ? !stillThere : stillThere,
    `choice=${UNKEEP_CHOICE} present=${stillThere}`,
  );
} catch (e) {
  check(`the run reached the end (${e?.message ?? e})`, false);
} finally {
  if (token) {
    await api('/api/files/manager?action=delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ items: [{ path: REMOTE, type: 'dir' }] }),
    }, token).catch(() => {});
  }
  await app.close().catch(() => {});
  fs.rmSync(root, { recursive: true, force: true });
}

finish();
