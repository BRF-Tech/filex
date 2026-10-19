import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const html = readFileSync(new URL('../ui/app.html', import.meta.url), 'utf8');
const wc = readFileSync(new URL('../../packages/webcomponent/src/index.ts', import.meta.url), 'utf8');
const main = readFileSync(new URL('../src/main.ts', import.meta.url), 'utf8');

test('the desktop initializes the same registry the bundled explorer reads before mounting', () => {
  // The value from core's SOURCE (the module the bundled FileExplorer.vue
  // imports), the type from core's package (task #275: re-exported from the
  // relative path, the function's source was rolled into dist/index.d.ts).
  assert.match(wc, /import \{ loadLocales as sourceLoadLocales \} from '\.\.\/\.\.\/core\/src\/lib\/uiLocales';/);
  assert.match(wc, /export const loadLocales: typeof coreLoadLocales = sourceLoadLocales;/);
  const mount = html.slice(html.indexOf('async function mountExplorer(account)'));
  assert.match(mount, /await filexWc\.loadLocales\?\.\(\{ base: account\.serverUrl, timeoutMs: 10000 \}\);\s*if \(mountedFor !== account\.id\) return;/);
  assert.ok(mount.indexOf('await filexWc.loadLocales') < mount.indexOf('window.filexApp.storages'));
  assert.doesNotMatch(html, /locale: LANG,/);
});

test('the language list cannot hold the window on "connecting": it is asked with a limit, and an older filex.js without it is skipped', async () => {
  const mount = html.slice(html.indexOf('async function mountExplorer(account)'));
  const stmt = /await filexWc\.loadLocales\?\.\([^;]*\);/.exec(mount);
  assert.ok(stmt, 'the loadLocales statement');
  const run = (filexWc: Record<string, unknown>) =>
    vm.runInContext(`(async () => { ${stmt[0]} return 'mounted'; })()`, vm.createContext({
      filexWc,
      account: { serverUrl: 'https://files.example' },
    })) as Promise<string>;
  // A development tree whose app/filex.js predates loadLocales: no TypeError.
  assert.equal(await run({}), 'mounted');
  // The limit reaches the loader (core gives up on the list after it and
  // answers with the built-in languages - web/tests/lib/explorerLocale.test.ts).
  let asked: { base?: string; timeoutMs?: number } | undefined;
  assert.equal(await run({ loadLocales: async (o: typeof asked) => { asked = o; return []; } }), 'mounted');
  // (Field by field: the object was made in the vm's realm.)
  assert.equal(asked?.base, 'https://files.example');
  assert.equal(asked?.timeoutMs, 10000);
});

test('pack language changes update both components even when native chrome stays English', () => {
  const explorer = { config: { locale: 'en', path: 'local://docs' } };
  const connections = { config: { locale: 'en' } };
  const state = { accountLocale: 'es' };
  const context = vm.createContext({
    state,
    document: { documentElement: { lang: 'en' }, querySelectorAll: () => [explorer, connections] },
  });
  const start = html.indexOf('      function explorerLocale()');
  assert.ok(start >= 0);
  vm.runInContext("let LANG = 'en';\n" + html.slice(start, html.indexOf('      const STRINGS', start)), context);
  assert.equal(vm.runInContext("applyLang('en')", context), false);
  assert.equal(explorer.config.locale, 'es');
  assert.equal(connections.config.locale, 'es');
  assert.equal(explorer.config.path, 'local://docs');
  state.accountLocale = 'zh';
  vm.runInContext("applyLang('en')", context);
  assert.equal(explorer.config.locale, 'zh');
  state.accountLocale = '';
  vm.runInContext("applyLang('tr')", context);
  assert.equal(explorer.config.locale, 'tr');
});

test('the settings bridge accepts pack language tags as well as built-in languages', () => {
  const handler = main.slice(main.indexOf("ipcMain.handle('settings:set'"));
  const guard = /if \((typeof patch.locale === 'string' && [^\n]+)\) \{/.exec(handler);
  assert.ok(guard);
  const accepts = new Function('patch', `return ${guard[1]}`);
  for (const locale of ['en', 'tr', 'es', 'zh', 'pt-br', 'zh-Hant']) assert.equal(accepts({ locale }), true);
  for (const locale of ['', '../es', '<es>', null, 7]) assert.equal(accepts({ locale }), false);
});
