import { api } from './client';

/**
 * Multi-tenant mode - Admin → Multi-tenant mode (backend
 * handlers/tenancy_admin.go, internal/tenancy; task #167).
 *
 *   GET /api/admin/tenancy
 *   PUT /api/admin/tenancy   {enabled, confirm?}
 *
 * The platform operator's alone: a tenant's administrator is refused both
 * (403 supertenant_only), and a change needs a person signed in to the panel
 * (an API key is refused, 403 session_required).
 *
 * A change is SAVED for the next start: the running server keeps the mode it
 * started with, and `restart_required` says so until filex is restarted.
 */
export interface TenancyState {
  /** The mode the running server enforces (capabilities `multi_tenant`). */
  in_force: boolean;
  /** The mode the next start will run with. */
  next_start: boolean;
  /** The switch as saved; null when it was never saved. */
  saved: boolean | null;
  /** FILEX_MULTI_TENANT or the config file pins the mode: no change here. */
  locked: boolean;
  locked_by?: 'environment' | 'config_file';
  /** What to change instead: FILEX_MULTI_TENANT, or `multi_tenant` in the config file. */
  variable?: string;
  restart_required: boolean;
  /** Tenants besides the platform's own: what turning the mode off puts in maintenance. */
  tenants: number;
}

/** Why a change was refused, when the server said it in a code the page words itself. */
export type TenancyRefusal =
  | { code: 'confirm_required'; tenants: number }
  | { code: 'tenancy_locked'; lockedBy: string };

export function tenancyRefusal(err: unknown): TenancyRefusal | null {
  const res = (err as { response?: { status?: number; data?: Record<string, unknown> } })?.response;
  const data = res?.data ?? {};
  if (data.error === 'confirm_required') {
    return { code: 'confirm_required', tenants: typeof data.tenants === 'number' ? data.tenants : 0 };
  }
  if (data.error === 'tenancy_locked') {
    return { code: 'tenancy_locked', lockedBy: typeof data.locked_by === 'string' ? data.locked_by : '' };
  }
  return null;
}

export const TenancyApi = {
  async get(): Promise<TenancyState> {
    const { data } = await api.get<TenancyState>('/admin/tenancy');
    return data;
  },
  /**
   * Saves the switch. Turning the mode off while tenants exist needs
   * `confirm` = their number (TenancyState.tenants).
   */
  async set(enabled: boolean, confirm?: string): Promise<TenancyState> {
    const body: { enabled: boolean; confirm?: string } = { enabled };
    if (confirm !== undefined) body.confirm = confirm;
    const { data } = await api.put<TenancyState>('/admin/tenancy', body);
    return data;
  },
};
