import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import { VitePWA } from 'vite-plugin-pwa';
import path from 'node:path';

// Vite config for the filex admin UI.
// The bundle is emitted to dist/ and consumed by the Go binary via go:embed.
// `base` MUST stay '/admin/' — the backend mounts the SPA there.
//
// ⚠ PWA scope boundary (Dilim 1, trap #2): the manifest + service worker live
// ONLY in this standalone SPA build. `@brftech/filex-core` (the embeddable
// <filex-explorer> hosted inside work.example.com "Dosyalar" and fishapp) has no PWA
// plumbing, so those hosts never show a filex install prompt or register a
// competing service worker. The SW `scope` below is pinned to '/admin/' as a
// second guard so it can't claim clients outside this app.
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
    VitePWA({
      // We drive the update flow ourselves (see useInstallPrompt →
      // useRegisterSW) so a new deploy prompts "reload" instead of silently
      // serving a stale bundle from the SW cache — the fishapp cache trap.
      registerType: 'prompt',
      // The Vue `useRegisterSW` composable registers the SW itself; don't also
      // auto-inject a registration script or the SW registers twice.
      injectRegister: false,
      // Service-worker registration scope. Deliberately narrower than the
      // manifest scope below: the SW owns the offline shell for the panel and
      // must not claim clients outside it. /drive/ (the end-user mount, see
      // routes.go) therefore has no offline shell — it loads from the network
      // like any other page, which is correct and not an oversight.
      scope: '/admin/',
      includeAssets: ['favicon.svg', 'icons/icon.svg'],
      manifest: {
        id: '/admin/',
        name: 'filex — File Manager',
        short_name: 'filex',
        description: 'Self-hosted file manager: browse, upload, share and edit your files.',
        start_url: '/admin/',
        // ⚠ '/' rather than '/admin/', and it is the MANIFEST scope only —
        // the service-worker registration above stays pinned to '/admin/'.
        // Manifest scope decides which navigations stay inside the installed
        // window; since GitHub #14 a non-admin who opens the app is handed
        // straight on to /drive/, and with a '/admin/' scope that hand-off
        // ejects them from the installed app into a browser tab on their very
        // first screen. Widening a scope is the safe direction (it only ever
        // keeps more URLs in-app); narrowing one orphans installed clients.
        //
        // ⚠ `id` must NOT follow it. The id is the app's identity — change it
        // and every existing install becomes a second, separate app.
        scope: '/',
        display: 'standalone',
        orientation: 'any',
        theme_color: '#4f46e5',
        background_color: '#0a0a0a',
        icons: [
          // A full-bleed SVG doubles as the "any" and "maskable" icon; Chrome
          // (desktop + Android) accepts sizes:"any" SVG for installability.
          // iOS Safari ignores SVG apple-touch icons — a PNG set is a known
          // follow-up once raster tooling is available in this environment.
          {
            src: 'icons/icon.svg',
            sizes: 'any',
            type: 'image/svg+xml',
            purpose: 'any',
          },
          {
            src: 'icons/icon.svg',
            sizes: 'any',
            type: 'image/svg+xml',
            purpose: 'maskable',
          },
        ],
      },
      workbox: {
        // Precache the built app shell + assets. navigateFallback keeps the
        // Vue history-mode routes (createWebHistory('/admin/')) working
        // offline by serving index.html for unmatched navigations.
        globPatterns: ['**/*.{js,css,html,svg,woff2}'],
        // Keep the heavy lazy-loaded chunks OUT of the precache. Monaco's
        // editor core (~3.8 MB) and its TypeScript worker (~7 MB) are only
        // pulled when someone opens the code editor; precaching them would
        // make every install download ~12 MB up front and blow past workbox's
        // 2 MiB-per-asset limit (which is what the build was failing on).
        // They still load fine over the network on demand — they are simply
        // not part of the offline shell.
        globIgnores: ['**/editor.main-*.js', '**/*.worker-*.js', '**/model-viewer-*.js'],
        navigateFallback: '/admin/index.html',
        // Never let the SW intercept the API — those must always hit the
        // network (and, in Electron, a remote origin).
        navigateFallbackDenylist: [/^\/api\//],
        cleanupOutdatedCaches: true,
      },
      devOptions: {
        // Enable the SW in `vite dev` so the install/update flow can be
        // exercised locally without a full production build. Can be switched
        // off (FILEX_DISABLE_DEV_SW=1) when a test runner's own harness frames
        // conflict with an active service worker.
        enabled: process.env.FILEX_DISABLE_DEV_SW !== '1',
        type: 'module',
        navigateFallback: '/admin/index.html',
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
