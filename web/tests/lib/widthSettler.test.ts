// The list view's width, and the loop it could fall into.
//
// ListView lays its table out against the width a ResizeObserver reports for
// its own scroll box, and that box's width depends on whether a vertical
// scrollbar is showing. Where a scrollbar takes room (Windows, or macOS set to
// "always show"), a layout can move its own container: the table lands a pixel
// wider than the pane, a horizontal scrollbar appears, the rows no longer fit,
// a vertical scrollbar appears, the pane is ~17 px narrower, the table is laid
// out again narrower, both scrollbars go, the pane is wide again — every frame.
// Measured in the field on a Windows PC: a 12-row folder in list view froze
// Edge and Opera for ~30 s and ran Chrome out of memory; grid view (no observer)
// opened it at once.
//
// The settler is the observer's filter: a real resize passes, a width that
// flips back to where it was a moment ago settles on the narrower of the two.
import { describe, expect, it } from 'vitest';

import { WIDTH_FLIP_WINDOW_MS, createWidthSettler } from '@brftech/filex-core';

describe('createWidthSettler', () => {
  it('passes the first observation and a real resize through', () => {
    const s = createWidthSettler();
    expect(s.observe(1200, 0)).toBe(1200);
    expect(s.observe(900, 5_000)).toBe(900);
    expect(s.observe(1400, 10_000)).toBe(1400);
  });

  it('ignores sub-pixel jitter', () => {
    const s = createWidthSettler();
    expect(s.observe(1200, 0)).toBe(1200);
    expect(s.observe(1200.4, 16)).toBeNull();
    expect(s.observe(1199.6, 32)).toBeNull();
  });

  it('settles a scrollbar flip on the narrower width and stays there', () => {
    const s = createWidthSettler();
    expect(s.observe(1200, 0)).toBe(1200);
    // The scrollbar appears: this is a change like any other the first time.
    expect(s.observe(1183, 16)).toBe(1183);
    // …and goes again a frame later: A → B → A is the layout moving its own
    // container. Stay on the narrow width, where the table fits either way.
    expect(s.observe(1200, 33)).toBeNull();
    // Every further flip between the two is ignored, however long it goes on.
    for (let t = 50; t < 5_000; t += 16) {
      expect(s.observe(t % 32 < 16 ? 1183 : 1200, t)).toBeNull();
    }
  });

  it('settles on the narrower width when the flip starts from the narrow side', () => {
    const s = createWidthSettler();
    expect(s.observe(1183, 0)).toBe(1183);
    expect(s.observe(1200, 16)).toBe(1200);
    expect(s.observe(1183, 33)).toBe(1183);
    expect(s.observe(1200, 50)).toBeNull();
  });

  it('lets a genuinely new width through after settling', () => {
    const s = createWidthSettler();
    s.observe(1200, 0);
    s.observe(1183, 16);
    s.observe(1200, 33);
    expect(s.observe(960, 2_000)).toBe(960);
    // …and the old pair is no longer pinned: going back is a resize.
    expect(s.observe(1200, 9_000)).toBe(1200);
  });

  it('treats a slow return to a previous width as a resize, not a loop', () => {
    const s = createWidthSettler();
    s.observe(1200, 0);
    s.observe(1183, 1_000);
    expect(s.observe(1200, 1_000 + WIDTH_FLIP_WINDOW_MS + 1)).toBe(1200);
  });
});
