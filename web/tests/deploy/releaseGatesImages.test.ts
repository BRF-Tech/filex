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
// Dockerfiles built there, and every publishing job waiting for `test`.
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

describe('a release cannot publish before its images build', () => {
  it.runIf(!DIR)('has no workflows to read here (they live in the public checkout)', () => {
    // Not a pass: the private tree has no .github/workflows. The local release
    // run sets FILEX_WORKFLOWS_DIR so the cases below run there too.
    expect(DIR).toBeUndefined();
  });

  it.runIf(!!DIR)('the release calls the gate WITHOUT skip_docker', () => {
    const test = job(code('release.yml'), 'test');
    expect(test.join('\n')).toMatch(/uses:\s*\.\/\.github\/workflows\/ci\.yml/);
    expect(test.filter((l) => /\bskip_docker\s*:/.test(l)), 'release.yml passes skip_docker to the gate').toEqual([]);
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
      expect(needs(job(release, name)), `${name} needs test`).toContain('test');
    }
    // …and what depends on those still depends on the gate, one step removed.
    expect(needs(job(release, 'docker-manifest'))).toContain('docker');
    expect(needs(job(release, 'desktop'))).toContain('binaries');
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
