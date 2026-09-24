/**
 * surfaceSteps — "one step asks one thing", as pure data.
 *
 * v3 §2 rule 3: a multi-step surface has at most ONE primary button plus
 * Back. The complaint behind it: a wizard step with eight fields and four
 * buttons, where nothing on the screen said which button was the way
 * forward — so the person picked one and found out afterwards.
 *
 * ⚠ Enforced by the RENDERER, not by each plugin. A plugin that marks three
 * of its actions `primary: true` gets one primary (the first one it
 * declared) and two ordinary buttons — it is not refused, and it does not
 * get to draw three ways forward. Nothing is hidden: a button the plugin
 * declared is a button the person can press, because silently dropping one
 * would break a wizard in a way no test of ours would ever see.
 *
 * ⚠ "Back" is recognised by its ACTION ID, which is the only signal on the
 * wire. `back`, `prev`, `previous` and the `step_back` / `go-back` spellings
 * of them, case- and separator-insensitive. A step that calls its own
 * backward action something else simply gets an ordinary button, which is
 * what it would have had anyway.
 */
import type { SurfaceAction, SurfaceNode } from '../types/Plugins';
import { walkNodes } from './surfaceValues';

/** Does this surface have a `steps` node — i.e. is it a step of a walk? */
export function hasSteps(nodes: SurfaceNode[] | undefined): boolean {
  let found = false;
  walkNodes(nodes, (n) => {
    if (n.type === 'steps') found = true;
  });
  return found;
}

const BACK_IDS = new Set(['back', 'prev', 'previous', 'stepback', 'goback', 'prevstep', 'previousstep']);

/** Is this the step's backward action? */
export function isBackAction(a: Pick<SurfaceAction, 'id'>): boolean {
  const id = String(a?.id ?? '')
    .toLowerCase()
    .replace(/[^a-z]/g, '');
  return BACK_IDS.has(id);
}

/** A footer, arranged: Back on the left, the ordinary buttons, one primary last. */
export interface StepFooter {
  back: SurfaceAction | null;
  /** In declaration order, minus Back and minus the one primary. */
  others: SurfaceAction[];
  primary: SurfaceAction | null;
}

/**
 * Arrange a surface's actions for a step.
 *
 * `stepped` is what `hasSteps` said. On a surface that is NOT a step the
 * plugin's own arrangement is left alone — a dialog with two equally weighted
 * actions is a legitimate screen, and the rule is about walks.
 */
export function stepFooter(actions: SurfaceAction[] | undefined, stepped: boolean): StepFooter {
  const list = (actions ?? []).filter((a) => a && a.id);
  if (!stepped) {
    return { back: null, others: list, primary: null };
  }
  const back = list.find(isBackAction) ?? null;
  const rest = list.filter((a) => a !== back);
  const primary = rest.find((a) => a.primary === true) ?? null;
  return { back, others: rest.filter((a) => a !== primary), primary };
}
