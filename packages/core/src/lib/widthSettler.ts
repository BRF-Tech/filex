/**
 * createWidthSettler — the filter between ListView's ResizeObserver and the
 * width its table is laid out against.
 *
 * ⚠⚠ Why a filter at all. The table's layout decides whether its scroll box
 * shows scrollbars, and the box's width depends on whether a vertical one is
 * showing. Where a scrollbar takes room (Windows, or macOS set to "always
 * show") a layout can move its own container: the table lands a pixel wider
 * than the pane, a horizontal scrollbar appears, the rows no longer fit, a
 * vertical scrollbar appears, the pane is ~17 px narrower, the table is laid
 * out narrower, both scrollbars go, the pane is wide again — every frame, for
 * as long as the folder is open. Measured in the field: a 12-row folder in list
 * view froze Edge and Opera for ~30 s and ran a Chrome tab out of memory on a
 * Windows PC, while grid view (which has no observer) opened it at once.
 *
 * `scrollbar-gutter: stable` on `.fe-list` removes the known cause; this is the
 * guard for the ones nobody has measured yet (a horizontal-only flip, rounding
 * at a fractional device pixel ratio). A width that returns to the value it had
 * one observation ago, within WIDTH_FLIP_WINDOW_MS, is not a resize — it is the
 * layout answering itself. The settler then stays on the NARROWER of the two,
 * where the table fits whichever way the scrollbars go, and ignores further
 * flips between those two widths until a genuinely different one arrives.
 */

/** How quickly a width has to come back to count as a flip, not a resize. A
 *  loop turns over once a frame (~16 ms); a person dragging a window back to
 *  the exact same pixel takes far longer than this. */
export const WIDTH_FLIP_WINDOW_MS = 500;

export interface WidthSettler {
  /**
   * Report a width the observer measured at `now` (ms, any monotonic clock).
   * Returns the width to lay the table out against, or null to keep the
   * current one.
   */
  observe(width: number, now: number): number | null;
}

export function createWidthSettler(): WidthSettler {
  /** What the table is laid out against; 0 until the first observation. */
  let current = 0;
  /** The width `current` replaced, and when it was replaced. */
  let previous: { width: number; at: number } | null = null;
  /** The two widths of a flip once one has been seen. */
  let pinned: [number, number] | null = null;

  const same = (a: number, b: number) => Math.abs(a - b) < 1;

  return {
    observe(width, now) {
      if (same(width, current)) return null;
      if (pinned && (same(width, pinned[0]) || same(width, pinned[1]))) return null;
      if (previous && same(width, previous.width) && now - previous.at <= WIDTH_FLIP_WINDOW_MS) {
        pinned = [width, current];
        previous = null;
        const narrow = Math.min(width, current);
        if (same(narrow, current)) return null;
        current = narrow;
        return narrow;
      }
      pinned = null;
      previous = current > 0 ? { width: current, at: now } : null;
      current = width;
      return width;
    },
  };
}
