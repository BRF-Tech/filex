// The installed web app's two halves, as data: the web app manifest and the
// service worker's options, handed to vite-plugin-pwa by vite.config.ts.
//
// ⚠ A module of their own (task #190) so a test can hold them to what a
// browser needs before it offers an install - the name, the start address,
// the display mode, a 192 px and a 512 px PNG that exist at those sizes, and a
// worker that answers `fetch` (web/tests/pwa/manifest.test.ts). Before this
// they were literals inside the plugin call, which nothing could read without
// running a build. Nothing here changed in the move.
import type { ManifestOptions, VitePWAOptions } from 'vite-plugin-pwa';

/** The public files the worker precaches beside the build (vite-plugin-pwa
 *  `includeAssets`). */
export const PWA_INCLUDE_ASSETS = ['favicon.svg', 'icons/icon.svg', 'icons/icon-192.png', 'icons/icon-512.png', 'icons/badge-96.png'];

/** The web app manifest (`manifest.webmanifest`). */
export const PWA_MANIFEST: Partial<ManifestOptions> = {
  id: '/admin/',
  name: 'filex - File Manager',
  short_name: 'filex',
  description: 'Self-hosted file manager: browse, upload, share and edit your files.',
  start_url: '/admin/',
  // ⚠ '/' rather than '/admin/', and it is the MANIFEST scope only —
  // the service-worker registration (vite.config.ts, `scope`) stays pinned
  // to '/admin/'.
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
  // Product blue — in step with index.html's theme-color meta,
  // web/public/favicon.svg, web/public/icons/icon.svg and LogoMark.vue.
  theme_color: '#2f6ceb',
  background_color: '#0a0a0a',
  icons: [
    // A full-bleed SVG doubles as the "any" and "maskable" icon; Chrome
    // (desktop + Android) accepts sizes:"any" SVG for installability.
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
    // ⚠ The PNG set is not a nicety. iOS Safari ignores an SVG
    // apple-touch icon, and — the reason it exists now — Chromium's
    // NOTIFICATION decoder has no SVG at all, so an installed app whose
    // only icon is the SVG raises toasts with no logo. Rasterised from
    // the same mark by `scripts/make-icon-pngs.mjs`.
    { src: 'icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
    { src: 'icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
    { src: 'icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
  ],
};

/** The generated service worker (workbox `generateSW`). */
export const PWA_WORKBOX: VitePWAOptions['workbox'] = {
  // ⚠ The notification handlers. The worker is GENERATED, so there is
  // nowhere in it to write `notificationclick` / `push`; this pulls in
  // `public/notify-sw.js` verbatim. Without it a toast raised BY the
  // worker (the only kind Android Chrome allows) has a click that does
  // nothing, and a future push would show the browser's "site updated
  // in the background" placeholder instead of the app's own toast.
  importScripts: ['notify-sw.js'],
  // ⚠ No map for the generated service worker. workbox builds it from a
  // temp copy, so its map's only source was the builder's temp path —
  // `C:/Users/<account>/AppData/Local/Temp/…/sw.js`, account name
  // included, inside every binary built on that machine. The map
  // describes generated code nobody debugs; scripts/check-embed.mjs
  // refuses any shipped map that names an absolute path.
  sourcemap: false,
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
  // ⚠ The app's own main chunk (the explorer, packages/core) IS the offline
  // shell and must be precached, and it has to fit workbox's 2 MiB default,
  // stated here so it is not raised in passing: vite-plugin-pwa fails the
  // build when it does not fit. The vault (encryption level 3) took it to
  // 2.13 MB once and the limit went to 3 MiB (task #94); its code is now
  // loaded when a vault is opened (composables/useE2eVault.ts →
  // e2eVaultEngine.ts, web/tests/quality/vaultLazy.test.ts) and the limit is
  // back. A chunk that grows past it is split, not let in: 0.54's train took
  // it to 2.12 MB again, and the dialogs a person opens (the viewer, sharing,
  // settings, search, the encryption dialogs) now load with their first use
  // (packages/core/lazySurfaces.ts, web/tests/quality/lazySurfaces.test.ts),
  // 1.89 MB after. `pnpm -C web size` prints the room left after a build
  // (web/scripts/size.mjs reads the number below).
  maximumFileSizeToCacheInBytes: 2 * 1024 * 1024,
  // ⚠ RELATIVE, resolved against the worker's own URL: `/admin/index.html`
  // at the root, `/filex/admin/index.html` under a base path. The
  // absolute '/admin/index.html' is not in the precache under a base, and
  // workbox then refuses every navigation ("non-precached-url").
  navigateFallback: 'index.html',
  // Never let the SW intercept the API — those must always hit the
  // network (and, in Electron, a remote origin).
  navigateFallbackDenylist: [/^\/api\//],
  cleanupOutdatedCaches: true,
};
