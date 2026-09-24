/**
 * usePendingOps — track async copy/move/delete operations.
 *
 * Backend POST /files/{copy,move,delete} returns `{op}` immediately
 * (status='pending') and the actual S3 work runs in a worker. The
 * client polls `/files/ops` every 2s while there's anything still
 * pending or running so the user gets live feedback ("3/5 copied").
 *
 * Polling is reference-counted: callers `register(op)` after starting a
 * new op; an interval ticks while the registered set has any non-
 * terminal entries. Terminal ops (done/error) stay in the local cache
 * for `RETAIN_MS` so the UI can flash a final state before they fade
 * out, then a sweep removes them.
 */

import { computed, ref, onBeforeUnmount } from 'vue';
import type { ExplorerConfig } from '../types/ExplorerConfig';
import type { FileApi } from './useFileApi';

/**
 * The queue kinds this package knows by name. A server may emit others (an
 * app-plugin job is `plugin-action` on the wire and `plugin` here); anything
 * unknown is carried through verbatim rather than being read as a delete.
 */
export type PendingOpType = 'copy' | 'move' | 'delete' | 'plugin' | 'trash-empty' | (string & {});

/** One produced file of a finished plugin job, as an adapter-qualified path. */
export interface PendingOpOutput {
  path: string;
}

export interface PendingOp {
  id: number;
  op_type: PendingOpType;
  /** `cancelled` — somebody stopped it (POST /ops/{id}/cancel). */
  status: 'pending' | 'running' | 'done' | 'error' | 'cancelled';
  progress_total: number;
  progress_done: number;
  /** Running cross-storage transfer's bytes (issue #27); absent otherwise. */
  bytes_total?: number;
  bytes_done?: number;
  target_path: string | null;
  source_dir: string | null;
  source_count: number;
  error_message: string | null;
  /** A failed APP job, classified by the server (lib/errorWords `jobFailure`
   *  says it); absent on copy/move/delete rows and on an older server. */
  error_code?: string;
  /** The engine a job needed and the server lacks (`error_code: engine_missing`). */
  error_engine?: string;
  started_at: string | null;
  finished_at: string | null;
  created_at: string | null;
  /* App-plugin jobs (`op_type: "plugin-action"` on the wire) — absent on
   * copy/move/delete rows. See docs/APP-PLUGINS-API.md → Ops rows. */
  plugin?: string;
  action?: string;
  /** The action's label in the caller's locale — the tray's row title. */
  label?: string;
  /** The last `job_progress` message. */
  message?: string;
  /** Files the job committed, once finished. */
  outputs?: PendingOpOutput[];
}

const POLL_MS = 2000;
const RETAIN_MS = 8000;

/**
 * normalizeOp maps the backend ops row (`{kind,total,done,failed,error,dest,
 * status:'ok'|'failed'|'partial'|…}`) onto the PendingOp contract the tray
 * renders (`{op_type,progress_total,progress_done,error_message,status:'done'|
 * 'error'|…}`). Without this the tray showed "undefined/undefined" for a
 * running op (progress_* were undefined) and never treated an 'ok'/'failed'
 * row as terminal. Tolerant of an already-normalized shape (idempotent).
 */
export function normalizeOp(raw: Record<string, unknown>): PendingOp {
  const num = (...vals: unknown[]): number => {
    for (const v of vals) if (typeof v === 'number') return v;
    return 0;
  };
  const str = (...vals: unknown[]): string | null => {
    for (const v of vals) if (typeof v === 'string' && v !== '') return v;
    return null;
  };
  const rawStatus = String(raw.status ?? 'pending');
  // ⚠ `cancelled` is an ending of its own. It used to fall through to
  // 'pending', so a job somebody cancelled sat in the operations centre as
  // "Queued" for as long as the list still carried the row.
  const status: PendingOp['status'] =
    rawStatus === 'ok' || rawStatus === 'done'
      ? 'done'
      : rawStatus === 'failed' || rawStatus === 'partial' || rawStatus === 'error'
        ? 'error'
        : rawStatus === 'cancelled'
          ? 'cancelled'
          : rawStatus === 'running'
            ? 'running'
            : 'pending';
  const sources = Array.isArray(raw.sources) ? raw.sources : [];
  // ⚠ Unknown kinds pass through. This used to fold everything that was not
  // copy/move into `delete`, which drew a bin beside a plugin job and read
  // "Deleted (1)" when it finished. `plugin-action` (the wire name) becomes
  // `plugin`; a row with no kind at all is the one case still read as delete
  // (the legacy delete endpoint's rows carried none).
  const opType = str(raw.op_type, raw.kind) ?? 'delete';
  const outputs = Array.isArray(raw.outputs)
    ? (raw.outputs as unknown[])
        .map((o) => (o && typeof o === 'object' ? str((o as { path?: unknown }).path) : null))
        .filter((p): p is string => p !== null)
        .map((path) => ({ path }))
    : undefined;
  return {
    id: num(raw.id),
    op_type: opType === 'plugin-action' ? 'plugin' : opType,
    status,
    progress_total: num(raw.progress_total, raw.total, sources.length),
    progress_done: num(raw.progress_done, raw.done),
    bytes_total: num(raw.bytes_total),
    bytes_done: num(raw.bytes_done),
    target_path: str(raw.target_path, raw.dest),
    source_dir: str(raw.source_dir),
    source_count: num(raw.source_count, sources.length),
    error_message: str(raw.error_message, raw.error),
    error_code: str(raw.error_code) ?? undefined,
    error_engine: str(raw.error_engine) ?? undefined,
    started_at: str(raw.started_at),
    finished_at: str(raw.finished_at),
    created_at: str(raw.created_at),
    plugin: str(raw.plugin) ?? undefined,
    action: str(raw.action) ?? undefined,
    label: str(raw.label) ?? undefined,
    message: str(raw.message) ?? undefined,
    outputs,
  };
}

export interface UsePendingOpsOptions {
  /** Called once an op flips into a terminal state (done|error). */
  onSettled?: (op: PendingOp) => void;
}

export function usePendingOps(
  config: ExplorerConfig,
  api: FileApi,
  opts: UsePendingOpsOptions = {},
) {
  void config; // kept for future per-instance config knobs
  const ops = ref<PendingOp[]>([]);
  const announced = new Set<number>();
  const settledAt = new Map<number, number>();
  // First poll after mount returns the server's recent-history window
  // (last 5 min of done/error rows). Mark them announced silently and
  // skip the callback so we don't double-fire toasts on F5.
  let firstPollDone = false;

  const hasActive = computed(() =>
    ops.value.some((o) => o.status === 'pending' || o.status === 'running'),
  );

  let timer: ReturnType<typeof setInterval> | undefined;

  function startPolling() {
    if (timer) return;
    if (!api.endpoints.opsList) return;
    timer = setInterval(() => {
      void poll();
    }, POLL_MS);
    // Kick a poll right away.
    void poll();
  }

  function stopPolling() {
    if (timer) {
      clearInterval(timer);
      timer = undefined;
    }
  }

  async function poll(): Promise<void> {
    if (!api.endpoints.opsList) return;
    try {
      const res = await api.jsonFetch<{ ops: Record<string, unknown>[] }>(api.endpoints.opsList);
      const incoming = (res.ops || []).map(normalizeOp);

      for (const op of incoming) {
        if ((op.status === 'done' || op.status === 'error' || op.status === 'cancelled') && !announced.has(op.id)) {
          announced.add(op.id);
          settledAt.set(op.id, Date.now());
          if (firstPollDone) {
            opts.onSettled?.(op);
          }
        }
      }

      // Filter incoming: pending/running always; settled only if user
      // is currently seeing them in the local tray.
      const localIds = new Set(ops.value.map((o) => o.id));
      const visibleIncoming = incoming.filter((op) => {
        if (op.status === 'pending' || op.status === 'running') return true;
        if (!firstPollDone) return false;
        return localIds.has(op.id);
      });

      const visibleIds = new Set(visibleIncoming.map((o) => o.id));
      const local = ops.value.filter((o) => !visibleIds.has(o.id));
      const merged = [...visibleIncoming, ...local];

      firstPollDone = true;

      // Sweep: drop terminal ops past RETAIN_MS.
      const now = Date.now();
      const after = merged.filter((o) => {
        if (o.status === 'pending' || o.status === 'running') return true;
        const at = settledAt.get(o.id);
        return at !== undefined && now - at < RETAIN_MS;
      });
      ops.value = after;

      if (!hasActive.value && after.length === 0) {
        stopPolling();
      }
    } catch (err) {
      // eslint-disable-next-line no-console
      console.warn('[usePendingOps] poll failed:', err);
    }
  }

  function register(raw: PendingOp | Record<string, unknown>): void {
    const op = normalizeOp(raw as Record<string, unknown>);
    const exists = ops.value.some((o) => o.id === op.id);
    if (!exists) {
      ops.value = [op, ...ops.value];
    }
    startPolling();
  }

  function dismiss(id: number): void {
    ops.value = ops.value.filter((o) => o.id !== id);
    settledAt.delete(id);
  }

  onBeforeUnmount(() => {
    stopPolling();
  });

  return {
    ops,
    hasActive,
    register,
    dismiss,
    poll,
    startPolling,
    stopPolling,
  };
}
