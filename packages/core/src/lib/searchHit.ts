/**
 * A search hit, turned into the things the explorer can act on — task #47.
 *
 * `/api/files/search` answers with raw node rows, not listing rows: an
 * in-storage relative `path`, a numeric `storage_id` and (on current servers)
 * the drive's NAME in `storage`. Everything done with a hit — the advanced
 * search's result rows, and the ⌘K palette's open / download / drag-out —
 * needs the wire address a listing row carries (`name://rel`). One module
 * builds it, so those surfaces cannot disagree about where a file is: "open
 * works, download 404s" is what several inline copies of one rule turn into.
 *
 * The second half is the desktop's rail: several accounts searched at once,
 * each hit tagged with its account, one group per account.
 */

import type { GlobalSearchHit } from '../composables/useFileApi';
import type { SearchAccount } from '../types/ExplorerConfig';
import type { FileNode } from '../types/FileNode';
import type { DragItem } from './dragOut';

/** What the explorer knows about drives when a hit does not say its own. */
export interface HitDriveContext {
  /** Drive names the host configured (`config.storages`). */
  configured: string[];
  /** The drive the explorer has open right now. */
  current: string;
}

/** The hit's path inside its drive, without leading or trailing slashes. */
export function hitRelPath(hit: GlobalSearchHit): string {
  return String(hit.path ?? '').replace(/^\/+|\/+$/g, '');
}

/**
 * Which drive a hit lives on: the name the server sent > `storage_name`
 * (older servers) > the only configured drive > the drive that is open.
 * A wrong last-resort guess lands on the listing's "folder not found" state,
 * which is a graceful dead end — not a request against the wrong file.
 */
export function hitStorageName(hit: GlobalSearchHit, ctx: HitDriveContext): string {
  if (typeof hit.storage === 'string' && hit.storage) return hit.storage;
  if (typeof hit.storage_name === 'string' && hit.storage_name) return hit.storage_name;
  if (ctx.configured.length === 1 && ctx.configured[0]) return ctx.configured[0];
  return ctx.current || '';
}

/**
 * Map one raw search hit onto the listing shape.
 *
 * ⚠ The drive comes from the HIT (`describeHits` puts it there), not from the
 * pane we happen to be standing in — a content search spans storages, so the
 * open drive is only the right answer by accident. `storageName` stays as the
 * fallback for a backend older than that field.
 *
 * (Moved here from FileExplorer's `advHitToNode` for #47, unchanged: the
 * palette's download and drag-out need the same row the advanced search draws.)
 */
export function hitToNode(h: GlobalSearchHit, storageName: string): FileNode {
  const rel = String(h.path ?? '').replace(/^\/+/, '');
  const drive = typeof h.storage === 'string' && h.storage ? h.storage : storageName;
  const name = String(h.name ?? rel.split('/').pop() ?? '');
  const dot = name.lastIndexOf('.');
  const mtime = typeof h.backend_mtime === 'string' ? h.backend_mtime : h.updated_at;
  const ms = typeof mtime === 'string' ? Date.parse(mtime) : NaN;
  return {
    id: typeof h.id === 'number' ? h.id : undefined,
    path: drive ? `${drive}://${rel}` : rel,
    basename: name,
    relativePath: rel,
    type: h.type === 'dir' ? 'dir' : 'file',
    extension: dot > 0 ? name.slice(dot + 1).toLowerCase() : '',
    size: typeof h.size === 'number' ? h.size : 0,
    // ⚠ Left UNSET when the row carries no parseable timestamp rather than
    // defaulted to 0 or to now: `matchesModified` treats a missing timestamp
    // as "unknown" and drops the row from a date filter, which is the honest
    // answer. Stamping it with `Date.now()` would file every such file under
    // "Today".
    last_modified: Number.isNaN(ms) ? undefined : ms,
    mime_type: typeof h.mime === 'string' ? h.mime : '',
    /* The content snippet the hit came with, so a content match can show why
       it matched. Undefined on name-only hits, exactly as the backend sends. */
    snippet: typeof h.snippet === 'string' && h.snippet ? h.snippet : undefined,
    /* Owner, so the People filter and the Owner column mean the same thing in a
       content result as they do in a folder listing. Undefined rather than
       guessed when the backend does not send it. */
    owner_id: typeof h.owner_id === 'number' ? h.owner_id : undefined,
    owner_name: typeof h.owner_name === 'string' ? h.owner_name : undefined,
    owner_self: h.owner_self === true ? true : undefined,
  };
}

/**
 * The hit as a drag-out / download item (`name://rel`, its name, its kind) —
 * or null when there is nothing to address: no path, or no drive to put it on.
 * The same `{path, basename, type}` a listing row hands the drag-out hook.
 */
export function hitItem(hit: GlobalSearchHit, ctx: HitDriveContext): DragItem | null {
  const drive = hitStorageName(hit, ctx);
  if (!hitRelPath(hit) || !drive) return null;
  const n = hitToNode(hit, drive);
  return { path: n.path, basename: n.basename, type: n.type };
}

/** One account's hits, in the server's order. `account` undefined = no rail. */
export interface HitGroup {
  account?: SearchAccount;
  hits: GlobalSearchHit[];
}

/**
 * Hits → one group per account, in the order accounts first appear (the
 * explorer lists its own account first), each capped at `limit` on its own —
 * a busy account must not push a quieter one off the list.
 */
export function groupHitsByAccount(hits: GlobalSearchHit[], limit: number): HitGroup[] {
  const groups: HitGroup[] = [];
  const byKey = new Map<string, HitGroup>();
  for (const hit of hits) {
    const key = hit.account?.id ?? '';
    let group = byKey.get(key);
    if (!group) {
      group = { account: hit.account, hits: [] };
      byKey.set(key, group);
      groups.push(group);
    }
    if (group.hits.length < limit) group.hits.push(hit);
  }
  return groups;
}
