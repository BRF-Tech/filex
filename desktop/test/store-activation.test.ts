// When store-e2e's activation checks may only warn (scripts/lib/store-activation.mjs).
//
// Run:  node --experimental-strip-types --test desktop/test/store-activation.test.ts
//
// ⚠ The rule is narrow on purpose: a warning only on CI AND only while nothing
// goes to the Microsoft Store. On a developer machine, or on a run that uploads
// the package, a failed activation is a failed release.
//
// The two window-watch checks ("no window came on screen") are the one
// exception to the second half: they guard a person's screen, so on CI they
// only warn even when the package is uploaded — and on a desktop they fail.

import assert from 'node:assert/strict';
import test from 'node:test';

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { accountVerdict, activationWaitFactor, softActivation, softWindowChecks, storeLogDirs, windowVerdicts } from '../scripts/lib/store-activation.mjs';

test('a developer machine never softens a failure', () => {
  assert.equal(softActivation({}), false);
  assert.equal(softActivation({ STORE_E2E_STRICT: '0' }), false);
  assert.equal(softActivation({ CI: 'false' }), false);
});

test('CI without a Store upload warns instead of failing', () => {
  assert.equal(softActivation({ CI: 'true' }), true);
  assert.equal(softActivation({ CI: 'true', STORE_E2E_STRICT: '0' }), true);
  assert.equal(softActivation({ CI: 'true', STORE_E2E_STRICT: '' }), true);
});

test('CI that uploads to the Store keeps every activation check a failure', () => {
  assert.equal(softActivation({ CI: 'true', STORE_E2E_STRICT: '1' }), false);
});

test('CI waits longer for a cold runner, a desktop does not', () => {
  assert.equal(activationWaitFactor({}), 1);
  assert.equal(activationWaitFactor({ CI: 'true' }), 3);
});

// ── the window watch: a person's screen, not a runner's ────────────────────

const appWindow = { at: '14:18:32.343', kind: 'hidden', pid: '46776', cls: 'Chrome_WidgetWin_1', size: '720x620', cloaked: '0', title: 'filex - Connect' };
const shownWindow = { ...appWindow, kind: 'visible', onScreen: true };
const clean = { ran: true, error: '', onScreen: [], hidden: [appWindow] };
const watchFailed = { ran: false, error: 'Add-Type : Cannot add type. Compilation errors occurred.', onScreen: [], hidden: [] };

test('on CI a watch that could not run only warns, both checks', () => {
  for (const env of [{ CI: 'true' }, { GITHUB_ACTIONS: 'true' }, { CI: 'true', GITHUB_ACTIONS: 'true' }]) {
    const v = windowVerdicts(watchFailed, env);
    assert.equal(v.length, 2);
    for (const x of v) {
      assert.equal(x.ok, false, x.name);
      assert.equal(x.soft, true, `${x.name} must only warn on CI (${JSON.stringify(env)})`);
      assert.match(x.detail, /did not run to the end: Add-Type/);
    }
  }
});

test('on CI even a window on the screen only warns — nobody is looking at it', () => {
  const [onScreen] = windowVerdicts({ ...clean, onScreen: [shownWindow] }, { CI: 'true' });
  assert.equal(onScreen.ok, false);
  assert.equal(onScreen.soft, true);
});

test('a Store upload on CI does not make the screen checks strict', () => {
  assert.equal(softWindowChecks({ CI: 'true', STORE_E2E_STRICT: '1' }), true);
  // …while the activation checks do turn strict there, as before.
  assert.equal(softActivation({ CI: 'true', STORE_E2E_STRICT: '1' }), false);
});

test('on a developer machine (the release pretag) both stay failures', () => {
  for (const env of [{}, { CI: 'false' }, { GITHUB_ACTIONS: '' }, { STORE_E2E_STRICT: '0' }]) {
    assert.equal(softWindowChecks(env), false, JSON.stringify(env));
    for (const x of windowVerdicts(watchFailed, env)) {
      assert.equal(x.ok, false);
      assert.equal(x.soft, false, `${x.name} must fail on a desktop (${JSON.stringify(env)})`);
    }
  }
  const [onScreen] = windowVerdicts({ ...clean, onScreen: [shownWindow] }, {});
  assert.equal(onScreen.ok, false);
  assert.equal(onScreen.soft, false);
  assert.match(onScreen.detail, /visible Chrome_WidgetWin_1 720x620 "filex - Connect"/);
});

test('a clean watch passes both, and a watch that saw no app window proves nothing', () => {
  assert.deepEqual(windowVerdicts(clean, {}).map((x) => x.ok), [true, true]);
  const blind = windowVerdicts({ ...clean, hidden: [{ ...appWindow, title: 'Default IME' }] }, {});
  assert.deepEqual(blind.map((x) => x.ok), [true, false]);
  assert.equal(blind[1].detail, 'no window of the app was seen at all');
});

test('store-e2e reports the window watch through these verdicts', () => {
  const dir = path.dirname(fileURLToPath(import.meta.url));
  const src = fs.readFileSync(path.join(dir, '..', 'scripts', 'store-e2e.mjs'), 'utf8');
  assert.match(src, /for \(const v of windowVerdicts\(seen, process\.env\)\) \{\s*softCheck\(v\.name, v\.ok, v\.detail, v\.soft,/);
  // …and nowhere else: no second, strict copy of either check.
  assert.equal(src.match(/no window of the Store copy came on screen/g), null);
  assert.equal(src.match(/kept off screen'/g), null);
});

// ── who ran it (task #248) ─────────────────────────────────────────────────
//
// GitHub's runner account is the built-in Administrator (RID 500), and no
// activation check ever passed there: the diagnosis now says so, and prints
// the account, its SID, its token and the UAC policy on every run.

const whoami = (user: string, sid: string, label: string) =>
  [
    `"${user}","${sid}"`,
    '',
    '"Everyone","Well-known group","S-1-1-0","Mandatory group, Enabled by default, Enabled group"',
    `"${label}","Label","S-1-16-12288",""`,
  ].join('\r\n');

test('the built-in Administrator with a full token is named as the reason a package does not start', () => {
  const v = accountVerdict(whoami('fv-az123\\runneradmin', 'S-1-5-21-1-2-3-500', 'Mandatory Label\\High Mandatory Level'), { EnableLUA: '1' });
  assert.equal(v.sid, 'S-1-5-21-1-2-3-500');
  assert.equal(v.builtinAdmin, true);
  assert.equal(v.integrity, 'high');
  assert.equal(v.elevated, true);
  assert.match(v.lines[0], /^account fv-az123\\runneradmin S-1-5-21-1-2-3-500 \(RID 500: the built-in Administrator\)$/);
  assert.match(v.lines[2], /FilterAdministratorToken=unset \(0\)/);
  assert.match(v.lines.at(-1)!, /^verdict: this is the built-in Administrator .* \(actions\/runner-images#9537\)\. Run the check as another account/);
  // With FilterAdministratorToken=1 Windows gives it a filtered token: not the cause.
  assert.doesNotMatch(accountVerdict(whoami('x\\a', 'S-1-5-21-1-2-3-500', 'Mandatory Label\\Medium Mandatory Level'), { EnableLUA: '1', FilterAdministratorToken: '1' }).lines.at(-1)!, /built-in Administrator/);
});

test('a second administrator under UAC passes the account, and UAC off is named', () => {
  const ok = accountVerdict(whoami('lab\\e2e', 'S-1-5-21-1-2-3-1001', 'Mandatory Label\\Medium Mandatory Level'), { EnableLUA: '1', ConsentPromptBehaviorAdmin: '5' });
  assert.equal(ok.builtinAdmin, false);
  assert.equal(ok.integrity, 'medium');
  assert.equal(ok.elevated, false);
  assert.match(ok.lines.at(-1)!, /^verdict: the account and its token allow a packaged app to start/);
  const noUac = accountVerdict(whoami('lab\\e2e', 'S-1-5-21-1-2-3-1001', 'Mandatory Label\\High Mandatory Level'), { EnableLUA: '0' });
  assert.match(noUac.lines.at(-1)!, /UAC is off \(EnableLUA=0\)/);
  // Nothing to read: said, never a guess.
  assert.equal(accountVerdict('', {}).sid, '?');
});

test("the diagnosis reads the Store copy's own log folder first, then the redirected userData", () => {
  const dirs = storeLogDirs({ home: path.join('C:', 'Users', 'e2e'), localAppData: path.join('C:', 'Users', 'e2e', 'AppData', 'Local'), pfn: 'Pkg_abc', userDataName: 'filex-store-e2e' });
  assert.deepEqual(dirs, [
    path.join('C:', 'Users', 'e2e', '.filex', 'desktop', 'logs'),
    path.join('C:', 'Users', 'e2e', 'AppData', 'Local', 'Packages', 'Pkg_abc', 'LocalCache', 'Roaming', 'filex-store-e2e', 'logs'),
  ]);
  const dir = path.dirname(fileURLToPath(import.meta.url));
  const src = fs.readFileSync(path.join(dir, '..', 'scripts', 'store-e2e.mjs'), 'utf8');
  assert.match(src, /for \(const logs of storeLogDirs\(\{ home: os\.homedir\(\),/);
  // The account is said at the start of every run, and again in a diagnosis.
  assert.match(src, /for \(const l of accountLines\(\)\) console\.log/);
  assert.match(src, /console\.log\(`--- account and token\\n\$\{accountLines\(\)\.join\('\\n'\)\}`\)/);
  assert.match(src, /execFileSync\('whoami\.exe', \['\/user', '\/groups', '\/fo', 'csv', '\/nh'\], \{ encoding: 'utf8', windowsHide: true \}\)/);
});
