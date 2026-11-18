// The weekly checks on GitHub (#364): what the build host's nightly chain ran
// on its own until 2026-11-18 and GitHub's matrix does not - filex against
// real ACME, SSO and office servers (.github/workflows/realenv.yml), the Go
// tests against a real S3 server (s3-live.yml, an S3 gateway of the run's
// own) and every screenshot scene (shots.yml's weekly nightly mode) - each
// once a week on the `nightly` branch's commit, and by hand.
//
// ⚠ Why (the owner, 2026-11-18): the night left the build host for GitHub,
// and these three were left with no home but a person's run. What these hold
// is what keeps a week from reading green when nothing ran: a skipped realenv
// stage is red, and a live S3 test that only skipped is red. The live S3
// tests get no provider's token (the owner, 2026-11-18, #365): s3-live.yml
// starts the Versity S3 Gateway ci.yml's S3 line starts (ci-s3.sh live) and
// reads no secret, so there is no "no credentials" case left to skip on.
// Nothing here publishes a picture.
//
// The workflows live in the PUBLIC checkout only; the change reaches it as
// packaging/ci/weekly-checks.patch, and the release tool names these
// workflow tests in its guards.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { DIR, REPO, bash, job, scriptOf, stepNamed, stepsOf, stepUsing } from '../helpers/releaseWorkflow';

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

/**
 * Runs a step's script in a fresh directory: `files` are written first, `bin`
 * holds fake commands. BASH_ENV and ENV stay out of its environment: a CI
 * that sets them (CircleCI) would put its own PATH ahead of the fakes in every
 * bash the script opens (lesson #1799), and PATH is set once, whatever its
 * case (Windows says Path).
 */
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
    const ghEnv = path.join(tmp, 'github-env.txt');
    const base = Object.fromEntries(Object.entries(process.env).filter(([k]) => !/^(path|bash_env|env)$/i.test(k)));
    const r = spawnSync(bash!, [fwd(path.join(tmp, 'step.sh'))], {
      cwd: work,
      encoding: 'utf8',
      env: { ...base, PATH: `${path.join(tmp, 'bin')}${path.delimiter}${process.env.PATH}`, GITHUB_STEP_SUMMARY: fwd(summary), GITHUB_OUTPUT: fwd(output), GITHUB_ENV: fwd(ghEnv), ...env },
    });
    const read = (f: string) => (fs.existsSync(f) ? fs.readFileSync(f, 'utf8') : '');
    return { code: r.status, out: `${r.stdout}${r.stderr}`, summary: read(summary), output: read(output), ghEnv: read(ghEnv) };
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
  const lives3 = fs.readFileSync(path.join(REPO, 'backend', 'internal', 'testutil', 'lives3', 'lives3.go'), 'utf8');
  /** The variables lives3 reads, by the names a person sets. */
  const names = [...lives3.matchAll(/\bEnv\w+\s*=\s*"(FILEX_TEST_S3_[A-Z_]+)"/g)].map((m) => m[1]);
  const ciS3 = () => fs.readFileSync(path.join(DIR!, 'scripts', 'ci-s3.sh'), 'utf8');

  it.runIf(has('s3-live.yml'))('runs every week and by hand against an S3 gateway of its own, and reads no secret', () => {
    const text = wf('s3-live.yml');
    expect(on(text)).toMatch(/schedule:\s*\n\s+- cron: "[0-9]+ [0-9]+ \* \* 0"/);
    expect(on(text)).toMatch(/workflow_dispatch:/);
    expect(on(text)).toMatch(/\npermissions:\s*\n\s+contents: read\s*\n/);
    // The owner, 2026-11-18 (#365): no provider's account or token for a
    // test. No environment, no secret, and so no run that skips for want of one.
    expect(text).not.toMatch(/secrets\.|environment:|no S3 credentials|FILEX_TEST_S3_/);
    const s3 = job(text, 's3-live');
    const steps = stepsOf(s3);
    for (const st of steps) expect(st.cond, `${st.name} runs on every run`).toBe('');
    const at = (name: string) => steps.findIndex((st) => st.name === name);
    expect(stepNamed(s3, 'An S3 gateway for the live tests').text).toMatch(/\n\s+run: bash \.github\/workflows\/scripts\/ci-s3\.sh live(\n|$)/);
    expect(at('An S3 gateway for the live tests')).toBeLessThan(at('The live tests'));
    // ci-s3.sh reads the gateway's image from the e2e harness, with node.
    expect(stepUsing(s3, 'actions/setup-node')).toBeTruthy();
    expect(steps.findIndex((st) => st.name.startsWith('actions/setup-node@'))).toBeLessThan(at('An S3 gateway for the live tests'));
    expect(s3).toMatch(/ref: \$\{\{ github\.event_name == 'schedule' && 'nightly' \|\| '' \}\}/);
    expect(s3).toMatch(/persist-credentials: false/);
    expect(stepNamed(s3, 'The live tests').text).toMatch(/INTEGRATION: "1"/);
    expect(on(text)).toContain('TESTS: TestLiveProviderConformance TestInit_Integration TestB2_OverRealS3 TestRenameOnS3_TheReportersSequence');
    // One gateway, ci.yml's: the S3 line starts it the same way, without the argument.
    expect(wf('ci.yml')).toMatch(/run: bash \.github\/workflows\/scripts\/ci-s3\.sh\n/);
  });

  it.runIf(has('s3-live.yml'))('names four tests that exist, in the packages it runs, each reading its server through lives3', () => {
    const text = wf('s3-live.yml');
    const tests = /\n {2}TESTS: (.+)\n/.exec(text)![1].split(' ');
    const pkgs = /\n {2}PKGS: (.+)\n/.exec(text)![1].split(' ');
    expect(tests).toEqual(['TestLiveProviderConformance', 'TestInit_Integration', 'TestB2_OverRealS3', 'TestRenameOnS3_TheReportersSequence']);
    const sources = pkgs
      .map((p) => path.join(REPO, 'backend', p))
      .flatMap((dir) => fs.readdirSync(dir).filter((f) => f.endsWith('_test.go')).map((f) => fs.readFileSync(path.join(dir, f), 'utf8')))
      .join('\n');
    for (const t of tests) expect(sources, t).toContain(`func ${t}(t *testing.T)`);
    // Each reads its server through lives3, region included (lesson #927).
    const backend = path.join(REPO, 'backend', 'internal');
    for (const f of ['storage/drivers/s3/live_conformance_test.go', 'storage/drivers/s3/s3_test.go', 'usage/b2_test.go', 'api/handlers/rename_s3_live_test.go']) {
      expect(fs.readFileSync(path.join(backend, f), 'utf8'), f).toContain('lives3.Config(t, ');
    }
    expect(names).toEqual(['FILEX_TEST_S3_ENDPOINT', 'FILEX_TEST_S3_BUCKET', 'FILEX_TEST_S3_ACCESS_KEY', 'FILEX_TEST_S3_SECRET_KEY', 'FILEX_TEST_S3_REGION', 'FILEX_TEST_S3_PATH_STYLE']);
  });

  it.runIf(has('s3-live.yml') && !!bash)("ci-s3.sh live names the gateway and a bucket of its own to the live tests; without an argument it is ci.yml's S3 line, as before", () => {
    const fakes = {
      'ci-s3.sh': ciS3(),
      'bin/node': '#!/usr/bin/env bash\necho versity/versitygw:vTEST\n',
      'bin/curl': '#!/usr/bin/env bash\necho 200\n',
      'bin/docker': '#!/usr/bin/env bash\necho "docker $*" >&2\n',
      'bin/sleep': '#!/usr/bin/env bash\nexit 0\n',
    };
    // The fakes go first inside the step, and the step refuses to go on with
    // any other docker: Git for Windows' bash.exe puts $HOME/bin ahead of the
    // PATH it is given, and the first run of this test reached the real Docker
    // through it (it asked Docker Hub for versity/versitygw:vTEST).
    const step = (args: string) =>
      `PATH="$PWD/bin:$PATH"\n[ "$(command -v docker)" = "$PWD/bin/docker" ] || { echo "not the fake docker: $(command -v docker)"; exit 99; }\nbash ci-s3.sh${args}\n`;
    const live = runStep(step(' live'), { files: fakes });
    expect(live.code, live.out).toBe(0);
    expect(live.out).toContain('docker run -d --rm --name filex-ci-s3 -p 127.0.0.1:7070:7070 -v /data');
    expect(live.out).toContain('versity/versitygw:vTEST');
    expect(live.out).toContain('docker exec filex-ci-s3 mkdir -p /data/filex-live');
    expect(live.out).toContain('S3 gateway up: versity/versitygw:vTEST on 127.0.0.1:7070, bucket filex-live');
    const vars = Object.fromEntries(live.ghEnv.trim().split('\n').map((l) => [l.slice(0, l.indexOf('=')), l.slice(l.indexOf('=') + 1)]));
    // Every variable lives3 reads, and nothing else: the gateway, path style,
    // signed for us-east-1 (lesson #927), with the key the container was started with.
    expect(Object.keys(vars).sort()).toEqual([...names].sort());
    expect(vars).toEqual({
      FILEX_TEST_S3_BUCKET: 'filex-live',
      FILEX_TEST_S3_ACCESS_KEY: 'filexe2e',
      FILEX_TEST_S3_SECRET_KEY: 'filexe2esecret',
      FILEX_TEST_S3_ENDPOINT: 'http://127.0.0.1:7070',
      FILEX_TEST_S3_REGION: 'us-east-1',
      FILEX_TEST_S3_PATH_STYLE: '1',
    });
    expect(live.out).toContain(`ROOT_ACCESS_KEY=${vars.FILEX_TEST_S3_ACCESS_KEY} -e ROOT_SECRET_KEY=${vars.FILEX_TEST_S3_SECRET_KEY}`);
    // Without an argument: ci.yml's multi-storage spec, the same seven lines as before #365.
    const e2e = runStep(step(''), { files: fakes });
    expect(e2e.code, e2e.out).toBe(0);
    expect(e2e.out).toContain('docker exec filex-ci-s3 mkdir -p /data/filex-e2e-multi');
    expect(e2e.ghEnv).toBe(
      'E2E_S3_BUCKET=filex-e2e-multi\nE2E_S3_ACCESS_KEY=filexe2e\nE2E_S3_SECRET_KEY=filexe2esecret\nE2E_S3_ENDPOINT=http://127.0.0.1:7070\nE2E_S3_PATH_STYLE=1\nE2E_S3_REGION=us-east-1\nE2E_S3_PREFIX=e2e-multi/\n',
    );
    // Anything else starts nothing.
    const other = runStep(step(' lieve'), { files: fakes });
    expect(other.code).toBe(2);
    expect(other.out).toContain('usage: ci-s3.sh [live]');
    expect(other.out).not.toContain('docker ');
    expect(other.ghEnv).toBe('');
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
    expect(patch.match(/^diff --git a\/\S+/gm)).toEqual([
      'diff --git a/.github/workflows/realenv.yml',
      'diff --git a/.github/workflows/s3-live.yml',
      'diff --git a/.github/workflows/scripts/ci-s3.sh',
      'diff --git a/.github/workflows/shots.yml',
    ]);
    // #365: s3-live.yml as it is now - the gateway of ci-s3.sh live, no environment, no secret.
    const s3 = patch.slice(patch.indexOf('diff --git a/.github/workflows/s3-live.yml'), patch.indexOf('diff --git a/.github/workflows/scripts/ci-s3.sh'));
    expect(s3).toContain('+        run: bash .github/workflows/scripts/ci-s3.sh live\n');
    expect(s3).not.toMatch(/^\+[^#\n]*(?:secrets\.|environment:)/m);
    const contributing = fs.readFileSync(path.join(REPO, 'docs', 'CONTRIBUTING.md'), 'utf8');
    expect(contributing).toMatch(/\n22\. `weekly-checks\.patch` \(#364, 0\.62\), on top of 21:/);
  });
});
