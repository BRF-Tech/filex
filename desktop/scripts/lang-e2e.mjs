// The language setting: does choosing one actually change the app - and the
// ACCOUNT?
//
// ⚠⚠ 0.54 (#191): the app has no language of its own any more. The window,
// the tray menu, the file list inside the window and the sync engine's
// messages follow the signed-in ACCOUNT's language (users.locale, read from
// /api/auth/me), and Settings → Language writes the account on the server
// (src/main.ts settings:set → writeAccountLocale), so the web panel and every
// notification follow it too. There is no "System" choice: with nobody signed
// in - or an account whose token the server no longer takes - the app speaks
// the system's language and the two buttons are disabled.
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

const openSettings = (w) =>
  w.evaluate(() => {
    const gear = [...document.querySelectorAll('.rail-btn')].pop();
    gear?.click();
  });
const pick = (w, code) => w.evaluate((c) => document.querySelector(`#settings [data-locale="${c}"]`)?.click(), code);

// ── it starts in the account's language ──────────────────────────────
const startLocale = await serverLocale();
await sleep(1500);
const first = await state();
const want = ['en', 'tr'].includes(startLocale.split(/[-_]/)[0]) ? startLocale.split(/[-_]/)[0] : 'en';
check('the window draws the account’s language (the system’s when it holds none the window draws)',
  first.effectiveLocale === want, `account=${startLocale || '(none)'} effective=${first.effectiveLocale}`);
check('the app keeps no language of its own', (first.locale ?? 'system') === 'system', String(first.locale));

// ── open Settings the way a user does: the gear in the rail ──────────
await openSettings(win);
await sleep(600);
const choices = await win.evaluate(() =>
  [...document.querySelectorAll('#settings [data-locale]')].map((b) => b.dataset.locale));
check('Settings offers English / Türkçe and no "System"', choices.join(',') === 'en,tr', choices.join(',') || 'yok');
check('…enabled while an account is signed in',
  await win.evaluate(() => [...document.querySelectorAll('#settings [data-locale]')].every((b) => !b.disabled)));

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

const shell = await win.evaluate(() => document.querySelector('#settings')?.innerText ?? '');
check('the shell is in Turkish', /Ayarlar/.test(shell) && /Dil/.test(shell),
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
const backEn = await win.evaluate(() => document.querySelector('#settings')?.innerText ?? '');
check('switching back is just as complete', /Settings/.test(backEn) && /Language/.test(backEn),
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
await openSettings(win2);
await sleep(600);
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
// Settings drawn again from the state as it is now: closed if it is open,
// then opened with the gear (which toggles).
await win2.evaluate(() => document.querySelector('#settings.open #close-settings')?.click());
await sleep(300);
await openSettings(win2);
await sleep(800);
const offered = await win2.evaluate(() =>
  [...document.querySelectorAll('#settings [data-locale]')].map((b) => ({ code: b.dataset.locale, disabled: b.disabled })));
check('…and both language buttons are disabled', offered.length === 2 && offered.every((b) => b.disabled),
  JSON.stringify(offered));
const why = await win2.evaluate(() => document.querySelector('#settings')?.innerText ?? '');
check('…saying why: the language belongs to the account', /Sign in to choose|Seçmek için giriş yap/.test(why),
  why.split('\n').find((l) => /Language|Dil/.test(l)) ?? '');

await again.close();
fs.rmSync(profile, { recursive: true, force: true });
fs.rmSync(home, { recursive: true, force: true });
finish();
