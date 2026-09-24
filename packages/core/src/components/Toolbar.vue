<script setup lang="ts">
/**
 * Toolbar — selection-aware action row.
 *
 * Layout:
 *   - No selection            → [📁 New Folder]    [search | ⬆ ↻]   [☰ ▦]
 *   - Anything selected       → the SELECTION BAR (gorunum:v1-selbar, below):
 *                               a tinted row under the breadcrumb, in the
 *                               filter row's place.
 *
 * Presentational only — all logic (rename, share, …) lives in
 * FileExplorer.vue, which listens for `action` emits.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue';
import type { ViewMode } from '../types/FileNode';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import ContextMenu, { type ContextAction } from './ContextMenu.vue';
/* gorunum:v2-topbar — ViewSwitcher is no longer mounted here. It lives on the
 * breadcrumb row in EVERY profile now (FileExplorer renders it inside
 * `.fe-subhead__actions`), so the header and the crumb row can no longer end
 * up offering different view modes — which is what two mount points cost. */
import { useLocale } from '../composables/useLocale';
import { inlineEndX } from '../lib/direction';
import { actionIconSvg } from '../lib/actionIcons';
import { eventMatchesShortcut, menuShortcutHint, shortcutHint } from '../composables/useKeyboardShortcuts';

export type SelectionMode = 'none' | 'single-file' | 'single-dir' | 'multi';

/* gorunum:v1-icons — the glyph for a toolbar row. Identical resolution to the
 * context menu's (lib/actionIcons by key, `icon` overriding when a row names a
 * variant or an embedder supplies its own mark), because the toolbar and the
 * menu render the SAME ContextAction[] and must not drift apart visually any
 * more than they may drift apart in content. */
function iconFor(a: ContextAction): string {
  return actionIconSvg(a.icon || a.key);
}

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
  /**
   * gorunum:v1-selbar — how many rows are ticked, for the bar's "16 selected".
   *
   * Optional, and the bar does not depend on it: when the parent does not pass
   * one the count is read off the listing itself (`[data-fe-path]` rows carry
   * `aria-selected`, in all three views). A number handed down here always
   * wins — it is the selection the parent actually holds, and the DOM is only
   * a mirror of it.
   */
  selectionCount?: number;
  /**
   * pane:p1 — WHICH pane the selection belongs to, as its `data-pane` value.
   *
   * ⚠⚠ This is not cosmetic and it is not optional once there are two panes.
   * The bar is teleported into a slot inside the pane it acts on, and both
   * halves of a split render `.fe__primary > .fe-selbar-slot` — so a
   * `querySelector` answers in DOCUMENT order and always hands back the LEFT
   * one. Measured 2026-09-13 before this prop existed: ticking rows in the
   * right-hand pane raised no bar at all, and the bar standing over the left
   * pane still described the left pane's older selection.
   *
   * Absent (a bare `<Toolbar>` in a test, an embedder with one listing) keeps
   * the old behaviour: the first `.fe__primary` in the root.
   */
  selectionPane?: string;
  /** True when clipboard has cut/copy items, so we can enable Paste. */
  pasteEnabled?: boolean;
  /** True when the universal converter (FILEX_CONVERT_URL) is available. */
  convertEnabled?: boolean;
  /**
   * True when the current dir has a parent the user can step up to.
   *
   * ⚠ gorunum:v2-topbar — NOT DRAWN any more, in either layout. "Up one
   * level" and the parent crumb walk to the same folder, and the crumb is
   * always on screen (the trail collapses to `root › … › parent › current`
   * and never past it), so the button was the same verb twice. The keyboard
   * binding and the `go-up` emit are untouched; the prop stays because
   * embedders pass it and because a header that wants the button back has
   * everything it needs here.
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
  /* === surucu:d1 — the shell ========================================== */
  /**
   * ⚠ There is no `shell` prop any more. There used to be one ('classic' vs
   * 'drive') and it decided which header a surface got; the header below is
   * now the only one there is, at every width and in every embed. Density,
   * theme, the shortcut editor, the tour and the other view modes live in the
   * "⋯" menu it renders — nothing was dropped from the build, and a control
   * that is on screen in one profile and unreachable in another was the
   * capability split this removed.
   */
  /** Folder name for the search field's placeholder ("Search in Photos"). */
  scopeLabel?: string;
  /* gorunum:v3-shell — the product mark, for a host that cannot fill the
     `#brand` slot. See ExplorerConfig.brand: a `<filex-explorer>` host cannot,
     measured, so these are the only door it has. The slot still wins — they are
     rendered as its FALLBACK content, which is the mechanism for "use mine if
     you gave me one", with no condition to keep in step. */
  brandName?: string;
  brandMarkUrl?: string;
  /**
   * gorunum:v3-shell — this surface has no listing for the field to narrow, so
   * Enter hands the query to the command palette instead.
   *
   * ⚠ It exists because putting Home inside this header gave Home a search box,
   * and a box that swallows what you type is worse than no box: measured on the
   * landing page, typing filtered nothing, showed nothing and re-fetched the two
   * per-user lists on every keystroke. The palette IS "search everywhere" — it
   * is what the ⌘K chip on this very field already promises — so on a surface
   * with nothing to filter, that is where the words go.
   *
   * ⚠ Gated rather than universal: in a folder, Enter must keep meaning
   * "nothing new", because the debounced input has already run the search.
   */
  searchEscalates?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:viewMode', v: ViewMode): void;
  (e: 'update:searchQuery', v: string): void;
  /* gorunum:v1-advsearch — the filter affordance inside the search field.
     Carries the text already typed so the dialog continues this search
     instead of opening an empty second one, exactly as the ⌘K chip does. */
  (e: 'open-advanced-search', v: string): void;
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
  (e: 'open-timezone'): void /* zaman:z3 */;
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
/**
 * gorunum:v1 — someone else may own this preference too.
 *
 * The web app's user-settings modal writes the same `filex.density` key, and
 * this component read it exactly once at setup — so flipping "compact file
 * list" there did nothing until the explorer was mounted again. A `storage`
 * event covers another tab; a same-tab write needs the writer to say so, which
 * is what the custom event is for.
 */
function adoptStoredDensity() {
  try {
    const v = localStorage.getItem(DENSITY_LS_KEY) === 'compact' ? 'compact' : 'comfortable';
    if (v !== density.value) {
      density.value = v;
      emit('update:density', v);
    }
  } catch {
    /* storage blocked */
  }
}
function onDensityStorage(e: StorageEvent) {
  if (e.key === null || e.key === DENSITY_LS_KEY) adoptStoredDensity();
}

onMounted(() => {
  emit('update:density', density.value);
  window.addEventListener('storage', onDensityStorage);
  window.addEventListener('filex:density', adoptStoredDensity);
});
onBeforeUnmount(() => {
  window.removeEventListener('storage', onDensityStorage);
  window.removeEventListener('filex:density', adoptStoredDensity);
});

function toggleDensity() {
  density.value = density.value === 'compact' ? 'comfortable' : 'compact';
  try {
    localStorage.setItem(DENSITY_LS_KEY, density.value);
  } catch {
    /* quota */
  }
  emit('update:density', density.value);
}

const { t, dir } = useLocale(() => props.locale);

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
  /* bag:b4 — the field used to be collapsed behind a magnifier at this width
     and this had to expand it first. It is on screen in both layouts now, so
     there is nothing to open; the tick stays because a caller may have just
     mounted the row. */
  await nextTick();
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

/* === gorunum:v1-selbar — the selection bar ===========================
 * Measured on the reference shell: the moment anything is ticked the FILTER
 * ROW is replaced by a tinted bar — "16 selected", then icon-only actions
 * (download · share · move · copy to · star · delete), then an × that clears
 * the selection. What filex did instead was fill the toolbar's primary row
 * with labelled buttons, one of them a loud red Delete.
 *
 * It is not a second menu. The icons are the SAME `ContextAction[]` the
 * right-click menu renders (`selectionActionList` in FileExplorer, handed here
 * as `actions`), in the order FileExplorer built them; the bar only chooses
 * which of them it can say with a glyph, and everything else — open, preview,
 * rename, tags, paste, the keep-on-device rows — is one click away in the
 * bar's own "⋯". Nothing is removed from the build.
 *
 * ⚠ Why the bar is TELEPORTED out of this component: the row it replaces lives
 * under the breadcrumb, inside `.fe__primary`, two boxes below the toolbar.
 * The toolbar is the only component handed the selection, so the bar is
 * RENDERED here and MOUNTED there, into a slot this component inserts right
 * after the breadcrumb wrapper. DOM order then still matches reading order
 * (the bar comes before the listing it acts on), which a `order:` trick on the
 * flex column would have broken for anyone tabbing through the page.
 * ⚠ With no `.fe__primary` to mount into (a bare `<Toolbar>` in a test, an
 * embedder using this component on its own) the Teleport is DISABLED and the
 * bar renders in place, so the actions never disappear.
 */

/** Keys the bar is willing to say with a glyph alone. The reference draws six
 *  — download, share, move, copy to, star, delete — which are filex's
 *  download / access / cut / copy / star / delete; `restore` joins them for a
 *  `.trash/` listing, whose whole vocabulary is restore + delete.
 *  ⚠ A SET, never an order: the bar walks `actions` in the parent's order, so
 *  it cannot end up disagreeing with the right-click menu about what comes
 *  first (filex lesson #67 — one list, two renderers). */
const BAR_ICON_KEYS = new Set(['download', 'access', 'cut', 'copy', 'star', 'restore', 'delete']);

const hasSelection = computed(() => mode.value !== 'none');

/** The row's two halves, in ONE pass over the parent's list so the order is
 *  the parent's. A key is promoted only if it also HAS a glyph — an icon-only
 *  button with no icon is an empty 28px box. */
const barSplit = computed(() => {
  const icons: ContextAction[] = [];
  const rest: ContextAction[] = [];
  /* ⚠ Walks the parent's list WITH its dividers, not `toolbarItems`. The "⋯"
   * is a dropdown drawn by the same ContextMenu as the right click, and the
   * dividers are what group it: the owner asked for a line between every
   * app's actions (lib/pluginMenu → one `sep-plugin:*` per app), and a "⋯"
   * built from the divider-free list ran Open, Rename, Tags and two apps'
   * verbs together as one undivided column while the right click beside it
   * drew the lines. An icon never takes a divider; ContextMenu collapses the
   * ones that end up leading, trailing or doubled once the icons are pulled
   * out. `restCount` is the number of REAL entries — a "⋯" whose menu is
   * nothing but dividers would open an empty box. */
  let restCount = 0;
  for (const a of props.actions) {
    if (a.hidden) continue;
    if (a.divider) {
      rest.push(a);
      continue;
    }
    if (BAR_ICON_KEYS.has(a.key) && iconFor(a)) icons.push(a);
    else {
      rest.push(a);
      restCount++;
    }
  }
  return { icons, rest: restCount ? rest : [], restCount };
});

/* ── folding (kept from ui-fix, moved onto the bar) ───────────────────
 * Actions that do not fit fold into the "⋯" instead of wrapping the row onto
 * a second line. Widths come from a hidden measurement strip that renders the
 * SAME buttons as the visible row — measure a node nobody sees and the
 * arithmetic is about a width nothing has. */
const selBarEl = ref<HTMLElement | null>(null);
const measureEl = ref<HTMLElement | null>(null);
const selMoreBtnEl = ref<HTMLElement | null>(null);
const selMoreRef = ref<InstanceType<typeof ContextMenu> | null>(null);
const visibleActionCount = ref(Number.MAX_SAFE_INTEGER);
const visibleBarItems = computed(() => barSplit.value.icons.slice(0, visibleActionCount.value));
const foldedBarItems = computed(() => barSplit.value.icons.slice(visibleActionCount.value));
/** The bar's "⋯": what did not fit, then everything that was never an icon. */
const barMenuItems = computed<ContextAction[]>(() => {
  const folded = foldedBarItems.value;
  const rest = barSplit.value.rest;
  if (folded.length && rest.length) {
    return [...folded, { divider: true, key: 'selbar-sep', label: '' }, ...rest];
  }
  return [...folded, ...rest];
});
const SELBAR_MORE_BTN_W = 32;

function recalcFold() {
  const cont = selBarEl.value;
  const meas = measureEl.value;
  const all = barSplit.value.icons.length;
  if (!cont || !meas) {
    visibleActionCount.value = all;
    return;
  }
  const gap = parseFloat(getComputedStyle(cont).gap) || 4;
  const widths = (Array.from(meas.children) as HTMLElement[]).map((c) => c.offsetWidth + gap);
  const total = widths.reduce((s, w) => s + w, 0);
  const avail = cont.clientWidth;
  // The "⋯" is already on screen whenever something never was an icon, so its
  // width is not free room; when nothing is folded away yet it still has to be
  // paid for the moment the first icon folds.
  const moreW = (selMoreBtnEl.value?.offsetWidth || SELBAR_MORE_BTN_W) + gap;
  if (total <= avail - (barSplit.value.rest.length ? moreW : 0)) {
    visibleActionCount.value = widths.length;
    return;
  }
  let used = moreW;
  let count = 0;
  for (const w of widths) {
    if (used + w > avail) break;
    used += w;
    count += 1;
  }
  visibleActionCount.value = count;
}

let foldRo: ResizeObserver | undefined;
watch(selBarEl, (el) => {
  foldRo?.disconnect();
  foldRo = undefined;
  if (el && typeof ResizeObserver !== 'undefined') {
    foldRo = new ResizeObserver(() => recalcFold());
    foldRo.observe(el);
  }
  void nextTick(recalcFold);
});
onBeforeUnmount(() => foldRo?.disconnect());
watch(
  () => [toolbarItems.value, props.narrow, props.trashActive, mode.value] as const,
  () => void nextTick(recalcFold),
  { deep: false },
);

function openBarMore() {
  const r = selMoreBtnEl.value?.getBoundingClientRect();
  // ⚠ RTL: from the button's END edge (its left in RTL) — ContextMenu flips it
  // back under the button from there, in either direction.
  selMoreRef.value?.show({ clientX: r ? inlineEndX(r, dir.value) : 0, clientY: r ? r.bottom + 4 : 0 } as MouseEvent, []);
}

/* ── where the bar lands ──────────────────────────────────────────────── */
const rootEl = ref<HTMLElement | null>(null);
/** The slot this component mounts the bar into, inside `.fe__primary` — one
 *  row above the filter row, and below both the crumbs and the tab strip.
 *  `shallowRef` on purpose: a DOM node is not state to make reactive. */
const selSlot = shallowRef<HTMLElement | null>(null);
/** True when WE created the slot, so teardown only removes our own node. */
const selSlotOwned = ref(false);
/** The listing's own ground, for the × (see clearSelection). */
const selBodyEl = shallowRef<HTMLElement | null>(null);
const selPrimaryEl = shallowRef<HTMLElement | null>(null);

/**
 * Which `.fe__primary` this bar belongs to right now.
 *
 * ⚠ `selectionPane` is our own prop and only ever 'main' / 'split', but it is
 * interpolated into a selector, so anything that is not a bare identifier is
 * refused rather than escaped — a selector built from unvetted text is a
 * selector that can be made to match something else.
 */
function findPrimary(): HTMLElement | null {
  const root = rootEl.value?.closest('.fe');
  if (!root) return null;
  const pane = props.selectionPane;
  if (pane && /^[A-Za-z][\w-]*$/.test(pane)) {
    const owned = root.querySelector<HTMLElement>(`.fe__primary[data-pane="${pane}"]`);
    if (owned) return owned;
  }
  return root.querySelector<HTMLElement>('.fe__primary');
}

/** Let go of the slot we are pointing at, removing it only if we made it. */
function releaseSelSlot() {
  const prev = selSlot.value;
  if (prev) {
    prev.classList.remove('is-active');
    if (selSlotOwned.value) prev.remove();
  }
  selSlot.value = null;
  selSlotOwned.value = false;
}

/**
 * Point the teleport at the pane that owns the selection.
 *
 * ⚠⚠ Called on mount AND whenever `selectionPane` changes — the original was a
 * one-shot `onMounted`, and a cached node is the whole bug: with two panes it
 * cached the left one forever, so the right pane could never raise a bar and
 * the left pane's bar described a selection nobody was touching.
 *
 * ⚠ `isConnected` is checked too, not just identity: the split pane is keyed
 * on the active tab, so switching tabs REPLACES its DOM and would otherwise
 * leave the teleport aimed at a detached node — a bar that renders into
 * nothing looks exactly like a bar that was never built.
 */
function resolveSelSlot() {
  const primary = findPrimary();
  if (!primary) {
    releaseSelSlot();
    selPrimaryEl.value = null;
    selBodyEl.value = null;
    return;
  }
  if (selPrimaryEl.value === primary && selSlot.value?.isConnected) return;
  releaseSelSlot();
  selPrimaryEl.value = primary;
  selBodyEl.value = primary.querySelector<HTMLElement>(':scope > .fe__body');
  /* gorunum:v2-topbar — prefer the slot the PARENT rendered.
   * ⚠ The row the bar has to sit under is now the tab strip, and the strip is
   * `v-if`'d on there being a tab — which is seeded in FileExplorer's own
   * onMounted, i.e. after this one. A node inserted here "after the crumbs"
   * would therefore land ABOVE a strip that appears a tick later, and the
   * selection bar would sit between the crumbs and the tabs for the rest of
   * the session. So FileExplorer renders `.fe-selbar-slot` itself, in the
   * right place, declaratively; this fallback is for a bare <Toolbar> in a
   * test and for an embedder mounting the component on its own. */
  const existing = primary.querySelector<HTMLElement>(':scope > .fe-selbar-slot');
  if (existing) {
    selSlot.value = existing;
    selSlotOwned.value = false;
  } else {
    const slot = document.createElement('div');
    slot.className = 'fe-selbar-slot';
    /* ⚠ Two queries, not one comma list: `querySelector` answers in DOCUMENT
     * order, so a list would hand back the crumb row (which comes first) even
     * when a tab strip is standing below it. */
    const head =
      primary.querySelector<HTMLElement>(':scope > .fe-tabs') ??
      primary.querySelector<HTMLElement>(':scope > .fe-subhead, :scope > .fe-subhead--plain');
    if (head) head.insertAdjacentElement('afterend', slot);
    else primary.prepend(slot);
    selSlot.value = slot;
    selSlotOwned.value = true;
  }
  syncSlotActive();
  reobserveSelection();
}

onMounted(resolveSelSlot);
onBeforeUnmount(releaseSelSlot);

/** The filter row hides behind `.fe-selbar-slot.is-active ~ .fe-filterbar`
 *  (styles/base.css). A class toggled here rather than `:has()` on the slot:
 *  the state is known in JS, and a rule that depends on a teleport having
 *  landed is a rule that is right only after a repaint.
 *  ⚠ `releaseSelSlot` clears the class off the slot we are leaving, so the
 *  pane we walked away from does not keep its filter row hidden. */
function syncSlotActive() {
  selSlot.value?.classList.toggle('is-active', hasSelection.value);
}
watch(hasSelection, syncSlotActive, { immediate: true });

/* The bar follows the focus. Re-resolving on `hasSelection` too is what covers
 * a pane that was remounted (a tab switch) while nothing was selected. */
watch([() => props.selectionPane, hasSelection], () => resolveSelSlot());

/* ── how many ─────────────────────────────────────────────────────────── */
const domSelCount = ref(0);
let selMo: MutationObserver | undefined;
let selTick = false;
function recountSelection() {
  const primary = selPrimaryEl.value;
  if (!primary) return;
  // `[data-fe-path]` is the row/card in all three views, and `aria-selected`
  // is the contract they already publish for a screen reader. No view-private
  // class, and nothing that is not a listing row can match.
  domSelCount.value = primary.querySelectorAll('[data-fe-path][aria-selected="true"]').length;
}
function scheduleRecount() {
  if (selTick) return;
  selTick = true;
  const later = (cb: () => void) =>
    typeof requestAnimationFrame === 'function' ? requestAnimationFrame(cb) : setTimeout(cb, 16);
  later(() => {
    selTick = false;
    recountSelection();
  });
}
/**
 * (Re)attach the row counter to the pane the bar is standing in.
 *
 * ⚠ The observer is torn down and rebuilt rather than left where it was: it
 * watches ONE `.fe__primary`, and after a focus change that is the wrong one —
 * the fallback count would then be the other pane's ticked rows.
 */
function reobserveSelection() {
  selMo?.disconnect();
  selMo = undefined;
  if (!hasSelection.value) {
    domSelCount.value = 0;
    return;
  }
  recountSelection();
  if (typeof MutationObserver === 'undefined' || !selPrimaryEl.value) return;
  selMo = new MutationObserver(scheduleRecount);
  selMo.observe(selPrimaryEl.value, {
    subtree: true,
    childList: true,
    attributes: true,
    attributeFilter: ['aria-selected'],
  });
}
watch(hasSelection, reobserveSelection);
onBeforeUnmount(() => selMo?.disconnect());

/** Never smaller than what the mode already proves: 'multi' is at least two
 *  rows even in the frame before the listing has been counted. */
const selectedCount = computed(() => {
  if (typeof props.selectionCount === 'number') return props.selectionCount;
  if (mode.value === 'none') return 0;
  return Math.max(domSelCount.value, mode.value === 'multi' ? 2 : 1);
});
const selectionLabel = computed(() => t('selection.count', { n: String(selectedCount.value) }));

function clearSelection() {
  /* The gesture the explorer already owns: a click on the listing's empty
   * ground (`@click.self="selection.clear()"` on `.fe__body`). Dispatched ON
   * that element, so `event.target === event.currentTarget` and Vue's `.self`
   * lets it through — the × therefore goes through the SAME clear a user's own
   * click does instead of a second path that can drift from it.
   * The emit is for a parent that would rather own the verb: an unhandled key
   * falls through `dispatchItemAction` without doing anything. */
  emit('action', 'clear-selection');
  selBodyEl.value?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
}
/* === /gorunum:v1-selbar ============================================== */

function fire(key: string) {
  emit('action', key);
}

/* === bag:b4 — narrow-mode state: the "⋯" overflow menu ===================
 * ⚠ `searchOpen` / `openSearch` / `closeSearch` went with the layout that
 * needed them: at 390px the field is in the row itself now, so there is no
 * collapsed state to expand and no magnifier to press. `.fe-toolbar--searching`
 * in styles/base.css is that state's leftover and matches nothing.
 */

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
  /* ⚠ Only in the narrow layout, and that is a duplicate being REMOVED rather
     than a layout rule. The wide header draws a Refresh button in its own
     trailing cluster, three glyphs to the left of this menu — so listing it
     here as well was the same verb twice on one row. The narrow header has no
     Refresh button (the cluster there is AI · "⋯" · the host's doors), so at
     that width the menu is the only door and it stays. Provable from inside
     this component: the button and this row are drawn by the same file, under
     the same `narrow` flag. */
  if (props.narrow) list.push({ key: 'refresh', label: t('toolbar.refresh') });
  list.push({
    key: 'density',
    label:
      density.value === 'compact'
        ? t('toolbar.density.comfortable')
        : t('toolbar.density.compact'),
  });
  /* wiring:d2 — narrow-mode ⋯ menu: BOTH inactive views are listed
     (list/grid/gallery); the old single toggle fell short with three modes. */
  if (props.viewMode !== 'list' && offersView('list')) list.push({ key: 'view-list', label: t('toolbar.view.list') });
  if (props.viewMode !== 'grid' && offersView('grid')) list.push({ key: 'view-grid', label: t('toolbar.view.grid') });
  if (props.viewMode !== 'gallery' && offersView('gallery')) list.push({ key: 'view-gallery', label: t('toolbar.view.gallery') });
  /* /wiring:d2 */
  /* ⚠ No navigation row here any more, and nothing replaced it.
     gezinti:g1 added one because the toolbar's own toggle was hidden while the
     narrow search field was expanded — and that expanded-search layout no
     longer exists (see the bag:b4 note at the top of this file). The hamburger
     in `.fe-toolbar__brand` is drawn at EVERY width and in every embed under
     exactly the condition this row was pushed under (`navEnabled !== false`),
     so the row could only ever be a second copy of a button that is always on
     screen. */
  /* koru:k1 — inspector toggle also reachable from the narrow overflow menu */
  list.push({ key: 'inspector', label: t('toolbar.inspector') });
  /* wiring:c1 — the theme gallery is opened from the ⋯ menu in narrow mode too */
  list.push({ key: 'theme', label: t('theme.menu') });
  /* zaman:z3 — the embed's time-zone setting. An embed has no settings dialog,
     so this menu is its only door; a host with its own settings surface (the
     filex web app) claims the settings half and drops this row, because its
     dialog already carries the same picker. */
  list.push({ key: 'timezone', label: t('tz.menu') });
  list.push({ key: 'shortcut-settings', label: t('shortcuts.settings.menu') }) /* wiring:c2 */;
  list.push({ key: 'tour', label: t('tour.restart') }); /* wiring:c4 */
  return list;
});

/** The narrow layout's whole menu: the folder/selection verbs first, then
 *  whatever is left of the settings half (which a host may have claimed —
 *  see `publishHeaderMenu`, hence the parameter rather than a closure). */
function narrowActions(util: ContextAction[]): ContextAction[] {
  const list: ContextAction[] = [];
  const writable =
    !props.trashActive && !props.atVirtualRoot && props.canWrite !== false;
  if (mode.value === 'none' && writable) {
    list.push({ key: 'new-folder', label: t('toolbar.new_folder') });
    if (props.pasteEnabled) list.push({ key: 'paste', label: t('ctx.paste') });
  }
  /* gorunum:v1-selbar — the selection's verbs are on screen in the selection
     bar now, and the ones it could not draw are in the bar's own "⋯". Listing
     them here as well would be a second door into the same room — the reason
     the drive header already carries only the settings half. */
  if (!hasSelection.value) list.push(...toolbarItems.value);
  if (!util.length) return list;
  if (list.length) list.push({ divider: true, key: 'bag-sep', label: '' });
  list.push(...util);
  return list;
}

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
/* ⚠ Read from the registry, not written here. This chip used to print a
 * hardcoded `⌘ K` / `Ctrl K`, which stopped being true the moment anyone
 * remapped the palette in the shortcut settings — the chip then named a
 * key that did nothing, on the one control whose whole job is to teach
 * that key. */
const paletteCombo = computed(() => shortcutHint('palette'));

/* tus:t1 — a button's tooltip names its key. Same source as the right-click
 * menu's key column, so the two can only ever say the same thing, and a verb
 * with no binding gets its plain label back rather than an empty bracket. */
function withKey(label: string, key: string): string {
  const combo = menuShortcutHint(key);
  return combo ? `${label} (${combo})` : label;
}

/* === gezinti:g2 — the collapse control says what it DOES ==================
 *
 * The button's label was `toolbar.nav` — "Navigation" / "Gezinti", a NOUN
 * naming the panel, which never says the button does anything to it. Measured
 * by the agent that narrowed the panel: the control works at every width; the
 * owner still could not find how to collapse the panel, because nothing on
 * screen said it could be collapsed. A static "Show or hide navigation" was
 * the stopgap; this is the fix, and it brings `sidenav.collapse` /
 * `sidenav.expand` back from being orphaned when the in-panel toggle went.
 *
 * ⚠⚠ TWO pairs, because `navOpen` means two different things.
 * `FileExplorer` hands down `isNarrow ? navDrawerOpen : sideNavExpanded`:
 *   · wide  — the panel is always on screen; the toggle moves it between a
 *             full column and a 56px icon rail. It is a WIDTH change, so:
 *             collapse / expand.
 *   · ≤560  — the panel is a DRAWER over the listing behind a scrim, and
 *             "closed" means it is not on screen at all. Nothing narrows, so
 *             "expand" would promise a width change to something that has no
 *             width; the pair there is open / close (`sidenav.close` is the
 *             string the drawer's own dismiss and its scrim already use, so
 *             the two doors out of the drawer say the same words).
 * Forcing one pair onto both widths would make the label wrong at one of them,
 * which is the failure this whole change is undoing.
 *
 * ⚠ ONE computed, bound to BOTH `title` and `aria-label`, so the tooltip and
 * the screen reader cannot be told two different things (the same rule the AI
 * affordance above is built on).
 *
 * ⚠ `aria-pressed` is left as it was, deliberately. With a state-bound label
 * it is arguably redundant, but the split toggle in TabBar.vue is built the
 * same way (changing label + `aria-pressed`) and changing one without the
 * other is the drift this wave exists to remove — that call belongs to one
 * pass over both, not to this line.
 */
const navToggleLabel = computed(() => {
  if (props.narrow) return props.navOpen ? t('sidenav.close') : t('sidenav.open');
  return props.navOpen ? t('sidenav.collapse') : t('sidenav.expand');
});

const drivePlaceholder = computed(() =>
  props.scopeLabel
    ? t('drive.search.placeholder', { scope: props.scopeLabel })
    : t('drive.search.placeholder_all'),
);

/** ⌘/Ctrl+K typed INSIDE the field. The global shortcut handler ignores
 *  keystrokes aimed at an input — which is correct, and it would otherwise
 *  make the hint on this very field a lie. */
function onSearchKeydown(ev: KeyboardEvent) {
  // The registry decides, so the chip beside this field and the key that
  // actually escalates stay the same key after a remap.
  if (eventMatchesShortcut(ev, 'palette')) {
    ev.preventDefault();
    ev.stopPropagation();
    emit('open-palette', localSearch.value);
    return;
  }
  /* gorunum:v3-shell — nothing on screen to narrow (Home): Enter means the
     same thing the chip does. Empty text is left alone — Enter on an empty box
     opening a palette nobody asked for is a keystroke doing something. */
  if (props.searchEscalates && ev.key === 'Enter' && localSearch.value) {
    ev.preventDefault();
    ev.stopPropagation();
    emit('open-palette', localSearch.value);
  }
}

/* === gorunum:v4-hostmenu — a host may take the settings half over ==========
 *
 * The header can end up with TWO dropdowns side by side: this component's "⋯"
 * and whatever the embedder put in `header-actions` (in the filex web app that
 * is the account avatar). Owner's decision, 2026-09-13, verbatim: *"`...`
 * bölgesini admin dropdown'ının içine alacağız."* — one menu in that corner,
 * not two.
 *
 * ⚠ It is a CLAIM, not a prop, and that is the whole design. This component
 * cannot ask "did my host fill the slot with a menu?": `$slots['header-actions']`
 * is truthy on every mount, because FileExplorer declares the pass-through
 * template unconditionally (the same trap its own `#brand` comment documents),
 * and a config flag would have to be threaded through a component this file
 * may not edit. So the toolbar ANNOUNCES the rows on a bubbling DOM event and
 * a host that intends to render them says so by setting `claimed` — the
 * `preventDefault()` shape, synchronous, with no registration order to get
 * right. Nobody listening (every embed today) → nothing changes and the "⋯"
 * keeps everything it had.
 *
 * ⚠ `composed: true`: the web-component build puts this DOM inside a shadow
 * root, and an uncomposed event stops at its boundary — i.e. the one build
 * where a host is most likely to be listening would never hear it.
 *
 * ⚠ Re-announced on every change, not once on mount: the labels are locale
 * strings and two of them are STATEFUL ("Compact view" ⇄ "Comfortable view",
 * and which view modes are inactive). A host holding the first list would
 * print yesterday's label for the rest of the session. Re-announcing is also
 * what lets a late listener claim, so the host does not have to win a race
 * with the explorer's mount.
 */
export interface HeaderMenuClaim {
  /** The rows, already localised, in the order this menu would draw them. */
  items: ContextAction[];
  /** Run one of them by key — the SAME handler the "⋯" uses, so a claimed row
   *  and an unclaimed one cannot drift apart in what they do. */
  run: (key: string) => void;
  /** Set to true by a host that will render `items` itself. */
  claimed: boolean;
}
const HEADER_MENU_EVENT = 'fe:header-menu';
const hostOwnsUtilities = ref(false);

function publishHeaderMenu() {
  const el = rootEl.value;
  if (!el) return;
  const detail: HeaderMenuClaim = {
    items: utilityActions.value.map((a) => ({ ...a })),
    run: (key: string) => onMoreSelect({ key, label: '' }),
    claimed: false,
  };
  el.dispatchEvent(
    new CustomEvent(HEADER_MENU_EVENT, { detail, bubbles: true, composed: true }),
  );
  hostOwnsUtilities.value = !!detail.claimed;
}

onMounted(() => void nextTick(publishHeaderMenu));
watch(utilityActions, () => void nextTick(publishHeaderMenu), { deep: false });
onBeforeUnmount(() => {
  /* ⚠ On `document`, not on our own root: by this point the element may
     already be detached and a bubbling event from it would reach nobody,
     leaving the host drawing rows for an explorer that is gone. */
  if (typeof document === 'undefined') return;
  document.dispatchEvent(
    new CustomEvent(HEADER_MENU_EVENT, {
      detail: { items: [], run: () => {}, claimed: false } as HeaderMenuClaim,
    }),
  );
});

/* surucu:d1 — the wide header renders the selection's actions as buttons (the
 * selection bar), so its "⋯" carries only the settings half; the narrow layout
 * renders no action buttons at all and still needs the whole list.
 * gorunum:v4-hostmenu — minus whatever the host took over. At 1440 that empties
 * the list and the "⋯" is not drawn at all; at 390 the folder verbs are still
 * this menu's, so it stays and simply loses its settings tail. */
const menuActions = computed<ContextAction[]>(() => {
  const util = hostOwnsUtilities.value ? [] : utilityActions.value;
  return props.narrow ? narrowActions(util) : util;
});

function openMore() {
  const r = moreBtnEl.value?.getBoundingClientRect();
  moreRef.value?.show({ clientX: r ? inlineEndX(r, dir.value) : 0, clientY: r ? r.bottom + 4 : 0 }, []); // ⚠ RTL: END edge, see openBarMore
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
    case 'timezone' /* zaman:z3 */:
      emit('open-timezone');
      break;
    case 'tour' /* wiring:c4 — bubbles to the FileExplorer root listener */:
      /* ⚠ From the TOOLBAR's root, not from the "⋯" button. That button is no
         longer always on screen — a host that claimed the settings half
         (gorunum:v4-hostmenu) leaves it unrendered, `moreBtnEl` null, and the
         optional chain then swallowed the whole restart in silence: the row
         was clicked, nothing happened, and nothing errored. Measured in a real
         browser — the row opened the shortcut editor fine and the tour row did
         nothing at all. The toolbar root is inside `.fe`, which is where the
         listener lives, so the event still arrives. */
      rootEl.value?.dispatchEvent(new CustomEvent('fe:tour-restart', { bubbles: true }));
      break;
    default:
      fire(a.key);
  }
}

/* === /bag:b4 === */
</script>

<template>
  <div
    ref="rootEl"
    class="fe-toolbar"
    :class="{
      'fe-toolbar--narrow': narrow /* bag:b4 */,
      /* surucu:d1 — ALWAYS on. It is no longer a variant: this is the only
         header filex has, and the modifier survives as the hook the one
         visual definition in styles/base.css was written against. Dropping it
         would mean moving those rules, not deleting a state. */
      'fe-toolbar--drive': true,
    }"
  >
    <!-- gorunum:v3-shell — the far-left corner, and it belongs to this row.
         The collapse control and the product mark used to be somewhere else
         each: the toggle inside the navigation panel's own header, one row
         down, and the mark up in a page bar that no longer exists — which left
         the top-left of every screen empty while the button that opens the
         panel sat below the line it is supposed to open.

         ⚠ Rendered for EVERY width and every embed, outside the two layout
         branches, so there is one definition of "the way back to the panel"
         instead of a wide copy and a narrow copy that drift.

         ⚠ The mark itself is a SLOT. This package has no product branding of
         its own and must not grow any: `<filex-explorer>` inside somebody
         else's app has no "filex" wordmark to show, and an embed that passes
         nothing renders nothing here — the toggle simply sits at the edge.
         Hidden at 390px even when a host fills it, where the row is three
         items wide already. -->
    <!-- ⚠ Not rendered at all when there is neither a panel to collapse nor a
         mark to show — a confined embed (`rootPath`, so `sideNav` defaults
         off) would otherwise reserve the gutter below for an empty box.
         ⚠ The mark counts in BOTH of its forms: the slot an SFC host fills and
         the `config.brand` a web-component host has to use instead. Asking only
         about the slot would have hidden the desktop app's own logo the moment
         it confined an explorer — the same silent blank corner this whole
         branch exists to stop. -->
    <div
      v-if="navEnabled !== false || !!$slots.brand || !!brandName || !!brandMarkUrl"
      class="fe-toolbar__brand"
      :class="{
        'fe-toolbar__brand--gutter': navEnabled !== false,
        /* gezinti:g3 — the panel collapsed to its rail, so this gutter has to
           collapse with it. `navOpen === false`, never `!navOpen`: the prop is
           optional, and an embedder that does not pass it must keep the
           expanded layout rather than lose its wordmark to a `undefined`. */
        'fe-toolbar__brand--rail': !narrow && navEnabled !== false && navOpen === false,
      }"
    >
      <!-- ⚠ `aria-pressed` without the `is-active` styling, and the two are
           not the same claim. A screen reader has to be told the panel is
           open; a sighted person can see it, and a permanently highlighted
           button in the top-left corner reads as "something is selected here"
           on a control nobody selected. The panel's presence IS its state. -->
      <button
        v-if="navEnabled !== false"
        type="button"
        class="fe-btn fe-btn--icon-only fe-toolbar__nav"
        :aria-pressed="!!navOpen"
        :title="navToggleLabel /* gezinti:g2 — a verb, and the state's verb */"
        :aria-label="navToggleLabel"
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
          <path d="M4 7h16M4 12h16M4 17h16" />
        </svg>
      </button>
      <!-- gezinti:g3 — the mark goes with the gutter. The rail is 56px and the
           brand's content measures 113px, so it cannot be shown in the
           collapsed state at any size; the reference does the same thing
           (measured signed-in: logo and wordmark both gone the moment the
           panel collapses, `☰` re-centred in the rail, search field slid
           left). ⚠ `navOpen !== false` — an embedder that passes no state at
           all keeps its mark. -->
      <div v-if="!narrow && navOpen !== false" class="fe-toolbar__mark">
        <slot name="brand">
          <img v-if="brandMarkUrl" class="fe-toolbar__markimg" :src="brandMarkUrl" alt="" />
          <span v-if="brandName">{{ brandName }}</span>
        </slot>
      </div>
    </div>

    <!-- === gorunum:v2-topbar — the wide header ==========================
         ONE header, in the reference shell's shape, for every profile: the
         search field takes the row and the trailing cluster carries what
         belongs to the APP rather than to a listing. The page chrome that
         used to sit above the explorer (wordmark, admin link, sign out,
         language) is gone, so this row is the only header on the screen.

         ⚠ What LEFT this row, and the single door each one kept — no control
         was dropped from the build:
           · navigation collapse → CAME BACK, to `.fe-toolbar__brand` above,
             at every width. It had gone to the panel's own edge control, which
             put the button that opens the panel one row BELOW the line it
             opens and left this corner empty; the panel no longer draws its
             own (except as the drawer's close button at 390px).
           · Up one level        → the parent crumb, always drawn (the trail
             collapses to `root › … › parent › current`, never past it).
             The keyboard binding is untouched.
           · New Folder / Upload → the panel's primary block
             (`sidenav-upload` / `sidenav-new-folder`, or its "+ New" menu in
             the drive profile). They come BACK here when a deployment has no
             panel at all (`sideNav: false`, or a confined root), because then
             there is nowhere else for them to live.
           · density, theme      → user settings, and the "⋯" menu below.
           · Details ⓘ, views    → the breadcrumb row, beside the crumbs.
         -->
    <template v-if="!narrow">
    <div class="fe-toolbar__primary">
      <!-- No navigation panel in this deployment → no primary block to hold
           these, so they are the one door and they render here. -->
      <button
        v-if="navEnabled === false && mode === 'none' && !trashActive && !atVirtualRoot && canWrite !== false"
        type="button"
        class="fe-btn fe-btn--primary"
        :title="withKey(t('toolbar.new_folder'), 'new-folder')"
        @click="emit('new-folder')"
      >
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('new-folder')"></span>
        <span class="fe-btn__label">{{ t('toolbar.new_folder') }}</span>
      </button>
      <button
        v-if="navEnabled === false && !atVirtualRoot && canWrite !== false"
        type="button"
        class="fe-btn fe-btn--icon-only"
        :title="withKey(t('toolbar.upload'), 'upload')"
        :aria-label="t('toolbar.upload')"
        @click="emit('upload')"
      >
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('upload')"></span>
      </button>

      <!-- gorunum:v1-selbar — the selection's actions used to be a row of
           labelled buttons here (Details / Cut / Copy / Paste / Star / a red
           filled Delete). They are now the selection bar, under the tab strip,
           where the reference puts them. -->

      <button
        v-if="pasteEnabled && mode === 'none' && !trashActive && !atVirtualRoot && canWrite !== false"
        type="button"
        class="fe-btn"
        :title="withKey(t('ctx.paste'), 'paste')"
        @click="fire('paste')"
      >
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('paste')"></span>
        <span class="fe-btn__label">{{ t('ctx.paste') }}</span>
      </button>
    </div>

    <div class="fe-toolbar__search-group fe-toolbar__search-group--drive">
      <!-- gorunum:v1 — ONE search field, in every shell. This used to be two
           blocks — a bare input in a bordered group for the classic profile
           and a wide field with the ⌘K chip for the drive one; the same box
           in two shapes is two products. It carries both class names on
           purpose: the `fe-drivesearch*` half is the one visual definition
           (styles/base.css), the `fe-search*` half is the hook the onboarding
           tour has always looked for.
           ⚠ The chip prints `shortcutHint('palette')` and the field escalates
           through `eventMatchesShortcut(ev, 'palette')` — never a literal key
           (web/tests/ui/shortcutHints.test.ts). -->
      <div class="fe-search fe-drivesearch" data-testid="drive-search">
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
          class="fe-search__input fe-drivesearch__input"
          :placeholder="drivePlaceholder"
          :value="localSearch"
          :aria-label="t('toolbar.search')"
          @input="onSearchInput"
          @keydown="onSearchKeydown"
        />
        <!-- gorunum:v1-advsearch — the filter affordance the header field has
             always been specced with, beside the palette chip. It opens the
             advanced dialog carrying whatever is already typed. -->
        <button
          type="button"
          class="fe-drivesearch__tune"
          :title="t('advsearch.open')"
          :aria-label="t('advsearch.open')"
          data-testid="drive-search-advanced"
          @click="emit('open-advanced-search', localSearch)"
        >
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
          <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('filter')"></span>
        </button>
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
    </div>

    <!-- gorunum:v2-topbar — the trailing cluster. Refresh and the "⋯" are the
         explorer's own; the AI mark is drawn but not yet wired; the host's
         account-level doors arrive through the slot. -->
    <div class="fe-toolbar__tail">
      <button
        type="button"
        class="fe-btn fe-btn--icon-only"
        :title="withKey(t('toolbar.refresh'), 'refresh')"
        :aria-label="t('toolbar.refresh')"
        @click="emit('refresh')"
      >
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('refresh')"></span>
      </button>
      <!-- gorunum:v2-topbar — the AI assistant, announced and not pretending.
           ⚠ `disabled`, not "opens an empty panel": a control that answers a
           click with nothing is read as broken, and the next thing that
           happens is a bug report about a feature that was never claimed. It
           says what it is and that it is not here yet, in both languages, and
           it will become live in place — the glyph does not move under
           anybody's finger the day it starts working. -->
      <span class="fe-toolbar__soon-wrap">
        <button
          type="button"
          class="fe-btn fe-btn--icon-only fe-toolbar__soon"
          disabled
          :aria-label="t('ai.assistant.soon')"
          aria-disabled="true"
          data-testid="header-ai"
          @click.prevent
        >
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
          <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('ai')"></span>
        </button>
        <!-- gorunum:v4-hostmenu — the words, on hover, and nowhere else at
             rest. Owner's decision, 2026-09-13, verbatim: *"Coming soon
             yazısını kaldıralım, üstüne gelince gözüksün."*
             ⚠ `aria-hidden`, and the button keeps `aria-label` with the SAME
             sentence: hover is a mouse gesture that a touch screen and a
             keyboard do not have, so the words have to be in the accessible
             name — and if they were in both, a screen reader would read the
             sentence twice.
             ⚠ The native `title` went with the pill. It is not an accessible
             name (the label above is), it never appears on touch, and leaving
             it would stack the OS tooltip on top of this one. -->
        <span
          class="fe-toolbar__soon-tip"
          aria-hidden="true"
          data-testid="header-ai-tip"
        >{{ t('ai.assistant.soon') }}</span>
      </span>
      <!-- Density, theme, the shortcut editor, the tour and the other view
           modes. Everything the header does not draw is one click away here;
           nothing is removed from the build. A menu is the one place a
           duplicate is allowed to live.
           gorunum:v4-hostmenu — ⚠ not drawn when there is nothing left in it:
           a host that claimed the settings half (the web app's avatar menu)
           empties this list at 1440, and a button that opens an empty menu is
           the "second control doing the same job" this pass removes. -->
      <button
        v-if="menuActions.length > 0"
        ref="moreBtnEl"
        type="button"
        class="fe-btn fe-btn--icon-only"
        :title="t('toolbar.more')"
        :aria-label="t('toolbar.more')"
        aria-haspopup="menu"
        data-testid="drive-more"
        @click="openMore"
      >
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('more')"></span>
      </button>
      <!-- The host's own doors (admin panel, settings, sign out). Empty in an
           embed, which is why the cluster is a slot and not a prop: an
           embedder that has no account chrome renders nothing at all here. -->
      <slot name="header-actions"></slot>
    </div>
    </template>

    <!-- surucu:d1 — narrow layout: [☰] [ search ] [⋯] + the host cluster.
         ⚠ The ONLY narrow layout now. There used to be a second one that hid
         search behind a magnifier and expanded it over the row; it was the
         right trade when the row also carried Up / Upload / actions, and this
         one does not — "+ New" lives in the drawer, upload has the floating
         button this width already draws (`.fe-fab`), and Up went to the parent
         crumb. So the field keeps the row, and only the ⌘K chip goes (CSS): a
         phone cannot press it, and the palette stays in the "⋯" menu.
         gorunum:v2-topbar — the trailing cluster is the wide header's, to the
         glyph: a control on screen at 1440 and unreachable at 390 is a
         capability split, not a layout. -->
    <template v-else>
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
        <!-- gorunum:v1-advsearch — the filter affordance the header field has
             always been specced with, beside the palette chip. It opens the
             advanced dialog carrying whatever is already typed.
             ⚠ Added to ALL THREE field blocks on purpose: this file's own note
             says the same box in two shapes is two products, so a control that
             existed only in the drive shell would re-open exactly that split. -->
        <button
          type="button"
          class="fe-drivesearch__tune"
          :title="t('advsearch.open')"
          :aria-label="t('advsearch.open')"
          data-testid="drive-search-advanced"
          @click="emit('open-advanced-search', localSearch)"
        >
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
          <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('filter')"></span>
        </button>
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
      <div class="fe-toolbar__tail">
        <!-- gorunum:v2-topbar — the same cluster as the wide header. A control
             that is on screen at 1440 and unreachable at 390 is a capability
             split, not a layout, so the AI mark and the host's doors come
             along; only Refresh stays behind (it is in the "⋯"). -->
        <span class="fe-toolbar__soon-wrap">
          <button
            type="button"
            class="fe-btn fe-btn--icon-only fe-toolbar__soon"
            disabled
            :aria-label="t('ai.assistant.soon')"
            aria-disabled="true"
            data-testid="header-ai"
            @click.prevent
          >
            <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
            <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('ai')"></span>
          </button>
          <!-- gorunum:v4-hostmenu — the same affordance as the wide header, to
               the glyph. ⚠ A phone has no hover at all, so here the accessible
               name is the ONLY door to these words — which is exactly why it
               carries the whole sentence and not a `title`. -->
          <span
            class="fe-toolbar__soon-tip"
            aria-hidden="true"
            data-testid="header-ai-tip"
          >{{ t('ai.assistant.soon') }}</span>
        </span>
        <button
          v-if="menuActions.length > 0"
          ref="moreBtnEl"
          type="button"
          class="fe-btn fe-btn--icon-only"
          :title="t('toolbar.more')"
          :aria-label="t('toolbar.more')"
          aria-haspopup="menu"
          data-testid="drive-more"
          @click="openMore"
        >
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
          <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('more')"></span>
        </button>
        <slot name="header-actions"></slot>
      </div>
    </template>


    <!-- The "⋯" menu of whichever layout is on screen.
         ⚠ Rendered unconditionally, outside the three layout branches. It used
         to live inside the narrow non-drive branch alone, which left both drive
         layouts (`data-testid="drive-more"`, wide and narrow) with a button
         whose `moreRef` was null — the click resolved to nothing and the whole
         settings half of the menu was unreachable in that profile. -->
    <ContextMenu
      ref="moreRef"
      :locale="locale"
      :theme="theme || 'auto'"
      :sheet="coarse"
      :actions="menuActions"
      @select="onMoreSelect"
    />

    <!-- === gorunum:v1-selbar — the selection bar ======================
         Teleported into the slot this component put right after the breadcrumb
         (see the script block). With no slot to mount into the Teleport is
         disabled and the bar renders here instead, so a bare <Toolbar> still
         offers the selection's actions. -->
    <Teleport :to="selSlot || 'body'" :disabled="!selSlot">
      <div
        v-if="hasSelection"
        class="fe-selbar"
        :class="{ 'fe-selbar--inline': !selSlot }"
        role="toolbar"
        :aria-label="selectionLabel"
        data-testid="selection-bar"
      >
        <span class="fe-selbar__count" role="status" data-testid="selection-count">{{
          selectionLabel
        }}</span>

        <div ref="selBarEl" class="fe-selbar__actions">
          <button
            v-for="a in visibleBarItems"
            :key="a.key"
            type="button"
            class="fe-selbar__btn"
            :class="{ 'fe-selbar__btn--danger': a.danger, 'is-disabled': a.disabled }"
            :disabled="a.disabled"
            :title="a.title || withKey(a.label, a.key) /* tasi:m1 — a greyed
                   row says WHY it is grey; everything else says what it does
                   and which key does it */"
            :aria-label="a.label"
            :data-key="a.key"
            :data-testid="`selbar-${a.key}`"
            @click="fire(a.key)"
          >
            <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
            <span class="fe-icon" aria-hidden="true" v-html="iconFor(a)"></span>
          </button>

          <button
            v-if="barMenuItems.length > 0"
            ref="selMoreBtnEl"
            type="button"
            class="fe-selbar__btn"
            :title="t('toolbar.more')"
            :aria-label="t('toolbar.more')"
            aria-haspopup="menu"
            data-testid="selbar-more"
            @click="openBarMore"
          >
            <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
            <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('more')"></span>
          </button>

          <!-- Invisible measuring strip: every icon action is always rendered
               here, as the SAME button the visible row renders, so the fold
               arithmetic runs off widths something really has.
               ⚠ `data-key` is on BOTH copies: which icons are on screen is a
               function of the viewport (that is what folding means), so a test
               that wants the list this selection OFFERS has to read it from a
               strip the fold does not touch. -->
          <div ref="measureEl" class="fe-selbar__measure" aria-hidden="true">
            <button
              v-for="a in barSplit.icons"
              :key="'m-' + a.key"
              type="button"
              class="fe-selbar__btn"
              :data-key="a.key"
              tabindex="-1"
            >
              <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
              <span class="fe-icon" aria-hidden="true" v-html="iconFor(a)"></span>
            </button>
          </div>
        </div>

        <button
          type="button"
          class="fe-selbar__close"
          :title="t('selection.clear')"
          :aria-label="t('selection.clear')"
          data-testid="selection-clear"
          @click="clearSelection"
        >
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
          <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('close')"></span>
        </button>
      </div>
    </Teleport>

    <ContextMenu
      ref="selMoreRef"
      :locale="locale"
      :theme="theme || 'auto'"
      :sheet="coarse"
      :actions="barMenuItems"
      @select="(a) => fire(a.key)"
    />
    <!-- === /gorunum:v1-selbar === -->
  </div>
</template>
