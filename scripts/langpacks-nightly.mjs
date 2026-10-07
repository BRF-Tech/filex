#!/usr/bin/env node
// The language packs' nightly translation (task #177), on the build host:
// once the nightly test run is over, an agent translates what the packs lack
// against main, and `langpacks.mjs apply` checks every answer and commits
// each pack - in the pack checkouts on this host. Nothing is pushed or
// tagged: release day fetches the commits (`langpacks.mjs pull`).
//
//   node scripts/langpacks-nightly.mjs run    [--env FILE] [--dry-run] [--no-wait]
//   node scripts/langpacks-nightly.mjs status [--env FILE]
//   node scripts/langpacks-nightly.mjs check  [--env FILE] [--probe]
//
//   run      tonight's translation, what filex-langpacks.timer starts: it
//            waits while the nightly run's tonight.json says the night is
//            going (LANGPACKS_NIGHT_FILE), fetches and checks LANGPACKS_REF
//            (origin/main) out in a worktree of the nightly run's checkout,
//            and runs `langpacks.mjs status --check` there - nothing
//            pending, nothing is done. Otherwise, pack by pack: `todo`, an
//            agent session that may write only its own directory of copies,
//            its answers taken into todo's worklist, `apply --commit`, and the
//            refused answers back to the agent (LANGPACKS_FIX_ROUNDS). Then
//            one notification: per language, what was translated and what is
//            left. --dry-run stops after the status (no agent, no commit);
//            --no-wait does not wait for the nightly run.
//   status   the last night and the ones before it.
//   check    what a night needs, checked: the checkout and the ref, the pack
//            checkouts (a commit identity, pushes from release day allowed),
//            the agent's command line and its credential (the account's
//            label, never the credential), the notification, the night file.
//            --probe also starts one short agent session with tonight's
//            command line and credential (it spends a few tokens).
//
//   --env FILE  the settings (default: $FILEX_LANGPACKS_ENV, else
//               /etc/filex-langpacks.env); the process environment wins
//
// Exit: 0 every pending string translated and committed, or nothing pending;
// 1 something is left or red (the notification says what); 2 could not run.
// It runs from the copies scripts/chain/install-langpacks.sh puts in
// <LANGPACKS_ROOT>/bin. docs/CONTRIBUTING.md -> Translations and language packs.
//
// ⚠ The agent writes only its own directory of copies, and that is held
// three ways: Claude Code's own boundary (lib/langpacks-nightly.mjs
// claudeArgs: --restricted, no command or web tool, no MCP server, file tools
// confined to the working directory) with a HOME of its own; only the answers
// cross, into the worklist `todo` wrote in a directory the agent never sees,
// which is the one `apply` reads (takeAnswers: the pack block that tells
// `apply` which pack to write is todo's, never the agent's); and every pack
// checkout, the tree and that directory are measured before and after the
// session - a change there stops the night, and nothing is applied.
//
// ⚠ The tree is a worktree of the nightly run's checkout (its objects, its
// fetch credential), made outside that checkout and removed after the run:
// the nightly run cleans its checkout with `git clean -ffdx` every night,
// which would take a worktree living inside it.

import { spawn, spawnSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { localDate } from './chain/nightly-lib.mjs';
import { findPacks, jsonText, readJSON } from './lib/langpacks.mjs';
import {
  accountEnv,
  agentPrompt,
  agentResult,
  applyOutcome,
  claudeArgs,
  composeReport,
  exitCode,
  fetchSpec,
  languageRows,
  nightDirName,
  nightVerdict,
  nightsToPrune,
  notifyWanted,
  pendingPacks,
  pickRef,
  readPromptTemplate,
  takeAnswers,
  translateSettings,
  waitStep,
} from './lib/langpacks-nightly.mjs';
import { announce, postRpc, toolResult } from './lib/notify.mjs';
import { loadSettings } from './lib/settings.mjs';
import { git, gitOut, revParse, run as runProgram } from './release/engine.mjs';
import { duration, usageOf } from './train/cli.mjs';

const DEFAULT_ENV = '/etc/filex-langpacks.env';

class SetupError extends Error {}

const slash = (p) => String(p).split(path.sep).join('/');
const iso = (ms = Date.now()) => new Date(ms).toISOString();
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const log = (line) => console.log(`[${iso().slice(11, 19)}] ${line}`);
const tail = (text, n = 12) => String(text ?? '').trim().split('\n').slice(-n).join('\n');

/** argv -> { cmd, env, dryRun, noWait, probe, help }. */
export function parseArgs(argv) {
  const out = { cmd: '', env: '', dryRun: false, noWait: false, probe: false, help: false };
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i];
    if (a === '--env') {
      const v = argv[i + 1];
      if (v === undefined || v.startsWith('--')) throw new Error('--env needs a file');
      out.env = v;
      i += 1;
    } else if (a === '--dry-run') out.dryRun = true;
    else if (a === '--no-wait') out.noWait = true;
    else if (a === '--probe') out.probe = true;
    else if (a === '--help' || a === '-h') out.help = true;
    else if (a.startsWith('-')) throw new Error(`unknown option ${a}`);
    else if (!out.cmd) out.cmd = a;
    else throw new Error(`one command at a time: ${out.cmd}, then ${a}`);
  }
  if (out.cmd && !['run', 'status', 'check'].includes(out.cmd)) throw new Error(`unknown command ${out.cmd}`);
  return out;
}

/* -- small helpers ------------------------------------------------------ */

/** `node <tree>/scripts/langpacks.mjs ...`, from the tree: its catalogue, its rules. */
function langpacks(tree, argv) {
  const r = spawnSync(process.execPath, [path.join(tree, 'scripts', 'langpacks.mjs'), ...argv], {
    cwd: tree,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  });
  return { code: typeof r.status === 'number' ? r.status : 2, out: r.stdout ?? '', err: `${r.stderr ?? ''}${r.error ? String(r.error.message) : ''}` };
}

function keep(dir, name, r) {
  try {
    fs.writeFileSync(path.join(dir, name), `exit ${r.code}\n${r.out}${r.err ? `\n--- stderr\n${r.err}` : ''}`);
  } catch {
    /* a log that cannot be written does not stop the night */
  }
}

function readSecret(file) {
  try {
    return fs.readFileSync(file, 'utf8').trim();
  } catch {
    return '';
  }
}

function copyIfThere(from, to) {
  if (!fs.existsSync(from)) return false;
  fs.mkdirSync(path.dirname(to), { recursive: true });
  fs.copyFileSync(from, to);
  return true;
}

function alive(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (e) {
    return e?.code === 'EPERM';
  }
}

/** One run at a time: a lock file holding the pid; a lock whose process is gone is taken over. */
function takeLock(file) {
  for (let i = 0; i < 2; i += 1) {
    try {
      fs.writeFileSync(file, `${process.pid}\n`, { flag: 'wx' });
      return { ok: true };
    } catch (e) {
      if (e?.code !== 'EEXIST') return { ok: false, why: `cannot take ${file}: ${e.message}` };
      const pid = Number(readSecret(file));
      if (alive(pid)) return { ok: false, why: `another run (pid ${pid}) holds ${file}` };
      fs.rmSync(file, { force: true });
    }
  }
  return { ok: false, why: `cannot take ${file}` };
}

/** Ends a process and everything it started: it runs in a process group of its own. */
function killTree(child) {
  if (!child?.pid) return;
  try {
    process.kill(-child.pid, 'SIGTERM');
  } catch {
    /* gone already */
  }
  setTimeout(() => {
    try {
      process.kill(-child.pid, 'SIGKILL');
    } catch {
      /* gone */
    }
  }, 10_000).unref();
}

/** The pack checkouts: LANGPACKS_PACKS, else the filex-lang-* checkouts beside the tree (in LANGPACKS_ROOT). */
function packDirs(cfg) {
  return cfg.packs.length ? cfg.packs : findPacks(cfg.worktree, { env: {} });
}

/* -- what is measured around the agent ------------------------------------ */

/** Every file under `dir`, names and bytes, as one digest. */
function dirDigest(dir) {
  const h = crypto.createHash('sha256');
  const walk = (d) => {
    let entries = [];
    try {
      entries = fs.readdirSync(d, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
    } catch {
      return;
    }
    for (const e of entries) {
      const p = path.join(d, e.name);
      if (e.isDirectory()) {
        h.update(`d ${e.name}\n`);
        walk(p);
      } else {
        h.update(`f ${e.name}\n`);
        h.update(fs.readFileSync(p));
      }
    }
  };
  walk(dir);
  return h.digest('hex');
}

/**
 * What every directory the agent must not write looks like now: the packs
 * and the tree by HEAD and `git status` (untracked files too, but for a
 * checkout as it stands - a person's, where untracked files come and go),
 * the directory `apply` reads by its files' digest. A directory that is not
 * a checkout measures as itself, unchanged.
 */
function fingerprint(guards) {
  const out = new Map();
  for (const g of guards) {
    if (g.digest) {
      out.set(g.dir, dirDigest(g.dir));
      continue;
    }
    const head = git(g.dir, 'rev-parse', 'HEAD');
    const st = git(g.dir, 'status', '--porcelain', g.untracked ? '--untracked-files=all' : '--untracked-files=no');
    out.set(g.dir, `${head.status === 0 ? head.stdout.trim() : '-'}\n${st.status === 0 ? st.stdout : '-'}`);
  }
  return out;
}

function changedSince(before, after) {
  return [...before.keys()].filter((d) => before.get(d) !== after.get(d));
}

/* -- the nightly run --------------------------------------------------------- */

/** The night as its file says now (and whether the process that wrote it is alive). */
function readNight(cfg) {
  let text = '';
  try {
    text = fs.readFileSync(cfg.nightFile, 'utf8');
  } catch (e) {
    return nightVerdict('', { nowMs: Date.now(), maxAgeH: cfg.nightMaxAgeH, error: e.code === 'ENOENT' ? `no ${cfg.nightFile}` : e.message });
  }
  let pid = null;
  try {
    pid = JSON.parse(text)?.pid ?? null;
  } catch {
    pid = null;
  }
  return nightVerdict(text, { nowMs: Date.now(), maxAgeH: cfg.nightMaxAgeH, alive: Number.isInteger(pid) ? alive(pid) : undefined });
}

async function waitForNight(cfg, t0) {
  for (;;) {
    const verdict = readNight(cfg);
    const step = waitStep({ verdict, waitedMin: (Date.now() - t0) / 60_000, maxWaitMin: cfg.waitMaxMin });
    if (step.step === 'go') return { ...verdict, ...step };
    log(`waiting: ${step.why}`);
    await sleep(cfg.waitPollMin * 60_000);
  }
}

/* -- the tree ---------------------------------------------------------------- */

/**
 * The tree translated against: the checkout itself (LANGPACKS_REF=none, a
 * run by hand), or the run's own worktree of it at the ref, fetched first
 * when it is a remote-tracking ref. A directory at the worktree's place that
 * is not a worktree of the checkout is left alone and the night stops.
 */
export function prepareTree(cfg, wait) {
  let pick = pickRef({ ref: cfg.ref, verdict: wait, fallback: cfg.fallbackRef });
  if (pick.ref === 'none') {
    const sha = revParse(cfg.filex, 'HEAD') ?? '';
    const subject = sha ? git(cfg.filex, 'log', '-1', '--format=%s').stdout.trim() : '';
    return { dir: cfg.filex, ref: pick.why, sha, subject, made: false };
  }
  const fetchRef = (ref) => {
    const spec = cfg.fetch ? fetchSpec(ref, git(cfg.filex, 'remote').stdout.split('\n').map((s) => s.trim()).filter(Boolean)) : null;
    if (spec) gitOut(cfg.filex, 'fetch', '--quiet', spec.remote, spec.refspec);
  };
  fetchRef(pick.ref);
  let sha = revParse(cfg.filex, `${pick.ref}^{commit}`);
  if (!sha && cfg.ref === 'night' && pick.ref !== cfg.fallbackRef) {
    // The night ran on a commit this checkout has not got: the fallback, and
    // the record says so.
    pick = { ref: cfg.fallbackRef, why: `${cfg.fallbackRef}: the night's ${pick.ref.slice(0, 8)} is not in this checkout` };
    fetchRef(pick.ref);
    sha = revParse(cfg.filex, `${pick.ref}^{commit}`);
  }
  if (!sha) throw new SetupError(`${pick.ref} is not a commit of ${cfg.filex}`);
  gitOut(cfg.filex, 'worktree', 'prune');
  const listed = gitOut(cfg.filex, 'worktree', 'list', '--porcelain')
    .split('\n')
    .filter((l) => l.startsWith('worktree '))
    .map((l) => path.resolve(l.slice('worktree '.length).trim()));
  const wt = cfg.worktree;
  if (listed.includes(path.resolve(wt))) {
    gitOut(wt, 'checkout', '-q', '--detach', '--force', sha);
  } else if (fs.existsSync(wt)) {
    throw new SetupError(`${wt} is there and is no worktree of ${cfg.filex}: move it away (nothing was deleted)`);
  } else {
    fs.mkdirSync(path.dirname(wt), { recursive: true });
    gitOut(cfg.filex, 'worktree', 'add', '-q', '--detach', wt, sha);
  }
  return { dir: wt, ref: pick.ref === sha ? pick.why : pick.ref, sha, subject: git(wt, 'log', '-1', '--format=%s').stdout.trim(), made: true };
}

export function removeTree(cfg, tree) {
  if (!tree?.made || cfg.keepTree) return;
  const r = git(cfg.filex, 'worktree', 'remove', '--force', tree.dir);
  if (r.status !== 0) log(`the worktree ${tree.dir} stays: ${tail(r.stderr || r.stdout, 1)}`);
}

/* -- the agent --------------------------------------------------------------- */

/**
 * The agent's credential, as the environment Claude Code reads it from:
 * LANGPACKS_CLAUDE_TOKEN_FILE (a run by hand), else the project's account
 * from the work server (LANGPACKS_WORK_URL, a project token in
 * LANGPACKS_WORK_TOKEN_FILE) - asked at every run, so a token renewed in the
 * vault is the one used, and no copy of it is kept on the host. A run with
 * LANGPACKS_AGENT_CMD (another agent) needs none.
 * Returns { env, label } or throws SetupError; the credential is never logged.
 */
export async function agentCredential(cfg, { fetch = globalThis.fetch } = {}) {
  if (cfg.agent.cmd) return { env: {}, label: 'LANGPACKS_AGENT_CMD' };
  if (cfg.agent.tokenFile) {
    const token = readSecret(cfg.agent.tokenFile);
    if (!token) throw new SetupError(`LANGPACKS_CLAUDE_TOKEN_FILE ${cfg.agent.tokenFile} is empty or missing`);
    return { env: { CLAUDE_CODE_OAUTH_TOKEN: token }, label: `the token in ${cfg.agent.tokenFile}` };
  }
  if (!cfg.agent.workUrl || !cfg.agent.workTokenFile) {
    throw new SetupError('no Claude credential: set LANGPACKS_WORK_URL and LANGPACKS_WORK_TOKEN_FILE (the project account), or LANGPACKS_CLAUDE_TOKEN_FILE');
  }
  const token = readSecret(cfg.agent.workTokenFile);
  if (!token) throw new SetupError(`LANGPACKS_WORK_TOKEN_FILE ${cfg.agent.workTokenFile} is empty or missing`);
  let res;
  try {
    res = await postRpc(
      cfg.agent.workUrl,
      token,
      { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'account.credential', arguments: cfg.agent.project ? { project: cfg.agent.project } : {} } },
      { fetch },
    );
  } catch (e) {
    throw new SetupError(`the work server could not be asked for the Claude account: ${e?.message ?? e}`);
  }
  const r = toolResult(res.status, res.text);
  if (!r.ok) throw new SetupError(`the work server gave no Claude account: ${r.why}`);
  const acc = accountEnv(r.text);
  if (!acc.env) throw new SetupError(acc.why);
  return { env: acc.env, label: `${acc.label} (${acc.type}, from the work server)` };
}

/**
 * One agent session in `dir`, the prompt on its stdin, in a process group
 * of its own. Its output goes to files beside the night's other logs (never
 * into the directory it works in); a session past its time is ended with
 * everything it started.
 */
function runAgent(cfg, { dir, prompt, timeoutMs, outFile, errFile, round, worklist, credential }) {
  return new Promise((resolve) => {
    const t0 = Date.now();
    fs.mkdirSync(path.join(cfg.agent.home, '.config'), { recursive: true });
    const agentEnv = {
      ...process.env,
      HOME: cfg.agent.home,
      XDG_CONFIG_HOME: path.join(cfg.agent.home, '.config'),
      CLAUDE_CONFIG_DIR: path.join(cfg.agent.home, '.claude'),
      AGENTTALK_WAKE_MAX: '0',
      LANGPACKS_WORKLIST: worklist,
      LANGPACKS_ROUND: String(round),
      ...credential,
    };
    const [bin, argv] = cfg.agent.cmd ? [cfg.agent.cmd, []] : [cfg.agent.bin, claudeArgs({ model: cfg.agent.model, extra: cfg.agent.args })];
    const outFd = fs.openSync(outFile, 'w');
    const errFd = fs.openSync(errFile, 'w');
    let done = false;
    let timedOut = false;
    let timer = null;
    const finish = (code, why = '') => {
      if (done) return;
      done = true;
      if (timer) clearTimeout(timer);
      for (const fd of [outFd, errFd]) {
        try {
          fs.closeSync(fd);
        } catch {
          /* closed */
        }
      }
      let summary = {};
      try {
        summary = agentResult(fs.readFileSync(outFile, 'utf8'));
      } catch {
        summary = {};
      }
      resolve({ round, code, timedOut, secs: Math.round((Date.now() - t0) / 1000), ...(why ? { why } : {}), ...summary });
    };
    let child;
    try {
      child = spawn(bin, argv, { cwd: dir, env: agentEnv, stdio: ['pipe', outFd, errFd], shell: !!cfg.agent.cmd, detached: true, windowsHide: true });
    } catch (e) {
      finish(127, `could not start the agent: ${e.message}`);
      return;
    }
    timer = setTimeout(() => {
      timedOut = true;
      killTree(child);
    }, Math.max(60_000, timeoutMs));
    child.on('error', (e) => finish(127, `could not start the agent: ${e.message}`));
    child.on('close', (code, signal) => finish(typeof code === 'number' ? code : signal ? 128 : 1));
    child.stdin.on('error', () => {});
    child.stdin.end(prompt);
  });
}

/**
 * One pack. `todo` writes into a directory the run keeps to itself - the
 * one `apply` reads; the agent works in another, on copies: the worklist,
 * AGENT.md, the catalogue, the glossary and the pack's translation. Then
 * rounds of agent -> answers taken into todo's worklist -> `apply --commit`,
 * until the pack is committed, the rounds are spent, or `apply` stops for a
 * reason the agent cannot fix.
 *
 * ⚠ Why two directories: `apply --worklist <dir>` takes every worklist file
 * in <dir> - each names the pack it writes - and the catalogue in
 * <dir>/catalogue, which `pack.mjs sync` copies into the pack. An agent that
 * wrote a second worklist, or edited that catalogue, in the directory
 * `apply` reads would reach a pack through it. In its own directory whatever
 * it writes stays there: only the answers cross.
 */
async function translatePack(cfg, { tree, pack, night, deadline, guards, template, credential }) {
  const base = path.basename(pack.dir);
  const todoDir = path.join(night, 'todo', base);
  const dir = path.join(night, 'agent', base);
  const logs = path.join(night, 'logs');
  const out = { name: pack.name, dir: pack.dir, base, langs: pack.langs, rounds: 0, agent: [], state: '', commit: '', feedback: '' };

  const catArgs = cfg.catalogue ? ['--catalogue', cfg.catalogue] : [];
  const td = langpacks(tree.dir, ['todo', '--packs', pack.dir, ...catArgs, '--out', todoDir]);
  keep(logs, `${base}-todo.txt`, td);
  const todoFile = path.join(todoDir, `${base}.json`);
  if (td.code !== 0 || !fs.existsSync(todoFile)) return { ...out, state: 'error', feedback: tail(td.err || td.out || `todo wrote no ${base}.json`) };
  const original = readJSON(todoFile);
  const items = Object.values(original.languages ?? {}).reduce((n, l) => n + (l.items?.length ?? 0), 0);

  // The agent's directory: copies only. It reads nothing outside it, and
  // nothing it does to them reaches the pack or what `apply` reads.
  fs.mkdirSync(dir, { recursive: true });
  const wlFile = path.join(dir, `${base}.json`);
  fs.copyFileSync(todoFile, wlFile);
  fs.cpSync(path.join(todoDir, 'catalogue'), path.join(dir, 'catalogue'), { recursive: true });
  if (fs.existsSync(path.join(todoDir, 'AGENT.md'))) {
    // Its paths, as the agent sees them: here.
    const guide = fs
      .readFileSync(path.join(todoDir, 'AGENT.md'), 'utf8')
      .split(slash(path.join(pack.dir, 'glossary.md')))
      .join('glossary.md')
      .split(slash(todoDir))
      .join('.');
    fs.writeFileSync(path.join(dir, 'AGENT.md'), guide);
  }
  const glossary = copyIfThere(path.join(pack.dir, 'glossary.md'), path.join(dir, 'glossary.md')) ? 'glossary.md' : '';
  const references = pack.langs.filter((t) => copyIfThere(path.join(pack.dir, 'translations', `${t}.json`), path.join(dir, 'reference', `${t}.json`))).map((t) => `reference/${t}.json`);
  const watched = [...guards, { dir: todoDir, digest: true }];

  let feedback = '';
  for (let round = 0; round <= cfg.agent.fixRounds; round += 1) {
    const left = deadline - Date.now();
    if (left < 3 * 60_000) {
      if (!out.state) out.state = 'time';
      break;
    }
    out.rounds = round + 1;
    const prompt = agentPrompt(template, { pack: pack.name, tag: pack.langs.join(', '), worklist: `${base}.json`, items, glossary, references, round, feedback });
    fs.writeFileSync(path.join(logs, `${base}-prompt-${round + 1}.md`), prompt);
    log(`${pack.name}: agent, round ${round + 1} (${items} item(s))`);
    const seen = fingerprint(watched);
    const a = await runAgent(cfg, {
      dir,
      prompt,
      timeoutMs: Math.min(cfg.agent.timeoutMin * 60_000, left - 2 * 60_000),
      outFile: path.join(logs, `${base}-agent-${round + 1}.json`),
      errFile: path.join(logs, `${base}-agent-${round + 1}.err`),
      round,
      worklist: wlFile,
      credential,
    });
    out.agent.push(a);
    log(`${pack.name}: agent ended (exit ${a.code}${a.timedOut ? ', at its time limit' : ''}, ${duration(a.secs)})`);
    const moved = changedSince(seen, fingerprint(watched));
    if (moved.length) {
      return { ...out, state: 'guard', feedback: `changed while the agent ran: ${moved.join(', ')} - nothing applied, the night stops` };
    }
    if (a.code === 127) return { ...out, state: 'agent', feedback: a.why ?? 'the agent did not start' };

    // Only the answers are taken from the worklist the agent wrote.
    let edited;
    try {
      edited = JSON.parse(fs.readFileSync(wlFile, 'utf8'));
    } catch (e) {
      // Kept for a person; the agent's copy goes back to the last good one,
      // which a next round can read again.
      fs.copyFileSync(wlFile, path.join(logs, `${base}-not-json-${round + 1}.json`));
      fs.copyFileSync(todoFile, wlFile);
      feedback = `${base}.json was not valid JSON after your edit (${e.message}), so it was put back as it was before this round. Answer its items again, and keep the file valid JSON.`;
      Object.assign(out, { state: 'refused', feedback });
      continue;
    }
    const taken = takeAnswers(original, edited);
    const answeredText = jsonText(taken.worklist);
    fs.writeFileSync(todoFile, answeredText);
    // The agent's copy too: a next round starts from its answers, in todo's frame.
    fs.writeFileSync(wlFile, answeredText);
    Object.assign(out, { answered: taken.answered, unanswered: taken.left, ignored: taken.ignored.length });

    const ap = langpacks(tree.dir, ['apply', '--worklist', todoDir, '--commit', ...(cfg.trailer ? ['--trailer', cfg.trailer] : [])]);
    keep(logs, `${base}-apply-${round + 1}.txt`, ap);
    const v = applyOutcome(ap.code, `${ap.out}\n${ap.err}`);
    Object.assign(out, { state: v.state, commit: v.commit ?? '', feedback: v.feedback });
    log(`${pack.name}: ${v.state}${v.commit ? ` ${v.commit}` : ''}`);
    if (!v.retry) break;
    feedback = v.feedback;
  }
  return out;
}

/* -- run ------------------------------------------------------------------- */

async function cmdRun(cfg, env, args) {
  const t0 = Date.now();
  fs.mkdirSync(cfg.state, { recursive: true });
  const lockFile = path.join(cfg.state, 'run.lock');
  const lock = takeLock(lockFile);
  if (!lock.ok) {
    console.error(`langpacks-nightly: ${lock.why}`);
    return 2;
  }
  let tree = null;
  let night = '';
  try {
    let day = '';
    try {
      day = localDate(t0, cfg.tz);
    } catch {
      day = localDate(t0, 'UTC');
    }
    const record = { day, at: iso(t0), host: os.hostname(), dryRun: args.dryRun, decision: '', packs: [], rows: [] };
    try {
      // 1. the nightly run
      if (args.noWait || !cfg.nightFile) {
        record.wait = { state: 'skipped', step: 'go', why: cfg.nightFile ? 'not waited for (--no-wait)' : 'not asked (LANGPACKS_NIGHT_FILE is empty)' };
      } else {
        record.wait = await waitForNight(cfg, t0);
      }
      log(`the nightly run: ${record.wait.why}`);

      // 2. the tree
      tree = prepareTree(cfg, record.wait);
      record.tree = { ref: tree.ref, sha: tree.sha, subject: tree.subject, dir: tree.dir };
      log(`tree: ${tree.ref} at ${tree.sha.slice(0, 8) || '?'} (${tree.dir})`);

      // 3. what is pending
      night = path.join(cfg.state, 'nights', nightDirName(t0, tree.sha));
      fs.mkdirSync(path.join(night, 'logs'), { recursive: true });
      record.night = night;
      const packs = packDirs(cfg);
      if (!packs.length) throw new SetupError(`no language pack checkout in ${path.dirname(cfg.worktree)} (scripts/chain/install-langpacks.sh clones them; or LANGPACKS_PACKS)`);
      const packArgs = ['--packs', packs.join(',')];
      const catArgs = cfg.catalogue ? ['--catalogue', cfg.catalogue] : [];
      const beforeFile = path.join(night, 'status-before.json');
      const st = langpacks(tree.dir, ['status', ...packArgs, ...catArgs, '--json', beforeFile, '--check']);
      keep(path.join(night, 'logs'), 'status-before.txt', st);
      if (st.code !== 0 && st.code !== 1) throw new SetupError(`langpacks.mjs status exited ${st.code}: ${tail(st.err || st.out, 4)}`);
      const before = readJSON(beforeFile);
      record.rows = languageRows(before, null);
      const todo = pendingPacks(before);
      if (st.code === 0 || !todo.length) {
        record.decision = 'up to date';
        log('every pack is up to date: nothing to translate');
      } else if (args.dryRun) {
        record.decision = 'dry run';
        log(`--dry-run: would translate ${todo.map((p) => `${p.name} (${p.pending})`).join(', ')}; no agent, no commit`);
      } else {
        // 4. pack by pack, with the credential asked once
        record.decision = 'translated';
        const credential = await agentCredential(cfg);
        log(`agent: ${cfg.agent.cmd ? 'LANGPACKS_AGENT_CMD' : `${cfg.agent.bin} ${cfg.agent.model}`}, ${credential.label}`);
        const deadline = t0 + cfg.budgetMin * 60_000;
        const guards = [{ dir: tree.dir, untracked: tree.made }, ...(before.packs ?? []).map((p) => ({ dir: p.dir, untracked: true }))];
        const template = readPromptTemplate();
        let stop = false;
        for (const p of todo) {
          if (stop) {
            record.packs.push({ name: p.name, dir: p.dir, state: 'skipped', rounds: 0 });
            continue;
          }
          if (deadline - Date.now() < 5 * 60_000) {
            record.packs.push({ name: p.name, dir: p.dir, state: 'time', rounds: 0 });
            continue;
          }
          const o = await translatePack(cfg, { tree, pack: p, night, deadline, guards, template, credential: credential.env });
          record.packs.push(o);
          if (o.state === 'guard') stop = true;
        }
        // 5. what is left
        const afterFile = path.join(night, 'status-after.json');
        const st2 = langpacks(tree.dir, ['status', ...packArgs, ...catArgs, '--json', afterFile]);
        keep(path.join(night, 'logs'), 'status-after.txt', st2);
        record.rows = languageRows(before, st2.code === 0 && fs.existsSync(afterFile) ? readJSON(afterFile) : null);
      }
    } catch (e) {
      record.error = String(e?.message ?? e);
      log(`could not run: ${record.error}`);
    } finally {
      try {
        removeTree(cfg, tree);
      } catch (e) {
        log(`the worktree stays: ${e.message}`);
      }
    }

    // The night's record, its notification, and the nights kept.
    const report = composeReport({
      day: record.day,
      tree: record.tree,
      wait: record.wait,
      rows: record.rows,
      packs: record.packs,
      error: record.error ?? '',
      took: duration((Date.now() - t0) / 1000),
      where: record.night ?? '',
    });
    record.ended = iso();
    record.result = report.result;
    record.exit = record.dryRun && !record.error ? 0 : exitCode({ result: report.result, error: record.error ?? '' });
    console.log(`\n${report.title}\n\n${report.message}`);
    if (!record.dryRun && notifyWanted(cfg.notify, report.result)) {
      const sent = await announce(
        { FILEX_NOTIFY_SOURCE: 'filex-langpacks', FILEX_WAKE_FROM: 'filex-langpacks', ...env },
        { title: report.title, message: report.message, result: report.result },
        { log },
      );
      record.notified = { notify: sent.notify?.sent ?? false, wake: (sent.wake ?? []).filter((w) => w.sent).map((w) => w.agent) };
    }
    saveRecord(cfg, record, report);
    pruneNights(cfg);
    return record.exit;
  } finally {
    fs.rmSync(lockFile, { force: true });
  }
}

function saveRecord(cfg, record, report) {
  try {
    const slim = {
      ...record,
      packs: record.packs.map((p) => ({
        name: p.name,
        dir: p.dir,
        state: p.state,
        commit: p.commit || undefined,
        rounds: p.rounds,
        answered: p.answered,
        unanswered: p.unanswered,
        agent: p.agent,
        said: p.state !== 'committed' && p.feedback ? tail(p.feedback, 20) : undefined,
      })),
      title: report.title,
    };
    fs.writeFileSync(path.join(cfg.state, 'last.json'), jsonText(slim));
    fs.appendFileSync(path.join(cfg.state, 'history.jsonl'), `${JSON.stringify({ day: slim.day, at: slim.at, ended: slim.ended, decision: slim.decision, result: slim.result, exit: slim.exit, sha: slim.tree?.sha, title: slim.title, error: slim.error })}\n`);
    if (record.night) fs.writeFileSync(path.join(record.night, 'report.txt'), `${report.title}\n\n${report.message}\n`);
  } catch (e) {
    log(`could not keep the night's record in ${cfg.state}: ${e.message}`);
  }
}

function pruneNights(cfg) {
  const dir = path.join(cfg.state, 'nights');
  let names = [];
  try {
    names = fs.readdirSync(dir);
  } catch {
    return;
  }
  for (const n of nightsToPrune(names, cfg.keepNights)) fs.rmSync(path.join(dir, n), { recursive: true, force: true });
}

/* -- status ---------------------------------------------------------------- */

function cmdStatus(cfg) {
  const last = path.join(cfg.state, 'last.json');
  if (!fs.existsSync(last)) {
    console.log(`no night yet in ${cfg.state}`);
    return 0;
  }
  const r = readJSON(last);
  console.log(`last night: ${r.title ?? r.day} - ${r.decision || '?'}, exit ${r.exit}`);
  if (r.error) console.log(`  could not run: ${r.error}`);
  for (const row of r.rows ?? []) console.log(`  ${row.pack} ${row.tag}: ${row.translated} translated, ${row.left} left`);
  for (const p of r.packs ?? []) console.log(`  ${p.name}: ${p.state}${p.commit ? ` ${p.commit}` : ''}${p.rounds ? `, ${p.rounds} round(s)` : ''}`);
  if (r.night) console.log(`  ${r.night}`);
  const hist = path.join(cfg.state, 'history.jsonl');
  if (fs.existsSync(hist)) {
    const lines = fs.readFileSync(hist, 'utf8').split('\n').filter(Boolean).slice(-10, -1).reverse();
    if (lines.length) console.log('before:');
    for (const l of lines) {
      try {
        const h = JSON.parse(l);
        console.log(`  ${h.day} ${h.decision || '?'} exit ${h.exit}: ${h.title ?? ''}`);
      } catch {
        /* a torn line */
      }
    }
  }
  return 0;
}

/* -- check ----------------------------------------------------------------- */

async function cmdCheck(cfg, env, args) {
  const problems = [];
  const say = (line) => console.log(line);
  say(`root: ${cfg.root}`);
  fs.mkdirSync(cfg.state, { recursive: true });

  if (!fs.existsSync(path.join(cfg.filex, '.git'))) problems.push(`${cfg.filex} is not a git checkout (LANGPACKS_FILEX: the nightly run's checkout)`);
  else {
    const ref = cfg.ref === 'night' ? cfg.fallbackRef : cfg.ref;
    const sha = ref === 'none' ? revParse(cfg.filex, 'HEAD') : revParse(cfg.filex, `${ref}^{commit}`);
    if (sha) say(`tree: ${ref} of ${cfg.filex} is ${sha.slice(0, 8)} now${cfg.fetch && fetchSpec(ref) ? ' (fetched again at every run)' : ''}`);
    else problems.push(`${ref} is not a commit of ${cfg.filex} yet (LANGPACKS_REF)`);
  }

  const packs = packDirs(cfg);
  if (!packs.length) problems.push(`no language pack checkout in ${path.dirname(cfg.worktree)} (scripts/chain/install-langpacks.sh clones them)`);
  for (const p of packs) {
    const email = git(p, 'config', 'user.email').stdout.trim();
    const push = git(p, 'config', 'receive.denyCurrentBranch').stdout.trim();
    const head = git(p, 'rev-parse', '--short=8', 'HEAD').stdout.trim();
    say(`pack: ${path.basename(p)} at ${head || '?'}${email ? '' : ', NO commit identity'}${push === 'updateInstead' ? '' : ', release-day pushes refused'}`);
    if (!email) problems.push(`${p}: no user.email - apply cannot commit (install-langpacks.sh --git-name/--git-email)`);
    if (push !== 'updateInstead') problems.push(`${p}: receive.denyCurrentBranch is not updateInstead - release day cannot push the release commit back`);
  }

  if (cfg.agent.cmd) say(`agent: LANGPACKS_AGENT_CMD (${cfg.agent.cmd}) - its own boundary, not Claude Code's`);
  else {
    const v = runProgram(cfg.agent.bin, ['--version']);
    if (v.status !== 0) problems.push(`agent: ${cfg.agent.bin} --version exited ${v.status} (not installed? see install-langpacks.sh): ${tail(v.stderr || v.stdout, 1)}`);
    else {
      const help = runProgram(cfg.agent.bin, ['--help']).stdout;
      const missing = claudeArgs({ model: cfg.agent.model })
        .filter((a) => a.startsWith('--'))
        .filter((f) => !help.includes(f));
      if (missing.length) problems.push(`agent: this Claude Code (${v.stdout.trim()}) does not know ${missing.join(' ')}: update it - the boundary depends on them`);
      else say(`agent: ${v.stdout.trim()}, model ${cfg.agent.model}, HOME ${cfg.agent.home}`);
    }
  }
  let credential = null;
  try {
    credential = await agentCredential(cfg);
    say(`credential: ${credential.label}`);
  } catch (e) {
    problems.push(`credential: ${e.message}`);
  }
  if (args.probe && cfg.agent.cmd) say('probe: skipped - LANGPACKS_AGENT_CMD is not Claude Code');
  else if (args.probe && problems.length) say('probe: skipped until the problems below are fixed');
  else if (args.probe) {
    // One short session with tonight's command line and credential, in an
    // empty directory: the sign-in, the model and every flag of the
    // boundary, proven.
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-langpacks-probe-'));
    try {
      const a = await runAgent(cfg, {
        dir,
        prompt: 'Reply with the single word OK, and do nothing else.\n',
        timeoutMs: 5 * 60_000,
        outFile: path.join(cfg.state, 'probe.json'),
        errFile: path.join(cfg.state, 'probe.err'),
        round: 0,
        worklist: '',
        credential: credential.env,
      });
      if (a.code === 0 && a.isError !== true && /\bOK\b/.test(a.result ?? '')) say(`probe: the agent answered (${duration(a.secs)}${a.costUsd !== undefined ? `, ${a.costUsd} USD` : ''})`);
      else problems.push(`probe: the agent did not answer OK (exit ${a.code}${a.timedOut ? ', timed out' : ''}): see ${path.join(cfg.state, 'probe.err')} and probe.json`);
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  }

  if (!env.FILEX_NOTIFY_URL && !env.FILEX_WAKE_URL) say('notify: nobody is told (FILEX_NOTIFY_URL, FILEX_WAKE_URL unset); the record is in last.json');
  else if (env.FILEX_NOTIFY_URL && !readSecret(env.FILEX_NOTIFY_KEY_FILE || '')) problems.push(`notify: no key in FILEX_NOTIFY_KEY_FILE (${env.FILEX_NOTIFY_KEY_FILE || 'unset'})`);
  else say(`notify: ${env.FILEX_NOTIFY_URL ? 'a person' : ''}${env.FILEX_NOTIFY_URL && env.FILEX_WAKE_URL ? ' and ' : ''}${env.FILEX_WAKE_URL ? 'an agent' : ''}`);
  say(cfg.nightFile ? `night: ${cfg.nightFile} - ${readNight(cfg).why}` : 'night: LANGPACKS_NIGHT_FILE is empty - the run does not wait for the nightly run');

  for (const p of problems) console.error(`PROBLEM ${p}`);
  if (!problems.length) say('ready');
  return problems.length ? 1 : 0;
}

/* -- main ------------------------------------------------------------------ */

export async function main(argv) {
  let args;
  try {
    args = parseArgs(argv);
  } catch (e) {
    console.error(`langpacks-nightly: ${e.message}\n\n${usageOf(import.meta.url)}`);
    return 2;
  }
  if (args.help || !args.cmd) {
    console.log(usageOf(import.meta.url));
    return args.help ? 0 : 2;
  }
  let settings;
  let cfg;
  try {
    const file = args.env || process.env.FILEX_LANGPACKS_ENV || (fs.existsSync(DEFAULT_ENV) ? DEFAULT_ENV : '');
    settings = loadSettings({ file });
    cfg = translateSettings(settings.env, { tz: Intl.DateTimeFormat().resolvedOptions().timeZone });
  } catch (e) {
    console.error(`langpacks-nightly: ${e.message}`);
    return 2;
  }
  if (args.cmd === 'status') return cmdStatus(cfg);
  if (args.cmd === 'check') return cmdCheck(cfg, settings.env, args);
  return cmdRun(cfg, settings.env, args);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).then(
    (code) => process.exit(code),
    (e) => {
      console.error(`langpacks-nightly: ${e?.stack ?? e}`);
      process.exit(2);
    },
  );
}
