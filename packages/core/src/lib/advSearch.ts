/**
 * gorunum:v1-advsearch — the model behind the Advanced search dialog.
 *
 * ⚠⚠ 0.54 (task #207, audit D6): the split described below is HISTORY. Every
 * choice in `filters` now travels to the server as a parameter
 * (`advFilterParams`: type, modified_after/before, min/max_size, under,
 * not_under, owner, hidden) and the server applies it to each candidate
 * BEFORE it cuts its page, on `/api/files/search` and on the manager's
 * `action=search` alike, and MCP `file_search` and the CLI take the same
 * parameters. The browser no longer narrows search hits; the count is the
 * server's rows and its `truncated` says whether the page was cut.
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
import { EMPTY_FILTERS, modifiedWindow, sizeBounds } from './fileFilters';
import { splitList } from './listInput';

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

/**
 * The narrowing as the server reads it (internal/nodefilter Parse): one
 * parameter per choice, absent when the choice narrows nothing. Dates are the
 * viewer's own clock turned into epoch milliseconds here - "today" is their
 * midnight, which only the browser knows - and the server compares them.
 *
 * `showHidden` is the explorer's "show hidden files" choice: off sends
 * `hidden=false`, so dot names are dropped before the page is cut rather than
 * after (a page of 250 hits could otherwise shrink to a handful).
 */
export function advFilterParams(
  f: DriveFilters,
  now: number = Date.now(),
  opts: { showHidden?: boolean } = {},
): Record<string, string> {
  const out: Record<string, string> = {};
  if (f.type && f.type !== 'any') out.type = f.type === 'folder' ? 'dir' : f.type;
  const w = modifiedWindow(f, now);
  if (w?.after !== undefined) out.modified_after = String(Math.floor(w.after));
  if (w?.before !== undefined) out.modified_before = String(Math.floor(w.before));
  const b = sizeBounds(f);
  if (b?.min !== undefined) out.min_size = String(Math.max(0, Math.floor(b.min)));
  if (b?.max !== undefined) out.max_size = String(Math.max(0, Math.floor(b.max)));
  const base = (f.pathBase ?? '').replace(/\/+$/, '');
  if (base && f.pathMode === 'here') out.under = base;
  if (base && f.pathMode === 'skip') out.not_under = base;
  const who = f.people ?? 'any';
  if (who === 'me' || who === 'system') out.owner = who;
  else if (who.startsWith('u:')) out.owner = who.slice(2);
  if (opts.showHidden === false) out.hidden = 'false';
  return out;
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
  // ⚠ lib/listInput: "،" and "，" are commas too (an Arabic or Chinese keyboard).
  return splitList(raw, { newlines: true });
}

/**
 * True when the hit list was cut, so the client-side filters ran over a window
 * rather than over everything that matches.
 *
 * The dialog and the count line say this out loud. A count that silently
 * describes "the first 50 hits, then filtered" as "N matching items" is the
 * exact shape of a number that looks measured and is not.
 *
 * `serverSaid` is the response's own `truncated` flag, and when the server
 * sends one it is the answer. Only the server knows: without the index the
 * manager reads a window of names several times the page size, so 400 rows can
 * be a complete answer and 12 a cut one (the window was full and only 12 of
 * its rows matched every word). A server too old to say leaves the guess this
 * function always made — a full page is a cut page.
 */
export function advSearchTruncated(returned: number, limit: number, serverSaid?: boolean): boolean {
  // (0.54: the server narrows before it cuts, so its flag covers the
  // narrowing too.)
  if (typeof serverSaid === 'boolean') return serverSaid;
  return returned >= limit;
}
