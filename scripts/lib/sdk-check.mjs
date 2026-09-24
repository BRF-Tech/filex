#!/usr/bin/env node
// The generated guest SDK copy must match the pluginkit it was generated from.
//
// ⚠⚠ WHY THIS EXISTS. filex-sign and filex-convert build against a GENERATED
// copy of `backend/pkg/pluginkit` (scripts/pluginkit-devmodule.sh writes it,
// each app's go.mod points at it with `replace … => ../filex-sdk-dev`).
// Nothing regenerated that copy as part of a build, so it went stale in
// silence: on 2026-09-24 the two apps had been built against a copy made
// before pluginkit changed three times, and they shipped modules compiled
// against a host contract that is NOT the one the server ships. Nothing was
// red. The tests passed, the manifests stamped cleanly, and the only visible
// symptom was that two agents measured two different sha256 for the same
// commit of the same repository — which reads like a flaky toolchain and cost
// an hour before it read like what it was.
//
// A stale host contract is not a warning. An app built against the wrong
// `pluginkit` can call a host function the server does not export, and the
// failure lands on a person using the app, months later, as "the plugin does
// nothing". So this refuses, with the one command that fixes it.
//
// ⚠ It is for the LOCAL DEV COPY ONLY. At release the apps drop the `replace`
// and point at the published module; there is no generated copy to be stale,
// nothing to check, and this exits 0 without printing a word. The `replace`
// is the whole trigger.
//
//   node sdk-check.mjs --write <sdk-dir> <pluginkit-dir>   (the generator)
//   node sdk-check.mjs [--sdk <dir>]                       (an app, at build)
//
// Exit codes: 0 fine (or nothing to check), 1 stale / unverifiable.

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

export const STAMP_FILE = 'sdk-stamp.json';

/** Every `.go` file under `dir` that the generator carries, sorted. */
function goFiles(dir, tests = false) {
  const out = [];
  const walk = (d, rel) => {
    let entries;
    try {
      entries = fs.readdirSync(d, { withFileTypes: true });
    } catch {
      return;
    }
    for (const e of entries.sort((a, b) => (a.name < b.name ? -1 : 1))) {
      const full = path.join(d, e.name);
      const r = rel ? `${rel}/${e.name}` : e.name;
      if (e.isDirectory()) walk(full, r);
      else if (e.name.endsWith('.go') && (tests || !e.name.endsWith('_test.go'))) out.push([r, full]);
    }
  };
  walk(dir, '');
  return out.sort((a, b) => (a[0] < b[0] ? -1 : 1));
}

/**
 * A hash of a Go source tree: each file's path and content, in path order.
 *
 * ⚠ Content is hashed as BYTES. Hashing text would make a line-ending change
 * invisible, and the copy is written on the same machines that keep tripping
 * over CRLF.
 */
export function treeHash(dir, { tests = false } = {}) {
  const h = createHash('sha256');
  const files = goFiles(dir, tests);
  for (const [rel, full] of files) {
    h.update(rel, 'utf8');
    h.update('\0');
    h.update(createHash('sha256').update(fs.readFileSync(full)).digest('hex'));
    h.update('\n');
  }
  return { hash: h.digest('hex'), count: files.length };
}

/** `to` relative to `from`, with forward slashes; null across drives. */
function relativeOrNull(from, to) {
  const rel = path.relative(path.resolve(from), path.resolve(to));
  if (!rel || path.isAbsolute(rel)) return null;
  return rel.split(path.sep).join('/');
}

/**
 * The same absolute path as the OTHER side of this machine spells it:
 * `/mnt/g/x` ↔ `G:/x`. Returns null when there is no other spelling.
 * `platform` is a parameter so the tests can ask as Windows from Linux and
 * the other way round.
 */
export function crossSpelling(p, platform = process.platform) {
  if (typeof p !== 'string' || !p) return null;
  if (platform === 'win32') {
    const m = /^\/mnt\/([a-zA-Z])(\/.*)?$/.exec(p);
    return m ? `${m[1].toUpperCase()}:${m[2] ?? '/'}` : null;
  }
  const m = /^([a-zA-Z]):[\\/](.*)$/.exec(p);
  return m ? `/mnt/${m[1].toLowerCase()}/${m[2].replace(/\\/g, '/')}` : null;
}

/**
 * Where the pluginkit this copy came from lives, seen from HERE: the path
 * relative to the copy first (right on both sides), then the recorded
 * absolute path, then that path as the other side of the machine spells it.
 */
export function sourceCandidates(sdkDir, stamp, platform = process.platform) {
  const out = [];
  if (stamp.source_rel) out.push(path.resolve(sdkDir, stamp.source_rel));
  if (stamp.source) {
    out.push(stamp.source);
    const other = crossSpelling(stamp.source, platform);
    if (other) out.push(other);
  }
  return out;
}

/** What the generator records beside the copy it just wrote. */
export function writeStamp(sdkDir, pluginkitDir) {
  const src = treeHash(pluginkitDir);
  const gen = treeHash(path.join(sdkDir, 'pkg', 'pluginkit'));
  const stamp = {
    // ⚠⚠ Written TWO ways, and read by `sourceCandidates` below. The same
    // tree is `/mnt/g/filex-wt-apps/…` under WSL and `G:\filex-wt-apps\…`
    // from Windows, and the first version of this guard stored only the
    // absolute path of whichever side generated the copy — so a build started
    // from the OTHER side found no such directory and refused a perfectly
    // current copy (v0.43.0 release run: generated under WSL, checked from
    // Git Bash, `no-source`, exit 1). A guard that refuses a good build is a
    // guard somebody switches off. `source_rel` is relative to the SDK copy
    // and means the same thing on both sides; `source` is kept for a copy on a
    // different drive from its source, where no relative path exists.
    source: path.resolve(pluginkitDir),
    source_rel: relativeOrNull(sdkDir, pluginkitDir),
    source_sha256: src.hash,
    source_files: src.count,
    generated_sha256: gen.hash,
    generated_at: new Date().toISOString(),
    how: 'bash scripts/pluginkit-devmodule.sh <dir>   (in the filex repository)',
  };
  fs.writeFileSync(path.join(sdkDir, STAMP_FILE), `${JSON.stringify(stamp, null, 2)}\n`);
  return stamp;
}

/**
 * Where this app's go.mod sends `github.com/brf-tech/filex/backend`, when it
 * sends it at a directory. `null` means the published module — nothing local
 * to check.
 */
export function replaceTarget(goModPath) {
  let src;
  try {
    src = fs.readFileSync(goModPath, 'utf8');
  } catch {
    return null;
  }
  // `replace a => ../b`, whether on its own line or inside a replace block.
  const re = /replace\s+([^\s]+)\s*=>\s*([^\s]+)|^\s*([^\s]+)\s*=>\s*([^\s]+)\s*$/gm;
  for (const m of src.matchAll(re)) {
    const target = m[2] ?? m[4];
    if (!target) continue;
    // A version, not a path: `=> example.com/x v1.2.3` is the published kind.
    if (!target.startsWith('.') && !target.startsWith('/')) continue;
    return path.resolve(path.dirname(goModPath), target);
  }
  return null;
}

/** The whole check, as data. `ok: true` means carry on. */
export function checkSdk({ cwd = process.cwd(), sdkDir = undefined } = {}) {
  const dir = sdkDir ?? replaceTarget(path.join(cwd, 'go.mod'));
  if (!dir) return { ok: true, silent: true, reason: 'published' };

  const stampPath = path.join(dir, STAMP_FILE);
  if (!fs.existsSync(stampPath)) {
    return {
      ok: false,
      reason: 'no-stamp',
      message: `the SDK copy at ${dir} carries no ${STAMP_FILE}, so there is no telling which pluginkit it came from`,
    };
  }
  let stamp;
  try {
    stamp = JSON.parse(fs.readFileSync(stampPath, 'utf8'));
  } catch (err) {
    return { ok: false, reason: 'bad-stamp', message: `${stampPath} is not readable JSON: ${err.message}` };
  }
  const tried = sourceCandidates(dir, stamp);
  const source = tried.find((c) => fs.existsSync(c));
  if (!source) {
    return {
      ok: false,
      reason: 'no-source',
      message:
        `the SDK copy says it came from ${stamp.source}, which is not there — nothing can confirm it is current
` +
        `  looked in: ${tried.join(', ') || '(the stamp names no source)'}`,
    };
  }
  const now = treeHash(source);
  if (now.hash !== stamp.source_sha256) {
    return {
      ok: false,
      reason: 'stale',
      message:
        `the SDK copy at ${dir} is STALE.\n` +
        `  it was generated from pluginkit ${String(stamp.source_sha256).slice(0, 12)}…\n` +
        `  ${source} is now      ${now.hash.slice(0, 12)}…`,
    };
  }
  const gen = treeHash(path.join(dir, 'pkg', 'pluginkit'));
  if (stamp.generated_sha256 && gen.hash !== stamp.generated_sha256) {
    return {
      ok: false,
      reason: 'edited',
      message: `the SDK copy at ${dir} has been EDITED since it was generated — it is a generated tree, never edit it`,
    };
  }
  return { ok: true, reason: 'current', stamp };
}

/** What a refusal prints. Always names the command that fixes it. */
export function refusalText(result, dir) {
  return [
    '',
    '  ✖ the guest SDK copy this build links is not the one the host ships',
    '',
    `  ${result.message}`,
    '',
    '  An app compiled against a stale pluginkit calls a host contract the',
    '  server does not have. Nothing goes red: it builds, it stamps, and it',
    '  misbehaves in front of somebody months later.',
    '',
    '  Fix it with, in the filex repository:',
    `      bash scripts/pluginkit-devmodule.sh ${dir ?? '<sdk dir>'}`,
    '',
  ].join('\n');
}

function main(argv) {
  if (argv[0] === '--write') {
    const [, sdkDir, pluginkitDir] = argv;
    if (!sdkDir || !pluginkitDir) {
      console.error('usage: node sdk-check.mjs --write <sdk-dir> <pluginkit-dir>');
      return 1;
    }
    const stamp = writeStamp(sdkDir, pluginkitDir);
    console.log(`${STAMP_FILE}: pluginkit ${stamp.source_sha256.slice(0, 12)}… (${stamp.source_files} files)`);
    return 0;
  }
  const i = argv.indexOf('--sdk');
  const sdkDir = i >= 0 ? argv[i + 1] : undefined;
  const res = checkSdk({ sdkDir });
  if (res.ok) {
    if (!res.silent) console.log(`[sdk] the guest SDK copy is current (pluginkit ${res.stamp.source_sha256.slice(0, 12)}…)`);
    return 0;
  }
  console.error(refusalText(res, sdkDir ?? replaceTarget(path.join(process.cwd(), 'go.mod'))));
  return 1;
}

// Run only as a script, never on import (the tests import the functions).
// ⚠ Through pathToFileURL, not a hand-built `file://` string: on Windows a
// path is `G:\\x\\y.mjs` while the URL is `file:///G:/x/y.mjs`, so the naive
// comparison never matched, main() never ran and the CLI exited 0 having
// checked nothing — a guard that silently passes, which is the one thing
// this file exists to stop. Caught by its own test, 2026-09-24.
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.exit(main(process.argv.slice(2)));
}
