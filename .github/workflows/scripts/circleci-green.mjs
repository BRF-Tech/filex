#!/usr/bin/env node
// Whether CircleCI passed a commit: the evidence a release run's `verify`
// takes when GitHub holds no full matrix and no dry run of that commit
// (release.yml; #181).
//
//   node circleci-green.mjs --project <slug> --sha <commit> [--branch main] [--workflow ci]
//
// Exit 0, and the workflow's page on stdout, when a workflow of that name in
// a pipeline of the branch that ran the commit succeeded (a rerun that passed
// counts, as on GitHub). Exit 1, and why on stderr, when none did: none ran,
// one failed, one is still going. Exit 2 when CircleCI could not be asked -
// whether the commit passed is then unknown, and unknown is not a pass.
//
// Why CircleCI at all: when GitHub Actions is down (2026-10-05: the 0.52.0
// dry run could not get a runner, twice), the release tool gates the commit
// on CircleCI's `ci` workflow instead (`pnpm release X.Y.Z --resume --gate
// circleci`). Once Actions is back, the tag run of that commit finds no
// ci.yml run and no dry run on it, and this is what lets it publish.
//
// The project slug: `gh/<org>/<repo>` for an organization connected through
// GitHub OAuth, `circleci/<org id>/<project id>` on the GitHub App (Project
// Settings → Overview). The token: CIRCLECI_TOKEN in the environment (the
// repository secret of that name), sent as the Circle-Token header and never
// printed. A public project's pipelines may answer without one; when they do
// not, the error says so.
//
// ⚠ It reads a commit exactly as the release tool's gate does
// (scripts/release/circleci.mjs and runVerdict in scripts/release/checks.mjs),
// but cannot import them: a run on a tag checks this commit's workflow
// scripts out on their own (CI_SCRIPTS in release.yml), not the tree.
// web/tests/deploy/releaseVerifyCircleci.test.ts runs both on the same
// answers and holds them to one verdict.
//
// Plain Node 18 or later, no dependency.

import { fileURLToPath } from 'node:url';
import path from 'node:path';

export const CIRCLECI_API = 'https://circleci.com/api/v2';

/** A project slug: two path segments after the VCS (or `circleci`), nothing that could leave the path. */
export const PROJECT_SLUG = /^(?:gh|github|bb|bitbucket|circleci)\/[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+$/;

/** A full commit id. */
export const COMMIT = /^[0-9a-f]{40}$/;

/**
 * Workflow states that are not over yet: `on_hold` waits for an approval.
 * Every other state is an end, and only `success` is green.
 */
const GOING = new Set(['running', 'on_hold']);

/**
 * The commit a pipeline ran: `vcs.revision` (both integrations), or what a
 * GitHub App pipeline's trigger parameters carry when `vcs` is absent.
 */
export function pipelineRevision(p) {
  return p?.vcs?.revision ?? p?.trigger_parameters?.git?.checkout_sha ?? p?.trigger_parameters?.github_app?.checkout_sha ?? null;
}

/** The app's page of a workflow. */
export function workflowUrl(project, pipelineNumber, workflowId) {
  return `https://app.circleci.com/pipelines/${project}/${pipelineNumber}/workflows/${workflowId}`;
}

/**
 * The verdict on `sha`: every workflow named `workflow` in the pipelines of
 * `branch` that ran it.
 *
 *   { state: 'success', url }                 one of them passed
 *   { state: 'running', url, status }         none passed, one is going
 *   { state: 'failure', url, status }         every one ended, none passed
 *   { state: 'none' }                         no pipeline of the commit ran it
 *   { error }                                 CircleCI could not be asked
 *
 * `fetch` and `token` are injectable so a test hands in a stand-in that never
 * touches the network.
 */
export async function circleciVerdict({ project, sha, branch = 'main', workflow = 'ci', token = '', fetch: doFetch = (...args) => globalThis.fetch(...args), api = CIRCLECI_API, pages = 5 }) {
  const headers = { Accept: 'application/json', ...(token ? { 'Circle-Token': token } : {}) };

  async function get(pathAndQuery) {
    let res;
    try {
      res = await doFetch(`${api}${pathAndQuery}`, { headers });
    } catch (e) {
      return { error: `GET ${pathAndQuery}: ${e?.message ?? e}` };
    }
    let body = null;
    try {
      body = await res.json();
    } catch {
      body = null;
    }
    if (!res.ok) {
      const said = body?.message ? `: ${body.message}` : '';
      let hint = '';
      if ([401, 403, 404].includes(res.status)) {
        hint = token
          ? ` - is ${project} the project's slug (Project Settings → Overview; the CIRCLECI_PROJECT_SLUG variable), and may this token read it?`
          : ` - CIRCLECI_TOKEN is not set: the repository secret of that name holds a CircleCI personal API token; and check that ${project} is the project's slug`;
      }
      return { error: `GET ${pathAndQuery} answered ${res.status}${said}${hint}` };
    }
    if (!body || typeof body !== 'object') return { error: `GET ${pathAndQuery} did not answer JSON` };
    return { body };
  }

  const found = [];
  let pageToken = null;
  for (let page = 0; page < pages; page++) {
    const q = new URLSearchParams({ branch });
    if (pageToken) q.set('page-token', pageToken);
    const r = await get(`/project/${project}/pipeline?${q}`);
    if (r.error) return { error: r.error };
    const items = Array.isArray(r.body.items) ? r.body.items : [];
    const mine = items.filter((p) => pipelineRevision(p) === sha);
    found.push(...mine);
    pageToken = r.body.next_page_token ?? null;
    // Newest first: past the pipelines of this commit, older pages hold
    // older commits.
    if (!pageToken || (found.length && !mine.length)) break;
  }
  const runs = [];
  for (const p of found) {
    const r = await get(`/pipeline/${p.id}/workflow`);
    if (r.error) return { error: r.error };
    for (const wf of Array.isArray(r.body.items) ? r.body.items : []) {
      if (wf?.name !== workflow) continue;
      runs.push({ status: String(wf?.status ?? '') || 'unknown', url: workflowUrl(project, p.number, wf.id) });
    }
  }
  if (!runs.length) return { state: 'none' };
  const ok = runs.find((r) => r.status === 'success');
  if (ok) return { state: 'success', url: ok.url };
  const going = runs.find((r) => GOING.has(r.status));
  if (going) return { state: 'running', url: going.url, status: going.status };
  return { state: 'failure', url: runs[0].url, status: runs[0].status };
}

/** `--name value` pairs; null when an argument is not one of them. */
function options(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i += 2) {
    const m = /^--(project|sha|branch|workflow)$/.exec(argv[i] ?? '');
    if (!m || argv[i + 1] === undefined) return null;
    out[m[1]] = argv[i + 1];
  }
  return out;
}

export async function main(argv, env = process.env) {
  const o = options(argv);
  const usage = 'usage: node circleci-green.mjs --project <slug> --sha <40-hex commit> [--branch main] [--workflow ci]';
  if (!o || !PROJECT_SLUG.test(o.project ?? '') || !COMMIT.test(o.sha ?? '')) {
    console.error(usage);
    return 2;
  }
  const branch = o.branch ?? 'main';
  const workflow = o.workflow ?? 'ci';
  const v = await circleciVerdict({ project: o.project, sha: o.sha, branch, workflow, token: env.CIRCLECI_TOKEN ?? '' });
  if (v.error) {
    console.error(`could not ask CircleCI (${v.error}): whether ${o.sha} passed there is unknown, and unknown does not pass`);
    return 2;
  }
  if (v.state === 'success') {
    console.log(v.url);
    return 0;
  }
  if (v.state === 'none') console.error(`no pipeline of ${o.project} on ${branch} ran the workflow "${workflow}" on ${o.sha}`);
  else if (v.state === 'running') console.error(`the workflow "${workflow}" on ${o.sha} is not over (${v.status}): ${v.url}`);
  else console.error(`the workflow "${workflow}" on ${o.sha} ended "${v.status}": ${v.url}`);
  return 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  // exitCode, not exit(): what was printed reaches a pipe before the process ends.
  main(process.argv.slice(2)).then(
    (code) => {
      process.exitCode = code;
    },
    (e) => {
      console.error(`circleci-green.mjs: ${e?.stack ?? e}`);
      process.exitCode = 2;
    },
  );
}
