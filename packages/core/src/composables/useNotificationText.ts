/**
 * One notification row's words on a screen - the SERVER's sentence
 * (`GET /api/notifications` answers each row's `title` and `body` already said
 * in the reader's language, backend internal/notify say.go), with an encrypted
 * item's real name where this tab's explorer has its folder unlocked
 * (lib/notificationText.ts).
 *
 * ⚠ A composable rather than a helper each surface writes, because the surfaces
 * are the bell list, the full-list screen, the page's pop-up and the admin
 * history, and they describe the SAME row: one of them naming an encrypted
 * item another one locks is the kind of difference this exists to prevent.
 *
 * ⚠⚠ It composes no sentence and needs no language: the language is the
 * server's to apply (the request names the screen's, `lang=`). Nothing here may
 * build words from a row's event or meta again
 * (web/tests/quality/noClientNotificationText.test.ts).
 */
import { resolveE2eName } from '../lib/e2eNameRegistry';
import { notificationText as said, type NotificationLike, type NotificationText } from '../lib/notificationText';

export function useNotificationText() {
  /** One row as this screen shows it. */
  function notificationText(row: NotificationLike): NotificationText {
    return said(row, resolveE2eName);
  }
  return { notificationText };
}
