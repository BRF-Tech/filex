// The Linux desktop entry is named in six places, and they must agree.
//
// Run:  node --experimental-strip-types --test desktop/test/linux-desktop-entry.test.ts
//
// electron-builder installs `<linux.executableName>.desktop` (snap:
// `<snap>_<app>.desktop`); Electron registers `filex://` against
// `desktopName` from package.json; src/channel.ts names the entry for
// `xdg-mime default` and the Wayland app id; src/login-item.ts names the
// login item file, which a snap's `autostart:` must match file for file.
// Until v0.43.1 two of these disagreed — `desktopName` was missing, so Electron
// registered `@brftech/filex-desktop.desktop`, a file nothing installs, and the
// entry declared no `x-scheme-handler/filex` — and nothing failed: the link
// simply never reached the app. This keeps them in step by failing.
// Since 2026-09-25 all of them say `filex-app` — `filex` is the CLI's name.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { LINUX_APP_NAME, STORE_IDS, linuxDesktopEntry } from '../src/channel.ts';
import { LINUX_AUTOSTART_NAME } from '../src/login-item.ts';

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (rel: string) => fs.readFileSync(path.join(ROOT, rel), 'utf8');
const YML = read('electron-builder.yml');
const PKG = JSON.parse(read('package.json')) as { version: string; desktopName?: string; scripts: Record<string, string> };
const MAIN = read('src/main.ts');

/** The lines of one top-level YAML block (`linux:`, `snap:` …). */
function yamlBlock(key: string): string {
  const m = new RegExp(`^${key}:\\n((?:[ \\t].*\\n|\\s*\\n)*)`, 'm').exec(YML);
  assert.ok(m, `electron-builder.yml has no top-level "${key}:" block`);
  return m[1];
}
/** A `  key: value` scalar inside a block, comments ignored. */
function scalar(block: string, key: string): string | undefined {
  return new RegExp(`^  ${key}:[ \\t]*([^#\\n]+?)[ \\t]*(?:#.*)?$`, 'm').exec(block)?.[1];
}
/** The `    - item` entries of a list inside a block. */
function list(block: string, key: string): string[] {
  const m = new RegExp(`^  ${key}:\\n((?:[ \\t]+(?:-.*|#.*)\\n)+)`, 'm').exec(block);
  assert.ok(m, `no "${key}:" list`);
  return [...m[1].matchAll(/^[ \t]+-[ \t]+([^#\n]+?)[ \t]*$/gm)].map((x) => x[1]);
}

const EXE = scalar(yamlBlock('linux'), 'executableName');

test('the .deb/.rpm/AppImage entry, package.json and channel.ts name the same file', () => {
  // ⚠ Not `filex`: that is the CLI's command, and a desktop package that puts
  // /usr/bin/filex on PATH shadows it (or is shadowed by it).
  assert.equal(EXE, 'filex-app');
  assert.equal(LINUX_APP_NAME, EXE);
  // The .deb/.rpm PACKAGE name is its own setting: electron-builder derives it
  // from productName (`filex`) for a scoped npm name, not from executableName
  // (measured: `Package: filex` until this line existed).
  assert.equal(scalar(yamlBlock('deb'), 'packageName'), EXE);
  assert.equal(scalar(yamlBlock('rpm'), 'packageName'), EXE);
  assert.equal(linuxDesktopEntry(null), `${EXE}.desktop`);
  // What Electron hands xdg-settings (CHROME_DESKTOP) before main.ts runs.
  assert.equal(PKG.desktopName, `${EXE}.desktop`);
});

test("the snap's entry is <snap>_<app>.desktop, both named after executableName", () => {
  assert.equal(STORE_IDS.snap, EXE);
  assert.equal(linuxDesktopEntry('snap'), `${EXE}_${EXE}.desktop`);
  assert.equal(STORE_IDS.aur, `${EXE}-bin`);
});

/** The version in `--<flag>=filex (<< X)` (deb) or `--<flag>=filex < X` (rpm). */
function fpmBound(block: string, flag: string): string | undefined {
  const arg = list(block, 'fpm')
    .map((x) => x.replace(/^"|"$/g, ''))
    .find((x) => x.startsWith(`--${flag}=`));
  if (!arg) return undefined;
  const m = /^--[a-z]+=filex (?:\(<< ([\d.]+)\)|< ([\d.]+))$/.exec(arg);
  return m ? m[1] ?? m[2] : undefined;
}
function newer(a: string, b: string): boolean {
  const [x, y] = [a, b].map((v) => v.split('.').map(Number));
  for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i] > y[i];
  return false;
}

test('the old `filex` desktop package is replaced on upgrade — and only the old one', () => {
  // A different package name is a different package: without these, the new
  // .deb/.rpm collides with the old one's files under /opt/filex.
  const deb = yamlBlock('deb');
  const rpm = yamlBlock('rpm');
  const bounds = [fpmBound(deb, 'conflicts'), fpmBound(deb, 'replaces'), fpmBound(rpm, 'conflicts'), fpmBound(rpm, 'replaces')];
  // ⚠⚠ Bounded, never bare: `filex` is the CLI's name from now on, and an
  // unbounded Replaces would uninstall a future `filex` CLI package.
  for (const b of bounds) assert.ok(b, `every relation is version-bounded: ${JSON.stringify(bounds)}`);
  assert.equal(new Set(bounds).size, 1, 'deb and rpm bound the same version');
  // Every desktop build that was still called `filex` sits below the bound:
  // the last one shipped as v0.43.2 (0.44.0 renamed it, and 0.44.0's desktop
  // packages never published). And the bound may not pass this tree's version,
  // or the CLI's own `filex` package of this release would be replaced too.
  // (This used to compare against package.json alone, which only held while
  // the tree was the last release before the rename — v0.44.1 broke it.)
  const LAST_OLD_NAME = '0.43.2';
  assert.ok(newer(bounds[0]!, LAST_OLD_NAME), `${bounds[0]} must be above ${LAST_OLD_NAME}, the last desktop package named filex`);
  assert.ok(!newer(bounds[0]!, PKG.version), `${bounds[0]} must not pass package.json ${PKG.version}, or this release's CLI package would be replaced`);
});

test('the .deb and .rpm depend on the ALSA library Electron loads at start', () => {
  // electron-builder's default Depends (measured on the v0.44.2 .deb: gtk3,
  // notify, nss, xss, xtst, xdg-utils, atspi, uuid, secret) has no sound
  // library, and Electron refuses to start without libasound.so.2: on a
  // minimal install the app did not open. Added through `fpm` so the default
  // list stays; the t64 name is Debian 13's and Ubuntu 24.04's, the rpm side
  // is Fedora's alsa-lib or openSUSE's libasound2.
  const args = (block: string) => list(yamlBlock(block), 'fpm').map((x) => x.replace(/^"|"$/g, ''));
  assert.ok(args('deb').includes('--depends=libasound2 | libasound2t64'), JSON.stringify(args('deb')));
  assert.ok(args('rpm').includes('--depends=(alsa-lib or libasound2)'), JSON.stringify(args('rpm')));
  // Only added, never a `depends:` list: that would replace the default one.
  assert.doesNotMatch(yamlBlock('deb'), /^ {2}depends:/m);
  assert.doesNotMatch(yamlBlock('rpm'), /^ {2}depends:/m);
});

test('every Linux package declares the scheme the sign-in hands back on', () => {
  const scheme = /const DEEP_LINK_SCHEME = '([^']+)'/.exec(MAIN)?.[1];
  assert.equal(scheme, 'filex');
  const linux = yamlBlock('linux');
  // In linux.mimeTypes, which becomes the entry's MimeType= line.
  assert.ok(
    list(linux, 'mimeTypes').includes(`x-scheme-handler/${scheme}`),
    'linux.mimeTypes lacks x-scheme-handler/filex — no desktop would hand the link to the app',
  );
  // ⚠ Not ALSO as `protocols`: electron-builder 24.13.3 appends a protocol's
  // scheme to the config's mimeTypes array once per target it builds
  // (measured twice in the .deb and three times in the .rpm).
  assert.doesNotMatch(linux, /^ {2}protocols:/m);
});

test("the snap's autostart entry is the file the login item writes", () => {
  const snap = yamlBlock('snap');
  assert.equal(scalar(snap, 'autoStart'), 'true');
  // electron-builder writes `autostart: <snap name>.desktop`; snapd starts the
  // file of that name under the snap's $HOME/.config/autostart.
  assert.equal(LINUX_AUTOSTART_NAME, `${EXE}.desktop`);
  // …and that is the name main.ts writes when nothing else is asked for.
  assert.match(MAIN, /function linuxAutostartFile\(name = LINUX_AUTOSTART_NAME\): string \{\n\s+return path\.join\(app\.getPath\('home'\), '\.config', 'autostart', name\);/);
});

test('the snap is strict, keeps its plugs, and never publishes by itself', () => {
  const snap = yamlBlock('snap');
  assert.equal(scalar(snap, 'confinement'), 'strict');
  const plugs = list(snap, 'plugs');
  // `default` keeps electron-builder's list (home, network, desktop, …).
  for (const p of ['default', 'password-manager-service', 'removable-media']) assert.ok(plugs.includes(p), p);
  // Not a GitHub download target: `dist:snap` builds it on its own.
  assert.ok(!list(yamlBlock('linux'), 'target').includes('snap'));
  assert.ok(list(yamlBlock('linux'), 'target').includes('rpm'));
  // ⚠ On a tagged CI run electron-builder publishes by itself, and a snap's
  // default publisher is the Snap Store — the upload is CI's explicit step.
  assert.match(PKG.scripts['dist:snap'], /--linux snap --publish never$/);
});

test('removing the .deb/.rpm removes its command — and upgrading does not', () => {
  const rel = scalar(yamlBlock('deb'), 'afterRemove');
  assert.equal(rel, 'build/linux/after-remove.tpl');
  assert.equal(scalar(yamlBlock('rpm'), 'afterRemove'), rel);
  const tpl = read(rel!);
  // The TARGET, not the link: Fedora's alternatives refuses the link and left
  // /usr/bin/filex-app dangling after `dnf remove` (measured).
  assert.match(tpl, /update-alternatives --remove '\$\{executable\}' '\/opt\/\$\{sanitizedProductName\}\/\$\{executable\}'/);
  assert.doesNotMatch(tpl, /--remove '\$\{executable\}' '\/usr\/bin/);
  // Only on a real removal: an rpm upgrade runs the old %postun AFTER the new
  // %post, and would take the new version's link with it.
  assert.match(tpl, /case "\$1" in\n\s+0\|remove\|purge\|disappear\) ;;\n\s+\*\) exit 0 ;;/);
  // electron-builder expands every dollar-brace name at build time and throws
  // on one it does not define: only its own two may appear.
  const macros = new Set([...tpl.matchAll(/\$\{([a-zA-Z]+)\}/g)].map((m) => m[1]));
  assert.deepEqual([...macros].sort(), ['executable', 'sanitizedProductName']);
});
