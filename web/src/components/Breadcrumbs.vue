<script setup lang="ts">
import { computed } from 'vue';
import { useRoute, RouterLink } from 'vue-router';
import { ChevronRight } from 'lucide-vue-next';
import { useI18n } from 'vue-i18n';

const route = useRoute();
const { t } = useI18n();

interface Crumb {
  label: string;
  /** The route this crumb stands for — the leaf's is the current route. */
  name: string;
  to?: { name: string };
}

/**
 * Dashboard › parent › this page — with no step named twice.
 *
 * ⚠ The trail always starts at the dashboard, and the dashboard's own leaf is
 * the dashboard, so standing on it read "Dashboard › Dashboard" (v0.41.0
 * screenshot pass). A crumb that names the route the previous crumb already
 * names is dropped — which leaves the dashboard a single crumb and hides the
 * trail there, and also covers a page whose `meta.parent` is the dashboard.
 */
const crumbs = computed<Crumb[]>(() => {
  const out: Crumb[] = [{ label: t('nav.dashboard'), name: 'dashboard', to: { name: 'dashboard' } }];
  const parent = route.meta?.parent as string | undefined;
  if (parent) {
    // Best-effort label: nav.<parent> if it exists, else the route name itself.
    const key = `nav.${parent}`;
    out.push({ label: t(key, parent), name: parent, to: { name: parent } });
  }
  if (route.meta?.breadcrumb) {
    out.push({ label: t(route.meta.breadcrumb as string), name: String(route.name ?? '') });
  }
  return out.filter((c, i) => i === 0 || c.name !== out[i - 1].name);
});

const single = computed(() => crumbs.value.length <= 1);
</script>

<template>
  <nav v-if="!single" class="flex items-center gap-1 text-xs text-zinc-500 dark:text-zinc-400">
    <template v-for="(c, i) in crumbs" :key="i">
      <ChevronRight v-if="i > 0" class="h-3 w-3 opacity-60" />
      <RouterLink
        v-if="c.to && i < crumbs.length - 1"
        :to="c.to"
        class="hover:text-brand-600 dark:hover:text-brand-400"
      >
        {{ c.label }}
      </RouterLink>
      <span
        v-else
        :class="i === crumbs.length - 1 ? 'font-medium text-zinc-700 dark:text-zinc-300' : ''"
      >
        {{ c.label }}
      </span>
    </template>
  </nav>
</template>
