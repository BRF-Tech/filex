import { api } from './client';
import type { PaginatedResponse, Share } from './types';

/**
 * The CALLER's own public links — "Paylaştıklarım" / "My shares".
 *
 * ⚠ Not `api/shares.ts`. That one talks to `/admin/shares`, which is every
 * user's links and is refused outright to an account without admin rights;
 * this one talks to `/shares`, which is scoped in the backend by
 * `created_by = me` (handlers/shares_mine.go). Two endpoints, two audiences,
 * and a view that mixed them up would be an admin screen an ordinary person
 * watches 403.
 */

/** One row: the backend's `ShareWithMeta` envelope, minus the creator. */
export interface MyShareRow {
  share: Share;
  node_path?: string;
  storage_name?: string;
  /** Name of the app plugin that opened this link, when one did. */
  plugin_name?: string;
  /** The canonical public link, built by the server from its public origin. */
  url?: string;
  /**
   * What an app's link IS — its page's declared purpose (db.AppLink): a
   * signing link is a signing request, opens the app's page for it, and says
   * before a revoke that the revoke cancels the request (the owner's
   * decision, 2026-09-21). Absent for an ordinary share.
   */
  app?: MyShareApp;
}

/** db.AppLink: texts are wire.Text ({lang: …}). */
export interface MyShareApp {
  plugin: string;
  page: string;
  label: Record<string, string>;
  revoke?: Record<string, string>;
  /** The app's home view the row opens (`/app/:plugin/:view?section=`). */
  view?: string;
  section?: string;
}

/** Why a PIN could not be shown. `''` means it was. */
export type PinUnavailable = 'no_pin' | 'not_recoverable' | 'no_secret_key';

/**
 * The answer to "what is this link's PIN?".
 *
 * ⚠ An authorized caller always gets HTTP 200 — the three refusals are facts
 * about the LINK, not failures of the request, and each one is a different
 * sentence to a person. `pin` null with no reason should be impossible; it is
 * typed as possible because a server older than this feature answers 404 and
 * the interceptor would surface that as an error anyway.
 */
export interface SharePinAnswer {
  pin: string | null;
  reason?: PinUnavailable;
}

export interface MyShareListParams {
  page?: number;
  page_size?: number;
  active?: boolean;
}

export const MySharesApi = {
  async list(params: MyShareListParams = {}): Promise<PaginatedResponse<MyShareRow>> {
    const page = params.page ?? 1;
    const pageSize = params.page_size ?? 25;
    const { data } = await api.get<{
      items?: MyShareRow[];
      total?: number;
      page?: number;
      page_size?: number;
    }>('/shares', {
      params: {
        limit: pageSize,
        offset: (page - 1) * pageSize,
        ...(params.active ? { active: 'true' } : {}),
      },
    });
    return {
      items: data.items ?? [],
      total: data.total ?? 0,
      page: data.page ?? page,
      page_size: data.page_size ?? pageSize,
    };
  },

  /** Read one link's PIN. Audited server-side on every call. */
  async pin(id: number): Promise<SharePinAnswer> {
    const { data } = await api.get<SharePinAnswer>(`/shares/${id}/pin`);
    return data;
  },

  /**
   * Revoke — the link stops working, the row stays.
   *
   * ⚠ `/files/share/{id}`, the endpoint that has ALWAYS been the user's own
   * revoke (handlers/share.go HandleDelete: owner or admin, soft revoke). A
   * second revoke route for this screen would be a second place to get the
   * ownership check right.
   */
  async revoke(id: number): Promise<void> {
    await api.delete(`/files/share/${id}`);
  },
};
