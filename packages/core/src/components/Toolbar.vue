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
import { useLocale } from '../composables/useLocale';

export type SelectionMode = 'none' | 'single-file' | 'single-dir' | 'multi';

export type ToolbarAction =
  | 'open'
  | 'preview'
  | 'download'
  | 'convert'
  | 'share'
  | 'rename'
  | 'delete'
  | 'restore'
  | 'cut'
  | 'copy'
  | 'paste';

const props = defineProps<{
  viewMode: ViewMode;
  searchQuery: string;
  /**
   * Tells the toolbar which action set to render (a `.trash/` listing
   * gets a restore-only menu instead of cut/copy/etc).
   */
  trashActive: boolean;
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
  locale: LocaleCode;
}>();

const emit = defineEmits<{
  (e: 'update:viewMode', v: ViewMode): void;
  (e: 'update:searchQuery', v: string): void;
  (e: 'new-folder'): void;
  (e: 'upload'): void;
  (e: 'refresh'): void;
  (e: 'go-up'): void;
  (e: 'action', key: ToolbarAction): void;
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

// What action buttons appear left of the search box. Keep ordering stable
// so muscle memory survives selection changes (e.g. delete is always
// last in the cluster). Trash listings hide cut/copy/move because the
// backend rejects them — we *only* allow restore + hard-delete from
// inside `.trash/`.
const actions = computed<Array<{ key: ToolbarAction; label: string; icon: string; danger?: boolean }>>(() => {
  if (props.trashActive) {
    if (mode.value === 'none') return [];
    return [
      { key: 'restore', label: t('ctx.restore'), icon: '↩' },
      { key: 'delete', label: t('ctx.delete_perm'), icon: '🗑', danger: true },
    ];
  }
  switch (mode.value) {
    case 'single-file':
      // `open` first because it's the canonical action — same behaviour
      // as a double-click. The host page wires it to `/files/edit` for
      // editor-grade types (office/drawio/mermaid/3D/etc) and falls
      // back to the preview modal otherwise. Keep `preview` next to
      // it so the explicit read-only path stays one click away.
      return [
        { key: 'open', label: t('ctx.open'), icon: '↗' },
        { key: 'preview', label: t('ctx.preview'), icon: '👁' },
        { key: 'download', label: t('ctx.download'), icon: '⬇' },
        ...(props.convertEnabled
          ? [{ key: 'convert' as ToolbarAction, label: t('ctx.convert'), icon: '🔄' }]
          : []),
        { key: 'share', label: t('ctx.share'), icon: '🔗' },
        { key: 'rename', label: t('ctx.rename'), icon: '✎' },
        { key: 'delete', label: t('ctx.delete'), icon: '🗑', danger: true },
      ];
    case 'single-dir':
      return [
        { key: 'open', label: t('ctx.open'), icon: '↗' },
        { key: 'share', label: t('ctx.share'), icon: '🔗' },
        { key: 'rename', label: t('ctx.rename'), icon: '✎' },
        { key: 'delete', label: t('ctx.delete'), icon: '🗑', danger: true },
      ];
    case 'multi':
      return [
        { key: 'cut', label: t('ctx.cut'), icon: '✂' },
        { key: 'copy', label: t('ctx.copy'), icon: '❐' },
        { key: 'delete', label: t('ctx.delete'), icon: '🗑', danger: true },
      ];
    default:
      return [];
  }
});

function fire(key: ToolbarAction) {
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
        v-if="mode === 'none' && !trashActive && !atVirtualRoot"
        type="button"
        class="fe-btn fe-btn--primary"
        :title="t('toolbar.new_folder')"
        @click="emit('new-folder')"
      >
        <span class="fe-icon">📁</span>
        <span class="fe-btn__label">{{ t('toolbar.new_folder') }}</span>
      </button>

      <button
        v-for="a in actions"
        :key="a.key"
        type="button"
        class="fe-btn"
        :class="{ 'fe-btn--danger': a.danger }"
        :title="a.label"
        @click="fire(a.key)"
      >
        <span class="fe-icon">{{ a.icon }}</span>
        <span class="fe-btn__label">{{ a.label }}</span>
      </button>

      <button
        v-if="pasteEnabled && mode === 'none' && !trashActive && !atVirtualRoot"
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
        v-if="!atVirtualRoot"
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
      <button
        type="button"
        class="fe-btn fe-btn--icon-only"
        :title="locale === 'en' ? 'Recently opened' : 'Son açılanlar'"
        @click="emit('open-recents')"
      >
        <span class="fe-icon">🕒</span>
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
