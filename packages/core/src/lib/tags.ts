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

/**
 * The two kinds of tag (v0.43.0, the owner's decision "tagler personal ve team
 * olarak ikiye ayrılır"):
 *
 *   personal — the person's own label, like a star. Nobody else sees it.
 *   team     — shared with everyone in the tenant who can see the file;
 *              adding or removing one needs edit permission on the file.
 *
 * ⚠ Before v0.43.0 there was one kind and it was shared with EVERY account on
 * the server while the code called it per-user — a tester's "müşteri teklifi"
 * showed in other people's panels and they could remove it. A tag on screen
 * therefore always says which kind it is (`TagKindIcon` + the words), so
 * nobody has to guess who can see it.
 */
export type TagKind = 'personal' | 'team';

/** A tag as the server hands it out: its name as typed and its kind. */
export interface TagItem {
  name: string;
  kind: TagKind;
}

export function isTagKind(v: unknown): v is TagKind {
  return v === 'personal' || v === 'team';
}

/**
 * Whether two names are one tag, the way the server decides it
 * (`internal/tagname.Key`): whitespace collapsed, NFC, the four Latin i's —
 * I, ı, İ, i — as one letter, then case-insensitive. The server is the
 * authority; the client uses this only so the picker does not offer to add a
 * tag the file already carries under different capitals.
 *
 * ⚠ The i's are mapped BEFORE lower-casing: JS lower-cases `İ` to "i" + U+0307 (a
 * combining dot) and leaves `ı` alone, so `toLowerCase` alone would call
 * "IŞIK" and "ışık" two tags — the Turkish case the server folds together.
 */
export function tagKey(name: string): string {
  return String(name ?? '')
    .normalize('NFC')
    .trim()
    .replace(/\s+/g, ' ')
    .replace(/[Iıİ]/g, 'i')
    .toLowerCase();
}

/**
 * The items in a tag answer. A v0.43+ server sends `items: [{name, kind}]`; an
 * older one sends only `tags: string[]` — and on an older server every tag was
 * shared with everybody, so those are TEAM tags. Anything malformed is dropped.
 */
export function tagItemsOf(body: unknown): TagItem[] {
  const b = (body ?? {}) as { items?: unknown; tags?: unknown };
  if (Array.isArray(b.items)) {
    return b.items.filter(
      (x: unknown): x is TagItem =>
        !!x &&
        typeof (x as TagItem).name === 'string' &&
        (x as TagItem).name !== '' &&
        isTagKind((x as TagItem).kind),
    );
  }
  if (Array.isArray(b.tags)) {
    return b.tags
      .filter((x: unknown): x is string => typeof x === 'string' && x !== '')
      .map((name) => ({ name, kind: 'team' as const }));
  }
  return [];
}

interface CacheEntry {
  at: number;
  tags: TagItem[];
}

const cache = new Map<string, CacheEntry>();
const inflight = new Map<string, Promise<TagItem[]>>();

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
 * etiket:k2 — "the tags changed", told to every explorer on the page.
 *
 * ⚠⚠ This used to travel as a component EVENT (TagPicker `change` → the
 * explorer's `onNodeTagsChanged`), and Vue drops an event emitted by a
 * component that has already been unmounted. The save is asynchronous, so a
 * person who added a tag and closed the dialog before the server answered —
 * a fraction of a second on a busy server — saved the tag and kept the panel
 * that said "No tags yet". Measured: the full Playwright run (a loaded
 * server) showed exactly that, while the spec alone (an idle one) passed.
 * A module-level announcement does not depend on the component still being
 * there, and it reaches EVERY explorer on the page, not only the one that
 * hosted the picker.
 */
const changeListeners = new Set<() => void>();

/** Listen for tag writes; returns the function that stops listening. */
export function onTagsChanged(fn: () => void): () => void {
  changeListeners.add(fn);
  return () => {
    changeListeners.delete(fn);
  };
}

/** A tag write succeeded: drop the cache, then tell every listener. The
 *  listeners re-ask WITHOUT forcing, so N explorers still cost one request
 *  (the first creates it, the rest join it in flight). */
export function announceTagsChanged(): void {
  invalidateTagCache();
  for (const fn of [...changeListeners]) {
    try {
      fn();
    } catch {
      /* one listener's failure must not silence the others */
    }
  }
}

/**
 * Every tag the person can see — both kinds — alphabetical, as the backend
 * returns them. Never throws: an older backend with no such route, or a
 * caller with no permission, gets an empty list and the panel simply shows
 * no tag section.
 */
export async function fetchAllTags(opts: TagListOptions = {}): Promise<TagItem[]> {
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
      const tags = tagItemsOf(await res.json());
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
 * through its own `nodeRowToFileNode`. `kind` narrows to one kind; '' = both
 * (what a `#.tag~x` link from before v0.43 means).
 */
export async function fetchTaggedRows(
  tag: string,
  opts: TagListOptions = {},
  limit = 200,
  kind: TagKind | '' = '',
): Promise<Record<string, unknown>[]> {
  const base = opts.apiBase ?? '';
  const k = kind ? `&kind=${kind}` : '';
  const res = await fetch(
    `${base}/api/files/manager/tagged?tag=${encodeURIComponent(tag)}&limit=${limit}${k}`,
    {
      headers: await (opts.authHeaders ?? (() => ({})))(),
      credentials: opts.authCredentials ?? 'same-origin',
    },
  );
  if (!res.ok) throw new Error(String(res.status));
  const body = await res.json();
  return Array.isArray(body?.nodes) ? body.nodes : [];
}
