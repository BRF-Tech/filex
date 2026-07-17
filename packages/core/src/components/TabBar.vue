<script setup lang="ts">
/**
 * TabBar — wiring:d1 sekme şeridi.
 *
 * Thin strip that sits ABOVE the toolbar. The host renders it only when
 * 2+ tabs exist (embed pixel-parity: a single tab shows nothing at all).
 * Purely presentational: every mutation is an emit; useTabs owns state.
 *
 * Interactions:
 *   click tab      → select
 *   × / middle-tık → close
 *   +              → new tab (clone of the current location)
 *   drag           → reorder (HTML5 DnD; live-swap while dragging)
 *   ⫿ (split)      → toggle the active tab's secondary pane
 *
 * UNSCOPED styles (`fe-tabs*` in base.css) — the webcomponent build
 * cannot carry scoped data-v hashes (v0.5.0 discovery).
 */
import { ref } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';

export interface TabItem {
  id: string;
  label: string;
  /** true → the tab carries a split pane (small accent dot). */
  split: boolean;
}

const props = defineProps<{
  tabs: TabItem[];
  activeId: string;
  locale: LocaleCode;
  /** false → the split toggle is hidden (narrow embeds). */
  splitEnabled?: boolean;
  /** true → the split toggle renders pressed (active tab has a pane). */
  splitActive?: boolean;
}>();

const emit = defineEmits<{
  (e: 'select', id: string): void;
  (e: 'close', id: string): void;
  (e: 'new'): void;
  (e: 'reorder', from: number, to: number): void;
  (e: 'toggle-split'): void;
}>();

const { t } = useLocale(() => props.locale);

// ---- drag-sort (HTML5 DnD, live swap) ------------------------------
const dragIndex = ref<number | null>(null);
const overIndex = ref<number | null>(null);

function onDragStart(i: number, ev: DragEvent) {
  dragIndex.value = i;
  if (ev.dataTransfer) {
    ev.dataTransfer.effectAllowed = 'move';
    // Firefox requires SOME payload for a drag to start. Deliberately not
    // the internal file-DnD MIME, so the explorer's drop surfaces ignore it.
    ev.dataTransfer.setData('text/plain', props.tabs[i]?.id ?? '');
  }
}

function onDragOver(i: number, ev: DragEvent) {
  if (dragIndex.value === null) return;
  ev.preventDefault();
  ev.stopPropagation();
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
  overIndex.value = i;
}

function onDragLeave(i: number) {
  if (overIndex.value === i) overIndex.value = null;
}

function onDrop(i: number, ev: DragEvent) {
  if (dragIndex.value === null) return;
  ev.preventDefault();
  ev.stopPropagation();
  if (dragIndex.value !== i) emit('reorder', dragIndex.value, i);
  dragIndex.value = null;
  overIndex.value = null;
}

function onDragEnd() {
  dragIndex.value = null;
  overIndex.value = null;
}

// Middle-click closes a tab (browser convention). The mousedown guard
// cancels Chromium's autoscroll on the scrollable strip — without it the
// auxclick event is never generated.
function onTabMiddleDown(ev: MouseEvent) {
  if (ev.button === 1) ev.preventDefault();
}
function onAux(id: string, ev: MouseEvent) {
  if (ev.button !== 1) return;
  ev.preventDefault();
  ev.stopPropagation();
  emit('close', id);
}
</script>

<template>
  <div class="fe-tabs" role="tablist" :aria-label="t('tabs.strip')">
    <div class="fe-tabs__scroll">
      <div
        v-for="(tab, i) in tabs"
        :key="tab.id"
        class="fe-tabs__tab"
        :class="{
          'is-active': tab.id === activeId,
          'is-dragover': overIndex === i && dragIndex !== null && dragIndex !== i,
        }"
        role="tab"
        :aria-selected="tab.id === activeId ? 'true' : 'false'"
        tabindex="0"
        draggable="true"
        :title="tab.label"
        @click="emit('select', tab.id)"
        @keydown.enter.prevent="emit('select', tab.id)"
        @mousedown="onTabMiddleDown($event)"
        @auxclick="onAux(tab.id, $event)"
        @dragstart="onDragStart(i, $event)"
        @dragover="onDragOver(i, $event)"
        @dragleave="onDragLeave(i)"
        @drop="onDrop(i, $event)"
        @dragend="onDragEnd"
      >
        <span v-if="tab.split" class="fe-tabs__splitdot" aria-hidden="true"></span>
        <span class="fe-tabs__label">{{ tab.label }}</span>
        <button
          type="button"
          class="fe-tabs__close"
          :aria-label="t('tabs.close')"
          :title="t('tabs.close')"
          @click.stop="emit('close', tab.id)"
        >×</button>
      </div>
      <button
        type="button"
        class="fe-tabs__new"
        :aria-label="t('tabs.new')"
        :title="t('tabs.new')"
        @click="emit('new')"
      >+</button>
    </div>
    <button
      v-if="splitEnabled !== false"
      type="button"
      class="fe-tabs__split"
      :class="{ 'is-active': splitActive }"
      :aria-label="splitActive ? t('tabs.split_off') : t('tabs.split')"
      :title="splitActive ? t('tabs.split_off') : t('tabs.split')"
      :aria-pressed="splitActive ? 'true' : 'false'"
      @click="emit('toggle-split')"
    >
      <svg
        class="fe-ficon"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
        focusable="false"
      >
        <rect x="3" y="4" width="18" height="16" rx="2" />
        <path d="M12 4v16" />
      </svg>
    </button>
  </div>
</template>
