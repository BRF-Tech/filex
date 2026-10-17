// The Windows version resource every filex.exe carries (task #259,
// scripts/lib/winres.mjs): the CLI's zip (.goreleaser.yml's `before` hook)
// and the desktop app's resources\bin\filex.exe (desktop/scripts/fetch-cli.mjs),
// one source for both.
//
// ⚠ Why: a security product quarantined the desktop app's sync engine. A
// filex.exe without a version resource is an anonymous program - no company,
// no product, no version in its Properties - and nothing tied it to the app
// that ran it. These hold that the version string goes into the resource,
// that both builds make it from the same place, and that a build reads it
// back off the binary. No signature: GitHub's packages stay unsigned.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import {
  GO_WINRES,
  VERSION_STRINGS,
  exeVersionStrings,
  fixedVersion,
  sysoFiles,
  versionInfo,
  versionResourceProblems,
  writeVersionResource,
} from '../../../scripts/lib/winres.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const read = (rel: string) => fs.readFileSync(path.join(REPO, rel), 'utf8');

/** A StringFileInfo String entry as Windows lays it out: key, NUL, padding, value, NUL. */
const entry = (key: string, value: string) => Buffer.concat([Buffer.from(`${key}\0`, 'utf16le'), Buffer.alloc(2), Buffer.from(`${value}\0`, 'utf16le')]);

describe('the Windows version resource (#259)', () => {
  it('names the company, the product, what it is, its file name and its version', () => {
    expect(VERSION_STRINGS).toMatchObject({ CompanyName: 'BRF Tech', ProductName: 'filex', OriginalFilename: 'filex.exe' });
    expect(VERSION_STRINGS.FileDescription).toMatch(/\S/);
    const v = versionInfo('0.57.0');
    const block = v.RT_VERSION['#1']['0000'];
    expect(block.fixed).toEqual({ file_version: '0.57.0.0', product_version: '0.57.0.0' });
    expect(block.info['0409']).toEqual({ ...VERSION_STRINGS, FileVersion: '0.57.0', ProductVersion: '0.57.0' });
    // A nightly and a snapshot keep their whole version in the strings; the
    // four numbers Windows keeps beside them drop what is not a number.
    const nightly = versionInfo('v0.58.0-nightly.20261018+cccccccc').RT_VERSION['#1']['0000'];
    expect(nightly.info['0409'].ProductVersion).toBe('0.58.0-nightly.20261018+cccccccc');
    expect(nightly.fixed.file_version).toBe('0.58.0.0');
    expect(fixedVersion('0.57.1-SNAPSHOT-abc1234')).toBe('0.57.1.0');
    expect(() => fixedVersion('dev')).toThrow(/no version/);
    // Nothing that would change how Windows starts it: no manifest, no icon.
    expect(Object.keys(v)).toEqual(['RT_VERSION']);
  });

  it('puts the version string into the resource go-winres makes, for each architecture asked', () => {
    const backend = fs.mkdtempSync(path.join(os.tmpdir(), 'winres-'));
    try {
      fs.mkdirSync(path.join(backend, 'cmd', 'filex'), { recursive: true });
      const calls: { bin: string; args: string[]; json: unknown; cwd: string }[] = [];
      const run = (bin: string, args: string[], { cwd }: { cwd: string }) => {
        const json = JSON.parse(fs.readFileSync(args[args.indexOf('--in') + 1], 'utf8'));
        calls.push({ bin, args, json, cwd });
        for (const a of args[args.indexOf('--arch') + 1].split(',')) fs.writeFileSync(path.join(cwd, 'cmd', 'filex', `rsrc_windows_${a}.syso`), 'x');
        return { status: 0, stdout: '', stderr: '' };
      };
      const files = writeVersionResource({ backend, version: '0.57.0', arches: ['arm64'], run });
      expect(files).toEqual([path.join(backend, 'cmd', 'filex', 'rsrc_windows_arm64.syso')]);
      expect(calls).toHaveLength(1);
      const [c] = calls;
      expect(c.bin).toBe('go');
      expect(c.args.slice(0, 3)).toEqual(['run', GO_WINRES, 'make']);
      expect(c.args.slice(c.args.indexOf('--out'), c.args.indexOf('--out') + 2)).toEqual(['--out', path.join('cmd', 'filex', 'rsrc')]);
      expect(c.cwd).toBe(backend);
      expect(c.json).toEqual(versionInfo('0.57.0'));
      expect(JSON.stringify(c.json)).toContain('"ProductVersion":"0.57.0"');
      // Pinned: a tool that changes under a release is not run.
      expect(GO_WINRES).toMatch(/^github\.com\/tc-hib\/go-winres@v\d+\.\d+\.\d+$/);
      // go-winres failing, or writing nothing, stops the build.
      expect(() => writeVersionResource({ backend, version: '0.57.0', arches: ['amd64'], run: () => ({ status: 1, stdout: '', stderr: 'boom' }) })).toThrow(/go-winres failed \(exit 1\): boom/);
      expect(() => writeVersionResource({ backend, version: '0.57.0', arches: ['amd64'], run: () => ({ status: 0, stdout: '', stderr: '' }) })).toThrow(/wrote no rsrc_windows_amd64\.syso/);
      expect(sysoFiles(['amd64', 'arm64'])).toEqual([path.join('cmd', 'filex', 'rsrc_windows_amd64.syso'), path.join('cmd', 'filex', 'rsrc_windows_arm64.syso')]);
    } finally {
      fs.rmSync(backend, { recursive: true, force: true });
    }
  });

  it("reads the strings back off a built .exe, and says what is missing or wrong", () => {
    const strings = { ...VERSION_STRINGS, FileVersion: '0.57.0', ProductVersion: '0.57.0' };
    const exe = Buffer.concat([Buffer.from('MZ\x90\x00 code '), ...Object.entries(strings).map(([k, v]) => entry(k, v)), Buffer.alloc(16)]);
    expect(exeVersionStrings(exe)).toEqual(strings);
    expect(versionResourceProblems(exe, '0.57.0')).toEqual([]);
    expect(versionResourceProblems(exe, 'v0.57.0')).toEqual([]);
    expect(versionResourceProblems(exe, '0.57.1')).toEqual(['FileVersion is "0.57.0", not "0.57.1"', 'ProductVersion is "0.57.0", not "0.57.1"']);
    expect(versionResourceProblems(Buffer.from('MZ no resource'), '0.57.0')).toContain('CompanyName is missing, not "BRF Tech"');
  });

  it('is made from the one source by both builds, and never left for another build to link', () => {
    // The CLI's zip: goreleaser's before hook, a plain command (no sh -c: v2's parser refuses it).
    expect(read('.goreleaser.yml')).toMatch(/^before:\n {2}hooks:\n {4}- node scripts\/lib\/winres\.mjs --version \{\{ \.Version \}\}$/m);
    // The desktop app's filex.exe: fetch-cli writes it for that one architecture, removes it after, and reads it back.
    const fetch = read('desktop/scripts/fetch-cli.mjs');
    expect(fetch).toContain("import { versionResourceProblems, writeVersionResource } from '../../scripts/lib/winres.mjs';");
    expect(fetch).toMatch(/if \(goos === 'windows'\) resource = writeVersionResource\(\{ backend: BACKEND, version, arches: \[goarch\] \}\);/);
    expect(fetch).toMatch(/\} finally \{\n {2}for \(const f of resource\) fs\.rmSync\(f, \{ force: true \}\);\n\}/);
    expect(fetch).toMatch(/versionResourceProblems\(fs\.readFileSync\(dest\), version\)/);
    // Build output, never committed.
    expect(read('.gitignore')).toMatch(/^backend\/cmd\/filex\/rsrc_windows_\*\.syso$/m);
    // go-winres runs on this machine: the target's GOOS/GOARCH never reach it.
    const lib = read('scripts/lib/winres.mjs');
    expect(lib).toMatch(/delete env\.GOOS;\n\s+delete env\.GOARCH;/);
  });

  // The real thing, opt-in (a Go toolchain and the module proxy): go-winres
  // and `go build` of a Windows filex.exe, then the version read back off it.
  it.runIf(process.env.FILEX_WINRES_LIVE === '1')('a real Windows build carries the version (FILEX_WINRES_LIVE=1)', () => {
    const backend = path.join(REPO, 'backend');
    const out = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'winres-live-')), 'filex.exe');
    const files = writeVersionResource({ backend, version: '9.8.7-test', arches: ['amd64'] });
    try {
      const r = spawnSync('go', ['build', '-trimpath', '-o', out, './cmd/filex'], { cwd: backend, encoding: 'utf8', env: { ...process.env, GOOS: 'windows', GOARCH: 'amd64', CGO_ENABLED: '0' } });
      expect(r.status, r.stderr).toBe(0);
      expect(versionResourceProblems(fs.readFileSync(out), '9.8.7-test')).toEqual([]);
    } finally {
      for (const f of files) fs.rmSync(f, { force: true });
      fs.rmSync(path.dirname(out), { recursive: true, force: true });
    }
  });
});
