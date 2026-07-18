/**
 * useTabs — wiring:d1 sekmeler (çalışma alanı tab şeridi).
 *
 * A LAYER ABOVE FileExplorer's location state, never a replacement for
 * it: each tab is a location snapshot `{ id, path, viewMode, split }`.
 * The composable performs no fetching and owns no navigation — the host
 * activates a tab by running its EXISTING `load(path)` pipeline and
 * reports navigations back through `syncActive()`, which keeps the
 * active snapshot glued to wherever the user actually is.
 *
 * `path` is stored in the SAME form as FileExplorer's `currentPath`
 * (virtual `<storage>/<rel>` in multi-storage mode, bare relative path
 * in single-storage mode) — i.e. the exact string `load(path)` accepts.
 * Storing the wire form (`adapter://rel`) instead would break the
 * rootPath-floor clamp inside load() (the floor is compared in user-path
 * form), so the user-path form is deliberate.
 *
 * Persistence: localStorage under a host-supplied key (the host derives
 * it from the pathPersist scope: disabled when `pathPersist: 'none'`,
 * suffixed with the rootPath confine so embedded instances with
 * different confines never clobber each other). Schema:
 *
 *   { "v": 1, "active": "<id>",
 *     "tabs": [ { "id", "path", "viewMode", "split": {"path"}|null } ] }
 */

import { computed, ref, watch, type Ref } from 'vue';
import type { ViewMode } from '../types/FileNode';

/** Per-tab split state — the secondary pane's own location. */
export interface TabSplit {
  path: string;
  /** ui-fix — the pane's OWN view mode (list/grid/gallery); undefined
   *  inherits the main panel's mode at split time. */
  viewMode?: ViewMode;
}

export interface TabState {
  id: string;
  /** Location snapshot in `currentPath` form (see module docs). */
  path: string;
  viewMode: ViewMode;
  split: TabSplit | null;
}

export interface UseTabsOptions {
  /** localStorage key; null disables persistence entirely. */
  storageKey: string | null;
}

function makeId(): string {
  try {
    return crypto.randomUUID();
  } catch {
    return `t${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
  }
}

export function useTabs(opts: UseTabsOptions) {
  const tabs: Ref<TabState[]> = ref([]);
  const activeId = ref<string>('');

  const activeTab = computed<TabState | null>(
    () => tabs.value.find((t) => t.id === activeId.value) ?? null,
  );
  const activeIndex = computed(() => tabs.value.findIndex((t) => t.id === activeId.value));
  const hasMultiple = computed(() => tabs.value.length > 1);

  // ------------------------------------------------------------------
  // Persistence
  // ------------------------------------------------------------------

  /** Restore from storage. Returns true when at least one tab loaded. */
  function restore(): boolean {
    if (!opts.storageKey) return false;
    try {
      const raw = localStorage.getItem(opts.storageKey);
      if (!raw) return false;
      const parsed = JSON.parse(raw) as { v?: unknown; active?: unknown; tabs?: unknown };
      if (!parsed || !Array.isArray(parsed.tabs) || parsed.tabs.length === 0) return false;
      const clean: TabState[] = [];
      for (const t of parsed.tabs as Array<Record<string, unknown>>) {
        if (!t || typeof t.path !== 'string') continue;
        // ViewMode is validated loosely on purpose: future modes (e.g. the
        // gallery wave) must survive a round-trip through an older schema.
        const vm =
          typeof t.viewMode === 'string' && t.viewMode ? (t.viewMode as ViewMode) : 'list';
        const rawSplit = t.split as { path?: unknown; viewMode?: unknown } | null | undefined;
        const split =
          rawSplit && typeof rawSplit === 'object' && typeof rawSplit.path === 'string'
            ? {
                path: rawSplit.path,
                viewMode:
                  typeof rawSplit.viewMode === 'string' && rawSplit.viewMode
                    ? (rawSplit.viewMode as ViewMode)
                    : undefined,
              }
            : null;
        clean.push({
          id: typeof t.id === 'string' && t.id ? t.id : makeId(),
          path: t.path,
          viewMode: vm,
          split,
        });
      }
      if (clean.length === 0) return false;
      tabs.value = clean;
      activeId.value = clean.some((t) => t.id === parsed.active)
        ? String(parsed.active)
        : clean[0].id;
      return true;
    } catch {
      return false;
    }
  }

  function persist(): void {
    if (!opts.storageKey) return;
    try {
      localStorage.setItem(
        opts.storageKey,
        JSON.stringify({ v: 1, active: activeId.value, tabs: tabs.value }),
      );
    } catch {
      /* private mode / quota */
    }
  }

  watch([tabs, activeId], persist, { deep: true });

  // ------------------------------------------------------------------
  // Mutations
  // ------------------------------------------------------------------

  /** Create the first tab (no-op once any tab exists). */
  function seed(path: string, viewMode: ViewMode): void {
    if (tabs.value.length > 0) return;
    const t: TabState = { id: makeId(), path, viewMode, split: null };
    tabs.value = [t];
    activeId.value = t.id;
  }

  /** Update the ACTIVE tab's snapshot (navigation / view-mode change). */
  function syncActive(patch: Partial<Pick<TabState, 'path' | 'viewMode'>>): void {
    const t = activeTab.value;
    if (!t) return;
    if (patch.path !== undefined) t.path = patch.path;
    if (patch.viewMode !== undefined) t.viewMode = patch.viewMode;
  }

  /** Open a new tab right after the active one (browser convention). */
  function openTab(
    path: string,
    o: { viewMode: ViewMode; background?: boolean; split?: TabSplit | null },
  ): TabState {
    const t: TabState = { id: makeId(), path, viewMode: o.viewMode, split: o.split ?? null };
    const idx = activeIndex.value;
    tabs.value.splice(idx === -1 ? tabs.value.length : idx + 1, 0, t);
    if (!o.background) activeId.value = t.id;
    return t;
  }

  /**
   * Close a tab. The last remaining tab never closes. Returns the tab
   * the host must ACTIVATE (right neighbour, else left) when the active
   * one was closed; null when the visible location is unchanged.
   */
  function closeTab(id: string): TabState | null {
    if (tabs.value.length <= 1) return null;
    const idx = tabs.value.findIndex((t) => t.id === id);
    if (idx === -1) return null;
    const wasActive = tabs.value[idx].id === activeId.value;
    tabs.value.splice(idx, 1);
    if (!wasActive) return null;
    const next = tabs.value[Math.min(idx, tabs.value.length - 1)];
    activeId.value = next.id;
    return next;
  }

  /** Make a tab active. Returns it when the active tab actually changed. */
  function activate(id: string): TabState | null {
    const t = tabs.value.find((x) => x.id === id);
    if (!t || t.id === activeId.value) return null;
    activeId.value = t.id;
    return t;
  }

  /** Cycle: +1 = next, -1 = previous (wraps). */
  function step(delta: number): TabState | null {
    if (tabs.value.length < 2) return null;
    const idx = activeIndex.value === -1 ? 0 : activeIndex.value;
    const next = tabs.value[(idx + delta + tabs.value.length) % tabs.value.length];
    return activate(next.id);
  }

  /** Drag-sort support: move the tab at `from` to position `to`. */
  function move(from: number, to: number): void {
    if (from === to) return;
    if (from < 0 || to < 0 || from >= tabs.value.length || to >= tabs.value.length) return;
    const list = [...tabs.value];
    const [t] = list.splice(from, 1);
    list.splice(to, 0, t);
    tabs.value = list;
  }

  /** Set (or clear with null) the ACTIVE tab's split state. */
  function setSplit(split: TabSplit | null): void {
    const t = activeTab.value;
    if (!t) return;
    t.split = split;
  }

  return {
    tabs,
    activeId,
    activeTab,
    activeIndex,
    hasMultiple,
    restore,
    seed,
    syncActive,
    openTab,
    closeTab,
    activate,
    step,
    move,
    setSplit,
  };
}

export type TabsApi = ReturnType<typeof useTabs>;
