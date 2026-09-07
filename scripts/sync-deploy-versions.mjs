#!/usr/bin/env node
// Keeps every packaged deployment target on the version we actually released.
//
// ⚠⚠ This is not bookkeeping. Each of these files decides which image a real
// installation pulls:
//
//   - `deploy/helm/filex/values.yaml` ships `tag: ""`, which the `filex.image`
//     helper resolves to `.Chart.appVersion` — so the chart's appVersion IS
//     the image every Helm user runs.
//   - the CasaOS, Umbrel and Runtipi manifests pin an explicit image tag, and
//     their `version` field is what their store shows and compares against to
//     decide whether an update exists.
//
// Both halves have already gone wrong here, the same way and for the same
// reason — nothing failed when they drifted:
//
//   2026-08-29: the Helm appVersion sat at `v0.4.0` while the app shipped
//               v0.27.x. Twenty-three releases of fixes a chart user never got.
//   2026-09-06: the three app-store manifests were STILL at `v0.4.0`, twenty-
//               nine releases behind — because the fix for the first case
//               covered only the chart. Anyone installing filex from CasaOS,
//               Umbrel or Runtipi got a build from February.
//
// So this script covers every target, and `web/tests/deploy/deployVersions.test.ts`
// fails the build if any of them drifts again.
//
// It also keeps Umbrel's `releaseNotes` in step, DERIVED from CHANGELOG.md
// (scripts/release-notes.mjs) rather than typed by hand — the field had no
// version number in it, so a frozen one would have described February's
// release with nothing to make that visible.
//
//   node scripts/sync-deploy-versions.mjs          # rewrite them
//   node scripts/sync-deploy-versions.mjs --check  # exit 1 if any is behind
//
// The Helm chart's OWN `version` is bumped (patch) whenever appVersion moves:
// Helm treats a chart as immutable at a given version, so shipping different
// contents under the same number is what makes `helm upgrade` a coin toss.
// Runtipi's `tipi_version` integer is bumped for the same reason — that
// counter, not the version string, is what Runtipi compares to offer an update.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { NOTES_BLOCK, readNotesBlock, releaseNotesFromRepo, renderNotesBlock } from './release-notes.mjs';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
// The release version, from the package the release step bumps. The repo root
// is private and stays at 0.1.0, so it cannot be the source of truth.
const SOURCE = path.join(REPO, 'web', 'package.json');

const check = process.argv.includes('--check');
const version = JSON.parse(fs.readFileSync(SOURCE, 'utf8')).version;
const tag = `v${version}`;

const rel = (...p) => path.join(REPO, ...p);
const read = (p) => fs.readFileSync(p, 'utf8');

/** One thing that must name the release, and how to find and rewrite it. */
const targets = [];

/**
 * pin declares a field whose whole value is the version.
 *
 * `find` must anchor tightly enough that it cannot match a neighbour: the
 * Umbrel compose file also carries `version: "3.7"` (the compose schema), and
 * rewriting that would break the app rather than update it.
 */
function pin(file, label, find, replace, want) {
  targets.push({ file, label, find, replace, want });
}

// ── Helm ─────────────────────────────────────────────────────────────────────
pin('deploy/helm/filex/Chart.yaml', 'helm appVersion',
    /^appVersion:\s*"?([^"\n]+)"?\s*$/m, (v) => `appVersion: "${v}"`, tag);

// ── CasaOS ───────────────────────────────────────────────────────────────────
pin('deploy/casaos/docker-compose.yml', 'casaos image',
    /^(\s*image:\s*ghcr\.io\/brf-tech\/filex:)(\S+)\s*$/m,
    (v, m) => `${m[1]}${v}`, tag, 2);
// ⚠ Two spaces: this is the key under `x-casaos:`, not a top-level one.
pin('deploy/casaos/docker-compose.yml', 'casaos store version',
    /^(  version:\s*)"([^"\n]+)"\s*$/m, (v, m) => `${m[1]}"${v}"`, version, 2);

// ── Umbrel ───────────────────────────────────────────────────────────────────
pin('deploy/umbrel/filex/docker-compose.yml', 'umbrel image',
    /^(\s*image:\s*ghcr\.io\/brf-tech\/filex:)(\S+)\s*$/m,
    (v, m) => `${m[1]}${v}`, tag, 2);
pin('deploy/umbrel/filex/umbrel-app.yml', 'umbrel app version',
    /^(version:\s*)"([^"\n]+)"\s*$/m, (v, m) => `${m[1]}"${v}"`, version, 2);

// ── Runtipi ──────────────────────────────────────────────────────────────────
pin('deploy/runtipi/filex/docker-compose.json', 'runtipi image',
    /("image":\s*"ghcr\.io\/brf-tech\/filex:)([^"]+)"/, (v, m) => `${m[1]}${v}"`, tag, 2);
pin('deploy/runtipi/filex/config.json', 'runtipi app version',
    /("version":\s*)"([^"]+)"/, (v, m) => `${m[1]}"${v}"`, version, 2);

let behind = [];
let wrote = new Map();

for (const t of targets) {
  const p = rel(t.file);
  const src = wrote.get(p) ?? read(p);
  const m = src.match(t.find);
  if (!m) {
    console.error(`sync-deploy-versions: ${t.file} has no ${t.label} line to read`);
    process.exit(2);
  }
  // The captured value is the last group for the multi-group patterns and the
  // first for the single-group ones.
  const current = (m.length > 2 ? m[2] : m[1]).trim();
  if (current === t.want) continue;
  behind.push({ ...t, current });
  if (!check) {
    wrote.set(p, src.replace(t.find, t.replace(t.want, m)));
  }
}

// ── Umbrel release notes ─────────────────────────────────────────────────────
// Every other store surface tells the user what changed; Umbrel's card had no
// `releaseNotes` at all. It is DERIVED from CHANGELOG.md rather than typed,
// for the same reason the version pins are: a hand-written field nobody
// remembers to touch freezes, and a frozen "what's new" is worse than an
// absent one — it describes a release the user is not getting, with no version
// number in it to make the staleness visible.
//
// ⚠ No changelog section for the release version is a hard error, not a
// shrug. A release with no changelog entry is the actual mistake.
const umbrelPath = rel('deploy/umbrel/filex/umbrel-app.yml');
const notes = releaseNotesFromRepo(REPO, version);
if (!notes) {
  console.error(
    `sync-deploy-versions: CHANGELOG.md has no "## [${version}]" section, so the store ` +
      'release notes cannot be derived. Add this release\'s changelog entry first.',
  );
  process.exit(2);
}

let notesDrifted = false;
{
  const src = wrote.get(umbrelPath) ?? read(umbrelPath);
  const current = readNotesBlock(src);
  // ⚠ "No block I can read" and "no block at all" must not be confused: the
  // insert branch below would then append a SECOND releaseNotes key, which
  // YAML resolves by silently keeping one of them. Refuse instead.
  if (current === null && /^releaseNotes:/m.test(src)) {
    console.error(
      'sync-deploy-versions: umbrel-app.yml has a releaseNotes key this script cannot read.\n' +
        '  It must be a literal block (`releaseNotes: |-`) with two-space indented LF lines.',
    );
    process.exit(2);
  }
  if (current !== notes) {
    notesDrifted = true;
    if (!check) {
      const block = renderNotesBlock(notes);
      // ⚠ Function replacements: the notes carry changelog prose, and `$&` or
      // `$1` inside a replacement STRING would be substituted, not inserted.
      wrote.set(
        umbrelPath,
        current === null
          ? src.replace(/^(version:\s*"[^"\n]*"\n)/m, (_m, keep) => keep + block)
          : src.replace(NOTES_BLOCK, () => block),
      );
    }
  }
}

if (behind.length === 0 && !notesDrifted) {
  console.log(`every deployment target already names ${tag}, release notes are current`);
  process.exit(0);
}

if (check) {
  for (const b of behind) {
    console.error(`  ${b.label}: ${b.current}, the release is ${b.want}  (${b.file})`);
  }
  if (notesDrifted) {
    console.error(
      `  umbrel releaseNotes: not what CHANGELOG.md [${version}] says  (deploy/umbrel/filex/umbrel-app.yml)`,
    );
  }
  console.error(
    `\n${behind.length + (notesDrifted ? 1 : 0)} deployment target(s) are out of date — anyone ` +
      'installing from them would run the old image or read the wrong notes. ' +
      'Run: node scripts/sync-deploy-versions.mjs',
  );
  process.exit(1);
}

// ── Helm chart version: patch-bump, because the contents changed ─────────────
const chartPath = rel('deploy/helm/filex/Chart.yaml');
if (wrote.has(chartPath)) {
  const src = wrote.get(chartPath);
  const cv = src.match(/^version:\s*([0-9]+)\.([0-9]+)\.([0-9]+)\s*$/m);
  if (!cv) {
    console.error('sync-deploy-versions: Chart.yaml has no chart version line');
    process.exit(2);
  }
  const next = `${cv[1]}.${cv[2]}.${Number(cv[3]) + 1}`;
  wrote.set(chartPath, src.replace(/^version:\s*[0-9]+\.[0-9]+\.[0-9]+\s*$/m, `version: ${next}`));
  console.log(`  helm chart version ${cv[1]}.${cv[2]}.${cv[3]} -> ${next}`);
}

// ── Runtipi's update counter + timestamp ─────────────────────────────────────
// ⚠ `tipi_version` is the integer Runtipi compares to decide an update exists.
// Moving the version STRING alone changes what the store displays and offers
// nobody an upgrade.
const runtipiPath = rel('deploy/runtipi/filex/config.json');
if (wrote.has(runtipiPath)) {
  let src = wrote.get(runtipiPath);
  const tv = src.match(/("tipi_version":\s*)(\d+)/);
  if (!tv) {
    console.error('sync-deploy-versions: runtipi config.json has no tipi_version');
    process.exit(2);
  }
  src = src.replace(/("tipi_version":\s*)\d+/, `$1${Number(tv[2]) + 1}`);
  src = src.replace(/("updated_at":\s*)\d+/, `$1${Date.now()}`);
  wrote.set(runtipiPath, src);
  console.log(`  runtipi tipi_version ${tv[2]} -> ${Number(tv[2]) + 1}, updated_at refreshed`);
}

for (const [p, src] of wrote) fs.writeFileSync(p, src);
for (const b of behind) console.log(`  ${b.label}: ${b.current} -> ${b.want}`);
if (notesDrifted) console.log(`  umbrel releaseNotes: rewritten from CHANGELOG.md [${version}]`);
console.log(
  `${behind.length} deployment target(s) now name ${tag}` +
    (notesDrifted ? ', release notes refreshed' : ''),
);
