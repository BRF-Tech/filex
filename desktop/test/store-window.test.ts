// #162 - the store screen ("App store" / "Uygulama mağazası") in the desktop
// app, the owner's decision of 2026-10-06: the same screen as the web app,
// under the same rule.
//
// What has to stay true:
//   · the window does NOT decide whether the row is drawn: it says it has the
//     page (`appStorePage: true`) and the explorer applies the one rule every
//     host gets (packages/core lib/appStoreRow: a person, and the server shows
//     them the screen - web/tests/lib/appStoreRow.test.ts). No desktop code
//     asks `/api/app-store` itself, and no second condition is written here;
//   · pressing the row opens the server's own `app-store` page - the SPA route
//     the web app opens - in a window of the app, the way a document opens
//     (makeDocumentWindow: the credential from the header injector, no bridge
//     into the page), for the account the window is mounted for;
//   · one store window per account, and its title is said in both languages.
//
// Run:  node --experimental-strip-types --test desktop/test/store-window.test.ts

import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const DESKTOP = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const REPO = path.join(DESKTOP, '..');
const read = (...p: string[]) => readFileSync(path.join(...p), 'utf8');
const MAIN = read(DESKTOP, 'src', 'main.ts');
const APP = read(DESKTOP, 'ui', 'app.html');
const PRELOAD = read(DESKTOP, 'src', 'preload-app.cts');

test('the window says it has the store screen, and decides nothing else', () => {
  assert.match(APP, /appStorePage: true,/);
  // Every desktop source, the window page included: none asks the server
  // whether to draw the row - that is the explorer's one rule.
  const sources = [
    APP,
    ...readdirSync(path.join(DESKTOP, 'src'))
      .filter((f) => /\.(ts|cts)$/.test(f))
      .map((f) => read(DESKTOP, 'src', f)),
  ];
  for (const src of sources) {
    assert.doesNotMatch(src, /\/api\/app-store/, 'a desktop file asks /api/app-store itself');
    assert.doesNotMatch(src, /appStoreVisible/, 'a desktop file decides the row itself');
  }
  // The rule the explorer applies, for the desktop as for the web.
  const explorer = read(REPO, 'packages', 'core', 'src', 'FileExplorer.vue');
  assert.match(explorer, /appStoreRowShown\(props\.config\.appStorePage === true, callerIsApp\.value/);
});

test('pressing the row opens the store window for the mounted account', () => {
  // <filex-explorer> forwards the event the window listens to.
  const wc = read(REPO, 'packages', 'webcomponent', 'src', 'index.ts');
  assert.match(wc, /onOpenAppStore: \(\) => emit\('open-app-store'\)/);
  assert.match(APP, /explorer\.addEventListener\('open-app-store', \(\) => \{\s*void window\.filexApp\.openStore\(account\.id\);/);
  assert.match(PRELOAD, /openStore: \(id: string\) => ipcRenderer\.invoke\('account:openStore', id\)/);
  assert.match(MAIN, /ipcMain\.handle\('account:openStore', \(_e, id: string\) => \{[\s\S]*?openStoreWindow\(acc\);/);
});

test('the store window is the server page the web app opens, in a document window', () => {
  // The SPA's people's door and its `app-store` route.
  assert.match(MAIN, /serverUrl\(acc\.serverUrl, '\/drive\/app-store'\)/);
  const router = read(REPO, 'web', 'src', 'router', 'index.ts');
  assert.match(router, /path: '\/app-store',\s*\r?\n\s*name: 'app-store',/);
  // The document window's shell: the header injector's credential, no
  // filexApp bridge into a remote page.
  const fn = MAIN.slice(MAIN.indexOf('function openStoreWindow('));
  const body = fn.slice(0, fn.indexOf('\n}\n'));
  assert.match(body, /makeDocumentWindow\(acc, storeRouteUrl\(acc\)/);
  // One per account: a second press brings it forward.
  assert.match(body, /storeWindows\.get\(acc\.id\)/);
  assert.match(body, /focusWindow\(open\)/);
});

test('the store window is titled in both languages', () => {
  assert.match(MAIN, /const STORE_STRINGS: Bilingual = \{\s*title: \['App store', 'Uygulama mağazası'\],/);
});
