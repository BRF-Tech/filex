/**
 * pluginPage — where an app plugin's `page` view lives, and how it is opened.
 *
 * A `modal` view is a dialog over the listing. A `page` view is the SAME
 * conversation (same `GET …/views/{p}/{v}?path=`, same `POST …/event`) drawn
 * as a whole page in a NEW TAB — for a wizard that needs the document beside
 * it, where a dialog's inner scroll would cramp both. The action row says
 * which (`view_placement`), so nothing about the plugin changes between the
 * two; only the frame does.
 *
 * ⚠⚠ This lives in the package, not in the admin SPA, because the rule is the
 * same everywhere the explorer runs — fm.example.com, the desktop shell, the
 * work.example.com and fishapp embeds. What differs per host is only WHERE the
 * page is served and HOW a new surface is opened, and those are the two
 * things `ExplorerConfig` asks the host for (`pluginPageBase`,
 * `openPluginPage`). A host that wires neither keeps the modal: a `page`
 * action still works, it just opens in a dialog rather than a tab. That is a
 * degraded frame, never a missing feature.
 *
 * ⚠ The address in the contract (`/apps/{plugin}/{view}?path=…`) is relative
 * to the SPA's MOUNT BASE, not to the site root: the same bundle is served
 * from `/admin/` and `/drive/` (backend routes.go → wireStatic), and
 * only those prefixes fall back to index.html. A bare `/apps/…` is a 404 on
 * the server, so the host passes the base it was served from.
 */

/** The path segment under a mount base that a plugin page lives at. */
export const PLUGIN_PAGE_SEGMENT = 'apps';

export interface PluginPageTarget {
  plugin: string;
  view: string;
  /** Adapter-qualified path of the row the action was invoked on. */
  path?: string;
}

/** `apps/sign/wizard?path=docs%3A%2F%2Fnda.pdf` — no leading slash. */
export function pluginPagePath(target: PluginPageTarget): string {
  const p = `${PLUGIN_PAGE_SEGMENT}/${encodeURIComponent(target.plugin)}/${encodeURIComponent(target.view)}`;
  return target.path ? `${p}?path=${encodeURIComponent(target.path)}` : p;
}

/**
 * The full address, given the base the SPA is mounted at (`/admin/`,
 * `/drive/`, `https://fm.example.com/admin/` — with or without its trailing
 * slash). An empty base yields a root-relative `/apps/…`, which is right for
 * a host that serves the SPA at the root and wrong for one that does not —
 * hence the config field rather than a guess.
 */
export function pluginPageUrl(base: string | undefined | null, target: PluginPageTarget): string {
  const b = String(base ?? '').replace(/\/+$/, '');
  return `${b}/${pluginPagePath(target)}`;
}

/** Does this action open its view as a full page rather than a dialog? */
export function isPagePlacement(placement: string | undefined | null): boolean {
  return placement === 'page';
}
