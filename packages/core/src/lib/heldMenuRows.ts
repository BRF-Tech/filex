// heldMenuRows: the rows an open menu draws, held in their places (#196).
//
// A menu's rows can change while it is open: an answer from the server lands
// and shows a row that was waiting on it ("Encrypt with E2EE…", a folder's
// permissions), the listing is read again and the same answer is asked anew,
// the desktop shell reports a different keep state. Drawn as they are, each of
// those moves every row below the change while the person is aiming at one:
// a click pressed on "Tags" was released on "Star" in the 0.53 release run, and
// the row under the pointer can as well be "Delete".
//
// So once a menu is open (ContextMenu `show()`), the rows it opened with keep
// their places until it closes:
//   - a row that is still offered is drawn as it is now (a label or a grey that
//     changes, changes in place);
//   - a row that is no longer offered keeps its place, greyed - never picked,
//     never pulled from under the pointer;
//   - a row that appears late is added at the END, after a divider of its own,
//     where it moves nothing.
// The next opening draws the menu in its own order again.
export interface MenuRowLike {
  key: string;
  label: string;
  hidden?: boolean;
  disabled?: boolean;
  divider?: boolean;
}

/** The divider in front of the rows that appeared after the menu opened. */
export const LATE_ROWS_DIVIDER_KEY = '__fe-ctx-late';

/** The rows on screen: hidden rows dropped, and the dividers that would be
 *  left leading, trailing or doubled by them collapsed, so every divider drawn
 *  separates two groups. */
export function visibleMenuRows<T extends MenuRowLike>(actions: readonly T[]): T[] {
  const out: T[] = [];
  for (const a of actions) {
    if (a.hidden) continue;
    if (a.divider && (out.length === 0 || out[out.length - 1].divider)) continue;
    out.push(a);
  }
  while (out.length > 0 && out[out.length - 1].divider) out.pop();
  return out;
}

/**
 * The rows of a menu that is open: `opened` (what `visibleMenuRows` drew when
 * it opened) in its own places, each row as `actions` has it now, and what
 * `actions` offers beyond it at the end.
 */
export function heldMenuRows<T extends MenuRowLike>(opened: readonly T[], actions: readonly T[]): T[] {
  const now = new Map<string, T>();
  for (const a of visibleMenuRows(actions)) {
    if (!a.divider && !now.has(a.key)) now.set(a.key, a);
  }
  const placed = new Set<string>();
  const rows: T[] = opened.map((a) => {
    if (a.divider) return a;
    placed.add(a.key);
    return now.get(a.key) ?? { ...a, disabled: true };
  });
  const late = [...now.values()].filter((a) => !placed.has(a.key));
  if (!late.length) return rows;
  const divider = { key: LATE_ROWS_DIVIDER_KEY, label: '', divider: true } as unknown as T;
  return [...rows, divider, ...late];
}
