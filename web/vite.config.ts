import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import path from 'node:path';

// Vite config for the filex admin UI.
// The bundle is emitted to dist/ and consumed by the Go binary via go:embed.
// `base` MUST stay '/admin/' — the backend mounts the SPA there.
export default defineConfig({
  plugins: [
    vue({
      template: {
        compilerOptions: {
          // <filex-explorer> is loaded at runtime from /embed.js;
          // let Vue treat any filex-* tag as a custom element so the
          // template compiler doesn't try to resolve a component.
          isCustomElement: (tag) => tag.startsWith('filex-'),
        },
      },
    }),
  ],
  base: '/admin/',
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    sourcemap: true,
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks: {
          'vue-vendor': ['vue', 'vue-router', 'pinia'],
          i18n: ['vue-i18n'],
          icons: ['lucide-vue-next'],
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:5212',
      '/embed.js': 'http://localhost:5212',
    },
  },
  preview: {
    port: 5174,
  },
});
