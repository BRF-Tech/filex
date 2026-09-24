// A document opened "in a new tab" opens under the prefix that served THIS
// page — `/drive/files/edit` for somebody who is not an administrator.
//
// ⚠ QA, 2026-09-21: the explorer was handed the bare `/files/edit`. The tab is
// opened by the BROWSER, not pushed by the router, so nothing prepended the
// base, and the router's hydration rewrote the bare path to
// `/admin/files/edit` (router/index.ts, the `/files/edit` carve-out) — a
// regular user landed on an /admin address every time they opened a file.
//
// Read from the source because the failure is a literal: a component test
// would need two mount prefixes and a real tab to reproduce it.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const EXPLORE = path.resolve(__dirname, '../../src/views/Explore.vue');

describe('the editor tab keeps the prefix the page was served from', () => {
  it('openPageBase and viewerBaseUrl are built from currentMountBase()', () => {
    const src = readFileSync(EXPLORE, 'utf8');
    for (const key of ['openPageBase', 'viewerBaseUrl']) {
      const line = src.split('\n').find((l) => l.trimStart().startsWith(`${key}:`)) ?? '';
      expect(line, `${key} is not set in Explore.vue`).not.toBe('');
      expect(line, `${key} must be built under currentMountBase() — a bare '/files/edit' sends a non-admin to /admin/files/edit`).toContain(
        'currentMountBase()',
      );
    }
  });
});
