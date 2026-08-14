// The download cap, driven through the UI that actually exists.
//
// It used to live in a standalone share dialog; when link creation moved into
// the "Share / Permissions" panel the field was left behind, so the server kept
// honouring `max_downloads` while nothing could set it. This drives the panel
// and then asks the SERVER what it stored — a select that renders but does not
// reach the API would pass a DOM-only check.
//
// Run: FILEX_EMAIL=… FILEX_PASSWORD=… node scripts/share-limit-e2e.mjs

import { SERVER, STORAGE, api, check, finish, launchApp, signIn, skipTour, sleep } from './lib/harness.mjs';

const NAME = `limit-e2e-${Date.now()}.txt`;
const { app } = await launchApp({ lang: 'tr' });
const { win, adminToken } = await signIn(app, { label: 'filex desktop — limit e2e' });

async function ensureNoTour() {
  for (let i = 0; i < 12; i++) {
    if ((await win.locator('.fe-tour').count()) === 0) return;
    await skipTour(win);
    await sleep(250);
  }
}
await ensureNoTour();

// ── fixture ──────────────────────────────────────────────────────────
const form = new FormData();
form.set('path', `${STORAGE}://`);
form.append('file[]', new Blob(['limit fixture\n'], { type: 'text/plain' }), NAME);
const up = await api('/api/files/manager?q=upload', { method: 'POST', body: form }, adminToken);
check('fixture uploaded', up.ok, `${up.status}`);
await win.reload();
await win.waitForLoadState('domcontentloaded');
await sleep(3000);
await ensureNoTour();

// ⚠ The toolbar renders EVERY action a second time in an aria-hidden measure
// strip (`visibility:hidden`), which still has an offsetParent and a client
// rect — so "the first match" is regularly the copy with no click handler.
// Skip that strip and require a real, visible box.
async function clickText(re) {
  return win.evaluate((src) => {
    const rx = new RegExp(src.s, src.f);
    const el = [...document.querySelectorAll('button, [role="menuitem"], .fe-ctx__item, .fx-perm-tab')]
      .filter((e) => !e.closest('.fe-toolbar__measure') && !e.closest('[aria-hidden="true"]'))
      .filter((e) => {
        const st = getComputedStyle(e);
        if (st.visibility === 'hidden' || st.display === 'none') return false;
        const r = e.getBoundingClientRect();
        return r.width > 0 && r.height > 0;
      })
      .find((e) => rx.test((e.textContent || '').trim()));
    el?.click();
    return !!el;
  }, { s: re.source, f: re.flags });
}

// ── open Share / Permissions → Link tab ──────────────────────────────
const selected = await win.evaluate((name) => {
  const row = [...document.querySelectorAll('.fe-list__row')].find((r) => r.textContent?.includes(name));
  row?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  return !!row;
}, NAME);
check('the fixture is listed and selectable', selected, NAME);
await sleep(500);

let opened = await clickText(/Payla[sş] \/ [İI]zinler|Share \/ Permissions/);
if (!opened) {
  await clickText(/^⋯$/);
  await sleep(500);
  opened = await clickText(/Payla[sş] \/ [İI]zinler|Share \/ Permissions/);
}
await sleep(1500);
check('the Share / Permissions panel opens', opened && (await win.locator('.fx-perm-modal').count()) > 0);
const onLink = await clickText(/^Ba[gğ]lant[iı]$|^Link$/);
await sleep(900);
check('…and the Link tab is reachable', onLink);

// ── the control itself ───────────────────────────────────────────────
const found = await win.evaluate(() => {
  const labels = [...document.querySelectorAll('.fx-perm-modal label')];
  const el = labels.find((l) => /ndirme limiti|Download limit/.test(l.textContent || ''));
  if (!el) return null;
  const sel = el.querySelector('select');
  return {
    options: sel ? [...sel.options].map((o) => o.textContent.trim()) : [],
    value: sel?.value ?? null,
  };
});
check('the link tab offers a download limit', !!found,
  found ? `${found.options.length} seçenek` : 'MISSING — the field never made it into the permissions panel');
check('…including a 3-download option', !!found?.options.some((o) => /^3 /.test(o)),
  (found?.options ?? []).join(' | '));
check('…defaulting to unlimited', found?.value === '0', String(found?.value));

// ── pick 3, create the link, ask the SERVER what it stored ───────────
const picked = await win.evaluate(() => {
  const el = [...document.querySelectorAll('.fx-perm-modal label')]
    .find((l) => /ndirme limiti|Download limit/.test(l.textContent || ''));
  const sel = el?.querySelector('select');
  if (!sel) return false;
  sel.value = '3';
  sel.dispatchEvent(new Event('change', { bubbles: true }));
  return true;
});
check('the limit can be set to 3', picked);
await sleep(300);
await clickText(/Ba[gğ]lant[iı] olu[sş]tur|Create link/);
await sleep(3000);

const list = await api(`/api/files/share?path=${encodeURIComponent(`${STORAGE}://${NAME}`)}`, {}, adminToken);
const body = await list.json().catch(() => ({}));
const rows = body.shares ?? [];
const stored = rows.length ? rows[rows.length - 1] : null;
check('the server stored the link', !!stored, `${rows.length} kayıt`);
check('…with max_downloads = 3', Number(stored?.max_downloads) === 3,
  `max_downloads=${stored?.max_downloads ?? 'null'} — a control that does not reach the API is decoration`);

// ── and now the part this suite used to skip ─────────────────────────
// ⚠ "the server stored 3" is not "the link hands out 3". It was stored
// correctly the whole time while the link served four: the cap was checked
// against a counter bumped only after the bytes had left. Ask the link itself.
if (stored?.url) {
  let served = 0;
  const codes = [];
  for (let i = 0; i < 5; i++) {
    const res = await fetch(stored.url, { redirect: 'follow' });
    const body = await res.text();
    codes.push(res.status);
    if (res.ok && body.includes('limit fixture')) served++;
  }
  check('the link serves exactly three files, not four', served === 3,
    `${served} indirme · HTTP ${codes.join(',')}`);
}

// ── cleanup ──────────────────────────────────────────────────────────
if (stored?.uuid) await api(`/api/files/share/${stored.uuid}`, { method: 'DELETE' }, adminToken);
await api('/api/files/manager?q=delete', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ items: [{ path: `${STORAGE}://${NAME}`, type: 'file' }] }),
}, adminToken);

await app.close();
finish();
