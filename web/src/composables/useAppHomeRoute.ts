/**
 * useAppHomeRoute — what BOTH pages that draw an app's `home` view read from
 * the address: the explorer's own page (`views/AppScreen.vue`, `/app/…`) and
 * the admin panel's (`views/AppHome.vue`, `/apps/…/home/…`).
 *
 * ⚠ One reading, not two. The section of the page on screen is `?section=`,
 * pushed as history so Back walks the sections (the owner, 2026-09-21); the
 * heading and icon come from the same "Apps" row the side bar drew. Written
 * once so the two pages cannot come to disagree about any of it — the
 * duplication gate caught the second copy the day it was written.
 */
import { computed } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';

import { effectiveTheme } from '@/lib/theme';
import { usePluginHomeApps } from '@/composables/usePluginHomeApps';

export function useAppHomeRoute() {
  const route = useRoute();
  const router = useRouter();
  const { locale } = useI18n();

  const plugin = computed(() => String(route.params.plugin ?? ''));
  const view = computed(() => String(route.params.view ?? ''));
  const section = computed(() => (typeof route.query.section === 'string' ? route.query.section : ''));

  /**
   * A section was chosen: it becomes the address, as a step in history. The
   * one the page LANDED on (`replace`) is written without a step, so Back
   * from the first section leaves the page instead of undoing a redirect.
   */
  function onSection(id: string, how?: { replace?: boolean }): void {
    if (id === section.value) return;
    const to = { query: { ...route.query, section: id } };
    void (how?.replace ? router.replace(to) : router.push(to));
  }

  const { find } = usePluginHomeApps();
  /** The side bar's row for this page — it may still be loading. */
  const row = computed(() => find(plugin.value, view.value));
  // Reactive on purpose: a cold deep link (a notification, a reload) paints
  // before the actions list answers, and the heading fills itself in rather
  // than staying the view id.
  const title = computed(() => row.value?.label || view.value);
  const icon = computed(() => row.value?.svg ?? '');

  // The active language, a language pack's included (the app's OWN words fall
  // back to English by themselves when it does not speak it — feat/043-lang).
  const uiLocale = computed<string>(() => locale.value);
  const theme = computed(() => effectiveTheme());

  return { plugin, view, section, onSection, title, icon, uiLocale, theme };
}
