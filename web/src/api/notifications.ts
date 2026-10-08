import type { WebPushDevice, WebPushStatus, WebPushSubscriptionBody } from '@brftech/filex-core';
import { api } from './client';
import type {
  DigestPolicy,
  NotificationItem,
  NotificationListResponse,
  NotificationSettings,
  NotificationSettingsPatch,
  WebhookConfig,
} from './types';

/** GET /api/admin/notifications/push: the instance's key and how many devices. */
export interface PushAdminInfo {
  push: { available: boolean; reason?: string; public_key?: string; key_created_at?: string };
  devices: number;
}

// ⚠ No `lang=`: every row of a list comes back said in the language of the
// reader's ACCOUNT (backend notify say.go, PersonLang) - the one setting the
// bell, the push, the email and the desktop toast all read. The screen's
// language IS the account's (changing it on screen writes the account), so a
// list asked again after a change (useNotificationWatcher) is said in it.

// NotificationsApi covers both user-scope (/api/notifications/...)
// and admin-scope (/admin/api/notifications/...) endpoints. The
// shared `api` axios baseURL is `/api`, so the admin paths reach
// `/api/admin/notifications/...` (matching the chi router).
export const NotificationsApi = {
  // User scope
  async list(params: { unread?: boolean; limit?: number; offset?: number } = {}): Promise<NotificationListResponse> {
    const { data } = await api.get<NotificationListResponse>('/notifications', {
      params: { ...params, unread: params.unread ? 'true' : undefined },
    });
    return data;
  },

  async unreadCount(): Promise<number> {
    const { data } = await api.get<{ count: number }>('/notifications/unread-count');
    return data.count;
  },

  async markRead(id: number): Promise<void> {
    await api.post(`/notifications/${id}/read`);
  },

  async markAllRead(): Promise<void> {
    await api.post('/notifications/read-all');
  },

  async getSettings(): Promise<NotificationSettings> {
    const { data } = await api.get<NotificationSettings>('/notifications/settings');
    return data;
  },

  async updateSettings(payload: NotificationSettingsPatch): Promise<NotificationSettings> {
    const { data } = await api.patch<NotificationSettings>('/notifications/settings', payload);
    return data;
  },

  // Web Push (#191): this browser as one of the person's devices
  // (core lib/webPush drives these; backend handlers/notification_push.go).
  async pushStatus(): Promise<WebPushStatus> {
    const { data } = await api.get<WebPushStatus>('/notifications/push');
    return data;
  },

  async pushSubscribe(body: WebPushSubscriptionBody): Promise<WebPushDevice> {
    const { data } = await api.post<WebPushDevice>('/notifications/push/subscriptions', body);
    return data;
  },

  async pushForget(endpoint: string): Promise<void> {
    await api.post('/notifications/push/forget', { endpoint });
  },

  async pushRemove(id: number): Promise<void> {
    await api.delete(`/notifications/push/subscriptions/${id}`);
  },

  async pushTest(): Promise<{ sent: number; failed: number }> {
    const { data } = await api.post<{ sent: number; failed: number }>('/notifications/push/test');
    return data;
  },

  // Admin scope
  async adminList(params: { unread?: boolean; limit?: number; offset?: number } = {}): Promise<NotificationListResponse> {
    const { data } = await api.get<NotificationListResponse>('/admin/notifications', {
      params: { ...params, unread: params.unread ? 'true' : undefined },
    });
    return data;
  },

  async sendTest(): Promise<{ id: number }> {
    const { data } = await api.post<{ id: number }>('/admin/notifications/test');
    return data;
  },

  async getWebhookConfig(): Promise<WebhookConfig> {
    const { data } = await api.get<WebhookConfig>('/admin/notifications/webhook-config');
    return data;
  },

  async updateWebhookConfig(url: string, token: string): Promise<{ ok: boolean }> {
    const { data } = await api.patch<{ ok: boolean }>('/admin/notifications/webhook-config', { url, token });
    return data;
  },

  // The digest's defaults for the tenant (or the instance) the caller runs.
  async getDigestPolicy(): Promise<DigestPolicy> {
    const { data } = await api.get<DigestPolicy>('/admin/notifications/digest');
    return data;
  },

  // The instance's Web Push key (the platform's administrators only).
  async getPushAdmin(): Promise<PushAdminInfo> {
    const { data } = await api.get<PushAdminInfo>('/admin/notifications/push');
    return data;
  },

  /** A new key: every device is forgotten and subscribes again on its next start. */
  async rotatePush(): Promise<{ push: PushAdminInfo['push']; devices_forgotten: number }> {
    const { data } = await api.post<{ push: PushAdminInfo['push']; devices_forgotten: number }>(
      '/admin/notifications/push/rotate',
    );
    return data;
  },

  /** `urgent_events: null` restores the built-in list; a field left out keeps its value. */
  async updateDigestPolicy(patch: { window_minutes?: number; urgent_events?: string[] | null }): Promise<DigestPolicy> {
    const { data } = await api.patch<DigestPolicy>('/admin/notifications/digest', patch);
    return data;
  },
};

// Re-export the row type so views can reference it without dipping
// into types.ts directly.
export type { NotificationItem };
