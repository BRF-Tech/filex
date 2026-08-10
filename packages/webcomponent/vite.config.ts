import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import dts from 'vite-plugin-dts';
import { resolve } from 'path';

/**
 * Vite config for @brftech/filex (Web Component).
 *
 * Bundles Vue 3 runtime + core SFC together so the resulting JS works
 * unzipped from any CDN (no peer to install on the host page).
 *
 * Side-effect import: just including the bundle registers the
 * `<filex-explorer>` custom element. Consumers don't have to call
 * anything explicit.
 */
export default defineConfig({
  plugins: [
    vue({ customElement: false }),
    dts({
      entryRoot: 'src',
      outDir: 'dist',
      include: ['src/**/*.ts'],
      rollupTypes: true,
      insertTypesEntry: true,
    }),
  ],
  resolve: {
    alias: { '@': resolve(__dirname, 'src') },
  },
  // Several deps (Vue dev-warnings, jose, formidable-debug) read
  // `process.env.NODE_ENV` at module-init time. Vite's `lib` build
  // does NOT auto-replace those because the consumer is supposed to
  // be a bundler with a real `process` shim. Our consumer is a raw
  // <script type="module"> on demo-fm.example.com, so define them
  // explicitly. NODE_ENV=production strips Vue's dev warnings AND
  // compiles out the dev-only code paths in dependencies.
  define: {
    'process.env.NODE_ENV': JSON.stringify('production'),
    'process.env': '{}',
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: true,
    cssCodeSplit: false,
    lib: {
      entry: resolve(__dirname, 'src/index.ts'),
      name: 'Filex',
      fileName: (format) => (format === 'es' ? 'filex.js' : 'filex.umd.cjs'),
      formats: ['es', 'umd'],
    },
    rollupOptions: {
      // Vue is BUNDLED IN — that's the whole point of the WC distribution.
      // Optional peers stay external; they're dynamic-imported anyway.
      //
      // ⚠ `markdown-it` is deliberately NOT in this list any more. Every
      // consumer of this bundle is a raw <script type="module"> — the desktop
      // app, work.brf.sh, fishapp — where an external BARE specifier cannot
      // resolve at all, so `import('markdown-it')` always failed and the
      // markdown preview pane rendered blank in every one of them (measured in
      // the desktop app, 2026-08-10). It is ~100 KB and lazily chunked, so it
      // costs nothing until a .md is opened. The heavier peers below stay
      // external on purpose — monaco alone is several megabytes — and their
      // viewers say plainly when they are missing rather than going blank.
      external: [
        'monaco-editor',
        'jszip',
        'mermaid',
        'epubjs',
        'xlsx',
        '@google/model-viewer',
        'codemirror',
        '@codemirror/state',
        '@codemirror/view',
        '@codemirror/commands',
        '@codemirror/theme-one-dark',
        /^@codemirror\/lang-/,
      ],
      output: {
        globals: {
          'monaco-editor': 'monaco',
          'highlight.js': 'hljs',
          jszip: 'JSZip',
          mermaid: 'mermaid',
          epubjs: 'ePub',
          xlsx: 'XLSX',
          '@google/model-viewer': 'ModelViewer',
        },
        exports: 'named',
        assetFileNames: (info) => {
          if (info.name && info.name.endsWith('.css')) return 'style.css';
          return 'assets/[name]-[hash][extname]';
        },
      },
    },
  },
});
