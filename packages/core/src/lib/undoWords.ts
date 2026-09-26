/**
 * What an Undo did, as the undo itself reports it.
 *
 * ⚠ The explorer said "Undone" whenever the undo's promise settled — about an
 * undo of a move that had only been QUEUED (the move back runs as a job,
 * minutes later on an object store), and about a restore that had brought
 * back two of five items. An undo now says which it was: nothing (it ran to
 * its end), `queued` (the operations centre follows it, and says when it
 * ends), or how many of how many it brought back.
 */
export type UndoOutcome = void | { queued?: boolean; done?: number; total?: number };

export function sayUndo(
  out: UndoOutcome,
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  if (out && out.queued) return t('toast.undo_queued');
  if (out && typeof out.done === 'number' && typeof out.total === 'number' && out.done < out.total) {
    return t('toast.undo_partial', { done: out.done, total: out.total });
  }
  return t('toast.undone');
}
