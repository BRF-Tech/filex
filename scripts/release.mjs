#!/usr/bin/env node
// pnpm release X.Y.Z — cut a filex release, in order, behind gates that cannot
// be skipped.
//
//   pnpm release 0.45.0                    start (stops at the first red gate
//                                          or at the first step that is a person's)
//   pnpm release 0.45.0 --resume           carry on from where it stopped
//   pnpm release 0.45.0 --resume --ack audit     a person confirms a step only a
//                                          person can do (audit | export-withheld | deploy)
//   pnpm release 0.45.0 --dry-run          every gate, nothing written: the stamp
//                                          goes to a scratch copy, the export to a
//                                          throwaway clone, no state is kept
//   pnpm release 0.45.0 --until docs       stop after that stage (to run the long
//                                          chain later)
//   pnpm release 0.45.0 --plan             print the stages and their gates, run nothing
//   pnpm release 0.45.0 --status           what the recorded run has done so far
//   --export <dir>                         the public checkout (default: the plan's)
//
// ⚠⚠ Why this exists. A release was ~10 ordered steps plus six more to
// deploy, and every step that was skipped was skipped SILENTLY: nothing
// failed, something wrong was published. On the night of 2026-09-24/25 four
// re-cuts went out, each for a gate somebody had to remember:
//
//   v0.43.1  an uncommitted workflow change in the public checkout was thrown
//            away before the export, and a CHANGELOG edit went in untested
//            (lessons #461, #462)
//   v0.44.0  GoReleaser failed on `envOrDefault` AFTER npm and the images had
//            gone out (lesson #510)
//   v0.44.1  a publisher token may only be `{{ .Env.X }}` — same step, again
//   pre-tag  two tests assumed "the newest CHANGELOG section is this release"
//            and went red on the tag run
//   docs     docs.filex.sh kept serving the previous release (lesson #511)
//
// So the order lives here, every gate is judged by its own exit code, and a
// red gate STOPS the release. There is no option to skip a gate — an unknown
// option is refused, so `--skip`, `--force` and friends do not quietly work.
//
// ⚠ What this script never does: sign a tag, push anything, deploy anything.
// Those are a person's steps. The script stops, prints the exact commands, and
// on --resume checks what the person did — the tag's signature and target,
// what the remotes now hold (a public tag naming a private commit is refused
// loudly, lesson #55), and what the servers, feeds and docs site serve.
//
// Where things are:
//   the order and the gate logic   scripts/release/stages.mjs
//   THIS repository's gates        scripts/release/plan.mjs  ← edit gates here
//   text-only rules (unit-tested)  scripts/release/checks.mjs
//   live-surface checks            scripts/release/verify.mjs
//   run state + logs               <git dir>/filex-release/vX.Y.Z.json and vX.Y.Z/logs/
//                                  (inside .git, so the tree stays clean)
//
// Exit codes: 0 released (or a green dry run) · 1 a gate is red · 2 usage ·
// 3 waiting for a person — do what it printed, then --resume.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { versionProblems } from './release/checks.mjs';
import { Gates, banner, bold, dim, findBash, gitOut, green, loadState, red, revParse, saveState, slash, stateFile, takeLock, yellow } from './release/engine.mjs';
import { ACKS, RUNNERS, STAGES, forgetRemotes } from './release/stages.mjs';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

function usage(msg) {
  if (msg) console.error(`release: ${msg}\n`);
  const lines = fs.readFileSync(fileURLToPath(import.meta.url), 'utf8').split('\n').slice(1);
  const end = lines.findIndex((l) => !l.startsWith('//'));
  console.error(lines.slice(0, end < 0 ? lines.length : end).join('\n').replace(/^\/\/ ?/gm, '').split('\n\n').slice(0, 2).join('\n\n'));
  console.error('\n(the whole story is at the top of scripts/release.mjs)');
  process.exit(2);
}

// ── arguments ───────────────────────────────────────────────────────────────
//
// ⚠ Lesson #405: a runner that reads options with `argv.includes()` treats an
// option it does not know as absent — a filter silently drops, a "skip" flag
// silently "works". Every option is on this table; anything else stops.
const argv = process.argv.slice(2);
const opts = { dry: false, resume: false, plan: false, status: false, until: null, exportDir: null, acks: new Set() };
const positional = [];
for (let i = 0; i < argv.length; i++) {
  const a = argv[i];
  const next = () => {
    const v = argv[++i];
    if (v === undefined || v.startsWith('--')) usage(`${a} needs a value`);
    return v;
  };
  if (a === '--dry-run') opts.dry = true;
  else if (a === '--resume') opts.resume = true;
  else if (a === '--plan') opts.plan = true;
  else if (a === '--status') opts.status = true;
  else if (a === '--until') opts.until = next();
  else if (a === '--export') opts.exportDir = next();
  else if (a === '--ack') {
    const v = next();
    if (!ACKS.includes(v)) usage(`--ack ${v}: a person can confirm only ${ACKS.join(', ')}`);
    opts.acks.add(v);
  } else if (a === '-h' || a === '--help') usage();
  else if (a.startsWith('-')) usage(`unknown option ${a} — there is no option to skip or force a gate`);
  else positional.push(a);
}
if (positional.length !== 1) usage('name exactly one version, e.g. pnpm release 0.45.0');
const version = positional[0];
const shape = versionProblems(version, null);
if (shape.length) usage(shape.join('\n'));
if (opts.until && !STAGES.some((s) => s.id === opts.until)) usage(`--until ${opts.until}: stages are ${STAGES.map((s) => s.id).join(', ')}`);
if (opts.dry && opts.resume) usage('--dry-run always starts from the beginning; it does not read or write a recorded run');
const tag = `v${version}`;

// ── the plan ────────────────────────────────────────────────────────────────
const planModule = await import(pathToFileURL(path.join(REPO, 'scripts', 'release', 'plan.mjs')).href);
const plan = {
  branch: 'main',
  remote: 'origin',
  exportRemote: 'origin',
  exportScript: 'scripts/export-public.sh',
  backendTagPrefix: 'backend/',
  signingKeys: [],
  privateHosts: { allow: [], forbid: [] },
  docSurfaces: [],
  audit: [],
  docs: [],
  pretag: [],
  exportGates: [],
  published: [],
  deployed: [],
  deployChecklist: [],
  ciWatch: [],
  ...(await planModule.default({ repo: REPO, version, tag })),
};
const exp = path.resolve(opts.exportDir ?? plan.exportDir ?? path.join(REPO, '..', 'filex-export'));

if (opts.plan) {
  console.log(`${bold(`filex release ${tag}`)} — the plan (nothing runs)\n`);
  const byStage = { audit: plan.audit, docs: plan.docs, pretag: plan.pretag, export: plan.exportGates, ci: plan.published, deploy: plan.deployed };
  STAGES.forEach((s, i) => {
    console.log(`${String(i + 1).padStart(2)}. ${bold(s.title.padEnd(10))} ${s.what}`);
    for (const g of s.builtin ?? []) console.log(`      ${dim('·')} ${dim(g)}`);
    for (const g of byStage[s.id] ?? []) console.log(`      - ${g.name}`);
  });
  console.log(`\n· built in (scripts/release/stages.mjs)    - this repository's plan (scripts/release/plan.mjs)`);
  process.exit(0);
}

// ── state ───────────────────────────────────────────────────────────────────
const gitDir = gitOut(REPO, 'rev-parse', '--absolute-git-dir');
const stFile = stateFile(gitDir, tag);
const recorded = loadState(stFile);

if (opts.status) {
  if (!recorded) {
    console.log(`no recorded run of ${tag} in ${slash(stFile)}`);
    process.exit(0);
  }
  console.log(`${bold(`filex release ${tag}`)} — ${recorded.complete ? green('released') : 'in progress'}  ${dim(slash(stFile))}`);
  for (const s of STAGES) {
    const st = recorded.stages[s.id];
    console.log(`  ${(st?.status ?? '—').padEnd(8)} ${s.title.padEnd(10)} ${st?.at ? dim(st.at) : ''}${st?.note ? `  ${dim(st.note)}` : ''}`);
  }
  const reds = recorded.ledger.filter((l) => !l.ok).slice(-5);
  for (const l of reds) console.log(`  ${red('last red')}  ${l.stage}: ${l.name}  ${dim(l.log)}`);
  process.exit(0);
}

let lock = null;
if (!opts.dry) {
  if (recorded && !opts.resume) {
    usage(`a release of ${tag} is already under way (${slash(stFile)}).\n` +
      `Carry on with:  pnpm release ${version} --resume\n` +
      `(To start over, delete that file — and undo what the run did: the release commit, the export in the public checkout.)`);
  }
  if (!recorded && opts.resume) usage(`there is no recorded run of ${tag} to resume — start it with: pnpm release ${version}`);
  lock = takeLock(`${stFile}.lock`);
  if (lock.held) {
    console.error(`release: another run of ${tag} is active (pid ${lock.held}). One at a time.`);
    process.exit(2);
  }
  process.on('exit', () => lock?.release?.());
  process.on('SIGINT', () => process.exit(130));
}

const state = opts.dry || !recorded
  ? { version, tag, started: new Date().toISOString(), stages: {}, acks: {}, ledger: [], complete: false }
  : recorded;
if (state.complete) {
  console.log(`${tag} has already been released through this tool (${slash(stFile)}).`);
  process.exit(0);
}

const tmp = opts.dry ? fs.mkdtempSync(path.join(os.tmpdir(), `filex-release-dry-${version}-`)) : null;
const logsDir = opts.dry ? path.join(tmp, 'logs') : path.join(gitDir, 'filex-release', tag, 'logs');
const bash = findBash();
const save = () => {
  if (!opts.dry) saveState(stFile, state);
};

const R = {
  repo: REPO,
  exp,
  version,
  tag,
  dry: opts.dry,
  acks: opts.acks,
  plan,
  state,
  bash,
  tmp,
  exportTarget: exp,
  gates: new Gates({
    logsDir,
    bash,
    repo: REPO,
    onRecord: (rec) => {
      if (!opts.dry) {
        state.ledger.push(rec);
        save();
      }
    },
  }),
  ctx() {
    return {
      repo: REPO,
      exportTarget: R.exportTarget,
      exportDir: exp,
      version,
      tag,
      prevTag: state.prev ?? null,
      releaseCommit: state.releaseCommit ?? revParse(REPO, 'HEAD'),
      exportHead: state.exportHead ?? null,
      dry: opts.dry,
      bash,
      logsDir,
    };
  },
};

// ── run ─────────────────────────────────────────────────────────────────────
console.log(`${bold(`filex release ${tag}`)}${opts.dry ? `  ${yellow('DRY RUN — nothing is written, pushed or tagged')}` : ''}`);
let branchName = 'detached';
try {
  branchName = gitOut(REPO, 'symbolic-ref', '--short', '-q', 'HEAD') || branchName;
} catch {
  /* detached: the preflight says so */
}
console.log(`  repo    ${slash(REPO)}  (${branchName} @ ${revParse(REPO, 'HEAD')?.slice(0, 10)})`);
console.log(`  export  ${slash(exp)}`);
console.log(`  ${opts.dry ? 'logs  ' : 'state '}  ${slash(opts.dry ? logsDir : stFile)}`);
if (!bash) console.log(`  ${red('no bash')}: Git for Windows' bash.exe was not found; every shell gate will fail`);

let exitCode = 0;
let dryWaits = 0;
for (let i = 0; i < STAGES.length; i++) {
  const s = STAGES[i];
  banner(`${i + 1}/${STAGES.length} ${s.title}`, dim(s.what));
  let res;
  try {
    forgetRemotes();
    res = await RUNNERS[s.id](R);
  } catch (e) {
    console.log(`  ${red('FAILED')}  the ${s.id} stage crashed: ${e?.stack ?? e}`);
    res = { status: 'red' };
  }
  const { instructions, dry, ...rest } = res;
  state.stages[s.id] = { ...rest, at: new Date().toISOString() };
  save();

  if (res.status === 'red') {
    console.log(`\n${red(bold(`STOPPED at ${s.id}`))}: a gate is red. Fix what it says and run:`);
    console.log(`    pnpm release ${version}${opts.dry ? ' --dry-run' : ' --resume'}`);
    console.log(dim('There is no option to skip a gate.'));
    exitCode = 1;
    break;
  }
  if (res.status === 'waiting') {
    if (opts.dry) {
      dryWaits++;
      console.log(`  ${yellow('person')}  ${dim('a real run stops here until a person has done this:')}`);
      for (const l of instructions) console.log(`          ${dim(l)}`);
      state.stages[s.id].status = 'dry';
    } else {
      console.log(`\n${yellow(bold(`WAITING at ${s.id} — this step is yours. RUN THIS NOW:`))}\n`);
      for (const l of instructions) console.log(`    ${l}`);
      exitCode = 3;
      break;
    }
  }
  if (i === STAGES.length - 1 && !opts.dry) {
    state.complete = true;
    state.finished = new Date().toISOString();
    save();
    console.log(`\n${green(bold(`${tag} is released, and everything it published was read back.`))}`);
  } else if (opts.until === s.id) {
    console.log(`\nstopped after ${s.id}, as asked (--until). Carry on with: pnpm release ${version}${opts.dry ? ' --dry-run' : ' --resume'}`);
    break;
  }
}

if (opts.dry) {
  const reds = R.gates.results.filter((r) => !r.ok).length;
  const greens = R.gates.results.length - reds;
  if (exitCode === 0) {
    console.log(`\n${green(bold('DRY RUN GREEN'))}: ${greens} gate(s) passed, ${dryWaits} step(s) would wait for a person. Nothing was written.`);
  } else {
    console.log(`\n${red(bold('DRY RUN RED'))}: ${reds} gate(s) failed. Nothing was written.`);
  }
  for (const sub of ['stamp', 'export']) fs.rmSync(path.join(tmp, sub), { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
  console.log(dim(`logs: ${slash(logsDir)}`));
}
process.exit(exitCode);
