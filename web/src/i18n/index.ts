import { watch } from 'vue';
import { createI18n } from 'vue-i18n';
import {
  acceptableLocale,
  detectLocale,
  ensureLocaleStrings,
  hasLocale,
  isBuiltinLocale,
  loadLocales,
  localeStrings,
  localesVersion,
  normalizeLocaleCode,
  flushPrefs,
  pluralChoiceIndex,
  savePref,
  setLocalPref,
} from '@brftech/filex-core';
import en from '../locales/en.json';
import tr from '../locales/tr.json';

/**
 * A language code. ⚠ Not `'en' | 'tr'` any more: a language pack (an app
 * whose manifest carries `ui_locales`) adds languages at run time, and every
 * place that narrowed the active language to the shipped pair turned a
 * pack's `es` back into English (or, worse, into Turkish).
 */
export type Locale = string;
/** The two the product SHIPS. ⚠ Not "the two it offers" — see `availableLocales`. */
export const SUPPORTED_LOCALES: readonly Locale[] = ['en', 'tr'];

/**
 * Is this a language the interface can be set to RIGHT NOW?
 *
 * ⚠⚠ The list is not a constant — a pack may add a language and it leaves
 * when the pack does. Every gate that used to read
 * `SUPPORTED_LOCALES.includes(x)` asks this instead.
 */
export function isOfferedLocale(v: string | null | undefined): boolean {
  const code = normalizeLocaleCode(v ?? '');
  return !!code && hasLocale(code);
}

const STORAGE_KEY = 'filex.locale';

/* ── which language, decided in ONE place ─────────────────────────────── */

/**
 * Every place a language choice comes from, strongest first:
 *
 *   pref    — the account's preference document (`/api/me/prefs`), the
 *             single source of truth for a signed-in person;
 *   device  — this browser's `filex.locale` (the first-paint mirror, and the
 *             only memory a signed-out window has);
 *   account — `users.locale` from `/api/auth/me` (older accounts);
 *   server  — the instance default (`FILEX_DEFAULT_LOCALE`, capabilities).
 *
 * Then the browser's own language, then English.
 *
 * ⚠⚠ HELD, not judged on arrival. Each of these used to be checked against
 * the offered list the moment it arrived and silently DROPPED when it failed —
 * and all four arrive before the list does (it is a network fetch, and the
 * pack's language is on it only once it lands). So `filex.locale=es` with the
 * account set to `es` opened the panel in English, every time (measured
 * 2026-09-21 with the Spanish pack). Now every source is recorded, and
 * `chosenLocale` picks the strongest one that is `acceptableLocale`: offered,
 * or — until the list has arrived — merely a plausible language tag, the way
 * the palette holds a custom theme whose list is still in flight
 * (packages/core lib/themes plausibleThemeId). When the list lands, `settle`
 * decides again; a choice nothing offers any more falls back, and stays
 * stored, so reinstalling the pack brings it back.
 */
const held = { pref: '', device: '', account: '', server: '' };

try {
  held.device = localStorage.getItem(STORAGE_KEY) ?? '';
} catch {
  /* a private window has no memory; the browser's language is the answer */
}

/** The language the interface should be in, from everything known so far. */
export function chosenLocale(): Locale {
  for (const c of [held.pref, held.device, held.account, held.server]) {
    if (c && acceptableLocale(c)) return normalizeLocaleCode(c);
  }
  // `detectLocale` walks the browser's list against what is OFFERED — the
  // built-ins, and a pack's language once the list has arrived.
  return detectLocale();
}

/** @deprecated read `chosenLocale` — kept for callers that ask by this name. */
export function getStoredLocale(): Locale {
  return chosenLocale();
}

/* ── a pack's strings, folded into THIS catalogue ─────────────────────── */

function flatten(o: Record<string, unknown>, pre = '', out: Record<string, true> = {}): Record<string, true> {
  for (const [k, v] of Object.entries(o)) {
    const key = pre ? `${pre}.${k}` : k;
    if (v && typeof v === 'object') flatten(v as Record<string, unknown>, key, out);
    else out[key] = true;
  }
  return out;
}

/**
 * Every key the admin panel's own catalogue has. ⚠⚠ A pack is ONE flat
 * namespace covering BOTH catalogues — this one (nested JSON, addressed by
 * dotted path) and the explorer's (`packages/core/src/locales`, flat) — so
 * only the keys that are THIS catalogue's are folded into vue-i18n. The rest
 * are the explorer's business (`localeTable`). Folding them all in, as this
 * file used to, pushed ~1 400 keys vue-i18n never reads through the
 * unflattening below, and the explorer's own prefix pairs
 * (`toolbar.search` beside `toolbar.search.placeholder`, 54 of them) cannot
 * both survive being turned into nested objects — one of each pair was
 * dropped, depending on key order. Filtered, no key here is both a string and
 * a branch, because this catalogue is itself a tree.
 */
const WEB_KEYS = flatten(en as Record<string, unknown>);

/** `a.b.c` = v, into a nested clone. Only ever called with WEB_KEYS. */
function setPath(root: Record<string, unknown>, key: string, value: string): void {
  const parts = key.split('.');
  let node = root;
  for (let i = 0; i < parts.length - 1; i++) {
    const next = node[parts[i]];
    if (!next || typeof next !== 'object') return;
    node = next as Record<string, unknown>;
  }
  node[parts[parts.length - 1]] = value;
}

/* ── plural forms, per language ───────────────────────────────────────── */

type PluralRule = (choice: number, choicesLength: number) => number;

/**
 * vue-i18n's `pluralRules` option — handed to `createI18n` below and filled
 * in as pack languages appear.
 *
 * ⚠⚠ vue-i18n's DEFAULT rule is English's (two forms: 1 → first, else second;
 * three: 0 / 1 / else), and it applies to every locale that has no entry
 * here. So until this existed an Arabic pack could not say "2 files" and a
 * Russian one could not say "5 files": the grammar had no place for the
 * forms those languages need. A pack language now reads its forms by its own
 * CLDR categories (`pluralChoiceIndex` in @brftech/filex-core lib/plural —
 * Arabic `zero | one | two | few | many | other`, Russian `one | few | many |
 * other`, and the old 1 / 2 / 3-form shapes still work).
 *
 * ⚠ The SAME object, mutated — not replaced. vue-i18n keeps the reference
 * (`_pluralRules` in the composer, `context.pluralRules` in core-base) and
 * looks `pluralRules[locale]` up again every time it builds a message
 * context, so an entry added after `createI18n` is consulted on the next
 * `t()` (verified against vue-i18n 9.14.5 / @intlify/core-base 9.14.5). A
 * pack's language is only known at run time; there is nothing to list up
 * front.
 *
 * ⚠ `en` and `tr` never get an entry: they keep vue-i18n's rule, which the
 * shipped catalogues were written and tested against.
 */
const pluralRules: Record<string, PluralRule> = {};

function registerPluralRule(code: Locale): void {
  if (!code || isBuiltinLocale(code) || pluralRules[code]) return;
  pluralRules[code] = (choice, choicesLength) => pluralChoiceIndex(code, choice, choicesLength);
}

/** localesVersion at the last fold, per language — a fold is not free. */
const folded = new Map<string, number>();
/** Languages whose vue-i18n table currently carries a pack's strings. */
const overlaid = new Set<string>();

/**
 * Give vue-i18n the table for `code`: the English catalogue (or the Turkish
 * one, for a pack that overlays Turkish) with the pack's strings on top.
 *
 * ⚠⚠ A DEEP overlay. The old fold was `{...en, ...base, ...unflatten(pack)}`
 * — a SHALLOW spread, so a pack that translated one string under `drive`
 * replaced the whole `drive` subtree with that one string; for a Turkish
 * overlay the rest of `drive` fell back to English.
 */
async function foldPackStrings(code: Locale): Promise<void> {
  const builtin = isBuiltinLocale(code);
  // ⚠ The plural rule BEFORE any message exists under the code: the English
  // placeholder painted just below is already read with it, and so is every
  // English form a partial pack leaves untranslated.
  registerPluralRule(code);
  // Paint SOMETHING under a pack's code at once — English — so vue-i18n does
  // not fall back key by key (a console line per string) while the fetch runs.
  if (!builtin && !i18n.global.availableLocales.includes(code as 'en')) {
    i18n.global.setLocaleMessage(code as 'en', en);
  }
  await ensureLocaleStrings(code);
  const version = localesVersion.value;
  if (folded.get(code) === version) return;
  folded.set(code, version);
  const pack = localeStrings(code);
  const keys = Object.keys(pack).filter((k) => WEB_KEYS[k]);
  // Nothing to lay on, and nothing laid before: the table is already right.
  if (!keys.length && !overlaid.has(code)) return;
  const base = JSON.parse(JSON.stringify(code === 'tr' ? tr : en)) as Record<string, unknown>;
  for (const k of keys) setPath(base, k, pack[k]);
  i18n.global.setLocaleMessage(code as 'en', base as unknown as typeof en);
  if (keys.length) overlaid.add(code);
  else overlaid.delete(code);
}

/**
 * WHO OWNS `<html lang>` ON THIS PAGE.
 *
 * ⚠⚠ Normally this app does: its language is the page's language. A PUBLIC
 * route is the exception — a share link, a file request, an app's signing
 * page. There is no account behind one, the visitor's pick lives in their
 * browser and nowhere else, and the page keeps its own language
 * (`PublicLinkPage`). Both of us wrote `<html lang>`, so whoever wrote last
 * won: a stranger pressed العربية, the page turned Arabic, and then this
 * app's `settle()` — called again when the offered list or a pack's strings
 * arrive — stamped `en` back over it (v0.43.0). Nothing on screen said so;
 * `dir` follows `lang`, so the layout went with it.
 *
 * One owner at a time, and the page says when it is the owner.
 */
let pageOwnsLanguage = false;

/** The public route takes the document's language while it is mounted. */
export function setPageOwnsLanguage(on: boolean): void {
  pageOwnsLanguage = on === true;
}

/** `<html lang>`, unless a page of its own is holding it. */
function stampLanguage(code: string): void {
  if (pageOwnsLanguage || typeof document === 'undefined') return;
  // `lang` only: `dir` follows from it (syncDocumentDir, main.ts), so a
  // right-to-left pack's language lays the page out right to left.
  document.documentElement.lang = code;
}

/**
 * Decide again, and apply. Cheap and idempotent: every source calls it, and
 * so does the arrival of the offered list and of a pack's strings.
 */
function settle(): void {
  const next = chosenLocale();
  if (i18n.global.locale.value !== next) i18n.global.locale.value = next as 'en';
  stampLanguage(next);
  void foldPackStrings(next);
}

/* ── the sources ──────────────────────────────────────────────────────── */

/** The person chose, in a picker: this device AND their account. */
export function setStoredLocale(locale: Locale): void {
  const code = normalizeLocaleCode(locale) || 'en';
  try {
    localStorage.setItem(STORAGE_KEY, code);
  } catch {
    /* see above */
  }
  held.device = code;
  held.pref = code;
  // ⚠⚠ …and on the ACCOUNT. Until v3 the switcher wrote to this browser only,
  // so "I set Turkish" was true on one machine and false on the next.
  savePref('locale', code);
  // ⚠ At once, not after the debounce: a language is picked with one click,
  // not dragged like a slider, and the account's copy OUTRANKS this browser's
  // on the next load — a reload inside the 400 ms debounce would read the
  // old language back from the server and undo the pick.
  void flushPrefs();
  settle();
}

/**
 * The account's language has arrived from `/api/me/prefs`. Outranks every
 * other source, and does NOT write back — it came from the server. The mirror
 * is updated so the next cold load paints in this language before the fetch
 * returns.
 */
export function applyPrefLocale(loc?: string | null): void {
  if (!loc) return;
  held.pref = loc;
  setLocalPref('locale', loc);
  settle();
}

/**
 * `users.locale` from /api/auth/me. ⚠⚠ Until v0.34.1 this preference was
 * written and never read; sign in from a second browser and the account's
 * language was ignored. Ranked below this device's own choice and the prefs
 * document; never persists.
 */
export function applyAccountLocale(loc?: string | null): void {
  if (!loc) return;
  held.account = loc;
  settle();
}

/**
 * The operator's FILEX_DEFAULT_LOCALE (from /api/capabilities), for a person
 * who has chosen nothing. Ranked below every personal source; never persists.
 */
export function applyServerDefaultLocale(def?: string | null): void {
  if (!def) return;
  held.server = def;
  settle();
}

/**
 * Ask the server which languages this instance offers, then decide again —
 * a held choice either becomes real (its pack is on the list) or falls back.
 * Also the call to make after an app is installed or removed.
 *
 * Never awaited by a caller that has something to paint: the two built-in
 * languages are already on screen.
 */
export async function loadOfferedLocales(): Promise<void> {
  await loadLocales({});
  folded.clear();
  settle();
}

export function applyStoredLocale(): void {
  stampLanguage(chosenLocale());
}

export const i18n = createI18n({
  legacy: false,
  globalInjection: true,
  locale: chosenLocale(),
  fallbackLocale: 'en',
  messages: { en, tr },
  pluralRules,
});

// A pack's strings can land from anywhere — this file's own fold, or the
// explorer asking `localeTable` for the same language first — and the list
// can change under an open window. Either way: decide again.
watch(localesVersion, () => settle());
settle();

export function t(key: string, params?: Record<string, unknown>, plural?: number): string {
  // Tiny helper so non-component code can translate without injecting i18n.
  // `plural` picks a counted message's form ("1 character | {n} characters").
  return plural === undefined ? i18n.global.t(key, params ?? {}) : i18n.global.t(key, params ?? {}, plural);
}
