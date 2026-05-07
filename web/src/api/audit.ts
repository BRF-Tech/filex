import { api } from './client';
import type { AuditEntry, PaginatedResponse } from './types';

export interface AuditListParams {
  user_id?: number;
  action?: string;
  target_type?: string;
  from?: string; // ISO datetime
  to?: string;
  page?: number;
  page_size?: number;
}

interface BackendEntryEnvelope {
  entry?: AuditEntry;
  user_email?: string;
}

interface BackendListResponse {
  entries: (AuditEntry | BackendEntryEnvelope)[] | null;
  total?: number;
  limit?: number;
  offset?: number;
}

export const AuditApi = {
  async list(params: AuditListParams = {}): Promise<PaginatedResponse<AuditEntry>> {
    // Backend returns `{entries, total, limit, offset}` and may wrap
    // each row as `{entry: AuditEntry, user_email}`. Normalize to the
    // paginated envelope the views expect.
    const { data } = await api.get<PaginatedResponse<AuditEntry> | BackendListResponse>(
      '/admin/audit',
      { params },
    );
    if ('items' in data && Array.isArray(data.items)) {
      return data as PaginatedResponse<AuditEntry>;
    }
    const env = data as BackendListResponse;
    const items = (env.entries ?? []).map((row) => {
      // Either the row already is an AuditEntry (id/at fields present)
      // or it's a `{entry, user_email}` wrapper.
      if (row && typeof row === 'object' && 'entry' in row && (row as BackendEntryEnvelope).entry) {
        const r = row as BackendEntryEnvelope;
        return { ...(r.entry as AuditEntry), user_email: r.user_email ?? null };
      }
      return row as AuditEntry;
    });
    const limit = env.limit ?? items.length;
    const offset = env.offset ?? 0;
    return {
      items,
      total: env.total ?? items.length,
      page: limit > 0 ? Math.floor(offset / limit) + 1 : 1,
      page_size: limit || items.length || 25,
    };
  },
};
