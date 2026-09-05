// Folder drag-out, reproduced with EXPLORER'S OWN COPY ENGINE.
//
// The suite simulates a drop with `fs.mkdirSync` / `copyFileSync`, which is
// what a program does — not what the shell does. Explorer copies through
// `IShellFolder::CopyHere` (the Shell.Application COM object), and the
// difference matters for a FOLDER: the shell recreates the directory with its
// own attributes and timestamps, keeps a handle on it while the view refreshes,
// and shows it in a window that may still own it when we try to replace it.
//
// This probe drags a folder the app has NOT prepared (so the placeholder route
// runs), copies the stand-in with CopyHere, and reports exactly what ends up on
// disk plus everything the main process printed on the way.
//
//   FILEX_SERVER=… FILEX_EMAIL=… FILEX_PASSWORD=… FILEX_STORAGE=… \
//     node scripts/dragout-folder-probe.mjs

import { execFile } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { SERVER, STORAGE, api, launchApp, signIn, skipTour, sleep } from './lib/harness.mjs';

// FILEX_PROBE_REMOTE lets the probe drag a folder that ALREADY exists — the
// user's own, on a real server — instead of seeding one. Nothing is written to
// the server in that mode.
const EXISTING = process.env.FILEX_PROBE_REMOTE ?? '';
const RUN = `folderprobe-${Date.now()}`;
const REMOTE = `${STORAGE}://${RUN}`;
const SUB = EXISTING || `${REMOTE}/klasor`;
const DRAG_NAME = EXISTING ? EXISTING.replace(/\/+$/, '').split('/').pop() : 'klasor';

function shellCopy(src, dstDir) {
  // Explorer's own copy: Shell.Application → NameSpace(dst).CopyHere(src).
  // 0x14 = no progress UI + yes-to-all, which is what a drag-drop does.
  const ps = `$s = New-Object -ComObject Shell.Application; ` +
    `$d = $s.NameSpace('${dstDir.replace(/'/g, "''")}'); ` +
    `$d.CopyHere('${src.replace(/'/g, "''")}', 0x14); ` +
    `Start-Sleep -Milliseconds 1500`;
  return new Promise((resolve) => {
    execFile('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', ps], { timeout: 30000 }, (err, so, se) =>
      resolve({ err: err?.message, stderr: String(se || '').trim() }),
    );
  });
}

function tree(dir, depth = 0) {
  const out = [];
  let entries = [];
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch (e) {
    return [`${'  '.repeat(depth)}<okunamadı: ${e.code}>`];
  }
  for (const e of entries) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      out.push(`${'  '.repeat(depth)}${e.name}/`);
      out.push(...tree(p, depth + 1));
    } else {
      out.push(`${'  '.repeat(depth)}${e.name}  ${fs.statSync(p).size} B`);
    }
  }
  return out;
}

const { app, profile } = await launchApp({ env: { FILEX_TEST_NO_OS_DRAG: '1' } });
const mainLog = [];
app.process().stdout?.on('data', (d) => mainLog.push(String(d)));
app.process().stderr?.on('data', (d) => mainLog.push('ERR ' + String(d)));

const { win, adminToken: token } = await signIn(app);
await skipTour(win).catch(() => {});

for (const [parent, name] of EXISTING ? [] : [[`${STORAGE}://`, RUN], [REMOTE, 'klasor']]) {
  const mk = await api('/api/files/manager?action=newfolder', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: parent, name }),
  }, token);
  if (!mk.ok) throw new Error(`seed ${parent}${name}: ${mk.status}`);
}
// Nested folders + an empty folder + a Turkish-named file + a bit of clutter:
// the shape of a real folder.
for (const [parent, name] of EXISTING ? [] : [[SUB, 'alt'], [SUB, 'bos-klasor'], [`${SUB}/alt`, 'daha-derin']]) {
  const mk = await api('/api/files/manager?action=newfolder', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: parent, name }),
  }, token);
  if (!mk.ok) throw new Error(`seed ${parent}/${name}: ${mk.status}`);
}
const seedFiles = [
  [SUB, 'ic.txt', 'klasorun icindeki dosya'],
  [SUB, 'ikinci.txt', 'ikinci dosya'],
  [SUB, 'Türkçe adlı dosya.txt', 'türkçe'],
  [`${SUB}/alt`, 'altdosya.txt', 'alt klasordeki'],
  [`${SUB}/alt/daha-derin`, 'derin.bin', 'x'.repeat(200000)],
];
for (let i = 0; i < 12; i++) seedFiles.push([SUB, `dosya-${i}.txt`, `icerik ${i}`]);
for (const [dir, name, body] of EXISTING ? [] : seedFiles) {
  const form = new FormData();
  form.append('path', `${dir}/`);
  form.append('file[]', new Blob([body], { type: 'text/plain' }), name);
  const up = await api('/api/files/manager?action=upload', { method: 'POST', body: form }, token);
  if (!up.ok) throw new Error(`seed ${name}: ${up.status}`);
}

const accountId = await win.evaluate(async () => (await window.filexApp.getState()).accounts[0].id);
const items = [{ path: SUB, basename: DRAG_NAME, type: 'dir' }];

// ⚠⚠ The drag is started and NOT awaited, and the drop happens while it is
// still in flight. That is the real sequence: on Windows `startDrag` hands
// control to the OS drag loop and does not return until the user lets go, so
// everything the app does "after starting the drag" actually happens after the
// DROP. A probe that awaited first would arm the app's watcher before the copy
// and miss the very bug this exists to catch.
console.log('--- drag:start (klasör, hazırlanmamış) — beklemeden ---');
const modePromise = win.evaluate(([acc, it]) => window.filexApp.dragStart(acc, it), [accountId, items]);

const pendingRoot = path.join(profile, 'drag-cache', 'pending', accountId);
let session = null;
for (let i = 0; i < 60 && !session; i++) {
  await sleep(100);
  if (fs.existsSync(pendingRoot)) {
    const dirs = fs.readdirSync(pendingRoot).map((d) => path.join(pendingRoot, d));
    session = dirs.length ? dirs[dirs.length - 1] : null;
  }
}
console.log('stand-in dizini =', session);
console.log('stand-in içeriği =', session ? fs.readdirSync(session) : '(yok)');

const dropDir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-shelldrop-'));
console.log('bırakma hedefi =', dropDir);

const res = await shellCopy(path.join(session, DRAG_NAME), dropDir);
const mode = await modePromise;
console.log('mode =', mode);
console.log('CopyHere:', JSON.stringify(res));
console.log('kopyadan hemen sonra:', tree(dropDir));

for (let i = 0; i < 30; i++) {
  await sleep(1000);
  const t = tree(dropDir);
  if (t.length > 1) {
    console.log(`${i + 1}. saniyede DOLDU:`);
    console.log(t.join('\n'));
    break;
  }
  if (i === 29) {
    console.log('30 sn sonunda hâlâ:');
    console.log(t.join('\n'));
  }
}

console.log('--- ana süreç çıktısı ---');
console.log(mainLog.join('').split('\n').filter((l) => /drag|drop|ENOENT|EPERM|EBUSY|Error|error/i.test(l)).slice(-25).join('\n') || '(ilgili satır yok)');

await app.close();
