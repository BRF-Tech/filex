#!/usr/bin/env node
// The parts of the GitHub test matrix (.github/workflows/ci.yml, tasks #173
// and #174): what each part is, and what GitHub calls it.
//
// ⚠ One place, read by three. ci.yml's `plan` job prints its matrices from
// here (`github-output`), every row carries the job's name, and every matrix
// job is named `${{ matrix.name }}`: so the release tool (scripts/release/
// plan.mjs, the gate stage) reads the run part by part under the very names
// this file gives, and web/tests/deploy/ciFullMatrix.test.ts holds the
// workflow's fixed job names to FIXED below. A part renamed in one place and
// not the other is a part the release gate waits for and never sees.
//
// The Go parts are the shards of scripts/test-shards.json (task #171): the
// `go` profile for plain `go test`, the `race` profile for `-race`. The
// browser parts are Playwright's own split (`node e2e/run.mjs local --shard
// i/N`, e2e/lib/shard.mjs), per engine.
//
//   node scripts/ci-parts.mjs github-output [--profile full|pr]
//       key=value lines for $GITHUB_OUTPUT: profile, go, race, frontend,
//       playwright, ds, extra (each a {"include":[…]} matrix).
//   node scripts/ci-parts.mjs list [--profile full|pr]
//       every job a run of that profile has, by name, one per line.
//
// Profiles:
//   full  a push of main, a run by hand: every part. The release reads this
//         run on the commit it lands (docs/CONTRIBUTING.md, Release process,
//         step 7), and a tag run's `verify` asks for its last part,
//         completeName('full'), before anything is published.
//   pr    a pull request: no -race, Chromium only, no Document Server and no
//         store or S3 lines. The full matrix runs again on the push that lands
//         it; the 20 jobs an organisation runs at once are shared with every
//         BRF-Tech repository, and a pull request should not hold a release.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
export const REPO = path.resolve(HERE, '..');

export const PROFILES = ['full', 'pr'];

/** The engines a full run splits Playwright for; a pull request runs the first. */
export const E2E_ENGINES = ['chromium', 'firefox', 'webkit'];

/**
 * The parts each engine's suite is cut into. ~520 tests an engine: a part is
 * about 130, 6-9 minutes on a GitHub runner, plus the setup.
 */
export const E2E_SHARDS = 4;

/**
 * The specs whose path differs with an ONLYOFFICE Document Server connected:
 * the build host runs them without one (scripts/chain/lists/e2e.txt, `nods`)
 * and every engine's whole suite with one. Here the engines' parts run
 * without one, and these again with one, on every engine; 116 reads the
 * server's name off the admin page and gates nothing without it.
 */
export const DS_SPECS = [
  '195-office-thumbnails',
  '196-app-plugin-convert-office',
  '96-app-plugin-convert',
  '90-deployment-smoke',
  '91-rounds-4-6-regression',
  '180-thumbnails',
  '100-viewer-audit',
  '199-csv-onlyoffice',
  '200-office-saved-beside',
  '116-admin-says-which',
];

/** The Playwright --grep of the Document Server parts. */
export const DS_GREP = `(${DS_SPECS.join('|')})\\.spec`;

/**
 * The browser lines the build host runs on their own (scripts/chain/lists/
 * e2e.txt): the store install with filex started without FILEX_PUBLIC_URL,
 * and the S3 specs against a local gateway. On every engine.
 */
export const EXTRA = [
  { kind: 'nopub', grep: '202-store-install', flag: '--no-public-url' },
  { kind: 's3', grep: '(26-s3|70-multi-storage)\\.spec', flag: '--s3' },
];

/** The unit suite runs once on CI's clock and once on the maintainer's. */
export const TIMEZONES = ['UTC', 'Europe/Istanbul'];

/** The jobs that are no matrix, by their key in ci.yml. */
export const FIXED = {
  plan: 'Plan the matrix',
  vet: 'Go vet + build',
  shards: 'Shard lists',
  engines: 'Databases (PostgreSQL + MySQL)',
  typecheck: 'Typechecks (core, desktop, e2e)',
  desktop: 'Desktop unit tests',
  docs: 'Docs (links, site, anchors, YAML)',
  build: 'Build for the browser suites',
  browser: 'Browser E2E (Cypress)',
  docker: 'Docker image builds',
};

export const goName = (shard) => `Go (${shard})`;
export const raceName = (shard) => `Go -race (${shard})`;
export const frontendName = (tz) => `Frontend (pnpm, TZ=${tz})`;
export const playwrightName = (engine, i, n) => `Playwright (${engine} ${i}/${n})`;
export const dsName = (engine) => `Playwright + Document Server (${engine})`;
export const extraName = (kind) => `Playwright (${kind}, ${E2E_ENGINES.join(' + ')})`;
/** The last job of a run: green only when every part of its profile was. */
export const completeName = (profile) => `All tests (${profile})`;

function profileOf(profile) {
  const p = profile ?? 'full';
  if (!PROFILES.includes(p)) throw new Error(`profile "${p}": ${PROFILES.join(' or ')}`);
  return p;
}

/** The shard names of a scripts/test-shards.json profile (go | race), in its order. */
export function shardNames(repo, profile) {
  const cfg = JSON.parse(fs.readFileSync(path.join(repo, 'scripts', 'test-shards.json'), 'utf8'));
  const shards = cfg.profiles?.[profile];
  if (!Array.isArray(shards) || shards.length === 0) throw new Error(`scripts/test-shards.json has no "${profile}" profile`);
  return shards.map((s) => s.name);
}

/**
 * The matrices of a profile, every row named as GitHub will name its job.
 * Never an empty include: GitHub fails a matrix with no row, so a part a
 * profile leaves out is switched off by its job's `if:`, not by an empty list.
 */
export function ciMatrix(repo = REPO, profile = 'full') {
  const p = profileOf(profile);
  const engines = p === 'full' ? E2E_ENGINES : E2E_ENGINES.slice(0, 1);
  const include = (rows) => ({ include: rows });
  const playwright = [];
  for (const engine of engines) {
    for (let i = 1; i <= E2E_SHARDS; i++) playwright.push({ name: playwrightName(engine, i, E2E_SHARDS), engine, shard: `${i}/${E2E_SHARDS}` });
  }
  return {
    profile: p,
    go: include(shardNames(repo, 'go').map((shard) => ({ name: goName(shard), shard }))),
    race: include(shardNames(repo, 'race').map((shard) => ({ name: raceName(shard), shard }))),
    frontend: include(TIMEZONES.map((tz) => ({ name: frontendName(tz), tz }))),
    playwright: include(playwright),
    ds: include(E2E_ENGINES.map((engine) => ({ name: dsName(engine), engine }))),
    extra: include(EXTRA.map((x) => ({ name: extraName(x.kind), kind: x.kind, grep: x.grep, flag: x.flag }))),
  };
}

/** Whether a matrix key runs in a profile (the rest are switched off by `if:`). */
export function runsIn(key, profile) {
  return profileOf(profile) === 'full' || !['race', 'ds', 'extra'].includes(key);
}

/**
 * Every job of a run of `profile`, by the name GitHub gives it: the fixed
 * jobs, every matrix row, and the job that says the whole run passed.
 */
export function ciParts(repo = REPO, profile = 'full') {
  const p = profileOf(profile);
  const m = ciMatrix(repo, p);
  const rows = [];
  for (const key of ['go', 'race', 'frontend', 'playwright', 'ds', 'extra']) {
    if (runsIn(key, p)) rows.push(...m[key].include.map((r) => r.name));
  }
  return [...Object.values(FIXED), ...rows, completeName(p)];
}

/**
 * The parts of the full matrix that stand for one of the release's heavy
 * gates (scripts/release/plan.mjs, `githubJobs`): the gate stage reads these
 * jobs one by one instead of running the gate on this machine.
 */
export function gateParts(repo = REPO) {
  const m = ciMatrix(repo, 'full');
  const names = (key) => m[key].include.map((r) => r.name);
  return {
    go: [FIXED.vet, ...names('go')],
    race: names('race'),
    migrations: [FIXED.engines],
    webLocalClock: [frontendName('Europe/Istanbul')],
    cypress: [FIXED.browser],
    playwright: [...names('playwright'), ...names('ds'), ...names('extra')],
  };
}

/**
 * The `key=value` lines ci.yml's plan job writes to $GITHUB_OUTPUT: the
 * profile, every matrix, and the three values the browser jobs read (the
 * Document Server grep, the number of parts, the engines of a full run).
 */
export function githubOutput(repo = REPO, profile = 'full') {
  const m = ciMatrix(repo, profile);
  const lines = [`profile=${m.profile}`];
  for (const key of ['go', 'race', 'frontend', 'playwright', 'ds', 'extra']) lines.push(`${key}=${JSON.stringify(m[key])}`);
  lines.push(`ds_grep=${DS_GREP}`, `e2e_shards=${E2E_SHARDS}`, `e2e_engines=${E2E_ENGINES.join(',')}`);
  return lines;
}

// ── the command line ────────────────────────────────────────────────────────

function main(argv) {
  const [cmd, ...rest] = argv;
  let profile = 'full';
  for (let i = 0; i < rest.length; i++) {
    if (rest[i] === '--profile' && rest[i + 1]) profile = rest[++i];
    else if (rest[i].startsWith('--profile=')) profile = rest[i].slice(10);
    else throw new Error(`unknown argument ${rest[i]}`);
  }
  if (cmd === 'github-output') {
    process.stdout.write(`${githubOutput(REPO, profile).join('\n')}\n`);
    return 0;
  }
  if (cmd === 'list') {
    process.stdout.write(`${ciParts(REPO, profile).join('\n')}\n`);
    return 0;
  }
  throw new Error('usage: node scripts/ci-parts.mjs <github-output|list> [--profile full|pr]');
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    process.exit(main(process.argv.slice(2)));
  } catch (e) {
    console.error(`ci-parts: ${e.message}`);
    process.exit(2);
  }
}
