import { api } from './client';
import type { AuditEntry, PaginatedResponse } from './types';

export interface AuditListParams {
  user_id?: number;
  action?: string;
  from?: string; // ISO datetime
  to?: string;
  page?: number;
  page_size?: number;
}

interface BackendEntryEnvelope {
  entry?: AuditEntry;
  user_email?: string;
  target_name?: string;
  user_name?: string;
}

interface BackendListResponse {
  entries: (AuditEntry | BackendEntryEnvelope)[] | null;
  total?: number;
  limit?: number;
  offset?: number;
}

/**
 * One audit row in the shape the views read.
 *
 * ⚠ The handler's rows carry `created_at`; `AuditEntry` — and so the Audit
 * page's "Created" column and the dashboard's Recent activity — reads `at`.
 * Nothing mapped one to the other, so every timestamp on both screens printed
 * "—". Shared by the audit list and the dashboard so there is one mapping.
 */
export function toAuditEntry(row: AuditEntry): AuditEntry {
  const r = row as AuditEntry & { created_at?: string };
  return { ...r, at: r.at ?? r.created_at ?? '' };
}

/**
 * The page's filter as the handler reads it.
 *
 * ⚠⚠ Three of the page's filters reached the server as nothing
 * (handlers/audit.go): it pages by `limit`/`offset` and was sent
 * `page`/`page_size`, so page 2 showed page 1 again; it parses `from`/`to` as
 * RFC 3339 and was sent `<input type="datetime-local">`'s "2026-09-22T14:00",
 * which does not parse and was dropped; and it has no `target_type` filter at
 * all. The time is read in the viewer's clock and sent as an instant.
 */
export function toServerParams(p: AuditListParams): Record<string, string | number> {
  const out: Record<string, string | number> = {};
  if (p.user_id) out.user_id = p.user_id;
  if (p.action) out.action = p.action;
  const size = p.page_size ?? 50;
  out.limit = size;
  out.offset = Math.max(0, ((p.page ?? 1) - 1) * size);
  for (const k of ['from', 'to'] as const) {
    const v = p[k];
    if (!v) continue;
    const d = new Date(v);
    if (!Number.isNaN(d.getTime())) out[k] = d.toISOString();
  }
  return out;
}

export const AuditApi = {
  async list(params: AuditListParams = {}): Promise<PaginatedResponse<AuditEntry>> {
    // Backend returns `{entries, total, limit, offset}` and may wrap
    // each row as `{entry: AuditEntry, user_email}`. Normalize to the
    // paginated envelope the views expect.
    const { data } = await api.get<PaginatedResponse<AuditEntry> | BackendListResponse>(
      '/admin/audit',
      { params: toServerParams(params) },
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
        return toAuditEntry({
          ...(r.entry as AuditEntry),
          user_email: r.user_email ?? null,
          user_name: r.user_name ?? null,
          target_name: r.target_name ?? null,
        });
      }
      return toAuditEntry(row as AuditEntry);
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
