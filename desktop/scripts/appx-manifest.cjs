// electron-builder `appxManifestCreated` hook: turns the manifest it generated
// into one the Microsoft Store accepts and filex can live with.
//
// electron-builder 24.13 writes the manifest from a fixed template, and three
// things in it do not fit:
//
//   1. The VERSION. The Store refuses a package whose first number is 0 and
//      reserves the fourth for itself (it must be 0) — "The other sections
//      must be set to an integer between 0 and 65535 (except for the first
//      section, which cannot be 0)". electron-builder copies package.json,
//      so filex 0.43.1 would go out as 0.43.1.0 and be refused. See
//      storeVersion() for the mapping and why it has the shape it has.
//   2. The NAMESPACES. The template declares uap, desktop and rescap only;
//      the protocol and file-type entries in build/appx-extensions.xml need
//      uap3 (for `Parameters`, so the link or the document arrives in argv
//      exactly as filex already reads it) and the startup task needs uap10
//      (for `uap10:Parameters="--hidden"` — the same flag the NSIS build's
//      Run entry carries, so a sign-in launch stays in the tray).
//   3. The target Windows VERSIONS: hard-coded to 10.0.14316.0 for both
//      MinVersion and MaxVersionTested. uap10 needs Windows 10 2004
//      (10.0.19041.0); Electron 31 does not run on anything older anyway.
//
// ⚠ Only the manifest changes. The app itself still reports its real version
// (0.43.1) in Settings and in every request it makes — the Store number is a
// packaging detail and stays in the package.
//
// Pure functions are exported for desktop/test/store-version.test.ts.

'use strict';

const fs = require('node:fs');
const path = require('node:path');

const MIN_VERSION = '10.0.19041.0';
const MAX_VERSION_TESTED = '10.0.26100.0';

const NAMESPACES = {
  uap3: 'http://schemas.microsoft.com/appx/manifest/uap/windows10/3',
  uap10: 'http://schemas.microsoft.com/appx/manifest/uap/windows10/10',
};

/**
 * filex's version → the version the Store package carries.
 *
 *   0.43.0  → 1.0.4300.0
 *   0.43.1  → 1.0.4301.0      (the patch lives in the third number: the fourth is the Store's)
 *   1.1.0   → 1.1.0.0         (from here on the two numbers are the same)
 *
 * ⚠⚠ filex must NEVER ship a 1.0.x. It would map to 1.0.x.0 — BELOW every
 * 0.x release already in the Store (1.0.4300.0 and up), and the Store only
 * accepts a package whose version is higher than the last one. The plan
 * (Burak, 2026-09-24) is to go from the last 0.x straight to 1.1.0.
 *
 * Every other way to squeeze 0.x in costs something forever: "major + 1"
 * (0.43.1 → 1.43.1.0) would make filex 1.1 into Store 2.1, and the two
 * numbers would never meet again.
 */
function storeVersion(version) {
  const m = /^(\d+)\.(\d+)\.(\d+)$/.exec(String(version).trim());
  if (!m) {
    // A pre-release (0.44.0-rc.1) has no place in a Store submission: the
    // Store has package flighting for betas, with ordinary version numbers.
    throw new Error(`the Store build needs a plain x.y.z version, got "${version}"`);
  }
  const [major, minor, patch] = m.slice(1).map(Number);
  const out = (a, b, c) => {
    for (const n of [a, b, c]) {
      if (n > 65535) throw new Error(`"${version}" maps to ${a}.${b}.${c}.0, and each part must fit 0-65535`);
    }
    return `${a}.${b}.${c}.0`;
  };
  if (major === 0) {
    if (patch > 99) {
      throw new Error(`"${version}": a 0.x patch number above 99 does not fit the minor*100+patch mapping`);
    }
    return out(1, 0, minor * 100 + patch);
  }
  if (major === 1 && minor === 0) {
    throw new Error(
      `"${version}" would be Store version 1.0.${patch}.0 — lower than every 0.x release already published ` +
        '(1.0.4300.0 and up), so the Store would refuse it. filex goes from 0.x straight to 1.1.0.',
    );
  }
  return out(major, minor, patch);
}

/** Rewrites a generated AppxManifest.xml; returns the new text. */
function patchManifest(xml, version) {
  let out = xml;

  // 1. Identity/@Version — the only Version attribute in the file.
  const identityVersion = /(<Identity\b[^>]*?\bVersion=")[^"]*(")/s;
  if (!identityVersion.test(out)) throw new Error('AppxManifest.xml: no <Identity Version="…"> to rewrite');
  out = out.replace(identityVersion, `$1${storeVersion(version)}$2`);

  // 2. Namespaces on <Package>, plus IgnorableNamespaces so an older Windows
  //    skips what it does not know instead of refusing the package.
  out = out.replace(/<Package\b([^>]*)>/s, (tag, attrs) => {
    let a = attrs;
    for (const [prefix, uri] of Object.entries(NAMESPACES)) {
      if (!new RegExp(`xmlns:${prefix}=`).test(a)) a += `\n   xmlns:${prefix}="${uri}"`;
    }
    const ignorable = new Set((/IgnorableNamespaces="([^"]*)"/.exec(a)?.[1] ?? '').split(/\s+/).filter(Boolean));
    for (const p of ['uap3', 'uap10']) ignorable.add(p);
    a = a.replace(/\s*IgnorableNamespaces="[^"]*"/, '');
    a += `\n   IgnorableNamespaces="${[...ignorable].join(' ')}"`;
    return `<Package${a}>`;
  });

  // 3. TargetDeviceFamily.
  out = out.replace(/(<TargetDeviceFamily\b[^>]*?\bMinVersion=")[^"]*(")/, `$1${MIN_VERSION}$2`);
  out = out.replace(/(<TargetDeviceFamily\b[^>]*?\bMaxVersionTested=")[^"]*(")/, `$1${MAX_VERSION_TESTED}$2`);

  return out;
}

module.exports = async function appxManifestCreated(manifestPath) {
  // The version the build is stamped with: release.yml writes the tag into
  // package.json (`npm version`) before electron-builder runs.
  const pkg = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'package.json'), 'utf8'));
  const xml = fs.readFileSync(manifestPath, 'utf8');
  fs.writeFileSync(manifestPath, patchManifest(xml, pkg.version), 'utf8');
  console.log(`  • appx manifest: filex ${pkg.version} → Store package ${storeVersion(pkg.version)}`);
};
module.exports.storeVersion = storeVersion;
module.exports.patchManifest = patchManifest;
