/**
 * gorunum:v1-advsearch — the model behind the Advanced search dialog.
 *
 * Kept out of the component so the two halves of a search can be read (and
 * tested) side by side, because the honesty of the whole feature lives in the
 * split:
 *
 *   SERVER — `text`, `tags`, `excludeTags`, `scope`. These are the ONLY things
 *   filex's search endpoints actually read. `GET|POST /api/files/search` takes
 *   `q`/`query`, `storage_id`, `limit` and `scope` (name|content|all) and
 *   nothing else (handlers/search.go `searchRequest`); the query text may carry
 *   `tag:x` / `-tag:x`, which the backend parses out and resolves against the
 *   database (docs/SEARCH.md, "Filtering by tag"). The manager's
 *   `?action=search&filter=…` speaks the same query language.
 *
 *   CLIENT — everything in `filters` (`lib/fileFilters.ts`). There is no date,
 *   size, mime or path parameter on either endpoint, so those four narrow the
 *   rows that came back. That is the same bargain the filter row already makes
 *   and is documented at the top of fileFilters.ts.
 *
 * ⚠ The one thing that is NOT the same bargain: over a folder listing the rows
 * in hand ARE the folder, so a client-side filter is a complete answer. Over a
 * SEARCH result they are the first N hits the ranker returned (50 by default on
 * `/api/files/search`, 250 on the manager's search action). A size range over
 * that is a filter over what came back — true of the rows shown, not a
 * statement about the storage. The dialog prints that rather than implying a
 * full scan; see `advSearchTruncated`.
 *
 * ⚠ Owner USED to be on this list, with the reason "there is no per-node owner
 * on the wire at all". Migration 00038 made ownership a real fact on the row
 * and the listing projection carries `owner_id` / `owner_name` / `owner_self`,
 * so `filters.people` is now a client-side filter over a field the rows
 * ALREADY carry — exactly the bargain the other four make. The rule did not
 * bend; the data arrived.
 *
 * What is still NOT modelled here, because nothing could carry it:
 *   - A "paths" scope. `scope` accepts exactly name|content|all
 *     (search.ParseScope); paths are matched *within* the name scope and cannot
 *     be selected on their own. Measured against the reference build on
 *     2026-09-13: its `scope:"path"` returns the byte-identical result set its
 *     `scope:"name"` returns, over every query tried.
 *   - "Match whole phrase". The query language has no phrase operator — the
 *     only quoting the parser knows keeps a `tag:"two words"` value together
 *     (search.splitQuery/unquote), and a quoted free-text token keeps its
 *     quotes and is matched with them.
 */
import type { DriveFilters, PeopleOption } from './fileFilters';
import { EMPTY_FILTERS } from './fileFilters';

/** Which fields the backend consults. Mirrors `search.ParseScope`. */
export type AdvScope = 'name' | 'content' | 'all';

/**
 * What one live-count run reports back.
 *
 * `capped` is the honest half: true when the server returned as many hits as
 * it was allowed to, so the client-side filters narrowed a window rather than
 * the whole result set and the number is "N of the first page", not "N in the
 * storage". The dialog prints a different sentence for each.
 */
export interface AdvCountResult {
  count: number;
  capped: boolean;
  /**
   * The per-account People members the counted rows actually contained
   * (`peopleOptions(rows)` minus the three fixed ones), so the Owner select
   * can offer a name without asking for a user directory it must not have.
   *
   * ⚠ Optional. A caller that does not report them — the explorer does not
   * yet — leaves the select with `Anyone` / `You` / `System`, which is three
   * working members, not a broken menu. The field exists so the follow-up is
   * one change in the shell that owns the API client, not a second change in
   * this component.
   */
  people?: PeopleOption[];
}

export interface AdvSearchRequest {
  /** Free text. Never carries `tag:` tokens — those live in the two arrays. */
  text: string;
  /** ANDed include filters, emitted as `tag:x`. */
  tags: string[];
  /** Exclusions, emitted as `-tag:x`. Applied after the includes. */
  excludeTags: string[];
  scope: AdvScope;
  /** The client-side half. Same model and same predicate as the filter row. */
  filters: DriveFilters;
}

export function emptyAdvSearch(pathBase = ''): AdvSearchRequest {
  return {
    text: '',
    tags: [],
    excludeTags: [],
    scope: 'name',
    filters: { ...EMPTY_FILTERS, pathBase },
  };
}

/** A tag value only needs quoting when it contains whitespace — the parser
 *  splits on spaces unless a double quote is open (search.splitQuery). */
function tagToken(tag: string, negated: boolean): string {
  const v = tag.trim();
  if (!v) return '';
  const body = /\s/.test(v) ? `"${v}"` : v;
  return `${negated ? '-' : ''}tag:${body}`;
}

/**
 * The string that actually goes on the wire, as `q` / `filter`.
 *
 * Text first so the free-text half reads the way the user typed it, then the
 * tag filters. A bare `tag:x` with no text is legal and means "list the files
 * carrying that tag, newest first" — the backend says so explicitly, so an
 * empty `text` is not an empty search.
 */
export function advQueryString(req: AdvSearchRequest): string {
  const parts: string[] = [];
  const text = req.text.trim();
  if (text) parts.push(text);
  for (const t of req.tags) {
    const tok = tagToken(t, false);
    if (tok) parts.push(tok);
  }
  for (const t of req.excludeTags) {
    const tok = tagToken(t, true);
    if (tok) parts.push(tok);
  }
  return parts.join(' ');
}

/** Nothing for the server to answer — no text and no tag filter. */
export function advSearchEmpty(req: AdvSearchRequest): boolean {
  return advQueryString(req) === '';
}

/**
 * Split a comma/newline separated tag box into values.
 *
 * Commas rather than spaces, because a tag may contain a space
 * (`quarterly report`) and splitting on whitespace would turn one filter that
 * matches into two that do not — and a tag that does not exist returns the
 * empty set by design, so the mistake would look like "there is nothing here".
 */
export function parseTagList(raw: string): string[] {
  return raw
    .split(/[,\n]/)
    .map((s) => s.trim())
    .filter((s) => s !== '');
}

/**
 * True when the hit list came back at the cap, so the client-side filters ran
 * over a window rather than over everything that matches.
 *
 * The dialog and the count line say this out loud. A count that silently
 * describes "the first 50 hits, then filtered" as "N matching items" is the
 * exact shape of a number that looks measured and is not.
 */
export function advSearchTruncated(returned: number, limit: number): boolean {
  return returned >= limit;
}
