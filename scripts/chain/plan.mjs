// What a chain run does: the jobs of a profile, with the memory each one is
// budgeted for and the time it is expected to take.
//
// The Go and -race jobs are the shards of scripts/test-shards.json (task
// #171), the one list the chain, GitHub and CircleCI all take their Go parts
// from: `node scripts/test-shards.mjs go --profile go|race --format chain`
// prints them in the line format below. The browser round is
// scripts/chain/lists/e2e.txt. CHAIN_GO_LIST, CHAIN_RACE_LIST and
// CHAIN_E2E_LIST replace either one (a file, or cmd:<command>).
//
// Pure apart from reading the lists: scripts/chain/run.mjs runs the plan,
// web/tests/deploy/chainSchedule.test.ts holds its shape.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { NIGHTLY_EXTRAS, nightlyExtras } from './nightly-lib.mjs';

export const CHAIN_DIR = path.dirname(fileURLToPath(import.meta.url));

export const PROFILES = ['full', 'targeted', 'nightly'];

// What the nightly profile adds to the full one (ds-go, s3-live, shots,
// realenv) is defined next to the nightly run's other decisions, which also
// need it.
export { NIGHTLY_EXTRAS, nightlyExtras };

/** Where the Go and -race jobs come from unless CHAIN_GO_LIST / CHAIN_RACE_LIST say otherwise. */
export const SHARD_LISTS = {
  go: 'cmd:node scripts/test-shards.mjs go --profile go --format chain',
  race: 'cmd:node scripts/test-shards.mjs go --profile race --format chain',
};

/**
 * GiB each job is budgeted for: its peak resident memory on the build host
 * (cAdvisor, 15 s samples, the 0.51 and 0.52 rounds), rounded up. Page cache
 * is not counted: the kernel gives it back under pressure, and run.mjs also
 * refuses to start a job while the host's MemAvailable is short (CHAIN_MEM_RESERVE_GB).
 *
 * measured peak RSS (working set):
 *   build 4.24 (4.43) · web 0.68 (2.04) · go full 0.56 (1.26) · race, every
 *   package but two 1.12 (1.83) · race shard 0.36-0.76 (0.37-1.07, the larger
 *   one building the binary) · Cypress 1.07 (1.58) · Playwright, one engine
 *   3.73-3.80 (4.0-4.79) · Document Server 0.46 (0.70) · MySQL 0.67 (0.86)
 *   + PostgreSQL 0.01 (0.15)
 * A Go job is a part of what "go full" ran, so it is budgeted below it; the
 * shard check compiles two test binaries without running them.
 *
 * The nightly extras were not measured on the host yet (task #175): ds-go and
 * s3-live are Go test binaries, shots one Playwright engine with its filex
 * instances, realenv a Playwright runner next to Keycloak, OpenLDAP, Pebble
 * and its own Document Server. Re-read them from the first nights' cAdvisor
 * peaks.
 */
export const WEIGHTS = {
  build: 4.5,
  web: 1,
  docs: 0.5,
  e2etsc: 0.5,
  'shards-check': 1,
  go: 0.75,
  migrate: 0.25,
  'race-build': 1,
  race: 1.5,
  'race-shard': 0.75,
  cypress: 1.5,
  e2e: 4,
  ds: 0.5,
  db: 0.75,
  'ds-go': 1,
  's3-live': 1,
  shots: 4,
  realenv: 4,
};

/**
 * Minutes a job is expected to take, for `run.mjs --plan` and the scheduling
 * test, where scripts/test-shards.json has nothing to say: measured on the
 * build host (chain.log of 0.51 and 0.52). The Go and -race shards' minutes
 * come from that file's weights (shardMinutes below), and a Playwright line
 * cut with --shard i/N takes 1/N of its engine's.
 */
export const EXPECTED_MINUTES = {
  build: 3.5,
  web: 2,
  docs: 0.5,
  e2etsc: 3,
  'shards-check': 3,
  go: 13.5,
  migrate: 1,
  'race-build': 3,
  race: 32,
  'race-shard': 30,
  cypress: 5,
  local: 25,
  nopub: 3,
  s3: 1,
  'e2e-chromium': 24,
  'e2e-firefox': 34,
  'e2e-webkit': 31,
  'e2e-nods': 3,
  // The nightly extras: shots as `pnpm shots` took on the host (8.6-9.9 min,
  // 0.50-0.52), the others estimated until the first nights say.
  'ds-go': 6,
  's3-live': 6,
  shots: 10,
  realenv: 25,
};

/**
 * How scripts/test-shards.json's weights (seconds under -race, summed over a
 * unit's packages) become a job's minutes:
 *   - a unit cut by test file runs as one process: its seconds are its time;
 *   - a package unit runs `go test -p` packages at once (race.sh: 3,
 *     CHAIN_GO_P: 6), so its wall time is about its sum over that many
 *     (0.52: 90 minutes of packages took 32 under -p 3);
 *   - plain `go test` is about RACE_SLOWDOWN times faster than -race
 *     (0.52: handlers 785 s plain against ~150 min under -race, wasmplugin
 *     424 s against ~70 min), and every Go job first compiles for about
 *     GO_COMPILE_MIN minutes.
 */
export const SHARD_TIMING = { RACE_P: 3, GO_P: 6, RACE_SLOWDOWN: 10, GO_COMPILE_MIN: 2 };

/** Expected minutes per Go and -race job name (go-<shard>, race-<shard>), from scripts/test-shards.json in `src`. */
export function shardMinutes(src) {
  let cfg;
  try {
    cfg = JSON.parse(fs.readFileSync(path.join(src, 'scripts', 'test-shards.json'), 'utf8'));
  } catch {
    return {};
  }
  const secs = new Map();
  const cut = new Set();
  for (const units of Object.values(cfg.units ?? {})) {
    for (const u of units) {
      secs.set(u.name, u.weight_s ?? 0);
      cut.add(u.name);
    }
  }
  for (const u of cfg.packages ?? []) secs.set(u.name, u.weight_s ?? 0);
  const { RACE_P, GO_P, RACE_SLOWDOWN, GO_COMPILE_MIN } = SHARD_TIMING;
  const round = (m) => Math.round(m * 10) / 10;
  const out = {};
  for (const [profile, shards] of Object.entries(cfg.profiles ?? {})) {
    for (const s of shards) {
      const total = s.units.reduce((n, u) => n + (secs.get(u) ?? 0), 0) / 60;
      const byFile = s.units.every((u) => cut.has(u));
      if (profile === 'race') out[`race-${s.name}`] = round(byFile ? total : total / RACE_P);
      if (profile === 'go') out[`go-${s.name}`] = round(GO_COMPILE_MIN + (byFile ? total : total / GO_P) / RACE_SLOWDOWN);
    }
  }
  return out;
}

/** A job's expected minutes: what the plan gave it, or the table by name and kind. */
export function expectedMinutes(job, table = EXPECTED_MINUTES) {
  if (Number.isFinite(job.expect)) return job.expect;
  return table[job.name] ?? table[job.weightKind ?? job.kind] ?? table[job.kind] ?? 10;
}

const NAME = /^[a-z0-9][a-z0-9-]*$/;
const KINDS = ['cypress', 'local', 'nopub', 's3'];

function lines(text) {
  return text
    .split('\n')
    .map((l) => l.replace(/\r$/, ''))
    .filter((l) => l.trim() && !l.trimStart().startsWith('#'))
    .map((l) => l.trim().split(/\s+/));
}

/** A Go or -race list: `name run-regex package...` (scripts/test-shards.mjs --format chain prints it). */
export function parseTestList(text, source = 'list') {
  const out = [];
  for (const [name, run, ...rest] of lines(text)) {
    if (!NAME.test(name)) throw new Error(`${source}: "${name}" is not a job name ([a-z0-9-])`);
    if (!run || rest.length === 0) throw new Error(`${source}: ${name} needs a run-regex (or -) and at least one package`);
    const pkgs = rest.filter((p) => !p.startsWith('!'));
    const exclude = rest.filter((p) => p.startsWith('!')).map((p) => p.slice(1));
    if (pkgs.length === 0) throw new Error(`${source}: ${name} names no package to test`);
    const regex = run === '-' ? '' : run;
    if (regex && (pkgs.length !== 1 || pkgs[0].includes('...') || exclude.length)) {
      throw new Error(`${source}: ${name} has a run-regex, so it names exactly one package (not ${rest.join(' ')})`);
    }
    out.push({ name, run: regex, pkgs, exclude });
  }
  assertUnique(out, source);
  return out;
}

/**
 * The `--shard i/N` of a browser line's arguments (`--shard i/N` or
 * `--shard=i/N`), or null. e2e/run.mjs refuses a malformed one; so does the
 * list, before a run is spent on it.
 */
export function shardOf(args, source = 'list') {
  const found = [];
  args.forEach((a, i) => {
    if (a === '--shard') found.push(args[i + 1] ?? '');
    else if (a.startsWith('--shard=')) found.push(a.slice(8));
  });
  if (found.length === 0) return null;
  const m = found.length === 1 ? /^(\d+)\/(\d+)$/.exec(found[0]) : null;
  const current = m ? Number(m[1]) : 0;
  const total = m ? Number(m[2]) : 0;
  if (!m || total < 1 || current < 1 || current > total) throw new Error(`${source}: --shard ${found.join(' ')}: one i/N, 1 <= i <= N`);
  return { current, total };
}

/** The browser list: `name kind engines ds args...`. */
export function parseE2eList(text, source = 'list') {
  const out = [];
  for (const [name, kind, engines, ds, ...args] of lines(text)) {
    if (!NAME.test(name)) throw new Error(`${source}: "${name}" is not a job name ([a-z0-9-])`);
    if (!KINDS.includes(kind)) throw new Error(`${source}: ${name}: kind "${kind}" is none of ${KINDS.join(', ')}`);
    if (!engines) throw new Error(`${source}: ${name} names no engines (- for Cypress)`);
    if (ds !== '0' && ds !== '1') throw new Error(`${source}: ${name}: ds is 0 or 1, not "${ds}"`);
    if (kind !== 'cypress' && engines === '-') throw new Error(`${source}: ${name}: a Playwright line names its engines`);
    const shard = shardOf(args, `${source}: ${name}`);
    if (shard && kind === 'cypress') throw new Error(`${source}: ${name}: --shard cuts the Playwright suite, not Cypress`);
    out.push({ name, kind, engines: engines === '-' ? [] : engines.split(','), ds: ds === '1', args, shard });
  }
  assertUnique(out, source);
  return out;
}

function assertUnique(rows, source) {
  const seen = new Set();
  for (const r of rows) {
    if (seen.has(r.name)) throw new Error(`${source}: ${r.name} is listed twice`);
    seen.add(r.name);
  }
}

/**
 * A list's text: `spec` is a file path, or `cmd:<command>` whose standard
 * output is the list, run in `cwd` (the tree under test) by bash, or by the
 * Windows shell where `run.mjs --plan` is asked on Windows.
 */
export function readList(spec, { cwd }) {
  if (spec.startsWith('cmd:')) {
    const cmd = spec.slice(4);
    const r = spawnSync(cmd, {
      cwd,
      shell: process.platform === 'win32' ? true : 'bash',
      encoding: 'utf8',
      maxBuffer: 64 * 1024 * 1024,
      windowsHide: true,
    });
    if (r.status !== 0) throw new Error(`list command failed (${r.status}): ${cmd}\n${r.stderr}`);
    return r.stdout;
  }
  return fs.readFileSync(path.isAbsolute(spec) ? spec : path.resolve(cwd, spec), 'utf8');
}

/** The lists a profile reads: the environment's, else the shards of scripts/test-shards.json and lists/e2e.txt. */
export function loadLists(env, { cwd }) {
  const go = env.CHAIN_GO_LIST || SHARD_LISTS.go;
  const race = env.CHAIN_RACE_LIST || SHARD_LISTS.race;
  const e2e = env.CHAIN_E2E_LIST || path.join(CHAIN_DIR, 'lists', 'e2e.txt');
  return {
    go: parseTestList(readList(go, { cwd }), go),
    race: parseTestList(readList(race, { cwd }), race),
    e2e: parseE2eList(readList(e2e, { cwd }), e2e),
  };
}

const slug = (pkg) => pkg.replace(/^\.\//, '').replace(/[^a-z0-9]+/gi, '-').replace(/^-|-$/g, '').toLowerCase() || 'root';

/** A short name per package: its last path element, or the whole path where two would share one. */
function shortNames(pkgs) {
  const last = (p) => slug(p.split('/').filter(Boolean).at(-1) || p);
  const count = new Map();
  for (const p of pkgs) count.set(last(p), (count.get(last(p)) ?? 0) + 1);
  return new Map(pkgs.map((p) => [p, count.get(last(p)) > 1 ? slug(p) : last(p)]));
}

/** Minutes for a browser line: its own, else its engine's (a single-engine Playwright line), cut by --shard. */
function trackMinutes(t, table) {
  const engine = t.engines.length === 1 && t.kind === 'local' && t.ds ? table[`e2e-${t.engines[0]}`] : undefined;
  const whole = table[`e2e-${t.name}`] ?? engine ?? table[t.kind] ?? 10;
  return t.shard && table[`e2e-${t.name}`] === undefined ? Math.round((whole / t.shard.total) * 10) / 10 : whole;
}

/**
 * The plan of a profile.
 *
 *   full      the build; the web, docs and e2e-typecheck gates; the shard
 *             list's own check (scripts/test-shards.mjs check); the Go jobs
 *             (with PostgreSQL, MySQL, Redis, SMB); the migrations up and
 *             down on SQLite, PostgreSQL and MySQL; the -race jobs; and the
 *             browser list.
 *   nightly   what full runs, and the NIGHTLY_EXTRAS CHAIN_NIGHTLY_EXTRAS
 *             asks for in the browser round: ds-go right after the last line
 *             with the Document Server, s3-live, shots and realenv at its end.
 *             scripts/chain/nightly.mjs starts it (task #175).
 *   targeted  the build and its three fast gates, then only what is asked
 *             for: CHAIN_GO_PKGS, CHAIN_RACE_PKGS, CHAIN_MIGRATE=1,
 *             CHAIN_CY_SPECS, CHAIN_E2E_GREP (on CHAIN_E2E_BROWSERS, with
 *             the Document Server unless CHAIN_E2E_DS=0), and the nightly
 *             extras CHAIN_EXTRAS names (targetedExtras: s3-live by hand,
 *             with credentials a person brings - nightly.mjs s3-live).
 *
 * `src` is the tree under test: the Go and -race jobs' expected minutes are
 * read from its scripts/test-shards.json.
 */
export function buildPlan({ profile, lists, env = {}, src = path.resolve(CHAIN_DIR, '..', '..') }) {
  if (!PROFILES.includes(profile)) throw new Error(`profile "${profile}" is none of ${PROFILES.join(', ')}`);
  const table = { ...EXPECTED_MINUTES, ...shardMinutes(src) };
  const pool = [];
  const add = (job) => {
    const j = { needs: ['build'], db: false, env: {}, ...job };
    pool.push({ ...j, expect: expectedMinutes(j, table) });
  };
  add({ name: 'build', kind: 'build', needs: [], prio: 0, weight: WEIGHTS.build });
  add({ name: 'web', kind: 'web', prio: 10, weight: WEIGHTS.web });
  add({ name: 'docs', kind: 'docs', prio: 11, weight: WEIGHTS.docs });
  add({ name: 'e2etsc', kind: 'e2etsc', prio: 12, weight: WEIGHTS.e2etsc });

  let goList = lists.go;
  let raceList = lists.race;
  let track = lists.e2e;
  let migrate = true;
  let shardsCheck = true;
  if (profile === 'targeted') {
    const words = (v) => (v || '').trim().split(/\s+/).filter(Boolean);
    goList = env.CHAIN_GO_PKGS ? [{ name: 'targeted', run: env.CHAIN_GO_RUN || '', pkgs: words(env.CHAIN_GO_PKGS), exclude: [] }] : [];
    raceList = env.CHAIN_RACE_PKGS ? [{ name: 'targeted', run: '', pkgs: words(env.CHAIN_RACE_PKGS), exclude: [] }] : [];
    migrate = env.CHAIN_MIGRATE === '1';
    shardsCheck = false;
    track = [];
    if (env.CHAIN_CY_SPECS) track.push({ name: 'cypress', kind: 'cypress', engines: [], ds: false, args: ['--spec', env.CHAIN_CY_SPECS], shard: null });
    if (env.CHAIN_E2E_GREP) {
      track.push({
        name: 'targeted',
        kind: 'local',
        engines: words((env.CHAIN_E2E_BROWSERS || 'chromium').replace(/,/g, ' ')),
        ds: env.CHAIN_E2E_DS !== '0',
        args: ['--grep', env.CHAIN_E2E_GREP],
        shard: null,
      });
    }
  }
  if (shardsCheck) add({ name: 'shards-check', kind: 'shards-check', prio: 13, weight: WEIGHTS['shards-check'] });

  goList.forEach((g, i) =>
    add({ name: `go-${g.name}`, kind: 'go', prio: 20 + i, weight: WEIGHTS.go, db: true, env: testEnv(g) }),
  );
  if (migrate) add({ name: 'migrate', kind: 'migrate', prio: 35, weight: WEIGHTS.migrate, db: true });

  // A shard with a -run regex names one package; that package's -race test
  // binary is built once, by a job of its own, and every shard of it runs it.
  const built = new Map();
  const short = shortNames([...new Set(raceList.filter((r) => r.run).map((r) => r.pkgs[0]))]);
  raceList.forEach((r, i) => {
    if (r.run) {
      const pkg = r.pkgs[0];
      const bin = `${slug(pkg)}.race.test`;
      if (!built.has(pkg)) {
        const name = `race-build-${short.get(pkg)}`;
        built.set(pkg, name);
        add({ name, kind: 'race-build', prio: 40 + built.size, weight: WEIGHTS['race-build'], env: { PKG: pkg, BIN: bin } });
      }
      add({
        name: `race-${r.name}`,
        kind: 'race',
        weightKind: 'race-shard',
        prio: 50 + i,
        weight: WEIGHTS['race-shard'],
        needs: ['build', built.get(pkg)],
        env: { ...testEnv(r), BIN: bin },
      });
    } else {
      add({ name: `race-${r.name}`, kind: 'race', prio: 40, weight: WEIGHTS.race, env: testEnv(r) });
    }
  });

  const round = track.map((t) => ({
    ...t,
    name: `e2e-${t.name}`,
    needs: ['build'],
    weight: t.kind === 'cypress' ? WEIGHTS.cypress : WEIGHTS.e2e,
    expect: trackMinutes(t, table),
  }));
  const extra = (name) => {
    const j = { name, kind: name, engines: [], ds: name === 'ds-go', args: [], shard: null, needs: ['build'], weight: WEIGHTS[name] };
    return { ...j, expect: expectedMinutes(j, table) };
  };
  if (profile === 'targeted') for (const name of targetedExtras(env)) round.push(extra(name));
  if (profile === 'nightly') {
    const extras = nightlyExtras(env);
    // The Document Server is stopped once no later line needs it
    // (run.mjs runTrack): ds-go goes where it is still up.
    if (extras.includes('ds-go')) round.splice(round.map((t) => t.ds).lastIndexOf(true) + 1, 0, extra('ds-go'));
    for (const name of ['s3-live', 'shots', 'realenv']) if (extras.includes(name)) round.push(extra(name));
  }
  // `index` is the line's place in the round: run.mjs gives each line the port
  // CHAIN_E2E_PORT + index.
  return { profile, pool, track: round.map((t, index) => ({ ...t, index })) };
}

/**
 * The nightly extras a targeted run adds: CHAIN_EXTRAS (comma-separated,
 * the names of NIGHTLY_EXTRAS), none by default. How one of them runs on its
 * own: `CHAIN_EXTRAS=s3-live` with FILEX_TEST_S3_* is the live S3 tests
 * against the bucket a person brings.
 */
export function targetedExtras(env = {}) {
  const v = String(env.CHAIN_EXTRAS ?? '').trim();
  if (!v || v === 'none') return [];
  const asked = v.split(/[\s,]+/).filter(Boolean);
  for (const a of asked) {
    if (!NIGHTLY_EXTRAS.includes(a)) throw new Error(`CHAIN_EXTRAS: "${a}" is none of ${NIGHTLY_EXTRAS.join(', ')} (or none)`);
  }
  return NIGHTLY_EXTRAS.filter((x) => asked.includes(x));
}

function testEnv(row) {
  return { RUN: row.run, PKGS: row.pkgs.join(' '), EXCLUDE: row.exclude.join(' ') };
}

/** GiB the browser round holds while it runs: its heaviest job, plus the Document Server if any job needs it. */
export function trackWeight(track) {
  if (track.length === 0) return 0;
  return Math.max(...track.map((t) => t.weight)) + (track.some((t) => t.ds) ? WEIGHTS.ds : 0);
}
