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
      {{ t('duplicates.summary', { groups: formatNumber(groups.length, locale), waste: formatBytes(totalWaste, locale) }) }}
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
      <div
        v-for="g in groups"
        :key="g.key"
        class="rounded-lg border border-zinc-200 dark:border-zinc-800 overflow-hidden"
      >
        <button
          type="button"
          class="w-full flex items-center gap-3 px-4 py-3 text-left text-sm hover:bg-zinc-50 dark:hover:bg-zinc-900/50"
          :aria-expanded="expanded.has(g.key)"
          @click="toggle(g.key)"
        >
          <component :is="expanded.has(g.key) ? ChevronDown : ChevronRight" class="h-4 w-4 shrink-0 text-zinc-400" />
          <span class="truncate font-medium flex-1">
            {{ g.nodes[0]?.name ?? g.key }}
          </span>
          <Badge tone="zinc" size="xs">{{ t('duplicates.copies', { n: formatNumber(g.count, locale) }) }}</Badge>
          <span class="text-xs tabular-nums text-zinc-500 whitespace-nowrap">{{ formatBytes(g.size, locale) }}</span>
          <span class="text-xs tabular-nums whitespace-nowrap text-rose-600 dark:text-rose-400 font-medium">
            {{ t('duplicates.wasted', { size: formatBytes(g.total_waste, locale) }) }}
          </span>
        </button>

        <div v-if="expanded.has(g.key)" class="border-t border-zinc-200 dark:border-zinc-800 overflow-x-auto">
          <table class="w-full text-sm">
            <thead class="bg-zinc-50 dark:bg-zinc-900 text-left text-xs text-zinc-500">
              <tr>
                <th class="px-4 py-2 font-medium">{{ t('duplicates.colPath') }}</th>
                <th class="px-4 py-2 font-medium">{{ t('duplicates.colStorage') }}</th>
                <th class="px-4 py-2 font-medium">{{ t('duplicates.colSize') }}</th>
                <th class="px-4 py-2 font-medium">{{ t('duplicates.colEtag') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="n in g.nodes"
                :key="n.id"
                class="border-t border-zinc-100 dark:border-zinc-800"
              >
                <td class="px-4 py-2">
                  <div class="font-medium">{{ n.name }}</div>
                  <div class="text-xs font-mono text-zinc-500 truncate max-w-[480px]" :title="n.path">{{ n.path }}</div>
                </td>
                <td class="px-4 py-2 text-zinc-600 dark:text-zinc-400 whitespace-nowrap">{{ storageName(n.storage_id) }}</td>
                <td class="px-4 py-2 tabular-nums whitespace-nowrap">{{ formatBytes(n.size, locale) }}</td>
                <td class="px-4 py-2 font-mono text-xs text-zinc-500 truncate max-w-[160px]" :title="n.etag">{{ n.etag || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </section>
</template>
