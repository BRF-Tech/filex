<script setup lang="ts">
/**
 * The admin panel's navigation - core's MegaMenu, bound to THIS app.
 *
 * ⚠⚠ Not a second menu. The drawing, the keyboard and the screen-reader
 * wiring are `@brftech/filex-core`'s `MegaMenu`; what the pages are and how
 * they are grouped is `lib/adminNav`. What stays here is only what neither
 * can know about this app: the router (addresses, the current page, the
 * guard's question per page), the reader's role and the instance's
 * capabilities, the installed apps, and vue-i18n.
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
import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { usePluginHomeApps } from '@/composables/usePluginHomeApps';
import { buildAdminNav, pageOpenTo } from '@/lib/adminNav';

withDefaults(defineProps<{ mode?: 'bar' | 'list' }>(), { mode: 'bar' });
const emit = defineEmits<{ (e: 'navigated'): void }>();

const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const auth = useAuthStore();
const caps = useCapabilitiesStore();
const { apps } = usePluginHomeApps();

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

/* Every address the menu was given, back to the route it was made from, so a
   click is pushed by NAME - nothing here takes an address apart. */
const routeOf = new Map<string, { name: string; params?: Record<string, string> }>();

const entries = computed(() =>
  buildAdminNav({
    t: (key) => t(key),
    open: (to) => pageOpenTo(router.resolve(to).meta, { isAdmin: auth.isAdmin, can: (p) => auth.can(p) }),
    href: (to) => {
      const href = router.resolve(to).href;
      routeOf.set(href, to);
      return href;
    },
    current: {
      name: String(route.name ?? ''),
      params: route.params,
      parent: typeof route.meta?.parent === 'string' ? route.meta.parent : undefined,
    },
    // A multi-tenant install (the sign-in form has a Realm): the operator who
    // may configure the instance runs Tenants, a tenant's own administrator
    // runs My tenant (docs/TENANT-ADMIN.md).
    tenants: caps.data.realm?.enabled === true && caps.data.caller_admin === true,
    tenantSelf: caps.data.realm?.enabled === true && caps.data.caller_admin !== true && auth.isAdmin,
    // An administrator's alone, as the old sidebar had it: an app's screen
    // is the role's page (the route names no admin.* permission).
    apps: auth.isAdmin ? apps.value : [],
    trashFull: trashCount.value > 0,
  }),
);

function onNavigate(target: { id: string; href: string }): void {
  const to = routeOf.get(target.href);
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
