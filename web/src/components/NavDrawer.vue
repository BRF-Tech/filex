<script setup lang="ts">
/**
 * The admin menu on a narrow screen (below 1024px): a drawer from the start
 * edge, holding the same menu as the top bar drawn as a headed list
 * (`AdminNav` in `list` mode). Every group is open, so a page is two taps
 * away - the menu button, then the page (owner's choice, 2026-10-03).
 *
 * It replaced the old sidebar, which was this drawer on a phone and a fixed
 * column on a wide screen; the wide screen's menu is the top bar's now.
 */
import { nextTick, ref, watch } from 'vue';
import { RouterLink } from 'vue-router';
import { X } from 'lucide-vue-next';

import LogoMark from './LogoMark.vue';
import AdminNav from './AdminNav.vue';

const props = defineProps<{ open: boolean }>();
const emit = defineEmits<{ (e: 'close'): void }>();

/* The drawer opens on the page being looked at, not at the top of a list of
   thirty-odd pages: the current one is brought into view as it opens. */
const scroller = ref<HTMLElement | null>(null);
watch(
  () => props.open,
  async (open) => {
    if (!open) return;
    await nextTick();
    const here = scroller.value?.querySelector<HTMLElement>('[aria-current="page"]');
    if (here && typeof here.scrollIntoView === 'function') here.scrollIntoView({ block: 'nearest' });
  },
);
</script>

<template>
  <aside
    id="admin-nav-drawer"
    :class="[
      'fixed inset-y-0 start-0 z-40 w-72 max-w-[85vw] transform bg-[var(--fe-bg)] border-e border-[var(--fe-border)] transition-transform',
      // ⚠ RTL: closed = pushed off the START edge, which is the right one there.
      open ? 'translate-x-0' : '-translate-x-full rtl:translate-x-full',
    ]"
    :inert="!open || undefined"
    data-testid="nav-drawer"
  >
    <div class="flex h-full flex-col">
      <div class="flex items-center justify-between gap-2 border-b border-[var(--fe-border)] px-4 h-14">
        <RouterLink :to="{ name: 'dashboard' }" class="flex items-center gap-2" @click="emit('close')">
          <LogoMark class="h-7 w-7" />
          <div class="flex flex-col leading-tight">
            <span class="text-sm font-semibold text-[var(--fe-text)]">filex</span>
            <span class="text-[10px] text-[var(--fe-text-muted)] uppercase tracking-wide">
              {{ $t('app.admin') }}
            </span>
          </div>
        </RouterLink>
        <button
          type="button"
          class="rounded p-1 text-[var(--fe-text-muted)] hover:bg-[var(--fe-bg-hover)]"
          :aria-label="$t('common.close')"
          @click="emit('close')"
        >
          <X class="h-5 w-5" />
        </button>
      </div>

      <div ref="scroller" class="flex-1 overflow-y-auto px-2 py-3">
        <AdminNav mode="list" @navigated="emit('close')" />
      </div>
    </div>
  </aside>
</template>
