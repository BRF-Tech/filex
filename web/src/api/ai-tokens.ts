import { api } from './client';

// AIToken mirrors backend model.APIToken (TokenHash is never serialized).
// `scopes` is a comma-separated allow-list; "" means all scopes.
export interface AIToken {
  id: number;
  user_id: number;
  label: string;
  scopes: string;
  last_used_at?: string | null;
  expires_at?: string | null;
  created_at: string;
}

export interface CreateTokenBody {
  label: string;
  scopes: string; // comma-separated; "" == all
  expires_in_days?: number;
}

export interface CreateTokenResult {
  token: string; // plaintext — shown ONCE
  row: AIToken;
}

export const AITokensApi = {
  async list(): Promise<AIToken[]> {
    const { data } = await api.get<{ tokens: AIToken[] }>('/admin/ai-tokens');
    return data.tokens ?? [];
  },

  async create(body: CreateTokenBody): Promise<CreateTokenResult> {
    const { data } = await api.post<CreateTokenResult>('/admin/ai-tokens', body);
    return data;
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/admin/ai-tokens/${id}`);
  },
};
