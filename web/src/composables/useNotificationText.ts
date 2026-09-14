/**
 * The SPA's half of `lib/notificationText.ts` — the same renderer, handed this
 * app's current language and its switch-label catalogue.
 *
 * ⚠ A composable rather than a helper the two surfaces each write, because the
 * two surfaces are the bell list and the browser notification and they are
 * describing the SAME row. If one of them assembled `locale` or the fallback
 * label slightly differently, a person would read one sentence in the bell and
 * a different one in the toast that told them to look at the bell.
 */
import { useI18n } from 'vue-i18n';

import {
  renderNotification,
  type NotificationLike,
  type NotificationText,
  type NotifyLocale,
} from '@/lib/notificationText';
import { userEventKey } from '@/lib/webhookEvents';

export function useNotificationText() {
  const { t, te, locale } = useI18n();

  /** Render one row in the language this app is currently showing. */
  function notificationText(row: NotificationLike): NotificationText {
    const lang: NotifyLocale = locale.value === 'tr' ? 'tr' : 'en';
    // ⚠ `te()` first: vue-i18n's `t()` returns the KEY itself for a missing
    // entry, so passing it blind would hand the renderer
    // "userSettings.notifications.events.foo_bar" as a human-readable label —
    // a worse string than the raw event id it is supposed to be rescuing.
    const key = userEventKey(row.event);
    return renderNotification(row, lang, {
      fallbackLabel: te(key) ? t(key) : undefined,
    });
  }

  return { notificationText };
}
