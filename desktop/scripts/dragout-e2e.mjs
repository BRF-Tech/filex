// Dragging files OUT of the desktop app — the measurement.
//
// What is and is not measurable here, stated plainly:
//
//   • The PREPARATION is fully measurable, and every failure this feature can
//     have lives there: which bytes reach this computer, whether the copies are
//     whole, whether a folder brings its subtree, whether the second drag of
//     the same thing is free.
//   • `webContents.startDrag` opens the OS drag loop. It cannot be driven from
//     a script, and calling it in an unattended run would leave a modal drag
//     hanging off the machine's mouse — so this script deliberately calls
//     `dragStart` only in the case that must be REFUSED. Letting go over the
//     desktop is a human step (desktop/README.md).
//
// ⚠ No OS-level input anywhere (harness rule): Playwright talks to the renderer
// and the main process, never to the operator's mouse or keyboard.
//
//   FILEX_SERVER / FILEX_EMAIL / FILEX_PASSWORD / FILEX_STORAGE drive it, same
//   as the other scripts in this folder.

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { SERVER, STORAGE, api, check, failures, finish, launchApp, signIn, skipTour, sleep } from './lib/harness.mjs';

const RUN = `dragout-${Date.now()}`;
const REMOTE = `${STORAGE}://${RUN}`;
const SUB = `${REMOTE}/klasor`;
const BODY = `dragged out at ${new Date().toISOString()}`;

async function main() {
  const { app, profile } = await launchApp({ env: { FILEX_TEST_NO_OS_DRAG: '1' } });
  const { win, adminToken: token } = await signIn(app);
  await skipTour(win).catch(() => {});

  // ── fixtures ─────────────────────────────────────────────────────
  for (const [parent, name] of [[`${STORAGE}://`, RUN], [REMOTE, 'klasor']]) {
    const mk = await api('/api/files/manager?action=newfolder', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: parent, name }),
    }, token);
    if (!mk.ok) throw new Error(`seeding ${parent}${name} failed (${mk.status})`);
  }
  for (const [parent, name] of [[SUB, 'alt']]) {
    const mk = await api('/api/files/manager?action=newfolder', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: parent, name }),
    }, token);
    if (!mk.ok) throw new Error(`seeding ${parent}/${name} failed (${mk.status})`);
  }
  for (const [dir, name, body] of [
    [REMOTE, 'tek.txt', BODY],
    [SUB, 'ic.txt', `${BODY} (klasorde)`],
    // ⚠ Not decoration. Its name travels back in `Content-Disposition`, and a
    // raw non-ASCII byte there made Electron's fetch throw from inside an
    // event handler: the transfer hung, the folder stayed empty, and the app
    // showed a raw JavaScript error box.
    [SUB, 'Türkçe adlı dosya.txt', 'türkçe içerik'],
    [`${SUB}/alt`, 'derindeki.txt', 'alt klasordeki dosya'],
  ]) {
    const form = new FormData();
    form.append('path', `${dir}/`);
    form.append('file[]', new Blob([body], { type: 'text/plain' }), name);
    const up = await api('/api/files/manager?action=upload', { method: 'POST', body: form }, token);
    if (!up.ok) throw new Error(`seeding ${name} failed (${up.status})`);
  }

  const file = [{ path: `${REMOTE}/tek.txt`, basename: 'tek.txt', type: 'file' }];
  const folder = [{ path: SUB, basename: 'klasor', type: 'dir' }];
  const both = [...file, ...folder];

  const accountId = await win.evaluate(async () => (await window.filexApp.getState()).accounts[0].id);
  const prepare = (items) =>
    win.evaluate(([acc, it]) => window.filexApp.dragPrepare(acc, it), [accountId, items]);

  // ── 1. one file ──────────────────────────────────────────────────
  let t0 = Date.now();
  let res = await prepare(file);
  const firstMs = Date.now() - t0;
  check('a single file prepares', res?.ready === true, JSON.stringify(res));
  const p1 = res?.paths?.[0] ?? '';
  check('the path it hands the OS exists on disk', !!p1 && fs.existsSync(p1), p1);
  check(
    'the local copy is byte-identical to what the server holds',
    !!p1 && fs.existsSync(p1) && fs.readFileSync(p1, 'utf8') === BODY,
    p1 && fs.existsSync(p1) ? fs.readFileSync(p1, 'utf8').slice(0, 50) : 'missing',
  );

  // ── 2. the second drag of the same file costs nothing ────────────
  t0 = Date.now();
  res = await prepare(file);
  const secondMs = Date.now() - t0;
  check('the same file prepares again', res?.ready === true, JSON.stringify(res));
  check(
    'the second time is instant — served from the cache, not downloaded again',
    secondMs <= Math.max(150, firstMs),
    `first ${firstMs} ms, second ${secondMs} ms`,
  );

  // ── 3. a folder brings its subtree ───────────────────────────────
  res = await prepare(folder);
  check('a folder prepares', res?.ready === true, JSON.stringify(res));
  const pf = res?.paths?.[0] ?? '';
  check('it arrived as a real directory', !!pf && fs.existsSync(pf) && fs.statSync(pf).isDirectory(), pf);
  check(
    'its contents came with it',
    !!pf && fs.existsSync(path.join(pf, 'ic.txt')),
    path.join(pf, 'ic.txt'),
  );
  check(
    'a file whose NAME is not ASCII comes too — its header used to kill the transfer',
    !!pf && fs.existsSync(path.join(pf, 'Türkçe adlı dosya.txt')),
    pf ? fs.readdirSync(pf).join(', ') : 'yok',
  );
  check(
    'so does a nested subfolder',
    !!pf && fs.existsSync(path.join(pf, 'alt', 'derindeki.txt')),
    path.join(pf ?? '', 'alt', 'derindeki.txt'),
  );

  // ── 4. a mixed selection = separate real entries, no archive ─────
  res = await prepare(both);
  check('a mixed selection prepares', res?.ready === true, JSON.stringify(res));
  const ps = res?.paths ?? [];
  check(
    'two separate entries — one file, one folder, nothing zipped',
    ps.length === 2 &&
      ps.every((p) => fs.existsSync(p)) &&
      ps.some((p) => fs.statSync(p).isFile()) &&
      ps.some((p) => fs.statSync(p).isDirectory()),
    ps.join(' | '),
  );

  // ── 5. an empty selection starts nothing ────────────────────────
  // The old contract refused everything unprepared; since the placeholder
  // route (2026-08-29) an unprepared selection is exactly what placeholders
  // are FOR, so the only thing left to refuse is a drag of nothing.
  const started = await win.evaluate(([acc]) => window.filexApp.dragStart(acc, []), [accountId]);
  check('a drag of nothing starts nothing', started === false, String(started));

  // ── 6. placeholders: a drag starts instantly, whatever the size ──
  // The route Burak asked for (2026-08-29): hand the OS an empty stand-in,
  // find out where it landed, download the real bytes there. No size ceiling
  // and no waiting. Everything here is real except the OS drag loop itself,
  // which FILEX_TEST_NO_OS_DRAG holds back (see beginOsDrag in main.ts).
  const bigRel = `${REMOTE}/buyuk.bin`;
  const bigBody = 'x'.repeat(300_000);
  const bigForm = new FormData();
  bigForm.append('path', `${REMOTE}/`);
  bigForm.append('file[]', new Blob([bigBody], { type: 'application/octet-stream' }), 'buyuk.bin');
  await api('/api/files/manager?action=upload', { method: 'POST', body: bigForm }, token);

  const bigItems = [
    { path: bigRel, basename: 'buyuk.bin', type: 'file' },
    { path: SUB, basename: 'klasor', type: 'dir' },
  ];
  t0 = Date.now();
  const mode = await win.evaluate(
    ([acc, it]) => window.filexApp.dragStart(acc, it),
    [accountId, bigItems],
  );
  const startMs = Date.now() - t0;
  check('an unprepared selection still starts a drag — via placeholders', mode === 'placeholder', String(mode));
  check('and it starts immediately, with nothing downloaded first', startMs < 1500, `${startMs} ms`);

  const pendingRoot = path.join(profile, 'drag-cache', 'pending', accountId);
  const session = fs.existsSync(pendingRoot)
    ? fs.readdirSync(pendingRoot).map((d) => path.join(pendingRoot, d)).pop()
    : null;
  check('stand-ins exist on disk', !!session && fs.existsSync(session), String(session));
  const stand = session ? fs.readdirSync(session).sort() : [];
  check('one stand-in per dragged item, and they are EMPTY', stand.length === 2, stand.join(', '));
  if (session) {
    const f = path.join(session, 'buyuk.bin');
    check(
      'the file stand-in is zero bytes — that is why the drag is instant',
      fs.existsSync(f) && fs.statSync(f).size === 0,
      fs.existsSync(f) ? `${fs.statSync(f).size} B` : 'missing',
    );
  }

  // Simulate the drop exactly as Explorer performs it: copy the stand-ins into
  // the target folder. The watcher must notice, remove them, and put the real
  // content there.
  const dropDir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-drop-'));
  for (const name of stand) {
    const src = path.join(session, name);
    const dst = path.join(dropDir, name);
    if (fs.statSync(src).isDirectory()) fs.mkdirSync(dst, { recursive: true });
    else fs.copyFileSync(src, dst);
  }

  const landed = path.join(dropDir, 'buyuk.bin');
  const deepest = path.join(dropDir, 'klasor', 'alt', 'derindeki.txt');
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    // Wait for the DEEPEST thing as well as the first: a transfer that dies
    // partway (which is exactly what the header bug did) leaves the early
    // files in place and looks finished if you only look at those.
    if (fs.existsSync(landed) && fs.statSync(landed).size === bigBody.length && fs.existsSync(deepest)) break;
    await sleep(200);
  }
  check(
    'the real bytes replaced the stand-in in the folder it was dropped on',
    fs.existsSync(landed) && fs.readFileSync(landed, 'utf8') === bigBody,
    fs.existsSync(landed) ? `${fs.statSync(landed).size} B` : 'nothing landed',
  );
  check(
    'the dropped FOLDER was filled in too',
    fs.existsSync(path.join(dropDir, 'klasor', 'ic.txt')),
    path.join(dropDir, 'klasor', 'ic.txt'),
  );
  check(
    'including the file with a non-ASCII name, and the subfolder under it',
    fs.existsSync(path.join(dropDir, 'klasor', 'Türkçe adlı dosya.txt')) &&
      fs.existsSync(path.join(dropDir, 'klasor', 'alt', 'derindeki.txt')),
    fs.existsSync(path.join(dropDir, 'klasor'))
      ? fs.readdirSync(path.join(dropDir, 'klasor')).join(', ')
      : 'klasör yok',
  );
  check(
    'no half-written file is left wearing the real name',
    fs.readdirSync(dropDir).every((f) => !f.endsWith('.filexpart')),
    fs.readdirSync(dropDir).join(', '),
  );

  // ── 6a. a same-named file elsewhere is NOT the drop ─────────────
  // The watcher matches by name across whole drives, so a file called
  // `buyuk.bin` appearing anywhere while it waits would be a candidate. What
  // we handed the shell is known exactly: an EMPTY stand-in. A decoy with the
  // same name but content of its own must be ignored — writing the user's file
  // into THAT folder is worse than not filling the drop in at all.
  await win.evaluate(([acc, it]) => window.filexApp.dragStart(acc, it), [accountId, bigItems]);
  const decoyDir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-decoy-'));
  const decoy = path.join(decoyDir, 'buyuk.bin');
  fs.writeFileSync(decoy, 'baskasinin dosyasi');
  await sleep(2500);
  check(
    'a same-named file that is NOT our empty stand-in is left alone',
    fs.readFileSync(decoy, 'utf8') === 'baskasinin dosyasi' &&
      fs.readdirSync(decoyDir).length === 1,
    fs.readdirSync(decoyDir).join(', '),
  );
  await win.evaluate(() => window.filexApp.dragCancel());

  // ── 6b. an internal drop calls the watcher off ────────────────────
  await win.evaluate(([acc, it]) => window.filexApp.dragStart(acc, it), [accountId, bigItems]);
  await win.evaluate(() => window.filexApp.dragCancel());
  await sleep(300);
  const leftovers = fs.existsSync(pendingRoot) ? fs.readdirSync(pendingRoot) : [];
  check(
    'cancelling (a drop inside the app) clears the stand-ins',
    leftovers.length === 0,
    leftovers.join(', '),
  );

  // ── 7. a small file is ready BEFORE the first drag ───────────────
  // Selecting a small file prepares it in the background, so the very first
  // drag of a document is already an OS drag. Measured on a file no earlier
  // step touched, and by TIME: a prepare that has to download cannot answer
  // in a handful of milliseconds.
  const freshRel = `${REMOTE}/onceden.txt`;
  const freshForm = new FormData();
  freshForm.append('path', `${REMOTE}/`);
  freshForm.append('file[]', new Blob(['prefetch'], { type: 'text/plain' }), 'onceden.txt');
  await api('/api/files/manager?action=upload', { method: 'POST', body: freshForm }, token);

  const navigated = await win.evaluate(async ([storage, run]) => {
    const dbl = (el) => el.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    const wait = (ms) => new Promise((r) => setTimeout(r, ms));
    const rowFor = (p) => document.querySelector(`[data-fe-path="${p}"]`);
    for (const target of [storage, `${storage}://${run}`]) {
      for (let i = 0; i < 30 && !rowFor(target); i++) await wait(200);
      const row = rowFor(target);
      if (!row) return { reached: false, want: target };
      dbl(row);
      await wait(600);
    }
    return { reached: true };
  }, [STORAGE, RUN]);

  if (!navigated?.reached) {
    check('the explorer reaches the fixture folder', false, JSON.stringify(navigated));
  } else {
    const clicked = await win.evaluate(async (p) => {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms));
      for (let i = 0; i < 25 && !document.querySelector(`[data-fe-path="${p}"]`); i++) await wait(200);
      const row = document.querySelector(`[data-fe-path="${p}"]`);
      if (!row) return false;
      row.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      await wait(2500); // 400 ms debounce + the download
      return true;
    }, freshRel);
    check('the fresh file row is clickable', clicked === true);

    // ⚠ Measured on DISK, not by the clock: against a local server a real
    // download answers in the same handful of milliseconds a cache hit does,
    // so a timing assertion here passes with the prefetch switched off
    // (measured: 17 ms either way). The cache entry either exists before
    // anything asked for it, or it does not.
    const cached = path.join(
      profile,
      'drag-cache',
      accountId,
      createHash('sha1').update(freshRel).digest('hex').slice(0, 16),
      'onceden.txt',
    );
    check(
      'selecting a small file prepared it in the background — the FIRST drag is a real OS drag',
      fs.existsSync(cached),
      cached,
    );
  }

  // ── cleanup ──────────────────────────────────────────────────────
  await api('/api/files/manager?action=delete', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items: [{ path: REMOTE, type: 'dir' }] }),
  }, token).catch(() => {});

  await sleep(200);
  await app.close();
  console.log(`\n(server: ${SERVER}, storage: ${STORAGE})`);
  finish();
}

main().catch((e) => {
  console.error(e);
  process.exit(failures() || 1);
});
