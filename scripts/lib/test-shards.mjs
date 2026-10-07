// The Go test shards: what scripts/test-shards.json says, read and checked.
//
// One list for every runner (task #171): the build host's chain
// (scripts/chain), GitHub Actions and CircleCI all ask scripts/test-shards.mjs
// for the same shards, so the three cannot drift apart. Pure apart from
// reading the backend's files; the Go toolchain is only asked by the CLI
// (`go list ./...`, `go test -list`), never here.
//
// The list has three layers:
//
//   units     the smallest pieces. A sharded package (internal/api/handlers,
//             internal/wasmplugin: each alone runs longer than any other
//             package) is cut into units by TEST FILE; the unit's -run regex
//             is the exact names of the tests in its files. Every other
//             package belongs to a package unit.
//   rest      in each sharded package one unit is marked `rest`: it also
//             takes every test file no unit lists - a file added tomorrow
//             runs there, without anyone editing the list. Among the package
//             units one is `rest` as well: every package `go list ./...`
//             knows that no unit names (a package added tomorrow).
//   profiles  how the units are run: `go` (plain go test, the Go gate) runs
//             two handlers units per job, `race` (go test -race) one. A
//             profile uses every unit exactly once.
//
// ⚠ The -run regex names tests exactly (`^(TestA|TestB)$`), not by a prefix
// or a letter range: a range splits by how tests happen to be NAMED, and a
// new test then lands wherever its name falls, balanced or not. A file is a
// feature's tests, written together and of a similar cost; it moves as one.

import fs from 'node:fs';
import path from 'node:path';

/** A unit or shard name: what a CI job and the chain's job name are built from. */
export const NAME = /^[a-z0-9][a-z0-9-]*$/;

/**
 * Longest -run regex a shard may carry. Linux limits ONE argument (or one
 * environment entry, the chain passes it as RUN=...) to 128 KiB
 * (MAX_ARG_STRLEN); past it the exec fails with "Argument list too long".
 */
export const MAX_RUN_CHARS = 100_000;

const PKG_PATH = /^\.\/[A-Za-z0-9_.\-/]+$/;

// ── Go source ───────────────────────────────────────────────────────────────

/**
 * The Go source with every comment and every string/rune literal's contents
 * blanked to spaces (newlines kept), so a test written INSIDE a raw string -
 * a Go program a test compiles, `func TestX` on a line of its own - is not
 * read as a test of this package.
 */
export function maskGo(src) {
  const out = src.split('');
  const blank = (i) => {
    if (out[i] !== '\n') out[i] = ' ';
  };
  let i = 0;
  while (i < src.length) {
    const c = src[i];
    const n = src[i + 1];
    if (c === '/' && n === '/') {
      while (i < src.length && src[i] !== '\n') blank(i++);
      continue;
    }
    if (c === '/' && n === '*') {
      const end = src.indexOf('*/', i + 2);
      const stop = end < 0 ? src.length : end + 2;
      while (i < stop) blank(i++);
      continue;
    }
    if (c === '"' || c === "'") {
      i++;
      while (i < src.length && src[i] !== c && src[i] !== '\n') {
        if (src[i] === '\\') blank(i++);
        if (i < src.length) blank(i++);
      }
      i++;
      continue;
    }
    if (c === '`') {
      i++;
      while (i < src.length && src[i] !== '`') blank(i++);
      i++;
      continue;
    }
    i++;
  }
  return out.join('');
}

/**
 * Go's own rule (testing's isTest): `Test`, `Example` or `Fuzz`, then nothing
 * or a character that is not a lower-case letter. `Testable` is a helper.
 */
export function isTestName(name) {
  for (const prefix of ['Test', 'Example', 'Fuzz']) {
    if (!name.startsWith(prefix)) continue;
    if (name.length === prefix.length) return true;
    const next = name[prefix.length];
    return !(next.toLowerCase() === next && next.toUpperCase() !== next);
  }
  return false;
}

/**
 * The tests, examples and fuzz targets a Go test file declares: top-level
 * functions only (a method `func (s *x) TestY` is not a test), TestMain left
 * out (it is the entry point, and it runs whatever -run says). Source order.
 */
export function goTestNames(src) {
  const masked = maskGo(src);
  const names = [];
  // Go identifiers are Unicode letters, digits and _: a name read short would
  // be a test in no -run group.
  for (const m of masked.matchAll(/^func[ \t]+([\p{L}_][\p{L}\p{Nd}_]*)[ \t]*\(/gmu)) {
    const name = m[1];
    if (name === 'TestMain' || !isTestName(name)) continue;
    if (!names.includes(name)) names.push(name);
  }
  return names;
}

/** The `*_test.go` files directly in `dir` (not its sub-packages), sorted. */
export function testFilesIn(dir) {
  if (!fs.existsSync(dir)) return [];
  return fs
    .readdirSync(dir, { withFileTypes: true })
    .filter((e) => e.isFile() && e.name.endsWith('_test.go'))
    .map((e) => e.name)
    .sort();
}

/** file → its test names, for every test file of the package `pkg` (./internal/x). */
export function packageTests(backendDir, pkg) {
  const dir = path.join(backendDir, pkg);
  const out = new Map();
  for (const f of testFilesIn(dir)) out.set(f, goTestNames(fs.readFileSync(path.join(dir, f), 'utf8')));
  return out;
}

/** The module path go.mod declares (`module …`). */
export function modulePath(backendDir) {
  const m = /^module\s+(\S+)/m.exec(fs.readFileSync(path.join(backendDir, 'go.mod'), 'utf8'));
  if (!m) throw new Error(`${path.join(backendDir, 'go.mod')}: no module line`);
  return m[1];
}

/** An import path `go list` prints → `./internal/x` (the module's root package is `.`). */
export function relPackage(importPath, module) {
  if (importPath === module) return '.';
  if (!importPath.startsWith(`${module}/`)) throw new Error(`${importPath} is not in module ${module}`);
  return `./${importPath.slice(module.length + 1)}`;
}

/**
 * The packages under `backendDir`, as `go list ./...` would name them - an
 * approximation for where there is no Go toolchain (the web unit suite):
 * every directory with a .go file that is not `//go:build ignore`, skipping
 * testdata, vendor, `.x` and `_x` directories and nested modules. The CLI's
 * `check` asks `go list` itself.
 */
export function walkGoPackages(backendDir) {
  const out = [];
  const walk = (dir) => {
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    const goFiles = entries.filter((e) => e.isFile() && e.name.endsWith('.go'));
    const buildable = goFiles.some((e) => !/^\/\/go:build ignore\s*$/m.test(fs.readFileSync(path.join(dir, e.name), 'utf8')));
    if (buildable) {
      const rel = path.relative(backendDir, dir).split(path.sep).join('/');
      out.push(rel ? `./${rel}` : '.');
    }
    for (const e of entries) {
      if (!e.isDirectory()) continue;
      if (e.name === 'testdata' || e.name === 'vendor' || e.name === 'node_modules') continue;
      if (e.name.startsWith('.') || e.name.startsWith('_')) continue;
      const sub = path.join(dir, e.name);
      if (fs.existsSync(path.join(sub, 'go.mod'))) continue;
      walk(sub);
    }
  };
  walk(backendDir);
  return out.sort();
}

// ── the list ────────────────────────────────────────────────────────────────

/** Every unit by name: `{ kind: 'tests', pkg, unit }` or `{ kind: 'packages', unit }`. */
export function unitIndex(cfg) {
  const index = new Map();
  for (const [pkg, units] of Object.entries(cfg.units ?? {})) {
    for (const unit of units ?? []) index.set(unit.name, { kind: 'tests', pkg, unit });
  }
  for (const unit of cfg.packages ?? []) index.set(unit.name, { kind: 'packages', unit });
  return index;
}

/**
 * What is wrong with the list's shape, one sentence each; [] when nothing is.
 * Only the list itself is read here - whether its files and packages exist is
 * planProfile's and unionReport's question.
 */
export function validateConfig(cfg) {
  const errors = [];
  const seen = new Set();
  const named = (where, name) => {
    if (typeof name !== 'string' || !NAME.test(name)) {
      errors.push(`${where}: "${name}" is not a name ([a-z0-9-], starting with a letter or digit)`);
      return;
    }
    if (seen.has(name)) errors.push(`${where}: the name "${name}" is used twice`);
    seen.add(name);
  };
  if (typeof cfg.module !== 'string' || !cfg.module) errors.push('module: the Go module directory (e.g. "backend") is missing');

  const sharded = Object.keys(cfg.units ?? {});
  if (sharded.length === 0) errors.push('units: no sharded package');
  for (const pkg of sharded) {
    if (!PKG_PATH.test(pkg) || pkg.includes('...')) errors.push(`units: "${pkg}" is not a package path like ./internal/x`);
    const units = cfg.units[pkg];
    if (!Array.isArray(units) || units.length === 0) {
      errors.push(`units ${pkg}: no unit`);
      continue;
    }
    const rests = units.filter((u) => u.rest === true);
    if (rests.length !== 1) errors.push(`units ${pkg}: exactly one unit is the rest (it takes the files no unit lists); found ${rests.length}`);
    const fileOwner = new Map();
    for (const u of units) {
      named(`units ${pkg}`, u.name);
      if (u.files !== undefined && !Array.isArray(u.files)) errors.push(`units ${pkg} ${u.name}: files is a list`);
      if (u.rest !== true && (!Array.isArray(u.files) || u.files.length === 0)) {
        errors.push(`units ${pkg} ${u.name}: a unit that is not the rest lists at least one file`);
      }
      for (const f of u.files ?? []) {
        if (typeof f !== 'string' || !f.endsWith('_test.go') || f.includes('/') || f.includes('\\')) {
          errors.push(`units ${pkg} ${u.name}: "${f}" is not a test file name (x_test.go, no directory)`);
          continue;
        }
        if (fileOwner.has(f)) errors.push(`units ${pkg}: ${f} is in ${fileOwner.get(f)} and ${u.name}`);
        else fileOwner.set(f, u.name);
      }
    }
  }

  const pkgUnits = cfg.packages ?? [];
  if (!Array.isArray(pkgUnits) || pkgUnits.length === 0) errors.push('packages: no package unit');
  const pkgRests = (Array.isArray(pkgUnits) ? pkgUnits : []).filter((u) => u.rest === true);
  if (pkgRests.length !== 1) errors.push(`packages: exactly one unit is the rest (every package no unit names); found ${pkgRests.length}`);
  const pkgOwner = new Map();
  for (const u of Array.isArray(pkgUnits) ? pkgUnits : []) {
    named('packages', u.name);
    if (u.rest === true && (u.packages ?? []).length) errors.push(`packages ${u.name}: the rest names no package (it is what is left)`);
    if (u.rest !== true && (!Array.isArray(u.packages) || u.packages.length === 0)) errors.push(`packages ${u.name}: names no package`);
    for (const p of u.packages ?? []) {
      if (typeof p !== 'string' || !(p === '.' || PKG_PATH.test(p)) || p.includes('...')) {
        errors.push(`packages ${u.name}: "${p}" is not a package path like ./internal/x (no ...)`);
        continue;
      }
      if (sharded.includes(p)) errors.push(`packages ${u.name}: ${p} is a sharded package; its units run it`);
      if (pkgOwner.has(p)) errors.push(`packages: ${p} is in ${pkgOwner.get(p)} and ${u.name}`);
      else pkgOwner.set(p, u.name);
    }
  }

  const index = unitIndex(cfg);
  const profiles = Object.entries(cfg.profiles ?? {});
  if (profiles.length === 0) errors.push('profiles: none');
  for (const [profile, shards] of profiles) {
    if (!Array.isArray(shards) || shards.length === 0) {
      errors.push(`profiles ${profile}: no shard`);
      continue;
    }
    const used = new Map();
    const shardNames = new Set();
    for (const s of shards) {
      if (typeof s.name !== 'string' || !NAME.test(s.name)) errors.push(`profiles ${profile}: "${s.name}" is not a shard name ([a-z0-9-])`);
      if (shardNames.has(s.name)) errors.push(`profiles ${profile}: the shard "${s.name}" is listed twice`);
      shardNames.add(s.name);
      if (!Array.isArray(s.units) || s.units.length === 0) {
        errors.push(`profiles ${profile} ${s.name}: runs no unit`);
        continue;
      }
      const kinds = new Set();
      for (const name of s.units) {
        const hit = index.get(name);
        if (!hit) {
          errors.push(`profiles ${profile} ${s.name}: no unit is called "${name}"`);
          continue;
        }
        kinds.add(hit.kind === 'tests' ? `tests ${hit.pkg}` : 'packages');
        if (used.has(name)) errors.push(`profiles ${profile}: the unit ${name} runs in ${used.get(name)} and ${s.name}`);
        else used.set(name, s.name);
      }
      if (kinds.size > 1) {
        errors.push(
          `profiles ${profile} ${s.name}: a shard runs the units of ONE sharded package, or package units - not ${[...kinds].join(' + ')} ` +
            '(a -run regex would apply to every package of the go test call)',
        );
      }
    }
    for (const name of index.keys()) {
      if (!used.has(name)) errors.push(`profiles ${profile}: the unit ${name} runs in no shard`);
    }
  }
  return errors;
}

/** `^(A|B)$` - the exact names; '' for none. */
export function runRegex(names) {
  return names.length ? `^(${names.join('|')})$` : '';
}

/**
 * The shards of `profile`, resolved against the tree under `backendDir`.
 *
 * A test-unit shard: `{ name, units, packages: [pkg], exclude: [], run,
 * tests, files }` - `files` the test files it runs, the rest unit's own plus
 * every file no unit lists. A package shard: `{ name, units, packages,
 * exclude, run: '' }`, where a shard holding the package rest is `./...`
 * minus every sharded package and every package another unit names.
 *
 * `notes.unplaced` (pkg → files no unit lists, running in the rest) and
 * `notes.stale` (pkg → files a unit lists that are not on disk) come back for
 * the caller to report; planProfile throws on neither.
 */
export function planProfile(cfg, profile, { backendDir }) {
  const errors = validateConfig(cfg);
  if (errors.length) throw new Error(`scripts/test-shards.json:\n  ${errors.join('\n  ')}`);
  const shards = cfg.profiles[profile];
  if (!shards) throw new Error(`no profile "${profile}" (known: ${Object.keys(cfg.profiles).join(', ')})`);
  const index = unitIndex(cfg);

  // Per sharded package: its files on disk, and which unit runs each one.
  const unplaced = new Map();
  const stale = new Map();
  const filesOfUnit = new Map();
  const tests = new Map();
  for (const [pkg, units] of Object.entries(cfg.units)) {
    const onDisk = packageTests(backendDir, pkg);
    tests.set(pkg, onDisk);
    const listed = new Set(units.flatMap((u) => u.files ?? []));
    const missing = [...listed].filter((f) => !onDisk.has(f)).sort();
    if (missing.length) stale.set(pkg, missing);
    const loose = [...onDisk.keys()].filter((f) => !listed.has(f)).sort();
    if (loose.length) unplaced.set(pkg, loose);
    for (const u of units) {
      const own = (u.files ?? []).filter((f) => onDisk.has(f));
      filesOfUnit.set(u.name, u.rest === true ? [...own, ...loose] : own);
    }
  }

  const sharded = Object.keys(cfg.units);
  const out = shards.map((s) => {
    const first = index.get(s.units[0]);
    if (first.kind === 'tests') {
      const pkg = first.pkg;
      const files = s.units.flatMap((u) => filesOfUnit.get(u)).sort();
      const onDisk = tests.get(pkg);
      const names = [...new Set(files.flatMap((f) => onDisk.get(f) ?? []))].sort();
      return { name: s.name, units: [...s.units], packages: [pkg], exclude: [], run: runRegex(names), tests: names.length, files };
    }
    const own = s.units.map((u) => index.get(u).unit);
    if (own.some((u) => u.rest === true)) {
      const elsewhere = cfg.packages.filter((u) => !s.units.includes(u.name)).flatMap((u) => u.packages ?? []);
      return { name: s.name, units: [...s.units], packages: ['./...'], exclude: [...new Set([...sharded, ...elsewhere])].sort(), run: '' };
    }
    return { name: s.name, units: [...s.units], packages: own.flatMap((u) => u.packages).sort(), exclude: [], run: '' };
  });
  return { profile, shards: out, notes: { unplaced, stale } };
}

/** A shard's packages as a list: `./...` resolved against `goPackages` (what `go list ./...` printed, as ./paths). */
export function expandPackages(shard, goPackages) {
  if (!shard.packages.includes('./...')) return [...shard.packages];
  const out = new Set(shard.packages.filter((p) => p !== './...'));
  for (const p of goPackages) if (!shard.exclude.includes(p)) out.add(p);
  return [...out].sort();
}

/**
 * Does the profile test exactly the packages `go list ./...` knows?
 *
 *   missing  a package no shard runs
 *   extra    a package a shard names that `go list` does not know (renamed,
 *            deleted, a typo) - a shard asking for it fails, or tests nothing
 *   twice    a package two package shards run, or a sharded package a
 *            package shard runs as well (it would run whole, past its units)
 *
 * The sharded packages count once however many shards cut them: whether
 * their shards together run every test is testsReport's question.
 */
export function unionReport(plan, goPackages) {
  const known = new Set(goPackages);
  const runs = new Map();
  const shardedPkgs = new Set();
  for (const s of plan.shards) {
    if (s.run) {
      for (const p of s.packages) shardedPkgs.add(p);
      continue;
    }
    for (const p of expandPackages(s, goPackages)) runs.set(p, [...(runs.get(p) ?? []), s.name]);
  }
  const twice = [];
  for (const [p, by] of runs) if (by.length > 1 || shardedPkgs.has(p)) twice.push(`${p} (${[...by, ...(shardedPkgs.has(p) ? ['its units'] : [])].join(', ')})`);
  const union = new Set([...runs.keys(), ...shardedPkgs]);
  return {
    missing: goPackages.filter((p) => !union.has(p)).sort(),
    extra: [...union].filter((p) => !known.has(p)).sort(),
    twice: twice.sort(),
  };
}

/**
 * Do the shards of each sharded package run each of its tests exactly once?
 * `parsed`: pkg → (file → names) as packageTests reads them; `authoritative`
 * (optional): pkg → names `go test -list` printed. A name the compiler knows
 * and no shard names is `unrun` - the one failure that would let a test
 * silently stop running.
 */
export function testsReport(plan, parsed, authoritative = new Map()) {
  const out = { unrun: [], twice: [], empty: [], tooLong: [] };
  const byPkg = new Map();
  for (const s of plan.shards) {
    if (!s.run) continue;
    const pkg = s.packages[0];
    if (!byPkg.has(pkg)) byPkg.set(pkg, new Map());
    const names = s.run.slice(2, -2).split('|').filter(Boolean);
    if (names.length === 0) out.empty.push(s.name);
    if (s.run.length > MAX_RUN_CHARS) out.tooLong.push(`${s.name} (${s.run.length} characters)`);
    for (const n of names) {
      const seen = byPkg.get(pkg);
      seen.set(n, [...(seen.get(n) ?? []), s.name]);
    }
  }
  for (const [pkg, seen] of byPkg) {
    for (const [n, by] of seen) if (by.length > 1) out.twice.push(`${pkg} ${n} (${by.join(', ')})`);
    const all = new Set([...(parsed.get(pkg)?.values() ?? [])].flat());
    for (const n of authoritative.get(pkg) ?? []) all.add(n);
    for (const n of [...all].sort()) if (!seen.has(n)) out.unrun.push(`${pkg} ${n}`);
  }
  return out;
}

// ── output ──────────────────────────────────────────────────────────────────

/**
 * The chain's list format (scripts/chain/plan.mjs parseTestList): `name run-regex
 * package...`, `-` for no regex, `!./pkg` to leave a package out of `./...`.
 */
export function chainLines(plan) {
  return plan.shards.map((s) => [s.name, s.run || '-', ...s.packages, ...s.exclude.map((p) => `!${p}`)].join(' '));
}

/** The arguments a shard adds to `go test`: `-run <regex>` (when cut) and its packages. */
export function goTestArgs(shard, goPackages) {
  const pkgs = shard.packages.includes('./...') ? expandPackages(shard, goPackages) : shard.packages;
  return [...(shard.run ? ['-run', shard.run] : []), ...pkgs];
}

/**
 * The shard copy i of a job that a CI runs N times side by side runs:
 * `--node i/N`, 1-based like Playwright's --shard (task #181). CircleCI's
 * `parallelism: N` is `$((CIRCLE_NODE_INDEX + 1))/$CIRCLE_NODE_TOTAL`,
 * GitLab's `parallel: N` is `$CI_NODE_INDEX/$CI_NODE_TOTAL`. The job then
 * names no shard of its own: it holds a number, and the list stays here.
 *
 * ⚠ N must be the profile's number of shards, and anything else is an error
 * that names both. A job run on fewer copies than there are shards leaves the
 * last shards out with every copy green; one run on more has a copy with
 * nothing to run, and a copy that runs nothing reads as a pass.
 */
export function shardAtNode(shards, raw, profile = '') {
  const m = /^(\d+)\/(\d+)$/.exec(String(raw ?? '').trim());
  if (!m) throw new Error(`--node ${raw ?? ''}: expected i/N, for example --node 3/8 (1-based, like --shard)`);
  const i = Number(m[1]);
  const n = Number(m[2]);
  const of = profile ? `profile ${profile}` : 'the profile';
  if (n !== shards.length) {
    throw new Error(`--node ${raw}: ${of} has ${shards.length} shard(s) and this job runs on ${n} - run it on ${shards.length} (CircleCI: parallelism: ${shards.length})`);
  }
  if (i < 1 || i > n) throw new Error(`--node ${raw}: i is 1 to ${n} (1-based)`);
  return shards[i - 1];
}

// ── timings ─────────────────────────────────────────────────────────────────

/**
 * Seconds per top-level test, from `go test -json` output or `-v` output
 * (`--- PASS: TestX (1.23s)` at the start of a line; a subtest's line is
 * indented and is not counted twice). A test seen more than once keeps its
 * longest time. `pkg` (an import path suffix, e.g. internal/api/handlers)
 * limits -json events to that package.
 */
export function parseGoTestTimes(text, pkg = '') {
  const times = new Map();
  const keep = (name, secs) => {
    if (!name || name.includes('/') || !Number.isFinite(secs)) return;
    times.set(name, Math.max(times.get(name) ?? 0, secs));
  };
  for (const line of String(text).split(/\r?\n/)) {
    if (line.startsWith('{')) {
      let ev;
      try {
        ev = JSON.parse(line);
      } catch {
        continue;
      }
      if (!['pass', 'fail', 'skip'].includes(ev.Action) || !ev.Test) continue;
      if (pkg && !(ev.Package === pkg || String(ev.Package ?? '').endsWith(`/${pkg}`))) continue;
      keep(ev.Test, Number(ev.Elapsed));
      continue;
    }
    const m = /^--- (?:PASS|FAIL|SKIP): (\S+) \(([0-9.]+)s\)/.exec(line);
    if (m) keep(m[1], Number(m[2]));
  }
  return times;
}

/**
 * Longest-processing-time-first: each item, heaviest first, to the bin that
 * is lightest so far. `items`: [{ key, weight }]. Returns `k` arrays of keys
 * (ties broken by key, so the same input always gives the same split).
 */
export function binPack(items, k) {
  const bins = Array.from({ length: k }, () => ({ total: 0, keys: [] }));
  const sorted = [...items].sort((a, b) => b.weight - a.weight || (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
  for (const it of sorted) {
    let best = 0;
    for (let i = 1; i < k; i++) if (bins[i].total < bins[best].total) best = i;
    bins[best].total += it.weight;
    bins[best].keys.push(it.key);
  }
  return bins.map((b) => ({ total: b.total, keys: b.keys.sort() }));
}

/**
 * New file lists for the units of `pkg`, from a weight per test: every test
 * file on disk is placed (stale names dropped), the units keep their names
 * and their order, the rest stays the rest. A test with no weight (it did not
 * run where the timings came from: skipped, another OS) counts as the median
 * of those that have one. Returns { units, totals } - `totals` the weight
 * each unit got, in unit order.
 */
export function rebalanceUnits(cfg, pkg, { backendDir, weights }) {
  const units = cfg.units?.[pkg];
  if (!units) throw new Error(`${pkg} is not a sharded package (known: ${Object.keys(cfg.units ?? {}).join(', ')})`);
  const onDisk = packageTests(backendDir, pkg);
  const known = [...weights.values()].sort((a, b) => a - b);
  const median = known.length ? known[Math.floor(known.length / 2)] : 1;
  const items = [...onDisk.entries()].map(([file, names]) => ({
    key: file,
    weight: names.reduce((sum, n) => sum + (weights.has(n) ? weights.get(n) : median), 0),
  }));
  const bins = binPack(items, units.length);
  // The heaviest bin goes to the first unit, and so on: the order is only a
  // name, but a stable one keeps a diff of the list readable.
  bins.sort((a, b) => b.total - a.total);
  return {
    units: units.map((u, i) => ({ ...u, files: bins[i].keys })),
    totals: bins.map((b) => b.total),
  };
}
