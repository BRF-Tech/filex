<script setup lang="ts">
/**
 * Tools: maintenance an administrator runs on purpose, one tab per tool
 * (components/tools/registry.ts). The open tab is in the address
 * (`?tab=thumbnails`), so a reload or a link lands on it.
 */
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { Wrench } from 'lucide-vue-next';

import { TOOLS } from '@/components/tools/registry';

const { t } = useI18n();
const route = useRoute();
const router = useRouter();

const known = (v: unknown): v is string => typeof v === 'string' && TOOLS.some((x) => x.id === v);
const active = ref<string>(known(route.query.tab) ? route.query.tab : TOOLS[0].id);
const current = computed(() => TOOLS.find((x) => x.id === active.value) ?? TOOLS[0]);

function setTab(id: string) {
  active.value = id;
  void router.replace({ query: { ...route.query, tab: id } });
}
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-center gap-2">
      <Wrench class="h-6 w-6 text-brand-600 dark:text-brand-400" />
      <h1 class="text-xl font-semibold">{{ t('tools.title') }}</h1>
    </header>
    <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('tools.subtitle') }}</p>

    <nav class="flex gap-1 border-b border-zinc-200 dark:border-zinc-800" role="tablist">
      <button
        v-for="tool in TOOLS"
        :key="tool.id"
        type="button"
        role="tab"
        :aria-selected="active === tool.id"
        :data-testid="`tools-tab-${tool.id}`"
        class="-mb-px border-b-2 px-3 py-2 text-sm font-medium transition"
        :class="active === tool.id
          ? 'border-brand-600 text-brand-600 dark:border-brand-400 dark:text-brand-400'
          : 'border-transparent text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100'"
        @click="setTab(tool.id)"
      >
        {{ t(`tools.tabs.${tool.id}`) }}
      </button>
    </nav>

    <div role="tabpanel" :data-testid="`tools-panel-${current.id}`">
      <component :is="current.component" />
    </div>
  </section>
</template>
