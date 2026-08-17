<script setup lang="ts">
/**
 * Connections — storage connections and "how to connect", in the admin
 * panel.
 *
 * ⚠ There is no form on this page. It mounts `ConnectionsPanel` from
 * `@brftech/filex-core`, which is the same component the desktop app
 * mounts as `<filex-connections>`; the driver fields come from the
 * server's own descriptors and the client instructions from the live
 * deployment. Anything that needs fixing here gets fixed once, in the
 * package, and lands on every surface.
 *
 * The deep operational knobs of a storage — sync mode and interval, RBAC,
 * sync runs, drift reports — stay on the admin-only Storages pages. Those
 * are console features, not connection features, and they have no meaning
 * in a desktop file manager.
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';

import { ConnectionsPanel, type ExplorerConfig } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';

import { explorerAuth } from '@/lib/explorerConfig';
import { effectiveTheme } from '@/lib/theme';
import { useStoragesStore } from '@/stores/storages';

const { t, locale } = useI18n();
const router = useRouter();
const storages = useStoragesStore();

// The panel is theme-aware but has no idea the admin shell toggles `.dark`
// on <html>; hand it the resolved answer, like Explore.vue does.
const currentTheme = ref<'light' | 'dark'>(effectiveTheme());
let htmlObserver: MutationObserver | null = null;
onMounted(() => {
  htmlObserver = new MutationObserver(() => {
    currentTheme.value = document.documentElement.classList.contains('dark') ? 'dark' : 'light';
  });
  htmlObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
});
onBeforeUnmount(() => htmlObserver?.disconnect());

const config = computed<ExplorerConfig>(() => ({
  // Same-origin: the Go binary serves this SPA and the API.
  apiBase: '',
  endpoint: '/api/files/manager',
  auth: explorerAuth(),
  theme: currentTheme.value,
  locale: locale.value === 'en' ? 'en' : 'tr',
}));

/** A storage added or removed here changes the admin store the rest of the
 *  panel reads (the dashboard counts, the explorer roots). */
function onChanged() {
  void storages.fetch().catch(() => {});
}
</script>

<template>
  <div class="max-w-4xl space-y-4">
    <div class="flex items-start justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold">{{ t('nav.connections') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('connections.subtitle') }}</p>
      </div>
      <button
        type="button"
        class="text-sm text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 underline"
        @click="router.push({ name: 'storages' })"
      >
        {{ t('connections.advanced') }}
      </button>
    </div>

    <ConnectionsPanel :config="config" @changed="onChanged" />
  </div>
</template>
