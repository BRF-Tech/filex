// Drives the FILE surface of the desktop app against a real server: opening
// documents, previewing media, downloading, renaming, searching, starring,
// deleting — the things the window exists for.
//
// Why it exists: on 2026-08-10 opening any office document in the app showed
// "Config fetch 401", starred files and recently-opened were quietly empty, and
// "Open in new tab" did nothing at all. None of that could be caught by the
// suites that were green at the time, because none of them opened a file.
//
// The two failures underneath were both about credentials the page cannot pass:
//   • the explorer hands viewers an auth-header FUNCTION, and a token that has
//     to be awaited (the desktop fetches it per call) was dropped on the floor
//     by every caller that did not await it → anonymous request → 401;
//   • <img>/<video>/<audio> and the download link carry no headers at all, so
//     the app injects the account's bearer for its own origin.
// Hence the blanket assertion below: NOTHING the window asks its server for may
// come back 401.
//
// ⚠ Since v0.42.0 a file opens in a WINDOW OF ITS OWN (the server's
// /files/edit page, src/main.ts openViewerWindow), not in a preview over the
// list; the checks follow it there (openFile). Rewritten 2026-09-27: until
// then ten checks were red on every run, still looking for the old modal.
//
// Run: node scripts/files-e2e.mjs
// Env: FILEX_SERVER, FILEX_EMAIL, FILEX_PASSWORD, FILEX_STORAGE

import fs from 'node:fs';
import path from 'node:path';
import { SHOTS, SERVER, STORAGE, api, arrived, check, finish, launchApp, signIn, skipTour, tickRow } from './lib/harness.mjs';

fs.mkdirSync(SHOTS, { recursive: true });

// Everything happens inside one scratch folder that this run creates and
// removes. A file test that writes into whatever folder it lands in is a file
// test nobody dares run against their own server.
const DIR = `files-e2e-${Date.now()}`;
const REMOTE = `${STORAGE}://${DIR}`;
let token = null;

/** A 1×1 PNG — small enough to inline, real enough for <img> to decode. */
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64',
);

async function upload(name, body, type) {
  const form = new FormData();
  form.append('path', `${REMOTE}/`);
  form.append('file[]', new Blob([body], { type }), name);
  const res = await api('/api/files/manager?action=upload', { method: 'POST', body: form }, token);
  if (!res.ok) throw new Error(`seeding ${name} failed (${res.status})`);
}

// The checkbox is the click that selects (issue #26); a click on the row opens it.
//
// ⚠ A tick TOGGLES. Opening a file selects its row on the way (the double
// click's first click), and whether that selection outlives the document
// window depends on the server's timing — against a fresh server the row was
// still selected, the tick below turned it OFF, and "download" found no
// selection bar at all. So: clear what is selected, tick this one row, and
// say how many are selected (the bar's own count).
//
// ⚠ And a tick can land on a row that is being replaced: against a server
// whose storage is still on its first sync the listing re-renders under the
// pointer, and one run in three the tick hit a row that was gone a moment
// later (nothing selected, no bar). The tick is repeated until the bar says
// ONE item is selected — what is measured afterwards is unchanged.
async function selectOnly(win, name) {
  let count = '';
  for (let attempt = 0; attempt < 3; attempt++) {
    await clearSelection(win);
    await tickRow(win, name);
    count = await win.evaluate(() => document.querySelector('[data-testid="selection-count"]')?.textContent?.trim() ?? '');
    const one = await win.evaluate((n) => {
      const sel = [...document.querySelectorAll('[data-fe-path][aria-selected="true"]')];
      return sel.length === 1 && (sel[0].getAttribute('data-fe-path') ?? '').endsWith(`/${n}`);
    }, name);
    if (one) return count;
    await win.waitForTimeout(700);
  }
  return count;
}

async function clearSelection(win) {
  const clear = win.locator('[data-testid="selection-bar"] [aria-label="Seçimi temizle"], [data-testid="selection-bar"] [aria-label="Clear selection"]').first();
  if (await clear.isVisible().catch(() => false)) {
    await clear.click().catch(() => {});
    await win.waitForTimeout(300);
  }
}

async function openRow(win, name) {
  return win.evaluate((n) => {
    const row = [...document.querySelectorAll('.fe-list__row')].find((r) => r.textContent?.includes(n));
    row?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    return !!row;
  }, name);
}

/**
 * Opens a FILE and returns the window it opens in.
 *
 * ⚠⚠ Since v0.42.0 every file opens in a window of its own (`openInHost`: the
 * explorer emits `file-opened`, the main process opens the server's
 * `/files/edit` page in a frameless document window — src/main.ts
 * openViewerWindow). This suite still looked for an in-page preview modal on
 * the main window, and ten of its checks went red on every run since — a
 * suite that fails for a reason nobody reads anymore protects nothing.
 */
async function openFile(app, win, name) {
  // ⚠ Twice at most, for the same reason as selectOnly: a double click on a
  // row the listing replaced a moment later opens nothing. The window that
  // does open is still checked for being this server's editor on this file.
  for (let attempt = 0; attempt < 2; attempt++) {
    const next = app.waitForEvent('window', { timeout: attempt === 0 ? 12_000 : 20_000 }).catch(() => null);
    await openRow(win, name);
    const doc = await next;
    if (doc) return arrived(doc, /\/files\/edit\?/);
  }
  return null;
}

async function closeDoc(doc) {
  await doc?.close().catch(() => {});
}

// ⚠ Turkish on purpose. The window follows the OS language now, and the bug
// that started this was reported on a Turkish desktop — the other suites pin
// en-US, so without this nobody ever renders the other half of the strings.
const { app } = await launchApp({ lang: 'tr' });

// What the MAIN process is asked to do with a URL, and what it downloads.
await app.evaluate(({ shell, session }) => {
  globalThis.__external = [];
  shell.openExternal = (u) => { globalThis.__external.push(u); return Promise.resolve(); };
  globalThis.__downloads = [];
  session.defaultSession.on('will-download', (e, item) => {
    globalThis.__downloads.push(item.getFilename());
    item.cancel();
  });
});

try {
  const { win, adminToken } = await signIn(app, { label: 'filex desktop — files e2e' });
  token = adminToken;

  // ── every request the window makes, watched from here on ──────────
  const denied = [];
  win.on('response', (r) => {
    if (r.url().startsWith(SERVER) && (r.status() === 401 || r.status() === 403)) {
      denied.push(`${r.status()} ${r.request().method()} ${new URL(r.url()).pathname}`);
    }
  });

  // ── fixtures ──────────────────────────────────────────────────────
  const mk = await api('/api/files/manager?action=newfolder', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: `${STORAGE}://`, name: DIR }),
  }, token);
  if (!mk.ok) throw new Error(`could not create the scratch folder (${mk.status})`);
  await upload('notes.md', '# Başlık\n\nfilex desktop e2e\n', 'text/markdown');
  await upload('data.csv', 'a,b\n1,2\n', 'text/csv');
  await upload('pixel.png', PNG, 'image/png');
  await upload('rapor.txt', 'düz metin\n', 'text/plain');

  await win.waitForTimeout(4500);
  await skipTour(win);

  // ── the boot surface ──────────────────────────────────────────────
  // It is gone by now, but its shape is worth pinning: it used to be a dashed
  // box in the top-left corner of an empty white window.
  const bootShape = await win.evaluate(() => {
    const b = document.querySelector('#boot');
    if (!b) return null;
    const cs = getComputedStyle(b);
    return { pos: cs.position, inset: cs.inset, place: cs.placeItems };
  });
  check('the connecting screen is a whole centred surface, not a corner box',
    !!bootShape && bootShape.pos === 'absolute' && /center/.test(bootShape.place ?? ''),
    JSON.stringify(bootShape));

  // ── the window speaks one language ────────────────────────────────
  const shellText = await win.evaluate(() => {
    document.querySelectorAll('#rail .rail-btn')[1]?.click();
    return document.querySelector('#settings')?.innerText ?? '';
  });
  await win.waitForTimeout(400);
  check('the app chrome follows the OS language', /Ayarlar|Hesaplar|Eşitlenen klasörler/.test(shellText),
    shellText.slice(0, 60).replace(/\n/g, ' '));
  await win.evaluate(() => document.querySelector('#close-settings')?.click());
  await win.waitForTimeout(400);

  // ── navigate into the scratch folder ──────────────────────────────
  await openRow(win, DIR);
  await win.waitForTimeout(2500);
  const listed = await win.evaluate(() => document.body.innerText);
  check('the explorer lists the folder contents',
    ['notes.md', 'data.csv', 'pixel.png', 'rapor.txt'].every((f) => listed.includes(f)));

  // ── image preview: in its own window, the <img> the page cannot put a
  //    header on ─────────────────────────────────────────────────────
  const imgDoc = await openFile(app, win, 'pixel.png');
  // ⚠ The document window loads the SERVER's own editor page (a real URL,
  // never app://): that is where the old "open in new tab" check's promise now
  // lives — the file opens on the server, in a window of its own.
  // The app asks for `<server>/files/edit`; the server answers from its web
  // app's base (`/admin/files/edit`). Either way: THIS server, the editor
  // route, and exactly this file.
  const docUrl = imgDoc ? new URL(imgDoc.url()) : null;
  check('a file opens in a window of its own, on the server',
    !!docUrl && docUrl.origin === new URL(SERVER).origin && /^(\/admin)?\/files\/edit$/.test(docUrl.pathname) &&
      docUrl.searchParams.get('path') === `${REMOTE}/pixel.png`,
    docUrl ? `${docUrl.pathname} path=${docUrl.searchParams.get('path')}` : 'no document window');
  let img = null;
  if (imgDoc) {
    await imgDoc.waitForSelector('.fe-preview__image', { timeout: 15_000 }).catch(() => {});
    await imgDoc.waitForFunction(() => (document.querySelector('.fe-preview__image')?.naturalWidth ?? 0) > 0, null, { timeout: 10_000 }).catch(() => {});
    img = await imgDoc.evaluate(() => {
      const el = document.querySelector('.fe-preview__image');
      return el ? { w: el.naturalWidth, src: el.src.slice(0, 60) } : null;
    });
    await imgDoc.screenshot({ path: path.join(SHOTS, '29-image-window.png'), timeout: 20000 }).catch(() => {});
  }
  check('an image preview actually loads its bytes', !!img && img.w > 0,
    img ? `naturalWidth=${img.w}` : 'no <img> rendered');
  await closeDoc(imgDoc);

  // ── download reaches the session that holds the credential ────────
  // From the file list, the way a person downloads without opening: tick the
  // row, press the selection bar's Download.
  const dlSel = await selectOnly(win, 'pixel.png');
  await win.waitForTimeout(400);
  const dlClicked = await win.evaluate(() => {
    const visible = (e) => e.getClientRects().length > 0 && !e.closest('[aria-hidden="true"]');
    const b = [...document.querySelectorAll('button')].filter(visible)
      .find((x) => /^(İndir|Download)\b/i.test(x.getAttribute('aria-label') ?? x.title ?? ''));
    b?.click();
    return !!b;
  });
  await win.waitForTimeout(2500);
  const downloads = await app.evaluate(() => globalThis.__downloads);
  check('the download button starts a real download', dlClicked && downloads.length > 0,
    downloads.join(' | ') || (dlClicked ? 'nothing downloaded' : `no Download button on the selection bar (selection: "${dlSel}")`));
  await clearSelection(win);

  // ── text + markdown ───────────────────────────────────────────────
  const mdDoc = await openFile(app, win, 'notes.md');
  // ⚠ The editor half is a <textarea>, whose value is NOT in innerText — an
  // assertion on the page's text alone reports an empty document for a file
  // that loaded perfectly. Read both halves: the raw bytes that arrived, and
  // the rendered preview beside them.
  let md = { raw: '', rendered: '' };
  if (mdDoc) {
    await mdDoc.waitForFunction(() => (document.querySelector('.fe-preview__md-split-input')?.value ?? '').length > 0, null, { timeout: 15_000 }).catch(() => {});
    // The rendered half arrives after the source: the markdown renderer is a
    // chunk of its own, loaded on first use.
    await mdDoc.waitForFunction(() => (document.querySelector('.fe-preview__md-split-output')?.textContent ?? '').trim().length > 0, null, { timeout: 15_000 }).catch(() => {});
    md = await mdDoc.evaluate(() => ({
      raw: document.querySelector('.fe-preview__md-split-input')?.value ?? '',
      rendered: document.querySelector('.fe-preview__md-split-output')?.innerText ?? '',
    }));
  }
  check('a markdown file opens with its content', /filex desktop e2e/.test(md.raw),
    md.raw.slice(0, 40).replace(/\n/g, ' ') || (mdDoc ? 'empty' : 'no document window'));
  check('markdown renders beside the source', /Başlık/.test(md.rendered),
    md.rendered.slice(0, 40).replace(/\n/g, ' '));
  await closeDoc(mdDoc);

  const csvDoc = await openFile(app, win, 'data.csv');
  let csv = '';
  if (csvDoc) {
    await csvDoc.waitForFunction(() => /\ba\b[\s\S]*\bb\b/.test(document.body?.innerText ?? ''), null, { timeout: 15_000 }).catch(() => {});
    csv = await csvDoc.evaluate(() => document.body?.innerText ?? '');
  }
  check('a csv file opens in the table viewer', /\ba\b[\s\S]*\bb\b/.test(csv) && !/401/.test(csv),
    csv.slice(0, 60).replace(/\n/g, ' ') || (csvDoc ? 'empty' : 'no document window'));
  await closeDoc(csvDoc);

  // ── the office document that started this ─────────────────────────
  const officePath = process.env.FILEX_OFFICE_FILE;
  if (officePath) {
    const copy = await api('/api/files/copy', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source: [officePath], target: REMOTE }),
    }, token);
    const name = officePath.split('/').pop();
    // ⚠ Copy is an ASYNC op (202 Accepted): opening the row straight afterwards
    // races the transfer and reports a missing editor for a file that simply
    // was not there yet. Wait for the server to actually list it.
    let arrived = false;
    for (let i = 0; i < 30 && !arrived; i++) {
      const r = await api(`/api/files/manager?action=index&path=${encodeURIComponent(REMOTE)}`, {}, token);
      arrived = r.ok && ((await r.json()).files ?? []).some((f) => f.basename === name);
      if (!arrived) await win.waitForTimeout(1000);
    }
    check('an office fixture is available', copy.ok && arrived, `copy → ${copy.status}, listed=${arrived}`);
    await win.evaluate(() => document.querySelector('.fe-toolbar button[title="Yenile"], .fe-toolbar button[title="Refresh"]')?.click());
    await win.waitForTimeout(2500);
    // In its own window, like every file (see openFile).
    const officeDoc = await openFile(app, win, name);
    await (officeDoc ?? win).waitForTimeout(9000);
    const office = await (officeDoc ?? win).evaluate(() => {
      return {
        text: document.body?.innerText ?? '',
        // ⚠ Do not look for the mount id. DocsAPI REPLACES the element it is
        // given with an iframe of its own naming, so both `#fe-onlyoffice-mount
        // iframe` and `iframe#fe-onlyoffice-mount` find nothing while a fully
        // rendered spreadsheet is on screen — measured against a screenshot
        // that showed the document open.
        frame: [...document.querySelectorAll('iframe')].some((f) => f.getClientRects().length > 0),
      };
    });
    check('an office document opens instead of reporting 401',
      office.frame && !/Config fetch|401/.test(office.text),
      office.frame ? office.text.slice(0, 60).replace(/\n/g, ' ') : 'no editor frame');
    await (officeDoc ?? win).screenshot({ path: path.join(SHOTS, '30-office.png'), animations: 'disabled', timeout: 20000 }).catch(() => {});
    await closeDoc(officeDoc);
  }

  // ── the selection bar, the way a person uses it ───────────────────
  // ⚠ Its actions are ICONS (data-testid `selbar-<key>`, the name in
  // aria-label), and what does not fit folds into "More actions"
  // (`selbar-more`, a real menu of role=menuitem rows). The old text matching
  // — "Yeniden Adlandır", "⋯" — found nothing on any run, so rename and delete
  // were never exercised at all.
  const listNames = () => win.evaluate(() =>
    [...document.querySelectorAll('[data-fe-path]')]
      .filter((e) => e.getClientRects().length > 0)
      .map((e) => e.querySelector('.fe-list__name, .fe-grid__label')?.getAttribute('title') ?? ''));
  const remoteNames = async () => {
    const r = await api(`/api/files/manager?action=index&path=${encodeURIComponent(REMOTE)}`, {}, token);
    return ((await r.json()).files ?? []).map((f) => f.basename);
  };
  /** Runs a selection action: its icon when the bar has room, else the row
   *  of the same name under "More actions". */
  async function selAction(key, label) {
    const icon = win.locator(`[data-testid="selection-bar"] [data-testid="selbar-${key}"]`);
    if (await icon.isVisible().catch(() => false)) {
      await icon.click();
      return 'icon';
    }
    const more = win.locator('[data-testid="selection-bar"] [data-testid="selbar-more"]');
    if (!(await more.isVisible().catch(() => false))) return null;
    await more.click();
    const row = win.getByRole('menuitem', { name: label }).first();
    if (!(await row.waitFor({ state: 'visible', timeout: 5000 }).then(() => true, () => false))) return null;
    await row.click();
    return 'menu';
  }

  // ── starring: one of the features that was silently 401 ───────────
  // Measured by the file appearing in the server's starred list — a 200 from
  // that list alone says nothing about the click.
  await selectOnly(win, 'rapor.txt');
  const starredVia = await selAction('star', /Yıldız|Star/i);
  const starredOnServer = await (async () => {
    for (let i = 0; i < 20; i++) {
      const r = await api('/api/files/manager/star/list?limit=500', {}, token);
      const nodes = r.ok ? ((await r.json()).nodes ?? []) : [];
      if (JSON.stringify(nodes).includes(`${DIR}/rapor.txt`)) return true;
      await win.waitForTimeout(500);
    }
    return false;
  })();
  check('starring a file reaches the server', !!starredVia && starredOnServer,
    `${starredVia ?? 'no star action on the selection bar'}, listed=${starredOnServer}`);
  await clearSelection(win);
  await win.waitForTimeout(400);

  // ── the folder filter ─────────────────────────────────────────────
  // "Bu klasörde filtrele…" (FilterBar, data-testid filter-find). The search
  // box in the toolbar searches the whole storage; it is not this.
  const find = win.locator('[data-testid="filter-find"]').first();
  const findShown = await find.isVisible().catch(() => false);
  if (findShown) await find.fill('rapor');
  await win.waitForTimeout(1500);
  const filtered = await listNames();
  check('the filter box narrows the listing',
    findShown && filtered.includes('rapor.txt') && !filtered.includes('pixel.png') && !filtered.includes('notes.md'),
    findShown ? filtered.join(', ') : 'no folder filter box on screen');
  if (findShown) await find.fill('');
  await win.waitForTimeout(1200);

  // ── grid view: thumbnails are fetched, not linked ─────────────────
  // The 1×1 PNG has a picture; its card must show it (fetched with the
  // account's credential — an <img> cannot carry one).
  const gridBtn = win.locator('[data-testid="view-grid"]').first();
  const gridShown = await gridBtn.isVisible().catch(() => false);
  if (gridShown) await gridBtn.click();
  await win.waitForFunction(() => [...document.querySelectorAll('.fe-grid__thumb img')].some((i) => i.naturalWidth > 0), null, { timeout: 15_000 }).catch(() => {});
  const thumbs = await win.evaluate(() =>
    [...document.querySelectorAll('.fe-grid__thumb img')].map((i) => ({ w: i.naturalWidth, src: i.src.slice(0, 24) })));
  check('grid thumbnails load', gridShown && thumbs.some((t) => t.w > 0),
    gridShown ? (thumbs.length ? JSON.stringify(thumbs.slice(0, 3)) : 'no <img> in any card') : 'no grid switch on screen');
  await win.screenshot({ path: path.join(SHOTS, '31-grid.png'), animations: 'disabled', timeout: 20000 }).catch(() => {});
  await win.locator('[data-testid="view-list"]').first().click().catch(() => {});
  await win.waitForTimeout(1500);

  // ── rename ────────────────────────────────────────────────────────
  await selectOnly(win, 'rapor.txt');
  const renameVia = await selAction('rename', /Yeniden adlandır|Rename/i);
  // The explorer's own dialog (RenameModal): one text box holding the name.
  const renameBox = win.locator('.fe-modal input.fe-input, [role="dialog"] input.fe-input').first();
  const renameOpen = await renameBox.waitFor({ state: 'visible', timeout: 5000 }).then(() => true, () => false);
  if (renameOpen) {
    await renameBox.fill('rapor-yeni.txt');
    await renameBox.press('Enter');
  }
  let names = [];
  for (let i = 0; i < 20; i++) {
    names = await remoteNames();
    if (names.includes('rapor-yeni.txt')) break;
    await win.waitForTimeout(500);
  }
  check('renaming a file changes it on the server', !!renameVia && renameOpen && names.includes('rapor-yeni.txt') && !names.includes('rapor.txt'),
    `${renameVia ?? 'no rename action'}, dialog=${renameOpen} → ${names.join(', ')}`);

  // ── delete → trash ────────────────────────────────────────────────
  await win.waitForTimeout(800);
  await selectOnly(win, 'data.csv');
  const deleteVia = await selAction('delete', /^Sil$|^Delete$/i);
  // Deleting asks first, in the explorer's own modal — "Çöpe at" / "Move to
  // trash", not a browser confirm().
  const confirmBtn = win.getByRole('button', { name: /^Çöpe at$|^Move to trash$/i }).first();
  const confirmed = await confirmBtn.waitFor({ state: 'visible', timeout: 5000 }).then(() => true, () => false);
  const leftBefore = await remoteNames();
  check('deleting asks for confirmation first', !!deleteVia && confirmed && leftBefore.includes('data.csv'),
    `${deleteVia ?? 'no delete action'}, confirm=${confirmed}, still there before confirming=${leftBefore.includes('data.csv')}`);
  if (confirmed) await confirmBtn.click();
  let left = [];
  for (let i = 0; i < 20; i++) {
    left = await remoteNames();
    if (!left.includes('data.csv')) break;
    await win.waitForTimeout(500);
  }
  check('deleting a file removes it from the listing', confirmed && !left.includes('data.csv'), left.join(', '));

  await win.screenshot({ path: path.join(SHOTS, '32-files.png'), animations: 'disabled', timeout: 20000 }).catch(() => {});

  // ── the blanket rule ──────────────────────────────────────────────
  check('nothing the window asked its server for was refused', denied.length === 0,
    denied.slice(0, 6).join(' | ') || 'no 401/403 at all');
} catch (e) {
  check('flow completed', false, String(e && e.message).split('\n')[0]);
} finally {
  if (token) {
    await api('/api/files/manager?action=delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ items: [{ path: REMOTE, type: 'dir' }] }),
    }, token).catch(() => {});
  }
  await app.close().catch(() => {});
}

finish();
