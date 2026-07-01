import { api } from './client';
import type { AIToken, CreateTokenBody, CreateTokenResult } from './ai-tokens';

// Self-service tokens: any authenticated user (incl. non-admin user/viewer)
// mints tokens bound to themselves, capped server-side to their role ceiling
// and own grants. Distinct from the admin /admin/ai-tokens surface.
export const SelfTokensApi = {
  async list(): Promise<AIToken[]> {
    const { data } = await api.get<{ tokens: AIToken[] }>('/tokens');
    return data.tokens ?? [];
  },

  async create(body: CreateTokenBody): Promise<CreateTokenResult> {
    const { data } = await api.post<CreateTokenResult>('/tokens', body);
    return data;
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/tokens/${id}`);
  },
};
