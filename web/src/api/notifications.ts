import { api } from './client';
import type {
  NotificationItem,
  NotificationListResponse,
  NotificationSettings,
  WebhookConfig,
} from './types';

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

  async updateSettings(payload: { in_app_enabled: boolean; muted_events: string[] }): Promise<NotificationSettings> {
    const { data } = await api.patch<NotificationSettings>('/notifications/settings', payload);
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
};

// Re-export the row type so views can reference it without dipping
// into types.ts directly.
export type { NotificationItem };
