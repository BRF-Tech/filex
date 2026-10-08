import { i18n } from '@/i18n';

/**
 * The language on screen, named to the server (`lang=`): a read whose answer
 * carries the server's words (notification rows, audit labels, the update
 * policy, the usage notes, a provider test) comes back said in it. The
 * account's language would rank above the browser's, and the screen - which
 * may be switched without the account being changed - is what is being read.
 */
export function screenLang(): string {
  return String(i18n.global.locale.value || '');
}

/** `{ lang }` for a request's params, or nothing before the locale is set. */
export function langParam(): { lang?: string } {
  const lang = screenLang();
  return lang ? { lang } : {};
}
