/**
 * surfaceConditions — `show_when` / `required_when`, as pure data.
 *
 * v3 §2.1: "a form may not present a contradiction". A field may declare
 * that it exists only while another field holds a particular answer ("the
 * name of the new file" appears only when the output IS a new file, and is
 * then required), and the renderer — not the plugin — is what makes that
 * true. The plugin cannot opt out of it and cannot get it half right.
 *
 * Three promises, all of them enforced here rather than in a component so
 * the modal, the page, the public shell and the tests share one answer:
 *
 *   1. a field whose `show_when` does not hold is NOT drawn;
 *   2. its value is dropped before the event is posted — it cannot arrive
 *      at the plugin as a surprise, which is the whole point;
 *   3. a field whose `required_when` holds behaves as `required`, so an
 *      empty one blocks the submit instead of failing on the server.
 *
 * ⚠ Conditions CASCADE. A field can be shown by a field that is itself
 * hidden, and a hidden controller answers nothing — so visibility is
 * resolved to a fixed point rather than in one pass. Without it, hiding the
 * middle of a three-field chain leaves the last one on screen asking about
 * an answer nobody can see.
 *
 * ⚠ This is a CONVENIENCE for the person at the screen, not a security
 * boundary — the host re-checks both rules when the job is submitted
 * (v3 §2.1). Same relationship `applies` has with the server's re-check.
 */
import type { PluginCondition, PluginField, SurfaceNode } from '../types/Plugins';
import { walkNodes } from './surfaceValues';

/** Is there anything in this value at all? `false` and `0` count as answers. */
export function hasAnswer(v: unknown): boolean {
  if (v === undefined || v === null) return false;
  if (typeof v === 'string') return v.trim() !== '';
  if (Array.isArray(v)) return v.length > 0;
  return true;
}

/** Every string a value holds — one for a scalar, several for a multi-select. */
function valueStrings(v: unknown): string[] {
  if (v === undefined || v === null) return [];
  if (Array.isArray(v)) return v.map((x) => String(x));
  if (typeof v === 'boolean') return [v ? 'true' : 'false'];
  return [String(v)];
}

/**
 * Does `cond` hold against the surface's values?
 *
 * ⚠ An absent condition HOLDS. `show_when: null` is "always", which is what
 * every field without one means, so the two paths do not diverge.
 */
export function conditionMet(cond: PluginCondition | null | undefined, values: Record<string, unknown>): boolean {
  if (!cond || !cond.key) return true;
  const raw = values?.[cond.key];
  const wanted = cond.equals ?? [];
  // No list of values: the condition is "that field has been answered".
  if (!wanted.length) return hasAnswer(raw);
  if (!hasAnswer(raw)) return false;
  const have = valueStrings(raw);
  return wanted.some((w) => have.includes(String(w)));
}

/** The fields of one form that the person may see, conditions cascaded. */
export function visibleFields(
  fields: PluginField[] | undefined,
  values: Record<string, unknown>,
): PluginField[] {
  const all = (fields ?? []).filter((f) => f && f.key);
  if (!all.length) return [];

  // Fixed point: a field hidden in this pass stops answering for the next
  // one, which can hide the field that depended on it, and so on. Bounded
  // by the field count — a cycle simply settles.
  let shown = all;
  for (let pass = 0; pass <= all.length; pass++) {
    const scope = scopedValues(all, shown, values);
    const next = all.filter((f) => conditionMet(f.show_when, scope));
    if (next.length === shown.length) return next;
    shown = next;
  }
  return shown;
}

/** The values as the VISIBLE fields answer them — a hidden field answers nothing. */
function scopedValues(
  all: PluginField[],
  shown: PluginField[],
  values: Record<string, unknown>,
): Record<string, unknown> {
  const visible = new Set(shown.map((f) => f.key));
  const out: Record<string, unknown> = { ...values };
  for (const f of all) if (!visible.has(f.key)) delete out[f.key];
  return out;
}

/** Is this field required right now — declared, or by a `required_when` that holds? */
export function fieldRequired(f: PluginField, values: Record<string, unknown>): boolean {
  if (f.required === true) return true;
  if (!f.required_when) return false;
  return conditionMet(f.required_when, values);
}

/** Every `form` node's fields in a surface, flat. */
export function formFields(nodes: SurfaceNode[] | undefined): PluginField[] {
  const out: PluginField[] = [];
  walkNodes(nodes, (n) => {
    if (n.type !== 'form') return;
    const fields = (n.props as { fields?: PluginField[] } | undefined)?.fields;
    for (const f of fields ?? []) if (f && f.key) out.push(f);
  });
  return out;
}

/** The keys a surface is currently NOT showing (across every form on it). */
export function hiddenKeys(nodes: SurfaceNode[] | undefined, values: Record<string, unknown>): string[] {
  const all = formFields(nodes);
  if (!all.length) return [];
  const shown = new Set(visibleFields(all, values).map((f) => f.key));
  return all.filter((f) => !shown.has(f.key)).map((f) => f.key);
}

/**
 * The values to POST: everything the surface holds, minus what a hidden
 * field was carrying.
 *
 * ⚠ Dropped, not blanked. `{name: ""}` is an answer ("call it nothing");
 * the contract says the value "is dropped before the job runs", so the key
 * must not be in the map at all.
 */
export function stripHiddenValues(
  nodes: SurfaceNode[] | undefined,
  values: Record<string, unknown>,
): Record<string, unknown> {
  const hidden = hiddenKeys(nodes, values);
  if (!hidden.length) return { ...values };
  const out = { ...values };
  for (const k of hidden) delete out[k];
  return out;
}

/**
 * The visible-and-required fields a person has not answered. Empty means the
 * submit may go; anything else is what to point at.
 */
export function missingRequired(
  nodes: SurfaceNode[] | undefined,
  values: Record<string, unknown>,
): PluginField[] {
  const all = formFields(nodes);
  if (!all.length) return [];
  const shown = visibleFields(all, values);
  const scope = scopedValues(all, shown, values);
  return shown.filter((f) => fieldRequired(f, scope) && !hasAnswer(scope[f.key]));
}
