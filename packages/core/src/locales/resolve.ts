import type { LocaleCode } from '../types/ExplorerConfig';
import { acceptableLocale, hasLocale, normalizeLocaleCode } from '../lib/uiLocales';

/**
 * The language filex falls back to when the host does not say.
 *
 * ⚠ It used to be `tr`, in eleven places. filex is an English-first
 * open-source product, so `<filex-explorer api-base="…">` with no `locale`
 * rendered a Turkish UI to whoever installed it — and the web component
 * defaults `locale` to `''`, so "no locale" is the normal case for an embed.
 * The admin SPA never showed it because it always passes one.
 */
const FALLBACK: LocaleCode = 'en';

/**
 * Resolve the locale to render in.
 *
 * Order, and the reasoning for it:
 *
 *   1. **What the host asked for.** An explicit `locale` always wins. A host
 *      that names a language has made a decision, and guessing over it would
 *      make the prop advisory.
 *   2. **What the browser asks for**, via `navigator.languages` /
 *      `navigator.language` — the same signal `Accept-Language` carries, and
 *      the reason a Turkish reader gets Turkish without the embedder doing
 *      anything.
 *   3. **`en`.**
 *
 * ⚠ Step 2 makes the same embed render differently for two people. That is
 * deliberate — it is what "follow the browser" means — but it also means a
 * screenshot, a test or a bug report about this component must state its
 * locale, because the component alone no longer determines it. Pass an
 * explicit `locale` anywhere the answer has to be stable.
 *
 * Matching is on the primary subtag, so `tr-TR` and `en-GB` resolve, and a
 * language pack's own tag (`es`, `pt-br`) resolves while the pack is offered;
 * anything nothing offers falls through rather than half-rendering.
 */
export function resolveLocale(explicit?: LocaleCode | '' | null): LocaleCode {
  // ⚠⚠ Not "en or tr". It was, and every explorer surface — the listing, the
  // details panel, the dialogs, some twelve call sites — turned a language
  // pack's `es` into the browser's guess: the admin panel around the explorer
  // spoke Spanish and the explorer inside it did not (measured 2026-09-21).
  // An offered language stands; while the server's list is still in flight a
  // plausible one is HELD (`acceptableLocale`), so the explorer does not
  // flash the browser's language on its way to the one the host asked for.
  if (explicit && acceptableLocale(explicit)) return normalizeLocaleCode(explicit);
  return detectLocale();
}

/**
 * The browser's preference, or `en`.
 *
 * ⚠ Guarded for a non-browser runtime: this package is imported by SSR and by
 * unit tests under Node, where `navigator` is absent — and an exception here
 * would take down the whole explorer over a language choice.
 */
export function detectLocale(): LocaleCode {
  try {
    const nav = typeof navigator === 'undefined' ? undefined : navigator;
    if (!nav) return FALLBACK;
    const wanted = nav.languages && nav.languages.length ? nav.languages : [nav.language];
    for (const tag of wanted) {
      if (!tag) continue;
      // An offered language, built-in or added by a pack: a visitor whose
      // browser asks for Spanish on an instance with a Spanish pack reads
      // Spanish. (`hasLocale` is reactive, so a caller inside a computed
      // re-decides when the pack list arrives.)
      if (hasLocale(tag)) return normalizeLocaleCode(tag);
    }
  } catch {
    /* a hostile or exotic runtime is not a reason to fail to render */
  }
  return FALLBACK;
}
