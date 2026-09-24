/**
 * pluginLabel — a plugin's multilingual text, read in the viewer's language.
 *
 * Mirrors `wire.Text.Get` in backend/pkg/pluginkit/wire/wire.go: the
 * requested language, then English, then whatever is there. One place, so an
 * action row, a surface title and a footer button cannot fall back
 * differently.
 */
import type { PluginText } from '../types/Plugins';

/** The text for `locale`, falling back to `en`, then to any non-empty value. */
export function labelOf(text: PluginText | string | null | undefined, locale: string): string {
  if (!text) return '';
  if (typeof text === 'string') return text;
  const lang = String(locale || 'en').toLowerCase().split('-')[0];
  const direct = text[lang];
  if (typeof direct === 'string' && direct !== '') return direct;
  const en = text.en;
  if (typeof en === 'string' && en !== '') return en;
  for (const v of Object.values(text)) {
    if (typeof v === 'string' && v !== '') return v;
  }
  return '';
}

/**
 * The app's own words for THIS reader and nobody else: the requested
 * language, or its base language (`pt` answers `pt-br`), and otherwise ''.
 *
 * Unlike `labelOf` it does NOT fall back to English — which is the whole
 * point of `appTextOr` below.
 */
export function labelIn(text: PluginText | string | null | undefined, locale: string): string {
  if (!text) return '';
  if (typeof text === 'string') return text;
  const lang = String(locale || 'en').toLowerCase().split('-')[0];
  const direct = text[lang];
  return typeof direct === 'string' && direct !== '' ? direct : '';
}

/**
 * An app's text where FILEX HAS WORDS OF ITS OWN for the same thing — a
 * caption an app may override, an empty state, a progress line.
 *
 * ⚠⚠ THE ORDER IS THE WHOLE POINT. The obvious spelling,
 * `labelOf(appText, locale) || t(key)`, reads as "the app first, filex if the
 * app said nothing" — but `labelOf` falls back to ENGLISH, and English is
 * truthy, so the host string is unreachable for every reader whose language
 * the app does not speak. Measured in v0.43.0 on the signing app's date box:
 * an app shipping en/tr/es/de/fr, a reader in Arabic, a language pack holding
 * filex's own Arabic for those very captions — and three English words in the
 * middle of an Arabic screen, because the app's English won.
 *
 * So: the app's words for this reader, then FILEX's words, and only then
 * whatever other language the app has — the last resort, and labelled as one.
 *
 * ⚠ This is for a host string that TRANSLATES THE SAME THING. Where the host
 * string is a generic stand-in for something the app NAMES (an action's
 * label falling back to "Confirm"), the app's own name in another language
 * says more than filex's placeholder, and `labelOf(…) || t(…)` is right.
 */
export function appTextOr(
  text: PluginText | string | null | undefined,
  locale: string,
  host: () => string,
): string {
  const mine = labelIn(text, locale);
  if (mine) return mine;
  const ours = host();
  if (ours) return ours;
  return labelOf(text, locale);
}
