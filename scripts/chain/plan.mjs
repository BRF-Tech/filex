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

import { DS_EXTRAS, NIGHTLY_EXTRAS, nightlyExtras } from './nightly-lib.mjs';

export const CHAIN_DIR = path.dirname(fileURLToPath(import.meta.url));

export const PROFILES = ['full', 'targeted', 'nightly'];

// What the nightly profile adds to the full one (ds-go, s3-live, shots,
// realenv) is defined next to the nightly run's other decisions, which also
// need it.
export { DS_EXTRAS, NIGHTLY_EXTRAS, nightlyExtras };

/** Where the Go and -race jobs come from unless CHAIN_GO_LIST / CHAIN_RACE_LIST say otherwise. */
export const SHARD_LISTS = {
  go: 'cmd:node scripts/test-shards.mjs go --profile go --format chain',
  race: 'cmd:node scripts/test-shards.mjs go --profile race --format chain',
};

/**
 * GiB each job is budgeted for: the most its container held on the build
 * host (cAdvisor working set, anonymous memory plus the page cache it keeps
 * active), rounded up. run.mjs also refuses to start a job while the host's
 * MemAvailable is short (CHAIN_MEM_RESERVE_GB), and records what each job
 * really held (result.json `load`): re-read these from there.
 *
 * ⚠ Task #194: until 0.53 the web job was budgeted at 1 GiB from the 0.51
 * rounds' 0.68 (2.04). The 0.53 runs measured 2.0-3.03 - vitest's 19 forks
 * on a 20-core host, and the type-checks after them came to 2.91 - so the web
 * job, Cypress, the shard check and the first Go job started together beside
 * the databases, and the host stalled for a minute at the start of three of
 * five runs (PSI memory "full" 0.55-0.73) while the budget said 8 of 8 GiB.
 *
 * measured (the four 0.53 full runs of 2026-10-06/07 and the night of
 * 2026-10-07; min-max of the per-run peaks):
 *   build 3.44-4.72 · web 2.00-3.03 · docs 0.31 · shards-check 0.69-0.95 ·
 *   go 0.13-0.76, go-io-rest 0.57-1.22 · race-db-io/race-rest 0.34-1.22 ·
 *   race shard 0.12-0.86 · Cypress 0.85-1.39 · Playwright chromium 3.26-3.92,
 *   firefox 2.91-3.17, webkit 3.83-4.04, nopub 2.07-3.05, nods 1.35-1.45 ·
 *   Document Server 0.63-0.72 · MySQL 0.68-0.77 + PostgreSQL 0.14 + Redis and
 *   Samba 0.04 · shots 1.06 (one run)
 * e2etsc, migrate and race-build end before a sample catches them (4-28 s).
 * cAdvisor refreshes a container about once a minute, so these are floors:
 * the -race jobs keep 1.5 for the 1.22 seen (their cgroup's own peak, page
 * cache included, was 1.8). JOB_WEIGHTS holds the jobs measured above their kind.
 *
 * The nightly extras until a night measures them: ds-go and s3-live are Go
 * test binaries (budgeted as a Go job), realenv a Playwright runner next to
 * Keycloak, OpenLDAP, Pebble and its own Document Server (as an engine);
 * shots held 1.06 in its one run and stays at an engine's 4.
 */
export const WEIGHTS = {
  build: 5,
  web: 3.25,
  docs: 0.5,
  e2etsc: 0.5,
  'shards-check': 1,
  go: 1,
  migrate: 0.25,
  'race-build': 1,
  race: 1.5,
  'race-shard': 1,
  cypress: 1.5,
  e2e: 4.25,
  ds: 0.75,
  db: 1,
  'ds-go': 1,
  's3-live': 1,
  shots: 4,
  realenv: 4,
};

/**
 * Jobs measured above their kind's weight, by job name (a shard of
 * scripts/test-shards.json): web/tests/deploy/chainSchedule.test.ts holds
 * that each still names a job of the full plan, so a renamed shard cannot
 * silently lose its weight.
 */
export const JOB_WEIGHTS = {
  // internal/io and the rest of the packages: 0.57-1.22 GiB against 0.2-0.76 for the other Go shards.
  'go-io-rest': 1.25,
};

/** A job's weight: its own (JOB_WEIGHTS), else its kind's. */
export function weightOf(name, kind) {
  return JOB_WEIGHTS[name] ?? WEIGHTS[kind];
}

/**
 * Minutes a job is expected to take, for `run.mjs --plan` and the scheduling
 * test, where scripts/test-shards.json has nothing to say: measured on the
 * build host (chain.log of 0.51 and 0.52). The Go and -race shards' minutes
 * come from that file's weights (shardMinutes below), and a Playwright line
 * cut with --shard i/N takes 1/N of its engine's.
 */
export const EXPECTED_MINUTES = {
  build: 3.5,
  // 2.4-4.7 in the 0.53 runs and the first night: vitest, then four type-checks.
  web: 4,
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
 *             asks for in the browser round: ds-go and shots (DS_EXTRAS,
 *             which need the Document Server) right after the last line with
 *             it, s3-live and realenv at its end. scripts/chain/nightly.mjs
 *             starts it (task #175).
 *   targeted  the build and its three fast gates, then only what is asked
 *             for: CHAIN_GO_PKGS, CHAIN_RACE_PKGS, CHAIN_MIGRATE=1,
 *             CHAIN_CY_SPECS, CHAIN_E2E_GREP (on CHAIN_E2E_BROWSERS, with
 *             the Document Server unless CHAIN_E2E_DS=0), and the nightly
 *             extras CHAIN_EXTRAS names (targetedExtras: s3-live by hand,
 *             with credentials a person brings - nightly.mjs s3-live; shots
 *             for a release's pictures).
 *
 * The shots job takes every scene (`pnpm shots --all`); CHAIN_SHOTS_ONLY
 * (comma-separated script names) narrows it to those, with `--only`
 * (shotsOnly). In the nightly profile only, a run whose one failure is a
 * language pack behind the tree is a warning, not a red job
 * (SHOTS_PACKS_BEHIND=warn).
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
    add({ name: `go-${g.name}`, kind: 'go', prio: 20 + i, weight: weightOf(`go-${g.name}`, 'go'), db: true, env: testEnv(g) }),
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
        weight: weightOf(`race-${r.name}`, 'race-shard'),
        needs: ['build', built.get(pkg)],
        env: { ...testEnv(r), BIN: bin },
      });
    } else {
      add({ name: `race-${r.name}`, kind: 'race', prio: 40, weight: weightOf(`race-${r.name}`, 'race'), env: testEnv(r) });
    }
  });

  const round = track.map((t) => ({
    ...t,
    name: `e2e-${t.name}`,
    needs: ['build'],
    weight: weightOf(`e2e-${t.name}`, t.kind === 'cypress' ? 'cypress' : 'e2e'),
    expect: trackMinutes(t, table),
  }));
  const extra = (name) => {
    const only = name === 'shots' ? shotsOnly(env) : '';
    const j = {
      name,
      kind: name,
      engines: [],
      ds: DS_EXTRAS.includes(name),
      args: [],
      shard: null,
      needs: ['build'],
      weight: WEIGHTS[name],
      env: {
        // job/shots.sh: `--only` these scripts instead of `--all`.
        ...(only ? { SHOTS_ONLY: only } : {}),
        // A night whose only failed scene is a language pack behind the tree
        // is a warning in the morning report, not red; a release's run (a
        // targeted CHAIN_EXTRAS=shots) stays red (the maintainer, 2026-10-08, #187).
        ...(name === 'shots' && profile === 'nightly' ? { SHOTS_PACKS_BEHIND: 'warn' } : {}),
      },
    };
    return { ...j, expect: expectedMinutes(j, table) };
  };
  if (profile === 'targeted') for (const name of targetedExtras(env)) round.push(extra(name));
  if (profile === 'nightly') {
    const extras = nightlyExtras(env);
    // The Document Server is stopped once no later line needs it
    // (run.mjs runTrack): ds-go and shots go where it is still up, right
    // after the last browser line with it, so it is not kept running through
    // the lines without it.
    const withDs = extras.filter((name) => DS_EXTRAS.includes(name));
    round.splice(round.map((t) => t.ds).lastIndexOf(true) + 1, 0, ...withDs.map(extra));
    for (const name of extras) if (!DS_EXTRAS.includes(name)) round.push(extra(name));
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

/**
 * The shot scripts CHAIN_SHOTS_ONLY narrows the shots job to (comma-separated
 * names, without .mjs), as one `--only` value, or '' for every scene. A
 * release whose language packs are behind takes the rest this way, and the
 * published language pack picture stands (docs/CONTRIBUTING.md → Release
 * process, step 2). scripts/shots.mjs refuses a name that is no shot script.
 */
export function shotsOnly(env = {}) {
  const v = String(env.CHAIN_SHOTS_ONLY ?? '').trim();
  if (!v) return '';
  const names = v.split(/[\s,]+/).filter(Boolean).map((n) => n.replace(/\.mjs$/, ''));
  for (const n of names) {
    if (!NAME.test(n)) throw new Error(`CHAIN_SHOTS_ONLY: "${n}" is not a shot script name ([a-z0-9-])`);
  }
  return names.join(',');
}

function testEnv(row) {
  return { RUN: row.run, PKGS: row.pkgs.join(' '), EXCLUDE: row.exclude.join(' ') };
}

/** GiB the browser round holds while it runs: its heaviest job, plus the Document Server if any job needs it. */
export function trackWeight(track) {
  if (track.length === 0) return 0;
  return Math.max(...track.map((t) => t.weight)) + (track.some((t) => t.ds) ? WEIGHTS.ds : 0);
}
