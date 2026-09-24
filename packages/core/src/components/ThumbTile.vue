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
 */
import { computed, getCurrentInstance, onBeforeUnmount, onMounted, ref } from 'vue';
import type { FileNode } from '../types/FileNode';
import { whenNearViewport } from '../lib/nearViewport';

defineOptions({ inheritAttrs: false });

const props = defineProps<{
  node: FileNode;
  /** The view's resolver: the object URL to draw, or null (not yet, or none). */
  srcOf: (n: FileNode) => string | null;
  /** Draw the play badge over the picture — a frame lifted out of a video is,
   *  on a card, indistinguishable from a photograph. */
  videoBadge?: boolean;
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
onBeforeUnmount(() => stopWaiting?.());

const src = computed(() => (near.value ? props.srcOf(props.node) : null));
</script>

<template>
  <img v-if="src" v-bind="$attrs" :src="src" draggable="false" />
  <slot v-else />
  <span v-if="src && videoBadge" class="fe-thumb__play" aria-hidden="true"></span>
</template>
