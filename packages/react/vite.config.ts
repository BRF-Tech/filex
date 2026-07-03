import { defineConfig } from 'vite';
import dts from 'vite-plugin-dts';
import { resolve } from 'path';

/**
 * Tiny adapter package — externalizes both React AND `@brftech/filex`
 * (which carries the WC + Vue runtime). Output is a few KB at most;
 * the actual bytes are paid for by the WC dependency.
 */
export default defineConfig({
  plugins: [
    dts({
      entryRoot: 'src',
      outDir: 'dist',
      include: ['src/**/*.ts', 'src/**/*.tsx'],
      rollupTypes: true,
      insertTypesEntry: true,
    }),
  ],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: true,
    lib: {
      entry: resolve(__dirname, 'src/index.ts'),
      name: 'FilexReact',
      fileName: (format) => (format === 'es' ? 'filex-react.js' : 'filex-react.cjs'),
      formats: ['es', 'cjs'],
    },
    rollupOptions: {
      external: ['react', 'react-dom', 'react/jsx-runtime', '@brftech/filex', '@lit/react'],
      output: {
        globals: {
          react: 'React',
          'react-dom': 'ReactDOM',
        },
        exports: 'named',
      },
    },
  },
});
