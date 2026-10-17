// The autonomous release round (#165, 0.57): a release a person asked for,
// cut with nobody at the keyboard - in the release workspace on the build
// host, started by a timer. It drives `pnpm release` (scripts/release.mjs),
// does the steps that tool leaves to a person - the export commit and both
// mains, the signed tags, the tag pushes, the ship (filex-ship.sh), a server
// patch's matrix watch - and tells a person how every round ended.
//
//   node scripts/train/auto.mjs request X.Y.Z --summary "one line" [--commit SHA]
//                                   [--profile minor|patch|full|server] [--task N]
//        queue a release of the commit a person audited (the README, the
//        screenshots, every page that describes the product: the audit stage
//        of `pnpm release`); --commit defaults to the private remote's main
//   node scripts/train/auto.mjs run [--dry-run]
//        one round: nothing queued is nothing done (exit 0); --dry-run says
//        what the round would do now and runs nothing
//   node scripts/train/auto.mjs status        the request and the last rounds
//   node scripts/train/auto.mjs report        the morning report, said to a person
//   node scripts/train/auto.mjs cancel        drop the request (undoes nothing)
//
//   --env FILE   settings (default: $FILEX_TRAIN_ENV, else
//                ~/.config/filex-train.env; the keys and what each does:
//                scripts/train/train.env.example, "The autonomous round")
//
// Run it with the narrow GitHub token in its environment (gh reads the runs,
// starts the dry run, and the public pushes go over HTTPS); the signing key
// is opened for the length of the signing alone, by FILEX_AUTO_SIGN_CMD.
// Where it runs and what starts it: docs/CONTRIBUTING.md, "The autonomous
// release round", and scripts/train/install-auto.sh.
//
// Exit: 0 released, or nothing to do · 1 red · 2 usage, or it could not start
// · 3 stopped for a person (the notification says what to do).
//
// ⚠ What it never does: confirm the audit on any commit but the one the
// request names, confirm a file the export withheld, move or delete a tag,
// push with --force, or roll back anything a remote has. A red stage before
// the mains were pushed is rolled back - the stamp commit dropped, the
// export's changes put aside in a stash, the release's record kept beside -
// and the number is not spent. After that, the round stops and says where.
//
// ⚠ Why (the owner, 2026-10-11 and 2026-10-15): a release came out of one PC,
// by hand, turn after turn - "if this PC burns, or I am out, no version". The
// code is written anywhere; the release goes out from the release workspace,
// by itself, once the work for it is done. The audit stays a person's: the
// request names the commit it was done on, and nothing else is released.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { announce } from '../lib/notify.mjs';
import { loadSettings } from '../lib/settings.mjs';
import { PROFILES, closingKeywords, compareVersions, deferredSpecs, newestTag, parseVersion, versionProblems } from '../release/checks.mjs';
import { git, gitOut, loadState, lsRemote, revParse, saveState, slash, stateFile, takeLock } from '../release/engine.mjs';
import { STAGES } from '../release/stages.mjs';
import { tagMessages } from '../release/surfaces.mjs';
import { Tail } from './when-done.mjs';
import { duration, usageOf } from './cli.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(HERE, '..', '..');

/** The stages before the mains are pushed: a red one there is rolled back. */
export const BEFORE_LAND = ['preflight', 'audit', 'docs', 'stamp', 'pretag', 'export'];

/** flock's exit code when the build host's lock stayed taken (nothing ran). */
export const LOCK_BUSY = 75;

/** What the round needs on this machine besides node and git. */
export const TOOLS = ['bash', 'gh', 'flock'];

/** The release's record, read once the round is under way. */
const RELEASE_HIGHLIGHTS = 'docs-site/data/release-highlights.json';

// ── settings ─────────────────────────────────────────────────────────────────

/** The round's settings, with their defaults (train.env.example says what each does). */
export function autoSettings(env = process.env) {
  const num = (v, d) => (v !== undefined && v !== '' && Number.isFinite(Number(v)) ? Number(v) : d);
  return {
    repo: env.FILEX_AUTO_REPO || REPO,
    minFreeGiB: num(env.FILEX_AUTO_MIN_FREE_GB, 30),
    diskPath: env.FILEX_AUTO_DISK_PATH || '/',
    chainLock: env.CHAIN_LOCK || '',
    lockWaitMin: num(env.FILEX_AUTO_LOCK_WAIT_MIN, 120),
    // gate: 45 minutes a pass (the release tool's own wait); ci: up to 2 hours.
    waits: num(env.FILEX_AUTO_WAITS, 8),
    signCmd: env.FILEX_AUTO_SIGN_CMD || 'with-signing-key',
    signTtl: num(env.FILEX_AUTO_SIGN_TTL, 600),
    task: env.FILEX_AUTO_TASK || '',
    nodeOptions: env.NODE_OPTIONS || '--max-old-space-size=4096',
  };
}

// ── the request ──────────────────────────────────────────────────────────────

/** Where the round keeps its request and its records: beside the release tool's, inside .git. */
export function autoDir(gitDir) {
  return path.join(gitDir, 'filex-release', 'auto');
}

/**
 * What is wrong with a request, [] when nothing. A request is a version, the
 * commit a person audited, the one line the export commit is given, and
 * optionally the profile and the work task the signing is recorded on.
 */
export function requestProblems(req) {
  const out = [];
  if (!req || typeof req !== 'object') return ['no request'];
  out.push(...versionProblems(req.version, null));
  if (!/^[0-9a-f]{40}$/.test(String(req.commit ?? ''))) out.push(`"${req.commit ?? ''}" is not a full commit id: the request names the commit the audit was done on`);
  const summary = String(req.summary ?? '').trim();
  if (!summary) out.push('no summary: the export commit is "vX.Y.Z - <summary>"');
  else if (summary.includes('\n')) out.push('the summary is one line');
  const kw = closingKeywords(summary);
  if (kw.length) out.push(`the summary says ${kw.map((k) => `"${k}"`).join(', ')}: on GitHub that CLOSES the issue - write "(#N)"`);
  if (req.profile && !PROFILES.includes(req.profile)) out.push(`profile "${req.profile}": one of ${PROFILES.join(', ')}`);
  if (req.task !== undefined && req.task !== null && req.task !== '' && !/^\d+$/.test(String(req.task))) out.push(`task "${req.task}" is not a task number`);
  return out;
}

/** The next version after the newest release tag: a `patch` or a `minor` step. */
export function nextVersion(tags, bump) {
  const newest = newestTag(tags);
  const v = parseVersion(newest ?? 'v0.0.0');
  if (bump === 'minor') return `${v.major}.${v.minor + 1}.0`;
  if (bump === 'patch') return `${v.major}.${v.minor}.${v.patch + 1}`;
  throw new Error(`--bump ${bump}: patch or minor`);
}

/** Whether a release-highlights.json text has the tag's summary. */
export function hasHighlight(text, tag) {
  try {
    const entry = JSON.parse(text)[tag];
    return typeof entry === 'string' && entry.trim() !== '';
  } catch {
    return false;
  }
}

// ── where a release stands ───────────────────────────────────────────────────

/** The first stage of a release's record that is not done (or null: every stage is). */
export function stageAt(state) {
  for (const s of STAGES) {
    const rec = state?.stages?.[s.id];
    if (!rec || rec.status !== 'done') return { id: s.id, status: rec?.status ?? 'not run' };
  }
  return null;
}

/** Whether a stage of the record is done. */
const isDone = (state, id) => state?.stages?.[id]?.status === 'done';

/**
 * What the round does after one run of the release tool, from its exit code
 * and the record it left:
 *   { do: 'done' }                       released (every stage done)
 *   { do: 'ack', acks }                  run again with these confirmations
 *   { do: 'land' | 'sign' | 'push' | 'ship' }   a person's step, done here
 *   { do: 'wait', stage }                run again: GitHub has not finished
 *   { do: 'held', held }                 published; a store holds the read-back
 *   { do: 'red', stage, why }            a gate is red
 *   { do: 'person', stage, why }         a step the round does not take
 * `done` is what the round already did this round (each person step once:
 * a second time the stage still waits is for a person), `waited` how often
 * it ran again for a wait.
 */
export function decide({ code, state, request, head, deferredAcks = [], done = new Set(), waited = 0, waits = 8 }) {
  const at = stageAt(state);
  if (code === 0) return at ? { do: 'red', stage: at.id, why: `the release tool ended green, and ${at.id} is not done` } : { do: 'done' };
  if (code === 1) {
    const ci = state?.stages?.ci;
    if (at?.id === 'ci' && ci?.status === 'red' && Array.isArray(ci.held) && ci.held.length) {
      return done.has('ship') ? { do: 'held', held: ci.held } : { do: 'ship', held: ci.held };
    }
    return { do: 'red', stage: at?.id ?? '?', why: 'a gate is red' };
  }
  if (code !== 3) return { do: 'red', stage: at?.id ?? '?', why: `the release tool exited ${code}` };
  if (!at) return { do: 'red', stage: '?', why: 'the release tool waits, and every stage is done' };
  const once = (step, why) => (done.has(step) ? { do: 'person', stage: at.id, why } : { do: step });
  switch (at.id) {
    case 'audit':
      if (head !== request.commit) return { do: 'person', stage: 'audit', why: `the audit was done on ${request.commit.slice(0, 10)}, and the checkout is on ${String(head).slice(0, 10)}: audit this commit, then queue it again` };
      return done.has('ack:audit') ? { do: 'person', stage: 'audit', why: 'confirmed, and the audit stage still waits' } : { do: 'ack', acks: ['audit'] };
    case 'pretag': {
      const left = deferredAcks.filter((a) => !done.has(`ack:${a}`));
      if (deferredAcks.length && left.length) return { do: 'ack', acks: left };
      return { do: 'person', stage: 'pretag', why: 'the pretag waits for something the round does not confirm' };
    }
    case 'export':
      return { do: 'person', stage: 'export', why: 'the export withheld files from the public tree: only a person confirms that list (--ack export-withheld)' };
    case 'land':
      return once('land', 'the export commit and both mains were pushed, and the land stage still waits');
    case 'gate':
    case 'ci':
      return waited < waits ? { do: 'wait', stage: at.id } : { do: 'person', stage: at.id, why: `GitHub has not finished after ${waited} waits` };
    case 'sign':
      return once('sign', 'the tags were signed, and the sign stage still waits');
    case 'push':
      return once('push', 'the tags were pushed, and the push stage still waits');
    case 'deploy':
      if (!done.has('ship')) return { do: 'ship' };
      return done.has('ack:deploy') ? { do: 'person', stage: 'deploy', why: 'confirmed, and the deploy stage still waits' } : { do: 'ack', acks: ['deploy'] };
    default:
      return { do: 'person', stage: at.id, why: `the ${at.id} stage waits for a person` };
  }
}

// ── the person's steps, done here ────────────────────────────────────────────

/**
 * The signing script: the private tag on the release commit, the public one
 * on the export commit, each checked at once. A tag that is already there is
 * left for the sign stage to judge (it is never moved). Runs under the
 * signing command, which opens the key for its length alone.
 */
export function signScript({ repo, exp, tag, releaseCommit, exportCommit, server = false, have = { repo: false, exp: false } }) {
  const msgs = tagMessages(tag, { server })
    .map((m) => `-m '${m}'`)
    .join(' ');
  const lines = ['set -euo pipefail'];
  if (!have.repo) lines.push(`git -C '${slash(repo)}' tag -s '${tag}' ${msgs} ${releaseCommit}`);
  lines.push(`git -C '${slash(repo)}' tag -v '${tag}'`);
  if (!have.exp) lines.push(`git -C '${slash(exp)}' tag -s '${tag}' ${msgs} ${exportCommit}`);
  lines.push(`git -C '${slash(exp)}' tag -v '${tag}'`);
  return `${lines.join('\n')}\n`;
}

/**
 * How to undo a round that went red before the mains were pushed, or why it
 * cannot be: { ok, why, steps: [{ dir, args, what }] }. `facts`: the record,
 * the request, the private checkout's HEAD and its parent, what the remotes
 * hold (`remote`: main and whether the tag is there, both sides), and the
 * export checkout's HEAD, its parent, whether its tree has changes, and the
 * tree of its HEAD.
 */
export function rollbackPlan({ state, request, repo, exp, head, headParent, remote, expHead, expHeadParent, expDirty, expHeadTree }) {
  const no = (why) => ({ ok: false, why, steps: [] });
  if (remote.error) return no(`could not ask the remotes (${remote.error}): unknown is not "not pushed"`);
  if (remote.tag || remote.expTag) return no(`${state.tag} is on a remote`);
  if (state.releaseCommit && remote.main === state.releaseCommit) return no('the release commit is on the private remote');
  if (isDone(state, 'land')) return no('the land stage is done: the mains were pushed');
  const steps = [];
  const made = [state.stampCommit, state.releaseCommit].filter(Boolean);
  if (head !== request.commit) {
    if (!made.includes(head) || headParent !== request.commit) return no(`the private checkout is on ${String(head).slice(0, 10)}: not the audited commit, nor the stamp on it - left for a person`);
    steps.push({ dir: repo, args: ['reset', '--keep', request.commit], what: `the stamp commit ${head.slice(0, 10)} dropped (the record keeps its id)` });
  }
  if (expDirty) steps.push({ dir: exp, args: ['stash', 'push', '--include-untracked', '-m', `filex auto: the export of ${state.tag}, rolled back`], what: "the export's changes put aside in a stash" });
  if (expHead !== remote.expMain) {
    if (expHeadParent !== remote.expMain || !state.exportTree || expHeadTree !== state.exportTree) return no(`the public checkout is on ${String(expHead).slice(0, 10)}: not its remote's main, nor the export commit on it - left for a person`);
    steps.push({ dir: exp, args: ['reset', '--keep', remote.expMain], what: `the export commit ${expHead.slice(0, 10)} dropped (it was never pushed)` });
  }
  return { ok: true, why: '', steps };
}

// ── the morning report ───────────────────────────────────────────────────────

/** What a release left to a person, from its record: lines, [] when nothing. */
export function owed(state) {
  if (!state) return [];
  const out = [];
  for (const d of Object.values(state.deferred ?? {})) {
    if (!d.checked) out.push(`${d.gate}: deferred at the pretag; when it ran green elsewhere: --ack ${d.checkedAck}`);
  }
  const held = state.stages?.ci?.held;
  if (Array.isArray(held) && held.length) out.push(`held by a store: ${held.join(', ')} - the next round reads them again`);
  if (state.profile === 'server' && state.matrix?.state !== 'green') out.push(`GitHub's full matrix behind the server patch: ${state.matrix?.state ?? 'not read yet'} (auto_ok stays off until it is green)`);
  return out;
}

/** The morning report: the rounds since the last one, the request, what is owed. */
export function morningReport({ rounds, request, states = {} }) {
  const lines = [];
  if (!rounds.length) lines.push('no round since the last report');
  for (const r of rounds) {
    const took = r.ended && r.started ? ` in ${duration((Date.parse(r.ended) - Date.parse(r.started)) / 1000)}` : '';
    lines.push(`${r.tag ?? '-'}: ${r.result}${r.stage ? ` at ${r.stage}` : ''}${took}${r.why ? ` - ${r.why}` : ''}${r.rolledBack ? ' (rolled back: the number is not spent)' : ''}`);
  }
  if (request) lines.push('', `request: v${request.version} on ${String(request.commit).slice(0, 10)} - ${request.status}${request.why ? `: ${request.why}` : ''}`);
  const owedLines = Object.entries(states).flatMap(([tag, st]) => owed(st).map((l) => `${tag}: ${l}`));
  if (owedLines.length) lines.push('', "left to a person:", ...owedLines.map((l) => `  ${l}`));
  lines.push('', 'and after every release: the replies on the issues and pull requests it answers, the language packs (node scripts/langpacks.mjs release X.Y.Z)');
  return lines;
}

// ── the round ────────────────────────────────────────────────────────────────

/** Whether a command is on PATH (no shell). */
export function onPath(name, env = process.env) {
  const exts = process.platform === 'win32' ? ['', '.exe', '.cmd', '.bat'] : [''];
  for (const dir of String(env.PATH ?? '').split(path.delimiter)) {
    if (!dir) continue;
    for (const ext of exts) {
      try {
        if (fs.statSync(path.join(dir, name + ext)).isFile()) return true;
      } catch {
        /* not here */
      }
    }
  }
  return false;
}

/** Free space under a path, in GiB (null when it cannot be read). */
export function freeGiB(p) {
  try {
    const s = fs.statfsSync(p);
    return (s.bavail * s.bsize) / 1024 ** 3;
  } catch {
    return null;
  }
}

/**
 * Runs a command with its output in a log file (never a pipe: a push into a
 * pipe has hidden its own failure, lessons #722/#726), prints the log's last
 * lines, and returns { code, tail }.
 */
function step(log, cmd, args, { cwd, env } = {}) {
  fs.mkdirSync(path.dirname(log), { recursive: true });
  const fd = fs.openSync(log, 'a');
  fs.writeSync(fd, `\n$ ${[cmd, ...args].join(' ')}\n`);
  const r = spawnSync(cmd, args, { cwd, env: env ?? process.env, stdio: ['ignore', fd, fd], windowsHide: true });
  fs.closeSync(fd);
  const tail = new Tail(40);
  tail.push(fs.readFileSync(log, 'utf8'));
  const code = typeof r.status === 'number' ? r.status : 127;
  console.log(`  ${code === 0 ? 'ok  ' : `exit ${code}`}  ${cmd} ${args.slice(0, 4).join(' ')}${args.length > 4 ? ' ...' : ''}  (${slash(log)})`);
  return { code, tail: tail.text() };
}

function readJson(file) {
  try {
    return JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch {
    return null;
  }
}

function writeJson(file, value) {
  saveState(file, value);
}

/** The round itself: returns its record { result, stage, why, rolledBack, ... }. */
async function round(ctx) {
  const { cfg, repo, exp, plan, req, tag, version, dir, log, settings } = ctx;
  const gitDir = gitOut(repo, 'rev-parse', '--absolute-git-dir');
  const stFile = stateFile(gitDir, tag);
  const env = { ...process.env, NODE_OPTIONS: cfg.nodeOptions };
  const deferredAcks = deferredSpecs(plan).map((s) => s.ack);
  const record = { tag, started: new Date().toISOString(), steps: [] };
  const note = (what) => {
    record.steps.push({ at: new Date().toISOString(), what });
    console.log(`  ${what}`);
  };

  // A fresh release starts on the audited commit, with room on the disk.
  let state = loadState(stFile);
  if (!state) {
    const free = freeGiB(cfg.diskPath);
    if (free !== null && free < cfg.minFreeGiB) return { ...record, result: 'person', stage: 'preflight', why: `${free.toFixed(1)} GiB free under ${cfg.diskPath}, the round wants ${cfg.minFreeGiB} (FILEX_AUTO_MIN_FREE_GB)` };
    for (const [d, label] of [[repo, 'the private checkout'], [exp, 'the public checkout']]) {
      if (git(d, 'status', '--porcelain', '--untracked-files=normal').stdout.trim()) return { ...record, result: 'person', stage: 'preflight', why: `${label} (${slash(d)}) has changes: the round starts on a clean tree` };
      if (git(d, 'symbolic-ref', '--short', '-q', 'HEAD').stdout.trim() !== plan.branch) return { ...record, result: 'person', stage: 'preflight', why: `${label} is not on ${plan.branch}` };
      const f = git(d, 'fetch', '--quiet', 'origin', plan.branch);
      if (f.status !== 0) return { ...record, result: 'red', stage: 'preflight', why: `git fetch in ${label}: ${(f.stderr || f.stdout).trim()}` };
      const m = git(d, 'merge', '--ff-only', '--quiet', `origin/${plan.branch}`);
      if (m.status !== 0) return { ...record, result: 'person', stage: 'preflight', why: `${label} cannot fast-forward to origin/${plan.branch}: ${(m.stderr || m.stdout).trim()}` };
    }
    const head = revParse(repo, 'HEAD');
    if (head !== req.commit) return { ...record, result: 'person', stage: 'audit', why: `main is on ${head?.slice(0, 10)}, and the audit was done on ${req.commit.slice(0, 10)}: audit the new commits, then queue it again` };
    const hl = git(repo, 'show', `${req.commit}:${RELEASE_HIGHLIGHTS}`);
    if (hl.status !== 0 || !hasHighlight(hl.stdout, tag)) return { ...record, result: 'person', stage: 'preflight', why: `${RELEASE_HIGHLIGHTS} has no "${tag}" summary on the audited commit: the ship publishes it (CONTRIBUTING, Release process, step 10)` };
  }

  const done = new Set();
  let waited = 0;
  let acks = [];
  for (let pass = 1; pass <= 40; pass++) {
    state = loadState(stFile);
    const fresh = !state;
    const args = [path.join(repo, 'scripts', 'release.mjs'), version];
    if (fresh) {
      if (req.profile) args.push('--profile', req.profile);
    } else args.push('--resume');
    for (const a of acks) args.push('--ack', a);
    for (const a of acks) done.add(`ack:${a}`);
    acks = [];
    // The stages that build hold the build host's lock; from the land on,
    // the release waits for GitHub and holds nothing (lesson #1562).
    const building = cfg.chainLock && !isDone(state, 'export');
    const cmd = building ? 'flock' : process.execPath;
    const cmdArgs = building ? ['-E', String(LOCK_BUSY), '-w', String(Math.round(cfg.lockWaitMin * 60)), cfg.chainLock, process.execPath, ...args] : args;
    const r = step(log, cmd, cmdArgs, { cwd: repo, env });
    if (building && r.code === LOCK_BUSY) return { ...record, result: 'busy', stage: stageAt(state)?.id ?? 'preflight', why: `the build host's lock (${cfg.chainLock}) stayed taken for ${cfg.lockWaitMin} min: nothing ran, the next round tries again` };
    state = loadState(stFile);
    const d = decide({ code: r.code, state, request: req, head: revParse(repo, 'HEAD'), deferredAcks, done, waited, waits: cfg.waits });
    if (d.do === 'done') {
      note(`${tag} released`);
      return { ...record, result: 'released', profile: state?.profile };
    }
    if (d.do === 'ack') {
      note(`confirming ${d.acks.join(', ')}${d.acks.includes('audit') ? ` (the audit of ${req.commit.slice(0, 10)}, by the request of ${req.at})` : ''}`);
      acks = d.acks;
      continue;
    }
    if (d.do === 'wait') {
      waited += 1;
      note(`${d.stage}: GitHub has not finished, asking again (${waited}/${cfg.waits})`);
      continue;
    }
    if (d.do === 'land') {
      done.add('land');
      const c = step(log, 'git', ['-C', exp, 'commit', '--quiet', '-m', `${tag} - ${req.summary}`]);
      if (c.code !== 0 && !/nothing to commit/.test(c.tail)) return { ...record, result: 'red', stage: 'land', why: `the export commit: ${lastLine(c.tail)}` };
      for (const [d2, remote] of [[repo, plan.remote], [exp, plan.exportRemote]]) {
        const p = step(log, 'git', ['-C', d2, 'push', remote, plan.branch]);
        if (p.code !== 0) return { ...record, result: 'red', stage: 'land', why: `git push ${remote} ${plan.branch} in ${slash(d2)}: ${lastLine(p.tail)}` };
      }
      note(`landed: the export commit ${revParse(exp, 'HEAD')?.slice(0, 10)}, both mains pushed without a tag`);
      continue;
    }
    if (d.do === 'sign') {
      done.add('sign');
      const script = path.join(dir, `sign-${tag}.sh`);
      fs.writeFileSync(
        script,
        signScript({
          repo,
          exp,
          tag,
          releaseCommit: state.releaseCommit,
          exportCommit: state.exportHead,
          server: state.profile === 'server',
          have: { repo: !!revParse(repo, `refs/tags/${tag}`), exp: !!revParse(exp, `refs/tags/${tag}`) },
        }),
      );
      const task = req.task || cfg.task;
      const s = step(log, cfg.signCmd, ['--ttl', String(cfg.signTtl), ...(task ? ['--task', String(task)] : []), '--purpose', `filex ${tag}: the release tags`, '--', 'bash', script]);
      if (s.code !== 0) return { ...record, result: 'red', stage: 'sign', why: `signing: ${lastLine(s.tail)}` };
      note(`signed ${tag} on ${String(state.releaseCommit).slice(0, 10)} and ${String(state.exportHead).slice(0, 10)}`);
      continue;
    }
    if (d.do === 'push') {
      done.add('push');
      // One ref at a time, never --tags (lesson #55).
      for (const [d2, remote] of [[repo, plan.remote], [exp, plan.exportRemote]]) {
        const p = step(log, 'git', ['-C', d2, 'push', remote, `refs/tags/${tag}`]);
        if (p.code !== 0) return { ...record, result: 'red', stage: 'push', why: `git push ${remote} refs/tags/${tag}: ${lastLine(p.tail)}` };
      }
      note(`pushed ${tag} to both remotes`);
      continue;
    }
    if (d.do === 'ship') {
      done.add('ship');
      const shipLog = path.join(path.dirname(log), `ship-${tag}.log`);
      const recorded = fs.existsSync(path.join(gitDir, 'filex-ship', `${tag}.json`));
      const s = step(shipLog, 'bash', [path.join(repo, 'scripts', 'train', 'filex-ship.sh'), version, ...(recorded ? ['--resume'] : []), ...(settings.file ? ['--env', settings.file] : [])], { cwd: repo, env });
      if (s.code !== 0) return { ...record, result: 'red', stage: 'deploy', why: `the ship is red (its own rollback ran for a failed instance): ${lastLine(s.tail)}` };
      note(`shipped ${tag}`);
      // The ship commits the Releases page when it changed; it goes with the
      // next push of main, which is this one.
      if (git(repo, 'log', '-1', '--format=%s').stdout.trim() === `docs(releases): ${tag}`) {
        const p = step(log, 'git', ['-C', repo, 'push', plan.remote, plan.branch]);
        if (p.code !== 0) note(`the Releases page commit is not pushed: ${lastLine(p.tail)}`);
      }
      if (d.held) return { ...record, result: 'held', stage: 'ci', why: `published and shipped; held by ${d.held.join(', ')}` };
      continue;
    }
    if (d.do === 'held') return { ...record, result: 'held', stage: 'ci', why: `held by ${d.held.join(', ')}` };
    // red or a person: before the mains were pushed, nothing is spent.
    const stopped = { ...record, result: d.do === 'red' ? 'red' : 'person', stage: d.stage, why: `${d.why}\n${r.tail}` };
    if (d.do === 'red' && BEFORE_LAND.includes(d.stage)) {
      const back = rollBack({ state, req, repo, exp, plan, stFile, log });
      stopped.rolledBack = back.ok;
      stopped.why = `${stopped.why}\n${back.ok ? `rolled back: ${back.steps.map((s) => s.what).join('; ')}` : `NOT rolled back: ${back.why}`}`;
    }
    return stopped;
  }
  return { ...record, result: 'person', stage: stageAt(loadState(stFile))?.id, why: 'forty runs of the release tool, and it is not done' };
}

const lastLine = (text) => String(text).trim().split('\n').slice(-3).join(' | ');

/** Undoes a round that went red before the mains were pushed (rollbackPlan says how). */
function rollBack({ state, req, repo, exp, plan, stFile, log }) {
  if (!state) return { ok: true, why: '', steps: [{ what: 'nothing was recorded, nothing to undo' }] };
  const pr = lsRemote(repo, plan.remote);
  const er = lsRemote(exp, plan.exportRemote);
  const remote = {
    error: pr.error || er.error || '',
    main: pr.map?.get(`refs/heads/${plan.branch}`) ?? null,
    tag: !!pr.map?.get(`refs/tags/${state.tag}`),
    expMain: er.map?.get(`refs/heads/${plan.branch}`) ?? null,
    expTag: !!er.map?.get(`refs/tags/${state.tag}`),
  };
  const back = rollbackPlan({
    state,
    request: req,
    repo,
    exp,
    head: revParse(repo, 'HEAD'),
    headParent: revParse(repo, 'HEAD^'),
    remote,
    expHead: revParse(exp, 'HEAD'),
    expHeadParent: revParse(exp, 'HEAD^'),
    expDirty: !!git(exp, 'status', '--porcelain', '--untracked-files=all').stdout.trim(),
    expHeadTree: revParse(exp, 'HEAD^{tree}'),
  });
  if (!back.ok) return back;
  for (const s of back.steps) {
    const r = step(log, 'git', ['-C', s.dir, ...s.args]);
    if (r.code !== 0) return { ok: false, why: `git ${s.args.join(' ')}: ${lastLine(r.tail)}`, steps: [] };
  }
  // The record is kept beside, under another name: a new round starts fresh.
  fs.renameSync(stFile, stFile.replace(/\.json$/, `.rolled-back-${Date.now()}.json`));
  return back;
}

// ── the commands ─────────────────────────────────────────────────────────────

export function parseArgs(argv) {
  const o = { cmd: argv[0] ?? '', version: null, summary: '', commit: '', profile: '', task: '', bump: '', env: '', dry: false };
  for (let i = 1; i < argv.length; i++) {
    const a = argv[i];
    const next = () => {
      const v = argv[++i];
      if (v === undefined) throw new Error(`${a} needs a value`);
      return v;
    };
    if (a === '--summary') o.summary = next();
    else if (a === '--commit') o.commit = next();
    else if (a === '--profile') o.profile = next();
    else if (a === '--task') o.task = next();
    else if (a === '--bump') o.bump = next();
    else if (a === '--env') o.env = next();
    else if (a === '--dry-run') o.dry = true;
    else if (a === '-h' || a === '--help') o.help = true;
    else if (!a.startsWith('-') && !o.version) o.version = a;
    else throw new Error(`unknown option ${a}`);
  }
  if (!o.help && !['request', 'run', 'status', 'report', 'cancel'].includes(o.cmd)) throw new Error(`no such command "${o.cmd}": request, run, status, report or cancel`);
  return o;
}

async function main(argv) {
  let o;
  try {
    o = parseArgs(argv);
  } catch (e) {
    console.error(`auto: ${e.message}\n\n${usageOf(import.meta.url)}`);
    return 2;
  }
  if (o.help || argv.length === 0) {
    console.log(usageOf(import.meta.url));
    return 0;
  }
  const settings = loadSettings({ file: o.env, envVar: 'FILEX_TRAIN_ENV', fallback: 'filex-train.env' });
  const cfg = autoSettings(settings.env);
  const repo = cfg.repo;
  const gitDir = gitOut(repo, 'rev-parse', '--absolute-git-dir');
  const dir = autoDir(gitDir);
  const reqFile = path.join(dir, 'request.json');
  const roundsDir = path.join(dir, 'rounds');
  const tell = (title, lines, result) => announce(settings.env, { title, message: lines.join('\n'), result }, { log: (l) => console.error(`auto: ${l}`) });

  if (o.cmd === 'request') {
    let version = o.version;
    if (!version && o.bump) version = nextVersion(gitOut(repo, 'tag', '--list', 'v*').split('\n').filter(Boolean), o.bump);
    let commit = o.commit;
    if (!commit) {
      gitOut(repo, 'fetch', '--quiet', 'origin', 'main');
      commit = gitOut(repo, 'rev-parse', 'origin/main');
    } else commit = revParse(repo, `${commit}^{commit}`) ?? commit;
    const req = { version, commit, summary: o.summary.trim(), profile: o.profile || undefined, task: o.task || undefined, at: new Date().toISOString(), by: os.userInfo().username, status: 'queued' };
    const problems = requestProblems(req);
    const hl = git(repo, 'show', `${commit}:${RELEASE_HIGHLIGHTS}`);
    if (hl.status !== 0 || !hasHighlight(hl.stdout, `v${version}`)) problems.push(`${RELEASE_HIGHLIGHTS} on ${commit.slice(0, 10)} has no "v${version}" summary: write it (CONTRIBUTING, Release process, step 10) and audit that commit`);
    const tags = gitOut(repo, 'tag', '--list', 'v*').split('\n').filter(Boolean);
    const newest = newestTag(tags);
    if (newest && parseVersion(version) && compareVersions(version, newest) <= 0) problems.push(`v${version} is not after ${newest}`);
    if (problems.length) {
      console.error(`auto: the request is refused:\n  ${problems.join('\n  ')}`);
      return 2;
    }
    writeJson(reqFile, req);
    console.log(`queued v${version} on ${commit.slice(0, 10)}: the next round releases it (${slash(reqFile)})`);
    return 0;
  }

  if (o.cmd === 'cancel') {
    if (!fs.existsSync(reqFile)) return console.log('no request'), 0;
    fs.renameSync(reqFile, path.join(dir, `request.cancelled-${Date.now()}.json`));
    console.log('the request is dropped; nothing a round did is undone');
    return 0;
  }

  const rounds = () =>
    (fs.existsSync(roundsDir) ? fs.readdirSync(roundsDir) : [])
      .filter((f) => f.endsWith('.json'))
      .sort()
      .map((f) => ({ file: f, ...readJson(path.join(roundsDir, f)) }));

  if (o.cmd === 'status') {
    const req = readJson(reqFile);
    console.log(req ? `request: v${req.version} on ${req.commit.slice(0, 10)}, ${req.status}${req.why ? ` - ${req.why.split('\n')[0]}` : ''}` : 'no request');
    for (const r of rounds().slice(-5)) console.log(`  ${r.file}: ${r.result}${r.stage ? ` at ${r.stage}` : ''}${r.why ? ` - ${String(r.why).split('\n')[0]}` : ''}`);
    return 0;
  }

  if (o.cmd === 'report') {
    const markFile = path.join(dir, 'last-report');
    const since = fs.existsSync(markFile) ? fs.readFileSync(markFile, 'utf8').trim() : '';
    const fresh = rounds().filter((r) => !since || r.file > since);
    const req = readJson(reqFile);
    const states = {};
    for (const r of fresh) if (r.tag && !states[r.tag]) states[r.tag] = loadState(stateFile(gitDir, r.tag));
    const lines = morningReport({ rounds: fresh, request: req, states });
    console.log(lines.join('\n'));
    const red = fresh.some((r) => r.result === 'red' || r.result === 'person');
    if (!o.dry) {
      await tell(`filex release rounds: ${fresh.length ? fresh.map((r) => `${r.tag} ${r.result}`).join(', ') : 'none'}`, lines, red ? 'red' : fresh.length ? 'ok' : 'info');
      fs.mkdirSync(dir, { recursive: true });
      if (fresh.length) fs.writeFileSync(markFile, `${fresh.at(-1).file}\n`);
    }
    return 0;
  }

  // ── run ──
  const req = readJson(reqFile);
  if (!req || !['queued', 'running', 'held', 'watching'].includes(req.status)) {
    console.log(req ? `the request of v${req.version} is ${req.status}: nothing to do until it is queued again` : 'nothing queued: nothing to do');
    return 0;
  }
  const problems = requestProblems(req);
  if (problems.length) {
    console.error(`auto: the request is not one the round takes:\n  ${problems.join('\n  ')}`);
    return 2;
  }
  const missing = [...TOOLS.filter((t) => t !== 'flock' || cfg.chainLock), cfg.signCmd].filter((t) => !onPath(t, settings.env));
  if (!settings.env.GH_TOKEN) missing.push('GH_TOKEN (run the round under the GitHub token wrapper)');
  const tag = `v${req.version}`;
  if (o.dry) {
    const st = loadState(stateFile(gitDir, tag));
    console.log(`would release ${tag} (${req.profile ?? 'the profile pnpm release picks'}) from ${req.commit.slice(0, 10)}: ${st ? `resume at ${stageAt(st)?.id ?? 'the end'}` : 'a fresh start'}`);
    if (missing.length) console.log(`missing here: ${missing.join(', ')}`);
    return missing.length ? 2 : 0;
  }
  if (missing.length) {
    await tell(`filex release round ${tag}: cannot start`, [`missing here: ${missing.join(', ')}`], 'red');
    return 2;
  }
  const lock = takeLock(path.join(dir, 'round.lock'));
  if (lock.held) {
    console.log(`a round is running (pid ${lock.held})`);
    return 0;
  }
  process.on('exit', () => lock.release?.());

  const planModule = await import(pathToFileURL(path.join(repo, 'scripts', 'release', 'plan.mjs')).href);
  const plan = { branch: 'main', remote: 'origin', exportRemote: 'origin', ...(await planModule.default({ repo, version: req.version, tag })) };
  const exp = path.resolve(plan.exportDir ?? path.join(repo, '..', 'filex-export'));
  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  const runDir = path.join(roundsDir, `${stamp}-${tag}`);
  fs.mkdirSync(runDir, { recursive: true });
  const log = path.join(runDir, 'release.log');
  writeJson(reqFile, { ...req, status: 'running', why: undefined });
  if (req.status === 'queued') await tell(`filex release round ${tag}: started`, [`from ${req.commit.slice(0, 10)}: ${req.summary}`, `log: ${slash(log)}`], 'info');

  const t0 = Date.now();
  let rec;
  try {
    rec = await round({ cfg, repo, exp, plan, req, tag, version: req.version, dir: runDir, log, settings });
  } catch (e) {
    rec = { tag, started: new Date(t0).toISOString(), result: 'red', stage: '?', why: `the round crashed: ${e?.stack ?? e}` };
  }
  // A server patch: GitHub's full matrix behind it switches auto_ok on.
  if (rec.result === 'released' && rec.profile === 'server') {
    const w = step(path.join(runDir, 'matrix-watch.log'), process.execPath, [path.join(repo, 'scripts', 'release', 'matrix-watch.mjs'), req.version, ...(settings.file ? ['--env', settings.file] : [])], { cwd: repo });
    rec.matrix = w.code === 0 ? 'green, auto_ok on' : w.code === 1 ? 'red, auto_ok off' : w.code === 3 ? 'not finished' : 'could not read';
    if (w.code === 3) rec.result = 'watching';
  }
  rec.ended = new Date().toISOString();
  writeJson(path.join(roundsDir, `${stamp}-${tag}.json`), rec);
  const status = { released: 'done', held: 'held', watching: 'watching', busy: 'queued', red: 'stopped', person: 'stopped' }[rec.result] ?? 'stopped';
  writeJson(reqFile, { ...req, status, why: rec.why ? String(rec.why).split('\n')[0] : undefined, round: `${stamp}-${tag}` });
  const quiet = rec.result === 'busy' || (req.status === 'held' && rec.result === 'held') || (req.status === 'watching' && rec.result === 'watching');
  if (!quiet) {
    const head = { released: `released in ${duration((Date.now() - t0) / 1000)}`, held: 'published, held by a store', watching: "released; GitHub's matrix behind it not finished", red: `RED at ${rec.stage}`, person: `stopped at ${rec.stage}: a person` }[rec.result];
    await tell(`filex release round ${tag}: ${head}`, [...(rec.why ? [String(rec.why)] : []), ...(rec.matrix ? [`matrix: ${rec.matrix}`] : []), ...(rec.rolledBack ? ['rolled back: the number is not spent'] : []), `log: ${slash(log)}`, ...(status === 'stopped' ? ['', `when it is fixed: node scripts/train/auto.mjs request ${req.version} --summary "..." (on the commit a person audited)`] : [])], rec.result === 'released' ? 'ok' : rec.result === 'held' || rec.result === 'watching' ? 'waiting' : 'red');
  }
  return { released: 0, held: 0, watching: 0, busy: 0, red: 1, person: 3 }[rec.result] ?? 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).then(
    (code) => process.exit(code),
    (e) => {
      console.error(`auto: ${e?.stack ?? e}`);
      process.exit(2);
    },
  );
}
