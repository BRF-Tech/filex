// Drives the REAL desktop app: each synced folder's card says what is true of
// THAT folder, now — its own error until its next clean pass, and whether
// changes made on this computer are watched — in English and in Turkish.
//
// ⚠ No OS-level input. Playwright talks to the renderer; the operator keeps
// their mouse and keyboard.
//
// What this proves that syncstatus.test.ts cannot: the engine really prints
// the lines the parser expects, the supervisor really feeds them through, and
// the card really renders them.
//
// Run: node scripts/sync-status-e2e.mjs
// Env: FILEX_SERVER, FILEX_EMAIL, FILEX_PASSWORD, FILEX_STORAGE (adapter name)
//
// ⚠ It sets the signed-in account's QUOTA to force a failure and puts it back
// to unlimited at the end: run it against a throwaway server, never a real one.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  STORAGE,
  api, check, finish, launchApp, signIn, sleep,
} from './lib/harness.mjs';

const REMOTE_A = `${STORAGE}://status-e2e-a`;
const REMOTE_B = `${STORAGE}://status-e2e-b`;
const dirA = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-status-a-'));
const dirB = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-status-b-'));
// Folder A is past the watch budget (the root + three files > 2); folder B
// (the root + at most one file) is not. The engine reads the same budget on
// every platform through FILEX_SYNC_WATCH_BUDGET.
for (const n of ['one.txt', 'two.txt', 'three.txt']) fs.writeFileSync(path.join(dirA, n), n);

const { app } = await launchApp({
  env: {
    FILEX_TEST_PICK_DIR: [dirA, dirB].join(path.delimiter),
    FILEX_SYNC_WATCH_BUDGET: '2',
  },
});

let adminToken = null;
let userId = null;
const setQuota = (bytes) =>
  api(`/api/admin/users/${userId}/quota`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ quota_bytes: bytes }),
  }, adminToken);
const removeRemote = (remote) =>
  api(`/api/files/manager?action=delete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items: [{ path: remote, type: 'dir' }] }),
  }, adminToken).catch(() => {});

/** What the card for `remote` shows right now. */
async function card(win, remote) {
  return win.evaluate((remote) => {
    const c = [...document.querySelectorAll('[data-pair]')]
      .find((el) => el.querySelector('code')?.textContent === remote);
    if (!c) return null;
    const line = c.querySelector('[data-line]');
    return {
      pair: c.getAttribute('data-pair'),
      line: line?.textContent ?? null,
      err: line?.classList.contains('err') ?? false,
      live: c.querySelector('[data-live]')?.textContent ?? null,
      local: c.querySelector('[data-local]')?.getAttribute('data-local') ?? null,
      localText: c.querySelector('[data-local]')?.textContent ?? null,
    };
  }, remote);
}

async function until(fn, ms = 20000) {
  const t0 = Date.now();
  let last = null;
  while (Date.now() - t0 < ms) {
    last = await fn();
    if (last.ok) return last;
    await sleep(150);
  }
  return last;
}

try {
  const { win, adminToken: tok } = await signIn(app, { label: 'filex desktop — status e2e' });
  adminToken = tok;
  const me = await (await api('/api/auth/me', {}, adminToken)).json();
  userId = me.id ?? me.user?.id;
  check('signed in', Boolean(userId), String(userId));
  await removeRemote(REMOTE_A);
  await removeRemote(REMOTE_B);

  await win.evaluate(() => document.querySelectorAll('#rail .rail-btn')[1].click());
  await win.waitForTimeout(600);
  await win.evaluate((r) => window.filexApp.addSync(r), REMOTE_A);
  await win.evaluate((r) => window.filexApp.addSync(r), REMOTE_B);

  // ── local watching, per folder ────────────────────────────────────
  const noted = await until(async () => {
    const a = await card(win, REMOTE_A);
    const b = await card(win, REMOTE_B);
    return { ok: a?.local === 'too-large' && a?.live && b?.live, a, b };
  });
  check('the folder that cannot be watched says so under ITSELF', noted?.a?.local === 'too-large',
    JSON.stringify(noted?.a));
  check('…in words, next to the live word', /30-second check/.test(noted?.a?.localText ?? '') && Boolean(noted?.a?.live),
    `${noted?.a?.localText} | ${noted?.a?.live}`);
  check('the watched folder carries no such note', noted?.b?.local === null, JSON.stringify(noted?.b));

  // ── an error belongs to its folder and ends with it ───────────────
  await setQuota(1);
  fs.writeFileSync(path.join(dirB, 'big.txt'), 'x'.repeat(4096));
  const failed = await until(async () => {
    const b = await card(win, REMOTE_B);
    return { ok: b?.err === true, b, a: await card(win, REMOTE_A) };
  });
  check('a failing upload shows its error under that folder', failed?.b?.err === true && /quota/i.test(failed?.b?.line ?? ''),
    failed?.b?.line ?? 'no card');
  check('…and NOT under the other folder', failed?.a?.err === false, failed?.a?.line ?? 'no card');

  await setQuota(0);
  fs.appendFileSync(path.join(dirB, 'big.txt'), 'y');
  const cleared = await until(async () => {
    const b = await card(win, REMOTE_B);
    return { ok: b?.err === false, b };
  });
  check('the error is gone once that folder syncs cleanly again', cleared?.b?.err === false, cleared?.b?.line ?? 'no card');
  const up = await api(`/api/files/manager?action=download&path=${encodeURIComponent(`${REMOTE_B}/big.txt`)}`, {}, adminToken);
  check('…because the file really went up', up.ok && (await up.text()).length === 4097);

  // ── the same card in Turkish ──────────────────────────────────────
  await win.evaluate(() => document.querySelector('#settings [data-locale="tr"]')?.click());
  const tr = await until(async () => {
    const a = await card(win, REMOTE_A);
    return { ok: /Bu bilgisayarda yapılan değişiklikler 30 saniyelik kontrolde bulunur/.test(a?.localText ?? ''), a };
  }, 8000);
  check('the note speaks Turkish with Turkish letters', tr?.ok === true, tr?.a?.localText ?? 'no card');
  check('…and so does the live word', /^Canlı/.test(tr?.a?.live ?? ''), tr?.a?.live ?? '');
  await win.evaluate(() => document.querySelector('#settings [data-locale="system"]')?.click());
} catch (e) {
  check('flow completed', false, String(e && e.message).split('\n')[0]);
} finally {
  if (adminToken && userId) await setQuota(0).catch(() => {});
  if (adminToken) {
    await removeRemote(REMOTE_A);
    await removeRemote(REMOTE_B);
  }
  await app.close().catch(() => {});
}

finish();
