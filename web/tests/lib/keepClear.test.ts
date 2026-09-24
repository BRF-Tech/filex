// keepClear — the install chip never stands on a control (2026-09-21: it hid
// the signing page's final "İmzala" button at 1366×768).
import { describe, expect, it } from 'vitest';
import { LIFT_GAP, placeChip, type Box, type Hit } from '@/lib/keepClear';

const VH = 768;
// The chip in its corner: 44px tall, 12px off the bottom edge.
const home: Box = { top: VH - 12 - 44, bottom: VH - 12, left: 1000, right: 1354 };

/** A page described as boxes of things to press; `pinned` ones are carried
 *  by a bar whose top edge is `barTop`. */
function page(controls: Array<Box & { barTop?: number }>) {
  return (b: Box): Hit[] =>
    controls
      .filter((c) => c.bottom > b.top && c.top < b.bottom && c.left < b.right && c.right > b.left)
      .map((c) => ({ pinnedTop: c.barTop ?? null }));
}

describe('placeChip', () => {
  it('stays in its corner when nothing to press is under it', () => {
    expect(placeChip(home, page([]), VH)).toEqual({ lift: 0, aside: false });
  });

  it('stands on top of a pinned bar of actions — the signing page footer', () => {
    // The footer bar spans the bottom 64px; "İmzala" sits in it, under the chip.
    const barTop = VH - 64;
    const imzala = { top: VH - 52, bottom: VH - 16, left: 1250, right: 1340, barTop };
    const got = placeChip(home, page([imzala]), VH);
    expect(got.aside).toBe(false);
    // The chip's bottom edge ends LIFT_GAP above the bar.
    expect(home.bottom - got.lift).toBe(barTop - LIFT_GAP);
  });

  it('steps aside for a button that scrolls with the page instead of chasing it', () => {
    const inFlow = { top: VH - 40, bottom: VH - 8, left: 1200, right: 1300 };
    expect(placeChip(home, page([inFlow]), VH)).toEqual({ lift: 0, aside: true });
  });

  it('steps aside when standing on the bar would put it on another control', () => {
    const barTop = VH - 64;
    const imzala = { top: VH - 52, bottom: VH - 16, left: 1250, right: 1340, barTop };
    // A link just above the bar, exactly where the lifted chip would stand.
    const above = { top: barTop - 40, bottom: barTop - 10, left: 1100, right: 1200 };
    expect(placeChip(home, page([imzala, above]), VH)).toEqual({ lift: 0, aside: true });
  });

  it('steps aside on a phone, where standing on the bar is standing on the page', () => {
    const phone: Box = { top: 844 - 12 - 44, bottom: 844 - 12, left: 12, right: 378 };
    const barTop = 844 - 64;
    const imzala = { top: 844 - 52, bottom: 844 - 16, left: 290, right: 375, barTop };
    expect(placeChip(phone, page([imzala]), 844, 390)).toEqual({ lift: 0, aside: true });
    // The same chip on a laptop is a corner thing and stands on the bar.
    expect(placeChip(home, page([{ ...imzala, top: VH - 52, bottom: VH - 16, left: 1250, right: 1340, barTop: VH - 64 }]), VH, 1366).aside).toBe(false);
  });

  it('steps aside rather than climb into the middle of the screen', () => {
    // A pinned panel of controls half the viewport tall.
    const barTop = VH / 2;
    const tall = { top: barTop, bottom: VH, left: 900, right: 1366, barTop };
    expect(placeChip(home, page([tall]), VH)).toEqual({ lift: 0, aside: true });
  });
});
