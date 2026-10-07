#!/usr/bin/env node
// Keeps `site/assets/badges/` the copy of `docs/badges/` that `site/README.md`
// says it is.
//
//   node scripts/sync-site-assets.mjs          # copy
//   node scripts/sync-site-assets.mjs --check  # exit 1 if any is stale
//
// ⚠ It used to copy the screenshots too: `site/assets/` held a copy of the
// current release's `docs/screenshots/vX.Y.Z/` pictures, because nothing else
// put them on filex.sh. Found 2026-09-06: `site/assets/admin-plugins.png` was
// the pre-fix capture whose footer read the PRIVATE repository's address,
// sitting on the public page weeks after the README's had been retaken. Since
// task #176 (2026-10-06) the screenshots are published once, on filex.sh
// itself, under names that carry their content hash, and `site/index.html`
// links them there like the README does (`node scripts/shots-site.mjs relink`
// keeps every page on the current file; e2e/shots/README.md). There is no copy
// left to drift, so there is nothing here to sync - and
// web/tests/deploy/siteAssets.test.ts fails if one comes back.
//
// ⚠ The store badges are what is still copied. `docs/badges/` holds the
// Microsoft Store and Snap Store artwork, exactly as the stores publish it,
// and is the one copy the README and docs/DESKTOP.md show; `site/` is
// withheld from the public export, so the README cannot point into it, and
// filex.sh is deployed from `site/` alone, so the page cannot point out of
// it. `site/assets/badges/` is therefore a mirror of `docs/badges/`, and a
// badge the site has that `docs/badges/` does not is an error, not a
// site-owned file: artwork we are not allowed to alter has one source.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const BADGES_SRC = path.join(REPO, 'docs', 'badges');
const BADGES_DST = path.join(REPO, 'site', 'assets', 'badges');

const check = process.argv.includes('--check');

if (!fs.existsSync(BADGES_SRC)) {
  console.error('docs/badges does not exist — the store badges have no source');
  process.exit(1);
}
const names = fs.readdirSync(BADGES_SRC).filter((f) => f.endsWith('.svg'));
if (names.length === 0) {
  console.error('docs/badges holds no .svg — nothing was compared');
  process.exit(1);
}
if (!check) fs.mkdirSync(BADGES_DST, { recursive: true });

const stale = [];
for (const name of names) {
  const src = path.join(BADGES_SRC, name);
  const dst = path.join(BADGES_DST, name);
  if (fs.existsSync(dst) && fs.readFileSync(src).equals(fs.readFileSync(dst))) continue;
  stale.push(`badges/${name}`);
  if (!check) fs.copyFileSync(src, dst);
}

const orphans = fs.existsSync(BADGES_DST) ? fs.readdirSync(BADGES_DST).filter((f) => f.endsWith('.svg') && !names.includes(f)) : [];
if (orphans.length > 0) {
  for (const o of orphans) console.error(`  not in docs/badges: site/assets/badges/${o}`);
  console.error('a store badge belongs in docs/badges first; the site copies it from there');
  process.exit(1);
}

if (stale.length === 0) {
  console.log('site/assets/badges matches docs/badges');
  process.exit(0);
}

if (check) {
  for (const n of stale) console.error(`  stale: site/assets/${n}`);
  console.error(`\n${stale.length} badge(s) differ from docs/badges. Run: node scripts/sync-site-assets.mjs`);
  process.exit(1);
}

for (const n of stale) console.log(`  copied ${n}`);
console.log(`${stale.length} badge(s) refreshed from docs/badges`);
