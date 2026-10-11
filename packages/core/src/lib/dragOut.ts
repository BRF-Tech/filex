/**
 * Dragging files OUT of filex — onto the desktop, into another app.
 *
 * There are exactly two ways a page can hand real files to the operating
 * system, and filex uses both because neither one alone covers the ask:
 *
 * 1. **`DownloadURL`** (any Chromium browser, so the web explorer AND the
 *    desktop app get it for free). One `dataTransfer.setData('DownloadURL',
 *    'mime:name:url')` and the browser downloads that URL into wherever the
 *    drop landed. Costs nothing until the drop happens — but Chromium carries
 *    exactly ONE such entry, so it is a single-file gesture, and the URL is
 *    fetched by the browser's own download stack, which sends cookies but no
 *    Authorization header (see `canDownloadUrlDrag`).
 *
 * 2. **A native OS drag** (`webContents.startDrag`, desktop app only). Takes a
 *    list of real paths, so folders and multi-selections drop as separate real
 *    files — the WinRAR gesture. The catch is in the word *real*: the shell
 *    copies the bytes at DROP time, from a path, so the file has to be on this
 *    computer BEFORE the drag starts. There is no lazy/virtual-file API to
 *    borrow (that is a Win32 `IDataObject` affair Chromium does not expose).
 *
 * ⚠ The native drag REPLACES the HTML5 drag — `startDrag` requires
 * `preventDefault()` on `dragstart`, and after that the app's own drop targets
 * see an OS file drag instead of `application/x-brf-files`. Dropping a row on a
 * folder inside filex would then UPLOAD the temp copy back to the server: a
 * round trip of the same bytes, a copy instead of a move, and the original left
 * behind. That is what `beginNativeDrag` is for — the payload is remembered on
 * this side, and every drop target asks `internalDragItems(ev)` rather than
 * reading the dataTransfer directly. An OS drag we started is still an internal
 * drag when it lands inside our own window.
 *
 * Because the bytes must exist first, the shell is asked to prepare them on
 * `mousedown` — before the gesture becomes a drag — and the native path is only
 * taken when it answered "ready". Anything else keeps the ordinary HTML5 drag,
 * so an internal move never waits on a download it does not need.
 */

import { rowInEncryptedFolder, type EncryptedRowLike } from './encryptedRow';

export interface DragItem {
  path: string;
  basename: string;
  type: 'file' | 'dir';
}

export const FE_DND_MIME = 'application/x-brf-files';
export const FE_DND_SRC_MIME = 'application/x-brf-files-src';

/** An OS drag this window started, and where it came from. */
interface NativeDrag {
  items: DragItem[];
  origin: string;
  startedAt: number;
}

let native: NativeDrag | null = null;

/**
 * A native drag has no `dragend` on our side (the HTML5 one never started), so
 * the record is dropped by whoever notices the gesture is over — a drop we
 * handled, a pointerup, or this ceiling. Five minutes is far longer than any
 * drag and short enough that a stale record cannot outlive the user's memory
 * of it.
 */
const NATIVE_DRAG_TTL_MS = 5 * 60_000;

export function beginNativeDrag(items: DragItem[], origin: string): void {
  native = { items: items.slice(), origin, startedAt: Date.now() };
}

export function endNativeDrag(): void {
  native = null;
}

export function activeNativeDrag(): NativeDrag | null {
  if (!native) return null;
  if (Date.now() - native.startedAt > NATIVE_DRAG_TTL_MS) {
    native = null;
    return null;
  }
  return native;
}

/**
 * True when this drag is filex's own — either the HTML5 payload is on the
 * dataTransfer, or we are mid-native-drag. Safe to call from `dragover`, where
 * `getData` is blocked and only `types` may be read.
 */
export function hasInternalDrag(ev: DragEvent): boolean {
  if (ev.dataTransfer?.types.includes(FE_DND_MIME)) return true;
  return activeNativeDrag() !== null;
}

/** The dragged rows, from whichever of the two drag mechanisms is in play. */
export function internalDragItems(ev: DragEvent): DragItem[] | null {
  const raw = ev.dataTransfer?.getData(FE_DND_MIME);
  if (raw) {
    try {
      const parsed = JSON.parse(raw) as DragItem[];
      return Array.isArray(parsed) && parsed.length > 0 ? parsed : null;
    } catch {
      return null;
    }
  }
  const n = activeNativeDrag();
  return n && n.items.length > 0 ? n.items : null;
}

/** The folder a drag started from — the origin stamp used by move ops. */
export function internalDragOrigin(ev: DragEvent): string | undefined {
  const v = ev.dataTransfer?.getData(FE_DND_SRC_MIME);
  if (v) return v;
  return activeNativeDrag()?.origin || undefined;
}

/**
 * Can this install hand a plain URL to the browser's download stack?
 *
 * Only when the credential travels on its own: a cookie session (the web
 * explorer) or no auth at all. With a bearer token the page holds the
 * credential and the download would arrive unauthenticated — a drop that
 * silently produces a 401 page named like the file is worse than a drop that
 * does nothing. The desktop app (bearer) uses the native path instead, and a
 * bearer page in a browser (the admin SPA) hands over a minted link — see
 * `createDragLinks`.
 */
export function canDownloadUrlDrag(auth: unknown): boolean {
  const kind = authKindOf(auth);
  return kind === undefined || kind === 'none' || kind === 'csrf';
}

/** `AuthConfig` carries the strategy as `kind`, or as `type` in the 0.1.0
 *  shape embedders still pass. Both mean the same thing here. */
export function authKindOf(auth: unknown): string | undefined {
  if (!auth || typeof auth !== 'object') return undefined;
  const a = auth as { kind?: unknown; type?: unknown };
  const raw = typeof a.kind === 'string' ? a.kind : typeof a.type === 'string' ? a.type : undefined;
  return raw;
}

/**
 * The `DownloadURL` value Chromium wants: `<mime>:<filename>:<absolute url>`.
 *
 * ⚠ Absolute. A relative URL is accepted by `setData` and then fails to
 * resolve in the browser process, which is a drop that quietly does nothing.
 */
export function downloadUrlPayload(item: DragItem, url: string, mime?: string): string | null {
  if (item.type !== 'file') return null;
  let abs = url;
  try {
    abs = new URL(url, typeof location !== 'undefined' ? location.href : undefined).toString();
  } catch {
    return null;
  }
  if (!/^https?:/i.test(abs)) return null;
  const name = item.basename.replace(/[:\r\n]/g, '_');
  return `${mime && mime.trim() ? mime : 'application/octet-stream'}:${name}:${abs}`;
}

/** Stable key for "is the shell's prepared set still the one being dragged?" */
export function dragKey(items: Array<{ path: string }>): string {
  return items
    .map((i) => i.path)
    .sort()
    .join(String.fromCharCode(0));
}

/* === #71 — a minted link for a session the browser cannot sign ===========
 *
 * A bearer page (the admin SPA) cannot give `DownloadURL` its plain download
 * URL: the browser's download stack sends no Authorization header. The server
 * mints a short-lived, single-use, credential-free link to one file instead
 * (`requestFileLink`). The catch is WHEN: `dragstart` must fill the
 * dataTransfer synchronously — it is writable during that event and never
 * after — so a link asked for at dragstart arrives too late, every time. It
 * has to exist before the drag begins.
 *
 * So the explorer asks while the pointer rests on a row and again on the press
 * (`warm`), and `dragstart` only reads what is already here (`take`). A person
 * hovers before pressing and presses before moving the few pixels that start
 * a drag; a mint is one small POST answered from the catalogue, not the
 * storage. When it has still not answered, the drag goes without a
 * `DownloadURL` — it never waits and never guesses.
 *
 * Three rules keep that cheap and honest:
 *   - one mint in flight at a time, and the rows a pointer crossed meanwhile
 *     collapse to the LAST one — sweeping across forty rows is two requests,
 *     not forty;
 *   - a link is handed out once: the server consumes it with the download it
 *     starts, so a second drag of the same row needs a new one;
 *   - a link close to its end is refreshed on the next hover/press, and is not
 *     handed out at all in its last seconds — a drop on a lapsed link is a 410
 *     the person only sees as a failed download.
 */

/** A minted link, and how long the server said it lives. */
export interface MintedLink {
  url: string;
  ttlMs: number;
}

export interface DragLinks {
  /** Get a link for this file ready (hover / press). Cheap to call often. */
  warm(path: string): void;
  /** The live link for this file, handed out ONCE; null when there is none. */
  take(path: string): string | null;
  /** False once the server has shown it cannot mint file links. */
  readonly supported: boolean;
}

export interface DragLinkOptions {
  now?: () => number;
  /** Below this much life left, the next warm mints a fresh link. */
  refreshBelowMs?: number;
  /** Below this much life left, a link is no longer handed to a drag. */
  usableAboveMs?: number;
  /** How long a refused mint is not asked again for the same file. */
  failPauseMs?: number;
  /** How many links are kept at once. */
  max?: number;
  /** Called once when the server turns out not to mint file links. */
  onUnsupported?: () => void;
}

export function createDragLinks(
  mint: (path: string) => Promise<MintedLink | null>,
  opts: DragLinkOptions = {},
): DragLinks {
  const now = opts.now ?? (() => Date.now());
  const refreshBelow = opts.refreshBelowMs ?? 30_000;
  const usableAbove = opts.usableAboveMs ?? 5_000;
  const failPause = opts.failPauseMs ?? 30_000;
  const max = opts.max ?? 32;
  const links = new Map<string, { url: string; expiresAt: number }>();
  const pausedUntil = new Map<string, number>();
  let inflight: string | null = null;
  let queued: string | null = null;
  let supported = true;

  function remaining(path: string): number {
    const e = links.get(path);
    return e ? e.expiresAt - now() : -Infinity;
  }

  function start(path: string) {
    inflight = path;
    // A throw from `mint` itself is a refusal like any other, not a crash of
    // the hover that asked.
    let pending: Promise<MintedLink | null>;
    try {
      pending = mint(path);
    } catch (err) {
      pending = Promise.reject(err);
    }
    pending
      .then((got) => {
        if (got === null) {
          if (supported) {
            supported = false;
            links.clear();
            opts.onUnsupported?.();
          }
          return;
        }
        links.delete(path);
        links.set(path, { url: got.url, expiresAt: now() + got.ttlMs });
        while (links.size > max) {
          const oldest = links.keys().next().value;
          if (oldest === undefined) break;
          links.delete(oldest);
        }
      })
      .catch(() => {
        pausedUntil.set(path, now() + failPause);
      })
      .finally(() => {
        inflight = null;
        const next = queued;
        queued = null;
        if (next) warm(next);
      });
  }

  function warm(path: string) {
    if (!supported || !path) return;
    if (remaining(path) > refreshBelow) return;
    const paused = pausedUntil.get(path);
    if (paused !== undefined) {
      if (paused > now()) return;
      pausedUntil.delete(path);
    }
    if (inflight === path) return;
    if (inflight !== null) {
      queued = path;
      return;
    }
    start(path);
  }

  function take(path: string): string | null {
    const e = links.get(path);
    if (!e) return null;
    links.delete(path);
    return e.expiresAt - now() > usableAbove ? e.url : null;
  }

  return {
    warm,
    take,
    get supported() {
      return supported;
    },
  };
}

/** What `mayDragOut` / `mayMintDragLink` read of a row or a search hit. */
export type DragLinkRow = EncryptedRowLike & { type?: string; basename?: string; vault_root?: string | null };

/**
 * A row as a drag hands it on: the item itself (path, name, kind) and what
 * says whether it may leave the window - a listing row (`FileNode`) and a
 * ⌘K hit (lib/searchHit `hitItem`) both carry it, as the server stamped it.
 */
export type DragOutRow = DragItem & EncryptedRowLike & { vault_root?: string | null };

/**
 * wiring:e2, #243 — may this row be handed OUT of the window: to the OS as a
 * file or a folder (the desktop shell's drag), or to the browser's download
 * stack (`DownloadURL`)? Not when it is inside an encrypted folder or a vault,
 * or is one (lib/encryptedRow rowInEncryptedFolder; `vault_root` on a vault's
 * own rows), and never the trash.
 *
 * The bytes behind such a row's URL are ciphertext, and the OS would save them
 * under the file's name as if they were the document. A vault's row is worse:
 * it exists only in this tab and its path names the file in plaintext, so
 * handing it to the download stack or the shell sends that name to the server.
 * A single encrypted file (`.fxe`, `encrypted: 'file'`) is the exception: it
 * IS the file, carrying its own key slots.
 *
 * ⚠ The ROW says it (the server's `encrypted` / `e2e_root`), not the folder
 * on screen: a ⌘K hit, a row on Recent, Starred, a tag view or a search sits
 * in an encrypted folder while a plain one is open. Before task #243 the
 * explorer read only the open folder, and the hit and the listing's drag kept
 * path, name and kind - so such a row went out.
 */
export function mayDragOut(row: DragLinkRow | null | undefined): boolean {
  return (
    !!row &&
    row.basename !== '.trash' &&
    !rowInEncryptedFolder(row) &&
    !(typeof row.vault_root === 'string' && row.vault_root !== '')
  );
}

/** The item a drag hands on - path, name, kind and nothing else - for the
 *  shell's hook and the in-app payload. */
export function dragItemOf(row: DragItem): DragItem {
  return { path: row.path, basename: row.basename, type: row.type };
}

/**
 * The rows a drag may hand out of the window, as plain items - or null when
 * it may hand out none: the drag started in an encrypted folder or a vault
 * (`inEncryptedFolder`, the pane's own listing: a folder row inside one says
 * nothing of it itself), or one of the rows may not leave (`mayDragOut`).
 * All or nothing, like the drag: a selection is one gesture.
 *
 * Refused, the drag stays filex's own - a move onto a folder in this window
 * still works; nothing is handed to the OS or the download stack.
 */
export function dragOutItems(
  rows: ReadonlyArray<DragOutRow>,
  opts: { inEncryptedFolder?: boolean } = {},
): DragItem[] | null {
  if (opts.inEncryptedFolder || rows.length === 0) return null;
  if (!rows.every((r) => mayDragOut(r))) return null;
  return rows.map(dragItemOf);
}

/**
 * #71, wiring:e2 — may the explorer ask the server for a drag-out link to this
 * row (`createDragLinks().warm`)? Only for a file the drag may hand out
 * (`mayDragOut`): a link minted for any other row is never used. In a vault
 * the mint is worse than useless: a vault's row exists only in this tab and
 * its path names the file in plaintext, and the mint posts that path to the
 * server - the name the vault keeps from it (e2e 236).
 */
export function mayMintDragLink(row: DragLinkRow | null | undefined): boolean {
  return !!row && row.type === 'file' && mayDragOut(row);
}

/** One report of a drop being filled in (ExplorerConfig.dragOut.onProgress). */
export interface DragProgressLike {
  done: number;
  total: number;
  name?: string;
  /** Where it was dropped: set once the drop has landed. */
  dropped?: string;
  finished?: boolean;
  error?: string;
  /** Files written into the drop so far (inside folders too). */
  files?: number;
}

export type DragProgressSay =
  | { kind: 'flash'; message: string }
  /** A line held on screen until the next report: the work is still going. */
  | { kind: 'hold'; message: string; stoppable: boolean }
  | null;

/**
 * What the explorer says about a drop being filled in.
 *
 * ⚠ After the drop the desktop app downloads what was dragged into the place
 * it was dropped. The explorer said "Downloading…" for 2.5 s once per dragged
 * item — one folder, one toast — and then nothing until the end, however many
 * minutes and files that was, with no way to stop it. While it runs the line
 * now stays, counts the files written so far, and offers Stop when the shell
 * can stop it; the end is said once.
 */
export function sayDragProgress(
  p: DragProgressLike,
  opts: { quiet: boolean; stoppable: boolean; t: (key: string, vars?: Record<string, string | number>) => string },
): DragProgressSay {
  const { t } = opts;
  const afterDrop = !!p.dropped;
  if (p.error === 'drop_not_found') return { kind: 'flash', message: t('dragout.not_found') };
  if (p.error === 'cancelled') return { kind: 'flash', message: t('dragout.stopped') };
  if (p.error) return afterDrop || !opts.quiet ? { kind: 'flash', message: p.error } : null;
  if (afterDrop) {
    if (p.finished) return { kind: 'flash', message: t('dragout.done') };
    return {
      kind: 'hold',
      message: p.files ? t('dragout.downloading_n', { n: p.files }) : t('dragout.downloading'),
      stoppable: opts.stoppable,
    };
  }
  if (opts.quiet) return null;
  if (!p.finished && p.done === 0) return { kind: 'flash', message: t('dragout.preparing') };
  return null;
}
