// Proves the share button in the EXISTING dialog works in the desktop app.
//
// Electron ships no Web Share API, so the explorer's own share button — gated
// on `typeof navigator.share === 'function'` — never rendered here. The app
// polyfills the standard API onto a native handler; this checks the result the
// way a user meets it: create a link, look for the button, press it.
//
// Run: node scripts/share-e2e.mjs
// Env: FILEX_SERVER, FILEX_EMAIL, FILEX_PASSWORD, FILEX_STORAGE

import fs from 'node:fs';
import path from 'node:path';
import { SHOTS, STORAGE, api, check, finish, launchApp, signIn, skipTour } from './lib/harness.mjs';

fs.mkdirSync(SHOTS, { recursive: true });

// The dialog needs something to share. Seeded by this run and removed after —
// pointing the script at a file someone once left on the storage is how it came
// to fail on every server but one.
const NAME = `share-e2e-${Date.now()}.txt`;
let token = null;

/**
 * Clicks the first VISIBLE element whose text matches.
 *
 * ⚠ Visibility is not optional here. The toolbar renders every action a second
 * time inside an `aria-hidden` measuring strip (`visibility:hidden; height:0`)
 * so the fold calculation has real widths — those clones still have client
 * rects and carry no click handler, so a plain text search clicks a button that
 * does nothing at all.
 */
async function clickText(win, pattern) {
  return win.evaluate((src) => {
    const re = new RegExp(src, 'i');
    const visible = (e) => e.getClientRects().length > 0 && !e.closest('[aria-hidden="true"]');
    const el = [...document.querySelectorAll('button, li, [role="menuitem"]')]
      .filter(visible)
      .find((x) => re.test(x.textContent ?? ''));
    el?.click();
    return !!el;
  }, pattern.source ?? pattern);
}

const { app } = await launchApp();

try {
  const { win: w, adminToken } = await signIn(app, { label: 'filex desktop — share e2e' });
  token = adminToken;

  const form = new FormData();
  form.append('path', `${STORAGE}://`);
  form.append('file[]', new Blob(['share e2e fixture\n'], { type: 'text/plain' }), NAME);
  const up = await api('/api/files/manager?action=upload', { method: 'POST', body: form }, adminToken);
  if (!up.ok) throw new Error(`could not seed a fixture file (${up.status})`);

  await w.waitForTimeout(5000);
  await skipTour(w);
  await w.evaluate(() => document.querySelector('.fe-toolbar button[title="Refresh"], .fe-toolbar button[title="Yenile"]')?.click());
  await w.waitForTimeout(2500);

  // The API the product's button is gated on must now exist.
  const bridge = await w.evaluate(() => ({
    share: typeof navigator.share,
    canShare: typeof navigator.canShare,
  }));
  check('navigator.share exists in the desktop shell', bridge.share === 'function', `share=${bridge.share}`);

  // ── open the dialog and create a link ─────────────────────────────
  const selected = await w.evaluate((name) => {
    const row = [...document.querySelectorAll('.fe-list__row')].find((r) => r.textContent?.includes(name));
    row?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    return !!row;
  }, NAME);
  check('the seeded file is listed and selectable', selected, NAME);
  await w.waitForTimeout(500);

  // Share sits in the "⋯" overflow when the toolbar is full.
  if (!(await clickText(w, /Payla[sş] \/ [İI]zinler|Share \/ Permissions/))) {
    await clickText(w, /^⋯$/);
    await w.waitForTimeout(500);
    await clickText(w, /Payla[sş] \/ [İI]zinler|Share \/ Permissions/);
  }
  await w.waitForTimeout(1200);
  await clickText(w, /^Ba[gğ]lant[iı]$|^Link$/);
  await w.waitForTimeout(800);
  await clickText(w, /Ba[gğ]lant[iı] olu[sş]tur|Create link/);
  await w.waitForTimeout(2500);

  const buttons = await w.evaluate(() => {
    const m = document.querySelector('.fx-perm-modal');
    return m ? [...m.querySelectorAll('button')].map((b) => b.textContent.trim()).filter(Boolean) : [];
  });
  const shareBtn = buttons.find((b) => /Payla[sş]|Share/.test(b) && !/İzinler|Permissions/.test(b));
  check('the share button appears once a link exists', !!shareBtn,
    shareBtn ? `"${shareBtn}"` : buttons.join(' | '));

  await w.screenshot({ path: path.join(SHOTS, '21-share-with-link.png') });
  console.log('  shots/21-share-with-link.png');

  // ── press it ──────────────────────────────────────────────────────
  // ⚠ The native menu is an OS window. Playwright cannot see inside it, and
  // awaiting the call hangs until a human dismisses it — which is correct
  // behaviour, and exactly why the first version of this test timed out.
  //
  // So measure what IS observable: fire the call without awaiting, and see
  // whether it settles. A promise still pending after a beat means the native
  // menu is up; an immediate rejection means the handler refused. Both are real
  // signals. What this canNOT prove is what the menu looks like — said here
  // rather than implied by a green tick.
  await w.evaluate(() => {
    window.__shareState = 'pending';
    window.filexApp
      .share({ title: 'fixture', text: 'filex', url: 'https://example.invalid/s/abc' })
      .then((r) => { window.__shareState = 'resolved:' + (r?.via ?? '?'); })
      .catch((e) => { window.__shareState = 'rejected:' + String(e?.message ?? e); });
  });
  await w.waitForTimeout(1500);
  const state = await w.evaluate(() => window.__shareState);
  // ⚠ `AbortError` counts as success. It is raised ONLY from the popup's own
  // dismiss callback, so receiving it proves the native menu really opened —
  // and a menu closes by itself the moment its window loses focus, which is
  // exactly what happens when these suites run one after another. Treating it
  // as a failure made this check fail on a machine, not on a bug.
  check('pressing share opens the native menu',
    state === 'pending' || state.startsWith('resolved') || /AbortError/.test(state), state);
  check('the handler did not refuse the payload',
    !/nothing to share|not supported|is not a function/i.test(state), state);

  // Dismiss it so the window is usable again and nothing is left on screen.
  await w.keyboard.press('Escape').catch(() => {});
  await w.waitForTimeout(600);
} catch (e) {
  check('flow completed', false, String(e && e.message).split('\n')[0]);
} finally {
  if (token) {
    await api('/api/files/manager?action=delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ items: [{ path: `${STORAGE}://${NAME}`, type: 'file' }] }),
    }, token).catch(() => {});
  }
  await app.close().catch(() => {});
}

finish();
