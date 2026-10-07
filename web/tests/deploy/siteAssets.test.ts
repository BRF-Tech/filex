// `site/assets/` holds the filex.sh page's own files and the store badges -
// and no copy of a screenshot.
//
// ⚠⚠ It used to hold a copy of the current release's screenshots, synced from
// `docs/screenshots/<release>/`. Found 2026-09-06: `site/assets/admin-plugins.png`
// was the pre-fix capture whose footer read the PRIVATE repository's address,
// on the public marketing page at filex.sh, after the same picture had been
// retaken for the README. Since task #176 (2026-10-06) the screenshots are
// published once, on filex.sh itself, under names that carry their content
// hash, and the page links them there like the README does
// (e2e/shots/manifest.json; web/tests/deploy/shotsSite.test.ts checks every
// link). A copy coming back is the drift coming back, so it fails here.

import { createHash } from 'node:crypto';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { MANIFEST_REL, readManifest } from '../../../scripts/lib/shots-site.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const DST = path.join(REPO, 'site', 'assets');
const manifest = readManifest(path.join(REPO, MANIFEST_REL));

// ⚠ `site/` is withheld from the public export (scripts/export-public.sh), so
// in the published tree this directory does not exist at all and there is
// nothing to compare. Skipping there is correct; skipping in the source repo
// would make the gate a decoration, so the two cases are told apart by the
// directory's presence and the skip is announced.
const sitePresent = existsSync(DST);

/**
 * The page's own pictures: `social-preview.png` is rendered from
 * `social-preview.src.html` (the link-preview card), `end-user-drive.png` is a
 * crop GitHub issue #14 embeds from its public URL. Neither is a screenshot a
 * shot script takes.
 */
const SITE_OWNED = ['social-preview.png', 'end-user-drive.png'];

// ⚠⚠ UNCONDITIONAL, and that is the whole reason it is out here.
//
// It used to sit inside the block below, where `describe.skipIf` skipped it
// along with everything it was guarding — so the one assertion written to catch
// "the list went empty through a rename" was switched off by the same condition
// that would have emptied it. Measured 2026-09-07: in the published tree, which
// CI runs this same suite against, this file reported its EIGHT tests as zero
// and exited 0.
//
// The predicate is two-valued. A checkout is either the source tree (site/
// is here) or the published one (site/ was withheld by the export, and
// `scripts/export-public.sh` went with it). Anything else says so instead of
// quietly measuring nothing.
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
  expect(sitePresent, 'scripts/export-public.sh is here, so this is the source tree, but site/assets is missing').toBe(true);
  expect(Object.keys(manifest.pictures).length, `${MANIFEST_REL} holds no picture — the comparison below compares nothing`).toBeGreaterThan(50);
});

describe.skipIf(!sitePresent)('site assets', () => {
  const pngs = sitePresent ? readdirSync(DST).filter((f) => f.endsWith('.png')) : [];
  const published = new Set(Object.values(manifest.pictures).map((p: { sha256: string }) => p.sha256));
  const rootNames = new Set(Object.keys(manifest.pictures).filter((n) => !n.includes('/')));

  it('the page still has its own pictures', () => {
    for (const name of SITE_OWNED) expect(pngs, `site/assets/${name} is gone`).toContain(name);
  });

  it.each(pngs)('site/assets/%s is not a copy of a published screenshot', (name) => {
    const sha = createHash('sha256').update(readFileSync(path.join(DST, name))).digest('hex');
    expect(
      published.has(sha) || rootNames.has(name),
      `site/assets/${name} is a screenshot (it is in ${MANIFEST_REL}). The page links the published file on ` +
        'filex.sh instead: node scripts/shots-site.mjs relink --write. A copy here is the copy that drifted.',
    ).toBe(false);
    expect(SITE_OWNED, `site/assets/${name} is neither a site-owned picture nor allowed here`).toContain(name);
  });
});

// The store badges: `docs/badges/` is the one copy (the README and DESKTOP.md
// show it, and it is public); `site/assets/badges/` mirrors it because filex.sh
// is deployed from `site/` alone. See scripts/sync-site-assets.mjs.
// ⚠ Read inside guards, not at describe scope: `describe.skipIf` still runs
// its callback, and a throw there would take the whole file down to "no tests".
const BADGES_SRC = path.join(REPO, 'docs', 'badges');
const BADGES_DST = path.join(DST, 'badges');
const badgeNames = existsSync(BADGES_SRC) ? readdirSync(BADGES_SRC).filter((f) => f.endsWith('.svg')) : [];
const siteBadges =
  sitePresent && existsSync(BADGES_DST) ? readdirSync(BADGES_DST).filter((f) => f.endsWith('.svg')) : [];

it('the store badges have a source to mirror', () => {
  expect(badgeNames.length, 'docs/badges holds no .svg — the comparison below compares nothing').toBeGreaterThan(0);
});

describe.skipIf(!sitePresent)('site store badges', () => {
  it.each(badgeNames)('site/assets/badges/%s is byte-identical to docs/badges', (name) => {
    const dst = path.join(BADGES_DST, name);
    expect(existsSync(dst), `site/assets/badges/${name} is missing. Run: node scripts/sync-site-assets.mjs`).toBe(true);
    expect(
      readFileSync(dst).equals(readFileSync(path.join(BADGES_SRC, name))),
      `site/assets/badges/${name} differs from docs/badges. Run: node scripts/sync-site-assets.mjs`,
    ).toBe(true);
  });

  it('the site carries no badge docs/badges does not have', () => {
    expect(siteBadges.filter((f) => !badgeNames.includes(f))).toEqual([]);
  });
});
