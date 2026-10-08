// "Undo" says what it did: undone, on its way, or - for the undo of a delete,
// which is one restore request - the server's own sentence for it.
//
// ⚠ Pressing Undo showed nothing while it ran, and then said "Undone" about an
// undo of a move that had only been QUEUED (the move back runs as a job,
// minutes later on an object store), and about a restore that had brought
// back two of five items. 0.54 (finding A15): the "{done} of {total}" this
// client composed is gone; the server says what came back and why the rest
// did not.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { sayUndo } from '@brftech/filex-core/src/lib/undoWords';

const words: Record<string, string> = {
  'toast.undone': 'UNDONE',
  'toast.undo_queued': 'UNDO QUEUED',
};
const t = (k: string, vars?: Record<string, string | number>) =>
  Object.entries(vars ?? {}).reduce((s, [a, b]) => s.replace(`{${a}}`, String(b)), words[k] ?? k);

describe('what an undo says', () => {
  it('an undo that ran to its end is undone', () => {
    expect(sayUndo(undefined, t)).toBe('UNDONE');
    expect(sayUndo({}, t)).toBe('UNDONE');
  });

  it('an undo that went into the queue is not undone yet', () => {
    expect(sayUndo({ queued: true }, t)).toBe('UNDO QUEUED');
  });

  it("an undo the server answered is said in the server's words, as they are", () => {
    const said = '2 items restored - 1 item was not restored: something already has the name “a.txt”';
    expect(sayUndo({ summary: said }, t)).toBe(said);
  });
});

describe('the explorer', () => {
  const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
  const run = src.slice(src.indexOf('async function runToastAction'), src.indexOf('// Data loading'));

  // The words themselves are lib/toastSlot `afterActionWords` ("Undoing…",
  // then sayUndo — tests/lib/toastSlot.test.ts); a plain action (a drag-out's
  // Stop) says its own.
  it('says it is undoing while the undo runs', () => {
    const running = run.indexOf("afterActionWords(after, 'running', undefined, t)");
    expect(running).toBeGreaterThan(-1);
    expect(running).toBeLessThan(run.indexOf('await act()'));
  });

  it('says what the undo did', () => {
    expect(run.indexOf("afterActionWords(after, 'done', out, t)")).toBeGreaterThan(run.indexOf('await act()'));
  });

  it("the undo of a delete is one restore request and hands back the server's sentence", () => {
    const del = src.slice(src.indexOf('async function confirmDelete'), src.indexOf('const ticket = deleteReq.begin();', src.indexOf('async function confirmDelete')));
    expect(del).toMatch(/await api\.restoreBatch\(nodeIds\)/);
    expect(del).toMatch(/return \{ summary: said\.summary \}/);
    expect(src).not.toMatch(/restoreIds/);
  });

  it('a queued move back reports that it was queued', () => {
    const reg = src.slice(src.indexOf('function registerMoveUndo'), src.indexOf('watch(\n  () => searchQuery.value'));
    expect(reg).toContain('queued: true');
  });
});
