import { api } from './client';

/**
 * PendingOp — async file operation row.
 *
 * Shape mirrors the SFC's `PendingOp` (`packages/core/src/composables/usePendingOps.ts`).
 * The backend currently emits its raw `ops.Op` shape from POST endpoints
 * (`kind` / `total` / `done`); the GET list endpoint that the SFC's
 * `useFileApi` polls is expected to translate to this shape — see
 * `internal/api/handlers/ops.go` for where that wiring belongs.
 *
 * If the list endpoint is missing, `list()` resolves to an empty array
 * (404 swallowed) so the tray stays silently empty.
 */
export interface PendingOp {
  id: number;
  op_type: 'copy' | 'move' | 'delete';
  status: 'pending' | 'running' | 'done' | 'error';
  progress_total: number;
  progress_done: number;
  target_path: string | null;
  source_dir: string | null;
  source_count: number;
  error_message: string | null;
  started_at: string | null;
  finished_at: string | null;
  created_at: string | null;
}

export interface OpsListResponse {
  ops: PendingOp[];
}

/**
 * Translate the backend's raw `ops.Op` shape into the SFC's `PendingOp`
 * shape. Used as a fallback when the endpoint emits the raw shape.
 */
export function normalizeOp(raw: Record<string, unknown>): PendingOp {
  const r = raw as {
    id?: number;
    kind?: PendingOp['op_type'];
    op_type?: PendingOp['op_type'];
    status?: string;
    total?: number;
    progress_total?: number;
    done?: number;
    progress_done?: number;
    failed?: number;
    dest?: string;
    target_path?: string;
    sources?: string[];
    source_dir?: string;
    source_count?: number;
    error?: string;
    error_message?: string;
    started_at?: string | null;
    finished_at?: string | null;
    created_at?: string | null;
  };
  // Backend uses 'ok' / 'failed' / 'partial' for terminal status; the SFC
  // uses 'done' / 'error'. Normalise.
  const status: PendingOp['status'] = (() => {
    const s = r.status;
    if (s === 'pending' || s === 'running') return s;
    if (s === 'ok') return 'done';
    if (s === 'failed' || s === 'partial') return 'error';
    if (s === 'done' || s === 'error') return s;
    return 'pending';
  })();
  const sources = r.sources;
  return {
    id: Number(r.id ?? 0),
    op_type: (r.op_type ?? r.kind ?? 'copy') as PendingOp['op_type'],
    status,
    progress_total: Number(r.progress_total ?? r.total ?? sources?.length ?? 0),
    progress_done: Number(r.progress_done ?? r.done ?? 0),
    target_path: r.target_path ?? r.dest ?? null,
    source_dir: r.source_dir ?? null,
    source_count: Number(r.source_count ?? sources?.length ?? 0),
    error_message: r.error_message ?? r.error ?? null,
    started_at: r.started_at ?? null,
    finished_at: r.finished_at ?? null,
    created_at: r.created_at ?? null,
  };
}

export const opsApi = {
  /**
   * List pending ops. Optional `status` filter mirrors the SFC's poll
   * shape. Returns an empty array when the endpoint is missing
   * (404 swallowed) so callers don't spam errors.
   */
  async list(params: { status?: 'running' | 'pending' | 'done' | 'error' } = {}): Promise<PendingOp[]> {
    try {
      const res = await api.get<OpsListResponse | { ops: Array<Record<string, unknown>> }>(
        '/files/ops',
        { params },
      );
      const data = res.data;
      const arr = Array.isArray((data as OpsListResponse).ops) ? (data as OpsListResponse).ops : [];
      return arr.map((row) => normalizeOp(row as unknown as Record<string, unknown>));
    } catch (e: unknown) {
      // 404/405/501 — backend hasn't wired the list yet OR is on an
      // older release where chi answers "method not allowed" because
      // only POST /ops is registered. Silent fallback in all three
      // cases so the tray polls forever without spamming the console.
      const status = (e as { response?: { status?: number } }).response?.status;
      if (status === 404 || status === 405 || status === 501) return [];
      throw e;
    }
  },

  /** Get one op by id. */
  async get(id: number): Promise<PendingOp | null> {
    try {
      const res = await api.get<Record<string, unknown>>(`/files/ops/${id}`);
      if (!res.data) return null;
      return normalizeOp(res.data);
    } catch (e: unknown) {
      const status = (e as { response?: { status?: number } }).response?.status;
      if (status === 404) return null;
      throw e;
    }
  },
};
