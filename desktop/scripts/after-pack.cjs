// afterPack: every step electron-builder.yml runs on a packed app, in order.
//
// electron-builder takes ONE afterPack hook, so the per-platform steps are
// collected here; each returns at once on a platform that is not its own.
//   - linux-launcher.cjs: the launcher in front of the Electron binary (Linux)
//   - adhoc-sign.cjs: the ad-hoc bundle seal of an unsigned build (macOS)
'use strict';

const linuxLauncher = require('./linux-launcher.cjs');
const adhocSign = require('./adhoc-sign.cjs');

module.exports = async function afterPack(context) {
  await linuxLauncher(context);
  await adhocSign(context);
};
