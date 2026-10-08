// The app's language IS the account's (#191, 2026-10-08).
//
// Run:  node --experimental-strip-types --test desktop/test/account-locale.test.ts
//
// ⚠ Red on the code before: the app kept a language of its own (Settings →
// Language: System / English / Türkçe, in this computer's state file), named
// it to the server on every bell poll (`lang=`) and to the sync engine
// (`--lang`), so the toast and the engine's messages could be in one language
// while the same person's phone, email and web bell were in another.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { accountLocaleOf, pinToAdopt, prefsWithLocale, uiLocaleFor } from '../src/account-locale.ts';

const here = path.dirname(fileURLToPath(import.meta.url));
const main = readFileSync(path.join(here, '..', 'src', 'main.ts'), 'utf8');
const html = readFileSync(path.join(here, '..', 'ui', 'app.html'), 'utf8');

test('the window draws the account language; the system speaks only with nobody signed in', () => {
  assert.equal(uiLocaleFor('tr', 'en-US'), 'tr');
  assert.equal(uiLocaleFor('en', 'tr-TR'), 'en', 'the account outranks the system');
  assert.equal(uiLocaleFor('tr-TR', 'en-US'), 'tr');
  // No account, or one that holds no language: the system's.
  assert.equal(uiLocaleFor(undefined, 'tr-TR'), 'tr');
  assert.equal(uiLocaleFor('', 'en-GB'), 'en');
  // A language a pack adds on the server: the window cannot draw it, so the
  // system's guess - the notifications and the engine still speak it.
  assert.equal(uiLocaleFor('es', 'tr-TR'), 'tr');
  assert.equal(uiLocaleFor('es', 'de-DE'), 'en');
});

test('the account language is read from /api/auth/me', () => {
  assert.equal(accountLocaleOf({ user: { locale: 'tr' } }), 'tr');
  assert.equal(accountLocaleOf({ user: { locale: ' es ' } }), 'es');
  for (const none of [{ user: {} }, { user: { locale: 7 } }, {}, null, undefined]) {
    assert.equal(accountLocaleOf(none), '', JSON.stringify(none));
  }
});

test('a language picked here is written as the WHOLE desktop document, with locale set', () => {
  // ⚠ The PUT replaces the document: the other preferences ride along.
  assert.deepEqual(prefsWithLocale({ surface: 'desktop', prefs: { theme: 'dark', locale: 'en' } }, 'tr'), {
    prefs: { theme: 'dark', locale: 'tr' },
  });
  assert.deepEqual(prefsWithLocale({ prefs: {} }, 'en'), { prefs: { locale: 'en' } });
  // The account's own key is not the document's.
  assert.deepEqual(prefsWithLocale({ prefs: { openWith: '{}', density: 'compact' } }, 'tr'), {
    prefs: { density: 'compact', locale: 'tr' },
  });
  for (const odd of [null, {}, { prefs: 'x' }, { prefs: [1] }]) {
    assert.deepEqual(prefsWithLocale(odd, 'en'), { prefs: { locale: 'en' } }, JSON.stringify(odd));
  }
});

test('an older install\'s pinned language goes to an account that holds none, and is forgotten either way', () => {
  assert.equal(pinToAdopt('tr', ''), 'tr');
  assert.equal(pinToAdopt('en', ''), 'en');
  assert.equal(pinToAdopt('tr', 'en'), null, 'an account that has a language keeps it');
  assert.equal(pinToAdopt('system', ''), null);
  assert.equal(pinToAdopt(undefined, ''), null);
});

test('the bell poll names no language: the server says the rows in the account language', () => {
  assert.doesNotMatch(main, /searchParams\.set\('lang'/);
  assert.match(main, /function effectiveLocale\(\): 'en' \| 'tr' \{\s*return uiLocaleFor\(activeAccount\(state\)\?\.locale, app\.getLocale\(\)\);/);
});

test('the sync engine is started without --lang and restarts when the account language changes', () => {
  // The engine asks the server for the account's language itself
  // (backend cmd/filex syncevents.go): one copy of the rule.
  assert.doesNotMatch(main, /function engineLang\(/);
  const prefs = /function currentWatchPrefs\(\): WatchPrefs \{([\s\S]*?)\n\}/.exec(main);
  assert.ok(prefs, 'currentWatchPrefs is where the watcher flags come from');
  assert.doesNotMatch(prefs[1], /\blang\b/);
  // A changed account language restarts that account's watchers.
  const apply = /function applyAccountLocale\(acc: Account, lang: string\): void \{([\s\S]*?)\n\}/.exec(main);
  assert.ok(apply);
  assert.match(apply[1], /supervisor\?\.stop\(acc\.id\)/);
  assert.match(apply[1], /refreshPairs\(\)/);
});

test('Settings → Language writes the account and offers no "System"', () => {
  const handler = /ipcMain\.handle\('settings:set'[\s\S]*?\n {2}\}\);/.exec(main);
  assert.ok(handler);
  assert.match(handler[0], /writeAccountLocale\(acc, patch\.locale\)/);
  assert.doesNotMatch(handler[0], /state\.locale = patch\.locale/);
  assert.doesNotMatch(html, /\['system', T\('set\.language_system'\)\]/);
  assert.match(html, /state\.effectiveLocale === code/);
});
