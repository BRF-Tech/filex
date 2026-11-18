#!/usr/bin/env node
// The nightly (task #175; since #361, 2026-11-18, the build host's half of
// it only): every night, the tip of the private main, exported the way a
// release is, pushed as ONE commit to the public repository's `nightly`
// branch - and GitHub does the rest. The build host runs no test, builds no
// image and packs no snap at night any more.
//
//   node scripts/chain/nightly.mjs run    --env FILE [--force] [--dry-run]
//   node scripts/chain/nightly.mjs status --env FILE   tonight and the last nights
//   node scripts/chain/nightly.mjs follow --env FILE [--dry-run]
//                                  GitHub's verdict on the last nightly, its
//                                  packages, and the filex.sh/nightly page
//                                  (report.mjs does it every morning)
//
// `run` (its timer: scripts/chain/systemd/filex-nightly.timer):
//   1. fetches the private main into NIGHTLY_SRC (default <CHAIN_ROOT>/src);
//   2. stops there when it has not moved since the last night that pushed
//      (nightly-lib.mjs decidePush), and - NIGHTLY_RELEASE_BUSY=skip, the
//      default - while GitHub tests a release (releaseBusy): the night's
//      full matrix would share the organisation's 20 jobs with the
//      release's gate. The next night pushes;
//   3. checks it out clean and runs scripts/chain/nightly-export.sh from
//      that tree: scripts/export-public.sh stages the public tree in a clone
//      of the public repository (NIGHTLY_PUBLIC_DIR) reset to its main, with
//      the same rewrite and the same refusals as a release, and the result is
//      committed on top of the public main as one commit by
//      NIGHTLY_COMMIT_AUTHOR (nightlyCommitMessage: the night, never the
//      private commit). An export equal to the public main pushes nothing;
//   4. force-pushes that commit to the `nightly` branch (NIGHTLY_PUSH_BRANCH)
//      through the export's pre-push gate (scripts/hooks/export-pre-push.sh):
//      from NIGHTLY_PUSH_CONTAINER - the release workspace, whose GitHub
//      token never leaves it (scripts/chain/nightly-push.sh, NIGHTLY_PUSH_WRAP)
//      - or, with none, from the clone here with this host's own credential;
//   5. keeps the night's record. When the change touched the S3 code
//      (nightly-lib.mjs S3_LIVE_PATHS), the morning report says that nothing
//      ran the live S3 tests on it.
//
// On GitHub, ci.yml's push trigger names the branch: its full matrix runs on
// the commit, and green, .github/workflows/nightly-desktop.yml (workflow_run)
// builds the nightly image and every desktop package from it. The morning
// report (report.mjs) reads all of it back.
//
// What it leaves under <CHAIN_ROOT>/nightly/: tonight.json (the night so far,
// rewritten at each step), history.jsonl (one line per night; the morning
// report adds GitHub's verdict to it), state.json (the last export, the last
// push, the last page published), tonight/ (the night's logs and bundle),
// nightly.lock (this process, while it runs).
//
// Exit: 0 pushed, or nothing to push; 1 the export refused the change or the
// push failed (recorded, and said in the morning); 2 it could not run (see
// tonight.json).
//
// ⚠ It runs from an installed copy (scripts/chain/install-nightly.sh copies
// this file, nightly-lib.mjs, report.mjs and env.mjs), never from the
// checkout it resets: what it runs from the tree (nightly-export.sh,
// nightly-push.sh, export-public.sh) is tonight's.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { mergeEnv } from './env.mjs';
import {
  SNAP_ARCHES,
  ciVerdict,
  decidePush,
  followLines,
  followTrouble,
  followVerdict,
  imageLine,
  localDate,
  nightRecord,
  nightlyCommitMessage,
  nightlySettings,
  nightlyVersion,
  parseAuthor,
  releaseBusy,
  renderNightlyPage,
  s3Touched,
  snapEdgeState,
} from './nightly-lib.mjs';

const USAGE = `usage: node scripts/chain/nightly.mjs <run|status|follow> [options]

  run       tonight's nightly (what its timer starts): the private main,
            exported, pushed to the public repository's nightly branch
  status    tonight and the last nights
  follow    GitHub's verdict on the last nightly, its image and desktop
            packages; built, the filex.sh/nightly page is made and published
            (NIGHTLY_SITE_CMD) - what report.mjs does every morning

  --env FILE     settings, KEY=VALUE (default: $NIGHTLY_ENV); the process
                 environment wins over the file. The keys, with their
                 defaults: scripts/chain/nightly.env.example
  --force        run: even when main has not moved, or GitHub tests a release
  --dry-run      run: fetch and decide, print what it would do, change nothing;
                 follow: make the page, publish nothing

Exit: 0 pushed or nothing to do, 1 refused or not pushed, 2 could not run.`;

export function parseNightlyArgs(argv) {
  const args = { cmd: 'run' };
  let i = 0;
  if (argv[0] && !argv[0].startsWith('-')) {
    args.cmd = argv[0];
    i = 1;
  }
  if (!['run', 'status', 'follow'].includes(args.cmd)) throw new Error(`unknown command ${args.cmd}`);
  for (; i < argv.length; i += 1) {
    const a = argv[i];
    if (a === '--env') {
      args.env = argv[i + 1];
      if (!args.env || args.env.startsWith('--')) throw new Error('--env needs a file');
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

export function writeJson(file, value) {
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

function writeHistory(stateDir, all) {
  const file = path.join(stateDir, 'history.jsonl');
  fs.writeFileSync(`${file}.tmp`, `${all.map((r) => JSON.stringify(r)).join('\n')}\n`);
  fs.renameSync(`${file}.tmp`, file);
}

function appendHistory(stateDir, record, keep) {
  writeHistory(stateDir, [...readHistory(stateDir), record].slice(-Math.max(1, keep)));
}

/**
 * GitHub's verdict on a pushed nightly, written into its night's record
 * (history.jsonl and, when it is tonight's, tonight.json) once it is final -
 * what the next nights compare their jobs with. Returns whether it wrote.
 */
export function recordCi(stateDir, sha, ci) {
  if (!sha || !ci || !['green', 'red', 'cancelled'].includes(ci.state)) return false;
  const all = readHistory(stateDir);
  let i = -1;
  for (let k = all.length - 1; k >= 0; k -= 1) {
    if (all[k].decision === 'pushed' && all[k].push?.sha === sha) {
      i = k;
      break;
    }
  }
  const tonightFile = path.join(stateDir, 'tonight.json');
  const tonight = readJson(tonightFile);
  if (tonight?.push?.sha === sha) writeJson(tonightFile, { ...tonight, ci });
  if (i < 0) return false;
  all[i] = nightRecord({ ...all[i], ci });
  writeHistory(stateDir, all);
  return true;
}

function git(cwd, args) {
  return spawnSync('git', ['-c', 'safe.directory=*', '-C', cwd, ...args], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
}

/**
 * The environment of what this process starts: its own, with the directory of
 * the node running it first on PATH - the export calls `node` by name, and a
 * systemd unit's PATH may not have the node it was given.
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

function readText(file) {
  try {
    return fs.readFileSync(file, 'utf8');
  } catch {
    return '';
  }
}

/** This process's own lock: one nightly at a time. A lock whose process is gone is taken over. */
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
      if (alive(pid)) throw new Error(`another nightly (pid ${pid}) holds ${file}`);
      fs.rmSync(file, { force: true });
    }
  }
  throw new Error(`could not take ${file}`);
}

// ── GitHub's API ────────────────────────────────────────────────────────────

/**
 * One GET of GitHub's REST API (`p` without the host): { ok, status, json },
 * or { ok: false, error }. With NIGHTLY_GITHUB_TOKEN_FILE the token is sent for
 * the rate limit (everything asked is public); it is never logged.
 */
export async function ghApi(cfg, p) {
  const headers = { accept: 'application/vnd.github+json', 'user-agent': 'filex-nightly', 'x-github-api-version': '2022-11-28' };
  const token = cfg.github.tokenFile ? readText(cfg.github.tokenFile).trim() : '';
  if (token) headers.authorization = `Bearer ${token}`;
  try {
    const res = await fetch(`https://api.github.com/${p}`, { headers, signal: AbortSignal.timeout(30_000) });
    const json = await res.json().catch(() => null);
    return res.ok ? { ok: true, status: res.status, json } : { ok: false, status: res.status, error: `HTTP ${res.status}${json?.message ? ` ${json.message}` : ''}` };
  } catch (e) {
    return { ok: false, error: e?.cause?.code ?? e?.message ?? String(e) };
  }
}

/** The repository's runs that are not over: what releaseBusy reads. Throws when GitHub cannot be asked. */
async function activeRuns(cfg) {
  const out = [];
  for (const status of ['in_progress', 'queued']) {
    const r = await ghApi(cfg, `repos/${cfg.github.repo}/actions/runs?status=${status}&per_page=100`);
    if (!r.ok) throw new Error(`the runs ${status} on ${cfg.github.repo}: ${r.error}`);
    out.push(...(r.json?.workflow_runs ?? []));
  }
  return out;
}

/**
 * GitHub's verdict on a nightly commit (ciVerdict): its newest ci.yml run of
 * a push, and that run's jobs, every page. { state: unknown, why } when
 * GitHub cannot be asked.
 */
export async function followCi(cfg, sha) {
  const { repo, ci } = cfg.github;
  const r = await ghApi(cfg, `repos/${repo}/actions/workflows/${ci}/runs?head_sha=${sha}&event=push&per_page=20`);
  if (!r.ok) return { state: 'unknown', why: `${ci} runs of ${short(sha)}: ${r.error}` };
  const runs = [...(r.json?.workflow_runs ?? [])].sort((a, b) => Date.parse(b.created_at ?? '') - Date.parse(a.created_at ?? ''));
  const run = runs[0] ?? null;
  if (!run || run.status !== 'completed') return ciVerdict({ run });
  const jobs = [];
  for (let page = 1; page <= 10; page += 1) {
    const j = await ghApi(cfg, `repos/${repo}/actions/runs/${run.id}/jobs?filter=latest&per_page=100&page=${page}`);
    if (!j.ok) return { state: 'unknown', why: `the jobs of run ${run.id}: ${j.error}` };
    const got = j.json?.jobs ?? [];
    jobs.push(...got.map((x) => ({ name: x.name, conclusion: x.conclusion, html_url: x.html_url })));
    if (!got.length || jobs.length >= (j.json?.total_count ?? 0)) break;
  }
  return ciVerdict({ run, jobs });
}

// ── run ─────────────────────────────────────────────────────────────────────

async function runNight(cfg, args) {
  const nowMs = Date.now();
  fs.mkdirSync(cfg.state, { recursive: true });
  const stateFile = path.join(cfg.state, 'state.json');
  const state = readJson(stateFile) ?? {};
  const tonightFile = path.join(cfg.state, 'tonight.json');
  const tonight = { night: localDate(nowMs, cfg.tz), at: iso(nowMs), pid: process.pid, phase: 'fetch', decision: null };
  const save = () => {
    if (!args.dryRun) writeJson(tonightFile, tonight);
  };
  const record = (fields, code) => {
    Object.assign(tonight, fields, { phase: 'done', ended: iso() });
    log(`${tonight.decision}: ${tonight.why ?? ''}`);
    if (!args.dryRun) {
      save();
      appendHistory(cfg.state, nightRecord(tonight), cfg.keepHistory);
    }
    return code;
  };
  save();
  log(`night ${tonight.night}: ${cfg.remote}/${cfg.branch} into ${cfg.src}`);

  // 1. The private main, as it is now.
  const main = fetchMain(cfg);
  if (main.error) return record({ decision: 'error', why: main.error }, 2);
  const { head } = main;
  tonight.sha = head;
  tonight.subject = main.subject;
  tonight.prev_sha = state.last_export?.sha ?? null;

  // 2. Whether it pushes at all.
  const d = decidePush({ head, last: state.last_export, force: !!args.force });
  if (!d.push) return record({ decision: 'skipped', why: d.why }, 0);
  const author = parseAuthor(cfg.push.author);
  if (!author) return record({ decision: 'error', why: 'NIGHTLY_COMMIT_AUTHOR is not set: "Name <address>", the account the public repository\'s commits are by' }, 2);
  if (cfg.push.busy === 'skip' && !args.force) {
    let busy = null;
    try {
      busy = releaseBusy(await activeRuns(cfg), { nowMs: Date.now() });
    } catch (e) {
      // GitHub that cannot be asked holds nothing back: the push is the night's one job.
      log(`could not ask GitHub whether a release runs (${e.message}): pushing anyway`);
    }
    if (busy) return record({ decision: 'deferred', why: `GitHub is testing a release - ${busy.why}; the next night pushes`, busy }, 0);
  }
  // A change to the S3 code is said in the morning: nothing tests it on its own.
  const s3 = s3Since(cfg, state.last_export?.sha ?? null, head);
  if (s3) tonight.s3 = s3;
  if (args.dryRun) {
    log(`would push: ${d.why}; export into ${cfg.push.publicDir}, ${cfg.push.container ? `push from ${cfg.push.container}` : 'push from this host'} to ${cfg.push.branch}${s3 ? `; the S3 code changed (${s3.files.length} file(s))` : ''}`);
    return 0;
  }

  // 3. The checkout at main, clean: the export archives its HEAD.
  tonight.phase = 'checkout';
  save();
  const unclean = checkoutClean(cfg, head);
  if (unclean) return record({ decision: 'error', why: unclean }, 2);

  // 4. The export, committed on the public main.
  tonight.phase = 'export';
  save();
  const out = path.join(cfg.state, 'tonight');
  fs.rmSync(out, { recursive: true, force: true });
  fs.mkdirSync(out, { recursive: true });
  const ex = exportNight(cfg, { head, out, author, date: tonight.night });
  if (ex.state === 'unchanged') {
    state.last_export = { sha: head, result: 'unchanged', at: iso() };
    writeJson(stateFile, state);
    return record({ decision: 'unchanged', why: `the export of ${short(head)} is the public main as it is (${short(ex.base)}): nothing new to test`, log: ex.log }, 0);
  }
  if (ex.state !== 'committed') return record({ decision: ex.state === 'refused' ? 'refused' : 'error', why: ex.why, log: ex.log }, ex.state === 'refused' ? 1 : 2);

  // 5. The push.
  tonight.phase = 'push';
  save();
  const pushed = pushNight(cfg, { src: cfg.src, out, sha: ex.sha });
  if (pushed.state !== 'pushed') return record({ decision: 'failed', why: pushed.why, log: pushed.log }, 1);
  const ymd = tonight.night.replace(/-/g, '');
  let version = '';
  try {
    version = nightlyVersion(readJson(path.join(cfg.push.publicDir, 'packages', 'core', 'package.json'))?.version, ymd, ex.sha);
  } catch (e) {
    log(`the version could not be read: ${e.message}`);
  }
  const url = `https://github.com/${cfg.github.repo}/commit/${ex.sha}`;
  tonight.push = { sha: ex.sha, base: ex.base, branch: cfg.push.branch, version, ymd, url, at: iso() };
  state.last_export = { sha: head, result: 'pushed', at: iso() };
  state.last_push = { revision: ex.sha, sha: head, base: ex.base, version, ymd, at: tonight.push.at };
  writeJson(stateFile, state);
  return record({ decision: 'pushed', why: d.why, log: pushed.log }, 0);
}

/**
 * The commit of main and its subject, fetched into the nightly's checkout, or
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

/** What of base..head touches the S3 code (s3Touched): { since, files }, or null. */
function s3Since(cfg, base, head) {
  if (!base || git(cfg.src, ['cat-file', '-e', `${base}^{commit}`]).status !== 0) return null;
  const files = git(cfg.src, ['diff', '--name-only', `${base}..${head}`]);
  if (files.status !== 0) return null;
  const goMod = git(cfg.src, ['diff', `${base}..${head}`, '--', 'backend/go.mod']);
  const hit = s3Touched(files.stdout.split('\n').filter(Boolean), goMod.stdout);
  return hit.length ? { since: base, files: hit } : null;
}

/**
 * scripts/chain/nightly-export.sh from tonight's tree: the export, committed
 * on the public main, and the commit as a bundle. { state: committed, sha,
 * base } | { state: unchanged, base } | { state: refused | error, why }, with
 * `log`.
 */
function exportNight(cfg, { head, out, author, date }) {
  const logFile = path.join(out, 'nightly-export.log');
  const messageFile = path.join(out, 'message.txt');
  // The message names the public commit it is made on, known only once the
  // script fetched: it fills @BASE@ in.
  fs.writeFileSync(messageFile, nightlyCommitMessage({ date, base: '@BASE@' }));
  log(`export of ${short(head)} into ${cfg.push.publicDir}`);
  const r = spawnSync('bash', [path.join(cfg.src, 'scripts', 'chain', 'nightly-export.sh')], {
    cwd: cfg.src,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    timeout: 30 * 60_000,
    env: childEnv({
      NE_SRC: cfg.src,
      NE_SHA: head,
      NE_PUBLIC_DIR: cfg.push.publicDir,
      NE_PUBLIC_REMOTE: cfg.push.remote,
      NE_OUT: out,
      NE_AUTHOR_NAME: author.name,
      NE_AUTHOR_EMAIL: author.email,
      NE_MESSAGE_FILE: messageFile,
    }),
  });
  fs.writeFileSync(logFile, `${r.stdout ?? ''}${r.stderr ?? ''}`);
  const [sha, onBase] = readText(path.join(out, 'commit.txt')).trim().split(/\s+/);
  if (r.status === 0 && /^[0-9a-f]{40}$/.test(sha ?? '')) return { state: 'committed', sha, base: onBase ?? '', log: logFile };
  if (r.status === 3) return { state: 'unchanged', base: onBase ?? sha ?? '', log: logFile };
  if (r.status === 1) return { state: 'refused', why: `the export refused ${short(head)}: ${tailOf(path.join(out, 'export.log'), 2) || lastLine(r.stderr)}`, log: logFile };
  return { state: 'error', why: `nightly-export.sh exited ${r.status ?? r.signal}: ${lastLine(r.stderr) || lastLine(r.stdout)}`, log: logFile };
}

/**
 * The night's commit to the public repository's `nightly` branch, through
 * the export's pre-push gate. From NIGHTLY_PUSH_CONTAINER: the bundle is
 * copied in and scripts/chain/nightly-push.sh (tonight's, sent on stdin, never
 * written into the command) fetches it into the container's own clone,
 * installs the gate there and pushes under NIGHTLY_PUSH_WRAP. With no
 * container: from the clone here, the gate installed from tonight's tree.
 * Returns { state: pushed | failed, why?, log }.
 */
function pushNight(cfg, { src, out, sha }) {
  const logFile = path.join(out, 'push.log');
  const p = cfg.push;
  if (!p.container) {
    const gate = spawnSync('bash', [path.join(src, 'scripts', 'hooks', 'install-export-hook.sh'), p.publicDir], { encoding: 'utf8', env: childEnv() });
    fs.writeFileSync(logFile, `${gate.stdout ?? ''}${gate.stderr ?? ''}`);
    if (gate.status !== 0) return { state: 'failed', why: `the export gate could not be installed in ${p.publicDir}: ${lastLine(gate.stderr) || lastLine(gate.stdout)}`, log: logFile };
    log(`pushing ${short(sha)} to ${p.branch} from ${p.publicDir}`);
    const r = git(p.publicDir, ['push', '--force', 'origin', `refs/heads/filex-nightly:refs/heads/${p.branch}`]);
    fs.appendFileSync(logFile, `${r.stdout ?? ''}${r.stderr ?? ''}`);
    return r.status === 0 ? { state: 'pushed', log: logFile } : { state: 'failed', why: `git push failed: ${lastLine(r.stderr)}`, log: logFile };
  }
  const up = spawnSync('docker', ['inspect', '-f', '{{.State.Running}}', p.container], { encoding: 'utf8' });
  if (up.status !== 0 || up.stdout.trim() !== 'true') {
    fs.writeFileSync(logFile, `${up.stdout ?? ''}${up.stderr ?? ''}`);
    return { state: 'failed', why: `${p.container}, the container the push is made from, is not running`, log: logFile };
  }
  const inside = `/tmp/filex-nightly-${process.pid}.bundle`;
  const cp = spawnSync('docker', ['cp', path.join(out, 'nightly.bundle'), `${p.container}:${inside}`], { encoding: 'utf8' });
  if (cp.status !== 0) {
    fs.writeFileSync(logFile, `${cp.stdout ?? ''}${cp.stderr ?? ''}`);
    return { state: 'failed', why: `the bundle could not be copied into ${p.container}: ${lastLine(cp.stderr)}`, log: logFile };
  }
  log(`pushing ${short(sha)} to ${p.branch} from ${p.container}`);
  const script = readText(path.join(src, 'scripts', 'chain', 'nightly-push.sh'));
  const argv = ['exec', '-i', ...(p.user ? ['-u', p.user] : []), p.container, 'bash', '-l', '-s', '--', inside, sha, p.branch, p.wrap, p.dir, p.gateSrc, p.remote];
  const r = spawnSync('docker', argv, { input: script, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, timeout: 40 * 60_000 });
  fs.writeFileSync(logFile, `${r.stdout ?? ''}${r.stderr ?? ''}`);
  spawnSync('docker', ['exec', '-u', '0', p.container, 'rm', '-f', inside], { encoding: 'utf8' });
  if (r.status === 0) return { state: 'pushed', log: logFile };
  return { state: 'failed', why: `the push from ${p.container} ended ${r.status ?? r.signal}: ${lastLine(r.stderr) || lastLine(r.stdout) || r.error?.message || ''}`, log: logFile };
}

// ── the morning's follow-up ─────────────────────────────────────────────────

/**
 * The ORAS client (NIGHTLY_ORAS_IMAGE) in a container, `dir` mounted as its
 * working directory. Anonymous: everything it reads is public. Returns
 * spawnSync's.
 */
function oras(cfg, args, { dir = '', timeoutMs = 120_000 } = {}) {
  const mounts = dir ? ['-v', `${dir}:/w`, '-w', '/w'] : [];
  return spawnSync('docker', ['run', '--rm', ...mounts, cfg.desktop.oras, ...args], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, timeout: timeoutMs });
}

/** A manifest's annotations and layers: { annotations, layers }, or { error }. */
function annotationsOf(cfg, ref) {
  const r = oras(cfg, ['manifest', 'fetch', ref]);
  if (r.status !== 0) return { error: lastLine(r.stderr) || `exit ${r.status}` };
  try {
    const m = JSON.parse(r.stdout);
    return { annotations: m.annotations ?? {}, layers: m.layers ?? [] };
  } catch (e) {
    return { error: `not a manifest: ${e.message}` };
  }
}

/** The newest run of the nightly workflow: { status, conclusion, url }, or null. */
async function newestRun(cfg) {
  const r = await ghApi(cfg, `repos/${cfg.desktop.workflowRepo}/actions/workflows/${cfg.desktop.workflow}/runs?per_page=1`);
  const run = r.ok ? r.json?.workflow_runs?.[0] : null;
  return run ? { status: run.status, conclusion: run.conclusion, url: run.html_url } : null;
}

/**
 * What the nightly image (NIGHTLY_IMAGE_REPO:nightly) says it is, from its
 * labels (the workflow sets the commit and the version): { state: built |
 * other | unknown, revision, version, why }, against `source` (last_push).
 */
export function followImage(cfg, source) {
  if (!source?.revision) return null;
  const ref = `${cfg.image.repo}:nightly`;
  let r = oras(cfg, ['manifest', 'fetch-config', ref]);
  if (r.status !== 0) r = oras(cfg, ['manifest', 'fetch-config', '--platform', 'linux/amd64', ref]);
  if (r.status !== 0) return { state: 'unknown', why: `${ref}: ${lastLine(r.stderr) || `exit ${r.status}`}` };
  let labels = {};
  try {
    labels = JSON.parse(r.stdout)?.config?.Labels ?? {};
  } catch (e) {
    return { state: 'unknown', why: `${ref}: not an image config (${e.message})` };
  }
  const revision = labels['org.opencontainers.image.revision'] ?? '';
  const version = labels['org.opencontainers.image.version'] ?? '';
  return { state: revision === source.revision ? 'built' : 'other', revision, version };
}

/** The Snap Store's public channel map of the snap, or null. */
async function channelMap(cfg) {
  try {
    const res = await fetch(`https://api.snapcraft.io/v2/snaps/info/${cfg.snap.name}?fields=version,revision`, { headers: { 'Snap-Device-Series': '16' }, signal: AbortSignal.timeout(30_000) });
    return res.ok ? (await res.json())?.['channel-map'] ?? [] : null;
  } catch {
    return null;
  }
}

/**
 * The morning's follow-up of the last nightly (#266): what GitHub's runners
 * made of it (followVerdict, `ci` its CI verdict); built, the night's
 * manifest read from the artifact, the filex.sh/nightly page made from it and
 * site/nightly.src.html (renderNightlyPage) and published by NIGHTLY_SITE_CMD
 * once per revision; and whether edge serves the snaps the workflow uploaded.
 * Never throws: a record for the morning report (followLines).
 */
export async function followDesktop(cfg, { nowMs = Date.now(), dry = false, ci = null } = {}) {
  if (cfg.desktop.mode !== 'github') return null;
  const stateFile = path.join(cfg.state, 'state.json');
  const state = readJson(stateFile) ?? {};
  const source = state.last_push;
  if (!source?.revision) return null;
  const nightly = annotationsOf(cfg, `${cfg.desktop.repo}:nightly`);
  const attempted = annotationsOf(cfg, `${cfg.desktop.repo}:attempted`);
  const rev = (a) => (a.annotations ? { revision: a.annotations['org.opencontainers.image.revision'] } : null);
  const f = followVerdict({ source, built: rev(nightly), attempted: rev(attempted), run: await newestRun(cfg), ci, nowMs });
  if (!f || f.state !== 'built') return f;
  // The night's manifest, from the artifact itself.
  const layer = (nightly.layers ?? []).find((l) => l.annotations?.['org.opencontainers.image.title'] === 'nightly.json');
  let manifest = null;
  if (layer) {
    const r = oras(cfg, ['blob', 'fetch', `${cfg.desktop.repo}@${layer.digest}`, '--output', '-']);
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
  f.snaps = manifest.snap ?? {};
  const uploaded = SNAP_ARCHES.filter((a) => ['released', 'review'].includes(f.snaps[a]?.state));
  if (uploaded.length) {
    const map = await channelMap(cfg);
    if (map) {
      f.edge = {};
      for (const arch of uploaded) {
        const e = snapEdgeState(map, { version: manifest.version, revision: f.snaps[arch].revision, at: manifest.built_at }, { arch, nowMs });
        if (e && e.state !== 'live') f.edge[arch] = e;
      }
    }
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
  const r = oras(cfg, ['resolve', `${cfg.desktop.repo}:nightly`]);
  const d = r.status === 0 ? r.stdout.trim() : '';
  return /^sha256:[0-9a-f]{64}$/.test(d) ? d : '';
}

// ── status by hand ──────────────────────────────────────────────────────────

function status(cfg) {
  const tonight = readJson(path.join(cfg.state, 'tonight.json'));
  const state = readJson(path.join(cfg.state, 'state.json')) ?? {};
  const history = readHistory(cfg.state);
  const out = [];
  if (tonight) {
    out.push(`tonight ${tonight.night}: ${tonight.phase}${tonight.decision ? `, ${tonight.decision}` : ''}${tonight.why ? ` (${tonight.why})` : ''} on ${short(tonight.sha)}${tonight.phase !== 'done' ? `, pid ${tonight.pid} ${alive(tonight.pid) ? 'alive' : 'gone'}` : ''}`);
  } else out.push('no night recorded yet');
  for (const r of history.slice(-7).reverse()) {
    const ci = r.ci ? ` ci.yml ${r.ci.state}` : '';
    out.push(`  ${r.night} ${short(r.sha)} ${r.decision}${r.push ? ` -> ${short(r.push.sha)} ${r.push.version ?? ''}` : ''}${ci}`);
  }
  if (state.last_push) out.push(`last nightly: ${short(state.last_push.revision)} (${state.last_push.version}), from ${short(state.last_push.sha)} at ${state.last_push.at}`);
  if (state.last_site) out.push(`last filex.sh/nightly page: ${state.last_site.version} at ${state.last_site.at}`);
  console.log(out.join('\n'));
  return 0;
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
    console.error('the nightly runs on Linux with Docker (status works anywhere)');
    return 2;
  }
  if (args.cmd === 'follow') {
    const state = readJson(path.join(cfg.state, 'state.json')) ?? {};
    const ci = state.last_push?.revision ? await followCi(cfg, state.last_push.revision) : null;
    const f = await followDesktop(cfg, { dry: !!args.dryRun, ci });
    const image = followImage(cfg, state.last_push);
    const lines = [ci ? `ci.yml on ${short(state.last_push.revision)}: ${ci.state}${ci.url ? ` - ${ci.url}` : ''}` : 'no nightly pushed yet', imageLine(image, f, `${cfg.image.repo}:nightly`), ...followLines(f)].filter(Boolean);
    console.log(lines.join('\n'));
    return f && followTrouble(f) ? 1 : 0;
  }
  let release;
  try {
    release = takeOwnLock(path.join(cfg.state, 'nightly.lock'));
  } catch (e) {
    console.error(e.message);
    return 2;
  }
  try {
    return await runNight(cfg, args);
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
