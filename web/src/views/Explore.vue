<script setup lang="ts">
// Explore page — fullscreen file browser. Renders the real
// @brftech/filex-core <FileExplorer/> SFC against the backend's
// `/api/files/manager?action=…` route. The slim header (logo +
// storage tabs + Refresh + Admin paneli + dark/locale switchers)
// is preserved; the rest of the chrome (toolbar, breadcrumb, list/
// grid, modals) is owned by the FileExplorer component itself.

import { computed, onMounted, ref, watch } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ChevronLeft, RefreshCcw, LayoutDashboard } from 'lucide-vue-next';

import { FileExplorer, type ExplorerConfig } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';

import { useAuthStore } from '@/stores/auth';
import { useStoragesStore } from '@/stores/storages';
import LogoMark from '@/components/LogoMark.vue';
import Button from '@/components/ui/Button.vue';
import LocaleSwitcher from '@/components/LocaleSwitcher.vue';
import DarkModeToggle from '@/components/DarkModeToggle.vue';

const { t, locale } = useI18n();
const router = useRouter();
const route = useRoute();
const auth = useAuthStore();
const storages = useStoragesStore();

const selectedStorageId = ref<number | null>(null);

const selectedStorage = computed(() =>
  selectedStorageId.value !== null
    ? storages.items.find((s) => s.id === selectedStorageId.value) ?? null
    : null,
);

// Bump on Refresh to remount the FileExplorer (cheapest forced
// reload — its own data fetcher reruns on construction).
const remountKey = ref(0);

function readCsrfCookie(): string | null {
  const prefix = 'filex_csrf=';
  for (const part of document.cookie.split(';')) {
    const trimmed = part.trim();
    if (trimmed.startsWith(prefix)) return decodeURIComponent(trimmed.slice(prefix.length));
  }
  return null;
}

function readBearerToken(): string | null {
  return sessionStorage.getItem('filex.bearer');
}

function buildConfig(adapterName: string): ExplorerConfig {
  // Auth shape mirrors the admin axios client: bearer if a token is
  // stashed in sessionStorage, otherwise CSRF + cookie session. The
  // FileExplorer's `useFileApi` honours either.
  const bearer = readBearerToken();
  const csrf = readCsrfCookie();
  const auth: ExplorerConfig['auth'] = bearer
    ? { kind: 'bearer', token: bearer }
    : csrf
      ? { kind: 'csrf', csrf }
      : { kind: 'none' };

  return {
    apiBase: '',
    endpoint: '/api/files/manager',
    capabilities: '/api/files/capabilities',
    auth,
    locale: locale.value === 'en' ? 'en' : 'tr',
    defaultAdapter: adapterName,
    initialPath: `${adapterName}://`,
    pathPersist: 'localStorage',
    trashVisible: false,
    showInfoPanel: true,
  };
}

const explorerConfig = computed<ExplorerConfig | null>(() => {
  if (!selectedStorage.value) return null;
  return buildConfig(selectedStorage.value.name);
});

function pickStorage(id: number) {
  selectedStorageId.value = id;
  remountKey.value += 1;
  router.replace({ query: { ...route.query, storage: id } });
}

function refresh() {
  remountKey.value += 1;
}

function back() {
  router.push({ name: 'dashboard' });
}

function onExplorerError(err: { message: string; context?: unknown }) {
  // Forward to console only — the FileExplorer surfaces a toast
  // itself. Future: pipe into the toast store.
  // eslint-disable-next-line no-console
  console.warn('[explore] FileExplorer error:', err);
}

watch(
  () => storages.items,
  (items) => {
    if (items.length === 0) {
      selectedStorageId.value = null;
      return;
    }
    if (selectedStorageId.value && items.some((s) => s.id === selectedStorageId.value)) return;
    const fromQuery = Number(route.query.storage);
    selectedStorageId.value =
      Number.isFinite(fromQuery) && items.some((s) => s.id === fromQuery)
        ? fromQuery
        : items[0].id;
    remountKey.value += 1;
  },
  { immediate: true },
);

onMounted(async () => {
  await Promise.allSettled([auth.fetchMe(), storages.fetch()]);
});
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
        <Button size="xs" variant="ghost" @click="refresh()" :title="t('common.refresh')">
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

    <main class="flex-1 flex flex-col min-h-0">
      <div
        v-if="!storages.items.length"
        class="flex flex-col items-center justify-center gap-3 mt-16 text-sm text-zinc-500"
      >
        <p>{{ t('explore.noStorage') }}</p>
        <Button v-if="auth.isAuthenticated" size="sm" variant="primary" @click="router.push({ name: 'storages.new' })">
          {{ t('explore.addStorage') }}
        </Button>
      </div>

      <div v-else-if="explorerConfig" class="flex-1 min-h-0 explore-host">
        <FileExplorer
          :key="`fx-${selectedStorageId}-${remountKey}`"
          :config="explorerConfig"
          @error="onExplorerError"
        />
      </div>
    </main>
  </div>
</template>

<style scoped>
.explore-host {
  /* The FileExplorer SFC fills its host via flex layout. */
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.explore-host :deep(.fe) {
  flex: 1 1 auto;
  min-height: 0;
  height: 100%;
}
</style>
