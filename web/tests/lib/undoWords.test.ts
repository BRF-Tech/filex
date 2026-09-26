// "Undo" says what it did: undone, on its way, or undone in part.
//
// ⚠ Pressing Undo showed nothing while it ran, and then said "Undone" about an
// undo of a move that had only been QUEUED (the move back runs as a job,
// minutes later on an object store), and about a restore that had brought
// back two of five items.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { sayUndo } from '@brftech/filex-core/src/lib/undoWords';

const words: Record<string, string> = {
  'toast.undone': 'UNDONE',
  'toast.undo_queued': 'UNDO QUEUED',
  'toast.undo_partial': 'UNDONE {done} OF {total}',
};
const t = (k: string, vars?: Record<string, string | number>) =>
  Object.entries(vars ?? {}).reduce((s, [a, b]) => s.replace(`{${a}}`, String(b)), words[k] ?? k);

describe('what an undo says', () => {
  it('an undo that ran to its end is undone', () => {
    expect(sayUndo(undefined, t)).toBe('UNDONE');
    expect(sayUndo({ done: 3, total: 3 }, t)).toBe('UNDONE');
  });

  it('an undo that went into the queue is not undone yet', () => {
    expect(sayUndo({ queued: true }, t)).toBe('UNDO QUEUED');
  });

  it('an undo that brought back part says how much', () => {
    expect(sayUndo({ done: 2, total: 5 }, t)).toBe('UNDONE 2 OF 5');
  });
});

describe('the explorer', () => {
  const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
  const run = src.slice(src.indexOf('async function runToastAction'), src.indexOf('// Data loading'));

  it('says it is undoing while the undo runs', () => {
    expect(run.indexOf("t('toast.undoing')")).toBeGreaterThan(-1);
    expect(run.indexOf("t('toast.undoing')")).toBeLessThan(run.indexOf('await act()'));
  });

  it('says what the undo did', () => {
    expect(run).toContain('sayUndo(');
  });

  it('a queued move back reports that it was queued', () => {
    const reg = src.slice(src.indexOf('function registerMoveUndo'), src.indexOf('watch(\n  () => searchQuery.value'));
    expect(reg).toContain('queued: true');
  });
});
