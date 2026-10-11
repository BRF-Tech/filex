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
//   node scripts/test-shards.mjs times --from <log|dir> [--from …] [--package <pkg>] [--source <text>] [--write]
//       Measures: the seconds each test file of a cut package and each other
//       package took, the median over the -race logs given (a directory: every
//       .out/.log/.json file under it; GitHub keeps each -race job's log as the
//       artifact race-<shard>, `gh run download <id> -p 'race-*'`). --write
//       saves scripts/test-times.json, keeping its model; --source says in it
//       where the logs came from (runs, versions - never a machine path).
//       --package: the cut package of logs that print no package line (the
//       chain's cut shards run their binary directly).
//
//   node scripts/test-shards.mjs rebalance [--package <pkg>] [--write]
//       New file lists for the units of the cut packages (or of --package),
//       from scripts/test-times.json; a test file the times do not know
//       weighs its package's mean per test. --write saves them, with every
//       unit's weight_s (package units too: their packages' sum), and prints
//       the estimate. The package units are cut by hand (a package moves
//       whole); `estimate` says when one is too heavy.
//
//   node scripts/test-shards.mjs rebalance --package <pkg> --from <log> [--from …] [--write]
//       The same from the time each test took in <log> (`go test -json` or
//       `-v` output of that package), the longest when a test is seen twice.
//
//   node scripts/test-shards.mjs estimate [--budget <min>]
//       Minutes each shard of the race profile is expected to take on the
//       runner scripts/test-times.json was measured on, with the tree as it
//       is; red (exit 1) when one is over the budget (RACE_BUDGET_MIN).
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
  estimateRace,
  fileWeigher,
  formatTimes,
  goTestArgs,
  measureTimes,
  modulePath,
  packageTests,
  packageUnitSeconds,
  parseGoTestTimes,
  planProfile,
  RACE_BUDGET_MIN,
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
const TIMES = path.join(REPO, 'scripts', 'test-times.json');

const argv = process.argv.slice(2);
const command = argv[0];
const VALUES = ['profile', 'format', 'shard', 'node', 'shards', 'browsers', 'grep', 'grep-invert', 'package', 'from', 'source', 'budget'];
/** Options that may be given more than once: a list. */
const REPEATED = ['from'];
const FLAGS = ['offline', 'write'];

function usage(code) {
  const out = code ? console.error : console.log;
  out('usage: node scripts/test-shards.mjs <go|check|e2e-check|times|rebalance|estimate> [options]');
  out('  go         [--profile go|race] [--format json|chain|matrix|args] [--shard <name> | --node <i/N>]');
  out('  check      [--offline]');
  out('  e2e-check  [--shards 4] [--browsers chromium[,firefox,webkit]] [--grep <p>] [--grep-invert <p>]');
  out('  times      --from <-race log or directory of them> [--from …] [--package <pkg>] [--source <text>] [--write]');
  out('  rebalance  [--package <pkg>] [--write]   (from scripts/test-times.json)');
  out('  rebalance  --package <pkg> --from <go test -json or -v log> [--write]');
  out(`  estimate   [--budget <minutes>, default ${RACE_BUDGET_MIN}]`);
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
    if (REPEATED.includes(key)) opts[key] = [...(opts[key] ?? []), v];
    else opts[key] = v;
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

const loadTimes = () => {
  if (!fs.existsSync(TIMES)) throw new Error(`${path.relative(REPO, TIMES)} is missing: measure first (times --from <logs> --write)`);
  return JSON.parse(fs.readFileSync(TIMES, 'utf8'));
};

/** The files `--from` names: a file as it is, a directory as every log under it. */
function logFiles(from) {
  const out = [];
  const walk = (p) => {
    const st = fs.statSync(p);
    if (st.isFile()) {
      out.push(p);
      return;
    }
    for (const e of fs.readdirSync(p, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : 1))) {
      const sub = path.join(p, e.name);
      if (e.isDirectory()) walk(sub);
      else if (e.isFile() && /\.(out|log|json|txt)$/.test(e.name)) out.push(sub);
    }
  };
  for (const f of from) walk(f);
  return out;
}

function printEstimate(rows, budget) {
  const over = rows.filter((r) => r.minutes > budget);
  for (const r of rows) console.log(`  ${r.name.padEnd(18)} ${r.minutes.toFixed(1).padStart(6)} min${r.minutes > budget ? `  ✗ over ${budget}` : ''}`);
  console.log(`  longest            ${Math.max(...rows.map((r) => r.minutes)).toFixed(1).padStart(6)} min (budget ${budget})`);
  return over;
}

function cmdTimes() {
  if (!opts.from?.length) throw new Error('times needs --from <a -race log, or a directory of them>');
  const cfg = loadConfig();
  const backendDir = backendOf(cfg);
  const files = logFiles(opts.from);
  if (files.length === 0) throw new Error(`no log under ${opts.from.join(', ')} (.out, .log, .json or .txt)`);
  const logs = files.map((f) => ({ name: path.basename(path.dirname(f)) + path.sep + path.basename(f), text: fs.readFileSync(f, 'utf8') }));
  const got = measureTimes(cfg, { backendDir, logs, pkg: opts.package ?? '' });
  console.log(`${got.logs} logs`);
  for (const [pkg, perFile] of Object.entries(got.files)) {
    const rows = Object.values(perFile);
    const secs = rows.reduce((s, [x]) => s + x, 0);
    const tests = rows.reduce((s, [, n]) => s + n, 0);
    console.log(`  ${pkg}: ${tests} tests in ${rows.length} files, ${(secs / 60).toFixed(1)} min`);
    if (got.unknownTests[pkg]) console.log(`    ${got.unknownTests[pkg].length} measured tests are in no file of the tree any more (renamed or deleted): left out`);
  }
  const pkgSecs = Object.values(got.packages).reduce((s, x) => s + x, 0);
  console.log(`  ${Object.keys(got.packages).length} other packages, ${(pkgSecs / 60).toFixed(1)} min`);
  for (const s of got.skipped) console.log(`  skipped ${s}: no package line, and no --package for it`);
  if (!opts.write) {
    console.log(`\n(not written: add --write to save it to ${path.relative(REPO, TIMES)})`);
    return 0;
  }
  const prev = fs.existsSync(TIMES) ? JSON.parse(fs.readFileSync(TIMES, 'utf8')) : {};
  const next = {
    $comment: prev.$comment ?? [],
    source: opts.source ?? prev.source ?? '',
    model: prev.model ?? { setup_min: 2, package_parallel: 3 },
    files: got.files,
    packages: got.packages,
  };
  fs.writeFileSync(TIMES, formatTimes(next));
  console.log(`\nwritten: ${path.relative(REPO, TIMES)}`);
  return 0;
}

function cmdRebalance() {
  const cfg = loadConfig();
  const backendDir = backendOf(cfg);
  let results;
  let times = null;
  if (opts.from?.length) {
    if (!opts.package) throw new Error('rebalance --from needs --package ./internal/x (the package of the log)');
    const pkg = opts.package;
    const text = opts.from.map((f) => fs.readFileSync(f, 'utf8')).join('\n');
    const weights = parseGoTestTimes(text, pkg.replace(/^\.\//, ''));
    if (weights.size === 0) throw new Error(`${opts.from.join(', ')}: no test time in it (a go test -json or -v log of ${pkg})`);
    console.log(`${pkg}: ${weights.size} test times from ${opts.from.join(', ')}`);
    results = [{ pkg, ...rebalanceUnits(cfg, pkg, { backendDir, weights }) }];
  } else {
    times = loadTimes();
    const pkgs = opts.package ? [opts.package] : Object.keys(cfg.units);
    results = pkgs.map((pkg) => {
      const fileWeight = fileWeigher(times, pkg, packageTests(backendDir, pkg));
      return { pkg, ...rebalanceUnits(cfg, pkg, { backendDir, fileWeight }) };
    });
    console.log(`from ${path.relative(REPO, TIMES)}`);
  }
  for (const { pkg, units, totals } of results) {
    console.log(`${pkg}:`);
    units.forEach((u, i) => console.log(`  ${u.name.padEnd(16)} ${String(u.files.length).padStart(4)} files  ${(totals[i] / 60).toFixed(1).padStart(6)} min${u.rest ? '  (rest)' : ''}`));
  }
  const measuredFrom = times ? 'measured: scripts/test-times.json (its source says which runs)' : `measured: ${opts.from.map((f) => path.basename(f)).join(', ')}`;
  const nextUnits = { ...cfg.units };
  for (const { pkg, units, totals } of results) {
    nextUnits[pkg] = units.map((u, i) => {
      const { measured: _old, ...rest } = u;
      return { ...rest, weight_s: Math.round(totals[i]), weight_from: measuredFrom };
    });
  }
  let next = { ...cfg, units: nextUnits };
  if (times) {
    const pkgSecs = packageUnitSeconds(next, times, walkGoPackages(backendDir));
    next = { ...next, packages: next.packages.map((u) => ({ ...u, weight_s: Math.round(pkgSecs.get(u.name) ?? 0), weight_from: measuredFrom })) };
  }
  const errors = validateConfig(next);
  if (errors.length) throw new Error(`the new split is not a valid list:\n  ${errors.join('\n  ')}`);
  if (times) {
    console.log('\nthe race profile, estimated:');
    printEstimate(estimateRace(next, times, { backendDir, goPackages: walkGoPackages(backendDir) }), RACE_BUDGET_MIN);
  }
  if (!opts.write) {
    console.log('\n(not written: add --write to save it to scripts/test-shards.json)');
    return 0;
  }
  fs.writeFileSync(CONFIG, `${JSON.stringify(next, null, 2)}\n`);
  console.log(`\nwritten: ${path.relative(REPO, CONFIG)}`);
  return 0;
}

// ── estimate ────────────────────────────────────────────────────────────────

function cmdEstimate() {
  const cfg = loadConfig();
  const backendDir = backendOf(cfg);
  const times = loadTimes();
  const budget = opts.budget === undefined ? RACE_BUDGET_MIN : Number(opts.budget);
  if (!Number.isFinite(budget) || budget <= 0) throw new Error(`--budget ${opts.budget}: minutes, a number above 0`);
  console.log(`the race profile on the runner of ${path.relative(REPO, TIMES)} (${times.source || 'no source given'}):`);
  console.log('(packages walked from the tree: an approximation of go list ./...)');
  const over = printEstimate(estimateRace(cfg, times, { backendDir, goPackages: walkGoPackages(backendDir) }), budget);
  if (over.length === 0) return 0;
  console.error(`\n${over.length} shard(s) over ${budget} min: node scripts/test-shards.mjs rebalance --write (a cut package), or move a package between package units (docs/CONTRIBUTING.md, Testing → Shards)`);
  return 1;
}

// ── main ────────────────────────────────────────────────────────────────────

const COMMANDS = { go: cmdGo, check: cmdCheck, 'e2e-check': cmdE2eCheck, times: cmdTimes, rebalance: cmdRebalance, estimate: cmdEstimate };
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
