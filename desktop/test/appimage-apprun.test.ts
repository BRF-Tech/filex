// The AppImage's AppRun (0.55, #68).
//
// Run:  node --experimental-strip-types --test desktop/test/appimage-apprun.test.ts
//
// electron-builder 26 writes its own AppRun, and that one starts the app with
// --no-sandbox in front of the person's arguments whenever `unshare -Ur true`
// fails: on Ubuntu 23.10 and later, an AppImage with no AppArmor profile. The
// launcher (build/linux/launcher.sh) passes a --no-sandbox through as the
// person's decision, so the image would open with Chromium's sandbox off
// where every AppImage since 0.50 refused and said what to do (the release's
// `refuses --exe "$img"` check, on Ubuntu 24.04). electron-builder 24's AppRun
// added nothing; build/linux/AppRun.sh is that one, and
// scripts/appimage-apprun.cjs puts it in the packed app, which electron-builder
// copies over its own.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (rel: string) => fs.readFileSync(path.join(ROOT, rel), 'utf8');
const YML = read('electron-builder.yml');
const require = createRequire(import.meta.url);
type Target = { name: string };
const step = require('../scripts/appimage-apprun.cjs') as ((c: unknown) => Promise<void>) & {
  placeAppRun(dir: string, name: string, targets: Target[]): { out: string; written: boolean };
  appRunText(name: string): string;
};

/** Uncommented lines only. */
const code = (text: string) =>
  text
    .split('\n')
    .filter((l) => !/^\s*#/.test(l))
    .join('\n');

/** The lines of one top-level YAML block (`deb:`, `rpm:` …). */
function yamlBlock(key: string): string {
  const m = new RegExp(`^${key}:\\n((?:[ \\t].*\\n|\\s*\\n)*)`, 'm').exec(YML);
  assert.ok(m, `electron-builder.yml has no top-level "${key}:" block`);
  return m[1];
}

test('the AppImage starts through an AppRun that adds nothing to the arguments, no --no-sandbox, as up to 0.54', () => {
  const run = step.appRunText('filex-app');
  assert.match(run, /^#!\/bin\/bash\n/);
  const c = code(run);
  assert.doesNotMatch(c, /--no-sandbox/);
  assert.doesNotMatch(c, /NO_SANDBOX/);
  assert.doesNotMatch(c, /unshare/);
  assert.match(c, /^BIN="\$APPDIR\/filex-app"$/m);
  // The launcher, with the person's arguments exactly and nothing in front.
  const execs = [...c.matchAll(/^\s*exec .*$/gm)].map((m) => m[0].trim());
  assert.deepEqual(execs, ['exec "$BIN"', 'exec "$BIN" "${args[@]}"']);
  assert.ok(!c.includes('{{'), 'a placeholder was left in AppRun');
  // A name that would not be one file is refused, never written into a script.
  assert.throws(() => step.appRunText('filex app; rm'), /not a file name/);
});

test('afterPack puts it at the top of the packed app for an AppImage, and removes it from any other pack', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'fx-apprun-'));
  const apprun = path.join(dir, 'AppRun');
  try {
    // dist:linux: AppImage, .deb and .rpm from one packed directory.
    assert.equal(step.placeAppRun(dir, 'filex-app', [{ name: 'appImage' }, { name: 'deb' }, { name: 'rpm' }]).written, true);
    assert.equal(fs.readFileSync(apprun, 'utf8'), step.appRunText('filex-app'));
    if (process.platform !== 'win32') assert.equal(fs.statSync(apprun).mode & 0o777, 0o755);
    // The snap is packed on its own afterwards, in the same directory.
    assert.equal(step.placeAppRun(dir, 'filex-app', [{ name: 'snap' }]).written, false);
    assert.ok(!fs.existsSync(apprun), 'the snap pack still has the AppImage AppRun');
    assert.equal(step.placeAppRun(dir, 'filex-app', []).written, false);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('every Linux pack runs it, no other does, and the .deb and the .rpm leave it out', async () => {
  const hook = read('scripts/after-pack.cjs');
  assert.match(hook, /require\('\.\/appimage-apprun\.cjs'\)/);
  assert.match(hook, /await appImageAppRun\(context\)/);
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'fx-apprun-'));
  const ctx = (platform: string) => ({ electronPlatformName: platform, appOutDir: dir, packager: { executableName: 'filex-app' }, targets: [{ name: 'appImage' }] });
  try {
    for (const platform of ['win32', 'darwin']) await step(ctx(platform));
    assert.ok(!fs.existsSync(path.join(dir, 'AppRun')));
    await step(ctx('linux'));
    assert.ok(fs.existsSync(path.join(dir, 'AppRun')));
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
  // fpm matches --exclude against the path in the package (opt/filex/...).
  for (const key of ['deb', 'rpm']) {
    assert.match(code(yamlBlock(key)), /^\s+- "--exclude=opt\/filex\/AppRun"$/m, `${key}.fpm`);
  }
});

// The one test here that reads node_modules: ci.yml's "Desktop unit tests"
// installs nothing (desktop/test imports nothing from node_modules), so there
// it is skipped, saying why; the release's pretag ("desktop: typecheck + unit")
// runs it on an installed desktop/.
let EB: string | null = null;
try {
  EB = require.resolve('electron-builder/package.json');
} catch {
  EB = null;
}

test("electron-builder writes its AppRun first and copies the packed app over the image's directory after", { skip: EB ? false : 'electron-builder is not installed here (ci.yml Desktop unit tests installs nothing); the release pretag runs this on an installed desktop/' }, () => {
  // This order is what puts our AppRun in the image: an electron-builder that
  // changes it would ship its own AppRun again without a word.
  const lib = path.dirname(createRequire(EB as string).resolve('app-builder-lib/package.json'));
  const dir = path.join(lib, 'out', 'targets', 'appimage');
  const util = fs.readFileSync(path.join(dir, 'appImageUtil.js'), 'utf8');
  for (const fn of ['buildLegacyFuse2AppImage', 'buildStaticRuntimeAppImage']) {
    const start = util.indexOf(`async function ${fn}(`);
    assert.ok(start >= 0, `app-builder-lib has no ${fn}`);
    const rest = util.slice(start);
    const end = rest.indexOf('\nasync function ', 1);
    const body = end < 0 ? rest : rest.slice(0, end);
    const written = body.indexOf('writeAppLauncherAndRelatedFiles(');
    const copied = body.search(/copyDir\)\(appDir, stageDir\)/);
    assert.ok(written > 0, `${fn} no longer writes its AppRun through writeAppLauncherAndRelatedFiles`);
    assert.ok(copied > written, `${fn} no longer copies the packed app over its AppRun`);
  }
  assert.match(fs.readFileSync(path.join(dir, 'AppImageTarget.js'), 'utf8'), /APP_RUN_ENTRYPOINT = "AppRun"/);
});
