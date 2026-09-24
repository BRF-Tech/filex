// Every plural form of an explorer message says the COUNT, never a literal "1".
//
// ⚠⚠ Why (translator finding, v0.43.0 — "explorer plurals have no zero form,
// 0 éléments"): the explorer picks a message's form by the language's CLDR
// category (`useLocale → countedKey`). In French the category `one` covers 0
// AND 1; in Russian it covers 1, 21, 31…. The English `_one` forms were
// written with a literal "1" ("1 item"), the language packs translated them
// the same way ("1 élément"), and so a French folder with nothing in it said
// "1 élément" — the singular form, with the wrong number baked in. A form that
// carries `{n}` reads "0 élément" in French and "21 файл" in Russian, which is
// what those languages say.
//
// The rule: when the plain message counts with `{count}`, `{n}` or `{days}`,
// every `_zero` / `_one` / `_two` / `_few` / `_many` form of it carries that
// same placeholder. The exceptions below name the thing instead of counting it.
//
// ⚠⚠ ALL THREE catalogues, since v0.43.0's pack sync: the explorer's, the
// server's `server.*` (srvtext + the notification phrases) and the admin
// panel's `one | other` choices. The explorer was swept in this release and
// the other two were not, so ten `server.*` `_one` forms and three admin
// choices still typed a literal `1` — and the es/de/fr translators copied it,
// three of them reporting that a `{count}` in a `server.*` form would be
// refused at run time. It is not: `srvtext.fits` builds the permitted set
// from the PLAIN key's placeholders and `srvtext.Plural` fills `count` before
// every other value (backend/internal/srvtext/srvtext.go).
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';
import { COUNT_VARS, useLocale } from '@brftech/filex-core/src/composables/useLocale';
import { loadCatalogue } from '../../../scripts/lib/i18n-catalogue.mjs';

/** A singular that names the one item rather than counting it. */
const NAMES_THE_ITEM = new Set(['toast.restore_taken_one']);

const FORMS = ['zero', 'one', 'two', 'few', 'many'];

function offenders(table: Record<string, string>): string[] {
  const out: string[] = [];
  for (const [key, value] of Object.entries(table)) {
    const m = /^(.*)_(zero|one|two|few|many)$/.exec(key);
    if (!m || NAMES_THE_ITEM.has(key)) continue;
    const base = table[m[1]];
    if (typeof base !== 'string') continue;
    const counter = COUNT_VARS.find((v) => base.includes(`{${v}}`));
    if (!counter) continue;
    if (!value.includes(`{${counter}}`)) out.push(`${key}: "${value}"`);
  }
  return out;
}

describe('explorer plural forms carry the count', () => {
  it('finds plural forms at all (a scanner that matched nothing would pass everything)', () => {
    const forms = Object.keys(en).filter((k) => FORMS.some((f) => k.endsWith(`_${f}`)));
    expect(forms.length).toBeGreaterThan(20);
  });

  it('English', () => {
    expect(offenders(en)).toEqual([]);
  });

  it('Turkish', () => {
    expect(offenders(tr)).toEqual([]);
  });

  it('a language whose `one` covers zero reads the real number', () => {
    // English itself has `one` only for 1, so this is the English table under
    // French's plural rule — exactly what a pack translated from it inherits.
    const { t } = useLocale(() => 'en');
    expect(t('inspector.folder_items', { n: 1 })).toBe('1 item');
    expect(t('inspector.folder_items', { n: 0 })).toBe('0 items');
    expect(en['inspector.folder_items_one'].replace('{n}', '0')).toBe('0 item');
  });
});

/* ── the server's catalogue (`server.*`) ──────────────────────────────── */

const cat = loadCatalogue(path.resolve(__dirname, '../../..')) as Record<string, Record<string, string>>;

/** `<base>_<category>` for the five category suffixes — srvtext's splitForm. */
function splitForm(key: string): { base: string; cat: string } | null {
  const m = /^(.*)_(zero|one|two|few|many)$/.exec(key);
  return m ? { base: m[1], cat: m[2] } : null;
}

/**
 * Server forms that type a number instead of carrying `{count}`.
 *
 * ⚠ `base` must be a key of the ENGLISH table: `server.public.drop_err_too_many`
 * ends in `_many` but its "base" (`…drop_err_too`) is not a key, so it is a
 * plain message, not a form of one.
 */
function serverOffenders(table: Record<string, string>): string[] {
  const out: string[] = [];
  for (const [key, value] of Object.entries(table)) {
    const f = splitForm(key);
    if (!f) continue;
    const base = cat.server[f.base];
    if (typeof base !== 'string') continue;
    const counter = COUNT_VARS.find((v) => base.includes(`{${v}}`));
    if (!counter) continue;
    if (!value.includes(`{${counter}}`)) out.push(`${key}: "${value}"`);
  }
  return out;
}

describe('server plural forms carry the count', () => {
  it('finds the server forms at all (a scanner that matched nothing would pass everything)', () => {
    const forms = Object.keys(cat.server).filter((k) => splitForm(k) && cat.server[splitForm(k)!.base]);
    expect(forms.length).toBeGreaterThan(8);
    expect(forms).toContain('server.mail.valid_days_one');
    expect(forms).toContain('server.notify.drop.received.title_one');
  });

  it('English', () => {
    expect(serverOffenders(cat.server)).toEqual([]);
  });

  it('Turkish', () => {
    expect(serverOffenders(cat.serverTr)).toEqual([]);
  });

  it('a form that carries {count} is one the server will accept — the plain key decides', () => {
    /* The half of `srvtext.fits` this test stands in for: a category form may
       carry every placeholder the PLAIN key carries, `{count}` included. A
       form with a placeholder the plain key has not is what gets refused. */
    for (const key of ['server.mail.valid_days_one', 'server.public.file_count_one']) {
      const base = cat.server[key.replace(/_one$/, '')];
      const extra = [...(cat.server[key].match(/\{[A-Za-z0-9_]+\}/g) ?? [])].filter((p) => !base.includes(p));
      expect(extra, `${key} uses a placeholder its plain key does not`).toEqual([]);
    }
  });
});

/* ── the admin panel's `one | other` choices ──────────────────────────── */

/** The admin panel counts with one more name than the explorer does
 *  (`duplicates.summary` counts groups) — the list web/tests/i18n/plurals.ts
 *  scans with. */
const WEB_COUNT_VARS = ['count', 'n', 'groups', 'days'];

/** vue-i18n choice forms whose branches disagree about the counting value. */
function adminOffenders(table: Record<string, string>): string[] {
  const out: string[] = [];
  for (const [key, value] of Object.entries(table)) {
    if (!value.includes('|')) continue;
    const forms = value.split('|').map((f) => f.trim());
    const counter = WEB_COUNT_VARS.find((v) => forms.some((f) => f.includes(`{${v}}`)));
    if (!counter) {
      out.push(`${key}: "${value}" — a choice with no counting value in any form`);
      continue;
    }
    const bare = forms.filter((f) => !f.includes(`{${counter}}`));
    if (bare.length) out.push(`${key}: "${value}"`);
  }
  return out;
}

describe('admin plural choices carry the count', () => {
  it('finds the choices at all', () => {
    const choices = Object.entries(cat.admin).filter(([, v]) => v.includes('|'));
    expect(choices.length).toBeGreaterThan(10);
  });

  it('English', () => {
    expect(adminOffenders(cat.admin)).toEqual([]);
  });

  it('Turkish', () => {
    expect(adminOffenders(cat.adminTr)).toEqual([]);
  });
});
