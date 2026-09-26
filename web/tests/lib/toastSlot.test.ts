// The explorer's one toast slot, shared by everything that talks: whose line
// may replace whose, and what a toast's action says once pressed.
//
// ⚠ #68 put a drag-out's "Stop" into the slot built for Undo: pressing it
// said "Undoing…", then "Undone", and read the listing again. And the drag's
// progress — one report per file written — repainted the slot every few
// milliseconds, so an 8 s Undo, a refusal, anything else on screen vanished
// the moment a drop was being filled in.
import { describe, expect, it } from 'vitest';

import { slotTakenBy, afterActionWords } from '@brftech/filex-core/src/lib/toastSlot';
import { wordsIn } from '@brftech/filex-core/src/lib/errorWords';

const en = wordsIn('en');
const tr = wordsIn('tr');

describe('a drag-out report and the toast slot', () => {
  it('takes an empty slot, and repaints its own line', () => {
    expect(slotTakenBy(null, 'drag-out', 'hold')).toBe(true);
    expect(slotTakenBy({ source: 'drag-out' }, 'drag-out', 'hold')).toBe(true);
    expect(slotTakenBy({ source: 'drag-out' }, 'drag-out', 'end')).toBe(true);
  });

  it('never clobbers another toast — an Undo, a refusal, anything', () => {
    expect(slotTakenBy({}, 'drag-out', 'hold'), 'progress over an Undo').toBe(false);
    expect(slotTakenBy({}, 'drag-out', 'end'), 'the end over an Undo').toBe(false);
  });

  it('does not repaint progress over its own "Stopping…", but ends it', () => {
    expect(slotTakenBy({ source: 'drag-out-stopping' }, 'drag-out', 'hold')).toBe(false);
    expect(slotTakenBy({ source: 'drag-out-stopping' }, 'drag-out', 'end')).toBe(true);
  });
});

describe('what a pressed toast action says', () => {
  it('an Undo says "Undoing…" and then what it did', () => {
    expect(afterActionWords(undefined, 'running', undefined, en)).toBe('Undoing…');
    expect(afterActionWords({ kind: 'undo' }, 'done', { queued: true }, en)).toBe(en('toast.undo_queued'));
    expect(afterActionWords({ kind: 'undo' }, 'done', undefined, en)).toBe('Undone');
  });

  it('a plain action says its own words, never "Undone"', () => {
    const stop = { kind: 'plain' as const, running: en('dragout.stopping'), done: en('dragout.stopped') };
    expect(afterActionWords(stop, 'running', undefined, en)).toBe('Stopping…');
    expect(afterActionWords(stop, 'done', undefined, en)).toBe(en('dragout.stopped'));
    expect(afterActionWords(stop, 'done', undefined, en)).not.toMatch(/Undo/);
    expect(tr('dragout.stopping')).toBe('Durduruluyor…');
  });
});
