// What a refused sign-in says beyond "no": how many tries are left before the
// lock falls, or — when it already fell — how long until the next try.
//
// The server (backend/internal/api/handlers/auth.go, loginFailed/writeLocked)
// answers a wrong attempt with 401 `{error, message, remaining, limit, scope}`
// and one that tripped or met a lock with 429 `{error, message, locked, scope,
// retry_after}` plus a Retry-After header. Read here, once, so the panel's
// form and any other sign-in form say the same thing.
import axios from 'axios';

import { ssoRefusalReason, type SsoRefusalReason } from '@/lib/ssoRefusal';

export type RefusalScope = 'account' | 'ip';

export interface LoginRefusal {
  status: number;
  /** A lock is in force: the attempt was not even tried. */
  locked: boolean;
  scope?: RefusalScope;
  /** Seconds until the lock ends (429). */
  retryAfter?: number;
  /** Wrong attempts left before a lock (401). */
  remaining?: number;
  limit?: number;
  /** A wrong two-factor code: the words about "email or password" do not fit. */
  totp?: boolean;
  /**
   * Why a person whose password was right gets no session (403 `{reason}`):
   * only from a provider whose operator switched show_refusal_reason on
   * (lib/ssoRefusal). Never set otherwise.
   */
  reason?: SsoRefusalReason;
}

const asScope = (v: unknown): RefusalScope | undefined => (v === 'account' || v === 'ip' ? v : undefined);
const asNumber = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);

/** The refusal in a failed sign-in request, or null when it says none of this. */
export function readLoginRefusal(err: unknown): LoginRefusal | null {
  if (!axios.isAxiosError(err) || !err.response) return null;
  const status = err.response.status;
  const d = (err.response.data ?? {}) as Record<string, unknown>;
  if (status === 429) {
    const header = Number.parseInt(String(err.response.headers?.['retry-after'] ?? ''), 10);
    const retryAfter = asNumber(d.retry_after) ?? (Number.isFinite(header) ? header : undefined);
    return { status, locked: true, scope: asScope(d.scope), retryAfter };
  }
  if (status === 403) {
    const reason = ssoRefusalReason(d.reason);
    return reason ? { status, locked: false, reason } : null;
  }
  if (status === 401) {
    return {
      status,
      locked: false,
      scope: asScope(d.scope),
      remaining: asNumber(d.remaining),
      limit: asNumber(d.limit),
      totp: d.totp_required === true,
    };
  }
  return null;
}

/** "4:05" — a lock's countdown, the way a clock reads it. */
export function clock(seconds: number): string {
  const s = Math.max(0, Math.ceil(seconds));
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, '0')}`;
}
