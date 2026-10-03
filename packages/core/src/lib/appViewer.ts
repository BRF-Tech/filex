/**
 * appViewer - which handler opens a file, for every surface that opens files:
 * the explorer's preview, "Open with", and the standalone editor tab
 * (/files/edit). ONE rule, so a file opens in the same place wherever it is
 * opened (0.50: docs/APP-PLUGINS.md → Default apps):
 *
 *  - the handlers of a file are the app interfaces that open it (`viewer`
 *    views with a `ui`, in the server's order: by app name, then the app's
 *    own order) and filex's own viewer, `builtin`, last;
 *  - the administrator's rule for the file's kind (`open_rules[ext]`, from
 *    the server, backend internal/assoc) puts the handlers it names first, in
 *    its order, and leaves out the ones it switched off; the rest follow in
 *    the default order - exactly assoc.Apply;
 *  - the one that opens it: what "Open with" chose, else the person's own
 *    "always open with" choice (lib/openWith), else the first that is on -
 *    each only when it is on. With every handler off, filex's own viewer
 *    opens it: a file must open somewhere.
 */
import type { OpenRule, PluginViewRow } from '../types/Plugins';
import { appliesItemOf, appliesMatches, type AppliesNodeLike } from './pluginApplies';

/** filex's own viewer, as a handler id (and "Open with"'s choice of it). */
export const BUILTIN_VIEWER = 'builtin';

type NodeLike = (AppliesNodeLike & { type?: string }) | null | undefined;

/** One handler that opens a file: an app's interface, or filex's own (view null). */
export interface OpenHandler {
  id: string;
  view: PluginViewRow | null;
}

/** The handler id of an app's interface. */
export function openHandlerId(v: Pick<PluginViewRow, 'plugin' | 'id'>): string {
  return `app:${v.plugin}/${v.id}`;
}

/**
 * A choice as a handler id. Accepts the spelling before 0.50 too
 * (`plugin/view`, what "Open in new tab" put in `app=`).
 */
export function normalizeOpenChoice(choice: string | null | undefined): string | null {
  if (!choice) return null;
  if (choice === BUILTIN_VIEWER || choice.startsWith('app:')) return choice;
  return `app:${choice}`;
}

/** A file's kind: its extension, lower-case, no dot ('' for none). */
export function openKindOf(node: NodeLike): string {
  if (!node) return '';
  return String(appliesItemOf(node).ext ?? '').toLowerCase();
}

/** The `viewer` views that are an app's own interface. */
export function viewerViews(views: readonly PluginViewRow[]): PluginViewRow[] {
  return views.filter((v) => v.placement === 'viewer' && !!v.ui);
}

/** The app interfaces that open this file, in the server's order. */
export function appViewersFor(views: readonly PluginViewRow[], node: NodeLike): PluginViewRow[] {
  if (!node || node.type !== 'file') return [];
  return viewerViews(views).filter((v) => appliesMatches(v.applies ?? {}, [appliesItemOf(node, v.plugin)]));
}

/** What the administrator's rule makes of a file's handlers. */
export interface OpenHandlers {
  /** The handlers that are on, in the order they are offered. */
  on: OpenHandler[];
  /** The ones switched off for the file's kind. */
  off: OpenHandler[];
  /** An administrator's rule decided the order. */
  custom: boolean;
}

/** The handlers of a file, ordered and switched off by the rule for its kind. */
export function openHandlersFor(
  views: readonly PluginViewRow[],
  node: NodeLike,
  rules?: Readonly<Record<string, OpenRule>> | null,
): OpenHandlers {
  if (!node || node.type !== 'file') return { on: [], off: [], custom: false };
  const avail: OpenHandler[] = [
    ...appViewersFor(views, node).map((v) => ({ id: openHandlerId(v), view: v })),
    { id: BUILTIN_VIEWER, view: null },
  ];
  const rule = rules?.[openKindOf(node)];
  if (!rule) return { on: avail, off: [], custom: false };
  const isOff = new Set(rule.off ?? []);
  const byId = new Map(avail.map((h) => [h.id, h]));
  const placed = new Set<string>();
  const on: OpenHandler[] = [];
  for (const id of rule.order ?? []) {
    const h = byId.get(id);
    if (!h || isOff.has(id) || placed.has(id)) continue;
    placed.add(id);
    on.push(h);
  }
  const off: OpenHandler[] = [];
  for (const h of avail) {
    if (isOff.has(h.id)) off.push(h);
    else if (!placed.has(h.id)) on.push(h);
  }
  return { on, off, custom: true };
}

/** The handler that opens the file (see the header for the rule). */
export function pickOpenHandler(
  views: readonly PluginViewRow[],
  node: NodeLike,
  choice?: string | null,
  rules?: Readonly<Record<string, OpenRule>> | null,
  personal?: string | null,
): OpenHandler | null {
  if (!node || node.type !== 'file') return null;
  const { on } = openHandlersFor(views, node, rules);
  const find = (id: string | null) => (id ? (on.find((h) => h.id === id) ?? null) : null);
  return find(normalizeOpenChoice(choice)) ?? find(normalizeOpenChoice(personal)) ?? on[0] ?? null;
}

/** The app interface that opens the file; null is filex's own viewer. */
export function pickAppViewer(
  views: readonly PluginViewRow[],
  node: NodeLike,
  choice?: string | null,
  rules?: Readonly<Record<string, OpenRule>> | null,
  personal?: string | null,
): PluginViewRow | null {
  return pickOpenHandler(views, node, choice, rules, personal)?.view ?? null;
}
