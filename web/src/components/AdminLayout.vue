<script setup lang="ts">
import { ref } from 'vue';
import { RouterView } from 'vue-router';
import Sidebar from './Sidebar.vue';
import TopNav from './TopNav.vue';
import Breadcrumbs from './Breadcrumbs.vue';
import PendingOpsTray from './PendingOpsTray.vue';

const sidebarOpen = ref(true);

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

    <Sidebar :open="sidebarOpen" @close="sidebarOpen = false" />

    <div class="flex min-w-0 flex-1 flex-col lg:pl-64">
      <TopNav @toggle-sidebar="toggleSidebar" />

      <main class="flex-1 px-4 py-4 sm:px-6 lg:px-8">
        <div class="mx-auto max-w-7xl">
          <Breadcrumbs class="mb-3" />
          <RouterView v-slot="{ Component }">
            <transition name="fade" mode="out-in">
              <component :is="Component" />
            </transition>
          </RouterView>
        </div>
      </main>

      <footer class="px-4 sm:px-6 lg:px-8 py-3 border-t border-zinc-200 dark:border-zinc-800 text-xs text-zinc-500 dark:text-zinc-400">
        <div class="mx-auto max-w-7xl flex flex-wrap items-center justify-between gap-2">
          <span>filex · self-hosted file manager</span>
          <a
            href="https://github.com/brf-tech/filex"
            class="hover:text-brand-600 dark:hover:text-brand-400"
            target="_blank"
            rel="noopener"
            >github.com/brf-tech/filex</a
          >
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
