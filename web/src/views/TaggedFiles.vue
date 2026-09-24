<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Tag } from 'lucide-vue-next';

import { TagsApi } from '@/api/tags';
import type { SearchHit } from '@/api/types';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { formatBytes, formatDate } from '@/lib/format';

import Badge from '@/components/ui/Badge.vue';
import EmptyState from '@/components/ui/EmptyState.vue';
import Spinner from '@/components/ui/Spinner.vue';
import { DataTable, TagKindIcon, type DataColumn, type TagItem, type TagKind } from '@brftech/filex-core';

const { t, locale } = useI18n();
const toast = useToastStore();

/* etiket:k2 (v0.43) — tags are personal or team. The chips come in two groups,
   each under its kind's name and glyph, and a chip opens the files of THAT
   kind: a personal "rapor" and the team's "rapor" are two different lists. */
const tags = ref<TagItem[]>([]);
const selected = ref<TagItem | null>(null);
const groups = computed(() =>
  (['personal', 'team'] as TagKind[])
    .map((kind) => ({ kind, list: tags.value.filter((x) => x.kind === kind) }))
    .filter((g) => g.list.length > 0),
);
const files = ref<SearchHit[]>([]);
const loadingTags = ref(false);
const loadingFiles = ref(false);

async function loadTags() {
  loadingTags.value = true;
  try {
    tags.value = await TagsApi.listAllTags();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loadingTags.value = false;
  }
}

async function selectTag(tag: TagItem) {
  selected.value = tag;
  loadingFiles.value = true;
  files.value = [];
  try {
    files.value = await TagsApi.filesByTag(tag.name, tag.kind);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loadingFiles.value = false;
  }
}

onMounted(loadTags);

/* ⚠ A `<ul class="card divide-y divide-zinc-200 dark:divide-zinc-800">` is
   what this was: a name, a path, a type, a size and a date per row — a table
   that had opted out of being one, and out of the palette with it (two frozen
   zinc hexes no theme can move). It is the explorer's own table now
   (DataTable — there is no other), so the name freezes on the left, every
   column resizes, hides, moves and sorts, and the arrangement is remembered
   on the account under `admin.tagged`. A tag's files arrive in one answer, so
   the table sorts them itself: every row is on screen. */
const columns = computed<DataColumn<SearchHit>[]>(() => [
  { id: 'filename', label: t('explore.cols.name'), sortable: true, width: 220 },
  { id: 'path', label: t('common.path'), sortable: true, width: 260 },
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
    sortValue: (h) => (typeof h.size === 'number' ? h.size : null),
  },
  {
    id: 'modified_at',
    label: t('explore.cols.modified'),
    sortable: true,
    sortDir: 'desc',
    width: 170,
    format: (h) => formatDate(h.modified_at, locale.value),
    sortValue: (h) => (h.modified_at ? Date.parse(h.modified_at) : null),
  },
]);
</script>

<template>
  <div class="space-y-4">
    <div>
      <h1 class="text-xl font-semibold">{{ t('tagged.title') }}</h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('tagged.subtitle') }}</p>
    </div>

    <div v-if="loadingTags" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <EmptyState
      v-else-if="tags.length === 0"
      :icon="Tag"
      :title="t('tagged.noTags')"
      :description="t('tagged.noTagsHint')"
      size="sm"
    />

    <template v-else>
      <!-- Tag chips, one group per kind -->
      <div v-for="g in groups" :key="g.kind" class="space-y-1.5" :data-testid="`tagged-group-${g.kind}`">
        <p class="flex items-center gap-1.5 text-xs font-semibold text-zinc-500 dark:text-zinc-400">
          <TagKindIcon :kind="g.kind" :size="14" />
          {{ t(`tagged.${g.kind}`) }}
          <span class="font-normal">— {{ t(`tagged.${g.kind}Help`) }}</span>
        </p>
        <div class="flex flex-wrap gap-2">
          <button
            v-for="tag in g.list"
            :key="`${tag.kind}:${tag.name}`"
            type="button"
            class="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-sm font-medium ring-1 ring-inset transition-colors"
            :class="
              selected && selected.name === tag.name && selected.kind === tag.kind
                ? 'bg-brand-600 text-white ring-brand-600'
                : 'bg-zinc-100 text-zinc-700 ring-zinc-300 hover:bg-zinc-200 dark:bg-zinc-800 dark:text-zinc-300 dark:ring-zinc-700 dark:hover:bg-zinc-700'
            "
            :data-tag-kind="tag.kind"
            @click="selectTag(tag)"
          >
            <TagKindIcon :kind="tag.kind" :size="14" />
            {{ tag.name }}
          </button>
        </div>
      </div>

      <!-- Files for the selected tag -->
      <EmptyState v-if="!selected" :icon="Tag" :title="t('tagged.selectTag')" size="sm" />

      <DataTable
        v-else
        table-id="admin.tagged"
        :columns="columns"
        :rows="files"
        :loading="loadingFiles"
        :empty="t('tagged.noResults')"
        row-key="id"
        data-testid="tagged-files"
      >
        <template #toolbar>
          <span class="text-xs">{{
            t('tagged.resultsForKind', { tag: selected.name, kind: t(`tagged.${selected.kind}`) })
          }}</span>
        </template>
        <template #cell-filename="{ row }">
          <span class="font-medium">{{ row.filename }}</span>
        </template>
        <template #cell-path="{ row }">
          <span class="tbl-mono tbl-clamp" :title="row.path">{{ row.path }}</span>
        </template>
        <template #cell-storage_name="{ row }">
          <Badge v-if="row.storage_name" tone="zinc" size="xs">{{ row.storage_name }}</Badge>
          <template v-else>—</template>
        </template>
      </DataTable>
    </template>
  </div>
</template>
