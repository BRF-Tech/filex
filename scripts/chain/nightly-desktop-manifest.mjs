#!/usr/bin/env node
// The night's desktop manifest (task #266): what .github/workflows/nightly-desktop.yml
// pushes beside the packages, and what the filex.sh/nightly page is made
// from (scripts/chain/nightly-lib.mjs desktopManifest, renderNightlyPage).
//
//   node scripts/chain/nightly-desktop-manifest.mjs --dir DIR --version V --revision SHA
//        --date YYYY-MM-DD --expect t1,t2,... [--run URL]
//
//   DIR        every row's output, as the workflow downloaded it: the packages
//              (filex-desktop-*) anywhere under it, each row's targets.txt
//              ("<target> built|failed"), and the arm64 snap's upload
//              (snap-arm64.json: { state, revision?, why? })
//   --expect   the targets the rows were asked for: one no targets.txt
//              names (a row that never got that far) is `failed`
//
// Writes DIR/nightly.json and DIR/SHA256SUMS (the packages only), moves the
// packages to DIR itself, and prints one line per row. Exit: 0 written, 1 no
// package at all, 2 usage.

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { classifyDesktopFile, desktopManifest } from './nightly-lib.mjs';

/** The rows' outcomes: every expected target, `failed` unless a targets.txt says otherwise. */
export function rowsOf(targetsTexts, expect) {
  const rows = Object.fromEntries(expect.map((t) => [t, 'failed']));
  for (const text of targetsTexts) {
    for (const line of String(text).split('\n')) {
      const [t, st] = line.trim().split(/\s+/);
      if (t && (st === 'built' || st === 'failed')) rows[t] = st;
    }
  }
  return rows;
}

function walk(dir, out = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else out.push(p);
  }
  return out;
}

function main(argv) {
  const o = {};
  for (let i = 0; i < argv.length; i += 2) {
    const k = argv[i];
    const v = argv[i + 1];
    if (!k?.startsWith('--') || v === undefined) {
      console.error(`usage: see the head of ${path.basename(fileURLToPath(import.meta.url))}`);
      return 2;
    }
    o[k.slice(2)] = v;
  }
  for (const k of ['dir', 'version', 'revision', 'date', 'expect']) {
    if (!o[k]) {
      console.error(`--${k} is required`);
      return 2;
    }
  }
  const dir = path.resolve(o.dir);
  const all = walk(dir);
  const targetsTexts = all.filter((p) => path.basename(p) === 'targets.txt').map((p) => fs.readFileSync(p, 'utf8'));
  const rows = rowsOf(targetsTexts, o.expect.split(',').filter(Boolean));
  const snapFile = all.find((p) => path.basename(p) === 'snap-arm64.json');
  let snapArm64 = null;
  try {
    snapArm64 = snapFile ? JSON.parse(fs.readFileSync(snapFile, 'utf8')) : null;
  } catch {
    snapArm64 = { state: 'failed', why: 'snap-arm64.json could not be read' };
  }
  const packages = all.filter((p) => classifyDesktopFile(path.basename(p)));
  const files = [];
  for (const p of packages) {
    const name = path.basename(p);
    const dest = path.join(dir, name);
    if (p !== dest) fs.renameSync(p, dest);
    const buf = fs.readFileSync(dest);
    files.push({ name, size: buf.length, sha256: createHash('sha256').update(buf).digest('hex') });
  }
  const manifest = desktopManifest({
    files,
    version: o.version,
    revision: o.revision,
    date: o.date,
    run: o.run ?? '',
    rows,
    snap: snapArm64 ? { arm64: snapArm64 } : {},
    builtAt: new Date().toISOString().replace(/\.\d{3}Z$/, 'Z'),
  });
  fs.writeFileSync(path.join(dir, 'nightly.json'), `${JSON.stringify(manifest, null, 2)}\n`);
  fs.writeFileSync(path.join(dir, 'SHA256SUMS'), manifest.files.map((f) => `${f.sha256}  ${f.name}\n`).join(''));
  for (const [t, st] of Object.entries(rows)) console.log(`${t}: ${st}`);
  console.log(`${manifest.files.length} package(s)${snapArm64 ? `; arm64 snap: ${snapArm64.state}` : ''}`);
  return manifest.files.length ? 0 : 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv.slice(2)));
}
