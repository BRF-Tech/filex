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

export const SHOTS_RELEASE = 'v0.46.0';

const REPO = resolve(dirname(fileURLToPath(import.meta.url)), '../..');

/** Repo-relative, forward slashes — the form README links and git use. */
export const SHOTS_ROOT_REL = `docs/screenshots/${SHOTS_RELEASE}`;

/** Absolute path of this release's screenshot folder. */
export const SHOTS_ROOT = join(REPO, 'docs', 'screenshots', SHOTS_RELEASE);

/** This release's folder for one shot script's set (`driveshell`, `sidenav`, …). */
export const shotsDir = (sub = '') => (sub ? join(SHOTS_ROOT, sub) : SHOTS_ROOT);

/**
 * The linker flags every filex a shot script photographs is built with.
 *
 * ⚠⚠ The sign-in page prints `caps.data.version`, and an unstamped build says
 * `0.1.0-dev` (backend/internal/version/version.go). That line is IN the
 * README from v0.43.0 on — `appearance/themed-signin-1440.png` — so the shop
 * window would state a version of filex that does not exist. Stamping it the
 * way goreleaser does (.goreleaser.yml, same symbol path: `-X main.version`
 * silently no-ops) makes the picture true.
 *
 * ⚠ It also makes the pictures QUIETER rather than noisier: the update check
 * is on by default, `0.1.0-dev` parses and sits below every published release,
 * and the release being cut does not. Scenes that must not reach the network
 * at all still set `FILEX_UPDATE_CHECK=0` themselves.
 */
export const SHOTS_LDFLAGS =
  `-s -w -X github.com/brf-tech/filex/backend/internal/version.Version=${SHOTS_RELEASE.replace(/^v/, '')}`;
