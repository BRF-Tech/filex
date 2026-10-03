<script setup lang="ts">
/**
 * PluginLogPanel - a plugin's log, live: an app plugin's (AppPluginDetail)
 * and a storage plugin's (StoragePluginsTab) alike.
 *
 * ⚠ ONE panel for both kinds, on purpose (issue #104): the storage plugins got
 * a log when the sync started writing to it (an entry the storage could not
 * answer for), and a second copy of the app plugins' panel is how the two
 * would have drifted. The server side is one helper too (backend
 * internal/pluginlog): a line repeated within the last 50 is counted on the
 * line already there instead of written again, and comes back from the poll
 * with the same `id`, a higher `count` and the time of the latest repeat
 * (`last`) - so the panel REPLACES the line it has by id, and never shows the
 * same sentence twice.
 *
 * `fetch(after)` asks for the lines above the cursor; the panel polls every
 * two seconds while `active` and keeps the newest 500.
 */
import { onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';

import type { PluginLogLine, PluginLogPage } from '@/api/plugins';
import { formatDate, formatDateFull } from '@/lib/format';
import Badge from '@/components/ui/Badge.vue';

const props = defineProps<{
  /** The lines above the cursor. */
  fetch: (after: number) => Promise<PluginLogPage>;
  /** Poll while true; the log starts over whenever it turns true again. */
  active: boolean;
  /** The container's test id. */
  testid?: string;
}>();

const { t, locale } = useI18n();

const POLL_MS = 2000;
const CAP = 500;

const lines = ref<PluginLogLine[]>([]);
let next = 0;
let timer: ReturnType<typeof setInterval> | undefined;

function keyOf(line: PluginLogLine): number {
  return typeof line.id === 'number' ? line.id : line.seq;
}

/** The lines above the cursor merged in: a repeat replaces its line (by id)
 *  and moves to the end, where the server put it. */
function merge(have: PluginLogLine[], got: PluginLogLine[]): PluginLogLine[] {
  if (!got.length) return have;
  const fresh = new Set(got.map(keyOf));
  return [...have.filter((l) => !fresh.has(keyOf(l))), ...got].slice(-CAP);
}

async function poll() {
  try {
    const page = await props.fetch(next);
    lines.value = merge(lines.value, page.lines);
    next = page.next;
  } catch {
    /* a missed poll is not an error worth a toast; the next tick retries */
  }
}

function stop() {
  if (timer) {
    clearInterval(timer);
    timer = undefined;
  }
}

function start() {
  stop();
  lines.value = [];
  next = 0;
  void poll();
  timer = setInterval(() => void poll(), POLL_MS);
}

watch(
  () => props.active,
  (on) => (on ? start() : stop()),
  { immediate: true },
);
onBeforeUnmount(stop);

function levelTone(level: string): 'rose' | 'amber' | 'zinc' {
  if (level === 'error') return 'rose';
  if (level === 'warn' || level === 'warning') return 'amber';
  return 'zinc';
}

/** The time a line is shown at: its latest repeat, else when it was written. */
function shownAt(line: PluginLogLine): string {
  return line.last || line.ts;
}

defineExpose({ merge });
</script>

<template>
  <section class="space-y-2">
    <div class="flex items-baseline justify-between">
      <h3 class="text-sm font-semibold">{{ t('pluginLog.title') }}</h3>
      <span class="text-[11px] text-zinc-500">{{ t('pluginLog.live') }}</span>
    </div>
    <div
      class="max-h-64 overflow-auto rounded-lg border border-zinc-200 bg-zinc-50 p-2 font-mono text-[11px] dark:border-zinc-800 dark:bg-zinc-950"
      :data-testid="testid || 'plugin-logs'"
    >
      <p v-if="!lines.length" class="text-zinc-500">{{ t('pluginLog.empty') }}</p>
      <div
        v-for="line in lines"
        :key="keyOf(line)"
        class="flex gap-2 whitespace-pre-wrap break-words"
        data-testid="plugin-log-line"
      >
        <span class="shrink-0 text-zinc-500" :title="formatDateFull(shownAt(line), locale)">{{ formatDate(shownAt(line), locale) }}</span>
        <Badge :tone="levelTone(line.level)" size="xs">{{ line.level }}</Badge>
        <span>{{ line.msg }}</span>
        <span
          v-if="(line.count ?? 1) > 1"
          class="shrink-0 text-zinc-500"
          :title="t('pluginLog.firstAt', { when: formatDateFull(line.ts, locale) })"
          data-testid="plugin-log-repeats"
        >{{ t('pluginLog.repeated', { count: line.count ?? 1 }) }}</span>
      </div>
    </div>
  </section>
</template>
