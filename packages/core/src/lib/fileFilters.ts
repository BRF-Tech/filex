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
 * filter over "the first page".
 *
 * ⚠ This file used to end "…and a People/owner filter is not, because
 * `nodes.owner_id` exists for quota accounting and is serialized by nothing".
 * That was true and is no longer: migration 00038 made ownership a real fact
 * on the row (who put it here, who touched it last, whether it arrived through
 * an anonymous drop link), and the listing projection now carries `owner_id`,
 * `owner_name` and `owner_self` on every row that has an owner. The People
 * filter therefore answers from a field the row ALREADY carries, exactly like
 * the other four — the rule did not bend, the data arrived.
 *
 * ⚠ A row with NO owner key is SYSTEM: nobody put it there through filex. That
 * is the honest word for ownerless and it is a real choice in the pill, not a
 * blank.
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

export type ModifiedFilter = 'any' | 'today' | '7d' | '30d' | 'year' | 'around';

export type SizeFilter = 'any' | 'lt1' | '1to10' | '10to100' | 'gt100' | 'range';

/** gorunum:v1-advsearch — how wide "around" reaches on each side of the anchor. */
export type AroundSpan = 'h1' | 'd1' | 'w1';

/** gorunum:v1-advsearch — what the path choice does to a row outside `pathBase`. */
export type PathMode = 'any' | 'here' | 'skip';

/**
 * Who a row belongs to. `any` is the neutral member every pill group needs;
 * `me` is the signed-in account, `system` is an ownerless row, and `u:<id>`
 * names one of the other accounts that actually appear in this listing.
 *
 * ⚠ The account members are NOT a fixed list. Offering every account on the
 * install would put names in the menu that cannot narrow anything here and
 * would leak the user directory into a folder view; `peopleOptions` derives
 * the choices from the rows in hand.
 */
export type PeopleFilter = 'any' | 'me' | 'system' | `u:${number}`;

export interface DriveFilters {
  type: TypeFilter;
  modified: ModifiedFilter;
  size: SizeFilter;
  /**
   * gorunum:v1 — "Filter in this folder…", the input the filter row carries
   * beside the chips. A substring of the NAME, matched over the rows in hand.
   *
   * ⚠ Not a second search box. The header field runs `action=search` on the
   * server and REPLACES the listing with hits (`isSearchResult`, results carry
   * their parent path and can come from subfolders); this one never leaves the
   * folder you are looking at and never issues a request — it narrows what is
   * already on screen, the same rows the chips narrow, which is why it belongs
   * to the same model and not to `searchQuery`.
   *
   * Optional so every existing `{ type, modified, size }` literal — embedders
   * included — still type-checks and behaves exactly as before.
   */
  name?: string;

  /* ── gorunum:v1-advsearch — the three the advanced-search dialog adds ─────
   *
   * All optional, so every existing `{ type, modified, size }` literal still
   * type-checks and behaves exactly as it did — the same contract `name` was
   * added under. They live HERE and not in a second model because the filter
   * row and the dialog narrow the same rows with the same predicate: two
   * definitions of "modified in the last 7 days" is two products.
   *
   * ⚠ They are client-side for the same reason the three above are, and the
   * reason is stated at the top of this file: the listing and search endpoints
   * read no date, size or path parameter. What makes them honest is that the
   * rows in hand are the complete answer for a FOLDER listing; over a SEARCH
   * result they narrow the hits the server returned, which is capped
   * (`/api/files/search` limit, the manager search's 250) — so a size range
   * over a truncated hit list is a filter over what came back, not over the
   * storage. The dialog says so rather than implying a full scan. */

  /** Anchor for `modified: 'around'`. `YYYY-MM-DD` or `YYYY-MM-DDTHH:mm`,
   *  read as LOCAL time (a date alone means that day's 00:00 local). */
  aroundDate?: string;
  /** Half-width of the `around` window. */
  aroundSpan?: AroundSpan;
  /** Inclusive floor for `size: 'range'`, in BYTES. null = open-ended. */
  sizeMin?: number | null;
  /** Inclusive ceiling for `size: 'range'`, in BYTES. null = open-ended. */
  sizeMax?: number | null;
  /** `here` keeps only rows under `pathBase`; `skip` keeps only rows outside it. */
  pathMode?: PathMode;
  /**
   * Who the row belongs to. Optional, under the same contract as `name` and
   * the advanced-search three: every existing `{ type, modified, size }`
   * literal still type-checks and behaves exactly as before.
   */
  people?: PeopleFilter;

  /** Adapter-qualified folder the path choice is measured against, e.g.
   *  `qldemo://Documents`. Captured when the dialog opens, because a search
   *  rebases the explorer to the storage root and the folder the user meant
   *  would otherwise be gone by the time the filter runs. */
  pathBase?: string;
}

export const EMPTY_FILTERS: DriveFilters = {
  type: 'any',
  modified: 'any',
  size: 'any',
  name: '',
  aroundDate: '',
  aroundSpan: 'd1',
  sizeMin: null,
  sizeMax: null,
  pathMode: 'any',
  pathBase: '',
  people: 'any',
};

/** Case- and accent-folded, so "İstanbul" answers to "ist" and "Ödev" to "od".
 *  ⚠ `toLowerCase()` alone maps `İ` to `i` + a combining dot, which then
 *  matches nothing the user typed; stripping the marks is what makes the two
 *  sides comparable. */
function fold(s: string): string {
  return s
    .normalize('NFD')
    .replace(/\p{M}+/gu, '')
    .toLowerCase();
}

/** The trimmed needle, or '' when the input is empty/whitespace. */
export function nameNeedle(f: DriveFilters): string {
  return (f.name ?? '').trim();
}

/** gorunum:v1-advsearch — `here`/`skip` only mean something with a base to
 *  measure against; without one the choice is inert and must not count as an
 *  active filter, or the empty state would blame a filter that filters nothing. */
function pathActive(f: DriveFilters): boolean {
  return (f.pathMode ?? 'any') !== 'any' && !!(f.pathBase ?? '');
}

/** The People choice, defaulted. Kept in one place so the four call sites
 *  below cannot disagree about what "no choice" means. */
function peopleOf(f: DriveFilters): PeopleFilter {
  return f.people ?? 'any';
}

export function filtersActive(f: DriveFilters): boolean {
  return (
    f.type !== 'any' ||
    f.modified !== 'any' ||
    f.size !== 'any' ||
    nameNeedle(f) !== '' ||
    pathActive(f) ||
    peopleOf(f) !== 'any'
  );
}

export function activeFilterCount(f: DriveFilters): number {
  return (
    (f.type !== 'any' ? 1 : 0) +
    (f.modified !== 'any' ? 1 : 0) +
    (f.size !== 'any' ? 1 : 0) +
    (nameNeedle(f) !== '' ? 1 : 0) +
    (pathActive(f) ? 1 : 0) +
    (peopleOf(f) !== 'any' ? 1 : 0)
  );
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
/** gorunum:v1-advsearch — half-width of each "around" window, in ms. */
const AROUND_MS: Record<AroundSpan, number> = {
  h1: 3_600_000,
  d1: 86_400_000,
  w1: 7 * 86_400_000,
};

/**
 * gorunum:v1-advsearch — read the anchor as LOCAL time.
 *
 * ⚠ `Date.parse('2026-09-12')` is UTC by spec while
 * `Date.parse('2026-09-12T00:00')` is local, so a bare date would slide the
 * whole window by the viewer's offset — in Istanbul (UTC+3) a ±1 hour search
 * "around the 12th" would have covered 03:00, not midnight. Appending the time
 * makes both forms mean the same clock the user is reading.
 */
function parseAnchor(raw: string | undefined): number | null {
  const s = (raw ?? '').trim();
  if (!s) return null;
  const ms = Date.parse(s.includes('T') ? s : `${s}T00:00`);
  return Number.isNaN(ms) ? null : ms;
}

function matchesModified(n: FileNode, g: DriveFilters, now: number): boolean {
  const f = g.modified;
  if (f === 'any') return true;
  const ms = typeof n.last_modified === 'number' ? n.last_modified : 0;
  // No timestamp = no answer. Dropping the row would hide files whose driver
  // gave us nothing; keeping it would put them in "Today". Hiding is the
  // honest one: the row does not satisfy "modified today", it is unknown.
  if (!ms) return false;
  if (f === 'around') {
    const anchor = parseAnchor(g.aroundDate);
    // No anchor typed yet: "around nothing" is not a window, so the choice is
    // inert rather than empty. Narrowing to zero rows the moment the user
    // picks the mode — before they have picked a date — reads as a broken
    // search, not as an unfinished one.
    if (anchor === null) return true;
    return Math.abs(ms - anchor) <= AROUND_MS[g.aroundSpan ?? 'd1'];
  }
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

function matchesSize(n: FileNode, g: DriveFilters): boolean {
  const f = g.size;
  if (f === 'any') return true;
  // ⚠ Folders drop out of every size choice rather than passing through. A
  // directory row's `size` is 0 from the projector, so "under 1 MB" would
  // otherwise list every folder in the drive — an answer that looks like a
  // measurement and is not one.
  if (n.type === 'dir') return false;
  const s = typeof n.size === 'number' ? n.size : 0;
  if (f === 'range') {
    // Either end may be left open. Both open = "any size" wearing another
    // name, which is exactly what an untouched custom range is.
    const lo = typeof g.sizeMin === 'number' ? g.sizeMin : null;
    const hi = typeof g.sizeMax === 'number' ? g.sizeMax : null;
    if (lo !== null && s < lo) return false;
    if (hi !== null && s > hi) return false;
    return true;
  }
  if (f === 'lt1') return s < MB;
  if (f === '1to10') return s >= MB && s < 10 * MB;
  if (f === '10to100') return s >= 10 * MB && s < 100 * MB;
  return s >= 100 * MB;
}

/**
 * gorunum:v1 — the name input's predicate, over a bare STRING.
 *
 * ⚠ Exported over a string rather than over a `FileNode`, because the name box
 * now narrows things that are not nodes: `surucu:d1-scope` puts the same box on
 * the multi-storage root (rows ARE nodes there, synthesized ones) and on Home,
 * whose storage cards are `HomeStorage` records with a `label`. Case- and
 * accent-folding is the part that must not be re-typed — a second `toLowerCase`
 * somewhere else is how "Ödev" stops answering to "od" on one surface and keeps
 * answering on another.
 */
export function nameMatches(name: string, needle: string): boolean {
  if (!needle) return true;
  return fold(name || '').includes(fold(needle));
}

/** The same predicate over a listing row. Folders take part like any other row:
 *  hiding the folder whose name you just typed would be the one result you
 *  meant. */
function matchesName(n: FileNode, needle: string): boolean {
  return nameMatches(n.basename || '', needle);
}

/**
 * gorunum:v1-advsearch — "only in this folder" / "skip this folder".
 *
 * Both sides are answered from `path`, which every search hit already carries
 * adapter-qualified (`qldemo://Documents/report.sql`). The base is compared
 * with a trailing slash so `Documents` cannot claim `Documents-old`, and the
 * folder row itself counts as inside it.
 */
function matchesPath(n: FileNode, g: DriveFilters): boolean {
  const mode = g.pathMode ?? 'any';
  const base = (g.pathBase ?? '').replace(/\/+$/, '');
  if (mode === 'any' || !base) return true;
  const p = n.path || '';
  const inside = p === base || p.startsWith(`${base}/`);
  return mode === 'here' ? inside : !inside;
}

/* ── Ownership, read off the row ──────────────────────────────────────────
 *
 * The backend omits every owner key it has nothing to say about, so a SYSTEM
 * row simply has no `owner_id` — which is why these read through the FileNode
 * index signature rather than named fields, the same way `snippet` and
 * `matched` are read in ListView. */

/** The row's owner id, or null for a SYSTEM row (nobody put it here). */
export function ownerIdOf(n: FileNode): number | null {
  const raw = (n as Record<string, unknown>).owner_id;
  return typeof raw === 'number' && raw > 0 ? raw : null;
}

/** True when the signed-in account owns this row. Answered by the SERVER
 *  (`owner_self`), because the embeddable core has no idea which filex account
 *  the host's session belongs to. */
export function ownedByViewer(n: FileNode): boolean {
  return (n as Record<string, unknown>).owner_self === true;
}

/** The owner's display name, or '' for a system row / a name the server could
 *  not resolve. The caller decides what to show instead. */
export function ownerNameOf(n: FileNode): string {
  const raw = (n as Record<string, unknown>).owner_name;
  return typeof raw === 'string' ? raw : '';
}

/** True when the row arrived through an anonymous drop link. The OWNER is
 *  still the person who created the link — this only says they did not put it
 *  there themselves. */
export function arrivedFromOutside(n: FileNode): boolean {
  return (n as Record<string, unknown>).external_upload === true;
}

/** One entry of the People pill. `name` is present only for the `u:<id>`
 *  members — the three fixed ones are named by the locale, not by data. */
export interface PeopleOption {
  value: PeopleFilter;
  name?: string;
}

/**
 * The People pill's menu for a given listing: `any`, `me`, `system`, and then
 * every OTHER account that actually owns something on screen, by name.
 *
 * ⚠ `me` and `system` are offered unconditionally, and that is deliberate:
 * a menu whose entries appear and disappear as you navigate is a menu you
 * cannot learn. The per-account entries are the ones that would be inert
 * elsewhere, so they are the ones derived from the rows.
 */
export function peopleOptions(files: FileNode[]): PeopleOption[] {
  const out: PeopleOption[] = [{ value: 'any' }, { value: 'me' }, { value: 'system' }];
  const seen = new Set<number>();
  for (const n of files) {
    const id = ownerIdOf(n);
    if (id === null || ownedByViewer(n) || seen.has(id)) continue;
    seen.add(id);
    out.push({ value: `u:${id}`, name: ownerNameOf(n) });
  }
  // Stable, readable order for the derived half; the fixed three stay put.
  const fixed = out.slice(0, 3);
  const rest = out
    .slice(3)
    .sort((a, b) => (a.name ?? '').localeCompare(b.name ?? '', undefined, { sensitivity: 'base' }));
  return [...fixed, ...rest];
}

/** Rows belonging to the chosen person. A folder takes part like any other
 *  row: somebody made it, and "show me what Ada put here" that silently keeps
 *  everyone's folders would be answering a different question. */
function matchesPeople(n: FileNode, f: DriveFilters): boolean {
  const want = peopleOf(f);
  if (want === 'any') return true;
  if (want === 'me') return ownedByViewer(n);
  if (want === 'system') return ownerIdOf(n) === null;
  const id = Number(want.slice(2));
  return Number.isFinite(id) && ownerIdOf(n) === id;
}

export function applyFilters(
  files: FileNode[],
  f: DriveFilters,
  now: number = Date.now(),
): FileNode[] {
  // No active filter → the SAME array reference, so an unfiltered explorer
  // renders exactly what it rendered before this file existed.
  if (!filtersActive(f)) return files;
  const needle = nameNeedle(f);
  return files.filter(
    (n) =>
      matchesType(n, f.type) &&
      matchesModified(n, f, now) &&
      matchesSize(n, f) &&
      matchesName(n, needle) &&
      matchesPath(n, f) &&
      matchesPeople(n, f),
  );
}
