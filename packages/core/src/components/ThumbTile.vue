<script setup lang="ts">
/**
 * ThumbTile — one file's thumbnail, or whatever the view draws instead.
 *
 * ⚠⚠ Why a component. A thumbnail's object URL arrives later, and whatever
 * read it is woken when it does. Read in a folder view's own template, that was
 * the whole view: every arrival re-rendered every row (see useThumbs). Read
 * here, it is this tile.
 *
 * It also asks late. The resolver starts the fetch, and it is only called once
 * the tile is near the viewport (lib/nearViewport), so opening a folder no
 * longer downloads the thumbnails of tiles nobody has scrolled to.
 *
 * The <img> takes the attributes the view gives the tile (class, alt, aria-*);
 * the default slot is the fallback — the view's type tile — drawn until the
 * picture is there, and for good when there is none.
 *
 * ⚠ No `loading="lazy"`: the picture is already only handed over near the
 * viewport, and it is an object URL in memory, so lazy would defer nothing but
 * the decode — and an <img> that has not loaded yet when useThumbs revokes its
 * URL (its oldest, past the cache's cap) would come up broken.
 *
 * The note (0.50, lib/thumbNote): a file whose thumbnail will not come because
 * of the file itself (damaged, encrypted, too large) wears a small marker with
 * an icon on its type tile, and the sentence why on a resting pointer or a
 * tap. Drawn only WITHOUT a picture, the type badge only WITH one: the two
 * never meet. The sentence is teleported to <body> (a list row's 24px box
 * clips), placed along the inline axis like the folder peek (lib/direction).
 */
import { computed, getCurrentInstance, nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import type { FileNode } from '../types/FileNode';
import type { ThumbTypeBadge } from '../lib/filePreview';
import type { ThumbNoteWords } from '../lib/thumbNote';
import { whenNearViewport } from '../lib/nearViewport';
import { clampAlongInline, dirOfElement } from '../lib/direction';
import { popupLayer } from '../lib/popupLayer';

defineOptions({ inheritAttrs: false });

const props = defineProps<{
  node: FileNode;
  /** The view's resolver: the object URL to draw, or null (not yet, or none). */
  srcOf: (n: FileNode) => string | null;
  /** Draw the play badge over the picture — a frame lifted out of a video is,
   *  on a card, indistinguishable from a photograph. */
  videoBadge?: boolean;
  /**
   * Name the kind in a corner of the picture (lib/filePreview `thumbTypeBadge`).
   * The list view passes it for a thumbnail drawn on paper: 24 pixels of a
   * white page on a white row reads as a missing icon. Drawn only WITH the
   * picture: the fallback is the coloured type tile, which already says it.
   * Decorative (`aria-hidden`): the row's accessible name is the file's.
   */
  typeBadge?: ThumbTypeBadge | null;
  /**
   * Why the file has no thumbnail, when the reason is the file's own
   * (lib/thumbNote `thumbNoteWords`): drawn on the fallback only.
   */
  note?: ThumbNoteWords | null;
}>();

const near = ref(false);
let stopWaiting: (() => void) | null = null;

onMounted(() => {
  /* The tile draws no box of its own (it is the picture, or the fallback), so
     it watches the box it sits in — the view's thumbnail cell. */
  const anchor = getCurrentInstance()?.subTree.el as Node | null | undefined;
  stopWaiting = whenNearViewport(anchor?.parentElement, () => {
    near.value = true;
  });
});
onBeforeUnmount(() => {
  stopWaiting?.();
  closeTip();
});

const src = computed(() => (near.value ? props.srcOf(props.node) : null));

/* ── the note's sentence ──────────────────────────────────────────────── */

const mark = ref<HTMLElement | null>(null);
const tip = ref<HTMLElement | null>(null);
const tipOpen = ref(false);
const tipDir = ref<'ltr' | 'rtl'>('ltr');

/** Under the marker, starting at its inline-start edge; above it when there
 *  is no room below; always inside the window (FolderPeek's rule). */
function placeTip() {
  const el = tip.value;
  const m = mark.value;
  if (!el || !m) return;
  const r = m.getBoundingClientRect();
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  const w = el.offsetWidth;
  const h = el.offsetHeight;
  const d = tipDir.value;
  const x = clampAlongInline(d === 'rtl' ? r.right : r.left, w, vw, d);
  let y = r.bottom + 6;
  if (y + h > vh - 8) y = Math.max(8, r.top - h - 6);
  el.style.insetInlineStart = `${Math.round(d === 'rtl' ? vw - x - w : x)}px`;
  el.style.top = `${Math.round(y)}px`;
  el.style.zIndex = String(popupLayer(m));
}

/** A tap anywhere else, or a scroll, puts it away. */
function onOutside(e: Event) {
  if (mark.value && e.target instanceof Node && mark.value.contains(e.target)) return;
  closeTip();
}

function openTip() {
  if (tipOpen.value || !mark.value) return;
  tipDir.value = dirOfElement(mark.value);
  tipOpen.value = true;
  document.addEventListener('pointerdown', onOutside, true);
  window.addEventListener('scroll', closeTip, true);
  void nextTick(placeTip);
}

function closeTip() {
  if (!tipOpen.value) return;
  tipOpen.value = false;
  document.removeEventListener('pointerdown', onOutside, true);
  window.removeEventListener('scroll', closeTip, true);
}

/** A mouse shows it while it rests on the marker; touch and pen tap it. */
function onEnter(e: PointerEvent) {
  if (e.pointerType === 'mouse') openTip();
}
function onLeave(e: PointerEvent) {
  if (e.pointerType === 'mouse') closeTip();
}
/* ⚠ What pressed last. A mouse's click lands on a marker whose sentence its
   own hover has just opened: toggling there put the sentence away on every
   click (0.50 full round, a real document server, all three engines - "a
   tap shows the sentence" never held with a mouse). A mouse's click keeps it
   open; touch and pen toggle it. */
let pressedBy = '';
function onDown(e: PointerEvent) {
  pressedBy = e.pointerType;
}
function onTap() {
  if (pressedBy === 'mouse') {
    openTip();
    return;
  }
  if (tipOpen.value) closeTip();
  else openTip();
}
</script>

<template>
  <img v-if="src" v-bind="$attrs" :src="src" draggable="false" />
  <slot v-else />
  <span v-if="src && videoBadge" class="fe-thumb__play" aria-hidden="true"></span>
  <!-- The colour is the type tile's own (`fe-ftile--<family>` fills it with
       `--fe-icon-<family>`, both themes); no second colour table. -->
  <span
    v-if="src && typeBadge"
    :class="['fe-thumb__type', `fe-ftile--${typeBadge.family}`, { 'fe-thumb__type--long': typeBadge.label.length > 4 }]"
    aria-hidden="true"
  >{{ typeBadge.label }}</span>
  <!-- The note: on the type tile only, never over a picture. The click is
       the marker's own (a tap shows the sentence), never the card's: it
       neither selects nor opens the file. -->
  <span
    v-if="!src && note"
    ref="mark"
    :class="['fe-thumb__note', `fe-thumb__note--${note.note}`]"
    role="img"
    :aria-label="note.full"
    :data-note="note.note"
    data-testid="thumb-note"
    @pointerenter="onEnter"
    @pointerleave="onLeave"
    @pointerdown="onDown"
    @click.stop="onTap"
    @dblclick.stop
  >
    <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path v-for="(d, i) in note.paths" :key="i" :d="d" />
    </svg>
  </span>
  <Teleport to="body">
    <span
      v-if="tipOpen && note"
      ref="tip"
      class="fe-thumb__note-tip"
      :dir="tipDir"
      role="tooltip"
      data-testid="thumb-note-tip"
    >{{ note.full }}</span>
  </Teleport>
</template>
