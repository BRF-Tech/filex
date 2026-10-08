// #196 - an open menu's rows hold their places (lib/heldMenuRows).
//
// The 0.53 release run (e2e 115, Firefox): the right-click menu of a file
// opened before the server had answered "may this file be encrypted?"
// (POST /api/files/e2e/allowed); when the answer landed 101 ms later
// "Encrypt with E2EE…" was drawn below "Download" and every row under it moved
// one place down - between the press and the release of a click on "Tags",
// which then landed on "Star". These tests pin the rule that replaced it: once
// open, a row that was drawn stays where it was drawn, a row that goes away
// keeps its place greyed, and a row that comes late is added at the end.
// The component half is web/tests/ui/contextMenuHoldsRows.test.ts; the browser
// half is e2e/tests/213-menu-rows-stay.spec.ts.
import { describe, expect, it } from 'vitest';

import {
  LATE_ROWS_DIVIDER_KEY,
  heldMenuRows,
  visibleMenuRows,
  type MenuRowLike,
} from '../../../packages/core/src/lib/heldMenuRows';

/** A file's menu, the way the explorer lists it: the encryption row hidden
 *  until the server answers. */
function fileMenu(encrypt: 'pending' | 'allowed'): MenuRowLike[] {
  return [
    { key: 'open', label: 'Open' },
    { key: 'download', label: 'Download' },
    { key: 'fxe-encrypt', label: 'Encrypt with E2EE…', hidden: encrypt === 'pending' },
    { key: 'details', label: 'Details' },
    { divider: true, key: 'sep1', label: '' },
    { key: 'rename', label: 'Rename' },
    { divider: true, key: 'sep-meta', label: '' },
    { key: 'star', label: 'Star' },
    { key: 'tags', label: 'Tags…' },
    { divider: true, key: 'sep2', label: '' },
    { key: 'delete', label: 'Delete' },
  ];
}

const keys = (rows: MenuRowLike[]) => rows.map((r) => r.key);

describe('visibleMenuRows', () => {
  it('drops hidden rows and the dividers they would leave leading, doubled or trailing', () => {
    const rows = visibleMenuRows([
      { divider: true, key: 'lead', label: '' },
      { key: 'a', label: 'A' },
      { key: 'gone', label: 'Gone', hidden: true },
      { divider: true, key: 's1', label: '' },
      { divider: true, key: 's2', label: '' },
      { key: 'b', label: 'B' },
      { divider: true, key: 'trail', label: '' },
      { key: 'c', label: 'C', hidden: true },
    ]);
    expect(keys(rows)).toEqual(['a', 's1', 'b']);
  });
});

describe('heldMenuRows - the rows of a menu that is open', () => {
  it('an answer that lands after the menu opened adds its row at the END: no row above it moves', () => {
    const opened = visibleMenuRows(fileMenu('pending'));
    const held = heldMenuRows(opened, fileMenu('allowed'));
    // Every row that was on screen is still at the same index...
    expect(keys(held).slice(0, opened.length)).toEqual(keys(opened));
    // ...and the late row comes after them, behind a divider of its own.
    expect(keys(held).slice(opened.length)).toEqual([LATE_ROWS_DIVIDER_KEY, 'fxe-encrypt']);
    // Tags is where the pointer was aimed, and it is still there.
    expect(keys(held).indexOf('tags')).toBe(keys(opened).indexOf('tags'));
  });

  it('a row that is no longer offered keeps its place, greyed, and is never picked from under the pointer', () => {
    const opened = visibleMenuRows(fileMenu('allowed'));
    const held = heldMenuRows(opened, fileMenu('pending'));
    expect(keys(held)).toEqual(keys(opened));
    expect(held.find((r) => r.key === 'fxe-encrypt')?.disabled).toBe(true);
    // The rows that are still offered are not greyed by it.
    expect(held.filter((r) => !r.divider && r.key !== 'fxe-encrypt').every((r) => !r.disabled)).toBe(true);
  });

  it('a row that is still offered is drawn as it is now: a new label or a new grey changes in place', () => {
    const opened = visibleMenuRows(fileMenu('pending'));
    const now = fileMenu('pending').map((r) => {
      if (r.key === 'star') return { ...r, label: 'Unstar' };
      if (r.key === 'rename') return { ...r, disabled: true };
      return r;
    });
    const held = heldMenuRows(opened, now);
    expect(keys(held)).toEqual(keys(opened));
    expect(held.find((r) => r.key === 'star')?.label).toBe('Unstar');
    expect(held.find((r) => r.key === 'rename')?.disabled).toBe(true);
  });

  it('nothing changed: the same rows, nothing added', () => {
    const opened = visibleMenuRows(fileMenu('allowed'));
    expect(keys(heldMenuRows(opened, fileMenu('allowed')))).toEqual(keys(opened));
  });

  it('a row that goes and comes back (the listing was read again, the answer asked anew) returns to its own place, not added twice', () => {
    const opened = visibleMenuRows(fileMenu('allowed'));
    const gone = heldMenuRows(opened, fileMenu('pending'));
    expect(gone.find((r) => r.key === 'fxe-encrypt')?.disabled).toBe(true);
    const back = heldMenuRows(opened, fileMenu('allowed'));
    expect(keys(back)).toEqual(keys(opened));
    expect(keys(back).filter((k) => k === 'fxe-encrypt')).toHaveLength(1);
    expect(back.find((r) => r.key === 'fxe-encrypt')?.disabled).toBeFalsy();
  });
});
