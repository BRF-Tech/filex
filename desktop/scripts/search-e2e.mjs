// ⌘K "Everywhere" in the desktop app — the measurement for task #47.
//
// The palette is the shared component's (packages/core CommandPalette); the
// web spec e2e/tests/47-palette-everywhere.spec.ts measures it in a browser.
// This measures the same thing in the app window, plus the two things only
// the app has:
//
//   1. Same query, same answer: the rows the palette draws are exactly the
//      server's `/api/files/search` answer for the signed-in person, in its
//      order — the browser spec checks the same equality, so the two surfaces
//      agree through the server rather than by eye.
//   2. From a hit: download (the bytes land, whole) and drag out (the shell's
//      OS drag receives it — FILEX_TEST_NO_OS_DRAG, see dragout-e2e.mjs for why
//      the hand-over itself stays a human step). A drag let go INSIDE the
//      palette starts no upload.
//   3. Two accounts on the rail (both on ONE server, which is the hard case:
//      one origin, two credentials). One badge per account, own account first;
//      the second person's own group holds only what they may see (NEGATIVE);
//      another account's hit downloads with THAT account's credential, drags
//      out as that account, and opening it switches the rail to it.
//
// ⚠ No OS-level input (harness rule). Throwaway profile and HOME.
//
//   FILEX_SERVER / FILEX_EMAIL / FILEX_PASSWORD — an ADMIN on a server you own
//   (a local instance: `node e2e/run.mjs local --keep --port <n>` prints one).
//   The script creates its own storage, files and second user, and removes the
//   storage at the end.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  SERVER,
  api,
  arrived,
  check,
  completeSignIn,
  finish,
  launchApp,
  signIn,
  skipTour,
  sleep,
} from './lib/harness.mjs';

const RUN = Date.now();
const STORAGE = `desk47-${RUN}`;
const ROOT = path.join(os.tmpdir(), `filex-${STORAGE}`);
// Unique per run: a run that died half way leaves its storage behind, and a
// shared word would put that storage's hits into this run's answer.
const WORD = `kestane${RUN}`;
const SECRET = { dir: 'Hukuk', name: 'sözleşme-47.txt', body: `Ceza şartı: ${WORD} maddesi uyarınca.\n` };
const SHARED = { dir: 'Paylasim', name: 'ortak-47.txt', body: `Ortak not — ${WORD} burada da geçiyor.\n` };
const FOLDER = { dir: 'Arsiv', name: `${WORD}-dosyalar` };
const OUTSIDER = { email: `desk47-${RUN}@example.test`, password: `desk47-outsider-pw-${RUN}`, name: 'Dışarıdaki Okur' };

async function json(res) {
  const t = await res.text();
  try { return JSON.parse(t); } catch { return t; }
}

async function seed(token) {
  fs.mkdirSync(ROOT, { recursive: true });
  const st = await api('/api/admin/storages', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name: STORAGE, driver: 'local', mount_path: ROOT, config: { path: ROOT },
      sync_mode: 'fsnotify', sync_interval_s: 0, enabled: true, read_only: false, rbac_enabled: true,
    }),
  }, token);
  if (!st.ok) throw new Error(`storage: ${st.status} ${await st.text()}`);
  const storageId = (await st.json()).id;
  const mk = async (parent, name) => {
    const r = await api('/api/files/manager?action=newfolder', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: `${STORAGE}://${parent}`, name }),
    }, token);
    if (!r.ok) throw new Error(`newfolder ${parent}/${name}: ${r.status}`);
  };
  for (const d of [SECRET.dir, SHARED.dir, FOLDER.dir]) await mk('', d);
  await mk(FOLDER.dir, FOLDER.name);
  for (const f of [SECRET, SHARED]) {
    const form = new FormData();
    form.append('path', `${STORAGE}://${f.dir}`);
    form.append('file[]', new Blob([f.body], { type: 'text/plain' }), f.name);
    const up = await api('/api/files/manager?action=upload', { method: 'POST', body: form }, token);
    if (!up.ok) throw new Error(`upload ${f.name}: ${up.status}`);
  }
  await api('/api/admin/users', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: OUTSIDER.email, password: OUTSIDER.password, display_name: OUTSIDER.name, role: 'user' }),
  }, token);
  const users = await json(await api('/api/admin/users', {}, token));
  const list = Array.isArray(users) ? users : (users.users ?? users.items ?? []);
  const outsider = list.find((u) => u.email === OUTSIDER.email);
  if (!outsider) throw new Error('the outsider account was not created');
  const g = await api('/api/files/permissions', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: `${STORAGE}://${SHARED.dir}`, user_id: outsider.id, level: 'viewer', is_dir: true }),
  }, token);
  if (!g.ok) throw new Error(`grant: ${g.status} ${await g.text()}`);
  return storageId;
}

/** The server's own answer — the exact call the palette makes. */
async function serverHits(token) {
  const r = await api(`/api/files/search?q=${encodeURIComponent(WORD)}&limit=8&scope=all`, {}, token);
  const b = await json(r);
  return ((b && b.results) || []).filter((h) => h.storage === STORAGE);
}

function expectedRow(h) {
  const rel = String(h.path ?? '').replace(/^\/+|\/+$/g, '');
  const parent = rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/')) : '';
  return `${parent ? `${h.storage}/${parent}` : h.storage}/${h.name}`;
}

/**
 * The first-use tour opens ~1 s AFTER the explorer mounts (once per person,
 * and each account here is a new person), and while it is up it takes every
 * click. `skipTour` right after the mount runs before it exists; wait for it.
 */
async function settleTour(win) {
  const tour = win.locator('.fe-tour');
  const deadline = Date.now() + 4000;
  while (Date.now() < deadline) {
    if (await tour.count()) {
      await skipTour(win).catch(() => {});
      await tour.waitFor({ state: 'detached', timeout: 5000 }).catch(() => {});
      return;
    }
    await sleep(200);
  }
}

async function openPalette(win, expected) {
  await win.keyboard.press('Escape').catch(() => {});
  await win.keyboard.press('Control+k');
  const input = win.locator('.fe-cmdp__input');
  await input.waitFor({ state: 'visible', timeout: 10_000 });
  await input.fill(WORD);
  const rows = win.locator('.fe-cmdp__item--hit');
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline && (await rows.count()) !== expected) await sleep(200);
  return rows;
}

async function drawn(win) {
  return win.locator('.fe-cmdp__item--hit').evaluateAll((els) =>
    els.map((el) => ({
      account: el.getAttribute('data-account') || '',
      row: `${el.querySelector('.fe-cmdp__crumb')?.textContent?.trim() ?? ''}/${el.querySelector('.fe-cmdp__label')?.textContent?.trim() ?? ''}`,
    })),
  );
}

/** Every download the window starts lands in `dir`, recorded, no dialog. */
async function captureDownloads(app, dir) {
  await app.evaluate(({ session }, into) => {
    globalThis.__wt47dl = [];
    session.defaultSession.on('will-download', (_e, item) => {
      const rec = { url: item.getURL(), name: item.getFilename(), state: 'progressing', path: '' };
      // ASCII, native separators: an interrupted download with 0 bytes is
      // what a save path Chromium dislikes looks like.
      rec.path = `${into.dir}${into.sep}${globalThis.__wt47dl.length}.download`;
      item.setSavePath(rec.path);
      item.on('updated', (_ev, state) => { rec.progress = `${state} ${item.getReceivedBytes()}/${item.getTotalBytes()} paused=${item.isPaused()} save=${item.getSavePath()}`; });
      item.once('done', (_ev, state) => { rec.state = state; });
      globalThis.__wt47dl.push(rec);
    });
  }, { dir, sep: path.sep });
}

async function nextDownload(app, before) {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    const all = await app.evaluate(() => globalThis.__wt47dl);
    const d = all[before];
    if (d && d.state !== 'progressing') return d;
    await sleep(200);
  }
  const all = await app.evaluate(() => globalThis.__wt47dl);
  console.log(`      (no finished download #${before}; recorded: ${JSON.stringify(all)})`);
  return null;
}

/** A picture of the window for the report, when FILEX_SHOTS_DIR asks for one. */
async function shot(win, name) {
  const dir = process.env.FILEX_SHOTS_DIR;
  if (!dir) return;
  fs.mkdirSync(dir, { recursive: true });
  await win.screenshot({ path: path.join(dir, `${name}.png`) }).catch(() => {});
}

async function logText(win) {
  const f = await win.evaluate(() => window.filexApp.logPath());
  return f && fs.existsSync(f) ? fs.readFileSync(f, 'utf8') : '';
}

/** A synthetic dragstart on a row, as the renderer sees a real one begin. */
async function dragRow(row) {
  return row.evaluate((el) => {
    const dt = new DataTransfer();
    const ev = new DragEvent('dragstart', { bubbles: true, cancelable: true, dataTransfer: dt });
    el.dispatchEvent(ev);
    return { prevented: ev.defaultPrevented, downloadUrl: dt.getData('DownloadURL') };
  });
}

/** A drag let go on the palette itself — an OS file drop, as the shell's stand-in would arrive. */
async function dropOnPalette(win) {
  await win.locator('.fe-cmdp__backdrop').evaluate((el) => {
    const dt = new DataTransfer();
    dt.items.add(new File(['stand-in'], 'sözleşme-47.txt', { type: 'text/plain' }));
    for (const type of ['dragenter', 'dragover', 'drop']) {
      el.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt }));
    }
  });
}

async function main() {
  const dlDir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-wt47-dl-'));
  const { app } = await launchApp({ env: { FILEX_TEST_NO_OS_DRAG: '1', FILEX_TEST_DRAG_BLOCK_MS: '300' } });
  const pageErrors = [];
  const { win, adminToken } = await signIn(app, { label: 'filex desktop — search e2e' });
  win.on('pageerror', (e) => pageErrors.push(String(e)));
  await skipTour(win).catch(() => {});

  await seed(adminToken);
  // Content is extracted in the background.
  let adminHits = [];
  for (let i = 0; i < 60 && adminHits.length < 3; i++) {
    adminHits = await serverHits(adminToken);
    if (adminHits.length < 3) await sleep(500);
  }
  check('fixtures: the index has the three hits', adminHits.length === 3, JSON.stringify(adminHits.map((h) => h.path)));
  await captureDownloads(app, dlDir);

  // The explorer mounted before the storage existed; a reload picks it up.
  await win.reload();
  await arrived(win, /^app:\/\/filex/);
  await win.locator('[data-fe-path]').first().waitFor({ state: 'visible', timeout: 20_000 });
  await settleTour(win);

  const adminId = await win.evaluate(async () => (await window.filexApp.getState()).activeId);

  // ── 1. same query, same answer ───────────────────────────────────
  const want = adminHits.map(expectedRow);
  await openPalette(win, want.length);
  let got = await drawn(win);
  console.log(`      palette rows (desktop, admin): ${JSON.stringify(got.map((g) => g.row))}`);
  check('the palette draws exactly the server’s answer, in its order', JSON.stringify(got.map((g) => g.row)) === JSON.stringify(want), JSON.stringify({ got: got.map((g) => g.row), want }));
  check('one account on the rail: no account badges', (await win.locator('[data-testid="palette-account-group"]').count()) === 0);
  await shot(win, 'palette-desktop-one-account');

  // ── 2a. download from a hit ──────────────────────────────────────
  const rows = win.locator('.fe-cmdp__item--hit');
  const secretRow = rows.filter({ hasText: SECRET.name });
  let n = (await app.evaluate(() => globalThis.__wt47dl.length));
  await secretRow.hover();
  await secretRow.getByTestId('palette-hit-download').click();
  let d = await nextDownload(app, n);
  check('a file hit downloads from its row, under its own name', d?.state === 'completed' && d.name === SECRET.name, JSON.stringify(d));
  check('…as the file, byte for byte', !!d && fs.existsSync(d.path) && fs.readFileSync(d.path, 'utf8') === SECRET.body, d?.path);
  check('…and the palette stays open (a download is not an open)', await win.locator('.fe-cmdp').isVisible());

  // ── 2b. drag out from a hit ──────────────────────────────────────
  const before = (await logText(win)).length;
  const drag = await dragRow(secretRow);
  check('the hit drag is handed to the shell (the HTML5 drag is cancelled for the OS one)', drag.prevented === true, JSON.stringify(drag));
  let log = '';
  for (let i = 0; i < 30; i++) {
    log = (await logText(win)).slice(before);
    if (log.includes('[drag] stand-ins')) break;
    await sleep(200);
  }
  check('the shell started an OS drag for exactly that file', log.includes('[drag] stand-ins') && log.includes(SECRET.name), log.split(/\r?\n/).filter((l) => l.includes('[drag]')).slice(0, 3).join(' | '));

  // ── 2c. a drop let go on the palette is not an upload ────────────
  const uploads = [];
  const onReq = (r) => { if (/action=upload|\/upload\/init/.test(r.url())) uploads.push(r.url()); };
  win.on('request', onReq);
  await dropOnPalette(win);
  await sleep(1500);
  win.off('request', onReq);
  check('a drop on the palette starts no upload', uploads.length === 0, uploads.join(', '));
  check('…and the palette is still there', await win.locator('.fe-cmdp').isVisible());
  await win.keyboard.press('Escape');

  // ── 3. a second account on the rail — same server, another person ─
  await win.evaluate(() => window.filexApp.addAccount());
  const connect = await arrived(await app.waitForEvent('window', { timeout: 20_000 }), /^app:\/\/shell/);
  const { token: outsiderToken } = await completeSignIn(connect, { email: OUTSIDER.email, password: OUTSIDER.password, label: 'filex desktop — search e2e (2)' });
  let st = null;
  for (let i = 0; i < 50; i++) {
    st = await win.evaluate(async () => window.filexApp.getState());
    if (st.accounts.length === 2 && st.activeId !== adminId) break;
    await sleep(200);
  }
  const outsiderId = st.activeId;
  check('two accounts on the rail, the new one active', st.accounts.length === 2 && outsiderId !== adminId, JSON.stringify({ n: st.accounts.length }));
  await win.locator('[data-fe-path]').first().waitFor({ state: 'visible', timeout: 20_000 });
  await settleTour(win);

  const theirs = await serverHits(outsiderToken);
  check('NEGATIVE (server): the outsider’s search holds only the granted file', JSON.stringify(theirs.map((h) => h.name)) === JSON.stringify([SHARED.name]), JSON.stringify(theirs.map((h) => h.name)));

  await openPalette(win, 1 + adminHits.length);
  got = await drawn(win);
  console.log(`      palette rows (desktop, two accounts): ${JSON.stringify(got)}`);
  await shot(win, 'palette-desktop-two-accounts');
  const heads = await win.locator('[data-testid="palette-account-group"]').evaluateAll((els) => els.map((e) => e.getAttribute('data-account')));
  check('one badge per account, the mounted account first', JSON.stringify(heads) === JSON.stringify([outsiderId, adminId]), JSON.stringify(heads));
  const own = got.filter((g) => g.account === outsiderId).map((g) => g.row);
  const other = got.filter((g) => g.account === adminId).map((g) => g.row);
  check('NEGATIVE (palette): the outsider’s own group is only what they may see', JSON.stringify(own) === JSON.stringify(theirs.map(expectedRow)), JSON.stringify(own));
  check('the other account’s group is that account’s answer, in its order', JSON.stringify(other) === JSON.stringify(want), JSON.stringify(other));
  check('rows are drawn group by group', JSON.stringify(got.map((g) => g.account)) === JSON.stringify([outsiderId, ...want.map(() => adminId)]));

  // ── 3a. another account's hit downloads with THAT account's credential ─
  const foreign = win.locator(`.fe-cmdp__item--hit[data-account="${adminId}"]`).filter({ hasText: SECRET.name });
  const refused = await api(`/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://${SECRET.dir}/${SECRET.name}`)}`, {}, outsiderToken);
  check('control: the outsider’s own credential is refused this file', refused.status >= 400, `HTTP ${refused.status}`);
  n = await app.evaluate(() => globalThis.__wt47dl.length);
  await foreign.hover();
  await foreign.getByTestId('palette-hit-download').click();
  d = await nextDownload(app, n);
  check('another account’s hit downloads, under its own name', d?.state === 'completed' && d.name === SECRET.name, JSON.stringify(d));
  check('…with that account’s credential: the real bytes, not the active account’s refusal', !!d && fs.existsSync(d.path) && fs.readFileSync(d.path, 'utf8') === SECRET.body, d && fs.existsSync(d.path) ? fs.readFileSync(d.path, 'utf8').slice(0, 80) : 'no file');

  // ── 3b. another account's hit drags out as that account ─────────
  // Either route counts (stand-ins, or the copies phase 1 already prepared
  // for this same account); what matters is WHICH account's drag it is.
  const dragLines = async (from, name) => {
    let lines = [];
    for (let i = 0; i < 30; i++) {
      lines = (await logText(win)).slice(from).split(/\r?\n/)
        .filter((l) => /\[drag\] (stand-ins|prepared copies)/.test(l) && l.includes(name));
      if (lines.length) break;
      await sleep(200);
    }
    return lines;
  };
  const before2 = (await logText(win)).length;
  const drag2 = await dragRow(foreign);
  check('another account’s hit drag goes to the shell', drag2.prevented === true && drag2.downloadUrl === '', JSON.stringify(drag2));
  let lines = await dragLines(before2, SECRET.name);
  check('…and the shell drags that file, as that account', lines.length > 0 && lines.every((l) => l.includes(adminId)), lines.join(' | '));
  await dropOnPalette(win);

  // ── 3b'. a copy prepared for one account never rides another's drag ─
  // The shell remembers the last prepared selection by its paths. Two accounts
  // can hold the same path — on one server as here, or on two servers with the
  // same drive name — and a drag of the outsider's own copy must not be handed
  // the administrator's bytes.
  const sharedItem = [{ path: `${STORAGE}://${SHARED.dir}/${SHARED.name}`, basename: SHARED.name, type: 'file' }];
  const prep = await win.evaluate(([acc, it]) => window.filexApp.dragPrepare(acc, it), [adminId, sharedItem]);
  check('fixture: the admin account has a prepared copy of the shared file', prep?.ready === true, JSON.stringify(prep));
  const ownShared = win.locator(`.fe-cmdp__item--hit[data-account="${outsiderId}"]`).filter({ hasText: SHARED.name });
  const before3 = (await logText(win)).length;
  await dragRow(ownShared);
  lines = await dragLines(before3, SHARED.name);
  check('a copy prepared for one account is never handed to another account’s drag', lines.length > 0 && lines.every((l) => l.includes('stand-ins') && l.includes(outsiderId)), lines.join(' | '));
  await dropOnPalette(win);

  // ── 3c. opening another account's hit switches the rail to it ───
  const docWin = app.waitForEvent('window', { timeout: 20_000 }).catch(() => null);
  await foreign.locator('.fe-cmdp__label').click();
  let active = '';
  for (let i = 0; i < 50; i++) {
    active = await win.evaluate(async () => (await window.filexApp.getState()).activeId);
    if (active === adminId) break;
    await sleep(200);
  }
  check('opening another account’s hit switches the rail to that account', active === adminId, active);
  const wire = `${STORAGE}://${SECRET.dir}/${SECRET.name}`;
  const selected = await (async () => {
    for (let i = 0; i < 50; i++) {
      const ok = await win.evaluate((w) => [...document.querySelectorAll('[data-fe-path]')]
        .some((el) => el.getAttribute('data-fe-path') === w && el.getAttribute('aria-selected') === 'true'), wire);
      if (ok) return true;
      await sleep(200);
    }
    return false;
  })();
  check('…lands on the hit’s folder with its row ticked', selected, wire);
  const dw = await docWin;
  if (dw) await arrived(dw, /files\/edit/, 15_000);
  const dwUrl = dw ? dw.url() : '';
  check('…and opens the file in its own window', !!dw && dwUrl.includes('/files/edit') && decodeURIComponent(dwUrl).includes(wire), dwUrl);

  check('no page errors in the window', pageErrors.length === 0, pageErrors.join(' | '));

  await api(`/api/admin/storages?name=${encodeURIComponent(STORAGE)}`, {}, adminToken).catch(() => {});
  const all = await json(await api('/api/admin/storages', {}, adminToken));
  for (const s of Array.isArray(all) ? all : []) {
    if (s.name === STORAGE) await api(`/api/admin/storages/${s.id}`, { method: 'DELETE' }, adminToken);
  }
  await app.close().catch(() => {});
  console.log(`\n(server: ${SERVER}, storage: ${STORAGE}, downloads: ${dlDir})`);
  finish();
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
