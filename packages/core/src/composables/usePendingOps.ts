/**
 * usePendingOps — track async copy/move/delete operations.
 *
 * Backend POST /files/{copy,move,delete} returns `{op}` immediately
 * (status='pending') and the actual S3 work runs in a worker. The
 * client polls `/files/ops` every 2s while there's anything still
 * pending or running so the user gets live feedback ("3/5 kopyalandı").
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

const POLL_MS = 2000;
const RETAIN_MS = 8000;

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
      const res = await api.jsonFetch<{ ops: PendingOp[] }>(api.endpoints.opsList);
      const incoming = res.ops || [];

      for (const op of incoming) {
        if ((op.status === 'done' || op.status === 'error') && !announced.has(op.id)) {
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

  function register(op: PendingOp): void {
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
