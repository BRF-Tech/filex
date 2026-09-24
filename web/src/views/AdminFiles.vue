<script setup lang="ts">
/**
 * AdminFiles — find a file by name and open its version history.
 *
 * ⚠⚠ It asked the operator to TYPE A NODE ID ("Node ID — örn. 1024"), and
 * told them to fish it out of the explorer's details panel or an API answer
 * (release-candidate sweep, 2026-09-21, QA #30). A database id is not how
 * anybody names a file. The page now searches by name — the same file search
 * the explorer uses (`/api/files/search`, which already applies the caller's
 * permissions) — and a row opens that file's history. The id travels in the
 * URL only; the history page names the file it is showing.
 *
 * ⚠ One table: the results are the explorer's DataTable, like every other
 * admin list. Folders are left out — a folder has no version history.
 */
import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { History, Search } from 'lucide-vue-next';

import { SearchApi, type SearchHitEx } from '@/api/search';
import { useStoragesStore } from '@/stores/storages';
import { extractError } from '@/api/client';
import { formatBytes, formatDate } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import { DataTable, type DataColumn } from '@brftech/filex-core';

const { t, locale } = useI18n();
const router = useRouter();
const storages = useStoragesStore();

const q = ref('');
const searched = ref('');
const hits = ref<SearchHitEx[]>([]);
const searching = ref(false);
const failure = ref('');

onMounted(() => {
  if (storages.empty) void storages.fetch().catch(() => {});
});

function storageName(id: number): string {
  return storages.items.find((s) => s.id === id)?.name ?? '';
}

async function search() {
  failure.value = '';
  const term = q.value.trim();
  if (!term) {
    failure.value = t('adminFiles.needTerm');
    hits.value = [];
    searched.value = '';
    return;
  }
  searching.value = true;
  try {
    const res = await SearchApi.query({ q: term, scope: 'name', page: 1, page_size: 50 });
    hits.value = res.items.filter((h) => !h.is_dir);
    searched.value = term;
  } catch (e: unknown) {
    failure.value = extractError(e, t('errors.generic'));
  } finally {
    searching.value = false;
  }
}

function open(row: SearchHitEx) {
  router.push({ name: 'files.versions', params: { nodeId: row.id } });
}

const columns = computed<DataColumn<SearchHitEx>[]>(() => [
  { id: 'filename', label: t('explore.cols.name'), sortable: true, width: 220 },
  { id: 'path', label: t('common.path'), sortable: true, width: 280 },
  {
    id: 'storage_id',
    label: t('common.storage'),
    sortable: true,
    width: 140,
    format: (h) => storageName(h.storage_id) || '—',
    sortValue: (h) => storageName(h.storage_id),
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
]);
</script>

<template>
  <section class="space-y-4">
    <header class="space-y-1">
      <h1 class="text-2xl font-semibold text-zinc-900 dark:text-zinc-100">
        {{ t('adminFiles.title') }}
      </h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">
        {{ t('adminFiles.subtitle') }}
      </p>
    </header>

    <!-- ⚠ novalidate: the empty-term message is ours, in the panel's language. -->
    <form class="flex gap-2 items-start" novalidate data-testid="admin-files-search" @submit.prevent="search">
      <Input
        v-model="q"
        :aria-label="t('adminFiles.searchLabel')"
        :placeholder="t('adminFiles.searchPlaceholder')"
        autocomplete="off"
        class="flex-1"
        data-testid="admin-files-q"
      />
      <Button type="submit" :loading="searching" data-testid="admin-files-go">
        <Search class="h-4 w-4" />
        {{ t('common.search') }}
      </Button>
    </form>
    <p v-if="failure" class="error-text" role="alert" data-testid="admin-files-error">{{ failure }}</p>

    <DataTable
      v-if="searched"
      table-id="admin.files.search"
      :columns="columns"
      :rows="hits"
      :loading="searching"
      :empty="t('adminFiles.noMatch', { q: searched })"
      row-key="id"
      data-testid="admin-files-results"
      @row-click="open"
    >
      <template #cell-filename="{ row }">
        <span class="inline-flex items-center gap-1.5 font-medium">
          <History class="h-3.5 w-3.5 shrink-0 text-zinc-400" aria-hidden="true" />
          <bdi>{{ row.filename }}</bdi>
        </span>
      </template>
      <template #cell-path="{ row }">
        <span class="tbl-clamp"><bdi>{{ row.path }}</bdi></span>
      </template>
    </DataTable>
    <p v-else class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('adminFiles.hint') }}</p>
  </section>
</template>
