// The npm packages are published through npm's trusted publishing (OIDC), with
// no token (#146).
//
// ⚠ Why (v0.50.0, 2026-10-03): the release went out without its npm packages.
// The npm job read an NPM_TOKEN secret, an npm granular token that lives 90
// days at most; it had run out, npm answered the publish with "PUT ... 404 Not
// Found", and nobody had seen it coming (lesson #965, task #137). A token that
// expires fails a release every quarter, and a failure that looks like a
// missing package at that.
//
// What the job that publishes to npm must hold, each one careless edit away:
//   - its own permissions: id-token: write (GitHub hands the job an identity,
//     which npm trades for a short-lived publish token per package) and
//     contents: read, nothing more;
//   - no token anywhere: no NPM_TOKEN, no NODE_AUTH_TOKEN, no _authToken line
//     in an .npmrc, no secret. A token beside OIDC is the thing that runs out
//     again;
//   - an npm (11.5.1 or later) and a Node (22.14.0 or later) that can do
//     trusted publishing (docs.npmjs.com/trusted-publishers), checked before
//     anything is published;
//   - provenance, and each package's repository.url set from the repository
//     the run is in: npm compares the two letter for letter and refuses a
//     mismatch (E422), and the export writes github.com/brf-tech/filex where
//     GitHub signs github.com/BRF-Tech/filex.
//
// The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts), and the release tool names these titles in its
// workflow guards (scripts/release/plan.mjs, WORKFLOW_GUARDS), so a skip there
// is red. The change reaches the public checkout as its own commit
// (packaging/ci/release-npm-trusted-publishing.patch). The trusted publishers
// themselves are set on npmjs.com, one per package (docs/CONTRIBUTING.md →
// Release process).
import { describe, expect, it } from 'vitest';

import { DIR, code, job, jobNames, stepsOf } from '../helpers/releaseWorkflow';

/** A command that publishes to npm (pnpm's publish runs npm's). */
const PUBLISHES_NPM = /\b(?:pnpm|npm) publish\b/;

/** The jobs of release.yml that publish to npm. */
function npmJobs(release: string): string[] {
  return jobNames(release).filter((n) => PUBLISHES_NPM.test(job(release, n)));
}

describe('the npm packages are published through trusted publishing, with no token', () => {
  it.runIf(!DIR)('has no workflows to read here (they live in the public checkout)', () => {
    // Not a pass: the private tree has no .github/workflows. The local release
    // run sets FILEX_WORKFLOWS_DIR so the cases below run there too.
    expect(DIR).toBeUndefined();
  });

  it.runIf(!!DIR)('the job that publishes to npm may ask GitHub for an identity token, and reads the repository, nothing more', () => {
    const release = code();
    const jobs = npmJobs(release);
    expect(jobs, 'the npm publish is still found').toContain('npm');
    for (const name of jobs) {
      const block = job(release, name);
      // Without its own permissions a job gets the release-wide ones
      // (contents: write, packages: write), and no id-token at all.
      const m = /\n {4}permissions:[ \t]*\n((?: {6}\S[^\n]*(?:\n|$))+)/.exec(block);
      expect(m, `${name} sets its own permissions`).not.toBeNull();
      const granted = Object.fromEntries(
        m![1]
          .trimEnd()
          .split('\n')
          .map((l) => l.trim().split(/:\s*/) as [string, string]),
      );
      expect(granted, name).toEqual({ contents: 'read', 'id-token': 'write' });
    }
  });

  it.runIf(!!DIR)('no npm token reaches the release: no NPM_TOKEN, no NODE_AUTH_TOKEN, no _authToken', () => {
    const release = code();
    expect(release, 'release.yml reads an npm token').not.toMatch(/NPM_TOKEN|NODE_AUTH_TOKEN/);
    for (const name of npmJobs(release)) {
      const block = job(release, name);
      expect(block, `${name} writes a token for npm`).not.toMatch(/_authToken|\.npmrc/);
      expect(block, `${name} reads a secret`).not.toMatch(/secrets\./);
    }
  });

  it.runIf(!!DIR)('publishes with an npm (11.5.1 or later) and a Node (22.14.0 or later) that can use trusted publishing', () => {
    const release = code();
    for (const name of npmJobs(release)) {
      const steps = stepsOf(job(release, name));
      const node = steps.find((s) => s.name.startsWith('actions/setup-node@'));
      expect(node, `${name} sets up Node`).toBeTruthy();
      const major = Number(/node-version:\s*["']?(\d+)/.exec(node!.text)?.[1]);
      expect(major, `${name}: Node 22 or later (trusted publishing needs 22.14.0)`).toBeGreaterThanOrEqual(22);
      const check = steps.findIndex((s) => /at_least 11\.5\.1 "\$npm_v"/.test(s.text) && /at_least 22\.14\.0 "\$node_v"/.test(s.text));
      expect(check, `${name} checks the npm and the Node it publishes with`).toBeGreaterThanOrEqual(0);
      const text = steps[check].text;
      expect(text, 'npm 11 goes over the npm 10 Node 22 brings').toMatch(/npm install -g npm@11\b/);
      expect(text, 'the npm checked is the one on the PATH').toMatch(/npm_v=\$\(npm --version\)/);
      expect(text, 'the Node checked is the one on the PATH').toMatch(/node_v=\$\(node --version\)/);
      const publish = steps.findIndex((s) => PUBLISHES_NPM.test(s.text));
      expect(publish, `${name}: the versions are checked before the publish`).toBeGreaterThan(check);
    }
  });

  it.runIf(!!DIR)('asks for provenance, and names the repository the way GitHub signs it', () => {
    const release = code();
    for (const name of npmJobs(release)) {
      const publish = stepsOf(job(release, name)).find((s) => PUBLISHES_NPM.test(s.text));
      expect(publish, `${name} publishes`).toBeTruthy();
      const text = publish!.text;
      // pnpm's recursive publish does not pass --provenance on to npm, so the
      // flag counts only on npm publish itself; npm's own config always does.
      const env = /\n\s+NPM_CONFIG_PROVENANCE:\s*["']?true["']?\s*\n/.test(text);
      const flag = /(?:^|[^p])npm publish\b[^\n]*--provenance\b/m.test(text);
      expect(env || flag, `${name} asks for provenance`).toBe(true);
      const url = text.indexOf('npm pkg set "repository.url=git+https://github.com/${GITHUB_REPOSITORY}.git"');
      expect(url, 'repository.url is set from the repository the run is in').toBeGreaterThanOrEqual(0);
      expect(text.search(PUBLISHES_NPM), 'before the publish').toBeGreaterThan(url);
    }
  });
});
