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
import { computed, onBeforeUnmount, ref } from 'vue';
import { hasInternalDrag } from '../lib/dragOut';
import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { encryptedFolderTile, fileIconTile, isEncryptedFolder } from '../lib/fileIcons';
import {
  createFilePreviews,
  drawsAsPage,
  drawsAsVideo,
  type FilePreview,
} from '../lib/filePreview'; /* gorunum:v1-preview */
import StarButton from './StarButton.vue';
import { applyDragGhost } from '../lib/dragGhost';
import { parentDirOf } from '../lib/listing'; /* the folder a row sits in — one split rule for every view */
import {
  groupByDate,
  groupingActive,
} from '../lib/dateGroups'; /* gruplama */
import { useSortStore, type ListingOrder } from '../lib/sortOrder'; /* gruplama */

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
  /** gorunum:v1-preview — inside an E2E-encrypted folder every body is
   *  ciphertext, so the text preview stays off. Same prop, same meaning and
   *  same default as GridView's. */
  e2eActive?: boolean;
  /**
   * gruplama — where these rows got their order, the same prop the list and
   * the grid take (`lib/sortOrder.ListingOrder`). Read for one thing only: a
   * date heading over the server's RANKED answer would be a claim about an
   * order the tiles are not in. Omitted = an ordinary folder listing.
   */
  order?: ListingOrder;
}>();

const emit = defineEmits<{
  (e: 'click-card', node: FileNode, mod: { ctrl: boolean; shift: boolean }): void;
  (e: 'dbl-card', node: FileNode): void;
  (e: 'context-card', node: FileNode, ev: MouseEvent): void;
  (e: 'item-drag-start', node: FileNode, ev: DragEvent): void;
  (e: 'item-drop-into', target: FileNode, ev: DragEvent): void;
  (e: 'star-change', node: FileNode, value: boolean): void;
}>();

const { t, formatSize, nodeDisplayName, formatDate, formatMonthYear, zonedYearMonth, toDate } =
  useLocale(() => props.locale);

/* gruplama — this pane's sort, by injection, so the gallery cannot disagree
 * with the list beside it about whether a date heading is honest right now. */
const sort = useSortStore();

/**
 * gruplama — THE DATE HEADINGS, from the one module that owns the ladder
 * (`lib/dateGroups`), read identically by ListView and GridView.
 *
 * ⚠ The gallery has no Folders / Files sections to collide with, so nothing
 * here replaces anything: folders keep a run of their own at the top — with no
 * heading, because the gallery has never named one — and each day of files
 * gets its label. Any sort key but `modified` draws nothing at all, exactly as
 * before this existed.
 *
 * ⚠ The tiles are handed to us ALREADY sorted (`FilePane.displayFiles` runs
 * the pane's comparator, folders first, then the key), which is why this
 * groups `props.files` in place rather than re-sorting: a second sort here is
 * a second opinion about the order, and the grid learned that lesson once.
 */
const grouped = computed(() =>
  groupByDate(props.files, {
    active: groupingActive(sort.key.value, props.order),
    dateOf: (n) => toDate(n.last_modified),
    aside: (n) => (n.type === 'dir' ? { id: 'dirs' } : null),
    labels: { t, formatMonthYear, zonedYearMonth },
  }),
);

function thumbOf(n: FileNode): string | null {
  return props.thumbSrc ? props.thumbSrc(n) : (n.thumb_url ?? null);
}

/* gorunum:v1-preview — the SAME preview the grid card draws, from the same
 * module and with the same bounds. Not a gallery variant: a text file in a
 * gallery is the identical hole (the backend's generic extension-card), and
 * "open it on the desktop, leave the embeds on the old behaviour" is how one
 * product becomes two. Only the type scale differs, and that is CSS. */
const previews = createFilePreviews({
  apiBase: props.apiBase,
  authHeaders: props.authHeaders ? () => props.authHeaders!() : undefined,
  credentials: props.authCredentials,
  disabled: () => props.e2eActive === true,
});
onBeforeUnmount(() => previews.dispose());

function previewKind(n: FileNode) {
  return previews.kindFor(n);
}

/** At most one element — a typed local for the template, no `!` per loop. */
function previewFor(n: FileNode): FilePreview[] {
  const p = previews.get(n);
  return p ? [p] : [];
}

function bindPreview(el: unknown, n: FileNode) {
  previews.bind(el instanceof Element ? el : null, n);
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

/**
 * zaman:z1 — routed through useLocale.formatDate, the package's one date
 * formatter. It was a bare `toLocaleString()`, i.e. the BROWSER's locale and
 * the BROWSER's zone: the same file's timestamp came out in a different
 * language here than in the list two clicks away, and in a different clock
 * than the viewer had chosen. Neither of those is a gallery decision.
 */
function displayDate(ms: number | undefined): string {
  return formatDate(ms, { time: true });
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
    <template v-for="n in files" :key="n.path">
    <!-- gruplama — the date label: a grid item spanning every column, so the
         tiles keep flowing under it. The cards keep `role="option"`; the
         heading is aria-hidden and the day it names is already in each
         card's own hover text. -->
    <p v-if="grouped.headingBefore(n)" class="fe-gal__heading" aria-hidden="true">{{ grouped.headingBefore(n) }}</p>
    <div
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
      <div class="fe-gal__thumb" :ref="(el) => bindPreview(el, n)">
        <!-- gorunum:v1-preview — a text-readable file shows its own first
             lines; it never reaches the <img> branch, so no generic
             placeholder thumbnail is fetched for it either. -->
        <template v-if="previewKind(n)">
          <!-- ⚠ Interpolated, never v-html: this is somebody's file content. -->
          <div
            v-for="p in previewFor(n)"
            :key="'fprev'"
            class="fe-fprev"
            :class="'fe-fprev--' + p.kind"
            :style="p.kind === 'table' ? { '--fprev-cols': String(p.cols) } : undefined"
            aria-hidden="true"
          >
            <!-- One grid, cells emitted flat: <template> makes no DOM, so a
                 short row's padding cells keep every column aligned. -->
            <template v-if="p.kind === 'table'">
              <template v-for="(row, ri) in p.rows" :key="ri">
                <span
                  v-for="(cell, ci) in row"
                  :key="ci"
                  class="fe-fprev__cell"
                  :class="{ 'is-head': ri === 0 }"
                >{{ cell }}</span>
              </template>
            </template>
            <template v-else>
              <div v-for="(ln, li) in p.lines" :key="li" class="fe-fprev__line"><span
                v-if="ln.tint"
                class="fe-fprev__tint"
              >{{ ln.indent }}{{ ln.tint }}</span>{{ ln.text }}</div>
            </template>
          </div>
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
          <span
            v-if="previewFor(n).length === 0"
            class="fe-gal__icon fe-gal__icon--svg"
            v-html="fileIconTile(n)"
          ></span>
        </template>
        <!-- draggable="false" for the same reason as GridView: HTML5 image
             drag adds a 'Files' MIME to dataTransfer, which would trip the
             parent's upload handler on internal drags. -->
        <img
          v-else-if="thumbOf(n)"
          :src="thumbOf(n)!"
          :alt="n.basename"
          :class="{ 'fe-thumb--page': drawsAsPage(n) /* gorunum:v1-preview — crop a page from its TOP */ }"
          loading="lazy"
          draggable="false"
        />
        <!-- ikon:emoji — see GridView: the folder shape survives, the padlock
             is cut out of it, and the name the 🔒 never had is on the span. -->
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
        <span
          v-else-if="isEncryptedFolder(n)"
          class="fe-gal__icon fe-gal__icon--svg"
          role="img"
          :aria-label="t('e2e.badge')"
          v-html="encryptedFolderTile()"
        ></span>
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
        <span v-else class="fe-gal__icon fe-gal__icon--svg" v-html="fileIconTile(n)"></span>
        <!-- Same star chip as the grid, same component, same rule: painted
             when starred, on hover/focus otherwise. It sits above .fe-gal__meta
             (which is aria-hidden and covers the tile's foot on hover). -->
        <!-- gorunum:v1-preview — a frame lifted out of a video is, on a card,
             indistinguishable from a photograph. The badge is the difference,
             and it is drawn only over a real frame: a video that fell back to
             its type tile already says what it is. -->
        <span
          v-if="!previewKind(n) && thumbOf(n) && drawsAsVideo(n)"
          class="fe-thumb__play"
          aria-hidden="true"
        ></span>
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
            v-if="showParentPath && parentDirOf(n.path)"
            class="fe-gal__meta-line fe-gal__meta-line--path"
            :title="parentDirOf(n.path)"
          >{{ parentDirOf(n.path) }}</span>
        </div>
      </div>
      <div class="fe-gal__label" :title="n.basename">
        {{ nodeDisplayName(n) }}
      </div>
    </div>
    </template>
    <div v-if="!loading && files.length === 0" class="fe-gal__empty">
      {{ t('empty.folder') }}
    </div>
  </div>
</template>
