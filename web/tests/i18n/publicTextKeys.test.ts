// Every `pt('…')` in packages/core is a sentence the SERVER has.
//
// ⚠ Why (#210, 0.54): the public pages (`/s/<token>`, `/d/<token>`) say only
// the server catalogue's `server.public.*` words, fetched from GET
// /api/public/strings (core composables/usePublicText). `coreKeysUsed.test.ts`
// guards `t('…')` against the CORE catalogue; a `pt('…')` key lives in another
// file - backend/internal/srvtext/locales/en.json - and a typo there would
// render as nothing at all (the page has no fallback words, on purpose). This
// reads the source and asks the server catalogue about every literal key.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';

import { describe, expect, it } from 'vitest';
import { en } from '@brftech/filex-core/src/locales/en';

const CORE_SRC = resolve(__dirname, '../../../packages/core/src');
const SERVER_EN = resolve(__dirname, '../../../backend/internal/srvtext/locales/en.json');
const SERVER_TR = resolve(__dirname, '../../../backend/internal/srvtext/locales/tr.json');

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) {
      if (name === 'locales' || name === 'node_modules') continue;
      walk(p, out);
    } else if (/\.(vue|ts)$/.test(name)) {
      out.push(p);
    }
  }
  return out;
}

/** `pt('key'` and `said('key'` - the two readers of the server's table. */
const CALL = /\b(?:pt|said)\(\s*'([A-Za-z0-9_]+)'\s*[),]/g;

describe('the public pages read only sentences the server has', () => {
  const files = walk(CORE_SRC);
  const serverEn = JSON.parse(readFileSync(SERVER_EN, 'utf8')) as Record<string, string>;
  const serverTr = JSON.parse(readFileSync(SERVER_TR, 'utf8')) as Record<string, string>;
  const keys = new Map<string, string>();
  for (const f of files) {
    for (const m of readFileSync(f, 'utf8').matchAll(CALL)) keys.set(m[1], f.slice(CORE_SRC.length + 1));
  }

  it('finds the readers at all', () => {
    // A scan that found nothing would pass everything below.
    expect(keys.size).toBeGreaterThan(15);
  });

  it('every key is server.public.<key> in the English catalogue', () => {
    const missing = [...keys].filter(([k]) => !(`server.public.${k}` in serverEn)).map(([k, f]) => `${f} → ${k}`);
    expect(missing).toEqual([]);
  });

  it('and in the Turkish one', () => {
    const missing = [...keys].filter(([k]) => !(`server.public.${k}` in serverTr)).map(([k, f]) => `${f} → ${k}`);
    expect(missing).toEqual([]);
  });

  it('the core catalogue keeps no second copy of them (no public.* key)', () => {
    expect(Object.keys(en).filter((k) => k.startsWith('public.'))).toEqual([]);
  });
});
