import { defineConfig } from 'vite';
import dts from 'vite-plugin-dts';
import { resolve } from 'path';

/**
 * The app-interface SDK: no dependency, a few KB. Two builds of the same
 * source — an ES module for an app that has a bundler, and a classic script
 * (`window.FilexAppUI`) for one that is a plain index.html.
 */
export default defineConfig({
  plugins: [
    dts({
      entryRoot: 'src',
      outDir: 'dist',
      include: ['src/**/*.ts'],
      rollupTypes: true,
      insertTypesEntry: true,
    }),
  ],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: true,
    target: 'es2020',
    lib: {
      entry: resolve(__dirname, 'src/index.ts'),
      name: 'FilexAppUI',
      fileName: (format) => (format === 'es' ? 'filex-app-ui.js' : 'filex-app-ui.iife.js'),
      formats: ['es', 'iife'],
    },
  },
});
