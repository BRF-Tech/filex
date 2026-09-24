<script setup lang="ts">
/**
 * PendingOpsTray — bottom-right consolidated progress tracker.
 *
 * Mounted at the AdminLayout level (visible everywhere in the SPA). One
 * card per op:
 *   [copy] 12 / 50 files · 24%   [×]
 *
 * Done ops fade out after 3s; failed ops show red + a Retry button and
 * stick until dismissed. The store handles all the polling logic — this
 * component is purely presentational.
 *
 * Initial mount kicks off polling; if the backend doesn't expose a list
 * endpoint the tray simply stays hidden.
 */
import { computed, onBeforeUnmount, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { Copy, Move, Trash2, RotateCcw, X, AlertTriangle, Check } from 'lucide-vue-next';

import { opPercent } from '@brftech/filex-core';
import { usePendingOpsStore } from '@/stores/pendingOps';
import { formatBytes } from '@/lib/format';
import type { PendingOp } from '@/api/ops';
import Button from '@/components/ui/Button.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t, locale } = useI18n();
const store = usePendingOpsStore();

onMounted(() => {
  store.start();
});

onBeforeUnmount(() => {
  store.stop();
});

const visibleItems = computed(() => store.items.slice().reverse());

function iconFor(opType: string) {
  if (opType === 'move') return Move;
  if (opType === 'delete' || opType === 'trash-empty') return Trash2;
  return Copy;
}

function verbFor(opType: string): string {
  switch (opType) {
    case 'move':
      return t('pendingOps.verb.move');
    case 'delete':
      return t('pendingOps.verb.delete');
    case 'trash-empty':
      return t('pendingOps.verb.trash');
    case 'copy':
    default:
      return t('pendingOps.verb.copy');
  }
}

/* issue #27 — the shared rule (core lib/opProgress): bytes when a transfer
 * reports them, `null` (indeterminate bar, no percentage) when there is no
 * honest number, per-source otherwise. */
function percentFor(op: PendingOp): number | null {
  return opPercent(op);
}

function progressLine(op: PendingOp): string {
  const bytesDone = op.bytes_done ?? 0;
  const bytesTotal = op.bytes_total ?? 0;
  if (bytesTotal > 0) {
    return t('pendingOps.progressBytes', {
      done: formatBytes(bytesDone, locale.value),
      total: formatBytes(bytesTotal, locale.value),
      percent: percentFor(op) ?? 0,
    });
  }
  if (bytesDone > 0) {
    return t('pendingOps.progressBytesOpen', { done: formatBytes(bytesDone, locale.value) });
  }
  const percent = percentFor(op);
  if (percent === null) return t('pendingOps.working');
  return t('pendingOps.progress', { done: op.progress_done, total: op.progress_total, percent });
}

function isTerminal(status: string): boolean {
  return status === 'done' || status === 'error';
}
</script>

<template>
  <div
    v-if="store.visible"
    class="pointer-events-none fixed inset-x-0 bottom-0 z-40 flex justify-end p-4 sm:p-6"
    aria-live="polite"
  >
    <div class="pointer-events-auto flex w-full max-w-sm flex-col gap-2">
      <transition-group
        name="tray"
        tag="div"
        class="flex flex-col gap-2"
      >
        <div
          v-for="item in visibleItems"
          :key="item.op.id"
          class="rounded-lg border bg-white shadow-lg dark:bg-zinc-900"
          :class="
            item.op.status === 'error'
              ? 'border-rose-300 dark:border-rose-700'
              : item.op.status === 'done'
                ? 'border-emerald-300 dark:border-emerald-700'
                : 'border-zinc-200 dark:border-zinc-700'
          "
        >
          <div class="flex items-start gap-3 px-3 py-2">
            <span
              class="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-full"
              :class="
                item.op.status === 'error'
                  ? 'bg-rose-100 text-rose-600 dark:bg-rose-900/40 dark:text-rose-300'
                  : item.op.status === 'done'
                    ? 'bg-emerald-100 text-emerald-600 dark:bg-emerald-900/40 dark:text-emerald-300'
                    : 'bg-brand-100 text-brand-600 dark:bg-brand-900/40 dark:text-brand-300'
              "
            >
              <AlertTriangle v-if="item.op.status === 'error'" class="h-4 w-4" />
              <Check v-else-if="item.op.status === 'done'" class="h-4 w-4" />
              <component v-else :is="iconFor(item.op.op_type)" class="h-4 w-4" />
            </span>

            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="text-sm font-medium text-zinc-900 dark:text-zinc-100 truncate">
                  {{ verbFor(item.op.op_type) }}
                </span>
                <Spinner
                  v-if="!isTerminal(item.op.status)"
                  size="xs"
                  class="text-brand-500"
                  :label="t('common.loading')"
                />
              </div>
              <p class="mt-0.5 text-xs text-zinc-600 dark:text-zinc-400">
                <template v-if="item.op.status === 'error'">
                  {{ item.op.error_message || t('pendingOps.failed') }}
                </template>
                <template v-else>
                  {{ progressLine(item.op) }}
                </template>
              </p>
              <div
                v-if="!isTerminal(item.op.status)"
                class="mt-1.5 h-1 w-full overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800"
                aria-hidden="true"
              >
                <span
                  v-if="percentFor(item.op) !== null"
                  class="block h-full bg-brand-500 transition-all duration-200"
                  :style="{ width: `${percentFor(item.op)}%` }"
                />
                <span
                  v-else
                  class="fx-tray-indeterminate block h-full w-1/3 bg-brand-500"
                  data-testid="tray-indeterminate"
                />
              </div>
            </div>

            <div class="flex shrink-0 items-center gap-1">
              <button
                v-if="item.op.status === 'error'"
                type="button"
                class="rounded p-1 text-zinc-500 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
                :title="t('pendingOps.retry')"
                :aria-label="t('pendingOps.retry')"
                @click="store.retry(item.op.id)"
              >
                <RotateCcw class="h-3.5 w-3.5" />
              </button>
              <button
                type="button"
                class="rounded p-1 text-zinc-500 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
                :title="
                  isTerminal(item.op.status) ? t('pendingOps.dismiss') : t('pendingOps.hide')
                "
                :aria-label="
                  isTerminal(item.op.status) ? t('pendingOps.dismiss') : t('pendingOps.hide')
                "
                @click="
                  isTerminal(item.op.status) ? store.dismiss(item.op.id) : store.cancel(item.op.id)
                "
              >
                <X class="h-3.5 w-3.5" />
              </button>
            </div>
          </div>
        </div>
      </transition-group>
    </div>
  </div>
</template>

<style scoped>
/* issue #27 — no honest percentage: a sliding segment, not a bar frozen at 0%. */
.fx-tray-indeterminate {
  animation: fx-tray-slide 1.2s ease-in-out infinite;
}
@keyframes fx-tray-slide {
  /* ⚠ RTL: the sweep runs the way the line reads (--filex-dir-x, core base.css). */
  0% { transform: translateX(calc(-100% * var(--filex-dir-x, 1))); }
  100% { transform: translateX(calc(300% * var(--filex-dir-x, 1))); }
}
@media (prefers-reduced-motion: reduce) {
  .fx-tray-indeterminate { animation: none; width: 100%; opacity: 0.45; }
}
.tray-enter-active,
.tray-leave-active {
  transition: all 200ms ease;
}
.tray-enter-from,
.tray-leave-to {
  opacity: 0;
  transform: translateY(8px);
}
</style>
