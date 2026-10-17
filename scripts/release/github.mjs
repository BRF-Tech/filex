// What the release asks GitHub Actions, through the `gh` CLI: the runs of a
// workflow on one commit, the jobs of a run (ci.yml's matrix, read part by
// part), the artifacts a run kept, starting a workflow by hand, and the pull
// requests the export commit merges (--merge-pr).
//
// The gate stage (stages.mjs → gate) takes this from the plan as
// `plan.github`, so the release's own test (web/tests/deploy/releaseCli.test.ts)
// hands in a stand-in that never touches the network.
//
// ⚠ Every answer says when it could not ask: "could not check" is not a pass,
// and an empty list from a failed `gh` call would read as "no run yet" and be
// waited on for hours.

import { run } from './engine.mjs';

/** `gh workflow run` as a person would type it, for the log and the docs. */
function dispatchArgs(repo, { workflow, ref, inputs = {} }) {
  const args = ['workflow', 'run', workflow, '-R', repo, '--ref', ref];
  for (const [k, v] of Object.entries(inputs)) args.push('-f', `${k}=${v}`);
  return args;
}

export function githubActions(repo) {
  return {
    repo,

    /**
     * The runs of `workflow` on `sha` started by `event`, newest first:
     * { id, status, conclusion, headSha, event, title, url, attempt, branch }.
     * `title` is the run's name (release.yml's `run-name`); `attempt` counts
     * its re-runs (1 for the first); `branch` is the branch or tag it ran on
     * (a tag run's is the tag).
     */
    runs({ workflow, sha, event }) {
      const r = run('gh', [
        'run', 'list', '-R', repo, '--workflow', workflow, '--commit', sha, '--event', event, '--limit', '50',
        '--json', 'databaseId,status,conclusion,headSha,event,displayTitle,url,attempt,headBranch',
      ]);
      if (r.status !== 0) return { error: (r.stderr || r.stdout).trim() || `gh exited ${r.status}` };
      let list;
      try {
        list = JSON.parse(r.stdout || '[]');
      } catch {
        return { error: `gh run list did not answer JSON: ${r.stdout.slice(0, 200)}` };
      }
      return {
        runs: list
          .filter((x) => x.headSha === sha && x.event === event)
          .map((x) => ({ id: x.databaseId, status: x.status, conclusion: x.conclusion, headSha: x.headSha, event: x.event, title: x.displayTitle, url: x.url, attempt: x.attempt ?? 1, branch: x.headBranch ?? null })),
      };
    },

    /**
     * The jobs of one run, its latest attempt (a re-run's jobs replace the
     * ones they re-ran): { id, name, status, conclusion, url, failedSteps }.
     * The gate stage reads ci.yml's matrix this way, part by part (#174), and
     * a red job's failed or cancelled steps say whether it is red in the code
     * or on the runner (checks.mjs → rerunDecision, #165). One JSON object a
     * line: a step's name may hold any character.
     */
    jobs({ runId }) {
      const r = run('gh', [
        'api', '-X', 'GET', `repos/${repo}/actions/runs/${runId}/jobs`, '-f', 'filter=latest', '-f', 'per_page=100', '--paginate',
        '--jq',
        '.jobs[] | {id, name, status, conclusion, url: .html_url, failedSteps: [(.steps // [])[] | select(.conclusion == "failure" or .conclusion == "timed_out" or .conclusion == "cancelled") | .name]} | @json',
      ]);
      if (r.status !== 0) return { error: (r.stderr || r.stdout).trim() || `gh exited ${r.status}` };
      const jobs = [];
      for (const line of String(r.stdout).split(/\r?\n/).filter(Boolean)) {
        let j;
        try {
          j = JSON.parse(line);
        } catch {
          return { error: `gh api .../jobs did not answer JSON: ${line.slice(0, 200)}` };
        }
        if (!j?.name || !j.status) return { error: `gh api .../jobs did not answer id, name, status: ${line.slice(0, 200)}` };
        jobs.push({ id: Number(j.id), name: j.name, status: j.status, conclusion: j.conclusion || null, url: j.url, failedSteps: Array.isArray(j.failedSteps) ? j.failedSteps : [] });
      }
      return { jobs };
    },

    /**
     * The names of a run's artifacts that have not expired: what a release
     * candidate kept for its tag run to promote.
     */
    artifacts({ runId }) {
      const r = run('gh', [
        'api', '-X', 'GET', `repos/${repo}/actions/runs/${runId}/artifacts`, '-f', 'per_page=100', '--paginate',
        '--jq', '.artifacts[] | select(.expired == false) | .name',
      ]);
      if (r.status !== 0) return { error: (r.stderr || r.stdout).trim() || `gh exited ${r.status}` };
      return { names: String(r.stdout).split(/\r?\n/).map((s) => s.trim()).filter(Boolean) };
    },

    /**
     * One pull request, as the land reads it before the export commit merges
     * it (stages.mjs → land, --merge-pr): { number, state, merged, base,
     * head, label, title } - `base` the branch it asks to go into, `head` its
     * head commit, `label` GitHub's "owner:branch" of that head.
     */
    pull({ number }) {
      const r = run('gh', [
        'api', '-X', 'GET', `repos/${repo}/pulls/${number}`,
        '--jq', '{number, state, merged, base: .base.ref, head: .head.sha, label: .head.label, title} | @json',
      ]);
      if (r.status !== 0) return { error: (r.stderr || r.stdout).trim() || `gh exited ${r.status}` };
      let p;
      try {
        p = JSON.parse(String(r.stdout).trim());
      } catch {
        return { error: `gh api .../pulls/${number} did not answer JSON: ${String(r.stdout).slice(0, 200)}` };
      }
      if (p?.number !== number || !/^[0-9a-f]{40}$/.test(String(p.head ?? '')) || !p.state || !p.base) {
        return { error: `gh api .../pulls/${number} did not answer its number, state, base and head: ${String(r.stdout).slice(0, 200)}` };
      }
      return { pull: { number: p.number, state: p.state, merged: !!p.merged, base: p.base, head: p.head, label: p.label ?? '', title: p.title ?? '' } };
    },

    /** Starts `workflow` on `ref` with `inputs`. */
    dispatch(spec) {
      const args = dispatchArgs(repo, spec);
      const r = run('gh', args);
      return r.status === 0 ? { ok: true, detail: `gh ${args.join(' ')}` } : { ok: false, detail: (r.stderr || r.stdout).trim() || `gh exited ${r.status}` };
    },

    command(spec) {
      return `gh ${dispatchArgs(repo, spec).join(' ')}`;
    },

    rerun(id) {
      return `gh run rerun ${id} --failed -R ${repo}`;
    },

    /**
     * Re-runs the failed (and cancelled) jobs of a finished run, as a new
     * attempt (#165): only ever for parts red on the runner, never in the code
     * (stages.mjs → gate).
     */
    rerunFailed({ runId }) {
      const r = run('gh', ['run', 'rerun', String(runId), '--failed', '-R', repo]);
      return r.status === 0 ? { ok: true, detail: `gh run rerun ${runId} --failed -R ${repo}` } : { ok: false, detail: (r.stderr || r.stdout).trim() || `gh exited ${r.status}` };
    },
  };
}
