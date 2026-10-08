// The release, in the order it has to happen. Each stage either finishes
// ("done"), stops on a red gate ("red"), or stops because the next step is a
// person's ("waiting") — committing the export, pushing, tagging and deploying
// are never done by this script, only checked after a person has done them.
// The one thing it starts on GitHub is a dry run of release.yml
// (`publish=false`), which publishes nothing.
//
// ⚠⚠ The tag comes LAST, on commits GitHub has already tested (task #76).
// Both mains are pushed without a tag; ci.yml runs on the public push, the
// gate stage starts release.yml's dry run on the same commit and waits for
// both; only when both passed are the tags made and pushed, and the tag run's
// `verify` job publishes nothing unless it finds those two runs on its commit.
// A red gate spends no number: fix main, resume, and the gate runs again.
// Before this, the tag started the test: 0.43.0 shipped without images,
// 0.43.1 stopped at the gate, 0.44.0/0.44.1 died in goreleaser after npm and
// the images were out, 0.45.0 published nothing — five numbers, and each one
// could only be fixed with the next.
//
// A stage that finished is not trusted blindly on --resume: it records what it
// ran against (the commit, the export tree) and runs again when that moved.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  bumpKind,
  chainVerdict,
  classifyDeletions,
  closingKeywords,
  dateChangelog,
  dryRunTitle,
  execBitDrift,
  globMatch,
  hasContent,
  hasSection,
  localDate,
  newestTag,
  packageVersion,
  partsLine,
  partsVerdict,
  privateHostLines,
  profileCovers,
  promotionVerdict,
  runVerdict,
  sectionGroups,
  selectGates,
  setPackageVersion,
  stampWrites,
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
  {
    id: 'pretag', title: 'pretag', what: 'the fast gates (the profile says which), on the release commit',
    builtin: ['gates side by side, in lanes (`jobs`)', 'a gate green on exactly these inputs before is not run again (--no-cache)', '(after the plan) the chain left the tree as it found it'],
  },
  {
    id: 'export', title: 'export', what: 'the public tree, and what must not be in it',
    builtin: ['public checkout gates again', 'scripts/export-public.sh', 'every executable bit kept (lesson #1108)', 'no deletion without a reason (deleted in source / withheld: --ack export-withheld)', 'no private host or module path', 'workflows untouched'],
  },
  {
    id: 'land', title: 'land', what: 'the export commit, then both mains pushed WITHOUT a tag — a person',
    builtin: ['public commit: exactly the exported tree, one commit, no closing keywords', 'private remote: main is the release commit', 'public remote: main is the export commit', '(no tag yet: the tag goes on what GitHub tested)'],
  },
  {
    id: 'gate', title: 'gate', what: 'GitHub tests the export commit before anything is tagged',
    builtin: [
      'ci.yml, started by the push of main, passed on the export commit as the full matrix: read part by part, every part green (the heavy suites are read here)',
      'release.yml dry run (publish=false, started here) passed on the export commit',
      'the dry run kept what the tag run promotes: both images by digest, the files of every desktop row',
      'meanwhile, here: the heavy gates GitHub does not run (minor), or a green scripts/chain run (--chain)',
      'waits for both; red spends no number — fix main, resume, and it runs again',
      'GitHub Actions down (--gate circleci): the CircleCI workflow `ci` on the export commit instead, recorded as the source; no dry run',
    ],
  },
  {
    id: 'sign', title: 'sign', what: 'signed tags on the commits that were tested — a person',
    builtin: ['private tag: annotated, good signature by the release key, on the release commit', 'public tag: signed, on the export commit GitHub tested, not the private one', 'backend/ tag (optional): signed, backend/ identical'],
  },
  { id: 'push', title: 'push', what: 'the tags, one ref at a time — a person', builtin: ['private remote: the tag is the release commit', 'public remote: the tag is the export commit — a tag naming the private commit is the lesson #55 leak'] },
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

// The public checkout may be AHEAD of GitHub by workflow commits alone: the
// packaging/ci patches are committed there after the first preflight and
// before the pretag, and the land pushes them with the export - never on their
// own (docs/CONTRIBUTING.md -> "The workflow patches, in order"). Anything
// else between GitHub and this checkout is still "push (or pull) first".
// v0.53.0 stopped here: the plain check refused the four patch commits the
// documented order had just asked for.
function exportInStep(dir, remote, branch, label) {
  const plain = inStepWithRemote(dir, remote, branch, label);
  return {
    name: label,
    check: () => {
      const first = plain.check();
      if (first.ok) return first;
      const r = remoteRefs(dir, remote);
      if (r.error) return first;
      const there = r.map.get(`refs/heads/${branch}`);
      const h = head(dir);
      if (!there || git(dir, 'merge-base', '--is-ancestor', there, h).status !== 0) return first;
      const files = gitOut(dir, 'diff', '--name-only', there, h).split('\n').filter(Boolean);
      const other = files.filter((f) => !f.startsWith('.github/'));
      if (other.length) {
        return { ok: false, detail: `${first.detail}\n  ahead of ${remote} by more than workflow commits: ${other.slice(0, 10).join(', ')}` };
      }
      const n = gitOut(dir, 'rev-list', '--count', `${there}..${h}`);
      return { ok: true, detail: `${branch} @ ${short(there)} + ${n} workflow commit(s) under .github/, pushed by the land` };
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
    exportInStep(exp, R.plan.exportRemote, R.plan.branch, `export checkout in step with ${R.plan.exportRemote}/${R.plan.branch}`),
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
    // A patch profile tests only what changed since the last release, so it
    // is for X.Y.Z+1 alone (#172; memory rule of 2026-09-25: "a patch is a
    // patch"). A minor or major always takes the minor profile or more.
    if (R.profile === 'patch') {
      gates.push({
        name: `--profile patch: ${R.version} is a patch step`,
        check: () => {
          const kind = prev ? bumpKind(R.version, prev) : null;
          if (kind === 'patch') return { ok: true, detail: `from ${prev}` };
          return { ok: false, detail: `${R.version} is ${kind ? `a ${kind} step from ${prev}` : 'the first release'}: the patch profile is for X.Y.Z+1 alone. Drop --profile patch.` };
        },
      });
    }
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
    if (prev && bumpKind(R.version, prev) === 'patch' && R.profile !== 'patch') {
      console.log(
        `  ${yellow('note  ')}  a patch step from ${prev}, cut with the ${R.profile ?? 'minor'} profile. --profile patch tests only what changed since ${prev} (unless ${prev} was never published: then this is a whole release).`,
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
    `  2. Screenshots: bump SHOTS_RELEASE in e2e/shots/release.mjs, take them in the build host's test chain - every scene, in one typeface:`,
    `       CHAIN_EXTRAS=shots bash scripts/chain/run.sh --profile targeted --src <the release checkout>`,
    `     and OPEN the contact sheet — it lists only the changed and new pictures: English, current, nothing covering them.`,
    `     Then: node scripts/shots-site.mjs upload && node scripts/shots-site.mjs accept --looked`,
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
  return (f) => stampWrites(f, pkgs);
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

/**
 * The files changed since the last release, for a patch's choice of gates;
 * null when there is no release to compare with (then every gate runs).
 */
function changedSince(R, prev) {
  if (!prev) return null;
  const r = git(R.repo, 'diff', '--name-only', '--no-renames', prev, 'HEAD');
  if (r.status !== 0) return null;
  return r.stdout.split('\n').map((s) => s.trim()).filter(Boolean);
}

/** Where a gate the CI runs is read from, in words: `ci.yml`, `CircleCI go`. */
function readFrom(g, source) {
  return source === 'circleci' ? `CircleCI ${g.circleci}` : g.github;
}

/** The CI the gate stage reads, by name. */
const ciName = (source) => (source === 'circleci' ? 'CircleCI' : 'GitHub');

/** Says, before the gates run, which run here, which later and which nowhere. */
function describeProfile(R, profile, sel, stage) {
  const S = R.state;
  const source = R.gateSource ?? 'github';
  let changed = '';
  if (stage === 'pretag' && profile === 'patch') changed = R.changed ? `, ${R.changed.length} file(s) changed since ${S.prev}` : ', nothing to compare with: every gate runs';
  console.log(`  ${dim('profile ')}  ${bold(profile)}${dim(changed)}`);
  if (stage !== 'pretag') return;
  for (const g of sel.remote) console.log(`  ${dim(source.padEnd(8))}  ${g.name} ${dim(`- ${readFrom(g, source)} on the export commit, read at the gate stage`)}`);
  for (const g of sel.local) console.log(`  ${dim('later   ')}  ${g.name} ${dim(`- here, at the gate stage, while ${ciName(source)} tests`)}`);
  for (const s of sel.skipped) console.log(`  ${dim('skip    ')}  ${s.gate.name} ${dim(`- ${s.why}`)}`);
}

export async function pretag(R) {
  const S = R.state;
  const h = head(R.repo);
  const prevRun = S.stages.pretag;
  const profile = R.profile ?? 'minor';
  // ⚠ A pretag recorded before profiles existed ran the whole chain (`full`).
  if (!R.dry && prevRun?.status === 'done' && prevRun.head === h && profileCovers(prevRun.profile, profile)) {
    return done({ head: h, profile: prevRun.profile ?? 'full', note: `already green on this commit (${prevRun.profile ?? 'full'})` });
  }
  if (!R.dry && h !== S.releaseCommit) {
    console.log(`  ${bold('FAILED')}  HEAD ${short(h)} is not the release commit ${short(S.releaseCommit)}`);
    return red();
  }
  if (R.dry) console.log(`  ${dim('dry run: the chain runs on HEAD as it is — the stamp above was only a scratch copy')}`);
  R.changed = profile === 'patch' ? changedSince(R, S.prev) : null;
  const sel = selectGates(R.plan, profile, R.changed, { source: R.gateSource ?? 'github' });
  describeProfile(R, profile, sel, 'pretag');
  // ⚠ #172: a red gate no longer sends the next run back to the start. The
  // gates that passed on exactly these inputs pass from the cache
  // (engine.mjs), so a fix in e2e/ re-runs what reads e2e/ and the builds.
  const ok = await R.gates.all('pretag', sel.pretag, R.ctx(), { jobs: R.plan.jobs ?? 1 });
  // A build that rewrites a tracked file is a commit nobody made. Last, and
  // whatever the gates said.
  const tree = await R.gates.one('pretag', {
    name: 'the chain left the tree exactly as it found it',
    check: () => {
      const st = git(R.repo, 'status', '--porcelain', '--untracked-files=all').stdout.split('\n').filter(Boolean);
      const now = head(R.repo);
      if (now !== h) return { ok: false, detail: `HEAD moved during the run: ${short(h)} → ${short(now)}` };
      return st.length ? { ok: false, detail: st.slice(0, 25).join('\n') } : { ok: true };
    },
  }, R.ctx());
  return ok && tree.ok ? done({ head: h, profile }) : red({ profile });
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
      // ⚠ Lesson #1108: on Windows the export's `git add -A` staged every new
      // file 100644, and e2e/realenv/run.sh - which CONTRIBUTING tells a reader
      // to run as it is - went public unrunnable. The export copies the bits
      // now; this reads them back, whatever the export did.
      name: 'the public tree keeps every executable bit the private tree has',
      check: () => {
        const priv = git(R.repo, 'ls-tree', '-r', '-z', h);
        const pub = git(target, 'ls-files', '-s', '-z');
        if (priv.status !== 0 || pub.status !== 0) return { ok: false, detail: `could not read the modes: ${(priv.stderr || pub.stderr).trim()}` };
        const d = execBitDrift(priv.stdout, pub.stdout);
        if (!d.lost.length && !d.gained.length) return { ok: true, detail: `${d.checked} file(s) compared` };
        return {
          ok: false,
          detail: [
            ...d.lost.slice(0, 20).map((p) => `executable here, not in the public tree: ${p}`),
            ...d.gained.slice(0, 20).map((p) => `executable in the public tree, not here: ${p}`),
            `${d.lost.length + d.gained.length} file(s). The export copies the bit (scripts/export-public.sh, section 4b); check what it printed.`,
          ].join('\n'),
        };
      },
    },
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
  // ⚠ Recorded red or green. The next --resume can only discard a checkout
  // that is PROVABLY this export (the tree recorded here, staged, nothing
  // else); recorded only on green, a red export gate left the public checkout
  // holding an export the resume could not recognise, and every resume
  // stopped on "nothing else pending" until a person cleared it by hand
  // (v0.45.0, twice: a private-host gate, then a flaky test).
  if (!R.dry) {
    S.exportTree = indexTree(target);
    S.exportBase = base;
  }
  if (!ok) return red();
  return done({ privateHead: h, exportBase: base });
}

// ── 7. land (a person) ──────────────────────────────────────────────────────

/**
 * The public commit the export became: one commit on top of the checkout the
 * export ran in — or HEAD itself when the export staged nothing because HEAD
 * already holds its tree (after a red gate, a fix to a workflow is committed
 * in the public checkout, and the export on top of it has nothing to add).
 */
function exportCommitOf(R) {
  const S = R.state;
  const h = head(R.exp);
  if (h && h !== S.exportBase) return { commit: h, made: true };
  if (h && revParse(R.exp, 'HEAD^{tree}') === S.exportTree) return { commit: h, made: false };
  return { commit: null, made: false };
}

export async function land(R) {
  const S = R.state;
  const exp = R.exp;
  const tag = R.tag;
  const { remote, exportRemote, branch } = R.plan;
  const commands = [
    `# the public checkout — commit exactly what the export staged:`,
    `git -C ${slash(exp)} commit -m "${tag} - <one line: what this release is>"     # no "Fixes #N" — it closes issues`,
    ``,
    `# both mains, WITHOUT a tag: the tags go on the commits GitHub has tested, after the gate`,
    `git -C ${slash(R.repo)} push ${remote} ${branch}`,
    `git -C ${slash(exp)} push ${exportRemote} ${branch}     # to the terminal or a file, never into a pipe (lessons #722/#726)`,
    ``,
    `then:  pnpm release ${R.version} --resume     (it starts release.yml's dry run on that commit and waits for GitHub)`,
  ];
  if (R.dry) return waiting(commands, { dry: true });

  // Landed already, for this release commit and this export: what the mains
  // hold now no longer matters (they move on once the tags are out).
  const prevRun = S.stages.land;
  if (prevRun?.status === 'done' && prevRun.releaseCommit === S.releaseCommit && prevRun.exportTree === S.exportTree && prevRun.exportHead) {
    S.exportHead = prevRun.exportHead;
    return done({ ...prevRun, note: `landed as ${short(prevRun.exportHead)}` });
  }

  const found = exportCommitOf(R);
  const pr = remoteRefs(R.repo, remote);
  const er = remoteRefs(exp, exportRemote);
  const todo = [];
  const specs = [
    {
      name: 'public commit: exactly the export, nothing more',
      check: () => {
        if (!found.commit) {
          todo.push('the export commit');
          return { ok: true, detail: 'not made yet' };
        }
        if (!found.made) return { ok: true, detail: `the export added nothing: ${short(found.commit)} already holds its tree` };
        const problems = [];
        const parent = revParse(exp, 'HEAD^');
        if (parent !== S.exportBase) problems.push(`HEAD's parent is ${short(parent)}, not ${short(S.exportBase)} — one commit on top of the checkout the export ran in`);
        if (revParse(exp, 'HEAD^{tree}') !== S.exportTree) problems.push('the committed tree is not the tree the export produced and the gates checked');
        const dirty = git(exp, 'status', '--porcelain', '--untracked-files=all').stdout.trim();
        if (dirty) problems.push(`the checkout still has changes:\n${dirty}`);
        const kw = closingKeywords(git(exp, 'log', '-1', '--format=%B').stdout);
        if (kw.length) problems.push(`the message says ${kw.map((k) => `"${k}"`).join(', ')} — on GitHub that CLOSES the issue (lesson #163). Amend it: write "(#N)" instead.`);
        return problems.length ? { ok: false, detail: problems.join('\n') } : { ok: true, detail: short(found.commit) };
      },
    },
    {
      name: `${remote}: ${branch} is the release commit`,
      check: () => {
        if (pr.error) return { ok: false, detail: `could not ask ${remote}: ${pr.error}` };
        const main = pr.map.get(`refs/heads/${branch}`);
        if (main === S.releaseCommit) return { ok: true, detail: short(main) };
        if (main && git(R.repo, 'merge-base', '--is-ancestor', main, S.releaseCommit).status === 0) {
          todo.push(`${remote} ${branch}`);
          return { ok: true, detail: 'not pushed yet' };
        }
        return { ok: false, detail: `${remote}'s ${branch} is ${short(main)}, not the release commit ${short(S.releaseCommit)}` };
      },
    },
    {
      name: `${exportRemote}: ${branch} is the export commit`,
      check: () => {
        if (er.error) return { ok: false, detail: `could not ask ${exportRemote}: ${er.error}` };
        if (!found.commit) return { ok: true, detail: 'once the export commit is made' };
        const main = er.map.get(`refs/heads/${branch}`);
        if (main === found.commit) return { ok: true, detail: short(main) };
        if (main && git(exp, 'merge-base', '--is-ancestor', main, found.commit).status === 0) {
          todo.push(`${exportRemote} ${branch}`);
          return { ok: true, detail: 'not pushed yet' };
        }
        return { ok: false, detail: `${exportRemote}'s ${branch} is ${short(main)}, not the export commit ${short(found.commit)}` };
      },
    },
  ];
  const ok = await R.gates.all('land', specs, R.ctx());
  if (!er.error && er.map.get(`refs/tags/${tag}`)) {
    console.log(
      `  ${yellow('note  ')}  ${tag} is on ${exportRemote} already, before GitHub tested anything: its release run stops at "verify" and publishes nothing. ` +
        `When the gate is green, re-run that run's failed jobs (gh run rerun <id> --failed).`,
    );
  }
  if (!ok) return red();
  if (todo.length) return waiting([`Still to do: ${todo.join(', ')}.`, '', ...commands]);
  S.exportHead = found.commit;
  return done({ exportHead: found.commit, releaseCommit: S.releaseCommit, exportTree: S.exportTree });
}

// ── 8. gate: GitHub tests the export commit ─────────────────────────────────

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/**
 * Whether a scripts/chain result (--chain <result.json>, task #170) stands in
 * for the heavy gates this machine would run at the gate stage.
 */
function chainEvidence(R, profile) {
  let result;
  try {
    result = JSON.parse(readText(R.chain));
  } catch (e) {
    return { ok: false, detail: `it cannot be read: ${e?.message ?? e}` };
  }
  const target = R.state.releaseCommit ?? head(R.repo);
  const sha = typeof result?.sha === 'string' ? result.sha : '';
  let since = null;
  if (sha && git(R.repo, 'merge-base', '--is-ancestor', sha, target).status === 0) {
    const d = git(R.repo, 'diff', '--name-only', '--no-renames', sha, target);
    if (d.status === 0) since = d.stdout.split('\n').map((s) => s.trim()).filter(Boolean);
  }
  let pkgs = [];
  try {
    pkgs = packageDirs(R.repo);
  } catch {
    /* no workspace to read: only CHANGELOG.md and deploy/ count as the stamp */
  }
  const v = chainVerdict(result, { accepted: R.plan.chainProfiles?.[profile] ?? [], since, stampFile: (f) => stampWrites(f, pkgs) });
  if (!v.ok) return { ok: false, detail: v.problems.join('; ') };
  return { ok: true, detail: `scripts/chain run ${result.run_id ?? '?'} (${result.profile}) passed on ${sha.slice(0, 10)}${since.length ? `; since then only the stamp: ${since.length} file(s)` : ''}` };
}

/**
 * The heavy gates GitHub does not run (the profile's `local`), here, on the
 * release commit, while GitHub tests the export commit - or a green
 * scripts/chain run in their place. Resolves to whether they passed. Gates
 * green on exactly these inputs before pass from the cache, so a resume after
 * a GitHub flake does not run Playwright again.
 */
async function heavyHere(R) {
  const profile = R.profile ?? 'minor';
  const { local } = selectGates(R.plan, profile, null, { source: R.gateSource ?? 'github' });
  if (!local.length) return true;
  const h = head(R.repo);
  if (!R.dry && h !== R.state.releaseCommit) {
    const rec = await R.gates.one('gate', {
      name: 'the heavy gates run on the release commit',
      check: () => ({ ok: false, detail: `HEAD is ${short(h)}, not the release commit ${short(R.state.releaseCommit)}: a fix goes on main and the release resumes, and the chain runs again on it` }),
    }, R.ctx());
    return rec.ok;
  }
  if (R.chain) {
    const ev = chainEvidence(R, profile);
    if (ev.ok) {
      const rec = await R.gates.one('gate', { name: `the ${local.length} heavy gate(s) ${ciName(R.gateSource)} does not run: a scripts/chain run`, check: () => ev }, R.ctx());
      return rec.ok;
    }
    console.log(`  ${yellow('note  ')}  --chain ${slash(R.chain)} does not answer for this release (${ev.detail}); the heavy gates run here`);
  }
  console.log(`  ${dim('here    ')}  while ${ciName(R.gateSource)} tests: ${local.map((g) => g.name).join(dim(' · '))}`);
  return R.gates.all('gate', local, R.ctx(), { jobs: R.plan.jobs ?? 1 });
}

/**
 * Waits until GitHub has run, on the export commit, the two things the tag
 * run's `verify` job will look for: ci.yml started by the push of main - its
 * full matrix, every part green (#173) - and a dry run of release.yml
 * (publish=false: it builds and packages every artifact and publishes none).
 * The dry run is started here, once per commit.
 *
 * ⚠ The push run is read PART BY PART when the plan names its parts
 * (`githubMatrix`, scripts/ci-parts.mjs; #174): every job the full matrix
 * has must have run and passed. A red part stops the wait at once, by its
 * name; a run that ended green without one of them (an older ci.yml, a
 * matrix that lost a part) is no full matrix and does not pass. A run's one
 * conclusion said neither which part failed nor that one never ran. The
 * heavy gates the profile reads from GitHub print the parts that stand for
 * them (`githubJobs`).
 *
 * ⚠ And the dry run must have kept what the tag run promotes (`promotion`):
 * a tag run builds no image and no desktop package, it publishes the ones
 * this dry run built. A dry run without them is no release candidate, and its
 * tag run would stop at `verify`.
 *
 * ⚠ A run is matched by its commit AND, for the dry run, by its name
 * (release.yml's `run-name`, dryRunTitle): GitHub's API does not give a run's
 * inputs, and a dry run of only=arm64, only=macos or only=snap-arm64, or of a
 * tag, did not test this commit.
 *
 * ⚠ #181: when GitHub Actions is down, `--gate circleci` reads the CircleCI
 * workflow `ci` (.circleci/config.yml) on the export commit instead, and no
 * dry run (gateOnCircleci below): the heavy suites CircleCI runs are read
 * from it, the rest run here (selectGates), and the stage records
 * `source: 'circleci'`. A person chooses it, never the tool: a GitHub that
 * answers red is red, not "down". Nothing is promoted then, since no dry run
 * ran: either the tag run builds the packages once Actions is back (its
 * `verify` takes the same green CircleCI workflow, #181), or the release is
 * packaged off GitHub (scripts/release/package-local.mjs).
 */
export async function gate(R) {
  const S = R.state;
  const sha = S.exportHead;
  const source = R.gateSource ?? 'github';
  const gh = R.plan.github;
  const wf = { ci: 'ci.yml', release: 'release.yml', ...(R.plan.gateWorkflows ?? {}) };
  const { exportRemote, branch } = R.plan;
  const dispatch = { workflow: wf.release, ref: branch, inputs: { publish: 'false' } };
  const title = dryRunTitle(sha ?? '<export commit>');
  const startCmd = gh?.command ? gh.command(dispatch) : `gh workflow run ${wf.release} --ref ${branch} -f publish=false`;
  const covered = selectGates(R.plan, R.profile ?? 'minor', R.profile === 'patch' ? (R.changed ?? changedSince(R, S.prev)) : null, { source }).remote;
  const matrix = R.plan.githubMatrix ?? null;
  const promotion = R.plan.promotion ?? null;
  if (covered.length) describeProfile(R, R.profile ?? 'minor', null, 'gate');
  for (const g of covered) {
    let parts = '';
    try {
      parts = g.githubJobs ? `, ${g.githubJobs(R.ctx()).length} part(s)` : '';
    } catch {
      /* said again, and judged, when the run is read */
    }
    console.log(`  ${dim(source.padEnd(8))}  ${g.name} ${dim(`- read from ${readFrom(g, source)}${source === 'github' ? parts : ''}`)}`);
  }
  if (source === 'circleci') return gateOnCircleci(R);
  if (R.dry) {
    // The heavy gates that run here run in a dry run too: it is every gate.
    const here = await heavyHere(R);
    const w = waiting(
      [
        `GitHub tests the export commit; nothing is tagged until both of these passed on it:`,
        `  - ${wf.ci}, started by the push of ${branch}${matrix ? ': the full matrix, read part by part, every part green' : ''}`,
        `  - ${wf.release} as a dry run, started here:  ${startCmd}`,
        `    (named "${title}": it builds and packages everything, and publishes nothing${promotion ? '; the tag run promotes what it keeps' : ''})`,
        `  (GitHub Actions down: pnpm release ${R.version} --resume --gate circleci reads CircleCI instead)`,
      ],
      { dry: true },
    );
    return here ? w : red();
  }
  if (!sha) {
    console.log(`  ${bold('FAILED')}  no export commit is recorded — the land stage records it`);
    return red();
  }
  const prevRun = S.stages.gate;
  if (prevRun?.status === 'done' && prevRun.exportHead === sha) return done({ ...prevRun, note: `${ciName(prevRun.source)} passed ${short(sha)}` });
  if (!gh) {
    console.log(`  ${bold('FAILED')}  the plan names no GitHub to ask (plan.github)`);
    return red();
  }

  // ⚠ #172: the heavy gates GitHub does not run start now, here, and run
  // beside the wait below. Every way out of this stage waits for them first
  // (`settle`): a red one is a red stage, and a stage that returned while they
  // ran would leave their processes behind.
  let hereDone = false;
  const here = heavyHere(R).then(
    (ok) => {
      hereDone = true;
      return ok;
    },
    (e) => {
      hereDone = true;
      console.log(`  ${bold('FAILED')}  the heavy gates here crashed: ${e?.stack ?? e}`);
      return false;
    },
  );
  const settle = async (res) => {
    if (!hereDone) console.log(`  ${dim('github  ')}  ${dim('GitHub has answered; waiting for the heavy gates running here')}`);
    const ok = await here;
    return res.status === 'red' || ok ? res : red();
  };

  const wait = { pollMs: 60_000, timeoutMs: 4 * 3600_000, appearMs: 10 * 60_000, ...(R.plan.gateWait ?? {}) };
  const t0 = Date.now();
  const fail = async (name, detail) => {
    await R.gates.one('gate', { name, check: () => ({ ok: false, detail }) }, R.ctx());
    return settle(red());
  };
  let expected = null;
  if (matrix) {
    try {
      expected = matrix.parts(R.ctx());
    } catch (e) {
      return fail(`the parts of ${wf.ci}'s matrix`, `could not list them: ${e?.message ?? e}`);
    }
    if (!expected?.length) return fail(`the parts of ${wf.ci}'s matrix`, 'the plan names none, so the run would be read as a whole');
  }
  let shown = '';
  for (;;) {
    const ci = gh.runs({ workflow: wf.ci, sha, event: 'push' });
    const dr = gh.runs({ workflow: wf.release, sha, event: 'workflow_dispatch' });
    const error = ci.error || dr.error;
    if (error) return fail('GitHub answered', `could not ask GitHub (${error}) — whether ${short(sha)} passed is unknown, and unknown does not pass`);

    const dryRuns = dr.runs.filter((x) => x.title === title);
    if (!dryRuns.length) {
      if (dr.runs.length) {
        return fail(
          `${wf.release} names its dry runs "${dryRunTitle('<sha>')}"`,
          `${dr.runs.length} run(s) of ${wf.release} were started by hand on ${short(sha)}, none named "${title}" ` +
            `(${dr.runs.slice(0, 3).map((x) => `"${x.title}"`).join(', ')}). The public ${wf.release} needs the run-name that names a dry run, ` +
            `and the verify job the tag run waits on: packaging/ci/release-verify.patch.`,
        );
      }
      if (S.gate?.sha !== sha) {
        forgetRemotes();
        const er = remoteRefs(R.exp, exportRemote);
        const main = er.error ? null : er.map.get(`refs/heads/${branch}`);
        if (main !== sha) {
          return fail(
            `${exportRemote}'s ${branch} is the export commit, so the dry run tests it`,
            er.error ? `could not ask ${exportRemote}: ${er.error}` : `${exportRemote}'s ${branch} is ${short(main)}, not the export commit ${short(sha)} — a dry run started now would test that instead`,
          );
        }
        const d = gh.dispatch(dispatch);
        if (!d.ok) return fail(`started the dry run of ${wf.release}`, d.detail);
        S.gate = { sha, dispatchedAt: new Date().toISOString() };
        R.save?.();
        console.log(`  ${dim('started ')}  ${startCmd}`);
      } else if (Date.now() - Date.parse(S.gate.dispatchedAt) > wait.appearMs) {
        return fail(
          `the dry run started at ${S.gate.dispatchedAt} reached ${short(sha)}`,
          `no run named "${title}" appeared. If ${exportRemote}'s ${branch} had moved when it started, it tested that commit instead: ` +
            `look with gh run list --workflow ${wf.release}, then start it on the export commit by hand (${startCmd}), or land again.`,
        );
      }
    }

    const v = { ci: runVerdict(ci.runs), dry: runVerdict(dryRuns) };
    // The push run, part by part (#174).
    let parts = null;
    let jobs = [];
    if (expected && v.ci.run) {
      const jr = gh.jobs ? gh.jobs({ runId: v.ci.run.id }) : { error: "the plan's GitHub cannot read the jobs of a run" };
      if (jr.error) {
        return fail('GitHub answered', `could not read the jobs of ${wf.ci} run ${v.ci.run.id} (${jr.error}) — whether every part passed is unknown, and unknown does not pass`);
      }
      jobs = jr.jobs;
      parts = partsVerdict(jobs, expected, { finished: v.ci.state !== 'running' });
    }
    const ciState = matrixState(v.ci.state, parts);
    const said = (x, state = x.state) => `${state}${x.run?.url ? ` ${x.run.url}` : ''}`;
    const line = `${wf.ci}: ${said(v.ci, ciState)}${parts ? ` (${partsLine(parts)})` : ''}  ·  dry run: ${said(v.dry)}`;
    if (line !== shown) {
      console.log(`  ${dim('github  ')}  ${line}`);
      if (parts) {
        for (const g of covered.filter((x) => x.githubJobs)) {
          let own;
          try {
            own = partsLine(partsVerdict(jobs, g.githubJobs(R.ctx()), { finished: v.ci.state !== 'running' }));
          } catch (e) {
            own = `its parts cannot be listed: ${e?.message ?? e}`;
          }
          console.log(`  ${dim('        ')}  ${g.name}: ${own}`);
        }
      }
      shown = line;
    }
    const finished = ciState === 'failure' || ciState === 'incomplete' || v.dry.state === 'failure' || (ciState === 'success' && v.dry.state === 'success');
    if (finished) {
      const ciVerdict = {
        name: `${wf.ci} (push) passed on ${short(sha)}`,
        check: () => {
          if (ciState === 'success') return { ok: true, detail: `${v.ci.run.url}${parts ? ` - ${partsLine(parts)}` : ''}` };
          if (ciState === 'incomplete') {
            return {
              ok: false,
              detail:
                `${v.ci.run.url ?? `run ${v.ci.run.id}`} passed, but it is not the full matrix: ${parts.missing.length} part(s) never ran - ` +
                `${parts.missing.slice(0, 12).map((p) => p.name).join(', ')}. The full matrix runs on a push of ${branch} (scripts/ci-parts.mjs), ` +
                `and a ${wf.ci} without these parts is a fault in the workflow (packaging/ci/ci-full-matrix.patch).`,
            };
          }
          if (ciState === 'failure' && parts?.failed.length) {
            const still = v.ci.state === 'running' ? ' The run is still going: re-run its red parts once it has finished.' : '';
            return {
              ok: false,
              detail: `${parts.failed.length} part(s) red: ${parts.failed.slice(0, 12).map((p) => `${p.name} (${p.conclusion}${p.url ? `, ${p.url}` : ''})`).join('; ')}.${still}`,
            };
          }
          if (ciState === 'failure') return { ok: false, detail: `${v.ci.run.url ?? `run ${v.ci.run.id}`} ended "${v.ci.run.conclusion}"` };
          return { ok: false, detail: ciState === 'none' ? `no ${wf.ci} run was started by a push of ${short(sha)}` : `not finished (${ciState})` };
        },
      };
      const dryVerdict = {
        name: `${wf.release} dry run passed on ${short(sha)}`,
        check: () => {
          const x = v.dry;
          if (x.state === 'success') return { ok: true, detail: x.run.url };
          if (x.state === 'failure') return { ok: false, detail: `${x.run.url ?? `run ${x.run.id}`} ended "${x.run.conclusion}"` };
          return { ok: false, detail: x.state === 'none' ? `no run named "${title}"` : `not finished (${x.state})` };
        },
      };
      const kept = {
        name: `the dry run kept what the ${R.tag} run promotes`,
        check: () => {
          if (!gh.artifacts) return { ok: false, detail: "the plan's GitHub cannot list the artifacts of a run" };
          const ar = gh.artifacts({ runId: v.dry.run.id });
          if (ar.error) return { ok: false, detail: `could not ask GitHub (${ar.error}) — whether the tag run has anything to promote is unknown, and unknown does not pass` };
          const pv = promotionVerdict(ar.names, promotion);
          if (!pv.ok) {
            return {
              ok: false,
              detail:
                `${v.dry.run.url ?? `run ${v.dry.run.id}`} kept no ${pv.lacking.join(', ')}: it is no release candidate, and the tag run would have nothing to promote ` +
                `(its version was tagged already, it was no dry run of everything, or what it kept has expired). Start one again on ${short(sha)}: ${startCmd}`,
            };
          }
          const without = pv.without.length
            ? `; without ${pv.without.join(', ')}: ${R.tag} goes out without the macOS packages, and a run with only=macos adds them once GitHub has macOS runners (docs/CONTRIBUTING.md, Release process)`
            : '';
          return { ok: true, detail: `${ar.names.length} artifact(s)${without}` };
        },
      };
      const verdicts = [ciVerdict, dryVerdict];
      if (promotion && ciState === 'success' && v.dry.state === 'success') verdicts.push(kept);
      const ok = await R.gates.all('gate', verdicts, R.ctx());
      if (ok) {
        return settle(done({ exportHead: sha, source: 'github', ci: v.ci.run.url, parts: parts ? partsLine(parts) : null, dryRun: v.dry.run.url, profile: R.profile ?? 'minor' }));
      }
      const failed = (ciState === 'failure' || ciState === 'incomplete' ? v.ci.run : null) ?? (v.dry.state === 'failure' ? v.dry.run : null);
      const rerun = failed && gh.rerun ? gh.rerun(failed.id) : 'gh run rerun <id> --failed';
      for (const l of [
        '',
        `Nothing is tagged, and ${R.tag} is not spent. Then:`,
        `  - a flake:  ${failed && v.ci.state === 'running' && failed === v.ci.run ? `once that run has finished, ${rerun}` : rerun}, then  pnpm release ${R.version} --resume`,
        `  - a fault in the code: fix it on ${branch} (a commit on top of the release commit), push, pnpm release ${R.version} --resume —`,
        `    the chain, the export, the landing and this gate run again on the fix`,
        `  - a fault in a workflow: fix it in ${slash(R.exp)}, commit, push ${branch}, pnpm release ${R.version} --resume —`,
        `    the export finds nothing new to stage, and this gate runs on the new public ${branch}`,
      ]) {
        console.log(`  ${l}`);
      }
      return settle(red());
    }
    if (Date.now() - t0 >= wait.timeoutMs) {
      return settle(
        waiting([
          `GitHub has not finished on ${short(sha)} yet (waited ${Math.round((Date.now() - t0) / 60_000)} min):`,
          `  ${line}`,
          '',
          `Carry on waiting with:  pnpm release ${R.version} --resume`,
        ]),
      );
    }
    await sleep(wait.pollMs);
  }
}

/**
 * The push run's state with its parts read (#174): a red part is red at once,
 * even while the run goes on; a run that passed without every part is
 * `incomplete`, never success. Without parts, the run's own state.
 */
export function matrixState(runState, parts) {
  if (!parts) return runState;
  if (parts.failed.length) return 'failure';
  if (runState === 'running') return 'running';
  if (runState === 'success') return parts.state === 'success' ? 'success' : 'incomplete';
  return runState;
}

/**
 * The gate stage when GitHub Actions is down (#181, `--gate circleci`): the
 * CircleCI workflow `ci` (plan.circleciWorkflow) on the export commit, and
 * the heavy gates CircleCI does not run, here, beside the wait - with the
 * settle, wait and give-up rules of GitHub's. Nothing is started on CircleCI:
 * the push of the public main started the pipeline, and a person starts one
 * again from CircleCI's page when it is missing.
 *
 * ⚠ What it does NOT test, and the log says so: the release dry run.
 * GoReleaser's snapshot, the images and the desktop packages are built for
 * the first time when the release is packaged off GitHub, and the tag run's
 * `verify` job, once Actions is back, publishes nothing for a commit that has
 * no ci.yml run and no dry run on it (CONTRIBUTING, Release process, "When
 * GitHub Actions is down").
 */
async function gateOnCircleci(R) {
  const S = R.state;
  const sha = S.exportHead;
  const cc = R.plan.circleci;
  const workflow = R.plan.circleciWorkflow ?? 'ci';
  const { branch } = R.plan;
  const where = cc?.where ? cc.where({ branch }) : `CircleCI, ${branch}`;
  const prevRun = S.stages.gate;
  if (!R.dry && sha && prevRun?.status === 'done' && prevRun.exportHead === sha) return done({ ...prevRun, note: `${ciName(prevRun.source)} passed ${short(sha)}` });
  const unseen = [
    `GitHub Actions is not asked (--gate circleci): the release dry run does not run, and nothing is promoted - the packages are first built by the tag run, or when the release is packaged off GitHub.`,
    `Once Actions is back, the tag run's verify job takes this CircleCI workflow too (no ci.yml run, no dry run on the commit), and that run builds the images and the desktop packages itself; it needs the CIRCLECI_TOKEN secret of the public repository.`,
  ];
  for (const l of unseen) console.log(`  ${yellow('note  ')}  ${l}`);
  if (R.dry) {
    const here = await heavyHere(R);
    const w = waiting(
      [
        `CircleCI tests the export commit; nothing is tagged until this passed on it:`,
        `  - the workflow "${workflow}" (.circleci/config.yml), started by the push of ${branch}: ${where}`,
        ...unseen.map((l) => `  ${l}`),
      ],
      { dry: true, source: 'circleci' },
    );
    return here ? w : red();
  }
  if (!sha) {
    console.log(`  ${bold('FAILED')}  no export commit is recorded — the land stage records it`);
    return red();
  }
  if (!cc) {
    console.log(`  ${bold('FAILED')}  the plan names no CircleCI to ask (plan.circleci)`);
    return red();
  }

  // Started now, run beside the wait, waited for on every way out - as on
  // GitHub (gate above).
  let hereDone = false;
  const here = heavyHere(R).then(
    (ok) => {
      hereDone = true;
      return ok;
    },
    (e) => {
      hereDone = true;
      console.log(`  ${bold('FAILED')}  the heavy gates here crashed: ${e?.stack ?? e}`);
      return false;
    },
  );
  const settle = async (res) => {
    if (!hereDone) console.log(`  ${dim('circleci')}  ${dim('CircleCI has answered; waiting for the heavy gates running here')}`);
    const ok = await here;
    return res.status === 'red' || ok ? res : red();
  };
  const wait = { pollMs: 60_000, timeoutMs: 4 * 3600_000, appearMs: 10 * 60_000, ...(R.plan.gateWait ?? {}) };
  const t0 = Date.now();
  const fail = async (name, detail) => {
    await R.gates.one('gate', { name, check: () => ({ ok: false, detail }) }, R.ctx());
    return settle(red());
  };
  let shown = '';
  for (;;) {
    let a;
    try {
      a = await cc.runs({ workflow, sha, branch });
    } catch (e) {
      a = { error: e?.message ?? String(e) };
    }
    if (a.error) return fail('CircleCI answered', `could not ask CircleCI (${a.error}) - whether ${short(sha)} passed is unknown, and unknown does not pass`);
    const v = runVerdict(a.runs);
    const line = `CircleCI ${workflow}: ${v.state}${v.run?.url ? ` ${v.run.url}` : ''}`;
    if (line !== shown) {
      console.log(`  ${dim('circleci')}  ${line}`);
      shown = line;
    }
    if (v.state === 'success' || v.state === 'failure') {
      const ok = await R.gates.all(
        'gate',
        [
          {
            name: `CircleCI ${workflow} passed on ${short(sha)}`,
            check: () =>
              v.state === 'success'
                ? { ok: true, detail: `${v.run.url} (GitHub Actions not asked: --gate circleci)` }
                : { ok: false, detail: `${v.run.url ?? `workflow ${v.run.id}`} ended "${v.run.conclusion}"` },
          },
        ],
        R.ctx(),
      );
      if (ok) {
        return settle(
          done({
            exportHead: sha,
            source: 'circleci',
            circleci: v.run.url,
            profile: R.profile ?? 'minor',
            note: `CircleCI passed ${short(sha)}; no GitHub run, no dry run (--gate circleci)`,
          }),
        );
      }
      for (const l of [
        '',
        `Nothing is tagged, and ${R.tag} is not spent. Then:`,
        `  - a flake: on CircleCI, "Rerun workflow from failed" on that workflow, then  pnpm release ${R.version} --resume --gate circleci`,
        `  - a fault in the code: fix it on ${branch} (a commit on top of the release commit), push, pnpm release ${R.version} --resume --gate circleci -`,
        `    the chain, the export, the landing and this gate run again on the fix`,
      ]) {
        console.log(`  ${l}`);
      }
      return settle(red());
    }
    if (v.state === 'none' && Date.now() - t0 >= wait.appearMs) {
      return fail(
        `CircleCI ran the workflow "${workflow}" on ${short(sha)}`,
        `no pipeline of ${cc.project ?? 'the project'} on ${branch} ran "${workflow}" on the export commit within ${Math.round(wait.appearMs / 60_000)} min. ` +
          `Is the project set up (CONTRIBUTING, Release process, "When GitHub Actions is down")? Start a pipeline on ${branch} by hand (${where}), then resume.`,
      );
    }
    if (Date.now() - t0 >= wait.timeoutMs) {
      return settle(
        waiting([
          `CircleCI has not finished on ${short(sha)} yet (waited ${Math.round((Date.now() - t0) / 60_000)} min):`,
          `  ${line}`,
          '',
          `Carry on waiting with:  pnpm release ${R.version} --resume --gate circleci`,
        ], { source: 'circleci' }),
      );
    }
    await sleep(wait.pollMs);
  }
}

// ── 9. sign (a person) ──────────────────────────────────────────────────────

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
  const releaseCommit = S.releaseCommit ?? '<release commit>';
  const exportCommit = S.exportHead ?? '<export commit>';
  // The CI the gate stage read: its record says (a record from before #181
  // has no source, and was GitHub's).
  const tester = ciName(S.stages.gate?.source ?? R.gateSource);
  const commands = [
    `# ${tester} has tested the export commit: tag exactly what was tested.`,
    `# the private tree — the release commit the chain passed on:`,
    `git -C ${slash(R.repo)} tag -s ${tag} -m "${tag}" ${releaseCommit}`,
    `git -C ${slash(R.repo)} tag -v ${tag}`,
    ``,
    `# the public checkout — the export commit, already on ${R.plan.branch}:`,
    `git -C ${slash(exp)} tag -s ${tag} -m "${tag}" ${exportCommit}`,
    `git -C ${slash(exp)} tag -v ${tag}`,
    `# only when the Go module is published this time:  git -C ${slash(exp)} tag -s ${backendTag} -m "${backendTag}" ${exportCommit}`,
    ``,
    `then:  pnpm release ${R.version} --resume`,
  ];
  if (R.dry) return waiting(commands, { dry: true });

  const privateTag = revParse(R.repo, `refs/tags/${tag}^{commit}`);
  const exportTag = revParse(exp, `refs/tags/${tag}^{commit}`);
  if (!privateTag && !exportTag) return waiting(commands);

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
      name: `public ${tag}: signed, on the export commit ${tester} tested`,
      check: () => {
        if (!exportTag) {
          todo.push('the public tag');
          return { ok: true, detail: 'not made yet' };
        }
        if (exportTag === S.releaseCommit) return { ok: false, detail: `${tag} in the public checkout names the PRIVATE release commit — that is the leak of lesson #55` };
        if (exportTag !== S.exportHead) return { ok: false, detail: `${tag} is on ${short(exportTag)}, not the export commit ${tester} tested, ${short(S.exportHead)}. Delete the LOCAL tag (git -C ${slash(exp)} tag -d ${tag}) and tag ${short(S.exportHead)}.` };
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
        const same = git(exp, 'diff', '--quiet', b, S.exportHead, '--', 'backend').status === 0;
        return same ? { ok: true } : { ok: false, detail: `${backendTag} (${short(b)}) has a different backend/ from the release` };
      },
    },
  ];
  const ok = await R.gates.all('sign', specs, R.ctx());
  if (!ok) return red();
  if (todo.length) return waiting([`Still to do: ${todo.join(', ')}.`, '', ...commands]);
  return done({ exportHead: S.exportHead });
}

// ── 10. push (a person) ─────────────────────────────────────────────────────

export async function push(R) {
  const S = R.state;
  const exp = R.exp;
  const tag = R.tag;
  const backendTag = `${R.plan.backendTagPrefix}${tag}`;
  const hasBackend = !R.dry && !!revParse(exp, `refs/tags/${backendTag}`);
  const commands = [
    `# ⚠ one ref at a time, never --tags (lesson #55: a rejected --tags push still sends the tags)`,
    `git -C ${slash(R.repo)} push ${R.plan.remote} refs/tags/${tag}`,
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
      name: `${R.plan.remote}: ${tag} is the release commit`,
      check: () => {
        if (pr.error) return { ok: false, detail: `could not ask ${R.plan.remote}: ${pr.error}` };
        const tagObj = pr.map.get(`refs/tags/${tag}`);
        const peeled = pr.map.get(`refs/tags/${tag}^{}`);
        if (!tagObj) {
          todo.push(`${R.plan.remote} ${tag}`);
          return { ok: true, detail: 'not pushed yet' };
        }
        if (peeled !== S.releaseCommit) return { ok: false, detail: `${R.plan.remote}'s ${tag} names ${short(peeled)}, not the release commit` };
        if (tagObj !== revParse(R.repo, `refs/tags/${tag}`)) return { ok: false, detail: `${R.plan.remote}'s ${tag} is a different tag object from the signed one here` };
        return { ok: true };
      },
    },
    {
      name: `${R.plan.exportRemote}: ${tag} is the EXPORT commit`,
      check: () => {
        if (er.error) return { ok: false, detail: `could not ask ${R.plan.exportRemote}: ${er.error}` };
        const tagObj = er.map.get(`refs/tags/${tag}`);
        const peeled = er.map.get(`refs/tags/${tag}^{}`);
        if (peeled && peeled === S.releaseCommit) {
          // ⚠⚠ Lesson #55, twice (v0.26.0, v0.27.5): the public tag named the
          // private commit, and with it every internal file in its history.
          return {
            ok: false,
            detail:
              `⚠⚠ ${R.plan.exportRemote}'s ${tag} names the PRIVATE release commit ${short(peeled)}. The private history is reachable in public.\n` +
              `  1. git -C ${slash(exp)} push ${R.plan.exportRemote} :refs/tags/${tag}\n` +
              `  2. push the export's tag again (one ref)\n` +
              `  3. gh run list --workflow Release: if a run built from that tag, its binaries came from the private tree — that is a real leak, tell the maintainer.`,
          };
        }
        const problems = [];
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

// ── 11. ci ──────────────────────────────────────────────────────────────────

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

// ── 12. deploy (a person, then verified) ───────────────────────────────────

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

export const RUNNERS = { preflight, audit, docs, stamp, pretag, export: exportStage, land, gate, sign, push, ci, deploy };
