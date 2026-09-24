// The one notification poll, and the browser notifications it raises.
//
// ⚠ It lives in App.vue, not in the bell. The bell used to own the 15 s poll,
// and the bell only exists inside the admin panel's top nav — so on
// `/drive/explore`, which is the ONLY screen a non-admin ever sees (see
// router/index.ts, GitHub #14), nothing polled and nothing could have notified
// them. Moving the loop up one level means every signed-in screen is covered
// by the SAME loop, at the SAME cadence, rather than by a second one.
//
// ⚠ No new polling. This is the bell's existing 15 s unread-count call,
// unchanged. The list is fetched ONLY when that count stops matching the one
// the list was fetched with (stores/notifications → syncUnread), so a quiet
// instance costs exactly what it cost before.
//
// ⚠⚠ And it is the SAME list the bell draws. This loop used to fetch its own
// copy of the unread head for the browser notifications while the bell kept
// the rows it fetched once per page — so the badge rose and the list never
// did. Now one refresh feeds both: the toast is raised from `notif.feed`, and
// the bell, open or closed, is reading that same array.

import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';

import { useAuthStore } from '@/stores/auth';
import { useNotificationsStore } from '@/stores/notifications';
import { openNotificationTarget } from '@/lib/notificationNav';
import {
  isNotificationClickable,
  notificationHref,
  resolveNotificationTarget,
} from '@/lib/notificationTarget';
import { currentMountBase } from '@/router';
import { canShowBrowserNotification, raiseBrowserNotification } from '@/lib/browserNotify';
import { useNotificationText } from '@/composables/useNotificationText';

/** Same cadence the bell has always polled at. */
export const NOTIFY_POLL_MS = 15_000;

export function useNotificationWatcher() {
  const auth = useAuthStore();
  const notif = useNotificationsStore();
  const router = useRouter();
  // ⚠ The sentence is composed HERE, in the reader's language, not taken from
  // the row. The row's title is written once on the server in one language,
  // and for eight of the eleven file events it is not written at all — Send
  // substitutes the event id, which is how `{title: "file.uploaded"}` reached
  // a real OS notification (measured 2026-09-12). Same renderer as the bell
  // and the desktop shell; see lib/notificationText.ts.
  const { notificationText } = useNotificationText();

  let handle: number | null = null;
  /**
   * The highest notification id that existed when this tab started watching.
   * ⚠ Without a baseline the first poll after a reload would toast every
   * unread row the user already had — a page refresh must not replay history.
   */
  const highWater = ref<number | null>(null);
  let lastCount = -1;
  let inFlight = false;

  async function prime(): Promise<void> {
    const ok = await notif.refreshFeed();
    highWater.value = ok ? Math.max(0, ...notif.feed.map((n) => n.id)) : 0;
  }

  /** Runs after `syncUnread` has already brought `notif.feed` up to date. */
  function announceNew(): void {
    if (highWater.value == null) return;
    if (!canShowBrowserNotification(auth.user?.id)) return;
    const since = highWater.value;
    // Oldest first, so a burst arrives in the order it happened. Unread only:
    // a row read in another tab meanwhile is not news.
    const fresh = notif.feed.filter((n) => n.id > since && !n.read_at).sort((a, b) => a.id - b.id);
    for (const n of fresh) {
      const text = notificationText(n);
      // ⚠⚠ Rule 1 applies to the TOAST as well as to the bell row: a
      // notification is clickable exactly when it has somewhere to go. The
      // bell renders an inert row as plain text — but this loop handed the
      // toast for that same row an `onClick` and an empty `url`, and an empty
      // url is what made the service worker fall back to `/admin/explore`
      // (notify-sw.js). So one row was inert in the popover and, four inches
      // away in the OS, took a non-admin to an admin address.
      //
      // ⚠ The address is carried as well as the callback for the rows that DO
      // go somewhere. The callback is the good path — it marks the row read
      // and navigates inside the running SPA — but on a device where only a
      // service worker may notify there is no callback to run, and the worker
      // can open nothing but a URL. Same resolver for both, so the two cannot
      // land in two different places.
      const goes = isNotificationClickable(n.target);
      const url = goes ? notificationHref(resolveNotificationTarget(n.target), currentMountBase()) : '';
      void raiseBrowserNotification(
        {
          title: text.title,
          body: text.body,
          tag: `filex-notification-${n.id}`,
          url,
          onClick: goes
            ? () => {
                void notif.markRead(n.id).catch(() => {});
                void openNotificationTarget(router, n.target);
              }
            : undefined,
        },
        auth.user?.id,
      );
    }
    if (fresh.length) highWater.value = Math.max(since, ...fresh.map((n) => n.id));
  }

  async function poll(): Promise<void> {
    if (inFlight || !auth.isAuthenticated) return;
    inFlight = true;
    try {
      await notif.syncUnread();
      const count = notif.unreadCount;
      // Only a RISE means something new arrived. Marking rows read lowers it,
      // and a lower count must never be read as "there is news".
      if (lastCount >= 0 && count > lastCount) announceNew();
      lastCount = count;
    } finally {
      inFlight = false;
    }
  }

  function start(): void {
    if (handle != null) return;
    void prime().then(poll);
    handle = window.setInterval(() => void poll(), NOTIFY_POLL_MS);
  }

  function stop(): void {
    if (handle != null) window.clearInterval(handle);
    handle = null;
    lastCount = -1;
    highWater.value = null;
  }

  onMounted(() => {
    if (auth.isAuthenticated) start();
  });
  // Signing in mid-session starts the loop; signing out stops it and forgets
  // the baseline, so the next account does not inherit the previous one's.
  watch(
    () => auth.isAuthenticated,
    (on) => (on ? start() : stop()),
  );
  onBeforeUnmount(stop);

  return { poll, start, stop, highWater };
}
