<script setup lang="ts">
/**
 * ListView — tabular layout (name / size / modified).
 *
 * Selection + context menu + double-click open are caller-owned — we
 * just emit the events and let FileExplorer.vue handle them uniformly
 * across List and Grid.
 */
import { computed } from 'vue';
import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
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

function iconFor(n: FileNode): string {
  if (n.basename === '.trash') return '🗑';
  if (n.mime_type === 'inode/storage') return '💾';
  if (n.type === 'dir') return '📁';
  const e = (n.extension || '').toLowerCase();
  if (['jpg', 'jpeg', 'png', 'webp', 'gif', 'bmp', 'avif', 'heic', 'svg'].includes(e)) return '🖼';
  if (['mp4', 'webm', 'mov', 'mkv', 'avi', 'ogv', 'm4v'].includes(e)) return '🎞';
  if (['mp3', 'wav', 'flac', 'ogg', 'm4a', 'aac', 'opus'].includes(e)) return '🎵';
  if (e === 'pdf') return '📕';
  if (['doc', 'docx', 'odt', 'rtf'].includes(e)) return '📄';
  if (['xls', 'xlsx', 'ods', 'csv'].includes(e)) return '📊';
  if (['ppt', 'pptx', 'odp'].includes(e)) return '📽';
  if (['zip', 'tar', 'gz', 'bz2', '7z', 'rar'].includes(e)) return '🗜';
  if (['txt', 'md', 'log', 'json', 'yml', 'yaml', 'xml'].includes(e)) return '📝';
  return '📎';
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

const rows = computed(() => props.files);
</script>

<template>
  <div class="fe-list" :class="{ 'is-loading': loading, 'has-star-col': !!apiBase || apiBase === '' }">
    <div class="fe-list__head" role="row">
      <div class="fe-list__col fe-list__col--star" role="columnheader" aria-label="Star"></div>
      <div class="fe-list__col fe-list__col--name" role="columnheader">{{ t('col.name') }}</div>
      <div class="fe-list__col fe-list__col--size" role="columnheader">{{ t('col.size') }}</div>
      <div class="fe-list__col fe-list__col--mod" role="columnheader">{{ t('col.modified') }}</div>
    </div>
    <div class="fe-list__body" role="rowgroup">
      <div
        v-for="n in rows"
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
          <span class="fe-list__icon" aria-hidden="true">{{ iconFor(n) }}</span>
          <div class="fe-list__name-wrap">
            <span class="fe-list__name" :title="n.basename">{{ nodeDisplayName(n) }}</span>
            <span
              v-if="showParentPath"
              class="fe-list__parent"
              :title="parentDir(n.path)"
            >{{ parentDir(n.path) || '—' }}</span>
          </div>
        </div>
        <div class="fe-list__col fe-list__col--size">
          {{ formatSize(n.size) }}
        </div>
        <div class="fe-list__col fe-list__col--mod">
          {{ displayDate(n.last_modified) }}
        </div>
      </div>
      <div v-if="!loading && rows.length === 0" class="fe-list__empty">
        {{ t('empty.folder') }}
      </div>
    </div>
  </div>
</template>
