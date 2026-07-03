// Translation parity test. If a key exists in en.json it MUST exist in
// tr.json and vice versa — otherwise switching locales would fall back to
// the literal key, which is the worst-case UX.
import { describe, it, expect } from 'vitest';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

type Tree = Record<string, unknown>;

function flatten(obj: Tree, prefix = ''): string[] {
  const out: string[] = [];
  for (const [k, v] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      out.push(...flatten(v as Tree, path));
    } else {
      out.push(path);
    }
  }
  return out;
}

describe('i18n key parity', () => {
  const enKeys = new Set(flatten(en as Tree));
  const trKeys = new Set(flatten(tr as Tree));

  it('every en.json key is present in tr.json', () => {
    const missing: string[] = [];
    for (const k of enKeys) {
      if (!trKeys.has(k)) missing.push(k);
    }
    expect(missing, `keys missing from tr.json: ${missing.join(', ')}`).toEqual([]);
  });

  it('every tr.json key is present in en.json', () => {
    const missing: string[] = [];
    for (const k of trKeys) {
      if (!enKeys.has(k)) missing.push(k);
    }
    expect(missing, `keys missing from en.json: ${missing.join(', ')}`).toEqual([]);
  });

  it('no empty translation values', () => {
    const empties: string[] = [];
    function walk(obj: Tree, prefix = '') {
      for (const [k, v] of Object.entries(obj)) {
        const path = prefix ? `${prefix}.${k}` : k;
        if (typeof v === 'string') {
          if (v.trim() === '') empties.push(`en.json:${path}`);
        } else if (v && typeof v === 'object') {
          walk(v as Tree, path);
        }
      }
    }
    walk(en as Tree);
    walk(tr as Tree, ''); // tr is appended below — refactor to keep separate lists
    // We don't share a single `empties` for both files because one bad
    // string in either is enough to fail. Simple aggregate is fine here.
    expect(empties).toEqual([]);
  });
});
