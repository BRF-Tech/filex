<script setup lang="ts">
/**
 * ListView — tabular layout (name / size / modified).
 *
 * Selection + context menu + double-click open are caller-owned — we
 * just emit the events and let FileExplorer.vue handle them uniformly
 * across List and Grid.
 */
import { computed, ref } from 'vue';
import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { fileIconSvg } from '../lib/fileIcons';
import { matchedInContent, snippetSegments } from '../lib/snippet'; /* bul:s3 */
import StarButton from './StarButton.vue';

const props = defineProps<{
  files: FileNode[];
  selected: Set<string>;
  /** Paths that were cut → dim them so the user sees the pending move. */
  clipped?: Set<string>;
  /**
   * Show each row's parent directory under the basename. Caller flips
   * this on while a search filter is active so the user can tell
   * `invoice.pdf` (in 2024/) apart from `invoice.pdf` (in 2025/) when
   * both match the same query.
   */
  showParentPath?: boolean;
  locale: LocaleCode;
  loading?: boolean;
  /** Set of node IDs flagged starred by the user — render an inline
   *  filled star indicator and let the user toggle it from the row. */
  starredIds?: Set<number>;
  /** Backend base URL + auth header builder forwarded to StarButton
   *  so it can POST /api/files/manager/star on click. Optional —
   *  embedders without auth wire-up pass nothing and the star column
   *  hides itself. */
  apiBase?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
}>();

const emit = defineEmits<{
  (e: 'click-row', node: FileNode, mod: { ctrl: boolean; shift: boolean }): void;
  (e: 'dbl-row', node: FileNode): void;
  (e: 'context-row', node: FileNode, ev: MouseEvent): void;
  (e: 'item-drag-start', node: FileNode, ev: DragEvent): void;
  (e: 'item-drop-into', target: FileNode, ev: DragEvent): void;
  (e: 'star-change', node: FileNode, value: boolean): void;
}>();

const { t, formatSize, nodeDisplayName } = useLocale(() => props.locale);

function isSelected(n: FileNode): boolean {
  return props.selected.has(n.path);
}

function onRowClick(n: FileNode, ev: MouseEvent) {
  emit('click-row', n, { ctrl: ev.ctrlKey || ev.metaKey, shift: ev.shiftKey });
}

function onRowDbl(n: FileNode) {
  emit('dbl-row', n);
}

function onRowCtx(n: FileNode, ev: MouseEvent) {
  // Stop bubbling so the root `@contextmenu` handler doesn't fire and
  // clear the selection we just set.
  ev.preventDefault();
  ev.stopPropagation();
  emit('context-row', n, ev);
}

function onItemDragStart(n: FileNode, ev: DragEvent) {
  emit('item-drag-start', n, ev);
}

function onItemDragOver(n: FileNode, ev: DragEvent) {
  if (n.type !== 'dir') return;
  if (!ev.dataTransfer?.types.includes('application/x-brf-files')) return;
  ev.preventDefault();
  ev.stopPropagation();
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
}

function onItemDrop(n: FileNode, ev: DragEvent) {
  if (n.type !== 'dir') return;
  if (!ev.dataTransfer?.types.includes('application/x-brf-files')) return;
  ev.preventDefault();
  ev.stopPropagation();
  emit('item-drop-into', n, ev);
}

let pressTimer: ReturnType<typeof setTimeout> | undefined;
let pressTarget: FileNode | null = null;

function onTouchStart(n: FileNode, ev: TouchEvent) {
  pressTarget = n;
  if (pressTimer) clearTimeout(pressTimer);
  pressTimer = setTimeout(() => {
    if (pressTarget) {
      const t0 = ev.touches[0];
      emit('context-row', pressTarget, {
        clientX: t0.clientX,
        clientY: t0.clientY,
        preventDefault: () => {},
      } as unknown as MouseEvent);
    }
  }, 500);
}

function cancelPress() {
  if (pressTimer) clearTimeout(pressTimer);
  pressTarget = null;
}

// Special rows keep their emoji (trash/storage are not file-TYPE icons);
// everything else renders the SVG icon set from lib/fileIcons.
function specialEmojiFor(n: FileNode): string | null {
  if (n.basename === '.trash') return '🗑';
  if (n.mime_type === 'inode/storage') return '💾';
  return null;
}

function isPinnedSpecial(n: FileNode): boolean {
  return n.basename === '.trash' || n.mime_type === 'inode/storage';
}

function displayDate(ms: number | undefined): string {
  if (!ms) return '—';
  const d = new Date(ms * (ms < 1e12 ? 1000 : 1));
  return d.toLocaleString();
}

function parentDir(path: string): string {
  const stripped = path.replace(/^[a-z][a-z0-9+.-]*:\/\//i, '');
  const idx = stripped.lastIndexOf('/');
  if (idx === -1) return '';
  return stripped.slice(0, idx);
}

/* bul:s3 — search-result enrichment. The v0.2 backend inlines `snippet`
 * (plain text, «» highlights) + `matched` on search hits; regular listings
 * never carry them, so presence-gating keeps normal rows untouched and an
 * older backend simply renders nothing extra. */
function rowSnippet(n: FileNode): string {
  const s = (n as Record<string, unknown>).snippet;
  return typeof s === 'string' ? s : '';
}

function rowInContent(n: FileNode): boolean {
  return matchedInContent((n as Record<string, unknown>).matched);
}

// ------------------------------------------------------------------
// Column sorting — local to the list view. Default (null) keeps the
// backend order, i.e. exactly the pre-existing behavior.
// ------------------------------------------------------------------

type SortKey = 'name' | 'size' | 'modified';
const SORT_LS_KEY = 'filex.list-sort';

const sortKey = ref<SortKey | null>(null);
const sortDir = ref<'asc' | 'desc'>('asc');
try {
  const raw = localStorage.getItem(SORT_LS_KEY);
  if (raw) {
    const p = JSON.parse(raw) as { key?: unknown; dir?: unknown };
    if (p.key === 'name' || p.key === 'size' || p.key === 'modified') sortKey.value = p.key;
    if (p.dir === 'asc' || p.dir === 'desc') sortDir.value = p.dir;
  }
} catch {
  /* private mode / bad JSON */
}

// Dates default to newest-first, the others to ascending.
function defaultDir(key: SortKey): 'asc' | 'desc' {
  return key === 'modified' ? 'desc' : 'asc';
}

// Click cycle per column: default direction → flipped → back to backend order.
function toggleSort(key: SortKey) {
  if (sortKey.value !== key) {
    sortKey.value = key;
    sortDir.value = defaultDir(key);
  } else if (sortDir.value === defaultDir(key)) {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc';
  } else {
    sortKey.value = null;
  }
  try {
    localStorage.setItem(
      SORT_LS_KEY,
      JSON.stringify({ key: sortKey.value, dir: sortDir.value }),
    );
  } catch {
    /* quota */
  }
}

function ariaSort(key: SortKey): 'ascending' | 'descending' | 'none' {
  if (sortKey.value !== key) return 'none';
  return sortDir.value === 'asc' ? 'ascending' : 'descending';
}

function modifiedMs(n: FileNode): number | null {
  const v = n.last_modified;
  if (!v) return null;
  return v * (v < 1e12 ? 1000 : 1);
}

const rows = computed(() => {
  const key = sortKey.value;
  if (!key) return props.files;
  // Virtual rows (.trash / storage drives) stay pinned on top in their
  // original order; only real entries take part in the sort.
  const pinned: FileNode[] = [];
  const rest: FileNode[] = [];
  for (const n of props.files) (isPinnedSpecial(n) ? pinned : rest).push(n);
  const dir = sortDir.value === 'asc' ? 1 : -1;
  rest.sort((a, b) => {
    if (key === 'name') {
      return (
        dir *
        a.basename.localeCompare(b.basename, undefined, { numeric: true, sensitivity: 'base' })
      );
    }
    if (key === 'size') return dir * ((a.size ?? -1) - (b.size ?? -1));
    const am = modifiedMs(a);
    const bm = modifiedMs(b);
    // Undated rows go last in both directions so date groups stay clean.
    if (am == null && bm == null) return 0;
    if (am == null) return 1;
    if (bm == null) return -1;
    return dir * (am - bm);
  });
  return [...pinned, ...rest];
});

// ------------------------------------------------------------------
// Date grouping — only while sorted by modified date. Rows are split
// into contiguous segments so the row markup below stays untouched.
// ------------------------------------------------------------------

interface Segment {
  id: string;
  /** null = no header (ungrouped mode, or the pinned virtual rows). */
  label: string | null;
  nodes: FileNode[];
}

const monthFmt = computed(
  () =>
    new Intl.DateTimeFormat(props.locale === 'tr' ? 'tr-TR' : 'en-US', {
      month: 'long',
      year: 'numeric',
    }),
);

function bucketFor(ms: number | null): { id: string; label: string } {
  if (ms == null) return { id: 'none', label: t('group.no_date') };
  const d = new Date(ms);
  const now = new Date();
  const dayStart = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const today = dayStart(now);
  const day = dayStart(d);
  const DAY = 86_400_000;
  // Future stamps (clock skew) read best as "today".
  if (day >= today) return { id: 'today', label: t('group.today') };
  if (day === today - DAY) return { id: 'yesterday', label: t('group.yesterday') };
  if (day > today - 7 * DAY) return { id: 'week', label: t('group.this_week') };
  if (d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth()) {
    return { id: 'month', label: t('group.this_month') };
  }
  return { id: `m-${d.getFullYear()}-${d.getMonth()}`, label: monthFmt.value.format(d) };
}

const segments = computed<Segment[]>(() => {
  const list = rows.value;
  if (sortKey.value !== 'modified') return [{ id: 'all', label: null, nodes: list }];
  const out: Segment[] = [];
  let cur: Segment | null = null;
  for (const n of list) {
    const b = isPinnedSpecial(n) ? { id: 'pinned', label: null } : bucketFor(modifiedMs(n));
    if (!cur || cur.id !== b.id) {
      cur = { id: b.id, label: b.label, nodes: [] };
      out.push(cur);
    }
    cur.nodes.push(n);
  }
  return out;
});
</script>

<template>
  <div class="fe-list" :class="{ 'is-loading': loading, 'has-star-col': !!apiBase || apiBase === '' }">
    <div class="fe-list__head" role="row">
      <div class="fe-list__col fe-list__col--star" role="columnheader" aria-label="Star"></div>
      <div class="fe-list__col fe-list__col--name" role="columnheader" :aria-sort="ariaSort('name')">
        <button type="button" class="fe-list__sort" :title="t('col.sort')" @click="toggleSort('name')">
          {{ t('col.name') }}
          <span v-if="sortKey === 'name'" class="fe-list__sort-arrow" aria-hidden="true">{{ sortDir === 'asc' ? '↑' : '↓' }}</span>
        </button>
      </div>
      <div class="fe-list__col fe-list__col--size" role="columnheader" :aria-sort="ariaSort('size')">
        <button type="button" class="fe-list__sort" :title="t('col.sort')" @click="toggleSort('size')">
          {{ t('col.size') }}
          <span v-if="sortKey === 'size'" class="fe-list__sort-arrow" aria-hidden="true">{{ sortDir === 'asc' ? '↑' : '↓' }}</span>
        </button>
      </div>
      <div class="fe-list__col fe-list__col--mod" role="columnheader" :aria-sort="ariaSort('modified')">
        <button type="button" class="fe-list__sort" :title="t('col.sort')" @click="toggleSort('modified')">
          {{ t('col.modified') }}
          <span v-if="sortKey === 'modified'" class="fe-list__sort-arrow" aria-hidden="true">{{ sortDir === 'asc' ? '↑' : '↓' }}</span>
        </button>
      </div>
    </div>
    <div class="fe-list__body" role="rowgroup">
      <template v-for="seg in segments" :key="seg.id">
      <div v-if="seg.label" class="fe-list__group" role="presentation">{{ seg.label }}</div>
      <div
        v-for="n in seg.nodes"
        :key="n.path"
        class="fe-list__row"
        :class="{
          'is-selected': isSelected(n),
          'is-dir': n.type === 'dir',
          'is-trash': n.trashed,
          'is-clipped': clipped?.has(n.path),
        }"
        role="row"
        tabindex="0"
        draggable="true"
        @click="onRowClick(n, $event)"
        @dblclick="onRowDbl(n)"
        @contextmenu="onRowCtx(n, $event)"
        @dragstart="onItemDragStart(n, $event)"
        @dragover="onItemDragOver(n, $event)"
        @drop="onItemDrop(n, $event)"
        @touchstart.passive="onTouchStart(n, $event)"
        @touchend="cancelPress"
        @touchmove="cancelPress"
      >
        <div class="fe-list__col fe-list__col--star" @click.stop>
          <StarButton
            v-if="typeof n.id === 'number' && n.type === 'file'"
            :starred="!!starredIds?.has(n.id)"
            :node-id="n.id"
            :api-base="apiBase"
            :auth-headers="authHeaders"
            compact
            @change="(val: boolean) => emit('star-change', n, val)"
          />
        </div>
        <div class="fe-list__col fe-list__col--name">
          <span v-if="specialEmojiFor(n)" class="fe-list__icon" aria-hidden="true">{{ specialEmojiFor(n) }}</span>
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
          <span v-else class="fe-list__icon fe-list__icon--svg" aria-hidden="true" v-html="fileIconSvg(n)"></span>
          <div class="fe-list__name-wrap">
            <span class="fe-list__name" :title="n.basename">
              {{ nodeDisplayName(n) }}
              <!-- bul:s3 — content-match badge -->
              <span v-if="rowInContent(n)" class="fe-list__badge">{{ t('search.in_content') }}</span>
            </span>
            <span
              v-if="showParentPath"
              class="fe-list__parent"
              :title="parentDir(n.path)"
            >{{ parentDir(n.path) || '—' }}</span>
            <!-- bul:s3 — content snippet («» → <mark> via TEXT segments, no innerHTML) -->
            <span v-if="rowSnippet(n)" class="fe-list__snippet">
              <template v-for="(seg, si) in snippetSegments(rowSnippet(n))" :key="si">
                <mark v-if="seg.match" class="fe-list__mark">{{ seg.text }}</mark>
                <template v-else>{{ seg.text }}</template>
              </template>
            </span>
          </div>
        </div>
        <div class="fe-list__col fe-list__col--size">
          {{ formatSize(n.size) }}
        </div>
        <div class="fe-list__col fe-list__col--mod">
          {{ displayDate(n.last_modified) }}
        </div>
      </div>
      </template>
      <div v-if="!loading && rows.length === 0" class="fe-list__empty">
        {{ t('empty.folder') }}
      </div>
    </div>
  </div>
</template>
