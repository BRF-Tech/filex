// A plugin must not be built against a guest SDK copy that has gone stale.
//
// ⚠⚠ Measured 2026-09-24, on the v0.43.0 release branch. filex-sign and
// filex-convert build against a GENERATED copy of `backend/pkg/pluginkit`
// (`scripts/pluginkit-devmodule.sh` writes it; each app's go.mod reaches it
// with `replace … => ../filex-sdk-dev`). Nothing regenerated that copy as
// part of a build, and `pkg/pluginkit` moved three times underneath it, so
// both apps were built against a host contract the server does not ship.
//
// Not one thing went red. The apps compiled, their tests passed, their
// manifests stamped cleanly. The only symptom was that two agents measured
// two different sha256 for the same commit of the same repository, which
// reads like a flaky toolchain — and it cost an hour before it read like what
// it was. The eventual proof was regenerating the SDK from the older
// pluginkit and watching the old hash come back byte for byte.
//
// So the copy now carries a stamp of the pluginkit it came from, and the
// build refuses when the two have drifted. These are the tests for it.
import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  checkSdk,
  crossSpelling,
  replaceTarget,
  sourceCandidates,
  treeHash,
  writeStamp,
  STAMP_FILE,
} from '../../../scripts/lib/sdk-check.mjs';

const REPO = path.resolve(__dirname, '../../..');
const SCRIPT = path.join(REPO, 'scripts', 'lib', 'sdk-check.mjs');
const PLUGINKIT = path.join(REPO, 'backend', 'pkg', 'pluginkit');

/** A throwaway SDK copy: the shape the generator writes, nothing more. */
function fakeSdk(): { dir: string; src: string } {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-sdk-test-'));
  const src = path.join(root, 'src', 'pluginkit');
  fs.mkdirSync(path.join(src, 'sub'), { recursive: true });
  fs.writeFileSync(path.join(src, 'a.go'), 'package pluginkit\n\nfunc A() {}\n');
  fs.writeFileSync(path.join(src, 'sub', 'b.go'), 'package sub\n\nfunc B() {}\n');
  fs.writeFileSync(path.join(src, 'a_test.go'), 'package pluginkit\n\n// not carried\n');
  const dir = path.join(root, 'sdk');
  fs.mkdirSync(path.join(dir, 'pkg'), { recursive: true });
  fs.cpSync(src, path.join(dir, 'pkg', 'pluginkit'), { recursive: true });
  fs.rmSync(path.join(dir, 'pkg', 'pluginkit', 'a_test.go'));
  writeStamp(dir, src);
  return { dir, src };
}

describe('the guest SDK staleness guard', () => {
  it('a freshly generated copy is current', () => {
    const { dir } = fakeSdk();
    expect(checkSdk({ sdkDir: dir })).toMatchObject({ ok: true, reason: 'current' });
  });

  it('REFUSES once the pluginkit it came from has moved on', () => {
    const { dir, src } = fakeSdk();
    fs.appendFileSync(path.join(src, 'a.go'), '\nfunc Added() {}\n');
    const res = checkSdk({ sdkDir: dir });
    expect(res.ok).toBe(false);
    expect(res.reason).toBe('stale');
    expect(res.message).toContain('STALE');
  });

  it('refuses a copy somebody edited by hand', () => {
    const { dir } = fakeSdk();
    fs.appendFileSync(path.join(dir, 'pkg', 'pluginkit', 'a.go'), '\nfunc Sneaked() {}\n');
    expect(checkSdk({ sdkDir: dir })).toMatchObject({ ok: false, reason: 'edited' });
  });

  it('refuses a copy with no stamp, and one whose source is gone', () => {
    const { dir } = fakeSdk();
    const stamp = path.join(dir, STAMP_FILE);
    const kept = fs.readFileSync(stamp, 'utf8');
    fs.rmSync(stamp);
    expect(checkSdk({ sdkDir: dir })).toMatchObject({ ok: false, reason: 'no-stamp' });
    // ⚠ BOTH spellings of the source have to be gone: the relative one alone
    // would still find it, which is the point of writing it.
    const gone = JSON.parse(kept);
    gone.source = '/nowhere/pluginkit';
    gone.source_rel = '../nowhere/pluginkit';
    fs.writeFileSync(stamp, JSON.stringify(gone));
    expect(checkSdk({ sdkDir: dir })).toMatchObject({ ok: false, reason: 'no-source' });
  });

  // ⚠ The released apps drop the replace and use the published module. The
  // guard must be silent there, or it becomes something to switch off.
  it('says nothing when go.mod points at the published module', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-rel-'));
    fs.writeFileSync(
      path.join(dir, 'go.mod'),
      'module example.com/app\n\ngo 1.25\n\nrequire github.com/brf-tech/filex/backend v0.43.0\n',
    );
    expect(checkSdk({ cwd: dir })).toMatchObject({ ok: true, silent: true, reason: 'published' });
    const out = execFileSync(process.execPath, [SCRIPT], { cwd: dir, encoding: 'utf8' });
    expect(out).toBe('');
  });

  it('reads the replace whether it is a line or a block', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-gomod-'));
    const one = path.join(dir, 'one.mod');
    fs.writeFileSync(one, 'module x\n\nreplace github.com/brf-tech/filex/backend => ../filex-sdk-dev\n');
    expect(replaceTarget(one)).toBe(path.resolve(dir, '../filex-sdk-dev'));
    const many = path.join(dir, 'many.mod');
    fs.writeFileSync(many, 'module x\n\nreplace (\n\tgithub.com/brf-tech/filex/backend => ../filex-sdk-dev\n)\n');
    expect(replaceTarget(many)).toBe(path.resolve(dir, '../filex-sdk-dev'));
    // A version, not a directory — that is the published kind, not a copy.
    const ver = path.join(dir, 'ver.mod');
    fs.writeFileSync(ver, 'module x\n\nreplace example.com/a => example.com/b v1.2.3\n');
    expect(replaceTarget(ver)).toBeNull();
  });

  it('the exit code is 1 and the message names the command that fixes it', () => {
    const { dir, src } = fakeSdk();
    fs.appendFileSync(path.join(src, 'a.go'), '\nfunc Added() {}\n');
    let status = 0;
    let stderr = '';
    try {
      execFileSync(process.execPath, [SCRIPT, '--sdk', dir], { encoding: 'utf8', stdio: 'pipe' });
    } catch (err) {
      const e = err as { status: number; stderr: string };
      status = e.status;
      stderr = e.stderr;
    }
    expect(status).toBe(1);
    expect(stderr).toContain('pluginkit-devmodule.sh');
    expect(stderr).toContain(dir);
  });

  // ── the same machine, seen from both sides ──
  //
  // ⚠⚠ The v0.43.0 release run: the copy was generated under WSL, which
  // stamped `/mnt/g/…`, and the check was then run from Git Bash on Windows,
  // where that path does not exist — `no-source`, exit 1, on a copy that was
  // perfectly current. A guard that refuses a good build gets switched off.

  it('spells one path both ways: /mnt/g/x <-> G:/x', () => {
    const bs = String.fromCharCode(92);
    expect(crossSpelling('/mnt/g/filex-wt-apps/backend', 'win32')).toBe('G:/filex-wt-apps/backend');
    expect(crossSpelling(`G:${bs}filex-wt-apps${bs}backend`, 'linux')).toBe('/mnt/g/filex-wt-apps/backend');
    expect(crossSpelling('G:/a/b', 'linux')).toBe('/mnt/g/a/b');
    expect(crossSpelling('/mnt/g', 'win32')).toBe('G:/');
    // Nothing to map: a Linux path with no drive, a relative path, nothing.
    expect(crossSpelling('/home/x', 'win32')).toBeNull();
    expect(crossSpelling('pkg/pluginkit', 'linux')).toBeNull();
    expect(crossSpelling('', 'win32')).toBeNull();
  });

  it('writes the source relative to the copy, and reads that first', () => {
    const { dir, src } = fakeSdk();
    const stamp = JSON.parse(fs.readFileSync(path.join(dir, STAMP_FILE), 'utf8'));
    expect(stamp.source_rel, 'relative to the copy, forward slashes').toBe('../src/pluginkit');
    expect(sourceCandidates(dir, stamp)[0]).toBe(path.resolve(dir, '../src/pluginkit'));
    expect(path.resolve(dir, stamp.source_rel)).toBe(path.resolve(src));
  });

  // The failing case itself: the absolute path is the OTHER side's spelling
  // and does not exist here, and the copy is still recognised as current.
  it('a copy stamped on the other side is still current here (the Windows run)', () => {
    const { dir } = fakeSdk();
    const stampPath = path.join(dir, STAMP_FILE);
    const stamp = JSON.parse(fs.readFileSync(stampPath, 'utf8'));
    stamp.source = '/mnt/q/somewhere/that/is/not/this/machine/pluginkit';
    fs.writeFileSync(stampPath, JSON.stringify(stamp));
    expect(checkSdk({ sdkDir: dir })).toMatchObject({ ok: true, reason: 'current' });
  });

  // No relative path at all (a copy on another drive from its source): the
  // cross spelling alone has to find it. Only measurable where a temp path
  // HAS a cross spelling — i.e. on Windows, which is exactly where it failed.
  it('with no relative path, the other spelling of the absolute one is found', () => {
    const { dir, src } = fakeSdk();
    const other = crossSpelling(path.resolve(src), process.platform === 'win32' ? 'linux' : 'win32');
    if (!other) return; // a Linux temp dir has no drive letter to map
    const stampPath = path.join(dir, STAMP_FILE);
    const stamp = JSON.parse(fs.readFileSync(stampPath, 'utf8'));
    stamp.source = other;
    delete stamp.source_rel;
    fs.writeFileSync(stampPath, JSON.stringify(stamp));
    expect(checkSdk({ sdkDir: dir })).toMatchObject({ ok: true, reason: 'current' });
  });

  // …and being found from the other side must not make a stale copy pass.
  it('found through the other spelling, a stale copy is still refused', () => {
    const { dir, src } = fakeSdk();
    const stampPath = path.join(dir, STAMP_FILE);
    const stamp = JSON.parse(fs.readFileSync(stampPath, 'utf8'));
    stamp.source = '/mnt/q/not/here/pluginkit';
    fs.writeFileSync(stampPath, JSON.stringify(stamp));
    fs.appendFileSync(path.join(src, 'a.go'), '\nfunc Moved() {}\n');
    expect(checkSdk({ sdkDir: dir })).toMatchObject({ ok: false, reason: 'stale' });
  });

  // Content, as bytes: a line-ending change must not slip past.
  it('hashes bytes, skips tests, and notices a CRLF-only change', () => {
    const { src } = fakeSdk();
    const before = treeHash(src).hash;
    const f = path.join(src, 'a.go');
    fs.writeFileSync(f, fs.readFileSync(f, 'utf8').replace(/\n/g, '\r\n'));
    expect(treeHash(src).hash).not.toBe(before);
    expect(treeHash(src).count).toBe(2); // a.go + sub/b.go, never a_test.go
  });

  // ⚠ The wiring, not just the logic: a guard nothing calls guards nothing.
  it('the generator writes the stamp and the checker into the copy', () => {
    const gen = fs.readFileSync(path.join(REPO, 'scripts', 'pluginkit-devmodule.sh'), 'utf8');
    expect(gen).toContain('cp "$root/scripts/lib/sdk-check.mjs" "$out/check-sdk.mjs"');
    expect(gen).toMatch(/check-sdk\.mjs" --write "\$out" "\$root\/backend\/pkg\/pluginkit"/);
  });

  it('the real pluginkit hashes, and every file the generator carries is counted', () => {
    const { hash, count } = treeHash(PLUGINKIT);
    expect(hash).toMatch(/^[0-9a-f]{64}$/);
    expect(count).toBeGreaterThan(10);
  });
});
