<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ListChecks, RefreshCcw, RotateCcw, X, AlertTriangle, CheckCircle2, Clock, Activity } from 'lucide-vue-next';

import { useQueueStore } from '@/stores/queue';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { QueueOpStatus } from '@/api/types';
import { formatDate } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Select from '@/components/ui/Select.vue';
import Badge from '@/components/ui/Badge.vue';
import StatCard from '@/components/ui/StatCard.vue';

const { t, locale } = useI18n();
const queue = useQueueStore();
const toast = useToastStore();

const refreshing = ref(false);

async function load() {
  refreshing.value = true;
  try {
    await queue.refresh();
  } finally {
    refreshing.value = false;
  }
}

onMounted(() => {
  load();
});

const statusOptions = computed(() => [
  { value: '', label: t('common.all') },
  { value: 'pending', label: 'pending' },
  { value: 'running', label: 'running' },
  { value: 'failed', label: 'failed' },
  { value: 'done', label: 'done' },
  { value: 'cancelled', label: 'cancelled' },
]);

function setStatus(v: string | number | null) {
  queue.setStatus((v as QueueOpStatus | '') ?? '');
  queue.fetchList();
}

function tone(s: QueueOpStatus): 'emerald' | 'sky' | 'rose' | 'amber' | 'zinc' {
  switch (s) {
    case 'done':
      return 'emerald';
    case 'running':
      return 'sky';
    case 'failed':
      return 'rose';
    case 'pending':
      return 'amber';
    case 'cancelled':
      return 'zinc';
    default:
      return 'zinc';
  }
}

async function retry(id: string) {
  try {
    await queue.retry(id);
    toast.success(t('queue.retried'));
  } catch (e: unknown) {
    toast.error(extractError(e, 'Retry failed'));
  }
}

async function cancel(id: string) {
  try {
    await queue.cancel(id);
    toast.success(t('queue.cancelled'));
  } catch (e: unknown) {
    toast.error(extractError(e, 'Cancel failed'));
  }
}

function shortID(id: string): string {
  return id.length > 12 ? id.slice(0, 8) + '…' : id;
}

function shortPayload(p: Record<string, unknown>): string {
  if (!p) return '';
  try {
    const s = JSON.stringify(p);
    return s.length > 80 ? s.slice(0, 77) + '…' : s;
  } catch {
    return '';
  }
}

function gotoPage(p: number) {
  queue.setPage(p);
  queue.fetchList();
}
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-2">
        <ListChecks class="h-6 w-6 text-brand-600 dark:text-brand-400" />
        <h1 class="text-xl font-semibold">{{ t('queue.title') }}</h1>
      </div>
      <Button variant="outline" size="sm" @click="load" :loading="refreshing">
        <RefreshCcw class="h-4 w-4" />
        {{ t('common.refresh') }}
      </Button>
    </header>

    <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
      <StatCard :label="t('queue.stats.pending')" :value="queue.stats.pending" :icon="Clock" icon-tone="amber" />
      <StatCard :label="t('queue.stats.running')" :value="queue.stats.running" :icon="Activity" icon-tone="sky" />
      <StatCard :label="t('queue.stats.failed')" :value="queue.stats.failed" :icon="AlertTriangle" icon-tone="rose" />
      <StatCard :label="t('queue.stats.done24h')" :value="queue.stats.done_24h" :icon="CheckCircle2" icon-tone="emerald" />
      <StatCard :label="t('queue.stats.cancelled')" :value="queue.stats.cancelled" :icon="X" icon-tone="zinc" />
    </div>

    <div class="flex items-end gap-3">
      <Select :model-value="queue.filter" :options="statusOptions" :label="t('queue.filter.status')" class="w-48" @update:model-value="setStatus" />
    </div>

    <div class="overflow-x-auto rounded-xl border border-zinc-200 dark:border-zinc-800">
      <table class="w-full text-sm">
        <thead class="bg-zinc-50 text-xs uppercase text-zinc-500 dark:bg-zinc-900 dark:text-zinc-400">
          <tr>
            <th class="px-3 py-2 text-left">{{ t('queue.fields.id') }}</th>
            <th class="px-3 py-2 text-left">{{ t('queue.fields.type') }}</th>
            <th class="px-3 py-2 text-left">{{ t('queue.fields.status') }}</th>
            <th class="px-3 py-2 text-right">{{ t('queue.fields.attempts') }}</th>
            <th class="px-3 py-2 text-left">{{ t('queue.fields.payload') }}</th>
            <th class="px-3 py-2 text-left">{{ t('queue.fields.lastError') }}</th>
            <th class="px-3 py-2 text-left">{{ t('queue.fields.enqueued') }}</th>
            <th class="px-3 py-2 text-right"></th>
          </tr>
        </thead>
        <tbody class="divide-y divide-zinc-200 dark:divide-zinc-800">
          <tr v-for="op in queue.items" :key="op.id" class="bg-white dark:bg-zinc-950">
            <td class="px-3 py-2 font-mono text-xs">{{ shortID(op.id) }}</td>
            <td class="px-3 py-2">{{ op.type }}</td>
            <td class="px-3 py-2"><Badge :tone="tone(op.status)">{{ op.status }}</Badge></td>
            <td class="px-3 py-2 text-right">{{ op.attempts }} / {{ op.max_attempts }}</td>
            <td class="px-3 py-2 text-xs font-mono text-zinc-500 dark:text-zinc-400">{{ shortPayload(op.payload) }}</td>
            <td class="px-3 py-2 text-xs text-rose-600 dark:text-rose-400">{{ op.last_error || '' }}</td>
            <td class="px-3 py-2 whitespace-nowrap text-xs">{{ formatDate(op.enqueued_at, locale) }}</td>
            <td class="px-3 py-2 text-right">
              <div class="flex justify-end gap-1">
                <Button v-if="op.status === 'failed'" size="xs" variant="outline" @click="retry(op.id)">
                  <RotateCcw class="h-3.5 w-3.5" /> {{ t('queue.retry') }}
                </Button>
                <Button v-if="op.status === 'pending'" size="xs" variant="outline" @click="cancel(op.id)">
                  <X class="h-3.5 w-3.5" /> {{ t('queue.cancel') }}
                </Button>
              </div>
            </td>
          </tr>
          <tr v-if="!queue.items.length && !queue.loading">
            <td colspan="8" class="px-3 py-8 text-center text-zinc-500 dark:text-zinc-400">
              {{ t('queue.empty') }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="queue.totalPages > 1" class="flex items-center justify-between text-xs">
      <span>{{ t('common.pageOf', { current: queue.currentPage, total: queue.totalPages }) }}</span>
      <div class="flex gap-2">
        <Button size="xs" variant="outline" :disabled="queue.currentPage <= 1" @click="gotoPage(queue.currentPage - 1)">
          {{ t('common.prev') }}
        </Button>
        <Button size="xs" variant="outline" :disabled="queue.currentPage >= queue.totalPages" @click="gotoPage(queue.currentPage + 1)">
          {{ t('common.next') }}
        </Button>
      </div>
    </div>
  </section>
</template>
