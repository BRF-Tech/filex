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
// ⚠ `site/` is withheld from the public export (scripts/export-public.sh), so
// in the published tree this directory does not exist at all and there is
// nothing to compare. Skipping there is correct; skipping in the source repo
// would make the gate a decoration, so the two cases are told apart by the
// directory's presence and the skip is announced.
const sitePresent = existsSync(DST);
const copies = sitePresent
  ? readdirSync(DST)
      .filter((f) => f.endsWith('.png'))
      .filter((f) => existsSync(path.join(SRC, f)))
  : [];

// ⚠⚠ UNCONDITIONAL, and that is the whole reason it was moved out here.
//
// It used to sit inside the block below, where `describe.skipIf` skipped it
// along with everything it was guarding — so the one assertion written to catch
// "the list went empty through a rename" was switched off by the same condition
// that would have emptied it. Measured 2026-09-07: in the published tree, which
// CI runs this same suite against, this file reports its EIGHT tests as zero
// and exits 0.
//
// The predicate is two-valued now. A checkout is either the source tree (site/
// is here and shares pictures with docs/) or the published one (site/ was
// withheld by the export, and `scripts/export-public.sh` went with it).
// Anything else — site/assets renamed, moved or emptied — is neither, and says
// so instead of quietly measuring nothing.
it('this checkout is coherently one tree or the other', () => {
  const exporterPresent = existsSync(path.join(REPO, 'scripts', 'export-public.sh'));
  if (!exporterPresent) {
    expect(
      sitePresent,
      'the exporter is absent, so this is the published tree — but site/assets is here. ' +
        'The two are withheld together; a tree with one and not the other is a broken checkout, ' +
        'and every assertion below would skip in silence.',
    ).toBe(false);
    return;
  }
  expect(
    sitePresent,
    'scripts/export-public.sh is here, so this is the source tree, but site/assets is missing',
  ).toBe(true);
  expect(
    copies.length,
    'site/assets exists and shares no picture with docs/screenshots. Either the sync stopped ' +
      '(node scripts/sync-site-assets.mjs) or one of the two directories was renamed — and until ' +
      'that is fixed the comparison below has nothing to compare.',
  ).toBeGreaterThan(0);
});

describe.skipIf(!sitePresent)('site assets', () => {
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
