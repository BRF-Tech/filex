// What the release asks GitHub Actions, through the `gh` CLI: the runs of a
// workflow on one commit, and starting a workflow by hand.
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
     * { id, status, conclusion, headSha, event, title, url }. `title` is the
     * run's name (release.yml's `run-name`).
     */
    runs({ workflow, sha, event }) {
      const r = run('gh', [
        'run', 'list', '-R', repo, '--workflow', workflow, '--commit', sha, '--event', event, '--limit', '50',
        '--json', 'databaseId,status,conclusion,headSha,event,displayTitle,url',
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
          .map((x) => ({ id: x.databaseId, status: x.status, conclusion: x.conclusion, headSha: x.headSha, event: x.event, title: x.displayTitle, url: x.url })),
      };
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
  };
}
