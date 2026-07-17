import { api } from './client';
import type {
  WebhookTarget,
  WebhookTargetCreatePayload,
  WebhookTargetPatchPayload,
  WebhookTargetTestResult,
} from './types';

// WebhooksApi covers the webhook v2 target CRUD + test-fire endpoints
// (/api/admin/webhooks). The legacy single global webhook stays on
// NotificationsApi (webhook-config). Secrets are write-only: responses
// only ever carry `secret_set`.
export const WebhooksApi = {
  async list(): Promise<WebhookTarget[]> {
    const { data } = await api.get<{ items: WebhookTarget[] }>('/admin/webhooks');
    return data.items ?? [];
  },

  async create(payload: WebhookTargetCreatePayload): Promise<WebhookTarget> {
    const { data } = await api.post<WebhookTarget>('/admin/webhooks', payload);
    return data;
  },

  async update(id: number, payload: WebhookTargetPatchPayload): Promise<WebhookTarget> {
    const { data } = await api.patch<WebhookTarget>(`/admin/webhooks/${id}`, payload);
    return data;
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/admin/webhooks/${id}`);
  },

  async test(id: number): Promise<WebhookTargetTestResult> {
    const { data } = await api.post<WebhookTargetTestResult>(`/admin/webhooks/${id}/test`);
    return data;
  },
};

export type { WebhookTarget };
