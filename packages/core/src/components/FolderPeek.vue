<script setup lang="ts">
/**
 * FolderPeek — the quiet list a folder shows when the pointer rests on it
 * (useFolderPeek): how many things are inside, and the first few, each with
 * its thumbnail or type tile. Not a menu: nothing in it takes a click
 * (`pointer-events: none` in base.css), so it can never catch the click that
 * was meant for the folder under it.
 *
 * ⚠ Teleported to <body>, so it carries its own `dir` (lesson #333) and takes
 * its layer from what it hangs over (lib/popupLayer), and it is placed in a
 * function through `el.style`, along its inline axis (lesson #508; lib/
 * direction does the direction-aware maths).
 */
import { computed, nextTick, ref, watch } from 'vue';
import type { FileNode } from '../types/FileNode';
import type { FolderPeekState } from '../composables/useFolderPeek';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { clampAlongInline, dirOfElement } from '../lib/direction';
import { popupLayer } from '../lib/popupLayer';
import { encryptedFolderTile, fileIconTile, iconFamilyFor, isEncryptedFolder } from '../lib/fileIcons';
import { drawsOnPaper, previewKindFor, thumbTypeBadge } from '../lib/filePreview';
import ThumbTile from './ThumbTile.vue';

const props = defineProps<{
  state: FolderPeekState | null;
  srcOf: (n: FileNode) => string | null;
  locale: LocaleCode;
}>();

const { t, dir, nodeDisplayName } = useLocale(() => props.locale);

const box = ref<HTMLElement | null>(null);

/**
 * A row gets its thumbnail when the thumbnail is a picture of the content: a
 * photograph, a video frame, or a page (lib/filePreview `drawsOnPaper`, the
 * list view's rule, so the two lists agree). A kind the list draws as its type
 * tile instead (a short text or csv, code: `previewKindFor`) keeps its tile
 * here too, and so does a kind the server can only draw as a coloured card
 * repeating the extension (audio, unknown).
 *
 * ⚠ This was a private set (image, video, pdf, doc, slides) that left a
 * spreadsheet, a text and an archive on their tiles from before 0.50 drew
 * their pages. A page gets the list's type badge (`thumbTypeBadge`): at 24
 * pixels it is a white square on a white card otherwise.
 */
const pictured = (n: FileNode) => {
  if (n.type === 'dir' || previewKindFor(n)) return false;
  const family = iconFamilyFor(n);
  return family === 'image' || family === 'video' || drawsOnPaper(n);
};
const more = computed(() => (props.state ? Math.max(0, props.state.total - props.state.items.length) : 0));

/** Below the card, starting at its inline-start edge; above it when there is
 *  no room below; always inside the window. */
function place() {
  const el = box.value;
  const st = props.state;
  if (!el || !st) return;
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  const w = el.offsetWidth;
  const h = el.offsetHeight;
  const d = dirOfElement(el);
  const start = d === 'rtl' ? st.anchor.right : st.anchor.left;
  const x = clampAlongInline(start, w, vw, d);
  let y = st.anchor.bottom + 6;
  if (y + h > vh - 8) y = Math.max(8, st.anchor.top - h - 6);
  // clampAlongInline answers from the viewport's left; the box is placed
  // along its own inline axis, so it is written as the inline-start inset.
  el.style.insetInlineStart = `${Math.round(d === 'rtl' ? vw - x - w : x)}px`;
  el.style.top = `${Math.round(y)}px`;
  el.style.zIndex = String(popupLayer(document.elementFromPoint(st.anchor.left + 1, st.anchor.top + 1)));
}

watch(
  () => props.state,
  async () => {
    await nextTick();
    place();
  },
);
</script>

<template>
  <Teleport to="body">
    <div
      v-if="state"
      ref="box"
      class="fe-fpeek"
      :dir="dir"
      role="tooltip"
      data-fe-peek
      :aria-label="nodeDisplayName(state.node)"
    >
      <p class="fe-fpeek__head">
        <bdi class="fe-fpeek__name">{{ nodeDisplayName(state.node) }}</bdi>
        <span v-if="!state.loading" class="fe-fpeek__count">
          {{ state.total === 0 ? t('peek.empty') : t('peek.items', { n: state.total }) }}
        </span>
      </p>
      <p v-if="state.loading" class="fe-fpeek__loading">{{ t('peek.loading') }}</p>
      <ul v-else-if="state.items.length" class="fe-fpeek__list">
        <li v-for="it in state.items" :key="it.path" class="fe-fpeek__row">
          <span class="fe-fpeek__tile">
            <ThumbTile
              v-if="pictured(it)"
              :node="it"
              :src-of="srcOf"
              :type-badge="thumbTypeBadge(it)"
              alt=""
              class="fe-fpeek__img"
            >
              <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
              <span class="fe-grid__tile--svg" v-html="fileIconTile(it)"></span>
            </ThumbTile>
            <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
            <span v-else-if="isEncryptedFolder(it)" class="fe-grid__tile--svg" v-html="encryptedFolderTile()"></span>
            <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
            <span v-else class="fe-grid__tile--svg" v-html="fileIconTile(it)"></span>
          </span>
          <bdi class="fe-fpeek__label">{{ nodeDisplayName(it) }}</bdi>
        </li>
      </ul>
      <p v-if="!state.loading && more > 0" class="fe-fpeek__more">{{ t('peek.more', { n: more }) }}</p>
    </div>
  </Teleport>
</template>
