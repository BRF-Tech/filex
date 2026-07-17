<script setup lang="ts">
// (unscoped styles by design — see base.css wiring:d2 block; SFC scoped
// styles are banned for webcomponent data-v hash mismatch reasons.)
/**
 * GalleryView — wiring:d2. Third view mode: large-thumbnail gallery for
 * visual browsing (photos/videos). Derived from GridView (same props/emits,
 * same listbox a11y, same selection/drag/touch contract) but renders big
 * square tiles: thumbnail (or SVG file-type icon fallback) with the name
 * below and size+date revealed on hover/focus. GridView itself is untouched.
 */
import { ref } from 'vue';
import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { fileIconSvg } from '../lib/fileIcons';
import { applyDragGhost } from '../lib/dragGhost';

const props = defineProps<{
  files: FileNode[];
  selected: Set<string>;
  clipped?: Set<string>;
  showParentPath?: boolean;
  locale: LocaleCode;
  loading?: boolean;
  /** Authenticated thumb resolver (useThumbs.src) — same contract as
   *  GridView: raw `thumb_url` is root-relative and unauthenticated, so
   *  embedded hosts NEED this. null = icon fallback. */
  thumbSrc?: (n: FileNode) => string | null;
}>();

const emit = defineEmits<{
  (e: 'click-card', node: FileNode, mod: { ctrl: boolean; shift: boolean }): void;
  (e: 'dbl-card', node: FileNode): void;
  (e: 'context-card', node: FileNode, ev: MouseEvent): void;
  (e: 'item-drag-start', node: FileNode, ev: DragEvent): void;
  (e: 'item-drop-into', target: FileNode, ev: DragEvent): void;
}>();

const { t, formatSize, nodeDisplayName } = useLocale(() => props.locale);

function thumbOf(n: FileNode): string | null {
  return props.thumbSrc ? props.thumbSrc(n) : (n.thumb_url ?? null);
}

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
  applyDragGhost(
    ev,
    nodeDisplayName(n),
    props.selected.has(n.path) ? props.selected.size : 1,
  );
  emit('item-drag-start', n, ev);
}

// Drop-target highlight — mirrors GridView (visual layer only).
const dropTargetPath = ref<string | null>(null);

function onItemDragOver(n: FileNode, ev: DragEvent) {
  if (n.type !== 'dir') return;
  if (!ev.dataTransfer?.types.includes('application/x-brf-files')) return;
  ev.preventDefault();
  ev.stopPropagation();
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
  dropTargetPath.value = n.path;
}

function onItemDragLeave(n: FileNode) {
  if (dropTargetPath.value === n.path) dropTargetPath.value = null;
}

function onItemDrop(n: FileNode, ev: DragEvent) {
  dropTargetPath.value = null;
  if (n.type !== 'dir') return;
  if (!ev.dataTransfer?.types.includes('application/x-brf-files')) return;
  ev.preventDefault();
  ev.stopPropagation();
  emit('item-drop-into', n, ev);
}

// Long-press → context menu, same as GridView (touch parity).
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

// Special rows keep their emoji (trash/storage are not file-TYPE icons).
function specialEmojiFor(n: FileNode): string | null {
  if (n.basename === '.trash') return '🗑';
  if (n.mime_type === 'inode/storage') return '💾';
  if (n.type === 'dir' && n.e2e === true) return '🔒'; /* wiring:e2 — şifreli klasör rozeti */
  return null;
}

function displayDate(ms: number | undefined): string {
  if (!ms) return '';
  const d = new Date(ms * (ms < 1e12 ? 1000 : 1));
  return d.toLocaleString();
}

// Hover meta line: size for files, entry count (when known) for dirs.
function metaFor(n: FileNode): string {
  const date = displayDate(n.last_modified);
  const size = n.type === 'dir' ? '' : formatSize(n.size);
  return [size, date].filter((s) => !!s).join(' · ');
}
</script>

<template>
  <div
    class="fe-gal"
    :class="{ 'is-loading': loading }"
    role="listbox"
    aria-multiselectable="true"
    :aria-label="t('gallery.aria')"
    :aria-busy="loading ? 'true' : undefined"
  >
    <div
      v-for="n in files"
      :key="n.path"
      class="fe-gal__card"
      :class="{
        'is-selected': isSelected(n),
        'is-dir': n.type === 'dir',
        'is-trash': n.trashed,
        'is-clipped': clipped?.has(n.path),
        'is-droptarget': dropTargetPath === n.path,
      }"
      tabindex="0"
      role="option"
      :aria-selected="isSelected(n) ? 'true' : 'false'"
      :aria-label="nodeDisplayName(n)"
      draggable="true"
      @click="onClick(n, $event)"
      @dblclick="onDbl(n)"
      @contextmenu="onCtx(n, $event)"
      @dragstart="onItemDragStart(n, $event)"
      @dragover="onItemDragOver(n, $event)"
      @dragleave="onItemDragLeave(n)"
      @drop="onItemDrop(n, $event)"
      @touchstart.passive="onTouchStart(n, $event)"
      @touchend="cancelPress"
      @touchmove="cancelPress"
    >
      <div class="fe-gal__thumb">
        <!-- draggable="false" for the same reason as GridView: HTML5 image
             drag adds a 'Files' MIME to dataTransfer, which would trip the
             parent's upload handler on internal drags. -->
        <img
          v-if="thumbOf(n)"
          :src="thumbOf(n)!"
          :alt="n.basename"
          loading="lazy"
          draggable="false"
        />
        <span v-else-if="specialEmojiFor(n)" class="fe-gal__icon">{{ specialEmojiFor(n) }}</span>
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
        <span v-else class="fe-gal__icon fe-gal__icon--svg" v-html="fileIconSvg(n)"></span>
        <div class="fe-gal__meta" aria-hidden="true">
          <span v-if="metaFor(n)" class="fe-gal__meta-line">{{ metaFor(n) }}</span>
          <span
            v-if="showParentPath && parentDir(n.path)"
            class="fe-gal__meta-line fe-gal__meta-line--path"
            :title="parentDir(n.path)"
          >{{ parentDir(n.path) }}</span>
        </div>
      </div>
      <div class="fe-gal__label" :title="n.basename">
        {{ nodeDisplayName(n) }}
      </div>
    </div>
    <div v-if="!loading && files.length === 0" class="fe-gal__empty">
      {{ t('empty.folder') }}
    </div>
  </div>
</template>
