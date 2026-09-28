import { defineStore } from 'pinia';
import { ref } from 'vue';
import { createNotificationFeed } from '@brftech/filex-core';
import { NotificationsApi } from '@/api/notifications';
import type { NotificationItem, NotificationSettings, WebhookConfig } from '@/api/types';
import { extractError } from '@/api/client';
import { t } from '@/i18n';

// Bell + admin store.
//
// ⚠⚠ The "user" half — the person's own rows, the unread count, the full list
// "see all" opens, marking read — is NOT written here any more. It is core's
// `createNotificationFeed` (packages/core/src/composables/useNotificationFeed),
// since 2026-09-27: the desktop app's explorer draws the same bell over the
// same feed logic, and two copies of "is this row read yet" would be two
// answers. This store composes it and adds the ADMIN half — the /notifications
// page's instance-wide audit list and the webhook settings — in one pinia
// store, so a row marked read in the bell stops looking unread in the admin
// table when the same person has both open (`alsoStamp`).
export const useNotificationsStore = defineStore('notifications', () => {
  /** The admin audit page's list (instance-wide, paginated). */
  const items = ref<NotificationItem[]>([]);

  const own = createNotificationFeed<NotificationItem>({
    transport: NotificationsApi,
    errorText: (e) => extractError(e, t('errors.loadFailed')),
    // ⚠ Kept apart from the feed, and stamped with it: `items` is the ADMIN
    // page's instance-wide list. Writing the person's own rows into it (what
    // the bell used to do) replaced the audit table under an admin who had
    // the bell open on that page; leaving it unstamped left a row read in the
    // bell looking unread in the table two inches away.
    alsoStamp: () => [items],
  });

  const total = ref(0);
  const limit = ref(50);
  const offset = ref(0);
  const onlyUnread = ref(false);
  /**
   * Unread rows in the ADMIN list (everybody's), which is not the admin's own
   * badge: the page printed "1 okunmamış" (its reader's count) beside three
   * unread rows of other people's (release-candidate sweep, 2026-09-21).
   */
  const adminUnread = ref(0);
  const settings = ref<NotificationSettings | null>(null);
  const webhook = ref<WebhookConfig | null>(null);
  const loading = ref(false);
  const error = ref<string | null>(null);

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
      error.value = extractError(e, t('errors.loadFailed'));
    } finally {
      loading.value = false;
    }
  }

  /** The admin list's own unread total — asked of the admin list itself. */
  async function fetchAdminUnread(): Promise<void> {
    try {
      const r = await NotificationsApi.adminList({ unread: true, limit: 1, offset: 0 });
      adminUnread.value = r.total ?? 0;
    } catch {
      /* the count is decoration; the list still loads */
    }
  }

  async function fetchSettings(): Promise<void> {
    try {
      settings.value = await NotificationsApi.getSettings();
    } catch (e: unknown) {
      error.value = extractError(e, t('errors.loadFailed'));
    }
  }

  async function updateSettings(payload: { in_app_enabled: boolean; muted_events: string[] }): Promise<void> {
    settings.value = await NotificationsApi.updateSettings(payload);
  }

  async function fetchWebhook(): Promise<void> {
    try {
      webhook.value = await NotificationsApi.getWebhookConfig();
    } catch (e: unknown) {
      error.value = extractError(e, t('errors.loadFailed'));
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
    // The person's own half — core's feed (see the note above).
    ...own,
    // The admin half.
    items,
    total,
    limit,
    offset,
    onlyUnread,
    adminUnread,
    settings,
    webhook,
    loading,
    error,
    fetchAdminList,
    fetchAdminUnread,
    fetchSettings,
    updateSettings,
    fetchWebhook,
    updateWebhook,
    sendTest,
    setPage,
    setUnreadFilter,
  };
});
