/**
 * catalogCoverage — how much of a storage its catalog covers, as the server
 * says it, and the one set of rules every view draws that by.
 *
 * filex keeps a catalog of every storage, and search, folder sizes and drive
 * usage are answered from it. While it does not cover a storage in full the
 * server says so (docs/LAZY-CATALOGUE.md → "Coverage in the UI"):
 *
 *   - `first_scan`   — any storage whose first sync has not finished;
 *   - `lazy_filling` — a lazily cataloged storage whose background pass is
 *                      still going (behaviour A);
 *   - `lazy_on_open` — a lazily cataloged storage that catalogs only the
 *                      folders people open (behaviour B).
 *
 * The listing and search responses carry it per storage in `storage_info`,
 * drive usage per row. A folder row whose own size leaves something out
 * carries `size_partial` (see useLocale `formatNodeSize`).
 *
 * ⚠ One module for the whole explorer: the banner over a listing, the banner
 * over search results and Home's drive cards read the SAME object through the
 * SAME rules, so they cannot tell a person two different things about one
 * storage.
 */

/** Why a storage's catalog is not complete. */
export type CoverageReason = 'first_scan' | 'lazy_filling' | 'lazy_on_open';

/** The server's `coverage` object (sync.CatalogueCoverage). */
export interface CatalogCoverage {
  complete: boolean;
  reason?: CoverageReason | string;
  /** `lazy` for a lazily cataloged storage. */
  mode?: string;
  /** The lazy behaviour: `background` (A) or `on_open` (B). */
  fill?: string;
  catalogued_folders?: number;
  pending_folders?: number;
  watched_folders?: number;
  max_watches?: number;
  filler?: string;
  held_back_folders?: number;
  reconciles?: number;
}

/** One entry of a listing's `storage_info`. */
export interface StorageInfo {
  name: string;
  read_only?: boolean;
  coverage?: CatalogCoverage | null;
}

/** A coverage object worth telling anybody about, or null. */
export function incomplete(c: CatalogCoverage | null | undefined): CatalogCoverage | null {
  if (!c || typeof c !== 'object' || c.complete === true) return null;
  return c;
}

/**
 * The share of the folders found so far that are cataloged, 0–99 while any
 * is still waiting (a floor, so "100%" is never shown beside a notice that
 * says the work is not done).
 */
export function coveragePercent(c: CatalogCoverage): number {
  const done = Math.max(0, c.catalogued_folders ?? 0);
  const pending = Math.max(0, c.pending_folders ?? 0);
  if (done + pending === 0) return 0;
  const pct = Math.floor((done * 100) / (done + pending));
  return pending > 0 ? Math.min(pct, 99) : pct;
}

/** The sentence one storage's coverage is told in: a catalog key and its values. */
export function coverageMessage(c: CatalogCoverage): { key: string; vars: Record<string, number> } {
  switch (c.reason) {
    case 'lazy_on_open':
      return { key: 'coverage.lazy_on_open', vars: {} };
    case 'lazy_filling':
      return { key: 'coverage.lazy_filling', vars: { pct: coveragePercent(c) } };
    default:
      return { key: 'coverage.first_scan', vars: {} };
  }
}

/** `storage_info` → storage name → its incomplete coverage (null = complete). */
export function coverageByStorage(info: unknown): Record<string, CatalogCoverage | null> {
  const out: Record<string, CatalogCoverage | null> = {};
  if (!Array.isArray(info)) return out;
  for (const row of info as StorageInfo[]) {
    if (!row || typeof row.name !== 'string' || row.name === '') continue;
    out[row.name] = incomplete(row.coverage ?? null);
  }
  return out;
}

/** What the banner over a listing or a search says, and whether it offers an action. */
export interface CoverageNotice {
  key: string;
  vars: Record<string, string | number>;
  /** The storage the notice is about, when it is about one. */
  storage?: string;
  /** Offer "Catalog everything" (an administrator, behaviour B). */
  offerCatalogAll: boolean;
}

/**
 * The notice for what is on screen.
 *
 *   - A listing, or a search inside one storage: that storage's coverage.
 *   - A search that spans every storage (the server searches them all when
 *     the query is typed at a storage's root and there is more than one):
 *     the names of the ones not fully cataloged.
 *
 * `admin` decides the action only; everybody reads the same sentence.
 */
export function coverageNotice(opts: {
  map: Record<string, CatalogCoverage | null>;
  adapter: string;
  searching: boolean;
  crossStorage: boolean;
  admin: boolean;
}): CoverageNotice | null {
  const { map, adapter, searching, crossStorage, admin } = opts;
  if (searching && crossStorage) {
    const names = Object.keys(map)
      .filter((n) => map[n] !== null)
      .sort((a, b) => a.localeCompare(b));
    if (names.length === 0) return null;
    if (names.length === 1) return single(names[0], map[names[0]] as CatalogCoverage, admin);
    return { key: 'coverage.search_some', vars: { names: names.join(', ') }, offerCatalogAll: false };
  }
  const c = map[adapter];
  return c ? single(adapter, c, admin) : null;
}

function single(storage: string, c: CatalogCoverage, admin: boolean): CoverageNotice {
  const m = coverageMessage(c);
  return { key: m.key, vars: m.vars, storage, offerCatalogAll: admin && c.reason === 'lazy_on_open' };
}
