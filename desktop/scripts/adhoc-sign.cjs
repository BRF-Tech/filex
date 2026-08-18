// afterPack: give unsigned macOS builds a real (ad-hoc) bundle seal.
//
// electron-builder with no certificate SKIPS signing, which leaves the app
// only "linker-signed": every Mach-O keeps the ad-hoc signature the linker
// stamped at build time, but the BUNDLE has no seal — Info.plist not bound,
// no sealed resources. macOS treats that half-signature as tampering, and on
// macOS 26 a downloaded copy is greeted with "malware blocked and moved to
// Trash", with no override offered (measured 2026-08-18, filex-desktop-arm64
// v0.20.2 out of the dmg). A forced deep ad-hoc re-sign is what turns that
// into the honest "unverified developer" dialog with the System Settings →
// Open Anyway path. It costs nothing and needs no certificate.
//
// ⚠ CommonJS on purpose: desktop/package.json is `"type": "module"`, and
// electron-builder loads hooks with require(); a .cjs file works under both.
//
// Skipped when a real identity is configured (CSC_LINK / CSC_NAME): osx-sign
// already produced a proper signature and a --force re-sign would replace the
// Developer ID seal with an ad-hoc one — strictly worse.
'use strict';

const { execFileSync } = require('node:child_process');
const path = require('node:path');

module.exports = async function adhocSign(context) {
  if (context.electronPlatformName !== 'darwin') return;
  if (process.env.CSC_LINK || process.env.CSC_NAME) return;

  const app = path.join(
    context.appOutDir,
    `${context.packager.appInfo.productFilename}.app`,
  );

  execFileSync('codesign', ['--force', '--deep', '--sign', '-', app], { stdio: 'inherit' });
  // Verify, so a broken seal fails THIS build instead of a user's first launch.
  execFileSync('codesign', ['--verify', '--deep', '--strict', app], { stdio: 'inherit' });
  console.log(`  • ad-hoc sealed ${path.basename(app)} (no certificate — Gatekeeper will show "unverified developer")`);
};
