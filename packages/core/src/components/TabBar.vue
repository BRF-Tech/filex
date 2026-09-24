<script setup lang="ts">
/**
 * TabBar — wiring:d1 the tab strip.
 *
 * Thin strip that sits ABOVE the toolbar. The host renders it only when
 * 2+ tabs exist (embed pixel-parity: a single tab shows nothing at all).
 * Purely presentational: every mutation is an emit; useTabs owns state.
 *
 * Interactions:
 *   click tab      → select
 *   ×/middle-click → close
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
  /**
   * gorunum:v5-panerow — the details (inspector) toggle lives in this row too.
   *
   * ⚠ Opt-in, and absent means NOT DRAWN rather than "closed": until the host
   * moves its own copy out of the breadcrumb row there would otherwise be two
   * of the same button on screen, which is the duplicate this wave removes.
   */
  inspectorEnabled?: boolean;
  /** true → the details toggle renders pressed (the panel is open). */
  inspectorOpen?: boolean;
  /**
   * gorunum:v5-panerow — hide the tabs themselves (and "+"), keeping the row.
   *
   * ⚠ True is not "no tabs exist", it is "this deployment does not offer
   * them": the `simple` profile drops tabs deliberately (#14 named them as
   * power-user chrome) but still needs this row, because the details toggle
   * lives in it. Without the flag the row could only be all-or-nothing, and
   * the ⓘ would have to keep a second home in the breadcrumb row for that one
   * profile — a control with two homes is how the two drift.
   *
   * ⚠⚠ Phrased as HIDE, not SHOW, and that is not taste. A prop declared
   * `showTabs?: boolean` compiles to a runtime Boolean prop, and Vue casts an
   * ABSENT Boolean prop to `false` — not to `undefined`. So `showTabs !== false`
   * read false for every host that passed nothing, and the strip rendered with
   * no tabs and no "+" at all. Measured in a real browser: `.fe-tabs__tab`
   * count went from 1 to 0 the moment the prop was added, in both themes at
   * both widths. Negative default = safe default.
   */
  hideTabs?: boolean;
}>();

const emit = defineEmits<{
  (e: 'select', id: string): void;
  (e: 'close', id: string): void;
  (e: 'new'): void;
  (e: 'reorder', from: number, to: number): void;
  (e: 'toggle-split'): void;
  (e: 'toggle-inspector'): void /* gorunum:v5-panerow */;
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
  // ⚠ The same guard the × has. `useTabs.closeTab()` refuses the last tab, so
  // middle-clicking it was already a no-op; leaving it live while the × is
  // hidden would be two doors to one verb disagreeing about whether it exists.
  if (props.tabs.length <= 1) return;
  emit('close', id);
}
</script>

<template>
  <div class="fe-tabs" role="tablist" :aria-label="t('tabs.strip')">
    <div v-if="!hideTabs" class="fe-tabs__scroll">
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
        <span class="fe-tabs__label"><bdi>{{ tab.label }}</bdi></span>
        <!-- ⚠ Not drawn when this is the only tab. Owner's decision,
             2026-09-13, verbatim: *"tek kalan tab'de x gözükmemeli, onu da
             kaldırırsın."* It is not a style choice — `useTabs.closeTab()`
             opens with `if (tabs.length <= 1) return null`, so at one tab the
             × has always been a control that cannot do anything. Drawing it
             was inviting a dead click. -->
        <button
          v-if="tabs.length > 1"
          type="button"
          class="fe-tabs__close"
          :aria-label="t('tabs.close')"
          :title="t('tabs.close')"
          @click.stop="emit('close', tab.id)"
        >×</button>
      </div>
    </div>
    <!-- ⚠ OUTSIDE the scrolling area. Inside it, "new tab" scrolled away with
         the tabs it creates: past a dozen tabs the + was off the right edge and
         the only way back to it was scrolling a strip most people do not know
         scrolls. Pinned here it is always where it was. -->
    <button
      v-if="!hideTabs"
      type="button"
      class="fe-tabs__new"
      :aria-label="t('tabs.new')"
      :title="t('tabs.new')"
      @click="emit('new')"
    >+</button>
    <!-- gorunum:v5-panerow — back in THIS row, where it was.
         ⚠ It spent one round teleported up into the breadcrumb row beside the
         details toggle. The reasoning was sound (that row is always drawn,
         this strip is `v-if`'d) and it was approved, but the owner looked at
         the result and rejected it: *"kanka split pane ve info butonunu alt
         kısımda bırak"*. He decides. The reachability problem the teleport was
         solving is real and is answered properly in the handover diff instead:
         this row becomes the WINDOW's row — tabs, +, split and the details
         toggle — and is drawn whenever any of the four has something to do,
         rather than only when a second tab exists. -->
    <button
      v-if="splitEnabled !== false"
      type="button"
      class="fe-tabs__split"
      :class="{ 'is-active': splitActive }"
      :aria-label="splitActive ? t('tabs.split_off') : t('tabs.split')"
      :title="splitActive ? t('tabs.split_off') : t('tabs.split')"
      :aria-pressed="splitActive ? 'true' : 'false'"
      data-testid="tabs-split"
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

    <!-- gorunum:v5-panerow — the details toggle's home in this row.
         ⚠ Rendered ONLY when the host passes `inspectorEnabled`, which nothing
         does yet: until the FileExplorer diff in the handover is applied, the
         ⓘ is still drawn in the breadcrumb row and this must stay silent, or
         there would be two of it. When the diff lands, `infoPanelToggle` is
         passed here and the subhead's copy is deleted in the same change. -->
    <button
      v-if="inspectorEnabled"
      type="button"
      class="fe-tabs__inspector"
      :class="{ 'is-active': inspectorOpen }"
      :aria-pressed="inspectorOpen ? 'true' : 'false'"
      :title="t('toolbar.inspector')"
      :aria-label="t('toolbar.inspector')"
      data-testid="tabs-inspector"
      @click="emit('toggle-inspector')"
    >
      <svg
        class="fe-ficon"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        aria-hidden="true"
        focusable="false"
      >
        <circle cx="12" cy="12" r="9" />
        <path d="M12 11v5" />
        <circle cx="12" cy="7.6" r="1" fill="currentColor" stroke="none" />
      </svg>
    </button>
  </div>
</template>
