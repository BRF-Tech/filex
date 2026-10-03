<script setup lang="ts">
/**
 * FolderMosaic — a folder drawn with the files that came into it last (the
 * listing's `preview`, newest first; docs/thumbnails.md → Folder previews).
 * One component, one shape per view, so the three can never disagree about
 * which files a folder shows:
 *
 *   - `peek` (the grid): the folder large, its back with the tab, up to three
 *     of its files rising out of it as prints turned a little, and its front
 *     over their lower half. With none, the same folder, empty, so a row of
 *     cards keeps one height and one look.
 *   - `fan` (the gallery): the same folder drawn large behind, and up to three
 *     prints fanned out in front of it from their foot, the newest in front.
 *   - `mini` (the list): the row's small folder with the newest file rising
 *     out of its mouth.
 *
 * With no files to show, each shape is its folder empty: a view where any
 * folder has files draws every folder so, and keeps one look.
 *
 * A file with a thumbnail is its picture, through ThumbTile (the same
 * authenticated loader and cache as the file cards, and only once near the
 * viewport); one without is its type icon on the print. An end-to-end
 * encrypted folder keeps its padlock glyph. An administrator can turn folder
 * previews off (Settings); the listing then sends no `preview` and the views
 * keep their classic folder icons.
 *
 * ⚠ No `<style>` here: the rules are in styles/base.css (`.fe-fmosaic`), so
 * the core and web-component stylesheets stay the same bytes.
 */
import { computed } from 'vue';
import type { FileNode } from '../types/FileNode';
import ThumbTile from './ThumbTile.vue';
import { encryptedFolderTile, fileIconTile, isEncryptedFolder } from '../lib/fileIcons';

const props = withDefaults(
  defineProps<{
    node: FileNode;
    /** The view's thumbnail resolver (useThumbs.src). */
    srcOf: (n: FileNode) => string | null;
    shape?: 'peek' | 'fan' | 'mini';
  }>(),
  { shape: 'peek' },
);

/** How many files a shape shows: the list's row has room for one. */
const room = computed(() => (props.shape === 'mini' ? 1 : 3));

/** The files as nodes the resolver and the icon table understand: a path
 *  under the folder (the cache key), the stamped URL when the listing gave
 *  one, and the extension the type icon is picked by. */
const items = computed<FileNode[]>(() =>
  (props.node.preview ?? []).slice(0, room.value).map((p) => {
    const dot = p.name.lastIndexOf('.');
    return {
      path: `${props.node.path.replace(/\/+$/, '')}/${p.name}`,
      basename: p.name,
      type: 'file' as const,
      extension: dot > 0 ? p.name.slice(dot + 1).toLowerCase() : undefined,
      thumb_url: p.thumb_url,
    };
  }),
);

/** Back to front: the newest is drawn last, on top and in the middle (1:
 *  left, 2: middle or right, 3: right; styles/base.css). */
const prints = computed(() => {
  const [a, b, c] = items.value;
  if (c) return [{ it: c, at: 1 }, { it: b, at: 3 }, { it: a, at: 2 }];
  if (b) return [{ it: b, at: 1 }, { it: a, at: 2 }];
  return a ? [{ it: a, at: 1 }] : [];
});
</script>

<template>
  <div
    class="fe-fmosaic"
    :class="[`fe-fmosaic--${shape}`, `fe-fmosaic--${items.length}`]"
    aria-hidden="true"
    data-fe-mosaic
  >
    <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
    <span v-if="isEncryptedFolder(node)" class="fe-fmosaic__glyph fe-grid__icon fe-grid__icon--svg" v-html="encryptedFolderTile()"></span>
    <div v-else class="fe-fmosaic__stage">
      <!-- The back, with its tab on the inline-start side. -->
      <svg class="fe-fmosaic__back" viewBox="0 0 120 88" preserveAspectRatio="none" focusable="false">
        <path d="M0 10a6 6 0 0 1 6-6h27.5a6 6 0 0 1 4.4 1.9L43.6 12H114a6 6 0 0 1 6 6v64a6 6 0 0 1-6 6H6a6 6 0 0 1-6-6z" />
      </svg>
      <div
        v-for="p in prints"
        :key="p.it.path"
        class="fe-fmosaic__print"
        :class="[`fe-fmosaic__print--${p.at}`, { 'is-icon': !p.it.thumb_url }]"
      >
        <ThumbTile :node="p.it" :src-of="srcOf" alt="" class="fe-fmosaic__img">
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
          <span class="fe-fmosaic__icon fe-grid__icon--svg" v-html="fileIconTile(p.it)"></span>
        </ThumbTile>
      </div>
      <!-- The front, a little narrower at the bottom, as if seen from above. -->
      <svg class="fe-fmosaic__front" viewBox="0 0 120 56" preserveAspectRatio="none" focusable="false">
        <path d="M5 0h110a5 5 0 0 1 5 5.4l-3.3 44.6a6 6 0 0 1-6 5.6H9.3a6 6 0 0 1-6-5.6L0 5.4A5 5 0 0 1 5 0z" />
      </svg>
    </div>
  </div>
</template>
