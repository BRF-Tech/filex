// The export's rewrite (example.com → example.com, the GitLab paths → GitHub) must
// reach every file the repository tracks.
//
// ⚠⚠ It walks the public tree and skips directories BY NAME, to stay out of
// build output. `release` and `build` were on that list, and they are also the
// names of source directories: scripts/release/ (the release tool),
// desktop/build/ (installer resources), web/tests/fixtures/release/. 27 tracked
// files were published exactly as written in the private tree. The first real
// `pnpm release` (v0.45.0, 2026-09-25) stopped at its private-host gate on
// scripts/release/plan.mjs naming the operator's own instance. A directory
// name that holds tracked files is not build output.

import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '..', '..', '..');
const script = readFileSync(path.join(REPO, 'scripts', 'export-public.sh'), 'utf8');

function skippedNames(): string[] {
  const m = /dirs\[:\] = \[d for d in dirs\s+if d not in \(([^)]*)\)\]/.exec(script);
  if (!m) throw new Error('export-public.sh: the rewrite walk no longer filters directories the way this test reads it');
  return [...m[1].matchAll(/'([^']+)'/g)].map((x) => x[1]!);
}

describe('export rewrite scope', () => {
  it('skips no directory that holds a tracked file', () => {
    const skipped = new Set(skippedNames());
    expect(skipped.has('node_modules')).toBe(true);
    const tracked = execFileSync('git', ['ls-files'], { cwd: REPO, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
      .split('\n')
      .filter(Boolean);
    const missed = tracked.filter((f) => f.split('/').slice(0, -1).some((seg) => skipped.has(seg)));
    expect(missed, `tracked files the rewrite never sees: ${missed.slice(0, 8).join(', ')}`).toEqual([]);
  });
});
