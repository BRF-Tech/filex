/**
 * plural — which form of a counted message a language reads for a number.
 *
 * ⚠⚠ Why this exists. Both catalogues used to have ONE plural rule, English's:
 * the explorer chose `<key>_one` when the count was exactly 1 and `<key>`
 * otherwise, and the admin panel left vue-i18n on its default (two forms:
 * 1 → first, else second; three forms: 0 / 1 / else). That is enough for
 * English and Turkish and for nothing much else. The Arabic pack's translator
 * could not write the language at all — Arabic has SIX cardinal categories
 * (zero, one, two, few, many, other) — and Russian, Polish, Czech and
 * Ukrainian need few/many beside one (found by the Arabic pack, v0.43.0).
 *
 * The categories are the Unicode CLDR's, and they come from the runtime's own
 * `Intl.PluralRules`: every browser and Node ship the CLDR tables, so the
 * product does not carry (and cannot get out of date with) a copy of them.
 *
 * The grammar a pack writes, which `docs/` and the pack validator describe:
 *
 *   explorer (packages/core/src/locales, flat keys)
 *     `<key>_zero` `<key>_one` `<key>_two` `<key>_few` `<key>_many`, and the
 *     plain `<key>` IS the `other` form (there is no `_other` key). A language
 *     writes only the categories it has; see `useLocale → countedKey`.
 *
 *   admin (web/src/locales, vue-i18n `a | b | …`)
 *     as many forms as the language has categories (those whole numbers
 *     0 … 999 reach, plus `other` — see `pluralCategories`), in CLDR order —
 *     Arabic `zero | one | two | few | many | other`, Russian `one | few |
 *     many | other` — or the old 1 / 2 / 3-form shapes; see
 *     `pluralChoiceIndex`.
 */

/** The CLDR cardinal categories in the order a message lists its forms. */
export const CLDR_ORDER = ['zero', 'one', 'two', 'few', 'many', 'other'] as const;
export type PluralCategory = (typeof CLDR_ORDER)[number];

/** What a runtime with no usable `Intl.PluralRules` is treated as: English. */
const ENGLISH_CATEGORIES: readonly string[] = ['one', 'other'];

/**
 * One `Intl.PluralRules` per language. `null` = the runtime has none at all.
 *
 * ⚠ Cached because `t()` asks once per counted string per repaint, and
 * constructing a `PluralRules` is the expensive part (`select` is cheap). A
 * session has two or three distinct tags.
 */
const rulesCache = new Map<string, Intl.PluralRules | null>();

function rulesFor(lang: string | undefined | null): Intl.PluralRules | null {
  const key = String(lang ?? '');
  const hit = rulesCache.get(key);
  if (hit !== undefined) return hit;
  let rules: Intl.PluralRules | null = null;
  if (typeof Intl !== 'undefined' && typeof Intl.PluralRules === 'function') {
    try {
      rules = new Intl.PluralRules(key || 'en');
    } catch {
      // ⚠ A tag `Intl` refuses (`RangeError: Incorrect locale information`)
      // must not take the page down with it — a pack names its language
      // itself, and the host only checks that it LOOKS like a tag. English
      // rules are what the rest of the interface falls back to as well.
      try {
        rules = new Intl.PluralRules('en');
      } catch {
        rules = null;
      }
    }
  }
  rulesCache.set(key, rules);
  return rules;
}

const categoriesCache = new Map<string, readonly string[]>();

/** The whole numbers sampled to find a language's categories: 0 … 999. */
const SAMPLE_MAX = 999;

/**
 * The categories `lang` HAS, in CLDR order: every category some whole number
 * from 0 to 999 falls in, plus `other` always.
 *
 * ⚠⚠ NOT `resolvedOptions().pluralCategories`. Current ICU lists `many` for
 * Spanish, French, Italian, Portuguese and Catalan — a category only exact
 * millions and compact numbers reach (`select(1000000)` is 'many') — so
 * Spanish would have three categories, and a Spanish admin string written in
 * the classic three forms `zero | one | other` would be read in CLDR order
 * (`one | many | other`): 1 would pick the "zero" form. Counting what whole
 * numbers people actually see select gives es/fr/it/pt `one | other`, Russian
 * `one | few | many | other`, Arabic all six, Japanese `other`, Latvian
 * `zero | one | other`, Romanian `one | few | other`.
 *
 * ⚠ The SAME definition as the pack validator (scripts/i18n-validate.mjs) and
 * the server (Go, golang.org/x/text CLDR tables, sampled 0 … 999 + other). A
 * third definition here would pass a pack there that renders wrong here.
 *
 * ⚠ In CLDR order, whatever order the runtime answers in: the admin panel
 * maps a message's forms to categories BY POSITION — an unsorted list would
 * hand Arabic's "few" form to the number 0.
 *
 * Cached: a thousand `select` calls, once per language per page.
 */
export function pluralCategories(lang: string | undefined | null): readonly string[] {
  const key = String(lang ?? '');
  const hit = categoriesCache.get(key);
  if (hit) return hit;
  let out: readonly string[] = ENGLISH_CATEGORIES;
  const rules = rulesFor(key);
  if (rules) {
    try {
      const seen = new Set<string>(['other']);
      for (let n = 0; n <= SAMPLE_MAX; n++) seen.add(rules.select(n));
      out = CLDR_ORDER.filter((c) => seen.has(c));
    } catch {
      out = ENGLISH_CATEGORIES;
    }
  }
  categoriesCache.set(key, out);
  return out;
}

/**
 * The category `lang` puts the number `n` in — `'one'`, `'few'`, `'other'`…
 *
 * ⚠ The absolute value: "−1 day" agrees like "1 day", and `select` has no
 * opinion about a sign. A count that is not a finite number (a formatted
 * "1,234" that `Number()` could not read) is `other`, the plain form, which
 * is what the single `_one` rule this replaced did with it too.
 */
export function pluralCategory(lang: string | undefined | null, n: number): string {
  if (!Number.isFinite(n)) return 'other';
  const abs = Math.abs(n);
  const rules = rulesFor(lang);
  if (!rules) return abs === 1 ? 'one' : 'other';
  try {
    return rules.select(abs);
  } catch {
    return abs === 1 ? 'one' : 'other';
  }
}

/**
 * Which of a vue-i18n message's `|`-separated forms `lang` reads for `choice`
 * — a vue-i18n `pluralRules` entry, `(choice, choicesLength) => index`.
 *
 *   - as many forms as the language has categories (more than one, and
 *     "has" as `pluralCategories` counts them): those categories, in CLDR
 *     order — Arabic `zero | one | two | few | many | other`, Russian
 *     `one | few | many | other`, Romanian `one | few | other`;
 *   - one form: that form (a language that does not inflect);
 *   - two: `one | other`;
 *   - three: the classic `zero | one | other`;
 *   - anything else: the last form, which is the `other` one.
 *
 * ⚠ The first rule outranks the next three, so a language with exactly three
 * categories (Latvian, Romanian) writes three forms in CLDR order, not as
 * `zero | one | other`. Spanish, French, Italian and Portuguese are NOT among
 * them — see `pluralCategories` on the `many` ICU lists for round millions —
 * so their three-form strings stay classic.
 *
 * ⚠ English and Turkish are not routed here — they keep vue-i18n's own rule,
 * which the shipped catalogues were written against (web/src/i18n).
 */
export function pluralChoiceIndex(
  lang: string | undefined | null,
  choice: number,
  choicesLength: number,
): number {
  const n = Math.abs(choice);
  const cats = pluralCategories(lang);
  const c = pluralCategory(lang, n);
  if (choicesLength === cats.length && choicesLength > 1) {
    // ⚠ A category outside the sampled list is real: Spanish `select(1000000)`
    // is 'many', and Spanish's list is `one | other`. It reads the last form
    // (`other`) — -1 would make vue-i18n print nothing at all.
    const i = cats.indexOf(c);
    return i >= 0 ? i : choicesLength - 1;
  }
  if (choicesLength === 1) return 0;
  if (choicesLength === 2) return c === 'one' ? 0 : 1;
  if (choicesLength === 3) return n === 0 ? 0 : c === 'one' ? 1 : 2;
  return choicesLength - 1;
}
