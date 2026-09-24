/**
 * THE COLUMN REORDER ARITHMETIC — every edge of it, on the pure function.
 *
 * ⚠⚠ Why this file exists. The same off-by-one shipped three times over, and
 * each time it was a caller mixing two index spaces: the drawn columns WITH
 * the dragged one in them, and the order WITHOUT it. Measured in a real
 * browser 2026-09-20 against the shipped columns (`type owner modified size
 * ★`):
 *
 *   · Type dragged ONE place right landed THREE places right.
 *   · Type dragged out and dropped back on its own place moved two right.
 *   · Repeating that walked Type PAST the star, so the header read
 *     `… Size ★ Type ⋮` and its label stood over a 24px control column.
 *
 * Owner, verbatim: "bir adım ileri çekince iki adım ilerliyor… ileri çekip,
 * bırakmadan kendi yerine geri getirip bırakınca yine bir adım ileri gidiyor."
 *
 * The contract the whole feature now rests on, in one sentence:
 * **`to` is the index the item ENDS UP AT.**
 *
 * ⚠ Imported from `…/src/lib/viewPrefs` rather than from the package root:
 * the root resolves to the BUILT bundle, so a test written against it measures
 * whatever was last built rather than the source beside it. The drop
 * translation in ListView.vue is measured in a real browser
 * (`e2e/columnReorder`), because the half this file cannot see is the
 * pointer arithmetic that produces the gap index.
 */
import { beforeEach, describe, expect, it } from 'vitest';

import {
  COLUMNS,
  __resetViewPrefs,
  attachViewPrefsStore,
  columnDropBand,
  columnOrder,
  moveColumn,
  moveColumnBy,
  reorderColumns,
  type ColumnId,
} from '@brftech/filex-core/src/lib/viewPrefs';

const settle = () => new Promise<void>((r) => setTimeout(r, 0));

async function attached(initial: unknown = { on: true }) {
  attachViewPrefsStore({ load: async () => initial, save: () => {} });
  await settle();
}

beforeEach(() => {
  __resetViewPrefs();
});

const movableOf = (order: readonly ColumnId[]) =>
  order.filter((id) => COLUMNS.find((c) => c.id === id)?.hideable);

describe('reorderColumns — `to` is where the item ends up', () => {
  const l = ['a', 'b', 'c', 'd', 'e'];

  it('one step forward moves ONE step, not two', () => {
    expect(reorderColumns(l, 1, 2)).toEqual(['a', 'c', 'b', 'd', 'e']);
  });

  it('n steps forward land on n', () => {
    expect(reorderColumns(l, 1, 3)).toEqual(['a', 'c', 'd', 'b', 'e']);
    expect(reorderColumns(l, 0, 4)).toEqual(['b', 'c', 'd', 'e', 'a']);
  });

  it('one step back moves ONE step', () => {
    expect(reorderColumns(l, 2, 1)).toEqual(['a', 'c', 'b', 'd', 'e']);
  });

  it('n steps back land on n', () => {
    expect(reorderColumns(l, 3, 1)).toEqual(['a', 'd', 'b', 'c', 'e']);
    expect(reorderColumns(l, 4, 0)).toEqual(['e', 'a', 'b', 'c', 'd']);
  });

  it('its own place changes NOTHING — first, last and middle alike', () => {
    for (let i = 0; i < l.length; i++) expect(reorderColumns(l, i, i)).toEqual(l);
  });

  it('the ends clamp rather than dropping the item', () => {
    expect(reorderColumns(l, 2, -5)).toEqual(['c', 'a', 'b', 'd', 'e']);
    expect(reorderColumns(l, 2, 99)).toEqual(['a', 'b', 'd', 'e', 'c']);
  });

  it('an index off the list leaves it alone, and the input is never mutated', () => {
    const copy = [...l];
    expect(reorderColumns(l, -1, 2)).toEqual(copy);
    expect(reorderColumns(l, 9, 2)).toEqual(copy);
    expect(l).toEqual(copy);
    expect(reorderColumns(l, 1, 2)).not.toBe(l);
  });

  it('round-trips: every move, undone by its mirror, is the identity', () => {
    for (let from = 0; from < l.length; from++) {
      for (let to = 0; to < l.length; to++) {
        expect(reorderColumns(reorderColumns(l, from, to), to, from)).toEqual(l);
      }
    }
  });

  it('the item really is AT `to` afterwards — the contract, checked directly', () => {
    for (let from = 0; from < l.length; from++) {
      for (let to = 0; to < l.length; to++) {
        expect(reorderColumns(l, from, to).indexOf(l[from])).toBe(to);
      }
    }
  });
});

describe('columnDropBand — the pinned columns cannot be passed', () => {
  it('opens at the first movable column and closes at the last', () => {
    const order = columnOrder();
    const band = columnDropBand(order);
    expect(order[band.lo]).toBe('type');
    expect(order[band.hi]).toBe('size');
    expect(order[0]).toBe('name');
    expect(order[order.length - 1]).toBe('star');
  });

  it('a list of nothing but pinned columns leaves no room at all', () => {
    expect(columnDropBand(['name', 'star'] as ColumnId[])).toEqual({ lo: 2, hi: 2 });
  });
});

describe('moveColumn — the stored order', () => {
  it('a drop past the end stops BEFORE the star', async () => {
    // The browser bug: Type walked to the end of the drawn columns and came to
    // rest after the star, a 24px control, taking its header label with it.
    await attached();
    moveColumn('type', 99);
    const order = columnOrder();
    expect(order[order.length - 1]).toBe('star');
    expect(order.indexOf('type')).toBe(order.length - 2);
  });

  it('a drop before the front stops AFTER Name', async () => {
    await attached();
    moveColumn('size', -3);
    const order = columnOrder();
    expect(order[0]).toBe('name');
    expect(order[1]).toBe('size');
  });

  it('refuses to move a pinned column at all', async () => {
    await attached();
    const before = columnOrder().join();
    moveColumn('star', 1);
    moveColumn('name', 3);
    expect(columnOrder().join()).toBe(before);
  });

  it('moving a column to where it already is writes nothing', async () => {
    await attached();
    const before = columnOrder().join();
    const at = columnOrder().indexOf('owner');
    moveColumn('owner', at);
    expect(columnOrder().join()).toBe(before);
  });

  it('one step each way, and the ends hold', async () => {
    await attached();
    const movable = movableOf(columnOrder());
    const [first] = movable;
    const last = movable[movable.length - 1];

    moveColumnBy(first, 1);
    expect(movableOf(columnOrder())[1]).toBe(first);
    moveColumnBy(first, -1);
    expect(movableOf(columnOrder())[0]).toBe(first);

    moveColumnBy(first, -1);
    expect(movableOf(columnOrder())[0]).toBe(first);
    moveColumnBy(last, 1);
    expect(movableOf(columnOrder()).at(-1)).toBe(last);
  });

  it('a hidden column keeps its place in the sequence when a visible one moves over it', async () => {
    // ⚠ The reason the drop is translated through the NEIGHBOUR's identity and
    // not through arithmetic on the drawn columns: `location` is off, but it
    // still holds a place, and moving Size across it must not reshuffle it.
    await attached({ on: true, c: { hidden: ['location'] } });
    const before = columnOrder();
    const locAt = before.indexOf('location');
    expect(locAt).toBeGreaterThan(0);
    moveColumn('size', before.indexOf('owner'));
    const after = columnOrder();
    expect(after.filter((c) => c !== 'size')).toEqual(before.filter((c) => c !== 'size'));
    expect(after.indexOf('size')).toBe(before.indexOf('owner'));
  });
});
