// The release gate's second source, CircleCI (task #181): what the release
// reads from its API, how a workflow's state becomes a verdict, which heavy
// gates it stands in for, and the proof that the engines ran.
//
// ⚠⚠ Why each of these matters. `pnpm release X.Y.Z --resume --gate
// circleci` lets a commit through the gate on CircleCI's word when GitHub
// Actions is down, so every way that word can be wrong has to read as red or
// as "wait", never as green:
//   - a call that fails (no token, a wrong project slug, the network) is an
//     error, not an empty list: an empty list reads as "no run yet" and is
//     waited on, and "could not check" is not a pass;
//   - a pipeline of another commit, or a workflow of another name, is not
//     this commit's test;
//   - only `success` is green; `failing` is red as soon as it is seen; a
//     rerun that passed wins, as on GitHub (runVerdict);
//   - a heavy gate CircleCI does not run is run here, never dropped;
//   - the engine suites skip without a DSN, and a skip is a pass: the CI job
//     reads its own events and is red unless both engines' parity subtests
//     passed (scripts/ci/engines-ran.mjs).
// The release as a whole, with --gate circleci, is driven end to end in
// releaseCli.test.ts.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

import { ENGINES, enginesRan } from '../../../scripts/ci/engines-ran.mjs';
import { GATE_SOURCES, runVerdict, selectGates } from '../../../scripts/release/checks.mjs';
import { circleciPipelines, pipelineRevision, workflowRun, workflowUrl } from '../../../scripts/release/circleci.mjs';

const REPO = path.resolve(__dirname, '../../..');
const SHA = 'a'.repeat(40);
const OTHER = 'b'.repeat(40);
const PROJECT = 'gh/BRF-Tech/filex';

const scratch: string[] = [];
afterAll(() => {
  for (const d of scratch) fs.rmSync(d, { recursive: true, force: true });
});

type Answer = { status?: number; body?: unknown; throws?: string; notJson?: boolean };

/**
 * A stand-in for CircleCI's API: `routes` maps "path?query" (after /api/v2)
 * to an answer. Every request is recorded with its headers.
 */
function fakeApi(routes: Record<string, Answer>) {
  const calls: Array<{ url: string; headers: Record<string, string> }> = [];
  const fetch = async (url: string, init: { headers: Record<string, string> }) => {
    calls.push({ url, headers: init.headers });
    const key = url.replace('https://circleci.com/api/v2', '');
    const a = routes[key];
    if (!a) return { ok: false, status: 404, json: async () => ({ message: `no route ${key}` }) };
    if (a.throws) throw new Error(a.throws);
    const status = a.status ?? 200;
    return {
      ok: status >= 200 && status < 300,
      status,
      json: async () => {
        if (a.notJson) throw new SyntaxError('Unexpected token <');
        return a.body;
      },
    };
  };
  return { fetch, calls };
}

const pipeline = (id: string, number: number, revision: string) => ({ id, number, state: 'created', vcs: { revision, branch: 'main' } });
const workflows = (...items: Array<{ id: string; name: string; status: string }>) => ({ body: { items, next_page_token: null } });

describe('circleci.mjs: a CircleCI workflow as a run', () => {
  it('only success is green; running and on_hold are not over; every other state is an end', () => {
    const at = (status: string) => workflowRun({ id: 'w', name: 'ci', status }, { sha: SHA, url: 'u' });
    expect(at('success')).toMatchObject({ status: 'completed', conclusion: 'success', headSha: SHA, title: 'ci', url: 'u' });
    for (const s of ['running', 'on_hold']) expect(at(s), s).toMatchObject({ status: 'in_progress', conclusion: '' });
    for (const s of ['failed', 'error', 'failing', 'canceled', 'unauthorized', 'not_run']) {
      expect(at(s), s).toMatchObject({ status: 'completed', conclusion: s });
      expect(runVerdict([at(s)]).state, s).toBe('failure');
    }
    expect(runVerdict([at('success')]).state).toBe('success');
    expect(runVerdict([at('running')]).state).toBe('running');
    expect(at('')).toMatchObject({ status: 'completed', conclusion: 'unknown' });
  });

  it('a rerun that passed wins over the run that failed; a rerun still going is waited for', () => {
    const failed = workflowRun({ id: 'w1', name: 'ci', status: 'failed' }, { sha: SHA, url: 'u1' });
    const passed = workflowRun({ id: 'w2', name: 'ci', status: 'success' }, { sha: SHA, url: 'u2' });
    const going = workflowRun({ id: 'w3', name: 'ci', status: 'running' }, { sha: SHA, url: 'u3' });
    expect(runVerdict([failed, passed])).toMatchObject({ state: 'success', run: { url: 'u2' } });
    expect(runVerdict([failed, going]).state).toBe('running');
  });

  it('the commit of a pipeline, from either integration', () => {
    expect(pipelineRevision(pipeline('p', 1, SHA))).toBe(SHA);
    expect(pipelineRevision({ trigger_parameters: { git: { checkout_sha: SHA } } })).toBe(SHA);
    expect(pipelineRevision({ trigger_parameters: { github_app: { checkout_sha: SHA } } })).toBe(SHA);
    expect(pipelineRevision({})).toBeNull();
    expect(pipelineRevision(null)).toBeNull();
  });

  it('a workflow\'s page, for the log and the record', () => {
    expect(workflowUrl('circleci/org-1/proj-2', 41, 'wf-9')).toBe('https://app.circleci.com/pipelines/circleci/org-1/proj-2/41/workflows/wf-9');
  });
});

describe('circleci.mjs: the runs of a workflow on one commit', () => {
  it('reads the pipelines of the branch, keeps the commit\'s, and the workflows of that name in them', async () => {
    const api = fakeApi({
      [`/project/${PROJECT}/pipeline?branch=main`]: { body: { items: [pipeline('p3', 3, OTHER), pipeline('p2', 2, SHA), pipeline('p1', 1, SHA)], next_page_token: null } },
      '/pipeline/p2/workflow': workflows({ id: 'w2', name: 'ci', status: 'success' }, { id: 'wx', name: 'nightly', status: 'failed' }),
      '/pipeline/p1/workflow': workflows({ id: 'w1', name: 'ci', status: 'failed' }),
    });
    const cc = circleciPipelines(PROJECT, { token: 'secret-token', fetch: api.fetch });
    const r = await cc.runs({ workflow: 'ci', sha: SHA, branch: 'main' });
    expect(r.error).toBeUndefined();
    expect(r.runs.map((x: { id: string }) => x.id)).toEqual(['w2', 'w1']);
    expect(r.runs[0]).toMatchObject({ conclusion: 'success', url: workflowUrl(PROJECT, 2, 'w2'), headSha: SHA });
    expect(runVerdict(r.runs).state).toBe('success');
    // the pipeline of the other commit was never opened
    expect(api.calls.map((c) => c.url)).not.toContain('https://circleci.com/api/v2/pipeline/p3/workflow');
    expect(api.calls.every((c) => c.headers['Circle-Token'] === 'secret-token')).toBe(true);
  });

  it('pages on until it is past the commit, and no further', async () => {
    const api = fakeApi({
      [`/project/${PROJECT}/pipeline?branch=main`]: { body: { items: [pipeline('p9', 9, OTHER)], next_page_token: 't2' } },
      [`/project/${PROJECT}/pipeline?branch=main&page-token=t2`]: { body: { items: [pipeline('p8', 8, SHA)], next_page_token: 't3' } },
      [`/project/${PROJECT}/pipeline?branch=main&page-token=t3`]: { body: { items: [pipeline('p7', 7, OTHER)], next_page_token: 't4' } },
      '/pipeline/p8/workflow': workflows({ id: 'w8', name: 'ci', status: 'running' }),
    });
    const r = await circleciPipelines(PROJECT, { token: 't', fetch: api.fetch }).runs({ workflow: 'ci', sha: SHA, branch: 'main' });
    expect(r.runs.map((x: { id: string }) => x.id)).toEqual(['w8']);
    expect(api.calls.some((c) => c.url.includes('page-token=t4'))).toBe(false);
  });

  it('no pipeline of the commit is no run - waited for, not passed', async () => {
    const api = fakeApi({ [`/project/${PROJECT}/pipeline?branch=main`]: { body: { items: [pipeline('p1', 1, OTHER)], next_page_token: null } } });
    const r = await circleciPipelines(PROJECT, { token: 't', fetch: api.fetch }).runs({ workflow: 'ci', sha: SHA, branch: 'main' });
    expect(r).toEqual({ runs: [] });
    expect(runVerdict(r.runs).state).toBe('none');
  });

  it('a call that fails is an error that says why, never an empty list', async () => {
    const pipelines = `/project/${PROJECT}/pipeline?branch=main`;
    const ask = async (routes: Record<string, Answer>, token = '') =>
      circleciPipelines(PROJECT, { token, fetch: fakeApi(routes).fetch }).runs({ workflow: 'ci', sha: SHA, branch: 'main' });

    const noToken = await ask({ [pipelines]: { status: 401, body: { message: 'You must log in first.' } } });
    expect(noToken.runs).toBeUndefined();
    expect(noToken.error).toContain('answered 401: You must log in first.');
    expect(noToken.error).toContain('CIRCLECI_TOKEN is not set');

    const wrongSlug = await ask({ [pipelines]: { status: 404, body: { message: 'Project not found' } } }, 't');
    expect(wrongSlug.error).toContain('answered 404');
    expect(wrongSlug.error).toContain(`is ${PROJECT} the project's slug`);

    const down = await ask({ [pipelines]: { throws: 'getaddrinfo ENOTFOUND circleci.com' } }, 't');
    expect(down.error).toContain('ENOTFOUND');

    const html = await ask({ [pipelines]: { notJson: true } }, 't');
    expect(html.error).toContain('did not answer JSON');

    const workflowsDown = await ask(
      { [pipelines]: { body: { items: [pipeline('p1', 1, SHA)], next_page_token: null } }, '/pipeline/p1/workflow': { status: 500, body: {} } },
      't',
    );
    expect(workflowsDown.error).toContain('/pipeline/p1/workflow answered 500');
  });

  it('sends no token when it has none (a public project may answer without one)', async () => {
    const api = fakeApi({ [`/project/${PROJECT}/pipeline?branch=main`]: { body: { items: [], next_page_token: null } } });
    await circleciPipelines(PROJECT, { token: '', fetch: api.fetch }).runs({ workflow: 'ci', sha: SHA, branch: 'main' });
    expect(api.calls).toHaveLength(1);
    expect(api.calls[0]!.headers['Circle-Token']).toBeUndefined();
  });
});

describe('selectGates: where the gate stage reads the heavy suites from', () => {
  const build = { name: 'build', always: true };
  const heavy = [
    { name: 'go', github: 'ci.yml', circleci: 'go' },
    { name: 'cypress', github: 'ci.yml', after: ['build'] },
    { name: 'playwright', circleci: 'e2e', after: ['build'], patchSkip: 'no browser suite in a patch' },
    { name: 'clock', after: ['build'], patchSkip: 'once, in UTC' },
  ];
  const plan = { pretag: [build], heavy };
  const n = (list: Array<{ name: string }>) => list.map((g) => g.name);

  it('GitHub, or CircleCI, and nothing else', () => {
    expect(GATE_SOURCES).toEqual(['github', 'circleci']);
    expect(() => selectGates(plan, 'minor', null, { source: 'none' })).toThrow('gate source none');
  });

  it('a minor: GitHub reads what ci.yml runs; CircleCI reads what it runs, and the rest runs here', () => {
    const gh = selectGates(plan, 'minor', null);
    expect(n(gh.remote)).toEqual(['go', 'cypress']);
    expect(gh.github).toBe(gh.remote);
    expect(n(gh.local)).toEqual(['build', 'playwright', 'clock']);
    const cc = selectGates(plan, 'minor', null, { source: 'circleci' });
    expect(n(cc.remote)).toEqual(['go', 'playwright']);
    expect(n(cc.local)).toEqual(['build', 'cypress', 'clock']);
  });

  it('a patch: a suite GitHub would have run and CircleCI does not runs here, it is not dropped', () => {
    const gh = selectGates(plan, 'patch', []);
    expect(n(gh.remote)).toEqual(['go', 'cypress']);
    expect(n(gh.skipped.map((s: { gate: { name: string } }) => s.gate))).toEqual(['playwright', 'clock']);
    const cc = selectGates(plan, 'patch', [], { source: 'circleci' });
    expect(n(cc.remote)).toEqual(['go', 'playwright']);
    expect(n(cc.local)).toEqual(['build', 'cypress']);
    expect(n(cc.skipped.map((s: { gate: { name: string } }) => s.gate))).toEqual(['clock']);
  });

  it('full: everything here, whichever CI the gate would read', () => {
    for (const source of GATE_SOURCES) {
      const f = selectGates(plan, 'full', null, { source });
      expect(f.remote).toEqual([]);
      expect(f.local).toEqual([]);
      expect(n(f.pretag)).toEqual(['build', 'go', 'cypress', 'playwright', 'clock']);
    }
  });
});

describe('scripts/ci/engines-ran.mjs: a skipped engine is not a pass', () => {
  const ev = (o: Record<string, unknown>) => JSON.stringify({ Time: '2026-10-06T00:00:00Z', ...o });
  const DB = 'github.com/brf-tech/filex/backend/internal/db';
  const parity = (engine: string, action = 'pass') => ev({ Action: action, Package: DB, Test: `TestSchemaParityAcrossEngines/parity/${engine}`, Elapsed: 1.2 });
  const ranDb = ev({ Action: 'run', Package: DB, Test: 'TestSchemaParityAcrossEngines' });

  it('both engines passed their parity subtest: proven', () => {
    expect(ENGINES).toEqual(['postgres', 'mysql']);
    expect(enginesRan([ranDb, parity('postgres'), parity('mysql'), 'ok  \tgithub.com/x 1.2s'].join('\n'))).toEqual({ tested: true, missing: [] });
  });

  it('an engine with no passed parity subtest - skipped, failed, never started - is missing', () => {
    expect(enginesRan([ranDb, parity('postgres')].join('\n')).missing).toEqual(['mysql']);
    expect(enginesRan([ranDb, parity('postgres'), parity('mysql', 'fail')].join('\n')).missing).toEqual(['mysql']);
    expect(enginesRan([ranDb, ev({ Action: 'skip', Package: DB, Test: 'TestMigrationsApplyOnEveryEngine/postgres' })].join('\n')).missing).toEqual(['postgres', 'mysql']);
  });

  it('a run that did not test internal/db has nothing to prove; a package below it is not internal/db', () => {
    expect(enginesRan('')).toEqual({ tested: false, missing: [] });
    const other = ev({ Action: 'pass', Package: `${DB}/drivers/sqlite`, Test: 'TestOpen' });
    expect(enginesRan([other, 'not json', '{broken'].join('\n'))).toEqual({ tested: false, missing: [] });
  });

  it('the CLI: 0 proven or nothing to prove, 1 an engine never ran, 2 nothing to read', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-engines-ran-'));
    scratch.push(dir);
    const file = (name: string, lines: string[]) => {
      const f = path.join(dir, name);
      fs.writeFileSync(f, `${lines.join('\n')}\n`);
      return f;
    };
    const cli = (...args: string[]) => spawnSync(process.execPath, [path.join(REPO, 'scripts', 'ci', 'engines-ran.mjs'), ...args], { encoding: 'utf8' });
    const both = cli(file('both.json', [ranDb, parity('postgres'), parity('mysql')]));
    expect(both.status, both.stderr).toBe(0);
    expect(both.stdout).toContain('internal/db ran on sqlite, postgres and mysql');
    const none = cli(file('none.json', [ev({ Action: 'pass', Package: 'github.com/brf-tech/filex/backend/internal/ops', Test: 'TestX' })]));
    expect(none.status).toBe(0);
    expect(none.stdout).toContain('did not test internal/db');
    const half = cli(file('half.json', [ranDb, parity('postgres')]));
    expect(half.status).toBe(1);
    expect(half.stderr).toContain('these engines never did: mysql');
    expect(cli(path.join(dir, 'missing.json')).status).toBe(2);
    expect(cli().status).toBe(2);
  });
});
