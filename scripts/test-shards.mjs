#!/usr/bin/env node
// The test shards: one list, every runner (task #171, #180).
//
// The Go shards live in scripts/test-shards.json (scripts/lib/test-shards.mjs
// explains its units, rests and profiles); the browser suite is split by
// Playwright itself (`node e2e/run.mjs local --shard i/N`, e2e/lib/shard.mjs).
// The build host's chain, GitHub Actions and CircleCI all ask this script, so
// they run the same shards.
//
//   node scripts/test-shards.mjs go [--profile go|race] [--format json|chain|matrix|args] [--shard <name> | --node <i/N>]
//       The shards of a profile. json (default): every shard with its
//       packages and -run regex. chain: the line format the build host's
//       chain reads its Go and -race jobs in (scripts/chain/plan.mjs, its
//       default source). matrix: {"include":[{"shard":…}]} for a CI matrix.
//       args: the arguments ONE shard (--shard, or --node) adds to `go test`,
//       one per line (`mapfile -t A < <(…); go test -race "${A[@]}"`); a
//       shard that holds the package rest asks `go list ./...` for it.
//       --node i/N picks the shard by position instead of by name: copy i of
//       a CI job run N times side by side (CircleCI's parallelism, GitLab's
//       parallel), refused unless N is the profile's number of shards.
//
//   node scripts/test-shards.mjs check [--offline]
//       Holds the list to the tree, red (exit 1) on any of:
//         - the list's shape (scripts/lib/test-shards.mjs validateConfig);
//         - per profile, the packages it runs are not exactly `go list ./...`
//           (a package missing, one it names that does not exist, one run twice);
//         - a test `go test -list` knows that no shard runs, a test two
//           shards run, a shard that runs no test, a -run regex too long to pass;
//         - a test file the list names that is not on disk (renamed or deleted).
//       A test file the list does not name is not red: it runs in its
//       package's rest unit, and is reported so the next rebalance places it.
//       --offline asks no Go toolchain: the package list is walked from the
//       tree (an approximation) and the tests are read from the sources.
//
//   node scripts/test-shards.mjs e2e-check [--shards 4] [--browsers chromium,firefox,webkit]
//                                          [--grep <p>] [--grep-invert <p>]
//       Lists the Playwright suite (`playwright test --list`, no server, no
//       browser) whole and once per part, and is red unless the parts add up
//       to the whole with every test in exactly one part.
//
//   node scripts/test-shards.mjs rebalance --package ./internal/api/handlers --from <log> [--write]
//       New file lists for a sharded package's units, from the time each test
//       took in <log> (`go test -json` or `-v` output of that package; the
//       chain's -race shards write -v). Prints the split; --write saves it.
//
// Go runs natively where there is a Go on PATH, else through WSL from WSL's
// own disk (scripts/lib/go-build.mjs wslMirrorCd: never on /mnt/<drive>).

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { localSpecs, parseListing, verifyPartition, MAX_SHARDS } from '../e2e/lib/shard.mjs';
import { nativeGo, wslGo, wslMirrorCd } from './lib/go-build.mjs';
import {
  chainLines,
  goTestArgs,
  modulePath,
  packageTests,
  parseGoTestTimes,
  planProfile,
  rebalanceUnits,
  relPackage,
  shardAtNode,
  testsReport,
  unionReport,
  validateConfig,
  walkGoPackages,
} from './lib/test-shards.mjs';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const CONFIG = path.join(REPO, 'scripts', 'test-shards.json');

const argv = process.argv.slice(2);
const command = argv[0];
const VALUES = ['profile', 'format', 'shard', 'node', 'shards', 'browsers', 'grep', 'grep-invert', 'package', 'from'];
const FLAGS = ['offline', 'write'];

function usage(code) {
  const out = code ? console.error : console.log;
  out('usage: node scripts/test-shards.mjs <go|check|e2e-check|rebalance> [options]');
  out('  go         [--profile go|race] [--format json|chain|matrix|args] [--shard <name> | --node <i/N>]');
  out('  check      [--offline]');
  out('  e2e-check  [--shards 4] [--browsers chromium[,firefox,webkit]] [--grep <p>] [--grep-invert <p>]');
  out('  rebalance  --package ./internal/api/handlers --from <go test -json or -v log> [--write]');
  out('(the header of scripts/test-shards.mjs says what each one does)');
  process.exit(code);
}

if (!command || command === '--help' || command === '-h') usage(command ? 0 : 2);

// ⚠ An option this script does not know is an error, not a no-op: the reason
// e2e/lib/args.mjs gives for run.mjs holds here too.
const opts = {};
for (let i = 1; i < argv.length; i++) {
  const a = argv[i];
  const [key, inline] = a.startsWith('--') ? a.slice(2).split(/=(.*)/s) : [null, null];
  if (key && VALUES.includes(key)) {
    const v = inline ?? argv[++i];
    if (v === undefined || (inline === undefined && v.startsWith('--'))) {
      console.error(`--${key} needs a value`);
      process.exit(2);
    }
    opts[key] = v;
  } else if (key && FLAGS.includes(key) && inline === undefined) {
    opts[key] = true;
  } else {
    console.error(`unknown argument ${a}`);
    usage(2);
  }
}

const loadConfig = () => JSON.parse(fs.readFileSync(CONFIG, 'utf8'));
const backendOf = (cfg) => path.join(REPO, cfg.module);

const shq = (s) => `'${String(s).split("'").join(`'"'"'`)}'`;

/** A function running `go <args>` in the backend module, or null when there is no Go. */
function goRunner(backendDir) {
  const spawnOpts = { encoding: 'utf8', maxBuffer: 256 * 1024 * 1024, windowsHide: true };
  if (nativeGo()) return (args) => spawnSync('go', args, { cwd: backendDir, ...spawnOpts });
  if (wslGo()) {
    return (args) =>
      spawnSync('wsl', ['-e', 'bash', '-lc', `${wslMirrorCd(backendDir)} && go ${args.map(shq).join(' ')}`], spawnOpts);
  }
  return null;
}

function mustGo(backendDir) {
  const go = goRunner(backendDir);
  if (!go) {
    throw new Error(
      'no Go toolchain: `go` is not on this PATH' +
        (process.platform === 'win32' ? ', and `wsl -e bash -lc "go version"` failed too' : '') +
        '. Install Go (backend/go.mod names the version), or use `check --offline`.',
    );
  }
  return go;
}

/** `go list ./...`, as ./paths. */
function goListPackages(go, backendDir) {
  const r = go(['list', './...']);
  if (r.status !== 0) throw new Error(`go list ./... failed (exit ${r.status})\n${String(r.stderr).trim().split('\n').slice(-20).join('\n')}`);
  const module = modulePath(backendDir);
  return String(r.stdout)
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean)
    .map((p) => relPackage(p, module))
    .sort();
}

/**
 * `go test -list .` on the sharded packages: the tests, examples and fuzz
 * targets the compiler sees, per package (benchmarks left out: go test does
 * not run them without -bench). Compiles the test binaries, runs no test.
 */
function goTestList(go, backendDir, pkgs) {
  const r = go(['test', '-count=1', '-list', '.', ...pkgs]);
  if (r.status !== 0) {
    throw new Error(`go test -list failed (exit ${r.status})\n${`${r.stdout}\n${r.stderr}`.trim().split('\n').slice(-30).join('\n')}`);
  }
  const module = modulePath(backendDir);
  const byPkg = new Map();
  let pending = [];
  for (const raw of String(r.stdout).split(/\r?\n/)) {
    const line = raw.trim();
    const done = /^(ok|\?)\s+(\S+)/.exec(line);
    if (done) {
      byPkg.set(relPackage(done[2], module), pending);
      pending = [];
      continue;
    }
    if (/^(Test|Example|Fuzz)[A-Za-z0-9_]*$/.test(line)) pending.push(line);
  }
  return byPkg;
}

// ── go ──────────────────────────────────────────────────────────────────────

function cmdGo() {
  const cfg = loadConfig();
  const backendDir = backendOf(cfg);
  const profile = opts.profile ?? 'go';
  const format = opts.format ?? 'json';
  const plan = planProfile(cfg, profile, { backendDir });
  for (const [pkg, files] of plan.notes.stale) console.error(`warning: ${pkg}: the list names test files that are not on disk: ${files.join(', ')}`);
  for (const [pkg, files] of plan.notes.unplaced) console.error(`note: ${pkg}: ${files.length} test file(s) no unit lists run in its rest unit: ${files.join(', ')}`);
  let shards = plan.shards;
  if (opts.shard && opts.node) throw new Error('--shard and --node both choose the shard: give one of them');
  if (opts.shard) {
    shards = shards.filter((s) => s.name === opts.shard);
    if (shards.length === 0) throw new Error(`profile ${profile} has no shard "${opts.shard}" (it has ${plan.shards.map((s) => s.name).join(', ')})`);
  }
  // A CI job's copy i of N (task #181): the job holds a count, the list here
  // holds the shards. The shard's name goes to stderr for the job's log.
  if (opts.node) {
    shards = [shardAtNode(plan.shards, opts.node, profile)];
    console.error(`node ${opts.node}: shard ${shards[0].name}`);
  }
  if (format === 'json') {
    const out = {
      profile,
      module: cfg.module,
      shards: shards.map((s) => ({
        name: s.name,
        units: s.units,
        packages: s.packages,
        exclude: s.exclude,
        run: s.run,
        ...(s.run ? { tests: s.tests, test_files: s.files.length } : {}),
      })),
    };
    process.stdout.write(`${JSON.stringify(out, null, 2)}\n`);
  } else if (format === 'chain') {
    process.stdout.write(`${chainLines({ shards }).join('\n')}\n`);
  } else if (format === 'matrix') {
    process.stdout.write(`${JSON.stringify({ include: shards.map((s) => ({ shard: s.name })) })}\n`);
  } else if (format === 'args') {
    if (!opts.shard && !opts.node) throw new Error('--format args is the arguments of ONE shard: add --shard <name> or --node <i/N>');
    const s = shards[0];
    const goPackages = s.packages.includes('./...') ? goListPackages(mustGo(backendDir), backendDir) : [];
    process.stdout.write(`${goTestArgs(s, goPackages).join('\n')}\n`);
  } else {
    throw new Error(`--format ${format}: json, chain, matrix or args`);
  }
  return 0;
}

// ── check ───────────────────────────────────────────────────────────────────

function cmdCheck() {
  const cfg = loadConfig();
  const backendDir = backendOf(cfg);
  const red = [];
  const say = (msg) => console.log(msg);

  const shape = validateConfig(cfg);
  if (shape.length) {
    for (const e of shape) red.push(`list: ${e}`);
    return report(red);
  }

  let goPackages;
  let authoritative = new Map();
  const shardedPkgs = Object.keys(cfg.units);
  if (opts.offline) {
    goPackages = walkGoPackages(backendDir);
    say(`packages: ${goPackages.length} (walked from the tree, --offline: an approximation of go list ./...)`);
  } else {
    const go = mustGo(backendDir);
    goPackages = goListPackages(go, backendDir);
    say(`packages: ${goPackages.length} (go list ./...)`);
    authoritative = goTestList(go, backendDir, shardedPkgs);
    for (const pkg of shardedPkgs) say(`${pkg}: ${authoritative.get(pkg)?.length ?? 0} tests (go test -list)`);
  }

  const parsed = new Map(shardedPkgs.map((pkg) => [pkg, packageTests(backendDir, pkg)]));
  for (const profile of Object.keys(cfg.profiles)) {
    const plan = planProfile(cfg, profile, { backendDir });
    say(`\nprofile ${profile}: ${plan.shards.length} shards`);
    for (const s of plan.shards) {
      const what = s.run ? `${s.tests} tests in ${s.files.length} files of ${s.packages[0]}` : s.packages.includes('./...') ? `./... less ${s.exclude.length} packages` : `${s.packages.length} packages`;
      say(`  ${s.name.padEnd(18)} ${what}`);
    }
    const union = unionReport(plan, goPackages);
    for (const p of union.missing) red.push(`${profile}: ${p} runs in no shard`);
    for (const p of union.extra) red.push(`${profile}: ${p} is named by a shard, and go list ./... does not know it`);
    for (const p of union.twice) red.push(`${profile}: ${p} runs twice`);
    const tests = testsReport(plan, parsed, authoritative);
    for (const t of tests.unrun) red.push(`${profile}: ${t} runs in no shard (the sources do not show it as a test of a listed or unlisted file - read how it is declared)`);
    for (const t of tests.twice) red.push(`${profile}: ${t} runs in two shards`);
    for (const s of tests.empty) red.push(`${profile}: the shard ${s} runs no test (a shard that runs nothing reads as a pass)`);
    for (const s of tests.tooLong) red.push(`${profile}: the -run regex of ${s} is too long to pass as one argument; split the unit`);
    if (profile === Object.keys(cfg.profiles)[0]) {
      for (const [pkg, files] of plan.notes.stale) red.push(`${pkg}: the list names test files that are not on disk (renamed or deleted?): ${files.join(', ')}`);
      for (const [pkg, files] of plan.notes.unplaced) say(`note: ${pkg}: ${files.length} test file(s) no unit lists run in its rest unit: ${files.join(', ')}`);
    }
  }
  return report(red);
}

function report(red) {
  if (red.length === 0) {
    console.log('\ntest shards: OK');
    return 0;
  }
  console.error(`\ntest shards: ${red.length} problem(s)`);
  for (const r of red) console.error(`  ✗ ${r}`);
  console.error('\nscripts/test-shards.json is the list; docs/CONTRIBUTING.md (Testing → Shards) says how to change it.');
  return 1;
}

// ── e2e-check ───────────────────────────────────────────────────────────────

function cmdE2eCheck() {
  const total = Number(opts.shards ?? 4);
  if (!Number.isInteger(total) || total < 1 || total > MAX_SHARDS) throw new Error(`--shards ${opts.shards}: 1 to ${MAX_SHARDS}`);
  const e2eDir = path.join(REPO, 'e2e');
  let cli;
  try {
    cli = createRequire(path.join(e2eDir, 'package.json')).resolve('@playwright/test/cli');
  } catch {
    throw new Error(`Playwright is not installed in ${e2eDir}. Run: cd ${REPO} && pnpm install --force`);
  }
  const specs = localSpecs(e2eDir).map((f) => `tests/${f}`);
  const filters = [
    ...(opts.grep ? ['--grep', opts.grep] : []),
    ...(opts['grep-invert'] ? ['--grep-invert', opts['grep-invert']] : []),
  ];
  const env = {
    ...process.env,
    ...(opts.browsers ? { E2E_BROWSERS: opts.browsers } : {}),
    // Listing loads the config and the specs; nothing is reached.
    E2E_BASE_URL: process.env.E2E_BASE_URL ?? 'http://127.0.0.1:5212',
  };
  const list = (extra) => {
    // node + the CLI's file, no shell: the arguments reach Playwright as they are.
    const r = spawnSync(process.execPath, [cli, 'test', ...specs, '--list', '--reporter=list', ...filters, ...extra], {
      cwd: e2eDir,
      env,
      encoding: 'utf8',
      maxBuffer: 256 * 1024 * 1024,
      windowsHide: true,
    });
    if (r.status !== 0) throw new Error(`playwright test --list ${extra.join(' ')} failed (exit ${r.status})\n${String(r.stderr || r.stdout).trim().slice(-3000)}`);
    const got = parseListing(r.stdout);
    if (got.total === null || got.total !== got.tests.length) {
      throw new Error(`could not read the listing of ${extra.join(' ') || 'the whole suite'}: Total ${got.total}, ${got.tests.length} test lines`);
    }
    return got.tests;
  };

  console.log(`e2e: ${specs.length} spec files, engines ${env.E2E_BROWSERS || 'chromium'}${filters.length ? `, ${filters.join(' ')}` : ''}`);
  const whole = list([]);
  console.log(`  whole run   ${whole.length} tests`);
  const parts = [];
  for (let i = 1; i <= total; i++) {
    const part = list([`--shard=${i}/${total}`]);
    parts.push(part);
    console.log(`  part ${`${i}/${total}`.padEnd(6)} ${part.length} tests`);
  }
  const v = verifyPartition(whole, parts);
  console.log(`  parts sum   ${v.sum} tests`);
  if (v.ok) {
    console.log(`\ne2e shards: OK - ${total} parts, ${v.sum} tests, each in exactly one part`);
    return 0;
  }
  console.error(`\ne2e shards: the parts do not add up to the whole run (${v.sum} vs ${v.total})`);
  for (const t of v.missing.slice(0, 50)) console.error(`  ✗ in no part: ${t}`);
  for (const t of v.extra.slice(0, 50)) console.error(`  ✗ in a part, not in the whole run: ${t}`);
  for (const t of v.twice.slice(0, 50)) console.error(`  ✗ in two parts: ${t}`);
  return 1;
}

// ── rebalance ───────────────────────────────────────────────────────────────

function cmdRebalance() {
  if (!opts.package || !opts.from) throw new Error('rebalance needs --package ./internal/x and --from <go test -json or -v log>');
  const cfg = loadConfig();
  const backendDir = backendOf(cfg);
  const pkg = opts.package;
  const weights = parseGoTestTimes(fs.readFileSync(opts.from, 'utf8'), pkg.replace(/^\.\//, ''));
  if (weights.size === 0) throw new Error(`${opts.from}: no test time in it (a go test -json or -v log of ${pkg})`);
  const { units, totals } = rebalanceUnits(cfg, pkg, { backendDir, weights });
  console.log(`${pkg}: ${weights.size} test times from ${opts.from}`);
  units.forEach((u, i) => console.log(`  ${u.name.padEnd(16)} ${String(u.files.length).padStart(4)} files  ${totals[i].toFixed(1).padStart(8)} s${u.rest ? '  (rest)' : ''}`));
  if (!opts.write) {
    console.log('\n(not written: add --write to save it to scripts/test-shards.json)');
    return 0;
  }
  const next = { ...cfg, units: { ...cfg.units, [pkg]: units.map((u, i) => ({ ...u, weight_s: Math.round(totals[i]), measured: true })) } };
  const errors = validateConfig(next);
  if (errors.length) throw new Error(`the new split is not a valid list:\n  ${errors.join('\n  ')}`);
  fs.writeFileSync(CONFIG, `${JSON.stringify(next, null, 2)}\n`);
  console.log(`\nwritten: ${path.relative(REPO, CONFIG)}`);
  return 0;
}

// ── main ────────────────────────────────────────────────────────────────────

const COMMANDS = { go: cmdGo, check: cmdCheck, 'e2e-check': cmdE2eCheck, rebalance: cmdRebalance };
if (!COMMANDS[command]) {
  console.error(`unknown command ${command}`);
  usage(2);
}
let code = 1;
try {
  code = COMMANDS[command]();
} catch (err) {
  console.error(`test-shards ${command}: ${err.message}`);
  code = 1;
}
process.exit(code);
