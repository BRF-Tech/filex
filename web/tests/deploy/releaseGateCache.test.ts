// The release's gate cache and its side-by-side runner (scripts/release/
// engine.mjs, task #172), on a scratch git repository with gates that count
// their own runs.
//
// ⚠⚠ Why a cache at all: a red gate used to send the next `--resume` back to
// the start of the chain. Over seven releases that was ~935 minutes - 0.52's
// e2e 98 fix touched only e2e/, and the resume ran the Go suite (21 min) and
// the migrations (10 min) again on a backend/ nobody had touched.
//
// ⚠⚠ And why it is held to these rules: a cache that passes a gate it should
// have run is worse than none. So the key is the content of what the gate
// reads (never "the commit"), only green is kept, and anything the cache
// cannot read - a broken entry, a working tree that is not HEAD, an outside
// input it cannot see - is a miss: the gate runs.
//
// The pure rules (keys, entries, profiles, a patch's Go packages, the chain
// evidence) are pinned at the bottom.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

import {
  cacheEntryHit,
  chainVerdict,
  gateCacheKey,
  goPackageDirs,
  matchesInputs,
  profileCovers,
  selectGates,
  stampWrites,
} from '../../../scripts/release/checks.mjs';
import { findBash, Gates } from '../../../scripts/release/engine.mjs';

const TIMEOUT = 60_000;
const temps: string[] = [];
afterEach(() => {
  for (const d of temps.splice(0)) fs.rmSync(d, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
});

function git(dir: string, ...args: string[]) {
  return execFileSync('git', ['-C', dir, '-c', 'user.name=Release Test', '-c', 'user.email=release@test.invalid', '-c', 'commit.gpgSign=false', ...args], {
    encoding: 'utf8',
    stdio: 'pipe',
  });
}

type Rec = { name: string; ok: boolean; cached?: boolean; detail: string };

/** A scratch repository with src/ and other/, committed; and a Gates over it. */
function scratch({ read = true, write = true }: { read?: boolean; write?: boolean } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-gate-cache-'));
  temps.push(root);
  const repo = path.join(root, 'repo');
  fs.mkdirSync(repo);
  git(repo, 'init', '-q');
  const put = (rel: string, text: string) => {
    fs.mkdirSync(path.dirname(path.join(repo, rel)), { recursive: true });
    fs.writeFileSync(path.join(repo, rel), text);
  };
  put('src/a.txt', 'one\n');
  put('other/b.txt', 'one\n');
  git(repo, 'add', '-A');
  git(repo, 'commit', '-q', '-m', 'start');
  const commit = (rel: string, text: string) => {
    put(rel, text);
    git(repo, 'add', '-A');
    git(repo, 'commit', '-q', '-m', `change ${rel}`);
  };
  const cacheDir = path.join(root, 'cache');
  const gates = new Gates({ logsDir: path.join(root, 'logs'), bash: findBash(), repo, cache: { dir: cacheDir, read, write } });
  // A gate that counts its runs in a file outside the repository.
  const counter = path.join(root, 'runs.txt');
  const runs = () => (fs.existsSync(counter) ? fs.readFileSync(counter, 'utf8').length : 0);
  const spec = (extra: Record<string, unknown> = {}) => ({
    name: 'the counted suite',
    inputs: ['src/**'],
    env: { COUNT_FILE: counter },
    cmd: ['node', '-e', "require('fs').appendFileSync(process.env.COUNT_FILE, 'x'); process.exit(Number(process.env.EXIT_WITH || 0))"],
    ...extra,
  });
  const one = (s: object, g: InstanceType<typeof Gates> = gates) => g.one('pretag', s, {}) as Promise<Rec>;
  return { root, repo, put, commit, cacheDir, gates, runs, spec, one };
}

describe('the gate cache: a gate green on exactly these inputs is not run again', () => {
  it('passes from the cache while nothing it reads has changed, and says so', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    const first = await s.one(s.spec());
    expect(first.ok).toBe(true);
    expect(first.cached).toBeFalsy();
    const again = await s.one(s.spec());
    expect(again.ok).toBe(true);
    expect(again.cached).toBe(true);
    expect(again.detail).toMatch(/from the cache/);
    expect(s.runs()).toBe(1);
  });

  it('runs again when an input changes, and not for a change outside its inputs', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    await s.one(s.spec());
    s.commit('other/b.txt', 'two\n');
    expect((await s.one(s.spec())).cached).toBe(true);
    expect(s.runs()).toBe(1);
    s.commit('src/a.txt', 'two\n');
    const r = await s.one(s.spec());
    expect(r.cached).toBeFalsy();
    expect(s.runs()).toBe(2);
    // …and the new inputs are green now, too.
    expect((await s.one(s.spec())).cached).toBe(true);
    expect(s.runs()).toBe(2);
  });

  it('runs again when what it runs changes (its command, its environment)', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    await s.one(s.spec());
    await s.one(s.spec({ env: { COUNT_FILE: path.join(s.root, 'runs.txt'), TZ: 'UTC' } }));
    expect(s.runs()).toBe(2);
  });

  it('a broken cache entry is a miss: the gate runs, and the entry is written again', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    await s.one(s.spec());
    const files = fs.readdirSync(s.cacheDir).filter((f) => f.endsWith('.json'));
    expect(files.length).toBe(1);
    fs.writeFileSync(path.join(s.cacheDir, files[0]!), '{ "schema": 1, "ok": tru');
    const r = await s.one(s.spec());
    expect(r.ok).toBe(true);
    expect(r.cached).toBeFalsy();
    expect(s.runs()).toBe(2);
    const entry = JSON.parse(fs.readFileSync(path.join(s.cacheDir, files[0]!), 'utf8'));
    expect(entry.ok).toBe(true);
    expect(entry.name).toBe('the counted suite');
  });

  it('an entry that does not say green, or names another gate, is a miss', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    await s.one(s.spec());
    const file = path.join(s.cacheDir, fs.readdirSync(s.cacheDir).find((f) => f.endsWith('.json'))!);
    const entry = JSON.parse(fs.readFileSync(file, 'utf8'));
    fs.writeFileSync(file, JSON.stringify({ ...entry, ok: false }));
    await s.one(s.spec());
    fs.writeFileSync(file, JSON.stringify({ ...entry, name: 'another gate' }));
    await s.one(s.spec());
    expect(s.runs()).toBe(3);
  });

  it('never keeps a red result', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    const red = s.spec({ env: { COUNT_FILE: path.join(s.root, 'runs.txt'), EXIT_WITH: '1' } });
    expect((await s.one(red)).ok).toBe(false);
    expect((await s.one(red)).ok).toBe(false);
    expect(s.runs()).toBe(2);
    expect(fs.existsSync(s.cacheDir) ? fs.readdirSync(s.cacheDir).filter((f) => f.endsWith('.json')) : []).toEqual([]);
  });

  it('is not consulted while the working tree differs from HEAD, untracked files included', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    await s.one(s.spec());
    s.put('src/new.test.ts', 'a stray test file the suite would pick up\n');
    await s.one(s.spec());
    expect(s.runs()).toBe(2);
  });

  it('is not consulted when what the gate reads outside the repository cannot be told (cacheExtra null)', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    await s.one(s.spec({ cacheExtra: () => 'workflows tree 1' }));
    expect((await s.one(s.spec({ cacheExtra: () => 'workflows tree 1' }))).cached).toBe(true);
    await s.one(s.spec({ cacheExtra: () => 'workflows tree 2' }));
    await s.one(s.spec({ cacheExtra: () => null }));
    expect(s.runs()).toBe(3);
  });

  it('caches no gate without inputs: builds and images make what later gates use', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    const build = s.spec({ name: 'build: something', inputs: undefined });
    await s.one(build);
    await s.one(build);
    expect(s.runs()).toBe(2);
  });

  it('--no-cache (read off) runs every gate; a dry run (write off) records nothing', { timeout: TIMEOUT }, async () => {
    const s = scratch({ read: false });
    await s.one(s.spec());
    await s.one(s.spec());
    expect(s.runs()).toBe(2);
    const dry = scratch({ write: false });
    await dry.one(dry.spec());
    await dry.one(dry.spec());
    expect(dry.runs()).toBe(2);
    expect(fs.existsSync(dry.cacheDir)).toBe(false);
  });
});

describe('gates side by side: lanes, `after`, and what is not run', () => {
  /** A gate that writes "<name> start/end <ms>" lines and sleeps between them. */
  const timed = (root: string, name: string, ms: number, extra: Record<string, unknown> = {}) => ({
    name,
    env: { TIMES: path.join(root, 'times.txt'), GATE: name, SLEEP: String(ms) },
    cmd: [
      'node',
      '-e',
      "const fs = require('fs'); const f = process.env.TIMES; fs.appendFileSync(f, process.env.GATE + ' start ' + Date.now() + '\\n'); setTimeout(() => { fs.appendFileSync(f, process.env.GATE + ' end ' + Date.now() + '\\n'); process.exit(process.env.EXIT_WITH ? 1 : 0); }, Number(process.env.SLEEP));",
    ],
    ...extra,
  });
  const times = (root: string) => {
    const f = path.join(root, 'times.txt');
    const out: Record<string, { start?: number; end?: number }> = {};
    if (!fs.existsSync(f)) return out;
    for (const line of fs.readFileSync(f, 'utf8').split('\n').filter(Boolean)) {
      const m = /^(.*) (start|end) (\d+)$/.exec(line)!;
      (out[m[1]!] ??= {})[m[2] as 'start' | 'end'] = Number(m[3]);
    }
    return out;
  };

  it('runs two lanes at once, and one lane one gate at a time, in list order', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    const ok = await s.gates.all(
      'pretag',
      [timed(s.root, 'a1', 1500, { lane: 'a' }), timed(s.root, 'b1', 1500, { lane: 'b' }), timed(s.root, 'a2', 100, { lane: 'a' })],
      {},
      { jobs: 3 },
    );
    expect(ok).toBe(true);
    const t = times(s.root);
    expect(t.b1!.start!, 'b1 waited for a lane it is not in').toBeLessThan(t.a1!.end!);
    expect(t.a2!.start!, 'a2 started beside a1, in the same lane').toBeGreaterThanOrEqual(t.a1!.end!);
  });

  it('a gate waits for the gates it is `after`, and one whose gate is red is recorded red, "not run"', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    const ok = await s.gates.all(
      'pretag',
      [
        timed(s.root, 'later', 50, { after: ['first'] }),
        timed(s.root, 'first', 300),
        timed(s.root, 'on a red one', 50, { after: ['broken'] }),
        timed(s.root, 'broken', 50, { env: { TIMES: path.join(s.root, 'times.txt'), GATE: 'broken', SLEEP: '50', EXIT_WITH: '1' } }),
      ],
      {},
      { jobs: 2 },
    );
    expect(ok).toBe(false);
    const t = times(s.root);
    expect(t.later!.start!).toBeGreaterThanOrEqual(t.first!.end!);
    expect(t['on a red one'], 'it ran although the gate it needs was red').toBeUndefined();
    const rec = (s.gates.results as Rec[]).find((r) => r.name === 'on a red one')!;
    expect(rec.ok).toBe(false);
    expect(rec.detail).toContain('not run: it needs "broken", which is red');
  });

  it('a cycle of `after` names is reported, not waited on forever', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    const ok = await s.gates.all('pretag', [timed(s.root, 'x', 10, { after: ['y'] }), timed(s.root, 'y', 10, { after: ['x'] })], {}, { jobs: 2 });
    expect(ok).toBe(false);
    for (const n of ['x', 'y']) expect((s.gates.results as Rec[]).find((r) => r.name === n)!.detail).toMatch(/never finishes/);
  });

  it('ignores an `after` that names a gate this run does not hold (a profile left it out)', { timeout: TIMEOUT }, async () => {
    const s = scratch();
    const ok = await s.gates.all('pretag', [timed(s.root, 'alone', 10, { after: ['build: server binary'] })], {}, { jobs: 2 });
    expect(ok).toBe(true);
  });
});

describe('the rules under the cache and the profiles (checks.mjs)', () => {
  it('a key is the content of the inputs, in any order, and the recipe', () => {
    const files = [
      { mode: '100644', id: 'a1', path: 'src/a' },
      { mode: '100644', id: 'b1', path: 'src/b' },
    ];
    const k = gateCacheKey({ name: 'g', recipe: { cmd: ['x'] }, files, facts: {} });
    expect(gateCacheKey({ name: 'g', recipe: { cmd: ['x'] }, files: [...files].reverse(), facts: {} })).toBe(k);
    expect(gateCacheKey({ name: 'g', recipe: { cmd: ['y'] }, files, facts: {} })).not.toBe(k);
    expect(gateCacheKey({ name: 'h', recipe: { cmd: ['x'] }, files, facts: {} })).not.toBe(k);
    expect(gateCacheKey({ name: 'g', recipe: { cmd: ['x'] }, files: [files[0]!, { ...files[1]!, id: 'b2' }], facts: {} })).not.toBe(k);
    expect(gateCacheKey({ name: 'g', recipe: { cmd: ['x'] }, files: [files[0]!, { ...files[1]!, mode: '100755' }], facts: {} })).not.toBe(k);
    expect(gateCacheKey({ name: 'g', recipe: { cmd: ['x'] }, files, facts: { node: 'v99' } })).not.toBe(k);
  });

  it('reads an entry as a hit only when it is whole, green and this gate on this key', () => {
    const good = JSON.stringify({ schema: 1, name: 'g', key: 'k', ok: true, at: 'now' });
    expect(cacheEntryHit(good, { key: 'k', name: 'g' }).hit).toBe(true);
    expect(cacheEntryHit('{', { key: 'k', name: 'g' })).toMatchObject({ hit: false, why: 'it does not parse' });
    expect(cacheEntryHit('null', { key: 'k', name: 'g' }).hit).toBe(false);
    expect(cacheEntryHit(good, { key: 'other', name: 'g' }).hit).toBe(false);
    expect(cacheEntryHit(good, { key: 'k', name: 'other' }).hit).toBe(false);
    expect(cacheEntryHit(JSON.stringify({ schema: 1, name: 'g', key: 'k', ok: 'yes' }), { key: 'k', name: 'g' }).hit).toBe(false);
    expect(cacheEntryHit(JSON.stringify({ schema: 2, name: 'g', key: 'k', ok: true }), { key: 'k', name: 'g' }).hit).toBe(false);
  });

  it('inputs: a glob takes a file, a "!glob" takes it back, whatever the order', () => {
    const e2e = ['**', '!docs/**', '!site/**'];
    expect(matchesInputs(e2e, 'e2e/tests/a.spec.ts')).toBe(true);
    expect(matchesInputs(e2e, 'docs/STORAGE.md')).toBe(false);
    expect(matchesInputs(['!docs/**', '**'], 'docs/STORAGE.md')).toBe(false);
    expect(matchesInputs(['backend/**'], 'backend/go.mod')).toBe(true);
    expect(matchesInputs(['backend/**'], 'web/backend.ts')).toBe(false);
    expect(matchesInputs([], 'anything')).toBe(false);
  });

  it('the gates run at the gate stage take along, transitively, the builds they are after', () => {
    const plan = {
      pretag: [{ name: 'ui' }, { name: 'bin', after: ['ui'] }, { name: 'images', after: ['bin'] }, { name: 'unit' }],
      heavy: [{ name: 'fixture' }, { name: 'browser', after: ['bin', 'fixture'] }, { name: 'go', github: 'ci.yml' }],
    };
    const minor = selectGates(plan, 'minor', null);
    expect(minor.local.map((g: { name: string }) => g.name)).toEqual(['ui', 'bin', 'fixture', 'browser']);
    expect(minor.github.map((g: { name: string }) => g.name)).toEqual(['go']);
    expect(selectGates(plan, 'full', null).local).toEqual([]);
  });

  it('a pretag green in one profile answers for the same or a narrower one, and one from before profiles was the whole chain', () => {
    expect(profileCovers('full', 'minor')).toBe(true);
    expect(profileCovers('minor', 'patch')).toBe(true);
    expect(profileCovers('patch', 'minor')).toBe(false);
    expect(profileCovers('minor', 'full')).toBe(false);
    expect(profileCovers(undefined, 'full')).toBe(true);
    expect(profileCovers('minor', 'nonsense')).toBe(false);
  });

  it("places a patch's Go changes in their packages, and asks for the whole module when it cannot", () => {
    const pkgs = new Set(['internal/perm', 'internal/wasmplugin', 'internal/db', 'cmd/filex']);
    const has = (d: string) => pkgs.has(d);
    expect(goPackageDirs(['backend/internal/perm/upgrade.go', 'e2e/x.spec.ts', 'backend/internal/perm/x_test.go'], has)).toEqual({ all: false, dirs: ['./internal/perm'] });
    // testdata belongs to the package above it (wasmplugin builds echo from it)
    expect(goPackageDirs(['backend/internal/wasmplugin/testdata/echo/main.go'], has).dirs).toEqual(['./internal/wasmplugin']);
    // a deleted package, or a directory with no Go in it, belongs to its parent
    expect(goPackageDirs(['backend/internal/db/gone/x.go'], has).dirs).toEqual(['./internal/db']);
    expect(goPackageDirs(['backend/go.mod'], has).all).toBe(true);
    expect(goPackageDirs(['backend/nowhere/x.sql'], has).all).toBe(true);
    expect(goPackageDirs(null, has).all).toBe(true);
    expect(goPackageDirs(['web/src/a.ts'], has)).toEqual({ all: false, dirs: [] });
  });

  it('knows what the stamp writes', () => {
    const pkgs = ['web', 'packages/core'];
    for (const f of ['CHANGELOG.md', 'web/package.json', 'packages/core/package.json', 'deploy/helm/filex/Chart.yaml']) expect(stampWrites(f, pkgs), f).toBe(true);
    for (const f of ['package.json', 'web/src/a.ts', 'docs/CHANGELOG.md', 'packages/core/src/package.json']) expect(stampWrites(f, pkgs), f).toBe(false);
  });

  describe('a scripts/chain run stands in only when it proves this release', () => {
    const green = { schema: 1, run_id: 'full-x', profile: 'full', sha: 'abc', finished: '2026-10-06T07:00:00Z', ok: true, stopped: false, dirty_files: 0 };
    const opts = { accepted: ['full', 'nightly'], since: [] as string[] | null, stampFile: (f: string) => f === 'CHANGELOG.md' };
    it('a finished green run of an accepted profile, on a commit of this release with only the stamp since', () => {
      expect(chainVerdict(green, { ...opts, since: ['CHANGELOG.md'] }).ok).toBe(true);
    });
    it.each([
      ['red', { ...green, ok: false }, opts, /red/],
      ['stopped', { ...green, ok: false, stopped: true }, opts, /stopped/],
      ['unfinished', { ...green, finished: null, ok: null }, opts, /never finished/],
      ['another profile', { ...green, profile: 'targeted' }, opts, /profile targeted/],
      ['a dirty checkout', { ...green, dirty_files: 3 }, opts, /uncommitted/],
      ['another schema', { ...green, schema: 2 }, opts, /schema 2/],
      ['a commit this release does not contain', green, { ...opts, since: null }, /does not contain/],
      ['code changed since', green, { ...opts, since: ['CHANGELOG.md', 'e2e/tests/98.spec.ts'] }, /beyond the stamp/],
    ])('not %s', (_what, result, o, why) => {
      const v = chainVerdict(result, o);
      expect(v.ok).toBe(false);
      expect(v.problems.join('\n')).toMatch(why);
    });
    it('not something that is not a result at all', () => {
      expect(chainVerdict(null, opts).ok).toBe(false);
    });
  });
});
