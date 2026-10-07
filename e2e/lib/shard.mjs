// `node e2e/run.mjs local --shard i/N`: one part of the Playwright suite, on a
// server of its own (task #171).
//
// The split is Playwright's own `--shard`, not a list this file cuts:
//
//   - it splits the tests Playwright has COLLECTED, after --grep, --grep-invert
//     and E2E_BROWSERS have been applied, so the N parts of a run add up to
//     the run without a shard by construction. A spec list cut here would be
//     cut before those filters: a part could end up with no test at all, and
//     a part with no test is "No tests found", a red run;
//   - it balances by number of tests, not by number of files (a spec here
//     holds anything from 1 to 60 tests);
//   - with `fullyParallel: false` a spec file is one group and is never cut in
//     two, so the tests of one spec still run in order, on one server;
//   - each part is a contiguous run of the sorted list, so a spec has the same
//     neighbours it has in a whole run except at a part's edge.
//
// ⚠ What a part must NOT share with another part on the same machine, so that
// N of them can run at once: the server (a free port and a mkdtemp data dir
// per run.mjs, as always), Playwright's output directory (Playwright EMPTIES it
// when a run starts, so a second part starting would delete the first one's
// traces and screenshots mid-run) and the kept server log. Those two get a
// directory per part, named by `shardLabel`.

import fs from 'node:fs';
import path from 'node:path';

/** The most parts a run may be cut into (a typo like 1/400 is refused). */
export const MAX_SHARDS = 64;

/** Every spec `run.mjs local` runs: all but the deployment smoke, which is the other profile. */
export function localSpecs(e2eDir) {
  return fs
    .readdirSync(path.join(e2eDir, 'tests'))
    .filter((f) => f.endsWith('.spec.ts') && !f.startsWith('90-deployment-smoke'))
    .sort();
}

/**
 * `i/N` → { current: i, total: N }. Throws on anything else: a shard that is
 * quietly ignored is a whole run reported as one part (or one part reported
 * as a whole run), the mistake lib/args.mjs exists to stop.
 */
export function parseShard(raw) {
  const m = /^(\d+)\/(\d+)$/.exec(String(raw ?? '').trim());
  if (!m) throw new Error(`--shard ${raw ?? ''}: expected i/N, for example --shard 2/4`);
  const current = Number(m[1]);
  const total = Number(m[2]);
  if (total < 1 || total > MAX_SHARDS) throw new Error(`--shard ${raw}: N is 1 to ${MAX_SHARDS}`);
  if (current < 1 || current > total) throw new Error(`--shard ${raw}: i is 1 to N (1-based, like Playwright)`);
  return { current, total };
}

/**
 * The shard this command line asks for, or null. `--shard 2/4` and
 * `--shard=2/4` both work; `--shard` with no value, or given twice, is an error.
 */
export function shardFromArgv(argv) {
  const found = [];
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--shard') {
      const next = argv[i + 1];
      found.push(next !== undefined && !next.startsWith('--') ? next : '');
    } else if (a.startsWith('--shard=')) {
      found.push(a.slice('--shard='.length));
    }
  }
  if (found.length === 0) return null;
  if (found.length > 1) throw new Error(`--shard is given ${found.length} times; give it once`);
  return parseShard(found[0]);
}

/** `shard-2-of-4`: the directory name a part keeps its output under. */
export function shardLabel(shard) {
  return `shard-${shard.current}-of-${shard.total}`;
}

/** Where run.mjs keeps the server log: e2e/.artifacts, or e2e/.artifacts/<label> for a part. */
export function artifactsDir(e2eDir, shard) {
  const base = path.join(e2eDir, '.artifacts');
  return shard ? path.join(base, shardLabel(shard)) : base;
}

/** Playwright's output directory: its default, e2e/test-results, or e2e/test-results/<label> for a part. */
export function outputDir(e2eDir, shard) {
  const base = path.join(e2eDir, 'test-results');
  return shard ? path.join(base, shardLabel(shard)) : base;
}

/** What a part adds to the Playwright command line; nothing without a shard. */
export function shardPlaywrightArgs(e2eDir, shard) {
  if (!shard) return [];
  return [`--shard=${shard.current}/${shard.total}`, `--output=${outputDir(e2eDir, shard)}`];
}

// ── checking a split: do the parts add up to the whole? ─────────────────────
//
// scripts/test-shards.mjs e2e-check lists the suite with `playwright test
// --list` once whole and once per part (no server, no browser), and holds the
// parts to the whole with these two.

/**
 * The tests a `playwright test --list --reporter=list` run printed, one string
 * each (`[project] › file:line:col › title › …`), and its `Total: N tests`.
 */
export function parseListing(stdout) {
  const tests = [];
  let total = null;
  let inList = false;
  for (const line of String(stdout).split(/\r?\n/)) {
    if (line.startsWith('Listing tests:')) {
      inList = true;
      continue;
    }
    const t = /^Total: (\d+) tests? in \d+ files?/.exec(line);
    if (t) {
      total = Number(t[1]);
      inList = false;
      continue;
    }
    if (inList && line.startsWith('  ') && line.trim()) tests.push(line.trim());
  }
  return { tests, total };
}

/**
 * Do the parts (one list of tests each) add up to the whole run, each test in
 * exactly one part? `missing`: in the whole run, in no part. `extra`: in a
 * part, not in the whole run. `twice`: in more than one part.
 */
export function verifyPartition(whole, parts) {
  const where = new Map();
  parts.forEach((part, i) => {
    for (const t of part) where.set(t, [...(where.get(t) ?? []), i + 1]);
  });
  const wholeSet = new Set(whole);
  const missing = whole.filter((t) => !where.has(t));
  const extra = [...where.keys()].filter((t) => !wholeSet.has(t));
  const twice = [...where].filter(([, at]) => at.length > 1).map(([t, at]) => `${t} (parts ${at.join(', ')})`);
  const sum = parts.reduce((n, part) => n + part.length, 0);
  return {
    ok: missing.length === 0 && extra.length === 0 && twice.length === 0 && sum === whole.length,
    sum,
    total: whole.length,
    missing,
    extra,
    twice,
  };
}
