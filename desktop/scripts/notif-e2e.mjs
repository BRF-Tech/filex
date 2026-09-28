// The bell and the account menu in the desktop window — measured in the app.
//
// 2026-09-27, owner: the app raised a native notification for every row and
// had nowhere inside its window to read one, mark it read or follow it, and
// its top bar had no account menu. The window now draws the web app's bell and
// avatar (packages/core NotificationBell / AccountMenu, through the explorer's
// `config.notifications` / `config.account`). This measures what a person
// would check, against a real server:
//
//   1. The top bar carries the bell and the avatar, and the "⋯" is folded into
//      the avatar at this width (one menu in the corner, as on the web).
//   2. A real write produces a real row, and the bell's count rises — handed
//      over by the main process's poll, not by a second loop in the window.
//   3. The bell opens, says what happened in words, and a click on the row
//      lands IN THE WINDOW: the folder, the row selected — and the server
//      stores it as read; the count drops.
//   4. "View all" opens the whole list over the files.
//   5. A NATIVE notification's click (the toast is captured in the main
//      process, never shown on the operator's screen) lands in the same place
//      and marks that row read on the server.
//   6. The avatar: who is signed in, "User settings", the admin door, the
//      file list's own rows the dialog does not carry, the version — and NO
//      Sign out (owner, 2026-09-27: an app setting, in ⚙ → Accounts).
//   7. "User settings" opens the web app's dialog IN this window: a profile
//      save and a bell switch reach the server with this window's credential,
//      the appearance switch repaints the file list, and the rows the app keeps
//      in ⚙ (language, clicks) or has no use for are not drawn.
//   8. Signing out from ⚙ → Accounts still forgets the account.
//
// ⚠ No OS-level input (harness rule): Playwright drives the page and the main
// process; `shell.openExternal` and `Notification#show` are replaced in the
// main process of THIS throwaway instance, so nothing reaches the desktop.
//
//   FILEX_SERVER / FILEX_EMAIL / FILEX_PASSWORD — an ADMIN on a server you own.
//   FILEX_STORAGE — a writable storage there. The script writes into a folder
//   of its own inside it and removes nothing else.
//   NOTIF_SHOTS — a directory for the screenshots (default: <tmp>/filex-notif-shots).

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { STORAGE, api, check, finish, launchApp, signIn, skipTour, sleep } from './lib/harness.mjs';

const RUN = Date.now();
const DIR = `bildirim-${RUN}`;
const FILE_A = `ölçüm-${RUN}-a.txt`;
const FILE_B = `ölçüm-${RUN}-b.txt`;
const SHOTS = process.env.NOTIF_SHOTS || path.join(os.tmpdir(), 'filex-notif-shots');
const LANG = process.env.NOTIF_LANG || 'en-US';
fs.mkdirSync(SHOTS, { recursive: true });

async function shot(win, name) {
  const file = path.join(SHOTS, `${LANG.slice(0, 2)}-${name}.png`);
  await win.screenshot({ path: file });
  console.log(`      shot: ${file}`);
}

/** The first-use tour opens ~1 s after the mount and swallows clicks. */
async function settleTour(win) {
  const tour = win.locator('.fe-tour');
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    if (await tour.count()) {
      await skipTour(win).catch(() => {});
      await tour.waitFor({ state: 'detached', timeout: 5000 }).catch(() => {});
      return;
    }
    await sleep(200);
  }
}

async function waitUntil(fn, timeout = 45_000, every = 300) {
  const deadline = Date.now() + timeout;
  let last;
  while (Date.now() < deadline) {
    last = await fn();
    if (last) return last;
    await sleep(every);
  }
  return last;
}

async function unreadOnServer(token) {
  const r = await api('/api/notifications/unread-count', {}, token);
  return (await r.json()).count;
}

async function rowFor(token, name) {
  const r = await api('/api/notifications?limit=20', {}, token);
  const b = await r.json();
  return (b.items ?? []).find((n) => JSON.stringify(n.meta ?? {}).includes(name) || String(n.target?.path ?? '').endsWith(name));
}

async function write(token, name) {
  const r = await api('/api/files/save-text', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: `${STORAGE}://${DIR}/${name}`, content: `measured ${RUN}\n` }),
  }, token);
  if (!r.ok) throw new Error(`save-text ${name}: ${r.status} ${await r.text()}`);
}

const bellCount = (win) =>
  win.evaluate(() => document.querySelector('[data-testid="notification-bell"] [data-testid="unread-badge"]')?.textContent?.trim() ?? '');

const selected = (win, qualified) =>
  win.evaluate((q) => {
    const norm = (p) => {
      const s = String(p ?? '');
      const at = s.indexOf('://');
      return at < 0 ? s : s.slice(0, at + 3) + s.slice(at + 3).replace(/^\/+/, '');
    };
    const row = [...document.querySelectorAll('[data-fe-path]')].find((el) => norm(el.getAttribute('data-fe-path')) === norm(q));
    return row ? row.getAttribute('aria-selected') === 'true' : null;
  }, qualified);

/** Every notification row inside `root`: its box holds its own content, and
 *  the next row starts below it. */
const rowsOverlap = (win, root) =>
  win.evaluate((sel) => {
    const rows = [...document.querySelectorAll(`${sel} [data-testid="notification-row"]`)];
    if (!rows.length) return { ok: false, detail: 'no rows' };
    const bad = [];
    rows.forEach((r, i) => {
      if (r.scrollHeight > r.clientHeight + 1) bad.push(`row ${i} cut: ${r.clientHeight}px box, ${r.scrollHeight}px content`);
      const next = rows[i + 1];
      if (next && next.getBoundingClientRect().top < r.getBoundingClientRect().bottom - 1) bad.push(`row ${i + 1} starts inside row ${i}`);
    });
    return { ok: bad.length === 0, detail: bad.slice(0, 3).join('; ') || `${rows.length} row(s)` };
  }, root);

const { app } = await launchApp({ lang: LANG });
try {
  // ── the main process of THIS instance: nothing leaves it ────────────────
  await app.evaluate(({ shell, Notification }) => {
    globalThis.__opened = [];
    shell.openExternal = async (url) => { globalThis.__opened.push(String(url)); };
    globalThis.__toasts = [];
    Notification.prototype.show = function show() { globalThis.__toasts.push(this); };
  });

  const { win, adminToken } = await signIn(app, { label: 'filex desktop — notif e2e' });
  // Every unread-count request the WINDOW makes (the main process's own go
  // through net.fetch and never appear here).
  const windowCounts = [];
  win.on('request', (r) => { if (/\/api\/notifications\/unread-count/.test(r.url())) windowCounts.push(Date.now()); });
  await win.setViewportSize?.({ width: 1440, height: 900 }).catch(() => {});
  await win.locator('filex-explorer .fe').waitFor({ timeout: 30_000 });
  await settleTour(win);

  // A folder of its own for this run.
  const mk = await api('/api/files/manager?action=newfolder', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: `${STORAGE}://`, name: DIR }),
  }, adminToken);
  check('the run has a folder of its own', mk.ok, `HTTP ${mk.status}`);

  // ── 1. the top bar ───────────────────────────────────────────────────────
  const bar = await waitUntil(() => win.evaluate(() => {
    const head = document.querySelector('filex-explorer .fe-toolbar__tail');
    if (!head) return null;
    const bell = head.querySelector('[data-testid="notification-bell"]');
    const avatar = head.querySelector('[data-testid="explore-account"]');
    if (!bell || !avatar) return null;
    return {
      bellFirst: !!(bell.compareDocumentPosition(avatar) & Node.DOCUMENT_POSITION_FOLLOWING),
      more: !!head.querySelector('[data-testid="drive-more"]'),
    };
  }), 20_000);
  check('the top bar carries the bell, then the avatar', !!bar && bar.bellFirst, JSON.stringify(bar));
  check('the "⋯" is folded into the avatar at this width (one menu in the corner)', !!bar && !bar.more);
  await shot(win, '01-topbar');

  // ── 2. a real row raises the count ───────────────────────────────────────
  const before = await unreadOnServer(adminToken);
  await write(adminToken, FILE_A);
  const rowA = await waitUntil(() => rowFor(adminToken, FILE_A), 15_000);
  check('the write produced a notification with a file target', rowA?.target?.kind === 'file', JSON.stringify(rowA?.target));
  const want = String(Math.min(99, before + 1));
  // ⚠ Up to two main-process polls (15 s): the first one after sign-in only
  // takes the baseline.
  const shown = await waitUntil(async () => ((await bellCount(win)) === want ? want : null), 50_000);
  check('the bell counts it — handed over by the main process', shown === want, `badge "${await bellCount(win)}", server ${before + 1}`);
  // No loop of its own: across two of the main process's polls (32 s) with
  // nothing happening, the window asks the server nothing.
  const quietFrom = windowCounts.length;
  await sleep(32_000);
  check('the window does not poll the count itself', windowCounts.length === quietFrom,
    `${windowCounts.length - quietFrom} unread-count request(s) from the window in 32 s`);
  await shot(win, '02-badge');

  // ── 3. open the bell, follow the row ─────────────────────────────────────
  await win.locator('[data-testid="notification-bell"]').click();
  const panel = win.locator('[data-testid="notification-panel"]');
  await panel.waitFor({ timeout: 10_000 });
  const firstRow = panel.locator('[data-testid="notification-row"]').first();
  await firstRow.waitFor({ timeout: 10_000 });
  const rowText = (await firstRow.innerText()).replace(/\s+/g, ' ');
  check('the row says what happened, in words', rowText.includes(FILE_A) && !/file\.uploaded/.test(rowText), rowText.slice(0, 120));
  check('a row with somewhere to go is a button', (await firstRow.getAttribute('data-clickable')) === 'yes');
  // ⚠ Measured, not assumed: the rows are drawn outside `.fe` (teleported),
  // where the HOST page's own rules reach them — this window styles every
  // <button> with a fixed height, and the rows were squeezed on top of each
  // other the first time this ran.
  const overlap = await rowsOverlap(win, '[data-testid="notification-panel"]');
  check('the rows do not overlap and nothing is cut off', overlap.ok, overlap.detail);
  await shot(win, '03-bell-open');
  await firstRow.click();
  const landedA = await waitUntil(() => selected(win, `${STORAGE}://${DIR}/${FILE_A}`), 15_000);
  check('the click lands in the window: the folder, the row selected', landedA === true);
  const readA = await waitUntil(async () => (await rowFor(adminToken, FILE_A))?.read_at ? true : null, 10_000);
  check('…and the server stores it as read', readA === true);
  const afterA = await waitUntil(async () => ((await bellCount(win)) === (before > 0 ? String(Math.min(99, before)) : '') ? true : null), 20_000);
  check('the count drops back', afterA === true, `badge "${await bellCount(win)}"`);
  await shot(win, '04-landed');

  // ── 4. view all ──────────────────────────────────────────────────────────
  await win.locator('[data-testid="notification-bell"]').click();
  await win.locator('[data-testid="notification-view-all"]').click();
  const screen = win.locator('[data-testid="notifications-screen"]');
  await screen.waitFor({ timeout: 10_000 }).catch(() => {});
  // The screen opens first and fetches its page after: wait for the rows,
  // not for the frame (the count read at once was 0 on one run in two).
  const screenRows = win.locator('[data-testid="notifications-screen-list"] [data-testid="notification-row"]');
  await screenRows.first().waitFor({ timeout: 10_000 }).catch(() => {});
  const listed = await screenRows.count();
  check('"View all" opens the whole list over the files', (await screen.count()) === 1 && listed >= 1, `${listed} row(s)`);
  const overlapAll = await rowsOverlap(win, '[data-testid="notifications-screen-list"]');
  check('…whose rows do not overlap either', overlapAll.ok, overlapAll.detail);
  await shot(win, '05-view-all');
  await win.locator('[data-testid="notifications-screen-close"]').click();

  // ── 5. the native notification ───────────────────────────────────────────
  // Leave the folder first, so a landing is a landing.
  await win.evaluate((s) => document.querySelector('filex-explorer')?.revealNotification?.({ kind: 'folder', storage: s, folder: '' }), STORAGE);
  await write(adminToken, FILE_B);
  const toast = await waitUntil(() => app.evaluate(() => (globalThis.__toasts || []).length), 50_000);
  const toastText = await app.evaluate(() => {
    const t = (globalThis.__toasts || []).at(-1);
    return t ? `${t.title} | ${t.body}` : '';
  });
  check('a native notification was raised (captured, never shown)', !!toast && toastText.includes(FILE_B), toastText);
  await app.evaluate(() => (globalThis.__toasts || []).at(-1)?.emit('click'));
  const landedB = await waitUntil(() => selected(win, `${STORAGE}://${DIR}/${FILE_B}`), 20_000);
  check('clicking it lands in the window, the row selected', landedB === true);
  const readB = await waitUntil(async () => (await rowFor(adminToken, FILE_B))?.read_at ? true : null, 10_000);
  check('…and marks THAT row read on the server', readB === true);

  // ── 6. the avatar: the person's INTERFACE settings, no Sign out ──────────
  // Owner, 2026-09-27: the avatar holds the person's settings; the app's own
  // are behind ⚙, and signing an account out of the app is one of those.
  await win.locator('[data-testid="explore-account"]').click();
  const menu = win.locator('[data-testid="explore-account-menu"]');
  await menu.waitFor({ timeout: 10_000 });
  const menuText = (await menu.innerText()).replace(/\s+/g, ' ');
  const rows = await menu.locator('.fx-acctmenu__item').evaluateAll((els) => els.map((e) => e.getAttribute('data-testid')));
  // The heading is the person as every screen names them (core personName):
  // the display name, else the login, else the address.
  const me = await (await api('/api/auth/me', {}, adminToken)).json();
  const who = [me.user?.display_name, me.user?.username, me.user?.email].find((v) => typeof v === 'string' && v.trim());
  const heading = (await menu.locator('.fx-acctmenu__who').innerText()).trim();
  check('the avatar says who is signed in', !!who && heading === who.trim(), `heading "${heading}", /api/auth/me "${who}"`);
  check('…offers User settings first, then the admin console', rows[0] === 'explore-user-settings' && rows[1] === 'explore-admin', rows.join(','));
  check('…then the file list’s own rows the dialog does not carry', rows.includes('explore-fe:tour') && rows.includes('explore-fe:shortcut-settings'), rows.join(','));
  check('…and no Sign out: that is an app setting (⚙ → Accounts)', !rows.some((r) => /signout/.test(r ?? '')) && !/Sign out|Çıkış yap|Oturumu kapat/.test(menuText), rows.join(','));
  check('…nor a row whose control is on screen or in the dialog (views, ⓘ, theme, density, time zone)',
    !rows.some((r) => /^explore-fe:(view-|inspector$|nav$|theme$|density$|timezone$)/.test(r ?? '')), rows.join(','));
  check('…with the server’s version at its foot', /filex v?\d+\.\d+\.\d+/.test(menuText), menuText.slice(-40));
  await shot(win, '06-account-menu');

  // ── 7. User settings, IN the window ──────────────────────────────────────
  const externalBefore = await app.evaluate(() => (globalThis.__opened || []).length);
  await menu.locator('[data-testid="explore-user-settings"]').click();
  const dlg = win.locator('[data-testid="user-settings-dialog"]');
  const dlgOpen = await dlg.waitFor({ state: 'visible', timeout: 10_000 }).then(() => true, () => false);
  const externalAfter = await app.evaluate(() => (globalThis.__opened || []).length);
  check('"User settings" opens the settings dialog in this window, not a browser', dlgOpen && externalAfter === externalBefore,
    `dialog=${dlgOpen} browser opens=${externalAfter - externalBefore}`);
  const tabs = await dlg.locator('[data-testid^="user-settings-tab-"]').evaluateAll((els) => els.map((e) => e.getAttribute('data-testid')));
  check('…with the web dialog’s five sections', tabs.length === 5, tabs.join(','));
  await shot(win, '07-settings-profile');

  // Profile: a real save, read back from the server.
  const newName = `Ölçüm Kişisi ${RUN % 1000}`;
  const nameBox = dlg.locator('.fx-us__grid input').first();
  await nameBox.fill(newName);
  await dlg.locator('[data-testid="user-settings-save-profile"]').click();
  const savedName = await waitUntil(async () => {
    const m = await (await api('/api/auth/me', {}, adminToken)).json();
    return m.user?.display_name === newName ? m.user.display_name : null;
  }, 10_000);
  check('a profile change is saved on the server, with the credential this window holds', savedName === newName, String(savedName));
  // The server's row can change before this window has read the answer to
  // its own request (the poll above races it), so the avatar gets the same
  // few seconds — and a stale name after them is a failure.
  const avatarName = () => win.evaluate(() => document.querySelector('[data-testid="explore-account"]')?.getAttribute('aria-label') ?? '');
  const headNow = (await waitUntil(async () => ((await avatarName()) === newName ? newName : null), 3000)) ?? (await avatarName());
  check('…and the avatar says the new name without a reload', headNow === newName, headNow);

  // Preferences: what every surface has, and none of what the app keeps in ⚙.
  await dlg.locator('[data-testid="user-settings-tab-preferences"]').click();
  const prefs = await win.evaluate(() => {
    const d = document.querySelector('[data-testid="user-settings-dialog"]');
    const has = (sel) => !!d?.querySelector(sel);
    return {
      timezone: has('[data-testid="user-settings-timezone"]'),
      appearance: has('[data-testid="user-settings-appearance"]'),
      density: has('[data-testid="user-settings-density"]'),
      folderView: has('[data-testid="user-settings-folder-view"]'),
      language: has('[data-testid="user-settings-locale"]'),
      openTrigger: has('[data-testid^="user-settings-opentrigger-"]'),
      startPage: has('[data-testid^="user-settings-start-"]'),
      downloads: has('[data-testid="user-settings-desktop-app"]'),
    };
  });
  check('Preferences carry time zone, appearance, compact list and the folder default',
    prefs.timezone && prefs.appearance && prefs.density && prefs.folderView, JSON.stringify(prefs));
  check('…and none of what the app keeps in ⚙ or has no use for (language, clicks, start page, downloads)',
    !prefs.language && !prefs.openTrigger && !prefs.startPage && !prefs.downloads, JSON.stringify(prefs));
  const modeBefore = await win.evaluate(() => document.querySelector('filex-explorer .fe')?.classList.contains('fe--theme-dark'));
  await dlg.locator(modeBefore ? '[data-testid="user-settings-theme-light"]' : '[data-testid="user-settings-theme-dark"]').click();
  const modeAfter = await waitUntil(() => win.evaluate(() => document.querySelector('filex-explorer .fe')?.classList.contains('fe--theme-dark')).then((d) => (d !== modeBefore ? 'flipped' : null)), 5000);
  check('the appearance switch repaints the file list behind the dialog', modeAfter === 'flipped');
  await shot(win, '08-settings-preferences');
  await dlg.locator(modeBefore ? '[data-testid="user-settings-theme-dark"]' : '[data-testid="user-settings-theme-light"]').click();

  // Notifications: the switch is the SERVER's.
  await dlg.locator('[data-testid="user-settings-tab-notifications"]').click();
  const inapp = dlg.locator('[data-testid="user-settings-inapp"]');
  await inapp.waitFor({ timeout: 5000 });
  const wasOn = (await inapp.getAttribute('aria-checked')) === 'true';
  await inapp.click();
  const serverPrefs = await waitUntil(async () => {
    const r = await (await api('/api/notifications/settings', {}, adminToken)).json();
    return r.in_app_enabled === !wasOn ? r : null;
  }, 10_000);
  check('the bell switch in the dialog is saved on the server', !!serverPrefs, JSON.stringify(serverPrefs));
  check('…and the browser-notification switch is not drawn in the app', (await dlg.locator('[data-testid="user-settings-browser"]').count()) === 0);
  await shot(win, '09-settings-notifications');
  await inapp.click();
  await waitUntil(async () => ((await (await api('/api/notifications/settings', {}, adminToken)).json()).in_app_enabled === wasOn ? true : null), 10_000);

  await dlg.locator('[data-testid="user-settings-tab-security"]').click();
  check('Security carries the password and two-factor forms',
    (await dlg.locator('input[autocomplete="current-password"]').count()) === 1 &&
      (await dlg.locator('[data-testid="user-settings-totp-enable"], [data-testid="user-settings-totp-disable"]').count()) === 1);
  await shot(win, '10-settings-security');
  await dlg.locator('[data-testid="user-settings-close"]').click();
  check('the dialog closes', await dlg.waitFor({ state: 'hidden', timeout: 5000 }).then(() => true, () => false));

  // A theme row is no longer in the avatar; the explorer's own rows still run
  // their own handler — the tour row opens the tour.
  await win.locator('[data-testid="explore-account"]').click();
  await menu.waitFor({ timeout: 10_000 });
  await menu.locator('[data-testid="explore-fe:tour"]').click();
  const tour = await waitUntil(() => win.evaluate(() => !!document.querySelector('.fe-tour')), 5000);
  check('an explorer row runs the explorer’s own handler', !!tour);
  await skipTour(win).catch(() => {});

  // ── 8. signing out: in the app's own Settings → Accounts ────────────────
  await win.evaluate(() => document.querySelectorAll('#rail .rail-btn')[1]?.click());
  const signOutBtn = win.locator('#settings [data-signout]');
  await signOutBtn.first().waitFor({ timeout: 10_000 });
  await signOutBtn.first().click();
  // The connect screen — a new window, or the sign-in window brought back.
  const next = await waitUntil(() => app.windows().find((w) => /^app:\/\/shell\/.*connect/.test(w.url())) ?? null, 20_000);
  const accounts = next
    ? await next.evaluate(async () => (await window.filexShell?.getState?.())?.accounts?.length ?? null).catch(() => null)
    : null;
  const explorerGone = !app.windows().some((w) => w.url().startsWith('app://filex'));
  check('Settings → Accounts → Sign out forgets the account and returns to the connect screen',
    !!next && accounts === 0 && explorerGone, `${next?.url()} accounts=${accounts} explorerGone=${explorerGone}`);
} catch (err) {
  check('the run completed', false, String(err?.stack || err));
} finally {
  await app.close().catch(() => {});
}
finish();
