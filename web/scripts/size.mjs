#!/usr/bin/env node
// The admin app's main chunk against workbox's precache limit, measured on the
// last build: `pnpm -C web build && pnpm -C web size`.
//
// ⚠ Why. The main chunk (dist/assets/index-*.js: the app shell and, with it,
// the explorer from packages/core) is the offline shell the service worker
// precaches, and vite-plugin-pwa FAILS the build when a precached file is over
// `maximumFileSizeToCacheInBytes` (web/pwa.config.ts, 2 MiB). A branch that
// fits on its own can still break the build once the train merges it next to
// two others: 0.54's integration branch was 2,121,274 bytes and its one test
// run died in the build (lesson #1276). This prints how much room is left, so
// it is read before the merge, not after the run.
//
// The limit is read from web/pwa.config.ts, never repeated here. Exit 1 when
// the main chunk is over it; the fix is to split the chunk (a dialog behind
// `lazySurface(() => import(...))`, packages/core/lazySurfaces.ts), not to
// raise the limit.
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const WEB = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

/** `maximumFileSizeToCacheInBytes` from pwa.config.ts's source: a product of
 *  whole numbers (`2 * 1024 * 1024`), or null when it is written otherwise. */
export function precacheLimit(pwaConfigSource) {
  const m = /maximumFileSizeToCacheInBytes:\s*([\d\s*_]+?)\s*,/.exec(pwaConfigSource);
  if (!m) return null;
  const factors = m[1].split('*').map((f) => f.trim().replace(/_/g, ''));
  if (factors.some((f) => !/^\d+$/.test(f))) return null;
  return factors.reduce((n, f) => n * Number(f), 1);
}

/** The main chunk index.html loads, relative to dist (`assets/index-….js`). */
export function mainChunkOf(indexHtml) {
  const m = /<script\b[^>]*\btype="module"[^>]*\bsrc="[^"]*?(assets\/[^"/]+\.js)"/.exec(indexHtml);
  return m ? m[1] : null;
}

const kb = (n) => `${(n / 1000).toFixed(1)} kB`;
const bytes = (n) => new Intl.NumberFormat('en-US').format(n);

function main() {
  const dist = path.join(WEB, 'dist');
  const html = path.join(dist, 'index.html');
  if (!existsSync(html)) {
    console.error('No build in web/dist. Run `pnpm -C web build` first (the packages before it: `pnpm run build:packages`).');
    return 2;
  }
  const limit = precacheLimit(readFileSync(path.join(WEB, 'pwa.config.ts'), 'utf8'));
  if (limit === null) {
    console.error('Could not read maximumFileSizeToCacheInBytes from web/pwa.config.ts (expected a product like 2 * 1024 * 1024).');
    return 2;
  }
  const chunk = mainChunkOf(readFileSync(html, 'utf8'));
  if (!chunk || !existsSync(path.join(dist, chunk))) {
    console.error('Could not find the main chunk index.html loads.');
    return 2;
  }
  const size = statSync(path.join(dist, chunk)).size;
  const room = limit - size;
  console.log(`main chunk      ${chunk}  ${bytes(size)} bytes (${kb(size)})`);
  console.log(`precache limit  ${bytes(limit)} bytes (web/pwa.config.ts)`);
  console.log(`room left       ${bytes(room)} bytes (${kb(room)}, ${((room / limit) * 100).toFixed(1)}%)`);

  const assets = path.join(dist, 'assets');
  const others = readdirSync(assets)
    .filter((f) => f.endsWith('.js') && `assets/${f}` !== chunk)
    .map((f) => ({ f, size: statSync(path.join(assets, f)).size }))
    .sort((a, b) => b.size - a.size)
    .slice(0, 5);
  console.log('largest other chunks:');
  for (const { f, size: s } of others) console.log(`  ${bytes(s).padStart(11)}  assets/${f}`);

  if (room < 0) {
    console.error('\nOVER the limit: the build fails here. Split the chunk (packages/core/lazySurfaces.ts); do not raise the limit.');
    return 1;
  }
  return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = main();
}
