// The test chain is ci.yml's matrix: a job per part, side by side (#173).
//
// ⚠⚠ Why (#165, measured 0.45-0.52): the heavy suites ran one after another
// on one build host before every release - 4 h 42 min on 0.51, the Go suite
// five times and vitest eight times over one release - and that, not the
// code, was most of a release day. The push of main now runs every part on
// GitHub's runners at once, and the release reads that run part by part.
//
// What must hold, each one careless edit away:
//   - every job is named the way scripts/ci-parts.mjs names it: the release
//     gate (scripts/release/stages.mjs) and a tag run's `verify` read the run
//     by those names, and a part renamed here is a part they wait for in vain;
//   - the Go parts are scripts/test-shards.json's shards (the one list every
//     runner takes, #171), beside every service the build host gives them;
//   - Playwright is split per engine by `--shard`, the Document Server specs
//     run again with one, and the build host's own browser lines are here;
//   - one verdict job, green only when every part of the profile was;
//   - a push of main is never cancelled: each commit keeps its verdict.
//
// The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts): read from `<repo>/.github/workflows` or
// FILEX_WORKFLOWS_DIR, and the release tool names these titles in its
// workflow guards (scripts/release/plan.mjs, WORKFLOW_GUARDS), so a skip there
// is red. The workflow reaches the public checkout as its own commit, landed
// with the export that brings these guards (packaging/ci/ci-full-matrix.patch).
// The tests of scripts/ci-parts.mjs alone run everywhere.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { APP_LOCATIONS } from '../../../e2e/helpers/app-locations.mjs';
import { DS_GREP, DS_SPECS, E2E_ENGINES, E2E_SHARDS, EXTRA, FIXED, ciMatrix, ciParts, completeName, githubOutput, runsIn } from '../../../scripts/ci-parts.mjs';
import { findBash, slash } from '../../../scripts/release/engine.mjs';

const REPO = path.resolve(__dirname, '../../..');
const DIR = [process.env.FILEX_WORKFLOWS_DIR, path.join(REPO, '.github', 'workflows')].find((d) => d && fs.existsSync(path.join(d, 'ci.yml')));
const bash = findBash();
const hasJq = !spawnSync('jq', ['--version'], { encoding: 'utf8' }).error;

/** The matrix jobs of ci.yml, and the plan output each one reads its rows from. */
const MATRIX_JOBS: Record<string, string> = { go: 'go', race: 'race', frontend: 'frontend', playwright: 'playwright', 'playwright-ds': 'ds', 'playwright-extra': 'extra' };

/** ci.yml without comment-only lines, LF. */
const ciCode = () =>
  fs
    .readFileSync(path.join(DIR!, 'ci.yml'), 'utf8')
    .split(/\r?\n/)
    .filter((l) => !/^\s*#/.test(l))
    .join('\n');

/** The lines of one job, up to the next job. */
function job(text: string, name: string): string {
  const lines = text.split('\n');
  const start = lines.findIndex((l) => l === `  ${name}:`);
  expect(start, `ci.yml job "${name}"`).toBeGreaterThanOrEqual(0);
  const next = lines.findIndex((l, i) => i > start && /^ {2}[A-Za-z0-9_-]+:\s*$/.test(l));
  return lines.slice(start, next < 0 ? undefined : next).join('\n');
}

function jobNames(text: string): string[] {
  const lines = text.split('\n');
  const start = lines.findIndex((l) => l === 'jobs:');
  return lines.slice(start + 1).map((l) => /^ {2}([A-Za-z0-9_-]+):\s*$/.exec(l)?.[1]).filter((n): n is string => !!n);
}

const nameOf = (block: string) => /\n {4}name:\s*(.+)/.exec(block)?.[1].trim() ?? '';
const ifOf = (block: string) => /\n {4}if:\s*(.+)/.exec(block)?.[1].trim() ?? '';
function needsOf(block: string): string[] {
  const inline = /\n {4}needs:\s*(.+)/.exec(block)?.[1] ?? '';
  return inline.replace(/[[\]]/g, '').split(',').map((s) => s.trim()).filter(Boolean);
}

/** The build host's browser list (scripts/chain/lists/e2e.txt). */
const chainList = () => fs.readFileSync(path.join(REPO, 'scripts', 'chain', 'lists', 'e2e.txt'), 'utf8');
/** The --grep of one line of that list, by its name. */
const chainGrep = (name: string) => new RegExp(`^${name}\\s+\\S+\\s+\\S+\\s+\\d\\s+--grep\\s+(\\S+)`, 'm').exec(chainList())?.[1];

describe('scripts/ci-parts.mjs: the parts of the matrix', () => {
  it('names every part once, and the pull request profile is the full one less -race, the Document Server and the extra lines', () => {
    const full = ciParts(REPO, 'full');
    expect(new Set(full).size, 'a part named twice').toBe(full.length);
    expect(full.at(-1)).toBe(completeName('full'));
    expect(completeName('full')).toBe('All tests (full)');
    const pr = ciParts(REPO, 'pr');
    expect(pr.at(-1)).toBe('All tests (pr)');
    const m = ciMatrix(REPO, 'full');
    const left = [...m.race.include, ...m.ds.include, ...m.extra.include].map((r) => r.name);
    for (const n of left) expect(pr, n).not.toContain(n);
    const onlyFirstEngine = m.playwright.include.filter((r) => r.engine !== E2E_ENGINES[0]).map((r) => r.name);
    for (const n of onlyFirstEngine) expect(pr, n).not.toContain(n);
    for (const n of full.filter((x) => !left.includes(x) && !onlyFirstEngine.includes(x) && x !== completeName('full'))) expect(pr, n).toContain(n);
    expect(['race', 'ds', 'extra'].map((k) => runsIn(k, 'pr'))).toEqual([false, false, false]);
    expect(() => ciParts(REPO, 'nightly')).toThrow('profile "nightly"');
  });

  it('cuts the Go suite as scripts/test-shards.json does, and Playwright into parts of every engine', () => {
    const cfg = JSON.parse(fs.readFileSync(path.join(REPO, 'scripts', 'test-shards.json'), 'utf8'));
    const m = ciMatrix(REPO, 'full');
    expect(m.go.include.map((r: { shard: string }) => r.shard)).toEqual(cfg.profiles.go.map((s: { name: string }) => s.name));
    expect(m.race.include.map((r: { shard: string }) => r.shard)).toEqual(cfg.profiles.race.map((s: { name: string }) => s.name));
    expect(m.playwright.include).toHaveLength(E2E_ENGINES.length * E2E_SHARDS);
    for (const engine of E2E_ENGINES) {
      for (let i = 1; i <= E2E_SHARDS; i++) expect(m.playwright.include).toContainEqual(expect.objectContaining({ engine, shard: `${i}/${E2E_SHARDS}` }));
    }
    expect(m.ds.include.map((r: { engine: string }) => r.engine)).toEqual(E2E_ENGINES);
    expect(ciMatrix(REPO, 'pr').playwright.include.map((r: { engine: string }) => r.engine)).toEqual(Array(E2E_SHARDS).fill(E2E_ENGINES[0]));
  });

  it('runs again with a Document Server every spec the build host runs without one, and the build host\'s own lines', () => {
    const nods = chainGrep('nods');
    expect(nods, 'scripts/chain/lists/e2e.txt has no nods line').toBeTruthy();
    for (const spec of /^\(([^)]*)\)/.exec(nods!)![1].split('|')) expect(DS_SPECS, spec).toContain(spec);
    const specs = fs.readdirSync(path.join(REPO, 'e2e', 'tests'));
    for (const spec of DS_SPECS) expect(specs.some((f) => f.startsWith(`${spec}.spec.`)), `${spec} is no spec (a rename greps nothing)`).toBe(true);
    expect(DS_GREP).toBe(`(${DS_SPECS.join('|')})\\.spec`);
    expect(EXTRA.find((x) => x.kind === 'nopub')?.grep).toBe(chainGrep('nopub'));
    expect(EXTRA.find((x) => x.kind === 's3')?.grep).toBe(chainGrep('s3'));
    expect(Object.fromEntries(EXTRA.map((x) => [x.kind, x.flag]))).toEqual({ nopub: '--no-public-url', s3: '--s3' });
  });

  it('writes one key=value line per output, every matrix a non-empty include (GitHub fails an empty one)', () => {
    for (const profile of ['full', 'pr']) {
      const lines = githubOutput(REPO, profile);
      const keys = lines.map((l) => l.slice(0, l.indexOf('=')));
      expect(keys).toEqual(['profile', 'go', 'race', 'frontend', 'playwright', 'ds', 'extra', 'ds_grep', 'e2e_shards', 'e2e_engines']);
      for (const l of lines) expect(l, 'a value on one line').not.toMatch(/\n/);
      for (const key of Object.values(MATRIX_JOBS)) {
        const v = JSON.parse(lines.find((l) => l.startsWith(`${key}=`))!.slice(key.length + 1));
        expect(v.include.length, `${profile} ${key}`).toBeGreaterThan(0);
        for (const row of v.include) expect(typeof row.name, `${key}: a row without its job's name`).toBe('string');
      }
    }
  });
});

describe('ci.yml: the full matrix', () => {
  it.runIf(!DIR)('has no workflows to read here (they live in the public checkout)', () => {
    // Not a pass: the private tree has no .github/workflows. The local release
    // run sets FILEX_WORKFLOWS_DIR so the cases below run there too.
    expect(DIR).toBeUndefined();
  });

  it.runIf(!!DIR)('names every part as scripts/ci-parts.mjs does: the fixed jobs, and every matrix job by its row', () => {
    const ci = ciCode();
    for (const [key, name] of Object.entries(FIXED)) expect(nameOf(job(ci, key)), key).toBe(name);
    for (const [key, out] of Object.entries(MATRIX_JOBS)) {
      const block = job(ci, key);
      expect(nameOf(block), `${key} is named by its row`).toBe('${{ matrix.name }}');
      expect(block, key).toContain(`matrix: \${{ fromJSON(needs.plan.outputs.${out}) }}`);
    }
    expect(nameOf(job(ci, 'all'))).toBe('All tests (${{ needs.plan.outputs.profile }})');
    // A job ci-parts does not know is a part the release never reads.
    expect(jobNames(ci).sort()).toEqual([...Object.keys(FIXED), ...Object.keys(MATRIX_JOBS), 'all'].sort());
    const plan = job(ci, 'plan');
    expect(plan).toContain('node scripts/ci-parts.mjs github-output --profile "$PROFILE"');
    for (const line of githubOutput(REPO, 'full')) {
      const key = line.slice(0, line.indexOf('='));
      expect(plan, `the plan job hands on ${key}`).toContain(`${key}: \${{ steps.m.outputs.${key} }}`);
    }
  });

  it.runIf(!!DIR)('the Go parts are the shards of scripts/test-shards.json, plain beside every service and under -race without one', () => {
    const ci = ciCode();
    const go = job(ci, 'go');
    expect(go).toContain('node scripts/test-shards.mjs go --profile go --shard "$SHARD" --format args');
    expect(go).toMatch(/go test -count=1 -timeout 30m "\$\{A\[@\]\}"/);
    for (const svc of ['postgres', 'mysql', 'redis', 'samba']) expect(go, `the Go parts run beside ${svc}`).toMatch(new RegExp(`\\n {6}${svc}:\\n`));
    for (const v of ['FILEX_TEST_PG_DSN', 'FILEX_TEST_MYSQL_DSN', 'FILEX_TEST_REDIS_URL', 'FILEX_SMB_TEST']) expect(go, v).toMatch(new RegExp(`\\n {6}${v}: \\S`));
    const race = job(ci, 'race');
    expect(race).toContain('node scripts/test-shards.mjs go --profile race --shard "$SHARD" --format args');
    expect(race).toMatch(/go test -race -count=1 -timeout 60m -v "\$\{A\[@\]\}"/);
    expect(race, 'the engines are the Go parts\' job').not.toMatch(/FILEX_TEST_(PG|MYSQL)_DSN|\n {4}services:/);
    expect(race, 'a shard that ran no test is red').toMatch(/ran no test/);
    for (const key of ['go', 'race']) {
      const b = job(ci, key);
      expect(b, key).toContain('bash scripts/build-wasm-fixture.sh');
      expect(b, key).toContain('FILEX_REQUIRE_WASM_FIXTURE: "1"');
      expect(b.indexOf('build-wasm-fixture.sh'), `${key} builds the fixture before it tests`).toBeLessThan(b.indexOf('go test'));
    }
    const vet = job(ci, 'vet');
    expect(vet).toMatch(/run: go vet \.\/\.\.\./);
    expect(vet).toMatch(/run: go build \.\/\.\.\./);
    const shards = job(ci, 'shards');
    expect(shards).toContain('node scripts/test-shards.mjs check');
    expect(shards).toContain('node scripts/test-shards.mjs e2e-check --shards "$E2E_SHARDS" --browsers "$E2E_ENGINES"');
  });

  // v0.53.0's first full matrix (run 37596680800, lesson #1236): GitHub runs
  // a `run:` block as `bash -e`, and `set -uo pipefail` left -e on. A green
  // shard's grep for failure lines found none and ended the step red (handlers-5:
  // "ok 735s", exit 1); a red go test ended it before rc was read. The step runs
  // here as GitHub runs it, with a stand-in `go`.
  it.runIf(!!DIR && !!bash)('the -race step under bash -e: a green shard ends 0, a red one with its own code and its summary line, an empty one red', () => {
    const lines = job(ciCode(), 'race').split('\n');
    const named = lines.findIndex((l) => l.trim() === '- name: The shard, under -race');
    expect(named, 'the -race step').toBeGreaterThanOrEqual(0);
    const at = lines.findIndex((l, i) => i > named && /^\s+run: \|\s*$/.test(l));
    const body = lines.slice(at + 1);
    const indent = body[0].length - body[0].trimStart().length;
    const end = body.findIndex((l) => !!l.trim() && l.length - l.trimStart().length < indent);
    const step = body.slice(0, end < 0 ? undefined : end).map((l) => l.slice(indent)).join('\n');
    const fakeGo = [
      'go() {',
      '  case "${FAKE_GO:-}" in',
      '    red) echo "--- FAIL: TestRace (0.01s)"; echo FAIL; return 1 ;;',
      '    none) echo "ok    example.com/x    0.01s [no tests to run]" ;;',
      '    *) echo "--- PASS: TestRace (0.01s)"; echo PASS ;;',
      '  esac',
      '}',
    ].join('\n');
    const shard = JSON.parse(fs.readFileSync(path.join(REPO, 'scripts', 'test-shards.json'), 'utf8')).profiles.race[0].name as string;
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'ci-race-'));
    try {
      fs.writeFileSync(path.join(tmp, 'race.sh'), `${fakeGo}\n${step}\n`);
      const summary = path.join(tmp, 'summary.md');
      const run = (fake: string) => {
        fs.rmSync(summary, { force: true });
        const r = spawnSync(bash!, ['--noprofile', '--norc', '-e', path.join(tmp, 'race.sh')], {
          cwd: REPO,
          encoding: 'utf8',
          env: { ...process.env, FAKE_GO: fake, SHARD: shard, RUNNER_TEMP: slash(tmp), GITHUB_STEP_SUMMARY: slash(summary) },
        });
        return { ...r, summary: fs.existsSync(summary) ? fs.readFileSync(summary, 'utf8') : '' };
      };
      const green = run('green');
      expect(green.status, `${green.stdout}\n${green.stderr}`).toBe(0);
      expect(green.summary).toContain(`### -race ${shard}: 1 tests, exit 0`);
      const red = run('red');
      expect(red.status).toBe(1);
      expect(red.stdout).toContain('--- FAIL: TestRace');
      expect(red.summary, 'a red shard still writes its line').toContain(`### -race ${shard}: 1 tests, exit 1`);
      const none = run('none');
      expect(none.status).toBe(1);
      expect(none.stdout).toContain(`::error::shard ${shard} ran no test`);
    } finally {
      fs.rmSync(tmp, { recursive: true, force: true });
    }
  });

  it.runIf(!!DIR)('splits Playwright into parts of each engine, and runs the Document Server specs again with one, on every engine', () => {
    const ci = ciCode();
    const pw = job(ci, 'playwright');
    expect(pw).toContain('E2E_BROWSERS: ${{ matrix.engine }}');
    expect(pw).toContain('node e2e/run.mjs local --binary bin/filex --shard "$SHARD"');
    expect(pw, 'a part with a Document Server is the other job').not.toContain('ci-docserver.sh');
    const ds = job(ci, 'playwright-ds');
    expect(ds).toContain('E2E_BROWSERS: ${{ matrix.engine }}');
    expect(ds).toContain('DS_GREP: ${{ needs.plan.outputs.ds_grep }}');
    expect(ds).toContain('bash .github/workflows/scripts/ci-docserver.sh "$E2E_PORT" 18080');
    expect(ds).toContain('node e2e/run.mjs local --binary bin/filex --port "$E2E_PORT" --grep "$DS_GREP"');
    // The Go test of the office engine reads the Convert app ci-apps.sh fetched.
    expect(ds.indexOf('ci-apps.sh')).toBeLessThan(ds.indexOf('ci-docserver.sh'));
    expect(ds).toMatch(/go test -count=1 -v -run '\^TestOfficeEngine_TheConvertAppOnARealDocumentServer\$' \.\/internal\/server\//);
    const docserver = fs.readFileSync(path.join(DIR!, 'scripts', 'ci-docserver.sh'), 'utf8');
    expect(docserver).toContain('--add-host filex:host-gateway');
    expect(docserver, 'filex listens on 127.0.0.1: the server reaches it through a forwarder').toContain('node scripts/chain/job/forward.mjs "$gw:$port" "127.0.0.1:$port"');
    for (const v of ['FILEX_ONLYOFFICE_URL', 'FILEX_ONLYOFFICE_JWT', 'FILEX_ONLYOFFICE_CALLBACK_URL', 'FILEX_TEST_OO_URL', 'FILEX_TEST_OO_JWT', 'FILEX_TEST_OO_LISTEN', 'FILEX_TEST_OO_CALLBACK', 'FILEX_TEST_OO_FIXTURES', 'FILEX_TEST_CONVERT']) {
      expect(docserver, v).toContain(`echo "${v}=`);
    }
    expect(docserver, 'the secret is masked').toContain('echo "::add-mask::$secret"');
  });

  it.runIf(!!DIR)('runs the browser lines the build host runs on their own: the store install without a public URL, and S3', () => {
    const ci = ciCode();
    const extra = job(ci, 'playwright-extra');
    expect(extra).toContain('E2E_BROWSERS: ${{ needs.plan.outputs.e2e_engines }}');
    expect(extra).toContain('node e2e/run.mjs local --binary bin/filex "$FLAG" --grep "$GREP"');
    expect(extra).toMatch(/- name: An S3 gateway for the multi-storage spec\n\s+if: matrix\.kind == 's3'\n\s+run: bash \.github\/workflows\/scripts\/ci-s3\.sh/);
    const s3 = fs.readFileSync(path.join(DIR!, 'scripts', 'ci-s3.sh'), 'utf8');
    for (const v of ['E2E_S3_BUCKET', 'E2E_S3_ENDPOINT=http://127.0.0.1:7070', 'E2E_S3_PATH_STYLE=1', 'E2E_S3_REGION=us-east-1']) expect(s3, v).toContain(v);
    expect(s3, 'the image is the harness\'s').toContain('S3_IMAGE_DEFAULT');
  });

  it.runIf(!!DIR)('the verdict job is green only when every part of its profile is, and a pull request leaves out exactly what ci-parts says', () => {
    const ci = ciCode();
    const jobs = jobNames(ci);
    const all = job(ci, 'all');
    expect(needsOf(all).sort(), 'the verdict waits for every part').toEqual(jobs.filter((j) => j !== 'all').sort());
    expect(ifOf(all), 'a red part turns it red, not skipped').toBe('always()');
    const heavy = jobs.filter((j) => ifOf(job(ci, j)) === "needs.plan.outputs.profile == 'full'").sort();
    expect(heavy).toEqual(['playwright-ds', 'playwright-extra', 'race']);
    for (const [key, out] of Object.entries(MATRIX_JOBS)) expect(runsIn(out, 'pr'), key).toBe(!heavy.includes(key));
    const mayBeSkipped = [...all.matchAll(/\.key == "([\w-]+)"/g)].map((m) => m[1]).sort();
    expect(mayBeSkipped, 'what a pull request may leave out').toEqual(heavy);
    for (const j of jobs.filter((x) => !heavy.includes(x) && x !== 'all')) expect(ifOf(job(ci, j)), `${j} can be switched off`).toBe('');
  });

  it.runIf(!!DIR && !!bash && hasJq)('the verdict script: a red or skipped part is red, a pull request may skip exactly the heavy parts', () => {
    const all = job(ciCode(), 'all');
    const at = all.split('\n').findIndex((l) => /^\s+run: \|\s*$/.test(l));
    const lines = all.split('\n').slice(at + 1);
    const indent = lines[0].length - lines[0].trimStart().length;
    const script = lines.filter((l) => !l.trim() || l.length - l.trimStart().length >= indent).map((l) => l.slice(indent)).join('\n');
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'ci-verdict-'));
    try {
      fs.writeFileSync(path.join(tmp, 'all.sh'), `${script}\n`);
      const run = (needs: Record<string, string>, profile: string) =>
        spawnSync(bash!, [path.join(tmp, 'all.sh')], {
          encoding: 'utf8',
          env: { ...process.env, NEEDS: JSON.stringify(Object.fromEntries(Object.entries(needs).map(([k, v]) => [k, { result: v }]))), PROFILE: profile, GITHUB_STEP_SUMMARY: slash(path.join(tmp, 'summary.md')) },
        });
      const green = Object.fromEntries(needsOf(all).map((n) => [n, 'success']));
      expect(run(green, 'full').status).toBe(0);
      expect(run({ ...green, race: 'skipped' }, 'full').status, 'a full run that skipped -race').not.toBe(0);
      expect(run({ ...green, race: 'skipped', 'playwright-ds': 'skipped', 'playwright-extra': 'skipped' }, 'pr').status).toBe(0);
      expect(run({ ...green, playwright: 'skipped' }, 'pr').status, 'a pull request that skipped Playwright').not.toBe(0);
      const red = run({ ...green, go: 'failure' }, 'full');
      expect(red.status).not.toBe(0);
      expect(red.stdout).toContain('::error::go: failure');
      expect(run(green, '').status, 'no plan, no verdict').not.toBe(0);
    } finally {
      fs.rmSync(tmp, { recursive: true, force: true });
    }
  });

  it.runIf(!!DIR)('a pull request runs the light profile; a push of main and a run by hand run the full one', () => {
    const ci = ciCode();
    const on = ci.slice(ci.indexOf('\non:'), ci.indexOf('\npermissions:'));
    expect(on).toMatch(/\n {2}push:\n {4}branches: \[main\]/);
    expect(on).toMatch(/\n {2}pull_request:/);
    expect(on).toMatch(/\n {2}workflow_dispatch:/);
    expect(on, 'nothing calls ci.yml since #174').not.toMatch(/workflow_call/);
    expect(job(ci, 'plan')).toContain("PROFILE: ${{ github.event_name == 'pull_request' && 'pr' || 'full' }}");
  });

  it.runIf(!!DIR)('never cancels the run of a push: each commit keeps its own verdict', () => {
    const ci = ciCode();
    const block = ci.slice(ci.indexOf('\nconcurrency:'), ci.indexOf('\njobs:'));
    expect(block).toContain("group: ci-${{ github.workflow }}-${{ github.event_name == 'pull_request' && github.ref || github.sha }}");
    expect(block).toContain("cancel-in-progress: ${{ github.event_name == 'pull_request' }}");
  });

  it.runIf(!!DIR)('takes every migration up, three steps down and up again through the CLI, on three engines', () => {
    const engines = job(ciCode(), 'engines');
    for (const svc of ['postgres', 'mysql']) expect(engines).toMatch(new RegExp(`\\n {6}${svc}:\\n`));
    expect(engines).toContain('go test -timeout 30m ./internal/db/ ./internal/queue/ ./internal/ops/');
    expect(engines).toContain("psql -U filex -d filex -c 'CREATE DATABASE cimig'");
    expect(engines).toContain("mysql -uroot -e 'CREATE DATABASE cimig'");
    expect(engines).toContain('MIG_DOWN: "3"');
    for (const e of ['sqlite', 'postgres', 'mysql']) expect(engines, e).toMatch(new RegExp(`\\n\\s+MIG_DSN_${e}: \\S`));
    expect(engines).toContain('bash .github/workflows/scripts/migrate-roundtrip.sh "$RUNNER_TEMP/filex"');
  });

  it.runIf(!!DIR && !!bash)('migrate-roundtrip.sh is red when a down does not undo exactly one migration, or an up does not bring them all back', () => {
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'migrate-roundtrip-'));
    try {
      // A stand-in CLI: five migrations, its count per engine in a file; it
      // misbehaves when told to.
      const bin = path.join(tmp, 'filex');
      fs.writeFileSync(
        bin,
        [
          '#!/usr/bin/env bash',
          'f="$FAKE_STATE/$FILEX_DB_DRIVER"',
          'n=$(cat "$f" 2>/dev/null || echo 0)',
          'total=5',
          'case "$1 $2" in',
          '  "migrate up") if [ -e "$f" ] && [ -n "${FAKE_UP_SHORT:-}" ]; then n=$((total - 1)); else n=$total; fi ;;',
          '  "migrate down") step=1; [ -n "${FAKE_DOWN_TWO:-}" ] && step=2; n=$((n - step)) ;;',
          '  "migrate status") for i in $(seq 1 $total); do if [ "$i" -le "$n" ]; then echo "    Mon Jan  5 2026 -- 0000${i}_m.sql"; else echo "    Pending -- 0000${i}_m.sql"; fi; done; exit 0 ;;',
          '  *) exit 9 ;;',
          'esac',
          'echo "$n" > "$f"',
          '',
        ].join('\n'),
      );
      fs.chmodSync(bin, 0o755);
      const script = path.join(DIR!, 'scripts', 'migrate-roundtrip.sh');
      const run = (extra: Record<string, string>) => {
        const state = fs.mkdtempSync(path.join(tmp, 'state-'));
        return spawnSync(bash!, [slash(script), slash(bin)], {
          encoding: 'utf8',
          env: {
            ...process.env,
            RUNNER_TEMP: slash(tmp),
            GITHUB_STEP_SUMMARY: slash(path.join(tmp, 'summary.md')),
            FAKE_STATE: slash(state),
            MIG_ENGINES: 'sqlite postgres',
            MIG_DSN_sqlite: 'file:x.db',
            MIG_DSN_postgres: 'postgres://x',
            ...extra,
          },
        });
      };
      const ok = run({});
      expect(ok.status, `${ok.stdout}${ok.stderr}`).toBe(0);
      expect(ok.stdout).toContain('sqlite: applied 5>4>3>2>5');
      expect(ok.stdout).toContain('postgres: applied 5>4>3>2>5');
      const two = run({ FAKE_DOWN_TWO: '1' });
      expect(two.status).not.toBe(0);
      expect(two.stdout).toContain('after down 1, 3 applied (expected 4)');
      const short = run({ FAKE_UP_SHORT: '1' });
      expect(short.status).not.toBe(0);
      expect(short.stdout).toContain('up again left 4 applied (the first up: 5)');
      const noDsn = run({ MIG_ENGINES: 'sqlite mysql' });
      expect(noDsn.status).not.toBe(0);
      expect(noDsn.stdout).toContain('mysql: no MIG_DSN_mysql');
    } finally {
      fs.rmSync(tmp, { recursive: true, force: true });
    }
  });

  it.runIf(!!DIR)('every browser part fetches the apps, maps the fonts as the build host does, and runs e2e/run.mjs', () => {
    const ci = ciCode();
    for (const key of ['playwright', 'playwright-ds', 'playwright-extra']) {
      const b = job(ci, key);
      const apps = b.indexOf('bash .github/workflows/scripts/ci-apps.sh "$RUNNER_TEMP/apps"');
      const fonts = b.indexOf('bash .github/workflows/scripts/ci-fonts.sh');
      const run = b.indexOf('node e2e/run.mjs local --binary bin/filex');
      expect(apps, `${key} fetches the apps`).toBeGreaterThan(0);
      expect(fonts, `${key} maps the fonts`).toBeGreaterThan(0);
      expect(run, `${key} runs after both`).toBeGreaterThan(Math.max(apps, fonts));
      expect(b, key).toContain('FILEX_REQUIRE_WASM_FIXTURE: "1"');
      expect(needsOf(b), key).toContain('build');
      expect(b, `${key} runs the binary the build job made`).toContain('name: e2e-build');
      // An artifact keeps neither the bit nor the time, and the specs refuse
      // an echo.wasm older than the sources beside it.
      expect(b).toContain('chmod +x bin/filex');
      expect(b).toContain('touch backend/internal/wasmplugin/testdata/echo/echo.wasm');
    }
    const build = job(ci, 'build');
    expect(build).toContain('pnpm run build:all');
    expect(build).toContain('bash scripts/build-wasm-fixture.sh');
    expect(build).toContain('node scripts/check-embed.mjs --binary bin/filex');
    // packages/core/dist rides along: 173 builds a real .fxe with the
    // product's own library (v0.53.0's first full matrix: "Cannot find module
    // packages/core/dist/filex-core.js" in Playwright chromium 2/4).
    expect(build).toMatch(/name: e2e-build\n\s+path: \|\n\s+bin\/filex\n\s+backend\/internal\/wasmplugin\/testdata\/echo\/echo\.wasm\n\s+packages\/core\/dist\n/);
    // The font map is the build host's, family for family.
    const aliases = (t: string) => [...t.matchAll(/<alias binding="strong"><family>([^<]+)<\/family><prefer><family>([^<]+)<\/family><\/prefer><\/alias>/g)].map((m) => `${m[1]}=${m[2]}`);
    const chain = aliases(fs.readFileSync(path.join(REPO, 'scripts', 'chain', 'run.mjs'), 'utf8'));
    expect(chain).toHaveLength(4);
    expect(aliases(fs.readFileSync(path.join(DIR!, 'scripts', 'ci-fonts.sh'), 'utf8'))).toEqual(chain);
    // The apps: every variable the specs look an app up by (app-locations.mjs).
    const appsSh = fs.readFileSync(path.join(DIR!, 'scripts', 'ci-apps.sh'), 'utf8');
    expect(Object.keys(APP_LOCATIONS).sort()).toEqual(['convert', 'lang-de', 'lang-es', 'lang-fr', 'sign']);
    for (const spot of Object.values(APP_LOCATIONS) as Array<{ env: string }>) expect(appsSh, spot.env).toContain(`echo "${spot.env}=`);
    expect(appsSh).toContain('for app in sign convert; do');
    expect(appsSh).toContain('gh release download -R "BRF-Tech/filex-$app" -p filex-app.json -p plugin.wasm');
    expect(appsSh).toContain('for lang in es de fr; do');
  });
});
