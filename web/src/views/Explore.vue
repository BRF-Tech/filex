<script setup lang="ts">
// Explore page — fullscreen file browser. Renders the real
// @brftech/filex-core <FileExplorer/> SFC with `multiStorageRoot`
// turned on: the user lands at "/" which lists every configured
// storage as a virtual folder. Clicking one drills into it; the
// breadcrumb walks `/ › s3-test › example › …`.
//
// The old per-storage tab strip is gone — the storage list is now
// the home screen of the explorer itself.

import { computed, onMounted, ref } from 'vue';
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

// `?storage=` deep links: `/admin/explore?storage=s3-test` →
// initialPath becomes `s3-test://`. Without one the explorer opens
// at the global root (storage list).
const initialPathFromQuery = computed(() => {
  const raw = route.query.storage;
  const rawStr = Array.isArray(raw) ? raw[0] : raw;
  if (typeof rawStr !== 'string' || !rawStr) return '';
  // Match by name first, then by numeric id.
  const byName = storages.items.find((s) => s.name === rawStr);
  if (byName) return `${byName.name}://`;
  const numeric = Number(rawStr);
  if (Number.isFinite(numeric)) {
    const byId = storages.items.find((s) => s.id === numeric);
    if (byId) return `${byId.name}://`;
  }
  return '';
});

const explorerConfig = computed<ExplorerConfig | null>(() => {
  if (!storages.items.length) return null;
  const bearer = readBearerToken();
  const csrf = readCsrfCookie();
  const authConf: ExplorerConfig['auth'] = bearer
    ? { kind: 'bearer', token: bearer }
    : csrf
      ? { kind: 'csrf', csrf }
      : { kind: 'none' };
  return {
    apiBase: '',
    endpoint: '/api/files/manager',
    capabilities: '/api/files/capabilities',
    auth: authConf,
    locale: locale.value === 'en' ? 'en' : 'tr',
    pathPersist: 'localStorage',
    trashVisible: false,
    showInfoPanel: true,
    multiStorageRoot: true,
    storages: storages.items.map((s) => ({
      name: s.name,
      label: s.name,
      driver: s.driver,
      readOnly: s.read_only,
    })),
    initialPath: initialPathFromQuery.value || '',
    // "Aç" / double-click → open the standalone editor in a new tab.
    // The route reads `?path=&type=&mode=` and mounts the right viewer
    // (OnlyOffice for office, Monaco for code, drawio iframe for
    // .drawio, image/PDF/3D viewers otherwise) with save-on-change.
    openPageBase: '/files/edit',
    viewerBaseUrl: '/files/edit',
    saveText: '/api/files/save-text',
    onlyOfficeConfig: '/api/files/onlyoffice/config',
  };
});

function refresh() {
  remountKey.value += 1;
}

function back() {
  router.push({ name: 'dashboard' });
}

function onExplorerError(err: { message: string; context?: unknown }) {
  // eslint-disable-next-line no-console
  console.warn('[explore] FileExplorer error:', err);
}

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
          :key="`fx-multi-${remountKey}`"
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
