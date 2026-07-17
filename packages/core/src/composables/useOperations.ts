/**
 * useOperations — unified long-running-operation store (wiring:c3).
 *
 * One presentation-level aggregation point for everything that used to be
 * scattered across corner UIs:
 *   - uploads        (useUploadChunked jobs / legacy multipart rows)
 *   - ops-queue jobs (usePendingOps copy/move/delete worker ops)
 *   - future kinds   (convert / archive — same `sync()` contract, own group)
 *
 * The store does NOT talk to the network. Publishers (renderless
 * UploadProgress / PendingOpsTray adapters) reconcile their reactive source
 * lists into the store via `sync(group, items)`; OperationsCenter renders the
 * merged picture (badge + panel). Action callbacks (cancel/retry/dismiss)
 * are registered per row so the panel can route user intent back to the
 * owning surface without the store knowing any transport details.
 *
 * Lifecycle rules (per the İşlem Merkezi contract):
 *   - running rows live in `active`;
 *   - done/aborted rows linger in `active` for DONE_LINGER_MS ("bitti"
 *     flash) then move to `history` (session-only, capped);
 *   - error rows are STICKY: they stay in `active` until the user
 *     dismisses or retries them — even after the source list swept them
 *     (usePendingOps drops terminal ops after its RETAIN window).
 */

import { computed, getCurrentScope, onScopeDispose, ref } from 'vue';

export type OperationKind = 'upload' | 'copy' | 'move' | 'delete' | 'convert' | 'archive';
export type OperationStatus = 'running' | 'done' | 'error' | 'aborted';

/** What a publisher hands to `sync()` for one row. */
export interface OperationInput {
  /** Stable id within the group (upload uuid / ops row id). */
  id: string | number;
  kind: OperationKind;
  /** Primary label — file name or target path. '' → renderer falls back to the kind verb. */
  name: string;
  /** 0..100, or null for indeterminate (renderer shows a spinner). */
  percent: number | null;
  status: OperationStatus;
  error?: string | null;
  /** Queue op accepted but not started yet ("Sırada"). */
  queued?: boolean;
  /** Progress counters for queue ops (3/5 items). */
  doneCount?: number;
  totalCount?: number;
  /** Byte progress for uploads. */
  uploadedBytes?: number;
  totalBytes?: number;
  cancellable?: boolean;
  retryable?: boolean;
}

export interface OperationActions {
  cancel?: () => void;
  /** Clean the row out of the OWNING source list (called on retire/dismiss). */
  dismiss?: () => void;
  retry?: () => void;
}

export interface Operation {
  /** `${group}:${id}` — unique across publishers. */
  key: string;
  kind: OperationKind;
  name: string;
  percent: number | null;
  status: OperationStatus;
  error: string | null;
  queued: boolean;
  doneCount: number | null;
  totalCount: number | null;
  uploadedBytes: number | null;
  totalBytes: number | null;
  cancellable: boolean;
  retryable: boolean;
  startedAt: number;
  settledAt: number | null;
}

const DONE_LINGER_MS = 4000;
const HISTORY_CAP = 50;

function toOperation(key: string, input: OperationInput, prev?: Operation): Operation {
  const terminal = input.status !== 'running';
  return {
    key,
    kind: input.kind,
    name: input.name,
    percent: input.percent,
    status: input.status,
    error: input.error ?? null,
    queued: input.queued ?? false,
    doneCount: input.doneCount ?? null,
    totalCount: input.totalCount ?? null,
    uploadedBytes: input.uploadedBytes ?? null,
    totalBytes: input.totalBytes ?? null,
    cancellable: input.cancellable ?? false,
    retryable: input.retryable ?? false,
    startedAt: prev?.startedAt ?? Date.now(),
    settledAt: prev?.settledAt ?? (terminal ? Date.now() : null),
  };
}

export function useOperations() {
  const active = ref<Operation[]>([]);
  const history = ref<Operation[]>([]);

  const actions = new Map<string, OperationActions>();
  /** Keys already moved to history — sync must not resurrect them. */
  const retired = new Set<string>();
  const lingerTimers = new Map<string, ReturnType<typeof setTimeout>>();

  function clearTimer(key: string) {
    const tm = lingerTimers.get(key);
    if (tm !== undefined) {
      clearTimeout(tm);
      lingerTimers.delete(key);
    }
  }

  /** Move a row from active to the session history (newest first). */
  function retire(key: string, opts: { toHistory?: boolean } = {}) {
    clearTimer(key);
    const idx = active.value.findIndex((o) => o.key === key);
    if (idx === -1) return;
    const [op] = active.value.splice(idx, 1);
    active.value = [...active.value];
    retired.add(key);
    if (opts.toHistory !== false) {
      history.value = [{ ...op, settledAt: op.settledAt ?? Date.now() }, ...history.value].slice(
        0,
        HISTORY_CAP,
      );
    }
  }

  function scheduleLinger(key: string) {
    if (lingerTimers.has(key)) return;
    lingerTimers.set(
      key,
      setTimeout(() => {
        lingerTimers.delete(key);
        // Retire + ask the owning source to drop its row so it doesn't
        // resurrect on the next sync (uploads keep done rows forever).
        const acts = actions.get(key);
        retire(key);
        acts?.dismiss?.();
      }, DONE_LINGER_MS),
    );
  }

  /**
   * Full-list reconcile for one publisher group. Upserts every present row,
   * removes vanished ones (sticky errors excepted), and manages the
   * done-linger → history hand-off.
   */
  function sync(
    group: string,
    items: Array<{ input: OperationInput; actions?: OperationActions }>,
  ): void {
    const prefix = `${group}:`;
    const present = new Set<string>();
    let next = [...active.value];
    let changed = false;

    for (const item of items) {
      const key = `${group}:${item.input.id}`;
      present.add(key);
      if (item.actions) actions.set(key, item.actions);
      if (retired.has(key)) continue; // already history — don't resurrect
      const idx = next.findIndex((o) => o.key === key);
      const op = toOperation(key, item.input, idx === -1 ? undefined : next[idx]);
      if (idx === -1) {
        next = [op, ...next];
        changed = true;
      } else if (JSON.stringify(next[idx]) !== JSON.stringify(op)) {
        next[idx] = op;
        changed = true;
      }
      if (op.status === 'done' || op.status === 'aborted') scheduleLinger(key);
    }

    // Vanished from source: running → drop silently (superseded elsewhere),
    // done/aborted → straight to history, error → sticky (keep visible).
    for (const o of [...next]) {
      if (!o.key.startsWith(prefix) || present.has(o.key)) continue;
      if (o.status === 'error') continue;
      clearTimer(o.key);
      next = next.filter((x) => x.key !== o.key);
      changed = true;
      if (o.status !== 'running') {
        retired.add(o.key);
        history.value = [{ ...o, settledAt: o.settledAt ?? Date.now() }, ...history.value].slice(
          0,
          HISTORY_CAP,
        );
      }
    }

    // Bookkeeping: retired keys whose source rows are gone can be forgotten
    // (their ids won't reappear — uuids / monotonic op ids).
    for (const key of [...retired]) {
      if (key.startsWith(prefix) && !present.has(key)) retired.delete(key);
    }
    for (const key of [...actions.keys()]) {
      if (key.startsWith(prefix) && !present.has(key) && !active.value.some((o) => o.key === key)) {
        // keep actions for rows still on screen (sticky errors)
        if (!next.some((o) => o.key === key)) actions.delete(key);
      }
    }

    if (changed) active.value = next;
  }

  function cancel(key: string) {
    actions.get(key)?.cancel?.();
  }

  function retry(key: string) {
    const acts = actions.get(key);
    retire(key); // failed attempt goes to history; retry publishes a fresh row
    acts?.dismiss?.();
    acts?.retry?.();
  }

  function dismiss(key: string) {
    const acts = actions.get(key);
    retire(key);
    acts?.dismiss?.();
  }

  function clearHistory() {
    history.value = [];
  }

  const runningCount = computed(
    () => active.value.filter((o) => o.status === 'running').length,
  );
  const errorCount = computed(() => active.value.filter((o) => o.status === 'error').length);
  const hasError = computed(() => errorCount.value > 0);

  /**
   * Aggregate progress across determinate running rows (uploads weighted by
   * bytes, queue ops by item counts). null → nothing determinate is running
   * (badge ring spins instead of filling).
   */
  const overallPercent = computed<number | null>(() => {
    const running = active.value.filter((o) => o.status === 'running');
    let sum = 0;
    let n = 0;
    for (const o of running) {
      let frac: number | null = null;
      if (o.totalBytes && o.totalBytes > 0) {
        frac = Math.min(1, (o.uploadedBytes ?? 0) / o.totalBytes);
      } else if (o.totalCount && o.totalCount > 0) {
        frac = Math.min(1, (o.doneCount ?? 0) / o.totalCount);
      } else if (o.percent !== null) {
        frac = Math.min(1, o.percent / 100);
      }
      if (frac !== null) {
        sum += frac;
        n += 1;
      }
    }
    if (n === 0) return null;
    return Math.round((sum / n) * 100);
  });

  if (getCurrentScope()) {
    onScopeDispose(() => {
      for (const tm of lingerTimers.values()) clearTimeout(tm);
      lingerTimers.clear();
    });
  }

  return {
    active,
    history,
    runningCount,
    errorCount,
    hasError,
    overallPercent,
    sync,
    cancel,
    retry,
    dismiss,
    clearHistory,
  };
}

export type OperationsStore = ReturnType<typeof useOperations>;
