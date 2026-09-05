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
import ViewSwitcher from './ViewSwitcher.vue';
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
  /**
   * gezinti:g1 — navigation panel state, for the pressed style on the toggle.
   * The panel has its own collapse control, but a narrow explorer renders it
   * as a drawer that is off screen when closed, so the only way back to it is
   * from here.
   */
  navOpen?: boolean;
  /** gezinti:g1 — false when the deployment has no navigation panel; the
   *  toggle then has nothing to toggle and is not rendered. */
  navEnabled?: boolean;
  /**
   * gezinti:g1 — view modes to offer. Absent = all three (the historical
   * behaviour). `ExplorerConfig.uiProfile: 'simple'` narrows it to list+grid:
   * a four-way switcher is one of the things the reporter in #14 named as
   * "power user tool", and gallery is the one nobody outside photos asks for.
   */
  viewModes?: ViewMode[];
  /* === surucu:d1 — the Drive shell ==================================== */
  /**
   * Which header this is.
   *
   *   'classic' (default) — the row this toolbar has always been.
   *   'drive'             — `uiProfile: 'drive'`: one search field across the
   *                         width with its ⌘K/Ctrl+K hint, and the controls
   *                         that belong to a *listing* rather than to the app
   *                         (view switcher, details toggle) moved down onto the
   *                         breadcrumb row, where the mockups put them.
   *
   * ⚠ Nothing is DELETED by 'drive'. Density, theme, the shortcut editor, the
   * tour and both other view modes are all in the "⋯" menu this variant
   * renders — the same list the narrow layout has used since bag:b4. A control
   * that is on screen in one profile and unreachable in another is a capability
   * split, not a preset.
   */
  shell?: 'classic' | 'drive';
  /** Folder name for the drive field's placeholder ("Search in Photos"). */
  scopeLabel?: string;
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
  (e: 'toggle-nav'): void /* gezinti:g1 */;
  (e: 'open-theme'): void /* wiring:c1 — tema galerisi */;
  (e: 'open-shortcut-settings'): void /* wiring:c2 */;
  /* surucu:d1 — escalate this query into the command palette (⌘K/Ctrl+K). */
  (e: 'open-palette', query: string): void;
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

/* === ui-fix — wide-mode action folding ===============================
 * Long selection-action rows used to wrap the whole toolbar and push the
 * search / view controls onto a second line. The row is now single-line:
 * actions that do not fit fold into a "⋯" menu. Widths come from a
 * hidden measurement strip (all actions always rendered there), so the
 * calculation never mutates what it measures. */
const primaryEl = ref<HTMLElement | null>(null);
const measureEl = ref<HTMLElement | null>(null);
const wideMoreBtnEl = ref<HTMLElement | null>(null);
const wideMoreRef = ref<InstanceType<typeof ContextMenu> | null>(null);
const visibleActionCount = ref(Number.MAX_SAFE_INTEGER);
const visibleToolbarItems = computed(() => toolbarItems.value.slice(0, visibleActionCount.value));
const overflowToolbarItems = computed(() => toolbarItems.value.slice(visibleActionCount.value));
const WIDE_MORE_BTN_W = 40;

function recalcFold() {
  if (props.narrow) return;
  const cont = primaryEl.value;
  const meas = measureEl.value;
  if (!cont || !meas) {
    visibleActionCount.value = toolbarItems.value.length;
    return;
  }
  const gap = parseFloat(getComputedStyle(cont).gap) || 8;
  let fixed = 0;
  for (const child of Array.from(cont.children) as HTMLElement[]) {
    if (child === meas || child === wideMoreBtnEl.value) continue;
    if (child.classList.contains('fe-btn--fold')) continue;
    if (child.offsetWidth > 0) fixed += child.offsetWidth + gap;
  }
  const widths = (Array.from(meas.children) as HTMLElement[]).map((c) => c.offsetWidth + gap);
  const total = widths.reduce((s, w) => s + w, 0);
  const avail = cont.clientWidth - fixed;
  if (total <= avail) {
    visibleActionCount.value = widths.length;
    return;
  }
  let used = WIDE_MORE_BTN_W + gap;
  let count = 0;
  for (const w of widths) {
    if (used + w > avail) break;
    used += w;
    count += 1;
  }
  visibleActionCount.value = count;
}

let foldRo: ResizeObserver | undefined;
onMounted(() => {
  if (typeof ResizeObserver !== 'undefined' && primaryEl.value) {
    foldRo = new ResizeObserver(() => recalcFold());
    foldRo.observe(primaryEl.value);
  }
  void nextTick(recalcFold);
});
onBeforeUnmount(() => foldRo?.disconnect());
watch(
  () => [toolbarItems.value, props.narrow, props.trashActive, mode.value] as const,
  () => void nextTick(recalcFold),
  { deep: false },
);

function openWideMore() {
  const r = wideMoreBtnEl.value?.getBoundingClientRect();
  wideMoreRef.value?.show({ clientX: r ? r.right : 0, clientY: r ? r.bottom + 4 : 0 } as MouseEvent, []);
}
/* === /ui-fix ========================================================= */

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
/* gezinti:g1 — is this view mode on offer? No prop = all of them, so every
   embedder that predates uiProfile keeps the switcher it had. */
function offersView(v: ViewMode): boolean {
  return !props.viewModes || props.viewModes.includes(v);
}

/* surucu:d1 — the tail of the "⋯" menu on its own: everything that is a
 * SETTING of the explorer rather than an action on a file. The narrow layout
 * shows it after the write/selection actions (unchanged); the drive header
 * shows only this half, because in that layout the selection actions are
 * already on screen as buttons and a menu that repeats them is a second door
 * to the same room. */
const utilityActions = computed<ContextAction[]>(() => {
  const list: ContextAction[] = [];
  list.push({ key: 'refresh', label: t('toolbar.refresh'), icon: '↻' });
  list.push({
    key: 'density',
    label:
      density.value === 'compact'
        ? t('toolbar.density.comfortable')
        : t('toolbar.density.compact'),
    icon: '⇅',
  });
  /* wiring:d2 — dar mod ⋯ menüsü: aktif olmayan İKİ görünüm de listelenir
     (list/grid/gallery); eski tekli toggle üç modda eksik kalıyordu. */
  if (props.viewMode !== 'list' && offersView('list')) list.push({ key: 'view-list', label: t('toolbar.view.list'), icon: '☰' });
  if (props.viewMode !== 'grid' && offersView('grid')) list.push({ key: 'view-grid', label: t('toolbar.view.grid'), icon: '▦' });
  if (props.viewMode !== 'gallery' && offersView('gallery')) list.push({ key: 'view-gallery', label: t('toolbar.view.gallery'), icon: '▣' });
  /* /wiring:d2 */
  /* gezinti:g1 — reachable from the overflow menu too, because the toolbar's
     own toggle is hidden while the narrow search field is expanded. */
  if (props.navEnabled !== false) {
    list.push({ key: 'nav', label: t('toolbar.nav'), icon: '☰' });
  }
  /* koru:k1 — inspector toggle also reachable from the narrow overflow menu */
  list.push({ key: 'inspector', label: t('toolbar.inspector'), icon: 'ℹ' });
  /* wiring:c1 — tema galerisi de dar modda ⋯ menüsünden açılır */
  list.push({ key: 'theme', label: t('theme.menu'), icon: '🎨' });
  list.push({ key: 'shortcut-settings', label: t('shortcuts.settings.menu'), icon: '⌨' }) /* wiring:c2 */;
  list.push({ key: 'tour', label: t('tour.restart'), icon: '🎓' }); /* wiring:c4 */
  return list;
});

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
  list.push(...utilityActions.value);
  return list;
});

/* === surucu:d1 — the drive header's search field ======================
 * ONE field, and it is the one the toolbar already had: typing here sets
 * `searchQuery`, which the explorer answers with
 * `?action=search&filter=…` — a real search of the folder you are standing in,
 * exactly as before.
 *
 * The ⌘K chip beside it is the ESCALATION, not a second box: it opens the
 * command palette carrying whatever has been typed, and the palette is where
 * "everywhere" lives (the global endpoint, saved searches, and the commands).
 * That is why the hint can sit on this field honestly — Ctrl+K from here does
 * what the hint says, and Ctrl+K from anywhere else still opens the palette
 * as it has since cila:c.
 */
const macLike =
  typeof navigator !== 'undefined' &&
  /mac|iphone|ipad|ipod/i.test(navigator.platform || navigator.userAgent || '');
const paletteCombo = computed(() => (macLike ? '⌘ K' : 'Ctrl K'));

const drivePlaceholder = computed(() =>
  props.scopeLabel
    ? t('drive.search.placeholder', { scope: props.scopeLabel })
    : t('drive.search.placeholder_all'),
);

/** ⌘/Ctrl+K typed INSIDE the field. The global shortcut handler ignores
 *  keystrokes aimed at an input — which is correct, and it would otherwise
 *  make the hint on this very field a lie. */
function onSearchKeydown(ev: KeyboardEvent) {
  if ((ev.ctrlKey || ev.metaKey) && (ev.key === 'k' || ev.key === 'K')) {
    ev.preventDefault();
    ev.stopPropagation();
    emit('open-palette', localSearch.value);
  }
}

/* surucu:d1 — the drive header renders the selection actions as buttons, so
 * its "⋯" carries only the settings half; the narrow layout renders no action
 * buttons at all and still needs the whole list. */
const menuActions = computed<ContextAction[]>(() =>
  props.shell === 'drive' && !props.narrow ? utilityActions.value : moreActions.value,
);

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
    case 'view-gallery' /* wiring:d2 */:
      emit('update:viewMode', 'gallery');
      break;
    case 'inspector' /* koru:k1 */:
      emit('toggle-inspector');
      break;
    case 'nav' /* gezinti:g1 */:
      emit('toggle-nav');
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
      'fe-toolbar--drive': shell === 'drive' /* surucu:d1 */,
    }"
  >
    <!-- bag:b4 — wide layout, untouched; renders exactly as before when not narrow -->
    <template v-if="!narrow">
    <div ref="primaryEl" class="fe-toolbar__primary">
      <!-- gezinti:g1 — navigation panel toggle. Leftmost, the position every
           Drive-shaped UI puts it in; the panel's own header carries the same
           action, and in narrow mode where the panel is an off-screen drawer
           this is the ONLY way back to it. -->
      <button
        v-if="navEnabled !== false"
        type="button"
        class="fe-btn fe-btn--icon-only fe-toolbar__nav"
        :class="{ 'is-active': navOpen }"
        :aria-pressed="!!navOpen"
        :title="t('toolbar.nav')"
        :aria-label="t('toolbar.nav')"
        data-testid="toolbar-nav"
        @click="emit('toggle-nav')"
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
          <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
          <path d="M9.5 4.5v15" />
        </svg>
      </button>

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

      <!-- surucu:d1 — in the drive shell the panel's "+ New" menu is the one
           primary action; a second New Folder button here would be a second
           front door to the same modal. -->
      <button
        v-if="shell !== 'drive' && mode === 'none' && !trashActive && !atVirtualRoot && canWrite !== false"
        type="button"
        class="fe-btn fe-btn--primary"
        :title="t('toolbar.new_folder')"
        @click="emit('new-folder')"
      >
        <span class="fe-icon">📁</span>
        <span class="fe-btn__label">{{ t('toolbar.new_folder') }}</span>
      </button>

      <button
        v-for="a in visibleToolbarItems"
        :key="a.key"
        type="button"
        class="fe-btn fe-btn--fold"
        :class="{ 'fe-btn--danger': a.danger, 'is-disabled': a.disabled }"
        :disabled="a.disabled"
        :title="a.label"
        @click="fire(a.key)"
      >
        <span class="fe-icon">{{ a.icon }}</span>
        <span class="fe-btn__label">{{ a.label }}</span>
      </button>

      <!-- ui-fix — sığmayan aksiyonlar tek satırı korumak için ⋯ menüsüne
           katlanır (arama/görünüm kontrolleri artık alt satıra düşmez). -->
      <button
        v-if="overflowToolbarItems.length > 0"
        ref="wideMoreBtnEl"
        type="button"
        class="fe-btn fe-btn--icon-only"
        :title="t('toolbar.more')"
        :aria-label="t('toolbar.more')"
        aria-haspopup="menu"
        @click="openWideMore"
      >
        <span class="fe-icon">⋯</span>
      </button>

      <!-- Görünmez ölçüm şeridi: TÜM aksiyonlar her zaman burada render
           edilir; katlama hesabı gerçek genişliklerden yapılır. -->
      <div ref="measureEl" class="fe-toolbar__measure" aria-hidden="true">
        <button
          v-for="a in toolbarItems"
          :key="'m-' + a.key"
          type="button"
          class="fe-btn"
          tabindex="-1"
        >
          <span class="fe-icon">{{ a.icon }}</span>
          <span class="fe-btn__label">{{ a.label }}</span>
        </button>
      </div>

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

    <div class="fe-toolbar__search-group" :class="{ 'fe-toolbar__search-group--drive': shell === 'drive' }">
      <!-- surucu:d1 — the drive shell's one search field: wide, in the header,
           with the ⌘K chip that opens the palette carrying this query. -->
      <div v-if="shell === 'drive'" class="fe-drivesearch" data-testid="drive-search">
        <svg
          class="fe-ficon fe-drivesearch__glass"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.9"
          stroke-linecap="round"
          aria-hidden="true"
          focusable="false"
        >
          <circle cx="11" cy="11" r="6.5" />
          <path d="M16 16l4.5 4.5" />
        </svg>
        <input
          ref="searchEl"
          type="search"
          class="fe-drivesearch__input"
          :placeholder="drivePlaceholder"
          :value="localSearch"
          :aria-label="t('toolbar.search')"
          @input="onSearchInput"
          @keydown="onSearchKeydown"
        />
        <button
          type="button"
          class="fe-drivesearch__kbd"
          :title="t('drive.search.hint_title', { combo: paletteCombo })"
          :aria-label="t('drive.search.hint_title', { combo: paletteCombo })"
          data-testid="drive-search-palette"
          @click="emit('open-palette', localSearch)"
        >
          {{ paletteCombo }}
        </button>
      </div>
      <div v-else class="fe-search">
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
        v-if="shell !== 'drive' && !atVirtualRoot && canWrite !== false"
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
      <!-- surucu:d1 — density, theme, the shortcut editor, the tour and the
           other view modes. Everything the drive header does not draw is one
           click away here; nothing is removed from the build. -->
      <button
        v-if="shell === 'drive'"
        ref="moreBtnEl"
        type="button"
        class="fe-btn fe-btn--icon-only"
        :title="t('toolbar.more')"
        :aria-label="t('toolbar.more')"
        aria-haspopup="menu"
        data-testid="drive-more"
        @click="openMore"
      >
        <span class="fe-icon" aria-hidden="true">⋯</span>
      </button>
    </div>

    <button
      v-if="shell !== 'drive'"
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

    <!-- koru:k1 — inspector (details panel) toggle.
         surucu:d1 — the drive shell renders it on the breadcrumb row instead,
         beside the view switcher, where both belong to the listing. -->
    <button
      v-if="shell !== 'drive'"
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
      v-if="shell !== 'drive'"
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

    <ViewSwitcher
      v-if="shell !== 'drive'"
      :view-mode="viewMode"
      :locale="locale"
      :modes="viewModes"
      @update:view-mode="emit('update:viewMode', $event)"
    />
    </template>

    <!-- surucu:d1 — narrow DRIVE layout: [☰] [ search ] [⋯].
         The classic narrow row hides search behind a magnifier, which is the
         right trade when the row also carries Up / Upload / actions. This shell
         does not: "+ New" lives in the drawer, upload has the floating button
         at this width already, and the whole point of the profile is that the
         search field is the thing you see. So the field keeps the row, and only
         the ⌘K chip goes (CSS) — a phone cannot press it, and the palette stays
         in the "⋯" menu. -->
    <template v-else-if="shell === 'drive'">
      <button
        v-if="navEnabled !== false"
        type="button"
        class="fe-btn fe-btn--icon-only fe-toolbar__nav"
        :class="{ 'is-active': navOpen }"
        :aria-pressed="!!navOpen"
        :title="t('toolbar.nav')"
        :aria-label="t('toolbar.nav')"
        data-testid="toolbar-nav"
        @click="emit('toggle-nav')"
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
          <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
          <path d="M9.5 4.5v15" />
        </svg>
      </button>
      <div class="fe-drivesearch" data-testid="drive-search">
        <svg
          class="fe-ficon fe-drivesearch__glass"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.9"
          stroke-linecap="round"
          aria-hidden="true"
          focusable="false"
        >
          <circle cx="11" cy="11" r="6.5" />
          <path d="M16 16l4.5 4.5" />
        </svg>
        <input
          ref="searchEl"
          type="search"
          class="fe-drivesearch__input"
          :placeholder="drivePlaceholder"
          :value="localSearch"
          :aria-label="t('toolbar.search')"
          @input="onSearchInput"
          @keydown="onSearchKeydown"
        />
        <button
          type="button"
          class="fe-drivesearch__kbd"
          :title="t('drive.search.hint_title', { combo: paletteCombo })"
          :aria-label="t('drive.search.hint_title', { combo: paletteCombo })"
          data-testid="drive-search-palette"
          @click="emit('open-palette', localSearch)"
        >
          {{ paletteCombo }}
        </button>
      </div>
      <button
        ref="moreBtnEl"
        type="button"
        class="fe-btn fe-btn--icon-only"
        :title="t('toolbar.more')"
        :aria-label="t('toolbar.more')"
        aria-haspopup="menu"
        data-testid="drive-more"
        @click="openMore"
      >
        <span class="fe-icon">⋯</span>
      </button>
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
        <!-- gezinti:g1 — navigation panel toggle. Leftmost, the position every
             Drive-shaped UI puts it in; the panel's own header carries the same
             action, and in narrow mode where the panel is an off-screen drawer
             this is the ONLY way back to it. -->
        <button
          v-if="navEnabled !== false"
          type="button"
          class="fe-btn fe-btn--icon-only fe-toolbar__nav"
          :class="{ 'is-active': navOpen }"
          :aria-pressed="!!navOpen"
          :title="t('toolbar.nav')"
          :aria-label="t('toolbar.nav')"
          data-testid="toolbar-nav"
          @click="emit('toggle-nav')"
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
            <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
          <path d="M9.5 4.5v15" />
          </svg>
        </button>

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
        :actions="menuActions"
        @select="onMoreSelect"
      />
    </template>

    <!-- ui-fix — wide modda katlanan aksiyonların ⋯ menüsü -->
    <ContextMenu
      v-if="!narrow"
      ref="wideMoreRef"
      :locale="locale"
      :theme="theme || 'auto'"
      :sheet="false"
      :actions="overflowToolbarItems"
      @select="(a) => fire(a.key)"
    />
  </div>
</template>
