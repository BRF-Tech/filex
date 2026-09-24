/**
 * RTL — which of the explorer's own glyphs MEAN a direction.
 *
 * lib/actionIcons marks them (`fe-aicon--dir`) and one base.css rule mirrors
 * them under `:dir(rtl)`. The set is small on purpose: an arrow along the
 * line turns, "open in a new tab" (up and out), refresh (a clock) and the
 * vertical arrows do not.
 */
import { describe, expect, it } from 'vitest';

import { actionIconSvg } from '@brftech/filex-core/src/lib/actionIcons';

describe('directional action icons', () => {
  it('back, go-into, sign-out, the path separator and the side panels are marked', () => {
    for (const k of ['restore', 'goto', 'sign-out', 'copy-path', 'nav', 'inspector']) {
      expect(actionIconSvg(k), k).toContain('class="fe-aicon fe-aicon--dir"');
    }
  });

  it('open-in-a-new-tab, refresh and the vertical arrows are not', () => {
    for (const k of ['open', 'open-tab', 'refresh', 'upload', 'download', 'go-up', 'subfolders', 'convert', 'close']) {
      expect(actionIconSvg(k), k).toContain('class="fe-aicon"');
      expect(actionIconSvg(k), k).not.toContain('fe-aicon--dir');
    }
  });
});
