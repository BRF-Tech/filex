// The dialogs a person opens are loaded with their first use, not with the
// explorer (packages/core/lazySurfaces.ts).
//
// ⚠ 0.54 (2026-10-08): the train's one test run failed in the build
// (int054-nightly-20261008-063029Z-612da023): the web app's main chunk was
// 2,121,274 bytes, over workbox's 2 MiB precache limit (web/pwa.config.ts),
// and vite-plugin-pwa fails the build then (lesson #1276). The limit stays
// where it is; the viewer, sharing, the settings dialog, search, the command
// palette and the encryption dialogs left the chunk instead (1,889,772 bytes
// measured after). Two halves keep them out, and this file holds both:
//
//   - the explorer loads them with `lazySurface(() => import(...))`, so no
//     module the explorer loads statically imports one;
//   - the core build gives each a chunk of its own (`lazySurfaceChunks`),
//     because src/index.ts re-exports several for hosts and the library's
//     entry chunk would otherwise keep them, out of the web build's reach.
//
// Red on the old code: FileExplorer imported every one of them statically,
// and packages/core/lazySurfaces.ts (with its chunk rule) did not exist.

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { LAZY_SURFACES, lazySurfaceChunks, lazySurfaceName, type ChunkGraph } from '../../../packages/core/lazySurfaces';
import { CORE, dynamicImports, loadedWith, rel, resolveImport, scriptOf, staticImportersOf } from '../helpers/coreImports';

const surfaces = new Set<string>(LAZY_SURFACES);

/** What the explorer loads statically: the library entry's graph, less the
 *  entry's own re-exports of a surface (each its own chunk in the build). */
const withExplorer = loadedWith(path.join(CORE, 'index.ts'), (from, to) => !(from === 'index.ts' && surfaces.has(to)));

describe('the dialogs a person opens are not loaded with the explorer', () => {
  it('the list names files of core, each once', () => {
    expect(LAZY_SURFACES.length).toBeGreaterThan(10);
    expect(new Set(LAZY_SURFACES).size).toBe(LAZY_SURFACES.length);
    for (const s of LAZY_SURFACES) expect(resolveImport(path.join(CORE, 'index.ts'), `./${s}`), s).not.toBeNull();
    const names = LAZY_SURFACES.map(lazySurfaceName);
    expect(new Set(names).size, 'two surfaces would share a chunk name').toBe(names.length);
  });

  it('the walk reaches the explorer and what it draws with (it is not vacuous)', () => {
    expect(withExplorer.has('FileExplorer.vue')).toBe(true);
    expect(withExplorer.has('composables/useLocale.ts')).toBe(true);
    expect(withExplorer.has('locales/en.ts')).toBe(true);
    expect(withExplorer.has('components/FilePane.vue')).toBe(true);
  });

  it('no surface is loaded with the explorer, and none is imported by what is', () => {
    const leaked = LAZY_SURFACES.filter((s) => withExplorer.has(s)).map(
      (s) => `${s} (imported by ${staticImportersOf(s, withExplorer).join(', ')})`,
    );
    expect(leaked, 'load it with lazySurface(() => import(...)) instead').toEqual([]);
  });

  it('each is still loaded: by import() from the explorer, or by a surface that is', () => {
    const imported = new Set<string>();
    for (const f of withExplorer) {
      const abs = path.join(CORE, f);
      for (const spec of dynamicImports(abs)) {
        const r = resolveImport(abs, spec);
        if (r) imported.add(rel(r));
      }
    }
    const orphans = LAZY_SURFACES.filter((s) => !imported.has(s) && staticImportersOf(s, LAZY_SURFACES).length === 0);
    expect(orphans).toEqual([]);
    expect(imported.has('modals/PreviewModal.vue')).toBe(true);
  });

  it('the explorer holds them through lazySurface (never a bare defineAsyncComponent)', () => {
    const fe = path.join(CORE, 'FileExplorer.vue');
    const script = scriptOf(fe);
    const viaHelper = new Set([...script.matchAll(/lazySurface\(\(\)\s*=>\s*import\('([^']+)'\)\)/g)].map((m) => m[1]));
    const loadedSurfaces = dynamicImports(fe).filter((spec) => surfaces.has(rel(resolveImport(fe, spec) ?? '')));
    expect(loadedSurfaces.length).toBeGreaterThan(10);
    expect(loadedSurfaces.filter((spec) => !viaHelper.has(spec))).toEqual([]);
  });

  it('the template still names them as before (the source-reading tests rely on it)', () => {
    const fe = readFileSync(path.join(CORE, 'FileExplorer.vue'), 'utf8');
    expect(fe).toMatch(/<UserSettingsDialog\s+v-if="settingsOpen"/);
    expect(fe).toMatch(/<PermissionsModal\s+v-if="showPerm && permTarget"/);
    expect(fe).toMatch(/<PreviewModal\b/);
  });

  it('a loading surface never suspends a host: lazySurface passes suspensible: false', () => {
    const helper = scriptOf(path.join(CORE, 'lib/lazySurface.ts'));
    expect(helper).toMatch(/defineAsyncComponent<T>\(\{\s*loader,\s*suspensible:\s*false\s*\}\)/);
  });
});

/** A module graph in the shape rollup hands `manualChunks`. */
function graph(root: string, modules: Record<string, { entry?: boolean; external?: boolean; imports?: string[] }>): ChunkGraph {
  const id = (m: string) => (m.startsWith('/') || /^[A-Z]:/.test(m) || !m.includes('.') ? m : `${root}/${m}`);
  const byId = new Map(Object.entries(modules).map(([m, v]) => [id(m), v]));
  return {
    getModuleIds: () => byId.keys(),
    getModuleInfo: (m) => {
      const v = byId.get(m) ?? byId.get(m.split('?')[0]);
      if (!v) return null;
      return { isEntry: !!v.entry, isExternal: !!v.external, importedIds: (v.imports ?? []).map(id) };
    },
  };
}

describe('the core build gives each surface a chunk of its own', () => {
  const ROOT = '/work/packages/core/src';
  const g = graph(ROOT, {
    'index.ts': { entry: true, imports: ['FileExplorer.vue', 'components/UserSettingsDialog.vue', 'vue'] },
    'FileExplorer.vue': { imports: ['composables/useLocale.ts', 'vue'] },
    'components/UserSettingsDialog.vue': {
      imports: ['composables/useLocale.ts', 'lib/accountRules.ts', 'components/UserSettingsDialog.vue?vue&type=style&index=0&lang.css'],
    },
    'components/UserSettingsDialog.vue?vue&type=style&index=0&lang.css': {},
    'composables/useLocale.ts': { imports: ['locales/en.ts'] },
    'locales/en.ts': {},
    'lib/accountRules.ts': {},
    'viewers/PdfViewer.vue': { imports: ['composables/useLocale.ts'] },
    vue: { external: true },
  });
  const chunk = (m: string) => lazySurfaceChunks(ROOT)(`${ROOT}/${m}`, g);

  it('a surface, and its stylesheet, are the surface chunk', () => {
    expect(chunk('components/UserSettingsDialog.vue')).toBe('UserSettingsDialog');
    expect(chunk('components/UserSettingsDialog.vue?vue&type=style&index=0&lang.css')).toBe('UserSettingsDialog');
  });

  it('what the entry reaches without a surface is the explorer chunk, so a surface does not drag it in', () => {
    expect(chunk('FileExplorer.vue')).toBe('index');
    expect(chunk('composables/useLocale.ts')).toBe('index');
    expect(chunk('locales/en.ts')).toBe('index');
  });

  it('a helper only a surface uses, a lazy viewer and the entry itself are left to rollup', () => {
    expect(chunk('lib/accountRules.ts')).toBeUndefined();
    expect(chunk('viewers/PdfViewer.vue')).toBeUndefined();
    expect(chunk('index.ts')).toBeUndefined();
  });

  it('a Windows source path names the same modules', () => {
    const win = 'C:\\work\\packages\\core\\src';
    const wg = graph('C:/work/packages/core/src', {
      'index.ts': { entry: true, imports: ['modals/PreviewModal.vue', 'FileExplorer.vue'] },
      'modals/PreviewModal.vue': {},
      'FileExplorer.vue': {},
    });
    const fn = lazySurfaceChunks(win);
    expect(fn('C:/work/packages/core/src/modals/PreviewModal.vue', wg)).toBe('PreviewModal');
    expect(fn('C:/work/packages/core/src/FileExplorer.vue', wg)).toBe('index');
  });

  it('the ES output of the core build uses it (the UMD bundle cannot: it inlines every import)', () => {
    const config = readFileSync(path.resolve(CORE, '../vite.config.ts'), 'utf8');
    expect(config).toMatch(/format:\s*'es',[\s\S]*?manualChunks:\s*lazySurfaceChunks\(resolve\(__dirname, 'src'\)\)/);
    expect(config).toMatch(/format:\s*'umd',\s*name:\s*'FilexCore'/);
  });
});
