<script setup lang="ts">
/**
 * PendingOpsTray — bottom-left toast list for queued copy/move/delete.
 *
 * Sits next to UploadProgress visually (offset to its left when both
 * are open). Each row shows op type + a progress bar driven by
 * `progress_done / progress_total`. Terminal rows linger for a moment
 * (RETAIN_MS in usePendingOps) so the final state is visible before
 * they fade out.
 */
import { computed } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { PendingOp } from '../composables/usePendingOps';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  ops: PendingOp[];
  locale: LocaleCode;
}>();

const emit = defineEmits<{
  (e: 'dismiss', id: number): void;
}>();

const { t } = useLocale(() => props.locale);

const sorted = computed(() => [...props.ops].sort((a, b) => b.id - a.id));

function percent(op: PendingOp): number {
  if (op.status === 'done') return 100;
  if (op.progress_total <= 0) return 0;
  return Math.min(100, Math.round((op.progress_done / op.progress_total) * 100));
}

function statusClass(op: PendingOp): string {
  if (op.status === 'done') return 'is-done';
  if (op.status === 'error') return 'is-error';
  return '';
}

function summary(op: PendingOp): string {
  const verb =
    op.op_type === 'copy'
      ? t('ops.copy')
      : op.op_type === 'move'
        ? t('ops.move')
        : t('ops.delete');
  if (op.status === 'pending') return `${verb} (${t('ops.queued')})`;
  if (op.status === 'running') return `${verb} ${op.progress_done}/${op.progress_total}`;
  if (op.status === 'done') return `${verb} ${t('ops.done')} (${op.progress_total})`;
  if (op.status === 'error') return `${verb} ${t('ops.error')}`;
  return verb;
}
</script>

<template>
  <transition-group
    v-if="sorted.length > 0"
    name="fe-ops-slide"
    tag="div"
    class="fe-ops"
    role="status"
    aria-live="polite"
  >
    <div
      v-for="op in sorted"
      :key="op.id"
      class="fe-ops__item"
      :class="statusClass(op)"
    >
      <div class="fe-ops__row">
        <span class="fe-ops__title" :title="op.target_path || op.source_dir || ''">
          {{ summary(op) }}
        </span>
        <button
          v-if="op.status === 'done' || op.status === 'error'"
          type="button"
          class="fe-ops__dismiss"
          aria-label="Dismiss"
          @click="emit('dismiss', op.id)"
        >×</button>
      </div>
      <div class="fe-ops__bar">
        <div class="fe-ops__bar-fill" :style="{ width: percent(op) + '%' }" />
      </div>
      <div v-if="op.status === 'error' && op.error_message" class="fe-ops__error">
        {{ op.error_message }}
      </div>
    </div>
  </transition-group>
</template>
