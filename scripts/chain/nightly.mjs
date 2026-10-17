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
//   node scripts/chain/nightly.mjs follow  --env FILE             GitHub's desktop packages of the last
//                                  source, and the filex.sh/nightly page (report.mjs does it each morning)
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
//   5. with a nightly build made, its desktop side (#266,
//      scripts/chain/nightly-desktop.sh, from the same public form): the
//      amd64 snap, uploaded to the Snap Store's edge channel
//      (NIGHTLY_SNAP=edge), and that public form as a source archive pushed
//      to ghcr (NIGHTLY_DESKTOP=github, NIGHTLY_DESKTOP_SRC_REPO), from which
//      GitHub's runners make every desktop package and the arm64 snap
//      (.github/workflows/nightly-desktop.yml) - followed up, and put on
//      filex.sh/nightly, in the morning (`follow`). An upload the Store holds
//      for a review is said in every morning report until edge serves it;
//   6. keeps the night's record and prunes old runs, images and desktop runs.
//
// What it leaves under <CHAIN_ROOT>/nightly/: tonight.json (the night so far,
// rewritten at each step), history.jsonl (one line per night), state.json
// (the last nightly build, the last nightly snap, the last source pushed and
// the last page published, the last realenv night, the commit the S3 code is
// measured from), nightly.lock
// (this process, while it runs). The chain's own runs/<run-id>/ and
// runs/latest-nightly.json are under <CHAIN_ROOT>/runs/, as for any run; the
// desktop runs under NIGHTLY_DESKTOP_DIR (<CHAIN_ROOT>/desktop/<date>-<commit>/:
// files/, SHA256SUMS, the logs; site/: the last page made).
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
  decideDesktop,
  decideRun,
  decideS3Live,
  desktopDirsToPrune,
  desktopVersion,
  followLines,
  followTrouble,
  followVerdict,
  expectedRunMin,
  extrasFor,
  imagesToPrune,
  jobStatuses,
  lastRan,
  localDate,
  nightRecord,
  nightlyLabels,
  nightlyRunId,
  nightlySettings,
  nightlyTags,
  nightlyVersion,
  nextS3LiveState,
  registryLogin,
  renderNightlyPage,
  resultFields,
  runsToPrune,
  s3LiveFile,
  s3Touched,
  snapEdgeState,
  startWindow,
} from './nightly-lib.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));

const USAGE = `usage: node scripts/chain/nightly.mjs <run|build|publish|status|follow|s3-live> [options]

  run       tonight's nightly run (what its timer starts)
  build     the nightly build of the last green night, now (--force: even
            when that commit was built already)
  publish   push the last nightly build to NIGHTLY_PUSH_REPO under
            :nightly and :nightly-<date> (--force: again)
  status    tonight and the last nights
  follow    GitHub's desktop packages of the last source pushed: built,
            building or failed; built, the filex.sh/nightly page is made and
            published (NIGHTLY_SITE_CMD) - what report.mjs does every morning
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
  if (!['run', 'build', 'publish', 'status', 'follow', 's3-live'].includes(args.cmd)) throw new Error(`unknown command ${args.cmd}`);
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
    if (tonight.build?.decision === 'built') {
      tonight.desktop = await desktopIfDue(cfg, { build: tonight.build, state, stateFile, deadline: null });
    }
    // A snap the Store holds for review is said every morning (#266).
    if (!args.dryRun) tonight.snapEdge = await snapEdgeCheck(cfg, state, tonight.desktop);
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

  // 6. Its desktop side (#266): the snap for edge, the weekly packages.
  tonight.phase = 'desktop';
  save();
  tonight.desktop = await desktopIfDue(cfg, { build: tonight.build, state, stateFile, deadline: win.deadline });
  tonight.snapEdge = await snapEdgeCheck(cfg, state, tonight.desktop);
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
  const labels = nightlyLabels({ counts: result.counts, runId: result.run_id ?? (runDir ? path.basename(runDir) : '') });
  const rec = await buildImage(cfg, { sha: result.sha, runDir: runDir ?? path.join(cfg.state, 'builds', short(result.sha)), deadline, labels });
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
async function buildImage(cfg, { sha, runDir, deadline, labels = {} }) {
  const out = path.join(runDir, 'nightly-build');
  fs.mkdirSync(out, { recursive: true });
  const logFile = path.join(out, 'build.log');
  const labelsFile = path.join(out, 'labels.txt');
  fs.writeFileSync(labelsFile, Object.entries(labels).map(([k, v]) => `${k}=${v}\n`).join(''));
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
  const r = await runLocked(cfg, 'nightly-build.sh', {
    logFile,
    deadline,
    expectMin: cfg.build.expectMin,
    env: {
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
      NB_LABELS_FILE: labelsFile,
    },
  });
  if (r.trouble) return { decision: 'failed', why: r.trouble, sha, log: logFile };
  if (r.status !== 0) return { decision: 'failed', why: `nightly-build.sh exited ${r.status}: ${tailOf(logFile, 2)}`, sha, log: logFile };
  const id = spawnSync('docker', ['image', 'inspect', '--format', '{{.Id}}', `${cfg.build.image}:${tag}`], { encoding: 'utf8' }).stdout.trim();
  return { decision: 'built', image: `${cfg.build.image}:${tag}`, id, version, tag, ymd, sha, at: iso(), log: logFile };
}

/**
 * A script of tonight's tree (scripts/chain/<name>) under the build lock, in
 * a process group of its own, until `deadline` (or two hours): it waits for
 * the lock at most as long as it could still end in time. `trouble` says why
 * it did not finish - the lock never came free, or it was stopped - and is ''
 * when it ran to its own exit code (`status`).
 */
async function runLocked(cfg, name, { env, logFile, deadline, expectMin }) {
  const startedMs = Date.now();
  const script = path.join(cfg.src, 'scripts', 'chain', name);
  const budgetMs = deadline ? Math.max(60_000, deadline - startedMs) : 120 * 60_000;
  const waitSecs = Math.max(0, Math.floor(Math.min(30 * 60_000, budgetMs - expectMin * 60_000) / 1000));
  const [cmd, argv] = cfg.lock === 'none' ? ['bash', [script]] : ['flock', ['-E', '75', '-w', String(waitSecs), cfg.lock, 'bash', script]];
  const r = await runGroup(cmd, argv, { cwd: cfg.src, logFile, timeoutMs: budgetMs, env: childEnv(env) });
  let trouble = '';
  if (r.status === 75) trouble = `the build lock ${cfg.lock} was not free within ${Math.round(waitSecs / 60)} min`;
  else if (r.timedOut || r.status === null) trouble = `stopped after ${Math.round((Date.now() - startedMs) / 60_000)} min (${r.timedOut ? 'out of time before the quiet window' : r.signal})`;
  return { ...r, trouble };
}

/**
 * Push a nightly build to NIGHTLY_PUSH_REPO as :nightly and :nightly-<date>.
 * With NIGHTLY_REGISTRY_USER and NIGHTLY_REGISTRY_TOKEN_FILE it logs in with a
 * Docker config of its own, removed afterwards: no credential stays in the
 * host's Docker config. Without either it uses the host's; with one of them
 * only it refuses (registryLogin).
 */
function publishImage(cfg, build) {
  const { pushRepo } = cfg.build;
  if (!pushRepo) throw new Error('NIGHTLY_PUSH_REPO is not set: say where the nightly build goes');
  if (!build?.image) throw new Error('no nightly build to publish');
  const refs = nightlyTags(build.ymd).map((t) => `${pushRepo}:${t}`);
  for (const ref of refs) {
    const r = spawnSync('docker', ['tag', build.image, ref], { encoding: 'utf8' });
    if (r.status !== 0) throw new Error(`docker tag ${build.image} ${ref} failed: ${lastLine(r.stderr)}`);
  }
  withRegistry(cfg, pushRepo.split('/')[0], (config) => {
    for (const ref of refs) {
      log(`pushing ${ref}`);
      const r = spawnSync('docker', config ? ['--config', config, 'push', ref] : ['push', ref], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
      if (r.status !== 0) throw new Error(`docker push ${ref} failed: ${lastLine(r.stderr || r.stdout)}`);
    }
  });
  const digests = spawnSync('docker', ['image', 'inspect', '--format', '{{join .RepoDigests " "}}', refs[0]], { encoding: 'utf8' }).stdout.trim();
  return { refs, digest: digests.split(' ').find((d) => d.startsWith(`${pushRepo}@`)) ?? '', at: iso() };
}

/**
 * Runs `fn` signed in to `registry`: with NIGHTLY_REGISTRY_USER and
 * NIGHTLY_REGISTRY_TOKEN_FILE, in a Docker config of its own (`fn` gets its
 * directory), removed afterwards - no credential stays in the host's Docker
 * config; without either, with the host's (`fn` gets ''); with one of them
 * only it refuses (registryLogin). The image push and the source push share it.
 */
function withRegistry(cfg, registry, fn) {
  const { registryUser, registryTokenFile } = cfg.build;
  const login = registryLogin({ registryUser, registryTokenFile });
  let config = '';
  try {
    if (login === 'own') {
      config = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-nightly-docker-'));
      const token = fs.readFileSync(registryTokenFile, 'utf8').trim();
      const r = spawnSync('docker', ['--config', config, 'login', registry, '-u', registryUser, '--password-stdin'], { input: token, encoding: 'utf8' });
      if (r.status !== 0) throw new Error(`docker login ${registry} failed: ${lastLine(r.stderr)}`);
    }
    return fn(config);
  } finally {
    if (config) {
      spawnSync('docker', ['--config', config, 'logout', registry], { encoding: 'utf8' });
      fs.rmSync(config, { recursive: true, force: true });
    }
  }
}

/**
 * The ORAS client (NIGHTLY_ORAS_IMAGE) in a container, `dir` mounted as its
 * working directory and, when given, a Docker config to sign in with
 * (mounted read-only; `--registry-config` points at it). Returns spawnSync's.
 */
function oras(cfg, args, { dir = '', config = '', timeoutMs = 30 * 60_000 } = {}) {
  const mounts = [...(dir ? ['-v', `${dir}:/w`, '-w', '/w'] : []), ...(config ? ['-v', `${config}:/auth:ro`] : [])];
  const auth = config ? ['--registry-config', '/auth/config.json'] : [];
  const [sub, ...rest] = args;
  const words = sub === 'manifest' || sub === 'blob' ? [sub, rest[0], ...auth, ...rest.slice(1)] : [sub, ...auth, ...rest];
  // As root in the container: the Docker config a login writes is 0600, and
  // the image's own user could not read it.
  return spawnSync('docker', ['run', '--rm', '--user', '0:0', ...mounts, cfg.desktop.oras, ...words], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, timeout: timeoutMs });
}

// ── the desktop side (#266) ─────────────────────────────────────────────────

/**
 * The desktop side of tonight's nightly build, when decideDesktop says one is
 * due: scripts/chain/nightly-desktop.sh (from tonight's tree, under the build
 * lock) makes the amd64 snap and the source archive from the public form of
 * the commit; then the snap goes to the Snap Store's edge channel and the
 * source to ghcr, for GitHub's runners (pushSource). Never throws:
 * every outcome is a record for the morning report, and nothing here turns a
 * green night red - a warning at most.
 */
async function desktopIfDue(cfg, { build, state, stateFile, deadline }) {
  const asked = cfg.snap.mode !== 'off' || cfg.desktop.mode !== 'off';
  const d = decideDesktop({ built: build?.decision === 'built', snapMode: cfg.snap.mode, desktopMode: cfg.desktop.mode });
  if (!d.targets.length) return asked && build?.decision === 'built' ? { decision: 'skipped', why: d.why, asked } : undefined;
  let version;
  try {
    version = desktopVersion(build.version);
  } catch (e) {
    return { decision: 'failed', why: e.message, asked };
  }
  if (!deadline) {
    const w = startWindow({ nowMs: Date.now(), windows: cfg.quiet, expectMin: cfg.desktop.expectMin, marginMin: cfg.marginMin, lockWaitMin: 30 });
    if (!w.ok) return { decision: 'skipped', why: `the desktop side waits: ${w.why}`, asked };
    deadline = w.deadline;
  }
  if (deadline && Date.now() + cfg.desktop.expectMin * 60_000 > deadline) {
    return { decision: 'skipped', why: `no time left before the quiet window (the desktop side takes ~${cfg.desktop.expectMin} min)`, asked };
  }
  const head = git(cfg.src, ['rev-parse', 'HEAD']).stdout.trim();
  if (head !== build.sha) return { decision: 'failed', why: `the checkout is at ${short(head)}, not at ${short(build.sha)}, the commit built tonight`, asked };
  const dir = path.join(cfg.desktop.out, `${build.ymd}-${short(build.sha)}`);
  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(dir, { recursive: true });
  const logFile = path.join(dir, 'desktop.log');
  log(`desktop side of ${short(build.sha)} (${version}): ${d.targets.join(', ')} - ${d.why}`);
  const c = cfg.desktop.cache;
  const r = await runLocked(cfg, 'nightly-desktop.sh', {
    logFile,
    deadline,
    expectMin: cfg.desktop.expectMin,
    env: {
      ND_SRC: cfg.src,
      ND_SHA: build.sha,
      ND_PUBLIC_DIR: cfg.desktop.publicDir,
      ND_PUBLIC_REMOTE: cfg.build.publicRemote,
      ND_OUT: dir,
      ND_VERSION: version,
      ND_TARGETS: d.targets.join(','),
      ND_IMAGE: cfg.desktop.image,
      ND_GOROOT: c.goroot,
      ND_GOMODCACHE: c.gomod,
      ND_GOCACHE: c.gocache,
      ND_PNPM_STORE: c.pnpm,
      ND_ELECTRON_CACHE: c.electron,
      ND_SNAPCRAFT: cfg.snap.snapcraft,
    },
  });
  const base = { version, sha: build.sha, dir, log: logFile, asked, at: iso() };
  if (r.trouble) return { ...base, decision: 'failed', why: r.trouble };
  const targets = Object.fromEntries(
    readText(path.join(dir, 'targets.txt'))
      .split('\n')
      .map((l) => l.trim().split(/\s+/))
      .filter((p) => p.length === 2)
      .map(([t, s]) => [t, s]),
  );
  const built = d.targets.filter((t) => targets[t] === 'built');
  const rec = {
    ...base,
    decision: built.length === d.targets.length ? 'built' : built.length ? 'partial' : 'failed',
    why: built.length === d.targets.length ? d.why : `nightly-desktop.sh exited ${r.status}: ${tailOf(logFile, 2)}`,
    targets: Object.fromEntries(d.targets.map((t) => [t, targets[t] ?? 'failed'])),
  };
  if (d.targets.includes('snap')) {
    rec.snap = targets.snap === 'built' ? uploadSnap(cfg, path.join(dir, 'files', 'filex-desktop-amd64.snap'), version) : { state: 'skipped', why: 'the snap was not packed tonight' };
    if (['released', 'review'].includes(rec.snap.state)) {
      state.last_snap = { version, revision: rec.snap.revision ?? null, state: rec.snap.state, sha: build.sha, dir, at: iso() };
    }
  }
  if (targets.source === 'built') {
    try {
      rec.source = pushSource(cfg, { build, file: path.join(dir, 'files', 'filex-nightly-src.tar.gz'), version });
      state.last_source = { revision: build.sha, version, ymd: build.ymd, ref: rec.source.ref, at: iso() };
    } catch (e) {
      rec.source = { error: e.message };
    }
  }
  writeJson(stateFile, state);
  pruneDesktop(cfg, state);
  return rec;
}

/**
 * Uploads a night's snap to the Snap Store's edge channel (amd64; the
 * release puts its own revisions on every channel, edge included). The
 * credential is the output of `snapcraft export-login`, read from
 * NIGHTLY_SNAP_CREDENTIALS_FILE into snapcraft's environment for this one
 * command and never logged. A revision the Store holds for its review is
 * `review`, not a failure: edge keeps its old revision until the review
 * passes, and the morning report says so until edge serves it.
 */
function uploadSnap(cfg, file, version) {
  // Asked for and not possible is a warning, not a quiet skip: a channel that
  // stops getting builds is found months later otherwise.
  if (!cfg.snap.credentialsFile) return { state: 'failed', why: 'NIGHTLY_SNAP_CREDENTIALS_FILE is not set: the snap was packed and not uploaded', name: cfg.snap.name };
  let creds = '';
  try {
    creds = fs.readFileSync(cfg.snap.credentialsFile, 'utf8').trim();
  } catch (e) {
    return { state: 'failed', why: `the Snap Store credential could not be read (${e.code ?? e.message})`, name: cfg.snap.name };
  }
  if (!creds) return { state: 'failed', why: `${cfg.snap.credentialsFile} is empty`, name: cfg.snap.name };
  log(`snapcraft upload --release=edge ${path.basename(file)} (${version})`);
  // The upload itself is the one GitHub's arm64 runner makes too
  // (scripts/chain/nightly-snap-edge.mjs, from tonight's tree): the same
  // command, the same reading of snapcraft's answer.
  const result = path.join(path.dirname(path.dirname(file)), 'snap-upload.json');
  const r = spawnSync(process.execPath, [path.join(cfg.src, 'scripts', 'chain', 'nightly-snap-edge.mjs'), file, result], {
    encoding: 'utf8',
    env: childEnv({ SNAPCRAFT_STORE_CREDENTIALS: creds, SNAPCRAFT: cfg.snap.snapcraft }),
    maxBuffer: 16 * 1024 * 1024,
    timeout: 50 * 60_000,
  });
  fs.writeFileSync(path.join(path.dirname(path.dirname(file)), 'snap-upload.log'), `${r.stdout ?? ''}\n${r.stderr ?? ''}`.split(creds).join('<credential>'));
  const v = readJson(result) ?? { state: 'failed', why: `nightly-snap-edge.mjs exited ${r.status ?? r.signal} and left no result` };
  return { ...v, name: cfg.snap.name };
}

/**
 * Pushes the night's source archive (the public tree) to
 * NIGHTLY_DESKTOP_SRC_REPO as :nightly, with what GitHub's runners need in
 * its annotations: the commit (org.opencontainers.image.revision), the
 * version every package carries, the night. The workflow takes it within the
 * hour (.github/workflows/nightly-desktop.yml) - a new revision there is a
 * new build. Signs in as the image push does (withRegistry). Throws.
 */
function pushSource(cfg, { build, file, version }) {
  if (!fs.existsSync(file)) throw new Error(`no source archive at ${file}`);
  const ref = `${cfg.desktop.srcRepo}:nightly`;
  const annotations = {
    'org.opencontainers.image.revision': build.sha,
    'org.opencontainers.image.version': build.version,
    'sh.filex.nightly.desktop-version': version,
    'sh.filex.nightly.date': `${build.ymd.slice(0, 4)}-${build.ymd.slice(4, 6)}-${build.ymd.slice(6, 8)}`,
  };
  const words = Object.entries(annotations).flatMap(([k, v]) => ['--annotation', `${k}=${v}`]);
  return withRegistry(cfg, ref.split('/')[0], (config) => {
    log(`pushing the source archive to ${ref}`);
    const r = oras(cfg, ['push', ref, ...words, `${path.basename(file)}:application/gzip`], { dir: path.dirname(file), config });
    if (r.status !== 0) throw new Error(`oras push ${ref} failed: ${lastLine(r.stderr || r.stdout) || r.error?.message || `exit ${r.status}`}`);
    const digest = /Digest:\s*(sha256:[0-9a-f]{64})/.exec(`${r.stdout}\n${r.stderr}`)?.[1] ?? '';
    return { ref, digest, at: iso() };
  });
}

/** A manifest's annotations, read anonymously (the packages are public): {} when there is none. */
function annotationsOf(cfg, ref) {
  const r = oras(cfg, ['manifest', 'fetch', ref], { timeoutMs: 120_000 });
  if (r.status !== 0) return { error: lastLine(r.stderr) || `exit ${r.status}` };
  try {
    const m = JSON.parse(r.stdout);
    return { annotations: m.annotations ?? {}, layers: m.layers ?? [] };
  } catch (e) {
    return { error: `not a manifest: ${e.message}` };
  }
}

/** The newest run of the nightly desktop workflow (GitHub's public API, no token), or null. */
async function newestRun(cfg) {
  try {
    const res = await fetch(`https://api.github.com/repos/${cfg.desktop.workflowRepo}/actions/workflows/${cfg.desktop.workflow}/runs?per_page=1`, {
      headers: { accept: 'application/vnd.github+json', 'user-agent': 'filex-nightly' },
      signal: AbortSignal.timeout(30_000),
    });
    if (!res.ok) return null;
    const run = (await res.json())?.workflow_runs?.[0];
    return run ? { status: run.status, conclusion: run.conclusion, url: run.html_url } : null;
  } catch {
    return null;
  }
}

/**
 * The morning's follow-up of the last source pushed (#266): what GitHub's
 * runners made of it (followVerdict); built, the night's manifest read from
 * the artifact, the filex.sh/nightly page made from it and site/nightly.src.html
 * (renderNightlyPage) and published by NIGHTLY_SITE_CMD once per revision;
 * and whether edge serves the arm64 snap the workflow uploaded. Never throws:
 * a record for the morning report (followLines).
 */
export async function followDesktop(cfg, { nowMs = Date.now(), dry = false } = {}) {
  if (cfg.desktop.mode !== 'github') return null;
  const stateFile = path.join(cfg.state, 'state.json');
  const state = readJson(stateFile) ?? {};
  const source = state.last_source;
  if (!source) return null;
  const nightly = annotationsOf(cfg, `${cfg.desktop.repo}:nightly`);
  const attempted = annotationsOf(cfg, `${cfg.desktop.repo}:attempted`);
  const rev = (a) => (a.annotations ? { revision: a.annotations['org.opencontainers.image.revision'] } : null);
  const f = followVerdict({ source, built: rev(nightly), attempted: rev(attempted), run: await newestRun(cfg), nowMs });
  if (!f || f.state !== 'built') return f;
  // The night's manifest, from the artifact itself.
  const layer = (nightly.layers ?? []).find((l) => l.annotations?.['org.opencontainers.image.title'] === 'nightly.json');
  let manifest = null;
  if (layer) {
    const r = oras(cfg, ['blob', 'fetch', `${cfg.desktop.repo}@${layer.digest}`, '--output', '-'], { timeoutMs: 120_000 });
    try {
      manifest = r.status === 0 ? JSON.parse(r.stdout) : null;
    } catch {
      manifest = null;
    }
  }
  if (!manifest) return { ...f, state: 'unknown', why: `${cfg.desktop.repo}:nightly carries no readable nightly.json` };
  f.rows = manifest.rows ?? {};
  f.platforms = [...new Set((manifest.files ?? []).map((x) => x.platform))];
  f.url = manifest.run ?? '';
  f.snapArm64 = manifest.snap?.arm64 ?? null;
  if (f.snapArm64?.state === 'released' || f.snapArm64?.state === 'review') {
    try {
      const res = await fetch(`https://api.snapcraft.io/v2/snaps/info/${cfg.snap.name}?fields=version,revision`, { headers: { 'Snap-Device-Series': '16' }, signal: AbortSignal.timeout(30_000) });
      if (res.ok) f.edgeArm64 = snapEdgeState((await res.json())?.['channel-map'] ?? [], { version: manifest.version, revision: f.snapArm64.revision, at: manifest.built_at }, { arch: 'arm64', nowMs }) ?? undefined;
    } catch {
      /* the Store is asked again tomorrow */
    }
    if (f.edgeArm64?.state === 'live') f.edgeArm64 = undefined;
  }
  // The page, once per revision.
  if (state.last_site?.revision === source.revision) {
    f.site = { state: 'same' };
    return f;
  }
  let template = '';
  try {
    template = fs.readFileSync(path.join(cfg.src, 'site', 'nightly.src.html'), 'utf8');
  } catch {
    f.site = { state: 'failed', detail: `no site/nightly.src.html in ${cfg.src}` };
    return f;
  }
  const siteDir = path.join(cfg.desktop.out, 'site');
  try {
    fs.rmSync(siteDir, { recursive: true, force: true });
    fs.mkdirSync(siteDir, { recursive: true });
    fs.writeFileSync(path.join(siteDir, 'index.html'), renderNightlyPage({ template, manifest }));
    fs.writeFileSync(path.join(siteDir, 'nightly.json'), `${JSON.stringify(manifest, null, 2)}\n`);
  } catch (e) {
    f.site = { state: 'failed', detail: e.message };
    return f;
  }
  if (!cfg.desktop.siteCmd) {
    f.site = { state: 'kept', detail: `NIGHTLY_SITE_CMD is not set; the page is in ${siteDir}` };
    return f;
  }
  if (dry) {
    f.site = { state: 'kept', detail: `dry run; the page is in ${siteDir}` };
    return f;
  }
  const digest = nightlyDigest(cfg);
  const r = spawnSync('sh', ['-c', `${cfg.desktop.siteCmd} "$1"`, 'nightly-site', siteDir], {
    encoding: 'utf8',
    env: childEnv({ NIGHTLY_SITE_REF: digest ? `${cfg.desktop.repo}@${digest}` : `${cfg.desktop.repo}:nightly`, NIGHTLY_ORAS_IMAGE: cfg.desktop.oras }),
    maxBuffer: 16 * 1024 * 1024,
    timeout: 30 * 60_000,
  });
  const last = lastLine(r.stdout) || lastLine(r.stderr);
  if (r.status === 0) {
    f.site = { state: 'published', detail: last || `${manifest.version} published` };
    state.last_site = { revision: source.revision, version: manifest.version, at: iso() };
    writeJson(stateFile, state);
  } else f.site = { state: 'failed', detail: `exit ${r.status ?? r.signal}: ${lastLine(r.stderr) || last}` };
  return f;
}

/** The digest `nightly` names now, so the page and the files it lists are one artifact. */
function nightlyDigest(cfg) {
  const r = oras(cfg, ['resolve', `${cfg.desktop.repo}:nightly`], { timeoutMs: 120_000 });
  const d = r.status === 0 ? r.stdout.trim() : '';
  return /^sha256:[0-9a-f]{64}$/.test(d) ? d : '';
}

/**
 * Whether edge serves the last nightly snap (snapEdgeState), read from the
 * public channel map - every night, the quiet ones too, so an upload the
 * Store holds is said each morning until it is not. A snap uploaded tonight is
 * the desktop record's to report. Never throws.
 */
async function snapEdgeCheck(cfg, state, tonightDesktop) {
  if (cfg.snap.mode === 'off' || !state.last_snap) return undefined;
  if (tonightDesktop?.snap && ['released', 'review'].includes(tonightDesktop.snap.state)) return undefined;
  if (state.last_snap.state === 'live') return undefined;
  let map = [];
  try {
    const res = await fetch(`https://api.snapcraft.io/v2/snaps/info/${cfg.snap.name}?fields=version,revision`, {
      headers: { 'Snap-Device-Series': '16' },
      signal: AbortSignal.timeout(30_000),
    });
    if (!res.ok) return { state: 'unknown', why: `api.snapcraft.io answered HTTP ${res.status}` };
    map = (await res.json())?.['channel-map'] ?? [];
  } catch (e) {
    return { state: 'unknown', why: `api.snapcraft.io: ${e?.cause?.code ?? e?.message ?? e}` };
  }
  const s = snapEdgeState(map, state.last_snap, { nowMs: Date.now() });
  if (s?.state === 'live') {
    state.last_snap = { ...state.last_snap, state: 'live', live_at: iso() };
    writeJson(path.join(cfg.state, 'state.json'), state);
  }
  return s ?? undefined;
}

/** The local desktop runs beyond NIGHTLY_DESKTOP_KEEP. Never the last one. */
function pruneDesktop(cfg, state) {
  let names = [];
  try {
    names = fs.readdirSync(cfg.desktop.out, { withFileTypes: true }).filter((e) => e.isDirectory()).map((e) => e.name);
  } catch {
    return;
  }
  const protect = [state.last_snap?.dir].filter(Boolean).map((d) => path.basename(d));
  for (const name of desktopDirsToPrune(names, cfg.desktop.keep, protect)) {
    log(`pruning the desktop run ${name}`);
    fs.rmSync(path.join(cfg.desktop.out, name), { recursive: true, force: true });
  }
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
  if (state.last_snap) out.push(`last nightly snap: ${state.last_snap.version}${state.last_snap.revision ? ` revision ${state.last_snap.revision}` : ''}, ${state.last_snap.state} (${state.last_snap.at})`);
  if (state.last_source) out.push(`last source for GitHub's desktop packages: ${state.last_source.version} (${short(state.last_source.revision)}), ${state.last_source.ref} at ${state.last_source.at}`);
  if (state.last_site) out.push(`last filex.sh/nightly page: ${state.last_site.version} at ${state.last_site.at}`);
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
  if (args.cmd === 'follow') {
    const f = await followDesktop(cfg, { dry: !!args.dryRun });
    console.log(f ? followLines(f).join('\n') || f.state : 'nothing to follow: NIGHTLY_DESKTOP is not github, or no source was pushed yet');
    return f && followTrouble(f) ? 1 : 0;
  }
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
