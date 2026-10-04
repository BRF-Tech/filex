<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { RouterView, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { useAuthStore } from '@/stores/auth';
import NavDrawer from './NavDrawer.vue';
import TopNav from './TopNav.vue';
import Breadcrumbs from './Breadcrumbs.vue';
import PendingOpsTray from './PendingOpsTray.vue';

/* The menu (GitHub #82, 0.51.0): from `lg` (1024px) up it is the top bar's
   mega menu (TopNav → AdminNav `bar`); below it, a DRAWER over the page with
   a backdrop (NavDrawer → AdminNav `list`). Only one of the two is mounted,
   so a page link exists once in the document and a screen reader is not
   offered the same menu twice.
   ⚠ The drawer starts CLOSED: an open one on top of every admin page a phone
   visited had its backdrop swallow the first tap (2026-09-19, caught by
   cypress/e2e/41-users-crud at 700px). */
const WIDE = '(min-width: 1024px)';
const wideMq = typeof window !== 'undefined' && typeof window.matchMedia === 'function' ? window.matchMedia(WIDE) : null;
const wide = ref(wideMq ? wideMq.matches : true);
const drawerOpen = ref(false);
const route = useRoute();
// Choosing a page from the drawer closes it — the page is what was asked for.
watch(() => route.fullPath, () => {
  drawerOpen.value = false;
});
// Crossing the breakpoint swaps the menus; a drawer left open must not
// reappear open the next time the window is narrowed.
function onWideChange(ev: MediaQueryListEvent) {
  wide.value = ev.matches;
  drawerOpen.value = false;
}
onMounted(() => wideMq?.addEventListener('change', onWideChange));
onBeforeUnmount(() => wideMq?.removeEventListener('change', onWideChange));
// ⚠ Said once, at the layout, rather than on each page: a visitor who is
// refused a save should already know why before they try it. The refusal
// itself is the server's (api/demo_guard.go) — this is the sign on the door.
const caps = useCapabilitiesStore();
// The public-URL sign is for the person who can fix it. A viewer cannot set
// an environment variable, and a warning they cannot act on is noise.
const auth = useAuthStore();
const { t } = useI18n();

function toggleDrawer() {
  drawerOpen.value = !drawerOpen.value;
}
</script>

<template>
  <!-- The ground and the ink are the THEME's (`--fe-bg-elev` under the
       `--fe-bg` cards, as in the explorer), not Tailwind's zinc pair: an
       operator theme paints the admin pages' ground too (#74). -->
  <div class="min-h-screen bg-[var(--fe-bg-elev)] text-[var(--fe-text)] flex">
    <template v-if="!wide">
      <!-- The drawer's backdrop: a tap on the page closes the menu. -->
      <div
        v-if="drawerOpen"
        class="fixed inset-0 z-30 bg-zinc-950/40 backdrop-blur-sm"
        data-testid="nav-drawer-backdrop"
        @click="drawerOpen = false"
      />
      <NavDrawer
        :open="drawerOpen"
        @close="drawerOpen = false"
      />
    </template>

    <div class="flex min-w-0 flex-1 flex-col">
      <TopNav
        :wide="wide"
        :drawer-open="drawerOpen"
        @toggle-drawer="toggleDrawer"
      />

      <main class="flex-1 px-4 py-4 sm:px-6 lg:px-8">
        <div class="mx-auto max-w-7xl">
          <p
            v-if="caps.demoReadOnly"
            class="mb-3 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-700/60 dark:bg-amber-950/40 dark:text-amber-200"
          >
            {{ t('demo.readOnly') }}
          </p>
          <p
            v-if="auth.isAdmin && caps.publicUrlUnset"
            class="mb-3 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-700/60 dark:bg-amber-950/40 dark:text-amber-200"
            data-testid="public-url-unset"
          >
            {{ t('instance.publicUrlUnset') }}
            <a
              href="https://docs.filex.sh/CONFIGURATION#public-url"
              target="_blank"
              rel="noopener"
              class="underline"
            >{{ t('instance.publicUrlDocs') }}</a>
          </p>
          <Breadcrumbs class="mb-3" />
          <RouterView v-slot="{ Component }">
            <transition
              name="fade"
              mode="out-in"
            >
              <component :is="Component" />
            </transition>
          </RouterView>
        </div>
      </main>

      <footer class="px-4 sm:px-6 lg:px-8 py-3 border-t border-zinc-200 dark:border-zinc-800 text-xs text-zinc-500 dark:text-zinc-400">
        <div class="mx-auto max-w-7xl flex flex-wrap items-center justify-between gap-2">
          <span>{{ t('app.footer') }}</span>
          <a
            href="https://github.com/BRF-Tech/filex"
            class="hover:text-brand-600 dark:hover:text-brand-400"
            target="_blank"
            rel="noopener"
          >github.com/BRF-Tech/filex</a>
        </div>
      </footer>
    </div>

    <!-- Bottom-right consolidated progress for async copy/move/delete ops.
      Mounted at the layout level so it's visible from every admin page,
      not just /admin/explore. See `PendingOpsTray.vue` + `pendingOps`
      store for polling details. -->
    <PendingOpsTray />
  </div>
</template>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 100ms ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
