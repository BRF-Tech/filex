/**
 * #117 — where a grant is, in words.
 *
 * A grant on a storage's ROOT (path prefix "") is the whole storage: how an
 * administrator gives a storage to a person or a group. Printed as it is
 * stored it read as "/" (a path nobody typed) or `alpha://` (an address), on
 * the one page whose job is to say who has what, where. Every admin table
 * that lists grants - Folder access, a group's folders - names it here, so
 * they say it the same way.
 */
type Translate = (key: string, values?: Record<string, unknown>) => string;

export interface GrantPlaceLike {
  storage_name: string;
  path_prefix: string;
}

/** A grant on the storage's root: the whole storage. */
export function isWholeStorage(g: Pick<GrantPlaceLike, 'path_prefix'>): boolean {
  return (g.path_prefix ?? '').replace(/^\/+|\/+$/g, '') === '';
}

/** The place, for a column of its own or a sentence: "alpha (whole
 *  storage)", else `alpha://Team/Reports`. */
export function grantPlace(g: GrantPlaceLike, t: Translate): string {
  return isWholeStorage(g)
    ? t('grants.wholeStorageOf', { storage: g.storage_name })
    : `${g.storage_name}://${g.path_prefix.replace(/^\/+/, '')}`;
}
