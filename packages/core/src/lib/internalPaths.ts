/**
 * internalPaths.ts — the client's ONE definition of the directories filex keeps
 * for itself inside a storage: the trash bin, version history, the legacy
 * thumbnail cache, and the desktop app's "open with filex" working area, plus
 * the empty-folder marker file.
 *
 * ⚠⚠ The server is the real gate. Since this release it never lists, searches
 * or serves these to a person (backend/internal/syspath — the list the whole
 * backend shares), and a notification never names or targets one. This copy
 * exists for what only the client can do: refuse to NAVIGATE into one (a URL
 * hash typed by hand, a tab or persisted path left over from before — the
 * owner's report of 2026-09-21 began with a notification click that landed in
 * `.filex-open`), and keep an embed that talks to an older server from
 * listing them.
 *
 * ⚠ web/tests/lib/internalPaths.test.ts parses backend/internal/syspath/
 * syspath.go and fails when this list and that one differ. Add a name there;
 * the test tells you it belongs here too. This file used to be three
 * hand-written checks in lib/listing.ts, one of them `path.includes('.thumbs')`
 * — which also hid a person's own `my.thumbs.txt`.
 */

/** The internal directory names, in syspath.go's order. */
export const INTERNAL_DIR_NAMES = ['.filex-trash', '.versions', '.thumbs', '.filex-open'] as const;

/** The zero-byte file that keeps an empty folder alive on a blob store. */
export const KEEP_MARKER_NAME = '.keepdir';

const DIRS: ReadonlySet<string> = new Set<string>(INTERNAL_DIR_NAMES);

/** The storage-relative segments of a path in any of the shapes the explorer
 *  carries: `docs://a/b`, `docs/a/b` (the hash form — its first segment is the
 *  storage, which is never an internal name), `/a/b/`, `a\b`. */
function segments(path: string): string[] {
  let p = String(path ?? '').trim().replace(/\\/g, '/');
  const at = p.indexOf('://');
  if (at >= 0) p = p.slice(at + 3);
  return p.split('/').filter((s) => s && s !== '.');
}

/** True for one entry name that is filex's own — a directory or the marker. */
export function isInternalName(name: string): boolean {
  const n = String(name ?? '').trim();
  return DIRS.has(n) || n === KEEP_MARKER_NAME;
}

/**
 * True when a person must never be shown `path`: it is one of filex's
 * directories or lies inside one (at any depth, which is the server's rule
 * too), or it is a keep marker.
 */
export function isInternalPath(path: string): boolean {
  const segs = segments(path);
  if (segs.some((s) => DIRS.has(s))) return true;
  return segs.length > 0 && segs[segs.length - 1] === KEEP_MARKER_NAME;
}

/**
 * The address a listing request should go to: `wire` itself, or — when it
 * points into one of filex's directories — that storage's root.
 *
 * "Resolved rather than refused": a person who arrives at `docs://.filex-open`
 * (by a stale notification link, an old tab, a typed hash) is standing in a
 * folder that is not theirs to see, and the useful place to put them is the
 * storage it belongs to — not a "folder not found" page that prints the very
 * path it is refusing to show.
 *
 * ⚠ Only the explorer's listing calls go through here. The desktop app's
 * open-with flow lists its working area by exact path over its own HTTP
 * client (desktop/src/openwith-io.ts), and the server keeps answering that.
 */
export function listingAddress(wire: string): string {
  const w = String(wire ?? '');
  if (!isInternalPath(w)) return w;
  const at = w.indexOf('://');
  return at >= 0 ? w.slice(0, at + 3) : '';
}
