// Telling the operating system which kinds of file filex opens (the kinds the
// apps on the person's servers open, src/app-kinds.ts): who tells it on each
// package, the Windows registry document, the Linux media types, desktop
// entry and type package - and the uninstallers taking all of it back.
//
// Run:  node --experimental-strip-types --test desktop/test/os-assoc.test.ts

import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  OWN_GLOB_WEIGHT,
  OWN_TYPE_PREFIX,
  WIN_APP_PROGID,
  assocRouteFor,
  globs2Files,
  linuxMimePackage,
  linuxTypesFor,
  mergeGlobs,
  ownMediaType,
  packageKinds,
  parseGlobs2,
  regQueryValueNames,
  registeredWindowsKinds,
  typeHintsOf,
  typesOfKind,
  windowsAssocChangedArgs,
  windowsAssocReg,
  windowsKinds,
  windowsRegBytes,
} from '../src/os-assoc.ts';
import { OPENWITH_DESKTOP_ENTRY, hiddenDesktopEntry } from '../src/channel.ts';
import { OFFICE_EXTENSIONS } from '../src/openwith.ts';

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (rel: string) => fs.readFileSync(path.join(ROOT, rel), 'utf8');

// ── who tells the OS ─────────────────────────────────────────────────

test('who tells the OS: the running app where it can, the package where it cannot, nobody in a test or a portable copy', () => {
  const base = { packaged: true, portable: false, testRun: false, channel: null, appImage: null } as const;
  assert.equal(assocRouteFor({ ...base, platform: 'win32' }), 'windows');
  assert.equal(assocRouteFor({ ...base, platform: 'win32', portable: true }), 'none', 'the portable .exe leaves nothing behind');
  assert.equal(assocRouteFor({ ...base, platform: 'win32', channel: 'msstore' }), 'build', 'an MSIX\'s registry writes are its own');
  assert.equal(assocRouteFor({ ...base, platform: 'linux' }), 'linux');
  assert.equal(assocRouteFor({ ...base, platform: 'linux', channel: 'aur' }), 'linux', 'the AUR package is the .deb\'s tree');
  assert.equal(assocRouteFor({ ...base, platform: 'linux', appImage: '/home/alice/Apps/filex.AppImage' }), 'linux-appimage');
  assert.equal(assocRouteFor({ ...base, platform: 'linux', channel: 'snap' }), 'build', 'a strict snap writes nowhere outside its folders');
  assert.equal(assocRouteFor({ ...base, platform: 'linux', channel: 'flatpak' }), 'build');
  assert.equal(assocRouteFor({ ...base, platform: 'darwin' }), 'build', 'Info.plist is fixed in the package');
  assert.equal(assocRouteFor({ ...base, platform: 'win32', packaged: false }), 'none', 'a run from source would point the OS at a checkout');
  assert.equal(assocRouteFor({ ...base, platform: 'linux', testRun: true }), 'none');
});

// ── Windows ──────────────────────────────────────────────────────────

const EXE = 'C:\\Users\\alice\\Uygulamalar Şçğ\\filex\\filex.exe';

test('Windows: the kinds go under the user\'s own hive, added to "Open with", never as the default', () => {
  const doc = windowsAssocReg({ exe: EXE, exeName: 'filex.exe', add: ['psd', 'drawio'], drop: [], typeName: 'filex document' });
  const lines = doc.split('\r\n');
  assert.equal(lines[0], 'Windows Registry Editor Version 5.00');
  assert.ok(!doc.includes('HKEY_LOCAL_MACHINE') && !doc.includes('HKEY_CLASSES_ROOT'), 'only HKCU');
  assert.ok(lines.includes(`[HKEY_CURRENT_USER\\Software\\Classes\\${WIN_APP_PROGID}]`));
  assert.ok(lines.includes('@="filex document"'));
  // The path's backslashes doubled, the verb's two arguments quoted - "%1" above all.
  assert.ok(lines.includes('@="\\"C:\\\\Users\\\\alice\\\\Uygulamalar Şçğ\\\\filex\\\\filex.exe\\" \\"%1\\""'), doc);
  assert.ok(lines.includes('@="C:\\\\Users\\\\alice\\\\Uygulamalar Şçğ\\\\filex\\\\filex.exe,0"'));
  for (const k of ['drawio', 'psd']) {
    const at = lines.indexOf(`[HKEY_CURRENT_USER\\Software\\Classes\\.${k}\\OpenWithProgids]`);
    assert.ok(at > 0, k);
    assert.equal(lines[at + 1], `"${WIN_APP_PROGID}"=""`);
  }
  const sup = lines.indexOf('[HKEY_CURRENT_USER\\Software\\Classes\\Applications\\filex.exe\\SupportedTypes]');
  assert.deepEqual(lines.slice(sup + 1, sup + 3), ['".drawio"=""', '".psd"=""']);
  // ⚠ The extension's own default value is never written: that is the
  // default app, which is the person's to choose.
  assert.ok(!/\\Classes\\\.[a-z0-9]+\]\r\n@=/.test(doc), 'no extension\'s default ProgId');
});

test('Windows: a kind no server opens any more is taken back, and the ProgId with the last one', () => {
  const doc = windowsAssocReg({ exe: EXE, exeName: 'filex.exe', add: ['psd'], drop: ['drawio', 'psd'], typeName: 'filex document' });
  const lines = doc.split('\r\n');
  const at = lines.indexOf('[HKEY_CURRENT_USER\\Software\\Classes\\.drawio\\OpenWithProgids]');
  assert.equal(lines[at + 1], `"${WIN_APP_PROGID}"=-`);
  assert.ok(lines.includes('".drawio"=-'));
  assert.ok(!lines.includes('".psd"=-'), 'a kind both added and dropped stays');
  assert.ok(!lines.some((l) => l.startsWith('[-')), 'kinds left: the ProgId stays');

  const last = windowsAssocReg({ exe: EXE, exeName: 'filex.exe', add: [], drop: ['psd'], typeName: 'x' }).split('\r\n');
  assert.ok(last.includes(`[-HKEY_CURRENT_USER\\Software\\Classes\\${WIN_APP_PROGID}]`));
  assert.ok(last.includes('".psd"=-'));
  assert.ok(!last.some((l) => l.endsWith('\\shell\\open\\command]')), 'nothing added');
});

test('Windows: only well-formed, one-part extensions reach the registry', () => {
  const doc = windowsAssocReg({ exe: EXE, exeName: 'filex.exe', add: ['tar.gz', 'a"b', 'x]y', 'PSD', '../x'], drop: [], typeName: 'x' });
  assert.ok(doc.includes('\\Classes\\.psd\\OpenWithProgids]'));
  for (const bad of ['tar.gz', 'a"b', 'x]y', '../x']) assert.ok(!doc.includes(`.${bad}\\`), bad);
  assert.deepEqual(windowsKinds(['tar.gz', 'psd', 'b', 'psd']), ['b', 'psd']);
  assert.throws(() => windowsAssocReg({ exe: EXE, exeName: '..\\x.exe', add: ['psd'], drop: [], typeName: 'x' }));
  assert.throws(() => windowsAssocReg({ exe: EXE, exeName: 'filex.bat', add: ['psd'], drop: [], typeName: 'x' }));
});

test('Windows: the .reg file is UTF-16 with a byte order mark, so a path with "ş" survives', () => {
  const doc = windowsAssocReg({ exe: EXE, exeName: 'filex.exe', add: ['psd'], drop: [], typeName: 'filex belgesi' });
  const bytes = windowsRegBytes(doc);
  assert.deepEqual([...bytes.subarray(0, 2)], [0xff, 0xfe]);
  assert.equal(bytes.subarray(2).toString('utf16le'), doc);
  assert.ok(bytes.subarray(2).toString('utf16le').includes('Uygulamalar Şçğ'));
});

test('Windows: what filex registered is read back from the registry, the installer\'s office documents left alone', () => {
  const stdout = [
    '',
    'HKEY_CURRENT_USER\\Software\\Classes\\Applications\\filex.exe\\SupportedTypes',
    '    .docx    REG_SZ    ',
    '    .csv    REG_SZ    ',
    '    .psd    REG_SZ    ',
    '    .Drawio    REG_SZ    ',
    '    (Default)    REG_SZ    ',
    '',
  ].join('\r\n');
  const names = regQueryValueNames(stdout);
  assert.deepEqual(names, ['.docx', '.csv', '.psd', '.Drawio']);
  assert.deepEqual(registeredWindowsKinds(names, OFFICE_EXTENSIONS), ['drawio', 'psd']);
  assert.deepEqual(regQueryValueNames('ERROR: The system was unable to find the specified registry key or value.'), []);
});

test('Windows: Explorer is told the associations changed, without a command line to quote', () => {
  const args = windowsAssocChangedArgs();
  assert.deepEqual(args.slice(0, 3), ['-NoProfile', '-NonInteractive', '-EncodedCommand']);
  const script = Buffer.from(args[3]!, 'base64').toString('utf16le');
  assert.match(script, /SHChangeNotify\(0x08000000, 0,/);
  assert.match(script, /shell32\.dll/);
});

test('the uninstaller takes back what the running app registered, and keeps it across an update', () => {
  const nsh = read('build/installer.nsh');
  assert.match(nsh, /!define FILEX_APP_PROGID "filex\.AppDocument"/);
  assert.equal(WIN_APP_PROGID, 'filex.AppDocument', 'the app and the uninstaller name one ProgId');
  const un = nsh.slice(nsh.indexOf('!macro customUnInstall'));
  const loop = un.indexOf('EnumRegValue');
  const release = un.indexOf('DeleteRegValue SHELL_CONTEXT "Software\\Classes\\$R9\\OpenWithProgids" "${FILEX_APP_PROGID}"');
  const progid = un.indexOf('DeleteRegKey SHELL_CONTEXT "Software\\Classes\\${FILEX_APP_PROGID}"');
  const apps = un.indexOf('DeleteRegKey SHELL_CONTEXT "Software\\Classes\\Applications\\${APP_EXECUTABLE_FILENAME}"');
  assert.ok(loop > 0 && release > loop && progid > release, 'every SupportedTypes extension loses filex.AppDocument, then the ProgId goes');
  assert.ok(apps > progid, 'BEFORE the Applications key - its SupportedTypes list is what says which');
  assert.ok(un.lastIndexOf('${ifNot} ${isUpdated}', loop) >= 0 && un.indexOf('${endIf}', loop) < apps, 'not on an update');
});

// ── Linux ────────────────────────────────────────────────────────────

/** What shared-mime-info 2.4 (Ubuntu 26.04) says about the catalog's kinds, measured 2026-10-16. */
const GLOBS2 = [
  '# This file was automatically generated by the',
  '80:application/x-doom-wad:*.wad',
  '50:application/x-wii-wad:*.wad',
  '50:application/vnd.oasis.opendocument.formula-template:*.otf',
  '50:font/otf:*.otf',
  '50:image/vnd.adobe.photoshop:*.psd',
  '50:application/vnd.openxmlformats-officedocument.wordprocessingml.document:*.docx',
  '50:text/x-c++src:*.C:cs',
  '50:application/x-compressed-tar:*.tar.gz',
  '50:text/x-readme:README*',
  '10:application/x-filex-fxtxt:*.fxtxt',
  '',
].join('\n');

test('Linux: which type the system gives an extension - the highest weight, every type that ties', () => {
  const g = parseGlobs2(GLOBS2);
  assert.deepEqual(g.get('wad'), { weight: 80, types: ['application/x-doom-wad'] });
  assert.deepEqual(g.get('otf')?.types.sort(), ['application/vnd.oasis.opendocument.formula-template', 'font/otf']);
  assert.deepEqual(g.get('tar.gz')?.types, ['application/x-compressed-tar']);
  assert.equal(g.has('c'), false, 'a case-sensitive glob is not the extension');
  assert.equal(g.has('fxtxt'), false, 'filex\'s own type does not make the extension known to the SYSTEM');
  assert.equal([...g.keys()].some((k) => k.includes('*')), false, 'only plain *.<ext> globs');
  const merged = mergeGlobs([parseGlobs2('50:image/x-a:*.psd'), g]);
  assert.deepEqual(merged.get('psd')?.types.sort(), ['image/vnd.adobe.photoshop', 'image/x-a']);
});

test('Linux: the globs2 files a desktop reads, in order', () => {
  assert.deepEqual(globs2Files({}, '/home/alice'), ['/home/alice/.local/share/mime/globs2', '/usr/local/share/mime/globs2', '/usr/share/mime/globs2']);
  assert.deepEqual(globs2Files({ XDG_DATA_HOME: '/d', XDG_DATA_DIRS: '/a:/b' }, '/home/alice'), ['/d/mime/globs2', '/a/mime/globs2', '/b/mime/globs2']);
});

test('Linux: a known extension is named by the system\'s type, an unknown one by filex\'s own', () => {
  const t = linuxTypesFor(['psd', 'fxtxt', 'wad', 'docx', 'tar.gz'], ['image/svg+xml', 'image/*'], parseGlobs2(GLOBS2), new Set(['application/vnd.openxmlformats-officedocument.wordprocessingml.document']));
  assert.deepEqual(t.types, [
    'application/x-compressed-tar',
    'application/x-doom-wad',
    `${OWN_TYPE_PREFIX}fxtxt`,
    'image/svg+xml',
    'image/vnd.adobe.photoshop',
  ]);
  assert.deepEqual(t.own, [{ ext: 'fxtxt', type: ownMediaType('fxtxt') }]);
  assert.ok(!t.types.includes('image/*'), 'a family would offer filex for every image');
});

test('Linux: an app that says which of an extension\'s types it opens is registered for that one alone', () => {
  const g = parseGlobs2(GLOBS2);
  // .otf is a font AND an OpenDocument formula template to shared-mime-info.
  const fonts = linuxTypesFor(['otf'], [], g, new Set(), { otf: ['font/otf'] });
  assert.deepEqual(fonts.types, ['font/otf'], 'not offered for a formula template');
  assert.deepEqual(linuxTypesFor(['otf'], [], g, new Set(), { otf: ['font/*'] }).types, ['font/otf'], 'a family names its own');
  // The app said nothing, or nothing that tells them apart: every type, as before.
  const both = ['application/vnd.oasis.opendocument.formula-template', 'font/otf'];
  assert.deepEqual(linuxTypesFor(['otf'], [], g).types, both);
  assert.deepEqual(linuxTypesFor(['otf'], [], g, new Set(), { otf: null }).types, both);
  assert.deepEqual(linuxTypesFor(['otf'], [], g, new Set(), { otf: ['application/x-other'] }).types, both);
  assert.deepEqual(typesOfKind(['a/x', 'b/y'], ['b/y']), ['b/y']);
  assert.deepEqual(typesOfKind(['a/x', 'b/y'], []), ['a/x', 'b/y']);
});

test('the hints: what the apps opening an extension name - none when one of them names nothing', () => {
  const FONTS = { ext: ['otf', 'ttf'], mime: ['font/otf', 'font/ttf'] };
  const MORE = { ext: ['otf'], mime: ['font/sfnt'] };
  const SILENT = { ext: ['ttf'], mime: [] };
  const OFF = { ext: ['otf'], mime: [], off: ['otf'] };
  assert.deepEqual(typeHintsOf([FONTS, MORE, OFF]), { otf: ['font/otf', 'font/ttf', 'font/sfnt'], ttf: ['font/otf', 'font/ttf'] });
  assert.deepEqual(typeHintsOf([FONTS, SILENT]), { otf: ['font/otf', 'font/ttf'], ttf: null }, 'an app that names no type: every type for its extensions');
  assert.deepEqual(typeHintsOf([SILENT, FONTS]).ttf, null, 'whichever comes first');
});

test('Linux: filex\'s own types are defined below the system\'s weight, and the file goes when none is left', () => {
  const xml = linuxMimePackage([{ ext: 'fxtxt', type: ownMediaType('fxtxt') }, { ext: 'jsdos', type: ownMediaType('jsdos') }])!;
  assert.match(xml, /^<\?xml version="1\.0" encoding="UTF-8"\?>/);
  assert.match(xml, /<mime-info xmlns="http:\/\/www\.freedesktop\.org\/standards\/shared-mime-info">/);
  assert.match(xml, /<mime-type type="application\/x-filex-fxtxt">\n {4}<comment>FXTXT file<\/comment>\n {4}<glob pattern="\*\.fxtxt" weight="10"\/>/);
  assert.ok(OWN_GLOB_WEIGHT < 50, 'shared-mime-info\'s default weight is 50: a system type always wins');
  for (const m of xml.matchAll(/<!--([\s\S]*?)-->/g)) assert.ok(!m[1]!.includes('--'), 'no double hyphen inside an XML comment');
  assert.equal(linuxMimePackage([]), null);
  assert.equal(linuxMimePackage([{ ext: 'a"b', type: 'x' }]), null, 'nothing malformed is written');
});

test('Linux: the entry names the types, hidden, with the launcher and filex\'s icon', () => {
  const body = hiddenDesktopEntry('/opt/filex/filex-app', ['image/vnd.adobe.photoshop', 'application/x-filex-fxtxt'], 'filex: opens the kinds of file the apps on your filex servers open', 'filex-app');
  const lines = body.split('\n');
  assert.equal(lines[0], '[Desktop Entry]');
  assert.ok(lines.includes('Exec=/opt/filex/filex-app %U'));
  assert.ok(lines.includes('TryExec=/opt/filex/filex-app'), 'ignored once the app is gone');
  assert.ok(lines.includes('Icon=filex-app'));
  assert.ok(lines.includes('NoDisplay=true'), 'the menu entry is the package\'s');
  assert.ok(lines.includes('MimeType=image/vnd.adobe.photoshop;application/x-filex-fxtxt;'));
  assert.ok(!hiddenDesktopEntry('/x', [], 'c', 'a b').includes('Icon='), 'an icon name that is not one is left out');
  // ⚠ Its own name: the package's filex-app.desktop in the user's folder would hide the package's.
  assert.equal(OPENWITH_DESKTOP_ENTRY, 'filex-app-openwith.desktop');
});

test('removing the .deb or .rpm takes the two files out of every home, by name, and nothing else', () => {
  const tpl = read('build/linux/after-remove.tpl');
  assert.ok(tpl.includes(`entry="$share/applications/${OPENWITH_DESKTOP_ENTRY}"`));
  assert.ok(tpl.includes(`types="$share/mime/packages/${OPENWITH_DESKTOP_ENTRY.replace(/\.desktop$/, '.xml')}"`));
  assert.match(tpl, /runuser -u "\$name" -- update-mime-database "\$share\/mime"/);
  assert.match(tpl, /runuser -u "\$name" -- update-desktop-database "\$share\/applications"/);
  // Only exact files: no wildcard, no directory removed.
  const rms = tpl.split('\n').filter((l) => /\brm\b/.test(l) && !l.trim().startsWith('#'));
  assert.deepEqual(rms.map((l) => l.trim()), ["rm -f '/usr/bin/${executable}'", 'rm -f "$entry"', 'rm -f "$types"']);
  // A home that is not an absolute path is skipped before anything is removed.
  assert.match(tpl, /case "\$home" in \/\?\*\) ;; \*\) continue ;; esac/);
  // ⚠ The template's dollar-brace variables are build-time macros: none new.
  assert.deepEqual([...new Set(tpl.match(/\$\{[a-zA-Z]+\}/g) ?? [])].sort(), ['${executable}', '${sanitizedProductName}']);
});

// ── the packages ─────────────────────────────────────────────────────

test('a package declares the store\'s one-part kinds beside its office documents, never a program', () => {
  assert.deepEqual(packageKinds(['psd', 'docx', 'tar.gz', 'exe', 'drawio', 'csv']), ['drawio', 'psd']);
});
