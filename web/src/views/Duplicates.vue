<script setup lang="ts">
/**
 * Duplicates view (bul:s3) — read-only report of duplicate files across
 * storages. Backed by `GET /api/admin/duplicates` (v0.2 "Bul" contract):
 * files are grouped by identical size + non-empty etag; `total_waste` is
 * (count-1)*size per group, groups sorted by waste desc.
 *
 * Deliberately NO delete action in this wave — the page only surfaces
 * where the bytes go.
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Copy, ChevronDown, ChevronRight, RefreshCcw, HardDrive, Layers } from 'lucide-vue-next';

import { api, extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { useStoragesStore } from '@/stores/storages';
import { formatBytes, formatNumber } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import StatCard from '@/components/ui/StatCard.vue';
import EmptyState from '@/components/ui/EmptyState.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Badge from '@/components/ui/Badge.vue';
import { DataTable, type DataColumn } from '@brftech/filex-core';

interface DupNode {
  id: number;
  storage_id: number;
  path: string;
  name: string;
  size: number;
  etag: string;
}

interface DupGroup {
  key: string;
  size: number;
  count: number;
  total_waste: number;
  nodes: DupNode[];
}

const { t, locale } = useI18n();
const toast = useToastStore();
const storages = useStoragesStore();

const groups = ref<DupGroup[]>([]);
const loading = ref(false);
const loaded = ref(false);
const expanded = ref<Set<string>>(new Set());

async function load() {
  loading.value = true;
  try {
    const { data } = await api.get<{ groups?: DupGroup[] | null }>('/admin/duplicates', {
      params: { limit: 100, min_size: 1 },
    });
    groups.value = Array.isArray(data?.groups) ? data.groups : [];
    loaded.value = true;
    // Auto-open the top group so the page isn't a wall of closed rows.
    if (groups.value.length > 0) expanded.value = new Set([groups.value[0].key]);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

function toggle(key: string) {
  const next = new Set(expanded.value);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  expanded.value = next;
}

function storageName(id: number): string {
  return storages.items.find((s) => s.id === id)?.name ?? `#${id}`;
}

const totalWaste = computed(() => groups.value.reduce((acc, g) => acc + (g.total_waste || 0), 0));
const totalCopies = computed(() =>
  groups.value.reduce((acc, g) => acc + Math.max(0, (g.count || 0) - 1), 0),
);
const hasGroups = computed(() => groups.value.length > 0);

/* The explorer's table (DataTable). Every group's member table shares ONE
 * arrangement (`admin.duplicates.members`): they are the same kind of table,
 * and widening Path in one group and finding it narrow in the next would read
 * as a bug. A group's members are all on screen, so sorting them is honest. */
const nodeColumns = computed<DataColumn<DupNode>[]>(() => [
  { id: 'name', label: t('duplicates.colPath'), sortable: true, width: 300 },
  {
    id: 'storage_id',
    label: t('duplicates.colStorage'),
    sortable: true,
    width: 150,
    sortValue: (n) => storageName(n.storage_id),
  },
  { id: 'size', label: t('duplicates.colSize'), sortable: true, align: 'right', width: 110 },
  { id: 'etag', label: t('duplicates.colEtag'), sortable: true, width: 220 },
]);

onMounted(async () => {
  // Best-effort — storage names are cosmetic; the report renders without them.
  try {
    await storages.fetch();
  } catch {
    /* tolerated */
  }
  await load();
});
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold">{{ t('duplicates.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('duplicates.subtitle') }}</p>
      </div>
      <Button variant="outline" size="sm" :loading="loading" @click="load">
        <RefreshCcw class="h-4 w-4" />
        {{ t('common.refresh') }}
      </Button>
    </header>

    <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
      <StatCard
        :label="t('duplicates.stats.groups')"
        :value="loaded ? formatNumber(groups.length, locale) : '—'"
        :icon="Copy"
        icon-tone="brand"
      />
      <StatCard
        :label="t('duplicates.stats.copies')"
        :value="loaded ? formatNumber(totalCopies, locale) : '—'"
        :icon="Layers"
        icon-tone="amber"
      />
      <StatCard
        :label="t('duplicates.stats.waste')"
        :value="loaded ? formatBytes(totalWaste, locale) : '—'"
        :icon="HardDrive"
        icon-tone="rose"
      />
    </div>

    <p v-if="hasGroups" class="text-sm text-zinc-600 dark:text-zinc-300">
      {{ t('duplicates.summary', { groups: formatNumber(groups.length, locale), waste: formatBytes(totalWaste, locale) }, groups.length) }}
    </p>

    <div v-if="loading && !loaded" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <EmptyState
      v-else-if="loaded && !hasGroups"
      :icon="Copy"
      :title="t('duplicates.emptyTitle')"
      :description="t('duplicates.emptyDescription')"
      size="sm"
    />

    <div v-else-if="hasGroups" class="space-y-2">
      <!-- ⚠ ONE card per group: the summary is the card's own header row and
           the members are the shared table inside it, so an expanded group
           reads as the same table as every other listing in the panel rather
           than as a second, quieter one. -->
      <div v-for="g in groups" :key="g.key" class="card overflow-hidden">
        <button
          type="button"
          class="tbl-bar w-full text-start"
          :class="!expanded.has(g.key) && 'border-b-0'"
          :aria-expanded="expanded.has(g.key)"
          @click="toggle(g.key)"
        >
          <component :is="expanded.has(g.key) ? ChevronDown : ChevronRight" class="h-4 w-4 shrink-0" />
          <span class="truncate font-medium flex-1">
            {{ g.nodes[0]?.name ?? g.key }}
          </span>
          <Badge tone="zinc" size="xs">{{ t('duplicates.copies', { n: formatNumber(g.count, locale) }, g.count) }}</Badge>
          <span class="text-xs tabular-nums whitespace-nowrap">{{ formatBytes(g.size, locale) }}</span>
          <span class="text-xs tabular-nums whitespace-nowrap text-rose-600 dark:text-rose-400 font-medium">
            {{ t('duplicates.wasted', { size: formatBytes(g.total_waste, locale) }) }}
          </span>
        </button>

        <DataTable
          v-if="expanded.has(g.key)"
          table-id="admin.duplicates.members"
          :columns="nodeColumns"
          :rows="g.nodes"
          row-key="id"
          class="fx-dup-table"
        >
          <template #cell-name="{ row }">
            <span class="font-medium">{{ row.name }}</span>
            <span class="tbl-sub tbl-mono tbl-clamp" :title="row.path">{{ row.path }}</span>
          </template>
          <template #cell-storage_id="{ row }">
            <span class="whitespace-nowrap">{{ storageName(row.storage_id) }}</span>
          </template>
          <template #cell-size="{ row }">
            <span class="tabular-nums whitespace-nowrap">{{ formatBytes(row.size, locale) }}</span>
          </template>
          <template #cell-etag="{ row }">
            <span class="tbl-mono tbl-clamp" :title="row.etag">{{ row.etag || '—' }}</span>
          </template>
        </DataTable>
      </div>
    </div>
  </section>
</template>

<style scoped>
/* The shared table brings its own card. Inside a group's card that is a second
   border and a second radius drawn a pixel inside the first — so the table
   here is the card's BODY and gives both up. Nothing else about it changes:
   the header, the rows, the gutters and the palette are the panel's.
   ⚠ `:global` and two classes: DataTable has two roots (the table and its
   teleported column menu), so Vue does not stamp this component's scope
   attribute on it and a plain scoped `.fx-dup-table` would match nothing;
   `.fe-table.fx-dup-table` out-weighs the core's one-class
   `.fe-table--framed` whichever stylesheet loads last. */
:global(.fe-table.fx-dup-table) {
  border: 0;
  border-radius: 0;
}
</style>
