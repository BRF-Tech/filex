/**
 * The explorer's ONE toast slot: whose line may replace whose, and what a
 * toast's action says once it is pressed.
 *
 * ⚠ The slot was built for Undo — `runToastAction` said "Undoing…", then
 * "Undone", and read the listing again — and #68 put a drag-out's "Stop"
 * into it: Stop said "Undone". A toast now says which kind its action is.
 *
 * ⚠ And the desktop app reports a drop being filled in once per file
 * written. Every report repainted the slot, so an 8 s Undo, a refusal,
 * anything else on screen vanished within milliseconds while a drop was
 * being filled in. A toast now says who put it up (`source`), and a drag
 * report takes the slot only when it is empty or holds the drag's own line.
 */
import { sayUndo, type UndoOutcome } from './undoWords';

/** Who put a toast up, where it matters: the drag-out's progress line, and
 *  its "Stopping…" after Stop was pressed. Anything else leaves it unset. */
export type ToastSource = 'drag-out' | 'drag-out-stopping';

/**
 * What pressing a toast's action is.
 *   - `undo` (the default): "Undoing…", then what the undo did (lib/undoWords),
 *     and the listing is read again;
 *   - `plain`: its own words while it runs and once it has, nothing read again.
 */
export type ToastAfter = { kind: 'undo' } | { kind: 'plain'; running: string; done: string };

/**
 * May a report from `source` take the slot from what is on it?
 *
 * `hold` is a line that stays until the next report (the drag's progress),
 * `end` the last word (done, stopped, failed). Only an empty slot or the
 * source's own line is taken; a "Stopping…" is ended by the end, never
 * repainted by progress that was already on its way.
 */
export function slotTakenBy(
  current: { source?: ToastSource } | null,
  source: 'drag-out',
  report: 'hold' | 'end',
): boolean {
  if (!current) return true;
  if (current.source === source) return true;
  if (current.source === 'drag-out-stopping') return report === 'end';
  return false;
}

/** What a toast says while its pressed action runs, and once it has. */
export function afterActionWords(
  after: ToastAfter | undefined,
  phase: 'running' | 'done',
  outcome: UndoOutcome,
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  if (after?.kind === 'plain') return phase === 'running' ? after.running : after.done;
  return phase === 'running' ? t('toast.undoing') : sayUndo(outcome, t);
}
