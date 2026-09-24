<script setup lang="ts">
/**
 * Connections — storage connections and "how to connect", in the admin
 * panel.
 *
 * ⚠ There is no form on this page. It mounts `ConnectionsPanel` from
 * `@brftech/filex-core`, which is the same component the desktop app
 * mounts as `<filex-connections>`; the client instructions are generated
 * from the live deployment. Anything that needs fixing here gets fixed
 * once, in the package, and lands on every surface.
 *
 * ⚠ And there is no storage LIST either, since v0.43.0. The panel used to
 * open on a "Storages" tab holding a poorer copy of the Storages pages —
 * the owner, testing the release: "ikisinde de depolar gözüküyor bu
 * depolar sekmesine hiç ihtiyaç yok". Storages are created, edited and
 * deleted on the Storages pages, which also hold the knobs this page never
 * had (sync mode and interval, RBAC, sync runs, drift reports). The link in
 * the header is the door; do not grow a second one here.
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';

import { ConnectionsPanel, type ExplorerConfig } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';

import { explorerAuth } from '@/lib/explorerConfig';
import { effectiveTheme } from '@/lib/theme';

const { t, locale } = useI18n();
const router = useRouter();

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
  locale: locale.value, // the active language — a language pack's too
}));
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

    <ConnectionsPanel :config="config" />
  </div>
</template>
