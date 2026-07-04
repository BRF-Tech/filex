<script setup lang="ts">
/**
 * GridView — card grid. Thumbnails preferred, fall back to icon.
 */
import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  files: FileNode[];
  selected: Set<string>;
  clipped?: Set<string>;
  showParentPath?: boolean;
  locale: LocaleCode;
  loading?: boolean;
}>();

const emit = defineEmits<{
  (e: 'click-card', node: FileNode, mod: { ctrl: boolean; shift: boolean }): void;
  (e: 'dbl-card', node: FileNode): void;
  (e: 'context-card', node: FileNode, ev: MouseEvent): void;
  (e: 'item-drag-start', node: FileNode, ev: DragEvent): void;
  (e: 'item-drop-into', target: FileNode, ev: DragEvent): void;
}>();

const { t, formatSize, nodeDisplayName } = useLocale(() => props.locale);

function isSelected(n: FileNode): boolean {
  return props.selected.has(n.path);
}

function onClick(n: FileNode, ev: MouseEvent) {
  emit('click-card', n, { ctrl: ev.ctrlKey || ev.metaKey, shift: ev.shiftKey });
}

function onDbl(n: FileNode) {
  emit('dbl-card', n);
}

function onCtx(n: FileNode, ev: MouseEvent) {
  ev.preventDefault();
  ev.stopPropagation();
  emit('context-card', n, ev);
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
      emit('context-card', pressTarget, {
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

function parentDir(path: string): string {
  const stripped = path.replace(/^[a-z][a-z0-9+.-]*:\/\//i, '');
  const idx = stripped.lastIndexOf('/');
  if (idx === -1) return '';
  return stripped.slice(0, idx);
}

function iconFor(n: FileNode): string {
  if (n.mime_type === 'inode/storage') return '💾';
  if (n.type === 'dir') return '📁';
  const e = (n.extension || '').toLowerCase();
  if (['jpg', 'jpeg', 'png', 'webp', 'gif', 'bmp', 'svg'].includes(e)) return '🖼';
  if (['mp4', 'webm', 'mov', 'mkv'].includes(e)) return '🎞';
  if (['mp3', 'wav', 'flac', 'ogg'].includes(e)) return '🎵';
  if (e === 'pdf') return '📕';
  if (['doc', 'docx', 'odt'].includes(e)) return '📄';
  if (['xls', 'xlsx', 'csv'].includes(e)) return '📊';
  if (['ppt', 'pptx'].includes(e)) return '📽';
  if (['zip', 'tar', 'gz', '7z'].includes(e)) return '🗜';
  return '📎';
}
</script>

<template>
  <div class="fe-grid" :class="{ 'is-loading': loading }">
    <div
      v-for="n in files"
      :key="n.path"
      class="fe-grid__card"
      :class="{
        'is-selected': isSelected(n),
        'is-dir': n.type === 'dir',
        'is-trash': n.trashed,
        'is-clipped': clipped?.has(n.path),
      }"
      tabindex="0"
      draggable="true"
      @click="onClick(n, $event)"
      @dblclick="onDbl(n)"
      @contextmenu="onCtx(n, $event)"
      @dragstart="onItemDragStart(n, $event)"
      @dragover="onItemDragOver(n, $event)"
      @drop="onItemDrop(n, $event)"
      @touchstart.passive="onTouchStart(n, $event)"
      @touchend="cancelPress"
      @touchmove="cancelPress"
    >
      <div class="fe-grid__thumb">
        <!--
          draggable="false" — HTML5 image drag dataTransfer adds 'Files'
          MIME, which trips the parent's upload handler; without this
          the user dragging an item with a thumbnail re-uploads it.
          Parent card stays draggable=true so internal move works.
        -->
        <img
          v-if="n.thumb_url"
          :src="n.thumb_url"
          :alt="n.basename"
          loading="lazy"
          draggable="false"
        />
        <span v-else class="fe-grid__icon">{{ iconFor(n) }}</span>
      </div>
      <div class="fe-grid__label" :title="n.basename">
        {{ nodeDisplayName(n) }}
      </div>
      <div
        v-if="showParentPath"
        class="fe-grid__parent"
        :title="parentDir(n.path)"
      >{{ parentDir(n.path) || '—' }}</div>
      <div class="fe-grid__meta">
        {{ formatSize(n.size) }}
      </div>
    </div>
    <div v-if="!loading && files.length === 0" class="fe-grid__empty">
      {{ t('empty.folder') }}
    </div>
  </div>
</template>
