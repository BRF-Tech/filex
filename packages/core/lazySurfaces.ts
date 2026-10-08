/**
 * The explorer's lazy surfaces: the dialogs and panels a person OPENS, which
 * FileExplorer.vue loads with `lazySurface(() => import(...))`
 * (src/lib/lazySurface.ts) instead of importing them with the explorer.
 *
 * ⚠⚠ Why this list exists. The web app's main chunk
 * (web/dist/assets/index-*.js) carries every part of this package the admin
 * app uses, and it has to fit workbox's 2 MiB precache limit
 * (web/pwa.config.ts) or `vite build` fails. It does not fit by trimming:
 * 0.54's train went from 2,073,132 to 2,121,274 bytes with a vault, Web Push
 * and a few menu rows, and the chain's build failed (lesson #1276). These
 * surfaces are not needed to draw the explorer, so they are not in the chunk
 * that draws it.
 *
 * ⚠ A dynamic import alone is not enough here. This package is consumed BUILT
 * (dist/filex-core.js), and src/index.ts re-exports several of these
 * components for hosts (PreviewModal, UserSettingsDialog, ...). A module the
 * library entry reaches statically lands in the entry's chunk, the explorer's
 * `import()` then resolves to that same chunk, and the web build, which sees
 * the library chunk as ONE module, cannot take it apart again. So the ES build
 * (vite.config.ts) gives each surface a chunk of its own with
 * `lazySurfaceChunks`: the entry still re-exports it (a host that imports
 * `UserSettingsDialog` gets the component, as before), but a host that does
 * not use the re-export does not load it, and the explorer loads it when it
 * mounts it.
 *
 * Adding a surface: write it here, load it in the explorer with
 * `lazySurface(() => import('./<path>'))`, and make sure nothing the explorer
 * loads statically still imports it (web/tests/quality/lazySurfaces.test.ts
 * walks the static imports and says which module does). Measure with
 * `pnpm -C web build && pnpm -C web size`.
 */

/** Paths under packages/core/src, each the root of a chunk of its own. */
export const LAZY_SURFACES = [
  // The viewer (and Quick Look, which wraps it): opened with a file.
  'modals/PreviewModal.vue',
  'components/QuickLook.vue',
  // The person's own settings, drawn in the explorer for a host that asks
  // (`config.account.settings`, the desktop app), and its time-zone picker.
  'components/UserSettingsDialog.vue',
  'components/TimeZoneDialog.vue',
  'components/TimeZonePicker.vue',
  // Sharing and permissions.
  'modals/PermissionsModal.vue',
  // Search, the command palette, new documents, archives.
  'components/AdvancedSearch.vue',
  'components/CommandPalette.vue',
  'modals/NewDocumentModal.vue',
  'modals/ArchiveCreateModal.vue',
  // Help and preferences.
  'components/OnboardingTour.vue',
  'components/ShortcutSettings.vue',
  // Encryption: making an encrypted folder, its settings, its password and
  // recovery key, single encrypted files.
  'components/EncryptedFolderModal.vue',
  'components/E2eSettingsModal.vue',
  'components/E2eChangePasswordModal.vue',
  'components/RecoveryKeyModal.vue',
  'components/E2eRecoveryUnlockModal.vue',
  'components/E2eFileEncryptModal.vue',
  'components/E2eFileUnlockModal.vue',
] as const;

export type LazySurface = (typeof LAZY_SURFACES)[number];

/** The chunk a surface is written to: its file name without the extension. */
export function lazySurfaceName(surface: string): string {
  return surface.replace(/^.*\//, '').replace(/\.[^.]+$/, '');
}

/** What rollup's `manualChunks` is handed, as much of it as this reads. */
export interface ChunkGraph {
  getModuleIds: () => IterableIterator<string> | Iterable<string>;
  getModuleInfo: (id: string) => { isEntry: boolean; isExternal: boolean; importedIds: readonly string[] } | null;
}

const posix = (p: string) => p.replace(/\\/g, '/');
const fileOf = (id: string) => posix(id.split('?')[0]);

/**
 * The ES build's `output.manualChunks`.
 *
 * Each surface is its own chunk. ⚠ Rollup puts every static dependency of a
 * manual chunk INTO that chunk unless the dependency is assigned a chunk too,
 * so a surface alone would drag the locales, useLocale and half the explorer
 * in with it, and the entry would then import them from the surface's chunk.
 * Hence the second rule: everything the entry reaches statically without
 * passing through a surface is assigned to `index`, the chunk the explorer
 * lives in (the same chunk the build made before). What neither rule names -
 * a surface's own helpers, the viewers' lazy chunks - is left to rollup.
 */
export function lazySurfaceChunks(srcDir: string, surfaces: readonly string[] = LAZY_SURFACES) {
  const root = posix(srcDir).replace(/\/$/, '');
  const named = new Map(surfaces.map((s) => [`${root}/${s}`, lazySurfaceName(s)] as const));
  // Rollup hands one graph object to every call of one output; a new one
  // (watch mode, another output) is walked again.
  let walked: { graph: ChunkGraph; main: Set<string> } | null = null;

  const reachedFromEntry = (graph: ChunkGraph): Set<string> => {
    const seen = new Set<string>();
    const todo: string[] = [];
    for (const id of graph.getModuleIds()) if (graph.getModuleInfo(id)?.isEntry) todo.push(id);
    while (todo.length) {
      const id = todo.pop()!;
      if (seen.has(id)) continue;
      seen.add(id);
      for (const dep of graph.getModuleInfo(id)?.importedIds ?? []) {
        if (seen.has(dep) || named.has(fileOf(dep))) continue;
        if (graph.getModuleInfo(dep)?.isExternal) continue;
        todo.push(dep);
      }
    }
    return seen;
  };

  return (id: string, graph: ChunkGraph): string | undefined => {
    const own = named.get(fileOf(id));
    if (own) return own;
    if (walked?.graph !== graph) walked = { graph, main: reachedFromEntry(graph) };
    if (!walked.main.has(id) || graph.getModuleInfo(id)?.isEntry) return undefined;
    return 'index';
  };
}
