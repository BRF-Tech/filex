// afterPack (Linux): the AppImage starts through electron-builder 24's AppRun.
//
// electron-builder 26 writes its own AppRun (app-builder-lib
// out/targets/appimage/appImageUtil.js, generateAppRunScript), and that one
// starts the app with --no-sandbox whenever `unshare -Ur true` fails: on
// Ubuntu 23.10 and later, an AppImage with no AppArmor profile. The launcher
// (build/linux/launcher.sh) takes a --no-sandbox on its command line as the
// person's decision and passes it through, so the image would open with
// Chromium's sandbox off where every AppImage since 0.50 refused and said
// what to do. 26 has no setting for its AppRun.
//
// What it does have: it writes its AppRun into the AppImage's staging
// directory FIRST and copies the packed app over that directory AFTER
// (buildLegacyFuse2AppImage and buildStaticRuntimeAppImage), with a plain
// file copy that replaces what is there. So an `AppRun` at the top of the
// packed app is the AppRun the image carries. This writes
// build/linux/AppRun.sh (24.13.3's, word for word) there, with the
// executable's name filled in, when the pack builds an AppImage, and removes
// it otherwise: the packed directory is reused by the next pack (the snap is
// packed on its own after dist:linux), and the snap must not carry it. The
// .deb and the .rpm, packed from the same directory as the AppImage, leave it
// out (`--exclude=opt/filex/AppRun` in electron-builder.yml).
// desktop/test/appimage-apprun.test.ts holds the order in electron-builder's
// source, so an upgrade that changes it fails there and not on a user's
// machine.
//
// CommonJS for the same reason as linux-launcher.cjs: electron-builder
// require()s its hooks and desktop/package.json is "type": "module".
'use strict';

const fs = require('node:fs');
const path = require('node:path');

const APPRUN = path.join(__dirname, '..', 'build', 'linux', 'AppRun.sh');
const PLACEHOLDER = '{{.ExecutableName}}';

/** AppRun for `executableName`, LF line ends whatever the checkout did. */
function appRunText(executableName) {
  if (!/^[A-Za-z0-9._-]+$/.test(executableName)) throw new Error(`appimage-apprun: "${executableName}" is not a file name AppRun can start`);
  const text = fs.readFileSync(APPRUN, 'utf8').replace(/\r\n/g, '\n');
  if (!text.includes(PLACEHOLDER)) throw new Error(`appimage-apprun: ${APPRUN} names no executable`);
  return text.split(PLACEHOLDER).join(executableName);
}

/** Whether this pack builds an AppImage (electron-builder's target name). */
function buildsAppImage(targets) {
  return Array.isArray(targets) && targets.some((t) => t && t.name === 'appImage');
}

/** Writes or removes `<appOutDir>/AppRun`; returns what it did. */
function placeAppRun(appOutDir, executableName, targets) {
  const out = path.join(appOutDir, 'AppRun');
  if (!buildsAppImage(targets)) {
    fs.rmSync(out, { force: true });
    return { out, written: false };
  }
  fs.writeFileSync(out, appRunText(executableName), { mode: 0o755 });
  fs.chmodSync(out, 0o755);
  return { out, written: true };
}

module.exports = async function appImageAppRun(context) {
  if (context.electronPlatformName !== 'linux') return;
  placeAppRun(context.appOutDir, context.packager.executableName, context.targets);
};
module.exports.placeAppRun = placeAppRun;
module.exports.appRunText = appRunText;
module.exports.APPRUN = APPRUN;
