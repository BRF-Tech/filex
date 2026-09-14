import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { NotificationsApi } from '@/api/notifications';
import type { NotificationItem, NotificationSettings, WebhookConfig } from '@/api/types';
import { extractError } from '@/api/client';

/** How many of the person's own rows the bell holds. It shows 15; the rest is
 *  headroom for the browser-notification diff, which reads the same list. */
export const FEED_LIMIT = 50;

// Bell + admin store. The "user" half drives the in-page bell; the
// "admin" half powers /notifications page (full audit + webhook
// config). They share one Pinia store so the bell list mutation
// reflects in the admin page when the same user opens both.
export const useNotificationsStore = defineStore('notifications', () => {
  /** The admin audit page's list (instance-wide, paginated). */
  const items = ref<NotificationItem[]>([]);
  /**
   * The signed-in person's OWN rows, newest first — what the bell lists and
   * what the watcher diffs to raise browser notifications.
   *
   * ⚠⚠ ONE list, filled from ONE place (`refreshFeed`), for both readers. The
   * bell used to fetch its rows once — on a vnode hook that fires when the
   * popover is first rendered, not when it opens — while the watcher polled
   * the count and fetched its own copy of the unread head. So the badge kept
   * climbing and the list never moved: measured 2026-09-14, badge 9 over a
   * list whose newest row predated the arrival, still so after closing and
   * reopening, and still so with the panel left open while a tenth arrived.
   *
   * ⚠ Kept apart from `items`, which is the ADMIN page's instance-wide list.
   * Writing the person's own rows into it (what the bell used to do) replaced
   * the audit table under an admin who had the bell open on that page.
   */
  const feed = ref<NotificationItem[]>([]);
  const feedLoading = ref(false);
  /**
   * The unread count the feed was fetched alongside. When the polled count
   * stops matching it, something arrived (or was read elsewhere) and the list
   * is behind the badge. `null` = never fetched.
   */
  let feedCount: number | null = null;
  /** Only the newest refresh may write — an open racing a poll must not let the
   *  slower, older answer land last. */
  let feedSeq = 0;
  const total = ref(0);
  const limit = ref(50);
  const offset = ref(0);
  const onlyUnread = ref(false);
  const unreadCount = ref(0);
  const settings = ref<NotificationSettings | null>(null);
  const webhook = ref<WebhookConfig | null>(null);
  const loading = ref(false);
  const error = ref<string | null>(null);

  const hasUnread = computed(() => unreadCount.value > 0);

  /**
   * Fetch the person's rows AND the unread count together, so the badge and
   * the list are read from the same moment. Resolves `false` when the fetch
   * failed (the previous list is kept — a failed request is not "no rows").
   */
  async function refreshFeed(): Promise<boolean> {
    const mine = ++feedSeq;
    feedLoading.value = true;
    try {
      const [r, count] = await Promise.all([
        NotificationsApi.list({ unread: false, limit: FEED_LIMIT, offset: 0 }),
        NotificationsApi.unreadCount(),
      ]);
      if (mine !== feedSeq) return true;
      feed.value = r.items ?? [];
      unreadCount.value = count;
      feedCount = count;
      return true;
    } catch {
      return false;
    } finally {
      if (mine === feedSeq) feedLoading.value = false;
    }
  }

  /**
   * The poll's one call: read the count, and bring the list up to date when
   * the count no longer matches the one the list was fetched with. A quiet
   * instance therefore costs one small request per tick, exactly as before.
   */
  async function syncUnread(): Promise<void> {
    await fetchUnread();
    if (feedCount !== unreadCount.value) await refreshFeed();
  }

  async function fetchAdminList(opts: { unread?: boolean } = {}): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      const r = await NotificationsApi.adminList({
        unread: opts.unread ?? onlyUnread.value,
        limit: limit.value,
        offset: offset.value,
      });
      items.value = r.items ?? [];
      total.value = r.total;
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load admin notifications');
    } finally {
      loading.value = false;
    }
  }

  async function fetchUnread(): Promise<void> {
    try {
      unreadCount.value = await NotificationsApi.unreadCount();
    } catch {
      /* swallow — bell badge defaults to 0 */
    }
  }

  /** Both lists, and the count the feed agrees with, move together — a local
   *  read must not look like news to the next poll. */
  async function markRead(id: number): Promise<void> {
    await NotificationsApi.markRead(id);
    const at = new Date().toISOString();
    const stamp = (n: NotificationItem) => (n.id === id && !n.read_at ? { ...n, read_at: at } : n);
    const wasUnread = [...items.value, ...feed.value].some((n) => n.id === id && !n.read_at);
    items.value = items.value.map(stamp);
    feed.value = feed.value.map(stamp);
    if (wasUnread && unreadCount.value > 0) {
      unreadCount.value -= 1;
      if (feedCount !== null && feedCount > 0) feedCount -= 1;
    }
  }

  async function markAllRead(): Promise<void> {
    await NotificationsApi.markAllRead();
    const at = new Date().toISOString();
    const stamp = (n: NotificationItem) => ({ ...n, read_at: n.read_at ?? at });
    items.value = items.value.map(stamp);
    feed.value = feed.value.map(stamp);
    unreadCount.value = 0;
    if (feedCount !== null) feedCount = 0;
  }

  async function fetchSettings(): Promise<void> {
    try {
      settings.value = await NotificationsApi.getSettings();
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load notification settings');
    }
  }

  async function updateSettings(payload: { in_app_enabled: boolean; muted_events: string[] }): Promise<void> {
    settings.value = await NotificationsApi.updateSettings(payload);
  }

  async function fetchWebhook(): Promise<void> {
    try {
      webhook.value = await NotificationsApi.getWebhookConfig();
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load webhook config');
    }
  }

  async function updateWebhook(url: string, token: string): Promise<void> {
    await NotificationsApi.updateWebhookConfig(url, token);
    await fetchWebhook();
  }

  async function sendTest(): Promise<{ id: number }> {
    return NotificationsApi.sendTest();
  }

  function setPage(p: number): void {
    offset.value = Math.max(0, (p - 1) * limit.value);
  }

  function setUnreadFilter(u: boolean): void {
    onlyUnread.value = u;
    offset.value = 0;
  }

  return {
    items,
    total,
    limit,
    offset,
    onlyUnread,
    unreadCount,
    settings,
    webhook,
    loading,
    error,
    hasUnread,
    feed,
    feedLoading,
    refreshFeed,
    syncUnread,
    fetchAdminList,
    fetchUnread,
    markRead,
    markAllRead,
    fetchSettings,
    updateSettings,
    fetchWebhook,
    updateWebhook,
    sendTest,
    setPage,
    setUnreadFilter,
  };
});
