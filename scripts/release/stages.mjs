// The release, in the order it has to happen. Each stage either finishes
// ("done"), stops on a red gate ("red"), or stops because the next step is a
// person's ("waiting") — tagging, pushing and deploying are never done by this
// script, only checked after a person has done them.
//
// A stage that finished is not trusted blindly on --resume: it records what it
// ran against (the commit, the export tree) and runs again when that moved.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  bumpKind,
  classifyDeletions,
  closingKeywords,
  dateChangelog,
  globMatch,
  hasContent,
  hasSection,
  localDate,
  newestTag,
  packageVersion,
  privateHostLines,
  sectionGroups,
  setPackageVersion,
  unreleasedBody,
  versionProblems,
  workspacePackages,
} from './checks.mjs';
import { bold, dim, git, gitOut, lsRemote, revParse, run, slash, yellow } from './engine.mjs';

// `builtin` names the gates this file runs itself, whatever the plan says —
// printed by `--plan` so the whole sequence is readable in one place.
export const STAGES = [
  {
    id: 'preflight', title: 'preflight', what: 'before anything is written',
    builtin: ['on main, tree clean (untracked too)', 'the number is valid and newer than the last tag', 'main is pushed', 'the tag exists nowhere yet (private and public)', '[Unreleased] has content', 'deployment targets name the current release', 'public checkout: present, clean, workflows committed, in step with GitHub'],
  },
  { id: 'audit', title: 'audit', what: 'README, screenshots, documentation — a person', builtin: ['waits for: --ack audit (bound to the commit it was given on)'] },
  { id: 'docs', title: 'docs', what: 'links, site build, anchors, YAML', builtin: [] },
  {
    id: 'stamp', title: 'stamp', what: 'CHANGELOG date, versions, deploy targets, release commit',
    builtin: ['[Unreleased] → [X.Y.Z] - today', 'every workspace package.json', 'sync-deploy-versions, then --check', 'the release page renders from the changelog', 'nothing else changed', 'commit "chore(release): vX.Y.Z" — by path'],
  },
  { id: 'pretag', title: 'pretag', what: 'the whole test chain, on the release commit', builtin: ['(after the plan) the chain left the tree as it found it'] },
  {
    id: 'export', title: 'export', what: 'the public tree, and what must not be in it',
    builtin: ['public checkout gates again', 'scripts/export-public.sh', 'no deletion without a reason (deleted in source / withheld: --ack export-withheld)', 'no private host or module path', 'workflows untouched'],
  },
  {
    id: 'sign', title: 'sign', what: 'signed tags — a person',
    builtin: ['private tag: annotated, good signature by the release key, on the tested commit', 'public commit: exactly the exported tree, one commit, no closing keywords', 'public tag: signed, on the export commit, not the private one', 'backend/ tag (optional): signed, backend/ identical'],
  },
  { id: 'push', title: 'push', what: 'one ref at a time — a person', builtin: ['private remote: main and the tag are the release commit', 'public remote: main and the tag are the export commit — a tag naming the private commit is the lesson #55 leak'] },
  { id: 'ci', title: 'ci', what: 'what the release workflow published', builtin: [] },
  { id: 'deploy', title: 'deploy', what: 'servers, feeds, embeds, docs — a person, then verified', builtin: ['waits for: --ack deploy'] },
];

/** The acknowledgements a person may give, one per human-only judgement. */
export const ACKS = ['audit', 'export-withheld', 'deploy'];

const short = (sha) => (sha ? sha.slice(0, 10) : '(none)');
const done = (extra = {}) => ({ status: 'done', ...extra });
const red = (extra = {}) => ({ status: 'red', ...extra });
const waiting = (instructions, extra = {}) => ({ status: 'waiting', instructions, ...extra });
const readText = (p) => fs.readFileSync(p, 'utf8');

function head(dir) {
  return revParse(dir, 'HEAD');
}

// One `git ls-remote` per remote per stage: every gate of a stage asks about
// the same moment, and each call to a real forge is a network round trip.
const remoteCache = new Map();
export function forgetRemotes() {
  remoteCache.clear();
}
function remoteRefs(dir, remote) {
  const key = `${dir}|${remote}`;
  if (!remoteCache.has(key)) remoteCache.set(key, lsRemote(dir, remote, ['--heads', '--tags']));
  return remoteCache.get(key);
}

// ── reusable gates ──────────────────────────────────────────────────────────

function cleanTree(dir, label) {
  return {
    name: label,
    check: () => {
      const r = git(dir, 'status', '--porcelain', '--untracked-files=all');
      if (r.status !== 0) return { ok: false, detail: `git status failed: ${r.stderr.trim()}` };
      const lines = r.stdout.split('\n').filter(Boolean);
      if (lines.length === 0) return { ok: true };
      return {
        ok: false,
        detail:
          `${lines.length} change(s) not committed — a release commits exactly what was tested, and untracked files are how a half-finished page from another branch shipped in v0.38.1:\n` +
          lines.slice(0, 25).join('\n'),
      };
    },
  };
}

function onMain(R) {
  return {
    name: `on ${R.plan.branch}`,
    check: () => {
      const b = git(R.repo, 'symbolic-ref', '--short', '-q', 'HEAD').stdout.trim();
      return b === R.plan.branch ? { ok: true } : { ok: false, detail: `the checkout is on "${b || 'a detached HEAD'}"; releases are cut from ${R.plan.branch}` };
    },
  };
}

function inStepWithRemote(dir, remote, branch, label) {
  return {
    name: label,
    check: () => {
      const h = head(dir);
      const r = remoteRefs(dir, remote);
      if (r.error) return { ok: false, detail: `could not ask ${remote} (${r.error}) — whether ${branch} is pushed is unknown, and unknown does not pass` };
      const there = r.map.get(`refs/heads/${branch}`);
      if (there === h) return { ok: true, detail: `${branch} @ ${short(h)}` };
      return {
        ok: false,
        detail: `${remote}'s ${branch} is ${short(there)} and this checkout is at ${short(h)} — push (or pull) first, so the release is cut from what everybody else sees`,
      };
    },
  };
}

function tagFree(dir, remote, tag, where) {
  return {
    name: `${tag} is not tagged yet (${where})`,
    check: () => {
      const local = revParse(dir, `refs/tags/${tag}`);
      const r = remoteRefs(dir, remote);
      if (r.error) return { ok: false, detail: `could not ask ${remote}: ${r.error}` };
      const remoteId = r.map.get(`refs/tags/${tag}`);
      if (!local && !remoteId) return { ok: true };
      return {
        ok: false,
        detail: `${tag} already exists ${[local && 'here', remoteId && `on ${remote}`].filter(Boolean).join(' and ')} — pick the next number; a published tag is never moved`,
      };
    },
  };
}

/** The public checkout: present, clean, its workflows committed, in step with GitHub. */
function exportCheckoutGates(R) {
  const exp = R.exp;
  return [
    {
      name: 'export checkout exists',
      check: () => {
        if (!fs.existsSync(exp)) return { ok: false, detail: `${exp} does not exist (pass --export <dir> or set exportDir in the plan)` };
        const r = git(exp, 'rev-parse', '--show-toplevel');
        if (r.status !== 0) return { ok: false, detail: `${exp} is not a git checkout` };
        if (!fs.existsSync(path.join(exp, '.github', 'workflows'))) {
          return { ok: false, detail: `${exp} has no .github/workflows — that checkout is where the release pipeline lives` };
        }
        return { ok: true, detail: slash(exp) };
      },
    },
    {
      // ⚠ Lesson #461: the workflows live ONLY in the public checkout. On
      // v0.43.1 an uncommitted workflow change there was thrown away with
      // `git checkout --` ("the export regenerates it anyway" — it does not),
      // and the tag went out with the old workflows.
      name: 'export checkout: workflows committed, nothing else pending',
      check: () => {
        const r = git(exp, 'status', '--porcelain', '--untracked-files=all');
        if (r.status !== 0) return { ok: false, detail: r.stderr.trim() };
        const lines = r.stdout.split('\n').filter(Boolean);
        if (!lines.length) return { ok: true };
        const wf = lines.filter((l) => l.slice(3).startsWith('.github/'));
        return {
          ok: false,
          detail:
            (wf.length
              ? `${wf.length} uncommitted change(s) under .github/ — COMMIT them in ${exp}. They exist nowhere else: the export does not regenerate workflows, and discarding them loses them (lesson #461).\n`
              : `the public checkout has uncommitted changes; the export refuses a dirty target:\n`) + lines.slice(0, 20).join('\n'),
        };
      },
    },
    inStepWithRemote(exp, R.plan.exportRemote, R.plan.branch, `export checkout in step with ${R.plan.exportRemote}/${R.plan.branch}`),
    tagFree(exp, R.plan.exportRemote, R.tag, 'public'),
  ];
}

// ── 1. preflight ────────────────────────────────────────────────────────────

export async function preflight(R) {
  const S = R.state;
  // The release commit exists: the tree is SUPPOSED to be ahead of the remote
  // and to carry the new CHANGELOG section, so only what must still hold is
  // checked again.
  const stamped = !!S.releaseCommit;
  const gates = [onMain(R), cleanTree(R.repo, 'working tree clean, tracked and untracked')];
  let prev = S.prev ?? null;

  if (stamped) {
    gates.push({
      name: `still on top of the release base ${short(S.base)}`,
      check: () => {
        const r = git(R.repo, 'merge-base', '--is-ancestor', S.base, 'HEAD');
        return r.status === 0 ? { ok: true } : { ok: false, detail: `HEAD no longer contains ${S.base}: the history under the release was rewritten` };
      },
    });
  } else {
    const tags = git(R.repo, 'tag', '--list', 'v*').stdout.split('\n').map((t) => t.trim()).filter(Boolean);
    prev = newestTag(tags);
    gates.push({
      name: `${R.version} is a valid next release`,
      check: () => {
        const p = versionProblems(R.version, prev);
        if (p.length) return { ok: false, detail: p.join('\n') };
        return { ok: true, detail: prev ? `${bumpKind(R.version, prev)} step from ${prev}` : 'first release' };
      },
    });
    gates.push(inStepWithRemote(R.repo, R.plan.remote, R.plan.branch, `${R.plan.branch} is pushed to ${R.plan.remote}`));
    gates.push(tagFree(R.repo, R.plan.remote, R.tag, 'private'));
    gates.push({
      name: 'CHANGELOG.md has an [Unreleased] section with something in it',
      check: () => {
        const cl = readText(path.join(R.repo, 'CHANGELOG.md'));
        if (hasSection(cl, R.version)) return { ok: false, detail: `CHANGELOG.md already has a [${R.version}] section, but nothing stamped it through this tool` };
        const body = unreleasedBody(cl);
        if (body === null) return { ok: false, detail: 'CHANGELOG.md has no "## [Unreleased]" heading' };
        if (!hasContent(body)) return { ok: false, detail: 'the [Unreleased] section is empty — write what this release changes first' };
        return { ok: true, detail: `${body.trim().split('\n').length} line(s)` };
      },
    });
    // ⚠ Lesson #52: the Helm chart sat at v0.4.0 for twenty-three releases
    // and the store manifests for twenty-nine, because nothing failed when they
    // drifted. If they do not name the CURRENT release before this one starts,
    // something moved them back — stop and find out what, before a stamp
    // quietly paves over it.
    gates.push({ name: 'deployment targets name the current release (before the bump)', cmd: ['node', 'scripts/sync-deploy-versions.mjs', '--check'] });
    gates.push(...exportCheckoutGates(R));
  }

  const ok = await R.gates.all('preflight', gates, R.ctx());
  if (!ok) return red();
  if (!stamped) {
    S.base = head(R.repo);
    S.prev = prev;
    // ⚠ Lesson #2: AUTO_UPGRADE installs apply a PATCH release by themselves,
    // so a feature shipped as a patch changes a user's screen unannounced.
    // Earlier patch releases did carry "Added" entries on purpose, so this is
    // said out loud rather than refused.
    const groups = /### (.+)/g;
    const body = unreleasedBody(readText(path.join(R.repo, 'CHANGELOG.md'))) ?? '';
    const names = [...body.matchAll(groups)].map((m) => m[1].trim());
    if (prev && bumpKind(R.version, prev) === 'patch' && names.includes('Added')) {
      console.log(
        `  ${yellow('note  ')}  a PATCH release with an "### Added" section: installs with AUTO_UPGRADE take patches unasked (lesson #2). A feature is a minor.`,
      );
    }
  }
  return done({ head: head(R.repo) });
}

// ── 2. audit (a person) ─────────────────────────────────────────────────────

export async function audit(R) {
  const S = R.state;
  if (S.releaseCommit) return done({ head: S.stages.audit?.head, note: 'confirmed before the stamp' });
  const ok = await R.gates.all('audit', R.plan.audit, R.ctx());
  if (!ok) return red();

  const h = head(R.repo);
  const range = S.prev ? `${S.prev}..HEAD` : 'HEAD';
  const commits = git(R.repo, 'rev-list', '--count', range).stdout.trim();
  const touched = new Set(git(R.repo, 'diff', '--name-only', S.prev ?? '4b825dc642cb6eb9a060e54bf8d69288fbee4904', 'HEAD').stdout.split('\n').filter(Boolean));
  const surfaces = R.plan.docSurfaces ?? [];
  const hit = surfaces.filter((g) => [...touched].some((f) => globMatch(g, f)));
  const miss = surfaces.filter((g) => !hit.includes(g));

  const instructions = [
    `A person has to do three things no script can (docs/CONTRIBUTING.md → Release process, steps 1-3):`,
    ``,
    `  1. README.md against what shipped since ${S.prev ?? 'the beginning'} — ${commits} commit(s):`,
    `       git log --oneline ${range}`,
    `  2. Screenshots: if a screen changed, bump SHOTS_RELEASE in e2e/shots/release.mjs, run \`pnpm shots\``,
    `     and OPEN the contact sheet — English, current, nothing covering them.`,
    `  3. Every surface that describes the product, not a fixed list — and the old pages are still true.`,
    ...(surfaces.length
      ? [
          `     touched in this range: ${hit.length ? hit.join(', ') : 'none of them'}`,
          `     NOT touched:           ${miss.length ? miss.join(', ') : '—'}`,
        ]
      : []),
    ``,
    `When all three are done:`,
    `    pnpm release ${R.version} --resume --ack audit`,
  ];

  if (R.acks.has('audit')) S.acks.audit = { head: h, at: new Date().toISOString() };
  if (S.acks.audit?.head === h) return done({ head: h, note: `confirmed by a person at ${S.acks.audit.at}` });
  if (S.acks.audit && S.acks.audit.head !== h) {
    instructions.unshift(`(The audit was confirmed at ${short(S.acks.audit.head)}; the tree has moved since, so it is asked again.)`, '');
  }
  return waiting(instructions);
}

// ── 3. docs ─────────────────────────────────────────────────────────────────

export async function docs(R) {
  const h = head(R.repo);
  const S = R.state;
  const prevRun = S.stages.docs;
  if (prevRun?.status === 'done' && prevRun.head === h) return done({ head: h, note: 'already green on this commit' });
  // The stamp commit itself changes only the changelog, the versions and the
  // deploy targets — a green docs run on its parent still holds. Any commit
  // after it (a fix on top of a red pretag) runs the docs gates again.
  if (prevRun?.status === 'done' && S.stampCommit && h === S.stampCommit && prevRun.head === revParse(R.repo, `${S.stampCommit}^`)) {
    return done({ head: prevRun.head, note: 'green on the commit the stamp was made on' });
  }
  const ok = await R.gates.all('docs', R.plan.docs, R.ctx());
  return ok ? done({ head: h }) : red();
}

// ── 4. stamp ────────────────────────────────────────────────────────────────

/** Workspace package directories, from pnpm-workspace.yaml. */
function packageDirs(dir) {
  const files = gitOut(dir, 'ls-files', '--', '*package.json').split('\n').filter(Boolean);
  const dirs = files.filter((f) => /^[^/]+(?:\/[^/]+)?\/package\.json$/.test(f) && !f.includes('node_modules')).map((f) => f.slice(0, -'/package.json'.length));
  return workspacePackages(readText(path.join(dir, 'pnpm-workspace.yaml')), dirs);
}

/** The files a stamp may change: anything else changing is a surprise. */
function stampAllowed(pkgs) {
  const exact = new Set(['CHANGELOG.md', ...pkgs.map((p) => `${p}/package.json`)]);
  return (f) => exact.has(f) || f.startsWith('deploy/');
}

/**
 * The writes of a release, in lesson #392's order: date the changelog FIRST
 * (the version sync reads the dated heading), then every workspace package,
 * then the deployment targets.
 */
async function applyStamp(R, dir, pkgs, stage) {
  const date = localDate();
  const clPath = path.join(dir, 'CHANGELOG.md');
  const writes = [];
  const g = await R.gates.one(stage, {
    name: `CHANGELOG.md: [Unreleased] becomes [${R.version}] - ${date}`,
    check: () => {
      const next = dateChangelog(readText(clPath), R.version, date);
      fs.writeFileSync(clPath, next);
      writes.push('CHANGELOG.md');
      return { ok: true };
    },
  }, R.ctx());
  if (!g.ok) return false;
  const b = await R.gates.one(stage, {
    name: `${pkgs.length} workspace packages name ${R.version}`,
    check: () => {
      const from = [];
      for (const p of pkgs) {
        const f = path.join(dir, p, 'package.json');
        const text = readText(f);
        from.push(`${p} ${packageVersion(text)}`);
        fs.writeFileSync(f, setPackageVersion(text, R.version));
        writes.push(`${p}/package.json`);
      }
      return { ok: true, detail: from.join(', ') };
    },
  }, R.ctx());
  if (!b.ok) return false;
  const s = await R.gates.one(stage, { name: 'deployment targets follow (sync-deploy-versions)', cmd: ['node', slash(path.join(dir, 'scripts', 'sync-deploy-versions.mjs'))], cwd: dir }, R.ctx());
  return s.ok;
}

/** What must hold after a stamp, whoever made it. */
function stampChecks(R, dir, pkgs) {
  return [
    { name: `deployment targets name v${R.version}`, cmd: ['node', slash(path.join(dir, 'scripts', 'sync-deploy-versions.mjs')), '--check'], cwd: dir },
    {
      name: `every workspace package says ${R.version}`,
      check: () => {
        const off = pkgs
          .map((p) => [p, packageVersion(readText(path.join(dir, p, 'package.json')))])
          .filter(([, v]) => v !== R.version);
        return off.length ? { ok: false, detail: off.map(([p, v]) => `${p}/package.json says ${v}`).join('\n') } : { ok: true, detail: pkgs.join(', ') };
      },
    },
    {
      // ⚠ Lesson #462: the release body is derived from the changelog and a
      // lead paragraph with no break in its first 800 characters made the
      // cut land mid-sentence and the release workflow's own test fail. The
      // workflow runs this same command before GoReleaser.
      name: `the release page renders from CHANGELOG.md [${R.version}]`,
      check: () => {
        const r = run('node', [path.join(dir, 'scripts', 'release-notes.mjs'), '--github', R.version], { cwd: dir });
        if (r.status !== 0) return { ok: false, detail: (r.stderr || r.stdout).trim() };
        if (!r.stdout.includes('## What changed') || r.stdout.length < 200) {
          return { ok: false, detail: `the rendered body is ${r.stdout.length} characters — too little to be the changelog entry` };
        }
        const groups = sectionGroups(readText(path.join(dir, 'CHANGELOG.md')), R.version);
        return { ok: true, detail: `${r.stdout.length} characters${groups.length ? `; ${groups.join(', ')}` : ''}`, log: r.stdout };
      },
    },
  ];
}

/**
 * Copies the files a stamp reads and writes into a scratch dir — from the
 * working tree, which preflight has just proved identical to HEAD.
 */
function materialize(R, into) {
  const all = gitOut(R.repo, 'ls-tree', '-r', '--name-only', 'HEAD').split('\n').filter(Boolean);
  const pkgs = packageDirs(R.repo);
  const want = new Set([
    'CHANGELOG.md',
    'pnpm-workspace.yaml',
    'scripts/sync-deploy-versions.mjs',
    'scripts/release-notes.mjs',
    'docs-site/.vitepress/github-slug.mjs',
    ...pkgs.map((p) => `${p}/package.json`),
  ]);
  const files = all.filter((f) => want.has(f) || f.startsWith('deploy/'));
  for (const f of files) {
    const out = path.join(into, f);
    fs.mkdirSync(path.dirname(out), { recursive: true });
    fs.copyFileSync(path.join(R.repo, f), out);
  }
  return { files, pkgs };
}

export async function stamp(R) {
  const S = R.state;
  const h = head(R.repo);
  const cl = readText(path.join(R.repo, 'CHANGELOG.md'));
  const pkgs = packageDirs(R.repo);

  // Already stamped: an earlier run, or fixes committed on top of the release
  // commit after a red pretag. Nothing is written again; everything the stamp
  // guarantees is checked again, on the commit that will be tagged.
  if (hasSection(cl, R.version)) {
    if (S.stages.stamp?.status === 'done' && S.stages.stamp.head === h) return done({ head: h, note: 'checked on this commit already' });
    if (!S.releaseCommit) {
      console.log(`  ${yellow('note  ')}  CHANGELOG.md already has [${R.version}] — checking the stamp rather than writing it`);
    }
    const ok = await R.gates.all('stamp', [cleanTree(R.repo, 'working tree clean'), ...stampChecks(R, R.repo, pkgs)], R.ctx());
    if (!ok) return red();
    if (S.releaseCommit && S.releaseCommit !== h) {
      const anc = git(R.repo, 'merge-base', '--is-ancestor', S.releaseCommit, h);
      if (anc.status !== 0) {
        console.log(`  ${bold('FAILED')}  HEAD ${short(h)} does not contain the release commit ${short(S.releaseCommit)}`);
        return red();
      }
      console.log(`  ${yellow('note  ')}  ${git(R.repo, 'rev-list', '--count', `${S.releaseCommit}..${h}`).stdout.trim()} commit(s) on top of the release commit — they will be tested and tagged`);
    }
    S.releaseCommit = h;
    return done({ head: h });
  }

  if (R.dry) {
    const scratch = path.join(R.tmp, 'stamp');
    fs.mkdirSync(scratch, { recursive: true });
    const { files } = materialize(R, scratch);
    const before = new Map(files.map((f) => [f, readText(path.join(scratch, f))]));
    const ok = (await applyStamp(R, scratch, pkgs, 'stamp')) && (await R.gates.all('stamp', stampChecks(R, scratch, pkgs), R.ctx()));
    const changed = files.filter((f) => readText(path.join(scratch, f)) !== before.get(f));
    console.log(`  ${dim('dry run: stamped a scratch copy, would write and commit:')}`);
    for (const f of changed) console.log(`            ${f}`);
    console.log(`            ${dim(`git commit -m "chore(release): ${R.tag}" -- <those ${changed.length} files>`)}`);
    return ok ? done({ head: h }) : red();
  }

  if (!(await applyStamp(R, R.repo, pkgs, 'stamp'))) return red();
  const allowed = stampAllowed(pkgs);
  const status = git(R.repo, 'status', '--porcelain', '--untracked-files=all').stdout.split('\n').filter(Boolean);
  const changed = status.map((l) => l.slice(3));
  const unexpected = changed.filter((f) => !allowed(f));
  const ok = await R.gates.all(
    'stamp',
    [
      ...stampChecks(R, R.repo, pkgs),
      {
        name: 'the stamp changed only what a stamp changes',
        check: () =>
          unexpected.length
            ? { ok: false, detail: `these changed too, and are not the stamp's to commit:\n${unexpected.join('\n')}` }
            : { ok: true, detail: `${changed.length} file(s)` },
      },
    ],
    R.ctx(),
  );
  if (!ok) {
    console.log(`  ${dim('the stamp is left UNCOMMITTED in the working tree, for you to read. `git checkout -- .` undoes it.')}`);
    return red();
  }
  const c = await R.gates.one('stamp', {
    name: `release commit "chore(release): ${R.tag}"`,
    check: () => {
      // ⚠ Paths, never `git add -A` (v0.38.1: two WIP files from another
      // branch rode in on a release commit) and never a bare `git commit`
      // (lesson #500: whatever else is in the index goes with it).
      const r = git(R.repo, 'commit', '-q', '-m', `chore(release): ${R.tag}`, '--', ...changed);
      if (r.status !== 0) return { ok: false, detail: (r.stderr || r.stdout).trim() };
      return { ok: true, detail: short(head(R.repo)) };
    },
  }, R.ctx());
  if (!c.ok) return red();
  S.releaseCommit = head(R.repo);
  S.stampCommit = S.releaseCommit;
  return done({ head: S.releaseCommit });
}

// ── 5. pretag ───────────────────────────────────────────────────────────────

export async function pretag(R) {
  const S = R.state;
  const h = head(R.repo);
  const prevRun = S.stages.pretag;
  if (!R.dry && prevRun?.status === 'done' && prevRun.head === h) return done({ head: h, note: 'already green on this commit' });
  if (!R.dry && h !== S.releaseCommit) {
    console.log(`  ${bold('FAILED')}  HEAD ${short(h)} is not the release commit ${short(S.releaseCommit)}`);
    return red();
  }
  if (R.dry) console.log(`  ${dim('dry run: the chain runs on HEAD as it is — the stamp above was only a scratch copy')}`);
  const specs = [
    ...R.plan.pretag,
    {
      // A build that rewrites a tracked file is a commit nobody made.
      name: 'the chain left the tree exactly as it found it',
      check: () => {
        const st = git(R.repo, 'status', '--porcelain', '--untracked-files=all').stdout.split('\n').filter(Boolean);
        const now = head(R.repo);
        if (now !== h) return { ok: false, detail: `HEAD moved during the run: ${short(h)} → ${short(now)}` };
        return st.length ? { ok: false, detail: st.slice(0, 25).join('\n') } : { ok: true };
      },
    },
  ];
  const ok = await R.gates.all('pretag', specs, R.ctx());
  return ok ? done({ head: h }) : red();
}

// ── 6. export ───────────────────────────────────────────────────────────────

/** The tree the export checkout's index holds (writes tree objects, never files). */
function indexTree(dir) {
  const r = git(dir, 'write-tree');
  return r.status === 0 ? r.stdout.trim() : null;
}

export async function exportStage(R) {
  const S = R.state;
  const h = head(R.repo);
  const prevRun = S.stages.export;

  if (!R.dry && prevRun?.status === 'done' && prevRun.privateHead === h) {
    const committed = revParse(R.exp, 'HEAD^{tree}') === S.exportTree && head(R.exp) !== S.exportBase;
    const staged =
      head(R.exp) === S.exportBase &&
      git(R.exp, 'diff', '--quiet').status === 0 &&
      !git(R.exp, 'ls-files', '--others', '--exclude-standard').stdout.trim() &&
      indexTree(R.exp) === S.exportTree;
    if (committed || staged) return done({ ...prevRun, note: 'the public checkout still holds this export' });
  }

  // An earlier export of ours that the release commit has since moved past:
  // put the checkout back — but only when it is PROVABLY nothing but that
  // export. Anything else there is somebody's work (lesson #461).
  if (!R.dry && S.exportTree) {
    const dirty = git(R.exp, 'status', '--porcelain', '--untracked-files=all').stdout.trim();
    if (dirty && head(R.exp) === S.exportBase) {
      const ours =
        git(R.exp, 'diff', '--quiet').status === 0 &&
        !git(R.exp, 'ls-files', '--others', '--exclude-standard').stdout.trim() &&
        indexTree(R.exp) === S.exportTree;
      if (ours) {
        const r = git(R.exp, 'reset', '-q', '--hard', 'HEAD');
        console.log(`  ${yellow('note  ')}  discarded the export of ${short(prevRun?.privateHead)} from ${R.exp} (it was exactly that export and nothing else)${r.status ? ` — reset failed: ${r.stderr}` : ''}`);
      }
    }
  }

  let ok = await R.gates.all('export', exportCheckoutGates(R), R.ctx());
  if (!ok) return red();

  let target = R.exp;
  if (R.dry) {
    target = path.join(R.tmp, 'export');
    const r = run('git', ['clone', '-q', '--no-hardlinks', R.exp, target]);
    if (r.status !== 0) {
      console.log(`  ${bold('FAILED')}  could not clone ${R.exp} for the dry run: ${r.stderr}`);
      return red();
    }
    console.log(`  ${dim(`dry run: exporting into a throwaway clone, ${slash(target)}`)}`);
  }
  R.exportTarget = target;
  const base = head(target);
  const script = path.join(R.repo, R.plan.exportScript);

  const specs = [
    {
      name: 'export script present',
      check: () =>
        fs.existsSync(script)
          ? { ok: true, detail: R.plan.exportScript }
          : { ok: false, detail: `${R.plan.exportScript} is not in this tree — the public checkout is produced from the private repository only` },
    },
    { name: 'rebuild the public tree', cmd: [R.bash, slash(script), slash(target)], cwd: os.tmpdir() },
    {
      name: 'the export deletes nothing it cannot explain',
      check: (c) => {
        const deleted = gitOut(target, 'diff', '--cached', '--no-renames', '--diff-filter=D', '--name-only').split('\n').filter(Boolean);
        if (!deleted.length) return { ok: true, detail: 'no deletions' };
        if (!c.prevTag) return { ok: false, detail: `${deleted.length} deletion(s) and no previous release tag to judge them against` };
        const sourceDeleted = gitOut(R.repo, 'diff', '--no-renames', '--diff-filter=D', '--name-only', c.prevTag, h).split('\n').filter(Boolean);
        const sourceFiles = gitOut(R.repo, 'ls-tree', '-r', '--name-only', h).split('\n').filter(Boolean);
        const k = classifyDeletions(deleted, { sourceDeleted, sourceFiles });
        const lines = [`${k.deletedInSource.length} deleted in the private tree since ${c.prevTag} as well`];
        if (k.unexplained.length) {
          return {
            ok: false,
            detail:
              `${k.unexplained.length} file(s) the public tree would LOSE, which the private tree neither deleted nor still has:\n` +
              k.unexplained.slice(0, 30).join('\n') +
              '\nThe export is removing something for a reason nobody wrote down. (2026-09-24: a broken export deleted 3,117.)',
          };
        }
        if (k.withheld.length) {
          const key = k.withheld.slice().sort().join('\n');
          if (R.acks.has('export-withheld')) S.acks['export-withheld'] = { list: key, at: new Date().toISOString() };
          if (S.acks['export-withheld']?.list !== key) {
            return {
              ok: false,
              detail:
                `${k.withheld.length} published file(s) the export now WITHHOLDS although the private tree still has them:\n` +
                k.withheld.slice(0, 30).join('\n') +
                `\nIf they were added to the private list on purpose, confirm exactly this list:  pnpm release ${R.version} --resume --ack export-withheld`,
            };
          }
          lines.push(`${k.withheld.length} withheld on purpose (confirmed by a person)`);
        }
        return { ok: true, detail: lines.join('; '), log: deleted.join('\n') };
      },
    },
    {
      // ⚠ Lesson #55 and the export's own rewrite: the public tree may name
      // this project's private hosts only as the two contact addresses.
      name: 'no private host or module path in the public tree',
      check: () => {
        if (!R.plan.privateHosts.forbid.length) return { ok: false, detail: 'the plan names no private-host patterns, so this gate would check nothing' };
        const pattern = R.plan.privateHosts.forbid.map((re) => re.source).join('|');
        const r = git(target, 'grep', '-nI', '-E', pattern);
        if (r.status > 1) return { ok: false, detail: `git grep failed: ${r.stderr}` };
        const hits = privateHostLines(r.stdout.split('\n').filter(Boolean), R.plan.privateHosts);
        return hits.length ? { ok: false, detail: `${hits.length} line(s):\n${hits.slice(0, 25).join('\n')}` } : { ok: true };
      },
    },
    {
      // Only the workflows: .github/ISSUE_TEMPLATE and the PR template come
      // from the private tree like everything else and may change.
      name: 'the export left the published workflows alone',
      check: () => {
        const wf = '.github/workflows';
        const staged = git(target, 'diff', '--cached', '--name-only', '--', wf).stdout.trim();
        const loose = git(target, 'status', '--porcelain', '--untracked-files=all', '--', wf).stdout.split('\n').filter((l) => l && !/^[MADR] {2}/.test(l)).join('\n');
        if (staged || loose) return { ok: false, detail: `the export changed ${wf}/ — they live only in the public checkout:\n${staged}\n${loose}` };
        return { ok: true };
      },
    },
    ...R.plan.exportGates,
  ];
  ok = await R.gates.all('export', specs, R.ctx());
  if (!ok) return red();
  if (!R.dry) {
    S.exportTree = indexTree(target);
    S.exportBase = base;
  }
  return done({ privateHead: h, exportBase: base });
}

// ── 7. sign (a person) ──────────────────────────────────────────────────────

/**
 * Verifies a signed, annotated tag. With `keys` set, the signature must be
 * by one of them (a GPG fingerprint or an SSH key fingerprint).
 */
function verifyTag(dir, ref, keys) {
  const type = git(dir, 'cat-file', '-t', ref).stdout.trim();
  if (type !== 'tag') return `${ref} is a lightweight tag — tag with \`git tag -s\``;
  const r = git(dir, 'verify-tag', '--raw', ref);
  const out = `${r.stdout}\n${r.stderr}`;
  if (r.status !== 0) return `${ref} carries no good signature (${out.trim().split('\n').pop()})`;
  if (keys?.length) {
    const flat = out.replace(/\s+/g, '').toUpperCase();
    if (!keys.some((k) => flat.includes(String(k).replace(/\s+/g, '').toUpperCase()))) {
      return `${ref} is signed, but not by the release key (${keys.join(', ')})`;
    }
  }
  return null;
}

export async function sign(R) {
  const S = R.state;
  const exp = R.exp;
  const tag = R.tag;
  const backendTag = `${R.plan.backendTagPrefix}${tag}`;
  const commands = [
    `# the private tree — on the commit the chain tested:`,
    `git -C ${slash(R.repo)} tag -s ${tag} -m "${tag}" ${S.releaseCommit ?? '<release commit>'}`,
    `git -C ${slash(R.repo)} tag -v ${tag}`,
    ``,
    `# the public checkout — commit exactly what the export staged, then tag THAT commit:`,
    `git -C ${slash(exp)} commit -m "${tag} - <one line: what this release is>"     # no "Fixes #N" — it closes issues`,
    `git -C ${slash(exp)} tag -s ${tag} -m "${tag}"`,
    `git -C ${slash(exp)} tag -v ${tag}`,
    `# only when the Go module is published this time:  git -C ${slash(exp)} tag -s ${backendTag} -m "${backendTag}"`,
    ``,
    `then:  pnpm release ${R.version} --resume`,
  ];
  if (R.dry) return waiting(commands, { dry: true });

  const privateTag = revParse(R.repo, `refs/tags/${tag}^{commit}`);
  const exportMoved = head(exp) !== S.exportBase;
  const exportTag = revParse(exp, `refs/tags/${tag}^{commit}`);
  if (!privateTag && !exportMoved && !exportTag) return waiting(commands);

  const todo = [];
  const specs = [
    {
      name: `private ${tag}: signed, on the release commit`,
      check: () => {
        if (!privateTag) {
          todo.push('the private tag');
          return { ok: true, detail: 'not made yet' };
        }
        if (privateTag !== S.releaseCommit) return { ok: false, detail: `${tag} is on ${short(privateTag)}; the tested release commit is ${short(S.releaseCommit)}. Delete the LOCAL tag (git tag -d ${tag}) and tag the release commit.` };
        const bad = verifyTag(R.repo, `refs/tags/${tag}`, R.plan.signingKeys);
        return bad ? { ok: false, detail: bad } : { ok: true };
      },
    },
    {
      name: 'public commit: exactly the export, nothing more',
      check: () => {
        if (!exportMoved) {
          todo.push('the export commit');
          return { ok: true, detail: 'not made yet' };
        }
        const problems = [];
        const parent = revParse(exp, 'HEAD^');
        if (parent !== S.exportBase) problems.push(`HEAD's parent is ${short(parent)}, not ${short(S.exportBase)} — one commit on top of the checkout the export ran in`);
        if (revParse(exp, 'HEAD^{tree}') !== S.exportTree) problems.push('the committed tree is not the tree the export produced and the gates checked');
        const dirty = git(exp, 'status', '--porcelain', '--untracked-files=all').stdout.trim();
        if (dirty) problems.push(`the checkout still has changes:\n${dirty}`);
        const kw = closingKeywords(git(exp, 'log', '-1', '--format=%B').stdout);
        if (kw.length) problems.push(`the message says ${kw.map((k) => `"${k}"`).join(', ')} — on GitHub that CLOSES the issue (lesson #163). Amend it: write "(#N)" instead.`);
        return problems.length ? { ok: false, detail: problems.join('\n') } : { ok: true, detail: short(head(exp)) };
      },
    },
    {
      name: `public ${tag}: signed, on the export commit`,
      check: () => {
        if (!exportTag) {
          todo.push('the public tag');
          return { ok: true, detail: 'not made yet' };
        }
        if (exportTag !== head(exp)) return { ok: false, detail: `${tag} is on ${short(exportTag)}, not the export commit ${short(head(exp))}` };
        if (exportTag === S.releaseCommit) return { ok: false, detail: `${tag} in the public checkout names the PRIVATE release commit — that is the leak of lesson #55` };
        const bad = verifyTag(exp, `refs/tags/${tag}`, R.plan.signingKeys);
        return bad ? { ok: false, detail: bad } : { ok: true };
      },
    },
    {
      name: `public ${backendTag} (optional): signed, backend/ identical`,
      check: () => {
        const b = revParse(exp, `refs/tags/${backendTag}^{commit}`);
        if (!b) return { ok: true, detail: 'not made — fine unless the Go module is published this time' };
        const bad = verifyTag(exp, `refs/tags/${backendTag}`, R.plan.signingKeys);
        if (bad) return { ok: false, detail: bad };
        const same = git(exp, 'diff', '--quiet', b, 'HEAD', '--', 'backend').status === 0;
        return same ? { ok: true } : { ok: false, detail: `${backendTag} (${short(b)}) has a different backend/ from the release` };
      },
    },
  ];
  const ok = await R.gates.all('sign', specs, R.ctx());
  if (!ok) return red();
  if (todo.length) return waiting([`Still to do: ${todo.join(', ')}.`, '', ...commands]);
  S.exportHead = head(exp);
  return done({ exportHead: S.exportHead });
}

// ── 8. push (a person) ──────────────────────────────────────────────────────

export async function push(R) {
  const S = R.state;
  const exp = R.exp;
  const tag = R.tag;
  const backendTag = `${R.plan.backendTagPrefix}${tag}`;
  const hasBackend = !R.dry && !!revParse(exp, `refs/tags/${backendTag}`);
  const commands = [
    `# ⚠ one ref at a time, never --tags (lesson #55: a rejected --tags push still sends the tags)`,
    `git -C ${slash(R.repo)} push ${R.plan.remote} ${R.plan.branch}`,
    `git -C ${slash(R.repo)} push ${R.plan.remote} refs/tags/${tag}`,
    `git -C ${slash(exp)} push ${R.plan.exportRemote} ${R.plan.branch}`,
    `git -C ${slash(exp)} push ${R.plan.exportRemote} refs/tags/${tag}`,
    ...(hasBackend ? [`git -C ${slash(exp)} push ${R.plan.exportRemote} refs/tags/${backendTag}`] : []),
    ``,
    `then:  pnpm release ${R.version} --resume`,
  ];
  if (R.dry) return waiting(commands, { dry: true });

  const pr = remoteRefs(R.repo, R.plan.remote);
  const er = remoteRefs(exp, R.plan.exportRemote);
  const todo = [];
  const specs = [
    {
      name: `${R.plan.remote}: ${R.plan.branch} and ${tag} are the release`,
      check: () => {
        if (pr.error) return { ok: false, detail: `could not ask ${R.plan.remote}: ${pr.error}` };
        const main = pr.map.get(`refs/heads/${R.plan.branch}`);
        const tagObj = pr.map.get(`refs/tags/${tag}`);
        const peeled = pr.map.get(`refs/tags/${tag}^{}`);
        const problems = [];
        if (main !== S.releaseCommit) {
          if (main && git(R.repo, 'merge-base', '--is-ancestor', main, S.releaseCommit).status === 0) todo.push(`${R.plan.remote} ${R.plan.branch}`);
          else problems.push(`${R.plan.remote}'s ${R.plan.branch} is ${short(main)}, not the release commit ${short(S.releaseCommit)}`);
        }
        if (!tagObj) todo.push(`${R.plan.remote} ${tag}`);
        else if (peeled !== S.releaseCommit) problems.push(`${R.plan.remote}'s ${tag} names ${short(peeled)}, not the release commit`);
        else if (tagObj !== revParse(R.repo, `refs/tags/${tag}`)) problems.push(`${R.plan.remote}'s ${tag} is a different tag object from the signed one here`);
        return problems.length ? { ok: false, detail: problems.join('\n') } : { ok: true };
      },
    },
    {
      name: `${R.plan.exportRemote}: ${R.plan.branch} and ${tag} are the EXPORT commit`,
      check: () => {
        if (er.error) return { ok: false, detail: `could not ask ${R.plan.exportRemote}: ${er.error}` };
        const main = er.map.get(`refs/heads/${R.plan.branch}`);
        const tagObj = er.map.get(`refs/tags/${tag}`);
        const peeled = er.map.get(`refs/tags/${tag}^{}`);
        const problems = [];
        if (peeled && peeled === S.releaseCommit) {
          // ⚠⚠ Lesson #55, twice (v0.26.0, v0.27.5): the public tag named the
          // private commit, and with it every internal file in its history.
          return {
            ok: false,
            detail:
              `⚠⚠ ${R.plan.exportRemote}'s ${tag} names the PRIVATE release commit ${short(peeled)}. The private history is reachable in public.\n` +
              `  1. git -C ${slash(exp)} push ${R.plan.exportRemote} :refs/tags/${tag}\n` +
              `  2. push the export's tag again (one ref)\n` +
              `  3. gh run list --workflow Release: if a run built from that tag, its binaries came from the private tree — that is a real leak, tell Burak.`,
          };
        }
        if (main !== S.exportHead) {
          if (main === S.exportBase) todo.push(`${R.plan.exportRemote} ${R.plan.branch}`);
          else problems.push(`${R.plan.exportRemote}'s ${R.plan.branch} is ${short(main)}, not the export commit ${short(S.exportHead)}`);
        }
        if (!tagObj) todo.push(`${R.plan.exportRemote} ${tag}`);
        else if (peeled !== S.exportHead) problems.push(`${R.plan.exportRemote}'s ${tag} names ${short(peeled)}, not the export commit ${short(S.exportHead)}`);
        else if (tagObj !== revParse(exp, `refs/tags/${tag}`)) problems.push(`${R.plan.exportRemote}'s ${tag} is a different tag object from the signed one in the checkout`);
        if (hasBackend) {
          const b = er.map.get(`refs/tags/${backendTag}`);
          if (!b) todo.push(`${R.plan.exportRemote} ${backendTag}`);
          else if (b !== revParse(exp, `refs/tags/${backendTag}`)) problems.push(`${R.plan.exportRemote}'s ${backendTag} is not the signed one in the checkout`);
        }
        return problems.length ? { ok: false, detail: problems.join('\n') } : { ok: true };
      },
    },
  ];
  const ok = await R.gates.all('push', specs, R.ctx());
  if (!ok) return red();
  if (todo.length) return waiting([`Not pushed yet: ${todo.join(', ')}.`, '', ...commands]);
  return done();
}

// ── 9. ci ───────────────────────────────────────────────────────────────────

export async function ci(R) {
  const lines = (R.plan.ciWatch ?? []).map((l) => l.replaceAll('{tag}', R.tag).replaceAll('{version}', R.version));
  if (R.dry) return waiting(['The release workflow runs on the pushed tag; then this checks what it published:', ...R.plan.published.map((g) => `  - ${g.name}`), ...lines], { dry: true });
  if (lines.length) for (const l of lines) console.log(`  ${dim(l)}`);
  const ok = await R.gates.all('ci', R.plan.published, R.ctx());
  if (!ok) {
    console.log(`\n  ${dim(`If the workflow is still running, wait for it and run: pnpm release ${R.version} --resume`)}`);
    return red();
  }
  return done();
}

// ── 10. deploy (a person, then verified) ────────────────────────────────────

export async function deploy(R) {
  const S = R.state;
  const list = R.plan.deployChecklist.map((l) => l.replaceAll('{tag}', R.tag).replaceAll('{version}', R.version).replaceAll('{export}', slash(R.exp)));
  const instructions = [
    'Deploy and publish (none of it is done by this script):',
    '',
    ...list.map((l) => `  ${l}`),
    '',
    `The automatic checks below must be green; what they cannot see (embeds, replies) is yours to confirm:`,
    `    pnpm release ${R.version} --resume --ack deploy`,
  ];
  if (R.dry) return waiting([...instructions, '', ...R.plan.deployed.map((g) => `  check: ${g.name}`)], { dry: true });
  const ok = await R.gates.all('deploy', R.plan.deployed, R.ctx());
  if (!ok) {
    console.log('');
    for (const l of instructions) console.log(`  ${l}`);
    return red();
  }
  if (R.acks.has('deploy')) S.acks.deploy = { at: new Date().toISOString() };
  if (!S.acks.deploy) return waiting(instructions);
  return done();
}

export const RUNNERS = { preflight, audit, docs, stamp, pretag, export: exportStage, sign, push, ci, deploy };
