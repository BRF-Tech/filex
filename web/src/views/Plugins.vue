<script setup lang="ts">
/**
 * Plugins — two kinds of thing filex can be extended with, one page:
 *
 *   • Storage plugins: drivers outside the binary (components/plugins/
 *     StoragePluginsTab.vue — the page as it was before Apps existed).
 *   • Apps: WebAssembly plugins that add rows to the file menu and run as
 *     ops jobs (components/plugins/AppPluginsTab.vue, docs/APP-PLUGINS-API.md).
 *
 * Both tabs stay mounted (`v-show`), so each loads exactly as it did as a
 * page of its own. The Apps tab opens first when the runtime is on and at
 * least one app is installed — the tab tells this shell once it has loaded;
 * a click by the operator always wins over that default.
 */
import { ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';

import StoragePluginsTab from '@/components/plugins/StoragePluginsTab.vue';
import AppPluginsTab from '@/components/plugins/AppPluginsTab.vue';

type Tab = 'storage' | 'apps';

const { t } = useI18n();
const route = useRoute();
const router = useRouter();

/**
 * The tab is in the address (`?tab=apps`), so coming BACK from an app's own
 * page (views/AppPluginPage.vue) lands on the Apps tab it was opened from,
 * whichever tab the default rule below would pick.
 */
const fromAddress: Tab | null = route.query.tab === 'apps' || route.query.tab === 'storage' ? route.query.tab : null;
const activeTab = ref<Tab>(fromAddress ?? 'storage');
const chosen = ref(fromAddress !== null);

function setTab(tab: Tab) {
  activeTab.value = tab;
  chosen.value = true;
  void router.replace({ query: { ...route.query, tab } });
}

function onAppsLoaded(info: { enabled: boolean; count: number }) {
  if (chosen.value) return;
  if (info.enabled && info.count > 0) activeTab.value = 'apps';
}
</script>

<template>
  <div class="space-y-4">
    <nav class="flex gap-1 border-b border-zinc-200 dark:border-zinc-800" role="tablist">
      <button
        v-for="tab in (['storage', 'apps'] as Tab[])"
        :key="tab"
        type="button"
        role="tab"
        :aria-selected="activeTab === tab"
        :data-testid="`plugins-tab-${tab}`"
        class="-mb-px border-b-2 px-3 py-2 text-sm font-medium transition"
        :class="activeTab === tab
          ? 'border-brand-600 text-brand-600 dark:border-brand-400 dark:text-brand-400'
          : 'border-transparent text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100'"
        @click="setTab(tab)"
      >
        {{ t(`appPlugins.tabs.${tab}`) }}
      </button>
    </nav>

    <div v-show="activeTab === 'storage'" role="tabpanel">
      <StoragePluginsTab />
    </div>
    <div v-show="activeTab === 'apps'" role="tabpanel">
      <AppPluginsTab @loaded="onAppsLoaded" />
    </div>
  </div>
</template>
