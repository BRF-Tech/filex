#!/usr/bin/env node
// The nightly run of the test chain (task #175): every night, the whole chain
// and its nightly extras on the tip of main, in a checkout and a directory of
// its own, under the build host's shared lock - so a release day starts from
// a night that already ran everything, and only what changed since is left.
//
//   node scripts/chain/nightly.mjs run     --env FILE [--force] [--dry-run]
//   node scripts/chain/nightly.mjs build   --env FILE [--force]   the nightly build of the last green night, now
//   node scripts/chain/nightly.mjs publish --env FILE [--force]   push the last nightly build (NIGHTLY_PUSH_REPO)
//   node scripts/chain/nightly.mjs status  --env FILE             tonight and the last nights
//   node scripts/chain/nightly.mjs s3-live --env FILE --s3-env CREDS
//                                  the live S3 tests on main now, with these credentials
//
// `run` (its timer: scripts/chain/systemd/filex-nightly.timer, 01:00):
//   1. fetches main into NIGHTLY_SRC (default <CHAIN_ROOT>/src);
//   2. stops there when main has not moved since the last night that ran,
//      when it is inside a NIGHTLY_QUIET window, or when the run would not be
//      over before the next one (scripts/chain/nightly-lib.mjs decideRun,
//      startWindow) - the weekly service-update round on the same host is
//      such a window;
//   3. checks main out (a clean tree: `git clean -x`, node_modules kept) and
//      runs scripts/chain/run.sh --profile nightly from it: the full chain
//      plus ds-go, shots, every NIGHTLY_REALENV_EVERY_DAYS days realenv, and
//      s3-live when the S3 code changed since its last green run (decideS3Live:
//      without credentials then, a warning in the morning report instead).
//      run.sh takes CHAIN_LOCK - the lock every build on the host takes - and
//      waits for it at most until the run could no longer end in time. Its
//      own end-of-run notification is off: the morning report is the one;
//   4. when the run is green and main moved since the last nightly build,
//      builds the image of that commit from its PUBLIC form
//      (scripts/chain/nightly-build.sh: scripts/export-public.sh's tree, never
//      the private one), tags it locally and, with NIGHTLY_PUBLISH=auto - the
//      build host's setting (nightly.env.example) - pushes it right away, the
//      first one too. `manual` (the code's default, for a checkout that names
//      no registry) leaves `publish` to a person; a push that fails is in the
//      morning report, and `publish` pushes that build again;
//   5. keeps the night's record and prunes old runs and images.
//
// What it leaves under <CHAIN_ROOT>/nightly/: tonight.json (the night so far,
// rewritten at each step), history.jsonl (one line per night), state.json
// (the last nightly build, the last realenv night, the commit the S3 code is
// measured from), nightly.lock (this
// process, while it runs). The chain's own runs/<run-id>/ and
// runs/latest-nightly.json are under <CHAIN_ROOT>/runs/, as for any run.
//
// Exit: 0 the night was recorded green or did not need to run, 1 it was
// recorded red, 2 it could not run (see tonight.json).
//
// ⚠ It runs from an installed copy (scripts/chain/install-nightly.sh copies
// this file, nightly-lib.mjs, report.mjs and env.mjs), never from the
// checkout it resets: everything it runs from the tree (run.sh, the build)
// is tonight's.

import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { mergeEnv, parseEnvFile } from './env.mjs';
import {
  S3_LIVE_KEYS,
  decideBuild,
  decideRun,
  decideS3Live,
  expectedRunMin,
  extrasFor,
  imagesToPrune,
  jobStatuses,
  lastRan,
  localDate,
  nightRecord,
  nightlyRunId,
  nightlySettings,
  nightlyTags,
  nightlyVersion,
  nextS3LiveState,
  registryLogin,
  resultFields,
  runsToPrune,
  s3LiveFile,
  s3Touched,
  startWindow,
} from './nightly-lib.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));

const USAGE = `usage: node scripts/chain/nightly.mjs <run|build|publish|status|s3-live> [options]

  run       tonight's nightly run (what its timer starts)
  build     the nightly build of the last green night, now (--force: even
            when that commit was built already)
  publish   push the last nightly build to NIGHTLY_PUSH_REPO under
            :nightly and :nightly-<date> (--force: again)
  status    tonight and the last nights
  s3-live   the live S3 tests on the tip of main, now, with the credentials
            in --s3-env (the build and its fast gates, then s3-live alone);
            green, the nightly run measures the S3 code from that commit

  --env FILE     settings, KEY=VALUE (default: $NIGHTLY_ENV); the process
                 environment wins over the file. The keys, with their
                 defaults: scripts/chain/nightly.env.example
  --s3-env FILE  s3-live: FILEX_TEST_S3_* lines (BUCKET, ACCESS_KEY,
                 SECRET_KEY, REGION, and ENDPOINT); never logged
  --force        run: even when main has not moved
  --dry-run      run: fetch and decide, print what it would do, change nothing

Exit: 0 green or nothing to do, 1 red, 2 could not run.`;

export function parseNightlyArgs(argv) {
  const args = { cmd: 'run' };
  let i = 0;
  if (argv[0] && !argv[0].startsWith('-')) {
    args.cmd = argv[0];
    i = 1;
  }
  if (!['run', 'build', 'publish', 'status', 's3-live'].includes(args.cmd)) throw new Error(`unknown command ${args.cmd}`);
  for (; i < argv.length; i += 1) {
    const a = argv[i];
    if (a === '--env') {
      args.env = argv[i + 1];
      if (!args.env || args.env.startsWith('--')) throw new Error('--env needs a file');
      i += 1;
    } else if (a === '--s3-env') {
      args.s3Env = argv[i + 1];
      if (!args.s3Env || args.s3Env.startsWith('--')) throw new Error('--s3-env needs a file');
      i += 1;
    } else if (a === '--force') args.force = true;
    else if (a === '--dry-run') args.dryRun = true;
    else if (a === '--help' || a === '-h') args.help = true;
    else throw new Error(`unknown option ${a}`);
  }
  return args;
}

// ── small things ────────────────────────────────────────────────────────────

const iso = (ms = Date.now()) => new Date(ms).toISOString().replace(/\.\d{3}Z$/, 'Z');
const short = (sha) => String(sha ?? '').slice(0, 8);
const log = (line) => console.log(`${iso()} nightly: ${line}`);

export function readJson(file) {
  try {
    return JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch {
    return null;
  }
}

function writeJson(file, value) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(`${file}.tmp`, `${JSON.stringify(value, null, 2)}\n`);
  fs.renameSync(`${file}.tmp`, file);
}

export function readHistory(stateDir) {
  let text = '';
  try {
    text = fs.readFileSync(path.join(stateDir, 'history.jsonl'), 'utf8');
  } catch {
    return [];
  }
  const out = [];
  for (const line of text.split('\n')) {
    if (!line.trim()) continue;
    try {
      out.push(JSON.parse(line));
    } catch {
      /* a line a crash cut short: the others still count */
    }
  }
  return out;
}

function appendHistory(stateDir, record, keep) {
  const all = [...readHistory(stateDir), record].slice(-Math.max(1, keep));
  const file = path.join(stateDir, 'history.jsonl');
  fs.writeFileSync(`${file}.tmp`, `${all.map((r) => JSON.stringify(r)).join('\n')}\n`);
  fs.renameSync(`${file}.tmp`, file);
}

function git(cwd, args) {
  return spawnSync('git', ['-c', 'safe.directory=*', '-C', cwd, ...args], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
}

/**
 * The environment of what this process starts: its own, with the directory of
 * the node running it first on PATH - run.sh and the export call `node` by
 * name, and a systemd unit's PATH may not have the node it was given.
 */
function childEnv(extra = {}) {
  const dir = path.dirname(process.execPath);
  return { ...process.env, PATH: [dir, process.env.PATH].filter(Boolean).join(path.delimiter), ...extra };
}

const lastLine = (s) => String(s ?? '').trim().split('\n').filter(Boolean).at(-1) ?? '';

function tailOf(file, n = 3) {
  try {
    return fs.readFileSync(file, 'utf8').trim().split('\n').slice(-n).join(' | ');
  } catch {
    return '';
  }
}

export function alive(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (e) {
    return e.code === 'EPERM';
  }
}

/** This process's own lock: one nightly run (or build, or publish) at a time. A lock whose process is gone is taken over. */
function takeOwnLock(file) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  for (let attempt = 0; attempt < 2; attempt += 1) {
    try {
      const fd = fs.openSync(file, 'wx');
      fs.writeSync(fd, `${process.pid}\n`);
      fs.closeSync(fd);
      const release = () => {
        try {
          if (fs.readFileSync(file, 'utf8').trim() === String(process.pid)) fs.rmSync(file, { force: true });
        } catch {
          /* gone already */
        }
      };
      process.on('exit', release);
      return release;
    } catch (e) {
      if (e.code !== 'EEXIST') throw e;
      const pid = Number(String(readText(file)).trim());
      if (alive(pid)) throw new Error(`another nightly run (pid ${pid}) holds ${file}`);
      fs.rmSync(file, { force: true });
    }
  }
  throw new Error(`could not take ${file}`);
}

function readText(file) {
  try {
    return fs.readFileSync(file, 'utf8');
  } catch {
    return '';
  }
}

// ── run ─────────────────────────────────────────────────────────────────────

async function runNight(cfg, args, envFile) {
  const nowMs = Date.now();
  fs.mkdirSync(cfg.state, { recursive: true });
  const history = readHistory(cfg.state);
  const stateFile = path.join(cfg.state, 'state.json');
  const state = readJson(stateFile) ?? {};
  const tonightFile = path.join(cfg.state, 'tonight.json');
  const tonight = { night: localDate(nowMs, cfg.tz), at: iso(nowMs), pid: process.pid, phase: 'fetch', decision: null };
  const save = () => {
    if (!args.dryRun) writeJson(tonightFile, tonight);
  };
  const record = (fields) => {
    Object.assign(tonight, fields, { phase: 'done', ended: iso() });
    log(`${tonight.decision}: ${tonight.why ?? ''}`);
    if (args.dryRun) return;
    save();
    appendHistory(cfg.state, nightRecord(tonight), cfg.keepHistory);
  };
  save();
  log(`night ${tonight.night}: ${cfg.remote}/${cfg.branch} into ${cfg.src}`);

  // 1. main, as it is now.
  const main = fetchMain(cfg);
  if (main.error) {
    record({ decision: 'error', why: main.error });
    return 2;
  }
  const { head } = main;
  tonight.sha = head;
  tonight.subject = main.subject;

  // 2. Whether it runs at all.
  const last = lastRan(history);
  const d = decideRun({ head, last, force: !!args.force });
  if (!d.run) {
    // A green night whose build failed (or found the lock busy) is built on
    // the next night main has not moved.
    const lastResult = last?.result ? readJson(last.result) : null;
    tonight.build = await buildIfDue(cfg, { result: lastResult, state, stateFile, runDir: last?.run_id ? path.join(cfg.runs, last.run_id) : null, deadline: null, envFile, dry: args.dryRun, retry: true });
    record({ decision: 'skipped', why: d.why });
    return 0;
  }
  const expectMin = expectedRunMin(history, cfg.expectMin);
  const win = startWindow({ nowMs: Date.now(), windows: cfg.quiet, expectMin, marginMin: cfg.marginMin, lockWaitMin: cfg.lockWaitMin });
  if (!win.ok) {
    record({ decision: 'refused', why: win.why });
    return 2;
  }
  const lastRealenvMs = state.last_realenv_at ? Date.parse(state.last_realenv_at) : null;
  // s3-live only when the S3 code changed since its last green run.
  const s3 = s3LiveDecision(cfg, { state, last, head });
  tonight.s3live = s3.status === 'no-credentials' ? { ...s3, hint: s3LiveHint(envFile) } : s3;
  const extras = extrasFor({ asked: cfg.extras, everyDays: cfg.realenvEveryDays, lastRealenvMs, nowMs }).filter((x) => x !== 's3-live' || s3.run);
  if (args.dryRun) {
    log(`would run: ${d.why}; extras ${extras.join(',') || 'none'}; s3-live ${s3.status}: ${s3.why}; ~${expectMin} min; lock wait at most ${win.lockWaitMin} min${win.deadline ? `; stopped at ${iso(win.deadline)} if still going` : ''}`);
    return 0;
  }

  // 3. The checkout at main, clean.
  tonight.phase = 'checkout';
  save();
  const unclean = checkoutClean(cfg, head);
  if (unclean) {
    record({ decision: 'error', why: unclean });
    return 2;
  }

  // 4. The chain.
  const runId = nightlyRunId(Date.now(), head);
  const runDir = path.join(cfg.runs, runId);
  Object.assign(tonight, {
    phase: 'running',
    decision: 'ran',
    why: d.why,
    run_id: runId,
    result: path.join(runDir, 'result.json'),
    latest: path.join(cfg.runs, 'latest-nightly.json'),
    extras,
    prev_sha: last?.sha ?? null,
  });
  save();
  log(`${d.why}: run ${runId}, extras ${extras.join(',') || 'none'}`);
  const { code, stoppedFor } = await runChain(cfg, { runId, envFile, extras, win });
  if (code === 3) {
    record({ decision: 'not-run', why: `the build lock ${cfg.lock} was not free within ${win.lockWaitMin} min`, exit: 3 });
    return 2;
  }
  const result = readJson(tonight.result);
  Object.assign(tonight, { exit: code, ...(stoppedFor ? { stoppedFor } : {}), ...resultFields(result) });
  if (extras.includes('realenv') && ['passed', 'failed'].includes(jobStatuses(result).realenv)) {
    state.last_realenv_at = tonight.at;
    writeJson(stateFile, state);
  }
  // Only a green s3-live moves where the S3 code is measured from: a change
  // that was not tested is reported again tomorrow.
  if (s3.status !== 'not-asked') {
    const passed = extras.includes('s3-live') && jobStatuses(result)['s3-live'] === 'passed';
    state.s3_live = nextS3LiveState(state.s3_live, { head, base: s3.since, passed, at: tonight.at });
    writeJson(stateFile, state);
  }

  // 5. The nightly build.
  tonight.phase = 'build';
  save();
  tonight.build = await buildIfDue(cfg, { result, state, stateFile, runDir, deadline: win.deadline, envFile });
  record({});
  prune(cfg, runId, state);
  if (result?.ok) return 0;
  return code === 0 || code === 1 ? 1 : 2;
}

/**
 * The commit of main and its subject, fetched into the run's checkout, or
 * `{ error }`.
 */
function fetchMain(cfg) {
  if (!fs.existsSync(path.join(cfg.src, '.git'))) return { error: `no checkout at ${cfg.src} (scripts/chain/install-nightly.sh clones it)` };
  const fetched = git(cfg.src, ['fetch', '--quiet', '--prune', cfg.remote, `+refs/heads/${cfg.branch}:refs/remotes/${cfg.remote}/${cfg.branch}`]);
  if (fetched.status !== 0) return { error: `git fetch ${cfg.remote} ${cfg.branch} failed: ${lastLine(fetched.stderr)}` };
  const head = git(cfg.src, ['rev-parse', '--verify', `refs/remotes/${cfg.remote}/${cfg.branch}^{commit}`]).stdout.trim();
  if (!/^[0-9a-f]{40}$/.test(head)) return { error: `no ${cfg.remote}/${cfg.branch} after the fetch` };
  return { head, subject: git(cfg.src, ['log', '-1', '--format=%s', head]).stdout.trim() };
}

/** Checks `head` out clean (`git clean -x`, node_modules kept): '' when it is, else why not. */
function checkoutClean(cfg, head) {
  const co = git(cfg.src, ['checkout', '--quiet', '--force', '--detach', head]);
  const cl = co.status === 0 ? git(cfg.src, ['clean', '-ffdxq', '-e', 'node_modules']) : co;
  const dirty = git(cfg.src, ['status', '--porcelain', '--untracked-files=no']).stdout.trim();
  if (co.status === 0 && cl.status === 0 && !dirty) return '';
  return `could not check ${short(head)} out clean: ${lastLine(co.stderr || cl.stderr) || dirty.split('\n')[0]}`;
}

/**
 * Tonight's decideS3Live: the S3 code measured from where state.json says
 * (the last green s3-live run, else the night the measuring started), else
 * from the last night that ran.
 */
function s3LiveDecision(cfg, { state, last, head }) {
  const base = state.s3_live ? state.s3_live.sha : last?.sha ?? null;
  let touched = null;
  if (base && git(cfg.src, ['cat-file', '-e', `${base}^{commit}`]).status === 0) {
    const files = git(cfg.src, ['diff', '--name-only', `${base}..${head}`]);
    const goMod = git(cfg.src, ['diff', `${base}..${head}`, '--', 'backend/go.mod']);
    if (files.status === 0) touched = s3Touched(files.stdout.split('\n').filter(Boolean), goMod.stdout);
  }
  return decideS3Live({ asked: cfg.extras.includes('s3-live'), base, touched, missing: cfg.s3LiveMissing });
}

/** The command that tests the S3 code by hand, for the morning report. */
function s3LiveHint(envFile) {
  return `node ${path.join(HERE, 'nightly.mjs')} s3-live${envFile ? ` --env ${envFile}` : ''} --s3-env <a file with FILEX_TEST_S3_*>`;
}

/**
 * run.sh in its own process group: the deadline and a stop end all of it.
 * The nightly profile with `extras`, or `profile` with `env` on top (the
 * s3-live command: targeted, CHAIN_EXTRAS=s3-live and its credentials).
 */
function runChain(cfg, { runId, envFile, extras = [], win, profile = 'nightly', env = {} }) {
  return new Promise((resolve) => {
    const argv = [path.join(cfg.src, 'scripts', 'chain', 'run.sh'), '--profile', profile, '--src', cfg.src, '--run-id', runId];
    if (envFile) argv.push('--env', envFile);
    const child = spawn('bash', argv, {
      cwd: cfg.src,
      stdio: 'inherit',
      detached: true,
      env: childEnv({
        CHAIN_ROOT: cfg.root,
        CHAIN_PREFIX: cfg.prefix,
        CHAIN_LOCK: cfg.lock,
        CHAIN_NIGHTLY_EXTRAS: extras.length ? extras.join(',') : 'none',
        // The morning report is the night's one notification.
        CHAIN_NOTIFY_URL: '',
        CHAIN_LOCK_WAIT_MIN: String(Math.max(0, Math.floor(win.lockWaitMin))),
        ...env,
      }),
    });
    let stoppedFor = '';
    const stop = (why) => {
      if (stoppedFor) return;
      stoppedFor = why;
      log(`stopping the chain: ${why}`);
      try {
        process.kill(-child.pid, 'SIGTERM');
      } catch {
        /* gone already */
      }
    };
    const timer = win.deadline
      ? setTimeout(() => stop(`it had to be over by ${iso(win.deadline)} (NIGHTLY_QUIET)`), Math.max(0, win.deadline - Date.now()))
      : null;
    const onSignal = (sig) => stop(`${sig}: the nightly run itself was stopped`);
    process.on('SIGTERM', onSignal);
    process.on('SIGINT', onSignal);
    const done = (out) => {
      if (timer) clearTimeout(timer);
      process.off('SIGTERM', onSignal);
      process.off('SIGINT', onSignal);
      resolve(out);
    };
    child.on('error', (e) => done({ code: 2, stoppedFor: `could not start run.sh: ${e.message}` }));
    child.on('exit', (code) => done({ code: code ?? 130, stoppedFor }));
  });
}

// ── the nightly build ───────────────────────────────────────────────────────

/**
 * The nightly build, when decideBuild says one is due. `retry`: a night that
 * did not run looks again at the last green night (a build that failed or
 * found the lock busy); it says nothing when none is due, and keeps to the
 * quiet windows itself.
 */
async function buildIfDue(cfg, { result, state, stateFile, runDir, deadline, envFile, dry = false, retry = false, force = false }) {
  const d = decideBuild({ result, mode: cfg.build.mode, lastBuildSha: force ? '' : state.last_build_sha ?? '' });
  if (!d.build) return retry ? undefined : { decision: 'skipped', why: d.why };
  if (deadline && Date.now() + cfg.build.expectMin * 60_000 > deadline) {
    return { decision: 'skipped', why: `no time left before the quiet window (a build takes ~${cfg.build.expectMin} min); the next night that does not run builds it` };
  }
  if (retry) {
    const w = startWindow({ nowMs: Date.now(), windows: cfg.quiet, expectMin: cfg.build.expectMin, marginMin: cfg.marginMin, lockWaitMin: 30 });
    if (!w.ok) return { decision: 'skipped', why: `the build of ${short(result.sha)} waits: ${w.why}` };
    deadline = w.deadline;
  }
  if (dry) {
    log(`would build ${short(result.sha)}: ${d.why}`);
    return { decision: 'skipped', why: `dry run (${d.why})` };
  }
  log(`nightly build of ${short(result.sha)}: ${d.why}`);
  const rec = await buildImage(cfg, { sha: result.sha, runDir: runDir ?? path.join(cfg.state, 'builds', short(result.sha)), deadline });
  rec.why = d.why;
  if (rec.decision !== 'built') return rec;
  state.last_build_sha = result.sha;
  state.last_build = { image: rec.image, id: rec.id, version: rec.version, tag: rec.tag, ymd: rec.ymd, sha: rec.sha, at: rec.at };
  writeJson(stateFile, state);
  rec.publishMode = cfg.build.publish;
  if (cfg.build.publish === 'auto') {
    try {
      rec.published = publishImage(cfg, state.last_build);
      state.last_build.published = rec.published;
      writeJson(stateFile, state);
    } catch (e) {
      rec.publishError = e.message;
    }
  } else if (cfg.build.publish === 'manual') {
    rec.publishHint = `node ${path.join(HERE, 'nightly.mjs')} publish${envFile ? ` --env ${envFile}` : ''}`;
  }
  return rec;
}

/**
 * A command in a process group of its own, its output appended to `logFile`:
 * past `timeoutMs`, or when this process is stopped, the whole group gets
 * SIGTERM - the docker build under the flock under the bash, not only the
 * first of them.
 */
function runGroup(cmd, argv, { cwd, env, logFile, timeoutMs }) {
  return new Promise((resolve) => {
    const fd = fs.openSync(logFile, 'a');
    const child = spawn(cmd, argv, { cwd, env, stdio: ['ignore', fd, fd], detached: true });
    fs.closeSync(fd);
    let timedOut = false;
    const kill = () => {
      try {
        process.kill(-child.pid, 'SIGTERM');
      } catch {
        /* gone already */
      }
    };
    const timer = setTimeout(() => {
      timedOut = true;
      kill();
    }, timeoutMs);
    process.on('SIGTERM', kill);
    process.on('SIGINT', kill);
    let settled = false;
    const done = (status, signal) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      process.off('SIGTERM', kill);
      process.off('SIGINT', kill);
      resolve({ status, signal, timedOut });
    };
    child.on('error', () => done(127, null));
    child.on('exit', (code, signal) => done(code, signal));
  });
}

/** scripts/chain/nightly-build.sh from tonight's tree, under the build lock: the image, tagged <NIGHTLY_IMAGE>:<sha8>. */
async function buildImage(cfg, { sha, runDir, deadline }) {
  const out = path.join(runDir, 'nightly-build');
  fs.mkdirSync(out, { recursive: true });
  const logFile = path.join(out, 'build.log');
  const head = git(cfg.src, ['rev-parse', 'HEAD']).stdout.trim();
  if (head !== sha) return { decision: 'failed', why: `the checkout is at ${short(head)}, not at ${short(sha)}, the commit that ran green`, sha };
  const nowMs = Date.now();
  const ymd = localDate(nowMs, cfg.tz).replace(/-/g, '');
  let version;
  try {
    version = nightlyVersion(readJson(path.join(cfg.src, 'packages', 'core', 'package.json'))?.version, ymd, sha);
  } catch (e) {
    return { decision: 'failed', why: e.message, sha };
  }
  const tag = short(sha);
  const script = path.join(cfg.src, 'scripts', 'chain', 'nightly-build.sh');
  const budgetMs = deadline ? Math.max(60_000, deadline - nowMs) : 120 * 60_000;
  const waitSecs = Math.max(0, Math.floor(Math.min(30 * 60_000, budgetMs - cfg.build.expectMin * 60_000) / 1000));
  const [cmd, argv] = cfg.lock === 'none' ? ['bash', [script]] : ['flock', ['-E', '75', '-w', String(waitSecs), cfg.lock, 'bash', script]];
  const r = await runGroup(cmd, argv, {
    cwd: cfg.src,
    logFile,
    timeoutMs: budgetMs,
    env: childEnv({
      NB_SRC: cfg.src,
      NB_SHA: sha,
      NB_PUBLIC_DIR: cfg.build.publicDir,
      NB_PUBLIC_REMOTE: cfg.build.publicRemote,
      NB_OUT: out,
      NB_IMAGE: cfg.build.image,
      NB_TAG: tag,
      NB_VERSION: version,
      NB_DATE: iso(nowMs),
      NB_DOCKERFILE: cfg.build.dockerfile,
    }),
  });
  if (r.status === 75) return { decision: 'failed', why: `the build lock ${cfg.lock} was not free within ${Math.round(waitSecs / 60)} min`, sha, log: logFile };
  if (r.timedOut || r.status === null) {
    return { decision: 'failed', why: `stopped after ${Math.round((Date.now() - nowMs) / 60_000)} min (${r.timedOut ? 'out of time before the quiet window' : r.signal})`, sha, log: logFile };
  }
  if (r.status !== 0) return { decision: 'failed', why: `nightly-build.sh exited ${r.status}: ${tailOf(logFile, 2)}`, sha, log: logFile };
  const id = spawnSync('docker', ['image', 'inspect', '--format', '{{.Id}}', `${cfg.build.image}:${tag}`], { encoding: 'utf8' }).stdout.trim();
  return { decision: 'built', image: `${cfg.build.image}:${tag}`, id, version, tag, ymd, sha, at: iso(), log: logFile };
}

/**
 * Push a nightly build to NIGHTLY_PUSH_REPO as :nightly and :nightly-<date>.
 * With NIGHTLY_REGISTRY_USER and NIGHTLY_REGISTRY_TOKEN_FILE it logs in with a
 * Docker config of its own, removed afterwards: no credential stays in the
 * host's Docker config. Without either it uses the host's; with one of them
 * only it refuses (registryLogin).
 */
function publishImage(cfg, build) {
  const { pushRepo, registryUser, registryTokenFile } = cfg.build;
  if (!pushRepo) throw new Error('NIGHTLY_PUSH_REPO is not set: say where the nightly build goes');
  if (!build?.image) throw new Error('no nightly build to publish');
  const login = registryLogin({ registryUser, registryTokenFile });
  const refs = nightlyTags(build.ymd).map((t) => `${pushRepo}:${t}`);
  for (const ref of refs) {
    const r = spawnSync('docker', ['tag', build.image, ref], { encoding: 'utf8' });
    if (r.status !== 0) throw new Error(`docker tag ${build.image} ${ref} failed: ${lastLine(r.stderr)}`);
  }
  const registry = pushRepo.split('/')[0];
  let config = '';
  const withConfig = (a) => (config ? ['--config', config, ...a] : a);
  try {
    if (login === 'own') {
      config = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-nightly-docker-'));
      const token = fs.readFileSync(registryTokenFile, 'utf8').trim();
      const r = spawnSync('docker', ['--config', config, 'login', registry, '-u', registryUser, '--password-stdin'], { input: token, encoding: 'utf8' });
      if (r.status !== 0) throw new Error(`docker login ${registry} failed: ${lastLine(r.stderr)}`);
    }
    for (const ref of refs) {
      log(`pushing ${ref}`);
      const r = spawnSync('docker', withConfig(['push', ref]), { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
      if (r.status !== 0) throw new Error(`docker push ${ref} failed: ${lastLine(r.stderr || r.stdout)}`);
    }
  } finally {
    if (config) {
      spawnSync('docker', ['--config', config, 'logout', registry], { encoding: 'utf8' });
      fs.rmSync(config, { recursive: true, force: true });
    }
  }
  const digests = spawnSync('docker', ['image', 'inspect', '--format', '{{join .RepoDigests " "}}', refs[0]], { encoding: 'utf8' }).stdout.trim();
  return { refs, digest: digests.split(' ').find((d) => d.startsWith(`${pushRepo}@`)) ?? '', at: iso() };
}

// ── build / publish / status by hand ────────────────────────────────────────

async function buildNow(cfg, args, envFile) {
  const history = readHistory(cfg.state);
  const stateFile = path.join(cfg.state, 'state.json');
  const state = readJson(stateFile) ?? {};
  const last = lastRan(history);
  const result = last?.result ? readJson(last.result) : null;
  const rec = await buildIfDue(cfg, { result, state, stateFile, runDir: last?.run_id ? path.join(cfg.runs, last.run_id) : null, deadline: null, envFile, force: !!args.force });
  console.log(JSON.stringify(rec ?? { decision: 'skipped' }, null, 2));
  return rec?.decision === 'built' && !rec.publishError ? 0 : rec?.decision === 'skipped' ? 0 : 1;
}

function publishNow(cfg, args) {
  const stateFile = path.join(cfg.state, 'state.json');
  const state = readJson(stateFile) ?? {};
  const build = state.last_build;
  if (!build) {
    console.error('no nightly build yet: `nightly.mjs build` makes one from the last green night');
    return 2;
  }
  if (build.published && !args.force) {
    console.log(`${build.image} was published already: ${build.published.refs.join(', ')} (--force pushes it again)`);
    return 0;
  }
  try {
    build.published = publishImage(cfg, build);
  } catch (e) {
    console.error(e.message);
    return 1;
  }
  writeJson(stateFile, state);
  // The morning report reads tonight.json: a build published before it says so.
  const tonightFile = path.join(cfg.state, 'tonight.json');
  const tonight = readJson(tonightFile);
  if (tonight?.build?.sha === build.sha && tonight.build.decision === 'built') {
    tonight.build.published = build.published;
    writeJson(tonightFile, tonight);
  }
  console.log(`published ${build.image} as ${build.published.refs.join(', ')}${build.published.digest ? ` (${build.published.digest})` : ''}`);
  return 0;
}

/**
 * The live S3 tests on the tip of main, now, with the credentials in
 * --s3-env: for a change to the S3 code a night could not test (no
 * credentials on the host), or a provider's own account a person brings for
 * one run. The build and its fast gates, then s3-live alone (run.sh
 * --profile targeted, CHAIN_EXTRAS=s3-live), under the build lock. The
 * credentials reach the chain in its environment and the job in a 0600 file
 * of the run; nothing logs them. Green, the nightly run measures the S3 code
 * from this commit on.
 */
async function s3LiveNow(cfg, args, envFile) {
  if (!args.s3Env) {
    console.error('s3-live needs --s3-env FILE: the FILEX_TEST_S3_* settings to test with (scripts/chain/nightly.env.example names them)');
    return 2;
  }
  let creds;
  let file;
  try {
    creds = parseEnvFile(fs.readFileSync(path.resolve(args.s3Env), 'utf8'));
    file = s3LiveFile(creds);
  } catch (e) {
    console.error(`--s3-env ${args.s3Env}: ${e.message}`);
    return 2;
  }
  if (file.missing.length) {
    console.error(`--s3-env ${args.s3Env} does not set ${file.missing.join(', ')}`);
    return 2;
  }
  const only = Object.fromEntries(
    [...S3_LIVE_KEYS.required, ...S3_LIVE_KEYS.optional].filter((k) => creds[k] !== undefined && creds[k] !== '').map((k) => [k, creds[k]]),
  );
  fs.mkdirSync(cfg.state, { recursive: true });
  const main = fetchMain(cfg);
  if (main.error) {
    console.error(main.error);
    return 2;
  }
  const win = startWindow({ nowMs: Date.now(), windows: cfg.quiet, expectMin: 30, marginMin: cfg.marginMin, lockWaitMin: cfg.lockWaitMin });
  if (!win.ok) {
    console.error(`not now: ${win.why}`);
    return 2;
  }
  const unclean = checkoutClean(cfg, main.head);
  if (unclean) {
    console.error(unclean);
    return 2;
  }
  const runId = `s3live-${nightlyRunId(Date.now(), main.head).slice('nightly-'.length)}`;
  log(`the live S3 tests on ${short(main.head)} ${main.subject}: run ${runId}`);
  const { code, stoppedFor } = await runChain(cfg, { runId, envFile, win, profile: 'targeted', env: { CHAIN_EXTRAS: 's3-live', ...only } });
  if (code === 3) {
    console.error(`the build lock ${cfg.lock} was not free within ${win.lockWaitMin} min`);
    return 2;
  }
  const result = readJson(path.join(cfg.runs, runId, 'result.json'));
  const job = (result?.jobs ?? []).find((j) => j.name === 's3-live');
  const passed = job?.status === 'passed';
  console.log(`s3-live: ${job?.status ?? 'no result'}${job?.summary ? ` - ${job.summary}` : ''}${stoppedFor ? ` (stopped: ${stoppedFor})` : ''}`);
  if (job?.log) console.log(`log ${job.log}`);
  if (passed) {
    const stateFile = path.join(cfg.state, 'state.json');
    const state = readJson(stateFile) ?? {};
    state.s3_live = nextS3LiveState(state.s3_live, { head: main.head, base: state.s3_live?.sha ?? null, passed: true, at: iso(), by: 'hand' });
    writeJson(stateFile, state);
    console.log(`the nightly run measures the S3 code from ${short(main.head)} on`);
  }
  return passed ? 0 : 1;
}

function status(cfg) {
  const tonight = readJson(path.join(cfg.state, 'tonight.json'));
  const state = readJson(path.join(cfg.state, 'state.json')) ?? {};
  const history = readHistory(cfg.state);
  const out = [];
  if (tonight) {
    out.push(`tonight ${tonight.night}: ${tonight.phase}${tonight.decision ? `, ${tonight.decision}` : ''}${tonight.why ? ` (${tonight.why})` : ''} on ${short(tonight.sha)}${tonight.phase !== 'done' ? `, pid ${tonight.pid} ${alive(tonight.pid) ? 'alive' : 'gone'}` : ''}`);
  } else out.push('no night recorded yet');
  for (const r of history.slice(-7).reverse()) {
    const verdict = r.decision === 'ran' ? (r.ok ? 'green' : r.stopped ? 'stopped' : 'RED') : r.decision;
    out.push(`  ${r.night} ${short(r.sha)} ${verdict}${r.wall ? ` ${r.wall}` : ''}${r.build?.decision === 'built' ? ` built ${r.build.version}` : ''}`);
  }
  if (state.last_build) {
    out.push(`last nightly build: ${state.last_build.image} ${state.last_build.version}${state.last_build.published ? `, published ${state.last_build.published.refs.join(', ')}` : ', not published'}`);
  }
  if (state.last_realenv_at) out.push(`last realenv night: ${state.last_realenv_at}`);
  if (state.s3_live) out.push(`S3 code measured from: ${state.s3_live.sha ? short(state.s3_live.sha) : 'nothing yet'} (${state.s3_live.by}, ${state.s3_live.at})`);
  console.log(out.join('\n'));
  return 0;
}

// ── tidy ────────────────────────────────────────────────────────────────────

/** The nightly runs beyond NIGHTLY_KEEP_RUNS and the local images beyond NIGHTLY_KEEP_IMAGES. Never tonight's, never the last build. */
function prune(cfg, keepRun, state) {
  let names = [];
  try {
    names = fs.readdirSync(cfg.runs, { withFileTypes: true }).filter((e) => e.isDirectory()).map((e) => e.name);
  } catch {
    /* no runs yet */
  }
  for (const name of runsToPrune(names, cfg.keepRuns, [keepRun])) {
    log(`pruning run ${name}`);
    fs.rmSync(path.join(cfg.runs, name), { recursive: true, force: true });
  }
  const ls = spawnSync('docker', ['image', 'ls', cfg.build.image, '--format', '{{.Tag}}'], { encoding: 'utf8' });
  if (ls.status !== 0) return;
  const tags = ls.stdout.split('\n').map((t) => t.trim()).filter(Boolean);
  for (const tag of imagesToPrune(tags, cfg.build.keepImages, [state.last_build?.tag].filter(Boolean))) {
    log(`removing the local image ${cfg.build.image}:${tag}`);
    spawnSync('docker', ['rmi', `${cfg.build.image}:${tag}`], { encoding: 'utf8' });
  }
}

// ── main ────────────────────────────────────────────────────────────────────

async function main() {
  let args;
  try {
    args = parseNightlyArgs(process.argv.slice(2));
  } catch (e) {
    console.error(`${e.message}\n\n${USAGE}`);
    return 2;
  }
  if (args.help) {
    console.log(USAGE);
    return 0;
  }
  const envArg = args.env || process.env.NIGHTLY_ENV || '';
  const envFile = envArg ? path.resolve(envArg) : '';
  let cfg;
  try {
    cfg = nightlySettings(mergeEnv(envFile ? fs.readFileSync(envFile, 'utf8') : '', process.env));
  } catch (e) {
    console.error(e.message);
    return 2;
  }
  if (args.cmd === 'status') return status(cfg);
  if (process.platform !== 'linux') {
    console.error('the nightly run runs on Linux with Docker (status works anywhere)');
    return 2;
  }
  let release;
  try {
    release = takeOwnLock(path.join(cfg.state, 'nightly.lock'));
  } catch (e) {
    console.error(e.message);
    return 2;
  }
  try {
    if (args.cmd === 'run') return await runNight(cfg, args, envFile);
    if (args.cmd === 'build') return await buildNow(cfg, args, envFile);
    if (args.cmd === 's3-live') return await s3LiveNow(cfg, args, envFile);
    return publishNow(cfg, args);
  } finally {
    release();
  }
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
