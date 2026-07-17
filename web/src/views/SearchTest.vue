<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Search, RefreshCcw, Database } from 'lucide-vue-next';

import { SearchApi, type SearchHitEx, type SearchIndexStats, type SearchScope } from '@/api/search';
import type { PaginatedResponse } from '@/api/types';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { formatBytes, formatDate, formatNumber, formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import StatCard from '@/components/ui/StatCard.vue';
import EmptyState from '@/components/ui/EmptyState.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Badge from '@/components/ui/Badge.vue';

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

async function rebuild() {
  rebuilding.value = true;
  try {
    await SearchApi.rebuild();
    toast.success(t('search.rebuildStarted'));
    await loadStats();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    rebuilding.value = false;
  }
}

const haveResults = computed(() => results.value.items.length > 0);

/* bul:s3 — snippet «» → <mark> via TEXT segments (never v-html). */
interface SnippetSeg {
  text: string;
  match: boolean;
}
function snippetSegments(snippet: string): SnippetSeg[] {
  const out: SnippetSeg[] = [];
  const re = /«([^«»]*)»/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(snippet)) !== null) {
    if (m.index > last) out.push({ text: snippet.slice(last, m.index), match: false });
    if (m[1]) out.push({ text: m[1], match: true });
    last = m.index + m[0].length;
  }
  if (last < snippet.length) out.push({ text: snippet.slice(last), match: false });
  return out;
}

const scopeOptions = computed(() => [
  { value: 'all', label: t('search.scopeAll') },
  { value: 'name', label: t('search.scopeName') },
  { value: 'content', label: t('search.scopeContent') },
]);

onMounted(loadStats);
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold">{{ t('search.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('search.subtitle') }}</p>
      </div>
      <Button variant="outline" size="sm" :loading="rebuilding" @click="rebuild">
        <RefreshCcw class="h-4 w-4" />
        {{ t('search.rebuild') }}
      </Button>
    </div>

    <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
      <StatCard
        :label="t('search.stats.documents')"
        :value="stats ? formatNumber(stats.document_count, locale) : '—'"
        :icon="Database"
        icon-tone="brand"
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

    <div v-if="searching" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <div v-else-if="haveResults" class="space-y-2">
      <p class="text-xs text-zinc-500">
        {{ formatNumber(results.total, locale) }} results
      </p>
      <ul class="card divide-y divide-zinc-200 dark:divide-zinc-800">
        <li v-for="hit in results.items" :key="hit.id" class="px-4 py-3 text-sm">
          <div class="flex items-center justify-between gap-2">
            <span class="truncate font-medium">
              {{ hit.filename }}
              <!-- bul:s3 — content-match badge -->
              <Badge
                v-if="hit.matched === 'content' || hit.matched === 'both'"
                tone="amber"
                size="xs"
                class="ml-1 align-middle"
              >{{ t('search.inContent') }}</Badge>
            </span>
            <Badge tone="zinc" size="xs">{{ hit.storage_name }}</Badge>
          </div>
          <p class="text-xs font-mono text-zinc-500 truncate">{{ hit.path }}</p>
          <!-- bul:s3 — «»-highlighted snippet, rendered as text segments (no v-html) -->
          <p v-if="hit.snippet" class="text-xs text-zinc-600 dark:text-zinc-300 mt-0.5 truncate">
            <template v-for="(seg, si) in snippetSegments(hit.snippet)" :key="si">
              <mark
                v-if="seg.match"
                class="rounded-sm bg-amber-200/70 dark:bg-amber-500/30 px-0.5 font-medium text-inherit"
              >{{ seg.text }}</mark>
              <template v-else>{{ seg.text }}</template>
            </template>
          </p>
          <p class="text-xs text-zinc-500 mt-0.5">
            {{ hit.mime || '—' }} · {{ formatBytes(hit.size, locale) }} ·
            {{ formatDate(hit.modified_at, locale) }} · score {{ hit.score.toFixed(3) }}
          </p>
        </li>
      </ul>
    </div>

    <EmptyState
      v-else-if="q && !searching"
      :icon="Search"
      :title="t('search.noResults')"
      size="sm"
    />
  </div>
</template>
