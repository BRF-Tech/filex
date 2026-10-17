#!/usr/bin/env node
// The Windows version resource (VERSIONINFO) every filex.exe carries: the
// CLI's zip on the Release (.goreleaser.yml, a `before` hook) and the
// desktop app's resources\bin\filex.exe (desktop/scripts/fetch-cli.mjs) -
// one source for both (task #259).
//
//   node scripts/lib/winres.mjs --version X.Y.Z [--backend DIR] [--arch amd64,arm64]
//
// writes backend/cmd/filex/rsrc_windows_<arch>.syso, which `go build` links
// into a Windows build of ./cmd/filex by itself (and ignores for every other
// OS). Made by go-winres (GO_WINRES, pinned), run with `go run` - nothing is
// added to the module. The .syso files are build output, never committed
// (.gitignore).
//
// ⚠ Why (task #259, a security product quarantined the desktop app's sync):
// a filex.exe with no version resource is an anonymous program to Windows
// and to the products that judge one - no company, no product, no version
// in its Properties, nothing that ties it to the desktop app that runs it.
// Every one now says CompanyName "BRF Tech", ProductName "filex", what it is,
// its original file name and its version. No signature: GitHub's packages
// stay unsigned (the signed copy is the Microsoft Store's).

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

/** go-winres, pinned: run with `go run`, so the module gains no dependency. */
export const GO_WINRES = 'github.com/tc-hib/go-winres@v0.3.3';

/** The strings every filex.exe carries beside its version. */
export const VERSION_STRINGS = {
  CompanyName: 'BRF Tech',
  ProductName: 'filex',
  FileDescription: 'filex - the file manager server and command line',
  InternalName: 'filex',
  OriginalFilename: 'filex.exe',
};

/**
 * The numeric version Windows keeps beside the strings: four numbers. A
 * pre-release or build suffix has no place there (it stays in the strings):
 * 0.57.0-nightly.20261018+abc -> 0.57.0.0.
 */
export function fixedVersion(version) {
  const m = /^v?(\d+)\.(\d+)\.(\d+)/.exec(String(version ?? ''));
  if (!m) throw new Error(`"${version}" is no version to put in a Windows resource (X.Y.Z...)`);
  return `${Number(m[1])}.${Number(m[2])}.${Number(m[3])}.0`;
}

/** The go-winres input (winres.json) for a version: VERSIONINFO alone - no icon, no manifest. */
export function versionInfo(version) {
  const v = String(version).replace(/^v/, '');
  const fixed = fixedVersion(v);
  return {
    RT_VERSION: {
      '#1': {
        '0000': {
          fixed: { file_version: fixed, product_version: fixed },
          info: { '0409': { ...VERSION_STRINGS, FileVersion: v, ProductVersion: v } },
        },
      },
    },
  };
}

/** The .syso files go-winres writes for `arches`, relative to the module. */
export const sysoFiles = (arches) => arches.map((a) => path.join('cmd', 'filex', `rsrc_windows_${a}.syso`));

/**
 * Writes the version resource for `arches` into <backend>/cmd/filex. `run`
 * starts a command ([bin, args, { cwd }] -> { status, stdout, stderr }): `go
 * run` of GO_WINRES by default. Returns the files written; throws when
 * go-winres fails or writes none.
 */
export function writeVersionResource({ backend, version, arches = ['amd64', 'arm64'], run = defaultRun }) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-winres-'));
  try {
    const json = path.join(dir, 'winres.json');
    fs.writeFileSync(json, `${JSON.stringify(versionInfo(version), null, 2)}\n`);
    const out = path.join('cmd', 'filex', 'rsrc');
    const r = run('go', ['run', GO_WINRES, 'make', '--in', json, '--out', out, '--arch', arches.join(',')], { cwd: backend });
    if (r.status !== 0) throw new Error(`go-winres failed (exit ${r.status}): ${String(r.stderr || r.stdout).trim().split('\n').slice(-5).join(' | ')}`);
    const files = sysoFiles(arches).map((f) => path.join(backend, f));
    const missing = files.filter((f) => !fs.existsSync(f));
    if (missing.length) throw new Error(`go-winres wrote no ${missing.map((f) => path.basename(f)).join(', ')}`);
    return files;
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function defaultRun(bin, args, { cwd }) {
  // go-winres runs HERE: a GOOS/GOARCH meant for the binary being built
  // (`GOARCH=arm64 node scripts/fetch-cli.mjs`) would build it for the wrong CPU.
  const env = { ...process.env, CGO_ENABLED: '0' };
  delete env.GOOS;
  delete env.GOARCH;
  const r = spawnSync(bin, args, { cwd, encoding: 'utf8', windowsHide: true, env });
  return { status: typeof r.status === 'number' ? r.status : 127, stdout: r.stdout ?? '', stderr: r.stderr ?? (r.error ? String(r.error.message) : '') };
}

/**
 * The VERSIONINFO strings of a built .exe, read off its bytes: each key of
 * VERSION_STRINGS plus FileVersion and ProductVersion, as the String entries
 * of StringFileInfo hold them (UTF-16LE: the key, its NUL, padding, the
 * value up to its NUL). What a build checks its own output with.
 */
export function exeVersionStrings(buf) {
  const out = {};
  for (const key of [...Object.keys(VERSION_STRINGS), 'FileVersion', 'ProductVersion']) {
    const needle = Buffer.from(`${key}\0`, 'utf16le');
    const at = buf.indexOf(needle);
    if (at < 0) continue;
    let p = at + needle.length;
    while (p + 1 < buf.length && buf.readUInt16LE(p) === 0) p += 2;
    let end = p;
    while (end + 1 < buf.length && buf.readUInt16LE(end) !== 0) end += 2;
    out[key] = buf.subarray(p, end).toString('utf16le');
  }
  return out;
}

/** What is wrong with a built filex.exe's version resource for `version`: [] when nothing. */
export function versionResourceProblems(buf, version) {
  const got = exeVersionStrings(buf);
  const want = { ...VERSION_STRINGS, FileVersion: String(version).replace(/^v/, ''), ProductVersion: String(version).replace(/^v/, '') };
  return Object.entries(want)
    .filter(([k, v]) => got[k] !== v)
    .map(([k, v]) => `${k} is ${got[k] === undefined ? 'missing' : `"${got[k]}"`}, not "${v}"`);
}

function main(argv) {
  const at = (flag) => {
    const i = argv.indexOf(flag);
    return i > -1 ? argv[i + 1] : undefined;
  };
  const version = at('--version');
  if (!version) {
    console.error('usage: node scripts/lib/winres.mjs --version X.Y.Z [--backend DIR] [--arch amd64,arm64]');
    return 2;
  }
  const backend = path.resolve(at('--backend') ?? path.join(path.dirname(fileURLToPath(import.meta.url)), '..', '..', 'backend'));
  const arches = (at('--arch') ?? 'amd64,arm64').split(',').filter(Boolean);
  try {
    const files = writeVersionResource({ backend, version, arches });
    console.log(`the Windows version resource of filex ${version}: ${files.map((f) => path.relative(backend, f)).join(', ')}`);
    return 0;
  } catch (e) {
    console.error(`winres: ${e.message}`);
    return 1;
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv.slice(2)));
}
