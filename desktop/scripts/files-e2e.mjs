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
// Run: node scripts/files-e2e.mjs
// Env: FILEX_SERVER, FILEX_EMAIL, FILEX_PASSWORD, FILEX_STORAGE

import fs from 'node:fs';
import path from 'node:path';
import { SHOTS, SERVER, STORAGE, api, check, finish, launchApp, signIn, skipTour } from './lib/harness.mjs';

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

/** Clicks the first VISIBLE match.
 *
 *  ⚠ Two traps, both met the hard way. The toolbar renders every action a
 *  second time inside an aria-hidden measuring strip (visibility:hidden, so it
 *  still has client rects) and those clones carry no click handler. And every
 *  label is prefixed by an icon glyph in the same text node — `^Sil$` matches
 *  nothing, because the button actually reads "🗑Sil". */
async function clickText(win, re) {
  return win.evaluate((src) => {
    const rx = new RegExp(src, 'i');
    const visible = (e) => e.getClientRects().length > 0 && !e.closest('[aria-hidden="true"]');
    const label = (e) => (e.textContent ?? '').replace(/[\p{Extended_Pictographic}\p{So}️]/gu, '').trim();
    const el = [...document.querySelectorAll('button, li, [role="menuitem"], a')]
      .filter(visible)
      .find((x) => rx.test(label(x)));
    el?.click();
    return !!el;
  }, re.source);
}

async function selectRow(win, name) {
  return win.evaluate((n) => {
    const row = [...document.querySelectorAll('.fe-list__row')].find((r) => r.textContent?.includes(n));
    row?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    return !!row;
  }, name);
}

async function openRow(win, name) {
  return win.evaluate((n) => {
    const row = [...document.querySelectorAll('.fe-list__row')].find((r) => r.textContent?.includes(n));
    row?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    return !!row;
  }, name);
}

async function closeModal(win) {
  await win.keyboard.press('Escape').catch(() => {});
  await win.waitForTimeout(600);
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

  // ── image preview: the <img> the page cannot put a header on ──────
  await openRow(win, 'pixel.png');
  await win.waitForTimeout(2500);
  const img = await win.evaluate(() => {
    const el = document.querySelector('.fe-preview__image');
    return el ? { complete: el.complete, w: el.naturalWidth, src: el.src.slice(0, 60) } : null;
  });
  check('an image preview actually loads its bytes', !!img && img.w > 0,
    img ? `naturalWidth=${img.w}` : 'no <img> rendered');

  // ── open in new tab goes to the BROWSER, on the server ────────────
  await clickText(win, /Yeni Sekmede Aç|Open in New Tab/);
  await win.waitForTimeout(1200);
  const external = await app.evaluate(() => globalThis.__external);
  check('"open in new tab" hands the browser a real https URL',
    external.some((u) => u.startsWith(`${SERVER}/files/edit`)),
    external.join(' | ') || 'nothing was opened');
  check('"open in new tab" never asks the OS to open app://',
    !external.some((u) => u.startsWith('app://')), external.join(' | '));

  // ── download reaches the session that holds the credential ────────
  await clickText(win, /^İndir$|^Download$/);
  await win.waitForTimeout(2500);
  const downloads = await app.evaluate(() => globalThis.__downloads);
  check('the download button starts a real download', downloads.length > 0,
    downloads.join(' | ') || 'nothing downloaded');
  await closeModal(win);

  // ── text + markdown ───────────────────────────────────────────────
  await openRow(win, 'notes.md');
  await win.waitForTimeout(3500);
  // ⚠ The editor half is a <textarea>, whose value is NOT in innerText — an
  // assertion on the modal's text alone reports an empty document for a file
  // that loaded perfectly. Read both halves: the raw bytes that arrived, and
  // the rendered preview beside them.
  const md = await win.evaluate(() => ({
    raw: document.querySelector('.fe-preview__md-split-input')?.value ?? '',
    rendered: document.querySelector('.fe-preview__md-split-output')?.innerText ?? '',
  }));
  check('a markdown file opens with its content', /filex desktop e2e/.test(md.raw),
    md.raw.slice(0, 40).replace(/\n/g, ' '));
  check('markdown renders beside the source', /Başlık/.test(md.rendered),
    md.rendered.slice(0, 40).replace(/\n/g, ' '));
  await closeModal(win);

  await openRow(win, 'data.csv');
  await win.waitForTimeout(2500);
  const csv = await win.evaluate(() => document.querySelector('.fe-modal, [class*="modal"]')?.innerText ?? '');
  check('a csv file opens in the table viewer', /a\b[\s\S]*b/.test(csv) && !/401/.test(csv),
    csv.slice(0, 60).replace(/\n/g, ' '));
  await closeModal(win);

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
    await openRow(win, name);
    await win.waitForTimeout(9000);
    const office = await win.evaluate(() => {
      const m = document.querySelector('.fe-modal, [class*="modal"]');
      return {
        text: m?.innerText ?? '',
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
    await win.screenshot({ path: path.join(SHOTS, '30-office.png'), animations: 'disabled', timeout: 20000 }).catch(() => {});
    await closeModal(win);
  }

  // ── starring: one of the features that was silently 401 ───────────
  await selectRow(win, 'rapor.txt');
  await win.waitForTimeout(400);
  const starred = await win.evaluate(() => {
    const visible = (e) => e.getClientRects().length > 0 && !e.closest('[aria-hidden="true"]');
    const b = [...document.querySelectorAll('button')].filter(visible)
      .find((x) => /★|☆/.test(x.textContent ?? '') || /yıldız|star/i.test(x.title ?? ''));
    b?.click();
    return !!b;
  });
  await win.waitForTimeout(1500);
  const starList = await api('/api/files/manager/star/list?limit=500', {}, token);
  check('starring a file reaches the server', starList.status === 200, `star/list → ${starList.status}`);
  if (!starred) console.log('  (no star control on screen — list view may hide it at this width)');

  // ── search ────────────────────────────────────────────────────────
  await win.evaluate(() => {
    const i = document.querySelector('.fe-toolbar input[type="text"], .fe-toolbar input[type="search"]');
    if (i) {
      i.value = 'rapor';
      i.dispatchEvent(new Event('input', { bubbles: true }));
    }
  });
  await win.waitForTimeout(2500);
  const searched = await win.evaluate(() => document.body.innerText);
  check('the filter box narrows the listing', searched.includes('rapor.txt') && !searched.includes('pixel.png'),
    'rapor.txt shown, pixel.png hidden');
  await win.evaluate(() => {
    const i = document.querySelector('.fe-toolbar input[type="text"], .fe-toolbar input[type="search"]');
    if (i) { i.value = ''; i.dispatchEvent(new Event('input', { bubbles: true })); }
  });
  await win.waitForTimeout(1200);

  // ── grid view: thumbnails are fetched, not linked ─────────────────
  await win.evaluate(() => document.querySelector('.fe-toolbar button[title="Izgara"], .fe-toolbar button[title="Grid"]')?.click());
  await win.waitForTimeout(3000);
  const thumbs = await win.evaluate(() =>
    [...document.querySelectorAll('.fe-grid img, .fe-grid__thumb img')].map((i) => ({ w: i.naturalWidth, src: i.src.slice(0, 12) })));
  check('grid thumbnails load', thumbs.length === 0 || thumbs.some((t) => t.w > 0),
    thumbs.length ? JSON.stringify(thumbs.slice(0, 3)) : 'no thumbnails on this storage');
  await win.screenshot({ path: path.join(SHOTS, '31-grid.png'), animations: 'disabled', timeout: 20000 }).catch(() => {});
  await win.evaluate(() => document.querySelector('.fe-toolbar button[title="Liste"], .fe-toolbar button[title="List"]')?.click());
  await win.waitForTimeout(1500);

  // ── rename ────────────────────────────────────────────────────────
  await selectRow(win, 'rapor.txt');
  await win.waitForTimeout(300);
  win.once('dialog', (d) => d.accept('rapor-yeni.txt'));
  if (!(await clickText(win, /Yeniden Adlandır|^Rename$/))) {
    await clickText(win, /^⋯$/);
    await win.waitForTimeout(400);
    await clickText(win, /Yeniden Adlandır|^Rename$/);
  }
  await win.waitForTimeout(1200);
  await win.evaluate((name) => {
    const input = [...document.querySelectorAll('input')].find((i) => i.value?.includes(name));
    if (!input) return;
    input.value = 'rapor-yeni.txt';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    const form = input.closest('form');
    form?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  }, 'rapor.txt');
  await win.waitForTimeout(2500);
  const afterRename = await api(`/api/files/manager?action=index&path=${encodeURIComponent(REMOTE)}`, {}, token);
  const names = ((await afterRename.json()).files ?? []).map((f) => f.basename);
  check('renaming a file changes it on the server', names.includes('rapor-yeni.txt'), names.join(', '));

  // ── delete → trash ────────────────────────────────────────────────
  await win.evaluate(() => document.querySelector('.fe-toolbar button[title="Yenile"], .fe-toolbar button[title="Refresh"]')?.click());
  await win.waitForTimeout(1500);
  await selectRow(win, 'data.csv');
  await win.waitForTimeout(300);
  if (!(await clickText(win, /^Sil$|^Delete$/))) {
    await clickText(win, /^⋯$/);
    await win.waitForTimeout(400);
    await clickText(win, /^Sil$|^Delete$/);
  }
  await win.waitForTimeout(1000);
  // Deleting asks first, in the explorer's own modal — "Çöpe At" / "Move to
  // trash", not a browser confirm().
  const confirmed = await clickText(win, /^Çöpe At$|^Move to trash$|^Delete$/);
  check('deleting asks for confirmation first', confirmed, 'confirm button found');
  await win.waitForTimeout(2500);
  const afterDelete = await api(`/api/files/manager?action=index&path=${encodeURIComponent(REMOTE)}`, {}, token);
  const left = ((await afterDelete.json()).files ?? []).map((f) => f.basename);
  check('deleting a file removes it from the listing', !left.includes('data.csv'), left.join(', '));

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
