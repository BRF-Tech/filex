// useThumbs — authenticated thumbnail loader for the grid view.
//
// The backend emits `thumb_url` as a ROOT-RELATIVE path ("/api/files/thumb/{id}").
// A plain `<img src>` only works for the native same-origin SPA: an embedded
// webcomponent resolves it against the HOST page's origin (work.brf.sh → 404)
// and, even with the URL fixed, `<img>` cannot carry the bearer header a
// proxied host (fishapp PWA) requires. So thumbs are fetched through the same
// auth machinery as every API call (headers + credentials), cached as object
// URLs, and handed to the grid; a failed fetch falls back to the file icon.

import { ref } from 'vue';
import type { FileNode } from '../types/FileNode';

interface ThumbApiSlice {
  authHeaders: (extra?: Record<string, string>) => Promise<Record<string, string>>;
  credentialsMode: () => RequestCredentials;
}

/** Cap the object-URL cache; beyond it oldest entries are revoked. Thumbs are
 *  small (~KBs) so this is generous while still bounding a long session. */
const MAX_CACHED = 500;

export function useThumbs(apiBase: string | undefined, api: ThumbApiSlice) {
  const urls = ref<Record<string, string>>({});
  const order: string[] = [];
  const failed = new Set<string>();
  const pending = new Set<string>();

  function resolveUrl(raw: string): string {
    if (/^https?:\/\//i.test(raw)) return raw;
    const base = (apiBase || '').replace(/\/+$/, '');
    return base && raw.startsWith('/') ? base + raw : raw;
  }

  /** Reactive: returns the loaded object URL for the node's thumb, kicking off
   *  the fetch on first sight. null = not (yet) available → show the icon. */
  function src(n: FileNode): string | null {
    const raw = n.thumb_url;
    if (!raw) return null;
    const got = urls.value[raw];
    if (got) return got;
    if (!failed.has(raw) && !pending.has(raw)) void load(raw);
    return null;
  }

  async function load(raw: string): Promise<void> {
    pending.add(raw);
    try {
      const res = await fetch(resolveUrl(raw), {
        headers: await api.authHeaders({ Accept: '*/*' }),
        credentials: api.credentialsMode(),
      });
      if (!res.ok) throw new Error(String(res.status));
      const blob = await res.blob();
      if (order.length >= MAX_CACHED) {
        const evict = order.shift();
        if (evict && urls.value[evict]) {
          URL.revokeObjectURL(urls.value[evict]);
          const next = { ...urls.value };
          delete next[evict];
          urls.value = next;
        }
      }
      order.push(raw);
      urls.value = { ...urls.value, [raw]: URL.createObjectURL(blob) };
    } catch {
      // 404 (thumb evicted server-side) or auth hiccup — icon fallback, and we
      // don't retry within this session to avoid hammering.
      failed.add(raw);
    } finally {
      pending.delete(raw);
    }
  }

  return { src };
}
