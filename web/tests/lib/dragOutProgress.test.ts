// A folder dragged out of the desktop app says how far its filling-in has got,
// and can be stopped.
//
// ⚠ After the drop the app downloads the folder into the place it was dropped.
// The explorer said "Downloading…" for 2.5 seconds, once per dragged item —
// one folder, one toast — and then nothing until it ended, however many
// minutes and files that was, with no way to stop it.
import { describe, expect, it } from 'vitest';

import { sayDragProgress } from '@brftech/filex-core/src/lib/dragOut';

const words: Record<string, string> = {
  'dragout.not_found': 'NOT_FOUND',
  'dragout.stopped': 'STOPPED',
  'dragout.done': 'DONE',
  'dragout.downloading': 'DOWNLOADING',
  'dragout.downloading_n': 'DOWNLOADING {n}',
  'dragout.preparing': 'PREPARING',
};
const t = (k: string, vars?: Record<string, string | number>) =>
  Object.entries(vars ?? {}).reduce((s, [a, b]) => s.replace(`{${a}}`, String(b)), words[k] ?? k);

const drop = { done: 0, total: 1, dropped: '/Users/ada/Desktop' };

describe('what the explorer says while a drop is filled in', () => {
  it('holds a line with the files so far, which can be stopped', () => {
    expect(sayDragProgress({ ...drop, files: 12 }, { quiet: false, stoppable: true, t })).toEqual({
      kind: 'hold',
      message: 'DOWNLOADING 12',
      stoppable: true,
    });
    expect(sayDragProgress(drop, { quiet: true, stoppable: false, t })).toEqual({
      kind: 'hold',
      message: 'DOWNLOADING',
      stoppable: false,
    });
  });

  it('says the end: done, stopped, or what went wrong', () => {
    expect(sayDragProgress({ ...drop, finished: true }, { quiet: false, stoppable: true, t })).toEqual({ kind: 'flash', message: 'DONE' });
    expect(sayDragProgress({ ...drop, finished: true, error: 'cancelled' }, { quiet: false, stoppable: true, t })).toEqual({
      kind: 'flash',
      message: 'STOPPED',
    });
    expect(sayDragProgress({ ...drop, finished: true, error: 'disk full' }, { quiet: true, stoppable: true, t })).toEqual({
      kind: 'flash',
      message: 'disk full',
    });
  });

  it('keeps the preparation before a drop quiet when asked', () => {
    expect(sayDragProgress({ done: 0, total: 2 }, { quiet: true, stoppable: true, t })).toBeNull();
    expect(sayDragProgress({ done: 0, total: 2 }, { quiet: false, stoppable: true, t })).toEqual({ kind: 'flash', message: 'PREPARING' });
    expect(sayDragProgress({ done: 0, total: 1, error: 'drop_not_found' }, { quiet: true, stoppable: true, t })).toEqual({
      kind: 'flash',
      message: 'NOT_FOUND',
    });
  });
});
