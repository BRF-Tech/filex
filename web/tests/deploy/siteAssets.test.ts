// `site/assets/` must be the copy of `docs/screenshots/` that `site/README.md`
// says it is.
//
// ⚠⚠ That sentence had been true when it was written and nothing kept it true.
// Found 2026-09-06: `site/assets/admin-plugins.png` was the pre-fix capture
// whose footer read `github.com/brf-tech/filex` — the PRIVATE repo — on
// the public marketing page at filex.sh, after the same picture had already
// been retaken for the README. Three more had fallen behind beside it.
//
// `scripts/sync-site-assets.mjs` copies them; this is why the next release
// cannot ship a marketing page showing an older product than the README.

import { existsSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '..', '..', '..');
const SRC = path.join(REPO, 'docs', 'screenshots');
const DST = path.join(REPO, 'site', 'assets');

// Files with no counterpart are site-owned, not copies: `social-preview.png`
// is rendered from `social-preview.src.html`, `end-user-drive.png` is a
// site-only crop. They are deliberately not synced.
const copies = readdirSync(DST)
  .filter((f) => f.endsWith('.png'))
  .filter((f) => existsSync(path.join(SRC, f)));

describe('site assets', () => {
  it('there are screenshots shared with the docs at all', () => {
    // Guards against the list going empty through a rename and this whole
    // suite passing vacuously.
    expect(copies.length).toBeGreaterThan(0);
  });

  it.each(copies)('site/assets/%s is byte-identical to docs/screenshots', (name) => {
    const a = readFileSync(path.join(SRC, name));
    const b = readFileSync(path.join(DST, name));
    expect(
      b.equals(a),
      `site/assets/${name} differs — filex.sh would show an older picture than the README. ` +
        'Run: node scripts/sync-site-assets.mjs',
    ).toBe(true);
  });
});
