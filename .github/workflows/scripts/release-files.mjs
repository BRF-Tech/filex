#!/usr/bin/env node
// The desktop files a release candidate keeps for its tag run, and the check
// the tag run makes before it publishes them (release.yml, `desktop`; #174).
//
//   node release-files.mjs keep  <dir>                    (the dry run)
//   node release-files.mjs check <dir> --version 1.2.3    (the tag run)
//
// `keep` writes <dir>/release-files.sha256: the sha256 of every file the
// desktop job publishes from <dir> (the globs of its "Attach to the release"
// step, and the Store packages), one `<sha256>  <name>` line each, sorted.
// `check` reads it back after the artifact is downloaded and is red unless
// the files are exactly those, byte for byte - none missing, none added,
// none changed - and every update feed in <dir> (latest*.yml) offers
// --version. The tag run publishes nothing it did not check this way.
//
// Plain Node, no dependency: the desktop rows run on Windows, macOS and Linux.

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const MANIFEST = 'release-files.sha256';

/** What the desktop job publishes from desktop/release, by extension. */
export const PUBLISHED = [/\.exe$/, /\.blockmap$/, /\.AppImage$/, /\.deb$/, /\.rpm$/, /\.snap$/, /\.dmg$/, /\.zip$/, /\.appx$/, /\.msixbundle$/, /^latest.*\.yml$/];

/** The files of `dir` (not its subdirectories) the desktop job would publish, sorted. */
export function publishedFiles(dir) {
  return fs
    .readdirSync(dir, { withFileTypes: true })
    .filter((e) => e.isFile() && PUBLISHED.some((re) => re.test(e.name)))
    .map((e) => e.name)
    .sort();
}

function sha256(file) {
  return createHash('sha256').update(fs.readFileSync(file)).digest('hex');
}

/** Writes the manifest of `dir`; returns its lines. */
export function keep(dir) {
  const files = publishedFiles(dir);
  if (files.length === 0) throw new Error(`${dir}: nothing to keep - no file the desktop job publishes`);
  const lines = files.map((f) => `${sha256(path.join(dir, f))}  ${f}`);
  fs.writeFileSync(path.join(dir, MANIFEST), `${lines.join('\n')}\n`);
  return lines;
}

/** The problems with `dir` against its manifest and `version`; [] when it is exactly what was kept. */
export function check(dir, version) {
  const problems = [];
  const at = path.join(dir, MANIFEST);
  if (!fs.existsSync(at)) return [`${dir} has no ${MANIFEST}: these are not files a release candidate kept`];
  const want = new Map();
  for (const line of fs.readFileSync(at, 'utf8').split(/\r?\n/).filter(Boolean)) {
    const m = /^([0-9a-f]{64}) {2}(.+)$/.exec(line);
    if (!m) {
      problems.push(`${MANIFEST}: a line that is no "<sha256>  <name>": ${line}`);
      continue;
    }
    want.set(m[2], m[1]);
  }
  const have = publishedFiles(dir);
  for (const f of have) {
    if (!want.has(f)) problems.push(`${f} is here and not in ${MANIFEST}: it was not kept by the dry run`);
    else if (sha256(path.join(dir, f)) !== want.get(f)) problems.push(`${f} is not the file the dry run kept (sha256 differs)`);
  }
  for (const f of want.keys()) if (!have.includes(f)) problems.push(`${f} was kept by the dry run and is missing here`);
  if (!version) problems.push('no --version to check the update feeds against');
  for (const f of have.filter((x) => /^latest.*\.yml$/.test(x))) {
    const v = /^version:\s*['"]?([^'"\s]+)/m.exec(fs.readFileSync(path.join(dir, f), 'utf8'))?.[1];
    if (v !== version) problems.push(`${f} offers ${v ?? 'no version'}, not ${version}`);
  }
  return problems;
}

function main(argv) {
  const [cmd, dir, ...rest] = argv;
  if (!dir || !['keep', 'check'].includes(cmd)) {
    console.error('usage: node release-files.mjs keep <dir> | check <dir> --version <x.y.z>');
    return 2;
  }
  if (cmd === 'keep') {
    const lines = keep(dir);
    console.log(`${lines.length} file(s) kept in ${path.join(dir, MANIFEST)}:\n${lines.join('\n')}`);
    return 0;
  }
  const i = rest.indexOf('--version');
  const version = i >= 0 ? rest[i + 1] : '';
  const problems = check(dir, version);
  if (problems.length) {
    for (const p of problems) console.log(`::error::${p}`);
    return 1;
  }
  console.log(`${publishedFiles(dir).length} file(s): the ones the dry run kept, byte for byte; the feeds offer ${version}`);
  return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv.slice(2)));
}
