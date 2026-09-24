<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { RefreshCcw } from 'lucide-vue-next';

import { useSyncStore } from '@/stores/sync';
import { useStoragesStore } from '@/stores/storages';
import type { SyncRun } from '@/api/types';
import { formatDate, formatDuration } from '@/lib/format';
import { syncStateLabel, syncTone } from '@/lib/syncTone';

import Button from '@/components/ui/Button.vue';
import Select from '@/components/ui/Select.vue';
import Badge from '@/components/ui/Badge.vue';
import { DataTable, type DataColumn } from '@brftech/filex-core';

const { t, locale } = useI18n();
const sync = useSyncStore();
const storages = useStoragesStore();

const storageId = ref<number | ''>('');
const state = ref<SyncRun['state'] | ''>('');
const page = ref(1);
const pageSize = 50;

async function load() {
  await sync.fetch({
    storage_id: typeof storageId.value === 'number' ? storageId.value : undefined,
    state: state.value || undefined,
    page: page.value,
    page_size: pageSize,
  });
}

watch([storageId, state], () => {
  page.value = 1;
  load();
});

function duration(r: SyncRun): string {
  if (!r.finished_at) return '—';
  return formatDuration(
    (new Date(r.finished_at).getTime() - new Date(r.started_at).getTime()) / 1000,
    locale.value,
  );
}

const storageOptions = computed(() => [
  { value: '', label: t('common.all') },
  ...storages.items.map((s) => ({ value: s.id, label: s.name })),
]);

/** The states a run can be filtered by. */
const RUN_STATES = ['ok', 'error', 'running', 'aborted'] as const;

/** A run's state in words; an unknown one as sent. ⚠ These were raw wire
 *  values AND a plain array built once at setup, so even the "All" label kept
 *  the language the page was opened in when the language changed — and the
 *  table's cells kept printing "ok" after the filter had been translated. */
function stateLabel(s: string): string {
  return syncStateLabel(s, t);
}

const stateOptions = computed(() => [
  { value: '', label: t('common.all') },
  ...RUN_STATES.map((s) => ({ value: s, label: stateLabel(s) })),
]);

function num(n: number | null | undefined): string {
  return typeof n === 'number' && Number.isFinite(n) ? String(n) : '0';
}

/** How long a run took, in ms — what Duration SORTS by ("2m" after "45s",
 *  not before it). Unfinished runs sort last. */
function durationMs(r: SyncRun): number | null {
  if (!r.finished_at) return null;
  return new Date(r.finished_at).getTime() - new Date(r.started_at).getTime();
}

/* The explorer's table (DataTable), remembered on the account under
 * `admin.sync`. ⚠ The runs are paged by the SERVER, which has no sort
 * parameter, so while they span more than one page DataTable closes the
 * headers and says why instead of re-ordering one page of 50. */
const columns = computed<DataColumn<SyncRun>[]>(() => [
  { id: 'storage_name', label: t('sync.fields.storage'), sortable: true, width: 180 },
  {
    id: 'started_at',
    label: t('sync.fields.started'),
    sortable: true,
    sortDir: 'desc',
    width: 170,
    format: (r) => (r.started_at ? formatDate(r.started_at, locale.value) : '—'),
    sortValue: (r) => (r.started_at ? Date.parse(r.started_at) : null),
  },
  {
    id: 'duration',
    label: t('sync.fields.duration'),
    sortable: true,
    width: 110,
    format: duration,
    sortValue: durationMs,
  },
  { id: 'state', label: t('sync.fields.state'), sortable: true, width: 110, sortValue: (r) => stateLabel(r.state) },
  {
    id: 'scanned',
    label: t('sync.fields.scanned'),
    align: 'right',
    sortable: true,
    width: 100,
    format: (r) => num(r.scanned),
    sortValue: (r) => r.scanned ?? 0,
  },
  {
    id: 'added',
    label: '+',
    align: 'right',
    sortable: true,
    width: 70,
    format: (r) => num(r.added),
    sortValue: (r) => r.added ?? 0,
  },
  {
    id: 'updated',
    label: '~',
    align: 'right',
    sortable: true,
    width: 70,
    format: (r) => num(r.updated),
    sortValue: (r) => r.updated ?? 0,
  },
  {
    id: 'deleted',
    label: '-',
    align: 'right',
    sortable: true,
    width: 70,
    format: (r) => num(r.deleted),
    sortValue: (r) => r.deleted ?? 0,
  },
]);

onMounted(async () => {
  await Promise.all([storages.fetch(), load()]);
});
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold">{{ t('sync.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('sync.subtitle') }}</p>
      </div>
      <Button variant="outline" size="sm" @click="load" :loading="sync.loading">
        <RefreshCcw class="h-4 w-4" />
        {{ t('common.refresh') }}
      </Button>
    </div>

    <DataTable
      table-id="admin.sync"
      :columns="columns"
      :rows="sync.items"
      :loading="sync.loading"
      :empty="t('sync.noResults')"
      :page="page"
      :page-size="pageSize"
      :total="sync.runs.total"
      row-key="id"
      @page="(p: number) => ((page = p), load())"
    >
      <template #toolbar>
        <Select
          :model-value="storageId"
          :options="storageOptions"
          size="sm"
          @update:model-value="(v) => (storageId = (v as number | ''))"
        />
        <Select
          :model-value="state"
          :options="stateOptions"
          size="sm"
          @update:model-value="(v) => (state = v as SyncRun['state'] | '')"
        />
      </template>
      <template #cell-state="{ row }">
        <Badge :tone="syncTone((row as SyncRun).state)" size="xs" data-testid="sync-run-state">
          {{ stateLabel((row as SyncRun).state) }}
        </Badge>
      </template>
    </DataTable>
  </div>
</template>
