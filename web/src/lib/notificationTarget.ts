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

/**
 * What a notification is about. Mirrors `model.NotificationTargetKind` (Go).
 *
 * `trash` is the Trash view with the item that was deleted from `path` (its
 * ORIGINAL path, in `storage`) selected — what a soft delete and an antivirus
 * quarantine address. ⚠⚠ It exists because the only address those events used
 * to carry was the item's key inside the bin: `{kind: 'file', path:
 * '.filex-trash/<key>'}`, which opened the bin's raw folder — breadcrumb
 * `docs › .filex-trash`, nothing listed, nothing to restore (owner's report,
 * 2026-09-21). An older client that does not know this kind resolves it to
 * `none` below, which is honest: the row reads, and goes nowhere.
 *
 * `app` is one of an app plugin's HOME pages (`open.plugin` + `open.view`,
 * optionally `open.section`) — a notice about a list rather than a file.
 */
export type NotificationTargetKind = 'file' | 'dir' | 'share' | 'trash' | 'app' | 'none';

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
  /**
   * An app plugin asked for one of ITS OWN screens to be opened on the file
   * (`notify_send target.action|view`, docs/APP-PLUGINS-API.md → v2). The
   * server validates that the named action/view belongs to the named plugin
   * before storing this, so a client may act on it without re-checking.
   *
   * ⚠ This is the difference between "a file you have nothing to do with
   * changed" and "please sign this": a `plugin.notice` that lands you in a
   * folder has made you find the screen yourself, which for a signer is
   * where the flow stops.
   */
  open?: { plugin: string; action?: string; view?: string; section?: string };
}

/**
 * Where a click actually goes. Deliberately NOT a URL: the desktop app has no
 * URLs to navigate — it hands the explorer component a path — so the shared
 * answer is the structured destination and each surface renders it its own way
 * (`notificationRoute` below for the web, IPC for the desktop).
 */
/** What an app wants opened once the file is on screen. */
export interface NotificationOpen {
  plugin: string;
  action?: string;
  view?: string;
}

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
      /**
       * An app screen to open once the row is revealed. ⚠ It rides on the
       * DESTINATION rather than being resolved separately so the two can never
       * disagree about which file they mean — the row that is selected is the
       * row the app screen runs on.
       */
      open?: NotificationOpen;
    }
  | {
      /**
       * The Trash view (the explorer's `.trash` sentinel), spanning every
       * storage. `select` is the row to select there, as `<storage>://<path>`
       * with the ORIGINAL path — the view lists items by where they came from.
       * Absent when the event could not say which item it was.
       */
      kind: 'trash';
      select?: string;
    }
  | { kind: 'share'; token: string }
  /**
   * An app's HOME page (`kind: "app"` — a notice about a list, not a file),
   * optionally at one of its sections. The web opens its own page for it
   * (the `app-home` route); a surface with no such page (the desktop app)
   * brings its window forward and stops.
   */
  | { kind: 'app'; plugin: string; view: string; section?: string }
  | { kind: 'none' };

/** The explorer's address for the Trash view (lib/listing.ts VIRTUAL_SEGMENTS). */
export const TRASH_VIEW_PATH = '.trash';

/**
 * Two ways of spelling one row's `data-fe-path`: `docs://Documents/a.txt` and
 * `docs:///Documents/a.txt`. The Trash view builds its rows as
 * `<storage>://<original path>`, and the original path is stored with a
 * leading slash — so a reveal that compared strings exactly found nothing to
 * select there (measured 2026-09-21: trash rows read `docs:///Documents/…`).
 * Every surface that looks a row up by path compares through this.
 */
export function sameRowPath(a: string | null | undefined, b: string | null | undefined): boolean {
  const norm = (p: string | null | undefined) => {
    const s = String(p ?? '');
    const at = s.indexOf('://');
    return at < 0 ? s : `${s.slice(0, at + 3)}${s.slice(at + 3).replace(/^\/+/, '')}`;
  };
  return norm(a) === norm(b);
}

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

  if (target.kind === 'app') {
    const plugin = String(target.open?.plugin ?? '').trim();
    const view = String(target.open?.view ?? '').trim();
    const section = String(target.open?.section ?? '').trim();
    if (!plugin || !view) return { kind: 'none' };
    return { kind: 'app', plugin, view, ...(section ? { section } : {}) };
  }

  const storage = String(target.storage ?? '').trim();
  const path = cleanPath(target.path);

  // The Trash view is a destination on its own — it spans every storage — so
  // a trash target with no storage still goes there; it just selects nothing.
  if (target.kind === 'trash') {
    return storage && path ? { kind: 'trash', select: `${storage}://${path}` } : { kind: 'trash' };
  }

  if (!storage) return { kind: 'none' };

  const open = openOf(target);

  if (target.kind === 'dir') return { kind: 'folder', storage, folder: path, ...(open ? { open } : {}) };
  if (target.kind === 'file') {
    // ⚠ A file target whose path is only the storage root names no file, so
    // there is no row to select — but the app screen the notice asked for is
    // still what it asked for, and dropping it here turns "please sign this"
    // into "here is a folder". The backend downgrades this shape to `dir`
    // before storing it (notify.resolveTarget), which is why nobody has seen
    // it; a hand-built target, an older row or a new emitter would.
    if (!path) return { kind: 'folder', storage, folder: '', ...(open ? { open } : {}) };
    return {
      kind: 'folder',
      storage,
      folder: parentOf(path),
      select: `${storage}://${path}`,
      ...(open ? { open } : {}),
    };
  }
  return { kind: 'none' };
}

/**
 * The app screen a target asks for, or `undefined`.
 *
 * ⚠ A plugin name with NEITHER an action nor a view is dropped: "open the
 * sign app" with nothing to open is not half an instruction, it is none, and
 * carrying it would make a row look actionable that has nothing to do.
 */
function openOf(target: NotificationTarget): NotificationOpen | undefined {
  const o = target.open;
  if (!o || typeof o !== 'object') return undefined;
  const plugin = String(o.plugin ?? '').trim();
  const action = String(o.action ?? '').trim();
  const view = String(o.view ?? '').trim();
  if (!plugin || (!action && !view)) return undefined;
  return { plugin, ...(action ? { action } : {}), ...(view ? { view } : {}) };
}

/**
 * Is there anywhere for a click on this row to GO?
 *
 * ⚠⚠ The answer used to be "yes, always": a row with no target took the
 * person to the notifications page, which is the page they were most likely
 * already looking at — a click that shuffles the view and answers nothing.
 * Worse, that page is admin-gated, so for everybody else the same click threw
 * away the folder they were standing in (the guard bounced them) and the row
 * was STILL not about anything. A row that cannot go anywhere now takes no
 * cursor, no hover and no click; reading it is the whole interaction.
 */
export function isNotificationClickable(target: NotificationTarget | null | undefined): boolean {
  return resolveNotificationTarget(target).kind !== 'none';
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
  if (dest.kind === 'trash') return TRASH_VIEW_PATH;
  if (dest.kind !== 'folder') return '';
  return dest.folder ? `${dest.storage}/${dest.folder}` : dest.storage;
}

/**
 * The inverse of `explorerHashPath`: `#qldemo/Documents` → `qldemo://Documents`.
 *
 * ⚠⚠ It lives HERE, beside its inverse, because the two path shapes are the
 * documented trap of this module: the address bar carries `#<storage>/<folder>`
 * while rows carry `data-fe-path="<storage>://<path>"`, and hand-converting one
 * into the other is how a hash ends up naming a folder called `qldemo:`. A
 * surface that needs the qualified form of "the folder currently open" asks
 * for it rather than slicing a string.
 *
 * `''` for an empty hash. A hash naming only a storage yields `<storage>://`,
 * which is that storage's root — the shape the explorer uses for it.
 */
export function qualifiedFromHash(hash: string | undefined | null): string {
  const raw = String(hash ?? '').replace(/^#/, '').replace(/^\/+/, '');
  if (!raw) return '';
  // The hash is written by the browser, so it arrives percent-encoded.
  let decoded = raw;
  try {
    decoded = decodeURI(raw);
  } catch {
    /* a malformed escape is not worth losing the navigation over */
  }
  const cut = decoded.indexOf('/');
  if (cut < 0) return `${decoded}://`;
  return `${decoded.slice(0, cut)}://${decoded.slice(cut + 1)}`;
}

/** A route the web SPA can push. `null` when the destination is not in-SPA. */
export function notificationRoute(
  dest: NotificationDestination,
): { name: string; params?: Record<string, string>; query: Record<string, string>; hash: string } | null {
  if (dest.kind === 'app') {
    return {
      name: 'app-home',
      params: { plugin: dest.plugin, view: dest.view },
      query: dest.section ? { section: dest.section } : {},
      hash: '',
    };
  }
  if (dest.kind === 'folder') {
    // ⚠ The app screen travels as QUERY PARAMETERS, not as router state: the
    // same address has to work when it is pasted, reloaded or opened in a new
    // tab, which is exactly what a notification link is for.
    const query: Record<string, string> = dest.select ? { select: dest.select } : {};
    if (dest.open) {
      query.app = dest.open.plugin;
      if (dest.open.action) query.appAction = dest.open.action;
      else if (dest.open.view) query.appView = dest.open.view;
    }
    return {
      name: 'explore',
      query,
      // vue-router wants the '#'; the explorer encodes segment-by-segment, so
      // we hand over the readable form and let the browser escape it.
      hash: `#${explorerHashPath(dest)}`,
    };
  }
  if (dest.kind === 'trash') {
    // The explorer opens `#.trash` as its Trash view (a sentinel, like
    // `#.recent`), and Explore.vue's reveal selects `select` once the rows
    // are drawn — the same two halves a file target uses.
    return {
      name: 'explore',
      query: dest.select ? { select: dest.select } : {},
      hash: `#${explorerHashPath(dest)}`,
    };
  }
  // ⚠⚠ `none` is NOT a route any more — see `isNotificationClickable`. A row
  // with nothing to open is drawn as plain text and nothing navigates; the
  // notifications page it used to push was either the page already on screen
  // or, for a non-admin, a guard bounce that threw away their folder.
  return null; // a share link is outside the SPA; `none` goes nowhere at all
}

/** The public URL of a share token. Outside the SPA — a real navigation. */
export function shareHref(token: string): string {
  return `/s/${encodeURIComponent(token)}`;
}

/**
 * The destination as a plain ADDRESS, for the places that cannot push a
 * route: a service worker's `notificationclick` (which has no router, and may
 * run with no page open at all) and anything that wants a copyable link.
 *
 * `base` is the prefix the SPA is served from (`/admin/`, `/drive/`) — the
 * same reason `pluginPageBase` exists on the explorer's config: only those
 * prefixes fall back to index.html on the server.
 *
 * ⚠ Hand-rolled escaping rather than `URLSearchParams`: this module is
 * imported by the DESKTOP MAIN PROCESS and by a service worker, and its whole
 * discipline is to depend on nothing. `''` when there is nowhere to go — the
 * caller must not turn that into "/".
 */
export function notificationHref(dest: NotificationDestination, base = '/'): string {
  if (dest.kind === 'share') return shareHref(dest.token);
  const prefix = `${String(base || '/').replace(/\/+$/, '')}/`;
  if (dest.kind === 'app') {
    const at = `${prefix}app/${encodeURIComponent(dest.plugin)}/${encodeURIComponent(dest.view)}`;
    return dest.section ? `${at}?section=${encodeURIComponent(dest.section)}` : at;
  }
  if (dest.kind !== 'folder' && dest.kind !== 'trash') return '';
  const q: string[] = [];
  if (dest.select) q.push(`select=${encodeURIComponent(dest.select)}`);
  if (dest.kind === 'folder' && dest.open) {
    q.push(`app=${encodeURIComponent(dest.open.plugin)}`);
    if (dest.open.action) q.push(`appAction=${encodeURIComponent(dest.open.action)}`);
    else if (dest.open.view) q.push(`appView=${encodeURIComponent(dest.open.view)}`);
  }
  const hash = explorerHashPath(dest);
  return `${prefix}explore${q.length ? `?${q.join('&')}` : ''}${hash ? `#${hash}` : ''}`;
}
