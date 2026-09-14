// Turning a resolved destination into an actual navigation, for the web.
//
// The RULE of where a click goes lives in `notificationTarget.ts` (shared with
// the desktop main process). This file is the web's half of the answer only:
// how that destination is reached inside a Vue SPA.

import type { Router } from 'vue-router';
import { useAuthStore } from '@/stores/auth';
import {
  notificationRoute,
  resolveNotificationTarget,
  shareHref,
  type NotificationTarget,
} from './notificationTarget';

/**
 * Navigate to whatever a notification is about.
 *
 * ⚠ The `hashchange` dispatch is not decoration. The explorer reads the folder
 * to open out of `location.hash` and listens for `hashchange` to follow later
 * edits — but `history.pushState`, which is what every vue-router navigation
 * uses, does NOT fire that event. So a click from a bell that is already on
 * the explorer page changed the address bar and left the listing exactly where
 * it was. Firing the event ourselves after the push is what makes the second
 * click work like the first.
 */
export async function openNotificationTarget(
  router: Router,
  target: NotificationTarget | null | undefined,
): Promise<void> {
  const dest = resolveNotificationTarget(target);

  if (dest.kind === 'share') {
    // A public share page is served by the backend, not by the SPA — a real
    // navigation, not a route.
    window.location.assign(shareHref(dest.token));
    return;
  }

  const route = notificationRoute(dest);
  if (!route) return;

  // ⚠ A row with no target resolves to the notifications PAGE, and that page
  // is the admin panel's instance-wide audit list, behind its route guard.
  // Pushing a non-admin there is not "nowhere" — the guard bounces them to
  // their front door, so clicking a notification in the explorer's bell would
  // throw away the folder they were standing in. For them the bell's own list
  // is the list: marking the row read (the caller does) is the whole click.
  if (router.resolve({ name: route.name }).meta.requiresAdmin && !useAuthStore().isAdmin) return;

  const hashBefore = window.location.hash;
  await router.push({ name: route.name, query: route.query, hash: route.hash || undefined });

  if (dest.kind === 'folder' && window.location.hash !== hashBefore) {
    window.dispatchEvent(new HashChangeEvent('hashchange'));
  }
}
