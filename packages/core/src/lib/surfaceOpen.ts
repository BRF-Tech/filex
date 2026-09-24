/**
 * surfaceOpen — "go to this file, and start that screen on it".
 *
 * ⚠⚠ v3 §3.0. A surface runs on the file it was opened on, which meant an
 * app's HOME screen could list documents and then do nothing when a row was
 * clicked: there was nowhere for a screen to send anybody. `Surface.open`
 * is that missing edge — the answer names a path and, optionally, one of the
 * plugin's own screens, and the client goes there.
 *
 * ⚠ It is NOT a second navigation design. The address is the one a
 * notification deep link already uses (`?select=<path>&app=<plugin>` plus
 * `appAction=` or `appView=`, with the folder in the hash), because the two
 * are the same request — "open this file with that screen on it" — and a
 * second spelling of it would be a second thing to keep working.
 *
 * ⚠⚠ Trusted on arrival, and that is the SERVER's promise, not an omission
 * here: the host checks the screen belongs to the plugin that answered, and
 * the handler re-checks the path against the ASKING person's permissions,
 * dropping the `open` rather than refusing the screen. So a client that
 * re-derived its own permission check would be writing a second, weaker
 * copy of a rule that has already been applied.
 *
 * ⚠ A public page never gets one — there is no explorer behind it — so a
 * frame with no navigator simply ignores it (see `openHrefFor` returning '').
 */
import type { SurfaceOpenRequest } from '../types/Plugins';
import { isPagePlacement, pluginPageUrl } from './pluginPage';

/* ⚠ The shape itself lives in `types/Plugins`, with every other wire
 * mirror. Declaring it here as well would be a second copy of a struct that
 * only has one definition in `wire.go`. */
export type { SurfaceOpenRequest };

/** What the answer amounts to, once the frame knows where it is mounted. */
export interface SurfaceOpenTarget {
  href: string;
  /**
   * A `page` view is a whole screen with the document beside it and opens in
   * its own tab — the same rule a `page` ACTION follows when it is clicked
   * in the menu, so a plugin's screen behaves the same however it was
   * reached.
   */
  newTab: boolean;
}

/** Is this a request the client can act on at all? */
export function isOpenRequest(v: unknown): v is SurfaceOpenRequest {
  if (!v || typeof v !== 'object') return false;
  const o = v as Record<string, unknown>;
  return typeof o.path === 'string' && o.path.trim() !== '';
}

/**
 * The folder part of an adapter-qualified path, as the explorer's hash wants
 * it (`storage/dir/sub`), and the file itself for `?select=`.
 */
export function openHashFor(qualified: string): string {
  const at = String(qualified ?? '').indexOf('://');
  if (at <= 0) return '';
  const storage = qualified.slice(0, at);
  const rel = qualified.slice(at + 3).replace(/^\/+/, '');
  const cut = rel.lastIndexOf('/');
  const dir = cut > 0 ? rel.slice(0, cut) : '';
  return dir ? `${storage}/${dir}` : storage;
}

export interface SurfaceOpenOptions {
  /** The plugin that answered — the screen named is one of ITS own. */
  plugin: string;
  /** The prefix the SPA is served from (`/admin/`, `/drive/`). */
  base?: string | null;
  /**
   * What placement the named view has, when the frame knows. `page` opens in
   * a tab on the view's own address; anything else (and an unknown one) goes
   * through the explorer, which is the frame that can open a dialog.
   */
  placementOf?: (id: string) => string | undefined;
}

/**
 * Where this answer sends the person.
 *
 * `''` when there is nowhere to go: no path, or a frame with no explorer
 * behind it. The caller draws its screen and does nothing, which is what a
 * public page must do.
 */
export function openTargetFor(
  req: SurfaceOpenRequest | null | undefined,
  opts: SurfaceOpenOptions,
): SurfaceOpenTarget {
  if (!isOpenRequest(req) || !opts.plugin) return { href: '', newTab: false };
  const base = String(opts.base ?? '').replace(/\/+$/, '');

  // A `page` view is its own address, with the document beside it.
  if (req.view && isPagePlacement(opts.placementOf?.(req.view))) {
    return { href: pluginPageUrl(base, { plugin: opts.plugin, view: req.view, path: req.path }), newTab: true };
  }

  const q: string[] = [`select=${encodeURIComponent(req.path)}`, `app=${encodeURIComponent(opts.plugin)}`];
  // ⚠ `action` OR `view`, never both — the contract says naming both is not
  // a request, and sending both would leave the explorer to pick one.
  if (req.action) q.push(`appAction=${encodeURIComponent(req.action)}`);
  else if (req.view) q.push(`appView=${encodeURIComponent(req.view)}`);
  const hash = openHashFor(req.path);
  return { href: `${base}/explore?${q.join('&')}${hash ? `#${hash}` : ''}`, newTab: false };
}
