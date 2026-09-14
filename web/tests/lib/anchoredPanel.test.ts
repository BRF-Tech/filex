// Where a teleported header dropdown goes — one rule for the avatar menu and
// the notification bell (web/src/lib/anchoredPanel.ts).
import { describe, expect, it } from 'vitest';

import { anchorUnderRightEdge, refElement } from '@/lib/anchoredPanel';

describe('anchorUnderRightEdge', () => {
  it('hangs 6px under the button, flush with its right edge', () => {
    const at = anchorUnderRightEdge({ bottom: 47, right: 1373 }, { width: 1440, height: 900 });
    expect(at.top).toBe('53px');
    expect(at.right).toBe('67px');
    expect(at.width).toBeUndefined();
  });

  it('keeps a wide panel on a phone: the bell at 390px', () => {
    // Measured geometry: the bell's right edge is at 327 on a 390px viewport,
    // one avatar-width left of the edge. Flush with it, a 360px panel would
    // start at -20px.
    const at = anchorUnderRightEdge({ bottom: 42, right: 327 }, { width: 390, height: 844 }, { width: 360 });
    const width = parseInt(at.width!, 10);
    const right = parseInt(at.right, 10);
    const left = 390 - right - width;
    expect(width).toBeLessThanOrEqual(390 - 16);
    expect(left).toBeGreaterThanOrEqual(8);
    expect(right).toBeGreaterThanOrEqual(8);
  });

  it('never lets the list run off the bottom', () => {
    const at = anchorUnderRightEdge({ bottom: 42, right: 327 }, { width: 390, height: 500 }, { width: 360, maxHeight: 520 });
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
