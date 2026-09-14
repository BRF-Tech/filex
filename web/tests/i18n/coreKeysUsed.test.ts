// Every `t('…')` in packages/core resolves to a real catalogue entry.
//
// ⚠⚠ Why this file exists beside `coreKeys.test.ts`, which already guards the
// catalogues. That one compares en.ts against tr.ts — so it catches a key that
// exists on ONE side, and is perfectly green for a key that exists on NEITHER.
// Its own header says it was written to catch the `convert.*` modal, which
// shipped "~10 labels whose keys existed in neither catalogue"; against that
// exact bug it would in fact have passed, because both files agreed there was
// nothing there.
//
// Measured, 2026-09-13: the column menu's two move buttons were written with
// `t('cols.move_left')` and `t('cols.move_right')`, neither key was ever added,
// parity was green, the build was green, and the buttons shipped with
// `cols.move_left` as their tooltip and their screen-reader label. Nothing on
// screen looked wrong — an icon button's label is invisible until somebody
// hovers it or listens to it.
//
// So this reads the SOURCE and asks the catalogue about every literal key it
// finds. Dynamic keys (`t('keep.badge_' + kind)`, `t(\`empty.${view}.hint\`)`,
// `t(someComputedKey)`) are deliberately skipped: there is no honest way to
// enumerate them here, and a scanner that guessed would either miss them or
// invent failures. Literal keys are the overwhelming majority and the ones a
// typo actually reaches.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';

import { describe, expect, it } from 'vitest';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';

const CORE_SRC = resolve(__dirname, '../../../packages/core/src');

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

/**
 * `t('some.key')` — and nothing else.
 *
 * ⚠ The closing quote must be followed by `)` or `,`: without it, `t('keep.
 * badge_' + b)` matches its own first fragment and the test demands a key
 * called `keep.badge_` that is not supposed to exist. The comma is what lets
 * `t('owner.last_actor', { who })` through, which does have a real key.
 */
const CALL = /\bt\(\s*'([A-Za-z0-9_.]+)'\s*[),]/g;

describe('every literal t() key in packages/core exists', () => {
  const files = walk(CORE_SRC);

  it('finds source to scan at all', () => {
    // A path that silently resolved to nothing would make every assertion
    // below vacuously true — the most comfortable kind of dead gate.
    expect(files.length).toBeGreaterThan(30);
  });

  it('resolves in en.ts', () => {
    const missing: string[] = [];
    for (const f of files) {
      const src = readFileSync(f, 'utf8');
      for (const m of src.matchAll(CALL)) {
        if (!(m[1] in en)) missing.push(`${f.slice(CORE_SRC.length + 1)} → ${m[1]}`);
      }
    }
    expect([...new Set(missing)], `t() keys with no en.ts entry:\n${missing.join('\n')}`).toEqual([]);
  });

  it('resolves in tr.ts', () => {
    const missing: string[] = [];
    for (const f of files) {
      const src = readFileSync(f, 'utf8');
      for (const m of src.matchAll(CALL)) {
        if (!(m[1] in tr)) missing.push(`${f.slice(CORE_SRC.length + 1)} → ${m[1]}`);
      }
    }
    expect([...new Set(missing)], `t() keys with no tr.ts entry:\n${missing.join('\n')}`).toEqual([]);
  });
});
