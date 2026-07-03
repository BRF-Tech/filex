/**
 * useSelection — multi-select state with Ctrl/Shift semantics.
 *
 * click(path, {ctrl, shift})
 *   no-mod  → replace selection with [path]
 *   ctrl    → toggle path in selection
 *   shift   → range select from anchor to path
 */

import { ref, computed } from 'vue';
import type { FileNode } from '../types/FileNode';

export function useSelection(items: () => FileNode[]) {
  const selected = ref<Set<string>>(new Set());
  const anchor = ref<string | null>(null);

  function click(path: string, mod: { ctrl?: boolean; shift?: boolean } = {}) {
    if (mod.shift && anchor.value) {
      const list = items();
      const aIdx = list.findIndex((n) => n.path === anchor.value);
      const bIdx = list.findIndex((n) => n.path === path);
      if (aIdx === -1 || bIdx === -1) {
        selected.value = new Set([path]);
        anchor.value = path;
        return;
      }
      const [lo, hi] = aIdx < bIdx ? [aIdx, bIdx] : [bIdx, aIdx];
      const next = new Set<string>();
      for (let i = lo; i <= hi; i++) next.add(list[i].path);
      selected.value = next;
      return;
    }

    if (mod.ctrl) {
      const next = new Set(selected.value);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      selected.value = next;
      anchor.value = path;
      return;
    }

    selected.value = new Set([path]);
    anchor.value = path;
  }

  function clear() {
    selected.value = new Set();
    anchor.value = null;
  }

  function selectAll() {
    selected.value = new Set(items().map((n) => n.path));
  }

  function has(path: string): boolean {
    return selected.value.has(path);
  }

  function add(path: string) {
    const next = new Set(selected.value);
    next.add(path);
    selected.value = next;
  }

  function remove(path: string) {
    const next = new Set(selected.value);
    next.delete(path);
    selected.value = next;
  }

  const size = computed(() => selected.value.size);
  const isEmpty = computed(() => selected.value.size === 0);

  const nodes = computed(() => {
    const list = items();
    return list.filter((n) => selected.value.has(n.path));
  });

  return { selected, anchor, click, clear, selectAll, has, add, remove, size, isEmpty, nodes };
}
