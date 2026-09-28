/**
 * One notification row, said in the reader's language — `lib/notificationText.ts`
 * handed the language on screen, the installed language pack's phrases and the
 * reader's direction.
 *
 * ⚠ A composable rather than a helper each surface writes, because the surfaces
 * are the bell list, the full-list screen and the browser notification, and
 * they describe the SAME row. If one of them assembled the language or the
 * fallback label slightly differently, a person would read one sentence in the
 * bell and a different one in the toast that told them to look at the bell.
 *
 * ⚠ Moved here from web/src/composables (2026-09-27) with the bell itself, so
 * the desktop app's bell says what the web's says. The one piece that stays
 * with a host is the EVENT LABEL (`NOTIFICATION_EVENT_LABEL`): the admin app's
 * settings catalogue names each kind of event ("A new file arrives") and the
 * renderer falls back on that name for an event its phrasebook does not know.
 * A host without that catalogue (the desktop app, an embed) provides nothing
 * and the renderer uses its own fallback — the same one the desktop's native
 * notification has always used.
 */
import { computed, getCurrentInstance, inject, type InjectionKey } from 'vue';

import { localeStrings, localesVersion } from '../lib/uiLocales';
import { foreignText } from '../lib/direction';
import { resolveE2eName } from '../lib/e2eNameRegistry';
import {
  renderNotification,
  type NotificationLike,
  type NotificationText,
  type NotifyLocale,
} from '../lib/notificationText';

/**
 * A host's human name for an event, in the language on screen — `undefined`
 * when it has none. Provided app-wide by the web (`app.provide` in main.ts), so
 * a bell drawn in a slot and one drawn in the admin nav read the same labels.
 */
export const NOTIFICATION_EVENT_LABEL: InjectionKey<(event: string) => string | undefined> =
  Symbol('filex-notification-event-label');

export function useNotificationText(
  /** The language on screen — any offered code, a language pack's included. */
  locale: () => string,
  eventLabel?: (event: string) => string | undefined,
) {
  const hostLabel =
    eventLabel ?? (getCurrentInstance() ? inject(NOTIFICATION_EVENT_LABEL, undefined) : undefined);

  /**
   * The installed language pack's `server.notify.*` strings for the language
   * on screen — the phrases in a language filex does not ship (or a pack's
   * overlay of one it does). Re-read when a pack arrives or leaves
   * (`localesVersion`), and filtered ONCE here: `localeStrings` copies the
   * whole ~3 000-key table, and a bell renders a row per notification.
   */
  const packNotify = computed(() => {
    void localesVersion.value;
    const all = localeStrings(locale());
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(all)) if (k.startsWith('server.notify.')) out[k] = v;
    return Object.keys(out).length ? out : undefined;
  });

  /** Render one row in the language this surface is currently showing. */
  function notificationText(row: NotificationLike): NotificationText {
    const code = String(locale() || 'en');
    const lang: NotifyLocale = code === 'tr' ? 'tr' : 'en';
    return renderNotification(row, lang, {
      fallbackLabel: hostLabel?.(row.event),
      strings: packNotify.value,
      lang: code,
      // ⚠⚠ The reader's direction. A notification is composed out of a
      // phrase and a ROW, never through a catalogue, so no post-translation
      // hook ever sees it — and nearly every body is machine text (a path, a
      // reason, an app's own words). Every surface this composable serves gets
      // it, because they describe one row.
      foreign: (text: string) => foreignText(code, text),
      // wiring:e2 names — an item inside an encrypted folder is named only
      // where an explorer in this tab has the folder unlocked; otherwise the
      // renderer says "🔒 Encrypted item" (never the ciphertext).
      e2eName: resolveE2eName,
    });
  }

  return { notificationText };
}
