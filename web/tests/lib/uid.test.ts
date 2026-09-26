// An upload must not depend on a secure context.
//
// ⚠ Why: `crypto.randomUUID` exists only on https or http://localhost. On
// plain http at any other address (a LAN IP, a VM) it is undefined, and the
// explorer called it to name every upload row: picking a file uploaded
// nothing, with "crypto.randomUUID is not a function" in the console. It had
// been so since the first commit; the sub-path e2e found it on 2026-09-26.
// One helper (lib/uid.ts) makes the id everywhere; this pins both.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { newId } from '@brftech/filex-core/src/lib/uid';

const V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const SRC = [
  path.resolve(__dirname, '../../../packages/core/src'),
  path.resolve(__dirname, '../../src'),
];

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = path.join(dir, n);
    if (statSync(p).isDirectory()) return n === 'node_modules' || n === 'dist' ? [] : files(p);
    return /\.(ts|vue)$/.test(n) && !n.endsWith('.d.ts') ? [p] : [];
  });
}

describe('newId', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('is a v4 UUID where randomUUID exists', () => {
    expect(newId()).toMatch(V4);
  });

  it('is still a random v4 UUID on an insecure page, where randomUUID is undefined', () => {
    const real = globalThis.crypto;
    vi.stubGlobal('crypto', { getRandomValues: (b: Uint8Array) => real.getRandomValues(b) });
    const a = newId();
    const b = newId();
    expect(a).toMatch(V4);
    expect(b).toMatch(V4);
    expect(a).not.toBe(b);
  });

  it('works with no crypto at all', () => {
    vi.stubGlobal('crypto', undefined);
    expect(newId()).toMatch(V4);
  });
});

describe('the explorer and the admin app', () => {
  it('never call crypto.randomUUID outside lib/uid.ts', () => {
    const offenders = SRC.flatMap(files)
      .filter((f) => !f.endsWith(path.join('lib', 'uid.ts')))
      .filter((f) => /\bcrypto\.randomUUID\s*\(/.test(readFileSync(f, 'utf8')))
      .map((f) => path.relative(path.resolve(__dirname, '../../..'), f));
    expect(offenders).toEqual([]);
  });
});
