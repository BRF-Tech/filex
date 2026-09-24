/**
 * pluginApplies — does an action's `applies` rule accept this selection?
 *
 * The client-side mirror of `wasmplugin.Matches` (backend/internal/wasmplugin/
 * manifest.go). The server re-checks on every run; this copy only decides
 * which rows the menu shows, so the two must agree or a row appears that the
 * server then refuses with 422. Keep the rules in the same order as the Go:
 *
 *   1. an empty selection never matches;
 *   2. more than one item needs `multi`;
 *   3. `min` / `max` bound the count when > 0;
 *   4. every item's kind must equal the rule's (`any` accepts both; an absent
 *      kind means `file`);
 *   5. `state` / `no_state` (v2): every item must carry at least one of the
 *      rule's `state` keys and none of its `no_state` keys;
 *   6. when the rule names extensions or mime types, every item must match
 *      at least one of EITHER list — an ext hit is enough, a mime hit is
 *      enough; with both lists empty anything of the right kind passes.
 *
 * ⚠⚠ The state keys a ROW carries are namespaced (`app_state: ["sign:pending"]`)
 * while the keys a RULE names are bare (`state: ["pending"]`) — an action can
 * only ever ask about its OWN plugin's keys. So the mirror needs to be told
 * which plugin is asking (`appliesToNodes(rule, nodes, plugin)`), strips
 * `"<plugin>:"` off the row's keys and compares what is left. Compare the two
 * without stripping and every state rule silently never matches: the menu row
 * simply is not there, which reads as "the app is broken", not as a bug here.
 */
import type { PluginApplies } from '../types/Plugins';

/** What the rule sees of a selected row. */
export interface AppliesItem {
  kind: 'file' | 'dir' | string;
  /** Lower-case, no dot. */
  ext?: string | null;
  mime?: string | null;
  /** BARE state keys the asking plugin keeps on this item (prefix stripped). */
  state?: string[];
}

/** A listing row, as the explorer holds it — only the three fields matter. */
export interface AppliesNodeLike {
  type?: string;
  extension?: string | null;
  mime_type?: string | null;
  basename?: string;
  /** `<plugin>:<key>` as the listing carries it. */
  app_state?: string[];
}

/** The rule's own keys out of a row's namespaced ones. `''` asks about nobody. */
export function stateKeysOf(node: AppliesNodeLike, plugin: string | undefined): string[] {
  if (!plugin || !Array.isArray(node.app_state)) return [];
  const prefix = `${plugin}:`;
  return node.app_state
    .filter((k): k is string => typeof k === 'string' && k.startsWith(prefix))
    .map((k) => k.slice(prefix.length));
}

/** Reduce a listing row to what the rule reads. */
export function appliesItemOf(node: AppliesNodeLike, plugin?: string): AppliesItem {
  let ext = String(node.extension ?? '').toLowerCase();
  if (!ext && node.basename) {
    const dot = node.basename.lastIndexOf('.');
    if (dot > 0) ext = node.basename.slice(dot + 1).toLowerCase();
  }
  return {
    kind: node.type === 'dir' ? 'dir' : 'file',
    ext,
    mime: node.mime_type ?? '',
    state: stateKeysOf(node, plugin),
  };
}

function mimeMatches(pattern: string, mime: string): boolean {
  if (pattern === mime) return true;
  if (pattern.endsWith('/*')) return mime.startsWith(pattern.slice(0, -1));
  return false;
}

/** The rule against a selection already reduced to items. */
export function appliesMatches(rule: PluginApplies | null | undefined, items: AppliesItem[]): boolean {
  const a = rule ?? {};
  const n = items.length;
  if (n === 0) return false;
  if (n > 1 && !a.multi) return false;
  const min = a.min ?? 0;
  const max = a.max ?? 0;
  if (min > 0 && n < min) return false;
  if (max > 0 && n > max) return false;
  const kind = a.kind || 'file';
  const exts = (a.ext ?? []).map((e) => e.toLowerCase());
  const mimes = (a.mime ?? []).map((m) => m.toLowerCase());
  const wantState = a.state ?? [];
  const banState = a.no_state ?? [];
  for (const it of items) {
    if (kind !== 'any' && kind !== it.kind) return false;
    const has = it.state ?? [];
    if (wantState.length > 0 && !wantState.some((k) => has.includes(k))) return false;
    if (banState.some((k) => has.includes(k))) return false;
    if (exts.length === 0 && mimes.length === 0) continue;
    const ext = String(it.ext ?? '').toLowerCase();
    let ok = exts.includes(ext);
    if (!ok) {
      const mime = String(it.mime ?? '').toLowerCase();
      ok = mimes.some((m) => mimeMatches(m, mime));
    }
    if (!ok) return false;
  }
  return true;
}

/**
 * The rule against listing rows. `plugin` is the ASKING action's plugin —
 * without it a `state` / `no_state` rule can never be satisfied, so pass it
 * wherever the action is known.
 */
export function appliesToNodes(
  rule: PluginApplies | null | undefined,
  nodes: AppliesNodeLike[],
  plugin?: string,
): boolean {
  return appliesMatches(rule, nodes.map((n) => appliesItemOf(n, plugin)));
}
