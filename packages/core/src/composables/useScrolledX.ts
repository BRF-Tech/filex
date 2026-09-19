import { ref } from 'vue';

/**
 * "Is this box scrolled sideways at all?" — the one fact a pinned column
 * needs in order to draw its left divider only while something is actually
 * sliding underneath it. A permanent divider would claim the column is
 * floating when it is simply the last cell of a table that fits.
 *
 * Bind `onScroll` to the scroll container and `scrolledX` to its
 * `is-scrolled-x` class; the stylesheet does the rest (base.css,
 * `.fe-s3keys__scroll.is-scrolled-x`).
 */
export function useScrolledX() {
  const scrolledX = ref(false);
  function onScroll(ev: Event) {
    const el = ev.currentTarget as HTMLElement | null;
    const on = (el?.scrollLeft ?? 0) > 0;
    if (on !== scrolledX.value) scrolledX.value = on;
  }
  return { scrolledX, onScroll };
}
