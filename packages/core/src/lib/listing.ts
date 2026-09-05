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

/**
 * Sentinel path segment → locale key, for the virtual views the explorer parks
 * in `dirname` (trash, recent, starred, shared). None has a real folder behind
 * it, so anything that renders a path segment has to translate them or it
 * prints the sentinel.
 *
 * ⚠ ONE map, deliberately. `.trash` predates the other three and its
 * translation was written twice — once in the breadcrumb, once in the tab
 * label. When recent/starred/shared arrived, only the breadcrumb copy was
 * extended, so the tab strip read ".shared" in front of users (reported
 * 2026-09-04). A second copy of a mapping is a second chance to forget it.
 */
export const VIRTUAL_SEGMENTS: Record<string, string> = {
  '.trash': 'node.trash',
  '.recent': 'node.recent',
  '.starred': 'node.starred',
  '.shared': 'node.shared',
};

/**
 * The tag view's sentinel: `.tag~<name>`, ONE segment, e.g. `.tag~invoices`.
 *
 * Every other virtual view has a fixed label, so a segment→locale-key map is
 * enough for them. A tag's label is the tag itself, so it cannot live in that
 * map — but it must not become a SECOND place that knows about sentinels
 * either, which is exactly how the tab strip came to print `.shared`. Hence
 * `virtualSegmentLabel()` below: the map keeps the static views, this prefix
 * keeps the dynamic one, and every surface that renders a path segment calls
 * the one function that knows both.
 *
 * ⚠ `~` rather than `:` or `/`. `writePersistedPath` runs each segment through
 * `encodeURIComponent`, which leaves `~ - _ . ! * ' ( )` alone and escapes `:`
 * — so `#.tag~invoices` stays readable in the address bar while `.tag:` would
 * show as `#.tag%3Ainvoices`. And one segment rather than two (`.tag/name`)
 * because a two-segment path gives the breadcrumb a clickable `.tag` parent
 * crumb that leads nowhere.
 */
export const TAG_SEGMENT_PREFIX = '.tag~';

/** `invoices` → `.tag~invoices`. */
export function makeTagSegment(tag: string): string {
  return `${TAG_SEGMENT_PREFIX}${tag}`;
}

/** `.tag~invoices` → `invoices`; '' for anything else (incl. a bare `.tag~`). */
export function tagOfSegment(segment: string): string {
  return segment.startsWith(TAG_SEGMENT_PREFIX)
    ? segment.slice(TAG_SEGMENT_PREFIX.length)
    : '';
}

/** True when `path` (user-facing form) IS a tag listing. */
export function tagOfPath(path: string): string {
  const clean = (path || '').replace(/^\/+|\/+$/g, '');
  return tagOfSegment(clean.split('/').pop() || clean);
}

/** The locale key for a STATIC sentinel segment, or '' otherwise. Prefer
 *  `virtualSegmentLabel` — a tag segment has no locale key to return. */
export function virtualSegmentKey(segment: string): string {
  return VIRTUAL_SEGMENTS[segment] ?? '';
}

/**
 * What a path segment READS AS: the translated view name for a static
 * sentinel, `#<tag>` for a tag view, '' when it is an ordinary folder (the
 * caller then shows the segment itself).
 *
 * `#` and not the bare name: a lone `invoices` crumb between `/` and nothing
 * is indistinguishable from a folder called invoices. The glyph needs no
 * translation and carries the tag's own name verbatim, which is the point.
 */
export function virtualSegmentLabel(segment: string, t: (key: string) => string): string {
  const key = VIRTUAL_SEGMENTS[segment];
  if (key) return t(key);
  const tag = tagOfSegment(segment);
  return tag ? `#${tag}` : '';
}
