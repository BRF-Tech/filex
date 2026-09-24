/**
 * uiLocales — the language list is not a constant any more.
 *
 * filex ships English and Turkish. A third source is an app — a LANGUAGE
 * PACK — that carries a language for FILEX ITSELF (`manifest.ui_locales`):
 * the server lists it, the interface offers it (public shell included),
 * marks it with the app it came from, and drops it when the app is removed.
 *
 * ⚠⚠ TWO requests, not one. `GET /api/public/branding` LISTS the languages
 * apps add — code, app, right-to-left or not — and carries no strings;
 * `GET /api/public/ui-locales/{code}` is one language's strings, fetched the
 * first time something asks for that language's table (`localeTable`,
 * `ensureLocaleStrings`). A complete language is ~150 KB, and the branding
 * answer is read by every public page and every panel start.
 *
 * ⚠⚠ The branding field used to be a MAP (`{"es": {key: text}}`) while this
 * file read it as a LIST (`b.ui_locales?.length`): a map has no length, so an
 * installed language joined the picker and then spoke English, and every test
 * stayed green because its fixture was typed this file's way (measured
 * 2026-09-21 with the Spanish pack). The web tests now read the SERVER's own
 * bytes (backend/internal/api/handlers/testdata/wire/public-*.json).
 *
 * ⚠ A language that arrives this way may be a PARTIAL table. Every lookup
 * falls back to English rather than printing a raw key — the rule the
 * built-in catalogues already follow, applied to a table we did not write.
 *
 * ⚠ The strings come from outside. They are rendered as TEXT, never markup,
 * and a key that could walk into an object's machinery (`__proto__`,
 * `constructor`, `prototype`) is dropped here as well as refused by the host.
 *
 * ⚠⚠ Reactive on purpose. A pack can be installed or removed while the window
 * is open, and its strings arrive after the page has painted; `localesVersion`
 * is what every consumer watches, and `hasLocale` reads it so a computed that
 * asked "is `es` offered?" asks again when the answer changes.
 */
import { ref } from 'vue';
import type { PublicBranding, PublicLocaleOption } from '../types/Public';
// ⚠ The two tables directly, not `../locales` (its index re-exports
// `locales/resolve`, which reads THIS module — an import cycle).
import { en } from '../locales/en';
import { tr } from '../locales/tr';
import type { LocaleCode } from '../types/ExplorerConfig';

/** Bumped whenever the list or a table changes; `useLocale` and the pickers watch it. */
export const localesVersion = ref(0);

/**
 * Has the server's list arrived? Until it has, a stored choice that LOOKS like
 * a language (`plausibleLocale`) is held rather than thrown away — the same
 * rule the palette follows for a custom theme whose list is still in flight
 * (`lib/themes` plausibleThemeId). Without it a person who picked a pack's
 * language opened every page in English: the stored `es` was judged against
 * a list that had not been fetched yet, refused, and never looked at again.
 */
export const localesKnown = ref(false);

/** The languages this package ships a full catalogue for. */
export const BUILTIN_LOCALES: readonly LocaleCode[] = ['en', 'tr'];

/**
 * A language's name IN that language — never translated into the viewer's,
 * because somebody looking for their own language is looking for their own
 * word for it. The table covers the common cases without asking `Intl`;
 * anything else asks `Intl.DisplayNames` in its own language, and only a code
 * even that cannot name is shown upper-cased.
 */
const BUILTIN_LABELS: Record<string, string> = {
  en: 'English',
  tr: 'Türkçe',
  de: 'Deutsch',
  fr: 'Français',
  es: 'Español',
  it: 'Italiano',
  nl: 'Nederlands',
  pt: 'Português',
  pl: 'Polski',
  ru: 'Русский',
  uk: 'Українська',
  ar: 'العربية',
  fa: 'فارسی',
  az: 'Azərbaycanca',
  ja: '日本語',
  ko: '한국어',
  zh: '中文',
};

/** A language's own name for itself. */
export function localeLabel(code: string): string {
  if (BUILTIN_LABELS[code]) return BUILTIN_LABELS[code];
  try {
    const name = new Intl.DisplayNames([code], { type: 'language' }).of(code);
    if (name && name.toLowerCase() !== code) {
      // Upper-cased in the language's own rules ("español" → "Español").
      return name.charAt(0).toLocaleUpperCase(code) + name.slice(1);
    }
  } catch {
    /* an old runtime or a tag Intl does not know */
  }
  return code.toUpperCase();
}

/** Everything a language added from outside carries. */
interface ExtraLocale extends Omit<PublicLocaleOption, 'strings'> {
  /** `null` = listed, strings not fetched yet. */
  strings: Record<string, string> | null;
}

const messages: Record<string, Record<string, string>> = { en, tr };

const extras = new Map<string, ExtraLocale>();
const inflight = new Map<string, Promise<boolean>>();

/* ── codes ────────────────────────────────────────────────────────────── */

const TAG = /^[a-z]{2,3}(-[a-z0-9]{2,8})*$/;

/**
 * Could this be a language tag at all? `es`, `pt-br`, `zh-hant` — yes;
 * `../etc`, `` — no. What a HELD choice has to be.
 */
export function plausibleLocale(raw: string | undefined | null): boolean {
  return TAG.test(String(raw ?? '').trim().toLowerCase().replace(/_/g, '-'));
}

/**
 * The code the interface uses for a tag.
 *
 * `tr-TR` and `TR` mean `tr`; `es-MX` means `es` when a pack offers `es`;
 * `pt` means `pt-br` when that is the only Portuguese on offer; a regional
 * pack (`pt-br`, `zh-hant`) keeps its region, because Brazilian and European
 * Portuguese — or the two Chinese scripts — are different tables. A tag
 * nothing offers yet is KEPT whole, so a choice held before the list arrives
 * still means what the person picked.
 */
export function normalizeLocaleCode(raw: string | undefined | null): string {
  const v = String(raw ?? '').trim().toLowerCase().replace(/_/g, '-');
  if (!TAG.test(v)) return '';
  if (extras.has(v)) return v;
  const primary = v.split('-')[0];
  if (isBuiltinLocale(primary)) return primary;
  if (extras.has(primary)) return primary;
  for (const k of extras.keys()) if (k.startsWith(`${primary}-`)) return k;
  return v;
}

export function isBuiltinLocale(code: string): code is LocaleCode {
  return (BUILTIN_LOCALES as readonly string[]).includes(code);
}

/* ── the registry ─────────────────────────────────────────────────────── */

/**
 * The strings a pack sent, minus anything that must not reach an object:
 * non-strings, empty values (untranslated — English shows instead) and keys
 * whose segments name JavaScript's machinery. The host refuses those keys at
 * install already (wire.UILocaleKeyOK); this is the second lock, because the
 * web SPA turns dotted keys into nested objects and `__proto__` would walk
 * that loop into Object.prototype.
 */
function cleanStrings(raw: Record<string, unknown> | null | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(raw ?? {})) {
    if (typeof v !== 'string' || !v.trim() || !k) continue;
    if (k.split('.').some((s) => s === '__proto__' || s === 'constructor' || s === 'prototype')) continue;
    out[k] = v;
  }
  return out;
}

/**
 * Add (or replace) a language the server offers. `strings` absent = listed
 * only; the table is fetched when first asked for.
 *
 * ⚠ A built-in language is never replaced wholesale — a pack that ships
 * `tr` is OVERLAYING keys onto the catalogue, not taking it over, or one
 * badly-packed app could blank the interface for everybody who reads Turkish.
 */
export function registerLocale(opt: PublicLocaleOption): void {
  const raw = String(opt?.code ?? '').trim().toLowerCase().replace(/_/g, '-');
  const code = TAG.test(raw) ? raw : '';
  if (!code) return;
  extras.set(code, {
    code,
    label: String(opt.label ?? '').trim() || localeLabel(code),
    source: opt.source === 'plugin' ? 'plugin' : opt.source || 'builtin',
    plugin: opt.plugin,
    rtl: opt.rtl === true,
    strings: opt.strings ? cleanStrings(opt.strings) : null,
  });
  localesVersion.value += 1;
}

/** Replace the whole extra set (what one branding answer says). */
export function setLocales(list: PublicLocaleOption[] | undefined): void {
  extras.clear();
  inflight.clear();
  for (const o of list ?? []) registerLocale(o);
  localesVersion.value += 1;
}

/** An app was removed: its languages go with it. */
export function unregisterPluginLocales(plugin: string): void {
  let changed = false;
  for (const [code, v] of extras) {
    if (v.source === 'plugin' && v.plugin === plugin) {
      extras.delete(code);
      changed = true;
    }
  }
  if (changed) localesVersion.value += 1;
}

/** Testing seam / sign-out: back to the two we ship, list not yet known. */
export function resetLocales(): void {
  extras.clear();
  inflight.clear();
  source = {};
  localesKnown.value = false;
  localesVersion.value += 1;
}

/**
 * Every language on offer, built-ins first and the rest in the order the
 * server listed them — so the two the product ships do not move around when
 * an app is installed. ⚠ THE one list every picker reads (the admin
 * header's switcher, the settings dialog, the public shell).
 */
export function availableLocales(): PublicLocaleOption[] {
  void localesVersion.value;
  const out: PublicLocaleOption[] = BUILTIN_LOCALES.map((code) => ({
    code,
    label: localeLabel(code),
    source: 'builtin',
  }));
  for (const [code, v] of extras) {
    if (isBuiltinLocale(code)) continue;
    out.push({ code, label: v.label, source: v.source, plugin: v.plugin, rtl: v.rtl });
  }
  return out;
}

/** Is this a language the interface can actually be set to? (Reactive.) */
export function hasLocale(code: string | undefined | null): boolean {
  void localesVersion.value;
  const c = normalizeLocaleCode(code);
  return !!c && (isBuiltinLocale(c) || extras.has(c));
}

/**
 * May a STORED choice stand? Offered, or — while the list has not arrived —
 * merely plausible. After the list arrives only an offered language stands;
 * the stored value itself is kept, so reinstalling the pack brings it back.
 */
export function acceptableLocale(code: string | undefined | null): boolean {
  if (hasLocale(code)) return true;
  return !localesKnown.value && plausibleLocale(code);
}

/* ── tables ───────────────────────────────────────────────────────────── */

/**
 * One language's table with English merged underneath.
 *
 * Order, and why: English underneath as the last resort, then the built-in
 * catalogue when the language has one, then the pack's table on top. So a
 * pack that translates three strings gets three strings and an otherwise
 * English screen — never a page of raw keys — and a pack that overlays two
 * Turkish strings changes exactly two.
 *
 * ⚠ Not what `t()` reads any more — see `localeOwnTable`. In this merge a
 * key the pack never wrote and a key it did are indistinguishable, and the
 * plural rule needs to tell them apart.
 *
 * ⚠ Asking for a language whose strings have not been fetched STARTS the
 * fetch and answers English meanwhile; `localesVersion` moves when they land,
 * and every computed that read this re-reads it.
 */
export function localeTable(code: string | undefined): Record<string, string> {
  const c = normalizeLocaleCode(code);
  const builtin = isBuiltinLocale(c) ? messages[c] : undefined;
  const extra = extras.get(c);
  if (extra && extra.strings === null) void ensureLocaleStrings(c);
  if (!extra?.strings || !Object.keys(extra.strings).length) return builtin ?? messages.en;
  return { ...messages.en, ...(builtin ?? {}), ...extra.strings };
}

/** What a language wrote ITSELF, and whose plural rules read it. */
export interface OwnLocaleTable {
  /** The language whose CLDR plural rules pick a form out of `strings`. */
  lang: string;
  /** Its own words only — English is NOT merged underneath. */
  strings: Readonly<Record<string, string>>;
}

/**
 * The words a language has of its OWN — what `t()` reads first.
 *
 *   - a built-in (`en`, `tr`): its catalogue, with a pack that overlays it on
 *     top (the overlay IS that language's words);
 *   - a pack's language: the pack's strings and nothing else;
 *   - a language with no strings yet (not fetched, fetch failed, empty pack,
 *     or a code nothing offers): English, AS English — `lang` is `en`.
 *
 * ⚠⚠ Why not `localeTable`. That one merges English under the pack, so "the
 * table has `toast.restored_one`" is true for a Spanish pack that wrote only
 * `toast.restored` — the English singular was sitting underneath — and the
 * count of one printed "1 item restored" in the middle of a Spanish screen.
 * The plural rule has to know which forms the language itself wrote, and
 * fall back to English as a WHOLE (English's form, by English's rule) only
 * when the language wrote none.
 *
 * ⚠ The last case answers `lang: 'en'` on purpose: French puts 0 in `one`,
 * so reading English strings by French rules would print "0 item".
 *
 * Starts the fetch for a listed language whose strings are not in hand, the
 * same as `localeTable`.
 */
export function localeOwnTable(code: string | undefined): OwnLocaleTable {
  const c = normalizeLocaleCode(code);
  const builtin = isBuiltinLocale(c) ? messages[c] : undefined;
  const extra = extras.get(c);
  if (extra && extra.strings === null) void ensureLocaleStrings(c);
  const pack = extra?.strings && Object.keys(extra.strings).length ? extra.strings : null;
  if (builtin) return { lang: c, strings: pack ? { ...builtin, ...pack } : builtin };
  if (pack) return { lang: c, strings: pack };
  return { lang: 'en', strings: messages.en };
}

/**
 * Just the strings a pack added for this language — no catalogue under them.
 *
 * ⚠ For a host with a catalogue of its own (the admin SPA's `web/src/locales`)
 * that merges the same table into ITS `t()`. Handing it `localeTable` would
 * fold this package's keys into the host's, which is two catalogues becoming
 * one by accident.
 */
export function localeStrings(code: string | undefined): Record<string, string> {
  return { ...(extras.get(normalizeLocaleCode(code))?.strings ?? {}) };
}

/* ── fetching ─────────────────────────────────────────────────────────── */

export interface LoadLocalesOptions {
  /** API origin; empty = same origin. */
  base?: string;
  fetchImpl?: typeof fetch;
  /** Per-call auth for a host that does not ride on cookies. */
  headers?: () => Promise<Record<string, string>> | Record<string, string>;
}

/** Where the list came from — the strings are fetched from the same place. */
let source: LoadLocalesOptions = {};

/**
 * Fetch one added language's strings (`GET /api/public/ui-locales/{code}`).
 * Resolves true when a table is in hand. Idempotent: concurrent askers share
 * one request, and a language already loaded costs nothing.
 *
 * ⚠ A failure leaves the language listed with an EMPTY table (English shows)
 * rather than retrying on every repaint — a pack removed between the list and
 * this request answers 404, and hammering the server over it helps nobody.
 */
export function ensureLocaleStrings(code: string | undefined): Promise<boolean> {
  const c = normalizeLocaleCode(code);
  const e = extras.get(c);
  if (!e) return Promise.resolve(false);
  if (e.strings !== null) return Promise.resolve(Object.keys(e.strings).length > 0);
  const pending = inflight.get(c);
  if (pending) return pending;
  const doFetch = source.fetchImpl ?? (typeof fetch === 'function' ? fetch : null);
  if (!doFetch) return Promise.resolve(false);
  const base = (source.base ?? '').replace(/\/+$/, '');
  const p = (async () => {
    let strings: Record<string, string> = {};
    try {
      const extra = source.headers ? await source.headers() : {};
      const res = await doFetch(`${base}/api/public/ui-locales/${encodeURIComponent(c)}`, {
        method: 'GET',
        headers: { Accept: 'application/json', ...extra },
        credentials: 'same-origin',
      });
      if (res.ok) {
        const body = (await res.json()) as { strings?: Record<string, unknown> };
        strings = cleanStrings(body?.strings);
      }
    } catch {
      /* English is a perfectly good answer */
    }
    const still = extras.get(c);
    if (still === e) {
      still.strings = strings;
      localesVersion.value += 1;
    }
    inflight.delete(c);
    return Object.keys(strings).length > 0;
  })();
  inflight.set(c, p);
  return p;
}

/**
 * Fold a branding answer's language list into the registry.
 *
 * `locales` is a list of CODES — the built-ins and whatever apps add — and
 * `ui_locales` the added ones as rows (code, app, rtl) WITHOUT strings.
 * ⚠ Registering again drops tables already fetched, so a pack that was
 * upgraded while the page was open is re-read the next time it is used
 * instead of serving yesterday's words until a reload.
 */
export function setLocalesFromBranding(
  b: PublicBranding | null | undefined,
  opts?: LoadLocalesOptions,
): PublicLocaleOption[] {
  if (opts) source = opts;
  if (!b) return availableLocales();
  const rows = Array.isArray(b.ui_locales) ? b.ui_locales : [];
  const listed = new Set(rows.map((r) => String(r?.code ?? '').toLowerCase()));
  // A code on `locales` with no row (an older server) still gets a name.
  const bare = (b.locales ?? [])
    .map((c) => String(c ?? '').trim().toLowerCase())
    .filter((c) => TAG.test(c) && !isBuiltinLocale(c) && !listed.has(c))
    .map((code) => ({ code, source: 'plugin' as const }));
  setLocales([...rows, ...bare]);
  localesKnown.value = true;
  return availableLocales();
}

/**
 * Ask the server which languages this instance offers.
 *
 * ⚠ It asks the BRANDING route, which is the one public answer about how
 * this instance presents itself — and the one the public shell already
 * fetches. ⚠ A failure is not an error: the two built-in languages are
 * already on the picker, and the list is then treated as KNOWN (a held
 * choice falls back) — waiting forever for an answer that will not come
 * would keep somebody's interface in a language nothing can serve.
 */
export async function loadLocales(opts: LoadLocalesOptions = {}): Promise<PublicLocaleOption[]> {
  source = opts;
  const doFetch = opts.fetchImpl ?? (typeof fetch === 'function' ? fetch : null);
  if (!doFetch) return availableLocales();
  const base = (opts.base ?? '').replace(/\/+$/, '');
  try {
    const extra = opts.headers ? await opts.headers() : {};
    const res = await doFetch(`${base}/api/public/branding`, {
      method: 'GET',
      headers: { Accept: 'application/json', ...extra },
      credentials: 'same-origin',
    });
    if (res.ok) {
      setLocalesFromBranding((await res.json()) as PublicBranding);
      return availableLocales();
    }
  } catch {
    /* the two built-in languages are a perfectly good answer */
  }
  localesKnown.value = true;
  localesVersion.value += 1;
  return availableLocales();
}
