/**
 * listing.ts — shared listing helpers used by BOTH the main panel
 * (FileExplorer.load) and the split-view secondary panel
 * (SecondaryPane.loadPane). SINGLE SOURCE so the two panes can never
 * drift apart: the internal-entry filter and the virtual `.trash` row
 * must be identical in both, otherwise split view shows mismatched rows
 * (the trash row missing on one side → visible row-offset).
 */
import type { FileNode } from '../types/FileNode';
import { E2E_MARKER_NAME } from './e2ecrypto';

/** Adapter-strip: `s3-test://fileman/x` → `fileman/x`. */
export function stripAdapter(p: string): string {
  const idx = p.indexOf('://');
  return idx === -1 ? p : p.slice(idx + 3);
}

/**
 * Hide system/internal entries the user must never see as files:
 * thumbnails, version history, the soft-delete store, keepdir markers
 * and the E2E marker. Shared by both panes' listing filters.
 */
export function filterInternalEntries(files: FileNode[]): FileNode[] {
  return (files || []).filter((f) => {
    if (f.path.includes('.thumbs')) return false;
    if (f.path.includes('.versions') || f.basename === '.versions') return false;
    if (f.basename === '.trash') return false;
    if (f.basename === '.keepdir') return false;
    if (f.basename === E2E_MARKER_NAME) return false;
    return true;
  });
}

/** True when `dirname` (adapter-qualified) is the storage root, where the
 *  virtual trash row belongs. */
export function isStorageRootDir(dirname: string): boolean {
  const rel = stripAdapter(dirname);
  return rel === 'fileman' || rel === '';
}

/** True when the listing IS the trash view (don't inject the row into itself). */
export function isTrashListing(dirname: string): boolean {
  return stripAdapter(dirname).startsWith('fileman/.trash');
}

/** The synthetic `.trash` row shown at storage root — rendered as
 *  "Çöp Kutusu" / "Trash" via the `.trash` basename → locale mapping. */
export function makeTrashRow(adapter: string): FileNode {
  return {
    type: 'dir',
    path: `${adapter}://fileman/.trash`,
    basename: '.trash',
    extension: '',
    storage: adapter,
    visibility: 'private',
    size: 0,
    file_size: 0,
    mime_type: 'inode/directory',
    extra_metadata: {},
  } as unknown as FileNode;
}

/**
 * Inject the virtual `.trash` row at the front of a root listing when
 * enabled. Returns true if a row was added (so the caller can hydrate
 * it). Mutates `files` in place. Single source for both panes.
 */
export function injectTrashRow(
  files: FileNode[],
  adapter: string,
  dirname: string,
  trashVisible: boolean,
): boolean {
  if (!trashVisible) return false;
  if (isTrashListing(dirname)) return false;
  if (!isStorageRootDir(dirname)) return false;
  files.unshift(makeTrashRow(adapter));
  return true;
}

/** Best-effort fill of the trash row's size (total bytes) + date (newest
 *  deletion) from the backend trash listing, so it reads like a real
 *  folder instead of "— / —". Non-blocking; mutates the row in place. */
export async function hydrateTrashRow(
  files: FileNode[],
  storage: string,
  api: { listTrash: (s?: string) => Promise<{ entries: Array<{ size?: number; deleted_at: string }> }> },
): Promise<void> {
  try {
    const { entries } = await api.listTrash(storage);
    const row = files.find((f) => f.basename === '.trash');
    if (!row) return;
    let total = 0;
    let newest = 0;
    for (const e of entries) {
      total += e.size || 0;
      const ts = Date.parse(e.deleted_at);
      if (!Number.isNaN(ts) && ts > newest) newest = ts;
    }
    row.size = total;
    if (newest > 0) row.last_modified = newest;
  } catch {
    /* keep the bare row */
  }
}
