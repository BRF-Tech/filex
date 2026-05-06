import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { NotificationsApi } from '@/api/notifications';
import type { NotificationItem, NotificationSettings, WebhookConfig } from '@/api/types';
import { extractError } from '@/api/client';

// Bell + admin store. The "user" half drives the in-page bell; the
// "admin" half powers /notifications page (full audit + webhook
// config). They share one Pinia store so the bell list mutation
// reflects in the admin page when the same user opens both.
export const useNotificationsStore = defineStore('notifications', () => {
  const items = ref<NotificationItem[]>([]);
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

  async function fetchUserList(opts: { unread?: boolean } = {}): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      const r = await NotificationsApi.list({
        unread: opts.unread ?? onlyUnread.value,
        limit: limit.value,
        offset: offset.value,
      });
      items.value = r.items ?? [];
      total.value = r.total;
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load notifications');
    } finally {
      loading.value = false;
    }
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

  async function markRead(id: number): Promise<void> {
    await NotificationsApi.markRead(id);
    items.value = items.value.map((n) => (n.id === id ? { ...n, read_at: new Date().toISOString() } : n));
    if (unreadCount.value > 0) unreadCount.value -= 1;
  }

  async function markAllRead(): Promise<void> {
    await NotificationsApi.markAllRead();
    items.value = items.value.map((n) => ({ ...n, read_at: n.read_at ?? new Date().toISOString() }));
    unreadCount.value = 0;
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
    fetchUserList,
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
