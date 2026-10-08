// Who saves an encrypted co-editing session, and when (task #189).
//
// The server has no key, so a browser saves, and all the browsers in a
// session must agree which one without talking: each folds the same log into
// the same state. The rules under test are the maintainers' (2026-10-06):
// a version every 10 minutes while there are unsaved changes, by the writer
// who joined first; Save saves at once; the last writer out saves; a vault
// edits alone.
import { describe, expect, it } from 'vitest';

import {
  OFFICE_E2E_AUTOSAVE_MS,
  OFFICE_E2E_UNSAVED_KEEP_DAYS,
  electSaver,
  emptySaveState,
  foldSaveState,
  isDirty,
  officeE2eMode,
  shouldSave,
  type OfficeSaveEntry,
  type OfficeSaveState,
} from '../../../packages/core/src/lib/e2eofficeSave';

const T0 = Date.UTC(2026, 9, 8, 9, 0, 0);
const MIN = 60 * 1000;

function fold(entries: OfficeSaveEntry[]): OfficeSaveState {
  return entries.reduce(foldSaveState, emptySaveState());
}

/** a, b (writers) and v (reads along) join; a writes at T0. */
function session(): OfficeSaveEntry[] {
  return [
    { seq: 1, kind: 'join', client: 'a', at: T0 - 5 * MIN, canEdit: true },
    { seq: 2, kind: 'join', client: 'b', at: T0 - 4 * MIN, canEdit: true },
    { seq: 3, kind: 'join', client: 'v', at: T0 - 3 * MIN, canEdit: false },
    { seq: 4, kind: 'lock', client: 'b', at: T0 - 2 * MIN },
    { seq: 5, kind: 'changes', client: 'b', at: T0 },
    { seq: 6, kind: 'changes', client: 'b', at: T0 + 3 * MIN },
  ];
}

describe('the rules as numbers', () => {
  it('a version every 10 minutes; unsaved changes kept 30 days', () => {
    expect(OFFICE_E2E_AUTOSAVE_MS).toBe(10 * MIN);
    expect(OFFICE_E2E_UNSAVED_KEEP_DAYS).toBe(30);
  });
});

describe('foldSaveState', () => {
  it('counts unsaved changes from the first change after the last save', () => {
    const s = fold(session());
    expect(isDirty(s)).toBe(true);
    expect(s.changesHead).toBe(6);
    expect(s.dirtySince).toBe(T0);
  });

  it('a lock is not a change', () => {
    const s = fold(session().slice(0, 4));
    expect(isDirty(s)).toBe(false);
    expect(s.dirtySince).toBeNull();
  });

  it('a save of everything makes it clean; a save of part restarts the clock for the rest', () => {
    const all = fold([...session(), { seq: 7, kind: 'saved', client: 'a', at: T0 + 4 * MIN, through: 6 }]);
    expect(isDirty(all)).toBe(false);
    expect(all.dirtySince).toBeNull();
    const part = fold([...session(), { seq: 7, kind: 'saved', client: 'a', at: T0 + 4 * MIN, through: 5 }]);
    expect(isDirty(part)).toBe(true);
    expect(part.dirtySince).toBe(T0 + 4 * MIN);
  });

  it('a save never moves the saved point back', () => {
    const s = fold([
      ...session(),
      { seq: 7, kind: 'saved', client: 'a', at: T0 + 4 * MIN, through: 6 },
      { seq: 8, kind: 'saved', client: 'b', at: T0 + 5 * MIN, through: 5 },
    ]);
    expect(s.savedThrough).toBe(6);
    expect(isDirty(s)).toBe(false);
  });

  it('the same log gives the same state in every browser', () => {
    expect(fold(session())).toEqual(fold(session()));
  });
});

describe('electSaver', () => {
  it('is the writer online who joined first', () => {
    const s = fold(session());
    expect(electSaver(s.members)).toBe('a');
    const aLeft = fold([...session(), { seq: 7, kind: 'leave', client: 'a', at: T0 + 4 * MIN }]);
    expect(electSaver(aLeft.members)).toBe('b');
  });

  it('is never a member that only reads, and nobody when no writer is online', () => {
    const s = fold([
      { seq: 1, kind: 'join', client: 'v', at: T0, canEdit: false },
      { seq: 2, kind: 'join', client: 'w', at: T0, canEdit: true },
    ]);
    expect(electSaver(s.members)).toBe('w');
    expect(electSaver(s.members.filter((m) => m.client === 'v'))).toBeNull();
  });
});

describe('shouldSave', () => {
  it('on the clock: the saver, once the oldest unsaved change is 10 minutes old', () => {
    const s = fold(session());
    expect(shouldSave(s, 'a', T0 + 9 * MIN, 'tick')).toBe(false);
    expect(shouldSave(s, 'a', T0 + 10 * MIN, 'tick')).toBe(true);
    // Not b: it wrote the changes, but a is the saver.
    expect(shouldSave(s, 'b', T0 + 10 * MIN, 'tick')).toBe(false);
    expect(shouldSave(s, 'v', T0 + 10 * MIN, 'tick')).toBe(false);
  });

  it('Save: whoever pressed it, if it may write and something is unsaved', () => {
    const s = fold(session());
    expect(shouldSave(s, 'b', T0, 'button')).toBe(true);
    expect(shouldSave(s, 'v', T0, 'button')).toBe(false);
    const clean = fold(session().slice(0, 4));
    expect(shouldSave(clean, 'a', T0, 'button')).toBe(false);
  });

  it('leaving: the last writer out saves, nobody else', () => {
    const s = fold(session());
    expect(shouldSave(s, 'a', T0, 'leave')).toBe(false);
    const aLeft = fold([...session(), { seq: 7, kind: 'leave', client: 'a', at: T0 + MIN }]);
    // Only the reader stays: b is the last writer.
    expect(shouldSave(aLeft, 'b', T0 + MIN, 'leave')).toBe(true);
  });

  it('nothing unsaved, nothing to do', () => {
    const s = fold([...session(), { seq: 7, kind: 'saved', client: 'a', at: T0 + MIN, through: 6 }]);
    for (const why of ['tick', 'leave', 'button'] as const) {
      expect(shouldSave(s, 'a', T0 + 60 * MIN, why)).toBe(false);
    }
  });
});

describe('officeE2eMode', () => {
  it('a vault edits alone; an encrypted folder with others', () => {
    expect(officeE2eMode({ vault: true })).toBe('solo');
    expect(officeE2eMode({ vault: false })).toBe('shared');
  });
});
