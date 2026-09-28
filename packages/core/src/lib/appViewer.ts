/**
 * appViewer — which app's own interface opens a file (a `viewer` view with a
 * `ui`), for every surface that opens files: the explorer's preview, "Open
 * with", and the standalone editor tab (/files/edit). ONE rule, so a file
 * opens in the same app wherever it is opened:
 *
 *  - the views are the server's list (`GET /api/files/plugins/actions`),
 *    in its order;
 *  - a view opens a file its `applies` rule matches;
 *  - "Open with" names one (`plugin/view`), or `builtin` for filex's own
 *    viewer; otherwise the first that matches opens it.
 */
import type { PluginViewRow } from '../types/Plugins';
import { appliesItemOf, appliesMatches, type AppliesNodeLike } from './pluginApplies';

/** "Open with" chose filex's own viewer over every app. */
export const BUILTIN_VIEWER = 'builtin';

/** The `viewer` views that are an app's own interface. */
export function viewerViews(views: readonly PluginViewRow[]): PluginViewRow[] {
  return views.filter((v) => v.placement === 'viewer' && !!v.ui);
}

/** The app interfaces that open this file, in the server's order. */
export function appViewersFor(
  views: readonly PluginViewRow[],
  node: (AppliesNodeLike & { type?: string }) | null | undefined,
): PluginViewRow[] {
  if (!node || node.type !== 'file') return [];
  return viewerViews(views).filter((v) => appliesMatches(v.applies ?? {}, [appliesItemOf(node, v.plugin)]));
}

/** The one that opens it: the choice (`plugin/view`, or `builtin`), else the first. */
export function pickAppViewer(
  views: readonly PluginViewRow[],
  node: (AppliesNodeLike & { type?: string }) | null | undefined,
  choice?: string | null,
): PluginViewRow | null {
  const list = appViewersFor(views, node);
  if (choice === BUILTIN_VIEWER) return null;
  if (choice) return list.find((v) => `${v.plugin}/${v.id}` === choice) ?? null;
  return list[0] ?? null;
}
