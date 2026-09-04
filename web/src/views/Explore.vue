<script setup lang="ts">
// Explore page — fullscreen file browser. Renders the real
// @brftech/filex-core <FileExplorer/> SFC with `multiStorageRoot`
// turned on: the user lands at "/" which lists every configured
// storage as a virtual folder. Clicking one drills into it; the
// breadcrumb walks `/ › s3-test › example › …`.
//
// The old per-storage tab strip is gone — the storage list is now
// the home screen of the explorer itself.

import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ChevronLeft, RefreshCcw, LayoutDashboard, KeyRound, LogOut, Cable } from 'lucide-vue-next';

import { FileExplorer, ConnectionsPanel, type ExplorerConfig } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';

import { useAuthStore } from '@/stores/auth';
import { useStoragesStore } from '@/stores/storages';
import LogoMark from '@/components/LogoMark.vue';
import Button from '@/components/ui/Button.vue';
import LocaleSwitcher from '@/components/LocaleSwitcher.vue';
import DarkModeToggle from '@/components/DarkModeToggle.vue';
import SelfTokensModal from '@/components/SelfTokensModal.vue';
import { effectiveTheme } from '@/lib/theme';
import { explorerAuth, readBearerToken, readCsrfCookie } from '@/lib/explorerConfig';
// Live collaboration (WebSocket + presence) now lives INSIDE @brftech/filex-core's
// FileExplorer, so every consumer (this panel + the embedded webcomponent) gets
// it automatically — no per-page realtime wiring here anymore.

const { t, locale } = useI18n();
const router = useRouter();
const route = useRoute();
const auth = useAuthStore();
const storages = useStoragesStore();

const showTokens = ref(false);
// "How to connect" — the outward half of the connections surface. This is
// where a NON-admin lands (the admin panel redirects them here), so it is
// the only place they can be told how to mount a drive.
const showConnect = ref(false);
async function doLogout() {
  await auth.logout();
  router.push({ name: 'login' });
}

// Bump on Refresh to remount the FileExplorer (cheapest forced
// reload — its own data fetcher reruns on construction).
const remountKey = ref(0);

// Reactive theme passthrough — without this the SFC's CSS variable
// cascade falls back to `prefers-color-scheme: dark` on OS dark
// systems even when the admin shell is on light, leaving the
// explorer pane locked to dark after the user flips the panel.
// MutationObserver watches `<html>` class changes; localStorage
// `storage` events keep cross-tab toggles in sync.
const currentTheme = ref<'light' | 'dark'>(effectiveTheme());
let htmlObserver: MutationObserver | null = null;
const onStorage = (e: StorageEvent) => {
  if (e.key === 'filex.theme') currentTheme.value = effectiveTheme();
};
onMounted(() => {
  htmlObserver = new MutationObserver(() => {
    currentTheme.value = document.documentElement.classList.contains('dark') ? 'dark' : 'light';
  });
  htmlObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
  window.addEventListener('storage', onStorage);
});
onBeforeUnmount(() => {
  htmlObserver?.disconnect();
  window.removeEventListener('storage', onStorage);
});

// Visible storages for the explorer root. Admins get the rich admin-store
// list; non-admins (user/viewer) can't hit /api/admin/storages, so we discover
// their visible storages from the manager root (StorageVisible-filtered) —
// otherwise the explorer would show "no storages" for every non-admin.
type RootEntry = { name: string; label: string; driver?: string; readOnly?: boolean };
const roots = ref<RootEntry[]>([]);
// True until the first storage-discovery pass finishes, so we show a loading
// screen instead of flashing the "no storage" empty state during startup.
const loading = ref(true);

async function fetchVisibleStorages(): Promise<RootEntry[]> {
  if (storages.items.length) {
    return storages.items.map((s) => ({
      name: s.name,
      label: s.name,
      driver: s.driver,
      readOnly: s.read_only,
    }));
  }
  try {
    const headers: Record<string, string> = {};
    const bearer = readBearerToken();
    const csrf = readCsrfCookie();
    if (bearer) headers['Authorization'] = `Bearer ${bearer}`;
    else if (csrf) headers['X-CSRF-TOKEN'] = csrf;
    const res = await fetch('/api/files/manager?action=index&path=', {
      headers,
      credentials: 'include',
    });
    if (!res.ok) return [];
    const body = await res.json();
    const names: string[] = Array.isArray(body?.storages) ? body.storages : [];
    return names.map((n) => ({ name: n, label: n }));
  } catch {
    return [];
  }
}

// `?storage=` deep links: `/admin/explore?storage=s3-test` →
// initialPath becomes `s3-test://`. Without one the explorer opens
// at the global root (storage list).
const initialPathFromQuery = computed(() => {
  const raw = route.query.storage;
  const rawStr = Array.isArray(raw) ? raw[0] : raw;
  if (typeof rawStr !== 'string' || !rawStr) return '';
  const byName = roots.value.find((s) => s.name === rawStr);
  if (byName) return `${byName.name}://`;
  return '';
});

const explorerConfig = computed<ExplorerConfig | null>(() => {
  if (!roots.value.length) return null;
  const authConf: ExplorerConfig['auth'] = explorerAuth();
  return {
    apiBase: '',
    endpoint: '/api/files/manager',
    capabilities: '/api/files/capabilities',
    auth: authConf,
    theme: currentTheme.value,
    locale: locale.value === 'en' ? 'en' : 'tr',
    // The address bar mirrors the current folder (#<storage>/<sub>…) so the
    // URL is a shareable deep link; localStorage still remembers the last
    // folder for hash-less visits. Priority: hash → ?storage= → remembered.
    pathPersist: 'hash+localStorage',
    trashVisible: true,
    showInfoPanel: true,
    multiStorageRoot: true,
    storages: roots.value,
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

// The connections panel must render even when the caller has no visible
// storage at all: "you cannot see anything yet, here is how you would
// connect" is a real state, and `explorerConfig` is deliberately null in
// exactly that case (the explorer has nothing to draw).
const connectConfig = computed<ExplorerConfig>(() => ({
  apiBase: '',
  endpoint: '/api/files/manager',
  auth: explorerAuth(),
  theme: currentTheme.value,
  locale: locale.value === 'en' ? 'en' : 'tr',
}));

async function refresh() {
  loading.value = true;
  try {
    roots.value = await fetchVisibleStorages();
    remountKey.value += 1;
  } finally {
    loading.value = false;
  }
}

function back() {
  router.push({ name: 'dashboard' });
}

function onExplorerError(err: { message: string; context?: unknown }) {
  // eslint-disable-next-line no-console
  console.warn('[explore] FileExplorer error:', err);
}

onMounted(async () => {
  try {
    await auth.fetchMe();
    // Admin store fetch is best-effort (403s for non-admins) — roots then fall
    // back to manager-root discovery inside fetchVisibleStorages().
    await storages.fetch().catch(() => {});
    roots.value = await fetchVisibleStorages();
  } finally {
    loading.value = false;
  }
});
</script>

<template>
  <!-- ui-fix — h-screen (was min-h-screen): min-height lets the page GROW past
       the viewport when the explorer content is tall (e.g. grid view / split),
       so .fe (height:100%) grows with it and .fe__body's internal overflow:auto
       never engages → the whole PAGE scrolls. height:100vh caps the shell so the
       listing scrolls INSIDE each pane instead. -->
  <div class="h-screen flex flex-col bg-zinc-50 dark:bg-zinc-950">
    <header
      class="sticky top-0 z-20 flex h-14 items-center gap-3 border-b border-zinc-200 dark:border-zinc-800 bg-white/80 dark:bg-zinc-900/80 backdrop-blur px-4 sm:px-6"
    >
      <button
        v-if="auth.isAdmin"
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
        <Button v-if="auth.isAdmin" size="xs" variant="outline" @click="router.push({ name: 'dashboard' })">
          <LayoutDashboard class="h-4 w-4" />
          {{ t('explore.gotoAdmin') }}
        </Button>
        <Button
          v-if="auth.isAuthenticated"
          size="xs"
          variant="ghost"
          :title="t('nav.connections')"
          data-testid="explore-connect"
          @click="showConnect = true"
        >
          <Cable class="h-4 w-4" />
        </Button>
        <Button v-if="auth.isAuthenticated && !auth.isAdmin" size="xs" variant="ghost" @click="showTokens = true" :title="t('explore.apiKeys')">
          <KeyRound class="h-4 w-4" />
        </Button>
        <Button v-if="auth.isAuthenticated" size="xs" variant="ghost" @click="doLogout" :title="t('explore.logout')">
          <LogOut class="h-4 w-4" />
        </Button>
        <DarkModeToggle />
        <LocaleSwitcher />
      </div>
    </header>
    <SelfTokensModal v-if="showTokens" @close="showTokens = false" />

    <!-- The connections surface, on the page a non-admin actually lands on.
         Same component as the admin route and as the desktop app — and it
         is the component, not this page, that decides what a caller without
         admin rights is shown.

         ⚠ z-[120], not z-50. The explorer's onboarding tour is appended to
         <body> at z-index 96 and its menus are fixed too, so anything in the
         normal stacking order gets painted over by them — measured: the tour
         card landed on top of this panel and swallowed its clicks. The
         desktop shell hit the same thing and fixed it the same way. -->
    <div
      v-if="showConnect"
      class="fixed inset-0 z-[120] overflow-auto bg-black/40 p-4 sm:p-8"
      data-testid="explore-connect-overlay"
      @click.self="showConnect = false"
    >
      <div
        class="mx-auto max-w-4xl rounded-xl bg-white dark:bg-zinc-900 p-5 shadow-xl"
        @click.stop
      >
        <ConnectionsPanel
          :config="connectConfig"
          initial-tab="connect"
          closable
          @close="showConnect = false"
          @changed="refresh()"
        />
      </div>
    </div>

    <main class="flex-1 flex flex-col min-h-0">
      <div
        v-if="loading"
        class="flex flex-1 flex-col items-center justify-center gap-4 text-zinc-500"
      >
        <span class="fx-explore-spinner" aria-hidden="true"></span>
        <p class="text-sm">{{ t('explore.loading') }}</p>
      </div>

      <div
        v-else-if="!roots.length"
        class="flex flex-col items-center justify-center gap-3 mt-16 text-sm text-zinc-500"
      >
        <!-- Two different facts wear the same empty screen. "No storages
             configured yet" is true for the operator and a guess for everyone
             else: a non-admin sees no storages when the instance has none AND
             when it has several that nobody granted them (RBAC on, docs/RBAC).
             Telling that user the server is empty sends them to configure
             something they cannot configure — the same admin-tool framing as
             the URL and the tab title in GitHub #14. -->
        <p>{{ auth.isAdmin ? t('explore.noStorage') : t('explore.noAccess') }}</p>
        <Button v-if="auth.isAdmin" size="sm" variant="primary" @click="router.push({ name: 'storages.new' })">
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
.fx-explore-spinner {
  width: 34px;
  height: 34px;
  border-radius: 50%;
  border: 3px solid rgb(161 161 170 / 0.25); /* zinc-400/25 */
  border-top-color: rgb(99 102 241); /* brand/indigo */
  animation: fx-explore-spin 0.7s linear infinite;
}
@keyframes fx-explore-spin {
  to { transform: rotate(360deg); }
}
</style>
