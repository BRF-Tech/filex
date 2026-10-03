/**
 * useFolderPeek — "what is in this folder?" on hover (docs/thumbnails.md →
 * Folder previews).
 *
 * A pointer that rests on a folder for PEEK_DELAY_MS gets a quiet list of
 * what is inside it (FolderPeek): how many things, and the first few with
 * their thumbnails. It is a glance, not a way in: nothing in it is clickable,
 * and it goes away the moment the pointer leaves, a button is pressed, the
 * page scrolls or a drag starts.
 *
 * ⚠ Mouse only. A touch "hover" is the first half of a tap, and a list that
 * appears under a finger on its way to open the folder is in the way.
 *
 * The folder is listed through the pane's own `index` (the API every listing
 * goes through), so what the peek shows is what opening the folder shows, and
 * the listing hands its missing thumbnails to the server's refresher as any
 * listing does. Answers are kept for PEEK_TTL_MS per folder.
 *
 * An administrator can turn folder previews off (Settings; the explorer reads
 * it from capabilities `folder_previews`): `enabled` then answers false and a
 * resting pointer shows nothing, as on the folder cards.
 */
import { shallowRef } from 'vue';
import type { FileNode } from '../types/FileNode';
import { byFoldersFirst } from '../lib/listing';

export const PEEK_DELAY_MS = 500;
export const PEEK_TTL_MS = 30_000;
/** How many entries the peek names; the rest is a count. */
export const PEEK_ROWS = 8;

export interface FolderPeekState {
  node: FileNode;
  /** The card or row it hangs from, measured when it opened. */
  anchor: DOMRect;
  loading: boolean;
  items: FileNode[];
  total: number;
}

export function useFolderPeek(
  index: (path: string) => Promise<{ files?: FileNode[] }>,
  /** Whether folder previews are on; read at every enter. Absent: on. */
  enabled: () => boolean = () => true,
) {
  const state = shallowRef<FolderPeekState | null>(null);
  const cache = new Map<string, { at: number; items: FileNode[]; total: number }>();
  let timer: ReturnType<typeof setTimeout> | undefined;
  /** Bumped on every enter and leave: an answer for an older hover is dropped. */
  let token = 0;

  async function listed(path: string): Promise<{ items: FileNode[]; total: number }> {
    const hit = cache.get(path);
    if (hit && Date.now() - hit.at < PEEK_TTL_MS) return hit;
    const resp = await index(path);
    const files = [...(resp.files ?? [])].sort(byFoldersFirst);
    const got = { at: Date.now(), items: files.slice(0, PEEK_ROWS), total: files.length };
    cache.set(path, got);
    return got;
  }

  /** The pointer came to rest on `node` (a folder) over `el`. */
  function enter(node: FileNode, el: Element | null | undefined, pointerType = 'mouse') {
    if (node.type !== 'dir' || pointerType !== 'mouse' || !el || !enabled()) return;
    const my = ++token;
    clearTimeout(timer);
    timer = setTimeout(async () => {
      if (my !== token) return;
      const anchor = el.getBoundingClientRect();
      state.value = { node, anchor, loading: true, items: [], total: 0 };
      try {
        const got = await listed(node.path);
        if (my !== token) return;
        state.value = { node, anchor, loading: false, items: got.items, total: got.total };
      } catch {
        if (my === token) state.value = null;
      }
    }, PEEK_DELAY_MS);
  }

  /** The pointer left, a button went down, the view scrolled, a drag began. */
  function leave() {
    token++;
    clearTimeout(timer);
    timer = undefined;
    state.value = null;
  }

  /** A folder changed: its next peek lists it again. */
  function forget(path?: string) {
    if (path) cache.delete(path);
    else cache.clear();
  }

  return { state, enter, leave, forget };
}
