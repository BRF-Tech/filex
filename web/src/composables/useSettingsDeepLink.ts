/**
 * `?settings=1` opens the person's settings dialog — the deep link that
 * replaced the retired /admin/profile page.
 *
 * ⚠ It has to exist, and it has to be on a route rather than a button: the
 * server prints where to change the first-run password into the startup
 * banner AND into `<data>/.first-run.txt`, a file that is already sitting on
 * installs in the field saying `/admin/profile`. A dialog reachable only by
 * clicking an avatar cannot be named in either place. The parameter is
 * stripped the moment it is honoured, so a reload or a shared URL does not
 * reopen it.
 *
 * ⚠⚠ Honoured by BOTH chromes that own the dialog: the admin panel's top nav
 * (TopNav.vue) and the explorer page (Explore.vue — Home and Files, the front
 * door of every role). It lived in TopNav alone, which is admin-only, so the
 * address worked for an operator and bounced everybody else; the desktop app's
 * avatar opens "User settings" in the browser through exactly this address
 * (`/drive/home?settings=1`, 2026-09-27) and needs it to work for every person.
 * One watcher, here, rather than the same six lines in two components.
 */
import { watch, type Ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';

export function useSettingsDeepLink(open: Ref<boolean>): void {
  const route = useRoute();
  const router = useRouter();
  watch(
    () => route.query.settings,
    (v) => {
      if (v === undefined || v === null) return;
      open.value = true;
      const { settings: _drop, ...rest } = route.query;
      void router.replace({ path: route.path, query: rest, hash: route.hash });
    },
    { immediate: true },
  );
}
