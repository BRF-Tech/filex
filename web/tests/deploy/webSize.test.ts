// `pnpm -C web size` (web/scripts/size.mjs): the main chunk of the last build
// against workbox's precache limit, read before a merge instead of learned
// from a failed test run (0.54, lesson #1276).
//
// The script reads the limit out of web/pwa.config.ts rather than repeating
// it; these tests hold the reader to the value the build really uses, and the
// chunk finder to the shape of the index.html vite writes.

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { PWA_WORKBOX } from '../../pwa.config';
import { mainChunkOf, precacheLimit } from '../../scripts/size.mjs';

const WEB = path.resolve(__dirname, '../..');

describe('web size', () => {
  it('reads the same precache limit the build uses', () => {
    const source = readFileSync(path.join(WEB, 'pwa.config.ts'), 'utf8');
    expect(precacheLimit(source)).toBe(PWA_WORKBOX?.maximumFileSizeToCacheInBytes);
  });

  it('reads a product of whole numbers and nothing else', () => {
    expect(precacheLimit('  maximumFileSizeToCacheInBytes: 2 * 1024 * 1024,\n')).toBe(2097152);
    expect(precacheLimit('maximumFileSizeToCacheInBytes: 3_145_728,')).toBe(3145728);
    expect(precacheLimit('maximumFileSizeToCacheInBytes: LIMIT,')).toBeNull();
    expect(precacheLimit('nothing here')).toBeNull();
  });

  it('finds the main chunk in the index.html vite writes', () => {
    const html =
      '<link rel="modulepreload" crossorigin href="/admin/assets/vue-vendor-BQrpblhC.js">\n' +
      '<script type="module" crossorigin src="/admin/assets/index-fO1nqFkV.js"></script>';
    expect(mainChunkOf(html)).toBe('assets/index-fO1nqFkV.js');
    expect(mainChunkOf('<script src="/admin/registerSW.js"></script>')).toBeNull();
  });

  it('is a script of the web package, documented for contributors', () => {
    const pkg = JSON.parse(readFileSync(path.join(WEB, 'package.json'), 'utf8')) as { scripts: Record<string, string> };
    expect(pkg.scripts.size).toBe('node scripts/size.mjs');
    const contributing = readFileSync(path.resolve(WEB, '../docs/CONTRIBUTING.md'), 'utf8');
    expect(contributing).toContain('pnpm -C web size');
  });
});
