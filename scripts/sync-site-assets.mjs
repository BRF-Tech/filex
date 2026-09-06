#!/usr/bin/env node
// Keeps `site/assets/` the copy of `docs/screenshots/` that `site/README.md`
// says it is.
//
// ⚠⚠ Nothing enforced that sentence, so the two drifted. Found 2026-09-06:
// `site/assets/admin-plugins.png` was the pre-fix capture whose footer read
// `github.com/brf-tech/filex` — the PRIVATE repo — sitting on the public
// marketing page at filex.sh, weeks after the same picture had been retaken
// for the README. Three more had fallen behind beside it.
//
// The screenshots are produced by `node e2e/shots/capture.mjs`; this copies
// the ones the site actually uses, and `web/tests/deploy/siteAssets.test.ts`
// fails the build if they diverge again.
//
//   node scripts/sync-site-assets.mjs          # copy
//   node scripts/sync-site-assets.mjs --check  # exit 1 if any is stale
//
// Files in `site/assets/` with no counterpart under `docs/screenshots/` are
// left alone: `social-preview.png` is rendered from `social-preview.src.html`
// and `end-user-drive.png` is a site-only crop. They are site-owned, not
// copies, and deleting them would break the page.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SRC = path.join(REPO, 'docs', 'screenshots');
const DST = path.join(REPO, 'site', 'assets');

const check = process.argv.includes('--check');

const stale = [];
for (const name of fs.readdirSync(DST).filter((f) => f.endsWith('.png'))) {
  const src = path.join(SRC, name);
  if (!fs.existsSync(src)) continue; // site-owned, see the note above
  const dst = path.join(DST, name);
  if (fs.readFileSync(src).equals(fs.readFileSync(dst))) continue;
  stale.push(name);
  if (!check) fs.copyFileSync(src, dst);
}

if (stale.length === 0) {
  console.log('site/assets matches docs/screenshots');
  process.exit(0);
}

if (check) {
  for (const n of stale) console.error(`  stale: site/assets/${n}`);
  console.error(
    `\n${stale.length} site asset(s) differ from docs/screenshots — filex.sh would show ` +
      'an older picture than the README. Run: node scripts/sync-site-assets.mjs',
  );
  process.exit(1);
}

for (const n of stale) console.log(`  copied ${n}`);
console.log(`${stale.length} site asset(s) refreshed from docs/screenshots`);
