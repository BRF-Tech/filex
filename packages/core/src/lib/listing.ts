/**
 * listing.ts — shared listing helpers used by BOTH the main panel
 * (FileExplorer.load) and the split-view secondary panel
 * (SecondaryPane.loadPane). SINGLE SOURCE so the two panes can never
 * drift apart: the internal-entry filter and the virtual `.trash` row
 * must be identical in both, otherwise split view shows mismatched rows
 * (the trash row missing on one side → visible row-offset).
 */
import { ref } from 'vue';

import type { FileNode } from '../types/FileNode';
import { E2E_MARKER_NAME } from './e2ecrypto';

/** Adapter-strip: `s3-test://fileman/x` → `fileman/x`. */
export function stripAdapter(p: string): string {
  const idx = p.indexOf('://');
  return idx === -1 ? p : p.slice(idx + 3);
}

/**
 * Hide system/internal entries the user must never see as files:
 * thumbnails, version history, the soft-delete store, keepdir markers,
 * the desktop app's open-with scratch area and the E2E marker. Shared by
 * both panes' listing filters.
 */
export function filterInternalEntries(files: FileNode[]): FileNode[] {
  return (files || []).filter((f) => {
    if (f.path.includes('.thumbs')) return false;
    if (f.path.includes('.versions') || f.basename === '.versions') return false;
    if (f.basename === '.trash') return false;
    // The desktop app's "open this document with filex" scratch area. Real
    // files live in it for the length of one editing session, but they are the
    // app's plumbing, not the user's documents -- and a user who has switched
    // hidden files on is asking to see their OWN dotfiles, not ours. Hidden
    // absolutely, like .filex-trash and .versions; stale copies are swept by
    // the desktop app itself, not by hand.
    if (f.basename === '.filex-open') return false;
    if (f.basename === '.keepdir') return false;
    if (f.basename === E2E_MARKER_NAME) return false;
    return true;
  });
}

const SHOW_HIDDEN_LS_KEY = 'filex.showHidden';

function readShowHidden(): boolean {
  try {
    return localStorage.getItem(SHOW_HIDDEN_LS_KEY) === '1';
  } catch {
    return false; // private mode / no storage → hidden, the safe default
  }
}

/**
 * Whether dot-prefixed entries are listed. Off by default, like every other
 * file manager. Shared by both panes so split view can't disagree with itself.
 */
export const showHiddenFiles = ref(readShowHidden());

export function setShowHiddenFiles(v: boolean): void {
  showHiddenFiles.value = v;
  try {
    localStorage.setItem(SHOW_HIDDEN_LS_KEY, v ? '1' : '0');
  } catch {
    /* preference just won't persist */
  }
}

/**
 * Hide dot-prefixed entries unless the user asked to see them.
 *
 * These are mostly not the user's files at all: a Mac writing over WebDAV
 * leaves a `.DS_Store` in every folder it opens and an AppleDouble `._name`
 * beside every file carrying extended attributes. Finder hides its own litter
 * locally, so seeing it reappear in the web UI reads as corruption.
 *
 * A toggle rather than a blocklist, because they ARE real files: hiding them
 * outright would leave the ones already uploaded unreachable and undeletable.
 */
export function filterHiddenEntries(files: FileNode[], showHidden: boolean): FileNode[] {
  if (showHidden) return files || [];
  return (files || []).filter((f) => !(f.basename || '').startsWith('.'));
}

/** filterInternalEntries + filterHiddenEntries — what both panes render. */
export function filterListing(files: FileNode[]): FileNode[] {
  return filterHiddenEntries(filterInternalEntries(files), showHiddenFiles.value);
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
