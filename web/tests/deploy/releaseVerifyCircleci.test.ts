// A commit CircleCI tested while GitHub Actions was down is published by its
// tag run once Actions is back, and that run builds what it has no dry run to
// promote (#181).
//
// ⚠⚠ Why (the maintainers, 2026-10-06): on 2026-10-05 the 0.52.0 dry run could not get
// a runner, twice, and the release went out from a PC. Since #181 the release
// tool gates a commit on CircleCI's `ci` workflow when Actions is down
// (`pnpm release X.Y.Z --resume --gate circleci`), but the tag run's `verify`
// asked GitHub alone - a ci.yml run and a dry run on the commit - so a commit
// gated on CircleCI could never be published by its tag run, not even after
// Actions came back. Now:
//   - GitHub's own runs come first: when both are there CircleCI is not
//     asked, and a tag run promotes the dry run as before (#174);
//   - when GitHub lacks either, a green CircleCI `ci` workflow on that very
//     commit lets the run publish - a tag push and a run that adds to a
//     release alike. The workflow reads it with
//     .github/workflows/scripts/circleci-green.mjs, which must read a commit
//     exactly as the release tool's gate does (scripts/release/circleci.mjs
//     and runVerdict): both are run on the same answers below;
//   - red, unfinished, none, or CircleCI not answering is no evidence;
//   - with CircleCI's word there is no dry run, so nothing is promoted: the
//     tag run builds both images (`docker`, pushed by digest), checks them in
//     `promote-check` as it checks a dry run's, tags them in
//     `docker-manifest`, and builds every desktop row.
//
// The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts): read from `<repo>/.github/workflows` or
// FILEX_WORKFLOWS_DIR. The release tool names these titles in its workflow
// guards (scripts/release/plan.mjs, WORKFLOW_GUARDS), so a skip there is red.
// The change reaches the public checkout as its own commit, landed with the
// export that brings these guards (packaging/ci/release-verify-circleci.patch,
// the fourth of the workflow patches).
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { describe, expect, it } from 'vitest';

import { runVerdict } from '../../../scripts/release/checks.mjs';
import { circleciPipelines } from '../../../scripts/release/circleci.mjs';
import {
  DIR,
  KEYS,
  RUN_SHA,
  TAG_SHA,
  bash,
  code,
  desktopEnv,
  desktopRuns,
  job,
  jobIf,
  jobNames,
  matrices,
  stepsOf,
  verify,
  verifyRun,
  verifyScript,
  type Row,
} from '../helpers/releaseWorkflow';

const hasJq = !spawnSync('jq', ['--version'], { encoding: 'utf8' }).error;

/** verify's env in a tag run of v1.2.3, the desktop rows given. */
const tagRun = (rows: Row[] = []) => ({ EVENT: 'push', SHA: TAG_SHA, ONLY: 'all', PUBLISH: 'true', TAG: 'v1.2.3', DESKTOP: JSON.stringify({ include: rows }) });

/** The full run's desktop rows, as plan writes them. */
const fullRows = () => matrices(job(code(), 'plan'))[1].rows;

// ── the job graph, read as GitHub reads it ──────────────────────────────────

interface Run {
  event: string;
  outputs: Record<string, Record<string, string>>;
  results: Record<string, string>;
}

/**
 * A job's condition (or an env expression) for one run: the references these
 * conditions use - github.event_name, needs.<job>.outputs.<name>,
 * needs.<job>.result, cancelled() - and nothing else. Anything more fails the
 * test instead of being guessed at.
 */
function evaluate(cond: string, run: Run): boolean {
  let js = cond
    .replace(/\bgithub\.event_name\b/g, () => JSON.stringify(run.event))
    .replace(/\bneeds\.([\w-]+)\.outputs\.(\w+)/g, (_m, j: string, o: string) => JSON.stringify(run.outputs[j]?.[o] ?? ''))
    .replace(/\bneeds\.([\w-]+)\.result\b/g, (_m, j: string) => JSON.stringify(run.results[j] ?? 'skipped'))
    .replace(/\bcancelled\(\)/g, 'false');
  const rest = js
    .replace(/"[^"]*"|'[^']*'/g, '')
    .replace(/\b(?:true|false)\b/g, '')
    .replace(/[\s()!=&|]/g, '');
  expect(rest, `a condition this test cannot read: ${cond}`).toBe('');
  js = js.replace(/!=/g, '!==').replace(/(?<![!=])==(?!=)/g, '===');
  return !!new Function(`return (${js});`)();
}

/** Whether a job runs: its `if:`, and - without a status function in it - every job it needs passed. */
function jobRuns(release: string, name: string, run: Run): boolean {
  const block = job(release, name);
  const cond = jobIf(block);
  const free = /\b(?:always|cancelled|failure|success)\(\)/.test(cond);
  const needs = (/\n {4}needs:\s*\[([^\]]*)\]/.exec(block)?.[1] ?? '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
  if (!free && needs.some((n) => (run.results[n] ?? 'skipped') !== 'success')) return false;
  return cond ? evaluate(cond, run) : true;
}

/** An env expression of a job (`NAME: ${{ ... }}`), without its ${{ }}. */
function envExpr(block: string, name: string): string {
  const m = new RegExp(`\\n\\s+${name}: \\$\\{\\{ (.+) \\}\\}\\n`).exec(block);
  expect(m, `${name} is an expression`).not.toBeNull();
  return m![1];
}

/** What plan and verify hand on in one kind of run; every job before them passed. */
function runOf(kind: 'tag-github' | 'tag-circleci' | 'candidate' | 'dry' | 'arm64'): Run {
  const tag = kind.startsWith('tag');
  const plan = {
    full: kind === 'arm64' ? 'false' : 'true',
    publish: tag || kind === 'arm64' ? 'true' : 'false',
    candidate: kind === 'candidate' ? 'true' : 'false',
  };
  const verifyOut: Record<string, string> =
    kind === 'tag-github'
      ? { evidence: 'github', promote: 'true', dry_run: '2' }
      : kind === 'tag-circleci'
        ? { evidence: 'circleci', promote: 'false' }
        : kind === 'arm64'
          ? { evidence: 'github', dry_run: '2' }
          : {};
  return { event: tag ? 'push' : 'workflow_dispatch', outputs: { plan, verify: verifyOut }, results: { plan: 'success', verify: 'success' } };
}

// ── the CircleCI readers, on the same answers ───────────────────────────────

const SHA = 'a'.repeat(40);
const OTHER = 'b'.repeat(40);
const PROJECT = 'gh/BRF-Tech/filex';
const PIPELINES = `/project/${PROJECT}/pipeline?branch=main`;

type Answer = { status?: number; body?: unknown; throws?: string };
type Routes = Record<string, Answer>;
type Verdict = { state?: string; url?: string; status?: string; error?: string };

/**
 * A stand-in for CircleCI's API, as source text: the same one answers the
 * release tool's reader here and the workflow's reader in the node process
 * that imports it. `routes` maps "path?query" (after /api/v2) to an answer;
 * every request is recorded with its headers.
 */
const FAKE_FETCH = `(routes, calls) => async (url, init) => {
  calls.push({ url, headers: init.headers });
  const key = url.replace('https://circleci.com/api/v2', '');
  const a = routes[key];
  if (!a) return { ok: false, status: 404, json: async () => ({ message: 'no route ' + key }) };
  if (a.throws) throw new Error(a.throws);
  const status = a.status ?? 200;
  return { ok: status >= 200 && status < 300, status, json: async () => a.body };
}`;
const fakeFetch = new Function(`return ${FAKE_FETCH};`)() as (routes: Routes, calls: unknown[]) => (url: string, init: unknown) => Promise<unknown>;

/** circleci-green.mjs of the public checkout, on `routes`, in a plain node process (it is not a file of this tree). */
function workflowReader(routes: Routes, token = 't'): { v: Verdict; calls: Array<{ url: string; headers: Record<string, string> }> } {
  const url = pathToFileURL(path.join(DIR!, 'scripts', 'circleci-green.mjs')).href;
  const src = [
    `const m = await import(${JSON.stringify(url)});`,
    'const calls = [];',
    `const fetch = (${FAKE_FETCH})(${JSON.stringify(routes)}, calls);`,
    `const v = await m.circleciVerdict({ project: ${JSON.stringify(PROJECT)}, sha: ${JSON.stringify(SHA)}, branch: 'main', workflow: 'ci', token: ${JSON.stringify(token)}, fetch });`,
    'process.stdout.write(JSON.stringify({ v, calls }));',
  ].join('\n');
  const r = spawnSync(process.execPath, ['--input-type=module', '-e', src], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error(r.stderr);
  return JSON.parse(r.stdout);
}

/** The release tool's reading of the same answers: scripts/release/circleci.mjs, then runVerdict. */
async function toolReader(routes: Routes, token = 't'): Promise<Verdict> {
  const a = await circleciPipelines(PROJECT, { token, fetch: fakeFetch(routes, []) }).runs({ workflow: 'ci', sha: SHA, branch: 'main' });
  if (a.error) return { error: a.error };
  const v = runVerdict(a.runs);
  return { state: v.state, url: v.run?.url };
}

const pipeline = (id: string, number: number, revision: string) => ({ id, number, state: 'created', vcs: { revision, branch: 'main' } });
const page = (items: unknown[], next: string | null = null): Answer => ({ body: { items, next_page_token: next } });
const workflows = (...items: Array<{ id: string; name: string; status: string }>): Answer => page(items);

const SCENARIOS: Array<[string, Routes]> = [
  [
    'a rerun that passed after one that failed, beside another workflow and another commit',
    {
      [PIPELINES]: page([pipeline('p3', 3, OTHER), pipeline('p2', 2, SHA), pipeline('p1', 1, SHA)]),
      '/pipeline/p3/workflow': workflows({ id: 'w3', name: 'ci', status: 'success' }),
      '/pipeline/p2/workflow': workflows({ id: 'w2', name: 'ci', status: 'success' }, { id: 'wx', name: 'nightly', status: 'failed' }),
      '/pipeline/p1/workflow': workflows({ id: 'w1', name: 'ci', status: 'failed' }),
    },
  ],
  ['one that is failing', { [PIPELINES]: page([pipeline('p1', 1, SHA)]), '/pipeline/p1/workflow': workflows({ id: 'w1', name: 'ci', status: 'failing' }) }],
  [
    'one still going beside one that failed',
    { [PIPELINES]: page([pipeline('p1', 1, SHA)]), '/pipeline/p1/workflow': workflows({ id: 'w1', name: 'ci', status: 'failed' }, { id: 'w2', name: 'ci', status: 'running' }) },
  ],
  ['one on hold', { [PIPELINES]: page([pipeline('p1', 1, SHA)]), '/pipeline/p1/workflow': workflows({ id: 'w1', name: 'ci', status: 'on_hold' }) }],
  ['one with no state', { [PIPELINES]: page([pipeline('p1', 1, SHA)]), '/pipeline/p1/workflow': workflows({ id: 'w1', name: 'ci', status: '' }) }],
  ['a workflow of another name only', { [PIPELINES]: page([pipeline('p1', 1, SHA)]), '/pipeline/p1/workflow': workflows({ id: 'w1', name: 'nightly', status: 'success' }) }],
  ['no pipeline of the commit', { [PIPELINES]: page([pipeline('p1', 1, OTHER)]) }],
  [
    'the commit two pages back, read through a GitHub App pipeline',
    {
      [PIPELINES]: page([pipeline('p9', 9, OTHER)], 't2'),
      [`${PIPELINES}&page-token=t2`]: page([{ id: 'p8', number: 8, trigger_parameters: { github_app: { checkout_sha: SHA } } }], 't3'),
      [`${PIPELINES}&page-token=t3`]: page([pipeline('p7', 7, OTHER)], 't4'),
      '/pipeline/p8/workflow': workflows({ id: 'w8', name: 'ci', status: 'success' }),
    },
  ],
];

const ERRORS: Array<[string, Routes, string]> = [
  ['no token', { [PIPELINES]: { status: 401, body: { message: 'You must log in first.' } } }, ''],
  ['a wrong slug', { [PIPELINES]: { status: 404, body: { message: 'Project not found' } } }, 'secret-token'],
  ['no network', { [PIPELINES]: { throws: 'getaddrinfo ENOTFOUND circleci.com' } }, 'secret-token'],
  ['a pipeline whose workflows cannot be read', { [PIPELINES]: page([pipeline('p1', 1, SHA)]), '/pipeline/p1/workflow': { status: 500, body: {} } }, 'secret-token'],
];

describe('a tag run of a commit CircleCI tested', () => {
  it.runIf(!DIR)('has no workflows to read here (they live in the public checkout)', () => {
    // Not a pass: the private tree has no .github/workflows. The local release
    // run sets FILEX_WORKFLOWS_DIR so the cases below run there too.
    expect(DIR).toBeUndefined();
  });

  it.runIf(!!DIR && !!bash)("publishes on CircleCI's green ci workflow when GitHub lacks its runs, and promotes nothing", () => {
    const r = verify(tagRun(), { circleci: 'green', circleciToken: 'cc-token' });
    expect(r.code, r.out).toBe(0);
    expect(r.outputs.evidence).toBe('circleci');
    expect(r.outputs.promote).toBe('false');
    expect(r.outputs.dry_run, 'no dry run to hand on').toBeUndefined();
    expect(r.outputs.desktop, "the plan's rows stand").toBeUndefined();
    // CircleCI was asked about this very commit: the ci workflow on main, of
    // the project the job names, with the token the job was handed.
    expect(r.circleciCalls).toEqual([{ argv: ['--project', 'gh/BRF-Tech/filex', '--sha', TAG_SHA, '--branch', 'main', '--workflow', 'ci'], token: 'cc-token' }]);
    // GitHub first, and no dry run's artifacts asked for afterwards; the tag
    // has no Release yet (one packaged off GitHub is the case below).
    expect(r.calls.some((c) => c.includes('workflows/ci.yml/runs'))).toBe(true);
    expect(r.calls.some((c) => c.includes('/artifacts'))).toBe(false);
    expect(r.calls.some((c) => c.startsWith('api repos/BRF-Tech/filex/releases/tags/v1.2.3 '))).toBe(true);
    expect(r.out).toContain('::notice title=CircleCI::');
    expect(r.out, 'what GitHub lacked is no error when CircleCI stands in').not.toContain('::error::');
    expect(r.summary).toContain('was tested on CircleCI before it was tagged');
    expect(r.summary).toContain('no dry run to promote');
    // Half of GitHub's evidence is none of it.
    for (const half of [{ ci: true }, { dry: true }, { ci: true, dry: true, full: false }]) {
      const h = verify(tagRun(), { ...half, circleci: 'green' });
      expect(h.code, `${JSON.stringify(half)}: ${h.out}`).toBe(0);
      expect(h.outputs.evidence, JSON.stringify(half)).toBe('circleci');
      expect(h.outputs.promote, JSON.stringify(half)).toBe('false');
    }
  });

  it.runIf(!!DIR && !!bash)('takes no other word from CircleCI: red, unfinished or unanswered publishes nothing, and says what GitHub lacked', () => {
    for (const circleci of ['red', 'running', 'error'] as const) {
      const r = verify(tagRun(), { circleci });
      expect(r.code, `${circleci}: ${r.out}`).not.toBe(0);
      expect(r.circleciCalls, circleci).toHaveLength(1);
      expect(r.out).toContain('::error::no successful ci.yml run started by a push of');
      expect(r.out).toContain("::error::no successful run of release.yml named 'dry run all");
      expect(r.out).toMatch(/::error::CircleCI: /);
      expect(r.out).toMatch(/::error::v1\.2\.3 publishes nothing/);
      expect(r.outputs.evidence, circleci).toBeUndefined();
      expect(r.outputs.promote, circleci).toBeUndefined();
    }
  });

  // A release packaged off GitHub while Actions was down (package-local.mjs)
  // has its Release, its images and its files out already, and winget and the
  // feeds pin those files by hash. Its tag run, started once Actions is back,
  // must not build and tag images over them on CircleCI's word.
  it.runIf(!!DIR && !!bash)("a tag run of a release already packaged off GitHub publishes nothing on CircleCI's word", () => {
    const r = verify(tagRun(), { circleci: 'green', release: 'yes' });
    expect(r.code, r.out).not.toBe(0);
    expect(r.out).toMatch(/::error::v1\.2\.3 has a GitHub Release already \(https:\/\/github\.com\/BRF-Tech\/filex\/releases\/tag\/v1\.2\.3\): it was packaged off GitHub, and this run publishes nothing/);
    expect(r.outputs.promote).toBeUndefined();
    // Not knowing is not a pass either.
    const unknown = verify(tagRun(), { circleci: 'green', release: 'error' });
    expect(unknown.code, unknown.out).not.toBe(0);
    expect(unknown.out).toContain('::error::could not ask whether v1.2.3 has a GitHub Release');
    expect(unknown.outputs.promote).toBeUndefined();
    // A run that adds to that release still may: it is how macOS and the arm64 snap get there.
    const mac = verify(verifyRun('macos', true), { circleci: 'green', release: 'yes' });
    expect(mac.code, mac.out).toBe(0);
    expect(mac.calls.some((c) => c.includes('/releases/tags/'))).toBe(false);
  });

  it.runIf(!!DIR && !!bash)("asks GitHub first: with its full matrix and a dry run, CircleCI is never asked", () => {
    const arm = verify(verifyRun('arm64', true), { ci: true, dry: true, circleci: 'green' });
    expect(arm.code, arm.out).toBe(0);
    expect(arm.outputs.evidence).toBe('github');
    expect(arm.outputs.dry_run).toBe('2');
    expect(arm.circleciCalls).toEqual([]);
    // A run that adds to a release builds from the tag: nothing to promote.
    expect(arm.outputs.promote).toBeUndefined();
  });

  it.runIf(!!DIR && !!bash && hasJq)("a tag run with GitHub's runs still promotes the dry run, and a dry run that kept nothing is still red", () => {
    const rows = fullRows();
    const every = ['digests-amd64', 'digests-arm64', ...rows.map((r) => `release-files-${r.label}`)];
    const r = verify(tagRun(rows), { ci: true, dry: true, artifacts: every, circleci: 'green' });
    expect(r.code, r.out).toBe(0);
    expect(r.outputs).toMatchObject({ evidence: 'github', promote: 'true', dry_run: '2' });
    expect(r.circleciCalls).toEqual([]);
    // GitHub's evidence is there, and the dry run is no candidate: red,
    // whatever CircleCI says - start the dry run again (CONTRIBUTING, step 7).
    const none = verify(tagRun(rows), { ci: true, dry: true, artifacts: [], circleci: 'green' });
    expect(none.code).not.toBe(0);
    expect(none.out).toContain('kept no digests-amd64');
    expect(none.outputs.promote).toBeUndefined();
    expect(none.circleciCalls).toEqual([]);
  });

  it.runIf(!!DIR && !!bash)("a run that adds to a release takes CircleCI's word on the tag's commit, as a tag push does", () => {
    for (const only of ['arm64', 'macos', 'snap-arm64']) {
      const r = verify(verifyRun(only, true), { circleci: 'green' });
      expect(r.code, `${only}: ${r.out}`).toBe(0);
      expect(r.outputs.evidence, only).toBe('circleci');
      expect(r.outputs.promote, `${only} builds from its tag: nothing to promote, nothing said`).toBeUndefined();
      expect(r.circleciCalls[0]?.argv, only).toContain(TAG_SHA);
      expect(r.circleciCalls[0]?.argv, only).not.toContain(RUN_SHA);
    }
  });

  it.runIf(!!DIR && !!bash)('a dry run asks CircleCI nothing', () => {
    for (const only of ['all', 'arm64', 'macos', 'snap-arm64']) {
      const r = verify({ EVENT: 'workflow_dispatch', SHA: RUN_SHA, ONLY: only, PUBLISH: 'false', TAG: only === 'all' ? '' : 'v1.2.3' }, { circleci: 'green' });
      expect(r.code, `${only}: ${r.out}`).toBe(0);
      expect(r.calls, only).toEqual([]);
      expect(r.circleciCalls, only).toEqual([]);
    }
  });

  it.runIf(!!DIR)("verify reads CircleCI with a token secret of its own, the project's slug from a variable, and this commit's reader", () => {
    const release = code();
    const v = job(release, 'verify');
    expect(v).toContain('evidence: ${{ steps.v.outputs.evidence }}');
    expect(v).toContain('promote: ${{ steps.v.outputs.promote }}');
    expect(v).toContain('CIRCLECI_TOKEN: ${{ secrets.CIRCLECI_TOKEN }}');
    expect(v).toContain("CIRCLECI_PROJECT: ${{ vars.CIRCLECI_PROJECT_SLUG || format('gh/{0}', github.repository) }}");
    const steps = stepsOf(v);
    const checkout = steps.find((s) => /actions\/checkout@/.test(s.text));
    expect(checkout, 'verify checks its reader out').toBeTruthy();
    expect(checkout!.text).toContain('ref: ${{ github.sha }}');
    expect(checkout!.text).toContain('path: .ci');
    expect(checkout!.text).toContain('sparse-checkout: .github/workflows/scripts');
    expect(checkout!.cond, 'a dry run asks nothing, and checks nothing out').toBe("needs.plan.outputs.publish == 'true'");
    expect(steps.some((s) => /actions\/setup-node@/.test(s.text))).toBe(true);
    const script = verifyScript(release);
    expect(script).toMatch(/node "\$CI_SCRIPTS"\/circleci-green\.mjs --project "\$CIRCLECI_PROJECT" --sha "\$SHA" --branch main --workflow ci 2>&1/);
    expect(script, 'the script never touches the token: the reader reads it from its environment').not.toContain('CIRCLECI_TOKEN');
    expect(script).not.toMatch(/^\s*set -[a-z]*x/m);
    // CircleCI is asked only once GitHub's runs are found missing.
    expect(script.indexOf('circleci-green.mjs')).toBeGreaterThan(script.indexOf('workflows/release.yml/runs'));
    // The token reaches verify's step and no other job; verify still only reads.
    expect(jobNames(release).filter((n) => job(release, n).includes('secrets.CIRCLECI_TOKEN'))).toEqual(['verify']);
    expect(v).toMatch(/\n {4}permissions:\n {6}actions: read\n {6}contents: read\n/);
    expect(fs.existsSync(path.join(DIR!, 'scripts', 'circleci-green.mjs')), 'the reader is in the workflow scripts').toBe(true);
  });

  it.runIf(!!DIR)('with nothing to promote a tag run builds the images and the desktop packages itself, and tags only what it checked', () => {
    const release = code();
    const dockerBlock = job(release, 'docker');
    const push = envExpr(dockerBlock, 'PUSH');
    const promote = envExpr(job(release, 'desktop'), 'PROMOTE');
    const at = (kind: Parameters<typeof runOf>[0], results: Record<string, string> = {}) => {
      const run = runOf(kind);
      run.results = { ...run.results, ...results };
      return {
        docker: jobRuns(release, 'docker', run),
        push: evaluate(push, run),
        check: jobRuns(release, 'promote-check', run),
        manifest: (checked: string) => jobRuns(release, 'docker-manifest', { ...run, results: { ...run.results, 'promote-check': checked } }),
        promote: evaluate(promote, run),
      };
    };
    // A tag run of a commit GitHub tested: promotes, builds nothing.
    const github = at('tag-github', { docker: 'skipped' });
    expect(github).toMatchObject({ docker: false, check: true, promote: true });
    expect(github.manifest('success')).toBe(true);
    // A tag run of a commit CircleCI tested: builds and pushes by digest,
    // checks this run's digests, tags them only once they were checked.
    const circleci = at('tag-circleci', { docker: 'success' });
    expect(circleci).toMatchObject({ docker: true, push: true, check: true, promote: false });
    expect(circleci.manifest('success')).toBe(true);
    expect(circleci.manifest('skipped')).toBe(false);
    expect(circleci.manifest('failure')).toBe(false);
    // …and a build that failed is never checked, let alone tagged.
    expect(at('tag-circleci', { docker: 'failure' }).check).toBe(false);
    // Runs by hand are as before: the candidate pushes, a plain dry run does
    // not, neither checks nor tags; a run that adds ARM packages builds no image.
    expect(at('candidate')).toMatchObject({ docker: true, push: true, check: false, promote: false });
    expect(at('candidate').manifest('skipped')).toBe(false);
    expect(at('dry')).toMatchObject({ docker: true, push: false, check: false, promote: false });
    expect(at('arm64', { docker: 'skipped' })).toMatchObject({ docker: false, check: false, promote: false });
    expect(at('arm64').manifest('skipped')).toBe(false);
    // The digests come from the dry run, or from this run when there is none.
    for (const name of ['promote-check', 'docker-manifest']) {
      expect(job(release, name), name).toContain('run-id: ${{ needs.verify.outputs.dry_run || github.run_id }}');
    }
    expect(job(release, 'promote-check')).toMatch(/\n {4}needs: \[plan, verify, docker\]/);
    // Every desktop row builds and publishes in a tag run with nothing to
    // promote, and downloads nothing from a dry run.
    for (const row of fullRows()) {
      const built = desktopRuns(release, { ...desktopEnv({ publish: true, full: true, only: 'all' }, row, KEYS), PROMOTE: 'false', CANDIDATE: 'false' }, row);
      expect(built.runs, row.label).not.toContain('The files the dry run built');
      expect(built.runs, row.label).toContain('What was produced');
      expect(built.steps.some((s) => /electron-builder|pnpm --filter \.\/desktop run|makeappx/.test(s.text)), `${row.label} builds`).toBe(true);
      expect(built.publishing.length, `${row.label} publishes`).toBeGreaterThan(0);
    }
  });

  it.runIf(!!DIR)('circleci-green.mjs reads a commit as the release tool does', async () => {
    for (const [what, routes] of SCENARIOS) {
      const w = workflowReader(routes).v;
      const t = await toolReader(routes);
      expect(t.error, `${what}: the release tool`).toBeUndefined();
      expect({ state: w.state, url: w.url }, what).toEqual({ state: t.state, url: t.url });
    }
    // The green one names the passing rerun, not the failed run or the other commit.
    expect(workflowReader(SCENARIOS[0][1]).v).toMatchObject({ state: 'success', url: `https://app.circleci.com/pipelines/${PROJECT}/2/workflows/w2` });
    expect(workflowReader(SCENARIOS[6][1]).v).toEqual({ state: 'none' });
  });

  it.runIf(!!DIR)('circleci-green.mjs says when it could not ask, never "not green", and never prints the token', async () => {
    for (const [what, routes, token] of ERRORS) {
      const w = workflowReader(routes, token);
      const t = await toolReader(routes, token);
      expect(t.error, `${what}: the release tool`).toBeTruthy();
      expect(w.v.error, what).toBeTruthy();
      expect(w.v.state, what).toBeUndefined();
      expect(w.v.error, what).not.toContain('secret-token');
    }
    expect(workflowReader(ERRORS[0][1], '').v.error).toContain('CIRCLECI_TOKEN is not set');
    // The token travels as the Circle-Token header, and nowhere else.
    const sent = workflowReader(SCENARIOS[0][1], 'secret-token');
    expect(sent.calls.length).toBeGreaterThan(1);
    for (const c of sent.calls) {
      expect(c.headers['Circle-Token']).toBe('secret-token');
      expect(c.url).not.toContain('secret-token');
    }
  });

  it.runIf(!!DIR)('circleci-green.mjs refuses what is no project slug or no commit before it asks anything', () => {
    const cli = path.join(DIR!, 'scripts', 'circleci-green.mjs');
    for (const argv of [
      [],
      ['--project', PROJECT],
      ['--project', `${PROJECT}/../other`, '--sha', SHA],
      ['--project', 'gh/BRF-Tech/filex?x=1', '--sha', SHA],
      ['--project', PROJECT, '--sha', 'v1.2.3'],
      ['--project', PROJECT, '--sha', SHA, '--token', 'x'],
    ]) {
      const r = spawnSync(process.execPath, [cli, ...argv], { encoding: 'utf8', env: { PATH: process.env.PATH ?? '', SYSTEMROOT: process.env.SYSTEMROOT ?? '' } });
      expect(r.status, argv.join(' ')).toBe(2);
      expect(r.stderr, argv.join(' ')).toMatch(/^usage: /m);
      expect(r.stdout, argv.join(' ')).toBe('');
    }
  });
});
