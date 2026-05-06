<script setup lang="ts">
// Explore page — full-screen file manager, no admin chrome.
// Used by the "Filex'i göster" CTA on the demo landing page.
//
// We mount the Web Component bundle (/embed.js) at runtime instead
// of importing @brftech/filex-core into the admin SPA. This keeps
// the admin bundle lean and reuses the exact artifact a third-party
// consumer would script-include.
//
// The component is auth-aware: cookies set during the demo login
// flow ride along on every fetch (same-origin, withCredentials).
//
// Storage selector: when the deploy has more than one storage we
// render a tab strip; first storage is selected by default.

import { computed, onMounted, ref } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ChevronLeft, RefreshCcw, LayoutDashboard } from 'lucide-vue-next';

import { useAuthStore } from '@/stores/auth';
import { useStoragesStore } from '@/stores/storages';
import LogoMark from '@/components/LogoMark.vue';
import Button from '@/components/ui/Button.vue';
import LocaleSwitcher from '@/components/LocaleSwitcher.vue';
import DarkModeToggle from '@/components/DarkModeToggle.vue';

const { t } = useI18n();
const router = useRouter();
const route = useRoute();
const auth = useAuthStore();
const storages = useStoragesStore();

const loadingScript = ref(true);
const scriptError = ref<string | null>(null);
const selectedStorageId = ref<number | null>(null);

const apiBase = computed(() => window.location.origin);
const filexConfig = computed(() => {
  const cfg: Record<string, unknown> = {
    apiBase: apiBase.value,
    locale: 'tr',
    storageId: selectedStorageId.value,
  };
  return JSON.stringify(cfg);
});

async function loadFilexBundle() {
  // Avoid double-include if the user navigates back here.
  if (window.customElements && window.customElements.get('filex-explorer')) {
    loadingScript.value = false;
    return;
  }
  // <link> first so the explorer's stylesheet is in place before
  // first paint.
  const css = document.createElement('link');
  css.rel = 'stylesheet';
  css.href = '/embed.css';
  document.head.appendChild(css);

  return new Promise<void>((resolve, reject) => {
    const s = document.createElement('script');
    s.type = 'module';
    s.src = '/embed.js';
    s.onload = () => {
      loadingScript.value = false;
      resolve();
    };
    s.onerror = () => {
      scriptError.value = 'embed.js load failed';
      loadingScript.value = false;
      reject(new Error('embed.js'));
    };
    document.head.appendChild(s);
  });
}

onMounted(async () => {
  // Hydrate session + storages so the explorer has a target id.
  await Promise.allSettled([auth.fetchMe(), storages.fetch()]);
  if (storages.items.length > 0) {
    const fromQuery = Number(route.query.storage);
    selectedStorageId.value =
      Number.isFinite(fromQuery) && storages.items.some((s) => s.id === fromQuery)
        ? fromQuery
        : storages.items[0].id;
  }
  await loadFilexBundle();
});

function pickStorage(id: number) {
  selectedStorageId.value = id;
  router.replace({ query: { ...route.query, storage: id } });
}

function back() {
  // Demo deployments → bare /admin/ (landing). Otherwise the dashboard.
  router.push({ name: 'dashboard' });
}
</script>

<template>
  <div class="min-h-screen flex flex-col bg-zinc-50 dark:bg-zinc-950">
    <!-- Slim top bar -->
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

      <!-- Storage tabs -->
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
        <Button size="xs" variant="ghost" @click="storages.fetch()" :title="t('common.refresh')">
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

    <!-- Body -->
    <main class="flex-1 flex flex-col">
      <div v-if="loadingScript" class="flex flex-1 items-center justify-center text-sm text-zinc-500">
        {{ t('explore.loading') }}
      </div>
      <div v-else-if="scriptError" class="flex flex-1 items-center justify-center text-sm text-rose-600">
        {{ scriptError }}
      </div>
      <div
        v-else-if="!storages.items.length"
        class="flex flex-1 flex-col items-center justify-center gap-3 text-sm text-zinc-500"
      >
        <p>{{ t('explore.noStorage') }}</p>
        <Button v-if="auth.isAuthenticated" size="sm" variant="primary" @click="router.push({ name: 'storages.new' })">
          {{ t('explore.addStorage') }}
        </Button>
      </div>
      <div v-else class="flex-1 p-3 sm:p-6">
        <!--
          The Web Component reads its config from the JSON string
          attribute. v-html-bypass via :config is intentional —
          it's our own JSON, not user input.
        -->
        <filex-explorer
          :api-base="apiBase"
          :storage-id="selectedStorageId ?? undefined"
          :config="filexConfig"
          class="block h-full w-full"
        />
      </div>
    </main>
  </div>
</template>

<style scoped>
filex-explorer {
  display: block;
  min-height: 70vh;
  width: 100%;
  /* Variables forwarded to the shadowed component if it reads them. */
  --filex-bg: transparent;
}
</style>
