// useThumbs — authenticated thumbnail loader for the folder views.
//
// The backend emits `thumb_url` as a ROOT-RELATIVE path ("/api/files/thumb/{id}").
// A plain `<img src>` only works for the native same-origin SPA: an embedded
// webcomponent resolves it against the HOST page's origin (work.example.com → 404)
// and, even with the URL fixed, `<img>` cannot carry the bearer header a
// proxied host (fishapp PWA) requires. So thumbs are fetched through the same
// auth machinery as every API call (headers + credentials), cached as object
// URLs, and handed to the views; a failed fetch falls back to the file icon.
//
// ⚠⚠ An arrival wakes ONE tile. The object URLs used to live in one reactive
// record that was REPLACED as each thumbnail arrived, so every arrival woke
// everything that had read any thumbnail — the whole folder view, which read
// them all. Field report: a folder of 344 files (~240 thumbnails) re-rendered
// itself ~240 times in a few seconds; a fast Mac lost ~4 s, a slower Windows PC
// froze Edge for ~30 s and ran a Chrome tab out of memory. The cache is now a
// reactive Map read per key, and the views read it through ThumbTile (one
// component per tile), so an arrival re-renders the tile that shows it.
//
// ⚠ Keyed by picture, not by URL. `thumb_url` carries a signature whose expiry
// moves every hour; keyed by URL, the first listing after the hour turned
// fetched every thumbnail of the folder again. The key is the URL's path plus
// the file's `last_modified`: a rotated signature is the same picture, an edited
// file is not.

import { shallowReactive, toRaw } from 'vue';
import type { FileNode } from '../types/FileNode';

interface ThumbApiSlice {
  authHeaders: (extra?: Record<string, string>) => Promise<Record<string, string>>;
  credentialsMode: () => RequestCredentials;
}

/** Cap the object-URL cache; beyond it oldest entries are revoked. Thumbs are
 *  small (~KBs) so this is generous while still bounding a long session. */
const MAX_CACHED = 500;

/** The cache key for a node's thumbnail — null when it has none. */
function thumbKey(n: FileNode): string | null {
  const raw = n.thumb_url;
  if (!raw) return null;
  const q = raw.indexOf('?');
  return `${q === -1 ? raw : raw.slice(0, q)}|${n.last_modified ?? ''}`;
}

export function useThumbs(apiBase: string | undefined, api: ThumbApiSlice) {
  const cache = shallowReactive(new Map<string, string>());
  const order: string[] = [];
  /** Signed URLs that failed. Per URL, not per key: 404 (thumb evicted
   *  server-side) or an auth hiccup is not retried within the session, but a
   *  freshly signed URL for the same picture may be tried once. */
  const failed = new Set<string>();
  /** Keys being fetched — one request per picture however many URLs name it. */
  const pending = new Set<string>();

  function resolveUrl(raw: string): string {
    if (/^https?:\/\//i.test(raw)) return raw;
    const base = (apiBase || '').replace(/\/+$/, '');
    return base && raw.startsWith('/') ? base + raw : raw;
  }

  /** Reactive, per node: returns the loaded object URL for the node's thumb,
   *  kicking off the fetch on first sight. null = not (yet) available → show
   *  the icon. Only what read THIS node's key is woken when it arrives. */
  function src(n: FileNode): string | null {
    const raw = n.thumb_url;
    const key = thumbKey(n);
    if (!raw || !key) return null;
    const got = cache.get(key);
    if (got) return got;
    if (!failed.has(raw) && !pending.has(key)) void load(raw, key);
    return null;
  }

  async function load(raw: string, key: string): Promise<void> {
    pending.add(key);
    try {
      const res = await fetch(resolveUrl(raw), {
        headers: await api.authHeaders({ Accept: '*/*' }),
        credentials: api.credentialsMode(),
      });
      if (!res.ok) throw new Error(String(res.status));
      const blob = await res.blob();
      if (order.length >= MAX_CACHED) {
        // ⚠ Dropped without a word to the tile still showing it: woken, the
        // tile would fetch it again, and that arrival would drop the next
        // oldest — past MAX_CACHED thumbnails on screen, a loop that never
        // ends. Its <img> has long loaded, and a loaded image outlives its
        // revoked URL; a tile drawn later asks again, once.
        const evict = order.shift();
        const store = toRaw(cache);
        const old = evict ? store.get(evict) : undefined;
        if (evict && old) {
          URL.revokeObjectURL(old);
          store.delete(evict);
        }
      }
      order.push(key);
      cache.set(key, URL.createObjectURL(blob));
    } catch {
      failed.add(raw);
    } finally {
      pending.delete(key);
    }
  }

  return { src };
}
