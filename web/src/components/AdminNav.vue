<script setup lang="ts">
/**
 * The admin panel's navigation - core's MegaMenu, bound to THIS app.
 *
 * ⚠⚠ Not a second menu. The drawing, the keyboard and the screen-reader
 * wiring are `@brftech/filex-core`'s `MegaMenu`; what the pages are and how
 * they are grouped is `lib/adminNav`. What neither can know about this app -
 * the router (addresses, the current page, the guard's question per page),
 * the reader's role and the instance's capabilities, the installed apps, and
 * vue-i18n - is bound once in `composables/useAdminNav`, which the panel's
 * search reads too (task #168); this file adds the trash's full bin and the
 * click.
 *
 * Drawn twice by the layout, never at once: `bar` in the top bar on a wide
 * screen (components/TopNav.vue), `list` in the drawer below 1024px
 * (components/NavDrawer.vue).
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { MegaMenu } from '@brftech/filex-core';

import { trashApi } from '@/api/trash';
import { useAdminNav } from '@/composables/useAdminNav';

withDefaults(defineProps<{ mode?: 'bar' | 'list' }>(), { mode: 'bar' });
const emit = defineEmits<{ (e: 'navigated'): void }>();

const { t } = useI18n();
const route = useRoute();
const router = useRouter();

// The trash row wears a full bin when there is something in it. Refreshed on
// mount and on every navigation (after emptying or restoring); best-effort,
// the empty bin when the question fails.
const trashCount = ref(0);
async function refreshTrash(): Promise<void> {
  try {
    trashCount.value = (await trashApi.list({ limit: 1 })).total;
  } catch {
    /* keep the empty bin */
  }
}
onMounted(refreshTrash);
watch(() => route.name, refreshTrash);

/* The menu as this reader sees it - shared with the panel's search
   (composables/useAdminNav), so the two cannot disagree about a page. */
const { entries, routeFor } = useAdminNav({ trashFull: computed(() => trashCount.value > 0) });

function onNavigate(target: { id: string; href: string }): void {
  const to = routeFor(target.href);
  if (!to) return;
  void router.push(to);
  emit('navigated');
}
</script>

<template>
  <MegaMenu
    :entries="entries"
    :label="t('nav.adminMenu')"
    :mode="mode"
    @navigate="onNavigate"
  />
</template>
