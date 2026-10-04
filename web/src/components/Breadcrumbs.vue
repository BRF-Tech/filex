<script setup lang="ts">
import { computed } from 'vue';
import { useRoute, useRouter, RouterLink } from 'vue-router';
import { ChevronRight } from 'lucide-vue-next';
import { useI18n } from 'vue-i18n';

import { adminNavPages, adminNavSectionOf } from '@/lib/adminNav';

const route = useRoute();
const router = useRouter();
const { t } = useI18n();

interface Crumb {
  label: string;
  /** The route this crumb stands for — the leaf's is the current route. */
  name: string;
  to?: { name: string };
}

/**
 * Dashboard › section › parent › this page — with no step named twice.
 *
 * The SECTION is where the page lives in the menu (lib/adminNav, GitHub #82):
 * "Dashboard › People & access › Users". It is words, not a link - a section
 * is a heading in a menu panel, not a page anybody could be taken to (owner's
 * choice, 2026-10-03). A sub-page takes its parent's section.
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
  const placed = adminNavSectionOf(parent ?? String(route.name ?? ''));
  if (placed) out.push({ label: t(placed.section.label), name: `section:${placed.section.id}` });
  if (parent) {
    // The parent's label is the menu's label for it (`admin-files` is "File
    // history", not `nav.admin-files`); nav.<parent> for a parent the menu
    // does not carry, else the route name itself.
    const key = adminNavPages().find((p) => p.route === parent)?.label ?? `nav.${parent}`;
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
        :data-testid="c.name.startsWith('section:') ? 'crumb-section' : undefined"
      >
        {{ c.label }}
      </span>
    </template>
  </nav>
</template>
