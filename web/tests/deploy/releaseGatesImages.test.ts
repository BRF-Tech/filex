// A tag must not publish anything until both container images are known to
// build.
//
// ⚠⚠ v0.43.0, run 36026548054. release.yml's first job, `test`, calls ci.yml
// as a gate, and passed it `skip_docker: true` ("the release's own docker job
// builds the same image"). But `binaries`, `docker` and `npm` all start once
// `test` passes, BESIDE one another, not one after another: when the images
// failed to build (the frontend stage did not copy two files its vite configs
// import), the npm packages and the GitHub Release were already public. Half a
// release, and nothing local had built an image either.
//
// So the gate builds both images, and cannot be told not to. This pins that:
// no `skip_docker` passed by the release, no `if:` on ci.yml's docker job, both
// Dockerfiles built there, and every publishing job waiting for the gate.
//
// ⚠⚠ #76: and the tag no longer starts the test. 0.43.0, 0.43.1, 0.44.0,
// 0.44.1 and 0.45.0 each spent a number on a red tag run. Both mains are now
// pushed untagged, CI runs on that push, the release tool starts a dry run of
// release.yml on the same commit, and only a commit that passed both is
// tagged. The gate of a tag run is `verify`: it publishes nothing unless
// GitHub holds those two successful runs on its commit - the CI run as the
// full matrix (#173) - and it fails when it cannot ask. Since #174 no run of
// release.yml runs the suite itself: the push run of the commit is the test.
//
// ⚠ The workflows live in the PUBLIC checkout only (scripts/export-public.sh
// keeps .github/workflows out of the private tree), so this reads them from
// `<repo>/.github/workflows` - which exists where GitHub runs this suite - or
// from FILEX_WORKFLOWS_DIR, which the local release run points at the public
// checkout. Without either it has nothing to read and says so.
//
// ⚠ Read as text, not parsed: no YAML parser is a dependency of the web app,
// and adding one on a release night changes the lockfile. Comment lines are
// dropped first, so a line explaining the old `skip_docker` is not mistaken
// for one setting it.
//
// ⚠ This file imports nothing from scripts/release: the workflow change it
// pins reaches the public checkout as its own commit, ahead of the export
// that brings the release tool along (packaging/ci/release-verify.patch), and
// in that commit this file has to run against the tool already there. The
// name of the dry run, "dry run all <sha>", is pinned here as text and in
// releaseChecks.test.ts against the tool's dryRunTitle().
import { describe, expect, it } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

const REPO = path.resolve(__dirname, '../../..');
const DIR = [process.env.FILEX_WORKFLOWS_DIR, path.join(REPO, '.github', 'workflows')].find(
  (d) => d && fs.existsSync(path.join(d, 'release.yml')) && fs.existsSync(path.join(d, 'ci.yml')),
);

/** A workflow's lines with comment-only lines removed. */
function code(file: string): string[] {
  return fs
    .readFileSync(path.join(DIR!, file), 'utf8')
    // Either line ending: a Windows checkout with autocrlf hands CRLF over, and
    // an exact match on `  docker:` would then find no job at all.
    .split(/\r?\n/)
    .filter((l) => !/^\s*#/.test(l));
}

/** The lines of one job (`  <name>:` under `jobs:`), up to the next job. */
function job(lines: string[], name: string): string[] {
  const start = lines.findIndex((l) => l === `  ${name}:`);
  expect(start, `job "${name}" exists`).toBeGreaterThanOrEqual(0);
  const next = lines.findIndex((l, i) => i > start && /^ {2}[A-Za-z0-9_-]+:\s*$/.test(l));
  return lines.slice(start, next < 0 ? undefined : next);
}

/** The jobs a job's `needs:` names, inline or as a list. */
function needs(block: string[]): string[] {
  const i = block.findIndex((l) => /^ {4}needs:/.test(l));
  if (i < 0) return [];
  const inline = block[i].replace(/^ {4}needs:\s*/, '').trim();
  if (inline) return inline.replace(/[[\]]/g, '').split(',').map((s) => s.trim()).filter(Boolean);
  const out: string[] = [];
  for (let j = i + 1; j < block.length && /^ {6}-\s/.test(block[j]); j++) out.push(block[j].replace(/^ {6}-\s*/, '').trim());
  return out;
}

/** Every job of a workflow, by name. */
function jobNames(lines: string[]): string[] {
  const start = lines.findIndex((l) => l === 'jobs:');
  return lines.slice(start + 1).map((l) => /^ {2}([A-Za-z0-9_-]+):\s*$/.exec(l)?.[1]).filter((n): n is string => !!n);
}

/**
 * The `&&` terms of a job's `if:` that must hold whatever its `||` groups say:
 * a parenthesised group demands nothing on its own.
 */
function topLevelTerms(cond: string): string[] {
  let s = cond.replace(/^ {4}if:\s*/, '').replace(/^\$\{\{\s*/, '').replace(/\s*\}\}\s*$/, '');
  for (let prev = ''; prev !== s; ) {
    prev = s;
    s = s.replace(/\([^()]*\)/g, '[]');
  }
  return s.split('&&').map((t) => t.trim());
}

/**
 * Whether a job can only run once `verify` has passed: it needs verify (or a
 * job that can), and its `if:` keeps the implicit success() or, with a status
 * function in it, demands that need's success outside every `||`.
 */
function waitsForVerify(lines: string[], name: string): boolean {
  if (name === 'verify') return true;
  const block = job(lines, name);
  const cond = block.find((l) => /^ {4}if:/.test(l)) ?? '';
  const free = /\b(always|cancelled|failure|success)\(\)/.test(cond);
  return needs(block)
    .filter((n) => n !== 'plan')
    .some((n) => waitsForVerify(lines, n) && (!free || topLevelTerms(cond).includes(`needs.${n}.result == 'success'`)));
}

describe('a release cannot publish before its images build', () => {
  it.runIf(!DIR)('has no workflows to read here (they live in the public checkout)', () => {
    // Not a pass: the private tree has no .github/workflows. The local release
    // run sets FILEX_WORKFLOWS_DIR so the cases below run there too.
    expect(DIR).toBeUndefined();
  });

  // ⚠ #174: until then release.yml ran ci.yml itself (`test`) in every run by
  // hand, and every package of a dry run waited for that second copy of the
  // suite the push of the same commit was running anyway. Now the push run is
  // the test - ci.yml's full matrix - and a dry run packages at once.
  it.runIf(!!DIR)('a dry run packages at once: no job of the release runs the test suite, the push run of its commit does', () => {
    const release = code('release.yml');
    expect(release.filter((l) => /uses:\s*\.\/\.github\/workflows\/ci\.yml/.test(l)), 'a job of release.yml calls ci.yml again').toEqual([]);
    expect(jobNames(release)).not.toContain('test');
    expect(release.filter((l) => /\bskip_docker\s*:/.test(l)), 'release.yml passes skip_docker').toEqual([]);
    // verify waits for the plan alone, and the packaging for verify: a dry
    // run (publish off) is let through at once (verify's script, below).
    expect(needs(job(release, 'verify'))).toEqual(['plan']);
    for (const name of ['binaries', 'docker', 'desktop']) expect(needs(job(release, name)), name).not.toContain('test');
    const script = runBlocks(job(release, 'verify')).map((b) => b.join('\n')).join('\n');
    const letThrough = script.indexOf('if [ "$PUBLISH" != true ]; then');
    expect(letThrough, 'verify lets a dry run through').toBeGreaterThan(0);
    expect(letThrough, 'before it asks GitHub anything').toBeLessThan(script.indexOf('gh api'));
    // ci.yml is not called any more, so it is no longer started by a caller.
    const ci = code('ci.yml');
    expect(ci.slice(0, ci.findIndex((l) => l === 'jobs:')).join('\n'), 'ci.yml still offers itself to a caller').not.toMatch(/workflow_call/);
  });

  it.runIf(!!DIR)("ci.yml's docker job cannot be switched off, and builds both images", () => {
    const ci = code('ci.yml');
    expect(ci.filter((l) => /^\s+skip_docker\s*:/.test(l)), 'ci.yml still declares a skip_docker input').toEqual([]);
    const docker = job(ci, 'docker');
    expect(docker.filter((l) => /^ {4}if:/.test(l)), "ci.yml's docker job has an `if:`").toEqual([]);
    const body = docker.join('\n');
    expect(body, 'the full image is built').toMatch(/docker build[^\n]*-f docker\/Dockerfile\s/);
    expect(body, 'the slim image is built').toMatch(/docker build[^\n]*-f docker\/Dockerfile\.slim\s/);
  });

  it.runIf(!!DIR)('every publishing job waits for the gate', () => {
    const release = code('release.yml');
    for (const name of ['binaries', 'docker', 'npm']) {
      const block = job(release, name);
      expect(needs(block), `${name} needs verify`).toContain('verify');
      // The implicit success(): a status function in `if:` would let the job
      // run past a verify that failed (always(), !cancelled(), failure()).
      expect(block.filter((l) => /^ {4}if:/.test(l) && /\b(always|cancelled|failure|success)\(\)/.test(l)), `${name} overrides the wait for verify`).toEqual([]);
    }
    // …and what depends on those still depends on the gate. The tags people
    // pull are made from the digests the dry run pushed, after both were
    // checked on their own architecture (#174), and wait for verify itself.
    expect(needs(job(release, 'docker-manifest'))).toEqual(expect.arrayContaining(['verify', 'promote-check']));
    expect(needs(job(release, 'promote-check'))).toContain('verify');
    const desktop = job(release, 'desktop');
    expect(needs(desktop)).toContain('binaries');
    expect(needs(desktop)).toContain('verify');
    expect(desktop.find((l) => /^ {4}if:/.test(l)), 'desktop runs a full release only after verify passed').toMatch(/needs\.verify\.result == 'success'/);
  });

  it.runIf(!!DIR)('a tag run publishes only a commit that passed CI and a dry run, and fails closed', () => {
    const verify = job(code('release.yml'), 'verify');
    const body = verify.join('\n');
    const cond = verify.find((l) => /^ {4}if:/.test(l)) ?? '';
    // It decides for itself whatever the run (a tag push, a run that adds
    // the ARM packages), and is never cancelled into a pass.
    expect(cond).toMatch(/!cancelled\(\)/);
    expect(cond).not.toMatch(/always\(\)/);
    expect(cond, 'verify skips the runs that add the ARM packages, and they publish unasked').not.toMatch(/outputs\.full/);
    expect(body, 'reads the runs with the token it is given, nothing more').toMatch(/permissions:\s*\n\s+actions: read/);
    const script = runBlocks(verify).map((b) => b.join('\n')).join('\n');
    expect(script).toMatch(/^set -euo pipefail$/m);
    // #174: no run asks for a suite of its own any more (the push run is it)
    expect(body).not.toMatch(/needs\.test\./);
    // a tag run: both runs, on THIS commit, successful
    expect(body).toMatch(/SHA: \$\{\{ github\.sha \}\}/);
    const ci = script.match(/actions\/workflows\/ci\.yml\/runs[\s\S]*?--jq '([^']*)'/);
    const dry = script.match(/actions\/workflows\/release\.yml\/runs[\s\S]*?--jq '([^']*)'/);
    expect(ci, 'asks for the ci.yml runs').not.toBeNull();
    expect(dry, 'asks for the release.yml runs').not.toBeNull();
    for (const [what, m, event] of [['ci.yml', ci, 'push'], ['the dry run', dry, 'workflow_dispatch']] as const) {
      const jq = m![1];
      expect(jq, `${what}: on this commit`).toContain('.head_sha == env.SHA');
      expect(jq, `${what}: started by ${event}`).toContain(`.event == "${event}"`);
      expect(jq, `${what}: passed`).toContain('.conclusion == "success"');
    }
    // the dry run of EVERYTHING, by the name run-name gives it
    expect(dry![1]).toContain('.display_title == env.WANT');
    expect(script, 'the name the release tool waits for (dryRunTitle)').toMatch(/export WANT="dry run all \$SHA"/);
    // #174: and the CI run was the FULL matrix - its last job passed
    // (scripts/ci-parts.mjs, completeName; the name pinned here as text)
    expect(script).toMatch(/export COMPLETE="All tests \(full\)"/);
    const jobs = script.match(/actions\/runs\/\$ci\/jobs[\s\S]*?--jq '([^']*)'/);
    expect(jobs, "asks for the jobs of that ci.yml run").not.toBeNull();
    expect(jobs![1]).toContain('.name == env.COMPLETE');
    expect(jobs![1]).toContain('.conclusion == "success"');
    // either missing: red, and nothing after it runs - unless CircleCI's ci
    // workflow passed on the commit (#181, releaseVerifyCircleci.test.ts)
    expect(script).toMatch(/if \[ "\$missing" -ne 0 \]; then[\s\S]*?exit 1/);
  });

  it.runIf(!!DIR)('every job that publishes waits for verify, a partial (only=arm64) run too', () => {
    const release = code('release.yml');
    // What publishes: the Release (goreleaser, gh release upload), images,
    // their tags, npm, the stores, the package managers, the AUR.
    const PUBLISHES = /gh release upload|snapcraft upload|pnpm publish|wingetcreate|msstore-submit|imagetools create|goreleaser-action|git push|push=\$\{\{/;
    const publishing = jobNames(release).filter((n) => job(release, n).some((l) => PUBLISHES.test(l)));
    expect(publishing, 'the markers still find the publishing jobs').toEqual(expect.arrayContaining(['binaries', 'docker', 'docker-manifest', 'npm', 'desktop', 'aur']));
    for (const name of publishing) expect(waitsForVerify(release, name), `${name} can publish without verify`).toBe(true);
    // A run that adds the ARM packages publishes from `desktop` alone, past a
    // skipped `binaries`: verify is demanded there whatever the run is.
    const desktop = job(release, 'desktop').find((l) => /^ {4}if:/.test(l)) ?? '';
    expect(topLevelTerms(desktop)).toContain("needs.verify.result == 'success'");
    // …and verify asks the tag's own commit when such a run publishes
    const script = runBlocks(job(release, 'verify')).map((b) => b.join('\n')).join('\n');
    expect(script).toMatch(/if \[ "\$PUBLISH" != true \]; then[\s\S]*?exit 0/);
    expect(script).toMatch(/SHA=\$\(gh api "repos\/\$GITHUB_REPOSITORY\/commits\/\$TAG" --jq \.sha\)/);
  });

  it.runIf(!!DIR)('names a dry run of everything after its commit, as the release gate looks for it', () => {
    const release = code('release.yml');
    const line = release.find((l) => /^run-name:/.test(l)) ?? '';
    expect(line, 'release.yml has no run-name').not.toBe('');
    // A run's inputs are not in GitHub's API: the name carries them. For a
    // run by hand with publish off, only=all and no tag, "{0} {1} {2}{3}"
    // reads "dry run all <sha>" - the name the release tool's dryRunTitle()
    // waits for and verify's WANT above looks for.
    const call = "format('{0} {1} {2}{3}', inputs.publish && 'publish' || 'dry run', inputs.only, inputs.tag && format('{0} ', inputs.tag) || '', github.sha)";
    expect(line.replace(/\s+/g, ' ')).toContain(call);
    expect(line).toMatch(/github\.event_name == 'workflow_dispatch' &&/);
  });

  it.runIf(!!DIR)("ci.yml's concurrency keeps a run the release calls apart from the branch's own", () => {
    // Until #174 release.yml called ci.yml, which then ran with the caller's
    // github context: a group of the ref alone put the release tool's dry run
    // on main and the CI run of that push of main in one group, and the newer
    // cancelled the older - one of the two runs the tag waits for. The group
    // still names both (ciFullMatrix.test.ts holds the rest: a push is grouped
    // by its commit and never cancelled).
    const ci = code('ci.yml');
    const i = ci.findIndex((l) => /^concurrency:/.test(l));
    expect(i, 'ci.yml has a concurrency block').toBeGreaterThanOrEqual(0);
    const group = ci.slice(i + 1).find((l) => /^\s+group:/.test(l)) ?? '';
    expect(group).toContain('github.workflow');
    expect(group).toContain('github.ref');
  });
});

/** Every `run: |` block of a workflow, as its script lines (indent removed). */
function runBlocks(lines: string[]): string[][] {
  const out: string[][] = [];
  for (let i = 0; i < lines.length; i++) {
    const m = /^(\s*)(?:-\s+)?run:\s*\|\s*$/.exec(lines[i]);
    if (!m) continue;
    const body: string[] = [];
    let indent = -1;
    for (let j = i + 1; j < lines.length; j++) {
      const l = lines[j];
      if (!l.trim()) continue;
      const ind = l.length - l.trimStart().length;
      if (indent < 0) indent = ind;
      if (ind < indent || ind <= m[1].length) break;
      body.push(l.slice(indent));
    }
    out.push(body);
  }
  return out;
}

// ⚠⚠ v0.45.1, run 36182595696: `winget validate` returned 0x8A150028
// ("succeeded with warnings"), the step accepted that code on purpose — and
// still failed. GitHub runs a pwsh step as `. '<script>'` followed by
// `exit $LASTEXITCODE`, so the tolerated code of the last native command IS
// the step's exit code. The desktop app's winget pull request was never
// opened. A step that tolerates a native exit code has to end by saying so.
describe('a pwsh step that tolerates a native exit code', () => {
  it.runIf(!!DIR)('ends with exit 0, because GitHub exits the step with $LASTEXITCODE', () => {
    const offenders: string[] = [];
    for (const file of ['release.yml', 'ci.yml']) {
      for (const body of runBlocks(code(file))) {
        const text = body.join('\n');
        if (!/\$LASTEXITCODE/.test(text) || !/-ne\s+0\s+-and\b/.test(text)) continue;
        const last = body.filter((l) => !/^\s*#/.test(l)).pop() ?? '';
        if (!/^\s*(exit 0|\$global:LASTEXITCODE\s*=\s*0)\s*$/.test(last)) offenders.push(`${file}: …${last.trim()}`);
      }
    }
    expect(offenders).toEqual([]);
  });
});
