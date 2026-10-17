// The language setting: does choosing one actually change the app - and the
// ACCOUNT?
//
// ⚠⚠ 0.54 (#191): the app has no language of its own any more. The window,
// the tray menu, the file list inside the window and the sync engine's
// messages follow the signed-in ACCOUNT's language (users.locale, read from
// /api/auth/me), and the language control writes the account on the server
// (src/main.ts settings:set → writeAccountLocale), so the web panel and every
// notification follow it too. There is no "System" choice: with nobody signed
// in - or an account whose token the server no longer takes - the app speaks
// the system's language and there is no control to pick another.
//
// ⚠ Task #258: the control is the user settings dialog's (the avatar → User
// settings → Preferences → Language), the same one the web app draws; App
// settings (⚙) carries no language row any more. The window carries the pick
// out through the dialog's hook (ui/app.html pickLanguage).
//
// The whole window is affected, not one label — the shell, the file explorer
// web component inside it (its own catalogue), and the tray menu built over in
// the main process — so a setting that only moves the parts the renderer
// happens to redraw is the failure worth testing for.
//
// ⚠ It changes the test account's language on the server, and puts back the
// one it found before it signs the app out.
//
// Run: FILEX_EMAIL=… FILEX_PASSWORD=… node scripts/lang-e2e.mjs

import fs from 'node:fs';
import { DESKTOP, _electron, api, check, finish, launchApp, signIn, sleep } from './lib/harness.mjs';

const LABEL = 'filex desktop — lang e2e';

// Launched with an ENGLISH OS locale on purpose: "it was already Turkish"
// cannot pass for a working switch.
const { app, profile, home } = await launchApp({ lang: 'en-US' });
const { win, adminToken } = await signIn(app, { label: LABEL });

const state = () => win.evaluate(() => window.filexApp.getState());

/** users.locale as the SERVER holds it ('' = none), read with the browser session. */
async function serverLocale() {
  const res = await api('/api/auth/me', { headers: { Accept: 'application/json' } }, adminToken);
  if (!res.ok) return `(GET /api/auth/me: ${res.status})`;
  const me = await res.json();
  return typeof me?.user?.locale === 'string' ? me.user.locale.trim() : '';
}

/** The user settings dialog on Preferences, opened the way a person does:
 *  the avatar, then "User settings". */
const openPreferences = (w) =>
  w.evaluate(async () => {
    const wait = (ms) => new Promise((r) => setTimeout(r, ms));
    if (!document.querySelector('[data-testid="user-settings-dialog"]')) {
      document.querySelector('[data-testid="explore-account"]')?.click();
      await wait(400);
      document.querySelector('[data-testid="explore-user-settings"]')?.click();
      await wait(600);
    }
    document.querySelector('[data-testid="user-settings-tab-preferences"]')?.click();
    await wait(300);
  });
const pick = async (w, code) => {
  await openPreferences(w);
  await w.evaluate((c) => document.querySelector(`[data-testid="user-settings-locale-${c}"]`)?.click(), code);
};
const localeButtons = (w) =>
  w.evaluate(() =>
    [...document.querySelectorAll('[data-testid^="user-settings-locale-"]')].map((b) => b.getAttribute('data-testid').slice('user-settings-locale-'.length)));

// ── it starts in the account's language ──────────────────────────────
const startLocale = await serverLocale();
await sleep(1500);
const first = await state();
const want = ['en', 'tr'].includes(startLocale.split(/[-_]/)[0]) ? startLocale.split(/[-_]/)[0] : 'en';
check('the window draws the account’s language (the system’s when it holds none the window draws)',
  first.effectiveLocale === want, `account=${startLocale || '(none)'} effective=${first.effectiveLocale}`);
check('the app keeps no language of its own', (first.locale ?? 'system') === 'system', String(first.locale));

// ── open User settings the way a user does: the avatar ───────────────
await openPreferences(win);
await sleep(600);
const choices = await localeButtons(win);
check('User settings offers English / Türkçe and no "System"', choices.includes('en') && choices.includes('tr') && !choices.includes('system'),
  choices.join(',') || 'yok');
check('…and App settings (⚙) has no language row of its own any more',
  await win.evaluate(() => !document.querySelector('#settings [data-locale]')));

// Start from English, so the switch below is a real one.
if (first.effectiveLocale !== 'en') {
  await pick(win, 'en');
  await sleep(1500);
}

// ── switch to Turkish ────────────────────────────────────────────────
await pick(win, 'tr');
await sleep(1500);

const afterTr = await state();
// accountLocale moves only once the server took the write (applyAccountLocale
// runs after writeAccountLocale answered true).
check('the choice is written to the account', afterTr.accountLocale === 'tr', String(afterTr.accountLocale));
check('…the server holds it (users.locale)', (await serverLocale()) === 'tr', await serverLocale());
check('…and the main process draws it', afterTr.effectiveLocale === 'tr', String(afterTr.effectiveLocale));
check('…with no language of the app’s own stored beside it', (afterTr.locale ?? 'system') === 'system', String(afterTr.locale));
check('the document declares the language it is in',
  (await win.evaluate(() => document.documentElement.lang)) === 'tr',
  await win.evaluate(() => document.documentElement.lang));

const shell = await win.evaluate(() => [
  document.querySelector('[data-testid="user-settings-dialog"]')?.innerText ?? '',
  [...document.querySelectorAll('.rail-btn')].pop()?.title ?? '',
].join('\n'));
check('the shell is in Turkish: the dialog, and the rail’s App settings', /Kullanıcı ayarları/.test(shell) && /Dil/.test(shell) && /Uygulama ayarları/.test(shell),
  shell.split('\n').slice(0, 3).join(' · '));

// ⚠⚠ The explorer is a separate component with its own catalogue, and this
// assertion reads its RENDERED TEXT rather than its `locale` property. The
// first version of this check asked the element what its locale was, got
// 'tr', and passed — while the file list on screen was still in English,
// because the component merges `{...attributes, ...config}` and the config
// property set at mount time won. A property is not a screen.
const listText = await win.evaluate(() => document.querySelector('filex-explorer')?.innerText ?? '');
// ⚠ Anchored on the LISTING'S COLUMN HEADERS, not on a button. The first
// version looked for "Yeni Klasör" / "Dosya adı" / "AD" — and gorunum:v1/v2
// took all three away: the drive shell replaced the New Folder button with a
// "+ Yeni" menu, and the headers stopped being upper-cased, so `AD\b` no longer
// matched "Ad". The check then reported "the file list is not in Turkish"
// against a file list that was entirely in Turkish (measured 2026-09-12).
// Owner and Size are drawn by every profile, in every layout, and there is no
// listing without them.
check('the file list itself is in Turkish, not just its locale property',
  /Yeni Klasör|Dosya adı|\bSahibi\b|\bBoyut\b/.test(listText),
  listText.split(/\n/).filter(Boolean).slice(0, 4).join(' · ') || 'boş');
// …and it changed in place: a language switch that throws you back to the root
// folder is its own bug.
check('…without remounting the explorer',
  (await win.evaluate(() => document.querySelectorAll('filex-explorer').length)) === 1);

// ── back to English, and the window follows again ────────────────────
await pick(win, 'en');
await sleep(1200);
const backEn = await win.evaluate(() => document.querySelector('[data-testid="user-settings-dialog"]')?.innerText ?? '');
check('switching back is just as complete', /User settings/.test(backEn) && /Language/.test(backEn),
  backEn.split('\n').slice(0, 3).join(' · '));
check('…and the account follows it back', (await serverLocale()) === 'en', await serverLocale());

// ── the account's language survives a restart on an English machine ──
await pick(win, 'tr');
await sleep(1000);
await app.close();

// Same profile AND same home: the state file lives under the profile, and the
// harness makes a fresh pair per launch — reusing them is what makes this a
// restart rather than a first run.
const again = await _electron.launch({
  args: [DESKTOP, `--user-data-dir=${profile}`, '--lang=en-US'],
  cwd: DESKTOP,
  env: { ...process.env, FILEX_NO_BROWSER: '1', FILEX_NO_UPDATE: '1', HOME: home, USERPROFILE: home },
});
const win2 = await again.firstWindow();
await sleep(2500);
const restored = await win2.evaluate(() => window.filexApp.getState());
check('the account’s language survives a restart on an English machine',
  restored.accountLocale === 'tr' && restored.effectiveLocale === 'tr',
  `account=${restored.accountLocale} effective=${restored.effectiveLocale}`);

// ── the account gets back the language it had ────────────────────────
await sleep(1500);
if (startLocale === 'en' || startLocale === 'tr') {
  await pick(win2, startLocale);
  await sleep(1200);
} else {
  // A language this window does not draw (a pack's), or none: through the
  // profile, the way the web panel writes it.
  await api('/api/auth/profile', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ locale: startLocale }),
  }, adminToken);
}
check('the test account has its own language back', (await serverLocale()) === startLocale,
  `${await serverLocale()} (was ${startLocale || '(none)'})`);

// ── an account the server no longer takes cannot choose ──────────────
// Its token revoked (the app's own, found by the label it signed in with),
// the next request the app makes is refused and the account is marked signed
// out; the language is the account's, so there is nothing to write to.
const tokens = await api('/api/tokens', { headers: { Accept: 'application/json' } }, adminToken)
  .then((r) => (r.ok ? r.json() : { tokens: [] }))
  .catch(() => ({ tokens: [] }));
const mine = (tokens.tokens ?? []).filter((t) => t.label === LABEL);
check('the app’s token is found by its label', mine.length > 0, `${mine.length} token(s)`);
for (const t of mine) await api(`/api/tokens/${t.id}`, { method: 'DELETE' }, adminToken);
await win2.evaluate(async () => {
  const st = await window.filexApp.getState();
  await window.filexApp.storages(st.activeId).catch(() => null);
});
await sleep(1500);
const out = await win2.evaluate(() => window.filexApp.getState());
const acc = (out.accounts ?? []).find((a) => a.id === out.activeId);
check('the refused account is marked signed out', !!acc?.signedOut, JSON.stringify(acc?.signedOut ?? null));
// The language is the account's: a signed-out account has no file list, so
// no avatar and no User settings to pick one in - and App settings never had
// a language row of its own since task #258.
const offered = await localeButtons(win2);
check('…and nothing offers a language to pick', offered.length === 0, JSON.stringify(offered));

await again.close();
fs.rmSync(profile, { recursive: true, force: true });
fs.rmSync(home, { recursive: true, force: true });
finish();
