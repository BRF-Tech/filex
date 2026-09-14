// The ONE notification-click resolver.
//
// A notification is only useful if it takes you to the thing it is about, and
// there are three places a click can start: the bell row, a browser
// `Notification`, and the desktop app's native OS notification. Before this
// module each of those would have had to work out "where does this go" for
// itself, which is three implementations of one rule and therefore three
// chances for them to disagree — the bell opening a folder while the toast for
// the same row opened the notifications page.
//
// So the rule lives here, once, and every surface calls it:
//
//   backend notification row ──► resolveNotificationTarget() ──► destination
//                                                               │
//     bell click ─────────────────────────────────────────────► │
//     browser Notification click ─────────────────────────────► │
//     desktop native notification click ──────────────────────► ┘
//
// ⚠⚠ This file is imported by the DESKTOP MAIN PROCESS as well as by the web
// bundle (desktop/src/main.ts → `../../web/src/lib/notificationTarget`), which
// is what makes "one resolver" true rather than aspirational. Keep it free of
// imports, of Vue, of `window` and of anything else that only exists in a
// browser: an import added here is an import added to an Electron main process
// that has no DOM.

/** What a notification is about. Mirrors `model.NotificationTargetKind` (Go). */
export type NotificationTargetKind = 'file' | 'dir' | 'share' | 'none';

/**
 * The typed target the backend puts on every notification row and every
 * webhook body (`docs/NOTIFICATIONS.md` → "Click target").
 *
 * `storage` is the storage NAME — the explorer addresses storages by name and
 * a numeric id is unreadable to a caller who cannot list them. `path` is
 * relative to that storage's root and never carries a `<storage>://` prefix.
 */
export interface NotificationTarget {
  kind: NotificationTargetKind;
  storage?: string;
  path?: string;
  id?: string;
}

/**
 * Where a click actually goes. Deliberately NOT a URL: the desktop app has no
 * URLs to navigate — it hands the explorer component a path — so the shared
 * answer is the structured destination and each surface renders it its own way
 * (`notificationRoute` below for the web, IPC for the desktop).
 */
export type NotificationDestination =
  | {
      kind: 'folder';
      /** Storage name — the first segment of every explorer path. */
      storage: string;
      /** Folder inside that storage, relative, '' for its root. */
      folder: string;
      /**
       * The row to select once the folder is open, as `<storage>://<path>` —
       * the exact string the explorer puts in `data-fe-path`. Absent when the
       * target is the folder itself.
       */
      select?: string;
    }
  | { kind: 'share'; token: string }
  | { kind: 'none' };

/** Strip a leading slash and any `<storage>://` prefix; normalise separators. */
function cleanPath(p: string | undefined | null): string {
  let out = String(p ?? '').trim().replace(/\\/g, '/');
  const at = out.indexOf('://');
  if (at >= 0) out = out.slice(at + 3);
  return out.replace(/^\/+/, '').replace(/\/+$/, '');
}

/** The folder part of a path — '' when the path is a name at the root. */
function parentOf(p: string): string {
  const i = p.lastIndexOf('/');
  return i < 0 ? '' : p.slice(0, i);
}

/**
 * Resolve a notification's target into the place a click should land.
 *
 * ⚠ A file target resolves to its FOLDER plus a selection, never to the file
 * as a path of its own. Opening "the file" would mean guessing whether to
 * preview it, edit it or download it — three different answers for three file
 * types — where showing it in its folder is the one answer that is right for
 * every type, and is also what "reveal" means in every file manager.
 *
 * ⚠ Anything incomplete resolves to `none`, never to a partial address: a file
 * target with no storage would otherwise open whichever storage the user
 * happened to have in front of them, which is worse than not moving at all.
 */
export function resolveNotificationTarget(
  target: NotificationTarget | null | undefined,
): NotificationDestination {
  if (!target || !target.kind || target.kind === 'none') return { kind: 'none' };

  if (target.kind === 'share') {
    const token = String(target.id ?? '').trim();
    return token ? { kind: 'share', token } : { kind: 'none' };
  }

  const storage = String(target.storage ?? '').trim();
  if (!storage) return { kind: 'none' };
  const path = cleanPath(target.path);

  if (target.kind === 'dir') return { kind: 'folder', storage, folder: path };
  if (target.kind === 'file') {
    if (!path) return { kind: 'folder', storage, folder: '' };
    return {
      kind: 'folder',
      storage,
      folder: parentOf(path),
      select: `${storage}://${path}`,
    };
  }
  return { kind: 'none' };
}

/**
 * The explorer's own path form for a destination — what goes in the address
 * bar hash, e.g. `qldemo/Documents` (and just `qldemo` for a storage root).
 *
 * ⚠ Measured, not assumed: the explorer writes `#<storage>/<relative path>`
 * while its rows carry `data-fe-path="<storage>://<relative path>"`. Two
 * different shapes of the same location, and mixing them up produces a hash
 * the explorer reads as a folder literally named `qldemo:`.
 */
export function explorerHashPath(dest: NotificationDestination): string {
  if (dest.kind !== 'folder') return '';
  return dest.folder ? `${dest.storage}/${dest.folder}` : dest.storage;
}

/** A route the web SPA can push. `null` when the destination is not in-SPA. */
export function notificationRoute(
  dest: NotificationDestination,
): { name: string; query: Record<string, string>; hash: string } | null {
  if (dest.kind === 'folder') {
    return {
      name: 'explore',
      query: dest.select ? { select: dest.select } : {},
      // vue-router wants the '#'; the explorer encodes segment-by-segment, so
      // we hand over the readable form and let the browser escape it.
      hash: `#${explorerHashPath(dest)}`,
    };
  }
  if (dest.kind === 'none') return { name: 'notifications', query: {}, hash: '' };
  return null; // a share link is a public page outside the SPA
}

/** The public URL of a share token. Outside the SPA — a real navigation. */
export function shareHref(token: string): string {
  return `/s/${encodeURIComponent(token)}`;
}
