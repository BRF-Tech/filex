// What the GitHub workflows run, and where their publishing secrets go.
//
// ⚠ Why (2026-11): compromised GitHub accounts pushed a CI secret stealer to
// tens of thousands of repositories, and an action referenced by a tag runs
// whatever that tag names on the day of the run - a tag is moved in one push.
// Nothing here was touched; this is the second layer.
//   - Every `uses:` names a full commit, with the tag it was taken from in a
//     comment (`actions/checkout@<40 hex> # v4.4.0`), so a moved or
//     hijacked tag changes nothing a run executes. A new action, or a new
//     version of one, is pinned the same way: the commit the tag names
//     (peeled, for an annotated tag), never the tag.
//   - Every job that reads a publishing secret runs in the `release`
//     environment, which only main and v* tags may deploy to (the repository's
//     Settings -> Environments). A run started on any other branch never sees
//     those secrets once they live in the environment, and a job added later
//     that reads one has to name it too.
//   - Every workflow says at the top what its token may do.
//
// The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts), and the release tool names these titles in its
// workflow guards (scripts/release/plan.mjs, WORKFLOW_GUARDS), so a skip there
// is red. The change reaches the public checkout as its own commits
// (packaging/ci/ci-actions-pinned.patch, packaging/ci/release-environment.patch).
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { DIR, job, jobNames } from '../helpers/releaseWorkflow';

/** Every workflow of the checkout: its file name and its text without comment-only lines, LF. */
function workflows(): { file: string; text: string }[] {
  return fs
    .readdirSync(DIR!)
    .filter((f) => /\.ya?ml$/.test(f))
    .sort()
    .map((file) => ({
      file,
      text: fs
        .readFileSync(path.join(DIR!, file), 'utf8')
        .split(/\r?\n/)
        .filter((l) => !/^\s*#/.test(l))
        .join('\n'),
    }));
}

/** An action reference pinned to a full commit, with its tag beside it. */
const PINNED = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+(?:\/[A-Za-z0-9_./-]+)?@[0-9a-f]{40} # v\d+(?:\.\d+)*$/;

/** The secrets a piece of a workflow reads, but the run's own token. */
const secretsIn = (text: string) =>
  [...new Set([...text.matchAll(/\bsecrets\.([A-Za-z0-9_]+)/g)].map((m) => m[1]))].filter((s) => s !== 'GITHUB_TOKEN').sort();

describe('the actions the workflows run, and where their secrets go', () => {
  it.runIf(!!DIR)('every action a workflow runs is pinned to a full commit, its tag in a comment', () => {
    const all = workflows();
    expect(all.map((w) => w.file)).toContain('release.yml');
    const bad: string[] = [];
    let n = 0;
    for (const { file, text } of all) {
      text.split('\n').forEach((line, i) => {
        const m = /^\s*(?:- )?uses:\s*(.+?)\s*$/.exec(line);
        if (!m) return;
        const ref = m[1];
        if (ref.startsWith('./')) return;
        n++;
        if (!PINNED.test(ref)) bad.push(`${file}: ${ref}`);
      });
    }
    expect(n, 'the workflows run actions').toBeGreaterThan(50);
    expect(bad, 'pin each one to the commit its tag names, the tag in a comment (docs/CONTRIBUTING.md -> Release process)').toEqual([]);
  });

  it.runIf(!!DIR)('every job that reads a publishing secret runs in the release environment', () => {
    const missing: string[] = [];
    const inRelease: string[] = [];
    for (const { file, text } of workflows()) {
      const at = text.split('\n').indexOf('jobs:');
      expect(at, `${file} has jobs`).toBeGreaterThan(0);
      const top = text.split('\n').slice(0, at).join('\n');
      expect(secretsIn(top), `${file} hands no secret to every job at once`).toEqual([]);
      for (const name of jobNames(text)) {
        const block = job(text, name);
        const secrets = secretsIn(block);
        const env = /\n {4}environment:\s*(.+)/.exec(block)?.[1].trim();
        if (env === 'release') inRelease.push(`${file}:${name}`);
        if (secrets.length && env !== 'release') missing.push(`${file}: job ${name} reads ${secrets.join(', ')} outside the release environment`);
      }
    }
    expect(missing).toEqual([]);
    // The jobs that publish today: the release's verify (CircleCI), binaries
    // (Homebrew, winget), desktop (every store) and aur, the Store's resubmit,
    // and the nightly's desktop (the Snap Store's edge).
    expect(inRelease).toEqual(
      expect.arrayContaining([
        'release.yml:verify',
        'release.yml:binaries',
        'release.yml:desktop',
        'release.yml:aur',
        'msstore-resubmit.yml:submit',
        'nightly-desktop.yml:desktop',
      ]),
    );
  });

  it.runIf(!!DIR)('every workflow says at the top what its token may do', () => {
    for (const { file, text } of workflows()) {
      expect(text, file).toMatch(/\npermissions:\n(?: {2}[a-z-]+: (?:read|write|none)[^\n]*\n)+/);
    }
  });
});
