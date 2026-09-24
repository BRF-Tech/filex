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
import type { OperationKind, OperationsStore, OperationStatus } from '../composables/useOperations';
import { opPercent } from '../lib/opProgress';
import { opFailure } from '../lib/errorWords';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  ops: PendingOp[];
  /** Kept for mount compatibility — strings now render in OperationsCenter. */
  locale: LocaleCode;
  center: OperationsStore;
  /**
   * An app-plugin job's output mode, as the host resolves it (the explorer
   * reads the cached actions list). ⚠ A resolver rather than a field on the
   * row: the ops endpoint does not carry it, so only a caller that holds the
   * actions list can answer — and one that does not simply draws no chip.
   */
  outputModeOf?: (op: PendingOp) => string | undefined;
  /** May the viewer administer this instance? Then a failure's raw words are
   *  shown as a second line (lib/errorWords); nobody else ever sees them. */
  callerAdmin?: boolean;
}>();

const { t } = useLocale(() => props.locale);

/** A failed op, said — lib/errorWords `opFailure`, the same call the
 *  explorer's toast makes. */
function failureOf(op: PendingOp) {
  return opFailure(op, t, { callerAdmin: props.callerAdmin === true });
}

const emit = defineEmits<{
  (e: 'dismiss', id: number): void;
  /** A plugin job's Cancel — the explorer posts `opsCancel`. */
  (e: 'cancel', id: number): void;
  /** "Open" on a finished job: reveal this output path in the listing. */
  (e: 'open', id: number, path: string): void;
}>();

function mapStatus(op: PendingOp): OperationStatus {
  if (op.status === 'done') return 'done';
  if (op.status === 'error') return 'error';
  if (op.status === 'cancelled') return 'aborted';
  return 'running'; // pending | running
}

const DRAWN_KINDS: ReadonlySet<string> = new Set(['copy', 'move', 'delete', 'plugin']);

/** A queue kind the center can draw. Anything it has no glyph for is shown as
 *  a generic job (`plugin`), never folded into delete. */
function mapKind(op: PendingOp): OperationKind {
  if (op.op_type === 'trash-empty') return 'trash';
  return (DRAWN_KINDS.has(op.op_type) ? op.op_type : 'plugin') as OperationKind;
}

function isPluginJob(op: PendingOp): boolean {
  return op.op_type === 'plugin';
}

/** Who may stop a row: a plugin job's starter (the server answers anyone
 *  else 403), and an "empty the trash" — which only an administrator
 *  starts — for an administrator. */
function mayCancel(op: PendingOp): boolean {
  if (op.status !== 'pending' && op.status !== 'running') return false;
  if (isPluginJob(op)) return true;
  return op.op_type === 'trash-empty' && props.callerAdmin === true;
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
          kind: mapKind(op),
          /* A trash empty names no file: its kind is its title. */
          name: op.op_type === 'trash-empty' ? '' : op.label || op.target_path || op.source_dir || '',
          percent: percentOf(op),
          status: mapStatus(op),
          error: op.status === 'error' ? failureOf(op).text : null,
          errorDetail: op.status === 'error' ? (failureOf(op).detail ?? null) : null,
          queued: op.status === 'pending',
          doneCount: op.progress_done,
          totalCount: op.progress_total,
          cancellable: mayCancel(op),
          retryable: false,
          message: op.message ?? null,
          outputs: (op.outputs ?? []).map((o) => o.path),
          outputMode: (isPluginJob(op) ? props.outputModeOf?.(op) : undefined) ?? null,
        },
        actions: {
          dismiss: () => emit('dismiss', op.id),
          cancel: () => emit('cancel', op.id),
          open: (path: string) => emit('open', op.id, path),
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
