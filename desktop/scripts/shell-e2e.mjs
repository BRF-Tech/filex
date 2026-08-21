// Drives the REAL app window and checks it is a FILE MANAGER, not a console.
//
// This suite exists because the first build shipped the admin SPA inside the
// window: signing in landed the user on a dashboard with users, storages and
// server settings — a server console wearing a desktop app's icon. The checks
// below are the specific things that were wrong, so they cannot come back.
//
// ⚠ No OS-level input. Playwright talks to the renderer.
//
// Run: node scripts/shell-e2e.mjs
// Env: FILEX_SERVER, FILEX_EMAIL, FILEX_PASSWORD

import path from 'node:path';
import {
  REPO, STORAGE,
  api, check, finish, launchApp, rowEvent, signIn, skipTour,
} from './lib/harness.mjs';

const { app } = await launchApp();

/** A file that is certainly there, put there by this run and taken away after. */
const SEED_NAME = process.env.FILEX_SEEDED_FILE ?? `desktop-e2e-${Date.now()}.txt`;
let seedToken = null;

try {
  // ── sign in ───────────────────────────────────────────────────────
  const connect = await app.firstWindow();
  await connect.waitForLoadState('domcontentloaded');
  check('starts on the connect screen', await connect.locator('#server').isVisible());
  check('the connect screen offers nothing but connecting',
    (await connect.locator('nav').count()) === 0,
    'accounts + sync + settings belong in the app window');

  const { win, adminToken } = await signIn(app, { label: 'filex desktop — shell e2e' });
  seedToken = adminToken;
  check('the app window loaded', true, `app://filex url=${win.url()}`);

  // ⚠ Seeded by this run rather than assumed. The check below is "a real file
  // from the server is on screen"; pointing it at a file someone happened to
  // leave on the storage made it fail on every server but one.
  if (!process.env.FILEX_SEEDED_FILE) {
    const form = new FormData();
    form.append('path', `${STORAGE}://`);
    form.append('file[]', new Blob(['desktop e2e fixture\n'], { type: 'text/plain' }), SEED_NAME);
    const up = await api('/api/files/manager?action=upload', { method: 'POST', body: form }, adminToken);
    if (!up.ok) throw new Error(`could not seed a fixture file (${up.status})`);
  }

  await win.waitForTimeout(4000);

  // ⭐ The point of this whole rewrite: what is on screen when you sign in.
  const explorerCount = await win.locator('filex-explorer').count();
  check('the window shows the FILE EXPLORER', explorerCount === 1, `${explorerCount} found`);

  // ⚠ Measured OUTSIDE the explorer. "Storages" is a word the file explorer
  // itself uses in English, so scanning the whole body reported an admin
  // console that was never there — the check has to look at the shell only.
  const shellText = await win.evaluate(() => {
    const clone = document.body.cloneNode(true);
    clone.querySelectorAll('filex-explorer').forEach((e) => e.remove());
    return clone.innerText ?? clone.textContent ?? '';
  });
  const consoleWords = ['Dashboard', 'Audit log', 'Webhooks', 'Storage adapters', 'Kontrol'];
  const found = consoleWords.filter((w) => shellText.includes(w));
  check('no admin console in the window', found.length === 0, found.join(', ') || 'clean');

  // ⚠ "the component rendered something" is too weak a claim — an empty folder
  // and a dead component look the same from the outside. The server is seeded
  // with a file, so the test asserts THAT FILE IS ON SCREEN: the token function,
  // the cross-origin request and the listing all have to work for it to appear.
  const seeded = SEED_NAME;
  let shows = false;
  for (let i = 0; i < 20 && !shows; i++) {
    shows = (await win.evaluate(() => document.body.innerText)).includes(seeded);
    if (!shows) await win.waitForTimeout(500);
  }
  check(`the explorer lists a real file from the server (${seeded})`, shows);

  // ── the account rail ──────────────────────────────────────────────
  const rail = await win.evaluate(() => {
    const r = document.querySelector('#rail');
    return {
      exists: !!r,
      avatars: r ? r.querySelectorAll('.avatar').length : 0,
      hasAdd: !!r?.querySelector('.rail-btn'),
    };
  });
  check('there is an account rail down the left', rail.exists && rail.avatars === 1,
    `${rail.avatars} account(s)`);
  check('the rail offers adding another account', rail.hasAdd);

  // ⚠ Optical centring, measured rather than eyeballed. A text glyph (⚙, +) is
  // laid out against the font baseline, so `place-items: center` centres the
  // line box while the shape itself sits low and left. Comparing the icon's own
  // box to its button's is the only way to catch that from a script.
  const icons = await win.evaluate(() => {
    return [...document.querySelectorAll('#rail .rail-btn')].map((b) => {
      const svg = b.querySelector('svg');
      if (!svg) return { hasSvg: false, dx: 99, dy: 99 };
      const br = b.getBoundingClientRect();
      const ir = svg.getBoundingClientRect();
      return {
        hasSvg: true,
        dx: Math.abs((ir.left + ir.width / 2) - (br.left + br.width / 2)),
        dy: Math.abs((ir.top + ir.height / 2) - (br.top + br.height / 2)),
      };
    });
  });
  const offCentre = icons.filter((i) => !i.hasSvg || i.dx > 1 || i.dy > 1);
  check('the rail icons are centred in their buttons', offCentre.length === 0,
    icons.map((i) => `dx=${i.dx.toFixed(2)} dy=${i.dy.toFixed(2)}`).join('  '));

  // ── settings: the APP's, not the server's ─────────────────────────
  await win.evaluate(() => document.querySelectorAll('#rail .rail-btn')[1].click());
  await win.waitForTimeout(500);
  const settings = await win.evaluate(() => {
    const s = document.querySelector('#settings');
    return { open: s?.classList.contains('open'), text: s?.innerText ?? '' };
  });
  check('settings opens inside the app', settings.open === true);
  check('settings manages ACCOUNTS', /Sign out/.test(settings.text));
  check('settings manages SYNCED FOLDERS', /Synced folders/i.test(settings.text));
  check('settings offers background running', /background/i.test(settings.text));
  check('settings offers start-at-login', /Start when I sign in/i.test(settings.text));
  check('the admin panel is a LINK OUT, not a screen in here',
    /Admin panel/.test(settings.text));
  // ⚠ Ask what is ACTUALLY on top, not whether the element I chose to hide is
  // hidden. The earlier version of this check asserted `#explorer-host` was
  // display:none — it was, and it passed, while the explorer's onboarding tour
  // (a child of <body>, not of the host) carried on sitting in the middle of
  // Settings. The screenshot showed it; the test did not. elementFromPoint is
  // the browser's own answer to "what would the user click here".
  const onTop = await win.evaluate(() => {
    const el = document.elementFromPoint(innerWidth / 2, innerHeight / 2);
    return { inSettings: !!el?.closest('#settings'), got: el?.className?.toString?.().slice(0, 40) ?? el?.tagName };
  });
  check('nothing overlays the settings panel', onTop.inSettings, `topmost = ${onTop.got}`);
  const tourVisible = await win.evaluate(() => {
    const t = document.querySelector('.fe-tour');
    return !!t && getComputedStyle(t).display !== 'none';
  });
  check("the explorer's onboarding tour is not on top of settings", !tourVisible);

  await win.screenshot({ path: path.join(REPO, 'desktop-shell-settings.png') });

  // ── the remote folder picker ──────────────────────────────────────
  await win.evaluate(() => document.querySelector('#add-sync')?.click());
  await win.waitForTimeout(2500);
  const picker = await win.evaluate(() => {
    const p = document.querySelector('#picker');
    return {
      open: p?.classList.contains('open'),
      items: [...p.querySelectorAll('#pk-list li')].map((li) => li.textContent),
    };
  });
  check('choosing a folder browses the REAL server', picker.open === true && picker.items.length > 0,
    picker.items.join(' | ') || 'nothing listed');

  // ⚠ The picker is opened FROM settings, so it has to outrank the panel that
  // launched it. It did not: the dialog rendered BEHIND a full-screen surface —
  // present in the DOM, invisible on screen. "It exists" is not "you can see
  // it"; ask the browser what is on top of the picker's own centre.
  const pickerOnTop = await win.evaluate(() => {
    const p = document.querySelector('#picker .panel');
    if (!p) return { ok: false, got: 'no panel' };
    const r = p.getBoundingClientRect();
    const el = document.elementFromPoint(r.left + r.width / 2, r.top + 20);
    return { ok: !!el?.closest('#picker'), got: el?.id || el?.className?.toString?.().slice(0, 40) };
  });
  check('the folder picker is on top, not behind settings', pickerOnTop.ok, `topmost = ${pickerOnTop.got}`);
  check('the storage is listed by name', picker.items.some((t) => t.includes(STORAGE)),
    picker.items.join(' | '));

  await win.screenshot({ path: path.join(REPO, 'desktop-shell-picker.png') });

  // Close it again and go back to the explorer.
  await win.evaluate(() => document.querySelector('#pk-close').click());
  await win.evaluate(() => document.querySelector('#close-settings')?.click());
  await win.waitForTimeout(400);
  const backToFiles = await win.evaluate(() =>
    !document.querySelector('#settings').classList.contains('open'));
  check('closing settings returns to the files', backToFiles);

  // ⚠ The dialog a user actually opens, and whether it LOOKS like a dialog.
  // It rendered as raw unstyled HTML in every embedded surface — no box, no
  // backdrop, browser-default inputs flowing down the page — because Vue's
  // scoped-style hash does not survive the web-component build. Nothing threw;
  // the functional suites were all green while it looked broken.
  // ⚠ The tour card is a child of <body>, not of the explorer, and it sits in
  // the middle of the window swallowing clicks. Dismiss it before driving
  // anything — measured: every click below landed on the tour instead.
  await skipTour(win);
  await rowEvent(win, seeded);
  await win.waitForTimeout(300);
  // ⚠ Share does not sit on the toolbar at this width — it is inside the "⋯"
  // overflow. Looking for the button on the toolbar found nothing and reported
  // a missing DIALOG, which is a different bug entirely.
  await win.evaluate(() => {
    // ⚠ VISIBLE buttons only. The toolbar keeps an aria-hidden measuring strip
    // that renders EVERY action so the fold calculation has real widths — so a
    // plain querySelectorAll finds a "Share / Permissions" button that is not
    // on screen and has no click handler. Clicking that one did nothing, and
    // the run reported a missing dialog instead of a missed button.
    const visible = (e) => e.getClientRects().length > 0 && !e.closest('[aria-hidden="true"]');
    const buttons = [...document.querySelectorAll('button')].filter(visible);
    const direct = buttons.find((x) => /Payla[sş] \/ [İI]zinler|Share \/ Permissions/i.test(x.textContent ?? ''));
    if (direct) { direct.click(); return; }
    // ⚠ The wide-mode overflow button carries no class of its own — only the
    // narrow one does (.fe-toolbar__more). Find it by the glyph it renders.
    buttons.find((x) => (x.textContent ?? '').trim() === '⋯')?.click();
  });
  await win.waitForTimeout(600);
  await win.evaluate(() => {
    // ⚠ Same trap as above, and it bites harder here: the measuring strip is
    // `visibility:hidden; height:0`, which still has client rects, so a
    // "is it visible" test that only measures boxes picks the handler-less
    // clone and the menu closes with nothing done.
    const visible = (e) => e.getClientRects().length > 0 && !e.closest('[aria-hidden="true"]');
    const item = [...document.querySelectorAll('button, li, [role="menuitem"]')]
      .filter(visible)
      .find((x) => /Payla[sş] \/ [İI]zinler|Share \/ Permissions/i.test(x.textContent ?? ''));
    item?.click();
  });
  await win.waitForTimeout(1500);

  const dialog = await win.evaluate(() => {
    const m = document.querySelector('.fx-perm-modal');
    if (!m) return { present: false };
    const cs = getComputedStyle(m);
    return {
      present: true,
      background: cs.backgroundColor,
      radius: cs.borderRadius,
      // A transparent background with no radius is the unstyled signature.
      styled: cs.backgroundColor !== 'rgba(0, 0, 0, 0)' && cs.borderRadius !== '0px',
    };
  });
  check('the share dialog opens', dialog.present === true);
  check('the share dialog is actually STYLED, not raw HTML', dialog.styled === true,
    `background=${dialog.background} radius=${dialog.radius}`);

  await win.screenshot({ path: path.join(REPO, 'desktop-shell-share.png') });
  await win.evaluate(() => {
    const x = [...document.querySelectorAll('button')].find((b) => /^[✕×]$/.test(b.textContent?.trim() ?? ''));
    x?.click();
  });
  await win.waitForTimeout(400);

  await win.screenshot({ path: path.join(REPO, 'desktop-shell.png') });
} catch (e) {
  check('flow completed', false, String(e && e.message).split('\n')[0]);
} finally {
  // Take the fixture away again — a test that leaves litter on a real storage
  // is a test nobody runs twice.
  if (seedToken && !process.env.FILEX_SEEDED_FILE) {
    await api('/api/files/manager?action=delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ items: [{ path: `${STORAGE}://${SEED_NAME}`, type: 'file' }] }),
    }, seedToken).catch(() => {});
  }
  await app.close().catch(() => {});
}

finish();
