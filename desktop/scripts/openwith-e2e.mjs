// End-to-end measurement of "Open with filex": a document on this machine,
// double-clicked, edited on the server, and written back over the original.
//
// What it drives, for real:
//   • the SECOND-INSTANCE route — a second `filex <path>` process while the app
//     is already running, which is how a double-click reaches a running app on
//     Windows and Linux;
//   • the scratch round trip — upload, editor window, poll, atomic write-back,
//     cleanup of the copy after the window closes;
//   • the SYNCED-TWIN route — a document inside a kept folder opens against its
//     remote twin and creates no copy at all;
//   • #184 — the document changing OUTSIDE filex while it is open: an agent
//     renames new bytes over it, the open editor moves to them (a new working
//     copy, a note on the page), a late save of the old editor lands BESIDE
//     the document, and the next save goes over the agent's version only
//     because filex now stands on it. The question for an editor WITH edits
//     needs ONLYOFFICE's edit events and is measured in the browser tests
//     (web/tests/components/officeOutsideChange.test.ts) and by hand;
//   • #151 — a save in another format: an old .doc whose save comes back as
//     DOCX (beside the working copy, as a 0.51+ server writes it, or over it,
//     as an older one did) lands beside the document as Rapor.docx, the next
//     save of the session goes to the same file, and the copies go with the
//     session; a .csv opens too, saved as CSV over itself, saved as a
//     spreadsheet beside it.
//
// ⚠ OnlyOffice itself is NOT in the loop, and cannot honestly be: the document
// server is a separate ~2 GB service that has to reach the filex instance over
// the network, and a local run has none. Its save is therefore performed the
// way OnlyOffice's own callback performs it — by writing new bytes over the
// scratch copy through the API. Everything on this side of that write is the
// real product code; the gap is one third-party POST.
//
// ⚠ No Playwright MCP and no OS input anywhere. This drives Electron through
// playwright-core directly, so a run never touches the operator's mouse.
//
// Prerequisites: a filex instance to sign in to.
//   FILEX_SERVER=http://127.0.0.1:8899 FILEX_EMAIL=admin@local FILEX_PASSWORD=admin \
//     node desktop/scripts/openwith-e2e.mjs

import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';

import { DESKTOP, REPO, EMAIL, PASSWORD, SERVER, arrived, check, finish, launchApp, signIn, sleep } from './lib/harness.mjs';

const require = createRequire(import.meta.url);
const ELECTRON_BIN = require('electron'); // the package's main export IS the exe path

const STORAGE = 'docs';
// ⚠ Non-ASCII on purpose. The names this feature exists for look like this, and
// every layer it crosses — multipart filename, wire path, URL query, the OS
// command line of the second instance — has its own way of mangling them.
const DOC_NAME = 'Bütçe Özeti.docx';
const ORIGINAL = Buffer.from('ORIGINAL-DOCUMENT-BYTES-' + 'x'.repeat(64));
const EDITED = Buffer.from('EDITED-ON-THE-SERVER-' + 'y'.repeat(200));
// #151: an old Word 97 file as far as anything reading its first bytes can
// tell (an OLE compound file)…
const OLE_DOC = Buffer.concat([Buffer.from([0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1]), Buffer.alloc(504, 0x20)]);

/** …and a zip that begins the way every OOXML package does
 *  (`[Content_Types].xml` first), tagged so two saves differ. */
function ooxmlBytes(tag) {
  const name = Buffer.from('[Content_Types].xml', 'latin1');
  const body = Buffer.from(`<Types>${tag}</Types>`, 'latin1');
  const head = Buffer.alloc(30);
  head.writeUInt32LE(0x04034b50, 0);
  head.writeUInt16LE(20, 4);
  head.writeUInt32LE(body.length, 18);
  head.writeUInt32LE(body.length, 22);
  head.writeUInt16LE(name.length, 26);
  return Buffer.concat([head, name, body, Buffer.from([0x50, 0x4b, 0x01, 0x02])]);
}

let adminToken = null;

async function api(pathname, init = {}) {
  const res = await fetch(`${SERVER}${pathname}`, {
    ...init,
    headers: {
      ...(adminToken ? { Authorization: `Bearer ${adminToken}` } : {}),
      ...(init.headers ?? {}),
    },
  });
  return res;
}

async function listScratch() {
  const res = await api(
    `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://.filex-open`)}`,
  );
  if (!res.ok) return [];
  const body = await res.json();
  return (body.files ?? []).filter((f) => f.type === 'file');
}

/** Writes bytes over a remote path — exactly what the OnlyOffice callback does
 *  when the document server reports a save. */
async function serverSideSave(basename, bytes) {
  const form = new FormData();
  form.append('path', `${STORAGE}://.filex-open`);
  form.append('file[]', new Blob([bytes]), basename);
  const res = await api('/api/files/manager?action=upload', { method: 'POST', body: form });
  if (!res.ok) throw new Error(`server-side save failed: ${res.status} ${await res.text()}`);
}

async function waitUntil(label, fn, timeoutMs = 30_000, stepMs = 400) {
  const until = Date.now() + timeoutMs;
  for (;;) {
    const got = await fn();
    if (got) return got;
    if (Date.now() > until) throw new Error(`timed out waiting for ${label}`);
    await sleep(stepMs);
  }
}

/** The OS's route into a running app: another process, same profile, one path. */
function openViaSecondInstance(profile, home, docPath) {
  const child = spawn(
    ELECTRON_BIN,
    [DESKTOP, `--user-data-dir=${profile}`, '--lang=en-US', docPath],
    {
      cwd: DESKTOP,
      env: { ...process.env, HOME: home, USERPROFILE: home, FILEX_NO_BROWSER: '1', FILEX_NO_UPDATE: '1' },
      stdio: 'ignore',
    },
  );
  return new Promise((resolve) => child.on('exit', () => resolve()));
}

async function main() {
  if (!EMAIL || !PASSWORD) throw new Error('set FILEX_EMAIL and FILEX_PASSWORD');

  // ── a storage to work in ───────────────────────────────────────────
  const login = await fetch(`${SERVER}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD, remember: true }),
  });
  if (!login.ok) throw new Error(`admin login failed (${login.status})`);
  adminToken = (await login.json()).token;

  // ⚠ Reused when it is already there. A second run against a `--keep` server
  // would otherwise die on a UNIQUE constraint before measuring anything, which
  // makes the suite look broken when the server is merely still up.
  const existing = await api('/api/admin/storages').then((r) => (r.ok ? r.json() : []));
  const already = (Array.isArray(existing) ? existing : (existing.items ?? [])).some(
    (s) => s?.name === STORAGE,
  );
  const storageRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-ow-storage-'));
  const made = already ? { ok: true, status: 200, text: async () => '' } : await api('/api/admin/storages', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name: STORAGE,
      driver: 'local',
      mount_path: storageRoot,
      // ⚠ `config.path`, not `config.root` — local.Driver.Init reads `path`
      // first, and a storage seeded the other way silently lands in the
      // server's working directory (e2e/README.md documents the incident).
      config: { path: storageRoot },
      sync_mode: 'fsnotify',
      sync_interval_s: 0,
      enabled: true,
      read_only: false,
    }),
  });
  if (!made.ok && made.status !== 409) {
    const text = await made.text();
    if (!/exists/i.test(text)) throw new Error(`could not seed a storage: ${made.status} ${text}`);
  }

  // ── the app ────────────────────────────────────────────────────────
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-ow-home-'));
  const mirror = path.join(home, 'mirror');
  fs.mkdirSync(mirror, { recursive: true });
  const docsDir = path.join(home, 'Belgeler');
  fs.mkdirSync(docsDir, { recursive: true });

  const { app, profile } = await launchApp({
    env: {
      HOME: home,
      USERPROFILE: home,
      // The real timings are minutes long by design (the grace period exists
      // because OnlyOffice saves ~10s after the last editor disconnects).
      // Shortened here so a run is seconds, not minutes.
      FILEX_OPENWITH_POLL_MS: '600',
      FILEX_OPENWITH_GRACE_MS: '6000',
      FILEX_OPENWITH_QUIET_MS: '1500',
      // The sync engine, for the synced-twin half. The harness deliberately
      // does not set this; this suite needs a pair to exist to measure the
      // route that avoids the scratch copy entirely.
      FILEX_CLI: path.join(REPO, 'bin', process.platform === 'win32' ? 'filex.exe' : 'filex'),
      // The mirror root, so "Keep on this computer" does not open a native
      // folder picker an unattended run cannot answer.
      FILEX_TEST_PICK_DIR: mirror,
    },
  });

  try {
    const { win } = await signIn(app, { label: 'openwith-e2e' });
    const state = await win.evaluate(() => window.filexApp.getState());
    const accountId = state.activeId;
    check('signed in with an account', Boolean(accountId), String(accountId));

    // ── 1. a document outside every synced folder ────────────────────
    const docPath = path.join(docsDir, DOC_NAME);
    fs.writeFileSync(docPath, ORIGINAL);
    check('the document starts as the original bytes',
      fs.readFileSync(docPath).equals(ORIGINAL), `${ORIGINAL.length} bytes`);
    check('nothing is in the scratch folder yet', (await listScratch()).length === 0);

    const editorPromise = app.waitForEvent('window', { timeout: 60_000 });
    await openViaSecondInstance(profile, home, docPath);
    const editor = await arrived(await editorPromise, /\/files\/edit/);
    const editorUrl = editor.url();
    check('a second instance carrying a path opened an editor window',
      editorUrl.includes('/files/edit'), editorUrl);
    check('the editor points at a scratch copy, not at the local file',
      decodeURIComponent(editorUrl).includes(`${STORAGE}://.filex-open/`), decodeURIComponent(editorUrl));

    // ⚠ A window that OPENED is not a window that WORKS. Without this the whole
    // suite would stay green against a page that had bounced to the login form
    // — the app's bearer reaches that origin through the header injector, and
    // if that ever stopped, "the editor opened" is exactly what a run would
    // still report. Measured on the page, not inferred from the URL.
    const editorPage = await editor.evaluate(() => ({
      title: document.title,
      passwordFields: document.querySelectorAll('input[type="password"]').length,
      banner: document.getElementById('filex-openwith-banner')?.textContent ?? '',
      desktopFlag: window.filexDesktop === true,
      text: (document.body.innerText || '').slice(0, 400),
    }));
    check('the editor page is not the sign-in form', editorPage.passwordFields === 0,
      `${editorPage.passwordFields} password fields · ${editorPage.text.replace(/\s+/g, ' ').slice(0, 120)}`);
    // The desktop app advertising the desktop app to itself, above the document
    // the user just double-clicked. The SPA has its own flag for this; the
    // editor window's one-line preload sets it.
    check('the desktop app does not offer to install itself', editorPage.desktopFlag &&
      !/desktop app/i.test(editorPage.text), editorPage.text.replace(/\s+/g, ' ').slice(0, 120));
    check('the banner names the local document that saves land on',
      editorPage.banner.includes(docPath), editorPage.banner || '(no banner)');

    const scratch = await waitUntil('the scratch copy to appear', async () => {
      const files = await listScratch();
      return files.length === 1 ? files[0] : null;
    });
    check('exactly one scratch copy exists', Boolean(scratch), scratch?.basename);
    check('the copy keeps the document\'s Turkish name and extension',
      /^[0-9a-f]{12}-Bütçe Özeti\.docx$/.test(scratch.basename), scratch.basename);

    const uploaded = await api(
      `/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://.filex-open/${scratch.basename}`)}`,
    );
    const uploadedBytes = Buffer.from(await uploaded.arrayBuffer());
    check('the copy on the server is byte-identical to the document',
      uploadedBytes.equals(ORIGINAL), `${uploadedBytes.length} bytes`);

    // Opening the same document again must NOT make a second working copy:
    // two sessions writing back to one path means whichever saves last wins and
    // the other edit disappears without a word.
    const windowsBefore = app.windows().length;
    await openViaSecondInstance(profile, home, docPath);
    await sleep(2000);
    check('opening the same document twice makes no second copy',
      (await listScratch()).length === 1, `${(await listScratch()).length} copies`);
    check('and no second editor window', app.windows().length === windowsBefore,
      `${app.windows().length} windows`);

    // ── 2. the editor saves (what OnlyOffice's callback would do) ─────
    await serverSideSave(scratch.basename, EDITED);
    const changed = await waitUntil('the local document to be written back', async () =>
      fs.readFileSync(docPath).equals(EDITED), 30_000, 300);
    check('the bytes on the ORIGINAL local path changed', changed === true,
      `${fs.statSync(docPath).size} bytes`);
    check('no half-written leftovers next to the document',
      fs.readdirSync(docsDir).length === 1, fs.readdirSync(docsDir).join(', '));

    // ── 2b. the document changes OUTSIDE filex (#184) ─────────────────
    // The way agents write: a temp file renamed over the document.
    const AGENT = Buffer.from('WRITTEN-BY-AN-AGENT-' + 'z'.repeat(150));
    const firstCopy = `${STORAGE}://.filex-open/${scratch.basename}`;
    const agentTmp = path.join(docsDir, '.agent-write.tmp');
    fs.writeFileSync(agentTmp, AGENT);
    fs.renameSync(agentTmp, docPath);
    const moved = await waitUntil('the editor to move to the outside version', async () => {
      const ow = await win.evaluate(() => window.filexApp.openWith());
      const s = (ow.sessions ?? []).find((x) => x.localPath === docPath);
      return s && s.remote !== firstCopy && !s.outside ? s : null;
    }, 30_000, 400).catch(() => null);
    check('the outside version went up as a NEW working copy and the editor took it in',
      Boolean(moved), JSON.stringify(moved));
    const pageNote = await waitUntil('the page to say so', async () => {
      const t = await editor.evaluate(() => document.querySelector('[data-testid="outside-note"]')?.textContent ?? '');
      return t.trim() || null;
    }, 10_000, 300).catch(() => '');
    check('the editor page says the file was updated outside filex', /updated outside filex/i.test(pageNote), pageNote);
    check("the agent's bytes are untouched", fs.readFileSync(docPath).equals(AGENT));
    const pagePath = new URL(editor.url()).searchParams.get('path');
    check('the page address follows the new working copy (a reload opens it)',
      Boolean(moved) && pagePath === moved.remote, String(pagePath));

    // What ONLYOFFICE would post for the OLD session after it was closed.
    await serverSideSave(scratch.basename, Buffer.from('A-LATE-SAVE-OF-THE-OLD-EDITOR'));
    const besideName = await waitUntil('the late save to land beside the document', async () =>
      fs.readdirSync(docsDir).find((n) => n.includes('.filex-conflict-')) ?? null, 20_000, 400).catch(() => null);
    check("a late save of the old editor is kept BESIDE the document, not over the agent's version",
      Boolean(besideName) && fs.readFileSync(docPath).equals(AGENT), String(besideName));

    // A save on the new working copy goes over the agent's version: filex
    // stands on it now.
    const EDITED2 = Buffer.from('EDITED-AFTER-THE-AGENT-' + 'w'.repeat(90));
    if (moved) {
      await serverSideSave(moved.remote.slice(moved.remote.lastIndexOf('/') + 1), EDITED2);
      const landed = await waitUntil('the save on the new copy to be written back', async () =>
        fs.readFileSync(docPath).equals(EDITED2), 30_000, 300).catch(() => false);
      check('the next save lands on the document (filex now stands on the agent\'s version)', landed === true);
    }
    const finalBytes = moved ? EDITED2 : EDITED;
    if (besideName) fs.rmSync(path.join(docsDir, besideName), { force: true });

    // ── 3. closing the window cleans the copy up ─────────────────────
    await editor.close();
    const gone = await waitUntil('the scratch copy to be removed', async () =>
      (await listScratch()).length === 0, 40_000, 500);
    check('the scratch copy is gone after the editor closed', gone === true);
    const records = path.join(profile, 'openwith');
    const leftover = () => (fs.existsSync(records) ? fs.readdirSync(records).filter((f) => f.endsWith('.json')) : []);
    // ⚠ Waited for, not sampled: finishOpenWith() removes the record AFTER the
    // server has deleted the copy, so "the copy is gone" can be observed a
    // moment before the record's own unlink — measured once on Electron 44
    // (d3d91c3cd7cb.json still there at the first look).
    await waitUntil('the session record to be removed', async () => leftover().length === 0, 5_000, 200)
      .catch(() => {});
    const left = leftover();
    check('the session record is gone too', left.length === 0, left.join(', '));
    check('the document still holds the edit', fs.readFileSync(docPath).equals(finalBytes));

    // ── 3b. an old .doc whose save comes back as DOCX (#151) ──────────
    // ONLYOFFICE writes no .doc: a 0.51+ server leaves the working copy as it
    // is and writes the edit beside it, `<session>-Rapor.docx`
    // (callback_format.go saveBeside), then `<session>-Rapor (2).docx` for the
    // next save of the session. The desktop used to watch the copy alone:
    // Rapor.doc never changed and the edit went with the working folder.
    const oldDir = path.join(home, 'Eski Belgeler');
    fs.mkdirSync(oldDir, { recursive: true });
    const oldDoc = path.join(oldDir, 'Rapor.doc');
    fs.writeFileSync(oldDoc, OLE_DOC);
    const oldEditorPromise = app.waitForEvent('window', { timeout: 60_000 });
    await openViaSecondInstance(profile, home, oldDoc);
    const oldEditor = await arrived(await oldEditorPromise, /\/files\/edit/);
    const oldCopy = await waitUntil('the .doc working copy to appear', async () =>
      (await listScratch()).find((f) => /^[0-9a-f]{12}-Rapor\.doc$/.test(f.basename)) ?? null);
    const oldStem = oldCopy.basename.slice(0, -'.doc'.length);
    const besideDoc = path.join(oldDir, 'Rapor.docx');

    const DOCX_1 = ooxmlBytes('FIRST-SAVE');
    await serverSideSave(`${oldStem}.docx`, DOCX_1);
    const landedBeside = await waitUntil('the save to land beside the .doc', async () =>
      fs.existsSync(besideDoc) && fs.readFileSync(besideDoc).equals(DOCX_1), 30_000, 300).catch(() => false);
    check('a .doc saved as DOCX lands BESIDE it as Rapor.docx', landedBeside === true,
      fs.readdirSync(oldDir).join(', '));
    check('Rapor.doc itself is untouched', fs.readFileSync(oldDoc).equals(OLE_DOC));
    const oldSession = await win.evaluate(() => window.filexApp.openWith())
      .then((ow) => (ow.sessions ?? []).find((s) => s.localPath === oldDoc));
    check('the session says where its saves go now', oldSession?.beside === besideDoc, JSON.stringify(oldSession));
    const oldBanner = await waitUntil('the strip to name the new file', async () => {
      const t = await oldEditor.evaluate(() => document.getElementById('filex-openwith-banner')?.textContent ?? '');
      return t.includes(besideDoc) ? t : null;
    }, 10_000, 300).catch(() => '');
    check('the strip under the editor names Rapor.docx', Boolean(oldBanner), String(oldBanner));

    // The next save of the session: the server finds its first name taken.
    const DOCX_2 = ooxmlBytes('SECOND-SAVE');
    await serverSideSave(`${oldStem} (2).docx`, DOCX_2);
    const sameFile = await waitUntil('the next save to reach Rapor.docx', async () =>
      fs.readFileSync(besideDoc).equals(DOCX_2), 30_000, 300).catch(() => false);
    check('the next save goes to the SAME Rapor.docx', sameFile === true);
    check('and makes no new file on this computer',
      JSON.stringify(fs.readdirSync(oldDir).sort()) === JSON.stringify(['Rapor.doc', 'Rapor.docx']),
      fs.readdirSync(oldDir).join(', '));

    // A server before 0.51 wrote ONLYOFFICE's DOCX over the working copy
    // itself: the bytes say what it is, and it goes the same way.
    const DOCX_3 = ooxmlBytes('THIRD-SAVE-OVER-THE-COPY');
    const overCopy = await serverSideSave(oldCopy.basename, DOCX_3).then(() => '', (e) => String(e?.message ?? e));
    const sniffed = !overCopy && await waitUntil('the DOCX written over the copy to reach Rapor.docx', async () =>
      fs.readFileSync(besideDoc).equals(DOCX_3), 30_000, 300).catch(() => false);
    check('a DOCX written over the .doc working copy goes to Rapor.docx, never over Rapor.doc',
      sniffed === true && fs.readFileSync(oldDoc).equals(OLE_DOC),
      overCopy || `Rapor.doc starts ${fs.readFileSync(oldDoc).subarray(0, 4).toString('hex')}`);

    await oldEditor.close();
    const oldGone = await waitUntil('the session\'s copies to be removed', async () =>
      (await listScratch()).length === 0, 40_000, 500).catch(() => false);
    check('the working copy and the saves beside it are removed with the session', oldGone === true,
      (await listScratch()).map((f) => f.basename).join(', '));
    check('Rapor.docx keeps the last save',
      fs.existsSync(besideDoc) && fs.readFileSync(besideDoc).equals(sniffed === true ? DOCX_3 : DOCX_2));

    // ── 3c. a .csv (#151, the maintainer's addition) ──────────────────
    // Saved as CSV, the server writes it over the working copy in the file's
    // own dialect (KeepCSV): it goes over the .csv. Saved as anything else,
    // beside it.
    const csvDoc = path.join(oldDir, 'Tablo.csv');
    const CSV_0 = Buffer.from('ad;adet\nelma;3\n');
    fs.writeFileSync(csvDoc, CSV_0);
    // ⚠ Waited for with a catch: an app that refuses the type opens no
    // window, and that is a failed check here, not the end of the suite.
    const csvEditorPromise = app.waitForEvent('window', { timeout: 30_000 }).catch(() => null);
    await openViaSecondInstance(profile, home, csvDoc);
    const csvWindow = await csvEditorPromise;
    const csvEditor = csvWindow ? await arrived(csvWindow, /\/files\/edit/) : null;
    const csvCopy = csvEditor && await waitUntil('the .csv working copy to appear', async () =>
      (await listScratch()).find((f) => /^[0-9a-f]{12}-Tablo\.csv$/.test(f.basename)) ?? null, 30_000, 400).catch(() => null);
    check('a .csv opens with filex: a working copy and an editor window', Boolean(csvEditor && csvCopy),
      (await listScratch()).map((f) => f.basename).join(', ') || '(no working copy, no window)');
    if (csvCopy) {
      const CSV_1 = Buffer.from('ad;adet\nelma;4\n');
      await serverSideSave(csvCopy.basename, CSV_1);
      const csvOver = await waitUntil('the CSV save to be written back', async () =>
        fs.readFileSync(csvDoc).equals(CSV_1), 30_000, 300).catch(() => false);
      check('a save as CSV is written over the .csv itself', csvOver === true, fs.readFileSync(csvDoc, 'utf8'));

      const csvStem = csvCopy.basename.slice(0, -'.csv'.length);
      const XLSX = ooxmlBytes('SHEET');
      await serverSideSave(`${csvStem}.xlsx`, XLSX);
      const besideCsv = path.join(oldDir, 'Tablo.xlsx');
      const xlsxBeside = await waitUntil('the spreadsheet save to land beside the .csv', async () =>
        fs.existsSync(besideCsv) && fs.readFileSync(besideCsv).equals(XLSX), 30_000, 300).catch(() => false);
      check('a save in another format lands beside the .csv as Tablo.xlsx', xlsxBeside === true,
        fs.readdirSync(oldDir).join(', '));
      check('and the .csv keeps its CSV', fs.readFileSync(csvDoc).equals(CSV_1));
    }
    if (csvEditor) await csvEditor.close();
    await waitUntil('the .csv session to be cleaned up', async () => (await listScratch()).length === 0, 40_000, 500)
      .catch(() => {});

    // ── 4. what Settings tells the user ──────────────────────────────
    // ⚠ Read off the rendered panel, not off the IPC answer. A card that is
    // computed correctly and never drawn is a feature nobody can find, and this
    // repo has been caught calling a UI "done" on the strength of a green
    // non-UI test before.
    const ow = await win.evaluate(() => window.filexApp.openWith());
    check('the app reports exactly the eleven types it handles (the ten office ones and .csv)',
      ow.extensions.length === 11 && ow.extensions.includes('docx') && ow.extensions.includes('csv') &&
        !ow.extensions.includes('pdf'),
      ow.extensions.join(' '));
    check('a run from source says it is NOT registered with the system',
      ow.registered === false, String(ow.registered));
    const expectedRoute = process.platform === 'win32' ? 'settings'
      : process.platform === 'linux' ? 'xdg' : 'manual';
    check('the "make it the default" route matches what this OS actually allows',
      ow.defaultRoute === expectedRoute, `${ow.defaultRoute} on ${process.platform}`);

    await win.evaluate(() => {
      const gear = [...document.querySelectorAll('#rail .rail-btn')].pop();
      gear?.click();
    });
    // ⚠ Case-insensitive: the panel's section headings are drawn through
    // `text-transform: uppercase`, and innerText returns what is RENDERED. A
    // case-sensitive match here reported "the card is missing" against a card
    // that was on screen — measured while writing this suite.
    const panel = await waitUntil('the settings panel to draw the card', async () => {
      const text = await win.evaluate(() => document.querySelector('#settings')?.innerText ?? '');
      return /open documents with filex/i.test(text) ? text : null;
    }, 10_000, 300);
    check('Settings draws the card, with the type list and the honest default-app wording',
      /\.docx/i.test(panel) && /choose the default app yourself|make filex the default|finder/i.test(panel),
      panel.split(/open documents with filex/i)[1]?.replace(/\s+/g, ' ').slice(0, 200));
    await win.evaluate(() => document.querySelector('#close-settings')?.click());

    // ── 5. a document INSIDE a synced folder takes the other route ───
    const mk = await api('/api/files/manager?action=newfolder', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: `${STORAGE}://`, name: 'Reports' }),
    });
    check('a folder to sync exists on the server', mk.ok || mk.status === 409, String(mk.status));

    let kept = null;
    try {
      kept = await win.evaluate(
        ([id]) => window.filexApp.syncKeep(id, 'docs://Reports'),
        [accountId],
      );
    } catch (err) {
      kept = { error: String(err?.message ?? err) };
    }
    const pairs = (kept?.syncFolders ?? []).filter((p) => p.remotePath === 'docs://Reports');
    if (!check('the folder is kept on this computer', pairs.length === 1, JSON.stringify(kept?.error ?? pairs))) {
      console.log('  (the synced-twin half needs a pair; skipping it)');
    } else {
      const twinDoc = path.join(pairs[0].localPath, 'Rapor.docx');
      fs.mkdirSync(path.dirname(twinDoc), { recursive: true });
      fs.writeFileSync(twinDoc, ORIGINAL);
      const twinPromise = app.waitForEvent('window', { timeout: 60_000 });
      await openViaSecondInstance(profile, home, twinDoc);
      const twinWin = await arrived(await twinPromise, /\/files\/edit/);
      const twinUrl = decodeURIComponent(twinWin.url());
      check('a document in a synced folder opens against its remote twin',
        twinUrl.includes('path=docs://Reports/Rapor.docx'), twinUrl);
      check('and no scratch copy was made for it', (await listScratch()).length === 0);
      await twinWin.close();
    }
  } finally {
    await app.close().catch(() => {});
  }
}

main().then(finish, (err) => {
  console.error(err);
  process.exit(1);
});
