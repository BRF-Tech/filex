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
//   pnpm release 0.45.0 --resume --only deploy
//                                          once the tags are out: the deploy checks
//                                          alone, on the tagged commits (when the ci
//                                          stage is held red by a store that is not
//                                          ours to hurry). Confirms nothing.
//   --profile minor|patch|full             which gates run where (task #172):
//                                          minor (default) the fast gates here, the
//                                          heavy suites on GitHub or here during the
//                                          gate stage; patch (X.Y.Z+1 only) builds
//                                          always, the rest only for what changed;
//                                          full every gate here, as up to 0.52
//   --no-cache                             run every gate, even one that passed on
//                                          exactly these inputs before
//   --chain <result.json>                  a green scripts/chain run on this tree
//                                          stands in for the heavy gates a minor
//                                          would run here at the gate stage
//   --gate github|circleci                 where the gate stage reads the export
//                                          commit's suites (task #181): GitHub
//                                          (default), or CircleCI's workflow `ci`
//                                          when GitHub Actions is down - no dry
//                                          run then; kept for later resumes
//   --export <dir>                         the public checkout (default: the plan's)
//
// ⚠⚠ The order (task #76): stamp → pretag → export → a person commits the
// export and pushes BOTH mains WITHOUT a tag → the gate starts release.yml's
// dry run on the export commit and waits until it and ci.yml passed there →
// a person signs the tags on exactly those commits and pushes them → the tag
// run publishes (its `verify` job refuses a commit without those two runs) →
// ci → deploy. A red gate spends no number: fix main and resume. Once a tag
// is on a remote, a resume never goes back to stamp or pretag.
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
// ⚠ What this script never does: commit the export, sign a tag, push anything,
// deploy anything. Those are a person's steps. The script stops, prints the
// exact commands, and on --resume checks what the person did — the tag's
// signature and target, what the remotes now hold (a public tag naming a
// private commit is refused loudly, lesson #55), and what the servers, feeds
// and docs site serve. The one thing it starts on GitHub is the dry run of
// release.yml (publish=false), which publishes nothing.
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

import { GATE_SOURCES, PROFILES, newestTag, selectGates, versionProblems } from './release/checks.mjs';
import { Gates, banner, bold, dim, findBash, gitOut, green, loadState, lsRemote, red, revParse, saveState, slash, stateFile, takeLock, yellow } from './release/engine.mjs';
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
const opts = { dry: false, resume: false, plan: false, status: false, until: null, only: null, exportDir: null, acks: new Set(), profile: null, noCache: false, chain: null, gate: null };
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
  else if (a === '--only') opts.only = next();
  else if (a === '--export') opts.exportDir = next();
  else if (a === '--profile') {
    const v = next();
    if (!PROFILES.includes(v)) usage(`--profile ${v}: the profiles are ${PROFILES.join(', ')}`);
    opts.profile = v;
  } else if (a === '--gate') {
    const v = next();
    if (!GATE_SOURCES.includes(v)) usage(`--gate ${v}: the gate reads ${GATE_SOURCES.join(' or ')}`);
    opts.gate = v;
  } else if (a === '--no-cache') opts.noCache = true;
  else if (a === '--chain') opts.chain = path.resolve(next());
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
// A patch profile tests only what changed since the last release: X.Y.Z+1
// alone. Preflight checks the step against the last tag as well.
if (opts.profile === 'patch' && Number(version.split('.')[2]) === 0) {
  usage(`--profile patch: ${version} is no patch number (X.Y.Z with Z above 0) — a minor or a major takes the minor profile, or full`);
}
if (opts.chain && !fs.existsSync(opts.chain)) usage(`--chain ${opts.chain}: no such file (the result.json of a scripts/chain run)`);
if (opts.until && !STAGES.some((s) => s.id === opts.until)) usage(`--until ${opts.until}: stages are ${STAGES.map((s) => s.id).join(', ')}`);
if (opts.dry && opts.resume) usage('--dry-run always starts from the beginning; it does not read or write a recorded run');
// --only re-reads what a published release deployed. It runs no other stage,
// and it can neither finish a release nor take a person's confirmation: a red
// ci stage stays red (lesson #1023: a Snap review held v0.51.0's ci stage, and
// the deploy checks had to be run by a hand-written script).
if (opts.only !== null) {
  if (opts.only !== 'deploy') usage(`--only ${opts.only}: only the deploy checks run on their own (--only deploy)`);
  if (!opts.resume) usage('--only deploy reads back a release that is under way: use it with --resume');
  if (opts.acks.size || opts.until) usage('--only deploy confirms nothing and stops nowhere else: drop --ack and --until');
  if (opts.profile || opts.chain || opts.noCache || opts.gate) usage('--only deploy runs no test gate: drop --profile, --chain, --no-cache and --gate');
}
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
  jobs: 1,
  pretag: [],
  heavy: [],
  chainProfiles: {},
  exportGates: [],
  published: [],
  deployed: [],
  deployChecklist: [],
  ciWatch: [],
  ...(await planModule.default({ repo: REPO, version, tag })),
};
const exp = path.resolve(opts.exportDir ?? plan.exportDir ?? path.join(REPO, '..', 'filex-export'));

if (opts.plan) {
  const profile = opts.profile ?? 'minor';
  // A patch's choice depends on what changed since the newest release tag.
  let changed = null;
  let since = null;
  if (profile === 'patch') {
    try {
      since = newestTag(gitOut(REPO, 'tag', '--list', 'v*').split('\n').map((t) => t.trim()).filter(Boolean));
      if (since) changed = gitOut(REPO, 'diff', '--name-only', '--no-renames', since, 'HEAD').split('\n').filter(Boolean);
    } catch {
      changed = null;
    }
  }
  const source = opts.gate ?? 'github';
  const sel = selectGates(plan, profile, changed, { source });
  const ci = source === 'circleci' ? 'CircleCI' : 'GitHub';
  console.log(`${bold(`filex release ${tag}`)} — the plan, profile ${bold(profile)}${source !== 'github' ? `, gate ${bold(source)}` : ''}${since ? ` (${changed?.length ?? '?'} file(s) changed since ${since})` : ''} (nothing runs)\n`);
  const byStage = { audit: plan.audit, docs: plan.docs, pretag: sel.pretag, export: plan.exportGates, gate: sel.local, ci: plan.published, deploy: plan.deployed };
  STAGES.forEach((s, i) => {
    console.log(`${String(i + 1).padStart(2)}. ${bold(s.title.padEnd(10))} ${s.what}`);
    for (const g of s.builtin ?? []) console.log(`      ${dim('·')} ${dim(g)}`);
    for (const g of byStage[s.id] ?? []) console.log(`      - ${g.name}${s.id === 'gate' ? dim(`  (here, while ${ci} tests)`) : ''}`);
    if (s.id === 'gate') for (const g of sel.remote) console.log(`      - ${g.name}${dim(`  (read from ${source === 'circleci' ? `CircleCI ${g.circleci}` : g.github})`)}`);
  });
  if (sel.skipped.length) {
    console.log(`\nnot run in the ${profile} profile:`);
    for (const x of sel.skipped) console.log(`      - ${x.gate.name}${dim(`  (${x.why})`)}`);
  }
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
  console.log(`${bold(`filex release ${tag}`)} — ${recorded.complete ? green('released') : 'in progress'}, profile ${recorded.profile ?? 'full'}, gate ${recorded.gateSource ?? 'github'}  ${dim(slash(stFile))}`);
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
// The profile a run started with holds for its resumes unless one names
// another (a patch found to be more than a patch: --resume --profile minor).
const profile = opts.profile ?? state.profile ?? 'minor';
const profileChanged = !!(state.profile && opts.profile && opts.profile !== state.profile);
const profileWas = state.profile;
state.profile = profile;
// Where the gate stage reads the export commit's suites (#181). Kept like the
// profile: a resume after `--gate circleci` reads CircleCI again unless it
// names GitHub (Actions is back). The stage records which one passed it.
const gateSource = opts.gate ?? state.gateSource ?? 'github';
const gateChanged = !!(opts.gate && opts.gate !== (state.gateSource ?? 'github'));
state.gateSource = gateSource;

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
  save,
  exportTarget: exp,
  profile,
  gateSource,
  chain: opts.chain,
  // A patch's changed files (stages.mjs → pretag); null: not asked, or none to compare with.
  changed: null,
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
    // A gate that passed on exactly these inputs is not run again (#172).
    // Beside the run state, inside the git dir: the tree stays clean. A dry
    // run reads it and writes nothing; --no-cache only stops the reading.
    cache: { dir: path.join(gitDir, 'filex-release', 'gate-cache'), read: !opts.noCache, write: !opts.dry },
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
      profile,
      changed: R.changed,
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
console.log(`  profile ${profile}${profileChanged ? ` ${yellow(`(was ${profileWas}: changed by --profile)`)}` : ''}${opts.noCache ? ' · every gate runs (--no-cache)' : ''}${opts.chain ? ` · chain ${slash(opts.chain)}` : ''}`);
if (gateSource !== 'github' || gateChanged) {
  console.log(`  gate    ${gateSource === 'circleci' ? yellow('circleci') : gateSource}${gateChanged ? ` ${yellow('(changed by --gate)')}` : ''}${gateSource === 'circleci' ? dim(' - CircleCI stands in for GitHub Actions: no dry run of the release') : ''}`);
}
console.log(`  ${opts.dry ? 'logs  ' : 'state '}  ${slash(opts.dry ? logsDir : stFile)}`);
if (!bash) console.log(`  ${red('no bash')}: Git for Windows' bash.exe was not found; every shell gate will fail`);

// ⚠ Once a tag is on a remote the release IS that tag, and main is free to
// move on — a deploy can take hours. Everything before the tag is a record of
// how it was made, not something to redo on today's HEAD: v0.47.0's resume,
// after fixes landed on main, re-stamped HEAD as the release commit, re-ran
// the whole test chain on it, and would have asked for the published tag to
// be moved (v0.46.0 the same, 2026-09-26). The release commit is read back
// from the tag, and it has to be the commit the test chain passed on.
//
// "On a remote" is asked of the remotes, not of the record: a person who
// pushed the tags and then a fix to main, before running --resume, has a
// record that still says "push: waiting" — and that resume re-stamped too.
const BEFORE_TAG = ['preflight', 'audit', 'docs', 'stamp', 'pretag', 'export'];
const PUBLISHED = new Set([...BEFORE_TAG, 'land', 'gate', 'sign', 'push']);
const published = !opts.dry && state.stages.push?.status === 'done';
let frozen = published;
if (!opts.dry && !published && state.releaseCommit) {
  const asked = [lsRemote(REPO, plan.remote, ['--tags']), fs.existsSync(exp) ? lsRemote(exp, plan.exportRemote, ['--tags']) : { map: new Map() }];
  const err = asked.find((a) => a.error);
  if (err) {
    console.log(`  ${red('FAILED')}  could not ask the remotes whether ${tag} is pushed (${err.error}) — unknown does not pass: a resume that guesses "not yet" re-stamps a published release`);
    process.exit(1);
  }
  frozen = asked.some((a) => a.map.has(`refs/tags/${tag}`));
}
if (frozen) {
  const remoteTagged = lsRemote(REPO, plan.remote, ['--tags']).map?.get(`refs/tags/${tag}^{}`) ?? null;
  const tagged = revParse(REPO, `${tag}^{commit}`) ?? remoteTagged;
  if (published ? !tagged || tagged !== state.stages.pretag?.head : tagged && tagged !== state.stages.pretag?.head) {
    console.log(`  ${red('FAILED')}  ${tag} names ${tagged?.slice(0, 10) ?? 'nothing here'}, but the test chain passed on ${state.stages.pretag?.head?.slice(0, 10) ?? 'no commit'}`);
    process.exit(1);
  }
  const unfinished = BEFORE_TAG.filter((id) => state.stages[id]?.status !== 'done');
  if (unfinished.length) {
    console.log(`  ${red('FAILED')}  ${tag} is on a remote, but ${unfinished.join(', ')} never finished: a published tag is never moved, and nothing before it can be redone. Tell the maintainer.`);
    process.exit(1);
  }
  if (tagged) state.releaseCommit = tagged;
  save();
}
if (opts.only && !frozen) {
  console.log(`  ${red('FAILED')}  --only deploy reads back a published release; ${tag} is on no remote yet`);
  process.exit(1);
}

let exitCode = 0;
let dryWaits = 0;
for (let i = 0; i < STAGES.length; i++) {
  const s = STAGES[i];
  if (opts.only && s.id !== opts.only) continue;
  banner(`${i + 1}/${STAGES.length} ${s.title}`, dim(s.what));
  if ((published && PUBLISHED.has(s.id)) || (frozen && BEFORE_TAG.includes(s.id))) {
    console.log(`  ${green('ok')}      published as ${tag} (${state.releaseCommit.slice(0, 10)}) — recorded ${state.stages[s.id]?.at ?? ''}; main has moved on and that is fine`);
    continue;
  }
  if (opts.only) {
    // The deploy checks, on the tagged commits; nothing is recorded as done
    // and no confirmation is taken, so the release stays where it was.
    forgetRemotes();
    let res;
    try {
      res = await RUNNERS[s.id](R);
    } catch (e) {
      console.log(`  ${red('FAILED')}  the ${s.id} stage crashed: ${e?.stack ?? e}`);
      res = { status: 'red' };
    }
    const passed = res.status !== 'red';
    console.log(`\n${passed ? green(bold(`the ${s.id} checks are green`)) : red(bold(`the ${s.id} checks are red`))} on ${tag} (${state.releaseCommit.slice(0, 10)}). The release stays where it was: ${dim(`pnpm release ${version} --status`)}`);
    exitCode = passed ? 0 : 1;
    break;
  }
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
