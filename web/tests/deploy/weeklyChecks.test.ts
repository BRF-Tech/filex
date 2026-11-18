// The weekly checks on GitHub (#364): what the build host's nightly chain ran
// on its own until 2026-11-18 and GitHub's matrix does not - filex against
// real ACME, SSO and office servers (.github/workflows/realenv.yml), the Go
// tests against a real S3 provider (s3-live.yml) and every screenshot scene
// (shots.yml's weekly nightly mode) - each once a week on the `nightly`
// branch's commit, and by hand.
//
// ⚠ Why (the owner, 2026-11-18): the night left the build host for GitHub,
// and these three were left with no home but a person's run. What these hold
// is what keeps a week from reading green when nothing ran: a skipped realenv
// stage is red, a live S3 test that only skipped is red, and a missing S3
// secret says "skipped: no S3 credentials" in the open instead of failing a
// run nobody can fix from the code. Nothing here publishes a picture.
//
// The workflows live in the PUBLIC checkout only; the change reaches it as
// packaging/ci/weekly-checks.patch, and the release tool names these
// workflow tests in its guards.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { DIR, REPO, bash, job, scriptOf, stepNamed, stepsOf } from '../helpers/releaseWorkflow';

/** A workflow without comment-only lines, LF. */
const wf = (file: string) =>
  fs
    .readFileSync(path.join(DIR!, file), 'utf8')
    .split(/\r?\n/)
    .filter((l) => !/^\s*#/.test(l))
    .join('\n');
const on = (text: string) => text.slice(0, text.indexOf('\njobs:'));
const fwd = (p: string) => p.split(path.sep).join('/');
const has = (file: string) => !!DIR && fs.existsSync(path.join(DIR, file));

/** Runs a step's script in a fresh directory: `files` are written first, `bin` holds fake commands. */
function runStep(script: string, { files = {}, env = {}, cwd = '' }: { files?: Record<string, string>; env?: Record<string, string>; cwd?: string } = {}) {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'weekly-'));
  try {
    for (const [rel, body] of Object.entries(files)) {
      fs.mkdirSync(path.dirname(path.join(tmp, rel)), { recursive: true });
      fs.writeFileSync(path.join(tmp, rel), body);
      if (rel.startsWith('bin/')) fs.chmodSync(path.join(tmp, rel), 0o755);
    }
    const work = path.join(tmp, cwd);
    fs.mkdirSync(work, { recursive: true });
    fs.writeFileSync(path.join(tmp, 'step.sh'), script);
    const summary = path.join(tmp, 'summary.md');
    const output = path.join(tmp, 'output.txt');
    const r = spawnSync(bash!, [fwd(path.join(tmp, 'step.sh'))], {
      cwd: work,
      encoding: 'utf8',
      env: { ...process.env, PATH: `${path.join(tmp, 'bin')}${path.delimiter}${process.env.PATH}`, GITHUB_STEP_SUMMARY: fwd(summary), GITHUB_OUTPUT: fwd(output), ...env },
    });
    const read = (f: string) => (fs.existsSync(f) ? fs.readFileSync(f, 'utf8') : '');
    return { code: r.status, out: `${r.stdout}${r.stderr}`, summary: read(summary), output: read(output) };
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

describe('filex against the real servers, every week (realenv.yml)', () => {
  it.runIf(has('realenv.yml'))('runs every week and by hand, on the nightly branch when scheduled, with the images pulled', () => {
    const text = wf('realenv.yml');
    expect(on(text)).toMatch(/schedule:\s*\n\s+- cron: "[0-9]+ [0-9]+ \* \* 0"/);
    expect(on(text)).toMatch(/workflow_dispatch:/);
    expect(on(text)).toMatch(/\npermissions:\s*\n\s+contents: read\s*\n/);
    const realenv = job(text, 'realenv');
    expect(realenv).toMatch(/runs-on: ubuntu-24\.04/);
    expect(realenv).not.toMatch(/secrets\.|environment:/);
    expect(realenv).toMatch(/ref: \$\{\{ github\.event_name == 'schedule' && 'nightly' \|\| '' \}\}/);
    expect(realenv).toMatch(/persist-credentials: false/);
    expect(stepNamed(realenv, 'The stages').text).toMatch(/REALENV_PULL: "1"/);
    // The Document Server ci.yml's office specs run against, never `latest`:
    // the first run's office-s2 went red on a `latest` that enforced its JWT
    // with JWT_ENABLED=false (run 44263716035).
    const ds = /image="\$\{DS_IMAGE:-([^}]+)\}"/.exec(fs.readFileSync(path.join(DIR!, 'scripts', 'ci-docserver.sh'), 'utf8'))?.[1];
    expect(ds).toMatch(/^onlyoffice\/documentserver:\d/);
    expect(stepNamed(realenv, 'The stages').text).toContain(`REALENV_OO_IMAGE: ${ds}`);
    expect(scriptOf(stepNamed(realenv, 'Packages, admin UI, embed, server binary').text)).toMatch(/pnpm run build:all/);
    // An input reaches the script through env alone.
    for (const l of text.split('\n').filter((x) => /\$\{\{[^}]*\binputs\./.test(x))) expect(l).toMatch(/^\s+[A-Z_]+: \$\{\{ inputs\.[a-z_]+ \}\}\s*$/);
  });

  it.runIf(has('realenv.yml') && !!bash)('reads a skipped stage as red: a week that ran no stage is not a week they passed', () => {
    const script = scriptOf(stepNamed(job(wf('realenv.yml'), 'realenv'), 'The stages').text);
    const stub = (lines: string[], rc: number) => ({ 'e2e/realenv/run.sh': `${lines.map((l) => `echo "${l}"`).join('\n')}\nexit ${rc}\n` });
    const green = runStep(script, { files: stub(['12:00:00 realenv: passed:  tls sso office'], 0) });
    expect(green.code, green.out).toBe(0);
    expect(green.summary).toContain('### Real servers: tls sso office passed');
    const skipped = runStep(script, { files: stub(['12:00:00 realenv: SKIPPED office: image onlyoffice/documentserver:latest is not on this machine', '12:00:00 realenv: passed:  tls sso'], 0) });
    expect(skipped.code).toBe(1);
    expect(skipped.out).toMatch(/::error::1 stage\(s\) were skipped: not a pass here/);
    expect(runStep(script, { files: stub(['12:00:00 realenv: FAILED:  sso'], 1) }).code).toBe(1);
    // The stages asked for by hand reach run.sh as words.
    const asked = runStep(script, { files: { 'e2e/realenv/run.sh': 'echo "args: $*"\necho "x realenv: passed:  $*"\n' }, env: { STAGES: 'tls sso' } });
    expect(asked.out).toContain('args: tls sso');
  });
});

describe('the live S3 tests, every week (s3-live.yml)', () => {
  const names = ['FILEX_TEST_S3_BUCKET', 'FILEX_TEST_S3_ACCESS_KEY', 'FILEX_TEST_S3_SECRET_KEY', 'FILEX_TEST_S3_REGION', 'FILEX_TEST_S3_ENDPOINT', 'FILEX_TEST_S3_PATH_STYLE'];

  it.runIf(has('s3-live.yml'))("reads the s3-live environment's secrets, and nothing runs without them", () => {
    const text = wf('s3-live.yml');
    expect(on(text)).toMatch(/schedule:\s*\n\s+- cron: "[0-9]+ [0-9]+ \* \* 0"/);
    expect(on(text)).toMatch(/workflow_dispatch:/);
    const s3 = job(text, 's3-live');
    expect(s3).toMatch(/\n {4}environment: s3-live\n/);
    for (const n of names) expect(s3).toContain(`${n}: \${{ secrets.${n} }}`);
    // Every step after the check runs only when the secrets are there.
    const steps = stepsOf(s3);
    expect(steps[0].name).toBe('The credentials');
    for (const s of steps.slice(1)) expect(s.cond, s.name).toBe("steps.creds.outputs.run == 'true'");
    expect(s3).toMatch(/ref: \$\{\{ github\.event_name == 'schedule' && 'nightly' \|\| '' \}\}/);
    expect(s3).toMatch(/INTEGRATION: "1"/);
    // The tests the build host's job ran, by name.
    expect(on(text)).toContain('TESTS: TestLiveProviderConformance TestInit_Integration TestB2_OverRealS3 TestRenameOnS3_TheReportersSequence');
    const chainJob = fs.readFileSync(path.join(REPO, 'scripts', 'chain', 'job', 's3-live.sh'), 'utf8');
    expect(chainJob).toContain("TESTS='TestLiveProviderConformance TestInit_Integration TestB2_OverRealS3 TestRenameOnS3_TheReportersSequence'");
  });

  it.runIf(has('s3-live.yml') && !!bash)('says "skipped: no S3 credentials" without a secret and stays green; runs with the four it needs', () => {
    const script = scriptOf(stepNamed(job(wf('s3-live.yml'), 's3-live'), 'The credentials').text);
    const none = runStep(script, { env: Object.fromEntries(names.map((n) => [n, ''])) });
    expect(none.code, none.out).toBe(0);
    expect(none.output).toBe('run=false\n');
    expect(none.out).toMatch(/::notice title=Live S3::skipped: no S3 credentials \(the s3-live environment has no secret: FILEX_TEST_S3_BUCKET FILEX_TEST_S3_ACCESS_KEY FILEX_TEST_S3_SECRET_KEY FILEX_TEST_S3_REGION\)/);
    expect(none.summary).toMatch(/Live S3: skipped, no S3 credentials/);
    const set = { FILEX_TEST_S3_BUCKET: 'b', FILEX_TEST_S3_ACCESS_KEY: 'k', FILEX_TEST_S3_SECRET_KEY: 'not-a-real-secret', FILEX_TEST_S3_REGION: 'eu-central-003', FILEX_TEST_S3_ENDPOINT: '', FILEX_TEST_S3_PATH_STYLE: '' };
    const ok = runStep(script, { env: set });
    expect(ok.output).toBe('run=true\n');
    expect(ok.out).not.toContain('not-a-real-secret');
    // The region too (lesson #927).
    expect(runStep(script, { env: { ...set, FILEX_TEST_S3_REGION: '' } }).output).toBe('run=false\n');
  });

  it.runIf(has('s3-live.yml') && !!bash)('reads a live test that only skipped as red', () => {
    const text = wf('s3-live.yml');
    const script = scriptOf(stepNamed(job(text, 's3-live'), 'The live tests').text);
    const tests = /\n {2}TESTS: (.+)\n/.exec(text)![1];
    const go = (lines: string[], rc = 0) => ({ 'bin/go': `#!/usr/bin/env bash\n${lines.map((l) => `echo "${l}"`).join('\n')}\nexit ${rc}\n` });
    const pass = tests.split(' ').map((t) => `--- PASS: ${t} (1.00s)`);
    const env = { TESTS: tests, PKGS: './x' };
    expect(runStep(script, { files: go(pass), env, cwd: 'backend' }).code).toBe(0);
    const skip = runStep(script, { files: go([...pass.slice(1), '--- SKIP: TestLiveProviderConformance (0.00s)']), env, cwd: 'backend' });
    expect(skip.code).toBe(1);
    expect(skip.out).toMatch(/::error::did not pass: TestLiveProviderConformance - a skip is not a pass here/);
    expect(runStep(script, { files: go(['--- FAIL: TestInit_Integration (1.00s)'], 1), env, cwd: 'backend' }).code).toBe(1);
  });
});

describe('every screenshot scene, every week (shots.yml)', () => {
  it.runIf(has('shots.yml'))('takes every scene of the nightly branch every week and by hand, and publishes nothing', () => {
    const text = wf('shots.yml');
    expect(on(text)).toMatch(/push:\s*\n\s+tags: \["v\*"\]/);
    expect(on(text)).toMatch(/schedule:\s*\n\s+- cron: "[0-9]+ [0-9]+ \* \* 0"/);
    expect(on(text)).toMatch(/nightly:\s*\n\s+description:[^\n]*\n\s+type: boolean\s*\n\s+default: false/);
    const shots = job(text, 'shots');
    expect(shots).toMatch(/runs-on: ubuntu-24\.04/);
    expect(shots).toMatch(/ref: \$\{\{ \(github\.event_name == 'schedule' \|\| inputs\.nightly\) && 'nightly' \|\| '' \}\}/);
    const take = stepNamed(shots, 'Build, verify the embedded UI, take every screenshot');
    expect(take.text).toMatch(/NIGHTLY: \$\{\{ \(github\.event_name == 'schedule' \|\| inputs\.nightly\) && 'true' \|\| 'false' \}\}/);
    expect(scriptOf(take.text)).toMatch(/if \[ "\$NIGHTLY" = true \]; then\n\s+pnpm shots --all --keep-going\nelse\n\s+pnpm shots\nfi/);
    // The published set is the build host's chain's to take and accept.
    expect(text).not.toMatch(/shots-site\.mjs|shots-upload|secrets\./);
  });
});

describe('the patch and its place in the guide', () => {
  it('is the 22nd workflow patch, on top of the nightly move', () => {
    const patch = fs.readFileSync(path.join(REPO, 'packaging', 'ci', 'weekly-checks.patch'), 'utf8');
    expect(patch.match(/^diff --git a\/\S+/gm)).toEqual(['diff --git a/.github/workflows/realenv.yml', 'diff --git a/.github/workflows/s3-live.yml', 'diff --git a/.github/workflows/shots.yml']);
    const contributing = fs.readFileSync(path.join(REPO, 'docs', 'CONTRIBUTING.md'), 'utf8');
    expect(contributing).toMatch(/\n22\. `weekly-checks\.patch` \(#364, 0\.62\), on top of 21:/);
  });
});
