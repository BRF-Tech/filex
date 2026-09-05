/**
 * surucu:d1 — the filter row's model, kept out of the components so the
 * predicate has one definition and can be tested without a DOM.
 *
 * ⚠ EVERY filter here answers from a field the listing row ALREADY carries
 * (`type`, `extension`, `mime_type`, `size`, `last_modified`). That is not a
 * shortcut, it is the constraint: `GET /api/files/manager?action=index` reads
 * no `mime` / `min_size` / `modified_after` / `owner` parameter — it returns
 * the whole directory and ignores anything else you send it (handlers/manager.go
 * `List`, the complete parameter list is `action`, `path`, `filter`, `storage`,
 * `parent`, `cache`). A control wired to a parameter the server does not read
 * looks like it works and quietly changes nothing, which is worse than not
 * shipping it.
 *
 * Because the endpoint has no `limit`/`offset` either, the listing in hand IS
 * the folder — so filtering it client-side is complete for the folder, not a
 * filter over "the first page". That is exactly why these three are honest and
 * a People/owner filter is not: `nodes.owner_id` exists for quota accounting,
 * is nil for anything a sync discovered, and is serialized by nothing
 * (handlers/shared.go: "There is no per-node owner").
 */
import type { FileNode } from '../types/FileNode';
import { iconFamilyFor, type IconFamily } from './fileIcons';

export type TypeFilter =
  | 'any'
  | 'folder'
  | 'document'
  | 'spreadsheet'
  | 'presentation'
  | 'pdf'
  | 'image'
  | 'video'
  | 'audio'
  | 'archive'
  | 'code';

export type ModifiedFilter = 'any' | 'today' | '7d' | '30d' | 'year';

export type SizeFilter = 'any' | 'lt1' | '1to10' | '10to100' | 'gt100';

export interface DriveFilters {
  type: TypeFilter;
  modified: ModifiedFilter;
  size: SizeFilter;
}

export const EMPTY_FILTERS: DriveFilters = { type: 'any', modified: 'any', size: 'any' };

export function filtersActive(f: DriveFilters): boolean {
  return f.type !== 'any' || f.modified !== 'any' || f.size !== 'any';
}

export function activeFilterCount(f: DriveFilters): number {
  return (f.type !== 'any' ? 1 : 0) + (f.modified !== 'any' ? 1 : 0) + (f.size !== 'any' ? 1 : 0);
}

/** Families a type choice accepts. `iconFamilyFor` is the taxonomy the icons
 *  already use, so a row's filter group and its glyph can never disagree. */
const TYPE_FAMILIES: Record<Exclude<TypeFilter, 'any'>, IconFamily[]> = {
  folder: ['folder'],
  document: ['doc', 'text'],
  spreadsheet: ['sheet'],
  presentation: ['slides'],
  pdf: ['pdf'],
  image: ['image'],
  video: ['video'],
  audio: ['audio'],
  archive: ['archive'],
  code: ['code'],
};

/** MIME fallback — a file with no extension still has a `mime_type` from the
 *  backend sniffer, and "IMG_0042" with no suffix is a real thing people have. */
const MIME_PREFIXES: Partial<Record<Exclude<TypeFilter, 'any'>, string[]>> = {
  image: ['image/'],
  video: ['video/'],
  audio: ['audio/'],
  pdf: ['application/pdf'],
  document: ['text/plain', 'application/msword', 'application/vnd.openxmlformats-officedocument.wordprocessing'],
  spreadsheet: ['text/csv', 'application/vnd.ms-excel', 'application/vnd.openxmlformats-officedocument.spreadsheet'],
  presentation: ['application/vnd.ms-powerpoint', 'application/vnd.openxmlformats-officedocument.presentation'],
  archive: ['application/zip', 'application/x-tar', 'application/gzip', 'application/x-7z'],
};

function matchesType(n: FileNode, t: TypeFilter): boolean {
  if (t === 'any') return true;
  if (t === 'folder') return n.type === 'dir';
  if (n.type === 'dir') return false;
  if (TYPE_FAMILIES[t].includes(iconFamilyFor(n))) return true;
  const mime = (n.mime_type || '').toLowerCase();
  return !!mime && (MIME_PREFIXES[t] ?? []).some((p) => mime.startsWith(p));
}

/** `now` is a parameter so a test can pin the clock instead of sleeping. */
function matchesModified(n: FileNode, f: ModifiedFilter, now: number): boolean {
  if (f === 'any') return true;
  const ms = typeof n.last_modified === 'number' ? n.last_modified : 0;
  // No timestamp = no answer. Dropping the row would hide files whose driver
  // gave us nothing; keeping it would put them in "Today". Hiding is the
  // honest one: the row does not satisfy "modified today", it is unknown.
  if (!ms) return false;
  if (f === 'year') {
    return new Date(ms).getFullYear() === new Date(now).getFullYear();
  }
  if (f === 'today') {
    const start = new Date(now);
    start.setHours(0, 0, 0, 0);
    return ms >= start.getTime();
  }
  const days = f === '7d' ? 7 : 30;
  return ms >= now - days * 86_400_000;
}

const MB = 1024 * 1024;

function matchesSize(n: FileNode, f: SizeFilter): boolean {
  if (f === 'any') return true;
  // ⚠ Folders drop out of every size choice rather than passing through. A
  // directory row's `size` is 0 from the projector, so "under 1 MB" would
  // otherwise list every folder in the drive — an answer that looks like a
  // measurement and is not one.
  if (n.type === 'dir') return false;
  const s = typeof n.size === 'number' ? n.size : 0;
  if (f === 'lt1') return s < MB;
  if (f === '1to10') return s >= MB && s < 10 * MB;
  if (f === '10to100') return s >= 10 * MB && s < 100 * MB;
  return s >= 100 * MB;
}

export function applyFilters(
  files: FileNode[],
  f: DriveFilters,
  now: number = Date.now(),
): FileNode[] {
  // No active filter → the SAME array reference, so an unfiltered explorer
  // renders exactly what it rendered before this file existed.
  if (!filtersActive(f)) return files;
  return files.filter(
    (n) =>
      matchesType(n, f.type) && matchesModified(n, f.modified, now) && matchesSize(n, f.size),
  );
}
