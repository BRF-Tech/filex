// The CircleCI config (.circleci/config.yml, task #181) is held to the tree it
// tests: the shard list, the scripts it calls, the versions the tree pins and
// the names the release gate reads.
//
// ⚠⚠ Why each of these matters. CircleCI is the release gate's stand-in when
// GitHub Actions is down (`pnpm release X.Y.Z --resume --gate circleci`), so a
// green CircleCI run must mean what a green GitHub run means:
//   - the Go job runs on exactly as many copies as the `go` profile of
//     scripts/test-shards.json has shards, and each copy asks the list for its
//     own (`--node i/N`). A copy count below the shard count leaves the last
//     shards out with every copy green; a second list in this file would
//     drift from the first. Neither shows on CircleCI itself;
//   - the Playwright job splits with `--shard i/N`, which adds up to the whole
//     suite by construction (e2e/lib/shard.mjs);
//   - the Go job proves PostgreSQL and MySQL ran (scripts/ci/engines-ran.mjs):
//     the engine suites skip without a DSN, and a skip is a pass;
//   - the Go job runs beside every service of scripts/ci-parts.mjs's
//     GO_SERVICES, as GitHub's does: the first run had no Samba, and the 11
//     live SMB tests passed by skipping;
//   - the Playwright job is prepared by scripts/ci/browser-prep.mjs, the
//     script GitHub's browser parts call, and names no app itself: the first
//     run fetched no app, and every app spec failed to load ("the sign app's
//     plugin.wasm is not built"); it gets BROWSER_BUILD from the build, as
//     GitHub's parts do;
//   - every script it calls, and every file those import, is in this tree AND
//     in the export: the config is exported with the code (it is not one of
//     the public checkout's own workflows), and a call to a withheld file
//     fails only on CircleCI;
//   - the images are the versions the tree pins (backend/go.mod,
//     pnpm-lock.yaml), so CircleCI tests the toolchain the release builds with;
//   - the workflow is the one the release gate reads (plan.circleciWorkflow),
//     and every job a plan gate says CircleCI runs is in it;
//   - main only, and no secret: it publishes nothing and reads nothing.
//
// ⚠ Read as text, not parsed: no YAML parser is a dependency of the web app
// (releaseGatesImages.test.ts says the same of the GitHub workflows). The
// config is written plainly enough for that: no anchors, one job per
// two-space key under `jobs:`.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { APP_LOCATIONS } from '../../../e2e/helpers/app-locations.mjs';
import { MAX_SHARDS } from '../../../e2e/lib/shard.mjs';
import { BROWSER_BUILD, GO_SERVICES } from '../../../scripts/ci-parts.mjs';
import { planProfile, shardAtNode } from '../../../scripts/lib/test-shards.mjs';
import plan from '../../../scripts/release/plan.mjs';
import { bashArray } from '../helpers/exporterArrays';

const REPO = path.resolve(__dirname, '../../..');
const read = (rel: string) => fs.readFileSync(path.join(REPO, rel), 'utf8');
const CONFIG_FILE = '.circleci/config.yml';
const TEXT = read(CONFIG_FILE);
/** The config's lines, comment-only lines removed (a comment explaining a rule is not the rule). */
const CODE = TEXT.split(/\r?\n/).filter((l) => !/^\s*#/.test(l));
const SHARDS = JSON.parse(read('scripts/test-shards.json'));
const GO_SHARDS = planProfile(SHARDS, 'go', { backendDir: path.join(REPO, SHARDS.module) }).shards;
const EXPORTER = 'scripts/export-public.sh';
const exporter = fs.existsSync(path.join(REPO, EXPORTER)) ? read(EXPORTER) : '';

/** The lines of a top-level section (`jobs:`, `workflows:`), up to the next one. */
function section(name: string): string[] {
  const start = CODE.findIndex((l) => l === `${name}:`);
  if (start < 0) return [];
  const end = CODE.findIndex((l, i) => i > start && /^[a-z_]+:/.test(l));
  return CODE.slice(start + 1, end < 0 ? CODE.length : end);
}

/** The names directly under a section (two spaces in): its jobs, its workflows. */
function keysOf(lines: string[]): string[] {
  return lines.map((l) => /^ {2}([a-z0-9_-]+):\s*$/.exec(l)?.[1]).filter((k): k is string => !!k);
}

/** One job's lines, from `  <name>:` under `jobs:` to the next job. */
function job(name: string): string[] {
  const lines = section('jobs');
  const start = lines.findIndex((l) => l === `  ${name}:`);
  if (start < 0) return [];
  const end = lines.findIndex((l, i) => i > start && /^ {2}[a-z0-9_-]+:\s*$/.test(l));
  return lines.slice(start, end < 0 ? lines.length : end);
}

const JOBS = keysOf(section('jobs'));
const parallelism = (name: string) => Number(/^\s+parallelism:\s*(\d+)\s*$/m.exec(job(name).join('\n'))?.[1] ?? NaN);
const NODE = '"$((CIRCLE_NODE_INDEX + 1))/$CIRCLE_NODE_TOTAL"';

describe('.circleci/config.yml: the parts are the shard list\'s', () => {
  it('the config was read: four jobs, one workflow', () => {
    expect(JOBS).toEqual(expect.arrayContaining(['build', 'go', 'web', 'e2e']));
    expect(keysOf(section('workflows'))).toEqual(['ci']);
  });

  it('the Go job runs on exactly as many copies as the go profile has shards', () => {
    expect(GO_SHARDS.length).toBeGreaterThan(1);
    expect(parallelism('go'), 'jobs.go.parallelism').toBe(GO_SHARDS.length);
  });

  it('each Go copy asks the list for its own shard, and the config names none', () => {
    const go = job('go').join('\n');
    expect(go).toContain(`node scripts/test-shards.mjs go --profile go --format args --node ${NODE}`);
    // A shard chosen by name here would be a second list.
    expect(go).not.toMatch(/--shard\b/);
    for (const s of GO_SHARDS) {
      if (!s.name.includes('-')) continue; // `db` is a word the config uses for other things
      expect(CODE.join('\n'), `the config names the shard ${s.name}`).not.toMatch(new RegExp(`\\b${s.name}\\b`));
    }
  });

  it('the Playwright job runs part i of N on each copy, in Chromium, against the binary the build made', () => {
    const e2e = job('e2e').join('\n');
    const n = parallelism('e2e');
    expect(n).toBeGreaterThan(1);
    expect(n).toBeLessThanOrEqual(MAX_SHARDS);
    expect(e2e).toContain(`node e2e/run.mjs local --binary bin/filex --shard ${NODE}`);
    expect(e2e).toMatch(/^\s+E2E_BROWSERS: chromium$/m);
    expect(e2e).toMatch(/^\s+FILEX_REQUIRE_WASM_FIXTURE: "1"$/m);
    expect(e2e).toContain('attach_workspace');
    const build = job('build').join('\n');
    expect(build).toContain('pnpm run build:all');
    expect(build).toContain('node scripts/check-embed.mjs --binary bin/filex');
    // BROWSER_BUILD, the list GitHub's build artifact is held to as well.
    const persisted = /persist_to_workspace:\n\s+root: \.\n\s+paths:\n((?:\s+- \S+\n?)+)/.exec(build)?.[1];
    expect(persisted, 'the build persists a workspace').toBeTruthy();
    expect(persisted!.trim().split(/\n/).map((l) => l.trim().replace(/^- /, ''))).toEqual(BROWSER_BUILD);
    expect(section('workflows').join('\n')).toMatch(/- e2e:\n\s+requires:\n\s+- build/);
  });

  it('the Playwright job is prepared by the script GitHub\'s browser parts call, and names no app of its own', () => {
    const e2e = job('e2e').join('\n');
    const apps = e2e.indexOf('node scripts/ci/browser-prep.mjs apps ');
    const fonts = e2e.indexOf('node scripts/ci/browser-prep.mjs fonts ');
    const run = e2e.indexOf('node e2e/run.mjs local');
    expect(apps, 'the apps are fetched').toBeGreaterThan(0);
    expect(fonts, 'the font map is set').toBeGreaterThan(0);
    expect(run, 'the suite runs after both').toBeGreaterThan(Math.max(apps, fonts));
    // Which apps, and where the specs find them, is app-locations.mjs: a name
    // or a variable of it here would be a second list.
    expect(Object.keys(APP_LOCATIONS).length).toBeGreaterThan(0);
    for (const spot of Object.values(APP_LOCATIONS) as Array<{ env: string; repo: string }>) {
      expect(TEXT, `the config names ${spot.env}`).not.toContain(spot.env);
      expect(TEXT, `the config names ${spot.repo}`).not.toContain(spot.repo);
    }
  });
});

describe('.circleci/config.yml: a green run means what a green GitHub run means', () => {
  it('the Go job runs beside PostgreSQL and MySQL and proves both ran, from the events of the same run', () => {
    const go = job('go').join('\n');
    expect(go).toMatch(/^\s+- image: postgres:\d+/m);
    expect(go).toMatch(/^\s+- image: mysql:8\.4$/m);
    expect(go).toMatch(/^\s+FILEX_TEST_PG_DSN: "postgres:\/\/filex:filex@127\.0\.0\.1:5432\//m);
    expect(go).toMatch(/^\s+FILEX_TEST_MYSQL_DSN: "root:filex@tcp\(127\.0\.0\.1:3306\)\//m);
    expect(go).toContain('--jsonfile ../test-results/go/go-test.json');
    expect(go).toContain('node scripts/ci/engines-ran.mjs test-results/go/go-test.json');
    expect(go.indexOf('engines-ran.mjs')).toBeGreaterThan(go.indexOf('gotestsum'));
    expect(go).toMatch(/^\s+FILEX_REQUIRE_WASM_FIXTURE: "1"$/m);
    expect(go).toContain('bash scripts/build-wasm-fixture.sh');
  });

  it('the Go job runs beside every service of GO_SERVICES, sets its variable, and waits for it', () => {
    const go = job('go').join('\n');
    const waited = /for port in ([\d ]+); do/.exec(go)?.[1]?.trim().split(/\s+/) ?? [];
    expect(waited.length, 'one port waited for per service').toBe(Object.keys(GO_SERVICES).length);
    for (const [v, svc] of Object.entries(GO_SERVICES)) {
      // GitHub names the service; here it is the image's name.
      expect(go, `the Go job runs beside ${svc}`).toMatch(new RegExp(`^\\s+- image: \\S*${svc}\\S*$`, 'm'));
      const value = new RegExp(`^\\s+${v}: "([^"]+)"$`, 'm').exec(go)?.[1];
      expect(value, `the Go job sets ${v}`).toBeTruthy();
      // A server that is not up is waited for, not skipped.
      const port = /127\.0\.0\.1:(\d+)/.exec(value!)?.[1];
      expect(waited, `${v}: its port is waited for`).toContain(port);
    }
  });

  it('vet, the unit suites in UTC and the desktop tests run, as in GitHub\'s ci.yml', () => {
    expect(job('build').join('\n')).toContain('go vet ./...');
    const web = job('web').join('\n');
    expect(web).toContain("pnpm --filter='./web' --filter='./packages/*' test");
    expect(web).toMatch(/^\s+TZ: UTC$/m);
    expect(web).toContain('node --experimental-strip-types --test test/*.test.ts');
  });

  it('every script and file it calls, and every file those import, is in this tree, and in the export', () => {
    const calls = new Set<string>();
    for (const m of CODE.join('\n').matchAll(/\b(?:node|bash)\s+((?:scripts|e2e)\/[A-Za-z0-9._/-]+\.(?:mjs|sh))/g)) calls.add(m[1]!);
    expect([...calls].sort()).toEqual(
      expect.arrayContaining([
        'e2e/run.mjs',
        'scripts/build-wasm-fixture.sh',
        'scripts/check-embed.mjs',
        'scripts/ci/browser-prep.mjs',
        'scripts/ci/engines-ran.mjs',
        'scripts/test-shards.mjs',
      ]),
    );
    for (const f of calls) expect(fs.existsSync(path.join(REPO, f)), `${f} is not in the tree`).toBe(true);
    // What a called module imports runs on CircleCI too: a withheld one fails
    // there alone. Relative imports, followed to the end.
    const queue = [...calls].filter((f) => f.endsWith('.mjs'));
    while (queue.length) {
      const f = queue.shift()!;
      for (const m of read(f).matchAll(/^\s*(?:import|export)\b[^'"]*?\bfrom\s+'(\.{1,2}\/[^']+)'/gm)) {
        const dep = path.posix.normalize(path.posix.join(path.posix.dirname(f), m[1]!));
        if (calls.has(dep)) continue;
        expect(fs.existsSync(path.join(REPO, dep)), `${dep} (imported by ${f}) is not in the tree`).toBe(true);
        calls.add(dep);
        if (dep.endsWith('.mjs')) queue.push(dep);
      }
    }
    expect(calls.has('e2e/helpers/app-locations.mjs'), 'the imports were followed').toBe(true);
    expect(calls.has('scripts/chain/run.mjs'), 'the imports were followed').toBe(true);
    if (!exporter) return; // the public tree has no exporter: it IS the export
    const files = new Set(bashArray(exporter, 'private_files', EXPORTER));
    const dirs = bashArray(exporter, 'private_dirs', EXPORTER);
    expect(files.has(EXPORTER), 'the lists were parsed').toBe(true);
    for (const f of [...calls, CONFIG_FILE]) {
      expect(files.has(f) || dirs.some((d) => f === d || f.startsWith(`${d}/`)), `${f} is withheld from the export`).toBe(false);
    }
  });

  it('carries nothing the export would rewrite', () => {
    // The export rewrites these on the way out: a config that holds one would
    // be published as something nobody wrote.
    expect(TEXT).not.toMatch(/brf\.sh|gitlab\.com[/:]brftech/);
  });

  it('the images are the versions the tree pins', () => {
    const goVersion = /^go (\d+\.\d+)/m.exec(read('backend/go.mod'))?.[1];
    expect(goVersion).toBeTruthy();
    const goImages = [...TEXT.matchAll(/cimg\/go:([0-9.]+)-node/g)].map((m) => m[1]);
    expect(goImages.length).toBeGreaterThan(0);
    for (const v of goImages) expect(v, 'cimg/go is backend/go.mod\'s Go').toBe(goVersion);
    const pw = /^ {2}playwright@(\d+\.\d+\.\d+):/m.exec(read('pnpm-lock.yaml'))?.[1];
    expect(pw).toBeTruthy();
    const pwImages = [...TEXT.matchAll(/mcr\.microsoft\.com\/playwright:v([0-9.]+)-/g)].map((m) => m[1]);
    expect(pwImages).toEqual([pw]);
  });

  it('runs on main only, and asks for no secret', () => {
    const wf = section('workflows').join('\n');
    expect(wf).toMatch(/when:\n\s+equal: \[main, << pipeline\.git\.branch >>\]/);
    expect(CODE.join('\n')).not.toMatch(/^\s+context:/m);
    expect(CODE.join('\n')).not.toMatch(/\$\{?(?:CIRCLE_TOKEN|CIRCLECI_TOKEN|GITHUB_TOKEN|NPM_TOKEN)/);
  });
});

describe('the release gate reads what this config runs', () => {
  const p = plan({ repo: REPO, version: '9.9.9', tag: 'v9.9.9' });

  it('the workflow the gate reads is the one this file defines', () => {
    expect(p.circleciWorkflow).toBe('ci');
    expect(keysOf(section('workflows'))).toContain(p.circleciWorkflow);
    expect(typeof p.circleci?.runs).toBe('function');
  });

  it('every CircleCI job a plan gate names is a job of that workflow', () => {
    const wf = section('workflows').join('\n');
    const named = (p.heavy as Array<{ name: string; circleci?: string }>).filter((g) => g.circleci);
    expect(named.map((g) => g.name)).toEqual(
      expect.arrayContaining(['go: vet + test', 'go: migrations on sqlite, postgres AND mysql', 'e2e: Playwright (whole)']),
    );
    for (const g of named) {
      for (const j of g.circleci!.split(/\s*\+\s*/)) {
        expect(JOBS, `${g.name}: CircleCI job ${j}`).toContain(j);
        expect(wf, `${g.name}: the workflow runs ${j}`).toMatch(new RegExp(`- ${j}(?::|$)`, 'm'));
      }
    }
  });
});

describe('scripts/test-shards.mjs --node i/N: a CI copy\'s shard, by position', () => {
  const cli = (...args: string[]) =>
    spawnSync(process.execPath, [path.join(REPO, 'scripts', 'test-shards.mjs'), ...args], { cwd: REPO, encoding: 'utf8' });
  const n = GO_SHARDS.length;

  it('copies 1..N together are every shard of the profile, each once', () => {
    const picked = Array.from({ length: n }, (_, i) => shardAtNode(GO_SHARDS, `${i + 1}/${n}`, 'go').name);
    expect(picked).toEqual(GO_SHARDS.map((s) => s.name));
    expect(new Set(picked).size).toBe(n);
  });

  it('refuses a copy count that is not the shard count, an i outside 1..N, and a malformed value', () => {
    expect(() => shardAtNode(GO_SHARDS, `1/${n - 1}`, 'go')).toThrow(`profile go has ${n} shard(s) and this job runs on ${n - 1}`);
    expect(() => shardAtNode(GO_SHARDS, `1/${n + 1}`, 'go')).toThrow(`parallelism: ${n}`);
    expect(() => shardAtNode(GO_SHARDS, `0/${n}`, 'go')).toThrow('i is 1 to');
    expect(() => shardAtNode(GO_SHARDS, `${n + 1}/${n}`, 'go')).toThrow('i is 1 to');
    for (const bad of ['', '3', '3of8', '-1/8', 'a/b']) expect(() => shardAtNode(GO_SHARDS, bad, 'go')).toThrow('expected i/N');
  });

  it('the CLI prints the copy\'s shard, and stops on a count that does not match', () => {
    const ok = cli('go', '--profile', 'go', '--format', 'json', '--node', `2/${n}`);
    expect(ok.status, ok.stderr).toBe(0);
    const out = JSON.parse(ok.stdout);
    expect(out.shards.map((s: { name: string }) => s.name)).toEqual([GO_SHARDS[1]!.name]);
    expect(ok.stderr).toContain(`node 2/${n}: shard ${GO_SHARDS[1]!.name}`);

    const wrong = cli('go', '--profile', 'go', '--format', 'json', '--node', `1/${n - 1}`);
    expect(wrong.status).toBe(1);
    expect(wrong.stderr).toContain(`has ${n} shard(s)`);
    expect(wrong.stdout).toBe('');

    const both = cli('go', '--profile', 'go', '--shard', GO_SHARDS[0]!.name, '--node', `1/${n}`);
    expect(both.status).toBe(1);
    expect(both.stderr).toContain('give one of them');
  });
});
