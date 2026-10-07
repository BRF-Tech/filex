<script setup lang="ts">
/**
 * A storage's initial copy to its replication target (#186), on its row of the
 * Replication page's pairings: what the storage already held when it was
 * linked, copied in the background from the queue. Counting, then copying
 * with how far it has come, waiting (the target stopped answering; it goes on
 * by itself), or done - and "Run again" to look at every file anew.
 *
 * The numbers are the server's (replica_initial_copies, internal/replica
 * initial.go): copied + already there + left out by a rule + failed = done.
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';

import type { ReplicaInitialCopy } from '@/api/types';
import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import { formatBytes, formatDate, formatNumber } from '@/lib/format';

const props = defineProps<{ copy: ReplicaInitialCopy; busy?: boolean }>();
const emit = defineEmits<{ (e: 'restart'): void }>();

const { t, locale } = useI18n();

const n = (v: number) => formatNumber(v ?? 0, locale.value);

const pct = computed(() => {
  const c = props.copy;
  if (c.phase === 'done') return 100;
  if (!c.counted || !c.total) return 0;
  return Math.min(100, Math.floor((c.done / c.total) * 100));
});

const tone = computed<'emerald' | 'amber' | 'zinc' | 'brand'>(() => {
  switch (props.copy.phase) {
    case 'done':
      return props.copy.failed > 0 ? 'amber' : 'emerald';
    case 'waiting':
      return 'amber';
    case 'pending':
      return 'zinc';
    default:
      return 'brand';
  }
});

const summary = computed(() => {
  const c = props.copy;
  switch (c.phase) {
    case 'pending':
      return t('replica.initial.pending');
    case 'counting':
      return t('replica.initial.counting', { n: n(c.total) });
    case 'copying':
    case 'waiting':
      return t('replica.initial.progress', { done: n(c.done), n: n(c.total) }, c.total);
    default:
      return t('replica.initial.finished', { n: n(c.total) }, c.total);
  }
});

/** The parts of "done", only the ones that are not zero. */
const parts = computed(() => {
  const c = props.copy;
  const out: { key: string; text: string; warn?: boolean }[] = [];
  if (c.copied) out.push({ key: 'copied', text: t('replica.initial.copied', { n: n(c.copied), size: formatBytes(c.copied_bytes ?? 0, locale.value) }) });
  if (c.present) out.push({ key: 'present', text: t('replica.initial.present', { n: n(c.present) }) });
  if (c.excluded) out.push({ key: 'excluded', text: t('replica.initial.excluded', { n: n(c.excluded) }) });
  if (c.failed) out.push({ key: 'failed', text: t('replica.initial.failed', { n: n(c.failed) }), warn: true });
  return out;
});

const finishedAt = computed(() =>
  props.copy.finished_unix ? formatDate(new Date(props.copy.finished_unix * 1000), locale.value) : '',
);
</script>

<template>
  <div class="mt-2 space-y-1.5" :data-testid="`replica-initial-${copy.storage_id}`" :data-phase="copy.phase">
    <div class="flex flex-wrap items-center gap-2">
      <Badge size="xs" :tone="tone" :data-testid="`replica-initial-phase-${copy.storage_id}`">
        {{ t('replica.initial.phase.' + copy.phase) }}
      </Badge>
      <span class="text-xs" :data-testid="`replica-initial-summary-${copy.storage_id}`">{{ summary }}</span>
      <Button
        v-if="copy.phase === 'done' || copy.phase === 'waiting'"
        size="xs"
        variant="ghost"
        class="ms-auto"
        :loading="busy"
        :disabled="busy"
        :data-testid="`replica-initial-restart-${copy.storage_id}`"
        :title="t('replica.initial.restartHint')"
        @click="emit('restart')"
      >
        {{ t('replica.initial.restart') }}
      </Button>
    </div>
    <div
      v-if="copy.phase === 'copying' || copy.phase === 'waiting'"
      class="h-1.5 w-full overflow-hidden rounded-full bg-zinc-100 dark:bg-zinc-800"
      role="progressbar"
      :aria-valuenow="pct"
      aria-valuemin="0"
      aria-valuemax="100"
      :aria-label="t('replica.initial.title')"
    >
      <div class="h-full bg-brand-500" :style="{ width: pct + '%' }"></div>
    </div>
    <p v-if="parts.length" class="flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-zinc-500">
      <span
        v-for="p in parts"
        :key="p.key"
        :class="p.warn ? 'text-rose-600 dark:text-rose-400' : ''"
        :data-testid="`replica-initial-${p.key}-${copy.storage_id}`"
      >{{ p.text }}</span>
    </p>
    <p
      v-if="copy.phase === 'waiting' && copy.last_error"
      class="text-[11px] text-amber-700 dark:text-amber-300 break-words"
      :data-testid="`replica-initial-error-${copy.storage_id}`"
    >
      {{ t('replica.initial.waiting', { error: copy.last_error }) }}
    </p>
    <p v-if="copy.phase === 'done' && finishedAt" class="text-[11px] text-zinc-500">
      {{ t('replica.initial.finishedAt', { when: finishedAt }) }}
    </p>
  </div>
</template>
