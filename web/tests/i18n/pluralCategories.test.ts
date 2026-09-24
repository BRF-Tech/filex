// A counted message in a language that is not English.
//
// ⚠⚠ The failure this pins: both catalogues had ONE plural rule, English's.
// The explorer read `<key>_one` for exactly 1 and `<key>` otherwise; the admin
// panel left vue-i18n on its default (two forms: 1 → first, else second).
// The Arabic pack's translator could not write the language — Arabic has six
// CLDR cardinal categories, zero one two few many other — and Russian, Polish,
// Czech and Ukrainian need few/many beside one. Now both catalogues pick the
// form by the language's own CLDR category (`Intl.PluralRules`,
// packages/core lib/plural):
//
//   explorer  `<key>_zero` `_one` `_two` `_few` `_many`; plain `<key>` = other
//   admin     as many `|` forms as the language has categories, CLDR order;
//             1 / 2 / 3 forms keep their old meaning
//
// ⚠ And a second failure the old explorer lookup hid: it read a table with
// English merged UNDER the pack, so a Spanish pack that wrote only the plain
// `toast.restored` still "had" `toast.restored_one` — the English one — and a
// count of one printed "1 item restored" in the middle of a Spanish screen.
//
// Mutation-tested (v0.43.0): `pluralCategory` answering 'other' without
// asking Intl turns the Arabic, Russian, Spanish and English cases red in both
// catalogues; dropping the vue-i18n rule registration (web/src/i18n
// `registerPluralRule`) turns the Arabic and Russian admin cases red; merging
// English under a pack's own table turns the Spanish explorer case red.
import fs from 'node:fs';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  CLDR_ORDER,
  pluralCategories,
  pluralCategory,
  pluralChoiceIndex,
} from '@brftech/filex-core/src/lib/plural';
import { registerLocale, resetLocales } from '@brftech/filex-core/src/lib/uiLocales';
import { useLocale } from '@brftech/filex-core/src/composables/useLocale';
import { ttlCeilingHint } from '@brftech/filex-core/src/lib/shareTtl';

const TR_JSON = JSON.parse(
  fs.readFileSync(path.resolve(__dirname, '../../src/locales/tr.json'), 'utf8'),
);

/** The numbers the brief names, and the Arabic category each one is in. */
const AR_CASES: Array<[number, string]> = [
  [0, 'zero'],
  [1, 'one'],
  [2, 'two'],
  [3, 'few'],
  [11, 'many'],
  [100, 'other'],
];

/* ── fixtures: real sentences, one per category ───────────────────────── */

const AR_RESTORED: Record<string, string> = {
  zero: 'لم تتم استعادة أي عنصر',
  one: 'تمت استعادة عنصر واحد',
  two: 'تمت استعادة عنصرين',
  few: 'تمت استعادة {n} عناصر',
  many: 'تمت استعادة {n} عنصرًا',
  other: 'تمت استعادة {n} عنصر',
};
const AR_FILES: Record<string, string> = {
  zero: 'لا ملفات',
  one: 'ملف واحد',
  two: 'ملفان',
  few: '{n} ملفات',
  many: '{n} ملفًا',
  other: '{n} ملف',
};
const RU_RESTORED: Record<string, string> = {
  one: 'Восстановлен {n} элемент',
  few: 'Восстановлено {n} элемента',
  many: 'Восстановлено {n} элементов',
  other: 'Восстановлено {n} элемента (дробь)',
};
const RU_FILES: Record<string, string> = {
  one: '{n} файл',
  few: '{n} файла',
  many: '{n} файлов',
  other: '{n} файла (дробь)',
};

/** A pack's explorer strings: `<key>_<cat>` for each category, plain = other. */
function explorerForms(key: string, forms: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [cat, text] of Object.entries(forms)) out[cat === 'other' ? key : `${key}_${cat}`] = text;
  return out;
}

/** A pack's admin string: the forms in CLDR order, `|`-separated. */
function adminForms(forms: Record<string, string>): string {
  return CLDR_ORDER.filter((c) => c in forms)
    .map((c) => forms[c])
    .join(' | ');
}

const fill = (s: string, n: number) => s.replaceAll('{n}', String(n));

/* ── lib/plural ───────────────────────────────────────────────────────── */

describe('lib/plural: CLDR categories from Intl.PluralRules', () => {
  it('lists a language’s categories in CLDR order, whatever order the runtime answers in', () => {
    expect(pluralCategories('ar')).toEqual(['zero', 'one', 'two', 'few', 'many', 'other']);
    expect(pluralCategories('cy')).toEqual(['zero', 'one', 'two', 'few', 'many', 'other']);
    expect(pluralCategories('ru')).toEqual(['one', 'few', 'many', 'other']);
    expect(pluralCategories('uk')).toEqual(['one', 'few', 'many', 'other']);
    expect(pluralCategories('pl')).toEqual(['one', 'few', 'many', 'other']);
    expect(pluralCategories('lv')).toEqual(['zero', 'one', 'other']);
    expect(pluralCategories('ro')).toEqual(['one', 'few', 'other']);
    expect(pluralCategories('ja')).toEqual(['other']);
    expect(pluralCategories('zh')).toEqual(['other']);
    for (const l of ['en', 'tr', 'de']) expect(pluralCategories(l), l).toEqual(['one', 'other']);
  });

  it('counts the categories whole numbers 0–999 reach, not ICU’s list — `many` for round millions is not one', () => {
    // ⚠ ICU lists `many` for these (select(1000000) === 'many'); taken as-is,
    // Spanish would have three categories and a classic "zero | one | other"
    // admin string would be read in CLDR order — 1 picking the "zero" form.
    for (const l of ['es', 'fr', 'it', 'pt', 'pt-br', 'ca']) {
      expect(pluralCategories(l), l).toEqual(['one', 'other']);
    }
    expect(pluralCategory('es', 1_000_000)).toBe('many');
  });

  it('puts each number in the category the language does', () => {
    for (const [n, cat] of AR_CASES) expect(pluralCategory('ar', n), `ar ${n}`).toBe(cat);
    expect(pluralCategory('ru', 21)).toBe('one');
    expect(pluralCategory('ru', 3)).toBe('few');
    expect(pluralCategory('ru', 5)).toBe('many');
    expect(pluralCategory('en', -1)).toBe('one');
    expect(pluralCategory('en', Number.NaN)).toBe('other');
  });

  it('a tag Intl refuses reads as English instead of throwing', () => {
    expect(() => pluralCategories('%%not-a-tag')).not.toThrow();
    expect(pluralCategories('%%not-a-tag')).toEqual(['one', 'other']);
    expect(pluralCategory('%%not-a-tag', 1)).toBe('one');
    expect(pluralCategory('%%not-a-tag', 2)).toBe('other');
  });

  it('a runtime with no Intl.PluralRules reads as English instead of throwing', async () => {
    const real = Intl.PluralRules;
    try {
      (Intl as { PluralRules?: unknown }).PluralRules = undefined;
      vi.resetModules();
      const fresh = await import('@brftech/filex-core/src/lib/plural');
      expect(fresh.pluralCategories('ar')).toEqual(['one', 'other']);
      expect(fresh.pluralCategory('ar', 1)).toBe('one');
      expect(fresh.pluralCategory('ar', 2)).toBe('other');
      expect(fresh.pluralChoiceIndex('ar', 2, 6)).toBe(5);
    } finally {
      (Intl as { PluralRules?: unknown }).PluralRules = real;
      vi.resetModules();
    }
  });

  it('maps a vue-i18n choice to a form: CLDR order when the counts match, the old shapes otherwise', () => {
    // six forms in Arabic = its six categories
    expect(AR_CASES.map(([n]) => pluralChoiceIndex('ar', n, 6))).toEqual([0, 1, 2, 3, 4, 5]);
    // four forms in Russian = one | few | many | other
    expect([1, 3, 5, 1.5].map((n) => pluralChoiceIndex('ru', n, 4))).toEqual([0, 1, 2, 3]);
    // the old shapes, in a language whose category count differs
    expect(pluralChoiceIndex('ar', 7, 1)).toBe(0);
    expect([1, 2, 0].map((n) => pluralChoiceIndex('ar', n, 2))).toEqual([0, 1, 1]);
    expect([0, 1, 2, 11].map((n) => pluralChoiceIndex('ar', n, 3))).toEqual([0, 1, 2, 2]);
    expect(pluralChoiceIndex('ar', 3, 5)).toBe(4);
    // A language with exactly three categories writes them in CLDR order…
    expect([1, 2, 20].map((n) => pluralChoiceIndex('ro', n, 3))).toEqual([0, 1, 2]);
    expect([0, 1, 2].map((n) => pluralChoiceIndex('lv', n, 3))).toEqual([0, 1, 2]);
    // …Spanish has two, so its three forms stay the classic zero | one | other,
    // and a round million (ICU's `many`) reads the `other` form.
    expect([0, 1, 5, 1_000_000].map((n) => pluralChoiceIndex('es', n, 3))).toEqual([0, 1, 2, 2]);
    expect([1, 5, 1_000_000].map((n) => pluralChoiceIndex('es', n, 2))).toEqual([0, 1, 1]);
  });
});

/* ── explorer (packages/core t()) ─────────────────────────────────────── */

describe('explorer t(): the pack language’s own plural category', () => {
  afterEach(() => resetLocales());

  it('Arabic: six forms of toast.restored, each for its numbers', () => {
    registerLocale({ code: 'ar', source: 'plugin', plugin: 'ar-pack', strings: explorerForms('toast.restored', AR_RESTORED) });
    const { t } = useLocale(() => 'ar');
    for (const [n, cat] of AR_CASES) {
      expect(t('toast.restored', { n }), `n=${n} → ${cat}`).toBe(fill(AR_RESTORED[cat], n));
    }
  });

  it('Russian: one / few / many, and the plain key for other', () => {
    registerLocale({ code: 'ru', source: 'plugin', plugin: 'ru-pack', strings: explorerForms('toast.restored', RU_RESTORED) });
    const { t } = useLocale(() => 'ru');
    expect(t('toast.restored', { n: 1 })).toBe('Восстановлен 1 элемент');
    expect(t('toast.restored', { n: 21 })).toBe('Восстановлен 21 элемент');
    expect(t('toast.restored', { n: 3 })).toBe('Восстановлено 3 элемента');
    expect(t('toast.restored', { n: 5 })).toBe('Восстановлено 5 элементов');
    expect(t('toast.restored', { n: 11 })).toBe('Восстановлено 11 элементов');
    expect(t('toast.restored', { n: 1.5 })).toBe('Восстановлено 1.5 элемента (дробь)');
  });

  it('Spanish with only the plain form: 1 reads the Spanish plain form, never the English singular', () => {
    registerLocale({
      code: 'es',
      source: 'plugin',
      plugin: 'es-pack',
      strings: {
        'toast.restored': '{n} elementos restaurados',
        'share.ttl.ceiling': 'Los enlaces valen como máximo {days} días (ajuste del servidor).',
      },
    });
    const { t } = useLocale(() => 'es');
    expect(t('toast.restored', { n: 1 })).toBe('1 elementos restaurados');
    expect(t('toast.restored', { n: 4 })).toBe('4 elementos restaurados');
    // A key the pack did not write at all is English as a WHOLE — English's
    // singular by English's rule — not a raw key.
    expect(t('inspector.folder_items', { n: 1 })).toBe('1 item');
    expect(t('inspector.folder_items', { n: 2 })).toBe('2 items');
    // …and the non-component path (lib/shareTtl) reads the same way.
    expect(ttlCeilingHint(1, 'es')).toBe('Los enlaces valen como máximo 1 días (ajuste del servidor).');
  });

  it('a listed language whose strings are not in hand is English by English’s rule', () => {
    // French puts 0 in `one`; reading English words by French rules would
    // print "0 item".
    registerLocale({ code: 'fr', source: 'plugin', plugin: 'fr-pack', strings: {} });
    const { t } = useLocale(() => 'fr');
    expect(t('inspector.folder_items', { n: 0 })).toBe('0 items');
    expect(t('inspector.folder_items', { n: 1 })).toBe('1 item');
  });

  it('English and Turkish read exactly as before', () => {
    const en = useLocale(() => 'en').t;
    const tr = useLocale(() => 'tr').t;
    expect(en('toast.restored', { n: 1 })).toBe('1 item restored');
    expect(en('toast.restored', { n: 0 })).toBe('0 items restored');
    expect(en('toast.restored', { n: 2 })).toBe('2 items restored');
    expect(en('toast.restored', { n: '1' })).toBe('1 item restored');
    expect(tr('toast.restored', { n: 1 })).toBe('1 öğe geri getirildi');
    expect(tr('toast.restored', { n: 5 })).toBe('5 öğe geri getirildi');
    expect(ttlCeilingHint(1, 'en')).toBe('Links can be valid for at most 1 day (server setting).');
    expect(ttlCeilingHint(7, 'en')).toBe('Links can be valid for at most 7 days (server setting).');
    expect(ttlCeilingHint(1, 'tr')).toBe('Bağlantılar en fazla 1 gün geçerli olabilir (sunucu ayarı).');
  });
});

/* ── admin (vue-i18n) ─────────────────────────────────────────────────── */

function serve(answers: Record<string, unknown>) {
  const f = vi.fn(async (url: string) => {
    const hit = Object.entries(answers).find(([suffix]) => url.endsWith(suffix));
    if (!hit) return { ok: false, status: 404, json: async () => ({}) };
    return { ok: true, status: 200, json: async () => hit[1] };
  });
  vi.stubGlobal('fetch', f);
}

/** The admin panel, cold, with one pack language chosen and served. */
async function adminIn(code: string, strings: Record<string, string>) {
  localStorage.setItem('filex.locale', code);
  serve({
    '/api/public/branding': {
      locales: ['en', 'tr', code],
      ui_locales: [{ code, source: 'plugin', plugin: `${code}-pack` }],
    },
    [`/api/public/ui-locales/${code}`]: { code, strings },
  });
  vi.resetModules();
  const mod = await import('@/i18n');
  await mod.loadOfferedLocales();
  // The pack's words have been folded into vue-i18n (the fold is async).
  const first = Object.values(strings)[0];
  await vi.waitFor(() =>
    expect(JSON.stringify(mod.i18n.global.getLocaleMessage(code))).toContain(JSON.stringify(first).slice(1, -1)),
  );
  return mod;
}

// ⚠ A generous timeout: each case imports `@/i18n` cold (vi.resetModules), and
// that pulls the built @brftech/filex-core bundle and vue-i18n — measured at
// ~4 s on the dev machine, most of the 5 s default before a single assertion.
const COLD = { timeout: 30_000 };

describe('admin (vue-i18n): a pack language’s plural rule', COLD, () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => vi.unstubAllGlobals());

  it('Arabic: six forms of dashboard.fileCount, each for its numbers', async () => {
    const { i18n } = await adminIn('ar', { 'dashboard.fileCount': adminForms(AR_FILES) });
    const t = i18n.global.t;
    for (const [n, cat] of AR_CASES) {
      expect(t('dashboard.fileCount', { n }, n), `n=${n} → ${cat}`).toBe(fill(AR_FILES[cat], n));
    }
    // An English form the pack left untranslated is read by the two-form
    // shape: `one` → first, every other category → second.
    expect(t('duplicates.copies', { n: 1 }, 1)).toBe('1 copy');
    expect(t('duplicates.copies', { n: 2 }, 2)).toBe('2 copies');
    expect(t('duplicates.copies', { n: 0 }, 0)).toBe('0 copies');
    // Registered for the pack language only.
    expect(Object.keys(i18n.global.pluralRules)).toEqual(['ar']);
  });

  it('Russian: one | few | many | other', async () => {
    const { i18n } = await adminIn('ru', { 'dashboard.fileCount': adminForms(RU_FILES) });
    const t = i18n.global.t;
    expect(t('dashboard.fileCount', { n: 1 }, 1)).toBe('1 файл');
    expect(t('dashboard.fileCount', { n: 21 }, 21)).toBe('21 файл');
    expect(t('dashboard.fileCount', { n: 3 }, 3)).toBe('3 файла');
    expect(t('dashboard.fileCount', { n: 5 }, 5)).toBe('5 файлов');
    expect(t('dashboard.fileCount', { n: 11 }, 11)).toBe('11 файлов');
    expect(t('dashboard.fileCount', { n: 1.5 }, 1.5)).toBe('1.5 файла (дробь)');
  });

  it('Spanish: one form is that form for every number; two are one | other; three are the classic zero | one | other', async () => {
    const { i18n } = await adminIn('es', {
      'dashboard.fileCount': '{n} archivos',
      'duplicates.copies': '{n} copia | {n} copias',
      'trash.empty_done': 'ninguna | una | {count} cosas',
    });
    const t = i18n.global.t;
    expect(t('dashboard.fileCount', { n: 1 }, 1)).toBe('1 archivos');
    expect(t('dashboard.fileCount', { n: 7 }, 7)).toBe('7 archivos');
    expect(t('duplicates.copies', { n: 1 }, 1)).toBe('1 copia');
    expect(t('duplicates.copies', { n: 0 }, 0)).toBe('0 copias');
    expect(t('duplicates.copies', { n: 1_000_000 }, 1_000_000)).toBe('1000000 copias');
    // ⚠ Read by ICU's own category list (`one | many | other`) this would be
    // CLDR order, and 1 would pick "ninguna".
    expect(t('trash.empty_done', { count: 0 }, 0)).toBe('ninguna');
    expect(t('trash.empty_done', { count: 1 }, 1)).toBe('una');
    expect(t('trash.empty_done', { count: 5 }, 5)).toBe('5 cosas');
  });

  it('English and Turkish keep vue-i18n’s own rule, and read exactly as before', async () => {
    serve({ '/api/public/branding': { locales: ['en', 'tr'] } });
    vi.resetModules();
    const { i18n, loadOfferedLocales } = await import('@/i18n');
    await loadOfferedLocales();
    const t = i18n.global.t;
    i18n.global.locale.value = 'en';
    expect(t('dashboard.fileCount', { n: 1 }, 1)).toBe('1 file');
    expect(t('dashboard.fileCount', { n: 0 }, 0)).toBe('0 files');
    expect(t('dashboard.fileCount', { n: 3 }, 3)).toBe('3 files');
    i18n.global.locale.value = 'tr';
    expect(t('dashboard.fileCount', { n: 1 }, 1)).toBe('1 dosya');
    expect(t('dashboard.fileCount', { n: 3 }, 3)).toBe('3 dosya');
    expect(Object.keys(i18n.global.pluralRules)).toEqual([]);
  });
});

/* ── the unit words that used to be glued after a number ──────────────── */

describe('a count and its noun are ONE message', COLD, () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => vi.unstubAllGlobals());

  it('storages.fileCount, usage.cost.billableAndFree — English and Turkish', async () => {
    serve({ '/api/public/branding': { locales: ['en', 'tr'] } });
    vi.resetModules();
    const { i18n } = await import('@/i18n');
    const t = i18n.global.t;
    i18n.global.locale.value = 'en';
    expect(t('storages.fileCount', { n: '1' }, 1)).toBe('1 file');
    expect(t('storages.fileCount', { n: '1,204' }, 1204)).toBe('1,204 files');
    expect(t('usage.cost.billableAndFree', { billable: '12', free: '10,000' }, 10000)).toBe('12 / 10,000 free');
    i18n.global.locale.value = 'tr';
    expect(t('storages.fileCount', { n: '1' }, 1)).toBe('1 dosya');
    expect(t('usage.cost.billableAndFree', { billable: '12', free: '10.000' }, 10000)).toBe('12 / 10.000 ücretsiz');
    // The words that stood alone are gone from the catalogue.
    expect(TR_JSON.storages.filesUnit).toBeUndefined();
    expect(TR_JSON.trash.days_left).toBeUndefined();
    // The Trash's time left is ONE key family, in the explorer's table, for
    // both Trash screens (core trashTimeLeft) — not a second copy here.
    expect(TR_JSON.trash.days_remaining).toBeUndefined();
    expect(TR_JSON.usage.cost.billableFree).toBeUndefined();
  });
});
