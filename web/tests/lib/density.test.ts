// The density switch in User settings is a SECOND writer of a key that
// packages/core owns. That makes the key name the entire contract, and a
// contract nobody checks is how a control becomes decoration: rename
// `filex.density` in core and this switch keeps saving, keeps reading its own
// value back, and the file list never changes again — the exact "saves, reads
// back, does nothing" shape the modal was written to avoid.
//
// So the gate parses core's Toolbar and compares. It is cheap because both
// halves are in this repo; it would be impossible if core were a third party,
// in which case the switch should not exist.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import { DENSITY_KEY, getDensity, setDensity } from '@/lib/density';

const TOOLBAR = path.resolve(
  __dirname,
  '../../../packages/core/src/components/Toolbar.vue',
);

describe('density preference', () => {
  it('writes the key packages/core reads', () => {
    const src = readFileSync(TOOLBAR, 'utf8');
    const m = src.match(/DENSITY_LS_KEY\s*=\s*'([^']+)'/);
    // Guard for the guard: a parser that matches nothing would make the
    // assertion below a tautology.
    expect(m, `no DENSITY_LS_KEY declaration found in ${TOOLBAR}`).not.toBeNull();
    expect(DENSITY_KEY).toBe(m![1]);
  });

  it('round-trips, and reads the same values core does', () => {
    expect(getDensity()).toBe('comfortable'); // nothing stored yet
    setDensity('compact');
    expect(localStorage.getItem(DENSITY_KEY)).toBe('compact');
    expect(getDensity()).toBe('compact');
    setDensity('comfortable');
    expect(getDensity()).toBe('comfortable');
  });

  it('treats any unknown stored value as comfortable, exactly like core', () => {
    // core: `localStorage.getItem(KEY) === 'compact' ? 'compact' : 'comfortable'`
    localStorage.setItem(DENSITY_KEY, 'cozy');
    expect(getDensity()).toBe('comfortable');
  });
});
