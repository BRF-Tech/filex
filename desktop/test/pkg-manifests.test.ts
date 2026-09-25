// The winget manifests and the Homebrew cask the release job generates.
//
// Run:  node --experimental-strip-types --test desktop/test/pkg-manifests.test.ts
//
// Both stores pin the installer by its SHA-256 at a URL, and both keep that
// pair forever: a manifest pointing at `latest/download` breaks the day the
// next release ships, and a wrong macOS floor installs an app that does not
// start. These are the promises checked here; `winget validate` and
// `brew style` check the syntax in CI.

import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {
  CASK_TOKEN,
  FILE_EXTENSIONS,
  WINGET_ID,
  homebrewCask,
  macosMinFor,
  run,
  wingetDir,
  wingetManifests,
} from '../scripts/pkg-manifests.mjs';
import { OFFICE_EXTENSIONS } from '../src/openwith.ts';

const SHA = 'ab'.repeat(32);

test('"Open with filex" in winget lists exactly what the app opens', () => {
  assert.deepEqual([...FILE_EXTENSIONS].sort(), [...OFFICE_EXTENSIONS].sort());
});

test('the cask\'s macOS floor follows Electron\'s', () => {
  assert.equal(macosMinFor(31), ':big_sur'); // arm64-only: Apple Silicon starts at 11
  assert.equal(macosMinFor(37), ':big_sur');
  assert.equal(macosMinFor(38), ':monterey');
  assert.equal(macosMinFor(43), ':monterey');
  assert.equal(macosMinFor(44), ':ventura');
});

test('winget: versioned URL, upper-case hash, per-user NSIS, the stable product code', () => {
  const files = wingetManifests({ version: '0.43.3', sha: SHA, releaseDate: '2026-09-25' });
  assert.deepEqual(Object.keys(files).sort(), [
    `${WINGET_ID}.installer.yaml`,
    `${WINGET_ID}.locale.en-US.yaml`,
    `${WINGET_ID}.locale.tr-TR.yaml`,
    `${WINGET_ID}.yaml`,
  ]);
  const inst = files[`${WINGET_ID}.installer.yaml`];
  assert.match(inst, /InstallerUrl: https:\/\/github\.com\/BRF-Tech\/filex\/releases\/download\/v0\.43\.3\/filex-desktop-x64\.exe/);
  assert.doesNotMatch(inst, /latest\/download/);
  assert.match(inst, new RegExp(`InstallerSha256: ${SHA.toUpperCase()}`));
  assert.match(inst, /InstallerType: nullsoft/);
  assert.match(inst, /Scope: user/);
  assert.match(inst, /Custom: \/currentuser/);
  assert.match(inst, /ProductCode: af48dd76-2015-5c31-806d-7410b4687915/);
  assert.match(inst, /DisplayName: filex 0\.43\.3/);
  assert.match(inst, /ReleaseDate: 2026-09-25/);
  for (const f of Object.values(files)) {
    assert.match(f, /ManifestVersion: 1\.12\.0/);
    assert.match(f, /PackageVersion: 0\.43\.3/);
  }
  // The Turkish text keeps its letters.
  assert.match(files[`${WINGET_ID}.locale.tr-TR.yaml`], /Kendi sunucunuzda çalışan dosya yöneticisi/);
});

test('winget-pkgs directory layout', () => {
  assert.equal(wingetDir('0.43.3').split(path.sep).join('/'), 'manifests/b/BRFTech/filex-app/0.43.3');
});

test('cask: versioned URL, the hash, arm64, the macOS floor, a zap that spares the CLI\'s state', () => {
  const rb = homebrewCask({ version: '0.43.3', sha: SHA, macosMin: ':ventura' });
  assert.match(rb, new RegExp(`cask "${CASK_TOKEN}" do`));
  assert.match(rb, /version "0\.43\.3"/);
  assert.match(rb, new RegExp(`sha256 "${SHA}"`));
  assert.match(rb, /url "https:\/\/github\.com\/BRF-Tech\/filex\/releases\/download\/v#\{version\}\/filex-desktop-arm64\.dmg"/);
  assert.match(rb, /depends_on arch: :arm64/);
  assert.match(rb, /depends_on macos: ">= :ventura"/);
  assert.match(rb, /app "filex\.app"/);
  // No Developer ID: the cask tells people how to let the first launch through.
  assert.match(rb, /caveats <<~EOS[\s\S]*Open Anyway[\s\S]*EOS/);
  assert.match(rb, /"~\/\.filex\/desktop"/);
  assert.doesNotMatch(rb, /"~\/\.filex"/);
});

test('run(): hashes the real files and writes both trees', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'pkg-manifests-'));
  try {
    const exe = path.join(dir, 'setup.exe');
    const dmg = path.join(dir, 'app.dmg');
    fs.writeFileSync(exe, 'windows bytes');
    fs.writeFileSync(dmg, 'mac bytes');
    const hash = (f: string) => crypto.createHash('sha256').update(fs.readFileSync(f)).digest('hex');
    const out = path.join(dir, 'out');
    const written = run(['--version', '1.1.0', '--windows', exe, '--mac', dmg, '--out', out]);
    assert.equal(written.length, 5);
    const inst = fs.readFileSync(path.join(out, 'winget', wingetDir('1.1.0'), `${WINGET_ID}.installer.yaml`), 'utf8');
    assert.match(inst, new RegExp(hash(exe).toUpperCase()));
    const rb = fs.readFileSync(path.join(out, 'homebrew', 'Casks', `${CASK_TOKEN}.rb`), 'utf8');
    assert.match(rb, new RegExp(`sha256 "${hash(dmg)}"`));
    assert.throws(() => run(['--version', 'v1.1.0', '--windows', exe, '--out', out]), /x\.y\.z/);
    assert.throws(() => run(['--version', '1.1.0', '--out', out]), /--windows and\/or --mac/);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
