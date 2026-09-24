// A counted message agrees with its number — "1 item", never "1 items".
//
// ⚠⚠ Found by the v0.41.0 screenshot pass (2026-09-14): the details panel's
// header read "1 items" over a folder holding one file. It was not one string.
// Both catalogues are flat tables with no plural machinery of their own, so
// EVERY `{n} <nouns>` message printed the plural for one, and a handful had
// been papered over with "item(s)" / "link(s)" / "storage(s)". Two call sites
// had each written their own singular picker, and nobody else had.
//
// The fix is one rule per catalogue, and these tests hold the catalogues to it:
//
//   • packages/core — `t()` reads `key_<category>` for the count's CLDR plural
//     category in the active language (composables/useLocale → countedKey;
//     for English that is `key_one` when the count is exactly one). A counted
//     English message needs a `_one` sibling; Turkish carries the same key with
//     the same words, because Turkish does not inflect a noun after a number
//     ("1 öğe", "5 öğe").
//   • web (vue-i18n) — the library's own choice syntax, `singular | plural`,
//     with the number passed as `t(key, vars, count)`. A choice message called
//     WITHOUT the count silently renders its first form for every number, so
//     each call site is checked for the third argument as well.
//
// Other languages' forms (Arabic's six, Russian's four) and the pack-vs-English
// lookup order are pinned in pluralCategories.test.ts.
//
// ⚠ English only is scanned for nouns: the Turkish side is covered by key
// parity (coreKeys.test.ts, keys.test.ts) and must never be "pluralised".
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';

import { describe, expect, it } from 'vitest';
import { createI18n } from 'vue-i18n';
import { en as coreEn } from '@brftech/filex-core/src/locales/en';
import { tr as coreTr } from '@brftech/filex-core/src/locales/tr';
import { COUNT_VARS, countedKey, useLocale } from '@brftech/filex-core/src/composables/useLocale';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

/** `{count} items`, `{n} matching items`, `{days} days`, `{n} people`, `{n} link(s)`. */
function countedNoun(value: string, vars: readonly string[]): boolean {
  const names = vars.join('|');
  const plural = new RegExp(`\\{(?:${names})\\}\\)?\\s+(?:[a-z]+\\s+)?(?:[a-z]+s|people)\\b`);
  return plural.test(value) || hedged(value, vars);
}

/** "item(s)" after a count — a sentence that could not decide. */
function hedged(value: string, vars: readonly string[]): boolean {
  return new RegExp(`\\{(?:${vars.join('|')})\\}[^.|]*?[a-z]\\(s\\)`).test(value);
}

describe('core: t() picks the singular from the count', () => {
  const has = (k: string) => k === 'x.items_one';

  it('uses key_one for exactly one, whichever counting variable carries it', () => {
    for (const v of COUNT_VARS) {
      expect(countedKey('x.items', { [v]: 1 }, has)).toBe('x.items_one');
      expect(countedKey('x.items', { [v]: '1' }, has)).toBe('x.items_one');
    }
  });

  it('keeps the plain key for zero, many, no count, or no singular in the catalogue', () => {
    expect(countedKey('x.items', { n: 0 }, has)).toBe('x.items');
    expect(countedKey('x.items', { n: 2 }, has)).toBe('x.items');
    expect(countedKey('x.items', { size: 1 }, has)).toBe('x.items');
    expect(countedKey('y.items', { n: 1 }, has)).toBe('y.items');
  });

  it("reads the language's own category when told the language, and English's when not", () => {
    const ru = (k: string) => ['x.items_one', 'x.items_few', 'x.items_many'].includes(k);
    expect(countedKey('x.items', { n: 3 }, ru, 'ru')).toBe('x.items_few');
    expect(countedKey('x.items', { n: 5 }, ru, 'ru')).toBe('x.items_many');
    expect(countedKey('x.items', { n: 21 }, ru, 'ru')).toBe('x.items_one');
    expect(countedKey('x.items', { n: 3 }, ru)).toBe('x.items');
  });

  it('renders the details panel caption the way the screenshot should have read', () => {
    const enT = useLocale(() => 'en').t;
    const trT = useLocale(() => 'tr').t;
    expect(enT('inspector.folder_items', { n: 1 })).toBe('1 item');
    expect(enT('inspector.folder_items', { n: 5 })).toBe('5 items');
    expect(enT('inspector.folder_items', { n: 0 })).toBe('0 items');
    // Turkish is unchanged by the rule — and must stay so.
    expect(trT('inspector.folder_items', { n: 1 })).toBe('1 öğe');
    expect(trT('inspector.folder_items', { n: 5 })).toBe('5 öğe');
  });
});

describe('core catalogue: every counted English message has a singular', () => {
  const counted = Object.entries(coreEn).filter(
    ([k, v]) => !k.endsWith('_one') && countedNoun(v, COUNT_VARS),
  );

  it('finds the counted messages at all (a scanner that matched nothing would pass everything)', () => {
    expect(counted.map(([k]) => k)).toContain('inspector.folder_items');
    expect(counted.length).toBeGreaterThan(10);
  });

  for (const [key, value] of counted) {
    it(`${key} — "${value}"`, () => {
      const one = coreEn[`${key}_one`];
      expect(one, `add '${key}_one' to en.ts (and the same words to tr.ts)`).toBeTruthy();
      expect(one).not.toMatch(/\(s\)/);
      expect(hedged(value, COUNT_VARS), 'say it in full: "items", not "item(s)"').toBe(false);
      expect(coreTr[`${key}_one`], `add '${key}_one' to tr.ts`).toBeTruthy();
    });
  }
});

/* ── web (vue-i18n) ───────────────────────────────────────────────────── */

type Tree = Record<string, unknown>;
function flatten(obj: Tree, prefix = '', out: Record<string, string> = {}): Record<string, string> {
  for (const [k, v] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object' && !Array.isArray(v)) flatten(v as Tree, path, out);
    else if (typeof v === 'string') out[path] = v;
  }
  return out;
}

const WEB_COUNT_VARS = ['count', 'n', 'groups', 'days'] as const;
const webEn = flatten(en as Tree);

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) {
      if (name === 'locales' || name === 'node_modules') continue;
      walk(p, out);
    } else if (/\.(vue|ts)$/.test(name)) out.push(p);
  }
  return out;
}

/** The argument list of the call that starts at `open` (the index of its `(`). */
function callArgs(src: string, open: number): string[] {
  const args: string[] = [];
  let depth = 0;
  let quote = '';
  let start = open + 1;
  for (let i = open; i < src.length; i++) {
    const c = src[i];
    if (quote) {
      if (c === quote && src[i - 1] !== '\\') quote = '';
      continue;
    }
    if (c === "'" || c === '"' || c === '`') quote = c;
    else if (c === '(' || c === '{' || c === '[') depth++;
    else if (c === ')' || c === '}' || c === ']') {
      depth--;
      if (depth === 0) {
        args.push(src.slice(start, i).trim());
        return args;
      }
    } else if (c === ',' && depth === 1) {
      args.push(src.slice(start, i).trim());
      start = i + 1;
    }
  }
  return args;
}

describe('web catalogue: counted English messages are vue-i18n choices', () => {
  const counted = Object.entries(webEn).filter(([, v]) => countedNoun(v.split('|').pop()!, WEB_COUNT_VARS));

  it('finds them', () => {
    expect(counted.map(([k]) => k)).toContain('duplicates.copies');
  });

  for (const [key, value] of counted) {
    it(`${key} — "${value}"`, () => {
      const forms = value.split('|').map((f) => f.trim());
      expect(forms.length, 'write "one form | other form" and pass the count as t(key, vars, count)').toBe(2);
      for (const f of forms) expect(hedged(f, WEB_COUNT_VARS)).toBe(false);
    });
  }

  it('renders the singular for one and the plural for many, and Turkish reads the same either way', () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en, tr } });
    const t = i18n.global.t;
    expect(t('duplicates.copies', { n: 1 }, 1)).toBe('1 copy');
    expect(t('duplicates.copies', { n: 3 }, 3)).toBe('3 copies');
    expect(t('plugins.conformance.failed', { count: 1 }, 1)).toBe('1 probe failed');
    i18n.global.locale.value = 'tr';
    expect(t('duplicates.copies', { n: 1 }, 1)).toBe('1 kopya');
    expect(t('duplicates.copies', { n: 3 }, 3)).toBe('3 kopya');
  });

  it('every call of a choice message passes the count', () => {
    const choices = Object.entries(webEn)
      .filter(([, v]) => v.includes('|'))
      .map(([k]) => k);
    const missing: string[] = [];
    let calls = 0;
    for (const f of walk(resolve(__dirname, '../../src'))) {
      const src = readFileSync(f, 'utf8');
      for (const key of choices) {
        const needle = `t('${key}'`;
        for (let at = src.indexOf(needle); at !== -1; at = src.indexOf(needle, at + 1)) {
          calls++;
          const args = callArgs(src, at + 1);
          if (args.length < 3) missing.push(`${f.split(/[\\/]src[\\/]/).pop()} → ${src.slice(at, at + 80)}`);
        }
      }
    }
    expect(calls).toBeGreaterThanOrEqual(7);
    expect(missing, missing.join('\n')).toEqual([]);
  });
});
