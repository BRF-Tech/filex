import { api } from './client';

// AIToken mirrors backend model.APIToken (TokenHash is never serialized).
// `scopes` is a comma-separated allow-list: the verbs, an optional `root:`
// and the levels of the permissions that are not at their default
// (`comments:rw`); an empty list grants nothing.
// `permissions` is the server's answer for every permission's level -
// {comments: "read" | "rw"} - so this screen never repeats the default rule.
// `usernames` is the comma-separated identity allow-list a caller may act
// under (X-Filex-Token-User); first entry = default, "" = label only.
export interface AIToken {
  id: number;
  user_id: number;
  label: string;
  scopes: string;
  permissions?: Record<string, string>;
  usernames: string;
  last_used_at?: string | null;
  expires_at?: string | null;
  created_at: string;
}

export interface CreateTokenBody {
  label: string;
  scopes: string; // comma-separated; at least one verb (`comments:rw` for comments)
  usernames?: string[]; // identity allow-list; first = default
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

  /** `permissions` sets levels ({comments: 'rw'}); the verbs never change. */
  async update(
    id: number,
    body: { label?: string; usernames?: string[]; permissions?: Record<string, string> },
  ): Promise<void> {
    await api.patch(`/admin/ai-tokens/${id}`, body);
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/admin/ai-tokens/${id}`);
  },
};
