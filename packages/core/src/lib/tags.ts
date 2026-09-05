/**
 * tags.ts — the list of tags that exist, for the navigation panel.
 *
 * `GET /api/files/manager/tags/all` is one query per call, and the panel is
 * rendered by every mounted explorer: the web app, the desktop app and every
 * embed on a page (work.example.com renders two side by side). Asking on each mount
 * would multiply a database scan by however many explorers a host happens to
 * put on screen, for a list that changes when somebody edits a tag — i.e.
 * rarely.
 *
 * So: a MODULE-level cache, shared by every instance in the page.
 *   - in-flight requests are deduped, so N explorers mounting in the same tick
 *     produce ONE request;
 *   - the answer is reused for TTL_MS;
 *   - `invalidateTagCache()` drops it the moment tags are written, so the
 *     panel is never stale after the user's own edit — which is the only
 *     staleness a user can actually notice.
 *
 * Keyed by apiBase: the desktop app can point at a different server between
 * mounts and must not be handed the previous server's tags.
 */

const TTL_MS = 60_000;

interface CacheEntry {
  at: number;
  tags: string[];
}

const cache = new Map<string, CacheEntry>();
const inflight = new Map<string, Promise<string[]>>();

export interface TagListOptions {
  apiBase?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  authCredentials?: RequestCredentials;
  /** Skip the cache (after a tag edit). */
  force?: boolean;
}

/** Drop every cached tag list. Called after a successful tag write. */
export function invalidateTagCache(): void {
  cache.clear();
  inflight.clear();
}

/**
 * Every distinct tag, alphabetical, as the backend returns them. Never
 * throws: an older backend with no such route, or a caller with no
 * permission, gets an empty list and the panel simply shows no tag section.
 */
export async function fetchAllTags(opts: TagListOptions = {}): Promise<string[]> {
  const base = opts.apiBase ?? '';
  const key = base || '(same-origin)';
  if (!opts.force) {
    const hit = cache.get(key);
    if (hit && Date.now() - hit.at < TTL_MS) return hit.tags;
    const pending = inflight.get(key);
    if (pending) return pending;
  }
  const run = (async () => {
    try {
      const res = await fetch(`${base}/api/files/manager/tags/all`, {
        headers: await (opts.authHeaders ?? (() => ({})))(),
        credentials: opts.authCredentials ?? 'same-origin',
      });
      if (!res.ok) return [];
      const body = await res.json();
      const tags: string[] = Array.isArray(body?.tags)
        ? body.tags.filter((x: unknown): x is string => typeof x === 'string' && x !== '')
        : [];
      cache.set(key, { at: Date.now(), tags });
      return tags;
    } catch {
      return [];
    } finally {
      inflight.delete(key);
    }
  })();
  inflight.set(key, run);
  return run;
}

/**
 * Nodes carrying `tag`, as raw node rows (`{id, path, name, type, …}`) — the
 * same shape `star/list` and `recent` answer with, so the caller maps them
 * through its own `nodeRowToFileNode`.
 */
export async function fetchTaggedRows(
  tag: string,
  opts: TagListOptions = {},
  limit = 200,
): Promise<Record<string, unknown>[]> {
  const base = opts.apiBase ?? '';
  const res = await fetch(
    `${base}/api/files/manager/tagged?tag=${encodeURIComponent(tag)}&limit=${limit}`,
    {
      headers: await (opts.authHeaders ?? (() => ({})))(),
      credentials: opts.authCredentials ?? 'same-origin',
    },
  );
  if (!res.ok) throw new Error(String(res.status));
  const body = await res.json();
  return Array.isArray(body?.nodes) ? body.nodes : [];
}
