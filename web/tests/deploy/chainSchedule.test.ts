// The test chain of scripts/chain/: what a profile runs, how it shares the
// build host's memory, and how long it is expected to take.
//
// ⚠ Why this exists (#170): through 0.52 the chain was a shell script written
// by hand for each release on the build host, every job one after another.
// 0.51's took 4 h 42 min - 115 of them four -race shards in a row, ~100 three
// browser engines in a row - and nobody could say in advance what a change of
// order would cost. The plan and the scheduler are pure now, so the budget
// and the three-hour bound are held here, against the minutes the host
// measured, before anyone spends an afternoon on a run.
//
// The Go and -race jobs are the shards of scripts/test-shards.json (#171),
// the list every runner takes its Go parts from: these tests hold the chain
// to that list, so a second list of the chain's own cannot come back.

import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import {
  NIGHTLY_EXTRAS,
  SHARD_LISTS,
  WEIGHTS,
  buildPlan,
  expectedMinutes,
  loadLists,
  nightlyExtras,
  parseE2eList,
  parseTestList,
  shardMinutes,
  trackWeight,
} from '../../../scripts/chain/plan.mjs';
import { KIND, hostJobEnv, jobImages, parseArgs, parseEnvFile } from '../../../scripts/chain/run.mjs';
import { pickJobs, poolBudget, readyJobs, simulate } from '../../../scripts/chain/schedule.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const CHAIN = path.join(REPO, 'scripts', 'chain');
const lists = loadLists({}, { cwd: REPO });
const full = buildPlan({ profile: 'full', lists, src: REPO });
const names = (jobs: Array<{ name: string }>) => jobs.map((j) => j.name);
const shardList = JSON.parse(fs.readFileSync(path.join(REPO, 'scripts', 'test-shards.json'), 'utf8'));
const shardJobs = (profile: string) => shardList.profiles[profile].map((s: { name: string }) => s.name);

type Job = { name: string; weight: number; prio: number; needs: string[]; kind: string };
const job = (name: string, weight: number, prio = 1, needs: string[] = []): Job => ({ name, weight, prio, needs, kind: 'race' });

function budgetOf(plan: ReturnType<typeof buildPlan>, memGb = 8, poolMax = 3) {
  return { memGb, trackGb: trackWeight(plan.track), dbGb: plan.pool.some((j: { db: boolean }) => j.db) ? WEIGHTS.db : 0, poolMax };
}

describe('the default lists', () => {
  it('take the Go and -race jobs from scripts/test-shards.json, and from nothing else', () => {
    expect(SHARD_LISTS.go).toBe('cmd:node scripts/test-shards.mjs go --profile go --format chain');
    expect(SHARD_LISTS.race).toBe('cmd:node scripts/test-shards.mjs go --profile race --format chain');
    expect(names(full.pool.filter((j: Job) => j.kind === 'go'))).toEqual(shardJobs('go').map((n: string) => `go-${n}`));
    expect(names(full.pool.filter((j: Job) => j.kind === 'race'))).toEqual(shardJobs('race').map((n: string) => `race-${n}`));
    // The chain's own Go lists (0.52's -race split by first letter, which left
    // Example and Fuzz functions in no shard, lesson #1119) are gone for good.
    expect(fs.existsSync(path.join(CHAIN, 'lists', 'race.txt'))).toBe(false);
    expect(fs.existsSync(path.join(CHAIN, 'lists', 'go.txt'))).toBe(false);
  });

  it('run the build, the gates, the shard check and the migrations next to the shards, and the same browser round', () => {
    const pool = names(full.pool);
    for (const n of ['build', 'web', 'docs', 'e2etsc', 'shards-check', 'migrate']) expect(pool).toContain(n);
    expect(full.pool.find((j: Job) => j.name === 'shards-check')).toMatchObject({ kind: 'shards-check', needs: ['build'] });
    expect(KIND['shards-check']).toMatchObject({ image: 'node', goroot: true, script: 'shards-check.sh' });
    expect(names(full.track)).toEqual(['e2e-cypress', 'e2e-chromium', 'e2e-firefox', 'e2e-webkit', 'e2e-nods', 'e2e-nopub', 'e2e-s3']);
    expect(full.track.filter((t: { ds: boolean }) => t.ds).map((t: { name: string }) => t.name)).toEqual(['e2e-chromium', 'e2e-firefox', 'e2e-webkit']);
  });

  it('build each sharded package once, and every shard of it waits for that build', () => {
    const builds = full.pool.filter((j: Job) => j.kind === 'race-build');
    expect(names(builds)).toEqual(['race-build-handlers', 'race-build-wasmplugin']);
    for (const shard of full.pool.filter((j: Job & { env: Record<string, string> }) => j.kind === 'race' && j.env.RUN)) {
      const b = builds.find((x: Job & { env: Record<string, string> }) => x.env.PKG === shard.env.PKGS);
      expect(shard.needs, shard.name).toEqual(['build', b.name]);
    }
  });

  it('run the packages the -run shards cut nowhere else in the -race round', () => {
    const sharded = [...new Set(lists.race.filter((r: { run: string }) => r.run).map((r: { pkgs: string[] }) => r.pkgs[0]))];
    expect(sharded.sort()).toEqual(Object.keys(shardList.units).sort());
    for (const r of lists.race.filter((x: { run: string; pkgs: string[] }) => !x.run && x.pkgs.includes('./...'))) {
      for (const pkg of sharded) expect(r.exclude, `${r.name} leaves out ${pkg}`).toContain(pkg);
    }
  });

  it("expect each Go and -race job to take what scripts/test-shards.json's weights say", () => {
    const table = shardMinutes(REPO);
    for (const j of full.pool.filter((x: Job) => x.kind === 'go' || x.kind === 'race')) {
      expect(table[j.name], `${j.name} has no weight in scripts/test-shards.json`).toBeGreaterThan(0);
      expect(j.expect, j.name).toBe(table[j.name]);
    }
    const unit = (name: string) => [...Object.values(shardList.units).flat(), ...shardList.packages].find((u: { name: string }) => u.name === name) as { weight_s: number };
    expect(table['race-handlers-1']).toBe(Math.round((unit('handlers-1').weight_s / 60) * 10) / 10);
  });

  it('name a job kind run.mjs knows and a job script that exists, for every job', () => {
    for (const j of [...full.pool, ...full.track]) {
      expect(KIND[j.kind], j.name).toBeTruthy();
      expect(fs.existsSync(path.join(CHAIN, 'job', KIND[j.kind].script)), KIND[j.kind].script).toBe(true);
    }
  });
});

describe('the list format', () => {
  it('refuses a run-regex over more than one package', () => {
    expect(() => parseTestList('x ^TestA ./a ./b')).toThrow(/exactly one package/);
    expect(() => parseTestList('x ^TestA ./...')).toThrow(/exactly one package/);
  });

  it('refuses a name twice, a bad name, and a line without a package', () => {
    expect(() => parseTestList('a - ./x\na - ./y')).toThrow(/twice/);
    expect(() => parseTestList('A_b - ./x')).toThrow(/job name/);
    expect(() => parseTestList('a -')).toThrow(/package/);
  });

  it('reads exclusions and the rest of an e2e line as its arguments', () => {
    expect(parseTestList('# c\n\np - ./... !./a !./b')).toEqual([{ name: 'p', run: '', pkgs: ['./...'], exclude: ['./a', './b'] }]);
    expect(parseE2eList('s3 s3 chromium,webkit 0 --grep (a|b)\\.spec')).toEqual([
      { name: 's3', kind: 's3', engines: ['chromium', 'webkit'], ds: false, args: ['--grep', '(a|b)\\.spec'], shard: null },
    ]);
    expect(() => parseE2eList('x selenium chromium 0')).toThrow(/kind/);
    expect(() => parseE2eList('x local - 0')).toThrow(/engines/);
    expect(() => parseE2eList('x local chromium yes')).toThrow(/ds is 0 or 1/);
  });

  it('reads a part of a split Playwright run, and refuses a malformed one', () => {
    expect(parseE2eList('c1 local chromium 1 --shard 1/4')[0].shard).toEqual({ current: 1, total: 4 });
    expect(parseE2eList('c2 local chromium 1 --grep x --shard=2/4')[0].shard).toEqual({ current: 2, total: 4 });
    expect(parseE2eList('c local chromium 1')[0].shard).toBeNull();
    expect(() => parseE2eList('c local chromium 1 --shard 5/4')).toThrow(/--shard/);
    expect(() => parseE2eList('c local chromium 1 --shard 1/4 --shard 2/4')).toThrow(/--shard/);
    expect(() => parseE2eList('cy cypress - 0 --shard 1/2')).toThrow(/not Cypress/);
  });

  it.skipIf(process.platform === 'win32')('takes a list from a command (cmd:)', () => {
    const l = loadLists({ CHAIN_RACE_LIST: "cmd:printf 'a ^TestA ./internal/x\\n'" }, { cwd: REPO });
    expect(l.race).toEqual([{ name: 'a', run: '^TestA', pkgs: ['./internal/x'], exclude: [] }]);
  });
});

describe('the profiles', () => {
  it('nightly runs what full runs, plus the Go tests on the Document Server and on a real S3, shots and realenv (#175)', () => {
    // ⚠ Before #175 nightly was full under another name: the extras below ran
    // in no chain at all - the Go test skipped itself, the shot scripts rotted
    // until a release day needed pictures, realenv ran when someone remembered.
    const nightly = buildPlan({ profile: 'nightly', lists, src: REPO });
    expect(names(nightly.pool)).toEqual(names(full.pool));
    expect(NIGHTLY_EXTRAS).toEqual(['ds-go', 's3-live', 'shots', 'realenv']);
    expect(names(nightly.track)).toEqual([
      'e2e-cypress', 'e2e-chromium', 'e2e-firefox', 'e2e-webkit', 'ds-go', 'e2e-nods', 'e2e-nopub', 'e2e-s3', 's3-live', 'shots', 'realenv',
    ]);
    // Right after the last line with the Document Server, which run.mjs stops
    // once no later line needs it: ds-go is the line that still needs it.
    const ds = nightly.track.map((t: { ds: boolean }) => t.ds);
    expect(nightly.track[ds.lastIndexOf(true)].name).toBe('ds-go');
    expect(nightly.track.map((t: { index: number }) => t.index)).toEqual(nightly.track.map((_: unknown, i: number) => i));
    for (const name of NIGHTLY_EXTRAS) {
      const t = nightly.track.find((x: { name: string }) => x.name === name);
      expect(t, name).toMatchObject({ kind: name, needs: ['build'], weight: WEIGHTS[name as keyof typeof WEIGHTS], expect: expectedMinutes({ name, kind: name }) });
    }
  });

  it('nightly takes the extras CHAIN_NIGHTLY_EXTRAS names, in their own order, and refuses one it does not know', () => {
    const only = (v: string) => names(buildPlan({ profile: 'nightly', lists, src: REPO, env: { CHAIN_NIGHTLY_EXTRAS: v } }).track).filter((n: string) => !n.startsWith('e2e-'));
    expect(only('realenv,ds-go')).toEqual(['ds-go', 'realenv']);
    expect(only('shots')).toEqual(['shots']);
    expect(only('s3-live')).toEqual(['s3-live']);
    expect(only('none')).toEqual([]);
    expect(only('')).toEqual(['ds-go', 's3-live', 'shots', 'realenv']);
    expect(nightlyExtras({ CHAIN_NIGHTLY_EXTRAS: ' shots , ds-go ' })).toEqual(['ds-go', 'shots']);
    expect(() => nightlyExtras({ CHAIN_NIGHTLY_EXTRAS: 'shots,lighthouse' })).toThrow(/lighthouse/);
    // The full profile never takes them, whatever the environment says.
    expect(names(buildPlan({ profile: 'full', lists, src: REPO, env: { CHAIN_NIGHTLY_EXTRAS: 'ds-go,s3-live,shots,realenv' } }).track)).toEqual(names(full.track));
  });

  it('runs every nightly job in a kind run.mjs knows, with a job script that exists; realenv on the host', () => {
    const nightly = buildPlan({ profile: 'nightly', lists, src: REPO });
    for (const j of [...nightly.pool, ...nightly.track]) {
      expect(KIND[j.kind], j.name).toBeTruthy();
      expect(fs.existsSync(path.join(CHAIN, 'job', KIND[j.kind].script)), KIND[j.kind].script).toBe(true);
    }
    // realenv starts containers with this host's Docker and mounts the tree
    // by its host path: in a job container that path does not exist.
    expect(KIND.realenv).toMatchObject({ host: true, script: 'realenv.sh' });
    expect(KIND.realenv.image).toBeUndefined();
    // ds-go reads the Document Server's secret, which is root's (0600).
    expect(KIND['ds-go']).toMatchObject({ image: 'go', dsEnv: true, apps: true });
    expect(KIND['ds-go'].user).toBeFalsy();
    // s3-live reads the bucket's key from a 0600 file of the run, which is root's.
    expect(KIND['s3-live']).toMatchObject({ image: 'go', script: 's3-live.sh', s3Live: true });
    expect(KIND['s3-live'].user).toBeFalsy();
    // shots builds its stamped binary and the plugins scene's example plugin.
    expect(KIND.shots).toMatchObject({ image: 'pw', browser: true, goroot: true });
    // A host job needs no image: asking Docker for "undefined" would end the
    // run at its setup.
    const images = { pw: 'pw:1', go: 'go:1', node: 'node:1' };
    expect(jobImages(new Set(['realenv', 'shots', 'ds-go', 'build']), images).sort()).toEqual(['go:1', 'node:1', 'pw:1']);
  });

  it('targeted runs the build and its fast gates, then only what is asked for', () => {
    const bare = buildPlan({ profile: 'targeted', lists, env: {} });
    expect(names(bare.pool)).toEqual(['build', 'web', 'docs', 'e2etsc']);
    expect(bare.track).toEqual([]);
    const asked = buildPlan({
      profile: 'targeted',
      lists,
      env: {
        CHAIN_GO_PKGS: './internal/auth/... ./internal/db/...',
        CHAIN_RACE_PKGS: './internal/auth/...',
        CHAIN_MIGRATE: '1',
        CHAIN_CY_SPECS: 'cypress/e2e/05-routing.cy.ts',
        CHAIN_E2E_GREP: '(98|116)-[a-z-]+\\.spec',
        CHAIN_E2E_BROWSERS: 'chromium,firefox,webkit',
      },
    });
    expect(names(asked.pool)).toEqual(['build', 'web', 'docs', 'e2etsc', 'go-targeted', 'migrate', 'race-targeted']);
    expect(asked.pool.find((j: Job) => j.name === 'go-targeted').env.PKGS).toBe('./internal/auth/... ./internal/db/...');
    expect(names(asked.track)).toEqual(['e2e-cypress', 'e2e-targeted']);
    expect(asked.track[1]).toMatchObject({ engines: ['chromium', 'firefox', 'webkit'], ds: true, args: ['--grep', '(98|116)-[a-z-]+\\.spec'] });
  });

  it('targeted runs a nightly extra by name, alone: the live S3 tests by hand (CHAIN_EXTRAS)', () => {
    const s3 = buildPlan({ profile: 'targeted', lists, env: { CHAIN_EXTRAS: 's3-live' } });
    expect(names(s3.pool)).toEqual(['build', 'web', 'docs', 'e2etsc']);
    expect(s3.track.map((t: { name: string; kind: string }) => `${t.name}:${t.kind}`)).toEqual(['s3-live:s3-live']);
    expect(names(buildPlan({ profile: 'targeted', lists, env: { CHAIN_EXTRAS: 'shots, s3-live' } }).track)).toEqual(['s3-live', 'shots']);
    expect(buildPlan({ profile: 'targeted', lists, env: { CHAIN_EXTRAS: 'none' } }).track).toEqual([]);
    expect(() => buildPlan({ profile: 'targeted', lists, env: { CHAIN_EXTRAS: 'lighthouse' } })).toThrow(/CHAIN_EXTRAS/);
    // The full and nightly profiles never read it.
    expect(names(buildPlan({ profile: 'full', lists, src: REPO, env: { CHAIN_EXTRAS: 's3-live' } }).track)).toEqual(names(full.track));
    // nightly.mjs s3-live asks for exactly this.
    expect(fs.readFileSync(path.join(CHAIN, 'nightly.mjs'), 'utf8')).toContain("profile: 'targeted', env: { CHAIN_EXTRAS: 's3-live', ...only }");
  });

  it('refuses a profile it does not have', () => {
    expect(() => buildPlan({ profile: 'quick', lists })).toThrow(/profile/);
    expect(() => parseArgs(['--profile', 'quick'])).toThrow(/profile/);
    expect(() => parseArgs(['--fast'])).toThrow(/unknown option/);
  });
});

describe('the scheduler', () => {
  it('reserves memory for a job that does not fit, so lighter jobs behind it cannot take it', () => {
    const big = job('big', 3, 1);
    const small = job('small', 1, 2);
    expect(pickJobs({ ready: [big, small], running: [job('r', 1.5)], budget: 4, maxJobs: 3 })).toEqual([]);
    expect(pickJobs({ ready: [small, big], running: [job('r', 1.5)], budget: 4, maxJobs: 3 })).toEqual([small]);
  });

  it('starts a job heavier than the budget when nothing else runs, and never more than maxJobs', () => {
    expect(pickJobs({ ready: [job('huge', 9)], running: [], budget: 4, maxJobs: 3 })).toEqual([job('huge', 9)]);
    const three = [1, 2, 3, 4].map((i) => job(`j${i}`, 0.5, i));
    expect(pickJobs({ ready: three, running: [], budget: 8, maxJobs: 3 })).toHaveLength(3);
  });

  it('skips a job whose need failed, and holds one whose need has not passed yet', () => {
    const pool = [job('build', 1, 0), job('a', 1, 1, ['build']), job('b', 1, 2, ['a'])];
    expect(readyJobs(pool, { build: 'failed', a: 'pending', b: 'pending' }).skip.map((j: Job) => j.name)).toEqual(['a']);
    expect(readyJobs(pool, { build: 'passed', a: 'running', b: 'pending' }).ready).toEqual([]);
  });

  it('gives the pool what the browser round and the databases do not hold', () => {
    expect(poolBudget({ memGb: 8, trackActive: true, trackGb: 4.5, dbUp: true, dbGb: 0.75 })).toBe(2.75);
    expect(poolBudget({ memGb: 8, trackActive: false, trackGb: 4.5, dbUp: false, dbGb: 0.75 })).toBe(8);
  });
});

describe('a full run, replayed with the minutes the build host measured', () => {
  const b = budgetOf(full);
  const sim = simulate(full, { ...b, minutes: expectedMinutes });
  const at = (name: string) => sim.timeline.find((e: { name: string }) => e.name === name);

  it('never holds more than the 8 GiB budget, and runs at most three pool jobs at once', () => {
    expect(sim.peakGb).toBeLessThanOrEqual(8 + 1e-9);
    expect(sim.maxRunning).toBeLessThanOrEqual(3);
  });

  it('runs -race next to Chromium: two or three race jobs while it runs', () => {
    const c = at('e2e-chromium');
    const overlapping = sim.timeline.filter(
      (e: { name: string; start: number; end: number }) => e.name.startsWith('race-') && !e.name.startsWith('race-build') && e.start < c.end && e.end > c.start,
    );
    expect(overlapping.length).toBeGreaterThanOrEqual(2);
  });

  it('ends within three hours (0.51 took 4 h 42 min)', () => {
    expect(sim.wall).toBeLessThanOrEqual(180);
  });

  it("still ends within three hours on 0.51's slower minutes (Firefox 42, Chromium 28, Cypress 10.5, the Go jobs half as slow again)", () => {
    const slow: Record<string, number> = { 'e2e-firefox': 42, 'e2e-chromium': 28, 'e2e-cypress': 10.5 };
    const s = simulate(full, {
      ...b,
      minutes: (j: Job) => slow[j.name] ?? (j.kind === 'go' ? expectedMinutes(j) * 1.5 : expectedMinutes(j)),
    });
    expect(s.wall).toBeLessThanOrEqual(180);
  });
});

describe('a nightly run, replayed with the same minutes', () => {
  const nightly = buildPlan({ profile: 'nightly', lists, src: REPO });
  const b = budgetOf(nightly);
  const sim = simulate(nightly, { ...b, minutes: expectedMinutes });
  const at = (name: string) => sim.timeline.find((e: { name: string }) => e.name === name);

  it('holds the browser round to the same share as the full run: the extras weigh no more than an engine', () => {
    expect(trackWeight(nightly.track)).toBe(trackWeight(full.track));
    expect(sim.peakGb).toBeLessThanOrEqual(8 + 1e-9);
  });

  it('runs ds-go while the Document Server is still up, and s3-live, shots and realenv after every browser line', () => {
    expect(at('ds-go').start).toBeGreaterThanOrEqual(at('e2e-webkit').end);
    expect(at('ds-go').end).toBeLessThanOrEqual(at('e2e-nods').start);
    expect(at('s3-live').start).toBeGreaterThanOrEqual(at('e2e-s3').end);
    expect(at('shots').start).toBeGreaterThanOrEqual(at('s3-live').end);
    expect(at('realenv').start).toBeGreaterThanOrEqual(at('shots').end);
  });

  it('ends within 3 h 30 min: started at 22:00 UTC it is over before a window at 01:45 UTC, less a 15 min margin', () => {
    // scripts/chain/nightly.env.example's NIGHTLY_QUIET keeps the Monday
    // morning clear (a weekly round on the same host at 02:00 UTC).
    expect(sim.wall).toBeLessThanOrEqual(210);
  });
});

describe('a host job (realenv)', () => {
  const env = hostJobEnv(
    { PATH: '/usr/bin', REALENV_LOCK: '/var/lib/filex-chain/lock', CHAIN_LOCK_HELD: '/var/lib/filex-chain/lock', HOME: '/root' },
    {
      job: { name: 'realenv' },
      dir: '/var/lib/filex-nightly/runs/nightly-20261007-010000Z-1a2b3c4d',
      src: '/var/lib/filex-nightly/src',
      prefix: 'fxnightly',
      images: { ds: 'onlyoffice/documentserver:9.4', pw: 'mcr.microsoft.com/playwright:v1.59.1-noble' },
      realenv: { stages: 'tls sso', allowSkip: false, privNet: '172.30.52', pubNet: '' },
    },
  );

  it('never waits for the lock the chain already holds', () => {
    // realenv takes REALENV_LOCK around each stage; the chain holds the build
    // lock for the whole run, so the same lock here would wait forever.
    expect(env.REALENV_LOCK).toBeUndefined();
    expect(env.CHAIN_LOCK_HELD).toBeUndefined();
    expect(fs.readFileSync(path.join(CHAIN, 'job', 'realenv.sh'), 'utf8')).toMatch(/^unset REALENV_LOCK$/m);
  });

  it("gets its own prefix, its work directory under the job's output, the chain's images, and pulls what it lacks", () => {
    expect(env).toMatchObject({
      JOB: 'realenv',
      SRC: '/var/lib/filex-nightly/src',
      REALENV_PREFIX: 'fxnightly-re',
      REALENV_PULL: '1',
      REALENV_OO_IMAGE: 'onlyoffice/documentserver:9.4',
      REALENV_RUNNER_IMAGE: 'mcr.microsoft.com/playwright:v1.59.1-noble',
      REALENV_STAGES: 'tls sso',
      REALENV_ALLOW_SKIP: '0',
      REALENV_PRIV_NET: '172.30.52',
      PATH: '/usr/bin',
    });
    expect(env.OUT.replace(/\\/g, '/')).toBe('/var/lib/filex-nightly/runs/nightly-20261007-010000Z-1a2b3c4d/out/realenv');
    expect(env.REALENV_WORK.replace(/\\/g, '/')).toBe('/var/lib/filex-nightly/runs/nightly-20261007-010000Z-1a2b3c4d/out/realenv/work');
    expect(env.REALENV_PUB_NET).toBeUndefined();
  });

  it('is red, not green, when a stage was skipped (unless CHAIN_REALENV_ALLOW_SKIP=1)', () => {
    // e2e/realenv/run.sh exits 0 when it skips a stage whose images it cannot
    // get: a night that ran no stage must not read as one they passed.
    const sh = fs.readFileSync(path.join(CHAIN, 'job', 'realenv.sh'), 'utf8');
    expect(sh).toMatch(/"\$skipped" -gt 0/);
    expect(sh).toMatch(/REALENV_ALLOW_SKIP:-0/);
  });
});

describe('the Go test on the Document Server (ds-go)', () => {
  const sh = fs.readFileSync(path.join(CHAIN, 'job', 'ds-go.sh'), 'utf8');
  const test = fs.readFileSync(path.join(REPO, 'backend', 'internal', 'server', 'office_engine_ds_test.go'), 'utf8');

  it('names the test that exists, and every variable it reads', () => {
    const name = /TEST='\^(\w+)\$'/.exec(sh)?.[1];
    expect(name).toBeTruthy();
    expect(test).toContain(`func ${name}(t *testing.T)`);
    for (const v of [...test.matchAll(/os\.Getenv\("(FILEX_TEST_[A-Z_]+)"\)/g)].map((m) => m[1])) expect(sh, v).toContain(`${v}=`);
    for (const f of ['letter.docx', 'report.xlsx']) expect(fs.existsSync(path.join(REPO, 'e2e', 'fixtures', 'file-types', f)), f).toBe(true);
    expect(sh).toContain('FILEX_TEST_OO_FIXTURES=/w/src/e2e/fixtures/file-types');
  });

  it('is red when the test only skipped: the test skips itself everywhere it is not told where the server is', () => {
    expect(sh).toMatch(/"\$passed" -eq 0/);
  });
});

describe('the Go tests against a real S3 provider (s3-live)', () => {
  const sh = fs.readFileSync(path.join(CHAIN, 'job', 's3-live.sh'), 'utf8');
  const runMjs = fs.readFileSync(path.join(CHAIN, 'run.mjs'), 'utf8');
  const backend = path.join(REPO, 'backend', 'internal');
  const tests = /^TESTS='([^']+)'$/m.exec(sh)?.[1].split(' ') ?? [];
  const pkgs = /^PKGS='([^']+)'$/m.exec(sh)?.[1].split(' ') ?? [];

  it('names four tests that exist, in the packages it tests', () => {
    expect(tests).toEqual(['TestLiveProviderConformance', 'TestInit_Integration', 'TestB2_OverRealS3', 'TestRenameOnS3_TheReportersSequence']);
    const sources = pkgs
      .map((p) => path.join(REPO, 'backend', p))
      .flatMap((dir) => fs.readdirSync(dir).filter((f) => f.endsWith('_test.go')).map((f) => fs.readFileSync(path.join(dir, f), 'utf8')))
      .join('\n');
    for (const t of tests) expect(sources, t).toContain(`func ${t}(t *testing.T)`);
    // Each reads its server through lives3, region included (lesson #927).
    for (const f of ['storage/drivers/s3/live_conformance_test.go', 'storage/drivers/s3/s3_test.go', 'usage/b2_test.go', 'api/handlers/rename_s3_live_test.go']) {
      expect(fs.readFileSync(path.join(backend, f), 'utf8'), f).toContain('lives3.Config(t, ');
    }
    // TestInit_Integration runs only with INTEGRATION=1.
    expect(sh).toMatch(/^export INTEGRATION=1$/m);
  });

  it('is red when a test only skipped, and red without the settings, never skipped', () => {
    expect(sh).toMatch(/"\$rc" -eq 0 \] && \[ -n "\$notpassed"/);
    expect(sh).toMatch(/summary "s3-live=1 no settings/);
    expect(sh).toMatch(/summary "s3-live=1 missing in the nightly settings/);
    expect(sh).not.toMatch(/exit 0/);
  });

  it('takes the key from a 0600 file of the run and never prints a value', () => {
    // Not `docker run -e`: its arguments are in every process list and in
    // docker inspect. run.mjs writes the file, removes it at the end, and
    // logs names only.
    expect(runMjs).toMatch(/'s3-live\.env'\), file\.text, \{ mode: 0o600 \}/);
    expect(runMjs).toMatch(/for \(const secret of \['ds\.secret', 's3-live\.env'\]\)/);
    expect(runMjs).not.toMatch(/FILEX_TEST_S3_[A-Z_]+: /);
    expect(sh).toContain('CREDS=/w/run/etc/s3-live.env');
    expect(sh).toMatch(/\| redact > "\$OUT\/go\.out"/);
    expect(sh).toContain('ENVIRON["FILEX_TEST_S3_SECRET_KEY"]');
    expect(sh).toContain('ENVIRON["FILEX_TEST_S3_ACCESS_KEY"]');
    expect(sh).not.toMatch(/set -x|echo "\$FILEX_TEST_S3|printenv|env \|/);
  });
});

describe('a browser round cut into parts', () => {
  const cut = buildPlan({
    profile: 'full',
    src: REPO,
    lists: { ...lists, e2e: parseE2eList([1, 2, 3, 4].map((i) => `chromium-${i} local chromium 1 --shard ${i}/4`).join('\n')) },
  });
  const e2eSh = fs.readFileSync(path.join(CHAIN, 'job', 'e2e.sh'), 'utf8');
  const runMjs = fs.readFileSync(path.join(CHAIN, 'run.mjs'), 'utf8');

  it("expects each part to take its share of the engine's minutes", () => {
    for (const t of cut.track) expect(t.expect, t.name).toBe(6);
  });

  it('gives every line a port of its own for the Document Server to call back on', () => {
    expect(cut.track.map((t: { index: number }) => t.index)).toEqual([0, 1, 2, 3]);
    expect(runMjs).toMatch(/PORT: String\(c\.e2ePort \+ \(job\.index \?\? 0\)\)/);
  });

  it("clears and keeps only the part's own output (lesson #1118)", () => {
    // Playwright empties its output directory when a run starts, and e2e/run.mjs
    // gives a part test-results/shard-i-of-N and .artifacts/shard-i-of-N for
    // that reason: a job that cleared the shared directories would delete
    // another part's traces mid-run.
    expect(e2eSh).toContain('rm -rf "e2e/test-results/$LABEL" "e2e/.artifacts/$LABEL"');
    expect(e2eSh).not.toMatch(/rm -rf e2e\/test-results(\s|$)/m);
    expect(e2eSh).not.toMatch(/rm -rf[^\n]*e2e\/\.artifacts(\s|$)/m);
    expect(e2eSh).toMatch(/! -name 'shard-\*'/);
  });
});

describe('run.mjs settings', () => {
  it('reads KEY=VALUE files without expanding anything', () => {
    expect(
      parseEnvFile('# c\nexport A=1\nB="x y"\nC=max(t{chip=~".*nvme.*"})\nD=$HOME\n'),
    ).toEqual({ A: '1', B: 'x y', C: 'max(t{chip=~".*nvme.*"})', D: '$HOME' });
    expect(() => parseEnvFile('not a pair')).toThrow(/KEY=VALUE/);
  });
});

describe('the docker shim of the S3 run', () => {
  const shim = fs.readFileSync(path.join(CHAIN, 'shim', 'docker'), 'utf8');
  const runMjs = fs.readFileSync(path.join(REPO, 'e2e', 'run.mjs'), 'utf8');

  it('expects the credentials e2e/run.mjs starts the gateway with', () => {
    const access = /const access = '([^']+)'/.exec(runMjs)?.[1];
    const secret = /const secret = '([^']+)'/.exec(runMjs)?.[1];
    expect(access && secret).toBeTruthy();
    expect(shim).toContain(`ROOT_ACCESS_KEY=${access}`);
    expect(shim).toContain(`ROOT_SECRET_KEY=${secret}`);
  });

  it('is made executable by the job, not by the checkout', () => {
    // ⚠ The public export does not keep the executable bit (e2e/realenv/run.sh
    // is 100755 here and 100644 there, measured 2026-10-06), and the chain's
    // directory is mounted read-only: a shim put on PATH as it is checked out
    // is "Permission denied" in the public tree, and --s3 fails at its first
    // `docker version`.
    const e2e = fs.readFileSync(path.join(CHAIN, 'job', 'e2e.sh'), 'utf8');
    expect(e2e).toMatch(/chmod 755 \/tmp\/chain-shim\/docker/);
    expect(e2e).not.toMatch(/PATH="\/w\/chain\/shim/);
  });
});
