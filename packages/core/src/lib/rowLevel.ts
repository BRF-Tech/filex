/**
 * rowLevel - which access level a row on screen is judged by.
 *
 * ⚠⚠ #103. A row's level decides how a double-click opens it (the editor or
 * the read-only preview) and which write verbs its menu carries. A row that
 * carries no `perm` of its own used to be judged by the level of the folder
 * the explorer was showing AT THE MOMENT OF THE CLICK - `dirPerm`, which only
 * `load()` sets and which a virtual view forgets on entry. Between two
 * listings that is not the folder the rows came from: going from Starred to a
 * folder clears the view at once, the Starred rows stay on screen until the
 * folder's answer arrives, and in that gap `dirPerm` is `''`. `permCanEdit('')`
 * is false, so a file double-clicked then opened read-only although the
 * person could edit it (seen once in the app-platform measurement,
 * 2026-09-28; e2e/tests/190-open-while-relisting.spec.ts holds the answer
 * back and double-clicks into the gap).
 *
 * Two rules close it, and both are here so the explorer and its test read the
 * same ones:
 *
 *   1. a folder listing hands its level to every row that came with it
 *      (`withListingLevel`, applied where the listing is committed) - so the
 *      row keeps the level of the folder it was listed in, whatever the
 *      explorer is showing by the time it is clicked;
 *   2. a row with no level of its own, judged while no folder level is known
 *      (`''`: a view was just left, or the server enforces no ACL), is NOT
 *      judged by the empty string. It is ungated, as a row in a virtual view
 *      already is - the client only shapes the surface, the server enforces.
 *
 * A row that carries its own `perm` (every listing does on a server with ACL
 * on - handlers/meta.go, manager.go projectFileNodes) is always judged by it.
 */
import type { FileNode } from '../types';

const LEVELS = new Set(['none', 'viewer', 'editor', 'owner']);

type Level = NonNullable<FileNode['perm']>;

function isLevel(v: unknown): v is Level {
  return typeof v === 'string' && LEVELS.has(v);
}

/**
 * A folder listing's rows, each carrying a level: its own, else the level the
 * listing states for its folder. A listing that states none (ACL off, an older
 * host) is returned as it is. Rows are copied, never mutated.
 */
export function withListingLevel(rows: FileNode[], listingLevel: unknown): FileNode[] {
  if (!isLevel(listingLevel)) return rows;
  return rows.map((r) => (typeof r.perm === 'string' ? r : { ...r, perm: listingLevel }));
}

/**
 * The level a row with no `perm` of its own is judged by. `undefined` means
 * ungated (the write verbs are offered and the server decides).
 *
 * @param virtual    the explorer is in a view whose rows span every storage
 *                   (Recent, Starred, Shared, a tag, Home) - no folder.
 * @param folderLevel the level of the folder being listed; `''` when none is
 *                   known.
 */
export function fallbackRowLevel(virtual: boolean, folderLevel: string): string | undefined {
  if (virtual) return undefined;
  return folderLevel ? folderLevel : undefined;
}
