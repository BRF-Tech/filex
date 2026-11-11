#!/usr/bin/env node
// The train's merge queue: a release's branches merged one after another onto
// the release branch - each on top of the one before, each with --no-ff and
// its own message file - CHANGELOG conflicts merged by section, and every
// merge built and checked in a worktree of its own BEFORE the release branch
// moves to it.
//
//   node scripts/train/merge-queue.mjs --queue <file> [options]
//
//   --queue FILE     the queue: plain lines, or the first ```queue block of a
//                    Markdown file (the train's RELEASE note, new-train.mjs)
//   --onto BRANCH    the branch the queue merges into (default: main). It must
//                    be the one checked out: the queue never switches branches
//   --msg-dir DIR    where a line without a message file finds one:
//                    DIR/<branch, "/" as "-">.txt (default: msg/ beside the queue)
//   --repo DIR       the checkout (default: the one the current directory is in)
//   --plan           print the queue - tip, commits ahead, message, merged or
//                    not - and merge nothing
//   --check 'CMD'    one more check after every merge (repeatable; bash, in the
//                    queue's worktree), e.g. --check 'cd web && npx vitest run'
//   --no-go          skip the Go build + vet after every merge
//   --no-packages    skip the install and the workspace packages' builds that
//                    come before the --check commands
//   --pnpm CMD       the pnpm to install and build with (default: pnpm)
//   --env FILE       settings (default: $FILEX_TRAIN_ENV, else
//                    ~/.config/filex-train.env): who is told when it ends, and
//                    where the private pipeline is (EMBARGO_API,
//                    EMBARGO_TOKEN_FILE; scripts/lib/embargo.mjs)
//   --embargo-unchecked WHY
//                    merge an embargoed branch (sec/<name>) whose tip has no
//                    green chain anyway; WHY goes into its merge commit as an
//                    `Embargo-Unchecked:` trailer and into the queue's report
//
// A queue line:   <branch> [<message file>]     (# starts a comment)
//
// Exit: 0 every branch is in and every check green; 1 a check is red or the
// queue could not start; 3 a conflict waits for a person (the merge was
// aborted, the release branch is where it was).
//
// ⚠ Why this exists (#179): the 0.52 and 0.53 rounds merged ten to fifteen
// branches each by hand in the main session - the same five commands per
// branch, the CHANGELOG conflict resolved with a scratch script, and the
// build checked only at the end. An agent's own merge onto main is refused by
// the permission system, so every merge waited for the main session's turn.
// One command, approved once, does the whole queue and stops only where a
// person is needed.
//
// ⚠⚠ Why the merge is made and checked in a worktree of its own (#329): until
// 2026-11-11 the queue committed each merge on the checked-out release branch
// and checked it there afterwards. A red check left the red merge on main -
// the queue never undid it, and the `git reset --hard` it printed is refused
// by the permission system an agent runs under - so the fix went in as a
// second merge and main's history kept a merge whose checks were red (#314,
// 76a9bc473). The same day a web check ran against the checkout's
// packages/core/dist, built from an older core, and was red for a change that
// was right (#321, 92ac09a00; lessons #1867, #1873). Now the merge is made in
// <repo>/.claude/worktrees/merge-queue (detached, the queue's own), the
// workspace packages the --check commands import are built there from that
// merge's own source (or proved built from it already), the checks run there,
// and only a green merge moves the release branch - by a fast-forward.
//
// What it does for each line, in order:
//   0. an embargoed branch (sec/<name>) enters only with a green chain on its
//      tip, asked of the private pipeline before anything is merged (task
//      #182: one was pushed with `-o ci.skip` and reached main without the
//      chain ever running on it) - or with --embargo-unchecked, said in the
//      commit;
//   1. a branch whose tip is already in the release branch is skipped - so
//      running the queue again after a stop carries on where it stopped;
//   2. the queue's worktree is put at the release branch's tip, and
//      `git merge --no-ff --no-commit <tip>` runs there with rerere on: a
//      conflict resolved once (in a rehearsal, or by hand) is resolved again
//      by itself;
//   3. CHANGELOG.md in conflict: merged by Keep a Changelog section
//      (changelog-merge.mjs); any other file still in conflict: the merge is
//      aborted and the queue stops for a person;
//   4. [Unreleased] gets one heading per kind (a clean merge can leave
//      `### Changed` twice, lesson #706);
//   5. committed with the line's message file, as written (in the worktree);
//   6. checked there: [Unreleased] is well formed, the Go module builds and
//      vets, the workspace packages are installed and built from this merge
//      (only those another workspace project depends on; one whose dist was
//      built from exactly this source is proved so and not built again), and
//      every --check;
//   7. green: the release branch fast-forwards to the checked merge. Red: the
//      release branch does not move, the merge stays in the worktree to look
//      at, and the queue stops - fix the branch and run the queue again.
// When the queue ends, however it ends, it says so (scripts/lib/notify.mjs).

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { chainVerdict, embargoSettings, isEmbargoed, pipelinesFor, readToken } from '../lib/embargo.mjs';
import { goShellArgv } from '../lib/go-build.mjs';
import { announce } from '../lib/notify.mjs';
import { loadSettings } from '../lib/settings.mjs';
import { Gates, banner, bold, dim, findBash, git, gitOut, green, red, revParse, run, slash, yellow } from '../release/engine.mjs';
import { normalizeChangelog, resolveInGit, unreleasedProblems } from './changelog-merge.mjs';
import { since, usageOf } from './cli.mjs';

const CHANGELOG = 'CHANGELOG.md';
const WORKSPACE_GATE = 'workspace packages: installed and built from this merge';
/** Where the queue merges and checks, relative to the repository's top. */
export const CANDIDATE_REL = path.join('.claude', 'worktrees', 'merge-queue');
const STATE_FILE = 'filex-merge-queue.json';

/** `branch` as a file name: feat/178-ship -> feat-178-ship. */
export const msgName = (branch) => `${branch.replace(/[\\/]+/g, '-')}.txt`;

/**
 * The queue's lines: [{ branch, msg }] with `msg` an absolute path. Reads the
 * first ```queue block when the text has one, else every line. A path is
 * relative to `dir`; a line without one takes `msgDir/<msgName>`.
 */
export function parseQueue(text, { dir, msgDir }) {
  const s = String(text).replace(/\r\n/g, '\n');
  const fence = /^```queue[^\n]*\n([\s\S]*?)^```/m.exec(s);
  const body = fence ? fence[1] : s;
  const out = [];
  const seen = new Set();
  for (const raw of body.split('\n')) {
    const line = raw.replace(/#.*$/, '').trim();
    if (!line) continue;
    const parts = line.split(/\s+/);
    if (parts.length > 2) throw new Error(`queue line "${raw.trim()}": a branch and at most one message file (a path with spaces is not supported)`);
    const [branch, msg] = parts;
    if (!/^[A-Za-z0-9._/-]+$/.test(branch) || branch.startsWith('-')) throw new Error(`queue line "${raw.trim()}": "${branch}" is not a branch name`);
    if (seen.has(branch)) throw new Error(`queue: ${branch} is listed twice`);
    seen.add(branch);
    out.push({ branch, msg: path.resolve(dir, msg ?? path.join(msgDir, msgName(branch))) });
  }
  return out;
}

/** The files a conflicted merge still holds in conflict, CHANGELOG.md apart. */
export function splitConflicts(unmerged) {
  const files = unmerged.filter(Boolean);
  return { changelog: files.includes(CHANGELOG), others: files.filter((f) => f !== CHANGELOG) };
}

const MARKER = /^(<{7}|>{7})( |$)/m;

/** The Go check: build and vet the module, with the embed directories it needs. */
export function goCheckScript() {
  const keep = (d) => `{ [ -n "$(ls -A embed/${d} 2>/dev/null)" ] || touch embed/${d}/.placeholder; }`;
  return `mkdir -p embed/admin embed/web && ${keep('admin')} && ${keep('web')} && go build ./... && go vet ./...`;
}

/** The command line, parsed. */
export function parseArgs(argv) {
  const o = { queue: null, onto: 'main', msgDir: null, repo: null, plan: false, checks: [], go: true, packages: true, pnpm: 'pnpm', env: '', embargoUnchecked: '' };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const next = () => {
      const v = argv[++i];
      if (v === undefined) throw new Error(`${a} needs a value`);
      return v;
    };
    if (a === '--queue') o.queue = next();
    else if (a === '--onto') o.onto = next();
    else if (a === '--msg-dir') o.msgDir = next();
    else if (a === '--repo') o.repo = next();
    else if (a === '--plan') o.plan = true;
    else if (a === '--check') o.checks.push(next());
    else if (a === '--no-go') o.go = false;
    else if (a === '--no-packages') o.packages = false;
    else if (a === '--pnpm') o.pnpm = next();
    else if (a === '--env') o.env = next();
    else if (a === '--embargo-unchecked') {
      o.embargoUnchecked = next().trim();
      if (!o.embargoUnchecked) throw new Error('--embargo-unchecked needs the reason, in words');
    } else if (a === '-h' || a === '--help') o.help = true;
    else throw new Error(`unknown option ${a}`);
  }
  if (!o.help && !o.queue) throw new Error('--queue FILE is required');
  return o;
}

/**
 * Why an embargoed row may not be merged, or '' when it may: its tip's chain
 * is green on the private pipeline (scripts/lib/embargo.mjs). `ask` is
 * pipelinesFor (a test hands its own). Not embargoed, already merged, or
 * waved through with --embargo-unchecked: ''.
 */
export async function embargoProblem(row, { settings, unchecked = '', ask = pipelinesFor, token = readToken } = {}) {
  if (!isEmbargoed(row.branch) || row.merged || !row.tip || unchecked) return '';
  const where = embargoSettings(settings);
  if (!where) {
    return `${row.branch}: an embargoed branch enters only with a green chain on its tip, and the settings do not say where the private pipeline is (EMBARGO_API, EMBARGO_TOKEN_FILE) - or say why not with --embargo-unchecked "<why>"`;
  }
  let pipelines;
  try {
    pipelines = await ask({ api: where.api, token: token(where.tokenFile), sha: row.tip });
  } catch (e) {
    return `${row.branch}: the private pipeline could not be asked about ${row.tip.slice(0, 10)} (${e.message}) - or say why not with --embargo-unchecked "<why>"`;
  }
  const v = chainVerdict(pipelines);
  return v.ok ? '' : `${row.branch}: ${v.why} on ${row.tip.slice(0, 10)} - run it (a push without -o ci.skip, or the pipeline by hand) and run the queue again, or say why not with --embargo-unchecked "<why>"`;
}

// ── the queue's own worktree ────────────────────────────────────────────────

/** Two paths name the same place (Windows: either slash, any case). */
export function samePath(a, b) {
  const norm = (p) => {
    const r = path.resolve(String(p)).replace(/[\\/]+$/, '');
    return process.platform === 'win32' ? r.replace(/\//g, '\\').toLowerCase() : r;
  };
  return norm(a) === norm(b);
}

/**
 * Where the queue merges and checks: <top>/.claude/worktrees/merge-queue,
 * with <top> the main checkout (the directory holding the common .git), so a
 * queue started from a worktree still puts its own beside the others.
 */
export function candidatePath(repo) {
  const common = gitOut(repo, 'rev-parse', '--path-format=absolute', '--git-common-dir');
  const top = path.basename(common) === '.git' ? path.dirname(common) : repo;
  return path.join(top, CANDIDATE_REL);
}

/**
 * The queue's worktree, made or reused, at `tip` with nothing else in it.
 * Returns { dir } or { problem }. A directory there that is not this
 * repository's worktree is refused, never removed: it is somebody's.
 */
export function prepareCandidate(repo, tip) {
  const dir = candidatePath(repo);
  const top = path.dirname(path.dirname(path.dirname(dir)));
  // Kept out of `git status`: the repository's .gitignore says so in filex;
  // anywhere else the queue says it in .git/info/exclude.
  if (git(top, 'check-ignore', '-q', `${slash(CANDIDATE_REL)}/`).status !== 0) {
    const exclude = path.join(gitOut(repo, 'rev-parse', '--path-format=absolute', '--git-common-dir'), 'info', 'exclude');
    fs.mkdirSync(path.dirname(exclude), { recursive: true });
    const had = fs.existsSync(exclude) ? fs.readFileSync(exclude, 'utf8') : '';
    fs.writeFileSync(exclude, `${had}${had && !had.endsWith('\n') ? '\n' : ''}/.claude/worktrees/\n`);
  }
  if (!fs.existsSync(dir)) {
    git(repo, 'worktree', 'prune');
    const add = git(repo, 'worktree', 'add', '-q', '--detach', dir, tip);
    if (add.status !== 0) return { problem: `the queue's worktree ${slash(dir)} could not be made: ${(add.stderr || add.stdout).trim()}` };
    return { dir };
  }
  const own = git(dir, 'rev-parse', '--path-format=absolute', '--show-toplevel', '--git-common-dir');
  const [ownTop, ownCommon] = own.stdout.trim().split('\n');
  const repoCommon = gitOut(repo, 'rev-parse', '--path-format=absolute', '--git-common-dir');
  if (own.status !== 0 || !samePath(ownTop, dir) || !samePath(ownCommon, repoCommon)) {
    return {
      problem: `${slash(dir)} is there and is not this repository's worktree - look at it and move it away (scripts/wt-clean.sh calls such a leftover OKSUZ); the queue does not delete it`,
    };
  }
  if (revParse(dir, 'MERGE_HEAD')) git(dir, 'merge', '--abort');
  for (const args of [
    ['reset', '-q', '--hard'],
    ['checkout', '-q', '--detach', tip],
    ['clean', '-fdq'],
  ]) {
    const r = git(dir, ...args);
    if (r.status !== 0) return { problem: `the queue's worktree ${slash(dir)}: git ${args.join(' ')} failed: ${(r.stderr || r.stdout).trim()}` };
  }
  return { dir };
}

// ── the workspace packages a check imports ──────────────────────────────────

/** The projects pnpm-workspace.yaml names: ['packages/core', 'web', ...]. */
export function workspaceProjects(dir) {
  let text;
  try {
    text = fs.readFileSync(path.join(dir, 'pnpm-workspace.yaml'), 'utf8');
  } catch {
    return [];
  }
  const out = [];
  let inPackages = false;
  for (const raw of text.replace(/\r\n/g, '\n').split('\n')) {
    if (/^\S/.test(raw)) inPackages = /^packages\s*:/.test(raw);
    const m = inPackages && /^\s+-\s*['"]?([^'"#\s]+)['"]?/.exec(raw);
    if (!m || m[1].startsWith('!')) continue;
    const entry = m[1].replace(/\/+$/, '');
    if (entry.endsWith('/*')) {
      const base = entry.slice(0, -2);
      let names = [];
      try {
        names = fs.readdirSync(path.join(dir, base), { withFileTypes: true }).filter((d) => d.isDirectory()).map((d) => d.name).sort();
      } catch {
        names = [];
      }
      for (const n of names) if (fs.existsSync(path.join(dir, base, n, 'package.json'))) out.push(`${base}/${n}`);
    } else if (fs.existsSync(path.join(dir, entry, 'package.json'))) out.push(entry);
  }
  return [...new Set(out)];
}

/**
 * The workspace packages some other project of the workspace depends on
 * (`workspace:`), and that have a build: what an import of one reads is its
 * built `main`, not its source. Dependencies first. [{ dir, name, main, deps }]
 */
export function workspaceLibraries(dir) {
  const projects = workspaceProjects(dir).map((p) => {
    let pkg = {};
    try {
      pkg = JSON.parse(fs.readFileSync(path.join(dir, p, 'package.json'), 'utf8'));
    } catch {
      pkg = {};
    }
    const wanted = new Set();
    for (const field of ['dependencies', 'devDependencies', 'peerDependencies', 'optionalDependencies']) {
      for (const [name, range] of Object.entries(pkg[field] ?? {})) if (String(range).startsWith('workspace:')) wanted.add(name);
    }
    return { dir: p, name: pkg.name ?? '', main: pkg.main ?? pkg.module ?? '', build: !!pkg.scripts?.build, wanted: [...wanted] };
  });
  const byName = new Map(projects.filter((p) => p.name).map((p) => [p.name, p]));
  const needed = new Set();
  for (const p of projects) for (const n of p.wanted) if (byName.has(n)) needed.add(n);
  const order = [];
  const seen = new Set();
  const visit = (name) => {
    if (seen.has(name)) return;
    seen.add(name);
    const p = byName.get(name);
    for (const n of p.wanted) if (byName.has(n)) visit(n);
    if (p.build) order.push({ dir: p.dir, name: p.name, main: p.main, deps: p.wanted.filter((n) => byName.has(n)).map((n) => byName.get(n).dir) });
  };
  for (const name of [...needed].sort()) visit(name);
  return order;
}

/** What a build of the workspace depends on outside its own directory. */
const ROOT_INPUTS = ['pnpm-lock.yaml', 'package.json', 'pnpm-workspace.yaml', 'tsconfig.json', 'tsconfig.base.json'];

function objectIds(dir, specs) {
  return specs.map((s) => `${s}=${revParse(dir, `HEAD:${s}`) ?? '-'}`);
}

/** The source a library's dist is built from, as one id (null: not committed). */
export function libraryFingerprint(dir, lib, depPrints) {
  const tree = revParse(dir, `HEAD:${lib.dir}`);
  if (!tree) return null;
  const parts = [`${lib.dir}=${tree}`, ...objectIds(dir, ROOT_INPUTS), ...lib.deps.map((d) => `dep ${d}=${depPrints[d] ?? '-'}`)];
  return crypto.createHash('sha256').update(parts.join('\n')).digest('hex');
}

/** What node_modules was installed from, as one id. */
export function installFingerprint(dir) {
  return crypto.createHash('sha256').update(objectIds(dir, ROOT_INPUTS.slice(0, 3)).join('\n')).digest('hex');
}

function readState(file) {
  try {
    return JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch {
    return {};
  }
}

/**
 * The workspace gate: node_modules installed from this merge's lockfile, and
 * every library's dist built from this merge's source. A dist is proved
 * fresh - and not built again - only when the queue built it itself from the
 * same source and it is the very file it left (its built `main`, by mtime and
 * size); anything else is built. Returns { ok, detail, log }.
 */
export function freshenWorkspace({ dir, bash, pnpm = 'pnpm', stateFile }) {
  const libs = workspaceLibraries(dir);
  const state = readState(stateFile);
  state.packages ??= {};
  const save = () => fs.writeFileSync(stateFile, `${JSON.stringify(state, null, 2)}\n`);
  const log = [];
  const sh = (cmd) => {
    log.push(`$ ${cmd}`);
    const r = run(bash, ['-c', cmd], { cwd: dir, env: { NODE_OPTIONS: process.env.NODE_OPTIONS || '--max-old-space-size=4096' } });
    log.push(`${r.stdout}${r.stderr}`.trim(), `(exit ${r.status})`);
    return r.status === 0;
  };
  if (!bash) return { ok: false, detail: 'no bash to install and build with (Git for Windows\' bin/bash.exe was not found)' };
  const t0 = Date.now();
  const said = [];
  const lock = installFingerprint(dir);
  if (state.install !== lock || !fs.existsSync(path.join(dir, 'node_modules'))) {
    delete state.install;
    save();
    if (!sh(`${pnpm} install --frozen-lockfile --prefer-offline`)) return { ok: false, detail: `${pnpm} install --frozen-lockfile failed (log)`, log: log.join('\n') };
    state.install = lock;
    save();
    said.push('installed');
  }
  const prints = {};
  const built = [];
  const proved = [];
  for (const lib of libs) {
    const fp = libraryFingerprint(dir, lib, prints);
    prints[lib.dir] = fp ?? `uncommitted-${Date.now()}`;
    const entry = lib.main ? path.join(dir, lib.dir, lib.main) : null;
    const was = state.packages[lib.dir];
    let st = null;
    try {
      st = entry ? fs.statSync(entry) : null;
    } catch {
      st = null;
    }
    if (fp && was?.fp === fp && st && was.mtimeMs === st.mtimeMs && was.size === st.size) {
      proved.push(lib.dir);
      continue;
    }
    delete state.packages[lib.dir];
    save();
    if (!sh(`${pnpm} --filter ./${lib.dir} run build`)) return { ok: false, detail: `${lib.dir}: its build failed (log)`, log: log.join('\n') };
    let now = null;
    try {
      now = entry ? fs.statSync(entry) : null;
    } catch {
      now = null;
    }
    if (entry && !now) return { ok: false, detail: `${lib.dir}: its build passed and left no ${lib.main}`, log: log.join('\n') };
    if (fp && now) state.packages[lib.dir] = { fp, main: lib.main, mtimeMs: now.mtimeMs, size: now.size };
    save();
    built.push(lib.dir);
  }
  if (built.length) said.push(`built ${built.join(', ')}`);
  if (proved.length) said.push(`${proved.join(', ')} already built from this source`);
  if (!libs.length) said.push('no workspace package is imported by another');
  return { ok: true, detail: `${said.join('; ')} (${since(t0)})`, log: log.join('\n') };
}

const USAGE = usageOf(import.meta.url);

async function main(argv) {
  let o;
  try {
    o = parseArgs(argv);
  } catch (e) {
    console.error(`merge-queue: ${e.message}\n\n${USAGE}`);
    return 2;
  }
  if (o.help) {
    console.log(USAGE);
    return 0;
  }
  const settings = loadSettings({ file: o.env, envVar: 'FILEX_TRAIN_ENV', fallback: 'filex-train.env' });
  const repo = path.resolve(o.repo ?? gitOut(process.cwd(), 'rev-parse', '--show-toplevel'));
  const queueFile = path.resolve(o.queue);
  const queueDir = path.dirname(queueFile);
  const msgDir = path.resolve(o.msgDir ?? path.join(queueDir, 'msg'));
  let queue;
  try {
    queue = parseQueue(fs.readFileSync(queueFile, 'utf8'), { dir: queueDir, msgDir });
  } catch (e) {
    console.error(`merge-queue: ${e.message}`);
    return 2;
  }
  if (queue.length === 0) {
    console.error(`merge-queue: ${slash(queueFile)} lists no branch`);
    return 2;
  }

  // ── where it stands ───────────────────────────────────────────────────────
  const problems = [];
  const head = git(repo, 'symbolic-ref', '--short', '-q', 'HEAD').stdout.trim();
  if (head !== o.onto) problems.push(`${slash(repo)} has ${head || 'a detached HEAD'} checked out, not ${o.onto} - check out ${o.onto} first (the queue never switches branches)`);
  if (revParse(repo, 'MERGE_HEAD')) problems.push('a merge is already in progress here - finish it (commit) or abort it first');
  const dirty = git(repo, 'status', '--porcelain', '--untracked-files=no').stdout.trim();
  if (dirty) problems.push(`the tree has changes to tracked files:\n${dirty}`);
  const rows = queue.map((q) => {
    const tip = revParse(repo, `${q.branch}^{commit}`);
    const merged = tip ? git(repo, 'merge-base', '--is-ancestor', tip, 'HEAD').status === 0 : false;
    const ahead = tip ? git(repo, 'rev-list', '--count', `HEAD..${tip}`).stdout.trim() : '?';
    let msgOk = false;
    try {
      msgOk = fs.readFileSync(q.msg, 'utf8').trim().length > 0;
    } catch {
      msgOk = false;
    }
    return { ...q, tip, merged, ahead, msgOk };
  });
  for (const r of rows) {
    if (!r.tip) problems.push(`${r.branch}: no such branch`);
    else if (!r.merged && !r.msgOk) problems.push(`${r.branch}: no message - write ${slash(r.msg)} (English, the merge commit's whole message)`);
    const embargo = await embargoProblem(r, { settings: settings.env, unchecked: o.embargoUnchecked });
    if (embargo) problems.push(embargo);
  }
  const waved = rows.filter((r) => isEmbargoed(r.branch) && !r.merged && r.tip && o.embargoUnchecked).map((r) => r.branch);
  if (waved.length) console.log(yellow(`embargoed, merged without a green chain on the tip (--embargo-unchecked): ${waved.join(', ')} - ${o.embargoUnchecked}\n`));

  const cand = candidatePath(repo);
  console.log(`${bold('merge queue')} onto ${bold(o.onto)} in ${slash(repo)} ${dim(`(${slash(queueFile)})`)}`);
  console.log(dim(`merged and checked in ${slash(cand)}; ${o.onto} moves only to a green merge\n`));
  rows.forEach((r, i) => {
    const state = !r.tip ? red('missing') : r.merged ? green('in') : `${r.ahead} commit(s)`;
    console.log(`  ${String(i + 1).padStart(2)}. ${r.branch.padEnd(40)} ${(r.tip ?? '').slice(0, 10).padEnd(10)} ${state}  ${dim(slash(r.msg))}`);
  });
  if (o.plan) {
    if (problems.length) console.log(`\n${yellow('would not start:')}\n${problems.map((p) => `  - ${p}`).join('\n')}`);
    return problems.length ? 1 : 0;
  }
  if (problems.length) {
    console.log(`\n${red(bold('NOT STARTED'))}:\n${problems.map((p) => `  - ${p}`).join('\n')}`);
    return 1;
  }

  // ── the merges ────────────────────────────────────────────────────────────
  const t0 = Date.now();
  const bash = findBash();
  const gitDir = gitOut(repo, 'rev-parse', '--absolute-git-dir');
  const runDir = path.join(gitDir, 'filex-train', 'queue', new Date().toISOString().replace(/[:.]/g, '-'));
  const done = [];
  let exit = 0;
  let stop = '';
  const rr = ['-c', 'rerere.enabled=true', '-c', 'rerere.autoupdate=true'];

  for (const [i, r] of rows.entries()) {
    // Asked again here, not only at the start: a branch built on an earlier
    // one of the queue is already in once that one is merged.
    if (r.merged || git(repo, 'merge-base', '--is-ancestor', r.tip, 'HEAD').status === 0) {
      done.push(`${r.branch}: already in`);
      continue;
    }
    banner(`${i + 1}/${rows.length} ${r.branch}`, dim(`${r.ahead} commit(s), tip ${r.tip.slice(0, 10)}`));
    const before = revParse(repo, 'HEAD');
    const c0 = prepareCandidate(repo, before);
    if (c0.problem) {
      stop = `${r.branch}: ${c0.problem}`;
      exit = 1;
      break;
    }
    const wt = c0.dir;
    const tm = Date.now();
    const m = run('git', ['-C', wt, ...rr, 'merge', '--no-ff', '--no-commit', r.tip], { env: { GIT_MERGE_AUTOEDIT: 'no' } });
    if (m.status !== 0 && !revParse(wt, 'MERGE_HEAD')) {
      stop = `${r.branch}: git merge did not start:\n${(m.stderr || m.stdout).trim()}`;
      exit = 1;
      break;
    }
    const trailer = waved.includes(r.branch) ? ['--trailer', `Embargo-Unchecked: ${o.embargoUnchecked}`] : [];
    const trailerSaid = trailer.length ? ` --trailer "Embargo-Unchecked: ${o.embargoUnchecked.replace(/"/g, "'")}"` : '';
    const byHand = [
      'Resolve it once, with rerere on, in the queue\'s worktree (the next run replays your resolution, checks it, and only then moves the branch):',
      `    git -C ${slash(wt)} ${rr.join(' ')} merge --no-ff --no-commit ${r.tip}`,
      `    node scripts/train/changelog-merge.mjs --git --repo ${slash(wt)}     # if CHANGELOG.md is in conflict too`,
      '    (resolve the rest, git add them)',
      `    git -C ${slash(wt)} -c rerere.enabled=true commit --cleanup=whitespace -F ${slash(r.msg)}${trailerSaid}`,
      'then run the queue again: it starts this branch over from the tip, and the recorded resolution replays.',
    ];
    const unmerged = git(wt, 'diff', '--name-only', '--diff-filter=U').stdout.split('\n').map((s) => s.trim());
    const c = splitConflicts(unmerged);
    if (c.others.length) {
      git(wt, 'merge', '--abort');
      stop = [`${r.branch}: in conflict beyond the changelog - ${c.others.join(', ')}. The merge was aborted; ${o.onto} did not move.`, ...byHand].join('\n');
      exit = 3;
      break;
    }
    if (c.changelog) {
      const res = resolveInGit(wt, CHANGELOG);
      if (!res.ok) {
        git(wt, 'merge', '--abort');
        stop = [`${r.branch}: CHANGELOG.md could not be merged by section, and that is a person's call:\n${res.why}\nThe merge was aborted; ${o.onto} did not move.`, ...byHand].join('\n');
        exit = 3;
        break;
      }
      console.log(`  ${green('ok    ')}  CHANGELOG.md merged by section`);
    }
    // One heading per kind, whether or not git saw a conflict.
    const staged = git(wt, 'diff', '--cached', '--name-only', 'HEAD').stdout.split('\n').map((s) => s.trim()).filter(Boolean);
    if (staged.includes(CHANGELOG)) {
      const file = path.join(wt, CHANGELOG);
      const n = normalizeChangelog(fs.readFileSync(file, 'utf8'));
      if (n.changed) {
        fs.writeFileSync(file, n.text);
        git(wt, 'add', '--', CHANGELOG);
        console.log(`  ${green('ok    ')}  [Unreleased]: one heading per kind`);
      }
    }
    const marked = staged.filter((f) => {
      const show = git(wt, 'show', `:${f}`);
      return show.status === 0 && MARKER.test(show.stdout);
    });
    if (marked.length) {
      git(wt, 'merge', '--abort');
      stop = `${r.branch}: conflict markers would be committed in ${marked.join(', ')} (a rerere replay gone wrong?). The merge was aborted; ${o.onto} did not move.`;
      exit = 1;
      break;
    }
    // An embargoed branch merged without its chain says so in its own commit,
    // where the history keeps it (task #182).
    const commit = run('git', ['-C', wt, '-c', 'rerere.enabled=true', 'commit', '--cleanup=whitespace', '-F', r.msg, ...trailer]);
    if (commit.status !== 0) {
      git(wt, 'merge', '--abort');
      stop = `${r.branch}: git commit failed (the merge was aborted; ${o.onto} did not move):\n${(commit.stderr || commit.stdout).trim()}`;
      exit = 1;
      break;
    }
    const after = revParse(wt, 'HEAD');
    console.log(`  ${green('ok    ')}  merged as ${after.slice(0, 10)} in the queue's worktree ${dim(`(${since(tm)})`)}`);

    // ── the checks, in the worktree ────────────────────────────────────────
    const gates = new Gates({ logsDir: path.join(runDir, `${String(i + 1).padStart(2, '0')}-${msgName(r.branch).replace(/\.txt$/, '')}`), bash, repo: wt });
    const workspace = o.packages && o.checks.length > 0 && fs.existsSync(path.join(wt, 'pnpm-workspace.yaml'));
    const stateFile = path.join(gitOut(wt, 'rev-parse', '--absolute-git-dir'), STATE_FILE);
    const specs = [
      {
        name: '[Unreleased] holds each heading once and no conflict marker',
        check: () => {
          let text = '';
          try {
            text = fs.readFileSync(path.join(wt, CHANGELOG), 'utf8');
          } catch {
            return { ok: true, detail: 'no CHANGELOG.md here' };
          }
          const p = unreleasedProblems(text);
          return p.length ? { ok: false, detail: p.join('\n') } : { ok: true };
        },
      },
      ...(o.go && fs.existsSync(path.join(wt, 'backend', 'go.mod'))
        ? [{ name: 'go: build + vet', cmd: goShellArgv({ dir: path.join(wt, 'backend'), checkout: wt, script: goCheckScript(), bash: bash ?? 'bash' }) }]
        : []),
      ...(workspace ? [{ name: WORKSPACE_GATE, check: () => freshenWorkspace({ dir: wt, bash, pnpm: o.pnpm, stateFile }) }] : []),
      ...o.checks.map((sh, k) => ({ name: `check ${k + 1}: ${sh}`, sh, ...(workspace ? { after: [WORKSPACE_GATE] } : {}) })),
    ];
    const green_ = await gates.all('merge', specs, {});
    if (!green_) {
      stop = [
        `${r.branch}: its merge ${after.slice(0, 10)} is red (logs above); ${o.onto} did not move, it is still ${before.slice(0, 10)}.`,
        `The red merge is left in ${slash(wt)} to look at. Fix the branch and run the queue again.`,
      ].join('\n');
      exit = 1;
      done.push(`${r.branch}: checks RED on ${after.slice(0, 10)}, ${o.onto} not moved`);
      break;
    }

    // ── green: the release branch moves to the merge that was checked ──────
    const now = revParse(repo, 'HEAD');
    if (now !== before) {
      stop = `${r.branch}: ${o.onto} moved while the queue checked (${before.slice(0, 10)} -> ${(now ?? '?').slice(0, 10)}); the queue did not move it. The green merge ${after.slice(0, 10)} is in ${slash(wt)}; run the queue again to merge onto the new tip.`;
      exit = 1;
      break;
    }
    const ff = run('git', ['-C', repo, 'merge', '--ff-only', '-q', after]);
    if (ff.status !== 0 || revParse(repo, 'HEAD') !== after) {
      stop = `${r.branch}: green, but ${o.onto} could not fast-forward to ${after.slice(0, 10)}:\n${(ff.stderr || ff.stdout).trim()}\nThe green merge is in ${slash(wt)}: git -C ${slash(repo)} merge --ff-only ${after.slice(0, 10)}`;
      exit = 1;
      break;
    }
    console.log(`  ${green('ok    ')}  ${o.onto} fast-forwarded to ${after.slice(0, 10)}`);
    done.push(`${r.branch}: merged ${after.slice(0, 10)} (${since(tm)})`);
  }

  // ── the end ───────────────────────────────────────────────────────────────
  const left = rows.filter((r) => !done.some((d) => d.startsWith(`${r.branch}:`))).map((r) => r.branch);
  console.log(`\n${bold('merge queue')} ${dim(`(${since(t0)})`)}`);
  for (const d of done) console.log(`  ${d}`);
  if (left.length) console.log(`  ${dim(`not reached: ${left.join(', ')}`)}`);
  if (stop) console.log(`\n${exit === 3 ? yellow(bold('WAITING FOR A PERSON')) : red(bold('STOPPED'))}\n${stop}`);
  else console.log(`\n${green(bold(`every branch is in ${o.onto}, every check green`))}`);

  const result = exit === 0 ? 'ok' : exit === 3 ? 'waiting' : 'red';
  const title = `filex merge queue onto ${o.onto}: ${exit === 0 ? 'done' : exit === 3 ? 'waiting for a person' : 'RED'} (${since(t0)})`;
  const wavedLine = waved.length ? [`embargoed without a green chain (--embargo-unchecked): ${waved.length} branch(es) - ${o.embargoUnchecked}`] : [];
  await announce(settings.env, { title, message: [...done, ...wavedLine, ...(left.length ? [`not reached: ${left.join(', ')}`] : []), ...(stop ? ['', stop] : [])].join('\n'), result }, {
    log: (l) => console.log(`  ${dim(l)}`),
  });
  return exit;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).then(
    (code) => process.exit(code),
    (e) => {
      console.error(`merge-queue: ${e?.stack ?? e}`);
      process.exit(1);
    },
  );
}
