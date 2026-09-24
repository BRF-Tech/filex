<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { RouterView, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { useAuthStore } from '@/stores/auth';
import Sidebar from './Sidebar.vue';
import TopNav from './TopNav.vue';
import Breadcrumbs from './Breadcrumbs.vue';
import PendingOpsTray from './PendingOpsTray.vue';

/* Below `lg` (1024px, the Tailwind breakpoint the sidebar's `lg:translate-x-0`
   uses) the sidebar is a DRAWER over the page, with a backdrop. It must start
   closed there: `ref(true)` opened it on top of every admin page a phone
   visited, and the backdrop then swallowed the first tap (2026-09-19, caught
   by cypress/e2e/41-users-crud at 700px). On a wide screen the ref is
   irrelevant to layout — the column is always shown — so `true` keeps the
   desktop exactly as it was. */
const WIDE = '(min-width: 1024px)';
const wideMq = typeof window !== 'undefined' && typeof window.matchMedia === 'function' ? window.matchMedia(WIDE) : null;
const sidebarOpen = ref(wideMq ? wideMq.matches : true);
const route = useRoute();
// Choosing a page from the drawer closes it — the page is what was asked for.
watch(() => route.fullPath, () => {
  if (wideMq && !wideMq.matches) sidebarOpen.value = false;
});
// Crossing the breakpoint: a drawer left open must not stay open when the
// window is narrowed (it would cover the page); widening restores the column.
function onWideChange(ev: MediaQueryListEvent) {
  sidebarOpen.value = ev.matches;
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

function toggleSidebar() {
  sidebarOpen.value = !sidebarOpen.value;
}
</script>

<template>
  <div class="min-h-screen bg-zinc-50 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 flex">
    <!-- Mobile backdrop when sidebar open -->
    <div
      v-if="sidebarOpen"
      class="fixed inset-0 z-30 bg-zinc-950/40 backdrop-blur-sm lg:hidden"
      @click="sidebarOpen = false"
    />

    <Sidebar
      :open="sidebarOpen"
      @close="sidebarOpen = false"
    />

    <div class="flex min-w-0 flex-1 flex-col lg:ps-64">
      <TopNav @toggle-sidebar="toggleSidebar" />

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
