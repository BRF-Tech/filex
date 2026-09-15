<script setup lang="ts">
/**
 * PendingOpsTray — renderless ops-queue publisher (wiring:c3).
 *
 * Historically a bottom-left toast list for queued copy/move/delete. Since
 * the unified operations center it renders NOTHING: it reconciles the
 * usePendingOps rows into the shared useOperations store; OperationsCenter
 * draws them. Terminal-state lingering ("done" flash → history) and sticky
 * errors are handled by the store — note usePendingOps sweeps terminal rows
 * after its RETAIN window, which the store treats as "move to history"
 * (errors stay pinned until the user dismisses them).
 *
 * Queue ops are not client-retryable: the normalized PendingOp no longer
 * carries the source list, so a failed op only offers dismiss.
 */
import { watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { PendingOp } from '../composables/usePendingOps';
import type { OperationsStore, OperationStatus } from '../composables/useOperations';
import { opPercent } from '../lib/opProgress';

const props = defineProps<{
  ops: PendingOp[];
  /** Kept for mount compatibility — strings now render in OperationsCenter. */
  locale: LocaleCode;
  center: OperationsStore;
}>();

const emit = defineEmits<{
  (e: 'dismiss', id: number): void;
}>();

function mapStatus(op: PendingOp): OperationStatus {
  if (op.status === 'done') return 'done';
  if (op.status === 'error') return 'error';
  return 'running'; // pending | running
}

/* issue #27 — bytes when a cross-storage transfer reports them, no fake 0%
 * otherwise; the rule is shared with the admin tray (lib/opProgress). */
const percentOf = (op: PendingOp): number | null => opPercent(op);

watch(
  () => props.ops,
  (ops) => {
    props.center.sync(
      'ops',
      ops.map((op) => ({
        input: {
          id: op.id,
          kind: op.op_type,
          name: op.target_path || op.source_dir || '',
          percent: percentOf(op),
          status: mapStatus(op),
          error: op.error_message,
          queued: op.status === 'pending',
          doneCount: op.progress_done,
          totalCount: op.progress_total,
          cancellable: false,
          retryable: false,
        },
        actions: {
          dismiss: () => emit('dismiss', op.id),
        },
      })),
    );
  },
  { immediate: true },
);
</script>

<template>
  <i v-if="false" />
</template>
