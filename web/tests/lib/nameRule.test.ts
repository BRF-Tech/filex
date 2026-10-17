/**
 * Which names a file may be given, and how a name is SHOWN
 * (packages/core/src/lib/nameRule.ts, task #238).
 *
 * ⚠⚠ The server is the rule (backend/internal/leafname). Both sides are held
 * to the ONE list of hostile names (backend/internal/testutil/hostilenames/
 * names.json, read through e2e/helpers/hostile-names.mjs): the client refuses
 * exactly the names the list calls refused, the Go test refuses exactly the
 * same ones, so the two cannot drift apart.
 *
 * ⚠ No `\u` escape in this file: every invisible character is built from its
 * code point (lesson #332).
 */
import { describe, expect, it, vi } from 'vitest';

vi.mock('@brftech/filex-core/src/lib/uiLocales', async (orig) => {
  const real = (await orig()) as Record<string, unknown>;
  return {
    ...real,
    availableLocales: () => [
      { code: 'en', label: 'English', source: 'builtin' },
      { code: 'tr', label: 'Türkçe', source: 'builtin' },
      { code: 'ar', label: 'العربية', source: 'plugin', plugin: 'lang-ar', rtl: true },
    ],
  };
});

import {
  REPLACEMENT_CHARACTER,
  containBidi,
  isBidiControl,
  nameRefusal,
  onlyDots,
  trimName,
  visibleName,
} from '@brftech/filex-core/src/lib/nameRule';
import { useLocale } from '@brftech/filex-core/src/composables/useLocale';
import { HOSTILE_NAMES, hostileName } from '../../../e2e/helpers/hostile-names.mjs';

const cp = (n: number) => String.fromCodePoint(n);
const RLO = cp(0x202e);
const LRI = cp(0x2066);
const FSI = cp(0x2068);
const PDI = cp(0x2069);
const LRM = cp(0x200e);

/** Every code point of `s` that is a bidi control. */
const bidiIn = (s: string) => [...s].filter((c) => isBidiControl(c.codePointAt(0) ?? 0));

describe('giving a name: the server rule, said before the click', () => {
  it('walks the whole shared list', () => {
    expect(HOSTILE_NAMES.length).toBeGreaterThan(40);
  });

  for (const n of HOSTILE_NAMES) {
    it(`${n.id}: ${n.refused ? 'refused' : 'accepted'}, as the server does`, () => {
      expect(nameRefusal(n.name) !== null).toBe(n.refused);
    });
  }

  it('says why, with the server reasons', () => {
    expect(nameRefusal('')).toBe('empty');
    expect(nameRefusal('a/b')).toBe('separator');
    expect(nameRefusal('a\\b')).toBe('separator');
    expect(nameRefusal('a' + cp(0) + 'b')).toBe('nul');
    // Every other control character too (the owner's decision for 0.57).
    for (const c of [0x01, 0x09, 0x0a, 0x0d, 0x1b, 0x1f, 0x7f]) {
      expect(nameRefusal('a' + cp(c) + 'b'), `U+${c.toString(16)}`).toBe('control');
    }
    expect(nameRefusal(hostileName('control-tab').name)).toBe('control');
    expect(nameRefusal(hostileName('control-newline').name)).toBe('control');
    // Not a C0 control: a C1 one and the zero-width space stay names.
    expect(nameRefusal('a' + cp(0x85) + 'b')).toBeNull();
    expect(nameRefusal(hostileName('zero-width-space').name)).toBeNull();
    expect(nameRefusal('..')).toBe('dots');
    expect(nameRefusal('.. ')).toBe('dots');
    expect(nameRefusal('a..b')).toBeNull();
    expect(nameRefusal('..hidden')).toBeNull();
    // Every name of only dots and spaces with a dot, as leafname_test.go
    // (the owner's decision for 0.57): Windows trims each to the folder.
    for (const n of ['.', '..', '...', '.. ', ' . . . ', '. .', '. ', ' .']) {
      expect(onlyDots(n), JSON.stringify(n)).toBe(true);
      expect(nameRefusal(n), JSON.stringify(n)).toBe('dots');
    }
    for (const n of ['a..b', '..hidden', '.env', 'report.', ' a ']) {
      expect(onlyDots(n), JSON.stringify(n)).toBe(false);
    }
    // Only spaces: the empty name (leafname.OnlySpaces).
    expect(nameRefusal('   ')).toBe('empty');
    expect(nameRefusal(' ')).toBe('empty');
    expect(nameRefusal(' a ')).toBeNull();
  });

  it('trims the way the server does (Go strings.TrimSpace)', () => {
    expect(trimName('  a b  ')).toBe('a b');
    // U+0085 (NEXT LINE) is white space to Go and not to String#trim;
    // U+FEFF (BOM) is the reverse.
    expect(trimName(cp(0x85) + 'a' + cp(0x85))).toBe('a');
    expect(trimName(cp(0xfeff) + 'a')).toBe(cp(0xfeff) + 'a');
  });
});

describe('showing a name: no bidi control rearranges it', () => {
  for (const n of HOSTILE_NAMES) {
    it(`${n.id}: shown without a bidi control${n.tags.includes('bidi-control') ? '' : ', and otherwise as it is'}`, () => {
      const shown = visibleName(n.name);
      expect(bidiIn(shown)).toEqual([]);
      if (!n.tags.includes('bidi-control')) expect(shown).toBe(n.name);
    });
  }

  it('the RLO name keeps its true extension at the end', () => {
    expect(visibleName(hostileName('rlo-extension').name)).toBe(`invoice${REPLACEMENT_CHARACTER}fdp.exe`);
  });

  it('a row, a tile and a crumb are drawn through it (nodeDisplayName)', () => {
    const { nodeDisplayName } = useLocale(() => 'en');
    const rlo = hostileName('rlo-extension').name;
    expect(nodeDisplayName({ basename: rlo })).toBe(`invoice${REPLACEMENT_CHARACTER}fdp.exe`);
    expect(nodeDisplayName({ basename: 'x.txt', fxe_name: rlo })).toBe(`invoice${REPLACEMENT_CHARACTER}fdp.exe`);
  });

  it('"Copy name" copies the name itself, control and all (nodeName)', () => {
    const { nodeName } = useLocale(() => 'en');
    const rlo = hostileName('rlo-extension').name;
    expect(nodeName({ basename: rlo })).toBe(rlo);
    for (const n of HOSTILE_NAMES) expect(nodeName({ basename: n.name })).toBe(n.name);
  });

  it('the trash prefix is taken off a trash row only', () => {
    const { nodeDisplayName } = useLocale(() => 'en');
    const stored = '20261015-120000-ab12__invoice.pdf';
    expect(nodeDisplayName({ basename: stored })).toBe(stored);
    expect(nodeDisplayName({ basename: stored, trashed: true })).toBe('invoice.pdf');
    expect(nodeDisplayName({ basename: stored, extra_metadata: { deleted_at: '2026-10-15' } })).toBe('invoice.pdf');
    expect(nodeDisplayName({ basename: stored, path: 'docs://.trash/' + stored })).toBe('invoice.pdf');
  });
});

describe('a value in a sentence cannot turn the sentence around', () => {
  it('an override, or an isolate left open, is shown as U+FFFD', () => {
    expect(containBidi('a' + RLO + 'b')).toBe('a' + REPLACEMENT_CHARACTER + 'b');
    expect(containBidi('a' + LRI + 'b')).toBe('a' + REPLACEMENT_CHARACTER + 'b');
    expect(containBidi('a' + PDI + 'b')).toBe('a' + REPLACEMENT_CHARACTER + 'b');
  });

  it('what filex and Intl put there themselves stays: closed isolates, marks', () => {
    const closed = FSI + '3 MB' + PDI + ' ' + LRI + 'x' + PDI;
    expect(containBidi(closed)).toBe(closed);
    expect(containBidi('12' + LRM + '/10')).toBe('12' + LRM + '/10');
  });

  for (const code of ['en', 'ar']) {
    it(`t() in ${code}: the RLO name is shown, the rest of the sentence is untouched`, () => {
      const { t } = useLocale(() => code);
      const said = t('names.reserved', { name: hostileName('rlo-extension').name });
      expect(said).not.toContain(RLO);
      expect(said).toContain(`invoice${REPLACEMENT_CHARACTER}fdp.exe`);
    });
  }

  it("t() in Arabic: a size t() already rendered keeps its own isolation", () => {
    const { t } = useLocale(() => 'ar');
    const inner = t('size.at_least', { size: '3 MB' });
    const outer = t('names.reserved', { name: inner });
    expect(outer).toContain(inner);
    expect(outer).not.toContain(REPLACEMENT_CHARACTER);
  });
});
