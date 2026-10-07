// The test shards (tasks #171, #180): the browser suite split by Playwright's
// --shard, each part on a server of its own; the Go suite split by one list,
// scripts/test-shards.json, that the build host's chain, GitHub and CircleCI
// all read.
//
// ⚠ What these hold, and why each one matters:
//   - the parts of a split add up to the whole: a test in no part is a test
//     that stopped running with every runner green;
//   - a Go test file nobody listed still runs (in its package's rest unit),
//     and a package nobody listed still runs (in the package rest): a new
//     file or package must not need an edit of the list to be tested;
//   - two parts on one machine share no output directory: Playwright EMPTIES
//     its output directory when a run starts, so a second part would delete
//     the first one's traces mid-run;
//   - an option run.mjs cannot read stops the run (lib/args.mjs).
//
// The real list is held to the real tree here without a Go toolchain (the
// package list is walked, the tests are read from the sources);
// `node scripts/test-shards.mjs check` asks `go list ./...` and
// `go test -list` themselves.
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

import { unknownOptions, VALUE_OPTIONS } from '../../../e2e/lib/args.mjs';
import {
  artifactsDir,
  localSpecs,
  outputDir,
  parseListing,
  parseShard,
  shardFromArgv,
  shardLabel,
  shardPlaywrightArgs,
  verifyPartition,
} from '../../../e2e/lib/shard.mjs';
import {
  binPack,
  chainLines,
  goTestArgs,
  goTestNames,
  isTestName,
  MAX_RUN_CHARS,
  packageTests,
  parseGoTestTimes,
  planProfile,
  rebalanceUnits,
  runRegex,
  testsReport,
  unionReport,
  validateConfig,
  walkGoPackages,
} from '../../../scripts/lib/test-shards.mjs';

const REPO = path.resolve(__dirname, '../../..');
const E2E = path.join(REPO, 'e2e');
const read = (rel: string) => readFileSync(path.join(REPO, rel), 'utf8');
const CONFIG = JSON.parse(read('scripts/test-shards.json'));
const BACKEND = path.join(REPO, CONFIG.module);

const scratch: string[] = [];
afterAll(() => {
  for (const d of scratch) rmSync(d, { recursive: true, force: true });
});

/** A throwaway Go module: { 'rel/path.go': 'source' }. */
function goTree(files: Record<string, string>): string {
  const dir = mkdtempSync(path.join(tmpdir(), 'filex-shards-'));
  scratch.push(dir);
  writeFileSync(path.join(dir, 'go.mod'), 'module example.com/m\n\ngo 1.25\n');
  for (const [rel, src] of Object.entries(files)) {
    mkdirSync(path.dirname(path.join(dir, rel)), { recursive: true });
    writeFileSync(path.join(dir, rel), src);
  }
  return dir;
}

const testFile = (pkg: string, ...names: string[]) =>
  [`package ${pkg}`, '', 'import "testing"', '', ...names.map((n) => `func ${n}(t *testing.T) {}`), ''].join('\n');

// ── e2e: --shard ────────────────────────────────────────────────────────────

describe('e2e/run.mjs --shard i/N', () => {
  it('reads i/N, 1-based, the way Playwright does', () => {
    expect(parseShard('1/1')).toEqual({ current: 1, total: 1 });
    expect(parseShard('2/4')).toEqual({ current: 2, total: 4 });
    expect(parseShard('4/4')).toEqual({ current: 4, total: 4 });
  });

  it('refuses a shard it cannot read, instead of running the whole suite', () => {
    for (const bad of ['0/4', '5/4', '4', 'a/b', '1/0', '1/65', '', '2/4/1', '-1/4']) {
      expect(() => parseShard(bad), bad).toThrow(/--shard/);
    }
  });

  it('takes --shard i/N and --shard=i/N, and refuses it empty or twice', () => {
    expect(shardFromArgv(['--binary', 'bin/filex'])).toBeNull();
    expect(shardFromArgv(['--shard', '2/4'])).toEqual({ current: 2, total: 4 });
    expect(shardFromArgv(['--keep', '--shard=3/4'])).toEqual({ current: 3, total: 4 });
    expect(() => shardFromArgv(['--shard'])).toThrow();
    expect(() => shardFromArgv(['--shard', '--keep'])).toThrow();
    expect(() => shardFromArgv(['--shard', '1/4', '--shard', '2/4'])).toThrow(/once/);
  });

  it('is an option run.mjs knows (an unknown one stops the run)', () => {
    expect(VALUE_OPTIONS).toContain('shard');
    expect(unknownOptions(['--shard', '2/4', '--binary', 'bin/filex'])).toEqual([]);
    expect(unknownOptions(['--shard=2/4'])).toEqual([]);
  });

  it('a part keeps its output and its server log apart from every other part', () => {
    const parts = [1, 2, 3, 4].map((current) => ({ current, total: 4 }));
    const outs = parts.map((s) => outputDir(E2E, s));
    const logs = parts.map((s) => artifactsDir(E2E, s));
    expect(new Set(outs).size).toBe(4);
    expect(new Set(logs).size).toBe(4);
    expect(shardLabel({ current: 2, total: 4 })).toBe('shard-2-of-4');
    // Under the directories CI already uploads, so nothing new to collect.
    for (const o of outs) expect(o.startsWith(path.join(E2E, 'test-results') + path.sep)).toBe(true);
    for (const l of logs) expect(l.startsWith(path.join(E2E, '.artifacts') + path.sep)).toBe(true);
  });

  it('without --shard nothing changes: the same directories, no extra Playwright argument', () => {
    expect(outputDir(E2E, null)).toBe(path.join(E2E, 'test-results'));
    expect(artifactsDir(E2E, null)).toBe(path.join(E2E, '.artifacts'));
    expect(shardPlaywrightArgs(E2E, null)).toEqual([]);
  });

  it('hands Playwright its own --shard and an output directory per part', () => {
    expect(shardPlaywrightArgs(E2E, { current: 2, total: 4 })).toEqual([
      '--shard=2/4',
      `--output=${path.join(E2E, 'test-results', 'shard-2-of-4')}`,
    ]);
  });

  it('run.mjs wires it: reads it, refuses it outside local, passes it on, keeps the log per part', () => {
    const src = read('e2e/run.mjs');
    expect(src).toContain("from './lib/shard.mjs'");
    expect(src).toMatch(/SHARD = shardFromArgv\(argv\.slice\(1\)\)/);
    expect(src).toMatch(/if \(SHARD && profile !== 'local'\)[\s\S]{0,200}process\.exit\(2\)/);
    expect(src).toContain('args.push(...shardPlaywrightArgs(E2E_DIR, SHARD))');
    expect(src).toContain('artifactsDir(E2E_DIR, SHARD)');
    // The spec list is the one e2e-check lists too.
    expect(src).toContain('localSpecs(E2E_DIR)');
    expect(read('scripts/test-shards.mjs')).toMatch(/import \{[^}]*localSpecs[^}]*\} from '\.\.\/e2e\/lib\/shard\.mjs'/);
  });

  it('run.mjs tries another port when a part loses its port, but never one --port named', () => {
    const src = read('e2e/run.mjs');
    expect(src).toMatch(/const attempts = fixed \? 1 : 3/);
    expect(src).toMatch(/if \(!\(err instanceof PortTaken\)\) throw err/);
    expect(src).toMatch(/BIND_FAILURE\.test\(serverLog\(\)\)[\s\S]{0,120}throw new PortTaken/);
  });

  it('lists the same specs run.mjs has always run: all but the deployment smoke, sorted', () => {
    const specs = localSpecs(E2E);
    expect(specs.length).toBeGreaterThan(50);
    expect(specs.some((f: string) => f.startsWith('90-deployment-smoke'))).toBe(false);
    expect([...specs].sort()).toEqual(specs);
  });
});

describe('e2e-check: do the parts add up to the whole?', () => {
  const listing = (...tests: string[]) =>
    ['Listing tests:', ...tests.map((t) => `  ${t}`), `Total: ${tests.length} ${tests.length === 1 ? 'test' : 'tests'} in 1 file`].join('\n');

  it('reads a `playwright test --list` listing', () => {
    const got = parseListing(listing('[chromium] › tests/00-smoke.spec.ts:3:1 › one', '[chromium] › tests/00-smoke.spec.ts:9:1 › two'));
    expect(got.total).toBe(2);
    expect(got.tests).toEqual(['[chromium] › tests/00-smoke.spec.ts:3:1 › one', '[chromium] › tests/00-smoke.spec.ts:9:1 › two']);
    expect(parseListing('Listing tests:\nTotal: 0 tests in 0 files\n')).toEqual({ tests: [], total: 0 });
  });

  const whole = Array.from({ length: 10 }, (_, i) => `t${i}`);

  it('a split where every test is in exactly one part is OK', () => {
    const v = verifyPartition(whole, [whole.slice(0, 3), whole.slice(3, 5), whole.slice(5, 8), whole.slice(8)]);
    expect(v.ok).toBe(true);
    expect(v.sum).toBe(10);
  });

  it('a test in no part is caught', () => {
    const v = verifyPartition(whole, [whole.slice(0, 3), whole.slice(4)]);
    expect(v.ok).toBe(false);
    expect(v.missing).toEqual(['t3']);
  });

  it('a test in two parts is caught, even when the sum would hide a missing one', () => {
    const v = verifyPartition(whole, [whole.slice(0, 4), [...whole.slice(3, 9)]]);
    expect(v.ok).toBe(false);
    expect(v.twice).toEqual(['t3 (parts 1, 2)']);
    expect(v.missing).toEqual(['t9']);
  });

  it('a test that is in a part and not in the whole run is caught', () => {
    const v = verifyPartition(whole, [whole.slice(0, 5), [...whole.slice(5), 'stray']]);
    expect(v.ok).toBe(false);
    expect(v.extra).toEqual(['stray']);
  });
});

// ── Go: reading the tests ───────────────────────────────────────────────────

describe('Go test names, as go test sees them', () => {
  it("follows Go's rule: Test, Example, Fuzz, then nothing or not a lower-case letter", () => {
    for (const ok of ['Test', 'TestX', 'Test_x', 'Test2', 'Example', 'ExampleFoo', 'Example_suffix', 'FuzzParse']) {
      expect(isTestName(ok), ok).toBe(true);
    }
    for (const no of ['Testable', 'Exampled', 'Fuzzy', 'testX', 'Helper', 'BenchmarkX']) {
      expect(isTestName(no), no).toBe(false);
    }
  });

  it('reads top-level tests only: no TestMain, no method, nothing inside a string or a comment', () => {
    const src = [
      'package x',
      '',
      'import "testing"',
      '',
      'func TestMain(m *testing.M) { m.Run() }',
      'func TestOne(t *testing.T) {}',
      'func Test_two(t *testing.T) {}',
      'func Testable() bool { return true }',
      'func (s *suite) TestMethod(t *testing.T) {}',
      'func ExampleOne() {}',
      'func FuzzOne(f *testing.F) {}',
      '// func TestInAComment(t *testing.T) {}',
      '/*',
      'func TestInABlock(t *testing.T) {}',
      '*/',
      'const prog = `package main',
      'func TestInARawString(t *testing.T) {}',
      '`',
      'var s = "func TestInAString(t *testing.T) {}"',
      'func testHelper(t *testing.T) {}',
      '',
    ].join('\n');
    expect(goTestNames(src)).toEqual(['TestOne', 'Test_two', 'ExampleOne', 'FuzzOne']);
  });

  it('reads CRLF sources the same', () => {
    expect(goTestNames(testFile('x', 'TestA', 'TestB').replace(/\n/g, '\r\n'))).toEqual(['TestA', 'TestB']);
  });

  it('names tests exactly: a -run group never takes a test whose name merely starts the same', () => {
    const re = new RegExp(runRegex(['TestA', 'TestB']));
    expect(re.test('TestA')).toBe(true);
    expect(re.test('TestB')).toBe(true);
    expect(re.test('TestAB')).toBe(false);
    expect(re.test('XTestA')).toBe(false);
    expect(runRegex([])).toBe('');
  });
});

// ── Go: the real list against the real tree ─────────────────────────────────

describe('scripts/test-shards.json, held to the tree', () => {
  const goPackages = walkGoPackages(BACKEND);
  const sharded = Object.keys(CONFIG.units);
  const parsed = new Map(sharded.map((pkg) => [pkg, packageTests(BACKEND, pkg)]));

  it('has a sound shape', () => {
    expect(validateConfig(CONFIG)).toEqual([]);
  });

  it('cuts the two packages that outrun every other one', () => {
    expect(sharded.sort()).toEqual(['./internal/api/handlers', './internal/wasmplugin']);
    expect(goPackages.length).toBeGreaterThan(100);
  });

  it('names no test file that is not on disk (a rename moves its tests to the rest, unbalanced)', () => {
    for (const profile of Object.keys(CONFIG.profiles)) {
      const plan = planProfile(CONFIG, profile, { backendDir: BACKEND });
      expect(Object.fromEntries(plan.notes.stale), profile).toEqual({});
    }
  });

  for (const profile of Object.keys(CONFIG.profiles)) {
    describe(`profile ${profile}`, () => {
      const plan = planProfile(CONFIG, profile, { backendDir: BACKEND });

      it('runs every package exactly once, and none that does not exist', () => {
        expect(unionReport(plan, goPackages)).toEqual({ missing: [], extra: [], twice: [] });
      });

      it('runs every test of a cut package in exactly one shard, and no shard runs nothing', () => {
        const r = testsReport(plan, parsed);
        expect(r).toEqual({ unrun: [], twice: [], empty: [], tooLong: [] });
        for (const pkg of sharded) {
          const all = new Set([...parsed.get(pkg)!.values()].flat());
          const ran = plan.shards.filter((s: { run: string; packages: string[]; tests: number }) => s.run && s.packages[0] === pkg).reduce((n: number, s: { tests: number }) => n + s.tests, 0);
          expect(ran, pkg).toBe(all.size);
        }
      });

      it('keeps every -run regex short enough to pass as one argument', () => {
        for (const s of plan.shards) expect(s.run.length, s.name).toBeLessThanOrEqual(MAX_RUN_CHARS);
      });

      it("prints lines scripts/chain's lists can read: a regex names exactly one package", () => {
        for (const line of chainLines(plan)) {
          const [name, run, ...pkgs] = line.split(' ');
          expect(name).toMatch(/^[a-z0-9][a-z0-9-]*$/);
          if (run !== '-') {
            expect(pkgs, name).toHaveLength(1);
            expect(pkgs[0]).not.toContain('...');
          }
        }
      });
    });
  }
});

// ── Go: the rest units, on a tree of our own ────────────────────────────────

describe('a new test file, a new package: they run without an edit of the list', () => {
  const tree = () =>
    goTree({
      'internal/big/a_test.go': testFile('big', 'TestA1', 'TestA2'),
      'internal/big/b_test.go': testFile('big', 'TestB1'),
      'internal/big/c_test.go': testFile('big', 'TestNewInAnUnlistedFile'),
      'internal/big/big.go': 'package big\n',
      'internal/db/db.go': 'package db\n',
      'internal/fresh/fresh.go': 'package fresh\n',
      'cmd/x/main.go': 'package main\n',
    });
  const cfg = {
    module: '.',
    units: {
      './internal/big': [
        { name: 'big-1', files: ['a_test.go'] },
        { name: 'big-2', rest: true, files: ['b_test.go'] },
      ],
    },
    packages: [
      { name: 'db', packages: ['./internal/db'] },
      { name: 'rest', rest: true },
    ],
    profiles: {
      race: [
        { name: 'big-1', units: ['big-1'] },
        { name: 'big-2', units: ['big-2'] },
        { name: 'db', units: ['db'] },
        { name: 'rest', units: ['rest'] },
      ],
    },
  };

  it('a test file no unit lists runs in the rest unit of its package', () => {
    const dir = tree();
    const plan = planProfile(cfg, 'race', { backendDir: dir });
    const rest = plan.shards.find((s: { name: string }) => s.name === 'big-2');
    expect(rest.files).toEqual(['b_test.go', 'c_test.go']);
    expect(new RegExp(rest.run).test('TestNewInAnUnlistedFile')).toBe(true);
    expect(Object.fromEntries(plan.notes.unplaced)).toEqual({ './internal/big': ['c_test.go'] });
    expect(testsReport(plan, new Map([['./internal/big', packageTests(dir, './internal/big')]])).unrun).toEqual([]);
  });

  it('a package no unit names runs in the package rest', () => {
    const dir = tree();
    const plan = planProfile(cfg, 'race', { backendDir: dir });
    const rest = plan.shards.find((s: { name: string }) => s.name === 'rest');
    expect(rest.packages).toEqual(['./...']);
    expect(rest.exclude).toEqual(['./internal/big', './internal/db']);
    const goPackages = walkGoPackages(dir);
    expect(goPackages).toEqual(['./cmd/x', './internal/big', './internal/db', './internal/fresh']);
    expect(goTestArgs(rest, goPackages)).toEqual(['./cmd/x', './internal/fresh']);
    expect(unionReport(plan, goPackages)).toEqual({ missing: [], extra: [], twice: [] });
  });

  it('a cut shard hands go test its -run and its one package', () => {
    const dir = tree();
    const plan = planProfile(cfg, 'race', { backendDir: dir });
    const one = plan.shards.find((s: { name: string }) => s.name === 'big-1');
    expect(goTestArgs(one, [])).toEqual(['-run', '^(TestA1|TestA2)$', './internal/big']);
    expect(chainLines({ shards: [one] })).toEqual(['big-1 ^(TestA1|TestA2)$ ./internal/big']);
    const rest = plan.shards.find((s: { name: string }) => s.name === 'rest');
    expect(chainLines({ shards: [rest] })).toEqual(['rest - ./... !./internal/big !./internal/db']);
  });

  it('a name the list gives that the tree does not have is reported, not run blind', () => {
    const dir = tree();
    const stale = { ...cfg, units: { './internal/big': [{ name: 'big-1', files: ['a_test.go', 'gone_test.go'] }, { name: 'big-2', rest: true, files: ['b_test.go'] }] } };
    const plan = planProfile(stale, 'race', { backendDir: dir });
    expect(Object.fromEntries(plan.notes.stale)).toEqual({ './internal/big': ['gone_test.go'] });
  });
});

describe('the checks are red when the list and the tree disagree', () => {
  const shard = (name: string, packages: string[], extra: Record<string, unknown> = {}) => ({ name, units: [name], packages, exclude: [], run: '', ...extra });

  it('a package in no shard, a package go list does not know, a package run twice', () => {
    const plan = { shards: [shard('a', ['./a']), shard('c', ['./c']), shard('a2', ['./a'])] };
    expect(unionReport(plan, ['./a', './b'])).toEqual({ missing: ['./b'], extra: ['./c'], twice: ['./a (a, a2)'] });
  });

  it('a cut package that a package shard runs whole as well', () => {
    const plan = { shards: [shard('big-1', ['./big'], { run: '^(TestA)$' }), shard('rest', ['./...'])] };
    expect(unionReport(plan, ['./big', './x']).twice).toEqual(['./big (rest, its units)']);
  });

  it('a test the compiler knows and no shard names (go test -list against the sources)', () => {
    const plan = { shards: [shard('big-1', ['./big'], { run: '^(TestA)$' })] };
    const parsed = new Map([['./big', new Map([['a_test.go', ['TestA']]])]]);
    const authoritative = new Map([['./big', ['TestA', 'TestDeclaredSomeOtherWay']]]);
    expect(testsReport(plan, parsed, authoritative).unrun).toEqual(['./big TestDeclaredSomeOtherWay']);
  });

  it('a test in two shards, a shard that runs nothing, a regex too long to pass', () => {
    const long = `^(${Array.from({ length: 5000 }, (_, i) => `TestAVeryLongNameNumber${i}`).join('|')})$`;
    const plan = {
      shards: [
        shard('one', ['./big'], { run: '^(TestA|TestB)$' }),
        shard('two', ['./big'], { run: '^(TestB)$' }),
        shard('none', ['./big'], { run: '^()$' }),
        shard('long', ['./other'], { run: long }),
      ],
    };
    const r = testsReport(plan, new Map());
    expect(r.twice).toEqual(['./big TestB (one, two)']);
    expect(r.empty).toEqual(['none']);
    expect(r.tooLong).toEqual([`long (${long.length} characters)`]);
  });

  it("the list's own shape: two rests, a file in two units, a unit no profile runs, a shard mixing kinds", () => {
    const bad = {
      module: 'backend',
      units: {
        './internal/big': [
          { name: 'big-1', rest: true, files: ['a_test.go'] },
          { name: 'big-2', rest: true, files: ['a_test.go'] },
        ],
      },
      packages: [{ name: 'db', packages: ['./internal/db', './internal/big'] }],
      profiles: { race: [{ name: 'mixed', units: ['big-1', 'db'] }] },
    };
    const errors = validateConfig(bad).join('\n');
    expect(errors).toMatch(/units \.\/internal\/big: exactly one unit is the rest/);
    expect(errors).toMatch(/a_test\.go is in big-1 and big-2/);
    expect(errors).toMatch(/packages: exactly one unit is the rest/);
    expect(errors).toMatch(/\.\/internal\/big is a sharded package/);
    expect(errors).toMatch(/the unit big-2 runs in no shard/);
    expect(errors).toMatch(/mixed: a shard runs the units of ONE sharded package/);
  });
});

// ── Go: timings and rebalancing ─────────────────────────────────────────────

describe('rebalance: from the times a run measured', () => {
  it('reads -v and -json output, top-level tests only, the longest time when seen twice', () => {
    const v = ['=== RUN   TestA', '--- PASS: TestA (1.50s)', '    --- PASS: TestA/sub (1.00s)', '--- FAIL: TestB (0.25s)', '--- PASS: TestA (2.00s)'].join('\n');
    expect(Object.fromEntries(parseGoTestTimes(v))).toEqual({ TestA: 2, TestB: 0.25 });
    const json = [
      '{"Action":"pass","Package":"m/internal/api/handlers","Test":"TestC","Elapsed":3.5}',
      '{"Action":"pass","Package":"m/internal/api/handlers","Test":"TestC/sub","Elapsed":3}',
      '{"Action":"pass","Package":"m/internal/other","Test":"TestD","Elapsed":9}',
      '{"Action":"output","Package":"m/internal/api/handlers","Test":"TestC","Output":"x"}',
      '{"Action":"pass","Package":"m/internal/api/handlers","Elapsed":12}',
    ].join('\n');
    expect(Object.fromEntries(parseGoTestTimes(json, 'internal/api/handlers'))).toEqual({ TestC: 3.5 });
  });

  it('packs heaviest first into the lightest bin, the same way every time', () => {
    const items = [
      { key: 'a', weight: 5 },
      { key: 'b', weight: 4 },
      { key: 'c', weight: 3 },
      { key: 'd', weight: 3 },
      { key: 'e', weight: 1 },
    ];
    const bins = binPack(items, 2);
    expect(bins.map((b: { total: number }) => b.total).sort()).toEqual([8, 8]);
    expect(binPack(items, 2)).toEqual(bins);
  });

  it('places every file on disk, drops stale names, keeps the units and the rest', () => {
    const dir = goTree({
      'internal/big/a_test.go': testFile('big', 'TestA'),
      'internal/big/b_test.go': testFile('big', 'TestB'),
      'internal/big/c_test.go': testFile('big', 'TestC'),
      'internal/big/d_test.go': testFile('big', 'TestD'),
    });
    const cfg = {
      units: {
        './internal/big': [
          { name: 'big-1', files: ['a_test.go', 'gone_test.go'] },
          { name: 'big-2', rest: true, files: ['b_test.go'] },
        ],
      },
    };
    const weights = new Map([
      ['TestA', 10],
      ['TestB', 9],
      ['TestC', 2],
    ]);
    const { units, totals } = rebalanceUnits(cfg, './internal/big', { backendDir: dir, weights });
    expect(units.map((u: { name: string }) => u.name)).toEqual(['big-1', 'big-2']);
    expect(units[1].rest).toBe(true);
    expect(units.flatMap((u: { files: string[] }) => u.files).sort()).toEqual(['a_test.go', 'b_test.go', 'c_test.go', 'd_test.go']);
    // TestD has no time: it counts as the median (9), so 10+2 | 9+9.
    expect(totals).toEqual([18, 12]);
  });
});
