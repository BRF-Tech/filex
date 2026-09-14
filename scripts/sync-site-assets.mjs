#!/usr/bin/env node
// Keeps `site/assets/` the copy of the current release's screenshots that
// `site/README.md` says it is.
//
// ⚠ "Current" is `docs/screenshots/<release>/`, the folder named ONCE in
// `e2e/shots/release.mjs`. Each release's pictures live in their own folder and
// the older folders are never touched, so copying from `docs/screenshots/`
// itself would keep filex.sh on the last pre-versioning set for ever — a
// comparison that passes because both sides are equally old.
//
// ⚠⚠ Nothing enforced that sentence, so the two drifted. Found 2026-09-06:
// `site/assets/admin-plugins.png` was the pre-fix capture whose footer read
// `github.com/brf-tech/filex` — the PRIVATE repo — sitting on the public
// marketing page at filex.sh, weeks after the same picture had been retaken
// for the README. Three more had fallen behind beside it.
//
// The screenshots are produced by `pnpm shots` (which also runs this); this copies
// the ones the site actually uses, and `web/tests/deploy/siteAssets.test.ts`
// fails the build if they diverge again.
//
//   node scripts/sync-site-assets.mjs          # copy
//   node scripts/sync-site-assets.mjs --check  # exit 1 if any is stale
//
// Files in `site/assets/` with no counterpart in that release folder are
// left alone: `social-preview.png` is rendered from `social-preview.src.html`
// and `end-user-drive.png` is a site-only crop. They are site-owned, not
// copies, and deleting them would break the page.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { SHOTS_ROOT, SHOTS_ROOT_REL } from '../e2e/shots/release.mjs';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SRC = SHOTS_ROOT;
const DST = path.join(REPO, 'site', 'assets');

const check = process.argv.includes('--check');

// ⚠ A release folder that was never written makes every file below look
// site-owned, and the loop then reports "matches" having compared nothing —
// the easiest way to get there is bumping release.mjs before retaking.
if (!fs.existsSync(SRC)) {
  console.error(`${SHOTS_ROOT_REL} does not exist — retake the screenshots first (pnpm shots)`);
  process.exit(1);
}

const stale = [];
let compared = 0;
for (const name of fs.readdirSync(DST).filter((f) => f.endsWith('.png'))) {
  const src = path.join(SRC, name);
  if (!fs.existsSync(src)) continue; // site-owned, see the note above
  compared++;
  const dst = path.join(DST, name);
  if (fs.readFileSync(src).equals(fs.readFileSync(dst))) continue;
  stale.push(name);
  if (!check) fs.copyFileSync(src, dst);
}

if (compared === 0) {
  console.error(`site/assets shares no picture with ${SHOTS_ROOT_REL} — nothing was compared`);
  process.exit(1);
}

if (stale.length === 0) {
  console.log(`site/assets matches ${SHOTS_ROOT_REL}`);
  process.exit(0);
}

if (check) {
  for (const n of stale) console.error(`  stale: site/assets/${n}`);
  console.error(
    `\n${stale.length} site asset(s) differ from ${SHOTS_ROOT_REL} — filex.sh would show ` +
      'an older picture than the README. Run: node scripts/sync-site-assets.mjs',
  );
  process.exit(1);
}

for (const n of stale) console.log(`  copied ${n}`);
console.log(`${stale.length} site asset(s) refreshed from ${SHOTS_ROOT_REL}`);
