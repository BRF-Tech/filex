/**
 * destinationTree — the rules behind "choose a folder".
 *
 * filex had no folder chooser. Copy and move existed (cut/paste, drag between
 * panes) but both required the destination to already be on screen, which is
 * why there was no "Move to…" or "Copy to…" anywhere: the verb was never the
 * missing part, the picker was. `NewDocumentModal` grew a private one-level
 * browser for its own "where should this document go?" question, and a second
 * private copy for Move/Copy would have been the moment the two started
 * disagreeing about what a writable folder is.
 *
 * Everything here is DOM-free and synchronous so the rules can be tested
 * without mounting anything. The component
 * (`modals/DestinationPickerModal.vue`) owns the reactive state and the
 * listing calls; this module owns the answers.
 *
 * # Wire paths
 *
 * A destination is an adapter-qualified wire path — `main://belgeler/2026` —
 * exactly what `api.copy` / `api.moveAsync` take and what the backend resolves
 * to a storage. The tree therefore spans storages, because copy and move
 * already do: `internal/ops` streams bytes between two drivers when the source
 * and destination storages differ. A picker that could only offer folders in
 * the current storage would hide a capability the product already has.
 */

import type { FileNode } from '../types/FileNode';

/**
 * The browser's top level: the list of drives rather than a folder.
 *
 * It cannot collide with a real destination because every wire path contains
 * `://` and this string does not. (The same sentinel and the same reasoning as
 * NewDocumentModal's private browser, which this module is meant to replace.)
 */
export const DRIVES = '@@drives';

/** Split `adapter://rel` into its two halves; a bare path has no adapter. */
export function splitWire(p: string): [string, string] {
  const i = p.indexOf('://');
  if (i < 0) return ['', trimSlashes(p)];
  return [p.slice(0, i), trimSlashes(p.slice(i + 3))];
}

/** Rebuild `adapter://rel`. An empty rel is the storage root. */
export function joinWire(adapter: string, rel: string): string {
  const r = trimSlashes(rel);
  return r ? `${adapter}://${r}` : `${adapter}://`;
}

function trimSlashes(s: string): string {
  return s.replace(/^\/+|\/+$/g, '');
}

/**
 * The wire path one level up, or null when there is nowhere further up.
 *
 * `multiDrive` decides what sits above a storage root: with more than one
 * storage the answer is the list of drives, with exactly one there is nothing
 * above the root and the Up button must be disabled rather than land the user
 * on a one-row list of the drive they are already in.
 */
export function parentOfWire(p: string, multiDrive: boolean): string | null {
  if (p === DRIVES) return null;
  const [adapter, rel] = splitWire(p);
  if (!adapter) return null;
  if (!rel) return multiDrive ? DRIVES : null;
  const parts = rel.split('/');
  parts.pop();
  return joinWire(adapter, parts.join('/'));
}

/** The last segment of a wire path — what a breadcrumb or a row shows. */
export function labelOfWire(p: string, drivesLabel: string): string {
  if (p === DRIVES) return drivesLabel;
  const [adapter, rel] = splitWire(p);
  if (!rel) return adapter || drivesLabel;
  const parts = rel.split('/');
  return parts[parts.length - 1] || adapter;
}

/** Every step from the storage root down to `p`, for a clickable breadcrumb. */
export function crumbsOfWire(p: string): Array<{ label: string; path: string }> {
  if (p === DRIVES || !p) return [];
  const [adapter, rel] = splitWire(p);
  if (!adapter) return [];
  const out = [{ label: adapter, path: joinWire(adapter, '') }];
  if (!rel) return out;
  let acc = '';
  for (const seg of rel.split('/')) {
    acc = acc ? `${acc}/${seg}` : seg;
    out.push({ label: seg, path: joinWire(adapter, acc) });
  }
  return out;
}

/**
 * May the caller write into a folder with this `perm`?
 *
 * ⚠ `undefined` AND `''` both mean "this storage does not enforce ACL", which
 * is the pre-RBAC default and full access. The declared type says one of four
 * levels, but the wire really does carry `''` for an unenforced storage, so
 * reading both as allowed is the truthful reading and not a convenience.
 *
 * This is deliberately the same predicate as `FileExplorer.permCanEdit` and
 * `NewDocumentModal.permAllowsWrite`. Two answers to "may I write here" is how
 * a dialog ends up offering a destination the toolbar hides.
 */
export function permAllowsWrite(perm: string | undefined | null): boolean {
  return perm === undefined || perm === null || perm === '' || perm === 'editor' || perm === 'owner';
}

/** Is `child` the same as, or inside, `ancestor`? Both are wire paths. */
export function isAtOrInside(child: string, ancestor: string): boolean {
  if (child === DRIVES || ancestor === DRIVES) return false;
  const [ca, cr] = splitWire(child);
  const [aa, ar] = splitWire(ancestor);
  if (ca !== aa) return false;
  if (ar === '') return true;
  return cr === ar || cr.startsWith(ar + '/');
}

/**
 * Why this folder cannot be the destination, or null when it can.
 *
 * `'self'` and `'descendant'` are kept apart because they need different
 * sentences. "You cannot move a folder into itself" and "…into a folder inside
 * it" are the two halves of the mistake, and a single message covering both
 * reads as a riddle at the moment the person is trying to work out what they
 * did wrong.
 *
 * ⚠ This is a courtesy, not a control. The same refusal exists in
 * `internal/ops.SubmitTo` and answers 400 whatever the picker offered —
 * measured on a live local storage 2026-09-13, where the API accepted the
 * request and the queue worker failed it minutes later with an errno.
 */
export function blockedReason(
  candidate: string,
  moving: string[] | undefined,
): 'self' | 'descendant' | null {
  if (!moving || moving.length === 0) return null;
  if (candidate === DRIVES) return null;
  const norm = (p: string) => {
    const [a, r] = splitWire(p);
    return joinWire(a, r);
  };
  const c = norm(candidate);
  for (const src of moving) {
    if (!src) continue;
    // Order matters: equality is also "inside", and the two need different
    // sentences.
    if (c === norm(src)) return 'self';
    if (isAtOrInside(candidate, src)) return 'descendant';
  }
  return null;
}

/** One row in the picker's list. */
export interface DestinationRow {
  /** Wire path of the folder. */
  path: string;
  /** What to show — the folder's own name. */
  label: string;
  /** May the caller write into it? Drives the disabled Choose button. */
  writable: boolean;
  /** Set when this row is one of the folders being moved, or inside one. */
  blocked: 'self' | 'descendant' | null;
}

/**
 * Turn a listing into rows.
 *
 * ⚠ An unwritable folder is LISTED and stays OPENABLE. The grant that allows
 * writing may live on a subfolder, so hiding the parent would make the
 * destination unreachable; hiding it also tells the user their folder is gone
 * rather than that they may not write into it. Same rule NewDocumentModal
 * settled on.
 *
 * ⚠ A blocked folder (the one being moved, or inside it) is also listed rather
 * than hidden: seeing it greyed out with a reason is how a person understands
 * the refusal. It is not openable, because everything under it is blocked too
 * and walking into a dead end is not navigation.
 */
export function destinationRows(
  files: FileNode[] | undefined,
  moving?: string[],
): DestinationRow[] {
  const out: DestinationRow[] = [];
  for (const f of files ?? []) {
    if (f.type !== 'dir') continue;
    out.push({
      path: f.path,
      label: f.basename,
      writable: permAllowsWrite(f.perm as string | undefined),
      blocked: blockedReason(f.path, moving),
    });
  }
  return out;
}

/** The drives level: one row per storage, each its own root. */
export function driveRows(storages: string[] | undefined, moving?: string[]): DestinationRow[] {
  return (storages ?? []).map((name) => {
    const path = joinWire(name, '');
    return {
      path,
      label: name,
      // A storage root's own writability is not knowable from the drive list;
      // it is answered by the listing when the user opens it. Offering it as
      // writable here and letting the listing correct that is the honest
      // order — the alternative greys out every drive until each is opened.
      writable: true,
      blocked: blockedReason(path, moving),
    };
  });
}

/**
 * Where the picker should open.
 *
 * The current folder, because that is where the person is standing and most
 * destinations are near it. Falls back to the drives list when there is more
 * than one storage and no current folder, and to the single storage's root
 * when there is only one.
 */
export function initialLocation(
  current: string | undefined,
  storages: string[] | undefined,
): string {
  const multi = (storages?.length ?? 0) > 1;
  if (current && current.includes('://')) return current;
  if (multi) return DRIVES;
  const only = storages?.[0];
  return only ? joinWire(only, '') : DRIVES;
}
