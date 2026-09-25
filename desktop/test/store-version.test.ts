// The Microsoft Store package's version, and the manifest it is written into.
//
// Run:  node --experimental-strip-types --test desktop/test/store-version.test.ts
//
// The Store refuses a first number of 0, keeps the fourth for itself, and only
// accepts a package HIGHER than the last one. filex is 0.x, so its Store
// number is a mapping — and a mapping that ever goes backwards is a release
// the Store will not take. These tests pin the order across the 0.x → 1.1
// hand-over, which is the one place it can break.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import appx from '../scripts/appx-manifest.cjs';

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');

const { storeVersion, patchManifest } = appx as unknown as {
  storeVersion: (v: string) => string;
  patchManifest: (xml: string, v: string) => string;
};

/** Store versions compare numerically, part by part. */
function cmp(a: string, b: string): number {
  const x = a.split('.').map(Number);
  const y = b.split('.').map(Number);
  for (let i = 0; i < 4; i++) if (x[i] !== y[i]) return x[i] - y[i];
  return 0;
}

test('0.x lives under 1.0, with the patch in the third number', () => {
  assert.equal(storeVersion('0.43.0'), '1.0.4300.0');
  assert.equal(storeVersion('0.43.1'), '1.0.4301.0');
  assert.equal(storeVersion('0.44.0'), '1.0.4400.0');
  assert.equal(storeVersion('0.9.3'), '1.0.903.0');
});

test('from 1.1 on, the Store number IS the filex number', () => {
  assert.equal(storeVersion('1.1.0'), '1.1.0.0');
  assert.equal(storeVersion('1.2.7'), '1.2.7.0');
  assert.equal(storeVersion('2.0.0'), '2.0.0.0');
});

test('every step of a realistic release history goes UP', () => {
  const history = ['0.43.0', '0.43.1', '0.43.9', '0.44.0', '0.99.99', '1.1.0', '1.1.1', '1.2.0', '2.0.0'];
  const mapped = history.map(storeVersion);
  for (let i = 1; i < mapped.length; i++) {
    assert.ok(cmp(mapped[i], mapped[i - 1]) > 0, `${history[i]} (${mapped[i]}) must be above ${history[i - 1]} (${mapped[i - 1]})`);
  }
  // The fourth number is the Store's.
  for (const v of mapped) assert.equal(v.split('.')[3], '0');
});

test('a 1.0.x release is refused — it would sort below every 0.x already published', () => {
  assert.throws(() => storeVersion('1.0.0'), /straight to 1\.1\.0/);
  assert.throws(() => storeVersion('1.0.5'), /lower than every 0\.x release/);
});

test('what the mapping cannot carry is refused, not wrapped', () => {
  assert.throws(() => storeVersion('0.43.100'), /above 99/);
  assert.throws(() => storeVersion('0.656.0'), /0-65535/);
  assert.throws(() => storeVersion('0.44.0-rc.1'), /plain x\.y\.z/);
  assert.throws(() => storeVersion('v0.44.0'), /plain x\.y\.z/);
});

// The shape electron-builder 24.13.3 writes (templates/appx/appxmanifest.xml).
const GENERATED = `<?xml version="1.0" encoding="utf-8"?>
<Package
   xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10"
   xmlns:uap="http://schemas.microsoft.com/appx/manifest/uap/windows10"
   xmlns:desktop="http://schemas.microsoft.com/appx/manifest/desktop/windows10"
   xmlns:rescap="http://schemas.microsoft.com/appx/manifest/foundation/windows10/restrictedcapabilities">
  <Identity Name="BRFTeknoloji.filex"
    ProcessorArchitecture="x64"
    Publisher='CN=00000000-0000-0000-0000-000000000000'
    Version="0.43.1.0" />
  <Dependencies>
    <TargetDeviceFamily Name="Windows.Desktop" MinVersion="10.0.14316.0" MaxVersionTested="10.0.14316.0" />
  </Dependencies>
</Package>`;

test('the manifest gets the Store version, the namespaces and the Windows range', () => {
  const out = patchManifest(GENERATED, '0.43.1');
  assert.match(out, /<Identity\b[^>]*Version="1\.0\.4301\.0"/s);
  assert.doesNotMatch(out, /Version="0\.43\.1\.0"/);
  assert.match(out, /xmlns:uap3="http:\/\/schemas\.microsoft\.com\/appx\/manifest\/uap\/windows10\/3"/);
  assert.match(out, /xmlns:uap10="http:\/\/schemas\.microsoft\.com\/appx\/manifest\/uap\/windows10\/10"/);
  assert.match(out, /IgnorableNamespaces="uap3 uap10"/);
  assert.match(out, /MinVersion="10\.0\.19041\.0"/);
  assert.match(out, /MaxVersionTested="10\.0\.26100\.0"/);
  // The XML declaration's own version="1.0" is not a package version.
  assert.match(out, /^<\?xml version="1\.0"/);
});

test('patching twice changes nothing the second time', () => {
  const once = patchManifest(GENERATED, '0.43.1');
  assert.equal(patchManifest(once, '0.43.1'), once);
});

test('build/appx-extensions.xml has no double hyphen inside a comment', () => {
  // XML forbids "--" inside <!-- -->, and makeappx then refuses the WHOLE
  // manifest with "'>' expected" and a line number in the generated file —
  // measured: a comment that named the tray flag by its spelling did it.
  const xml = fs.readFileSync(path.join(ROOT, 'build', 'appx-extensions.xml'), 'utf8');
  const bad = [...xml.matchAll(/<!--([\s\S]*?)-->/g)].filter((m) => m[1].includes('--'));
  assert.deepEqual(bad.map((m) => m[1].trim().slice(0, 60)), []);
});

test('a manifest with no Identity version fails loudly instead of shipping 0.x', () => {
  assert.throws(() => patchManifest('<Package><Identity Name="x" /></Package>', '0.43.1'), /no <Identity/);
});
