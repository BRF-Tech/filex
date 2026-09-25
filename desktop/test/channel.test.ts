// Which store installed this copy, and what follows from it.
//
// Run:  node --experimental-strip-types --test desktop/test/channel.test.ts
//
// A store copy is the store's to update: if electron-updater stays wired in
// one, it downloads the direct-download installer and runs it from inside the
// package — on Windows that is a second copy nobody can see, on Linux a
// `pkexec dpkg -i` against a sandbox. So the detection has to be right in both
// directions: every store copy found, and nothing else mistaken for one.

import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';

import {
  APPIMAGE_DESKTOP_ENTRY,
  LEGACY_LINUX_DESKTOP_ENTRY,
  LINUX_APP_NAME,
  appImageDesktopEntry,
  defaultHandlerRoute,
  directCopyMarkers,
  engineStateDir,
  explorerVisibleRoot,
  linuxDesktopEntry,
  linuxSandbox,
  otherCopyOf,
  retargetMimeapps,
  storeChannel,
  storePageUrl,
  userHome,
} from '../src/channel.ts';

test('an MSIX package on Windows is the Microsoft Store channel', () => {
  assert.equal(storeChannel({ platform: 'win32', windowsStore: true, env: {} }), 'msstore');
});

test('the NSIS install and the portable .exe are NOT store copies', () => {
  assert.equal(storeChannel({ platform: 'win32', windowsStore: false, env: {} }), null);
  assert.equal(storeChannel({ platform: 'win32', env: { PORTABLE_EXECUTABLE_DIR: 'E:\\' } }), null);
});

test('a Flatpak is recognised by the variable flatpak sets inside the sandbox', () => {
  assert.equal(storeChannel({ platform: 'linux', env: { FLATPAK_ID: 'sh.filex.Filex' } }), 'flatpak');
});

test('a snap needs BOTH snapd variables — one alone is somebody\'s shell setting', () => {
  assert.equal(storeChannel({ platform: 'linux', env: { SNAP: '/snap/filex-app/12', SNAP_NAME: 'filex-app' } }), 'snap');
  assert.equal(storeChannel({ platform: 'linux', env: { SNAP: '/home/ada/snap' } }), null);
});

test('the AppImage, the .deb and the .rpm keep their own updater', () => {
  assert.equal(storeChannel({ platform: 'linux', env: { APPIMAGE: '/home/ada/filex.AppImage' } }), null);
  assert.equal(storeChannel({ platform: 'linux', env: {} }), null);
  // electron-builder's own markers: electron-updater's DebUpdater/RpmUpdater.
  assert.equal(storeChannel({ platform: 'linux', env: {}, packageType: 'deb' }), null);
  assert.equal(storeChannel({ platform: 'linux', env: {}, packageType: 'rpm\n' }), null);
});

test('the AUR package is recognised by the marker its PKGBUILD writes', () => {
  // Without it the .deb's own `deb` would stay, and electron-updater would
  // run `pkexec dpkg -i` on Arch.
  assert.equal(storeChannel({ platform: 'linux', env: {}, packageType: 'aur\n' }), 'aur');
  assert.equal(storeChannel({ platform: 'linux', env: {}, packageType: 'aur' }), 'aur');
  // Linux only: the marker means nothing anywhere else.
  assert.equal(storeChannel({ platform: 'win32', env: {}, packageType: 'aur' }), null);
  // A snap built from the same tree is still a snap.
  assert.equal(storeChannel({ platform: 'linux', env: { SNAP: '/snap/filex-app/3', SNAP_NAME: 'filex-app' }, packageType: 'aur' }), 'snap');
});

test('variables from another platform do not leak across', () => {
  // A Windows machine with a FLATPAK_ID in its environment (WSL interop,
  // somebody's dotfiles) is still an NSIS install.
  assert.equal(storeChannel({ platform: 'win32', env: { FLATPAK_ID: 'x', SNAP: 'y', SNAP_NAME: 'z' } }), null);
  assert.equal(storeChannel({ platform: 'darwin', windowsStore: true, env: {} }), null);
});

test('the store page: product page once the Store ID is known, the updates page until then', () => {
  const ids = { msstore: '', flatpak: 'sh.filex.Filex', snap: 'filex-app', aur: 'filex-app-bin' };
  assert.equal(storePageUrl('msstore', ids), 'ms-windows-store://downloadsandupdates');
  assert.equal(storePageUrl('msstore', { ...ids, msstore: '9ABCDEF12345' }), 'ms-windows-store://pdp/?ProductId=9ABCDEF12345');
  assert.equal(storePageUrl('flatpak', ids), 'appstream://sh.filex.Filex');
  assert.equal(storePageUrl('snap', ids), 'snap://filex-app');
  // And the defaults are those names: the snap and the AUR package are
  // filex-app — `filex` is the CLI's.
  assert.equal(storePageUrl('snap'), 'snap://filex-app');
  assert.equal(storePageUrl('aur'), 'https://aur.archlinux.org/packages/filex-app-bin');
  // The AUR has no app: its web page.
  assert.equal(storePageUrl('aur', ids), 'https://aur.archlinux.org/packages/filex-app-bin');
});

test('only the MSIX copy moves Explorer-visible files out of AppData', () => {
  const userData = path.join('C:', 'Users', 'ada', 'AppData', 'Roaming', 'filex');
  const home = path.join('C:', 'Users', 'ada');
  assert.equal(explorerVisibleRoot('msstore', userData, home), path.join(home, '.filex', 'desktop'));
  // Nowhere else is AppData redirected: every other channel keeps the paths
  // its users already have.
  assert.equal(explorerVisibleRoot(null, userData, home), userData);
  assert.equal(explorerVisibleRoot('flatpak', userData, home), userData);
  assert.equal(explorerVisibleRoot('snap', userData, home), userData);
  assert.equal(explorerVisibleRoot('aur', userData, home), userData);
});

test('the direct-download copy is looked for where the installer puts it, and by its Start-menu shortcut', () => {
  const env = { LOCALAPPDATA: String.raw`C:\Users\ada\AppData\Local`, APPDATA: String.raw`C:\Users\ada\AppData\Roaming` };
  assert.deepEqual(directCopyMarkers(env), [
    String.raw`C:\Users\ada\AppData\Local\Programs\filex\filex.exe`,
    // The installer lets the user choose the folder; the shortcut is made
    // wherever the app went.
    String.raw`C:\Users\ada\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\filex.lnk`,
  ]);
  // No environment, nothing to look for — never a relative path.
  assert.deepEqual(directCopyMarkers({}), []);
});

test('which other copy of filex this one warns about', () => {
  const env = { LOCALAPPDATA: String.raw`C:\Users\ada\AppData\Local`, APPDATA: String.raw`C:\Users\ada\AppData\Roaming` };
  const all = () => true;
  const none = () => false;
  const only = (p: string) => (q: string) => q === p;
  // The Store copy and the filex.sh copy share accounts through AppData.
  assert.equal(otherCopyOf('msstore', 'win32', env, only(String.raw`C:\Users\ada\AppData\Local\Programs\filex\filex.exe`)), 'direct');
  assert.equal(otherCopyOf('msstore', 'win32', env, none), null);
  // ⚠ The snap keeps its own accounts and sync history (SNAP_USER_COMMON),
  // so the per-pair lock never sees the other engine: a .deb, .rpm, AppImage
  // or AUR copy next to the snap syncs the same folders twice. A strict snap
  // cannot see the host's /usr or /opt, so only this side can tell.
  assert.equal(otherCopyOf(null, 'linux', {}, only('/snap/bin/filex-app')), 'snap');
  assert.equal(otherCopyOf('aur', 'linux', {}, only('/snap/bin/filex-app')), 'snap');
  assert.equal(otherCopyOf(null, 'linux', {}, none), null);
  // A copy never warns about itself, and a sandbox cannot look.
  assert.equal(otherCopyOf('snap', 'linux', {}, all), null);
  assert.equal(otherCopyOf('flatpak', 'linux', {}, all), null);
  // The filex.sh copy on Windows and the macOS app have nothing to find.
  assert.equal(otherCopyOf(null, 'win32', env, all), null);
  assert.equal(otherCopyOf(null, 'darwin', {}, all), null);
});

// ─────────────────────────── Linux ───────────────────────────

// What snapd sets for every command of a strict snap (snapcraft.io, "Environment
// variables"): HOME is the per-revision directory, the real one is kept aside.
const SNAP_ENV = {
  SNAP: '/snap/filex-app/7',
  SNAP_NAME: 'filex-app',
  HOME: '/home/ada/snap/filex-app/7',
  SNAP_USER_DATA: '/home/ada/snap/filex-app/7',
  SNAP_USER_COMMON: '/home/ada/snap/filex-app/common',
  SNAP_REAL_HOME: '/home/ada',
};

test('a snap puts the filex folder in the home the user sees, not in its revision directory', () => {
  // Electron's app.getPath('home') reads HOME — inside a snap that is
  // ~/snap/filex-app/<rev>, hidden from the file manager and gone with the snap.
  assert.equal(userHome('snap', SNAP_ENV, SNAP_ENV.HOME), '/home/ada');
  // A snapd too old to set SNAP_REAL_HOME: HOME is all there is.
  const { SNAP_REAL_HOME: _gone, ...old } = SNAP_ENV;
  assert.equal(userHome('snap', old, SNAP_ENV.HOME), SNAP_ENV.HOME);
  // Every other channel keeps the home it always had, whatever lies around in
  // the environment.
  for (const ch of [null, 'flatpak', 'aur', 'msstore'] as const) {
    assert.equal(userHome(ch, SNAP_ENV, '/home/ada'), '/home/ada');
    assert.equal(userHome(ch, { SNAP_REAL_HOME: '/elsewhere' }, '/home/ada'), '/home/ada');
  }
});

test("a snap keeps the sync engine's state where refreshes and reverts leave it alone", () => {
  assert.equal(engineStateDir('snap', SNAP_ENV), '/home/ada/snap/filex-app/common/sync');
  // Without SNAP_USER_COMMON there is no revision-proof place to name: the
  // engine's own default stands rather than a guessed path.
  assert.equal(engineStateDir('snap', { SNAP: '/snap/filex-app/7', SNAP_NAME: 'filex-app' }), null);
  // Everywhere else the engine keeps ~/.filex/sync, shared with a terminal
  // `filex` — a stray variable must not move somebody's pairings.
  for (const ch of [null, 'flatpak', 'aur', 'msstore'] as const) assert.equal(engineStateDir(ch, SNAP_ENV), null);
});

test('the desktop entry is the one each package actually installs', () => {
  // filex-app, not filex: `filex` is the CLI's command (2026-09-25).
  assert.equal(LINUX_APP_NAME, 'filex-app');
  assert.equal(linuxDesktopEntry(null), 'filex-app.desktop');
  assert.equal(linuxDesktopEntry('aur'), 'filex-app.desktop');
  assert.notEqual(linuxDesktopEntry(null), LEGACY_LINUX_DESKTOP_ENTRY);
  // snapd: <snap>_<app>.desktop
  assert.equal(linuxDesktopEntry('snap'), 'filex-app_filex-app.desktop');
  assert.equal(linuxDesktopEntry('flatpak'), 'sh.filex.Filex.desktop');
  // A bare AppImage names the entry it writes for itself — never the
  // package's, which it would shadow — and it kept its pre-rename name: an
  // image rewrites that very file at every start, a new name would orphan it.
  assert.equal(linuxDesktopEntry(null, '/home/ada/Apps/filex.AppImage'), APPIMAGE_DESKTOP_ENTRY);
  assert.equal(APPIMAGE_DESKTOP_ENTRY, 'filex-appimage.desktop');
  assert.notEqual(APPIMAGE_DESKTOP_ENTRY, linuxDesktopEntry(null));
  // A snap is a snap even if somebody launched it with APPIMAGE set.
  assert.equal(linuxDesktopEntry('snap', '/x.AppImage'), 'filex-app_filex-app.desktop');
});

test("an AppImage's own entry points at the image, hidden, with the link", () => {
  const body = appImageDesktopEntry('/home/ada/Apps/filex.AppImage', ['application/rtf', 'x-scheme-handler/filex']);
  const lines = body.split('\n');
  // Unquoted: xdg-settings takes the first word of Exec literally, and a
  // quoted path makes it refuse the entry (measured).
  assert.ok(lines.includes('Exec=/home/ada/Apps/filex.AppImage %U'), body);
  // Desktops skip the entry once the image is gone.
  assert.ok(lines.includes('TryExec=/home/ada/Apps/filex.AppImage'), body);
  assert.ok(lines.includes('NoDisplay=true'));
  assert.ok(lines.includes('MimeType=application/rtf;x-scheme-handler/filex;'));
  assert.equal(lines[0], '[Desktop Entry]');
});

test("an AppImage path is quoted the way the Desktop Entry spec reads it back", () => {
  // Quoting escapes " ` $ \ with a backslash and doubles %; then Exec, being
  // a string value, has every backslash escaped once more — so a literal $ is
  // written \\$ (the spec's own example).
  const body = appImageDesktopEntry('/home/ada/My $Apps/50% "x".AppImage', ['x-scheme-handler/filex']);
  assert.ok(body.includes(String.raw`Exec="/home/ada/My \\$Apps/50%% \\"x\\".AppImage" %U`), body);
  // No reserved character: not quoted, but a field code must still not appear.
  assert.ok(appImageDesktopEntry('/home/ada/100%.AppImage', []).includes('Exec=/home/ada/100%%.AppImage %U'));
  const slash = appImageDesktopEntry(String.raw`/home/ada/a\b.AppImage`, ['x-scheme-handler/filex']);
  assert.ok(slash.includes(String.raw`Exec="/home/ada/a\\\\b.AppImage" %U`), slash);
  assert.ok(slash.includes(String.raw`TryExec=/home/ada/a\\b.AppImage`), slash);
});

test('"make filex the default" runs xdg-mime only where it reaches the desktop', () => {
  assert.equal(defaultHandlerRoute('linux', null), 'xdg');
  assert.equal(defaultHandlerRoute('linux', 'aur'), 'xdg');
  // Inside a sandbox xdg-mime writes a mimeapps.list no desktop reads — a
  // button there would report success and change nothing.
  assert.equal(defaultHandlerRoute('linux', 'snap'), 'linux-manual');
  assert.equal(defaultHandlerRoute('linux', 'flatpak'), 'linux-manual');
  assert.equal(defaultHandlerRoute('win32', null), 'settings');
  assert.equal(defaultHandlerRoute('win32', 'msstore'), 'settings');
  assert.equal(defaultHandlerRoute('darwin', null), 'manual');
  assert.equal(linuxSandbox('aur'), false);
  assert.equal(linuxSandbox(null), false);
});

test("the user's defaults follow the entry across the rename", () => {
  const before = [
    '[Default Applications]',
    'application/msword=filex.desktop',
    'text/plain=gedit.desktop;filex.desktop;',
    'x-scheme-handler/filex=filex.desktop',
    '',
    '[Added Associations]',
    // Both names already: the old one goes, the new one is not doubled.
    'application/rtf=filex.desktop;filex-app.desktop;',
    '',
    '[Something Else]',
    'application/msword=filex.desktop',
    '',
  ].join('\n');
  const after = retargetMimeapps(before, 'filex.desktop', 'filex-app.desktop');
  assert.ok(after);
  const lines = after.split('\n');
  assert.ok(lines.includes('application/msword=filex-app.desktop'));
  assert.ok(lines.includes('text/plain=gedit.desktop;filex-app.desktop;'));
  assert.ok(lines.includes('x-scheme-handler/filex=filex-app.desktop'));
  assert.ok(lines.includes('application/rtf=filex-app.desktop;'));
  // Outside the three association sections nothing is ours to rewrite.
  assert.equal(lines.filter((l) => l === 'application/msword=filex.desktop').length, 1);
  // A whole list item only: another app whose name merely contains it stays.
  assert.equal(retargetMimeapps('[Default Applications]\na/b=myfilex.desktop;filex.desktop.bak\n', 'filex.desktop', 'filex-app.desktop'), null);
  // Nothing to change → null, so the file is not rewritten at every start.
  assert.equal(retargetMimeapps(after, 'filex.desktop', 'filex-app.desktop'), null);
});
