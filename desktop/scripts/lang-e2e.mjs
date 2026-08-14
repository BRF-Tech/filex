// The language setting: does choosing one actually change the app?
//
// Before this the window followed the OS and there was nothing to choose, so
// somebody on an English Windows could not have a Turkish filex. The whole
// window is affected, not one label — the shell, the file explorer web
// component inside it (its own catalogue), and the tray menu built over in the
// main process — so a setting that only moves the parts the renderer happens to
// redraw is the failure worth testing for.
//
// Run: FILEX_EMAIL=… FILEX_PASSWORD=… node scripts/lang-e2e.mjs

import fs from 'node:fs';
import { DESKTOP, _electron, check, finish, launchApp, signIn, sleep } from './lib/harness.mjs';

// Launched with an ENGLISH OS locale on purpose: "it was already Turkish"
// cannot pass for a working switch.
const { app, profile, home } = await launchApp({ lang: 'en-US' });
const { win } = await signIn(app, { label: 'filex desktop — lang e2e' });

const state = () => win.evaluate(() => window.filexApp.getState());

// ── it starts by following the OS ────────────────────────────────────
const first = await state();
check('the language setting starts on "system"', (first.locale ?? 'system') === 'system', String(first.locale));
check('…which resolves to English on an English machine', first.effectiveLocale === 'en', String(first.effectiveLocale));

// ── open Settings the way a user does: the gear in the rail ──────────
await win.evaluate(() => {
  const gear = [...document.querySelectorAll('.rail-btn')].pop();
  gear?.click();
});
await sleep(600);
const choices = await win.evaluate(() =>
  [...document.querySelectorAll('#settings [data-locale]')].map((b) => b.dataset.locale));
check('Settings offers system / English / Türkçe', choices.join(',') === 'system,en,tr', choices.join(',') || 'yok');

// ── switch to Turkish ────────────────────────────────────────────────
await win.evaluate(() => document.querySelector('#settings [data-locale="tr"]')?.click());
await sleep(1500);

const afterTr = await state();
check('the choice is stored', afterTr.locale === 'tr', String(afterTr.locale));
check('…and the main process resolves it', afterTr.effectiveLocale === 'tr', String(afterTr.effectiveLocale));
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
check('the file list itself is in Turkish, not just its locale property',
  /Yeni Klasör|Dosya adı|AD/.test(listText),
  listText.split(/\n/).filter(Boolean).slice(0, 4).join(' · ') || 'boş');
// …and it changed in place: a language switch that throws you back to the root
// folder is its own bug.
check('…without remounting the explorer',
  (await win.evaluate(() => document.querySelectorAll('filex-explorer').length)) === 1);

// ── back to English, and the window follows again ────────────────────
await win.evaluate(() => document.querySelector('#settings [data-locale="en"]')?.click());
await sleep(1200);
const backEn = await win.evaluate(() => document.querySelector('#settings')?.innerText ?? '');
check('switching back is just as complete', /Settings/.test(backEn) && /Language/.test(backEn),
  backEn.split('\n').slice(0, 3).join(' · '));

// ── and the choice survives a restart ────────────────────────────────
await win.evaluate(() => document.querySelector('#settings [data-locale="tr"]')?.click());
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
check('the choice survives a restart on an English machine',
  restored.locale === 'tr' && restored.effectiveLocale === 'tr',
  `locale=${restored.locale} effective=${restored.effectiveLocale}`);

await again.close();
fs.rmSync(profile, { recursive: true, force: true });
fs.rmSync(home, { recursive: true, force: true });
finish();
