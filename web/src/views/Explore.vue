<script setup lang="ts">
// Explore page — fullscreen file browser, no admin chrome.
//
// v0.1: native Vue list UI hitting /api/files/manager + /api/files/read.
// The @brftech/filex Web Component (loaded from /embed.js) targets a
// different API surface (Vuefinder-style /api/files/ops, /api/files/
// capabilities) so we render our own list here until the backend
// contracts converge in v0.2.

import { computed, onMounted, ref, watch } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import {
  ChevronLeft,
  RefreshCcw,
  LayoutDashboard,
  Folder,
  FileText,
  FileImage,
  FileVideo,
  FileAudio,
  FileArchive,
  Download,
  ChevronRight,
  Home,
} from 'lucide-vue-next';

import { useAuthStore } from '@/stores/auth';
import { useStoragesStore } from '@/stores/storages';
import { api, extractError } from '@/api/client';
import { formatBytes, formatDate } from '@/lib/format';
import LogoMark from '@/components/LogoMark.vue';
import Button from '@/components/ui/Button.vue';
import LocaleSwitcher from '@/components/LocaleSwitcher.vue';
import DarkModeToggle from '@/components/DarkModeToggle.vue';

interface Node {
  id: number;
  storage_id: number;
  name: string;
  path: string;
  type: 'file' | 'dir';
  size: number;
  mime: string;
  parent_id: number | null;
  backend_mtime?: string;
}

const { t, locale } = useI18n();
const router = useRouter();
const route = useRoute();
const auth = useAuthStore();
const storages = useStoragesStore();

const selectedStorageId = ref<number | null>(null);
const breadcrumbs = ref<Node[]>([]);
const nodes = ref<Node[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

const currentParent = computed<number | null>(() =>
  breadcrumbs.value.length ? breadcrumbs.value[breadcrumbs.value.length - 1].id : null,
);

async function fetchList() {
  if (!selectedStorageId.value) return;
  loading.value = true;
  error.value = null;
  try {
    const params: Record<string, string | number> = { storage: selectedStorageId.value };
    if (currentParent.value !== null) params.parent = currentParent.value;
    const { data } = await api.get<{ nodes: Node[] | null }>('/files/manager', { params });
    nodes.value = data.nodes ?? [];
  } catch (e: unknown) {
    error.value = extractError(e, 'Failed to list files');
  } finally {
    loading.value = false;
  }
}

function pickStorage(id: number) {
  selectedStorageId.value = id;
  breadcrumbs.value = [];
  router.replace({ query: { ...route.query, storage: id } });
}

function openDir(n: Node) {
  if (n.type !== 'dir') return;
  breadcrumbs.value = [...breadcrumbs.value, n];
}

function gotoCrumb(idx: number) {
  // -1 = root
  breadcrumbs.value = idx < 0 ? [] : breadcrumbs.value.slice(0, idx + 1);
}

function downloadUrl(n: Node): string {
  return `/api/files/read?id=${n.id}`;
}

function iconFor(n: Node) {
  if (n.type === 'dir') return Folder;
  const m = (n.mime || '').toLowerCase();
  if (m.startsWith('image/')) return FileImage;
  if (m.startsWith('video/')) return FileVideo;
  if (m.startsWith('audio/')) return FileAudio;
  if (/zip|tar|7z|rar|gzip|bzip/.test(m)) return FileArchive;
  return FileText;
}

const sortedNodes = computed(() =>
  [...nodes.value].sort((a, b) => {
    if (a.type !== b.type) return a.type === 'dir' ? -1 : 1;
    return a.name.localeCompare(b.name, locale.value);
  }),
);

watch(currentParent, fetchList);
watch(selectedStorageId, fetchList);

onMounted(async () => {
  await Promise.allSettled([auth.fetchMe(), storages.fetch()]);
  if (storages.items.length > 0) {
    const fromQuery = Number(route.query.storage);
    selectedStorageId.value =
      Number.isFinite(fromQuery) && storages.items.some((s) => s.id === fromQuery)
        ? fromQuery
        : storages.items[0].id;
  }
  await fetchList();
});

function back() {
  router.push({ name: 'dashboard' });
}
</script>

<template>
  <div class="min-h-screen flex flex-col bg-zinc-50 dark:bg-zinc-950">
    <header
      class="sticky top-0 z-20 flex h-14 items-center gap-3 border-b border-zinc-200 dark:border-zinc-800 bg-white/80 dark:bg-zinc-900/80 backdrop-blur px-4 sm:px-6"
    >
      <button
        type="button"
        class="rounded p-1.5 text-zinc-700 dark:text-zinc-200 hover:bg-zinc-100 dark:hover:bg-zinc-800"
        :title="t('common.back')"
        @click="back"
      >
        <ChevronLeft class="h-5 w-5" />
      </button>
      <LogoMark class="h-6 w-6" />
      <span class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">filex</span>
      <span class="text-xs text-zinc-500 hidden sm:inline">{{ t('explore.tagline') }}</span>

      <nav v-if="storages.items.length > 1" class="ml-4 flex items-center gap-1 overflow-x-auto">
        <button
          v-for="s in storages.items"
          :key="s.id"
          type="button"
          class="px-3 py-1 rounded-md text-xs"
          :class="s.id === selectedStorageId
            ? 'bg-brand-100 text-brand-700 dark:bg-brand-500/20 dark:text-brand-200'
            : 'text-zinc-600 hover:bg-zinc-100 dark:text-zinc-300 dark:hover:bg-zinc-800'"
          @click="pickStorage(s.id)"
        >
          {{ s.name }}
        </button>
      </nav>

      <div class="ml-auto flex items-center gap-1.5">
        <Button size="xs" variant="ghost" @click="fetchList()" :title="t('common.refresh')">
          <RefreshCcw class="h-4 w-4" />
        </Button>
        <Button v-if="auth.isAuthenticated" size="xs" variant="outline" @click="router.push({ name: 'dashboard' })">
          <LayoutDashboard class="h-4 w-4" />
          {{ t('explore.gotoAdmin') }}
        </Button>
        <DarkModeToggle />
        <LocaleSwitcher />
      </div>
    </header>

    <main class="flex-1 flex flex-col">
      <div class="px-4 sm:px-6 pt-3 flex flex-wrap items-center gap-1 text-xs text-zinc-600 dark:text-zinc-400">
        <button class="inline-flex items-center gap-1 hover:text-zinc-900 dark:hover:text-zinc-100" @click="gotoCrumb(-1)">
          <Home class="h-3.5 w-3.5" />
          {{ t('explore.root') }}
        </button>
        <template v-for="(c, i) in breadcrumbs" :key="c.id">
          <ChevronRight class="h-3 w-3 text-zinc-400" />
          <button class="hover:text-zinc-900 dark:hover:text-zinc-100" @click="gotoCrumb(i)">
            {{ c.name }}
          </button>
        </template>
      </div>

      <div class="flex-1 px-4 sm:px-6 pb-6 pt-2">
        <div v-if="loading" class="text-sm text-zinc-500 mt-6">{{ t('explore.loading') }}</div>

        <div v-else-if="error" class="rounded-md border border-rose-200 bg-rose-50 p-3 text-sm text-rose-800 dark:border-rose-500/40 dark:bg-rose-500/10 dark:text-rose-300">
          {{ error }}
        </div>

        <div
          v-else-if="!storages.items.length"
          class="flex flex-col items-center justify-center gap-3 mt-16 text-sm text-zinc-500"
        >
          <p>{{ t('explore.noStorage') }}</p>
          <Button v-if="auth.isAuthenticated" size="sm" variant="primary" @click="router.push({ name: 'storages.new' })">
            {{ t('explore.addStorage') }}
          </Button>
        </div>

        <div v-else-if="!sortedNodes.length" class="text-sm text-zinc-500 mt-6">
          {{ t('explore.empty') }}
        </div>

        <div
          v-else
          class="overflow-hidden rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900"
        >
          <table class="w-full text-sm">
            <thead class="bg-zinc-50 dark:bg-zinc-900/60 text-xs uppercase text-zinc-500 dark:text-zinc-400">
              <tr>
                <th class="px-4 py-2 text-left">{{ t('explore.cols.name') }}</th>
                <th class="px-4 py-2 text-right">{{ t('explore.cols.size') }}</th>
                <th class="px-4 py-2 text-left hidden sm:table-cell">{{ t('explore.cols.mime') }}</th>
                <th class="px-4 py-2 text-left hidden md:table-cell">{{ t('explore.cols.modified') }}</th>
                <th class="px-4 py-2 text-right"></th>
              </tr>
            </thead>
            <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
              <tr
                v-for="n in sortedNodes"
                :key="n.id"
                class="hover:bg-zinc-50 dark:hover:bg-zinc-800/40 cursor-pointer"
                @click="openDir(n)"
              >
                <td class="px-4 py-2">
                  <span class="inline-flex items-center gap-2 text-zinc-900 dark:text-zinc-100">
                    <component :is="iconFor(n)" class="h-4 w-4 text-zinc-500" />
                    {{ n.name }}
                  </span>
                </td>
                <td class="px-4 py-2 text-right text-xs text-zinc-500">
                  {{ n.type === 'dir' ? '—' : formatBytes(n.size, locale) }}
                </td>
                <td class="px-4 py-2 text-xs text-zinc-500 hidden sm:table-cell">{{ n.mime || '—' }}</td>
                <td class="px-4 py-2 text-xs text-zinc-500 hidden md:table-cell">
                  {{ n.backend_mtime ? formatDate(n.backend_mtime, locale) : '—' }}
                </td>
                <td class="px-4 py-2 text-right">
                  <a
                    v-if="n.type === 'file'"
                    :href="downloadUrl(n)"
                    target="_blank"
                    rel="noopener"
                    class="inline-flex items-center gap-1 text-xs text-brand-600 hover:underline dark:text-brand-400"
                    @click.stop
                  >
                    <Download class="h-3.5 w-3.5" />
                    {{ t('explore.download') }}
                  </a>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </main>
  </div>
</template>
