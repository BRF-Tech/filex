// Where a teleported header dropdown goes — one rule for the avatar menu and
// the notification bell (web/src/lib/anchoredPanel.ts).
import { describe, expect, it } from 'vitest';

import { anchorUnderEndEdge, refElement } from '@/lib/anchoredPanel';

describe('anchorUnderEndEdge', () => {
  it('hangs 6px under the button, flush with its right edge', () => {
    const at = anchorUnderEndEdge({ bottom: 47, right: 1373 }, { width: 1440, height: 900 });
    expect(at.top).toBe('53px');
    expect(at.right).toBe('67px');
    expect(at.width).toBeUndefined();
  });

  it('keeps a wide panel on a phone: the bell at 390px', () => {
    // Measured geometry: the bell's right edge is at 327 on a 390px viewport,
    // one avatar-width left of the edge. Flush with it, a 360px panel would
    // start at -20px.
    const at = anchorUnderEndEdge({ bottom: 42, right: 327 }, { width: 390, height: 844 }, { width: 360 });
    const width = parseInt(at.width!, 10);
    const right = parseInt(at.right!, 10);
    const left = 390 - right - width;
    expect(width).toBeLessThanOrEqual(390 - 16);
    expect(left).toBeGreaterThanOrEqual(8);
    expect(right).toBeGreaterThanOrEqual(8);
  });

  // ⚠ RTL — the header's controls sit at the LEFT end, so the panel hangs flush
  // with the button's left edge and the answer is a `left`, never a `right`.
  // The LTR cases above are the same arithmetic measured from the other side.
  it('in a right-to-left header, hangs flush with the button\'s LEFT edge', () => {
    const at = anchorUnderEndEdge({ bottom: 47, left: 67, right: 99 }, { width: 1440, height: 900 }, { dir: 'rtl' });
    expect(at.top).toBe('53px');
    expect(at.left).toBe('67px');
    expect(at.right).toBeUndefined();
  });

  it('in RTL, keeps a wide panel on a phone — the mirror of the 390px case', () => {
    // The bell mirrored: its left edge 63px from the left of a 390px phone.
    const at = anchorUnderEndEdge({ bottom: 42, left: 63, right: 95 }, { width: 390, height: 844 }, { width: 360, dir: 'rtl' });
    const width = parseInt(at.width!, 10);
    const left = parseInt(at.left!, 10);
    const right = 390 - left - width;
    expect(at.right).toBeUndefined();
    expect(width).toBeLessThanOrEqual(390 - 16);
    expect(left).toBeGreaterThanOrEqual(8);
    expect(right).toBeGreaterThanOrEqual(8);
  });

  it('never lets the list run off the bottom', () => {
    const at = anchorUnderEndEdge({ bottom: 42, right: 327 }, { width: 390, height: 500 }, { width: 360, maxHeight: 520 });
    expect(parseInt(at.maxHeight, 10)).toBeLessThanOrEqual(500 - 48 - 8);
  });
});

describe('refElement', () => {
  it('unwraps a component ref to its element, and passes an element through', () => {
    const el = document.createElement('button');
    expect(refElement(el)).toBe(el);
    expect(refElement({ $el: el })).toBe(el);
    expect(refElement(null)).toBeNull();
    expect(refElement({ $el: 'not an element' })).toBeNull();
  });
});
