<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { RefreshCcw } from 'lucide-vue-next';

import { useSyncStore } from '@/stores/sync';
import { useStoragesStore } from '@/stores/storages';
import type { SyncRun } from '@/api/types';
import { formatDate, formatDuration } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Select from '@/components/ui/Select.vue';
import Badge from '@/components/ui/Badge.vue';
import Table, { type Column } from '@/components/ui/Table.vue';

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

const stateTone = (s: SyncRun['state']) => {
  switch (s) {
    case 'ok':
      return 'emerald';
    case 'error':
      return 'rose';
    case 'running':
      return 'sky';
    case 'aborted':
      return 'amber';
    default:
      return 'zinc';
  }
};

function duration(r: SyncRun): string {
  if (!r.finished_at) return '—';
  return formatDuration(
    (new Date(r.finished_at).getTime() - new Date(r.started_at).getTime()) / 1000,
  );
}

const storageOptions = computed(() => [
  { value: '', label: t('common.all') },
  ...storages.items.map((s) => ({ value: s.id, label: s.name })),
]);

const stateOptions = [
  { value: '', label: t('common.all') },
  { value: 'ok', label: 'ok' },
  { value: 'error', label: 'error' },
  { value: 'running', label: 'running' },
  { value: 'aborted', label: 'aborted' },
];

function num(n: number | null | undefined): string {
  return typeof n === 'number' && Number.isFinite(n) ? String(n) : '0';
}

const columns = computed<Column<SyncRun>[]>(() => [
  { key: 'storage_name', label: t('sync.fields.storage') },
  {
    key: 'started_at',
    label: t('sync.fields.started'),
    format: (r) => (r.started_at ? formatDate(r.started_at, locale.value) : '—'),
  },
  { key: 'duration', label: t('sync.fields.duration'), format: duration },
  { key: 'state', label: t('sync.fields.state'), cell: 'slot' },
  { key: 'scanned', label: t('sync.fields.scanned'), align: 'right', format: (r) => num(r.scanned) },
  { key: 'added', label: '+', align: 'right', format: (r) => num(r.added) },
  { key: 'updated', label: '~', align: 'right', format: (r) => num(r.updated) },
  { key: 'deleted', label: '-', align: 'right', format: (r) => num(r.deleted) },
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

    <Table
      :columns="columns"
      :rows="sync.runs.items"
      :loading="sync.loading"
      :empty="t('sync.noResults')"
      :page="page"
      :page-size="pageSize"
      :total="sync.runs.total"
      row-key="id"
      @page="(p) => ((page = p), load())"
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
        <Badge :tone="stateTone((row as SyncRun).state)" size="xs">
          {{ (row as SyncRun).state }}
        </Badge>
      </template>
    </Table>
  </div>
</template>
