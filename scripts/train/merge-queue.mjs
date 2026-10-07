#!/usr/bin/env node
// The train's merge queue: a release's branches merged one after another onto
// the release branch - each on top of the one before, each with --no-ff and
// its own message file - CHANGELOG conflicts merged by section, and every
// merge built and vetted before the next one starts.
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
//                    checkout), e.g. --check 'pnpm -s --filter ./web build'
//   --no-go          skip the Go build + vet after every merge
//   --env FILE       settings (default: $FILEX_TRAIN_ENV, else
//                    ~/.config/filex-train.env): who is told when it ends
//
// A queue line:   <branch> [<message file>]     (# starts a comment)
//
// Exit: 0 every branch is in and every check green; 1 a check is red or the
// queue could not start; 3 a conflict waits for a person (the merge was
// aborted, the tree is as it was before it).
//
// ⚠ Why this exists (#179): the 0.52 and 0.53 rounds merged ten to fifteen
// branches each by hand in the main session - the same five commands per
// branch, the CHANGELOG conflict resolved with a scratch script, and the
// build checked only at the end. An agent's own merge onto main is refused by
// the permission system, so every merge waited for the main session's turn.
// One command, approved once, does the whole queue and stops only where a
// person is needed.
//
// What it does for each line, in order:
//   1. a branch whose tip is already in the release branch is skipped - so
//      running the queue again after a stop carries on where it stopped;
//   2. `git merge --no-ff --no-commit <tip>` with rerere on: a conflict
//      resolved once (in a rehearsal, or by hand) is resolved again by itself;
//   3. CHANGELOG.md in conflict: merged by Keep a Changelog section
//      (changelog-merge.mjs); any other file still in conflict: the merge is
//      aborted and the queue stops for a person;
//   4. [Unreleased] gets one heading per kind (a clean merge can leave
//      `### Changed` twice, lesson #706);
//   5. committed with the line's message file, as written;
//   6. checked: [Unreleased] is well formed, the Go module builds and vets,
//      and every --check. A red check stops the queue with the merge made - it
//      is never undone by the queue; fix it on top and run the queue again.
// When the queue ends, however it ends, it says so (scripts/lib/notify.mjs).

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { goShellArgv } from '../lib/go-build.mjs';
import { announce } from '../lib/notify.mjs';
import { loadSettings } from '../lib/settings.mjs';
import { Gates, banner, bold, dim, findBash, git, gitOut, green, red, revParse, run, slash, yellow } from '../release/engine.mjs';
import { normalizeChangelog, resolveInGit, unreleasedProblems } from './changelog-merge.mjs';
import { since, usageOf } from './cli.mjs';

const CHANGELOG = 'CHANGELOG.md';

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
  const o = { queue: null, onto: 'main', msgDir: null, repo: null, plan: false, checks: [], go: true, env: '' };
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
    else if (a === '--env') o.env = next();
    else if (a === '-h' || a === '--help') o.help = true;
    else throw new Error(`unknown option ${a}`);
  }
  if (!o.help && !o.queue) throw new Error('--queue FILE is required');
  return o;
}

const USAGE = usageOf(import.meta.url);

function abortMerge(repo) {
  git(repo, 'merge', '--abort');
}

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
  }

  console.log(`${bold('merge queue')} onto ${bold(o.onto)} in ${slash(repo)} ${dim(`(${slash(queueFile)})`)}\n`);
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
    const tm = Date.now();
    const m = run('git', ['-C', repo, ...rr, 'merge', '--no-ff', '--no-commit', r.tip], { env: { GIT_MERGE_AUTOEDIT: 'no' } });
    if (m.status !== 0 && !revParse(repo, 'MERGE_HEAD')) {
      stop = `${r.branch}: git merge did not start:\n${(m.stderr || m.stdout).trim()}`;
      exit = 1;
      break;
    }
    const unmerged = git(repo, 'diff', '--name-only', '--diff-filter=U').stdout.split('\n').map((s) => s.trim());
    const c = splitConflicts(unmerged);
    if (c.others.length) {
      abortMerge(repo);
      stop = [
        `${r.branch}: in conflict beyond the changelog - ${c.others.join(', ')}. The merge was aborted; nothing changed.`,
        'Merge it by hand, with rerere on so the next run (and a re-run of this queue) replays it:',
        `    git -C ${slash(repo)} ${rr.join(' ')} merge --no-ff --no-commit ${r.tip}`,
        `    node scripts/train/changelog-merge.mjs --git --repo ${slash(repo)}     # if CHANGELOG.md is in conflict too`,
        '    (resolve the rest, git add them)',
        `    git -C ${slash(repo)} -c rerere.enabled=true commit --cleanup=whitespace -F ${slash(r.msg)}`,
        'then run the queue again: it carries on after this branch.',
      ].join('\n');
      exit = 3;
      break;
    }
    if (c.changelog) {
      const res = resolveInGit(repo, CHANGELOG);
      if (!res.ok) {
        abortMerge(repo);
        stop = `${r.branch}: CHANGELOG.md could not be merged by section, and that is a person's call:\n${res.why}\nThe merge was aborted. Merge it by hand (the commands above apply), then run the queue again.`;
        exit = 3;
        break;
      }
      console.log(`  ${green('ok    ')}  CHANGELOG.md merged by section`);
    }
    // One heading per kind, whether or not git saw a conflict.
    const staged = git(repo, 'diff', '--cached', '--name-only', 'HEAD').stdout.split('\n').map((s) => s.trim()).filter(Boolean);
    if (staged.includes(CHANGELOG)) {
      const file = path.join(repo, CHANGELOG);
      const n = normalizeChangelog(fs.readFileSync(file, 'utf8'));
      if (n.changed) {
        fs.writeFileSync(file, n.text);
        git(repo, 'add', '--', CHANGELOG);
        console.log(`  ${green('ok    ')}  [Unreleased]: one heading per kind`);
      }
    }
    const marked = staged.filter((f) => {
      const show = git(repo, 'show', `:${f}`);
      return show.status === 0 && MARKER.test(show.stdout);
    });
    if (marked.length) {
      abortMerge(repo);
      stop = `${r.branch}: conflict markers would be committed in ${marked.join(', ')} (a rerere replay gone wrong?). The merge was aborted.`;
      exit = 1;
      break;
    }
    const commit = run('git', ['-C', repo, '-c', 'rerere.enabled=true', 'commit', '--cleanup=whitespace', '-F', r.msg]);
    if (commit.status !== 0) {
      abortMerge(repo);
      stop = `${r.branch}: git commit failed (the merge was aborted):\n${(commit.stderr || commit.stdout).trim()}`;
      exit = 1;
      break;
    }
    const after = revParse(repo, 'HEAD');
    console.log(`  ${green('ok    ')}  merged as ${after.slice(0, 10)} ${dim(`(${since(tm)})`)}`);

    // ── the checks ──────────────────────────────────────────────────────────
    const gates = new Gates({ logsDir: path.join(runDir, `${String(i + 1).padStart(2, '0')}-${msgName(r.branch).replace(/\.txt$/, '')}`), bash, repo });
    const specs = [
      {
        name: '[Unreleased] holds each heading once and no conflict marker',
        check: () => {
          let text = '';
          try {
            text = fs.readFileSync(path.join(repo, CHANGELOG), 'utf8');
          } catch {
            return { ok: true, detail: 'no CHANGELOG.md here' };
          }
          const p = unreleasedProblems(text);
          return p.length ? { ok: false, detail: p.join('\n') } : { ok: true };
        },
      },
      ...(o.go && fs.existsSync(path.join(repo, 'backend', 'go.mod'))
        ? [{ name: 'go: build + vet', cmd: goShellArgv({ dir: path.join(repo, 'backend'), checkout: repo, script: goCheckScript(), bash: bash ?? 'bash' }) }]
        : []),
      ...o.checks.map((sh, k) => ({ name: `check ${k + 1}: ${sh}`, sh })),
    ];
    const green_ = await gates.all('merge', specs, {});
    if (!green_) {
      stop = [
        `${r.branch}: merged as ${after.slice(0, 10)}, and a check is red (logs above).`,
        'The queue does not undo a merge. Fix it with a commit on top and run the queue again,',
        `or take the merge back yourself: git -C ${slash(repo)} reset --hard ${before.slice(0, 10)}`,
      ].join('\n');
      exit = 1;
      done.push(`${r.branch}: merged ${after.slice(0, 10)}, checks RED`);
      break;
    }
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
  await announce(settings.env, { title, message: [...done, ...(left.length ? [`not reached: ${left.join(', ')}`] : []), ...(stop ? ['', stop] : [])].join('\n'), result }, {
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
