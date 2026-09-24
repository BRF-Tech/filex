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
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

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

const QUEUE_STATUSES = ['pending', 'running', 'failed', 'done', 'cancelled'] as const;

/** A status in words. ⚠ The filter and the badge printed the raw wire value
 *  ("pending", "running") in every language — a string no language pack, and
 *  not even Turkish, could reach. An unknown status (a newer server) is
 *  shown as sent rather than hidden. */
function statusLabel(s: string): string {
  return (QUEUE_STATUSES as readonly string[]).includes(s) ? t(`queue.status.${s}`) : s;
}

const statusOptions = computed(() => [
  { value: '', label: t('common.all') },
  ...QUEUE_STATUSES.map((s) => ({ value: s, label: statusLabel(s) })),
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
    toast.error(extractError(e, t('errors.actionFailed')));
  }
}

async function cancel(id: string) {
  try {
    await queue.cancel(id);
    toast.success(t('queue.cancelled'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.actionFailed')));
  }
}

function shortID(id: string): string {
  return id.length > 12 ? id.slice(0, 8) + '…' : id;
}

/** The job types the catalogue names (`queue.type.*`). */
const QUEUE_TYPES = [
  'content_index',
  'antivirus_scan',
  'thumb',
  'copy',
  'move',
  'delete',
  'replica_retry',
  'replica_report',
  'reconcile',
] as const;

/** A job's type in words. ⚠ It printed the handler's id — `content_index` —
 *  in the Turkish panel (release-candidate sweep, 2026-09-21). An unknown
 *  type (a newer server, a plugin's) is shown as sent. */
function typeLabel(type: string): string {
  return (QUEUE_TYPES as readonly string[]).includes(type) ? t(`queue.type.${type}`) : type;
}

/**
 * What the job is about. ⚠ The column printed the payload as JSON —
 * `{"node_id":8}` — an id nobody can look up; the server now names the file
 * or storage it points at (`subject`). A payload it cannot name is shown as
 * its values, without the braces and quotes of the wire format.
 */
function aboutOf(row: { subject?: string; payload: Record<string, unknown> }): string {
  if (row.subject) return row.subject;
  const vals = Object.values(row.payload ?? {})
    .filter((v) => v !== null && v !== undefined && typeof v !== 'object')
    .map(String);
  const s = vals.join(' · ');
  return s.length > 80 ? s.slice(0, 77) + '…' : s;
}

function gotoPage(p: number) {
  queue.setPage(p);
  queue.fetchList();
}

type QueueRow = (typeof queue.items)[number];

/* Status sorts by where a job is in its life, not by the alphabet: the ones
 * that need somebody (failed, pending) come before the ones that are over. */
const STATUS_RANK: Record<string, number> = { failed: 0, pending: 1, running: 2, done: 3, cancelled: 4 };

/* The explorer's table (DataTable), remembered on the account under
 * `admin.queue`. ⚠ The list is paged by the SERVER, which has no sort
 * parameter, so while it spans more than one page DataTable closes the
 * headers and says why instead of re-ordering one page. */
const columns = computed<DataColumn<QueueRow>[]>(() => [
  { id: 'id', label: t('queue.fields.id'), sortable: true, width: 120 },
  { id: 'type', label: t('queue.fields.type'), sortable: true, width: 160, sortValue: (r) => typeLabel(r.type) },
  {
    id: 'status',
    label: t('queue.fields.status'),
    sortable: true,
    width: 110,
    sortValue: (r) => STATUS_RANK[r.status] ?? 9,
  },
  {
    id: 'attempts',
    label: t('queue.fields.attempts'),
    sortable: true,
    align: 'right',
    width: 100,
    sortValue: (r) => r.attempts,
  },
  { id: 'payload', label: t('queue.fields.payload'), width: 240 },
  { id: 'last_error', label: t('queue.fields.lastError'), sortable: true, width: 220 },
  {
    id: 'enqueued_at',
    label: t('queue.fields.enqueued'),
    sortable: true,
    sortDir: 'desc',
    width: 160,
    sortValue: (r) => (r.enqueued_at ? Date.parse(r.enqueued_at) : null),
  },
]);

/** The row's verbs, behind its one pinned `Actions` control. Both are
 *  state-bound — a job can be retried only after it failed and cancelled only
 *  while it is still pending — so a running job's control is simply disabled
 *  rather than absent, and the column stays the same width all the way down
 *  instead of going ragged row by row. */
function rowActions(row: QueueRow): ContextAction[] {
  return [
    { key: 'retry', label: t('queue.retry'), icon: 'refresh', hidden: row.status !== 'failed' },
    { key: 'cancel', label: t('queue.cancel'), icon: 'close', hidden: row.status !== 'pending' },
  ];
}

function onRowAction(key: string, row: QueueRow) {
  if (key === 'retry') retry(row.id);
  else if (key === 'cancel') cancel(row.id);
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

    <DataTable
      table-id="admin.queue"
      :columns="columns"
      :rows="queue.items"
      :loading="queue.loading"
      :empty="t('queue.empty')"
      row-key="id"
      :page="queue.currentPage"
      :page-size="queue.limit"
      :total="queue.total"
      :row-actions="(row: QueueRow) => rowActions(row)"
      :row-actions-test-id="(row: QueueRow) => `queue-actions-${row.id}`"
      @row-action="(key: string, row: QueueRow) => onRowAction(key, row)"
      @page="gotoPage"
    >
      <template #toolbar>
        <Select
          :model-value="queue.filter"
          :options="statusOptions"
          :label="t('queue.filter.status')"
          size="sm"
          class="w-48"
          @update:model-value="setStatus"
        />
      </template>

      <template #cell-id="{ row }">
        <span class="tbl-mono">{{ shortID(row.id) }}</span>
      </template>
      <template #cell-status="{ row }">
        <Badge :tone="tone(row.status)">{{ statusLabel(row.status) }}</Badge>
      </template>
      <template #cell-attempts="{ row }"><bdi dir="ltr">{{ row.attempts }} / {{ row.max_attempts }}</bdi></template>
      <template #cell-type="{ row }">
        <span :title="row.type" data-testid="queue-type">{{ typeLabel(row.type) }}</span>
      </template>
      <template #cell-payload="{ row }">
        <span class="tbl-clamp" data-testid="queue-about">{{ aboutOf(row) }}</span>
      </template>
      <template #cell-last_error="{ row }">
        <span class="tbl-clamp text-rose-600 dark:text-rose-400">{{ row.last_error || '' }}</span>
      </template>
      <template #cell-enqueued_at="{ row }">
        <span class="whitespace-nowrap">{{ formatDate(row.enqueued_at, locale) }}</span>
      </template>
    </DataTable>
  </section>
</template>
