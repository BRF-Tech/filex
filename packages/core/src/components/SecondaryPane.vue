<script setup lang="ts">
/**
 * SecondaryPane — wiring:d1 the right-hand (secondary) pane of the split view.
 *
 * Deliberately LIGHT: its own path + its own listing — fetched through
 * the SAME FileApi instance the host already constructed (`api.index`,
 * no copied fetch logic) — a small breadcrumb, double-click navigation,
 * click selection and drag&drop in BOTH directions. No toolbar, no
 * inspector, no upload: those belong to the main panel.
 *
 * Path semantics, confinement (`clamp`) and wire-form resolution
 * (`qualify` / `toUser`) are injected from the host so the pane can
 * never drift from the main panel's multi-storage / rootFloor rules.
 *
 * Cross-pane transfers only EMIT (`transfer`); the host decides
 * move-vs-copy and runs the existing ops APIs. Unscoped styles
 * (`fe-split*` in base.css) — webcomponent data-v rule.
 */
import { computed, ref } from 'vue';
import type { FileApi } from '../composables/useFileApi';
import type { FileNode, ViewMode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { filterListing, injectTrashRow, hydrateTrashRow } from '../lib/listing';
import ListView from './ListView.vue';
import GridView from './GridView.vue';
import GalleryView from './GalleryView.vue';

// Same literals ListView/GridView already hardcode for the internal DnD
// channel; `-src` carries the origin directory so the host can move (and
// undo) correctly across panes.
const FE_DND_MIME = 'application/x-brf-files';
const FE_DND_SRC_MIME = 'application/x-brf-files-src';
import { hasInternalDrag, internalDragItems, internalDragOrigin } from '../lib/dragOut';

const props = defineProps<{
  api: FileApi;
  /** Opening location, in the host's `currentPath` form. */
  initialPath: string;
  locale: LocaleCode;
  /** Host's own qualify(): user path → wire `<adapter>://<rel>`. */
  qualify: (p: string) => string;
  /** Host's wire → user-path converter (multi-storage aware). */
  toUser: (wire: string) => string;
  /** Host's rootFloor clamp — keeps the pane inside the confine. */
  clamp: (p: string) => string;
  /** Label for the root crumb (adapter name / storage list). */
  rootLabel: string;
  /** rootFloor (user-path form, '' when unconfined) — trims crumbs. */
  floor?: string;
  /** Multi-storage mode: '' is the virtual drives root (no fetch). */
  multiRoot?: boolean;
  /** Synthesized storage rows for the virtual drives root. */
  virtualRows?: () => FileNode[];
  /** Active-panel highlight (keyboard target). */
  active?: boolean;
  /** ui-fix — the pane renders the SAME view components as the main
   *  panel (list/grid/gallery) instead of its own flat list. */
  viewMode?: ViewMode;
  /** ui-fix — authenticated thumb resolver, forwarded to grid/gallery. */
  thumbSrc?: (n: FileNode) => string | null;
  /** Desktop selective sync badge resolver, forwarded to the views. */
  keepBadgeFor?: (n: FileNode) => 'kept' | 'syncing' | 'cloud' | 'partial' | null;
  /** ui-fix — mirror the main panel's virtual `.trash` row at storage root
   *  so both split panes list identical rows (no row-offset). Defaults on. */
  trashVisible?: boolean;
  /**
   * The navigation panel is already offering Trash, so neither pane draws the
   * virtual row. Passed down rather than worked out here: the panel belongs to
   * the host explorer, and a pane that guessed at it is how the two halves of a
   * split end up listing different rows.
   */
  navOffersTrash?: boolean;
}>();

const emit = defineEmits<{
  (e: 'navigate', path: string): void;
  (e: 'activate'): void;
  (e: 'close'): void;
  (e: 'open-tab', path: string): void;
  (e: 'transfer', p: { sources: string[]; targetWire: string; originWire?: string }): void;
  /* ui-fix — right-click inside the side pane: the menu is opened by the main
   * component (FileExplorer). node=null → right-click on empty space (nothing
   * selected, so "Paste" only). */
  (e: 'context', node: FileNode | null, ev: MouseEvent): void;
  /* ui-fix — when the trash row is opened: the trash view belongs to the main
   * panel (it carries the restore actions), so the host opens it via loadTrash(). */
  (e: 'open-trash'): void;
}>();

const { t } = useLocale(() => props.locale);

const path = ref<string>('');
const files = ref<FileNode[]>([]);
const loading = ref(false);
const error = ref('');
const selected = ref(new Set<string>());

function isStorageRow(n: FileNode): boolean {
  return n.mime_type === 'inode/storage';
}

const atVirtualRoot = computed(
  () => !!props.multiRoot && !(path.value ?? '').replace(/^\/+|\/+$/g, ''),
);

async function loadPane(target?: string): Promise<void> {
  const requested = props.clamp(target ?? path.value ?? '');
  loading.value = true;
  error.value = '';
  try {
    // Multi-storage virtual root: synthesize the drives list, no backend
    // call — mirrors the main panel's load() branch.
    if (props.multiRoot && !requested) {
      files.value = props.virtualRows ? props.virtualRows() : [];
      path.value = '';
      selected.value = new Set();
      emit('navigate', '');
      return;
    }
    const resp = await props.api.index(props.qualify(requested));
    // Same internal-entry filter + virtual `.trash` row as the main panel
    // (shared helpers → both split panes list identical rows, no offset).
    files.value = filterListing(resp.files);
    if (
      injectTrashRow(files.value, resp.adapter, resp.dirname, props.trashVisible !== false, {
        // ⚠ The panel is a sibling of BOTH panes. If it is offering Trash, this
        // pane must drop the row too — otherwise the duplication is not removed,
        // it just moves to the right-hand half of a split.
        navOffersTrash: props.navOffersTrash,
      })
    ) {
      void hydrateTrashRow(files.value, resp.adapter, props.api);
    }
    path.value = props.toUser(resp.dirname);
    selected.value = new Set();
    emit('navigate', path.value);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

void loadPane(props.initialPath ?? '');

// ------------------------------------------------------------------
// Breadcrumb
// ------------------------------------------------------------------

interface Crumb {
  label: string;
  target: string;
  last: boolean;
}

const crumbs = computed<Crumb[]>(() => {
  const clean = (path.value || '').replace(/^\/+|\/+$/g, '');
  const segs = clean ? clean.split('/') : [];
  const floor = (props.floor || '').replace(/^\/+|\/+$/g, '');
  const out: Crumb[] = [];
  let acc = '';
  for (const s of segs) {
    acc = acc ? `${acc}/${s}` : s;
    // Confined embeds: hide the segments strictly ABOVE the floor — the
    // floor segment itself acts as the pane's root crumb.
    if (floor && !(acc === floor || acc.startsWith(floor + '/'))) continue;
    out.push({ label: s, target: acc, last: false });
  }
  if (out.length > 0) out[out.length - 1].last = true;
  return out;
});

const showRootCrumb = computed(() => !props.floor);

function crumbGo(target: string) {
  emit('activate');
  void loadPane(target);
}

// ------------------------------------------------------------------
// Selection + navigation
// ------------------------------------------------------------------

/* ui-fix — adapter for the shared view components' click contract
 * ({ctrl, shift} mod object instead of a MouseEvent). Shift behaves as
 * ctrl here: the pane keeps a simple Set, no range anchor. */
function onViewClick(n: FileNode, mod: { ctrl: boolean; shift: boolean }) {
  emit('activate');
  const multi = mod.ctrl || mod.shift;
  const next = new Set<string>(multi ? selected.value : []);
  if (multi && next.has(n.path)) next.delete(n.path);
  else next.add(n.path);
  selected.value = next;
}

/* ui-fix — right-click on a pane item: activate + select it, then let the
 * host open the pane context menu (its actions are pane-routed). Selecting
 * first keeps the menu's targets on THIS pane, not the main panel. */
function onViewContext(n: FileNode, ev: MouseEvent) {
  ev.preventDefault();
  ev.stopPropagation();
  emit('activate');
  if (!selected.value.has(n.path)) selected.value = new Set([n.path]);
  emit('context', n, ev);
}

/* ui-fix — right-click on empty space (the background): a selection-less menu
 * ("Paste" only) → pasting files into an empty / into the current folder. The
 * row menu calls stopProp, so this only fires on genuinely empty space. */
function onBgContext(ev: MouseEvent) {
  ev.preventDefault();
  ev.stopPropagation(); // don't bubble into the canvas menu on the FileExplorer root
  emit('activate');
  selected.value = new Set();
  emit('context', null, ev);
}

/* ui-fix — drop-into from the shared views: same payload path as the
 * old flat rows (dir check + wire-qualified row paths). */
function onViewDropInto(target: FileNode, ev: DragEvent) {
  dropBg.value = false;
  if (target.type !== 'dir' || isStorageRow(target)) return;
  if (!acceptDrag(ev)) return;
  handleDropPayload(ev, target.path);
}

function onRowDbl(n: FileNode) {
  if (n.type !== 'dir') return;
  // The virtual `.trash` row opens the backend trash listing (restore
  // actions) — that view lives in the main panel; let the host open it.
  if (n.basename === '.trash') {
    emit('activate');
    emit('open-trash');
    return;
  }
  void loadPane(isStorageRow(n) ? n.path : props.toUser(n.path));
}

function clearSelection() {
  selected.value = new Set();
}

// ------------------------------------------------------------------
// Drag source
// ------------------------------------------------------------------

function onRowDragStart(n: FileNode, ev: DragEvent) {
  if (!ev.dataTransfer) return;
  if (isStorageRow(n) || atVirtualRoot.value || n.basename === '.trash') {
    ev.preventDefault();
    return;
  }
  if (!selected.value.has(n.path)) selected.value = new Set([n.path]);
  const items = files.value
    .filter((f) => selected.value.has(f.path) && !isStorageRow(f))
    .map((f) => ({ path: f.path, basename: f.basename, type: f.type }));
  if (items.length === 0) {
    ev.preventDefault();
    return;
  }
  ev.dataTransfer.setData(FE_DND_MIME, JSON.stringify(items));
  ev.dataTransfer.setData(FE_DND_SRC_MIME, props.qualify(path.value));
  ev.dataTransfer.setData('text/plain', items.map((i) => i.path).join('\n'));
  ev.dataTransfer.effectAllowed = 'move';
}

// ------------------------------------------------------------------
// Drop target (dir rows + pane background)
// ------------------------------------------------------------------

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
  handleDropPayload(ev, props.qualify(path.value));
}

// ------------------------------------------------------------------
// Host API (keyboard routing when this pane is active)
// ------------------------------------------------------------------

function goUp() {
  const cur = (path.value ?? '').replace(/^\/+|\/+$/g, '');
  const floor = (props.floor || '').replace(/^\/+|\/+$/g, '');
  if (!cur || cur === floor) return;
  const idx = cur.lastIndexOf('/');
  void loadPane(idx === -1 ? '' : cur.slice(0, idx));
}

function selectAll() {
  selected.value = new Set(files.value.filter((f) => !isStorageRow(f)).map((f) => f.path));
}

function selectedNodes(): FileNode[] {
  return files.value.filter((f) => selected.value.has(f.path));
}

function openSelected() {
  const n = selectedNodes()[0];
  if (n && n.type === 'dir') onRowDbl(n);
}

function reload(): Promise<void> {
  return loadPane();
}

function getPath(): string {
  return path.value;
}

defineExpose({ reload, goUp, selectAll, openSelected, selectedNodes, getPath });
</script>

<template>
  <section
    class="fe-split"
    :class="{ 'fe-pane--focus': active }"
    role="region"
    :aria-label="t('split.pane')"
    @pointerdown="emit('activate')"
  >
    <header class="fe-split__crumbs">
      <button
        v-if="showRootCrumb"
        type="button"
        class="fe-split__crumb"
        :class="{ 'is-last': crumbs.length === 0 }"
        :title="rootLabel"
        @click="crumbGo('')"
      >{{ rootLabel }}</button>
      <template v-for="c in crumbs" :key="c.target">
        <span class="fe-split__sep" aria-hidden="true">›</span>
        <button
          type="button"
          class="fe-split__crumb"
          :class="{ 'is-last': c.last }"
          :title="c.label"
          @click="crumbGo(c.target)"
        >{{ c.label }}</button>
      </template>
      <span class="fe-split__spacer"></span>
      <button
        type="button"
        class="fe-split__closebtn"
        :aria-label="t('split.close')"
        :title="t('split.close')"
        @click="emit('close')"
      >×</button>
    </header>

    <div
      class="fe-split__body"
      :class="{ 'is-dropover': dropBg }"
      @click.self="clearSelection"
      @contextmenu="onBgContext"
      @dragover="onBgDragOver"
      @dragleave="onBgDragLeave"
      @drop="onBgDrop"
    >
      <div v-if="loading && files.length === 0" class="fe-split__state" role="status">
        {{ t('loading') }}
      </div>
      <div v-else-if="error" class="fe-split__state">
        <span>{{ t('split.error') }}</span>
        <button type="button" class="fe-btn" @click="() => loadPane()">
          {{ t('split.retry') }}
        </button>
      </div>
      <div v-else-if="files.length === 0" class="fe-split__state">
        {{ t('empty.folder') }}
      </div>
      <!-- ui-fix — the same view components (list/grid/gallery), driven by the
           pane's own viewMode; the old flat fe-split__list is gone. -->
      <ListView
        v-else-if="(viewMode ?? 'list') === 'list'"
        :files="files"
        :selected="selected"
        :locale="locale"
        :loading="loading"
        :keep-badge-for="keepBadgeFor"
        @click-row="onViewClick"
        @dbl-row="onRowDbl"
        @context-row="onViewContext"
        @item-drag-start="onRowDragStart"
        @item-drop-into="onViewDropInto"
      />
      <GridView
        v-else-if="viewMode === 'grid'"
        :files="files"
        :selected="selected"
        :locale="locale"
        :loading="loading"
        :thumb-src="thumbSrc"
        :keep-badge-for="keepBadgeFor"
        @click-card="onViewClick"
        @dbl-card="onRowDbl"
        @context-card="onViewContext"
        @item-drag-start="onRowDragStart"
        @item-drop-into="onViewDropInto"
      />
      <GalleryView
        v-else
        :files="files"
        :selected="selected"
        :locale="locale"
        :loading="loading"
        :thumb-src="thumbSrc"
        @click-card="onViewClick"
        @dbl-card="onRowDbl"
        @context-card="onViewContext"
        @item-drag-start="onRowDragStart"
        @item-drop-into="onViewDropInto"
      />
    </div>
  </section>
</template>
