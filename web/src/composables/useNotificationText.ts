/**
 * Core's `useNotificationText`, handed this app's language and its settings
 * dialog's name for each kind of event (`userSettings.notifications.events.*`).
 *
 * ⚠ Not a second renderer: the composable and the phrasebook are
 * `@brftech/filex-core`'s (moved there with the bell on 2026-09-27). The bell's
 * own rows get the same two things from core directly — the language as a prop,
 * the labels through `NOTIFICATION_EVENT_LABEL`, which main.ts provides — so the
 * browser toast (useNotificationWatcher) and the admin page, the two readers of
 * this file, say what the bell says.
 */
import { useI18n } from 'vue-i18n';
import { useNotificationText as useCoreNotificationText, userEventKey } from '@brftech/filex-core';

export function useNotificationText() {
  const { t, te, locale } = useI18n();
  // ⚠ `te()` first: vue-i18n's `t()` returns the KEY itself for a missing
  // entry, so passing it blind would hand the renderer
  // "userSettings.notifications.events.foo_bar" as a human-readable label —
  // a worse string than the raw event id it is supposed to be rescuing.
  return useCoreNotificationText(
    () => String(locale.value),
    (event) => {
      const key = userEventKey(event);
      return te(key) ? t(key) : undefined;
    },
  );
}
