<script setup lang="ts">
/**
 * GridView — card grid. Thumbnails preferred, fall back to icon.
 */
import { ref } from 'vue'; /* wiring:c4 */
import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { fileIconSvg } from '../lib/fileIcons';
import { snippetSegments } from '../lib/snippet'; /* bul:s3 */
import { applyDragGhost } from '../lib/dragGhost'; /* wiring:c4 */

const props = defineProps<{
  files: FileNode[];
  selected: Set<string>;
  clipped?: Set<string>;
  showParentPath?: boolean;
  locale: LocaleCode;
  loading?: boolean;
  /** Authenticated thumb resolver (useThumbs.src). Raw `thumb_url` is
   *  root-relative and unauthenticated — a bare <img src> only works for the
   *  native same-origin SPA, so embedded hosts NEED this. null = icon. */
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

// Prefer the authenticated resolver when the host wired one; otherwise fall
// back to the raw URL (legacy same-origin behavior).
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
  /* wiring:c4 — custom ghost (name + multi-select count badge), visual only. */
  applyDragGhost(
    ev,
    nodeDisplayName(n),
    props.selected.has(n.path) ? props.selected.size : 1,
  );
  emit('item-drag-start', n, ev);
}

/* wiring:c4 — droptarget highlight, mirrors ListView (visual layer only). */
const dropTargetPath = ref<string | null>(null);

function onItemDragOver(n: FileNode, ev: DragEvent) {
  if (n.type !== 'dir') return;
  if (!ev.dataTransfer?.types.includes('application/x-brf-files')) return;
  ev.preventDefault();
  ev.stopPropagation();
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
  dropTargetPath.value = n.path; /* wiring:c4 */
}

/* wiring:c4 */
function onItemDragLeave(n: FileNode) {
  if (dropTargetPath.value === n.path) dropTargetPath.value = null;
}

function onItemDrop(n: FileNode, ev: DragEvent) {
  dropTargetPath.value = null; /* wiring:c4 */
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

// Special rows keep their emoji (trash/storage are not file-TYPE icons);
// everything else renders the SVG icon set from lib/fileIcons.
function specialEmojiFor(n: FileNode): string | null {
  if (n.basename === '.trash') return '🗑';
  if (n.mime_type === 'inode/storage') return '💾';
  if (n.type === 'dir' && n.e2e === true) return '🔒'; /* wiring:e2 — şifreli klasör rozeti */
  return null;
}

/* bul:s3 — search-result enrichment, same presence-gating as ListView:
 * only search hits carry `snippet`, so normal listings render nothing. */
function cardSnippet(n: FileNode): string {
  const s = (n as Record<string, unknown>).snippet;
  return typeof s === 'string' ? s : '';
}

// Plain-text form for the title attribute — same parser as the render
// path, guillemets dropped (no separate sanitize route).
function snippetTitle(snippet: string): string {
  return snippetSegments(snippet)
    .map((seg) => seg.text)
    .join('');
}
</script>

<template>
  <!-- wiring:c4 — listbox semantics (multi-selectable cards as options);
       localized label + busy state. Structure/layout untouched. -->
  <div
    class="fe-grid"
    :class="{ 'is-loading': loading }"
    role="listbox"
    aria-multiselectable="true"
    :aria-label="t('grid.aria')"
    :aria-busy="loading ? 'true' : undefined"
  >
    <div
      v-for="n in files"
      :key="n.path"
      class="fe-grid__card"
      :class="{
        'is-selected': isSelected(n),
        'is-dir': n.type === 'dir',
        'is-trash': n.trashed,
        'is-clipped': clipped?.has(n.path),
        'is-droptarget': dropTargetPath === n.path /* wiring:c4 */,
      }"
      tabindex="0"
      role="option"
      :aria-selected="isSelected(n) ? 'true' : 'false'"
      :aria-label="nodeDisplayName(n) /* wiring:c4 */"
      :data-fe-path="n.path /* wiring:d1 — orta-tık yeni sekme delegasyonu */"
      draggable="true"
      @click="onClick(n, $event)"
      @dblclick="onDbl(n)"
      @contextmenu="onCtx(n, $event)"
      @dragstart="onItemDragStart(n, $event)"
      @dragover="onItemDragOver(n, $event)"
      @dragleave="onItemDragLeave(n) /* wiring:c4 */"
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
          v-if="thumbOf(n)"
          :src="thumbOf(n)!"
          :alt="n.basename"
          loading="lazy"
          draggable="false"
        />
        <span v-else-if="specialEmojiFor(n)" class="fe-grid__icon">{{ specialEmojiFor(n) }}</span>
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
        <span v-else class="fe-grid__icon fe-grid__icon--svg" v-html="fileIconSvg(n)"></span>
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
      <!-- bul:s3 — content snippet («» → <mark> via TEXT segments, no innerHTML) -->
      <div v-if="cardSnippet(n)" class="fe-grid__snippet" :title="snippetTitle(cardSnippet(n))">
        <template v-for="(seg, si) in snippetSegments(cardSnippet(n))" :key="si">
          <mark v-if="seg.match" class="fe-grid__mark">{{ seg.text }}</mark>
          <template v-else>{{ seg.text }}</template>
        </template>
      </div>
    </div>
    <div v-if="!loading && files.length === 0" class="fe-grid__empty">
      {{ t('empty.folder') }}
    </div>
  </div>
</template>
