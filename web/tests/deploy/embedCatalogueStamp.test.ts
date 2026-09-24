// The ONE embedded file a filex rewrites as it serves it, and the allowance
// `scripts/check-embed.mjs` makes for it.
//
// ⚠⚠ Why this is a test. `check-embed` proves a binary serves web/dist byte
// for byte before `pnpm shots` takes a picture — the check that exists because
// 71 screenshots were once taken of a 16-hour-old interface. Exactly one file
// breaks byte equality on purpose: the translator's context file goes out with
// the RUNNING binary's version in its `filex` field
// (backend/internal/api/catalogue_version.go), so a pack's coverage is measured
// against the server it was taken from. A DEV build stamps nothing, so nobody
// had met this; a release build stamps, and the check called it a stale binary
// (measured 2026-09-23 on the v0.43.0 screenshot run, in the container scene).
//
// The allowance is therefore narrow, and these are the two ways it could go
// wrong quietly:
//
//   1. it stops being narrow — `canonicalJSON` ignoring more than the version;
//   2. it stops describing the server — the path or the field is renamed in
//      Go, and check-embed goes on excusing a file nothing rewrites, which is
//      a real difference waved through.

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { CATALOGUE_CONTEXT, canonicalJSON } from '../../../scripts/check-embed.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const GO = readFileSync(path.join(REPO, 'backend', 'internal', 'api', 'catalogue_version.go'), 'utf8');

describe('the allowance still describes what the server does', () => {
  it('names the path the handler serves', () => {
    const declared = /catalogueContextPath\s*=\s*"([^"]+)"/.exec(GO);
    expect(declared, 'catalogue_version.go no longer declares catalogueContextPath').not.toBeNull();
    expect(
      CATALOGUE_CONTEXT,
      'check-embed excuses a different file than the one the server rewrites — either the excuse is ' +
        'now waving through a real difference, or a release binary will be reported as stale again',
    ).toBe(declared![1]);
  });

  it('names the field the handler replaces', () => {
    // `doc["filex"], _ = json.Marshal(v)` — the version goes into this key and
    // no other, which is why dropping it is enough to compare the rest.
    expect(
      GO,
      'the handler no longer writes the version into "filex" — check-embed drops that key and would ' +
        'compare two documents that differ somewhere it is not looking',
    ).toContain('doc["filex"]');
  });

  it('a development build stamps nothing, so this path is release-only', () => {
    // releaseVersion() returns "" for dev/snapshot builds; the file then goes
    // out exactly as built and the ordinary byte comparison applies.
    expect(GO).toMatch(/strings\.Contains\(v,\s*"dev"\)/);
    expect(GO).toMatch(/strings\.Contains\(v,\s*"snapshot"\)/);
  });
});

describe('the comparison the allowance uses', () => {
  const built = { filex: '0.42.2', generated: '2026-09-01', keys: 3021, langs: ['en', 'tr'] };

  it('says two documents match when only the version differs', () => {
    const served = { ...built, filex: '0.43.0' };
    expect(canonicalJSON(served, 'filex')).toBe(canonicalJSON(built, 'filex'));
  });

  it('does not care about key order — Go marshals a map alphabetically', () => {
    const reordered = { langs: ['en', 'tr'], keys: 3021, filex: '0.43.0', generated: '2026-09-01' };
    expect(canonicalJSON(reordered, 'filex')).toBe(canonicalJSON(built, 'filex'));
  });

  it('still sees every OTHER difference — this is the half that must not rot', () => {
    for (const changed of [
      { ...built, keys: 3202 },
      { ...built, generated: '2026-09-23' },
      { ...built, langs: ['en'] },
      { ...built, langs: ['tr', 'en'] },
      { ...built, extra: true },
    ]) {
      expect(canonicalJSON(changed, 'filex'), JSON.stringify(changed)).not.toBe(canonicalJSON(built, 'filex'));
    }
  });

  it('drops the version at every level, and nothing else nested', () => {
    const a = { meta: { filex: '0.43.0', n: 1 }, list: [{ filex: 'x', n: 2 }] };
    const b = { meta: { filex: '0.42.2', n: 1 }, list: [{ filex: 'y', n: 2 }] };
    const c = { meta: { filex: '0.42.2', n: 9 }, list: [{ filex: 'y', n: 2 }] };
    expect(canonicalJSON(a, 'filex')).toBe(canonicalJSON(b, 'filex'));
    expect(canonicalJSON(a, 'filex')).not.toBe(canonicalJSON(c, 'filex'));
  });
});
