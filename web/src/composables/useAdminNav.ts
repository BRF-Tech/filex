/**
 * The admin menu as THIS reader sees it, bound to this app: one computation
 * for every place that draws the menu (components/AdminNav.vue - the top
 * bar and the phone's drawer) or searches it (components/AdminSearch.vue,
 * task #168).
 *
 * ⚠⚠ Why it is shared: a page the menu does not offer must not turn up in
 * the search either. Who sees which page is lib/adminNav's rule
 * (`buildAdminNav`, `pageOpenTo`) plus the tenant switches below; binding it
 * to the router and the stores in ONE place is what lets the search read the
 * very entries the menu draws, rather than a second copy of the conditions
 * that would drift from the first.
 */
import { computed } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';

import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { usePluginHomeApps } from '@/composables/usePluginHomeApps';
import { useTenancy } from '@/composables/useTenancy';
import { buildAdminNav, pageOpenTo } from '@/lib/adminNav';

/** A route the menu links to, by name (a click is pushed by name). */
export interface AdminNavRoute {
  name: string;
  params?: Record<string, string>;
}

export function useAdminNav(opts: { trashFull?: { readonly value: boolean } } = {}) {
  const { t } = useI18n();
  const route = useRoute();
  const router = useRouter();
  const auth = useAuthStore();
  const caps = useCapabilitiesStore();
  const { apps } = usePluginHomeApps();
  const tenancy = useTenancy();

  /* Every address the menu was given, back to the route it was made from, so a
     click is pushed by NAME - nothing here takes an address apart. */
  const routeOf = new Map<string, AdminNavRoute>();

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
      // A multi-tenant install (composables/useTenancy, the one answer): the
      // operator who may configure the instance runs Tenants, a tenant's own
      // administrator runs My tenant (docs/TENANT-ADMIN.md). Off, neither.
      tenants: tenancy.operator.value,
      tenantSelf: tenancy.tenantAdmin.value,
      // The Multi-tenant mode switch: whoever may configure the instance, on
      // every install (it is where the mode is turned on).
      platform: caps.data.caller_admin === true,
      // An administrator's alone, as the old sidebar had it: an app's screen
      // is the role's page (the route names no admin.* permission).
      apps: auth.isAdmin ? apps.value : [],
      trashFull: opts.trashFull?.value === true,
    }),
  );

  /** The route an address in `entries` was made from. */
  function routeFor(href: string): AdminNavRoute | undefined {
    return routeOf.get(href);
  }

  return { entries, routeFor };
}
