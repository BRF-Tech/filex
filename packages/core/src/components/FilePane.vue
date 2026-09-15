<script setup lang="ts">
/**
 * FilePane — pane:p1 THE pane. One component, rendered once per half of the
 * window.
 *
 * ⚠⚠ WHY THIS FILE EXISTS, and why adding the missing controls to the old
 * `SecondaryPane` would have been the wrong fix for the second time.
 *
 * The split view used to be two IMPLEMENTATIONS of a pane: `.fe__primary`
 * inside `FileExplorer.vue` on the left, `SecondaryPane.vue` on the right.
 * They shared the three view components and nothing else, so every control
 * that hangs off a listing had to be written twice or it existed only on the
 * left. It was written twice exactly once — the right-click menu, in July
 * 2026, after the owner reported the two halves offering different actions —
 * and the underlying shape was left alone. Two months later the right half was
 * missing six more things: the breadcrumb was a private 28-line computed
 * instead of `Breadcrumb.vue`, and there was no filter row, no view switcher,
 * no sort control, no per-folder column menu and no selection bar at all.
 *
 * So the rule this file enforces is not "keep them in sync". It is that there
 * is only one of them:
 *
 *     a feature added to a pane cannot miss the other pane,
 *     because there is only one pane.
 *
 * ⚠ THE THREE DIFFERENCES THAT ARE REAL. Everything else is identical by
 * construction:
 *
 *   1. **Which pane has the keyboard** — `focused`, drawn as the accent line
 *      (`.fe-pane--focus`). It is the only signal of where a shortcut lands,
 *      so it is a prop and never an internal guess.
 *   2. **Where the listing came from** — the main pane's rows are produced by
 *      the explorer's own loader, which also serves search, the trash, the
 *      panel's virtual views and encrypted folders; the split pane asks the
 *      backend for one folder. Expressed as `selfDriven`: false = the host
 *      hands over `rows` / `loading` / `error`, true = this pane runs
 *      `loadFolder()` below. Nothing else in the component branches on it.
 *   3. **Whether it can be closed** — `closable`. A window always has a main
 *      pane; the second one is the one you opened.
 *
 * ⚠ WHAT STAYS WITH THE HOST, and why none of it is pane state: the API
 * client, the clipboard, the context menu and `dispatchItemAction`, uploads,
 * quick look, the inspector, the onboarding tour's targets and the tab strip.
 * Those are properties of the WINDOW — there is one clipboard and one context
 * menu no matter how many panes are open — so a pane that owned any of them
 * would be a second window.
 *
 * ⚠ Selection is HOST-HELD and passed in, which is not a compromise: the
 * explorer routes cut/copy/paste, the inspector, the toolbar's count and every
 * keyboard shortcut through "the active pane's selection", so it needs to hold
 * both. It holds two instances of ONE composable (`useSelection`), which is
 * the same single-definition rule one level up. Same for the filters
 * (`lib/fileFilters`) and the sort (`lib/sortOrder`).
 *
 * ⚠⚠ THE SORT IS PER PANE and this component is what makes it so. It creates
 * its own `SortStore` and `provide()`s it, so the filter row's sort control
 * and the list view's column headers inside THIS pane drive THIS pane's order
 * — measured 2026-09-13: with the old module-level singleton, choosing
 * "Size ↓" in the right pane silently re-ordered the left one. The main pane
 * keeps the default store, because the explorer's per-folder view memory reads
 * and writes exactly that state.
 *
 * Unscoped styles (`fe-pane*`, and the `fe__primary` / `fe-subhead` / `fe__body`
 * classes the layout already owns) — webcomponent data-v rule.
 */
import { computed, provide, ref, watch } from 'vue';

import type { FileApi } from '../composables/useFileApi';
import type { ClickMod } from '../composables/useRowTouch';
import type { FileNode, ViewMode } from '../types/FileNode';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import {
  filterListing,
  injectTrashRow,
  hydrateTrashRow,
  isVirtualViewPath,
} from '../lib/listing';
import { iconFamilyFor } from '../lib/fileIcons';
import { applyFilters, filtersActive, type DriveFilters } from '../lib/fileFilters';
import {
  SORT_STORE_KEY,
  createSortStore,
  useSortStore,
  type ListingOrder,
  type SortStore,
} from '../lib/sortOrder';
import { hasInternalDrag, internalDragItems, internalDragOrigin } from '../lib/dragOut';
import Breadcrumb from './Breadcrumb.vue';
import FilterBar from './FilterBar.vue';
import ViewSwitcher from './ViewSwitcher.vue';
import ListView from './ListView.vue';
import GridView from './GridView.vue';
import GalleryView from './GalleryView.vue';

/** The same literals the view components hardcode for the internal DnD
 *  channel; `-src` carries the ORIGIN DIRECTORY so the host can move across
 *  panes and undo it afterwards. */
const FE_DND_MIME = 'application/x-brf-files';
const FE_DND_SRC_MIME = 'application/x-brf-files-src';

export type PaneId = 'main' | 'split';

const props = withDefaults(
  defineProps<{
    /** Which half this is. Also the `data-pane` hook every measurement uses. */
    paneId: PaneId;
    api: FileApi;
    locale: LocaleCode;
    /** Resolved theme — forwarded to the children whose popovers are teleported
     *  out of `.fe`, where the token scope no longer reaches them. */
    theme?: ThemeMode;
    /** This pane owns the keyboard. The accent line, and nothing else. */
    focused?: boolean;
    /** Offer the × (the split pane). */
    closable?: boolean;

    // ── addressing ────────────────────────────────────────────────────
    /** The pane's location, in the host's user-path form. With
     *  `selfDriven` it is the OPENING location and this pane moves on from
     *  there; otherwise it is the address, full stop. */
    path: string;
    /** true → this pane fetches its own folder (see difference 2 above). */
    selfDriven?: boolean;
    /** user path → wire `<adapter>://<rel>`. */
    qualify: (p: string) => string;
    /** wire → user path (multi-storage aware). */
    toUser: (wire: string) => string;
    /** rootFloor clamp — keeps the pane inside the confine. */
    clamp: (p: string) => string;
    /** Override for the storage crumb's label. ⚠ Optional, and normally
     *  omitted: the pane derives it from its OWN wire path (`paneAdapter`), so
     *  two panes sitting in two different storages each name their own. The
     *  old right-hand pane was handed the window's label and printed "/" for a
     *  folder called `thumbfix` — measured 2026-09-13, and the exact shape of
     *  bug a second implementation produces. */
    rootLabel?: string;
    /** The confine in WIRE form, for `Breadcrumb`'s own floor handling. */
    rootPath?: string;
    /** rootFloor in user-path form ('' when unconfined). */
    floor?: string;
    /** Multi-storage mode: '' is the virtual drives root (no fetch). */
    multiRoot?: boolean;
    /** Synthesized storage rows for the virtual drives root. */
    virtualRows?: () => FileNode[];
    /** Mirror the virtual `.trash` row at a storage root. Defaults on. */
    trashVisible?: boolean;
    /** The navigation panel is already offering Trash, so no pane draws the
     *  virtual row. Passed down rather than worked out here: the panel is a
     *  sibling of BOTH panes, and a pane that guessed would list a row the
     *  other one does not. */
    navOffersTrash?: boolean;

    // ── the rows, when the host drives them ───────────────────────────
    rows?: FileNode[];
    loading?: boolean;
    error?: string;
    /** Where these rows got their order (`lib/sortOrder`). A ranked answer
     *  keeps the server's order and closes every sort control over it. */
    order?: ListingOrder;

    // ── chrome ────────────────────────────────────────────────────────
    viewMode: ViewMode;
    viewModes?: ViewMode[];
    /** Draw the address row. Off for a host that supplies its own heading. */
    showCrumbs?: boolean;
    /** Draw the filter row (and with it the sort control). */
    showFilterBar?: boolean;
    /**
     * surucu:d1-scope — `find` reduces the filter row to its name box (see
     * `FilterBar`'s own note). The host asks for it where the BODY is not a
     * listing of files at all — Home. It is NOT asked for at the multi-storage
     * root: a pane knows perfectly well when it is showing the drive list
     * (`atVirtualRoot`), and making the host say so for the main pane would
     * leave the split pane's own drive list with four chips that narrow nothing.
     */
    filterMode?: 'full' | 'find';
    /** What the name box says it narrows, already translated. Ignored at the
     *  drive root, which names itself. */
    findLabel?: string;
    /** Draw the view switcher in the address row. */
    showViewSwitcher?: boolean;
    /** Override for what the last crumb's "Subfolders" chevron lists. ⚠
     *  Optional, and normally omitted: the pane already HOLDS the folder's
     *  listing, so it answers "what is inside this folder" from its own rows.
     *  Passing it from the host is how only one pane ended up with the
     *  chevron. */
    crumbSubfolders?: { label: string; adapterPath: string }[];
    /** Per-folder memory key, for the column menu's two folder rows. */
    folderKey?: string;
    /** Print each row's parent folder (a search, a cross-folder view). */
    showParentPath?: boolean;
    /** Paths that were cut — dimmed, and never dragged. */
    clipped?: Set<string>;
    /** A second narrowing composed AFTER this pane's own filter row (the
     *  advanced-search dialog's). Null when there is none. */
    extraFilters?: DriveFilters | null;
    canWrite?: boolean;
    canPaste?: boolean;

    // ── the listing's own state, held by the host ─────────────────────
    selected: Set<string>;
    filters: DriveFilters;

    // ── forwarded to the view components ──────────────────────────────
    thumbSrc?: (n: FileNode) => string | null;
    keepBadgeFor?: (n: FileNode) => 'kept' | 'syncing' | 'cloud' | 'partial' | null;
    starredIds?: Set<number>;
    starEnabled?: boolean;
    apiBase?: string;
    authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
    authCredentials?: RequestCredentials;
    /** No reading file bytes for a card preview inside an encrypted folder. */
    e2eActive?: boolean;

    /** The host is drawing the body itself (Home, a dead deep link, the
     *  encrypted lock screen) — those are WINDOW states with no listing
     *  behind them, so the pane's own state chain must not also run. */
    bodyOverride?: boolean;
  }>(),
  {
    showCrumbs: true,
    showFilterBar: true,
    showViewSwitcher: true,
    trashVisible: true,
  },
);

const emit = defineEmits<{
  /** This pane was touched — it now owns the keyboard. */
  (e: 'activate'): void;
  (e: 'close'): void;
  /** The pane's location changed (self-driven) or was asked to change. */
  (e: 'navigate', path: string): void;
  /** A row was double-clicked / activated. */
  (e: 'open', node: FileNode): void;
  /** The virtual `.trash` row was opened — the trash view carries the restore
   *  actions and belongs to the main pane, so the host decides. */
  (e: 'open-trash'): void;
  (e: 'click-row', node: FileNode, mod: { ctrl: boolean; shift: boolean; touch?: boolean }): void;
  /** Right-click. `null` = empty space (nothing selected → "Paste" only). */
  (e: 'context', node: FileNode | null, ev: MouseEvent): void;
  (e: 'clear-selection'): void;
  (e: 'display-order', nodes: FileNode[]): void;
  (e: 'item-drag-start', node: FileNode, ev: DragEvent): void;
  (e: 'item-drop-into', target: FileNode, ev: DragEvent): void;
  /** A drop on the pane background / a crumb: the host owns move-vs-copy. */
  (e: 'transfer', p: { sources: string[]; targetWire: string; originWire?: string }): void;
  (e: 'update:viewMode', v: ViewMode): void;
  (e: 'update:filters', v: DriveFilters): void;
  (e: 'crumb-context', payload: { x: number; y: number; adapterPath: string; label: string }): void;
  (e: 'copy-path', adapterPath: string): void;
  (e: 'crumb-drop', adapterPath: string, ev: DragEvent): void;
  (e: 'new-folder'): void;
  (e: 'upload'): void;
  (e: 'paste'): void;
  (e: 'select-all'): void;
  (e: 'clear-filters'): void;
  (e: 'star-change', node: FileNode, value: boolean): void;
  /** The listing failed and the person asked for it again. */
  (e: 'retry'): void;
}>();

const { t } = useLocale(() => props.locale);

/* ── the pane's sort ───────────────────────────────────────────────────
 * ⚠⚠ The main pane keeps the DEFAULT store on purpose. The explorer's
 * per-folder view memory (`lib/viewPrefs`) restores and records through the
 * module-level `applySort` / `activeSortKey`, i.e. through that store; handing
 * the main pane a fresh one would leave the memory writing to a piece of state
 * nothing renders. The split pane gets its own, which is what makes two sort
 * controls two sorts. */
const sort: SortStore = props.paneId === 'main' ? useSortStore() : createSortStore();
provide(SORT_STORE_KEY, sort);

/* ── self-driven listing (difference 2) ──────────────────────────────── */

const ownPath = ref<string>('');
const ownRows = ref<FileNode[]>([]);
const ownLoading = ref(false);
const ownError = ref('');
/** koru:k1 — the folder's RBAC level as the last `index` reported it. Kept
 *  because the details panel asks the FOCUSED pane for it, and until now the
 *  split pane had no answer to give. */
const ownPerm = ref('');

function isStorageRow(n: FileNode): boolean {
  return iconFamilyFor(n) === 'storage';
}

/** THE location this pane is showing, whichever side is driving. */
const panePath = computed(() => (props.selfDriven ? ownPath.value : (props.path ?? '')));
const paneRows = computed<FileNode[]>(() =>
  props.selfDriven ? ownRows.value : (props.rows ?? []),
);
const paneLoading = computed(() => (props.selfDriven ? ownLoading.value : !!props.loading));
const paneError = computed(() => (props.selfDriven ? ownError.value : (props.error ?? '')));

const atVirtualRoot = computed(
  () => !!props.multiRoot && !(panePath.value ?? '').replace(/^\/+|\/+$/g, ''),
);

/**
 * One folder, through the SAME `api.index` + `lib/listing` helpers the host's
 * own loader uses — so the two panes cannot end up listing different rows for
 * the same folder (the internal-entry filter and the virtual `.trash` row are
 * both shared functions, not copies).
 */
async function loadFolder(target?: string): Promise<void> {
  const requested = props.clamp(target ?? ownPath.value ?? '');
  ownLoading.value = true;
  ownError.value = '';
  try {
    // Multi-storage virtual root: synthesize the drives list, no backend call.
    if (props.multiRoot && !requested) {
      ownRows.value = props.virtualRows ? props.virtualRows() : [];
      ownPerm.value = ''; /* the drive list is not a folder — no level to carry */
      ownPath.value = '';
      emit('navigate', '');
      return;
    }
    const resp = await props.api.index(props.qualify(requested));
    ownPerm.value = ((resp as { perm?: unknown }).perm as string) || '';
    ownRows.value = filterListing(resp.files);
    if (
      injectTrashRow(ownRows.value, resp.adapter, resp.dirname, props.trashVisible !== false, {
        navOffersTrash: props.navOffersTrash,
      })
    ) {
      void hydrateTrashRow(ownRows.value, resp.adapter, props.api);
    }
    ownPath.value = props.toUser(resp.dirname);
    emit('navigate', ownPath.value);
  } catch (err) {
    ownError.value = err instanceof Error ? err.message : String(err);
  } finally {
    ownLoading.value = false;
  }
}

if (props.selfDriven) void loadFolder(props.path ?? '');

/** Navigate THIS pane. Self-driven panes fetch; a host-driven pane asks its
 *  host, which is the only thing that knows how to leave a search or a
 *  virtual view on the way. */
function goTo(target: string) {
  emit('activate');
  if (props.selfDriven) void loadFolder(target);
  else emit('navigate', target);
}

/* ── the address row ───────────────────────────────────────────────────
 * `Breadcrumb.vue`, not a private crumb computed. It already knows about the
 * multi-storage "/" crumb, the confined root, the virtual segments' labels,
 * the ✏ path editor and drops onto a crumb — all of which the old right-hand
 * pane either lacked or re-derived. */
/**
 * ⚠⚠ A VIRTUAL VIEW IS NOT QUALIFIED, and this line is the whole of bug #1.
 *
 * `qualify()` turns a user path into `<adapter>://<rel>` for the backend, and
 * in multi-storage mode it does that by splitting the FIRST SEGMENT off as the
 * storage. Handed `.starred` it answers `.starred://` — perfectly reasonable
 * for `thumbfix`, catastrophic for a sentinel: from there the sentinel is the
 * ADAPTER, `Breadcrumb` draws it as the storage crumb, and a storage crumb is
 * never run through `virtualSegmentLabel`. The resolver was reached on every
 * render and could not possibly match. (Before tonight the host handed
 * `Breadcrumb` its own `dirname`/`adapter` refs, which the virtual views set to
 * `.starred` / `''` — unqualified — so this never arose until each pane began
 * deriving its own.)
 *
 * A view has no storage: `.recent` spans every one of them. So the wire form IS
 * the sentinel, `paneAdapter` below comes out empty, and the segment reaches
 * the resolver as a segment.
 */
const paneWire = computed(() =>
  isVirtualViewPath(panePath.value) ? panePath.value : props.qualify(panePath.value),
);
/** The adapter THIS pane is inside. In multi-storage mode the two panes can
 *  be in different storages, so it is read off the pane's own wire path
 *  rather than taken from the window. */
const paneAdapter = computed(() => {
  const w = paneWire.value;
  const i = w.indexOf('://');
  return i === -1 ? '' : w.slice(0, i);
});

function onCrumbNavigate(adapterPath: string) {
  emit('activate');
  if (!props.selfDriven) {
    emit('navigate', adapterPath);
    return;
  }
  // Multi-storage emits '' for the global "/" crumb — the virtual drives root.
  if (props.multiRoot && !adapterPath) {
    void loadFolder('');
    return;
  }
  void loadFolder(props.toUser(adapterPath));
}

/* ── the rows this pane draws ──────────────────────────────────────────
 * The filter row's narrowing, then the host's extra one (the advanced-search
 * dialog composes with the chips rather than replacing them), then the order.
 *
 * ⚠ Sorted HERE, at the pane, and not inside each view: "sorted by size" is a
 * fact about the LISTING, and three components each sorting for themselves is
 * how the grid and the list drifted apart in the first place (filex #67). */
const displayFiles = computed<FileNode[]>(() => {
  const base = applyFilters(paneRows.value, props.filters);
  return sort.sortListing(
    props.extraFilters ? applyFilters(base, props.extraFilters) : base,
    props.order ?? 'sort',
  );
});
const filtersOn = computed(() => filtersActive(props.filters) || !!props.extraFilters);

/* surucu:d1-scope — the filter row's shape, decided HERE rather than by the
 * host, for the reason the `filterMode` prop documents: the drive list is a
 * state a pane is in, and both panes can be in it independently. */
const paneFilterMode = computed<'full' | 'find'>(() =>
  atVirtualRoot.value ? 'find' : (props.filterMode ?? 'full'),
);
const paneFindLabel = computed(() =>
  atVirtualRoot.value ? t('filter.find.storages') : (props.findLabel ?? ''),
);

/* What the last crumb's chevron lists.
 *
 * ⚠ Off `paneRows`, not `displayFiles`: the chevron answers "what is inside
 * this folder", and a filter row that is hiding half the listing must not also
 * decide where you are allowed to navigate — a folder you cannot see is still a
 * folder you can walk into.
 * ⚠ An empty array rather than undefined when there are no subfolders: the
 * prop's presence is what draws the control, and a control that vanishes in
 * leaf folders is one nobody learns to look for. */
const paneSubfolders = computed(() =>
  paneRows.value
    .filter((f) => f.type === 'dir')
    .map((f) => ({ label: f.basename, adapterPath: f.path })),
);

/* ── selection + activation ────────────────────────────────────────────
 * The pane never mutates the selection; it reports the gesture and the host's
 * `useSelection` instance for this pane answers. One set of Ctrl/Shift
 * semantics for both panes — the old right pane had a simplified copy in
 * which Shift behaved as Ctrl. */
/* issue #26, second round — a press on the NAME opens, so the second click of
 * a habitual double-click lands on whatever the NEW listing put under the
 * pointer (the folder already opened on the first click). Without this guard
 * that click would open or select a row the user never aimed at, and a file's
 * name would open twice (two viewer tabs). */
const NAME_OPEN_GUARD_MS = 500;
let nameOpenedAt = 0;

function withinNameOpenGuard(): boolean {
  return Date.now() - nameOpenedAt < NAME_OPEN_GUARD_MS;
}

function onViewClick(n: FileNode, mod: ClickMod) {
  if (withinNameOpenGuard()) return;
  /* issue #26, second round — the reporter's rule on every device: a press on
   * the item's name opens it, mouse or finger, selection or not. Ctrl/shift
   * still mean "add to / extend the selection", so a modifier click on a name
   * selects as it always did. The checkbox selects on its own path. */
  if (mod.name && !mod.ctrl && !mod.shift) {
    nameOpenedAt = Date.now();
    onRowOpen(n);
    return;
  }
  /* issue #26 — a finger has no double-click, so a TAP elsewhere on the item
   * is the open gesture too: with nothing selected it opens what it lands on,
   * exactly as a double-click would. Once something is selected (a long press
   * selects, via its menu) such a tap adds to or removes from the selection,
   * so picking several files still works. A mouse click beside the name keeps
   * click-to-select; see composables/useRowTouch. */
  if (mod.touch && !mod.ctrl && !mod.shift) {
    if (props.selected.size === 0) {
      onRowOpen(n);
      return;
    }
    mod = { ...mod, ctrl: true };
  }
  emit('activate');
  emit('click-row', n, mod);
}

function onViewDbl(n: FileNode) {
  if (withinNameOpenGuard()) return;
  onRowOpen(n);
}

function onViewContext(n: FileNode, ev: MouseEvent) {
  ev.preventDefault();
  ev.stopPropagation();
  emit('activate');
  emit('context', n, ev);
}

/** Right-click on genuinely empty space: a selection-less menu ("Paste"). The
 *  row menu calls stopPropagation, so this only fires on the background. */
function onBgContext(ev: MouseEvent) {
  ev.preventDefault();
  ev.stopPropagation();
  emit('activate');
  emit('clear-selection');
  emit('context', null, ev);
}

function onBgClick() {
  emit('clear-selection');
}

function onRowOpen(n: FileNode) {
  emit('activate');
  if (n.basename === '.trash') {
    emit('open-trash');
    return;
  }
  if (n.type === 'dir' && props.selfDriven) {
    void loadFolder(isStorageRow(n) ? n.path : props.toUser(n.path));
    return;
  }
  emit('open', n);
}

/* ── drag source ───────────────────────────────────────────────────────
 * The pane writes the standard payload (the internal MIME, the ORIGIN
 * directory that makes a cross-pane move undoable, and a plain-text
 * fallback) and then hands the event on. The host can still take the drag
 * over — the desktop shell turns it into a real OS drag — which is why the
 * emit comes last and the host is free to `preventDefault`. */
function onRowDragStart(n: FileNode, ev: DragEvent) {
  if (!ev.dataTransfer) return;
  if (isStorageRow(n) || atVirtualRoot.value || n.basename === '.trash') {
    ev.preventDefault();
    return;
  }
  const items = paneRows.value
    .filter((f) => props.selected.has(f.path) && !isStorageRow(f))
    .filter((f) => !props.clipped?.has(f.path))
    .map((f) => ({ path: f.path, basename: f.basename, type: f.type }));
  const payload = items.length > 0 ? items : [{ path: n.path, basename: n.basename, type: n.type }];
  ev.dataTransfer.setData(FE_DND_MIME, JSON.stringify(payload));
  ev.dataTransfer.setData(FE_DND_SRC_MIME, paneWire.value);
  ev.dataTransfer.setData('text/plain', payload.map((i) => i.path).join('\n'));
  ev.dataTransfer.effectAllowed = 'move';
  emit('item-drag-start', n, ev);
}

/* ── drop target (dir rows + the pane background) ─────────────────────── */

const dropBg = ref(false);

function acceptDrag(ev: DragEvent): boolean {
  return hasInternalDrag(ev);
}

function handleDropPayload(ev: DragEvent, targetWire: string) {
  const items = internalDragItems(ev);
  if (!items || !targetWire) return;
  const origin = internalDragOrigin(ev);
  const sources = items
    .map((i) => i.path)
    .filter((p) => p && p !== targetWire && !targetWire.startsWith(p + '/'));
  if (sources.length === 0) return;
  ev.preventDefault();
  ev.stopPropagation();
  emit('transfer', { sources, targetWire, originWire: origin });
}

function onViewDropInto(target: FileNode, ev: DragEvent) {
  dropBg.value = false;
  if (target.type !== 'dir' || isStorageRow(target)) return;
  if (!acceptDrag(ev)) return;
  emit('item-drop-into', target, ev);
}

function onBgDragOver(ev: DragEvent) {
  if (!acceptDrag(ev) || atVirtualRoot.value) return;
  ev.preventDefault();
  ev.stopPropagation();
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
  dropBg.value = true;
}

function onBgDragLeave() {
  dropBg.value = false;
}

function onBgDrop(ev: DragEvent) {
  dropBg.value = false;
  if (!acceptDrag(ev) || atVirtualRoot.value) return;
  handleDropPayload(ev, paneWire.value);
}

/* ── the host's handle on this pane ────────────────────────────────────
 * Keyboard routing, cut/copy/paste and the context menu all act on "the
 * active pane"; these are the four things the host cannot work out from its
 * own state. Both panes expose the SAME handle, so the routing in the host is
 * one branch on an id rather than two code paths. */
function reload(): Promise<void> {
  return props.selfDriven ? loadFolder() : Promise.resolve();
}

function goUp() {
  const cur = (panePath.value ?? '').replace(/^\/+|\/+$/g, '');
  const floor = (props.floor || '').replace(/^\/+|\/+$/g, '');
  if (!cur || cur === floor) return;
  const idx = cur.lastIndexOf('/');
  goTo(idx === -1 ? '' : cur.slice(0, idx));
}

function getPath(): string {
  return panePath.value;
}

/** What is on screen, in the order it is drawn — the list a Select-all or a
 *  Shift-range has to be arithmetic over. */
function visibleNodes(): FileNode[] {
  return displayFiles.value.filter((n) => !isStorageRow(n));
}

/**
 * How many rows this pane's folder holds — BEFORE the filter row narrows them,
 * which is what "12 items" in the details panel means (`visibleNodes()` is the
 * other question: what is drawn right now).
 */
function rowCount(): number {
  return paneRows.value.length;
}

/**
 * The paths this pane's folder HOLDS — unfiltered, and that is the point.
 *
 * ⚠ Not `visibleNodes()`. The one caller asks "does this row still exist?", and
 * `visibleNodes()` answers "is this row currently drawn?" — two different
 * questions that only agree while the filter row is empty. Answering the first
 * with the second would make typing in the filter box look exactly like a
 * deletion to whoever is asking.
 */
function rowPaths(): string[] {
  return paneRows.value.map((n) => n.path);
}

/**
 * koru:k1 — the RBAC level of the folder this pane is in, '' when ACL is not
 * enforced (or when the host drives the rows: it answered the request, so it
 * is holding the `perm` this pane never saw).
 */
function dirPerm(): string {
  return props.selfDriven ? ownPerm.value : '';
}

defineExpose({ reload, goUp, getPath, visibleNodes, loadFolder, rowCount, rowPaths, dirPerm });

/* A host-driven pane whose address moved (a tab switch, a search) has to drop
 * any drop-highlight it was showing; nothing else here is stateful across a
 * navigation. */
watch(panePath, () => {
  dropBg.value = false;
});
</script>

<template>
  <section
    class="fe__primary fe-pane"
    :class="{
      'fe-pane--focus': focused,
      'fe-pane--split': paneId === 'split',
      'fe-pane--dropover': dropBg,
    }"
    :data-pane="paneId"
    :data-testid="`pane-${paneId}`"
    role="region"
    :aria-label="paneId === 'split' ? t('split.pane') : t('toolbar.view_label')"
    @pointerdown.capture="emit('activate')"
  >
    <!-- The address row. `Breadcrumb.vue` in BOTH panes — the right-hand half
         used to draw a private crumb strip that knew nothing about the
         multi-storage root, the confine, the ✏ editor or crumb drops.

         ⚠ Not drawn when it would be EMPTY. Home asks for no crumbs (its cards
         come from every folder in every storage, so a trail would have to name
         one) and no view switcher, and as of 2026-09-13 it supplies no heading
         either — so without this guard the page opened with a 38px bordered
         strip containing nothing, which reads as a rendering fault rather than
         as a design. -->
    <div v-if="showCrumbs || !!$slots.heading || showViewSwitcher || closable" class="fe-subhead">
      <slot name="heading">
        <Breadcrumb
          v-if="showCrumbs"
          :dirname="paneWire"
          :adapter="paneAdapter"
          :root-label="rootLabel || paneAdapter"
          :locale="locale"
          :multi-storage-root="multiRoot"
          :root-path="rootPath"
          :subfolders="crumbSubfolders ?? paneSubfolders"
          @navigate="onCrumbNavigate"
          @copy-path="(p: string) => emit('copy-path', p)"
          @crumb-context="(p) => emit('crumb-context', p)"
          @crumb-drop="(p: string, ev: DragEvent) => emit('crumb-drop', p, ev)"
        />
      </slot>
      <div class="fe-subhead__actions">
        <ViewSwitcher
          v-if="showViewSwitcher"
          :view-mode="viewMode"
          :locale="locale"
          :modes="viewModes"
          @update:view-mode="(v: ViewMode) => { emit('activate'); emit('update:viewMode', v); }"
        />
        <button
          v-if="closable"
          type="button"
          class="fe-btn fe-btn--icon-only fe-pane__close"
          :title="t('split.close')"
          :aria-label="t('split.close')"
          data-testid="pane-close"
          @click="emit('close')"
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
            <path d="M6 6l12 12M18 6L6 18" />
          </svg>
        </button>
      </div>
    </div>

    <!-- Where the selection bar lands. Rendered by the pane, declaratively and
         in the right place, because the row above it only exists after the
         first tab is seeded — a node inserted by script would end up above it
         for the rest of the session. The filter row hides behind
         `.fe-selbar-slot.is-active ~ .fe-filterbar`. -->
    <div class="fe-selbar-slot"></div>

    <!-- ⚠ No `&& !atVirtualRoot` any more. The drive list KEEPS this row — it
         is the one place a person can narrow a long list of storages by name —
         but as the name box alone (`paneFilterMode`), because a drive answers
         none of the four chips. -->
    <FilterBar
      v-if="showFilterBar"
      :value="filters"
      :locale="locale"
      :theme="theme"
      :mode="paneFilterMode"
      :find-label="paneFindLabel"
      :order="order"
      :shown="displayFiles.length"
      :total="paneRows.length"
      :files="paneRows"
      :can-paste="canPaste"
      :can-write="canWrite"
      @update:value="(v: DriveFilters) => { emit('activate'); emit('update:filters', v); }"
      @new-folder="emit('activate'); emit('new-folder')"
      @upload="emit('activate'); emit('upload')"
      @paste="emit('activate'); emit('paste')"
      @select-all="emit('activate'); emit('select-all')"
    />

    <!-- Strips that belong to the WINDOW's state rather than to the listing
         (live presence, the encrypted-folder banners, the trash banner). Only
         the host can know about them, so it puts them here. -->
    <slot name="banners"></slot>

    <div
      class="fe__body"
      :class="{ 'is-dropover': dropBg }"
      @click.self="onBgClick"
      @contextmenu="onBgContext"
      @dragover="onBgDragOver"
      @dragleave="onBgDragLeave"
      @drop="onBgDrop"
    >
      <!-- The host draws the body itself for the states that have no listing
           behind them (Home, a dead deep link, the lock screen). -->
      <slot v-if="bodyOverride" name="body"></slot>

      <!-- Initial load: skeleton ghosts (view-mode aware) instead of an
           empty/"no files" flash. Only when there is nothing yet — navigation
           keeps the current list. -->
      <div v-else-if="paneLoading && paneRows.length === 0" class="fe__skeleton" role="status">
        <span class="fe-sr-only">{{ t('loading') }}</span>
        <div v-if="viewMode !== 'list'" class="fe-skel-grid" aria-hidden="true">
          <div v-for="i in 8" :key="i" class="fe-skel-card">
            <div class="fe-skel fe-skel--thumb"></div>
            <div class="fe-skel fe-skel--label"></div>
          </div>
        </div>
        <div v-else class="fe-skel-list" aria-hidden="true">
          <div v-for="i in 8" :key="i" class="fe-skel-row">
            <div class="fe-skel fe-skel--icon"></div>
            <div class="fe-skel fe-skel--name"></div>
            <div class="fe-skel fe-skel--size"></div>
            <div class="fe-skel fe-skel--date"></div>
          </div>
        </div>
      </div>

      <!-- Listing failed with nothing else to show: a retryable error state in
           the same visual language, in BOTH panes. The right-hand half used to
           print one unstyled line and a bare button. -->
      <div v-else-if="paneError && paneRows.length === 0" class="fe-state">
        <svg
          class="fe-state__art"
          viewBox="0 0 120 100"
          width="110"
          height="92"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <circle cx="60" cy="50" r="28" />
          <path d="M60 36v18" />
          <circle cx="60" cy="63" r="1.8" fill="currentColor" stroke="none" />
          <path d="M24 88h72" stroke-dasharray="3 5" />
        </svg>
        <p class="fe-state__title">{{ t('error.title') }}</p>
        <p class="fe-state__hint">{{ t('error.hint') }}</p>
        <div class="fe-state__actions">
          <button
            type="button"
            class="fe-btn fe-btn--primary"
            @click="selfDriven ? loadFolder() : emit('retry')"
          >
            {{ t('error.retry') }}
          </button>
        </div>
        <details class="fe-state__details">
          <summary class="fe-state__details-summary">{{ t('error.details') }}</summary>
          <pre class="fe-state__details-pre">{{ paneError }}</pre>
        </details>
      </div>

      <!-- The folder HAS rows and the filters hid all of them. "This folder is
           empty" would be false, and the way back is the chip row just above,
           so the message names it and offers the button. -->
      <div
        v-else-if="!paneLoading && filtersOn && displayFiles.length === 0 && paneRows.length > 0"
        class="fe-state"
        data-testid="empty-filtered"
      >
        <svg
          class="fe-state__art"
          viewBox="0 0 120 100"
          width="110"
          height="92"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M26 28h68L68 58v24l-16 8V58z" />
        </svg>
        <p class="fe-state__title">{{ t('filter.empty.title') }}</p>
        <p class="fe-state__hint">{{ t('filter.empty.hint') }}</p>
        <div class="fe-state__actions">
          <button type="button" class="fe-btn" @click="emit('clear-filters')">
            {{ t('filter.clear') }}
          </button>
        </div>
      </div>

      <!-- Loaded, zero rows: the host's own empty state when it has one to
           offer (an empty search, an empty panel view, an empty trash), this
           pane's plain one otherwise. -->
      <slot
        v-else-if="!paneLoading && paneRows.length === 0"
        name="empty"
      >
        <div class="fe-state">
          <svg
            class="fe-state__art"
            viewBox="0 0 120 100"
            width="110"
            height="92"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M18 36v42a6 6 0 0 0 6 6h72a6 6 0 0 0 6-6V44a6 6 0 0 0-6-6H62l-9-10H24a6 6 0 0 0-6 6z" />
          </svg>
          <p class="fe-state__title">{{ t('empty.folder') }}</p>
        </div>
      </slot>

      <ListView
        v-else-if="viewMode === 'list'"
        :files="displayFiles"
        :order="order"
        :selected="selected"
        :clipped="clipped"
        :show-parent-path="showParentPath"
        :locale="locale"
        :loading="paneLoading"
        :folder-key="folderKey"
        :theme="theme"
        :thumb-src="thumbSrc"
        :keep-badge-for="keepBadgeFor"
        :starred-ids="starredIds"
        :star-enabled="starEnabled"
        :api-base="apiBase"
        :auth-headers="authHeaders"
        :auth-credentials="authCredentials"
        @click-row="onViewClick"
        @display-order="(nodes: FileNode[]) => emit('display-order', nodes)"
        @dbl-row="onViewDbl"
        @context-row="onViewContext"
        @item-drag-start="onRowDragStart"
        @item-drop-into="onViewDropInto"
        @star-change="(n: FileNode, v: boolean) => emit('star-change', n, v)"
      />
      <GridView
        v-else-if="viewMode === 'grid'"
        :files="displayFiles"
        :order="order"
        :sections="true"
        :selected="selected"
        :clipped="clipped"
        :show-parent-path="showParentPath"
        :locale="locale"
        :loading="paneLoading"
        :keep-badge-for="keepBadgeFor"
        :thumb-src="thumbSrc"
        :e2e-active="e2eActive"
        :starred-ids="starredIds"
        :star-enabled="starEnabled"
        :api-base="apiBase"
        :auth-headers="authHeaders"
        :auth-credentials="authCredentials"
        @click-card="onViewClick"
        @display-order="(nodes: FileNode[]) => emit('display-order', nodes)"
        @dbl-card="onViewDbl"
        @context-card="onViewContext"
        @item-drag-start="onRowDragStart"
        @item-drop-into="onViewDropInto"
        @star-change="(n: FileNode, v: boolean) => emit('star-change', n, v)"
      />
      <GalleryView
        v-else
        :files="displayFiles"
        :order="order"
        :selected="selected"
        :clipped="clipped"
        :show-parent-path="showParentPath"
        :locale="locale"
        :loading="paneLoading"
        :thumb-src="thumbSrc"
        :e2e-active="e2eActive"
        :starred-ids="starredIds"
        :star-enabled="starEnabled"
        :api-base="apiBase"
        :auth-headers="authHeaders"
        :auth-credentials="authCredentials"
        @click-card="onViewClick"
        @dbl-card="onViewDbl"
        @context-card="onViewContext"
        @item-drag-start="onRowDragStart"
        @item-drop-into="onViewDropInto"
        @star-change="(n: FileNode, v: boolean) => emit('star-change', n, v)"
      />
    </div>
  </section>
</template>
