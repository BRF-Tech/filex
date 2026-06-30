<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Tag, FileText } from 'lucide-vue-next';

import { TagsApi } from '@/api/tags';
import type { SearchHit } from '@/api/types';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { formatBytes, formatDate } from '@/lib/format';

import Badge from '@/components/ui/Badge.vue';
import EmptyState from '@/components/ui/EmptyState.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t, locale } = useI18n();
const toast = useToastStore();

const tags = ref<string[]>([]);
const selected = ref<string | null>(null);
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

async function selectTag(tag: string) {
  selected.value = tag;
  loadingFiles.value = true;
  files.value = [];
  try {
    files.value = await TagsApi.filesByTag(tag);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loadingFiles.value = false;
  }
}

onMounted(loadTags);
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
      <!-- Tag chips -->
      <div class="flex flex-wrap gap-2">
        <button
          v-for="tag in tags"
          :key="tag"
          type="button"
          class="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-sm font-medium ring-1 ring-inset transition-colors"
          :class="
            selected === tag
              ? 'bg-brand-600 text-white ring-brand-600'
              : 'bg-zinc-100 text-zinc-700 ring-zinc-300 hover:bg-zinc-200 dark:bg-zinc-800 dark:text-zinc-300 dark:ring-zinc-700 dark:hover:bg-zinc-700'
          "
          @click="selectTag(tag)"
        >
          <Tag class="h-3.5 w-3.5" />
          {{ tag }}
        </button>
      </div>

      <!-- Files for the selected tag -->
      <div v-if="loadingFiles" class="card card-body text-center text-zinc-500"><Spinner /></div>

      <EmptyState
        v-else-if="!selected"
        :icon="Tag"
        :title="t('tagged.selectTag')"
        size="sm"
      />

      <div v-else-if="files.length > 0" class="space-y-2">
        <p class="text-xs text-zinc-500">
          {{ t('tagged.resultsFor', { tag: selected }) }}
        </p>
        <ul class="card divide-y divide-zinc-200 dark:divide-zinc-800">
          <li v-for="hit in files" :key="hit.id" class="px-4 py-3 text-sm">
            <div class="flex items-center justify-between gap-2">
              <span class="truncate font-medium">{{ hit.filename }}</span>
              <Badge v-if="hit.storage_name" tone="zinc" size="xs">{{ hit.storage_name }}</Badge>
            </div>
            <p class="text-xs font-mono text-zinc-500 truncate">{{ hit.path }}</p>
            <p class="text-xs text-zinc-500 mt-0.5">
              {{ hit.mime || '—' }} · {{ formatBytes(hit.size, locale) }} ·
              {{ formatDate(hit.modified_at, locale) }}
            </p>
          </li>
        </ul>
      </div>

      <EmptyState
        v-else
        :icon="FileText"
        :title="t('tagged.noResults')"
        size="sm"
      />
    </template>
  </div>
</template>
