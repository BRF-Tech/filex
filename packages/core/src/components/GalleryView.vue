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
import { hasInternalDrag } from '../lib/dragOut';
import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { fileIconSvg } from '../lib/fileIcons';
import StarButton from './StarButton.vue';
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
  /**
   * Starring on a card. Same contract as ListView: the id set the explorer
   * keeps, plus the API wiring StarButton needs. Absent apiBase → no star is
   * rendered at all, exactly as in the list.
   *
   * ⚠ Not list-only. The Starred VIEW shipped before starring was reachable
   * from anywhere but the list, so a user in grid view — the mode the panel's
   * own screenshots show — had a view they could not fill.
   */
  starredIds?: Set<number>;
  /**
   * Offer the star affordance at all. Follows the Starred view: when the panel
   * leaves that view out — a shared app token has no single person behind it,
   * so "your starred files" is one list shown to strangers — offering to star
   * something is offering to write into that same shared list. Owner's call,
   * 2026-09-05, verbatim (translated from Turkish): "if the starred place is
   * not visible, then star/unstar should not be visible either".
   */
  starEnabled?: boolean;
  apiBase?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  authCredentials?: RequestCredentials;
}>();

const emit = defineEmits<{
  (e: 'click-card', node: FileNode, mod: { ctrl: boolean; shift: boolean }): void;
  (e: 'dbl-card', node: FileNode): void;
  (e: 'context-card', node: FileNode, ev: MouseEvent): void;
  (e: 'item-drag-start', node: FileNode, ev: DragEvent): void;
  (e: 'item-drop-into', target: FileNode, ev: DragEvent): void;
  (e: 'star-change', node: FileNode, value: boolean): void;
}>();

const { t, formatSize, nodeDisplayName } = useLocale(() => props.locale);

function thumbOf(n: FileNode): string | null {
  return props.thumbSrc ? props.thumbSrc(n) : (n.thumb_url ?? null);
}

/** A card carries a star when the host wired the API and the node is a file
 *  with a server id — the same rule the list row uses. */
function canStar(n: FileNode): boolean {
  if (props.starEnabled === false) return false;
  return props.apiBase !== undefined && typeof n.id === 'number' && n.type === 'file';
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
  if (!hasInternalDrag(ev)) return;
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
  if (!hasInternalDrag(ev)) return;
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
  if (n.type === 'dir' && n.e2e === true) return '🔒'; /* wiring:e2 — encrypted-folder badge */
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
      :data-fe-path="n.path /* wiring:d1 - middle-click new-tab delegation */"
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
        <!-- Same star chip as the grid, same component, same rule: painted
             when starred, on hover/focus otherwise. It sits above .fe-gal__meta
             (which is aria-hidden and covers the tile's foot on hover). -->
        <div v-if="canStar(n)" class="fe-gal__star" @click.stop @dblclick.stop>
          <StarButton
            :starred="!!starredIds?.has(n.id!)"
            :node-id="n.id!"
            :api-base="apiBase"
            :auth-headers="authHeaders"
            :auth-credentials="authCredentials"
            :locale="locale"
            compact
            card
            @change="(val: boolean) => emit('star-change', n, val)"
          />
        </div>
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
