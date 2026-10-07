#!/usr/bin/env node
// The whole test chain on one Linux host with Docker, its independent parts
// at the same time within a memory budget. Start it with scripts/chain/run.sh,
// which takes the host's build lock first; docs/CONTRIBUTING.md → "The whole
// chain on one Linux host" says what it runs and how to configure it.
//
// What runs (scripts/chain/plan.mjs): the build; then, side by side,
//   - the browser round (scripts/chain/lists/e2e.txt), one job at a time:
//     Cypress, Playwright on each engine with a Document Server, the runs
//     without one, without a public URL and against S3;
//   - the pool: the web, docs and e2e-typecheck gates, the shard list's own
//     check, the Go shards with their database sidecars, the migrations on
//     three engines, and the -race shards (both from scripts/test-shards.json
//     through scripts/test-shards.mjs), as many at once as the budget allows
//     (scripts/chain/schedule.mjs).
// The nightly profile adds the Go test against the Document Server, the Go
// tests against a real S3 provider, `pnpm shots` and e2e/realenv to the
// browser round (plan.mjs NIGHTLY_EXTRAS); realenv is a host job, the one job
// that is not a container.
//
// What it leaves, under $CHAIN_ROOT/runs/<run-id>/: chain.log (a JOBSTART and
// a JOBEXIT line per job), logs/<job>.log, out/<job>/ (test output), and
// result.json (each job's name, time, exit code, log and one-line summary;
// rewritten after every job, final when `finished` is set). A copy of the
// last result of each profile is $CHAIN_ROOT/runs/latest-<profile>.json.
// When CHAIN_NOTIFY_URL is set, the end of the run is posted there.
//
// ⚠ Every container, network and sidecar it starts is named <prefix>-* and
// labelled filex-chain=<prefix>; at start it removes what a stopped run of
// the same prefix left behind, and nothing else. One run per prefix at a
// time: the lock is what guarantees it.

import { spawn, spawnSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { postNotify } from '../lib/notify.mjs';
import { mergeEnv, parseEnvFile } from './env.mjs';
import { S3_LIVE_KEYS, s3LiveFile } from './nightly-lib.mjs';
import { CHAIN_DIR, PROFILES, WEIGHTS, buildPlan, expectedMinutes, loadLists, trackWeight } from './plan.mjs';
import { pickJobs, poolBudget, readyJobs, simulate } from './schedule.mjs';

const REPO = path.resolve(CHAIN_DIR, '..', '..');

const USAGE = `usage: bash scripts/chain/run.sh [options]

  --profile full|targeted|nightly   what to run (default: full); nightly is full
                   plus CHAIN_NIGHTLY_EXTRAS (ds-go, s3-live, shots, realenv), and is
                   what scripts/chain/nightly.mjs starts
  --src DIR        the checkout under test (default: the repository run.sh is in)
  --env FILE       settings, KEY=VALUE lines (default: $CHAIN_ENV, else
                   ~/.config/filex-chain.env when it exists); the process
                   environment wins over the file
  --run-id ID      this run's directory name (default: <profile>-<UTC time>-<commit>)
  --plan           print the jobs, the memory budget and the expected wall
                   time, and run nothing (works on any machine)
  --keep-services  leave the sidecars running at the end
  --print-lock     print the lock file run.sh takes, and exit

Exit: 0 every job green, 1 a job red or skipped, 2 a setup error, 130 stopped;
run.sh: 3 the lock was not free within CHAIN_LOCK_WAIT_MIN minutes.`;

// ── settings ────────────────────────────────────────────────────────────────

export function parseArgs(argv) {
  const args = { profile: 'full' };
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i];
    const val = () => {
      const v = argv[i + 1];
      if (v === undefined || v.startsWith('--')) throw new Error(`${a} needs a value`);
      i += 1;
      return v;
    };
    if (a === '--profile') args.profile = val();
    else if (a === '--src') args.src = val();
    else if (a === '--env') args.env = val();
    else if (a === '--run-id') args.runId = val();
    else if (a === '--plan') args.plan = true;
    else if (a === '--keep-services') args.keepServices = true;
    else if (a === '--print-lock') args.printLock = true;
    else if (a === '--help' || a === '-h') args.help = true;
    else throw new Error(`unknown option ${a}`);
  }
  if (!PROFILES.includes(args.profile)) throw new Error(`--profile ${args.profile}: one of ${PROFILES.join(', ')}`);
  return args;
}

export { parseEnvFile };

function environment(args) {
  const fallback = path.join(os.homedir(), '.config', 'filex-chain.env');
  const file = args.env || process.env.CHAIN_ENV || (fs.existsSync(fallback) ? fallback : '');
  return { env: mergeEnv(file ? fs.readFileSync(file, 'utf8') : '', process.env), file };
}

function settings(env, args) {
  const num = (key, def) => {
    const v = env[key];
    if (v === undefined || v === '') return def;
    const n = Number(v);
    if (!Number.isFinite(n) || n < 0) throw new Error(`${key}=${v} is not a number`);
    return n;
  };
  const root = path.resolve(env.CHAIN_ROOT || path.join(os.homedir(), '.cache', 'filex-chain'));
  const src = path.resolve(args.src || env.CHAIN_SRC || REPO);
  const dir = (key, sub) => path.resolve(env[key] || path.join(root, 'cache', sub));
  const goImage = env.CHAIN_GO_IMAGE || goImageOf(src);
  const [uid, gid] = (env.CHAIN_GO_USER || '1000:1000').split(':').map(Number);
  return {
    root,
    src,
    lock: env.CHAIN_LOCK || path.join(root, 'lock'),
    prefix: env.CHAIN_PREFIX || 'fxchain',
    memGb: num('CHAIN_MEM_GB', 8),
    reserveGb: num('CHAIN_MEM_RESERVE_GB', 1),
    memWaitMin: num('CHAIN_MEM_WAIT_MIN', 20),
    poolMax: num('CHAIN_POOL_MAX', 3),
    cpus: num('CHAIN_CPUS', Math.min(12, os.cpus().length)),
    goUser: { uid, gid },
    images: {
      pw: env.CHAIN_PW_IMAGE || playwrightImageOf(src),
      go: goImage,
      node: env.CHAIN_NODE_IMAGE || 'node:22',
      pg: env.CHAIN_PG_IMAGE || 'postgres:16-alpine',
      my: env.CHAIN_MYSQL_IMAGE || 'mysql:8.4',
      redis: env.CHAIN_REDIS_IMAGE || 'redis:7-alpine',
      smb: env.CHAIN_SMB_IMAGE || 'dperson/samba',
      ds: env.CHAIN_DS_IMAGE || 'onlyoffice/documentserver:latest',
      s3: env.CHAIN_S3_IMAGE || s3ImageOf(src),
    },
    cache: {
      gomod: dir('CHAIN_GOMODCACHE', 'gomod'),
      gocache: dir('CHAIN_GOCACHE', 'gocache'),
      // The build compiles as root, the Go jobs as CHAIN_GO_USER: a cache the
      // build created first is root's, and the Go jobs could not write to it.
      // Point both at one directory only where it already belongs to that user.
      gocacheRoot: dir('CHAIN_GOCACHE_ROOT', 'gocache-root'),
      pnpm: dir('CHAIN_PNPM_STORE', 'pnpm-store'),
      cypress: dir('CHAIN_CYPRESS_CACHE', 'cypress'),
      electron: dir('CHAIN_ELECTRON_CACHE', 'electron'),
      home: dir('CHAIN_HOME', 'home'),
      goroot: dir('CHAIN_GOROOT', `go-${goImage.replace(/[^a-z0-9.]+/gi, '-')}`),
    },
    apps: {
      sign: env.CHAIN_SIGN_APP_DIR || '',
      convert: env.CHAIN_CONVERT_APP_DIR || '',
      'lang-es': env.CHAIN_LANG_ES_DIR || '',
      'lang-de': env.CHAIN_LANG_DE_DIR || '',
      'lang-fr': env.CHAIN_LANG_FR_DIR || '',
      'lang-ar': env.CHAIN_LANG_AR_DIR || '',
    },
    requireApps: env.CHAIN_REQUIRE_APPS !== '0',
    dsSubnet: env.CHAIN_DS_SUBNET || '172.30.81.0/24',
    e2ePort: num('CHAIN_E2E_PORT', 5212),
    migDown: num('CHAIN_MIG_DOWN', 3),
    goP: num('CHAIN_GO_P', 6),
    temp: {
      prom: env.CHAIN_TEMP_PROM || '',
      query: env.CHAIN_TEMP_QUERY || '',
      max: num('CHAIN_TEMP_MAX', 72),
      waitMin: num('CHAIN_TEMP_WAIT_MIN', 30),
    },
    notify: {
      url: env.CHAIN_NOTIFY_URL || '',
      keyFile: env.CHAIN_NOTIFY_KEY_FILE || '',
      group: env.CHAIN_NOTIFY_GROUP || 'infra',
      source: env.CHAIN_NOTIFY_SOURCE || 'filex-chain',
    },
    // The s3-live job of the nightly profile: the FILEX_TEST_S3_* settings,
    // handed to the job in a 0600 file by prepare() (nightly-lib s3LiveFile).
    s3Live: Object.fromEntries(
      [...S3_LIVE_KEYS.required, ...S3_LIVE_KEYS.optional].filter((k) => env[k] !== undefined).map((k) => [k, env[k]]),
    ),
    // The realenv job of the nightly profile (job/realenv.sh).
    realenv: {
      stages: env.CHAIN_REALENV_STAGES || '',
      allowSkip: env.CHAIN_REALENV_ALLOW_SKIP === '1',
      privNet: env.CHAIN_REALENV_PRIV_NET || '',
      pubNet: env.CHAIN_REALENV_PUB_NET || '',
    },
  };
}

function playwrightImageOf(src) {
  const lock = readOr(path.join(src, 'pnpm-lock.yaml'));
  const m = /^ {2}playwright@(\d+\.\d+\.\d+):/m.exec(lock);
  if (!m) throw new Error(`no playwright@<version> in ${src}/pnpm-lock.yaml: set CHAIN_PW_IMAGE`);
  return `mcr.microsoft.com/playwright:v${m[1]}-noble`;
}

function goImageOf(src) {
  const m = /^go (\d+\.\d+)/m.exec(readOr(path.join(src, 'backend', 'go.mod')));
  if (!m) throw new Error(`no go directive in ${src}/backend/go.mod: set CHAIN_GO_IMAGE`);
  return `golang:${m[1]}`;
}

function s3ImageOf(src) {
  const m = /S3_IMAGE_DEFAULT = '([^']+)'/.exec(readOr(path.join(src, 'e2e', 'lib', 's3server.mjs')));
  if (!m) throw new Error(`no S3_IMAGE_DEFAULT in ${src}/e2e/lib/s3server.mjs: set CHAIN_S3_IMAGE`);
  return m[1];
}

function readOr(file) {
  try {
    return fs.readFileSync(file, 'utf8');
  } catch {
    return '';
  }
}

function git(src, args) {
  const r = spawnSync('git', ['-c', 'safe.directory=*', '-C', src, ...args], { encoding: 'utf8' });
  return r.status === 0 ? r.stdout.trim() : '';
}

// ── --plan ──────────────────────────────────────────────────────────────────

function budgetOf(cfg, plan) {
  const trackGb = trackWeight(plan.track);
  const dbGb = plan.pool.some((j) => j.db) ? WEIGHTS.db : 0;
  return { memGb: cfg.memGb, trackGb, dbGb, poolMax: cfg.poolMax };
}

function hm(minutes) {
  const m = Math.round(minutes);
  return `${Math.floor(m / 60)}:${String(m % 60).padStart(2, '0')}`;
}

function printPlan(cfg, plan) {
  const b = budgetOf(cfg, plan);
  const sim = simulate(plan, { ...b, minutes: expectedMinutes });
  const sha = git(cfg.src, ['rev-parse', '--short', 'HEAD']) || '?';
  const when = new Map(sim.timeline.map((e) => [e.name, `${hm(e.start)}-${hm(e.end)}`]));
  const w = Math.max(22, ...[...plan.pool, ...plan.track].map((j) => j.name.length)) + 2;
  const needsW = Math.max(...plan.pool.map((j) => j.needs.join(',').length)) + 2;
  const out = [];
  out.push(`profile ${plan.profile} · ${cfg.src} (${sha})`);
  out.push(
    `memory budget ${b.memGb} GiB: browser round ${b.trackGb}, databases ${b.dbGb} while a Go or migration job is left, ` +
      `the pool the rest (at most ${b.poolMax} jobs at once); a job also waits while MemAvailable is under its weight + ${cfg.reserveGb} GiB`,
  );
  out.push('', `${'pool'.padEnd(w + 2)}${'needs'.padEnd(needsW)}GiB   ~min  expected`);
  for (const j of plan.pool) {
    out.push(`  ${j.name.padEnd(w)}${j.needs.join(',').padEnd(needsW)}${String(j.weight).padEnd(6)}${String(expectedMinutes(j)).padEnd(6)}${when.get(j.name) ?? ''}`);
  }
  out.push('', `${'browser round, in order'.padEnd(w + 2)}kind     engines                  ds GiB   ~min  expected`);
  for (const t of plan.track) {
    out.push(
      `  ${t.name.padEnd(w)}${t.kind.padEnd(9)}${(t.engines.join(',') || '-').padEnd(25)}${(t.ds ? '1' : '0').padEnd(3)}` +
        `${String(t.weight).padEnd(6)}${String(expectedMinutes(t)).padEnd(6)}${when.get(t.name) ?? ''}${t.args.length ? `  ${t.args.join(' ')}` : ''}`,
    );
  }
  out.push(
    '',
    `expected wall time ${hm(sim.wall)} (the minutes above through the same scheduler; ` +
      `at most ${sim.maxRunning} pool jobs at once, ${sim.peakGb.toFixed(2)} GiB held at the peak)`,
  );
  console.log(out.join('\n'));
}

// ── the run ─────────────────────────────────────────────────────────────────

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const iso = () => new Date().toISOString().replace(/\.\d{3}Z$/, 'Z');

function memAvailableGb() {
  const m = /^MemAvailable:\s+(\d+) kB/m.exec(readOr('/proc/meminfo'));
  return m ? Number(m[1]) / 1048576 : Infinity;
}

function docker(args, opts = {}) {
  return spawnSync('docker', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, ...opts });
}

/** Per job kind: image, script, container memory cap, CPU weight, and what it needs mounted. */
export const KIND = {
  build: { image: 'node', script: 'build.sh', mem: '10g', goroot: true, env: { NODE_OPTIONS: '--max-old-space-size=5120' } },
  // ⚠ The CPU weight of a browser: vitest holds every test to a wall-clock
  // timeout (5 s) as a browser spec does, and a Go or -race job holds none
  // that short. Beside the -race jobs at the default weight, a file that takes
  // 0.8 s alone took 14.7 s and one of its tests went over (appPluginFileTypes,
  // targeted-20261006-191050Z; homeLanding and packLocaleBoot in the full run).
  web: { image: 'pw', script: 'web.sh', mem: '8g', shares: 4096 },
  docs: { image: 'node', script: 'docs.sh', mem: '6g' },
  e2etsc: { image: 'pw', script: 'e2etsc.sh', mem: '4g' },
  go: { image: 'go', script: 'go.sh', mem: '8g', user: true },
  migrate: { image: 'go', script: 'migrate.sh', mem: '2g', user: true },
  'shards-check': { image: 'node', script: 'shards-check.sh', mem: '4g', goroot: true },
  'race-build': { image: 'go', script: 'race-build.sh', mem: '6g', user: true },
  race: { image: 'go', script: 'race.sh', mem: '6g', user: true },
  cypress: { image: 'pw', script: 'e2e.sh', mem: '9g', shares: 4096, browser: true },
  local: { image: 'pw', script: 'e2e.sh', mem: '9g', shares: 4096, browser: true },
  nopub: { image: 'pw', script: 'e2e.sh', mem: '9g', shares: 4096, browser: true },
  s3: { image: 'pw', script: 'e2e.sh', mem: '9g', shares: 4096, browser: true },
  // The nightly extras (plan.mjs NIGHTLY_EXTRAS). ds-go runs as root: the
  // Document Server's secret file is root's, 0600; so does s3-live, for the
  // bucket's key (etc/s3-live.env, 0600 - never a `-e` argument).
  'ds-go': { image: 'go', script: 'ds-go.sh', mem: '6g', apps: true, dsEnv: true },
  's3-live': { image: 'go', script: 's3-live.sh', mem: '6g', s3Live: true },
  shots: { image: 'pw', script: 'shots.sh', mem: '9g', shares: 4096, browser: true, goroot: true },
  // ⚠ A host job is no container: e2e/realenv/run.sh starts containers of its
  // own with this host's Docker, and mounts the tree by its path HERE.
  realenv: { host: true, script: 'realenv.sh' },
};

/** The images the jobs of these kinds run in (a host job runs in none). */
export function jobImages(kinds, images) {
  return [...new Set([...kinds].filter((k) => KIND[k].image).map((k) => images[KIND[k].image]))];
}

/**
 * The environment of a host job (KIND[kind].host): this process's own, and
 * where the job writes. ⚠ REALENV_LOCK is dropped: the chain holds the build
 * lock for the whole run, and realenv waiting for the same lock would wait
 * forever.
 */
export function hostJobEnv(base, { job, dir, src, prefix, images, realenv = {} }) {
  const env = { ...base };
  delete env.REALENV_LOCK;
  delete env.CHAIN_LOCK_HELD;
  return {
    ...env,
    JOB: job.name,
    OUT: path.join(dir, 'out', job.name),
    SRC: src,
    RUN_DIR: dir,
    REALENV_PREFIX: `${prefix}-re`,
    REALENV_PULL: '1',
    REALENV_WORK: path.join(dir, 'out', job.name, 'work'),
    REALENV_OO_IMAGE: images.ds,
    REALENV_RUNNER_IMAGE: images.pw,
    REALENV_STAGES: realenv.stages || '',
    REALENV_ALLOW_SKIP: realenv.allowSkip ? '1' : '0',
    ...(realenv.privNet ? { REALENV_PRIV_NET: realenv.privNet } : {}),
    ...(realenv.pubNet ? { REALENV_PUB_NET: realenv.pubNet } : {}),
  };
}

const FONTS_CONF = `<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "fonts.dtd">
<!-- The Playwright image's sans-serif is a CJK face that sets Latin wide; the
     suite's layout checks are calibrated on a Latin one (e2e lesson, 0.50). -->
<fontconfig>
  <alias binding="strong"><family>sans-serif</family><prefer><family>Liberation Sans</family></prefer></alias>
  <alias binding="strong"><family>system-ui</family><prefer><family>Liberation Sans</family></prefer></alias>
  <alias binding="strong"><family>ui-sans-serif</family><prefer><family>Liberation Sans</family></prefer></alias>
  <alias binding="strong"><family>Segoe UI</family><prefer><family>Liberation Sans</family></prefer></alias>
</fontconfig>
`;

class Chain {
  constructor(cfg, plan, args, envFile) {
    this.cfg = cfg;
    this.plan = plan;
    this.args = args;
    this.envFile = envFile;
    const sha = git(cfg.src, ['rev-parse', 'HEAD']);
    this.sha = sha;
    this.subject = git(cfg.src, ['log', '-1', '--format=%s']);
    this.dirty = git(cfg.src, ['status', '--porcelain', '--untracked-files=no']).split('\n').filter(Boolean).length;
    const stamp = new Date().toISOString().replace(/[-:]/g, '').replace(/\..*/, '').replace('T', '-');
    this.runId = args.runId || `${plan.profile}-${stamp}Z-${sha.slice(0, 8) || 'nogit'}`;
    this.dir = path.join(cfg.root, 'runs', this.runId);
    this.P = cfg.prefix;
    this.secret = crypto.randomBytes(12).toString('hex');
    this.status = {};
    this.records = {};
    this.running = new Map();
    this.dbUp = false;
    this.dsUp = false;
    this.vgwUp = false;
    this.trackDone = plan.track.length === 0;
    this.trackWaiting = false;
    this.stopping = false;
    this.minAvailGb = memAvailableGb();
    this.peakHeldGb = 0;
    this.started = iso();
    this.t0 = Date.now();
    this.budget = budgetOf(cfg, plan);
    for (const j of [...plan.pool, ...plan.track]) this.status[j.name] = 'pending';
  }

  log(line) {
    const s = `${iso()} ${line}`;
    fs.appendFileSync(path.join(this.dir, 'chain.log'), `${s}\n`);
    console.log(s);
  }

  // ── setup ──

  prepare() {
    for (const d of ['logs', 'out', 'bin', 'tmp', 'etc', 'home-go', 'vgw-data', 'fc/fontconfig']) {
      fs.mkdirSync(path.join(this.dir, d), { recursive: true });
    }
    // Not the Go toolchain's directory: `docker cp` creates it, and into an
    // existing one it would copy the toolchain as a subdirectory.
    for (const [name, d] of Object.entries(this.cfg.cache)) if (name !== 'goroot') fs.mkdirSync(d, { recursive: true });
    const { uid, gid } = this.cfg.goUser;
    fs.writeFileSync(
      path.join(this.dir, 'etc', 'passwd'),
      `root:x:0:0:root:/root:/bin/bash\nnobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin\nfx:x:${uid}:${gid}:fx:/w/run/home-go:/bin/bash\n`,
    );
    fs.writeFileSync(path.join(this.dir, 'etc', 'group'), `root:x:0:\nnogroup:x:65534:\nfx:x:${gid}:\n`);
    fs.writeFileSync(path.join(this.dir, 'fc', 'fontconfig', 'fonts.conf'), FONTS_CONF);
    fs.writeFileSync(path.join(this.dir, 'etc', 'ds.secret'), crypto.randomBytes(24).toString('hex'), { mode: 0o600 });
    this.prepareS3Live();
    if (process.getuid?.() === 0) {
      for (const d of ['out', 'bin', 'tmp', 'home-go']) fs.chownSync(path.join(this.dir, d), uid, gid);
      for (const d of [this.cfg.cache.gomod, this.cfg.cache.gocache]) {
        if (fs.statSync(d).uid === 0 && fs.readdirSync(d).length === 0) fs.chownSync(d, uid, gid);
      }
    }
  }

  /**
   * The s3-live job's credentials, from the settings into etc/s3-live.env
   * (0600, removed with ds.secret when the run ends). Names only in the log;
   * without the required ones the job itself is red and says which.
   */
  prepareS3Live() {
    if (![...this.plan.pool, ...this.plan.track].some((j) => KIND[j.kind]?.s3Live)) return;
    let file;
    try {
      file = s3LiveFile(this.cfg.s3Live);
    } catch (e) {
      this.log(`s3-live: ${e.message} - nothing handed to the job`);
      return;
    }
    if (file.text) fs.writeFileSync(path.join(this.dir, 'etc', 's3-live.env'), file.text, { mode: 0o600 });
    const given = Object.keys(this.cfg.s3Live).filter((k) => this.cfg.s3Live[k] !== '');
    this.log(`s3-live: ${given.length ? given.join(', ') : 'no FILEX_TEST_S3_* setting'}${file.missing.length ? `; missing ${file.missing.join(', ')} - the job will be red` : ''}`);
  }

  needs() {
    const kinds = new Set([...this.plan.pool, ...this.plan.track].map((j) => j.kind));
    return {
      db: this.plan.pool.some((j) => j.db),
      ds: this.plan.track.some((t) => t.ds),
      s3: kinds.has('s3'),
      images: jobImages(kinds, this.cfg.images),
    };
  }

  ensureImages() {
    const n = this.needs();
    const images = [...n.images, this.cfg.images.go];
    if (n.db) images.push(this.cfg.images.pg, this.cfg.images.my, this.cfg.images.redis, this.cfg.images.smb);
    if (n.ds) images.push(this.cfg.images.ds);
    if (n.s3) images.push(this.cfg.images.s3);
    for (const image of [...new Set(images)]) {
      if (docker(['image', 'inspect', image]).status === 0) continue;
      this.log(`pulling ${image}`);
      const r = docker(['pull', '--quiet', image]);
      if (r.status !== 0) throw new Error(`docker pull ${image} failed: ${r.stderr.trim()}`);
    }
    const goroot = this.cfg.cache.goroot;
    if (!fs.existsSync(path.join(goroot, 'bin', 'go'))) {
      this.log(`copying the Go toolchain of ${this.cfg.images.go} to ${goroot}`);
      const name = `${this.P}-goroot`;
      docker(['rm', '-f', name]);
      if (docker(['create', '--name', name, this.cfg.images.go]).status !== 0) throw new Error(`docker create ${this.cfg.images.go} failed`);
      fs.mkdirSync(path.dirname(goroot), { recursive: true });
      const r = docker(['cp', `${name}:/usr/local/go`, goroot]);
      docker(['rm', '-f', name]);
      if (r.status !== 0) throw new Error(`copying the Go toolchain failed: ${r.stderr.trim()}`);
    }
  }

  removeLeftovers() {
    const ids = docker(['ps', '-aq', '--filter', `label=filex-chain=${this.P}`]).stdout.split('\n').filter(Boolean);
    if (ids.length) {
      this.log(`removing ${ids.length} container(s) a previous ${this.P} run left`);
      docker(['rm', '-f', ...ids]);
    }
    docker(['network', 'rm', `${this.P}-ds`]);
  }

  label() {
    return ['--label', `filex-chain=${this.P}`, '--label', `filex-chain-run=${this.runId}`];
  }

  sidecar(name, args) {
    const r = docker(['run', '-d', '--rm', '--name', `${this.P}-${name}`, ...this.label(), ...args]);
    if (r.status !== 0) throw new Error(`starting ${this.P}-${name} failed: ${r.stderr.trim()}`);
  }

  async waitFor(what, probe, tries, everyMs) {
    for (let i = 0; i < tries && !this.stopping; i += 1) {
      if (probe()) return true;
      await sleep(everyMs);
    }
    this.log(`${what} did not answer in ${Math.round((tries * everyMs) / 1000)}s`);
    return false;
  }

  async servicesUp() {
    const net = `container:${this.P}-net`;
    this.sidecar('net', ['--init', this.cfg.images.go, 'sleep', 'infinity']);
    if (!this.needs().db) return;
    const pw = this.secret;
    this.sidecar('pg', [
      '--network', net, '--tmpfs', '/var/lib/postgresql/data:size=2g',
      '-e', 'POSTGRES_USER=filex', '-e', `POSTGRES_PASSWORD=${pw}`, this.cfg.images.pg,
      '-c', 'fsync=off', '-c', 'synchronous_commit=off', '-c', 'full_page_writes=off', '-c', 'max_connections=300',
    ]);
    this.sidecar('my', [
      '--network', net, '--tmpfs', '/var/lib/mysql:size=3g', '-e', `MYSQL_ROOT_PASSWORD=${pw}`, this.cfg.images.my,
      '--innodb-flush-log-at-trx-commit=0', '--sync-binlog=0', '--skip-log-bin', '--innodb-doublewrite=OFF', '--max-connections=500',
    ]);
    this.sidecar('redis', ['--network', net, this.cfg.images.redis]);
    this.sidecar('smb', [
      '--network', net, '--tmpfs', '/share:mode=1777',
      '-e', 'USER=filex;filexpass', '-e', 'SHARE=data;/share;;no;no;filex;;;', this.cfg.images.smb,
    ]);
    const pg = await this.waitFor('PostgreSQL', () => docker(['exec', `${this.P}-pg`, 'pg_isready', '-U', 'filex']).status === 0, 60, 2000);
    const my = await this.waitFor(
      'MySQL',
      () => docker(['exec', '-e', `MYSQL_PWD=${pw}`, `${this.P}-my`, 'mysql', '-uroot', '-e', 'select 1']).status === 0,
      90,
      2000,
    );
    if (!pg || !my) throw new Error('the database sidecars did not start');
    this.dbUp = true;
    this.log('sidecars up: net, PostgreSQL, MySQL, Redis, SMB');
  }

  dbDown() {
    docker(['rm', '-f', `${this.P}-pg`, `${this.P}-my`, `${this.P}-redis`, `${this.P}-smb`]);
    this.dbUp = false;
    this.log('database sidecars removed (no Go or migration job left)');
  }

  dsAddr() {
    const base = this.cfg.dsSubnet.replace(/\.\d+\/\d+$/, '');
    return { filex: `${base}.2`, ds: `${base}.10` };
  }

  async dsStart() {
    const { filex, ds } = this.dsAddr();
    const netName = `${this.P}-ds`;
    if (docker(['network', 'create', '--subnet', this.cfg.dsSubnet, ...this.label(), netName]).status !== 0) {
      throw new Error(`docker network create ${netName} (${this.cfg.dsSubnet}) failed`);
    }
    docker(['network', 'connect', '--ip', filex, '--alias', 'filex', netName, `${this.P}-net`]);
    const secret = fs.readFileSync(path.join(this.dir, 'etc', 'ds.secret'), 'utf8').trim();
    this.sidecar('ds', [
      '--network', netName, '--ip', ds, '--memory', '3g',
      '-e', 'JWT_ENABLED=true', '-e', `JWT_SECRET=${secret}`, '-e', 'JWT_HEADER=Authorization',
      '-e', 'ALLOW_PRIVATE_IP_ADDRESS=true', '-e', 'ALLOW_META_IP_ADDRESS=true', this.cfg.images.ds,
    ]);
    const ok = await this.waitFor(
      'the Document Server',
      () => docker(['exec', `${this.P}-ds`, 'curl', '-s', '-m', '3', 'http://localhost/healthcheck']).stdout.trim() === 'true',
      120,
      3000,
    );
    const seen = docker(['exec', `${this.P}-net`, 'curl', '-s', '-m', '5', `http://${ds}/healthcheck`]).stdout.trim();
    this.dsUp = true;
    this.log(`Document Server ${ok && seen === 'true' ? 'up' : 'NOT healthy'} at ${ds} (filex is ${filex} there)`);
    return ok && seen === 'true';
  }

  dsStop() {
    docker(['rm', '-f', `${this.P}-ds`]);
    docker(['network', 'disconnect', '-f', `${this.P}-ds`, `${this.P}-net`]);
    docker(['network', 'rm', `${this.P}-ds`]);
    this.dsUp = false;
    this.log('Document Server removed');
  }

  async vgwStart() {
    fs.mkdirSync(path.join(this.dir, 'vgw-data', 'filex-e2e-multi'), { recursive: true });
    this.sidecar('vgw', [
      '--network', `container:${this.P}-net`, '-v', `${path.join(this.dir, 'vgw-data')}:/data`,
      '-e', 'ROOT_ACCESS_KEY=filexe2e', '-e', 'ROOT_SECRET_KEY=filexe2esecret',
      '-e', 'VGW_BACKEND=posix', '-e', 'VGW_BACKEND_ARG=/data', '-e', 'VGW_HEALTH=/health', this.cfg.images.s3,
    ]);
    this.vgwUp = true;
    return this.waitFor(
      'the S3 gateway',
      () => docker(['exec', `${this.P}-net`, 'curl', '-s', '-o', '/dev/null', '-w', '%{http_code}', 'http://127.0.0.1:7070/health']).stdout.trim() === '200',
      30,
      2000,
    );
  }

  servicesDown() {
    if (this.args.keepServices) {
      this.log('sidecars left running (--keep-services)');
      return;
    }
    const ids = docker(['ps', '-aq', '--filter', `label=filex-chain-run=${this.runId}`]).stdout.split('\n').filter(Boolean);
    if (ids.length) docker(['rm', '-f', ...ids]);
    docker(['network', 'rm', `${this.P}-ds`]);
    for (const secret of ['ds.secret', 's3-live.env']) {
      try {
        fs.rmSync(path.join(this.dir, 'etc', secret), { force: true });
      } catch {
        /* the run's directory is the user's to tidy */
      }
    }
  }

  // ── a job ──

  containerArgs(job) {
    const k = KIND[job.kind];
    const c = this.cfg;
    const user = k.user ? ['--user', `${c.goUser.uid}:${c.goUser.gid}`,
      '-v', `${path.join(this.dir, 'etc', 'passwd')}:/etc/passwd:ro`, '-v', `${path.join(this.dir, 'etc', 'group')}:/etc/group:ro`] : [];
    const mounts = [
      '-v', `${c.src}:/w/src`, '-v', `${this.dir}:/w/run`, '-v', `${CHAIN_DIR}:/w/chain:ro`,
      '-v', `${c.cache.gomod}:/w/cache/gomod`, '-v', `${k.user ? c.cache.gocache : c.cache.gocacheRoot}:/w/cache/gocache`,
      '-v', `${c.cache.pnpm}:/w/cache/pnpm-store`, '-v', `${c.cache.cypress}:/w/cache/cypress`,
      '-v', `${c.cache.electron}:/w/cache/electron`, '-v', `${c.cache.home}:/w/home`,
    ];
    if (k.goroot) mounts.push('-v', `${c.cache.goroot}:/usr/local/go:ro`);
    if (k.browser || k.apps) {
      for (const [name, dir] of Object.entries(c.apps)) if (dir) mounts.push('-v', `${dir}:/w/apps/${name}:ro`);
    }
    const env = {
      JOB: job.name,
      PATH: '/usr/local/go/bin:/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin',
      GOMODCACHE: '/w/cache/gomod', GOCACHE: '/w/cache/gocache', GOFLAGS: '-buildvcs=false', GOTOOLCHAIN: 'local',
      npm_config_store_dir: '/w/cache/pnpm-store', CYPRESS_CACHE_FOLDER: '/w/cache/cypress', electron_config_cache: '/w/cache/electron',
      NODE_OPTIONS: '--max-old-space-size=6144', COREPACK_ENABLE_DOWNLOAD_PROMPT: '0', COREPACK_HOME: '/w/home/corepack',
      HOME: k.user ? '/w/run/home-go' : '/w/home',
      // The build made the echo app fixture: a Go test that cannot find it is
      // red, not skipped.
      ...(k.user ? { FILEX_REQUIRE_WASM_FIXTURE: '1' } : {}),
      ...(k.env || {}),
      ...job.env,
    };
    if (job.kind === 'go') {
      Object.assign(env, {
        GO_P: String(c.goP),
        FILEX_TEST_PG_DSN: `postgres://filex:${this.secret}@127.0.0.1:5432/postgres?sslmode=disable`,
        FILEX_TEST_MYSQL_DSN: `root:${this.secret}@tcp(127.0.0.1:3306)/mysql?parseTime=true&loc=UTC&charset=utf8mb4`,
        FILEX_TEST_REDIS_URL: 'redis://127.0.0.1:6379/0',
        FILEX_SMB_TEST: '127.0.0.1:445',
      });
    }
    if (job.kind === 'migrate') {
      Object.assign(env, {
        MIG_DOWN: String(c.migDown),
        MIG_ENGINES: this.dbUp ? 'sqlite postgres mysql' : 'sqlite',
        MIG_DSN_sqlite: 'file:/w/run/tmp/migrate.db',
        MIG_DSN_postgres: `postgres://filex:${this.secret}@127.0.0.1:5432/chainmig?sslmode=disable`,
        MIG_DSN_mysql: `root:${this.secret}@tcp(127.0.0.1:3306)/chainmig?parseTime=true&loc=UTC&charset=utf8mb4`,
      });
    }
    if (k.browser) {
      const { filex, ds } = this.dsAddr();
      Object.assign(env, {
        HOME: '/root',
        XDG_CONFIG_HOME: '/w/run/fc',
        KIND: job.kind,
        ENGINES: job.engines.join(','),
        DS: job.ds ? '1' : '0',
        ARGS: job.args.join(' '),
        // One port per line of the list: a part never waits on another's.
        PORT: String(c.e2ePort + (job.index ?? 0)),
        DS_URL: `http://${ds}`,
        DS_FILEX_IP: filex,
        DS_SECRET_FILE: '/w/run/etc/ds.secret',
        REQUIRE_APPS: c.requireApps ? '1' : '0',
      });
    }
    if (k.dsEnv) {
      // The Document Server calls the job back at http://filex:PORT: the job
      // shares the network namespace run.mjs connected to its network.
      const { ds } = this.dsAddr();
      Object.assign(env, {
        DS_URL: `http://${ds}`,
        DS_SECRET_FILE: '/w/run/etc/ds.secret',
        PORT: String(c.e2ePort + (job.index ?? 0)),
        REQUIRE_APPS: c.requireApps ? '1' : '0',
      });
    }
    const envArgs = Object.entries(env).flatMap(([key, v]) => ['-e', `${key}=${v}`]);
    return [
      'run', '--rm', '--name', `${this.P}-${job.name}`, ...this.label(),
      '--network', `container:${this.P}-net`, '--memory', k.mem, '--cpus', String(c.cpus),
      '--cpu-shares', String(k.shares ?? 1024), '--shm-size', '2g', '--init',
      ...user, ...mounts, ...envArgs, '-w', '/w/src', c.images[k.image], 'bash', `/w/chain/job/${k.script}`,
    ];
  }

  beforeJob(job) {
    if (job.kind !== 'migrate' || !this.dbUp) return;
    const pw = ['-e', `PGPASSWORD=${this.secret}`];
    docker(['exec', ...pw, `${this.P}-pg`, 'psql', '-U', 'filex', '-d', 'postgres', '-c', 'DROP DATABASE IF EXISTS chainmig']);
    docker(['exec', ...pw, `${this.P}-pg`, 'psql', '-U', 'filex', '-d', 'postgres', '-c', 'CREATE DATABASE chainmig']);
    docker(['exec', '-e', `MYSQL_PWD=${this.secret}`, `${this.P}-my`, 'mysql', '-uroot', '-e',
      'DROP DATABASE IF EXISTS chainmig; CREATE DATABASE chainmig CHARACTER SET utf8mb4']);
  }

  async runJob(job, lane) {
    this.status[job.name] = 'running';
    const logFile = path.join(this.dir, 'logs', `${job.name}.log`);
    const k = KIND[job.kind];
    const image = k.host ? 'host' : this.cfg.images[k.image];
    const start = Date.now();
    this.beforeJob(job);
    fs.writeFileSync(logFile, `JOBSTART ${iso()} ${job.name} ${image} ${k.script}\n`);
    this.log(`JOBSTART ${job.name} lane=${lane} weight=${job.weight}GiB image=${image}`);
    const rc = await new Promise((resolve) => {
      const fd = fs.openSync(logFile, 'a');
      let settled = false;
      const finish = (code) => {
        if (settled) return;
        settled = true;
        fs.closeSync(fd);
        resolve(code);
      };
      // A host job gets a process group of its own: stop() ends the job and
      // every process it started (detached), not only the bash in front.
      const child = k.host
        ? spawn('bash', [path.join(CHAIN_DIR, 'job', k.script)], {
            cwd: this.cfg.src,
            env: hostJobEnv(process.env, { job, dir: this.dir, src: this.cfg.src, prefix: this.P, images: this.cfg.images, realenv: this.cfg.realenv }),
            stdio: ['ignore', fd, fd],
            detached: true,
          })
        : spawn('docker', this.containerArgs(job), { stdio: ['ignore', fd, fd] });
      this.running.set(job.name, { job, child, host: !!k.host });
      child.on('error', (e) => {
        fs.appendFileSync(logFile, `could not start docker: ${e.message}\n`);
        finish(127);
      });
      child.on('exit', (code) => finish(code ?? 128));
    });
    this.running.delete(job.name);
    fs.appendFileSync(logFile, `JOBEXIT=${rc} ${iso()}\n`);
    const secs = Math.round((Date.now() - start) / 1000);
    const summary = this.stopping ? 'stopped' : summaryOf(logFile);
    this.status[job.name] = rc === 0 && !this.stopping ? 'passed' : 'failed';
    this.records[job.name] = {
      name: job.name, lane, kind: job.kind, weight_gb: job.weight, status: this.status[job.name], exit: rc,
      start: new Date(start).toISOString(), end: iso(), secs, log: logFile, out: path.join(this.dir, 'out', job.name), summary,
    };
    this.log(`JOBEXIT ${job.name} rc=${rc} ${fmtSecs(secs)} ${summary}`);
    this.writeResult(false);
    return rc;
  }

  /** runJob, with a failure of the chain's own (not the job's) recorded as a red job instead of ending the run. */
  startJob(job, lane) {
    return this.runJob(job, lane).catch((e) => {
      this.running.delete(job.name);
      this.status[job.name] = 'failed';
      this.records[job.name] = { name: job.name, lane, kind: job.kind, weight_gb: job.weight, status: 'failed', exit: null, summary: `chain error: ${e.message}` };
      this.log(`JOBEXIT ${job.name} chain error: ${e.message}`);
      return 1;
    });
  }

  /** Every 5 s: the lowest MemAvailable seen; every 5 min, a MEM line in chain.log. */
  sample() {
    const avail = memAvailableGb();
    this.minAvailGb = Math.min(this.minAvailGb, avail);
    const now = Date.now();
    if (now - (this.lastMemLog ?? 0) < 300_000) return;
    this.lastMemLog = now;
    const names = [...this.running.keys()].join(',') || '-';
    this.log(`MEM available=${avail.toFixed(1)}GiB lowest=${this.minAvailGb.toFixed(1)}GiB running=${names}`);
  }

  skip(job, why) {
    this.status[job.name] = 'skipped';
    this.records[job.name] = { name: job.name, lane: job.lane, kind: job.kind, weight_gb: job.weight, status: 'skipped', exit: null, summary: why };
    this.log(`SKIP ${job.name}: ${why}`);
  }

  // ── the browser round ──

  async cool(job) {
    const { prom, query, max, waitMin } = this.cfg.temp;
    if (!prom || !query) return;
    const read = async () => {
      try {
        const r = await fetch(`${prom.replace(/\/$/, '')}/api/v1/query?query=${encodeURIComponent(query)}`, { signal: AbortSignal.timeout(10_000) });
        const v = (await r.json())?.data?.result?.[0]?.value?.[1];
        return v === undefined ? NaN : Number(v);
      } catch {
        return NaN;
      }
    };
    const t0 = Date.now();
    let t = await read();
    while (Number.isFinite(t) && t > max && Date.now() - t0 < waitMin * 60_000 && !this.stopping) {
      await sleep(30_000);
      t = await read();
    }
    this.log(`temperature before ${job.name}: ${Number.isFinite(t) ? `${Math.round(t)} C` : 'unknown'} after ${Math.round((Date.now() - t0) / 1000)}s`);
  }

  async trackMemory(job) {
    const need = job.weight + this.cfg.reserveGb;
    const deadline = Date.now() + this.cfg.memWaitMin * 60_000;
    if (memAvailableGb() >= need) return;
    this.trackWaiting = true;
    this.log(`${job.name} waits for memory: MemAvailable ${memAvailableGb().toFixed(1)} GiB, needs ${need} GiB (the pool starts nothing meanwhile)`);
    while (memAvailableGb() < need && Date.now() < deadline && !this.stopping) await sleep(5000);
    this.trackWaiting = false;
    this.log(`${job.name}: MemAvailable ${memAvailableGb().toFixed(1)} GiB, starting`);
  }

  async runTrack() {
    while (['pending', 'running'].includes(this.status.build) && !this.stopping) await sleep(1000);
    const track = this.plan.track;
    if (this.status.build !== 'passed') {
      for (const t of track) this.skip({ ...t, lane: 'e2e' }, 'the build is not green');
      this.trackDone = true;
      return;
    }
    for (let i = 0; i < track.length && !this.stopping; i += 1) {
      const job = track[i];
      const later = track.slice(i + 1);
      try {
        if (job.ds && !this.dsUp && !(await this.dsStart())) {
          this.skip({ ...job, lane: 'e2e' }, 'the Document Server did not become healthy');
          continue;
        }
        if (!job.ds && this.dsUp && !later.some((t) => t.ds)) this.dsStop();
        if (job.kind === 's3' && !this.vgwUp && !(await this.vgwStart())) {
          this.skip({ ...job, lane: 'e2e' }, 'the S3 gateway did not answer');
          continue;
        }
      } catch (e) {
        this.skip({ ...job, lane: 'e2e' }, `chain error: ${e.message}`);
        continue;
      }
      if (!['cypress', 'ds-go', 's3-live'].includes(job.kind)) await this.cool(job);
      await this.trackMemory(job);
      await this.startJob(job, 'e2e');
      if (this.vgwUp && !later.some((t) => t.kind === 's3')) {
        docker(['rm', '-f', `${this.P}-vgw`]);
        this.vgwUp = false;
      }
    }
    if (this.dsUp) this.dsStop();
    this.trackDone = true;
  }

  // ── the pool ──

  async runPool() {
    const pool = this.plan.pool;
    const dbJobs = pool.filter((j) => j.db).map((j) => j.name);
    const settled = (n) => ['passed', 'failed', 'skipped'].includes(this.status[n]);
    let held = '';
    while (!this.stopping) {
      const { ready, skip } = readyJobs(pool, this.status);
      for (const job of skip) this.skip({ ...job, lane: 'pool' }, `needs ${job.needs.filter((n) => this.status[n] !== 'passed').join(', ')}`);
      if (this.dbUp && dbJobs.every(settled)) this.dbDown();
      const trackActive = this.status.build === 'passed' && !this.trackDone;
      const budget = poolBudget({ memGb: this.budget.memGb, trackActive, trackGb: this.budget.trackGb, dbUp: this.dbUp, dbGb: this.budget.dbGb });
      const runningPool = [...this.running.values()].map((r) => r.job).filter((j) => pool.includes(j));
      let avail = memAvailableGb();
      const holding = runningPool.reduce((s, j) => s + j.weight, 0) + (trackActive ? this.budget.trackGb : 0) + (this.dbUp ? this.budget.dbGb : 0);
      this.peakHeldGb = Math.max(this.peakHeldGb, holding);
      if (!this.trackWaiting) {
        for (const job of pickJobs({ ready, running: runningPool, budget, maxJobs: this.cfg.poolMax })) {
          if ((runningPool.length > 0 || this.running.size > 0) && avail - job.weight < this.cfg.reserveGb) {
            const why = `${job.name} held: MemAvailable ${avail.toFixed(1)} GiB, weight ${job.weight} + reserve ${this.cfg.reserveGb}`;
            if (held !== job.name) this.log(why);
            held = job.name;
            break;
          }
          held = '';
          avail -= job.weight;
          runningPool.push(job);
          this.startJob(job, 'pool');
        }
      }
      if (pool.every((j) => settled(j.name))) break;
      await sleep(2000);
    }
    if (this.dbUp) this.dbDown();
  }

  // ── the result ──

  result(final) {
    const jobs = [...this.plan.pool, ...this.plan.track].map(
      (j) => this.records[j.name] ?? { name: j.name, status: this.status[j.name], exit: null },
    );
    const secs = Math.round((Date.now() - this.t0) / 1000);
    return {
      schema: 1,
      run_id: this.runId,
      profile: this.plan.profile,
      src: this.cfg.src,
      sha: this.sha,
      subject: this.subject,
      dirty_files: this.dirty,
      started: this.started,
      finished: final ? iso() : null,
      secs,
      wall: fmtSecs(secs),
      ok: final ? jobs.every((j) => j.status === 'passed') && !this.stopping : null,
      stopped: this.stopping,
      budget: {
        mem_gb: this.budget.memGb, browser_round_gb: this.budget.trackGb, databases_gb: this.budget.dbGb,
        pool_max: this.budget.poolMax, reserve_gb: this.cfg.reserveGb,
        mem_total_gb: round2(os.totalmem() / 2 ** 30), min_mem_available_gb: round2(this.minAvailGb), peak_held_gb: round2(this.peakHeldGb),
      },
      counts: {
        passed: jobs.filter((j) => j.status === 'passed').length,
        failed: jobs.filter((j) => j.status === 'failed').length,
        skipped: jobs.filter((j) => j.status === 'skipped').length,
        total: jobs.length,
      },
      jobs,
      log: path.join(this.dir, 'chain.log'),
      env_file: this.envFile || null,
    };
  }

  writeResult(final) {
    const r = this.result(final);
    const file = path.join(this.dir, 'result.json');
    fs.writeFileSync(`${file}.tmp`, `${JSON.stringify(r, null, 2)}\n`);
    fs.renameSync(`${file}.tmp`, file);
    if (final) fs.copyFileSync(file, path.join(this.cfg.root, 'runs', `latest-${this.plan.profile}.json`));
    return r;
  }

  async notify(r) {
    const { url, keyFile, group, source } = this.cfg.notify;
    if (!url) return;
    const bad = r.jobs.filter((j) => j.status !== 'passed');
    const lines = [
      `${r.counts.passed}/${r.counts.total} jobs green on ${r.sha.slice(0, 8)} ${r.subject}`,
      ...bad.slice(0, 25).map((j) => `${j.status === 'skipped' ? 'skipped' : 'RED'} ${j.name}: ${j.summary ?? ''}`),
      `result: ${path.join(this.dir, 'result.json')}`,
    ];
    // The one poster every maintainer tool shares (scripts/lib/notify.mjs).
    await postNotify(
      { url, keyFile, group, source },
      {
        severity: r.ok ? 'success' : 'danger',
        title: `filex chain ${r.profile}: ${r.ok ? 'green' : r.stopped ? 'stopped' : 'RED'} in ${r.wall} (${r.sha.slice(0, 8)})`,
        message: lines.join('\n'),
      },
      { log: (m) => this.log(m), keyVar: 'CHAIN_NOTIFY_KEY_FILE' },
    );
  }

  stop(signal) {
    if (this.stopping) return;
    this.stopping = true;
    this.log(`stopping on ${signal}: removing this run's containers`);
    const ids = docker(['ps', '-aq', '--filter', `label=filex-chain-run=${this.runId}`]).stdout.split('\n').filter(Boolean);
    if (ids.length) docker(['rm', '-f', ...ids]);
    // A host job's containers are its own (realenv removes them on TERM).
    for (const { job, child, host } of this.running.values()) {
      if (!host || !child.pid) continue;
      this.log(`stopping ${job.name} (process group ${child.pid})`);
      try {
        process.kill(-child.pid, 'SIGTERM');
      } catch {
        /* already gone */
      }
    }
  }

  async run() {
    fs.mkdirSync(this.dir, { recursive: true });
    this.prepare();
    this.log(`chain ${this.runId} start: profile=${this.plan.profile} src=${this.cfg.src} ${this.sha.slice(0, 8)} ${this.subject}${this.dirty ? ` (+${this.dirty} uncommitted)` : ''}`);
    this.log(
      `budget ${this.budget.memGb} GiB: browser round ${this.budget.trackGb}, databases ${this.budget.dbGb}, pool max ${this.budget.poolMax}; ` +
        `MemAvailable now ${memAvailableGb().toFixed(1)} of ${(os.totalmem() / 2 ** 30).toFixed(1)} GiB; lock ${process.env.CHAIN_LOCK_HELD || 'none'}`,
    );
    // Not SIGHUP: started under nohup the run must outlive the session that started it.
    for (const sig of ['SIGINT', 'SIGTERM']) process.on(sig, () => this.stop(sig));
    try {
      this.removeLeftovers();
      this.ensureImages();
      await this.servicesUp();
    } catch (e) {
      this.log(`setup failed: ${e.message}`);
      this.servicesDown();
      this.writeResult(true);
      return 2;
    }
    const sampler = setInterval(() => this.sample(), 5000);
    await Promise.all([this.runPool(), this.runTrack()]);
    while (this.running.size) await sleep(1000);
    clearInterval(sampler);
    this.servicesDown();
    const r = this.writeResult(true);
    this.log(`chain ${this.runId} done in ${r.wall}: ${r.counts.passed} passed, ${r.counts.failed} red, ${r.counts.skipped} skipped -> ${path.join(this.dir, 'result.json')}`);
    await this.notify(r);
    if (this.stopping) return 130;
    return r.ok ? 0 : 1;
  }
}

function summaryOf(logFile) {
  const text = readOr(logFile).slice(-65536);
  const lines = text.split('\n').filter((l) => l.startsWith('SUMMARY '));
  return lines.length ? lines.at(-1).slice(8).trim() : '(no SUMMARY line)';
}

function fmtSecs(s) {
  return `${Math.floor(s / 3600)}h${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}m${String(s % 60).padStart(2, '0')}s`;
}

const round2 = (n) => (Number.isFinite(n) ? Math.round(n * 100) / 100 : null);

// ── main ────────────────────────────────────────────────────────────────────

async function main() {
  let args;
  try {
    args = parseArgs(process.argv.slice(2));
  } catch (e) {
    console.error(`${e.message}\n\n${USAGE}`);
    return 2;
  }
  if (args.help) {
    console.log(USAGE);
    return 0;
  }
  const { env, file } = environment(args);
  const cfg = settings(env, args);
  if (args.printLock) {
    console.log(cfg.lock);
    return 0;
  }
  const plan = buildPlan({ profile: args.profile, lists: loadLists(env, { cwd: cfg.src }), env, src: cfg.src });
  if (args.plan) {
    printPlan(cfg, plan);
    return 0;
  }
  if (process.platform !== 'linux') {
    console.error('the chain runs on Linux with Docker (--plan works anywhere)');
    return 2;
  }
  if (!process.env.CHAIN_LOCK_HELD && cfg.lock !== 'none') {
    console.error(`start the chain with scripts/chain/run.sh: it takes the lock (${cfg.lock}) first`);
    return 2;
  }
  if (docker(['version', '--format', '{{.Server.Version}}']).status !== 0) {
    console.error('docker does not answer');
    return 2;
  }
  return new Chain(cfg, plan, args, file).run();
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then(
    (code) => process.exit(code),
    (e) => {
      console.error(e.stack || e.message);
      process.exit(2);
    },
  );
}
