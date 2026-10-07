// What the release asks CircleCI, through its API v2: the workflow runs of a
// pipeline on one commit (task #181).
//
// CircleCI is the release gate's second source. When GitHub Actions is down
// (2026-10-05: the 0.52.0 dry run could not get a runner, twice), a person
// resumes the release with `--gate circleci`, and the gate stage reads the
// public repository's `.circleci/config.yml` workflow on the export commit
// from here instead of ci.yml and the dry run from GitHub. CircleCI schedules
// its own jobs, so an Actions outage does not stop it; a runner of our own
// that takes its work from GitHub would stop with it.
//
// The answers have the shape github.mjs gives (`{ runs: [{ id, status,
// conclusion, headSha, title, url }] }` or `{ error }`), so the gate judges
// both with the same runVerdict (checks.mjs).
//
// ⚠ Every answer says when it could not ask: "could not check" is not a
// pass, and an empty list from a failed call would read as "no run yet" and
// be waited on.
//
// The project slug: `gh/<org>/<repo>` for an organization connected through
// GitHub OAuth, `circleci/<org id>/<project id>` for one on the GitHub App
// (Project Settings → Overview shows it). The token: a CircleCI personal API
// token in CIRCLECI_TOKEN, read from the environment and never written
// anywhere; the maintainers keep it in the team vault. A public project's
// pipelines may answer without one; when they do not, the error says so.

export const CIRCLECI_API = 'https://circleci.com/api/v2';

/**
 * The commit a pipeline ran: `vcs.revision` (both integrations), or what a
 * GitHub App pipeline's trigger parameters carry when `vcs` is absent.
 */
export function pipelineRevision(p) {
  return p?.vcs?.revision ?? p?.trigger_parameters?.git?.checkout_sha ?? p?.trigger_parameters?.github_app?.checkout_sha ?? null;
}

/**
 * Workflow states that are not over yet. The API's workflow states are
 * success, running, not_run, failed, error, failing, on_hold, canceled and
 * unauthorized; `on_hold` waits for an approval (this repository's config has
 * none). Every state not listed here is an end, and only `success` is green.
 */
const GOING = new Set(['running', 'on_hold']);

/**
 * One CircleCI workflow as a run in github.mjs's shape. `failing` is a
 * workflow with a failed job and others still running: it cannot end green,
 * so it counts as finished - unless a rerun of it (another workflow of the
 * same name on the commit) passes, which runVerdict lets win.
 */
export function workflowRun(wf, { sha, url }) {
  const status = String(wf?.status ?? '');
  const going = GOING.has(status);
  return {
    id: wf?.id ?? null,
    status: going ? 'in_progress' : 'completed',
    conclusion: going ? '' : status || 'unknown',
    headSha: sha,
    title: wf?.name ?? '',
    url,
  };
}

/** The app's page of a workflow, for the log and the record. */
export function workflowUrl(project, pipelineNumber, workflowId) {
  return `https://app.circleci.com/pipelines/${project}/${pipelineNumber}/workflows/${workflowId}`;
}

/**
 * A client for one project. `fetch` and `token` are injectable so the
 * release's tests hand in a stand-in that never touches the network.
 *
 *   runs({ workflow, sha, branch })  the runs of workflow `workflow` in every
 *                                    pipeline of `branch` that ran `sha`
 */
export function circleciPipelines(project, { token = process.env.CIRCLECI_TOKEN ?? '', fetch: doFetch = (...args) => globalThis.fetch(...args), api = CIRCLECI_API, pages = 5 } = {}) {
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
          ? ` - is ${project} the project's slug (Project Settings → Overview), and may this token read it?`
          : ` - CIRCLECI_TOKEN is not set: put a CircleCI personal API token in it (the team vault keeps one), and check that ${project} is the project's slug`;
      }
      return { error: `GET ${pathAndQuery} answered ${res.status}${said}${hint}` };
    }
    if (!body || typeof body !== 'object') return { error: `GET ${pathAndQuery} did not answer JSON` };
    return { body };
  }

  return {
    project,

    async runs({ workflow, sha, branch }) {
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
          runs.push(workflowRun(wf, { sha, url: workflowUrl(project, p.number, wf.id) }));
        }
      }
      return { runs };
    },

    /** Where a person sees the same pipelines, for the log. */
    where({ branch }) {
      return `https://app.circleci.com/pipelines/${project}?branch=${encodeURIComponent(branch)}`;
    },
  };
}
