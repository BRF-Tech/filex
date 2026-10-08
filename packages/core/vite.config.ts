import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import dts from 'vite-plugin-dts';
import { resolve } from 'path';
// The signing faces stay FILES instead of base64 in style.css — see the
// plugin's own note for why library mode forces the question, and why the
// web-component build has to use the very same plugin.
import { fontsAsFiles } from '../../scripts/vite-fonts-as-files.mjs';
import type { Rollup } from 'vite';
import { lazySurfaceChunks } from './lazySurfaces';
// A TypeScript diagnostic in the declaration build fails the build instead of
// shipping `any` in dist/index.d.ts (DataTable's slots did, for months).
import { failOnDtsDiagnostics } from '../../scripts/vite-dts-strict.mjs';

/** What the ES and the UMD output share. */
const output: Rollup.OutputOptions = {
  globals: {
    vue: 'Vue',
    '@headlessui/vue': 'HeadlessUIVue',
    'lucide-vue-next': 'LucideVueNext',
    'monaco-editor': 'monaco',
    'highlight.js': 'hljs',
    'markdown-it': 'markdownit',
    jszip: 'JSZip',
    mermaid: 'mermaid',
    epubjs: 'ePub',
    xlsx: 'XLSX',
    '@google/model-viewer': 'ModelViewer',
  },
  exports: 'named',
  // Single rolled-up style file regardless of how many SFCs the
  // tree has — consumers do `import '@brftech/filex-core/style.css'`
  // exactly once.
  assetFileNames: (info) => {
    if (info.name && info.name.endsWith('.css')) return 'style.css';
    return 'assets/[name]-[hash][extname]';
  },
};

/**
 * Vite library build for @brftech/filex-core.
 *
 * Produces ESM + UMD bundles, a single rolled-up `style.css`, and full
 * `.d.ts` declarations (entry rolled up into `index.d.ts`).
 *
 * External peers:
 *   - Vue (host provides)
 *   - Monaco / highlight.js / markdown-it / CodeMirror lang packs:
 *     dynamic-imported at runtime, externalized so the consumer's
 *     bundler resolves them against its own node_modules. Keeps our
 *     bundle small (~70 KB ESM) and lets the host share single copies
 *     across other features.
 */
export default defineConfig({
  plugins: [
    fontsAsFiles(resolve(__dirname, 'src/assets/fonts'), resolve(__dirname, 'dist')),
    vue({ customElement: false }),
    dts({
      entryRoot: 'src',
      outDir: 'dist',
      include: ['src/index.ts', 'src/**/*.ts', 'src/**/*.vue'],
      exclude: ['**/*.spec.ts', '**/*.test.ts'],
      rollupTypes: true,
      insertTypesEntry: true,
      afterDiagnostic: failOnDtsDiagnostics('@brftech/filex-core'),
    }),
  ],
  resolve: {
    alias: { '@': resolve(__dirname, 'src') },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: true,
    cssCodeSplit: false,
    lib: {
      entry: resolve(__dirname, 'src/index.ts'),
      name: 'FilexCore',
      // The formats are the two outputs below (ES + UMD).
      fileName: (format) => (format === 'es' ? 'filex-core.js' : 'filex-core.umd.cjs'),
    },
    rollupOptions: {
      external: [
        'vue',
        // The bell's popover and the account menu (NotificationBell,
        // AccountMenu) — a DEPENDENCY of this package, kept out of its bundle
        // so the admin SPA, which imports it too, does not carry two copies.
        // The web-component build bundles it once (packages/webcomponent).
        '@headlessui/vue',
        // The user settings dialog's rail glyphs (UserSettingsDialog) — the
        // admin app's own icon set, kept external for the same reason.
        'lucide-vue-next',
        // Optional peers — the editor + preview path lazy-imports these
        // at runtime; the consumer either installs them or the feature
        // gracefully degrades. See FileExplorer + PreviewModal sources
        // for the dynamic-import sites.
        'monaco-editor',
        'highlight.js',
        'markdown-it',
        'jszip',
        'mermaid',
        'epubjs',
        'xlsx',
        '@google/model-viewer',
        // CodeMirror core + helpers
        'codemirror',
        '@codemirror/state',
        '@codemirror/view',
        '@codemirror/commands',
        '@codemirror/theme-one-dark',
        // CodeMirror language packs (lazy-loaded)
        /^@codemirror\/lang-/,
      ],
      // ⚠ Two outputs written out (lib.formats would make the same two) so the
      // ES one alone can carry `manualChunks`: the UMD bundle inlines every
      // dynamic import and rollup refuses manualChunks there.
      output: [
        {
          ...output,
          format: 'es',
          // The dialogs the explorer opens get a chunk each, so a host that
          // draws the explorer does not carry them in its own first chunk
          // (lazySurfaces.ts; the web app's 2 MiB precache limit).
          manualChunks: lazySurfaceChunks(resolve(__dirname, 'src')),
        },
        { ...output, format: 'umd', name: 'FilexCore' },
      ],
    },
  },
});
