// A patch release's Go gate (scripts/release/gates/go-targeted.sh, task #172)
// tests the packages the patch changed AND every package that imports one of
// them - directly, through another package, or from its tests - and the whole
// module whenever it cannot tell.
//
// ⚠ Why the importers: a change to internal/perm can break internal/api's
// handlers without one line of them changing. Testing only the changed
// directory would pass that patch; the whole suite (GitHub, on the export
// commit) would find it an hour later.
//
// A stand-in `go` answers `go list` from a fixed dependency graph and writes
// down what it was asked to vet and test; nothing is built.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

import { findBash, slash } from '../../../scripts/release/engine.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const SCRIPT = path.join(REPO, 'scripts', 'release', 'gates', 'go-targeted.sh');
const bash = findBash();
const TIMEOUT = 60_000;
const M = 'example.com/m';

// ImportPath | every dependency (transitive, as `go list` gives .Deps) | test imports
const GRAPH = [
  `${M}/internal/a||`,
  `${M}/internal/b|${M}/internal/a|`,
  `${M}/internal/c|${M}/internal/b ${M}/internal/a|`,
  `${M}/internal/d|${M}/internal/x|${M}/internal/b`,
  `${M}/internal/e|${M}/internal/x|`,
  `${M}/internal/x||`,
  `${M}/cmd/filex|${M}/internal/c ${M}/internal/b ${M}/internal/a|`,
].join('\n');

const temps: string[] = [];
afterEach(() => {
  for (const d of temps.splice(0)) fs.rmSync(d, { recursive: true, force: true });
});

function goTargeted(dirs: string | undefined) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-go-targeted-'));
  temps.push(dir);
  const bin = path.join(dir, 'bin');
  fs.mkdirSync(bin);
  fs.writeFileSync(path.join(dir, 'graph.txt'), `${GRAPH}\n`);
  fs.writeFileSync(
    path.join(bin, 'go'),
    [
      '#!/usr/bin/env bash',
      'case "$1" in',
      '  list)',
      '    shift',
      '    [ "$1" = -e ] && shift',
      '    if [ "$1" = -f ]; then cat "$GO_GRAPH"; exit 0; fi',
      `    for d in "$@"; do echo "${M}/\${d#./}"; done`,
      '    ;;',
      '  vet|test) echo "$*" >> "$GO_LOG" ;;',
      '  *) echo "unexpected: go $*" >&2; exit 2 ;;',
      'esac',
      '',
    ].join('\n'),
  );
  fs.chmodSync(path.join(bin, 'go'), 0o755);
  // One PATH key: Windows spells it Path, and a second spelling is a coin toss.
  const base: NodeJS.ProcessEnv = { ...process.env };
  const pathKey = Object.keys(base).find((k) => k.toUpperCase() === 'PATH') ?? 'PATH';
  const searchPath = base[pathKey];
  delete base[pathKey];
  delete base.FILEX_GO_DIRS;
  const log = path.join(dir, 'go.log');
  const env: NodeJS.ProcessEnv = { ...base, PATH: `${bin}${path.delimiter}${searchPath}`, GO_GRAPH: slash(path.join(dir, 'graph.txt')), GO_LOG: slash(log) };
  if (dirs !== undefined) env.FILEX_GO_DIRS = dirs;
  const r = spawnSync(bash!, [slash(SCRIPT)], { cwd: dir, encoding: 'utf8', env });
  const calls = fs.existsSync(log) ? fs.readFileSync(log, 'utf8').split('\n').filter(Boolean) : [];
  return { status: r.status, out: `${r.stdout}${r.stderr}`, calls };
}

describe.runIf(!!bash)("a patch's Go gate: the changed packages and their importers", () => {
  it('tests what changed, what depends on it, and what imports it from its tests - and nothing else', { timeout: TIMEOUT }, () => {
    const r = goTargeted('./internal/a');
    expect(r.status, r.out).toBe(0);
    const want = [`${M}/cmd/filex`, `${M}/internal/a`, `${M}/internal/b`, `${M}/internal/c`, `${M}/internal/d`].join(' ');
    expect(r.calls).toEqual([`vet ${want}`, `test -timeout 30m ${want}`]);
    expect(r.calls.join('\n')).not.toContain(`${M}/internal/e`);
    expect(r.calls.join('\n')).not.toContain(`${M}/internal/x`);
  });

  it('takes several changed packages at once', { timeout: TIMEOUT }, () => {
    const r = goTargeted('./internal/x ./internal/c');
    expect(r.status, r.out).toBe(0);
    const want = [`${M}/cmd/filex`, `${M}/internal/c`, `${M}/internal/d`, `${M}/internal/e`, `${M}/internal/x`].join(' ');
    expect(r.calls).toEqual([`vet ${want}`, `test -timeout 30m ${want}`]);
  });

  it.each([
    ['./... (a change it could not place)', './...'],
    ['nothing named', undefined],
    ['an empty list', ''],
    ['a package no package in the module is or imports', './internal/zzz'],
  ])(
    'tests the whole module for %s',
    (_what, dirs) => {
      const r = goTargeted(dirs);
      expect(r.status, r.out).toBe(0);
      expect(r.calls).toEqual(['vet ./...', 'test -timeout 30m ./...']);
    },
    TIMEOUT,
  );
});
