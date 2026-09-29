// Every release sends its desktop package to the Microsoft Store, and a Store
// problem never costs the release anything else.
//
// ⚠ Why: until 0.47.0 release.yml only uploaded a DRAFT (`--noCommit`) and a
// person wrote "What's new" and submitted it in Partner Center; before
// 2026-09-26 it had no credentials at all, so every release skipped the Store
// with a warning and the package was uploaded by hand. Now an Entra ID app
// ("filex release pipeline", Manager role) submits it:
// .github/workflows/scripts/msstore-submit.ps1 uploads, points "What's new" at
// the GitHub Release, and commits the submission to certification.
//
// ⚠ The Store package's activation checks cannot gate the submission on the
// runner: GitHub's Windows runner has never activated an MSIX (v0.44.2 to
// v0.46.1). They are strict in the local release run instead
// (scripts/release/plan.mjs, "desktop: the Store package works as a Store
// copy"), so the workflow must not switch them to strict on CI — doing so
// would fail the Store job on every release and nothing would ever be
// submitted.
//
// The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts): read from `<repo>/.github/workflows` or
// FILEX_WORKFLOWS_DIR, and skipped when neither exists.
import { describe, expect, it } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

const REPO = path.resolve(__dirname, '../../..');
const DIR = [process.env.FILEX_WORKFLOWS_DIR, path.join(REPO, '.github', 'workflows')].find(
  (d) => d && fs.existsSync(path.join(d, 'release.yml')),
);
const SCRIPT = 'scripts/msstore-submit.ps1';

/** A file's lines without comment-only lines (`#` in YAML and PowerShell). */
const code = (rel: string) =>
  fs
    .readFileSync(path.join(DIR!, rel), 'utf8')
    .split(/\r?\n/)
    .filter((l) => !/^\s*#/.test(l))
    .join('\n');

/** The text of one step (from its `- name:` to the next step or job). */
function step(text: string, name: string): string {
  const at = text.indexOf(`- name: ${name}`);
  expect(at, `step "${name}"`).toBeGreaterThan(0);
  const rest = text.slice(at + 1);
  const next = rest.search(/\n\s+- (name|uses):|\n {2}[A-Za-z0-9_-]+:\s*\n/);
  return next < 0 ? rest : rest.slice(0, next);
}

describe('the release submits its desktop package to the Microsoft Store', () => {
  it.runIf(!!DIR)('commits the submission through msstore-submit.ps1, never as a draft', () => {
    const release = code('release.yml');
    const submit = step(release, 'Submit to the Microsoft Store');
    expect(submit).toMatch(/msstore-submit\.ps1 -Msix \$msix -Version \$ver/);
    expect(submit).toMatch(/exit 0\s*$/);
    expect(release).not.toMatch(/--noCommit/);
    const script = code(SCRIPT);
    // The one --noCommit in the script is followed by the commit call.
    expect(script).toMatch(/--noCommit[\s\S]+\/submissions\/\$sid\/commit/);
    expect(script).toMatch(/releaseNotes/);
  });

  // ⚠ v0.47.0's submission never left the runner: the script passed the .msix
  // as `-i`, which msstore 0.4.x parses as --inputDirectory and rejects with
  // "Input directory does not exist" (exit 1). msstore takes a package file as
  // its pathOrUrl argument (MSIXProjectPublisher), with no -i at all.
  it.runIf(!!DIR)('hands msstore the .msix as its path, not as -i', () => {
    const script = code(SCRIPT);
    expect(script).toMatch(/msstore publish \$Msix -id \$app --noCommit/);
    expect(script).not.toMatch(/msstore publish[^\n]*\s-i\s/);
  });

  // A release the Store did not take (skipped behind a submission still in
  // certification, or failed like v0.47.0's) is sent again from the package it
  // kept, with the same script — never rebuilt, never uploaded by hand.
  it.runIf(!!DIR)('can resubmit a release from the package it kept', () => {
    const wf = code('msstore-resubmit.yml');
    expect(wf).toMatch(/workflow_dispatch:/);
    expect(wf).toMatch(/gh run download \$run [^\n]*-n "msix-\$env:TAG"/);
    expect(wf).toMatch(/msstore-submit\.ps1 -Msix \$msix -Version \$env:TAG\.TrimStart\('v'\)/);
    // The inputs reach the shell through env, never spliced into a script:
    // every line that reads one is an env mapping (the tag, and the run id of
    // a run that only added the ARM packages, release.yml -f only=arm64).
    expect(wf).toMatch(/TAG: \$\{\{ inputs\.tag \}\}/);
    const reads = wf.split('\n').filter((l) => /\$\{\{\s*inputs\./.test(l));
    expect(reads.length).toBeGreaterThanOrEqual(1);
    for (const l of reads) expect(l, 'an input read outside env').toMatch(/^\s+[A-Z_]+: \$\{\{ inputs\.[a-z_]+ \}\}\s*$/);
    for (const s of ['MSSTORE_TENANT_ID', 'MSSTORE_SELLER_ID', 'MSSTORE_CLIENT_ID', 'MSSTORE_CLIENT_SECRET']) {
      expect(wf).toContain(`${s}: \${{ secrets.${s} }}`);
    }
    expect(wf).toContain('MSSTORE_PRODUCT_ID: ${{ vars.MSSTORE_PRODUCT_ID }}');
    // The skip message tells the maintainer to run it.
    expect(code(SCRIPT)).toMatch(/gh workflow run msstore-resubmit\.yml -R BRF-Tech\/filex -f tag=v\$Version/);
  });

  // ⚠ v0.47.0's resubmit uploaded the package, then a Store call answered
  // "400 (Bad Request)" and the warning said nothing else: the reason is in
  // the response body (ErrorDetails), and which call it was is the line.
  // ⚠ The reason, once printed: "Only JSON content is accepted" on the commit.
  // Invoke-RestMethod sends a body-less POST as
  // application/x-www-form-urlencoded (measured against httpbin, 2026-09-27);
  // -ContentType application/json alone makes it JSON, still with no body.
  it.runIf(!!DIR)('commits the submission as JSON', () => {
    expect(code(SCRIPT)).toMatch(/-Method Post -Headers \$h -ContentType 'application\/json' "\$api\/submissions\/\$sid\/commit"/);
  });

  it.runIf(!!DIR)('says which call failed and what the Store answered', () => {
    const script = code(SCRIPT);
    expect(script.match(/ErrorDetails\.Message/g)?.length ?? 0).toBeGreaterThanOrEqual(2);
    expect(script).toMatch(/InvocationInfo\.ScriptLineNumber/);
  });

  it.runIf(!!DIR)('leaves a submission still in certification alone, and never fails the release', () => {
    const script = code(SCRIPT);
    for (const state of ['CommitStarted', 'PreProcessing', 'Certification', 'Release', 'PendingPublication', 'Publishing']) {
      expect(script, state).toContain(`'${state}'`);
    }
    // Every way out is exit 0 (Skip prints a warning, then exits 0).
    expect(script).not.toMatch(/\bexit\s+[1-9]/);
    expect(script).not.toMatch(/\bthrow\b/);
    expect(script).toMatch(/\}\s*catch\s*\{\s*\n\s*Skip /);
  });

  it.runIf(!!DIR)('keeps the Store package check soft on the runner; the local release run is the strict one', () => {
    const release = code('release.yml');
    const e2e = step(release, 'Install the Store package and check it as a Store copy');
    expect(e2e).toMatch(/e2e:store/);
    expect(release).not.toMatch(/STORE_E2E_STRICT/);
  });
});
