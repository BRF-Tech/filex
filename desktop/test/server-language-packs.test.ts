import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const html = readFileSync(new URL('../ui/app.html', import.meta.url), 'utf8');
const wc = readFileSync(new URL('../../packages/webcomponent/src/index.ts', import.meta.url), 'utf8');
const main = readFileSync(new URL('../src/main.ts', import.meta.url), 'utf8');

test('the desktop initializes the same registry the bundled explorer reads before mounting', () => {
  assert.match(wc, /export \{ loadLocales \} from '\.\.\/\.\.\/core\/src\/lib\/uiLocales'/);
  const mount = html.slice(html.indexOf('async function mountExplorer(account)'));
  assert.match(mount, /await filexWc\.loadLocales\(\{ base: account\.serverUrl \}\);\s*if \(mountedFor !== account\.id\) return;/);
  assert.ok(mount.indexOf('await filexWc.loadLocales') < mount.indexOf('window.filexApp.storages'));
  assert.doesNotMatch(html, /locale: LANG,/);
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
