// The release the screenshots in this directory are taken FOR — the one value
// that decides where every shot script writes by default, where
// `scripts/sync-site-assets.mjs` copies filex.sh's pictures from, and which
// paths `scripts/shop-window-data.mjs` declares.
//
// ⚠⚠ Each release's pictures go in their OWN folder, `docs/screenshots/vX.Y.Z/`,
// and the older folders are never retaken, moved or deleted (owner's rule for
// v0.41.0: "eski görüntüler eski klasöründe kalsın, yeni bir klasörde yeni
// versiyon görüntülerini alırsın koyarsın"). A page that deliberately shows an
// old interface — a migration note, an old release's changelog — keeps linking
// the old folder and stays true; the README and the docs move to the new one.
//
// ⚠ A constant, not read from a package.json, on purpose. The root
// package.json is the monorepo's `0.1.0`, which names no release, and the
// packages' versions are bumped at release step 5 — AFTER the screenshots are
// retaken at step 2 — so reading them would file the new pictures under the
// release that already shipped. Bump this line at step 2; nothing else in the
// shot scripts names a folder.
//
// Bumping it without repointing README.md is caught, not silent:
// `web/tests/deploy/shopWindow.test.ts` fails when a README picture is not one
// `SCREENSHOTS` declares, and `scripts/check-links.mjs` fails on a link to a
// folder that was never written.

import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const SHOTS_RELEASE = 'v0.41.0';

const REPO = resolve(dirname(fileURLToPath(import.meta.url)), '../..');

/** Repo-relative, forward slashes — the form README links and git use. */
export const SHOTS_ROOT_REL = `docs/screenshots/${SHOTS_RELEASE}`;

/** Absolute path of this release's screenshot folder. */
export const SHOTS_ROOT = join(REPO, 'docs', 'screenshots', SHOTS_RELEASE);

/** This release's folder for one shot script's set (`driveshell`, `sidenav`, …). */
export const shotsDir = (sub = '') => (sub ? join(SHOTS_ROOT, sub) : SHOTS_ROOT);
