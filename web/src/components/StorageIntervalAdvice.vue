<script setup lang="ts">
/**
 * What a scan interval would cost (#353), under the interval field of both
 * storage forms. The SERVER judges it (internal/syncadvice): this asks while
 * the person types - after a short pause, the previous question cancelled -
 * and words the answer. No threshold and no formula lives here.
 *
 * The person may still save any interval; a suggestion is one click away
 * (`apply`, in minutes, the field's unit).
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';

import { StoragesApi } from '@/api/storages';
import type { IntervalAdvice } from '@/api/types';
import { formatBytes, formatDuration, formatInterval, formatNumber } from '@/lib/format';
import { secondsFromMinutes } from '@/lib/syncInterval';
import Button from './ui/Button.vue';

const props = withDefaults(
  defineProps<{
    /** The saved storage; null on the new-storage form (nothing measured). */
    storageId: number | null;
    /** The driver, for the new-storage form's default mode. */
    driver?: string;
    /** The mode the form shows ('' = the storage's / the driver's default). */
    mode: string;
    /** The interval the form shows, in minutes ('' = the server default). */
    intervalMin: number | '';
  }>(),
  { driver: '' },
);

const emit = defineEmits<{ (e: 'apply', minutes: number): void }>();

const { t, locale } = useI18n();

/** How long typing must pause before the server is asked. */
const ASK_AFTER_MS = 400;

const advice = ref<IntervalAdvice | null>(null);
let timer: ReturnType<typeof setTimeout> | undefined;
let inFlight: AbortController | undefined;

async function ask() {
  inFlight?.abort();
  const mine = new AbortController();
  inFlight = mine;
  try {
    const got = await StoragesApi.intervalAdvice(
      props.storageId,
      {
        interval_s: secondsFromMinutes(props.intervalMin),
        ...(props.mode ? { mode: props.mode } : {}),
        ...(!props.storageId && props.driver ? { driver: props.driver } : {}),
      },
      mine.signal,
    );
    if (mine.signal.aborted) return;
    advice.value = got && typeof got === 'object' && !Array.isArray(got) && 'state' in got ? got : null;
  } catch {
    // A cancelled question, or a server that cannot answer (before 0.61):
    // no advice is better than a stale one.
    if (!mine.signal.aborted) advice.value = null;
  }
}

function askSoon() {
  if (timer) clearTimeout(timer);
  timer = setTimeout(() => void ask(), ASK_AFTER_MS);
}

onMounted(() => void ask());
watch(() => [props.storageId, props.driver, props.mode, props.intervalMin], askSoon);
onBeforeUnmount(() => {
  if (timer) clearTimeout(timer);
  inFlight?.abort();
});

const vars = computed(() => {
  const a = advice.value;
  if (!a) return {};
  return {
    interval: formatInterval(a.interval_s, locale.value),
    pct: formatNumber(a.busy_percent, locale.value),
    target: formatNumber(a.target_busy_percent, locale.value),
    suggested: a.suggested_interval_s ? formatInterval(a.suggested_interval_s, locale.value) : '',
    duration: a.last_scan ? formatDuration(a.last_scan.duration_s, locale.value) : '',
    entries: a.last_scan ? formatNumber(a.last_scan.entries, locale.value) : '',
  };
});

/** The sentence for the verdict; '' for none. */
const line = computed(() => {
  const a = advice.value;
  if (!a) return '';
  switch (a.state) {
    case 'unmeasured':
      return t('storages.advice.unmeasured');
    case 'ok':
      return t('storages.advice.ok', vars.value);
    case 'busy':
      return t('storages.advice.busy', vars.value);
    case 'continuous':
      return t('storages.advice.continuous', vars.value);
    case 'lazy':
      return t('storages.advice.lazy');
    case 'unused':
      return t('storages.advice.unused');
    default:
      return '';
  }
});

const warns = computed(() => advice.value?.state === 'busy' || advice.value?.state === 'continuous');
const suggestedMin = computed(() =>
  warns.value && advice.value?.suggested_interval_s ? Math.round(advice.value.suggested_interval_s / 60) : 0,
);

/** The out-of-memory warning, whatever the verdict on the interval. */
const memoryLine = computed(() => {
  const m = advice.value?.memory;
  if (!m?.high) return '';
  const v = {
    peak: formatBytes(m.peak_bytes, locale.value, 1024),
    limit: formatBytes(m.limit_bytes, locale.value, 1024),
    pct: formatNumber(m.percent, locale.value),
  };
  return m.source === 'container' ? t('storages.advice.memoryContainer', v) : t('storages.advice.memoryHost', v);
});
</script>

<template>
  <div
    v-if="line || memoryLine"
    class="-mt-1 space-y-1.5 text-xs"
    data-testid="storage-interval-advice"
    :data-state="advice?.state"
    aria-live="polite"
  >
    <p
      v-if="line"
      :class="warns ? 'text-amber-600 dark:text-amber-500' : 'text-zinc-500 dark:text-zinc-400'"
      data-testid="storage-interval-advice-line"
    >
      {{ line }}
    </p>
    <Button
      v-if="suggestedMin"
      type="button"
      variant="outline"
      size="xs"
      data-testid="storage-interval-advice-apply"
      @click="emit('apply', suggestedMin)"
    >
      {{ t('storages.advice.apply', { interval: vars.suggested }) }}
    </Button>
    <p
      v-if="memoryLine"
      class="text-rose-600 dark:text-rose-400"
      data-testid="storage-interval-advice-memory"
    >
      {{ memoryLine }}
    </p>
  </div>
</template>
