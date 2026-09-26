<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Search, RefreshCcw, Database } from 'lucide-vue-next';

import { SearchApi, type SearchHitEx, type SearchIndexStats, type SearchScope } from '@/api/search';
import type { PaginatedResponse } from '@/api/types';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { formatBytes, formatDate, formatNumber, formatRelative } from '@/lib/format';
/* bul:s3 — snippet «» -> <mark> via TEXT segments (never v-html). The parser
 * is the contract's, not this view's: it was a byte-for-byte copy of
 * lib/snippet.ts, and web already depends on the package that owns it. */
import { snippetSegments } from '@brftech/filex-core';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import StatCard from '@/components/ui/StatCard.vue';
import Badge from '@/components/ui/Badge.vue';
import { DataTable, type DataColumn } from '@brftech/filex-core';

const { t, locale } = useI18n();
const toast = useToastStore();

const q = ref('');
/* bul:s3 — search scope (name | content | all). */
const scope = ref<SearchScope>('all');
const results = ref<PaginatedResponse<SearchHitEx>>({
  items: [],
  total: 0,
  page: 1,
  page_size: 25,
});
const stats = ref<SearchIndexStats | null>(null);
const searching = ref(false);
const rebuilding = ref(false);

async function loadStats() {
  try {
    stats.value = await SearchApi.stats();
  } catch {
    /* tolerated */
  }
}

async function runSearch() {
  if (!q.value.trim()) {
    results.value = { items: [], total: 0, page: 1, page_size: 25 };
    return;
  }
  searching.value = true;
  try {
    results.value = await SearchApi.query({ q: q.value, page: 1, page_size: 25, scope: scope.value });
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    searching.value = false;
  }
}

/* ⚠ The rebuild runs in the background, and this page read its state once:
 * "Rebuild started", then a Running badge that never went away and no word of
 * the end. A second press answered 409 with the server's English ("rebuild
 * already in progress"). The page now looks again while a rebuild runs — the
 * button stays busy — and says when the new index is live. */
const REBUILD_POLL_MS = 2000;
/** A rebuild is running and this page is following it. */
const followingRebuild = ref(false);
let rebuildPoll: ReturnType<typeof setTimeout> | undefined;
let alive = true;

async function rebuild() {
  if (rebuilding.value || followingRebuild.value) return;
  rebuilding.value = true;
  try {
    await SearchApi.rebuild();
    toast.success(t('search.rebuildStarted'));
    await followRebuild(true);
  } catch (e: unknown) {
    if ((e as { response?: { status?: number } })?.response?.status === 409) {
      toast.info(t('search.rebuildAlreadyRunning'));
      await followRebuild(true);
      return;
    }
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    rebuilding.value = false;
  }
}

/** Reads the stats; while a rebuild runs, reads them again every two seconds.
 *  Its end is said when this page watched it run, or asked for it (`asked`). */
async function followRebuild(asked: boolean) {
  await loadStats();
  if (stats.value?.rebuilding) {
    followingRebuild.value = true;
    if (alive) rebuildPoll = setTimeout(() => void followRebuild(false), REBUILD_POLL_MS);
    return;
  }
  if (followingRebuild.value || asked) toast.success(t('search.rebuildDone'));
  followingRebuild.value = false;
}


const scopeOptions = computed(() => [
  { value: 'all', label: t('search.scopeAll') },
  { value: 'name', label: t('search.scopeName') },
  { value: 'content', label: t('search.scopeContent') },
]);

// A rebuild started in another tab, or before this page was opened, is
// followed as well.
onMounted(() => followRebuild(false));

onBeforeUnmount(() => {
  // Leaving stops the watching, never the rebuild.
  alive = false;
  if (rebuildPoll !== undefined) clearTimeout(rebuildPoll);
});

/* ⚠ The results were a `<ul class="card divide-y divide-zinc-200 …">`: one
   line for the name, one for the path, one for the snippet and one for the
   metadata, which is four columns wearing a list's clothes — and two frozen
   zinc hexes the palette cannot move. The explorer's table now (DataTable,
   remembered under `admin.search`), with the file name frozen on the left so
   a sideways scroll still says which hit each row is. The snippet keeps its
   «»-to-<mark> TEXT segments; nothing here renders HTML from the index.

   ⚠ Sorting: the rows are the server's top 25, in RANK order. `:total` is
   handed to the table (with no page size, so no pager is drawn) precisely so
   that while there are more hits than rows on screen the headers close and
   say why — sorting 25 of 300 by size would show the "smallest" of an
   arbitrary 25. When every hit is on screen, sorting them is honest, and
   Score (descending) puts the ranking back. */
const columns = computed<DataColumn<SearchHitEx>[]>(() => [
  { id: 'filename', label: t('explore.cols.name'), sortable: true, width: 220 },
  { id: 'path', label: t('common.path'), sortable: true, width: 240 },
  { id: 'snippet', label: t('common.match'), width: 260 },
  { id: 'storage_name', label: t('common.storage'), sortable: true, width: 130 },
  {
    id: 'mime',
    label: t('explore.cols.mime'),
    sortable: true,
    width: 150,
    format: (h) => h.mime || '\u2014',
    sortValue: (h) => h.mime || null,
  },
  {
    id: 'size',
    label: t('explore.cols.size'),
    align: 'right',
    sortable: true,
    width: 100,
    format: (h) => formatBytes(h.size, locale.value),
    sortValue: (h) => h.size,
  },
  {
    id: 'modified_at',
    label: t('explore.cols.modified'),
    sortable: true,
    sortDir: 'desc',
    width: 160,
    format: (h) => formatDate(h.modified_at, locale.value),
    sortValue: (h) => (h.modified_at ? Date.parse(h.modified_at) : null),
  },
  {
    id: 'score',
    label: t('common.score'),
    align: 'right',
    sortable: true,
    sortDir: 'desc',
    width: 90,
    format: (h) => h.score.toFixed(3),
    sortValue: (h) => h.score,
  },
]);
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold">{{ t('search.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('search.subtitle') }}</p>
      </div>
      <Button variant="outline" size="sm" :loading="rebuilding || followingRebuild" :disabled="followingRebuild" @click="rebuild">
        <RefreshCcw class="h-4 w-4" />
        {{ t('search.rebuild') }}
      </Button>
    </div>

    <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
      <!-- ⚠ FILES, the way the Panel counts them — the bare document count
           includes every folder, so the two pages disagreed about the same
           index ("İNDEKSLENMİŞ DOSYA 28" on the Panel, "İNDEKSLENMİŞ
           DÖKÜMAN 37" here: 28 files and 9 folders; release-candidate sweep,
           2026-09-21). The folders are said, not hidden. -->
      <StatCard
        :label="t('search.stats.files')"
        :value="stats ? formatNumber(stats.file_count ?? stats.document_count, locale) : '—'"
        :hint="stats?.folder_count != null ? t('search.stats.foldersToo', { n: formatNumber(stats.folder_count, locale) }, stats.folder_count) : undefined"
        :icon="Database"
        icon-tone="brand"
        data-testid="search-stat-files"
      />
      <StatCard
        :label="t('search.stats.size')"
        :value="stats ? formatBytes(stats.index_size_bytes, locale) : '—'"
        :icon="Database"
        icon-tone="emerald"
      />
      <StatCard
        :label="t('search.stats.lastBuilt')"
        :value="stats?.last_built_at ? formatRelative(stats.last_built_at, locale) : '—'"
        :icon="RefreshCcw"
        icon-tone="amber"
      />
    </div>

    <p v-if="stats?.rebuilding" class="text-sm text-amber-600 dark:text-amber-400">
      <Badge tone="amber" dot>{{ t('common.running') }}</Badge>
    </p>
    <p class="text-xs text-zinc-500">{{ t('search.rebuildHint') }}</p>

    <form class="flex gap-2 items-start" @submit.prevent="runSearch">
      <Input
        v-model="q"
        :placeholder="t('search.queryPlaceholder')"
        autocomplete="off"
        class="flex-1"
      />
      <!-- bul:s3 — scope: name | content | all -->
      <Select
        :model-value="scope"
        :options="scopeOptions"
        :aria-label="t('search.scope')"
        class="w-36"
        @update:model-value="(v) => { scope = v as SearchScope; if (q.trim()) runSearch(); }"
      />
      <Button type="submit" :loading="searching">
        <Search class="h-4 w-4" />
        {{ t('common.search') }}
      </Button>
    </form>

    <DataTable
      v-if="searching || q"
      table-id="admin.search"
      :columns="columns"
      :rows="results.items"
      :total="results.total"
      :loading="searching"
      :empty="t('search.noResults')"
      row-key="id"
      data-testid="search-results"
    >
      <template #toolbar>
        <span class="text-xs">{{ t('search.resultCount', { n: formatNumber(results.total, locale) }, results.total) }}</span>
      </template>
      <template #cell-filename="{ row }">
        <!-- ONE root: a Badge beside a name that wraps is squeezed below its
             own label and spills under it (web/tests/ui/tablePinnedActions →
             "a Badge shares its cell with nothing"). -->
        <div>
          <span class="font-medium">{{ row.filename }}</span>
          <!-- bul:s3 — content-match badge -->
          <Badge
            v-if="row.matched === 'content' || row.matched === 'both'"
            tone="amber"
            size="xs"
            class="ms-1 align-middle"
          >{{ t('search.inContent') }}</Badge>
        </div>
      </template>
      <template #cell-path="{ row }">
        <span class="tbl-mono tbl-clamp" :title="row.path">{{ row.path }}</span>
      </template>
      <!-- bul:s3 — «»-highlighted snippet, rendered as text segments (no v-html) -->
      <template #cell-snippet="{ row }">
        <span v-if="row.snippet" class="tbl-clamp">
          <template v-for="(seg, si) in snippetSegments(row.snippet)" :key="si">
            <mark
              v-if="seg.match"
              class="rounded-sm bg-amber-200/70 dark:bg-amber-500/30 px-0.5 font-medium text-inherit"
            >{{ seg.text }}</mark>
            <template v-else>{{ seg.text }}</template>
          </template>
        </span>
        <template v-else>—</template>
      </template>
      <template #cell-storage_name="{ row }">
        <Badge tone="zinc" size="xs">{{ row.storage_name }}</Badge>
      </template>
    </DataTable>
  </div>
</template>
