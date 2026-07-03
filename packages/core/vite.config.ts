import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import dts from 'vite-plugin-dts';
import { resolve } from 'path';

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
    vue({ customElement: false }),
    dts({
      entryRoot: 'src',
      outDir: 'dist',
      include: ['src/index.ts', 'src/**/*.ts', 'src/**/*.vue'],
      exclude: ['**/*.spec.ts', '**/*.test.ts'],
      rollupTypes: true,
      insertTypesEntry: true,
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
      fileName: (format) => (format === 'es' ? 'filex-core.js' : 'filex-core.umd.cjs'),
      formats: ['es', 'umd'],
    },
    rollupOptions: {
      external: [
        'vue',
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
      output: {
        globals: {
          vue: 'Vue',
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
      },
    },
  },
});
