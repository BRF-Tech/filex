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
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { ViewMode } from '../types/FileNode';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import ContextMenu, { type ContextAction } from './ContextMenu.vue';
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
  /**
   * bag:b4 — narrow/embed mini mode. When true the toolbar collapses to
   * [↑?] [🔍] [⬆] [⋯]: secondary actions (New Folder, Refresh, density,
   * view switcher, selection actions, Paste) move into the "⋯" overflow
   * menu and search expands from an icon to a full-width input. When
   * absent/false the classic wide layout renders unchanged.
   */
  narrow?: boolean;
  /**
   * bag:b4 — resolved theme, forwarded to the teleported overflow menu
   * (which loses the `.fe` variable scope under <body>).
   */
  theme?: ThemeMode;
  /* koru:k1 — inspector (details panel) toggle state, for the pressed style. */
  inspectorOpen?: boolean;
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
  (e: 'update:density', v: Density): void;
  (e: 'toggle-inspector'): void /* koru:k1 */;
  (e: 'open-theme'): void /* wiring:c1 — tema galerisi */;
  (e: 'open-shortcut-settings'): void /* wiring:c2 */;
}>();

// Density toggle — the toolbar owns the persisted preference; the parent
// only mirrors the value into a root class so both views pick it up.
export type Density = 'comfortable' | 'compact';
const DENSITY_LS_KEY = 'filex.density';
const density = ref<Density>(
  (() => {
    try {
      return localStorage.getItem(DENSITY_LS_KEY) === 'compact' ? 'compact' : 'comfortable';
    } catch {
      return 'comfortable';
    }
  })(),
);
onMounted(() => emit('update:density', density.value));

function toggleDensity() {
  density.value = density.value === 'compact' ? 'comfortable' : 'compact';
  try {
    localStorage.setItem(DENSITY_LS_KEY, density.value);
  } catch {
    /* quota */
  }
  emit('update:density', density.value);
}

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

async function focusSearch() {
  /* bag:b4 — in narrow mode the input is collapsed behind the icon. */
  if (props.narrow && !searchOpen.value) {
    searchOpen.value = true;
    await nextTick();
  }
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

/* === bag:b4 — narrow-mode state: expandable search + "⋯" overflow menu === */

const searchOpen = ref(false);
// Leaving narrow mode discards the transient expanded-search state so the
// wide layout always comes back exactly as it was.
watch(
  () => props.narrow,
  (n) => {
    if (!n) searchOpen.value = false;
  },
);

async function openSearch() {
  searchOpen.value = true;
  await nextTick();
  searchEl.value?.focus();
}
function closeSearch() {
  // Keep the query (it still filters the listing); the icon shows an
  // active state while a query is set.
  searchOpen.value = false;
}

// Coarse-pointer detection — the overflow menu renders as a bottom sheet on
// touch devices, matching the file context menu.
const coarse = ref(false);
let coarseMq: MediaQueryList | undefined;
function syncCoarse(e?: MediaQueryListEvent | MediaQueryList) {
  coarse.value = !!(e && 'matches' in e && e.matches);
}
onMounted(() => {
  if (typeof window === 'undefined' || !window.matchMedia) return;
  coarseMq = window.matchMedia('(pointer: coarse)');
  syncCoarse(coarseMq);
  coarseMq.addEventListener?.('change', syncCoarse);
});
onBeforeUnmount(() => {
  coarseMq?.removeEventListener?.('change', syncCoarse);
});

const moreBtnEl = ref<HTMLElement | null>(null);
const moreRef = ref<InstanceType<typeof ContextMenu> | null>(null);

// Everything the wide toolbar renders as standalone buttons, folded into one
// action list: folder-level writes (New Folder / Paste), the shared
// selection actions, then the view utilities (Refresh / density / view mode).
const moreActions = computed<ContextAction[]>(() => {
  const list: ContextAction[] = [];
  const writable =
    !props.trashActive && !props.atVirtualRoot && props.canWrite !== false;
  if (mode.value === 'none' && writable) {
    list.push({ key: 'new-folder', label: t('toolbar.new_folder'), icon: '📁' });
    if (props.pasteEnabled) list.push({ key: 'paste', label: t('ctx.paste'), icon: '📋' });
  }
  list.push(...toolbarItems.value);
  if (list.length) list.push({ divider: true, key: 'bag-sep', label: '' });
  list.push({ key: 'refresh', label: t('toolbar.refresh'), icon: '↻' });
  list.push({
    key: 'density',
    label:
      density.value === 'compact'
        ? t('toolbar.density.comfortable')
        : t('toolbar.density.compact'),
    icon: '⇅',
  });
  list.push(
    props.viewMode === 'list'
      ? { key: 'view-grid', label: t('toolbar.view.grid'), icon: '▦' }
      : { key: 'view-list', label: t('toolbar.view.list'), icon: '☰' },
  );
  /* koru:k1 — inspector toggle also reachable from the narrow overflow menu */
  list.push({ key: 'inspector', label: t('toolbar.inspector'), icon: 'ℹ' });
  /* wiring:c1 — tema galerisi de dar modda ⋯ menüsünden açılır */
  list.push({ key: 'theme', label: t('theme.menu'), icon: '🎨' });
  list.push({ key: 'shortcut-settings', label: t('shortcuts.settings.menu'), icon: '⌨' }) /* wiring:c2 */;
  list.push({ key: 'tour', label: t('tour.restart'), icon: '🎓' }); /* wiring:c4 */
  return list;
});

function openMore() {
  const r = moreBtnEl.value?.getBoundingClientRect();
  moreRef.value?.show({ clientX: r ? r.right : 0, clientY: r ? r.bottom + 4 : 0 }, []);
}

function onMoreSelect(a: ContextAction) {
  switch (a.key) {
    case 'new-folder':
      emit('new-folder');
      break;
    case 'refresh':
      emit('refresh');
      break;
    case 'density':
      toggleDensity();
      break;
    case 'view-list':
      emit('update:viewMode', 'list');
      break;
    case 'view-grid':
      emit('update:viewMode', 'grid');
      break;
    case 'inspector' /* koru:k1 */:
      emit('toggle-inspector');
      break;
    case 'theme' /* wiring:c1 */:
      emit('open-theme');
      break;
    case 'shortcut-settings' /* wiring:c2 */:
      emit('open-shortcut-settings');
      break;
    case 'tour' /* wiring:c4 — bubbles to the FileExplorer root listener */:
      moreBtnEl.value?.dispatchEvent(new CustomEvent('fe:tour-restart', { bubbles: true }));
      break;
    default:
      fire(a.key);
  }
}

/* === /bag:b4 === */
</script>

<template>
  <div
    class="fe-toolbar"
    :class="{
      'fe-toolbar--narrow': narrow /* bag:b4 */,
      'fe-toolbar--searching': narrow && searchOpen /* bag:b4 */,
    }"
  >
    <!-- bag:b4 — wide layout, untouched; renders exactly as before when not narrow -->
    <template v-if="!narrow">
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
          :aria-label="t('toolbar.search') /* wiring:c4 — was hardcoded EN */"
          @input="onSearchInput"
        />
      </div>
      <button
        v-if="!atVirtualRoot && canWrite !== false"
        type="button"
        class="fe-btn fe-btn--icon-only"
        :title="t('toolbar.upload')"
        :aria-label="t('toolbar.upload') /* wiring:c4 */"
        @click="emit('upload')"
      >
        <span class="fe-icon" aria-hidden="true">⬆</span>
      </button>
      <button
        type="button"
        class="fe-btn fe-btn--icon-only"
        :title="t('toolbar.refresh')"
        :aria-label="t('toolbar.refresh') /* wiring:c4 */"
        @click="emit('refresh')"
      >
        <span class="fe-icon" aria-hidden="true">↻</span>
      </button>
    </div>

    <button
      type="button"
      class="fe-btn fe-btn--icon-only fe-toolbar__density"
      :class="{ 'is-active': density === 'compact' }"
      :aria-pressed="density === 'compact'"
      :aria-label="density === 'compact' ? t('toolbar.density.comfortable') : t('toolbar.density.compact')"
      :title="density === 'compact' ? t('toolbar.density.comfortable') : t('toolbar.density.compact')"
      @click="toggleDensity"
    >
      <!-- Row-spacing glyph: tight rows when compact is ON, airy rows otherwise. -->
      <svg
        v-if="density === 'compact'"
        class="fe-ficon"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        aria-hidden="true"
        focusable="false"
      >
        <path d="M4 6.5h16M4 10.5h16M4 14.5h16M4 18.5h16" />
      </svg>
      <svg
        v-else
        class="fe-ficon"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        aria-hidden="true"
        focusable="false"
      >
        <path d="M4 5.5h16M4 12h16M4 18.5h16" />
      </svg>
    </button>

    <!-- koru:k1 — inspector (details panel) toggle -->
    <button
      type="button"
      class="fe-btn fe-btn--icon-only fe-toolbar__inspector"
      :class="{ 'is-active': inspectorOpen }"
      :aria-pressed="!!inspectorOpen"
      :title="t('toolbar.inspector')"
      :aria-label="t('toolbar.inspector')"
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

    <!-- wiring:c1 — tema galerisi (palet ikonu) -->
    <button
      type="button"
      class="fe-btn fe-btn--icon-only fe-toolbar__theme"
      :title="t('theme.menu')"
      :aria-label="t('theme.menu')"
      @click="emit('open-theme')"
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
        <path d="M12 3a9 9 0 1 0 0 18h1.4a2.1 2.1 0 0 0 1.5-3.6 2.1 2.1 0 0 1 1.5-3.6H19a2 2 0 0 0 2-2A9 9 0 0 0 12 3z" />
        <circle cx="7.8" cy="10.2" r="1.15" fill="currentColor" stroke="none" />
        <circle cx="11.6" cy="7.2" r="1.15" fill="currentColor" stroke="none" />
        <circle cx="16" cy="8.6" r="1.15" fill="currentColor" stroke="none" />
      </svg>
    </button>
    <!-- /wiring:c1 -->

    <div class="fe-toolbar__view" role="tablist" :aria-label="t('toolbar.view_label') /* wiring:c4 — was hardcoded EN */">
      <button
        type="button"
        class="fe-btn fe-btn--icon-only"
        :class="{ 'is-active': viewMode === 'list' }"
        role="tab"
        :aria-selected="viewMode === 'list'"
        :title="t('toolbar.view.list')"
        :aria-label="t('toolbar.view.list') /* wiring:c4 */"
        @click="emit('update:viewMode', 'list')"
      >
        <span class="fe-icon" aria-hidden="true">☰</span>
      </button>
      <button
        type="button"
        class="fe-btn fe-btn--icon-only"
        :class="{ 'is-active': viewMode === 'grid' }"
        role="tab"
        :aria-selected="viewMode === 'grid'"
        :title="t('toolbar.view.grid')"
        :aria-label="t('toolbar.view.grid') /* wiring:c4 */"
        @click="emit('update:viewMode', 'grid')"
      >
        <span class="fe-icon" aria-hidden="true">▦</span>
      </button>
    </div>
    </template>

    <!-- bag:b4 — narrow layout: [↑?] ... [🔍] [⬆] [⋯], search expands full-width -->
    <template v-else>
      <template v-if="searchOpen">
        <div class="fe-search fe-search--full">
          <input
            ref="searchEl"
            type="search"
            class="fe-search__input"
            :placeholder="t('toolbar.search.placeholder')"
            :value="localSearch"
            :aria-label="t('toolbar.search')"
            @input="onSearchInput"
            @keydown.esc.stop.prevent="closeSearch"
          />
        </div>
        <button
          type="button"
          class="fe-btn fe-btn--icon-only"
          :title="t('toolbar.search.close')"
          :aria-label="t('toolbar.search.close')"
          @click="closeSearch"
        >
          <span class="fe-icon">✕</span>
        </button>
      </template>
      <template v-else>
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

        <div class="fe-toolbar__spacer" />

        <button
          type="button"
          class="fe-btn fe-btn--icon-only fe-toolbar__search-toggle"
          :class="{ 'is-active': !!localSearch }"
          :title="t('toolbar.search')"
          :aria-label="t('toolbar.search')"
          @click="openSearch"
        >
          <span class="fe-icon">🔍</span>
        </button>
        <button
          v-if="!atVirtualRoot && canWrite !== false"
          type="button"
          class="fe-btn fe-btn--icon-only"
          :title="t('toolbar.upload')"
          :aria-label="t('toolbar.upload')"
          @click="emit('upload')"
        >
          <span class="fe-icon">⬆</span>
        </button>
        <button
          ref="moreBtnEl"
          type="button"
          class="fe-btn fe-btn--icon-only fe-toolbar__more"
          :title="t('toolbar.more')"
          :aria-label="t('toolbar.more')"
          aria-haspopup="menu"
          @click="openMore"
        >
          <span class="fe-icon">⋯</span>
        </button>
      </template>

      <ContextMenu
        ref="moreRef"
        :locale="locale"
        :theme="theme || 'auto'"
        :sheet="coarse"
        :actions="moreActions"
        @select="onMoreSelect"
      />
    </template>
  </div>
</template>
