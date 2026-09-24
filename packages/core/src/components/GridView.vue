<script setup lang="ts">
/**
 * GridView — card grid.
 *
 * gorunum:v1 — the card has an anatomy now, and it is not the same anatomy
 * for a folder and for a file:
 *
 *   folder  186×56   glyph · name over "Folder" · ⋮
 *   file    186×166  184×108 preview · 56px footer (24px type tile · name
 *                    over "1.7 MB • Sep 9, 2026" · ⋮)
 *
 * A folder has no preview to show and no size or date worth printing, so a
 * square thumbnail box above its name was 108px of nothing on the most common
 * row in any listing. The two shapes share one footer row — the folder card
 * IS that row — so there is a single piece of markup to change when the row
 * changes.
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue'; /* wiring:c4 */
import type { FileNode } from '../types/FileNode';
import { hasInternalDrag } from '../lib/dragOut';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { lockOf, lockWords } from '../lib/appLock';
import { linkWordsFor } from '../lib/symlink'; /* issue #34 — a link that will not open */
import { checkMod, clickMod, useRowTouch, type ClickMod } from '../composables/useRowTouch';
import ItemCheck from './ItemCheck.vue';
import { encryptedFolderTile, fileIconTile, isEncryptedFolder } from '../lib/fileIcons';
import {
  createFilePreviews,
  drawsAsPage,
  drawsAsVideo,
  type FilePreview,
} from '../lib/filePreview'; /* gorunum:v1-preview */
import { byFoldersFirst, parentDirOf } from '../lib/listing'; /* gorunum:v1 */
import {
  groupByDate,
  groupingActive,
} from '../lib/dateGroups'; /* gruplama */
import { useSortStore, type ListingOrder } from '../lib/sortOrder'; /* gruplama */
import StarButton from './StarButton.vue';
import ThumbTile from './ThumbTile.vue';
import { snippetSegments } from '../lib/snippet'; /* bul:s3 */
import { applyDragGhost } from '../lib/dragGhost'; /* wiring:c4 */
import { contentDir } from '../lib/direction';

const props = withDefaults(defineProps<{
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
  /** Desktop selective sync: availability badge per tile. Absent on the
   *  web — no badge renders at all. */
  keepBadgeFor?: (n: FileNode) => 'kept' | 'syncing' | 'cloud' | 'partial' | null;
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
  /**
   * surucu:d1 — draw "Folders" and "Files" as labelled sections.
   *
   * ⚠ It used to arrive with `uiProfile: 'drive'`; the explorer now passes it
   * unconditionally, because the sections are part of the shell rather than of
   * a profile. The prop stays for an embedder mounting this view directly:
   * absent/false renders the grid without them, down to the DOM — the two
   * headings are the only extra nodes, and without this prop none are emitted.
   *
   * ⚠ The prop gates the HEADINGS, not the order. Folders come before files
   * in every profile (see `ordered`) — a heading only names a group that is
   * there either way.
   */
  sections?: boolean;
  /**
   * gruplama — where these rows got their order, exactly as `ListView` takes
   * it (`lib/sortOrder.ListingOrder`).
   *
   * ⚠ It changes nothing about the ORDER here — the pane hands this view rows
   * that are already sorted, and `compareInOrder('relevance')` is
   * byte-for-byte the folders-first pass below. It is read for one thing: a
   * date heading over a RANKED answer would be a claim about an order the
   * rows are not in. Omitted = an ordinary folder listing.
   */
  order?: ListingOrder;
  /**
   * gorunum:v1-preview — we are inside an E2E-encrypted folder.
   *
   * Every body on the wire in there is ciphertext, so reading a file's first
   * kilobytes to draw its first lines would spend a request to learn that the
   * bytes do not decode. The text preview stays off; the cards keep their
   * type tiles. Absent/false = the ordinary case.
   */
  e2eActive?: boolean;
  /**
   * issue #26 — draw the checkbox on each card, the one click that selects.
   * Default on. Home passes false: its cards open on a click and there is no
   * selection there to put anything into.
   */
  selectable?: boolean;
}>(), {
  // ⚠ Not left to `undefined`: Vue casts an absent boolean prop to false, and
  // the listing would lose its checkboxes wherever the host said nothing.
  selectable: true,
});

const emit = defineEmits<{
  (e: 'click-card', node: FileNode, mod: ClickMod): void;
  (e: 'dbl-card', node: FileNode): void;
  (e: 'context-card', node: FileNode, ev: MouseEvent): void;
  (e: 'item-drag-start', node: FileNode, ev: DragEvent): void;
  (e: 'item-drop-into', target: FileNode, ev: DragEvent): void;
  (e: 'star-change', node: FileNode, value: boolean): void;
  /** gorunum:v1 — the order this view renders, so the parent's shift-range
   *  arithmetic runs over what the user sees rather than over the backend's
   *  answer. See the same emit in ListView. */
  (e: 'display-order', nodes: FileNode[]): void;
}>();

/**
 * gorunum:v1 — folders first, always, and by the SHARED comparator
 * (`lib/listing.byFoldersFirst`) the list view uses as its own primary key.
 * Two sorts, one rule: a second private copy of "dirs are 0, files are 1" is
 * how the grid and the list end up disagreeing (filex lesson #67).
 *
 * `Array.prototype.sort` is stable, and this comparator returns 0 for any two
 * nodes of the same kind, so whatever order the parent sends survives
 * untouched INSIDE each group. The day a sort control reaches the grid,
 * descending will mean folders descending and then files descending, without
 * this line being touched.
 *
 * Display order only. Selection is keyed by path and the explorer's shift-range
 * still walks its own list, so nothing here moves an index the parent holds.
 */
const { t, formatSize, nodeDisplayName, formatDate, formatMonthYear, zonedYearMonth, toDate } =
  useLocale(() => props.locale);

/* App plugins — an app's hold on a row (docs/APP-PLUGINS-API.md → "File
   locks"). The listing already caps `perm` at viewer, so the write verbs are
   gone without this; the badge is what stops that reading as a bug. ⚠ One
   source of words for every view and the details panel (lib/appLock). */
function lockTitleOf(n: FileNode): string {
  return lockWords(lockOf(n), { t, formatDate, locale: props.locale });
}

/* issue #34 — a symlink the server will not follow. Same shape as the lock
   badge above and for the same reason: the words live in ONE module
   (lib/symlink), so the row, the details panel and the toast that explains a
   refused open cannot drift apart. `null` for every ordinary row — a link
   whose target is inside the root was already followed and arrives as that
   target, so it is not one of these. */
function linkOf(n: FileNode) {
  return linkWordsFor(n, { t });
}


const ordered = computed<FileNode[]>(() => [...props.files].sort(byFoldersFirst));
watch(
  ordered,
  (list) => emit('display-order', list),
  { immediate: true },
);

const firstDirPath = computed(() => ordered.value.find((f) => f.type === 'dir')?.path ?? null);
const firstFilePath = computed(() => ordered.value.find((f) => f.type !== 'dir')?.path ?? null);

/* gruplama — the same sort this pane's list view reads, by injection, so the
 * two cannot disagree about whether a date heading is honest right now. */
const sort = useSortStore();

/**
 * gruplama — THE DATE HEADINGS, from `lib/dateGroups`, which is also where the
 * list gets them. Not a grid variant of the ladder: a rung added there has to
 * appear here on the same day or the two views name the same day differently.
 */
const grouped = computed(() =>
  groupByDate(ordered.value, {
    active: groupingActive(sort.key.value, props.order),
    dateOf: (n) => toDate(n.last_modified),
    /* Folders keep their own run at the top — the same rule the list follows,
     * and the reason "folders before files" survives a date sort. Here the run
     * is the one that already had a name. */
    aside: (n) =>
      n.type === 'dir'
        ? { id: 'dirs', label: props.sections ? t('drive.section.folders') : null }
        : null,
    labels: { t, formatMonthYear, zonedYearMonth },
  }),
);

/**
 * ⚠⚠ TWO HEADING SYSTEMS DO NOT STACK. While the rows are in date order the
 * "Files" label is replaced by the date headings rather than sitting above
 * them: the date headings ARE the files' headings, and "Files" followed
 * immediately by "Today" names one group twice and tells the reader nothing
 * the second time. "Folders" stays, because the folders are still one run and
 * no date can name it.
 *
 * Off that order (any other sort key, or a ranked search answer) the grid
 * keeps exactly the Folders / Files sections it has always drawn.
 */
function headingBefore(n: FileNode): string | null {
  if (grouped.value.active) return grouped.value.headingBefore(n);
  if (!props.sections) return null;
  if (n.path === firstDirPath.value) return t('drive.section.folders');
  if (n.path === firstFilePath.value) return t('drive.section.files');
  return null;
}


// Prefer the authenticated resolver when the host wired one; otherwise fall
// back to the raw URL (legacy same-origin behavior). ⚠ Called by ThumbTile,
// not by this template — see the note on the tile in the markup.
function thumbOf(n: FileNode): string | null {
  return props.thumbSrc ? props.thumbSrc(n) : (n.thumb_url ?? null);
}

/* ==================================================================
 * gorunum:v1-preview — the card shows the file's own first lines.
 *
 * ⚠ The brief for this slice said these files "have no thumbnail". Measured
 * on the local backend, they do: `internal/thumb/generic.go` renders a card
 * tinted from a hash of the extension with the letters "TS"/"CSV"/"ZIP" drawn
 * in it, and the listing hands out a `thumb_url` for it (app.ts → a 1,745-byte
 * JPEG). So the choice is not "preview or nothing", it is "the file's first
 * lines, or a coloured rectangle repeating the extension the footer tile and
 * the Type column both already say".
 *
 * Hence the order below: for a node we can read as TEXT the `<img>` is not
 * rendered at all — which also means `thumbOf()` is never called for it and
 * `useThumbs` never fetches that generic JPEG, so this costs the same one
 * request it replaces. Every other node is untouched: images, video, audio
 * and PDFs keep their real, content-derived thumbnails.
 *
 * While the read is in flight (and if it fails, or the bytes turn out not to
 * be text) the card shows the type tile on `--fe-bg-elev` — the spec's stated
 * fallback, and no flash of a placeholder we are about to replace.
 * ================================================================== */
const previews = createFilePreviews({
  // Read once: the explorer's `config.apiBase` is fixed for the life of a
  // mounted panel, and `enabled` has to be answerable at construction.
  apiBase: props.apiBase,
  // Wrapped rather than passed, so a parent that re-creates its arrow on each
  // render is still called through the CURRENT prop. ⚠ The builder is async;
  // `createFilePreviews` awaits it (see the note there — a Promise spread into
  // a headers object sends no Authorization and 401s in silence).
  authHeaders: props.authHeaders ? () => props.authHeaders!() : undefined,
  credentials: props.authCredentials,
  disabled: () => props.e2eActive === true,
});
onBeforeUnmount(() => previews.dispose());

/** Non-null only for a node whose bytes we can read as text. */
function previewKind(n: FileNode) {
  return previews.kindFor(n);
}

/** At most one element, so the template gets a typed local without a
 *  non-null assertion repeated at every nested loop. */
function previewFor(n: FileNode): FilePreview[] {
  const p = previews.get(n);
  return p ? [p] : [];
}

/** Template-ref sink: the preview box itself is what the IntersectionObserver
 *  watches, so nothing is fetched for a card that never comes into view.
 *  ⚠ `unknown` because a Vue template `:ref` hands back an Element, a
 *  component instance or null depending on what it is put on. */
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

/**
 * issue #26 — the card's checkbox is the one click that selects (a click
 * anywhere else on the card opens it), routed exactly like the list row's:
 * one `click-card` emit marked `check`, one `useSelection.click`.
 */
function onCheckClick(n: FileNode, ev: MouseEvent) {
  emit('click-card', n, checkMod(ev));
}

function onClick(n: FileNode, ev: MouseEvent) {
  emit('click-card', n, clickMod(ev, touch.isTap(ev)));
}

function onDbl(n: FileNode) {
  emit('dbl-card', n);
}

function onCtx(n: FileNode, ev: MouseEvent) {
  ev.preventDefault();
  ev.stopPropagation();
  emit('context-card', n, ev);
}

/**
 * gorunum:v1 — the ⋮ button. It calls the RIGHT-CLICK handler with the same
 * node and the same event, so the menu it opens is the context menu, built by
 * the explorer's one `selectionActionList`. There is deliberately no action
 * list here: a second one drifts from the first the week after it is written
 * (filex lesson #67).
 */
function onMenuButton(n: FileNode, ev: MouseEvent) {
  onCtx(n, ev);
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
  if (!hasInternalDrag(ev)) return;
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
  if (!hasInternalDrag(ev)) return;
  ev.preventDefault();
  ev.stopPropagation();
  emit('item-drop-into', n, ev);
}

/* Long press → the card's menu; a tap is reported as a tap (issue #26). */
const touch = useRowTouch<FileNode>(
  (n, at) =>
    emit('context-card', n, { ...at, preventDefault: () => {}, stopPropagation: () => {} } as unknown as MouseEvent),
  {
    // A finger lifted on the item, off its controls, opens at touchend — see useRowTouch.
    onTap: (n) => emit('click-card', n, { ctrl: false, shift: false, touch: true }),
  },
);

/**
 * The card's second line. A file prints "1.7 MB • Sep 9, 2026"; a folder
 * prints what it is, because its size is 0 and its date is the date something
 * inside it changed.
 *
 * Same epoch normalization the list and the gallery use (the backend has sent
 * both seconds and milliseconds), and the same locale the rest of the card is
 * drawn in — `toLocaleDateString` with the explorer's own tag, not the
 * browser's, so a Turkish panel says "9 Eyl 2026" whatever the OS is set to.
 */
function displayDate(ms: number | undefined): string {
  return formatDate(ms);
}

function captionFor(n: FileNode): string {
  if (n.type === 'dir') return t('node.folder');
  const when = displayDate(n.last_modified);
  const size = formatSize(n.size);
  return when ? `${size} • ${when}` : size;
}

function keepGlyph(b: 'kept' | 'syncing' | 'cloud' | 'partial'): string {
  if (b === 'kept') return '\u2713';
  if (b === 'syncing') return '\u27f3';
  if (b === 'partial') return '\u25d0';
  return '\u2601';
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
    :class="{ 'is-loading': loading, 'has-selection': selectable && selected.size > 0 }"
    role="listbox"
    aria-multiselectable="true"
    :aria-label="t('grid.aria')"
    :aria-busy="loading ? 'true' : undefined"
  >
    <template v-for="n in ordered" :key="n.path">
    <!-- surucu:d1 — the section label: a grid item spanning every column. The
         cards keep `role="option"`; the heading is aria-hidden, and the group
         it names is already in each card's own label. -->
    <p v-if="headingBefore(n)" class="fe-grid__heading" aria-hidden="true">{{ headingBefore(n) }}</p>
    <div
      class="fe-grid__card"
      :class="{
        'fe-grid__card--folder': n.type === 'dir' /* gorunum:v1 */,
        'fe-grid__card--file': n.type !== 'dir' /* gorunum:v1 */,
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
      :data-fe-path="n.path /* wiring:d1 — middle-click open-in-new-tab delegation */"
      draggable="true"
      @click="onClick(n, $event)"
      @dblclick="onDbl(n)"
      @contextmenu="onCtx(n, $event)"
      @dragstart="onItemDragStart(n, $event)"
      @dragover="onItemDragOver(n, $event)"
      @dragleave="onItemDragLeave(n) /* wiring:c4 */"
      @drop="onItemDrop(n, $event)"
      @touchstart.passive="touch.onTouchStart(n, $event)"
      @touchend="touch.onTouchEnd"
      @touchmove.passive="touch.onTouchMove"
    >
      <!-- gorunum:v1 — the preview, files only: 184×108. A thumbnail when
           there is one, otherwise the type tile centred on --fe-bg-elev.
           gorunum:v1-preview — and, for a file we can read as text, its own
           first lines instead (see the block comment in the script). -->
      <div v-if="n.type !== 'dir'" class="fe-grid__thumb" :ref="(el) => bindPreview(el, n)">
        <!-- gorunum:v1-preview — text-readable kinds never reach the <img>
             branch, so no generic placeholder thumbnail is fetched for them. -->
        <template v-if="previewKind(n)">
          <!-- ⚠ Interpolated, never v-html: this is somebody's file content. -->
          <div
            v-for="p in previewFor(n)"
            :key="'fprev'"
            class="fe-fprev"
            :dir="contentDir(p.kind)"
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
            class="fe-grid__icon fe-grid__icon--svg"
            v-html="fileIconTile(n)"
          ></span>
        </template>
        <!-- ⚠⚠ The thumbnail is read inside ThumbTile, never here: read in
             this template, each thumbnail that arrived re-rendered every card
             of the folder (see ThumbTile). The tile draws the <img> once there
             is a picture — draggable="false", because an image drag puts a
             'Files' MIME on the dataTransfer and the parent's upload handler
             re-uploads the file being moved — and its slot until then.
             gorunum:v1-preview — the play badge goes over a real video frame
             only: a video that fell back to its type tile already says what
             it is. -->
        <ThumbTile
          v-else
          :node="n"
          :src-of="thumbOf"
          :video-badge="drawsAsVideo(n)"
          :alt="n.basename"
          :class="{ 'fe-thumb--page': drawsAsPage(n) /* gorunum:v1-preview — crop a page from its TOP */ }"
        >
          <!-- ikon:emoji — an encrypted folder is still a FOLDER, so it keeps the
               folder's own shape and colour with the padlock cut out of it; the
               🔒 it replaces said "locked" and nothing else. One definition,
               in lib/fileIcons, for all three views. -->
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
          <span
            v-if="isEncryptedFolder(n)"
            class="fe-grid__icon fe-grid__icon--svg"
            role="img"
            :aria-label="t('e2e.badge')"
            v-html="encryptedFolderTile()"
          ></span>
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
          <span v-else class="fe-grid__icon fe-grid__icon--svg" v-html="fileIconTile(n)"></span>
        </ThumbTile>
        <!-- Star, ON the tile. A hover-only affordance would be invisible to
             the person looking for what they starred, so the chip is always
             painted once the file IS starred and only appears on hover/focus
             otherwise (see .fe-grid__star in styles/base.css). @click.stop so
             starring never doubles as opening the card. -->
        <div v-if="canStar(n)" class="fe-grid__star" @click.stop @dblclick.stop>
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
      </div>

      <!-- gorunum:v1 — the row both cards share: tile · name over caption · ⋮.
           On a file it is the 56px footer under the preview; on a folder it is
           the whole 56px card. -->
      <div class="fe-grid__foot">
        <!-- issue #26 — the checkbox takes the tile's place while it shows
             (hovered, focused, selected, or once anything is selected), so
             the name does not move when it appears. -->
        <span class="fe-grid__lead" :class="{ 'fe-grid__lead--pick': selectable }">
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
          <span
            v-if="isEncryptedFolder(n)"
            class="fe-grid__tile fe-grid__tile--svg"
            role="img"
            :aria-label="t('e2e.badge')"
            v-html="encryptedFolderTile()"
          ></span>
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
          <span v-else class="fe-grid__tile fe-grid__tile--svg" v-html="fileIconTile(n)"></span>
          <span
            v-if="selectable"
            class="fe-item-check"
            data-fe-control
            @click.stop="onCheckClick(n, $event)"
            @dblclick.stop
          >
            <ItemCheck :on="isSelected(n)" :label="nodeDisplayName(n)" />
          </span>
        </span>
        <div class="fe-grid__main">
          <div class="fe-grid__label" :title="n.basename">
            <bdi>{{ nodeDisplayName(n) }}</bdi>
            <span
              v-if="keepBadgeFor && keepBadgeFor(n)"
              :class="['fe-keepbadge', 'fe-keepbadge--' + keepBadgeFor(n)]"
              :title="t('keep.badge_' + keepBadgeFor(n))"
              role="img"
              :aria-label="t('keep.badge_' + keepBadgeFor(n))"
            >{{ keepGlyph(keepBadgeFor(n)!) }}</span>
          </div>
          <!-- The whole caption on hover: the card has room for "110 B • Sep 22,
               2026" and not for every language's date (German "22. Sept. 2026"
               lost its year to the ellipsis, v0.43.0 translator pass). -->
          <div class="fe-grid__meta" :title="captionFor(n)">
            <span
            v-if="lockTitleOf(n)"
            class="fe-applock"
            role="img"
            :title="lockTitleOf(n)"
            :aria-label="lockTitleOf(n)"
            data-testid="lock-badge"
            ><span class="fe-applock__glyph" aria-hidden="true">&#128274;</span>{{ t('applock.badge') }}</span>
            <!-- issue #34 — a symlink the server will not follow. Same badge,
                 same words, same module as the list and the gallery. -->
            <span
            v-if="linkOf(n)"
            class="fe-symlink"
            :class="'fe-symlink--' + linkOf(n)!.state"
            role="img"
            :title="linkOf(n)!.why"
            :aria-label="linkOf(n)!.why"
            data-testid="symlink-badge"
            :data-link-state="linkOf(n)!.state"
            ><span class="fe-symlink__glyph" aria-hidden="true">&#128279;</span>{{ linkOf(n)!.badge }}</span>
            {{ captionFor(n) }}
          </div>
          <!-- A card at a storage's root has no parent folder to name: no line,
               not an em dash — the same rule the gallery follows and the list's
               Location cell (tablo:t1). The dash read as a stray character under
               the filename in search results. -->
          <div
            v-if="showParentPath && parentDirOf(n.path)"
            class="fe-grid__parent"
            :title="parentDirOf(n.path)"
          ><bdi>{{ parentDirOf(n.path) }}</bdi></div>
          <!-- bul:s3 — content snippet («» → <mark> via TEXT segments, no innerHTML) -->
          <div v-if="cardSnippet(n)" class="fe-grid__snippet" :title="snippetTitle(cardSnippet(n))">
            <template v-for="(seg, si) in snippetSegments(cardSnippet(n))" :key="si">
              <mark v-if="seg.match" class="fe-grid__mark">{{ seg.text }}</mark>
              <template v-else>{{ seg.text }}</template>
            </template>
          </div>
        </div>
        <button
          type="button"
          class="fe-grid__menu"
          :aria-label="t('toolbar.more')"
          :title="t('toolbar.more')"
          @click.stop="onMenuButton(n, $event)"
          @dblclick.stop
        >⋮</button>
      </div>
    </div>
    </template>
    <div v-if="!loading && files.length === 0" class="fe-grid__empty">
      {{ t('empty.folder') }}
    </div>
  </div>
</template>
