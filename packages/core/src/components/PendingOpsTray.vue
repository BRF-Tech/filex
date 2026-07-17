<script setup lang="ts">
/**
 * PendingOpsTray — renderless ops-queue publisher (wiring:c3).
 *
 * Historically a bottom-left toast list for queued copy/move/delete. Since
 * the unified operations center it renders NOTHING: it reconciles the
 * usePendingOps rows into the shared useOperations store; OperationsCenter
 * draws them. Terminal-state lingering ("bitti" flash → history) and sticky
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

function percentOf(op: PendingOp): number | null {
  if (op.status === 'done') return 100;
  if (op.progress_total <= 0) return null;
  return Math.min(100, Math.round((op.progress_done / op.progress_total) * 100));
}

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
