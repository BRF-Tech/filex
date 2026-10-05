// afterPack (Linux): the launcher goes in front of the Electron binary.
//
// electron-builder leaves the Electron binary in the packed directory under
// `linux.executableName` (filex-app). This renames it to `filex-app-bin` and
// puts desktop/build/linux/launcher.sh in its place, so everything that starts
// the app by that name - /usr/bin/filex-app and the menu entry of the .deb and
// the .rpm, the AppImage's AppRun, the snap's command.sh - goes through the
// launcher first. The launcher checks that Chromium's sandbox can be built and,
// when it cannot, says what to do and exits; outside a snap it never starts the
// app without it. In the snap it starts the app with --no-sandbox, under the
// snap's strict confinement (since 0.52; see the file).
//
// Every Linux target is made from this one directory, so one hook covers the
// four of them; Windows and macOS packs are left alone.
//
// What the rename does NOT change, measured and held by tests:
//   - Chromium's own children start /proc/self/exe (filex-app-bin), and it
//     looks for chrome-sandbox beside that file: the same directory.
//   - WM_CLASS and the Wayland app id come from the app's name and
//     CHROME_DESKTOP, not from the binary's file name.
//   - `process.execPath` becomes .../filex-app-bin. The one place that writes
//     it down for later, the "Start when I sign in" entry, names the launcher
//     instead (src/login-item.ts loginItemExecutable); app.relaunch() after a
//     .deb or .rpm update starts filex-app-bin directly, whose helper is in place.
//
// CommonJS for the same reason as adhoc-sign.cjs: electron-builder require()s
// its hooks and desktop/package.json is "type": "module".
'use strict';

const fs = require('node:fs');
const path = require('node:path');

const LAUNCHER = path.join(__dirname, '..', 'build', 'linux', 'launcher.sh');

/** The launcher's text with LF line ends, whatever the checkout did to it: a
 *  CR after the shebang is "/bin/sh^M: bad interpreter" on every machine. */
function launcherText() {
  return fs.readFileSync(LAUNCHER, 'utf8').replace(/\r\n/g, '\n');
}

/**
 * Moves `<dir>/<name>` to `<dir>/<name>-bin` and writes the launcher as
 * `<dir>/<name>`. Safe to run twice on the same directory: a launcher already
 * in place is left as it is, and a directory with a `-bin` but no launcher is
 * refused rather than guessed at.
 */
function installLauncher(appOutDir, executableName) {
  const exe = path.join(appOutDir, executableName);
  const bin = `${exe}-bin`;
  const text = launcherText();
  if (fs.existsSync(bin)) {
    if (fs.existsSync(exe) && fs.readFileSync(exe, 'utf8') === text) return { exe, bin, moved: false };
    throw new Error(`linux-launcher: ${bin} exists but ${exe} is not the launcher`);
  }
  if (!fs.existsSync(exe)) throw new Error(`linux-launcher: ${exe} is missing; nothing to put the launcher in front of`);
  fs.renameSync(exe, bin);
  fs.writeFileSync(exe, text, { mode: 0o755 });
  fs.chmodSync(exe, 0o755);
  fs.chmodSync(bin, 0o755);
  return { exe, bin, moved: true };
}

module.exports = async function linuxLauncher(context) {
  if (context.electronPlatformName !== 'linux') return;
  installLauncher(context.appOutDir, context.packager.executableName);
};
module.exports.installLauncher = installLauncher;
module.exports.LAUNCHER = LAUNCHER;
