<script setup lang="ts">
/**
 * Toolbar — selection-aware action row.
 *
 * Layout:
 *   - No selection            → [📁 Yeni Klasör]   [search | ⬆ ↻]   [☰ ▦]
 *   - 1 file selected         → [👁 ⬇ 🔗 ✎ 🗑]    [search | ⬆ ↻]   [☰ ▦]
 *   - 1 folder selected       → [↗ ✎ 🗑]          [search | ⬆ ↻]   [☰ ▦]
 *   - Multi selection         → [✂ ❐ 🗑]          [search | ⬆ ↻]   [☰ ▦]
 *
 * Presentational only — all logic (rename, share, …) lives in
 * FileExplorer.vue, which listens for `action` emits.
 */
import { computed, ref, watch } from 'vue';
import type { ViewMode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { ContextAction } from './ContextMenu.vue';
import { useLocale } from '../composables/useLocale';

export type SelectionMode = 'none' | 'single-file' | 'single-dir' | 'multi';

const props = defineProps<{
  viewMode: ViewMode;
  searchQuery: string;
  /**
   * Tells the toolbar which action set to render (a `.trash/` listing
   * gets a restore-only menu instead of cut/copy/etc).
   */
  trashActive: boolean;
  /**
   * The action list to render, supplied by FileExplorer. This is the SAME
   * list the right-click context menu renders for the current selection, so
   * the two menus can never drift apart. Dividers/hidden entries are filtered
   * out here (the toolbar is a flat row).
   */
  actions: ContextAction[];
  /** Current selection metadata — toolbar uses this to pick its mode. */
  selectionMode?: SelectionMode;
  /** True when clipboard has cut/copy items, so we can enable Paste. */
  pasteEnabled?: boolean;
  /** True when the universal converter (FILEX_CONVERT_URL) is available. */
  convertEnabled?: boolean;
  /**
   * True when the current dir has a parent the user can step up to.
   * Hidden at storage root because there's nothing above it.
   */
  canGoUp?: boolean;
  /**
   * Multi-storage virtual root marker — when true, mutation buttons
   * (New Folder / Upload / Paste) are hidden because there's no real
   * backend folder to write to.
   */
  atVirtualRoot?: boolean;
  /**
   * RBAC: false hides the write affordances (New Folder / Upload / Paste) when
   * the current user lacks edit on this directory. Undefined = no RBAC gating
   * (backward-compatible for embedders that don't pass it).
   */
  canWrite?: boolean;
  locale: LocaleCode;
}>();

const emit = defineEmits<{
  (e: 'update:viewMode', v: ViewMode): void;
  (e: 'update:searchQuery', v: string): void;
  (e: 'new-folder'): void;
  (e: 'upload'): void;
  (e: 'refresh'): void;
  (e: 'go-up'): void;
  (e: 'action', key: string): void;
  (e: 'open-recents'): void;
}>();

const { t } = useLocale(() => props.locale);

const searchEl = ref<HTMLInputElement | null>(null);
const localSearch = ref(props.searchQuery);

watch(() => props.searchQuery, (v) => {
  localSearch.value = v;
});

let debounce: ReturnType<typeof setTimeout> | undefined;
function onSearchInput(ev: Event) {
  const v = (ev.target as HTMLInputElement).value;
  localSearch.value = v;
  if (debounce) clearTimeout(debounce);
  debounce = setTimeout(() => emit('update:searchQuery', v), 200);
}

function focusSearch() {
  searchEl.value?.focus();
  searchEl.value?.select();
}

defineExpose({ focusSearch });

const mode = computed<SelectionMode>(() => props.selectionMode ?? 'none');

// The visible action buttons = the shared list from FileExplorer, minus
// dividers and hidden entries (the toolbar is a flat row, not a dropdown).
// Disabled entries render greyed-out. Because this is the SAME list the
// context menu uses, the two menus are guaranteed to match.
const toolbarItems = computed(() => props.actions.filter((a) => !a.divider && !a.hidden));

function fire(key: string) {
  emit('action', key);
}
</script>

<template>
  <div class="fe-toolbar">
    <div class="fe-toolbar__primary">
      <button
        v-if="canGoUp"
        type="button"
        class="fe-btn fe-btn--icon-only"
        :title="t('toolbar.go_up')"
        :aria-label="t('toolbar.go_up')"
        @click="emit('go-up')"
      >
        <span class="fe-icon">↑</span>
      </button>

      <button
        v-if="mode === 'none' && !trashActive && !atVirtualRoot && canWrite !== false"
        type="button"
        class="fe-btn fe-btn--primary"
        :title="t('toolbar.new_folder')"
        @click="emit('new-folder')"
      >
        <span class="fe-icon">📁</span>
        <span class="fe-btn__label">{{ t('toolbar.new_folder') }}</span>
      </button>

      <button
        v-for="a in toolbarItems"
        :key="a.key"
        type="button"
        class="fe-btn"
        :class="{ 'fe-btn--danger': a.danger, 'is-disabled': a.disabled }"
        :disabled="a.disabled"
        :title="a.label"
        @click="fire(a.key)"
      >
        <span class="fe-icon">{{ a.icon }}</span>
        <span class="fe-btn__label">{{ a.label }}</span>
      </button>

      <button
        v-if="pasteEnabled && mode === 'none' && !trashActive && !atVirtualRoot && canWrite !== false"
        type="button"
        class="fe-btn"
        :title="t('ctx.paste')"
        @click="fire('paste')"
      >
        <span class="fe-icon">📋</span>
        <span class="fe-btn__label">{{ t('ctx.paste') }}</span>
      </button>
    </div>

    <div class="fe-toolbar__spacer" />

    <div class="fe-toolbar__search-group">
      <div class="fe-search">
        <input
          ref="searchEl"
          type="search"
          class="fe-search__input"
          :placeholder="t('toolbar.search.placeholder')"
          :value="localSearch"
          aria-label="Search"
          @input="onSearchInput"
        />
      </div>
      <button
        v-if="!atVirtualRoot && canWrite !== false"
        type="button"
        class="fe-btn fe-btn--icon-only"
        :title="t('toolbar.upload')"
        @click="emit('upload')"
      >
        <span class="fe-icon">⬆</span>
      </button>
      <button
        type="button"
        class="fe-btn fe-btn--icon-only"
        :title="t('toolbar.refresh')"
        @click="emit('refresh')"
      >
        <span class="fe-icon">↻</span>
      </button>
    </div>

    <div class="fe-toolbar__view" role="tablist" aria-label="View">
      <button
        type="button"
        class="fe-btn fe-btn--icon-only"
        :class="{ 'is-active': viewMode === 'list' }"
        role="tab"
        :aria-selected="viewMode === 'list'"
        :title="t('toolbar.view.list')"
        @click="emit('update:viewMode', 'list')"
      >
        <span class="fe-icon">☰</span>
      </button>
      <button
        type="button"
        class="fe-btn fe-btn--icon-only"
        :class="{ 'is-active': viewMode === 'grid' }"
        role="tab"
        :aria-selected="viewMode === 'grid'"
        :title="t('toolbar.view.grid')"
        @click="emit('update:viewMode', 'grid')"
      >
        <span class="fe-icon">▦</span>
      </button>
    </div>
  </div>
</template>
